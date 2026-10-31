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
	"github.com/erlidev/eika/internal/egress"
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

func TestHomeGetsTheSandboxDefaults(t *testing.T) {
	a := newAPI(t)

	cx := a.home(t)
	if cx.Sandbox.Limits.PIDs != 4096 || cx.Sandbox.Limits.CPUs != 0 || cx.Sandbox.Limits.MemoryMB != 0 {
		t.Errorf("limits = %+v, want the built-in process limit and nothing else", cx.Sandbox.Limits)
	}
	if cx.Sandbox.Egress.Mode != "open" || !slices.Equal(cx.Sandbox.Egress.Allow, egress.DefaultAllowlist) {
		t.Errorf("sandbox = %+v, want open egress with the suggested allowlist and no ports", cx.Sandbox)
	}
	if got := a.host.confinements(cx.ID); len(got) == 0 || got[0].Limits.PIDs != 4096 || got[0].Proxied {
		t.Errorf("created with %+v, want the default limits", got)
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

func TestSandboxSettingsAreChecked(t *testing.T) {
	a := newAPI(t)
	for name, body := range map[string]map[string]any{
		"more cores than the host has":  {"sandbox_limits": map[string]any{"cpus": 5}},
		"too little memory":             {"sandbox_limits": map[string]any{"memory_mb": 10}},
		"more memory than the host has": {"sandbox_limits": map[string]any{"memory_mb": 64 << 10}},
		"too few processes":             {"sandbox_limits": map[string]any{"pids": 4}},
		"a field that is not a limit":   {"sandbox_limits": map[string]any{"disk_mb": 10}},
		"a port in the allowlist":       {"sandbox_egress": map[string]any{"mode": "open", "allow": []string{"github.com:443"}}},
		"an unknown egress mode":        {"sandbox_egress": map[string]any{"mode": "closed", "allow": []string{}}},
		"a url in the allowlist":        {"sandbox_egress": map[string]any{"mode": "open", "allow": []string{"https://github.com"}}},
		"restricted egress without one": {"sandbox_egress": map[string]any{"mode": "allowlist", "allow": []string{"github.com"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if rec := request(t, a.Server, "PUT", "/api/settings", body); rec.Code != 400 {
				t.Errorf("put settings = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	a.host.egressControl = true
	if rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"sandbox_egress": map[string]any{"mode": "allowlist", "allow": []string{"github.com"}},
	}); rec.Code != 200 {
		t.Errorf("put settings with egress control = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEgressPolicyKnowsAComputerByItsProxyToken(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	cx := a.home(t)
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 0, 0, "allowlist", []string{"github.com"})), 200)

	policy, err := a.EgressPolicy(t.Context(), cx.ID)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if policy.Token != a.host.proxyToken(cx.ID) || policy.Mode != egress.ModeAllowlist || !slices.Equal(policy.Allow, []string{"github.com"}) {
		t.Errorf("policy = %+v", policy)
	}
	if _, err := a.EgressPolicy(t.Context(), "missing"); !errors.Is(err, egress.ErrUnknownComputer) {
		t.Errorf("policy of a missing computer = %v, want ErrUnknownComputer", err)
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
	if err := a.host.Destroy(t.Context(), &computer.Computer{ID: home.ID}); err != nil {
		t.Fatalf("remove the container: %v", err)
	}
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
	if rec := request(t, a.Server, "PUT", "/api/settings", settingsSandbox(9, 0, 0, "open", nil)); rec.Code != 400 {
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
	reset := computer.Confinement{Limits: computer.Limits{PIDs: 4096}}
	if confined := a.host.confinements(home.ID); confined[len(confined)-1] != reset {
		t.Errorf("last confinement = %+v, want the defaults %+v", confined[len(confined)-1], reset)
	}
}

func TestAStoppedHomeTakesTheSavedSandboxWhenItStarts(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	a.stopIdle(t)
	if got := a.computerState(t, home.ID); got != "stopped" {
		t.Fatalf("home = %q, want stopped", got)
	}

	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 1024, 200, "open", nil)), 200)
	want := computer.Confinement{Limits: computer.Limits{MemoryBytes: 1 << 30, PIDs: 200}}
	confined := a.host.confinements(home.ID)
	if last := confined[len(confined)-1]; last != want {
		t.Fatalf("confinement of the stopped container = %+v, want %+v", last, want)
	}

	// The run that starts it confines it once more, from the row.
	n := len(confined)
	a.bashIn(t, home.ID, "awake")
	confined = a.host.confinements(home.ID)
	if len(confined) != n+1 || confined[n] != want {
		t.Errorf("confinements after the start = %+v, want %+v applied again", confined[n:], want)
	}
}

func TestSavingTheSandboxSettingsBeforeHomeExistsIsPickedUpByEnsureHome(t *testing.T) {
	a := newAPI(t)
	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 0, 77, "open", nil)), 200)
	if a.containers(t) != 0 {
		t.Fatalf("containers = %d, want none made by a settings write", a.containers(t))
	}

	home := a.home(t)
	if home.Sandbox.Limits.PIDs != 77 {
		t.Errorf("home sandbox = %+v, want the saved process limit", home.Sandbox)
	}
	if got := a.host.confinements(home.ID); len(got) == 0 || got[0].Limits.PIDs != 77 {
		t.Errorf("home was created with %+v, want the saved limits", got)
	}
}

func TestSavingTheSandboxSettingsWhileHomeHasNoContainerUpdatesTheRow(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)
	if err := a.host.Destroy(t.Context(), &computer.Computer{ID: home.ID}); err != nil {
		t.Fatalf("remove the container: %v", err)
	}

	decodeBody[settingsWire](t, request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 0, 88, "open", nil)), 200)
	if got := a.homeOf(t).Sandbox.Limits.PIDs; got != 88 {
		t.Fatalf("home process limit = %d, want the saved 88", got)
	}
	if err := a.EnsureHome(t.Context()); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if confined := a.host.confinements(home.ID); confined[len(confined)-1].Limits.PIDs != 88 {
		t.Errorf("the new container was confined with %+v, want the saved limits", confined)
	}
}

func TestADockerFailureLeavesTheSandboxSettingsSavedForTheNextStart(t *testing.T) {
	a := newAPI(t)
	home := a.home(t)

	a.host.failConfine(errors.New("update container: no space left on device"))
	rec := request(t, a.Server, "PUT", "/api/settings", settingsSandbox(0, 512, 300, "open", nil))
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
