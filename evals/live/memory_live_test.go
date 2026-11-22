//go:build live

package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/erlidev/eika/evals/live"
	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

// memoryCore is a live Core with its own memory computer and the real guard's
// settings: screening required at the product's threshold. The memory
// computer, its repository volume and the database are this test's alone and
// are removed with it. A fake ntfy topic counts the pushes.
func memoryCore(t *testing.T, target *live.Target, guard string, pushes *atomic.Int64) *liveCore {
	t.Helper()
	if guard == "" {
		t.Fatal("the guard URL is required: memory writes are screened by the real guard")
	}
	sink := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		pushes.Add(1)
	}))
	t.Cleanup(sink.Close)
	// The memory computer needs the internal sandbox network. The harness
	// runs outside Docker and reaches it by container IP; both networks are
	// this test's own and are removed after its containers.
	open, internal := testNetwork(t, false), testNetwork(t, true)
	return newLiveCoreWith(t, target, guard, func(cfg *config.Config, st *store.Store, box *secret.Box) {
		// The memory section is counted with the endpoint's own tokenizer,
		// which Core knows from a probe's capabilities.
		providers, err := st.Providers(t.Context())
		if err != nil || len(providers) != 1 {
			t.Fatalf("read the live provider: %v (%d rows)", err, len(providers))
		}
		capabilities, err := json.Marshal(target.Probe.Capabilities)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecordProbe(t.Context(), providers[0].ID, store.ProbeResult{
			Backend: string(target.Probe.Backend), Slots: target.Probe.Slots, KV: string(target.Probe.KV), KVTokens: target.Probe.KVTokens,
			API: "chat", Capabilities: capabilities, At: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		sealed, err := box.Seal(sink.URL + "/topic")
		if err != nil {
			t.Fatal(err)
		}
		topic, _ := json.Marshal(sealed)
		threshold, _ := json.Marshal(cfg.ScreenThreshold)
		if err := st.SetSettings(t.Context(), map[string]json.RawMessage{
			"ntfy_topic": topic, "screen_required": json.RawMessage("true"), "screen_threshold": threshold,
		}); err != nil {
			t.Fatal(err)
		}
		cfg.PublicURL = "https://eika.example"
		cfg.SandboxNetwork, cfg.SandboxInternalNetwork = open, internal
		cfg.EgressListen, cfg.EgressProxyURL = "127.0.0.1:0", "http://eika:3128"
	})
}

// testNetwork creates a Docker network for one test and removes it after.
func testNetwork(t *testing.T, internal bool) string {
	t.Helper()
	name := fmt.Sprintf("eika-live-%d-%d", os.Getpid(), time.Now().UnixNano())
	args := []string{"network", "create"}
	if internal {
		args = append(args, "--internal")
	}
	if out, err := exec.CommandContext(t.Context(), "docker", append(args, name)...).CombinedOutput(); err != nil {
		t.Fatalf("create network: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "network", "rm", name).CombinedOutput(); err != nil {
			t.Errorf("remove network %s: %v: %s", name, err, out)
		}
	})
	return name
}

func memoryScenarios(t *testing.T, report *live.Report, target *live.Target, guard string) {
	t.Run("memory_save_recall", func(t *testing.T) {
		started := time.Now()
		var pushes atomic.Int64
		core := memoryCore(t, target, guard, &pushes)
		mem := &live.MemoryReport{}
		notes, err := core.memoryScenario(mem, &pushes)
		result := live.Result{Name: "memory_save_recall", Passed: err == nil, Seconds: time.Since(started).Seconds(), Calls: []live.Call{}, Notes: notes, Memory: mem}
		if err != nil {
			result.Error = err.Error()
			t.Error(err)
		}
		report.Add(result)
		for _, n := range notes {
			t.Log(n)
		}
	})
}

// toolsSince lists the tools home called after seq, in order.
func (c *liveCore) toolsSince(seq int64) ([]string, error) {
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Seq <= seq || e.Kind != store.KindAssistant {
			continue
		}
		var m struct {
			ToolCalls []struct {
				Name string `json:"name"`
			} `json:"tool_calls"`
		}
		if json.Unmarshal(e.Payload, &m) != nil {
			continue
		}
		for _, call := range m.ToolCalls {
			out = append(out, call.Name)
		}
	}
	return out, nil
}

func has(list []string, name string) bool {
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}

// memoryCounts reads the index rows by state.
func (c *liveCore) memoryCounts(m *live.MemoryReport) error {
	for state, dst := range map[string]*int{"active": &m.Active, "pending": &m.Pending, "archived": &m.Archived} {
		n, err := c.store.CountMemories(c.t.Context(), state)
		if err != nil {
			return err
		}
		*dst = n
	}
	return nil
}

// preambleMemory is the memory section of home's newest preamble.
func (c *liveCore) preambleMemory() (string, error) {
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		return "", err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == store.KindPreamble {
			return live.PreambleMemory(entries[i].Payload)
		}
	}
	return "", fmt.Errorf("home has no preamble")
}

func (c *liveCore) asks() (int, error) {
	tools, err := c.toolsSince(0)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, name := range tools {
		if name == "ask_user" {
			n++
		}
	}
	return n, nil
}

// compactWithPadding adds complete padding turns, so a compaction has
// history to fold, and compacts.
func (c *liveCore) compactWithPadding() error {
	padding := strings.Repeat("Ordinary catalogue notes about seed storage. ", max(c.window*4/10, 16000)/44)
	for _, text := range []string{"Catalogue notes: " + padding, "More catalogue notes: " + padding} {
		for _, message := range []provider.Message{provider.UserMessage(text), provider.AssistantMessage("Noted.", nil)} {
			e, err := session.MessageEntry(message)
			if err != nil {
				return err
			}
			if _, err := c.store.AppendEntry(c.t.Context(), c.home, e); err != nil {
				return fmt.Errorf("seed history: %w", err)
			}
		}
	}
	return c.compact("Summarise the conversation briefly.")
}

func (c *liveCore) memoryScenario(m *live.MemoryReport, pushes *atomic.Int64) (notes []string, result error) {
	ctx := c.t.Context()
	note := func(format string, args ...any) { notes = append(notes, fmt.Sprintf(format, args...)) }
	defer func() {
		if err := c.memoryCounts(m); err != nil && result == nil {
			result = err
		}
		var err error
		if m.Asks, err = c.asks(); err != nil && result == nil {
			result = err
		}
		m.Pushes = int(pushes.Load())
		note("memory active=%d pending=%d archived=%d in_preamble=%v indexed=%v task_done=%v asks=%d pushes=%d",
			m.Active, m.Pending, m.Archived, m.InPreamble, m.Indexed, m.TaskDone, m.Asks, m.Pushes)
	}()
	m.TaskDone = true
	turn := func(text string) ([]string, error) {
		seq, err := c.lastSeq()
		if err != nil {
			return nil, err
		}
		if err := c.turn(text); err != nil {
			m.TaskDone = false
			return nil, err
		}
		return c.toolsSince(seq)
	}

	// 1. Told in a user turn, it is saved in that turn.
	tools, err := turn("A preference to keep for good: I want every meeting summary you write for me as a bullet list, with a one-line action summary first. Please save that to your memory now.")
	if err != nil {
		return notes, err
	}
	if !has(tools, "memory_write") {
		return notes, fmt.Errorf("home did not call memory_write in the turn that stated the preference; tools: %v", tools)
	}
	list, err := c.store.ListMemories(ctx, memory.ListQuery{Limit: 50})
	if err != nil {
		return notes, err
	}
	if len(list) != 1 || list[0].State != "active" {
		return notes, fmt.Errorf("want one active memory after the save, got %+v", list)
	}
	name := list[0].Name
	note("saved %q (%s) state=%s source=%s", name, list[0].Type, list[0].State, list[0].Source)

	// 2. A later turn finds and reads it rather than guessing.
	tools, err = turn("How do I like my meeting summaries formatted? Look it up in your memory, do not guess.")
	if err != nil {
		return notes, err
	}
	if !has(tools, "memory_search") && !has(tools, "memory_read") {
		return notes, fmt.Errorf("home neither searched nor read memory; tools: %v", tools)
	}
	note("recall tools: %v", tools)

	// 3. After a compaction it is in the home preamble.
	if err := c.compactWithPadding(); err != nil {
		return notes, err
	}
	section, err := c.preambleMemory()
	if err != nil {
		return notes, err
	}
	m.InPreamble, m.Indexed = live.SectionNames(section, name)
	if !m.InPreamble {
		return notes, fmt.Errorf("the memory %q is not in the preamble after compaction:\n%s", name, section)
	}

	// 4. Restart Core, wipe the index and rebuild it from the files.
	c.stop()
	db, err := pgx.Connect(ctx, c.cfg.DatabaseURL)
	if err != nil {
		return notes, err
	}
	_, err = db.Exec(ctx, `DELETE FROM core.memories`)
	_ = db.Close(ctx)
	if err != nil {
		return notes, fmt.Errorf("clear the index: %w", err)
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rep, err := server.ReindexMemoryFrom(rctx, c.cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), true)
	if err != nil {
		return notes, fmt.Errorf("reindex: %w", err)
	}
	note("reindex: %+v", rep)
	if rep.Reindexed != 1 || len(rep.Invalid) != 0 {
		return notes, fmt.Errorf("reindex report %+v, want one row rebuilt", rep)
	}
	c.start()
	rec, err := c.store.GetMemory(ctx, name)
	if err != nil || rec.State != "active" {
		return notes, fmt.Errorf("after restart and reindex %q: state=%q: %w", name, rec.State, err)
	}
	if err := c.compactWithPadding(); err != nil {
		return notes, err
	}
	if section, err = c.preambleMemory(); err != nil {
		return notes, err
	}
	if named, _ := live.SectionNames(section, name); !named {
		return notes, fmt.Errorf("the memory %q is missing from the preamble after restart and reindex:\n%s", name, section)
	}

	// 5. Archived on request.
	tools, err = turn("I have changed my mind about the meeting summary format. Forget that preference.")
	if err != nil {
		return notes, err
	}
	if !has(tools, "memory_forget") {
		return notes, fmt.Errorf("home did not call memory_forget; tools: %v", tools)
	}
	rec, err = c.store.GetMemory(ctx, name)
	if err != nil || rec.State != "archived" {
		return notes, fmt.Errorf("after the request %q: state=%q: %w", name, rec.State, err)
	}
	if err := c.compactWithPadding(); err != nil {
		return notes, err
	}
	if section, err = c.preambleMemory(); err != nil {
		return notes, err
	}
	if strings.Contains(section, name) {
		return notes, fmt.Errorf("the archived memory %q is still in the preamble:\n%s", name, section)
	}
	return notes, nil
}
