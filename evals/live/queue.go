package live

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
)

// The scenarios of the inference queue run its leases on a pool of their own
// in the scenario, over the bench's real model: the queue's behaviour is the
// same whatever the server, and what the bench adds is that the calls it
// admits are real ones.

const (
	// queueMaxOutput bounds each reply of a queue scenario.
	queueMaxOutput = 24
	// queueWait bounds how long a scenario waits for its calls to line up.
	queueWait = 20 * time.Second
	// sharedPromptTokens is the size of each of the two prompts of
	// shared_admission. Together they pass the bench's 131072-token cache, so
	// they cannot be in the server at once, and each alone fits it.
	sharedPromptTokens = 66_000
	// sharedCache is the cache size the scenario's pool states. The probe
	// reads kv unknown from the bench (llama.cpp does not say whether the
	// cache is unified), so the scenario states what the bench's command line
	// sets: -c 131072 with a unified cache.
	sharedCache = 131072
)

// quietRequest is a short request with thinking off and a small reply.
func quietRequest(system, user string) provider.Request {
	maxOutput := queueMaxOutput
	temperature := 0.0
	effort := provider.EffortNone
	return provider.Request{
		System:         system,
		ThinkingSwitch: provider.SwitchTemplate,
		Sampling:       provider.Sampling{MaxOutput: &maxOutput, Temperature: &temperature, ReasoningEffort: &effort},
		Messages:       []provider.Message{provider.UserMessage(user)},
	}
}

// leased makes a model call holding a lease on the queue, and returns the call
// with the time it waited for the lease.
func leased(ctx context.Context, t *Target, q *inference.Queue, need inference.Need, label string, req provider.Request, admitted, finished func()) (Call, error) {
	began := time.Now()
	lease, err := q.Acquire(ctx, need)
	if err != nil {
		return Call{Label: label}, fmt.Errorf("%s: %w", label, err)
	}
	defer lease.Release()
	waited := time.Since(began).Seconds()
	if admitted != nil {
		admitted()
	}
	if finished != nil {
		// Deferred after the release, so that it runs while the lease is
		// still held.
		defer finished()
	}
	call, _, err := t.Complete(lease.Context(), label, req)
	call.QueueSeconds = &waited
	return call, err
}

// waitForWaiting polls the queue until n calls wait on its only pool.
func waitForWaiting(ctx context.Context, q *inference.Queue, n int) error {
	deadline := time.Now().Add(queueWait)
	for {
		if st := q.Snapshot(); len(st) == 1 && len(st[0].Waiting) == n {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("%d calls never waited in the queue", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// QueuePriority is the scenario in which an interactive call overtakes a
// background call that was waiting first, on a pool of one slot. A call holds
// the slot while a background call and then an interactive call line up
// behind it; the interactive call must be admitted, and answered, first. The
// calls are real requests to the bench's model.
func QueuePriority(nonce string) Scenario {
	return Scenario{
		Name: "queue_priority",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			q := inference.New(nil)
			q.SetPools([]inference.Pool{{Name: "bench", Slots: 1}})
			system := fmt.Sprintf("Session %s. Answer with one short word.", nonce)

			holder, err := q.Acquire(ctx, inference.Need{Pool: "bench", Priority: inference.Foreground})
			if err != nil {
				return nil, err
			}
			defer holder.Release()

			var (
				mu    sync.Mutex
				order []string
				calls = map[string]Call{}
				errs  []error
				wg    sync.WaitGroup
			)
			line := func(label string, priority inference.Priority) {
				wg.Go(func() {
					call, err := leased(ctx, t, q, inference.Need{Pool: "bench", Priority: priority},
						label, quietRequest(system, "Say "+label+"."), func() {
							mu.Lock()
							order = append(order, label)
							mu.Unlock()
						}, nil)
					mu.Lock()
					defer mu.Unlock()
					calls[label] = call
					if err != nil {
						errs = append(errs, err)
					}
				})
			}
			// The background call waits first, and the interactive one is
			// asked for only once the background one is queued.
			line("background", inference.Background)
			if err := waitForWaiting(ctx, q, 1); err != nil {
				return nil, err
			}
			line("interactive", inference.Interactive)
			if err := waitForWaiting(ctx, q, 2); err != nil {
				return nil, err
			}

			// The holder's own call is a real one, made while they wait.
			hold, _, holdErr := t.Complete(holder.Context(), "holder", quietRequest(system, "Say holder."))
			holder.Release()
			wg.Wait()

			out := []Call{hold}
			for _, label := range []string{"interactive", "background"} {
				out = append(out, calls[label])
			}
			if holdErr != nil {
				return out, holdErr
			}
			if len(errs) > 0 {
				return out, errs[0]
			}
			if !slices.Equal(order, []string{"interactive", "background"}) {
				return out, fmt.Errorf("calls were admitted in the order %s, want the interactive call before the background call that waited first",
					strings.Join(order, ", "))
			}
			if wi, wb := *calls["interactive"].QueueSeconds, *calls["background"].QueueSeconds; wi >= wb {
				return out, fmt.Errorf("the interactive call waited %.2f s and the background call %.2f s; the interactive call came later and should have waited less", wi, wb)
			}
			return out, nil
		},
	}
}

// repeatedPrompt builds a prompt of about tokens tokens from one sentence
// repeated, which the model reads at the same speed as any text and answers
// without effort. Its start is the nonce, so no earlier run's cache holds it.
func repeatedPrompt(ctx context.Context, t *Target, nonce string, tokens int) (string, error) {
	const sentence = "The quick brown fox jumps over the lazy dog while the river runs past the old stone mill. "
	per, err := t.Source.Text(ctx, strings.Repeat(sentence, 100))
	if err != nil {
		return "", fmt.Errorf("count a sample of the prompt: %w", err)
	}
	if per <= 0 {
		return "", fmt.Errorf("count a sample of the prompt: the tokenizer counted %d tokens", per)
	}
	n := tokens * 100 / per
	return nonce + "\n" + strings.Repeat(sentence, n), nil
}

// SharedAdmission is the scenario in which two sessions whose prompts together
// pass a shared cache are admitted one after the other. Each prompt is about
// 66k tokens of one repeated sentence, so the two cannot be in the server's
// 131072-token cache together, and each fits it alone. The pool states the
// cache explicitly (the probe reads kv unknown for the bench), both sessions ask
// at once, and neither call may fail or be cut off. It costs two real prefills
// of that size, one after the other.
func SharedAdmission(nonce string) Scenario {
	return Scenario{
		Name: "shared_admission",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			q := inference.New(nil)
			q.SetPools([]inference.Pool{{Name: "bench", Slots: 2, KV: inference.KVShared, Cache: sharedCache}})

			type session struct {
				label string
				req   provider.Request
				need  inference.Need
			}
			var sessions []session
			for _, label := range []string{"session A", "session B"} {
				text, err := repeatedPrompt(ctx, t, nonce+" "+label, sharedPromptTokens)
				if err != nil {
					return nil, err
				}
				req := quietRequest("Read the text, then reply with the single word ok.", text+"\n\nReply with the single word ok.")
				req.Model = t.Model
				predicted, err := t.Counter.Predict(ctx, req)
				if err != nil {
					return nil, fmt.Errorf("%s: count the prompt: %w", label, err)
				}
				tokens := predicted.Tokens + queueMaxOutput
				if tokens < sharedPromptTokens-4000 || 2*tokens <= sharedCache {
					return nil, fmt.Errorf("%s: the prompt is %d tokens; want about %d, so that two of them pass the %d-token cache",
						label, tokens, sharedPromptTokens, sharedCache)
				}
				sessions = append(sessions, session{label, req, inference.Need{
					Pool: "bench", Priority: inference.Interactive, Tokens: tokens, SessionID: label,
				}})
			}

			var (
				mu     sync.Mutex
				calls  []Call
				errs   []error
				inside int
				atOnce int
				wg     sync.WaitGroup
			)
			for _, s := range sessions {
				wg.Go(func() {
					call, err := leased(ctx, t, q, s.need, s.label, s.req, func() {
						mu.Lock()
						defer mu.Unlock()
						inside++
						atOnce = max(atOnce, inside)
					}, func() {
						mu.Lock()
						defer mu.Unlock()
						inside--
					})
					mu.Lock()
					defer mu.Unlock()
					calls = append(calls, call)
					if err != nil {
						errs = append(errs, err)
					}
				})
			}
			wg.Wait()

			slices.SortFunc(calls, func(a, b Call) int { return strings.Compare(a.Label, b.Label) })
			switch {
			case len(errs) > 0:
				return calls, errs[0]
			case atOnce != 1:
				return calls, fmt.Errorf("%d calls were in the server at once, want the two admitted one after the other", atOnce)
			}
			for _, c := range calls {
				if c.StopReason != "stop" {
					return calls, fmt.Errorf("%s ended with %q, want it to finish", c.Label, c.StopReason)
				}
				if c.CompletionTokens == nil || *c.CompletionTokens == 0 {
					return calls, fmt.Errorf("%s produced nothing", c.Label)
				}
			}
			return calls, nil
		},
	}
}
