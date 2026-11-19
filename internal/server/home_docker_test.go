//go:build docker

package server_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// homeOf returns the home computer as the API lists it, and fails the test
// when there is not exactly one.
func (a *api) homeOf(t *testing.T) computerWire {
	t.Helper()
	var homes []computerWire
	for _, cx := range decodeBody[computersWire](t, request(t, a.Server, "GET", "/api/computers", nil), 200).Computers {
		if cx.Home {
			homes = append(homes, cx)
		}
	}
	if len(homes) != 1 {
		t.Fatalf("home computers = %+v, want exactly one", homes)
	}
	return homes[0]
}

// home makes the home computer and returns it as the API lists it.
func (a *api) home(t *testing.T) computerWire {
	t.Helper()
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	return a.homeOf(t)
}

// stopIdle stops the running computers the way the idle stopper does, by
// sweeping once to see them and again after the timeout.
func (a *api) stopIdle(t *testing.T) {
	t.Helper()
	start := time.Now()
	a.SweepIdle(t.Context(), start)
	a.SweepIdle(t.Context(), start.Add(15*time.Minute))
}

// containers counts the computer containers the host holds.
func (a *api) containers() int { return a.host.count() }

// anotherServer returns a second harness process on the same database and
// host, which is what a restart is.
func (a *api) anotherServer(t *testing.T) *server.Server {
	t.Helper()
	s := server.New(testConfig(), testLogger(), server.Deps{Store: a.store, Computers: a.host}, server.Options{})
	t.Cleanup(s.Close)
	return s
}

func TestEnsureHomeCreatesAndStartsTheHomeComputer(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}

	home := a.homeOf(t)
	if home.Name != "home" || home.State != "running" {
		t.Errorf("home = %+v, want the running computer named home", home)
	}
	if a.containers() != 1 {
		t.Errorf("containers = %d, want 1", a.containers())
	}
	if got := a.host.confinements(home.ID); len(got) == 0 || got[0].Limits.PIDs != 4096 {
		t.Errorf("home was created with %+v, want the sandbox defaults", got)
	}
}

func TestEnsureHomeRacingMakesOneComputer(t *testing.T) {
	a := newAPI(t)
	servers := []*server.Server{a.Server, a.anotherServer(t), a.anotherServer(t)}

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() { errs <- servers[i%len(servers)].EnsureHome(t.Context()) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("EnsureHome: %v", err)
		}
	}

	all := decodeBody[computersWire](t, request(t, a.Server, "GET", "/api/computers", nil), 200).Computers
	if len(all) != 1 || !all[0].Home {
		t.Errorf("computers = %+v, want the home computer alone", all)
	}
	if a.containers() != 1 {
		t.Errorf("containers = %d, want 1", a.containers())
	}
}

func TestKeepHomeRetriesUntilTheHostCanMakeIt(t *testing.T) {
	a := newAPI(t)
	a.host.failCreate(errors.New("pull access denied for eika-sandbox"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.KeepHome(t.Context(), 5*time.Millisecond)
	}()
	waitFor(t, "the first attempt to record the home computer", func() bool {
		var found bool
		for _, cx := range decodeBody[computersWire](t, request(t, a.Server, "GET", "/api/computers", nil), 200).Computers {
			found = found || cx.Home
		}
		return found
	})
	select {
	case <-done:
		t.Fatal("KeepHome returned while the host could not create the computer")
	case <-time.After(30 * time.Millisecond):
	}

	a.host.failCreate(nil)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("KeepHome did not return once the host could create the computer")
	}
	if home := a.homeOf(t); home.State != "running" {
		t.Errorf("home = %+v, want running", home)
	}
}

// bashIn delegates a command to a worker and returns its result.
func (a *api) bashIn(t *testing.T, computerID, word string) string {
	t.Helper()
	if err := a.BindAgentToolsForTest(a.host); err != nil {
		t.Fatal(err)
	}
	home, err := a.store.EnsureHomeSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	eika := providertest.New(call("start", "subagent_start", map[string]any{"agent": "worker", "name": "shell", "brief": "Run the command"}), providertest.Text("started"))
	worker := providertest.New(call("cmd", "bash", map[string]any{"command": "echo " + word}), call("done", "report", map[string]any{"summary": "done"}), providertest.Text("done"))
	a.use(byAgent{"You are Eika": answeringNotices(eika), "You are a worker": worker})
	a.postMessage(t, home.ID, "run the command", "", 202)
	var child store.Session
	waitFor(t, "worker to finish", func() bool {
		child, err = a.store.Subagent(t.Context(), home.ID, "shell")
		return err == nil && child.State == store.SessionIdle
	})
	if child.ComputerID != computerID {
		t.Fatalf("worker computer = %s, want %s", child.ComputerID, computerID)
	}
	path, err := a.store.SessionPath(t.Context(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := session.Messages(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, word) {
			return m.Content
		}
	}
	t.Fatalf("no command result among %+v", messages)
	return ""
}

func TestBashOnHomeMakesAMissingContainerAgain(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	home := a.homeOf(t)
	a.host.lose(t, home.ID)

	if got := a.bashIn(t, home.ID, "rebuilt"); !strings.Contains(got, "rebuilt") {
		t.Errorf("bash result = %q, want its output", got)
	}
	if after := a.homeOf(t); after.ID != home.ID || a.containers() != 1 {
		t.Errorf("after: home %s with %d containers, want %s alone", after.ID, a.containers(), home.ID)
	}
}
