//go:build docker

package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/workspace"
)

// sandboxWire is a workspace's sandbox as the API sends it.
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

// usageWire is GET /api/workspaces/{id}/usage.
type usageWire struct {
	CPUPercent float64 `json:"cpu_percent"`
	CPUs       float64 `json:"cpus"`
	PIDsLimit  int64   `json:"pids_limit"`
	Blocked    []struct {
		Host  string `json:"host"`
		Count int    `json:"count"`
	} `json:"blocked"`
}

// sandboxOf builds a sandbox request body.
func sandboxOf(cpus float64, memoryMB, pids int64, mode string, allow []string) map[string]any {
	return map[string]any{
		"limits": map[string]any{"cpus": cpus, "memory_mb": memoryMB, "pids": pids},
		"egress": map[string]any{"mode": mode, "allow": allow},
	}
}

func TestANewWorkspaceGetsTheSandboxDefaults(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)

	ws := a.newWorkspace(t, project.ID)
	if ws.Sandbox.Limits.PIDs != 4096 || ws.Sandbox.Limits.CPUs != 0 || ws.Sandbox.Limits.MemoryMB != 0 {
		t.Errorf("limits = %+v, want the built-in process limit and nothing else", ws.Sandbox.Limits)
	}
	if ws.Sandbox.Egress.Mode != "open" || !slices.Equal(ws.Sandbox.Egress.Allow, egress.DefaultAllowlist) {
		t.Errorf("sandbox = %+v, want open egress with the suggested allowlist and no ports", ws.Sandbox)
	}
	if got := a.host.confinements(ws.ID); len(got) != 1 || got[0].Limits.PIDs != 4096 || got[0].Proxied {
		t.Errorf("created with %+v, want the default limits", got)
	}

	// The settings' defaults apply to the next workspace.
	rec := request(t, a.Server, "PUT", "/api/settings", map[string]any{
		"sandbox_limits": map[string]any{"cpus": 2, "memory_mb": 2048, "pids": 512},
	})
	if rec.Code != 200 {
		t.Fatalf("put settings = %d: %s", rec.Code, rec.Body.String())
	}
	next := a.newWorkspace(t, project.ID)
	want := workspace.Limits{CPUs: 2, MemoryBytes: 2048 << 20, PIDs: 512}
	if got := a.host.confinements(next.ID); len(got) != 1 || got[0].Limits != want {
		t.Errorf("created with %+v, want %+v", got, want)
	}
}

func TestSandboxSettingsAreChecked(t *testing.T) {
	a := newAPI(t)
	for name, body := range map[string]map[string]any{
		"more cores than the host has":  {"sandbox_limits": map[string]any{"cpus": 5}},
		"too little memory":             {"sandbox_limits": map[string]any{"memory_mb": 10}},
		"a field that is not a limit":   {"sandbox_limits": map[string]any{"disk_mb": 10}},
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

func TestCreatingAWorkspaceChecksItsSandbox(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	create := func(sb map[string]any) *httptest.ResponseRecorder {
		return request(t, a.Server, "POST", "/api/workspaces", map[string]any{
			"project_id": project.ID, "name": "work", "sandbox": sb,
		})
	}
	for name, sb := range map[string]map[string]any{
		"more cores than the host has":    sandboxOf(8, 0, 0, "open", nil),
		"more memory than the host has":   sandboxOf(0, 64<<10, 0, "open", nil),
		"too few processes":               sandboxOf(0, 0, 4, "open", nil),
		"a bad allowlist entry":           sandboxOf(0, 0, 0, "open", []string{"github.com:443"}),
		"restricted egress on this host":  sandboxOf(0, 0, 0, "none", nil),
		"an allowlist on this host, even": sandboxOf(0, 0, 0, "allowlist", []string{"github.com"}),
	} {
		t.Run(name, func(t *testing.T) {
			if rec := create(sb); rec.Code != 400 {
				t.Errorf("create = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}

	a.host.egressControl = true
	rec := create(sandboxOf(1.5, 1024, 256, "allowlist", []string{"GitHub.com ", "github.com", "*.npmjs.org"}))
	ws := decodeBody[workspaceWire](t, rec, 201)
	if !slices.Equal(ws.Sandbox.Egress.Allow, []string{"github.com", "*.npmjs.org"}) {
		t.Errorf("allowlist = %v, want it lowercased, trimmed, and without the repeat", ws.Sandbox.Egress.Allow)
	}
	want := workspace.Confinement{Limits: workspace.Limits{CPUs: 1.5, MemoryBytes: 1 << 30, PIDs: 256}, Proxied: true}
	if got := a.host.confinements(ws.ID); len(got) != 1 || got[0] != want {
		t.Errorf("created with %+v, want %+v", got, want)
	}
}

func TestChangingASandboxAppliesItToTheContainer(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)

	rec := request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(2, 4096, 0, "none", []string{"github.com"}))
	changed := decodeBody[workspaceWire](t, rec, 200)
	if changed.Sandbox.Egress.Mode != "none" || changed.Sandbox.Limits.MemoryMB != 4096 {
		t.Errorf("sandbox = %+v, want what was sent", changed.Sandbox)
	}
	confined := a.host.confinements(ws.ID)
	want := workspace.Confinement{Limits: workspace.Limits{CPUs: 2, MemoryBytes: 4096 << 20}, Proxied: true}
	if len(confined) != 2 || confined[1] != want {
		t.Fatalf("confinements = %+v, want the new one applied at once", confined)
	}
	if got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID, nil), 200); !reflect.DeepEqual(got.Sandbox, changed.Sandbox) {
		t.Errorf("stored sandbox = %+v, want %+v", got.Sandbox, changed.Sandbox)
	}

	// A start applies the recorded sandbox again, which completes a change
	// the container took only part of.
	request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil)
	request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/start", nil)
	if confined := a.host.confinements(ws.ID); len(confined) != 3 || confined[2] != want {
		t.Errorf("confinements after a restart = %+v, want the recorded one again", confined)
	}

	if rec := request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 0, "sideways", nil)); rec.Code != 400 {
		t.Errorf("an unknown mode = %d, want 400", rec.Code)
	}
	if rec := request(t, a.Server, "PUT", "/api/workspaces/missing/sandbox", sandboxOf(0, 0, 0, "open", nil)); rec.Code != 404 {
		t.Errorf("a missing workspace = %d, want 404", rec.Code)
	}
}

func TestUsageReportsTheSampleAndTheRefusedHosts(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 100, "allowlist", []string{"github.com"}))

	// The sandbox asks the proxy for a host that is not on its list.
	proxy := httptest.NewServer(a.egress)
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword(ws.ID, "proxy-token-"+ws.ID)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatalf("get through the proxy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("proxy status = %d, want 403", resp.StatusCode)
	}

	usage := decodeBody[usageWire](t, request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/usage", nil), 200)
	if usage.CPUPercent != 42 || usage.CPUs != 4 || usage.PIDsLimit != 100 {
		t.Errorf("usage = %+v, want the sample, the host's cores, and the process limit", usage)
	}
	if len(usage.Blocked) != 1 || usage.Blocked[0].Host != "example.com" {
		t.Errorf("blocked = %+v, want example.com", usage.Blocked)
	}

	request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/stop", nil)
	if rec := request(t, a.Server, "GET", "/api/workspaces/"+ws.ID+"/usage", nil); rec.Code != 409 {
		t.Errorf("usage of a stopped workspace = %d, want 409", rec.Code)
	}
}

func TestEgressPolicyKnowsAWorkspaceByItsProxyToken(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 0, "allowlist", []string{"github.com"}))

	policy, err := a.EgressPolicy(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if policy.Token != "proxy-token-"+ws.ID || policy.Mode != egress.ModeAllowlist || !slices.Equal(policy.Allow, []string{"github.com"}) {
		t.Errorf("policy = %+v", policy)
	}
	if _, err := a.EgressPolicy(t.Context(), "missing"); !errors.Is(err, egress.ErrUnknownWorkspace) {
		t.Errorf("policy of a missing workspace = %v, want ErrUnknownWorkspace", err)
	}
}

// A child agent works in its parent's container, so it is confined by the
// parent's sandbox rather than a copy.
func TestChildrenShareTheSandbox(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(1, 512, 64, "allowlist", []string{"github.com"}))
	sess := a.newSession(t, ws.ID)

	a.script(
		spawnStep("c1", "worker", "write the notes", true),
		providertest.Text("nothing to do"),
		providertest.Calls("", providertest.Call("c2", "bash",
			map[string]any{"command": "echo one > one.txt && git add -A && git commit -qm one"})),
		providertest.Text("the child is done"),
	)
	a.postMessage(t, sess.ID, "hand it to a child", "", 202)
	a.waitIdle(t, sess.ID)
	child := a.waitAgent(t, sess.ID)

	got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+child.WorkspaceID, nil), 200)
	if got.WorktreeOf != ws.ID || got.Sandbox.Limits.MemoryMB != 0 {
		t.Errorf("child = %+v, want a worktree of %s with no sandbox of its own", got, ws.ID)
	}
	if confined := a.host.confinements(child.WorkspaceID); len(confined) != 0 {
		t.Errorf("child confined with %+v, want no container of its own", confined)
	}
	if rec := request(t, a.Server, "PUT", "/api/workspaces/"+child.WorkspaceID+"/sandbox",
		sandboxOf(0, 0, 0, "open", nil)); rec.Code != 409 {
		t.Errorf("changing a child's sandbox = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	// The child's usage is its holder's container, under the holder's limits.
	usage := decodeBody[usageWire](t, request(t, a.Server, "GET", "/api/workspaces/"+child.WorkspaceID+"/usage", nil), 200)
	if usage.PIDsLimit != 64 || usage.CPUs != 1 {
		t.Errorf("child usage = %+v, want the holder's limits", usage)
	}
}
