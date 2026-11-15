package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// runSubagent is the lifecycle service's run callback: one message in the
// child's own session, through the run manager every session uses, at the
// priority delegation chose. It returns when the run has ended and recorded
// how; cancelling ctx aborts the run and waits for it.
func (s *Server) runSubagent(ctx context.Context, child store.Session, text string, priority inference.Priority) error {
	_, active, err := s.runs.launch(ctx, child.ID, "", job{message: provider.UserMessage(text), priority: priority})
	if err != nil {
		return err
	}
	select {
	case <-active.done:
	case <-ctx.Done():
		active.abort()
		<-active.done
	}
	// A run the user aborted from its session view is cancelled, not failed.
	if active.wasAborted() {
		return fmt.Errorf("subagent run aborted: %w", context.Canceled)
	}
	return active.outcome()
}

// delegationPriority is the priority of the runs a home session starts or
// resumes: foreground when the home run that asks answers the user, and
// background otherwise. Only a home session delegates, so a subagent cannot
// start subagents of its own.
func (s *Server) delegationPriority(ctx context.Context, home string) (inference.Priority, error) {
	if s.subagents == nil {
		return 0, fmt.Errorf("subagents are unavailable")
	}
	sess, err := s.deps.Store.Session(ctx, home)
	if err != nil {
		return 0, err
	}
	if sess.Kind != store.SessionHome {
		return 0, fmt.Errorf("only Eika's home session can delegate")
	}
	if active := s.runs.active(home); active != nil && active.priority() == inference.Interactive {
		return inference.Foreground, nil
	}
	return inference.Background, nil
}

// delegation binds the delegation tools to the server. It is a getter
// because the tool registry is built before the server is.
type delegation struct{ server func() *Server }

// StartSubagent starts a named child of home. A worker works on the home
// computer; a researcher has none.
func (d delegation) StartSubagent(ctx context.Context, home string, child store.Session) (store.Session, bool, error) {
	s := d.server()
	priority, err := s.delegationPriority(ctx, home)
	if err != nil {
		return store.Session{}, false, err
	}
	child.ComputerID = ""
	if child.Agent == "worker" {
		cx, err := s.deps.Store.HomeComputer(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return store.Session{}, false, fmt.Errorf("the home computer is not ready yet")
		}
		if err != nil {
			return store.Session{}, false, err
		}
		child.ComputerID = cx.ID
	}
	return s.subagents.Start(ctx, home, child, priority)
}

// MessageSubagent resumes a child of home with text.
func (d delegation) MessageSubagent(ctx context.Context, home, name, text string) (store.Session, bool, error) {
	s := d.server()
	priority, err := s.delegationPriority(ctx, home)
	if err != nil {
		return store.Session{}, false, err
	}
	return s.subagents.Message(ctx, home, name, text, priority)
}

// Subagents returns one child of home, or all of them.
func (d delegation) Subagents(ctx context.Context, home, name string) ([]store.Session, error) {
	s := d.server()
	if _, err := s.delegationPriority(ctx, home); err != nil {
		return nil, err
	}
	return s.subagents.Status(ctx, home, name)
}

// CancelSubagent stops a child's run and waits for it to end.
func (d delegation) CancelSubagent(ctx context.Context, home, name string) (store.Session, error) {
	s := d.server()
	if _, err := s.delegationPriority(ctx, home); err != nil {
		return store.Session{}, err
	}
	return s.subagents.Cancel(ctx, home, name)
}

// refuseSubagent refuses a message or compaction posted to a subagent's
// session: Eika messages its subagents, and the lifecycle service owns their
// runs and states.
func (s *Server) refuseSubagent(ctx context.Context, id string) error {
	sess, err := s.deps.Store.Session(ctx, id)
	if err != nil {
		return err
	}
	if sess.Kind == store.SessionSubagent {
		return conflictf("subagent %s takes messages from Eika only", sess.Name)
	}
	return nil
}

// projectWorkdir makes a project's directory exist on the home computer and
// returns ex working in it.
func (s *Server) projectWorkdir(ctx context.Context, projectID string, ex executor.Executor) (executor.Executor, error) {
	p, err := s.deps.Store.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if _, err := executor.Resolve(ex.Root(), p.Directory); err != nil {
		return nil, conflictf("project %s's directory %s is not on the home computer", p.Slug, p.Directory)
	}
	var out bytes.Buffer
	res, err := ex.Exec(ctx, executor.ExecSpec{Command: "mkdir", Args: []string{"-p", "--", p.Directory},
		Stdout: &out, Stderr: &out, Timeout: 30 * time.Second})
	if err == nil && res.ExitCode != 0 {
		err = errors.New(strings.TrimSpace(out.String()))
	}
	if err != nil {
		return nil, fmt.Errorf("create project %s's directory %s: %w", p.Slug, p.Directory, err)
	}
	return workdir{Executor: ex, dir: p.Directory}, nil
}

// workdir runs commands in a project's directory and resolves relative paths
// there. Absolute paths are unchanged; the executor still confines every
// path to the computer root.
type workdir struct {
	executor.Executor
	dir string
}

func (w workdir) at(p string) string {
	if path.IsAbs(p) {
		return p
	}
	return path.Join(w.dir, p)
}

func (w workdir) Exec(ctx context.Context, spec executor.ExecSpec) (executor.ExecResult, error) {
	spec.Dir = w.at(spec.Dir)
	return w.Executor.Exec(ctx, spec)
}

func (w workdir) ReadFile(ctx context.Context, p string, opts executor.ReadOpts) ([]byte, error) {
	return w.Executor.ReadFile(ctx, w.at(p), opts)
}

func (w workdir) WriteFile(ctx context.Context, p string, data []byte) error {
	return w.Executor.WriteFile(ctx, w.at(p), data)
}

func (w workdir) Stat(ctx context.Context, p string) (executor.FileInfo, error) {
	return w.Executor.Stat(ctx, w.at(p))
}

func (w workdir) List(ctx context.Context, p string) ([]executor.FileInfo, error) {
	return w.Executor.List(ctx, w.at(p))
}
