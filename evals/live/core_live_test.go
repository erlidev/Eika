//go:build live

package live_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/injection"
	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

const coreToken = "live-test-token"

func TestMain(m *testing.M) {
	if os.Getenv("EIKA_LIVE_CORE_CONFIG") != "" {
		os.Exit(m.Run())
	}
	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(m.Run())
	}
	env, err := live.LoadEnv(live.EnvFile(home), os.LookupEnv)
	if err != nil || env[live.EndpointVar] == "" {
		os.Exit(m.Run())
	}
	os.Exit(storetest.Main(m))
}

func TestLiveCoreFixture(t *testing.T) {
	path := os.Getenv("EIKA_LIVE_CORE_CONFIG")
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := server.Run(context.Background(), cfg, slog.New(slog.NewJSONHandler(os.Stderr, nil)), server.Options{LiveTest: true}); err != nil {
		t.Fatal(err)
	}
}

type liveCore struct {
	t               *testing.T
	cfg             config.Config
	store           *store.Store
	home            string
	window          int
	configPath      string
	dir             string
	process         *exec.Cmd
	address         string
	queueNotes      []string
	restartNotes    []string
	prefixNotes     []string
	compactionNotes []string
	source          counter.Source
}

func newLiveCore(t *testing.T, target *live.Target, guard string) *liveCore {
	return newLiveCoreWith(t, target, guard, nil)
}

func newLiveCoreWith(t *testing.T, target *live.Target, guard string, configure func(*config.Config, *store.Store, *secret.Box)) *liveCore {
	t.Helper()
	ctx := t.Context()
	if out, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("Docker is required for the live Core: %v: %s", err, out)
	}
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
	keyPath := filepath.Join(dir, "secret.key")
	box, err := secret.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := box.Seal(target.Key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProvider(ctx, store.Provider{Name: "live", Kind: "openai", BaseURL: target.BaseURL, APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	slots := target.Server.Flags.Slots
	if slots < 2 {
		slots = 2
	}
	if _, err := st.OverrideProvider(ctx, p.ID, store.ProviderOverride{Slots: &slots}); err != nil {
		t.Fatal(err)
	}
	window := target.Server.Flags.Context
	if window < 8192 {
		window = 32768
	}
	if _, err := st.CreateModel(ctx, store.Model{ProviderID: p.ID, Name: "live", Model: target.Model, ContextWindow: window, MaxOutput: 2048, ReasoningEfforts: []string{provider.EffortNone}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(ctx, map[string]json.RawMessage{"model_classes": json.RawMessage(`{"main":"live","fast":"live"}`)}); err != nil {
		t.Fatal(err)
	}
	home, err := st.EnsureHomeSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Listen, cfg.DatabaseURL, cfg.SecretKeyFile, cfg.AuthToken = "127.0.0.1:0", db, keyPath, coreToken
	cfg.EikadBinary, cfg.SandboxNetwork, cfg.SandboxInternalNetwork = eikad, "", ""
	cfg.GuardURL = guard
	cfg.FetchFixtureHosts = []string{injection.Host}
	if image := os.Getenv("EIKA_TEST_SANDBOX_IMAGE"); image != "" {
		cfg.SandboxImage = image
	}
	if configure != nil {
		configure(&cfg, st, box)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "core.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	c := &liveCore{t: t, cfg: cfg, store: st, home: home.ID, window: window, source: target.Source, configPath: path, dir: dir}
	t.Cleanup(func() {
		c.stop()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// The memory computer belongs to this Core's own database and is
		// removed with it; the user's real stack has a different id.
		if id, err := st.MemoryComputerID(ctx); err == nil {
			for _, args := range [][]string{{"rm", "-f", computer.ContainerName(id)}, {"volume", "rm", "-f", computer.VolumeName(id)}} {
				if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
					t.Errorf("remove memory resource: %v: %s", err, out)
				}
			}
		}
		if id, err := st.SkillsComputerID(ctx); err == nil {
			for _, args := range [][]string{{"rm", "-f", computer.ContainerName(id)}, {"volume", "rm", "-f", computer.VolumeName(id)}} {
				if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
					t.Errorf("remove skills resource: %v: %s", err, out)
				}
			}
		}
		row, err := st.HomeComputer(ctx)
		if err != nil {
			return
		}
		for _, args := range [][]string{{"rm", "-f", computer.ContainerName(row.ID)}, {"volume", "rm", "-f", computer.VolumeName(row.ID)}} {
			if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
				t.Errorf("remove home resource: %v: %s", err, out)
			}
		}
	})
	c.start()
	return c
}

func (c *liveCore) start() {
	c.t.Helper()
	log, err := os.CreateTemp(c.dir, "core-log-")
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		_ = log.Close()
		if c.t.Failed() {
			if b, err := os.ReadFile(log.Name()); err == nil {
				c.t.Logf("Core log:\n%s", b)
			}
		}
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestLiveCoreFixture$")
	cmd.Env = append(os.Environ(), "EIKA_LIVE_CORE_CONFIG="+c.configPath, "EIKA_LIVE_PUSH_SINK=1")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.process = cmd
	err = c.wait(90*time.Second, func() bool {
		raw, err := os.ReadFile(log.Name())
		if err != nil {
			return false
		}
		scan := bufio.NewScanner(bytes.NewReader(raw))
		for scan.Scan() {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"addr"`
			}
			if json.Unmarshal(scan.Bytes(), &event) == nil && event.Message == "http server listening" {
				c.address = event.Address
			}
		}
		if c.address == "" {
			return false
		}
		client := &http.Client{Timeout: time.Second}
		resp, err := client.Get("http://" + c.address + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	if err != nil {
		c.t.Fatalf("start Core: %v", err)
	}
}
func (c *liveCore) stop() {
	if c.process != nil {
		_ = c.process.Process.Kill()
		_ = c.process.Wait()
		c.process = nil
		c.address = ""
	}
}
func (c *liveCore) wait(d time.Duration, ready func() bool) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ready() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s", d)
}
func (c *liveCore) postRun(path string, body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, "http://"+c.address+path, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+coreToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("post %s: %s: %s", path, resp.Status, b)
	}
	var run struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return "", fmt.Errorf("decode run: %w", err)
	}
	if run.ID == "" {
		return "", fmt.Errorf("post %s returned no run id", path)
	}
	return run.ID, nil
}
func (c *liveCore) post(path string, body any) error {
	_, err := c.postRun(path, body)
	return err
}
func (c *liveCore) messageRun(text string) (string, error) {
	return c.postRun("/api/sessions/"+c.home+"/messages", map[string]string{"text": text})
}
func (c *liveCore) message(text string) error {
	_, err := c.messageRun(text)
	return err
}

// waitRun waits for both the durable result and the session reservation to end.
func (c *liveCore) waitRun(id, sessionID string) error {
	ctx := c.t.Context()
	deadline := time.Now().Add(5 * time.Minute)
	client := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		run, err := c.store.Run(ctx, id)
		if err != nil {
			return fmt.Errorf("read run: %w", err)
		}
		if run.State != store.RunRunning && run.State != store.RunDone {
			return fmt.Errorf("run %s ended in %s: %s", id, run.State, run.Error)
		}
		if run.State == store.RunDone {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+c.address+"/api/sessions/"+sessionID+"/run", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+coreToken)
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			var state struct {
				Active bool `json:"active"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&state)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("read run state: %s", resp.Status)
			}
			if decodeErr != nil {
				return fmt.Errorf("decode run state: %w", decodeErr)
			}
			if !state.Active {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("run %s did not finish", id)
}

// answerForRun links a stored run to its turn through the model call's entry.
func (c *liveCore) answerForRun(runID, text string) (bool, error) {
	ctx := c.t.Context()
	entries, err := c.store.Entries(ctx, c.home)
	if err != nil {
		return false, err
	}
	calls, err := c.store.ModelCalls(ctx, c.home)
	if err != nil {
		return false, err
	}
	callEntries := make(map[string]bool)
	for _, call := range calls {
		if call.RunID == runID {
			callEntries[call.EntryID] = true
		}
	}
	var turnID string
	for _, e := range entries {
		if e.Kind != store.KindUser {
			continue
		}
		m, _, err := session.Message(e)
		if err != nil {
			return false, err
		}
		if m.Content == text {
			turnID = e.RunID
		}
	}
	if turnID == "" {
		return false, nil
	}
	linked, answer := false, false
	for _, e := range entries {
		if e.RunID != turnID {
			continue
		}
		if callEntries[e.ID] {
			linked = true
		}
		if e.Kind == store.KindAssistant {
			m, _, err := session.Message(e)
			if err != nil {
				return false, err
			}
			if strings.TrimSpace(m.Content) != "" {
				answer = true
			}
		}
	}
	return linked && answer, nil
}

func (c *liveCore) hasTool(id, name string) bool {
	path, err := c.store.SessionPath(c.t.Context(), id)
	if err != nil {
		return false
	}
	for _, e := range path {
		if e.Kind == store.KindAssistant && strings.Contains(string(e.Payload), `"name":"`+name+`"`) {
			return true
		}
	}
	return false
}

func (c *liveCore) exitScenario() error {
	ctx := c.t.Context()
	firstRun, err := c.messageRun("Look into how to organise a small community seed library and keep notes in a project for it. Create a project and start a worker named seed-research to write a short notes file in it. Ask the worker to run sleep 180 with a bash timeout of 240 seconds before reporting, so I can talk to you while it works.")
	if err != nil {
		return err
	}
	var child store.Session
	if err := c.wait(3*time.Minute, func() bool {
		var err error
		child, err = c.store.Subagent(ctx, c.home, "seed-research")
		return err == nil && child.State == store.SessionRunning
	}); err != nil {
		return fmt.Errorf("worker did not start: %w", err)
	}
	projects, err := c.store.Projects(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, p := range projects {
		if p.ID == child.ProjectID {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("worker has no created project")
	}
	if err := c.waitRun(firstRun, c.home); err != nil {
		return fmt.Errorf("first home run: %w", err)
	}
	current, err := c.store.Session(ctx, child.ID)
	if err != nil {
		return err
	}
	if current.State != store.SessionRunning {
		return fmt.Errorf("worker stopped before the second home message")
	}
	const secondMessage = "While the worker is running, tell me one short idea for organising seeds."
	secondRun, err := c.messageRun(secondMessage)
	if err != nil {
		return err
	}
	if secondRun == firstRun {
		return fmt.Errorf("second message joined the first run")
	}
	if err := c.wait(3*time.Minute, func() bool {
		answered, err := c.answerForRun(secondRun, secondMessage)
		if err != nil || !answered {
			return false
		}
		child, err := c.store.Session(ctx, child.ID)
		return err == nil && child.State == store.SessionRunning
	}); err != nil {
		return fmt.Errorf("second home run did not answer while worker ran: %w", err)
	}

	if err := c.waitRun(secondRun, c.home); err != nil {
		return fmt.Errorf("second home run: %w", err)
	}

	if err := c.wait(5*time.Minute, func() bool {
		s, err := c.store.Session(ctx, child.ID)
		if err != nil || s.State != store.SessionIdle || s.ReportNoticeID == "" {
			return false
		}
		entries, err := c.store.Entries(ctx, c.home)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.Kind == store.KindNotice && e.ID == s.ReportNoticeID {
				return true
			}
		}
		return false
	}); err != nil {
		return fmt.Errorf("worker report notice: %w", err)
	}
	before, err := c.store.Runs(ctx, child.ID)
	if err != nil {
		return err
	}
	if err := c.message("Follow up on the seed library. Resume seed-research by name and ask it to add one more practical suggestion to its notes and report."); err != nil {
		return err
	}
	if err := c.wait(5*time.Minute, func() bool {
		s, err := c.store.Subagent(ctx, c.home, "seed-research")
		if err != nil || s.ID != child.ID || s.State != store.SessionIdle {
			return false
		}
		runs, err := c.store.Runs(ctx, child.ID)
		return err == nil && len(runs) > len(before)
	}); err != nil {
		return fmt.Errorf("resume same worker: %w", err)
	}
	if !c.hasTool(c.home, "subagent_message") {
		return fmt.Errorf("home did not call subagent_message")
	}
	return nil
}
