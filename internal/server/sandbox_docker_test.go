//go:build docker

package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
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
	Ports []struct {
		Port  int    `json:"port"`
		Label string `json:"label"`
	} `json:"ports"`
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
func sandboxOf(cpus float64, memoryMB, pids int64, mode string, allow []string, ports ...int) map[string]any {
	listed := []map[string]any{}
	for _, p := range ports {
		listed = append(listed, map[string]any{"port": p})
	}
	return map[string]any{
		"limits": map[string]any{"cpus": cpus, "memory_mb": memoryMB, "pids": pids},
		"egress": map[string]any{"mode": mode, "allow": allow},
		"ports":  listed,
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
	if ws.Sandbox.Egress.Mode != "open" || !slices.Equal(ws.Sandbox.Egress.Allow, egress.DefaultAllowlist) || len(ws.Sandbox.Ports) != 0 {
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
		"a port listed twice":             sandboxOf(0, 0, 0, "open", nil, 3000, 3000),
		"the daemon's port":               sandboxOf(0, 0, 0, "open", nil, 7000),
		"a port out of range":             sandboxOf(0, 0, 0, "open", nil, 70000),
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
	rec := create(sandboxOf(1.5, 1024, 256, "allowlist", []string{"GitHub.com ", "github.com", "*.npmjs.org"}, 5173))
	ws := decodeBody[workspaceWire](t, rec, 201)
	if !slices.Equal(ws.Sandbox.Egress.Allow, []string{"github.com", "*.npmjs.org"}) {
		t.Errorf("allowlist = %v, want it lowercased, trimmed, and without the repeat", ws.Sandbox.Egress.Allow)
	}
	if len(ws.Sandbox.Ports) != 1 || ws.Sandbox.Ports[0].Port != 5173 {
		t.Errorf("ports = %+v, want 5173", ws.Sandbox.Ports)
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

	rec := request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(2, 4096, 0, "none", []string{"github.com"}, 3000))
	changed := decodeBody[workspaceWire](t, rec, 200)
	if changed.Sandbox.Egress.Mode != "none" || changed.Sandbox.Limits.MemoryMB != 4096 || len(changed.Sandbox.Ports) != 1 {
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
	proxyURL.User = url.UserPassword(ws.ID, "hub-token-"+ws.ID)
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

func TestEgressPolicyKnowsAWorkspaceByItsHubToken(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 0, "allowlist", []string{"github.com"}))

	policy, err := a.Server.EgressPolicy(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if policy.Token != "hub-token-"+ws.ID || policy.Mode != egress.ModeAllowlist || !slices.Equal(policy.Allow, []string{"github.com"}) {
		t.Errorf("policy = %+v", policy)
	}
	if _, err := a.Server.EgressPolicy(t.Context(), "missing"); err != egress.ErrUnknownWorkspace {
		t.Errorf("policy of a missing workspace = %v, want ErrUnknownWorkspace", err)
	}
}

func TestForksAndChildrenKeepTheSandboxButNotThePorts(t *testing.T) {
	a := newAPI(t)
	a.host.egressControl = true
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(1, 512, 64, "allowlist", []string{"github.com"}, 3000))
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

	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	last := path.Entries[len(path.Entries)-1]
	fork := decodeBody[sessionWire](t, request(t, a.Server, "POST", "/api/sessions/"+sess.ID+"/fork",
		map[string]any{"entry_id": last.ID, "with_workspace": true}), 201)

	for name, id := range map[string]string{"child": child.WorkspaceID, "fork": fork.WorkspaceID} {
		got := decodeBody[workspaceWire](t, request(t, a.Server, "GET", "/api/workspaces/"+id, nil), 200)
		if got.Sandbox.Limits.MemoryMB != 512 || got.Sandbox.Egress.Mode != "allowlist" || len(got.Sandbox.Ports) != 0 {
			t.Errorf("%s sandbox = %+v, want the parent's limits and egress and no ports", name, got.Sandbox)
		}
		if confined := a.host.confinements(id); len(confined) != 1 || !confined[0].Proxied || confined[0].Limits.PIDs != 64 {
			t.Errorf("%s created with %+v, want the parent's confinement", name, confined)
		}
	}
}

// previewRequest sends one request to a preview's host.
func previewRequest(t *testing.T, a *api, host, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Host = host
	if cookie != nil {
		r.AddCookie(cookie)
		r.AddCookie(&http.Cookie{Name: "app", Value: "kept"})
	}
	rec := httptest.NewRecorder()
	a.Server.Handler().ServeHTTP(rec, r)
	return rec
}

func TestAPreviewForwardsAListedPortToTheSignedInBrowser(t *testing.T) {
	a := newAPI(t)
	project, dir := a.newProject(t, "demo")
	initRepo(t, dir)
	ws := a.newWorkspace(t, project.ID)

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The app sees its own cookies, never the preview's, and the host
		// the browser asked for.
		if _, err := r.Cookie("eika_preview"); err == nil {
			http.Error(w, "the preview cookie leaked", http.StatusTeapot)
			return
		}
		cookie, _ := r.Cookie("app")
		io.WriteString(w, "app at "+r.Host+" "+r.URL.Path+" cookie="+cookie.Value)
	}))
	defer app.Close()
	a.host.ports[5173] = app.URL

	open := func() *httptest.ResponseRecorder {
		return request(t, a.Server, "POST", "/api/workspaces/"+ws.ID+"/ports/5173/preview", nil)
	}
	if rec := open(); rec.Code != 404 {
		t.Errorf("preview of a port not listed = %d, want 404", rec.Code)
	}
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 0, "open", nil, 5173))

	body := decodeBody[struct {
		URL string `json:"url"`
	}](t, open(), 200)
	u, err := url.Parse(body.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", body.URL, err)
	}
	// httptest's requests are for example.com, which previews live under.
	if u.Host != "5173-"+ws.ID+".example.com" || u.Path != "/__eika/preview" || u.Query().Get("ticket") == "" {
		t.Fatalf("preview url = %s", body.URL)
	}

	if rec := previewRequest(t, a, u.Host, "/", nil); rec.Code != 401 {
		t.Errorf("a preview without a session = %d, want 401", rec.Code)
	}
	redeemed := previewRequest(t, a, u.Host, u.RequestURI(), nil)
	if redeemed.Code != http.StatusSeeOther {
		t.Fatalf("redeeming the ticket = %d: %s", redeemed.Code, redeemed.Body.String())
	}
	cookies := redeemed.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "eika_preview" || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v, want one http-only preview session", cookies)
	}
	if again := previewRequest(t, a, u.Host, u.RequestURI(), nil); again.Code != 403 {
		t.Errorf("a ticket used twice = %d, want 403", again.Code)
	}

	page := previewRequest(t, a, u.Host, "/src/main.ts", cookies[0])
	if page.Code != 200 || page.Body.String() != "app at "+u.Host+" /src/main.ts cookie=kept" {
		t.Errorf("forwarded = %d %q", page.Code, page.Body.String())
	}
	// The session opens this port of this workspace and no other.
	if rec := previewRequest(t, a, "3000-"+ws.ID+".example.com", "/", cookies[0]); rec.Code != 401 {
		t.Errorf("the session on another port = %d, want 401", rec.Code)
	}

	// Taking the port off the list closes the preview at once.
	request(t, a.Server, "PUT", "/api/workspaces/"+ws.ID+"/sandbox", sandboxOf(0, 0, 0, "open", nil))
	if rec := previewRequest(t, a, u.Host, "/", cookies[0]); rec.Code != 403 || !strings.Contains(rec.Body.String(), "no longer forwards") {
		t.Errorf("a port taken off the list = %d %q, want 403", rec.Code, rec.Body.String())
	}
}
