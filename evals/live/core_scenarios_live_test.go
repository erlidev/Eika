//go:build live

package live_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

func (c *liveCore) homeExecutor() (executor.Executor, error) {
	row, err := c.store.HomeComputer(c.t.Context())
	if err != nil {
		return nil, err
	}
	host, err := computer.NewHost(computer.Options{DockerSocket: c.cfg.DockerSocket, Image: c.cfg.SandboxImage, EikadBinary: c.cfg.EikadBinary}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return nil, err
	}
	c.t.Cleanup(func() { _ = host.Close() })
	cx, err := host.Inspect(c.t.Context(), row.ID)
	if err != nil {
		return nil, err
	}
	return host.Executor(cx)
}

func (c *liveCore) restartScenario() (result error) {
	ctx := c.t.Context()
	const name = "recovery-worker"
	const marker = "live-recovery-marker"
	homeSeq, err := c.lastSeq()
	if err != nil {
		return err
	}
	var childID string
	defer func() {
		if result != nil {
			c.restartSnapshot("final", childID, homeSeq)
		}
	}()
	firstRun, err := c.messageRun("Start a worker named recovery-worker. Ask it to run exactly this bash command as its first action: echo ran >> /home/eika/live-recovery-marker; while [ ! -f /home/eika/live-recovery-release ]; do sleep 1; done, with bash timeout 600 seconds. It must report after the command. This is a recovery test.")
	if err != nil {
		return err
	}
	var child store.Session
	var callID string
	var command string
	var ex executor.Executor
	if err := c.wait(5*time.Minute, func() bool {
		var err error
		child, err = c.store.Subagent(ctx, c.home, name)
		if err == nil {
			childID = child.ID
		}
		if err != nil || child.State != store.SessionRunning {
			return false
		}
		path, err := c.store.SessionPath(ctx, child.ID)
		if err != nil {
			return false
		}
		msgs, err := session.Messages(path)
		if err != nil {
			return false
		}
		for _, m := range msgs {
			for _, call := range m.ToolCalls {
				if call.Name == "bash" && strings.Contains(string(call.Arguments), marker) {
					callID = call.ID
					var args struct {
						Command string `json:"command"`
						Timeout int    `json:"timeout"`
					}
					if json.Unmarshal([]byte(call.Arguments), &args) == nil && args.Timeout >= 600 {
						command = args.Command
					}
				}
			}
		}
		if callID == "" {
			return false
		}
		for _, m := range msgs {
			if m.Role == provider.RoleTool && m.ToolCallID == callID {
				return false
			}
		}
		if ex == nil {
			ex, err = c.homeExecutor()
			if err != nil {
				return false
			}
		}
		b, err := ex.ReadFile(ctx, marker, executor.ReadOpts{})
		return err == nil && string(b) == "ran\n"
	}); err != nil {
		return fmt.Errorf("started non-read call: %w", err)
	}
	defer func() { _ = ex.WriteFile(context.WithoutCancel(ctx), "live-recovery-release", []byte("release")) }()
	if !strings.Contains(command, "while [ ! -f /home/eika/live-recovery-release ]; do sleep 1; done") {
		return fmt.Errorf("worker did not use the gated command with a 600 second timeout")
	}
	// As in the process-death test, let home finish its delegation before
	// killing Core. Otherwise recovery also interrupts home's tool call.
	if err := c.waitRun(firstRun, c.home); err != nil {
		return fmt.Errorf("finish delegation before restart: %w", err)
	}
	current, err := c.store.Session(ctx, child.ID)
	if err != nil {
		return err
	}
	pathBefore, err := c.store.SessionPath(ctx, child.ID)
	if err != nil {
		return err
	}
	messagesBefore, err := session.Messages(pathBefore)
	if err != nil {
		return err
	}
	if current.State != store.SessionRunning {
		return fmt.Errorf("worker was not running before restart: %s", current.State)
	}
	for _, m := range messagesBefore {
		if m.Role == provider.RoleTool && m.ToolCallID == callID {
			return fmt.Errorf("sandbox call finished before restart")
		}
	}
	c.stop()
	c.start()
	if err := ex.WriteFile(ctx, "live-recovery-release", []byte("release")); err != nil {
		return fmt.Errorf("release sandbox call: %w", err)
	}
	recovered, err := c.store.Session(ctx, child.ID)
	if err != nil {
		return err
	}
	if recovered.State != store.SessionIdle {
		return fmt.Errorf("interrupted child state is %s", recovered.State)
	}
	path, err := c.store.SessionPath(ctx, child.ID)
	if err != nil {
		return err
	}
	msgs, err := session.Messages(path)
	if err != nil {
		return err
	}
	results := 0
	for _, m := range msgs {
		if m.Role == provider.RoleTool && m.ToolCallID == callID {
			results++
			if !m.IsError || !strings.Contains(m.Content, "outcome is unknown") {
				return fmt.Errorf("interrupted call did not have an unknown outcome")
			}
		}
	}
	if results != 1 {
		return fmt.Errorf("interrupted call has %d results", results)
	}
	entries, err := c.store.Entries(ctx, c.home)
	if err != nil {
		return err
	}
	notice := false
	for _, e := range entries {
		if e.Kind == store.KindNotice {
			text, err := session.TextOf(e)
			if err == nil && strings.Contains(text, name) && strings.Contains(text, "interrupted") {
				notice = true
			}
		}
	}
	if !notice {
		return fmt.Errorf("interruption notice missing")
	}
	runs, err := c.store.Runs(ctx, child.ID)
	if err != nil {
		return err
	}
	if len(runs) == 0 || runs[len(runs)-1].State != store.RunAborted {
		return fmt.Errorf("interrupted run was not aborted")
	}
	// Give startup scheduling time to expose an unintended retry.
	time.Sleep(6 * time.Second)
	b, err := ex.ReadFile(ctx, marker, executor.ReadOpts{})
	if err != nil {
		return fmt.Errorf("read marker after restart: %w", err)
	}
	if string(b) != "ran\n" {
		return fmt.Errorf("marker after restart = %q", b)
	}
	after, err := c.store.Runs(ctx, child.ID)
	if err != nil {
		return fmt.Errorf("read child runs: %w", err)
	}
	if len(after) != len(runs) {
		return fmt.Errorf("child resumed without a message")
	}
	// A live model may answer without calling the tool. Retry once only when
	// the completed home run did not start the child; never duplicate a run.
	resumed := false
	for attempt := 1; attempt <= 2; attempt++ {
		resumeRun, err := c.messageRun("Resume recovery-worker by name using subagent_message. Do not repeat the interrupted command; ask it to report that recovery was checked.")
		if err != nil {
			return err
		}
		if err := c.waitRun(resumeRun, c.home); err != nil {
			return fmt.Errorf("resume home run: %w", err)
		}
		current, err := c.store.Runs(ctx, child.ID)
		if err != nil {
			return fmt.Errorf("read resumed child runs: %w", err)
		}
		if len(current) > len(runs) {
			resumed = true
			break
		}
	}
	if !resumed {
		return fmt.Errorf("home did not start another run for child %s after two resume requests", child.ID)
	}

	if err := c.wait(5*time.Minute, func() bool {
		s, err := c.store.Subagent(ctx, c.home, name)
		if err != nil || s.ID != child.ID || s.State != store.SessionIdle {
			return false
		}
		r, err := c.store.Runs(ctx, child.ID)
		return err == nil && len(r) > len(runs) && r[len(r)-1].State == store.RunDone
	}); err != nil {
		return fmt.Errorf("resume interrupted worker: %w", err)
	}
	b, err = ex.ReadFile(ctx, marker, executor.ReadOpts{})
	if err != nil {
		return fmt.Errorf("read marker after resume: %w", err)
	}
	if string(b) != "ran\n" {
		return fmt.Errorf("marker after resume = %q", b)
	}
	return nil
}

func (c *liveCore) lastSeq() (int64, error) {
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	return entries[len(entries)-1].Seq, nil
}

func (c *liveCore) turn(text string) error {
	runID, err := c.messageRun(text)
	if err != nil {
		return err
	}
	if err := c.waitRun(runID, c.home); err != nil {
		return err
	}
	answered, err := c.answerForRun(runID, text)
	if err != nil {
		return err
	}
	if !answered {
		return fmt.Errorf("run %s produced no answer", runID)
	}
	return nil
}

func (c *liveCore) compact(instructions string) error {
	seq, err := c.lastSeq()
	if err != nil {
		return err
	}
	// A worker's report can start a home run after the scenario's last wait;
	// compaction is refused while a run is active, so retry until it ends.
	var runID string
	deadline := time.Now().Add(5 * time.Minute)
	for {
		runID, err = c.postRun("/api/sessions/"+c.home+"/compact", map[string]string{"instructions": instructions})
		if err == nil || !strings.Contains(err.Error(), "already has a run in progress") || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return err
	}
	if err := c.waitRun(runID, c.home); err != nil {
		return err
	}
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Seq > seq && e.Kind == store.KindCompaction {
			return nil
		}
	}
	return fmt.Errorf("run %s produced no compaction", runID)
}

func (c *liveCore) compactScenario(window int) (result error) {
	const fact = "K27"
	const question = "Which shelf was lot 23 on? Please look it up in our history."
	// Insert complete turns so setup does not depend on model output. The
	// compactor and recall turn still use the real server and model.
	padding := strings.Repeat("Ordinary catalogue notes about seed storage. ", max(window*4/10, 16000)/44)
	for _, text := range []string{"Here is my seed inventory:\n" + seedInventory(), "Catalogue notes: " + padding, "More catalogue notes: " + padding} {
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
	if err := c.compact("Summarize the inventory as a whole. Do not include individual lot numbers or shelf values."); err != nil {
		return fmt.Errorf("compaction: %w", err)
	}

	path, err := c.store.SessionPath(c.t.Context(), c.home)
	if err != nil {
		return err
	}
	var summary string
	for _, e := range path {
		if e.Kind == store.KindCompaction {
			compact, err := session.CompactionOf(e)
			if err != nil {
				return err
			}
			summary = compact.Summary
		}
	}
	if summary == "" {
		return fmt.Errorf("compaction produced no summary")
	}

	seq, err := c.lastSeq()
	if err != nil {
		return err
	}
	// Record the turn even when a later assertion fails, including every tool.
	defer func() {
		if result != nil {
			c.compactionNotes = append(c.compactionNotes, fmt.Sprintf("summary (truncated): %q", clipRecall(summary)))
			c.recallSnapshot(seq, question, fact)
		}
	}()
	runID, err := c.messageRun(question)
	if err != nil {
		return err
	}
	if err := c.waitRun(runID, c.home); err != nil {
		return fmt.Errorf("recall turn: %w", err)
	}
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		return err
	}
	user, err := recallUser(entries, seq, question)
	if err != nil {
		return err
	}
	turnEntries := make(map[string]bool)
	for _, e := range entries {
		if e.Seq >= user.Seq && e.RunID == user.RunID {
			turnEntries[e.ID] = true
		}
	}
	calls, err := c.store.ModelCalls(c.t.Context(), c.home)
	if err != nil {
		return err
	}
	var first *store.ModelCall
	for i := range calls {
		if calls[i].RunID == runID && turnEntries[calls[i].EntryID] {
			first = &calls[i]
			break
		}
	}
	if first == nil {
		return fmt.Errorf("recall run %s recorded no model call", runID)
	}
	request, err := c.recordedRequest(c.home, *first)
	if err != nil {
		return fmt.Errorf("rebuild recall request: %w", err)
	}
	if c.source == nil {
		return fmt.Errorf("no renderer for recall request")
	}
	rendered, err := c.source.Prompt(c.t.Context(), request)
	if err != nil {
		return fmt.Errorf("render recall request: %w", err)
	}
	if err := checkRecallPrompt(rendered, summary, fact); err != nil {
		return err
	}

	tools, answered, err := recallOutcome(entries, seq, question, fact)
	if err != nil {
		return err
	}
	if !hasHistorySearch(tools) {
		return fmt.Errorf("recalled without history_search: tools=%v answer contains fact=%t", tools, answered)
	}
	if !answered {
		return fmt.Errorf("history_search was called but answer did not contain the fact")
	}
	return nil
}

// containsFact ignores case and punctuation while keeping word boundaries.
func containsFact(text, fact string) bool {
	words := func(s string) []string {
		var b strings.Builder
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				b.WriteRune(unicode.ToLower(r))
			} else {
				b.WriteByte(' ')
			}
		}
		return strings.Fields(b.String())
	}
	want := strings.Join(words(fact), "")
	if want == "" {
		return false
	}
	parts := words(text)
	for i := range parts {
		joined := ""
		for j := i; j < len(parts); j++ {
			joined += parts[j]
			if joined == want {
				return true
			}
			if len(joined) >= len(want) || !strings.HasPrefix(want, joined) {
				break
			}
		}
	}
	return false
}

// seedInventory makes the shelf for lot 23 unique among the ordinary lots.
func seedInventory() string {
	varieties := []string{"Blue Lake", "Cherokee Trail of Tears", "Calypso", "Dragon Tongue", "Jacob's Cattle", "Black Turtle", "Scarlet Runner", "Hidatsa Shield"}
	var rows strings.Builder
	for i := 1; i <= 50; i++ {
		shelf := fmt.Sprintf("%c%d", 'A'+rune((i-1)%8), (i*7)%31+1)
		if i == 23 {
			shelf = "K27"
		}
		fmt.Fprintf(&rows, "Lot %d: %s, %d g, shelf %s, packed 2025-%02d-%02d\n", i, varieties[(i-1)%len(varieties)], 50+(i*11)%200, shelf, 1+(i%12), 1+(i*3)%28)
	}
	return rows.String()
}

func checkRecallPrompt(rendered, summary, fact string) error {
	if !containsFact(rendered, fact) {
		return nil
	}
	if containsFact(summary, fact) {
		return fmt.Errorf("fact leaked into the compaction summary and recall request")
	}
	return fmt.Errorf("fact leaked into the recall request outside the compaction summary (preamble, notice, retained message, reasoning or tool arguments)")
}

func hasHistorySearch(tools []string) bool {
	for _, name := range tools {
		if name == "history_search" {
			return true
		}
	}
	return false
}

func recallUser(entries []store.Entry, seq int64, question string) (store.Entry, error) {
	for _, e := range entries {
		if e.Seq <= seq || e.Kind != store.KindUser {
			continue
		}
		m, _, err := session.Message(e)
		if err != nil {
			return store.Entry{}, err
		}
		if m.Content == question && e.RunID != "" {
			return e, nil
		}
	}
	return store.Entry{}, fmt.Errorf("recall message is not stored")
}

func recallOutcome(entries []store.Entry, seq int64, question, fact string) ([]string, bool, error) {
	user, err := recallUser(entries, seq, question)
	if err != nil {
		return nil, false, err
	}
	var tools []string
	answered := false
	for _, e := range entries {
		if e.Seq <= user.Seq || e.RunID != user.RunID {
			continue
		}
		m, ok, err := session.Message(e)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		for _, call := range m.ToolCalls {
			tools = append(tools, call.Name)
		}
		if m.Role == provider.RoleAssistant && containsFact(m.Content, fact) {
			answered = true
		}
	}
	return tools, answered, nil
}

func (c *liveCore) recallSnapshot(seq int64, question, fact string) {
	entries, err := c.store.Entries(c.t.Context(), c.home)
	if err != nil {
		c.compactionNotes = append(c.compactionNotes, fmt.Sprintf("read recall entries: %v", err))
		return
	}
	user, err := recallUser(entries, seq, question)
	if err != nil {
		c.compactionNotes = append(c.compactionNotes, err.Error())
		return
	}

	count := 0
	var answer string
	for _, e := range entries {
		if e.Seq <= user.Seq || e.RunID != user.RunID {
			continue
		}
		m, ok, err := session.Message(e)
		if err != nil || !ok {
			continue
		}
		for _, call := range m.ToolCalls {
			count++
			c.compactionNotes = append(c.compactionNotes, fmt.Sprintf("recall tool turn=%s id=%s name=%s arguments=%s", user.RunID, call.ID, call.Name, clipRecall(string(call.Arguments))))
		}
		if m.Role == provider.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			answer = m.Content
		}
		if m.Role == provider.RoleTool {
			c.compactionNotes = append(c.compactionNotes, fmt.Sprintf("recall tool result id=%s error=%t contains_fact=%t text=%q", m.ToolCallID, m.IsError, containsFact(m.Content, fact), clipRecall(m.Content)))
		}
	}
	if count == 0 {
		c.compactionNotes = append(c.compactionNotes, "recall turn made no tool calls")
	}
	c.compactionNotes = append(c.compactionNotes, fmt.Sprintf("recall final assistant answer=%q", clipRecall(answer)))
}

func clipRecall(text string) string {
	const limit = 1200
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}

func (c *liveCore) recordedRequest(sessionID string, record store.ModelCall) (provider.Request, error) {
	ctx := c.t.Context()
	full, err := c.store.ModelCall(ctx, sessionID, record.ID)
	if err != nil {
		return provider.Request{}, err
	}
	var rendered agent.Context
	if err := json.Unmarshal(full.Sections, &rendered.Sections); err != nil {
		return provider.Request{}, err
	}
	if err := json.Unmarshal(full.Tools, &rendered.Tools); err != nil {
		return provider.Request{}, err
	}
	if err := json.Unmarshal(full.Parameters, &rendered.Parameters); err != nil {
		return provider.Request{}, err
	}
	if full.EntryID != "" {
		path, err := c.store.EntryPath(ctx, sessionID, full.EntryID)
		if err != nil {
			return provider.Request{}, err
		}
		messages, err := session.Messages(path)
		if err != nil {
			return provider.Request{}, err
		}
		rendered = rendered.WithMessages(messages)
	}
	return rendered.Request(), nil
}

func (c *liveCore) checkCached(id string, before int) (int, error) {
	ctx := c.t.Context()
	calls, err := c.store.ModelCalls(ctx, id)
	if err != nil {
		return 0, err
	}
	if c.source == nil {
		return 0, fmt.Errorf("prefix check needs the bench tokenizer")
	}
	if len(calls) < before+2 {
		return 0, fmt.Errorf("session %s has %d new model calls, need two", id, len(calls)-before)
	}
	for i := before + 1; i < len(calls); i++ {
		previous, current := calls[i-1], calls[i]
		if previous.PromptTokens == nil || current.CachedTokens == nil {
			return 0, fmt.Errorf("cache measurements missing for session %s", id)
		}
		first, err := c.recordedRequest(id, previous)
		if err != nil {
			return 0, fmt.Errorf("rebuild previous request: %w", err)
		}
		second, err := c.recordedRequest(id, current)
		if err != nil {
			return 0, fmt.Errorf("rebuild next request: %w", err)
		}
		firstPrompt, err := c.source.Prompt(ctx, first)
		if err != nil {
			return 0, fmt.Errorf("render previous request: %w", err)
		}
		secondPrompt, err := c.source.Prompt(ctx, second)
		if err != nil {
			return 0, fmt.Errorf("render next request: %w", err)
		}
		stable, tail, err := stablePromptPrefix(ctx, c.source, firstPrompt, secondPrompt)
		if err != nil {
			return 0, fmt.Errorf("session %s calls %s -> %s: %w", id, previous.ID, current.ID, err)
		}
		prediction := "unreported"
		if previous.PredictedPromptTokens != nil {
			prediction = fmt.Sprintf("%d (%s)", *previous.PredictedPromptTokens, previous.Prediction)
		}
		c.prefixNotes = append(c.prefixNotes, fmt.Sprintf("session %s calls %s -> %s: previous prompt=%d, Core prediction=%s, stable prefix=%d bytes, generation header=%d tokens, cached=%d, request tail=%s", id, previous.ID, current.ID, *previous.PromptTokens, prediction, stable, tail, *current.CachedTokens, requestTail(first)))
		// Compare server measurements with each other. Only the measured
		// assistant-generation header may differ when the reply is replayed.
		if err := checkCachedPrefix(*previous.PromptTokens, *current.CachedTokens, tail); err != nil {
			return 0, fmt.Errorf("session %s: %w", id, err)
		}
	}
	return len(calls) - before, nil
}

func checkCachedPrefix(previous, cached, header int) error {
	if previous <= 0 || header <= 0 || header > previous || cached < 0 {
		return fmt.Errorf("invalid prompt=%d cached=%d generation header=%d", previous, cached, header)
	}
	if cached < previous-header {
		return fmt.Errorf("cached %d tokens; previous prompt=%d, generation header=%d, need at least %d", cached, previous, header, previous-header)
	}
	return nil
}

func requestTail(req provider.Request) string {
	messages := req.Messages[max(0, len(req.Messages)-4):]
	var parts []string
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("%s(content=%t,reasoning=%t,reasoning_items=%d,tools=%d,context=%t,summary=%t)", m.Role, m.Content != "", m.Reasoning != "", len(m.ReasoningItems), len(m.ToolCalls), m.Context != "", m.Summary))
	}
	return strings.Join(parts, ",")
}

// paddingNeeded leaves room for two ordinary turns when history is short.
func paddingNeeded(keep, present int) int {
	missing := keep + keep/10 + 128 - present
	if missing <= 0 {
		return 0
	}
	return min(missing, keep*3/5+1)
}

func (c *liveCore) prefixScenario() error {
	ctx := c.t.Context()
	calls, err := c.store.ModelCalls(ctx, c.home)
	if err != nil {
		return err
	}
	start := len(calls)
	for _, prompt := range []string{"For a seed library, name one storage concern in a short sentence.", "Name a different storage concern in a short sentence."} {
		if err := c.turn(prompt); err != nil {
			return err
		}
	}
	// Reuse the retained history from the earlier scenario; add only the
	// estimated tokens needed to put an old turn beyond the kept share.
	keep := int(agent.DefaultKeepRecentShare * float64(c.window))
	for i := 0; i < 2; i++ {
		path, err := c.store.SessionPath(ctx, c.home)
		if err != nil {
			return err
		}
		messages, err := session.Messages(path)
		if err != nil {
			return err
		}
		context := (agent.Context{}).WithMessages(messages)
		total := 0
		for j, m := range context.Messages {
			if !m.Summary {
				total += context.MessageSizes[j]
			}
		}
		target := paddingNeeded(keep, total)
		if target == 0 {
			break
		}
		const passage = "Seed catalogue padding for the compaction check. "
		filler := strings.Repeat(passage, (target*4+len(passage)-1)/len(passage))
		if err := c.turn(fmt.Sprintf("Compaction padding %d; reply briefly. %s", i+1, filler)); err != nil {
			return fmt.Errorf("prefix padding: %w", err)
		}
	}
	if _, err := c.checkCached(c.home, start); err != nil {
		return fmt.Errorf("home prefix: %w", err)
	}
	if err := c.compact(""); err != nil {
		return fmt.Errorf("prefix compaction: %w", err)
	}

	calls, err = c.store.ModelCalls(ctx, c.home)
	if err != nil {
		return err
	}
	start = len(calls)
	for _, prompt := range []string{"Name one seed label field in a short sentence.", "Name a second seed label field in a short sentence."} {
		if err := c.turn(prompt); err != nil {
			return err
		}
	}
	if _, err := c.checkCached(c.home, start); err != nil {
		return fmt.Errorf("home prefix after compaction: %w", err)
	}
	child, err := c.store.Subagent(ctx, c.home, "seed-research")
	if err != nil {
		return err
	}
	calls, err = c.store.ModelCalls(ctx, child.ID)
	if err != nil {
		return err
	}
	start = len(calls)
	for _, prompt := range []string{"Resume seed-research by name. Ask it to report one concise seed storage suggestion.", "Resume seed-research again by name. Ask it to report a different concise seed storage suggestion."} {
		runs, err := c.store.Runs(ctx, child.ID)
		if err != nil {
			return err
		}
		if err := c.turn(prompt); err != nil {
			return err
		}
		if err := c.wait(5*time.Minute, func() bool {
			s, err := c.store.Session(ctx, child.ID)
			if err != nil || s.State != store.SessionIdle {
				return false
			}
			r, err := c.store.Runs(ctx, child.ID)
			return err == nil && len(r) > len(runs) && r[len(r)-1].State == store.RunDone
		}); err != nil {
			return fmt.Errorf("prefix worker: %w", err)
		}
	}
	if _, err := c.checkCached(child.ID, start); err != nil {
		return fmt.Errorf("subagent prefix: %w", err)
	}
	return nil
}

func (c *liveCore) restartNote(format string, args ...any) {
	c.restartNotes = append(c.restartNotes, fmt.Sprintf(format, args...))
}

func (c *liveCore) restartSnapshot(stage, childID string, homeSeq int64) {
	ctx := c.t.Context()
	c.restartNote("restart %s:", stage)
	for _, id := range []string{c.home, childID} {
		if id == "" {
			continue
		}
		row, err := c.store.Session(ctx, id)
		if err != nil {
			c.restartNote("session %s: %v", id, err)
			continue
		}
		c.restartNote("session %s name=%s state=%s waiting=%q head=%s", id, row.Name, row.State, row.WaitingReason, row.HeadEntryID)
		runs, err := c.store.Runs(ctx, id)
		if err != nil {
			c.restartNote("session %s runs: %v", id, err)
		} else {
			for _, r := range runs {
				c.restartNote("run %s session=%s state=%s error=%q", r.ID, id, r.State, r.Error)
			}
		}
		entries, err := c.store.Entries(ctx, id)
		if err != nil {
			c.restartNote("session %s entries: %v", id, err)
			continue
		}
		for _, e := range entries {
			if id == c.home && e.Seq <= homeSeq {
				continue
			}
			if e.Kind == store.KindNotice {
				text, err := session.TextOf(e)
				if err == nil {
					c.restartNote("notice %s session=%s text=%q", e.ID, id, clipRestart(text))
				}
				continue
			}
			m, ok, err := session.Message(e)
			if err != nil || !ok {
				continue
			}
			if m.Content != "" {
				c.restartNote("entry %s session=%s turn=%s role=%s error=%t text=%q", e.ID, id, e.RunID, m.Role, m.IsError, clipRestart(m.Content))
			}
			for _, call := range m.ToolCalls {
				c.restartNote("tool session=%s turn=%s id=%s name=%s arguments=%s", id, e.RunID, call.ID, call.Name, clipRestart(string(call.Arguments)))
			}
		}
	}
}

func clipRestart(s string) string {
	const limit = 500
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

// The template's final assistant-generation header can change when the answer
// is replayed. Check every byte before it, then count only that header at its
// tokenizer-confirmed boundary.
func stablePromptPrefix(ctx context.Context, source counter.Source, previous, next string) (int, int, error) {
	if source == nil {
		return 0, 0, fmt.Errorf("no tokenizer for prefix check")
	}
	boundaries, err := source.Boundaries(ctx, previous)
	if err != nil {
		return 0, 0, fmt.Errorf("find template boundaries: %w", err)
	}
	if len(boundaries) == 0 {
		return 0, 0, fmt.Errorf("no generation header boundary")
	}
	cut := -1
	for i := len(boundaries) - 1; i >= 0; i-- {
		at := boundaries[i]
		if at <= 0 || at >= len(previous) || len(previous)-at > 256 {
			continue
		}
		header := previous[at:]
		end := strings.Index(header, "|>")
		if end >= 0 && strings.HasPrefix(strings.TrimSpace(header[end+2:]), "assistant") {
			cut = at
			break
		}
	}
	if cut < 0 {
		return 0, 0, fmt.Errorf("no assistant generation header at the end of the previous prompt")
	}
	if !strings.HasPrefix(next, previous[:cut]) {
		at := 0
		for at < cut && at < len(next) && previous[at] == next[at] {
			at++
		}
		return 0, 0, fmt.Errorf("rendered prefix changed at byte %d before generation header at byte %d", at, cut)
	}
	header, err := source.Text(ctx, previous[cut:])
	if err != nil {
		return 0, 0, fmt.Errorf("count generation header: %w", err)
	}
	if header <= 0 {
		return 0, 0, fmt.Errorf("invalid generation header token count %d", header)
	}
	return cut, header, nil
}
