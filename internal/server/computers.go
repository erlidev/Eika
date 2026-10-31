package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/store"
)

// teardownTimeout bounds the work that has to finish even though the request
// that asked for it is gone: removing a half-created computer, and stopping
// the runs in a computer that is about to go away. Both leave a container,
// a volume, or a goroutine behind if they are cut short.
const teardownTimeout = 2 * time.Minute

// computerBody is one computer on the wire.
type computerBody struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	State       string `json:"state"`
	ContainerID string `json:"container_id,omitempty"`
	// Sandbox is what the container may consume, reach, and expose.
	Sandbox sandboxBody `json:"sandbox"`
	// Pinned computers are listed before the others.
	Pinned bool `json:"pinned"`
	// Archived computers are set aside under a heading of their own; the
	// container is left as it is.
	Archived bool `json:"archived"`
	// Home marks the computer that holds the user's durable files, which the
	// harness creates itself and which cannot be deleted.
	Home      bool      `json:"home"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// createComputerRequest is the body of POST /api/computers. Image and
// BuildContext are alternatives: a build context is a directory on the Docker
// host holding Dockerfile and whatever it needs.
type createComputerRequest struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	BuildContext string `json:"build_context"`
	Dockerfile   string `json:"dockerfile"`
	// Sandbox is the new computer's limits and network. Left out, it gets
	// the settings' defaults.
	Sandbox *sandboxBody `json:"sandbox"`
}

// updateComputerRequest is the body of PATCH /api/computers/{id}. An
// absent field is left alone. The container does not change.
type updateComputerRequest struct {
	Name     *string `json:"name"`
	Pinned   *bool   `json:"pinned"`
	Archived *bool   `json:"archived"`
}

// computersResponse is the body of GET /api/computers.
type computersResponse struct {
	Computers []computerBody `json:"computers"`
}

// handleListComputers lists the computers.
func (s *Server) handleListComputers(w http.ResponseWriter, r *http.Request) {
	computers, err := s.deps.Store.Computers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]computerBody, 0, len(computers))
	for _, cx := range computers {
		out = append(out, asComputer(cx))
	}
	writeJSON(w, s.log, http.StatusOK, computersResponse{Computers: out})
}

// handleCreateComputer creates the container, starts it, and records the
// result.
func (s *Server) handleCreateComputer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[createComputerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cx, err := s.createComputer(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.computerState(r.Context(), cx.ID, cx.State)
	s.log.Info("computer created", "computer_id", cx.ID)
	writeJSON(w, s.log, http.StatusCreated, asComputer(cx))
}

// createComputer runs the whole creation sequence. Everything the host
// created is destroyed again when a later step fails, so a failed request
// leaves no container behind and writes no row.
func (s *Server) createComputer(ctx context.Context, req createComputerRequest) (store.Computer, error) {
	name, err := checkName("name", req.Name)
	if err != nil {
		return store.Computer{}, err
	}
	sandbox, err := s.newSandbox(ctx, req.Sandbox)
	if err != nil {
		return store.Computer{}, err
	}

	image := strings.TrimSpace(req.Image)
	if image == "" && req.BuildContext == "" {
		image = s.sandboxImage(ctx)
	}
	spec := computer.Spec{
		ID:           store.NewID(),
		Image:        image,
		BuildContext: req.BuildContext,
		Dockerfile:   req.Dockerfile,
		Confinement:  confinement(sandbox),
	}
	host, err := s.deps.Computers.Create(ctx, spec)
	if err != nil {
		return store.Computer{}, err
	}
	if err := s.deps.Computers.Start(ctx, &host); err != nil {
		s.discard(ctx, host)
		return store.Computer{}, err
	}

	cx, err := s.deps.Store.CreateComputer(ctx, store.Computer{
		ID:          host.ID,
		Name:        name,
		Image:       host.Image,
		State:       string(host.State),
		ContainerID: host.ContainerID,
		Sandbox:     sandbox,
	})
	if err != nil {
		s.discard(ctx, host)
		return store.Computer{}, err
	}
	return cx, nil
}

// handleComputer returns one computer.
func (s *Server) handleComputer(w http.ResponseWriter, r *http.Request) {
	cx, err := s.deps.Store.Computer(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asComputer(cx))
}

// handleUpdateComputer renames, pins, or archives a computer. The state
// event it publishes, with the state unchanged, is how the sidebars that
// show the computer learn of it.
func (s *Server) handleUpdateComputer(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[updateComputerRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	changes := store.ComputerChanges{Pinned: req.Pinned, Archived: req.Archived}
	if req.Name != nil {
		name, err := checkName("name", *req.Name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		changes.Name = &name
	}
	cx, err := s.deps.Store.UpdateComputer(r.Context(), r.PathValue("id"), changes)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.computerState(r.Context(), cx.ID, cx.State)
	s.log.Info("computer updated", "computer_id", cx.ID)
	writeJSON(w, s.log, http.StatusOK, asComputer(cx))
}

// handleStartComputer starts a stopped computer's container again, held
// to the sandbox its row records: a change that reached the container only
// in part before it stopped is completed here.
func (s *Server) handleStartComputer(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, func(ctx context.Context, cx store.Computer, host *computer.Computer) error {
		if err := s.deps.Computers.Confine(ctx, host, confinement(cx.Sandbox)); err != nil {
			return err
		}
		if err := s.deps.Computers.Start(ctx, host); err != nil {
			return err
		}
		// A computer started by hand gets the whole idle timeout, however
		// long it sat unused before it stopped.
		s.idle.used(cx.ID, time.Now())
		return nil
	})
}

// handleStopComputer stops a computer's container, keeping its files.
func (s *Server) handleStopComputer(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, s.stopComputer)
}

// stopComputer stops the runs in a computer and then its container.
func (s *Server) stopComputer(ctx context.Context, cx store.Computer, host *computer.Computer) error {
	s.stopRunsIn(ctx, cx)
	return s.deps.Computers.Stop(ctx, host)
}

// transition answers a request for one lifecycle operation on a computer.
func (s *Server) transition(w http.ResponseWriter, r *http.Request, op func(context.Context, store.Computer, *computer.Computer) error) {
	cx, err := s.deps.Store.Computer(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cx, err = s.changeState(r.Context(), cx, op); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asComputer(cx))
}

// changeState runs one lifecycle operation on a computer and records the
// state it reached.
func (s *Server) changeState(ctx context.Context, cx store.Computer, op func(context.Context, store.Computer, *computer.Computer) error) (store.Computer, error) {
	host, err := s.deps.Computers.Inspect(ctx, cx.ID)
	if err != nil {
		return store.Computer{}, err
	}
	if err := op(ctx, cx, &host); err != nil {
		return store.Computer{}, err
	}
	if err := s.deps.Store.SetComputerState(ctx, cx.ID, string(host.State), host.ContainerID); err != nil {
		return store.Computer{}, err
	}
	s.computerState(ctx, cx.ID, string(host.State))
	cx.State = string(host.State)
	return cx, nil
}

// notRunning is the conflict a computer whose container is not running
// answers with.
func notRunning(cx store.Computer, state computer.State) error {
	return conflictf("computer %s is %s, not running", cx.ID, state)
}

// handleDeleteComputer destroys a computer's container and volume and
// removes its rows, its sessions with them.
func (s *Server) handleDeleteComputer(w http.ResponseWriter, r *http.Request) {
	cx, err := s.deps.Store.Computer(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if cx.Home {
		s.fail(w, r, conflictf("computer %s is the home computer, which holds your files and cannot be deleted", cx.ID))
		return
	}
	if err := s.destroyComputer(r.Context(), cx); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("computer deleted", "computer_id", cx.ID)
	w.WriteHeader(http.StatusNoContent)
}

// destroyComputer stops the runs in a computer, removes its container, and
// deletes its rows. A container that is already gone is not an error: the row is
// what the user asked to be rid of.
func (s *Server) destroyComputer(ctx context.Context, cx store.Computer) error {
	s.stopRunsIn(ctx, cx)
	host, err := s.deps.Computers.Inspect(ctx, cx.ID)
	switch {
	case err == nil:
		if err := s.deps.Computers.Destroy(ctx, &host); err != nil {
			return err
		}
	case errors.Is(err, computer.ErrNoComputer):
		s.log.Warn("computer container is already gone", "computer_id", cx.ID, "error", err)
	default:
		return err
	}
	if err := s.deps.Store.DeleteComputer(ctx, cx.ID); err != nil {
		return err
	}
	s.forgetEgress(cx.ID)
	s.computerState(ctx, cx.ID, string(computer.StateGone))
	return nil
}

// stopRunsIn aborts every run in a computer, then ends the stdio MCP servers running there, before the container goes
// away under them. It detaches from the request for the same reason discard
// does: a run left going would keep writing through an executor that no
// longer has a container behind it.
func (s *Server) stopRunsIn(ctx context.Context, cx store.Computer) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	s.runs.stopSessionsOf(ctx, s.deps.Store, cx.ID)
	if s.deps.MCP != nil {
		s.deps.MCP.StopComputer(cx.ID)
	}
}

// discard removes a computer the harness created but could not finish. It
// runs on a context of its own: a client that gave up on the request is the
// most likely reason the creation failed, and a cancelled cleanup would leave
// the container and its volume behind for good.
func (s *Server) discard(ctx context.Context, host computer.Computer) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	if err := s.deps.Computers.Destroy(ctx, &host); err != nil {
		s.log.Error("discard half-created computer", "computer_id", host.ID, "error", err)
	}
}

// teardown detaches a context from the request that carried it, keeping a
// bound of its own so that nothing runs forever.
func teardown(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), teardownTimeout)
}

// asComputer renders a stored computer on the wire.
func asComputer(cx store.Computer) computerBody {
	return computerBody{
		ID:          cx.ID,
		Name:        cx.Name,
		Image:       cx.Image,
		State:       cx.State,
		ContainerID: cx.ContainerID,
		Sandbox:     asSandbox(cx.Sandbox),
		Pinned:      cx.Pinned,
		Archived:    cx.Archived,
		Home:        cx.Home,
		CreatedAt:   cx.CreatedAt,
		UpdatedAt:   cx.UpdatedAt,
	}
}
