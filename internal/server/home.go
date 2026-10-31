package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/store"
)

// homeName is the name the home computer is created with.
const homeName = "home"

// Bounds on how soon EnsureHome tries again after it failed, which it does
// until it succeeds: the first retry comes soon, since the usual cause is a
// sandbox image still being built, and later ones settle at the maximum.
const (
	firstHomeRetry   = 5 * time.Second
	longestHomeRetry = time.Minute
)

// EnsureHome makes the home computer exist and run: the one computer that
// holds the user's durable files. It finds the home row, or records it, and
// then creates the container when there is none and starts it when it is
// stopped. Calling it again changes nothing, so the harness calls it on
// every start and a run calls it when the container has gone missing.
//
// The row comes first and the database allows one home, so two calls at once
// and two starts of the harness agree on one id, and with it one container
// and one volume. A volume that outlived its container is reattached: the
// files in it are the point of the computer. A failure leaves the row as it
// was and the container, if it got that far, in place for the next call.
func (s *Server) EnsureHome(ctx context.Context) error {
	s.homeMu.Lock()
	defer s.homeMu.Unlock()

	sandbox, err := s.newSandbox(ctx, nil)
	if err != nil {
		return fmt.Errorf("choose the home computer's sandbox: %w", err)
	}
	row, created, err := s.deps.Store.EnsureHomeComputer(ctx, store.Computer{
		ID:      store.NewID(),
		Name:    homeName,
		Image:   s.sandboxImage(ctx),
		State:   string(computer.StateCreating),
		Sandbox: sandbox,
	})
	if err != nil {
		return err
	}
	if created {
		s.log.Info("home computer recorded", "computer_id", row.ID)
		s.announceHome(ctx, row.ID, row.State)
	}

	host, err := s.deps.Computers.Inspect(ctx, row.ID)
	switch {
	case err == nil:
	case errors.Is(err, computer.ErrNoComputer):
		if host, err = s.deps.Computers.Create(ctx, computer.Spec{
			ID:          row.ID,
			Image:       row.Image,
			Confinement: confinement(row.Sandbox),
		}); err != nil {
			return fmt.Errorf("create the home computer: %w", err)
		}
		s.log.Info("home computer created", "computer_id", row.ID)
	default:
		return err
	}

	if host.State != computer.StateRunning {
		startErr := s.startComputer(ctx, row, &host)
		// The container exists even if it would not start, and the row says
		// so; the next call starts it.
		if startErr != nil {
			host.State = computer.StateStopped
		}
		if err := s.record(ctx, row.ID, host); err != nil {
			return err
		}
		if startErr != nil {
			return fmt.Errorf("start the home computer: %w", startErr)
		}
		s.idle.used(row.ID, time.Now())
		return nil
	}
	return s.record(ctx, row.ID, host)
}

// record writes the state a computer's container is in and announces it. The
// home computer's is announced on the global topic too: a client learns that
// it exists, and that it is ready, without asking again.
func (s *Server) record(ctx context.Context, id string, host computer.Computer) error {
	if err := s.deps.Store.SetComputerState(ctx, id, string(host.State), host.ContainerID); err != nil {
		return err
	}
	s.computerState(ctx, id, string(host.State))
	s.announceHome(ctx, id, string(host.State))
	return nil
}

// announceHome publishes the home computer's state on the global topic.
func (s *Server) announceHome(ctx context.Context, id, state string) {
	e, err := event.New(event.TypeComputerState, event.TopicGlobal, event.ComputerState{ComputerID: id, State: state})
	if err != nil {
		s.log.Error("encode home computer state event", "computer_id", id, "error", err)
		return
	}
	s.deps.Bus.Emit(ctx, e)
}

// keepHome ensures the home computer, and tries again after delay, then after
// longer ones up to longestHomeRetry, until that works or ctx is cancelled. It
// runs beside the server, never in front of it: a Docker daemon that is not
// there yet, or a sandbox image that is still being built, must not keep the
// API from serving. What the user sees meanwhile is the home computer listed
// as creating, and in Settings the system check naming what is missing.
func (s *Server) keepHome(ctx context.Context, delay time.Duration) {
	for {
		err := s.EnsureHome(ctx)
		if err == nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		s.log.Error("ensure home computer", "error", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, longestHomeRetry)
	}
}

// startComputer starts a stopped computer's container, held to the sandbox
// its row records: a change that reached the container only in part before it
// stopped is completed here.
func (s *Server) startComputer(ctx context.Context, cx store.Computer, host *computer.Computer) error {
	if err := s.deps.Computers.Confine(ctx, host, confinement(cx.Sandbox)); err != nil {
		return err
	}
	return s.deps.Computers.Start(ctx, host)
}

// runExecutor returns the executor a run in a computer acts through. The home
// computer is the one a run may find stopped or gone and still use: the idle
// stopper stops it like any other, and nothing in the UI starts a computer by
// hand, so a run starts it, and makes its container again over the volume when
// that is missing. Any other computer that is not running refuses the run.
func (s *Server) runExecutor(ctx context.Context, id string) (executor.Executor, error) {
	cx, err := s.deps.Store.Computer(ctx, id)
	if err != nil {
		return nil, err
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	host, err := s.deps.Computers.Inspect(ctx, cx.ID)
	if errors.Is(err, computer.ErrNoComputer) && cx.Home {
		if err := s.EnsureHome(ctx); err != nil {
			return nil, err
		}
		host, err = s.deps.Computers.Inspect(ctx, cx.ID)
	}
	if err != nil {
		return nil, err
	}
	if host.State == computer.StateStopped && cx.Home {
		if cx, err = s.changeState(ctx, cx, func(ctx context.Context, cx store.Computer, host *computer.Computer) error {
			return s.startComputer(ctx, cx, host)
		}); err != nil {
			return nil, err
		}
		s.idle.used(cx.ID, time.Now())
		if host, err = s.deps.Computers.Inspect(ctx, cx.ID); err != nil {
			return nil, err
		}
	}
	if host.State != computer.StateRunning {
		return nil, notRunning(cx, host.State)
	}
	return s.deps.Computers.Executor(host)
}
