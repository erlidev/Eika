package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
)

// Scenario is one scripted conversation with the model. Run sends its
// requests through Target.Complete and returns the call of each, in order; a
// scenario fails by returning an error.
type Scenario struct {
	// Name identifies the scenario in the report and as a subtest.
	Name string
	// Run plays the scenario.
	Run func(ctx context.Context, t *Target) ([]Call, error)
}

// Scenarios returns every scenario of the suite. Add a new one here.
func Scenarios() []Scenario {
	nonce := time.Now().UTC().Format("150405.000000")
	return []Scenario{
		ProviderProbe(),
		CacheReuse(nonce),
		UsageFields(nonce),
		OnResponses(UsageFields(nonce)),
		CountingConversation(nonce),
		OnResponses(CountingConversation(nonce)),
		ToolsAndReasoning(nonce),
		ImageInput(),
		CountCost(nonce),
		AnchoredCounting(nonce),
		AgentLoop(nonce),
		ReasoningCount(nonce),
		ContextBreakdown(nonce),
		ContextBlockPrefix(nonce),
		OnResponses(ContextBlockPrefix(nonce)),
		PrefixAcrossCompaction(nonce),
		OnResponses(PrefixAcrossCompaction(nonce)),
		ToolArguments(nonce),
		OnResponses(ToolArguments(nonce)),
		ThinkingSwitches(nonce),
		OnResponses(ThinkingSwitches(nonce)),
		CutOffReasoning(nonce),
		ResponsesConformance(),
		SessionTitle(),
		QueuePriority(nonce),
		SharedAdmission(nonce),
		SlotHintHonoured(nonce, provider.APIChat),
		SlotHintHonoured(nonce, provider.APIResponses),
		SlotAffinity(nonce, provider.APIChat),
		SlotAffinity(nonce, provider.APIResponses),
		ResponsesDrift(nonce),
		OnResponses(CutOffReasoning(nonce)),
		OnResponses(ToolsAndReasoning(nonce)),
		OnResponses(ImageInput()),
		OnResponses(AgentLoop(nonce)),
		OnResponses(ReasoningCount(nonce)),
		OnResponses(ContextBreakdown(nonce)),
	}
}

// Result is the outcome of one scenario.
type Result struct {
	// Name is the scenario's name.
	Name string `json:"name"`
	// Passed is whether the scenario ran without an error.
	Passed bool `json:"passed"`
	// Error says why it did not, and is empty when it passed.
	Error string `json:"error,omitempty"`
	// Seconds is how long the scenario took, wall clock.
	Seconds float64 `json:"seconds"`
	// Summary totals the calls.
	Summary Summary `json:"summary"`
	// Calls are the model calls the scenario made, in order, including the
	// ones before a failure.
	Calls []Call `json:"calls"`
	// Notes are the findings a scenario states in words, such as what a
	// control run cost, in the order it stated them.
	Notes []string `json:"notes,omitempty"`
}

// Note records a finding of the scenario being played, to be reported with
// its result. It does nothing on a target that is not playing one.
func (t *Target) Note(format string, args ...any) {
	if t.notes != nil {
		*t.notes = append(*t.notes, fmt.Sprintf(format, args...))
	}
}

// Execute plays the scenario against t and returns its result. The scenario
// is one session: it gets a token counter of its own, which predicts every
// request it sends. A prediction called exact that drifted fails the scenario,
// whatever else it asserts.
func (s Scenario) Execute(ctx context.Context, t *Target) Result {
	start := time.Now()
	session := *t
	if t.Source != nil {
		session.Counter = counter.New(t.Source)
	}
	var notes []string
	session.notes = &notes
	calls, err := s.Run(ctx, &session)
	if err == nil {
		err = exactDrift(calls)
	}
	res := Result{
		Name:    s.Name,
		Passed:  err == nil,
		Seconds: time.Since(start).Seconds(),
		Summary: Summarise(calls),
		Calls:   calls,
		Notes:   notes,
	}
	if res.Calls == nil {
		res.Calls = []Call{}
	}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

// callTimeout bounds one model call, so a scenario against a stuck server
// fails instead of hanging the run.
const callTimeout = 2 * time.Minute

// Complete sends req through the provider, naming the model, and reads the
// whole response. It returns the response text and what the call measured,
// labelled as given. A response the server cut off at the request's token
// limit is not an error: scenarios bound their output on purpose.
func (t *Target) Complete(ctx context.Context, label string, req provider.Request) (Call, string, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req.Model = t.Model

	var predicted *provider.Prediction
	var countTime time.Duration
	if t.Counter != nil {
		began := time.Now()
		if p, err := t.Counter.Predict(ctx, req); err == nil {
			predicted = &p
		}
		countTime = time.Since(began)
	}

	start := time.Now()
	events, err := t.Provider.Stream(ctx, req)
	if err != nil {
		return Call{Label: label}, "", fmt.Errorf("%s: start request: %w", label, err)
	}
	var text strings.Builder
	var usage provider.Usage
	var timings provider.Timings
	var stop string
	var failure error
	for e := range events {
		switch e.Kind {
		case provider.KindTextDelta:
			text.WriteString(e.Text)
		case provider.KindUsage:
			usage, timings = e.Usage, e.Timings
		case provider.KindDone:
			stop = e.StopReason
		case provider.KindError:
			failure = e.Err
		}
	}
	call := newCall(label, usage, timings, stop, time.Since(start))
	if req.SlotHint != nil {
		call.Slot = new(*req.SlotHint)
	}
	if predicted != nil {
		call.Predicted, call.Method = &predicted.Tokens, string(predicted.Method)
		seconds := countTime.Seconds()
		call.CountSeconds = &seconds
		if out, ok := t.Counter.Observe(ctx, req, usage); ok {
			call.Drift = &out.Drift
		}
	}
	if failure != nil {
		return call, text.String(), fmt.Errorf("%s: %w", label, failure)
	}
	if !usage.InputTokens.Reported() {
		return call, text.String(), fmt.Errorf("%s: the server reported no usage", label)
	}
	return call, text.String(), nil
}
