//go:build docker

package server_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/server"
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
func (a *api) containers(t *testing.T) int {
	t.Helper()
	all, err := a.host.List(t.Context())
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	return len(all)
}

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
	if a.containers(t) != 1 {
		t.Errorf("containers = %d, want 1", a.containers(t))
	}
	if got := a.host.confinements(home.ID); len(got) == 0 || got[0].Limits.PIDs != 4096 {
		t.Errorf("home was created with %+v, want the sandbox defaults", got)
	}
}

func TestEnsureHomeIsIdempotent(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("first EnsureHome: %v", err)
	}
	first := a.homeOf(t)
	confined := len(a.host.confinements(first.ID))

	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("second EnsureHome: %v", err)
	}
	if again := a.homeOf(t); again.ID != first.ID || a.containers(t) != 1 {
		t.Errorf("after a second ensure: home %s, %d containers; want home %s and 1 container", again.ID, a.containers(t), first.ID)
	}
	if got := len(a.host.confinements(first.ID)); got != confined {
		t.Errorf("a running home was confined again: %d confinements, want %d", got, confined)
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
	if a.containers(t) != 1 {
		t.Errorf("containers = %d, want 1", a.containers(t))
	}
}

func TestEnsureHomeAfterARestartFindsTheSameComputer(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	before := a.homeOf(t)

	if err := a.anotherServer(t).EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome after the restart: %v", err)
	}
	if after := a.homeOf(t); after.ID != before.ID || a.containers(t) != 1 {
		t.Errorf("after the restart: home %s with %d containers, want home %s alone", after.ID, a.containers(t), before.ID)
	}
}

func TestEnsureHomeMakesAMissingContainerAgain(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	before := a.homeOf(t)
	if err := a.host.Destroy(t.Context(), &computer.Computer{ID: before.ID}); err != nil {
		t.Fatalf("remove the container: %v", err)
	}

	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome with the container gone: %v", err)
	}
	if after := a.homeOf(t); after.ID != before.ID || after.State != "running" || a.containers(t) != 1 {
		t.Errorf("after: home %s %s with %d containers, want %s running alone", after.ID, after.State, a.containers(t), before.ID)
	}
}

func TestEnsureHomeStartsAStoppedContainer(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	home := a.homeOf(t)
	a.stopIdle(t)
	if got := a.computerState(t, home.ID); got != "stopped" {
		t.Fatalf("state after stop = %q, want stopped", got)
	}

	if err := a.anotherServer(t).EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome with the container stopped: %v", err)
	}
	if got := a.computerState(t, home.ID); got != "running" {
		t.Errorf("state after ensure = %q, want running", got)
	}
}

func TestHomeIsListedWhileTheHarnessCannotMakeIt(t *testing.T) {
	a := newAPI(t)
	a.host.failCreate(errors.New("pull access denied for eika-sandbox"))

	if err := a.EnsureHome(t.Context()); err == nil {
		t.Fatal("EnsureHome succeeded without a sandbox image")
	}
	home := a.homeOf(t)
	if home.State != "creating" || a.containers(t) != 0 {
		t.Errorf("home = %+v with %d containers, want it listed as creating and nothing made", home, a.containers(t))
	}

	a.host.failCreate(nil)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome once the image is there: %v", err)
	}
	if after := a.homeOf(t); after.ID != home.ID || after.State != "running" {
		t.Errorf("home = %+v, want %s running", after, home.ID)
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

// bashIn runs one echo in a new session on a computer and returns what the
// tool answered.
func (a *api) bashIn(t *testing.T, computerID, word string) string {
	t.Helper()
	sess := a.newSession(t, computerID)
	a.script(
		providertest.Calls("", providertest.Call("c1", "bash", map[string]any{"command": "echo " + word})),
		providertest.Text("done"),
	)
	a.postMessage(t, sess.ID, "say "+word, "", 202)
	if state := a.waitIdle(t, sess.ID); state.Run == nil || state.Run.State != "done" {
		t.Fatalf("run = %+v, want it done", state.Run)
	}
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	for _, m := range path.Messages {
		if m.Role == "tool" {
			return m.Content
		}
	}
	t.Fatalf("no tool result among %+v", path.Messages)
	return ""
}

func TestAChatOnHomeRunsBash(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if got := a.bashIn(t, a.homeOf(t).ID, "hello-home"); !strings.Contains(got, "hello-home") {
		t.Errorf("bash result = %q, want its output", got)
	}
}

func TestAnIdleStoppedHomeStartsWhenBashRuns(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	home := a.homeOf(t)

	start := time.Now()
	a.SweepIdle(t.Context(), start)
	a.SweepIdle(t.Context(), start.Add(15*time.Minute))
	if got := a.computerState(t, home.ID); got != "stopped" {
		t.Fatalf("home after the idle timeout = %q, want stopped", got)
	}

	if got := a.bashIn(t, home.ID, "back-again"); !strings.Contains(got, "back-again") {
		t.Errorf("bash result = %q, want the output of a command run after the restart", got)
	}
	if got := a.computerState(t, home.ID); got != "running" {
		t.Errorf("home after the run = %q, want running", got)
	}
}

func TestBashOnHomeMakesAMissingContainerAgain(t *testing.T) {
	a := newAPI(t)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	home := a.homeOf(t)
	if err := a.host.Destroy(t.Context(), &computer.Computer{ID: home.ID}); err != nil {
		t.Fatalf("remove the container: %v", err)
	}

	if got := a.bashIn(t, home.ID, "rebuilt"); !strings.Contains(got, "rebuilt") {
		t.Errorf("bash result = %q, want its output", got)
	}
	if after := a.homeOf(t); after.ID != home.ID || a.containers(t) != 1 {
		t.Errorf("after: home %s with %d containers, want %s alone", after.ID, a.containers(t), home.ID)
	}
}

// seedComputer records a computer that is not home, with a running container.
// Nothing in the harness makes one yet: a project's own computer will.
func (a *api) seedComputer(t *testing.T) computerWire {
	t.Helper()
	id := store.NewID()
	cx, err := a.host.Create(t.Context(), computer.Spec{ID: id})
	if err != nil {
		t.Fatalf("create the container: %v", err)
	}
	if err := a.host.Start(t.Context(), &cx); err != nil {
		t.Fatalf("start the container: %v", err)
	}
	if _, err := a.store.CreateComputer(t.Context(), store.Computer{
		ID: id, Name: "project", Image: cx.Image, State: string(cx.State), ContainerID: cx.ContainerID,
	}); err != nil {
		t.Fatalf("record the computer: %v", err)
	}
	return computerWire{ID: id, Name: "project", State: string(cx.State)}
}

func TestARunStartsAStoppedComputerThatIsNotHome(t *testing.T) {
	a := newAPI(t)
	other := a.seedComputer(t)
	a.stopIdle(t)
	if got := a.computerState(t, other.ID); got != "stopped" {
		t.Fatalf("computer after the idle timeout = %q, want stopped", got)
	}

	if got := a.bashIn(t, other.ID, "started-for-the-run"); !strings.Contains(got, "started-for-the-run") {
		t.Errorf("bash result = %q, want its output", got)
	}
	if got := a.computerState(t, other.ID); got != "running" {
		t.Errorf("computer after the run = %q, want running", got)
	}
}

func TestARunSaysWhyAComputerCouldNotBeStarted(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	sess := a.newSession(t, home.ID)
	a.stopIdle(t)
	a.host.startErr = errors.New("cannot allocate memory")

	a.script(providertest.Text("unreachable"))
	rec := request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/messages", map[string]any{"text": "hello"})
	if code, message := errorOf(t, rec); rec.Code != 409 || code != "conflict" ||
		!strings.Contains(message, "could not be started") || !strings.Contains(message, "cannot allocate memory") {
		t.Errorf("run = %d %q %q, want a conflict naming the start failure", rec.Code, code, message)
	}
	if got := a.computerState(t, home.ID); got != "stopped" {
		t.Errorf("state after the refused run = %q, want stopped", got)
	}
}
