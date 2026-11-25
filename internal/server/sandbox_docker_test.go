//go:build docker

package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/store"
)

// sandboxWire is a computer's sandbox as the API sends it.
type sandboxWire struct {
	Limits struct {
		CPUs     float64 `json:"cpus"`
		MemoryMB int64   `json:"memory_mb"`
		PIDs     int64   `json:"pids"`
	} `json:"limits"`
	Egress struct {
		Mode  string   `json:"mode"`
		Allow []string `json:"allow"`
	} `json:"egress"`
}

// sandboxOf builds a sandbox request body.
func sandboxOf(cpus float64, memoryMB, pids int64, mode string, allow []string) map[string]any {
	return map[string]any{
		"limits": map[string]any{"cpus": cpus, "memory_mb": memoryMB, "pids": pids},
		"egress": map[string]any{"mode": mode, "allow": allow},
	}
}

func TestTheSavedAllowlistIsStoredAsTheProxyReadsIt(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	home := a.home(t)

	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings",
		settingsSandbox(1.5, 1024, 256, "allowlist", []string{"GitHub.com ", "github.com", "*.npmjs.org"})), 200)
	got := a.homeOf(t).Sandbox
	if !slices.Equal(got.Egress.Allow, []string{"github.com", "*.npmjs.org"}) {
		t.Errorf("allowlist = %v, want it lowercased, trimmed, and without the repeat", got.Egress.Allow)
	}
	want := computer.Confinement{Limits: computer.Limits{CPUs: 1.5, MemoryBytes: 1 << 30, PIDs: 256}, Proxied: true}
	if confined := a.host.confinements(home.ID); confined[len(confined)-1] != want {
		t.Errorf("last confinement = %+v, want %+v", confined[len(confined)-1], want)
	}
}

func TestTheProxyKnowsARecreatedHomeByItsNewToken(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	home := a.home(t)
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 0, 0, "allowlist", []string{"github.com"})), 200)

	proxy := httptest.NewServer(a.egress)
	defer proxy.Close()
	status := func(token string) int {
		t.Helper()
		proxyURL, _ := url.Parse(proxy.URL)
		proxyURL.User = url.UserPassword(home.ID, token)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		resp, err := client.Get("http://example.com/")
		if err != nil {
			t.Fatalf("get through the proxy: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// A host that is not on the list is refused, which is how the proxy shows
	// it accepted the credentials; a wrong token is asked for again.
	old := a.host.proxyToken(home.ID)
	if got := status(old); got != http.StatusForbidden {
		t.Fatalf("status with the token the container was made with = %d, want 403", got)
	}

	// The container is lost and made again over the same id, and with it a
	// proxy token of its own.
	a.host.lose(t, home.ID)
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	fresh := a.host.proxyToken(home.ID)
	if fresh == old {
		t.Fatalf("the new container has the old token %q", old)
	}
	if got := status(fresh); got != http.StatusForbidden {
		t.Errorf("status with the new token = %d, want 403: the proxy must accept it", got)
	}
	if got := status(old); got != http.StatusProxyAuthRequired {
		t.Errorf("status with the old token = %d, want 407: the proxy must refuse it", got)
	}
}

// settingsSandbox is a PUT /api/settings body that sets the home computer's
// limits and egress.
func settingsSandbox(cpus float64, memoryMB, pids int64, mode string, allow []string) map[string]any {
	sb := sandboxOf(cpus, memoryMB, pids, mode, allow)
	return map[string]any{"sandbox_limits": sb["limits"], "sandbox_egress": sb["egress"]}
}

func TestSavingTheSandboxSettingsConfinesTheHomeContainer(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	home := a.home(t)
	before := len(a.host.confinements(home.ID))

	rec := request(t, a.Server, "PUT", "/api/settings", settingsSandbox(2, 4096, 0, "none", []string{"github.com"}))
	decodeBody[settingsWire](t, rec, 200)

	want := computer.Confinement{Limits: computer.Limits{CPUs: 2, MemoryBytes: 4096 << 20}, Proxied: true}
	confined := a.host.confinements(home.ID)
	if len(confined) != before+1 || confined[len(confined)-1] != want {
		t.Fatalf("confinements = %+v, want %+v applied at once", confined, want)
	}
	got := a.homeOf(t).Sandbox
	if got.Limits.CPUs != 2 || got.Limits.MemoryMB != 4096 || got.Egress.Mode != "none" || !slices.Equal(got.Egress.Allow, []string{"github.com"}) {
		t.Errorf("home sandbox = %+v, want what was saved", got)
	}

	// A write the settings refuse changes nothing, and neither does one that
	// names no sandbox key.
	if rec := request(t, a.Server, "PUT", "/api/settings", settingsSandbox(9, 0, 0, "public", nil)); rec.Code != 400 {
		t.Fatalf("too many cores = %d, want 400", rec.Code)
	}
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"theme": "light"}), 200)
	if got := len(a.host.confinements(home.ID)); got != before+1 {
		t.Errorf("%d confinements, want %d: only a valid sandbox write confines", got, before+1)
	}
	if got := a.homeOf(t).Sandbox; got.Limits.CPUs != 2 {
		t.Errorf("home sandbox = %+v, want it unchanged", got)
	}

	// Clearing the settings goes back to the defaults.
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", map[string]any{"sandbox_limits": nil, "sandbox_egress": nil}), 200)
	reset := computer.Confinement{Limits: computer.Limits{PIDs: 4096}, Proxied: true}
	if confined := a.host.confinements(home.ID); confined[len(confined)-1] != reset {
		t.Errorf("last confinement = %+v, want the defaults %+v", confined[len(confined)-1], reset)
	}
}

func TestADockerFailureLeavesTheSandboxSettingsSavedForTheNextStart(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)

	a.host.failConfine(errors.New("update container: no space left on device"))
	rec := request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 512, 300, "public", nil))
	if rec.Code != 500 {
		t.Fatalf("put settings = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	// The row and the settings hold the new values, though the container
	// kept the old ones.
	if got := a.homeOf(t).Sandbox.Limits; got.MemoryMB != 512 || got.PIDs != 300 {
		t.Errorf("home limits = %+v, want the saved ones", got)
	}
	saved := decodeBody[settingsWire](t, request(t, a.Server, "GET", "/api/settings", nil), 200)
	if string(saved.Settings["sandbox_limits"]) == "" {
		t.Error("sandbox_limits is not saved")
	}
	a.host.failConfine(nil)

	a.stopIdle(t)
	a.bashIn(t, home.ID, "converged")
	want := computer.Confinement{Limits: computer.Limits{MemoryBytes: 512 << 20, PIDs: 300}}
	if confined := a.host.confinements(home.ID); confined[len(confined)-1] != want {
		t.Errorf("last confinement = %+v, want %+v from the row at the next start", confined[len(confined)-1], want)
	}
}

func TestReconcileConfinesRunningComputers(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	a.host.egressControl = true
	if err := a.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	confined := a.host.confinements(home.ID)
	if !confined[len(confined)-1].Proxied {
		t.Fatal("running computer was not moved behind the proxy")
	}
}

func TestReconcileStopsFailedComputerAndConfinesOthers(t *testing.T) {
	a := newAPI(t)
	first := a.home(t)
	ctx := t.Context()
	_, err := a.store.CreateComputer(ctx, store.Computer{ID: "second", Name: "second", State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.host.Create(ctx, computer.Spec{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.host.Start(ctx, &second); err != nil {
		t.Fatal(err)
	}
	a.host.egressControl = true
	a.host.confineErr = errors.New("cannot confine")
	a.host.confineErrID = first.ID
	if err := a.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	stopped, err := a.host.Inspect(ctx, first.ID)
	if err != nil || stopped.State != computer.StateStopped || a.computerState(t, first.ID) != "stopped" {
		t.Fatalf("failed computer: %+v, %v", stopped, err)
	}
	confined := a.host.confinements(second.ID)
	if !confined[len(confined)-1].Proxied {
		t.Fatal("later computer was not confined")
	}
}
