//go:build docker

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// TestCoreProcessFixture runs the production composition in a separate process.
func TestCoreProcessFixture(t *testing.T) {
	path := os.Getenv("EIKA_PROCESS_TEST_CONFIG")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := server.Run(context.Background(), cfg, slog.New(slog.NewJSONHandler(os.Stderr, nil)), server.Options{}); err != nil {
		t.Fatal(err)
	}
}

// coreListenAddress reads only the listening event from this Core's log.
func coreListenAddress(output []byte) string {
	scan := bufio.NewScanner(bytes.NewReader(output))
	for scan.Scan() {
		var event struct {
			Message string `json:"msg"`
			Address string `json:"addr"`
		}
		if json.Unmarshal(scan.Bytes(), &event) == nil && event.Message == "http server listening" {
			return event.Address
		}
	}
	return ""
}

func TestCoreListenAddress(t *testing.T) {
	log := []byte("{\"msg\":\"other\",\"addr\":\"wrong\"}\n{\"msg\":\"http server listening\",\"addr\":\"127.0.0.1:33037\"}\n")
	if got := coreListenAddress(log); got != "127.0.0.1:33037" {
		t.Fatalf("listening address = %q", got)
	}
}

type deathModel struct {
	mu           sync.Mutex
	home, child  int
	resumeIssued bool
	premature    int
}

func (m *deathModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"test-model","object":"model","created":0,"owned_by":"test"}]}`)
		return
	}
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	defer m.mu.Unlock()
	var name string
	var args map[string]any
	if bytes.Contains(body, []byte("You are a worker")) {
		m.child++
		if m.child > 1 && !m.resumeIssued {
			m.premature++
		}
		switch m.child {
		case 1:
			name = "bash"
			args = map[string]any{"command": "echo ran >> /home/eika/process-death-count; sleep 300"}
		case 2:
			name = "report"
			args = map[string]any{"summary": "Resumed work"}
		}
	} else if bytes.Contains(body, []byte("You are Eika")) {
		m.home++
		switch m.home {
		case 1:
			name = "subagent_start"
			args = map[string]any{"agent": "worker", "name": "process-worker", "brief": "Run the command"}
		case 3:
			if bytes.Contains(body, []byte("Resume the worker")) {
				m.resumeIssued = true
				name = "subagent_message"
				args = map[string]any{"subagent": "process-worker", "text": "continue"}
			}
		}
	}
	delta := map[string]any{"content": "done"}
	finish := "stop"
	if name != "" {
		encoded, _ := json.Marshal(args)
		delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call-%s", name), "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}
		finish = "tool_calls"
	}
	chunk, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
}

func (m *deathModel) counts() (int, int) { m.mu.Lock(); defer m.mu.Unlock(); return m.home, m.child }

func TestProcessDeathRecoversAndResumesTheSameChild(t *testing.T) {
	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("no docker socket")
	}
	if err := exec.Command("docker", "image", "inspect", realSandboxImage()).Run(); err != nil {
		t.Skip("sandbox image missing: run make sandbox-test")
	}
	ctx := t.Context()
	db := storetest.URL(t)
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	dir := t.TempDir()
	eikad := filepath.Join(dir, "eikad")
	build := exec.CommandContext(ctx, "go", "build", "-o", eikad, "github.com/erlidev/eika/cmd/eikad")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build eikad: %v: %s", err, out)
	}
	model := &deathModel{}
	modelServer := httptest.NewServer(model)
	t.Cleanup(modelServer.Close)
	keyPath := filepath.Join(dir, "secret.key")
	box, err := secret.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := box.Seal("test-key")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProvider(ctx, store.Provider{Name: "test", Kind: "openai", BaseURL: modelServer.URL, APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.OverrideProvider(ctx, p.ID, store.ProviderOverride{Slots: new(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateModel(ctx, store.Model{ProviderID: p.ID, Name: "test-model", Model: "test-model", ContextWindow: 32768, MaxOutput: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(ctx, map[string]json.RawMessage{"model_classes": json.RawMessage(`{"main":"test-model","fast":"test-model"}`)}); err != nil {
		t.Fatal(err)
	}
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var addr string
	cfg := testConfig()
	cfg.Listen, cfg.DatabaseURL, cfg.SecretKeyFile = "127.0.0.1:0", db, keyPath
	cfg.SandboxImage, cfg.EikadBinary = realSandboxImage(), eikad
	cfg.SandboxNetwork, cfg.SandboxInternalNetwork = "", ""
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "core.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	host, err := computer.NewHost(computer.Options{DockerSocket: cfg.DockerSocket, Image: cfg.SandboxImage, EikadBinary: eikad}, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		row, err := st.HomeComputer(cleanupCtx)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				t.Errorf("find home computer for cleanup: %v", err)
			}
			return
		}
		for _, resource := range []struct{ kind, name string }{
			{"container", computer.ContainerName(row.ID)}, {"volume", computer.VolumeName(row.ID)},
		} {
			args := []string{"rm", "-f", resource.name}
			if resource.kind == "volume" {
				args = append([]string{"volume"}, args...)
			}
			out, err := exec.CommandContext(cleanupCtx, "docker", args...).CombinedOutput()
			if err != nil {
				t.Errorf("remove %s %s: %v: %s", resource.kind, resource.name, err, out)
			}
		}
	})
	var process *exec.Cmd
	client := &http.Client{Timeout: 2 * time.Second}
	start := func() {
		t.Helper()
		process = exec.Command(os.Args[0], "-test.run=^TestCoreProcessFixture$")
		process.Env = append(os.Environ(), "EIKA_PROCESS_TEST_CONFIG="+configPath, "EIKA_TEST_DATABASE_URL="+db)
		log, err := os.CreateTemp(dir, "core-log-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = log.Close()
			if t.Failed() {
				if output, err := os.ReadFile(log.Name()); err == nil {
					t.Logf("Core log:\n%s", output)
				}
			}
		})
		process.Stdout, process.Stderr = log, log
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		started := process
		t.Cleanup(func() {
			if started.ProcessState == nil {
				_ = started.Process.Kill()
				_ = started.Wait()
			}
		})
		addr = ""
		waitFor(t, "Core to serve", func() bool {
			output, err := os.ReadFile(log.Name())
			if err != nil {
				return false
			}
			addr = coreListenAddress(output)
			if addr == "" {
				return false
			}
			resp, err := client.Get("http://" + addr + "/healthz")
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == 200
		})
	}
	post := func(text string) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/api/sessions/"+home.ID+"/messages", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusAccepted {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("post: %s: %s", resp.Status, b)
		}
	}
	start()
	waitFor(t, "home computer", func() bool {
		row, err := st.HomeComputer(ctx)
		if err != nil {
			return false
		}
		cx, err := host.Inspect(ctx, row.ID)
		return err == nil && cx.State == computer.StateRunning
	})
	post("Start the worker")
	var child store.Session
	var ex executor.Executor
	waitFor(t, "the sandbox call to start", func() bool {
		child, err = st.Subagent(ctx, home.ID, "process-worker")
		if err != nil {
			return false
		}
		row, err := st.HomeComputer(ctx)
		if err != nil {
			return false
		}
		cx, err := host.Inspect(ctx, row.ID)
		if err != nil {
			return false
		}
		ex, err = host.Executor(cx)
		if err != nil {
			return false
		}
		b, err := ex.ReadFile(ctx, "process-death-count", executor.ReadOpts{})
		return err == nil && string(b) == "ran\n"
	})
	waitFor(t, "home turn to finish", func() bool {
		runs, err := st.Runs(ctx, home.ID)
		return err == nil && len(runs) == 1 && runs[0].State == store.RunDone
	})
	// The marker shows the command started; the stored call and running
	// run show Core has not yet received a result when it is killed.
	path, err := st.SessionPath(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := session.Messages(path)
	if err != nil {
		t.Fatal(err)
	}
	var callID string
	for _, m := range messages {
		for _, call := range m.ToolCalls {
			if call.Name == "bash" && call.Wire() == "call-bash" {
				if callID != "" {
					t.Fatal("multiple bash calls before restart")
				}
				callID = call.ID
			}
		}
	}
	if callID == "" {
		t.Fatalf("no stored bash call: %+v", messages)
	}
	for _, m := range messages {
		if m.Role == provider.RoleTool && m.ToolCallID == callID {
			t.Fatalf("bash finished before Core was killed: %+v", m)
		}
	}
	childRuns, err := st.Runs(ctx, child.ID)
	if err != nil || len(childRuns) != 1 || childRuns[0].State != store.RunRunning {
		t.Fatalf("child run before kill = %+v, %v", childRuns, err)
	}
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Wait()
	start()
	recovered, err := st.Session(ctx, child.ID)
	if err != nil || recovered.State != store.SessionIdle {
		t.Fatalf("recovered child = %+v, %v", recovered, err)
	}
	path, err = st.SessionPath(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err = session.Messages(path)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, m := range messages {
		if m.Role == provider.RoleTool && m.ToolCallID == callID {
			results++
			if !m.IsError || !strings.Contains(m.Content, "outcome is unknown") {
				t.Errorf("recovery result = %+v", m)
			}
		}
	}
	if results != 1 {
		t.Fatalf("recovery results = %d", results)
	}
	entries, err := st.Entries(ctx, home.ID)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, e := range entries {
		if e.Kind == store.KindNotice {
			text, err := session.TextOf(e)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, "process-worker") && strings.Contains(text, "interrupted") {
				notices++
			}
		}
	}
	if notices != 1 {
		t.Fatalf("interruption notices = %d", notices)
	}
	// Recovery finishes before the listening log. Observe past the first
	// five-second startup retry as well as the immediate scheduling path.
	deadline := time.Now().Add(6 * time.Second)
	var runs []store.Run
	for {
		beforeHome, beforeChild := model.counts()
		runs, err = st.Runs(ctx, child.ID)
		current, sessionErr := st.Session(ctx, child.ID)
		if err != nil || sessionErr != nil || len(runs) != 1 || runs[0].State != store.RunAborted ||
			current.State != store.SessionIdle || current.HeadEntryID != recovered.HeadEntryID || beforeHome != 2 || beforeChild != 1 {
			t.Fatalf("automatic activity: runs = %+v, err = %v, child = %+v, err = %v, model calls = %d/%d", runs, err, current, sessionErr, beforeHome, beforeChild)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	post("Resume the worker")
	waitFor(t, "the same child to finish its resumed run", func() bool {
		runs, err = st.Runs(ctx, child.ID)
		return err == nil && len(runs) == 2 && runs[0].State == store.RunAborted && runs[1].State == store.RunDone
	})
	after, err := st.Subagent(ctx, home.ID, "process-worker")
	if err != nil || after.ID != child.ID {
		t.Fatalf("resumed child = %+v, %v", after, err)
	}
	path, err = st.SessionPath(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err = session.Messages(path)
	if err != nil {
		t.Fatal(err)
	}
	resumed := false
	for _, m := range messages {
		if m.Role == provider.RoleUser && m.Content == "continue" {
			resumed = true
		}
	}
	if !resumed {
		t.Error("the resume message is missing from the child")
	}
	model.mu.Lock()
	childCalls, premature := model.child, model.premature
	model.mu.Unlock()
	if premature != 0 {
		t.Errorf("child model calls before the explicit resume: %d", premature)
	}
	if childCalls < 2 {
		t.Errorf("child model calls = %d, want at least 2", childCalls)
	}
	b, err := ex.ReadFile(ctx, "process-death-count", executor.ReadOpts{})
	if err != nil || string(b) != "ran\n" {
		t.Errorf("sandbox call count = %q, %v", b, err)
	}
}
