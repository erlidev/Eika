package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/openai"
	"github.com/erlidev/eika/internal/provider/probe"
)

// The scenarios of slot affinity send id_slot to the bench's llama.cpp, whose
// two slots are the slots of the numbers they name, and read /slots and the
// cache counts the server states to see what it did with them.

const (
	// slotsPoll is how often the honoured-hint scenario reads /slots while a
	// call runs.
	slotsPoll = 25 * time.Millisecond
	// slotProbeOutput is the length of the reply of the calls whose slot is
	// watched: long enough, at the bench's decode speed, to be seen on /slots.
	slotProbeOutput = 200
	// slotHeadTokens is the size of the head the two sessions of slot_affinity
	// share, as a system prompt does, and slotAskTokens that of each message
	// that is the session's own.
	slotHeadTokens = 3000
	slotAskTokens  = 600
	// slotTurns is how many times each session speaks.
	slotTurns = 3
	// slotMinReuse is the share of the previous prompt of its session that a
	// call on its own slot must find cached. The rest is the tail of the
	// previous prompt that the chat template renders again once the reply is
	// part of the history.
	slotMinReuse = 0.9
)

// hintedProvider returns the provider of api on the bench's endpoint, built as
// the harness builds one for a llama.cpp server with several slots.
func (t *Target) hintedProvider(api provider.API) (provider.Provider, counter.Source, error) {
	e := provider.Endpoint{BaseURL: t.BaseURL, APIKey: t.Key, API: api, SlotHints: true}
	if api == provider.APIResponses {
		p, err := openai.NewResponses(e)
		return p, t.ResponsesSource, err
	}
	p, err := openai.New(e)
	return p, t.Source, err
}

// slotState is what /slots says about one slot.
type slotState struct {
	ID         int  `json:"id"`
	Processing bool `json:"is_processing"`
}

// slots reads which of the server's slots are processing a request now.
func (t *Target) slots(ctx context.Context) ([]slotState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(t.BaseURL, "/v1")+"/slots", nil)
	if err != nil {
		return nil, fmt.Errorf("read /slots: %w", err)
	}
	if t.Key != "" {
		req.Header.Set("Authorization", "Bearer "+t.Key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read /slots: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read /slots: status %d: the server must serve /slots (not started with --no-slots) for this scenario to see its slots", resp.StatusCode)
	}
	var out []slotState
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("read /slots: %w", err)
	}
	return out, nil
}

// watchSlots reads /slots every slotsPoll until stop is closed and returns,
// through the function it gives back, the slots it saw processing.
func (t *Target) watchSlots(ctx context.Context) (stop func() ([]int, error)) {
	var (
		mu   sync.Mutex
		seen = map[int]bool{}
		err  error
		done = make(chan struct{})
		exit = make(chan struct{})
	)
	go func() {
		defer close(exit)
		for {
			states, readErr := t.slots(ctx)
			mu.Lock()
			if readErr != nil {
				err = readErr
			}
			for _, s := range states {
				if s.Processing {
					seen[s.ID] = true
				}
			}
			mu.Unlock()
			select {
			case <-done:
				return
			case <-time.After(slotsPoll):
			}
		}
	}()
	return func() ([]int, error) {
		close(done)
		<-exit
		mu.Lock()
		defer mu.Unlock()
		var ids []int
		for id := range seen {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return ids, err
	}
}

// requireSlots fails a scenario that needs a server with several slots.
func (t *Target) requireSlots() (int, error) {
	if n := t.Server.Flags.Slots; n >= 2 {
		return n, nil
	}
	return 0, fmt.Errorf("the server states %d slots; slot affinity needs a llama.cpp server with several", t.Server.Flags.Slots)
}

// SlotHintHonoured is the scenario that asks the server to run requests on a
// named slot and reads /slots while they run to see which slot it used. The
// slot order 1, 1, 0, 0 is chosen so that a server that ignored the field
// would be seen to: least recently used slot selection would put the second
// request elsewhere than the first, and selection by the prompt's similarity
// to the slot's cache would put the third on the slot of the second. Under api
// it is the same through that protocol's route. A number the server has no
// slot for must not fail the call.
func SlotHintHonoured(nonce string, api provider.API) Scenario {
	name := "slot_hint_honoured"
	if api == provider.APIResponses {
		name = "responses_" + name
	}
	return Scenario{
		Name: name,
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			slots, err := t.requireSlots()
			if err != nil {
				return nil, err
			}
			p, source, err := t.hintedProvider(api)
			if err != nil {
				return nil, err
			}
			session := *t
			session.Provider, session.Source, session.Counter = p, source, counter.New(source)

			var calls []Call
			steps := []int{1, 1, 0, 0, slots + 5}
			for i, slot := range steps {
				req := quietRequest("You count when asked.", fmt.Sprintf("%s probe %d. Count from 1 to 200 separated by commas, nothing else.", nonce, i+1))
				req.Sampling.MaxOutput = new(slotProbeOutput)
				req.SlotHint = &slot
				label := fmt.Sprintf("id_slot %d", slot)

				watching := t.watchSlots(ctx)
				call, _, callErr := session.Complete(ctx, label, req)
				seen, watchErr := watching()
				calls = append(calls, call)
				switch {
				case callErr != nil:
					return calls, fmt.Errorf("%s: a slot hint must not fail a call: %w", label, callErr)
				case watchErr != nil:
					return calls, watchErr
				}
				t.Note("%s: /slots showed slot(s) %v processing it", label, seen)
				if slot >= slots {
					// The server has no such slot; what it does with the number
					// is its own business as long as the call succeeds.
					continue
				}
				if !slices.Equal(seen, []int{slot}) {
					return calls, fmt.Errorf("%s: /slots showed slots %v processing the request, want only slot %d: the server did not honour id_slot", label, seen, slot)
				}
			}
			return calls, nil
		},
	}
}

// SlotAffinity is the scenario that plays two sessions through the agent loop
// on the queue's slot affinity, and then the same two sessions with no hint,
// and reports what affinity saved. The sessions share a head (a system prompt
// of about 3000 tokens, as every session of an assistant shares one) and each
// has messages of its own, and they speak in turn: A, B, A, B, A, B. Each
// phase is a pair of agents on one queue over the bench's two slots, so the
// calls are made by the loop, through the queue's lease, with the slot the
// lease names.
//
// With hints each session must keep one slot, the two must differ, and every
// call after a session's first must find nearly all of the session's previous
// prompt cached. Without them llama.cpp places each request itself, and the
// scenario reports what that cost; it asserts nothing about the control except
// that affinity prefilled no more than it.
func SlotAffinity(nonce string, api provider.API) Scenario {
	name := "slot_affinity"
	if api == provider.APIResponses {
		name = "responses_" + name
	}
	return Scenario{
		Name: name,
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			slots, err := t.requireSlots()
			if err != nil {
				return nil, err
			}
			p, source, err := t.hintedProvider(api)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(ctx, agentLoopTimeout)
			defer cancel()

			// The harness's own question to the server, which decides whether
			// it names slots at all.
			verdict := probe.CheckSlotAffinity(ctx, probe.SlotCheck{Provider: p, Model: t.Model, Build: t.Server.Build})
			t.Note("slot affinity check: %s; the last request found %d of the first's %d prompt tokens cached. %s",
				verdict.Verdict, verdict.Cached, verdict.Prompt, verdict.Reason)

			// Each phase has a head of its own, so that neither finds the
			// other's in the server's cache.
			affine, err := playSessions(ctx, t, p, source, slots, true, name+" "+nonce+" affine")
			if err != nil {
				return affine, err
			}
			control, err := playSessions(ctx, t, p, source, slots, false, name+" "+nonce+" control")
			calls := slices.Concat(affine, control)
			if err != nil {
				return calls, err
			}

			prefilled := func(calls []Call) (int, float64) {
				tokens, seconds := 0, 0.0
				for _, c := range calls {
					if c.PrefilledTokens != nil {
						tokens += *c.PrefilledTokens
					}
					if c.PrefillSeconds != nil {
						seconds += *c.PrefillSeconds
					}
				}
				return tokens, seconds
			}
			withTokens, withSeconds := prefilled(affine)
			withoutTokens, withoutSeconds := prefilled(control)
			t.Note("prefilled with affinity %d tokens (%.2f s), without hints %d tokens (%.2f s): affinity saved %d tokens (%.2f s)",
				withTokens, withSeconds, withoutTokens, withoutSeconds, withoutTokens-withTokens, withoutSeconds-withSeconds)
			// The sessions are played with hints whatever the verdict, to see
			// what hints do; the check must agree with what they did.
			switch verdict.Verdict {
			case probe.Passed:
				if err := requireReuse(affine); err != nil {
					return calls, fmt.Errorf("the check passed, but the sessions did not keep their prefixes: %w", err)
				}
				if withTokens > withoutTokens {
					return calls, fmt.Errorf("the check passed, but affinity prefilled %d tokens and the control %d", withTokens, withoutTokens)
				}
			default:
				if withTokens < withoutTokens {
					return calls, fmt.Errorf("the check %s (%s), but naming slots prefilled %d tokens against the control's %d: the harness would give up a saving",
						verdict.Verdict, verdict.Reason, withTokens, withoutTokens)
				}
				t.Note("the harness sends this server no id_slot: naming slots found %d tokens cached against the control's %d",
					cachedOf(affine), cachedOf(control))
			}
			return calls, nil
		},
	}
}

// cachedOf is the cached tokens of calls.
func cachedOf(calls []Call) int {
	n := 0
	for _, c := range calls {
		if c.CachedTokens != nil {
			n += *c.CachedTokens
		}
	}
	return n
}

// sessionAsk is the text a session's turn says: a distinct sentence repeated,
// so that two sessions share nothing after the head.
func sessionAsk(session string, turn, tokens int) string {
	sentence := fmt.Sprintf("Session %s keeps notes on topic %s, number %d: the %s ledger lists %s entries by date. ", session, session, turn, session, session)
	// A sentence is about 30 tokens.
	return strings.Repeat(sentence, max(tokens/30, 1)) + "Reply with the single word ok."
}

// playSessions plays two sessions A and B alternately through the agent loop
// over provider p on a queue whose pool is the bench's slots, with slot hints
// when hinted. The sessions' system prompt is the head they share. It returns
// the call of each model request the loop made, in order.
func playSessions(ctx context.Context, t *Target, p provider.Provider, source counter.Source, slots int, hinted bool, head string) ([]Call, error) {
	text, err := repeatedPrompt(ctx, t, head, slotHeadTokens)
	if err != nil {
		return nil, err
	}
	system := text + "\nYou are Eika. Answer in one short sentence."

	q := inference.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	pool := inference.Pool{Name: "bench", Slots: slots}
	if hinted {
		pool.Hinted = []inference.SlotEndpoint{{Name: "bench", Slots: slots}}
	}
	q.SetPools([]inference.Pool{pool})

	type player struct {
		name    string
		agent   *agent.Agent
		session *agent.Session
		records *callRecords
	}
	var players []*player
	for _, name := range []string{"A", "B"} {
		records := &callRecords{}
		sampling := quiet()
		players = append(players, &player{
			name:    name,
			records: records,
			session: agent.NewSession("live-"+name, ""),
			agent: agent.New(p, nil, agent.Options{
				Model:          t.Model,
				ContextWindow:  32768,
				BasePrompt:     &system,
				Sampling:       sampling,
				ThinkingSwitch: provider.SwitchTemplate,
				Recorder:       records,
				Counter:        counter.New(source),
				Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
				Inference:      agent.Inference{Queue: q, Pool: "bench", Endpoint: "bench"},
			}),
		})
	}

	phase := "control"
	if hinted {
		phase = "affine"
	}
	var calls []Call
	seen := map[string]int{}
	for turn := 1; turn <= slotTurns; turn++ {
		for _, pl := range players {
			tokens := slotAskTokens
			if turn == 1 {
				tokens = 2 * slotAskTokens
			}
			if err := pl.agent.Run(ctx, pl.session, provider.UserMessage(sessionAsk(pl.name, turn, tokens))); err != nil {
				return calls, fmt.Errorf("%s session %s turn %d: %w", phase, pl.name, turn, err)
			}
			recorded := pl.records.all()
			for ; seen[pl.name] < len(recorded); seen[pl.name]++ {
				r := recorded[seen[pl.name]]
				c := newCall(fmt.Sprintf("%s %s%d", phase, pl.name, turn), r.Usage, r.Timings, "", 0)
				if pr := r.Prediction; pr.Method != "" {
					tokens := pr.Tokens
					c.Predicted, c.Method, c.Drift = &tokens, string(pr.Method), r.Drift
				}
				c.Slot = r.Queue.Slot
				calls = append(calls, c)
			}
		}
	}
	return calls, nil
}

// requireReuse checks the calls of the affine phase: each session on one slot
// of its own, and each call after a session's first reading its previous prompt
// from that slot's cache.
func requireReuse(calls []Call) error {
	slotOf := map[string]int{}
	previous := map[string]*Call{}
	for i := range calls {
		c := &calls[i]
		// The label is "affine A1": the session is the letter after the phase.
		session := string(strings.Fields(c.Label)[1][0])
		if c.Slot == nil {
			return fmt.Errorf("%s: the call named no slot, though the pool states the server's slots", c.Label)
		}
		if first, ok := slotOf[session]; ok && first != *c.Slot {
			return fmt.Errorf("%s: session %s moved from slot %d to slot %d though both were free", c.Label, session, first, *c.Slot)
		}
		slotOf[session] = *c.Slot
		if prev := previous[session]; prev != nil {
			switch {
			case c.CachedTokens == nil || prev.PromptTokens == nil:
				return fmt.Errorf("%s: the server stated no cached count, so reuse cannot be measured", c.Label)
			case float64(*c.CachedTokens) < slotMinReuse*float64(*prev.PromptTokens):
				return fmt.Errorf("%s: %d tokens cached of the %d the session's previous call sent (want at least %.0f%%): its prefix was not kept in the slot",
					c.Label, *c.CachedTokens, *prev.PromptTokens, slotMinReuse*100)
			}
		}
		previous[session] = c
	}
	if a, b := slotOf["A"], slotOf["B"]; a == b {
		return fmt.Errorf("both sessions ran on slot %d", a)
	}
	return nil
}
