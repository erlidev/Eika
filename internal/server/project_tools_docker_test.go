//go:build docker

package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/store"
)

type projectReadHost struct {
	*fakeHost
	started chan struct{}
	release chan struct{}
}

func (h *projectReadHost) Executor(cx computer.Computer) (executor.Executor, error) {
	ex, err := h.fakeHost.Executor(cx)
	if err != nil {
		return nil, err
	}
	return &projectReadExecutor{Executor: ex, started: h.started, release: h.release}, nil
}

type projectReadExecutor struct {
	executor.Executor
	started chan struct{}
	release chan struct{}
}

func (e *projectReadExecutor) Stat(ctx context.Context, path string) (executor.FileInfo, error) {
	close(e.started)
	select {
	case <-e.release:
	case <-ctx.Done():
		return executor.FileInfo{}, ctx.Err()
	}
	return e.Executor.Stat(ctx, path)
}

func TestProjectToolsUseHomeExecutorAndKeepItRunning(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	host := &projectReadHost{fakeHost: a.host, started: make(chan struct{}), release: make(chan struct{})}
	if err := a.BindProjectToolsForTest(host); err != nil {
		t.Fatal(err)
	}
	cx, err := a.host.Inspect(t.Context(), home.ID)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := a.host.Executor(cx)
	if err != nil {
		t.Fatal(err)
	}
	dir := ex.Root() + "/project"
	if err := ex.WriteFile(t.Context(), "project/STATUS.md", []byte("status from home")); err != nil {
		t.Fatal(err)
	}
	sess, err := a.store.CreateSession(t.Context(), store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	a.assignClasses(t, map[string]any{"main": "test-model"})
	call := func(id, name string, args map[string]any) providertest.Step {
		return providertest.Calls("", providertest.Call(id, name, args))
	}
	scripted := a.script(call("create", "project_create", map[string]any{"slug": "test", "name": "Test", "directory": dir}),
		call("open", "project_open", map[string]any{"project": "test"}),
		call("update", "project_update", map[string]any{"project": "test", "description": "updated"}),
		call("list", "project_list", map[string]any{}), providertest.Text("done"))
	start := time.Now()
	a.SweepIdle(t.Context(), start.Add(-time.Hour))
	a.postMessage(t, sess.ID, "manage project", "", 202)
	select {
	case <-host.started:
	case <-time.After(10 * time.Second):
		t.Fatal("project status read did not start")
	}
	defer func() {
		select {
		case <-host.release:
		default:
			close(host.release)
		}
	}()
	a.SweepIdle(t.Context(), start)
	if got := a.computerState(t, home.ID); got != "running" {
		t.Fatalf("home stopped during project read: %s", got)
	}
	close(host.release)
	a.waitIdle(t, sess.ID)
	reqs := scripted.Requests()
	if len(reqs) != 5 {
		t.Fatalf("requests = %d, want 5", len(reqs))
	}
	results := map[string]string{}
	ids := provider.WireToolCallIDs(reqs[len(reqs)-1].Messages)
	for _, m := range reqs[len(reqs)-1].Messages {
		if m.Role == provider.RoleTool {
			results[provider.WireToolCallID(ids, m.ToolCallID)] = m.Content
		}
	}
	for id, want := range map[string]string{"create": "Test", "open": "status from home", "update": "updated", "list": "updated"} {
		if !strings.Contains(results[id], want) {
			t.Errorf("%s result = %q, want %q", id, results[id], want)
		}
	}
}
