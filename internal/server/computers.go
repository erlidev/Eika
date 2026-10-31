package server

import (
	"context"
	"net/http"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/store"
)

// teardownTimeout bounds the work that has to finish even though the request
// that asked for it is gone: stopping the runs in a computer that is about to
// stop, and stopping the run of a session that is being deleted. Both leave a
// goroutine behind if they are cut short.
const teardownTimeout = 2 * time.Minute

// computerBody is one computer on the wire.
type computerBody struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	State       string `json:"state"`
	ContainerID string `json:"container_id,omitempty"`
	// Sandbox is what the container may consume and reach.
	Sandbox sandboxBody `json:"sandbox"`
	// Home marks the computer that holds the user's durable files, which the
	// harness creates itself.
	Home      bool      `json:"home"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// stopComputer stops the runs in a computer and then its container.
func (s *Server) stopComputer(ctx context.Context, cx store.Computer, host *computer.Computer) error {
	s.stopRunsIn(ctx, cx)
	return s.deps.Computers.Stop(ctx, host)
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

// stopRunsIn aborts every run in a computer, then ends the stdio MCP servers
// running there, before the container goes away under them. It detaches from
// the request that asked for it: a run left going would keep writing through
// an executor that no longer has a container behind it.
func (s *Server) stopRunsIn(ctx context.Context, cx store.Computer) {
	ctx, cancel := teardown(ctx)
	defer cancel()
	s.runs.stopSessionsOf(ctx, s.deps.Store, cx.ID)
	if s.deps.MCP != nil {
		s.deps.MCP.StopComputer(cx.ID)
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
		Home:        cx.Home,
		CreatedAt:   cx.CreatedAt,
		UpdatedAt:   cx.UpdatedAt,
	}
}
