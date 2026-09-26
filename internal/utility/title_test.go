package utility_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/utility"
)

func TestTitleSendsOneRequestWithThinkingOff(t *testing.T) {
	cases := []struct {
		name       string
		model      utility.Model
		wantEffort string
		wantMax    int
	}{
		{
			name:       "a model that offers none gets none in its thinking switch",
			model:      utility.Model{ID: "qwen", ReasoningEfforts: []string{"none", "high"}, ReasoningEffort: "high", ThinkingSwitch: provider.SwitchTemplate},
			wantEffort: provider.EffortNone,
			wantMax:    1024,
		},
		{
			name:       "a model that does not offer none keeps its own effort",
			model:      utility.Model{ID: "o4", ReasoningEfforts: []string{"low", "high"}, ReasoningEffort: "low", MaxOutput: 256},
			wantEffort: "low",
			wantMax:    256,
		},
		{
			name:    "a model with no efforts sends none of its own",
			model:   utility.Model{ID: "plain"},
			wantMax: 1024,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("Fix the login redirect"))
			tc.model.Provider = p
			title, err := utility.Title(t.Context(), tc.model, "the login page redirects forever, fix it")
			if err != nil {
				t.Fatalf("Title: %v", err)
			}
			if title != "Fix the login redirect" {
				t.Errorf("title = %q", title)
			}
			reqs := p.Requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			req := reqs[0]
			if !strings.Contains(req.System, "Respond with only the session title") {
				t.Errorf("system prompt = %q", req.System)
			}
			if req.Model != tc.model.ID || req.ThinkingSwitch != tc.model.ThinkingSwitch || len(req.Tools) != 0 {
				t.Errorf("request = %+v", req)
			}
			if len(req.Messages) != 1 || req.Messages[0].Role != provider.RoleUser ||
				req.Messages[0].Content != "the login page redirects forever, fix it" {
				t.Errorf("messages = %+v, want the first message alone", req.Messages)
			}
			effort := ""
			if req.Sampling.ReasoningEffort != nil {
				effort = *req.Sampling.ReasoningEffort
			}
			if effort != tc.wantEffort {
				t.Errorf("effort = %q, want %q", effort, tc.wantEffort)
			}
			if req.Sampling.MaxOutput == nil || *req.Sampling.MaxOutput != tc.wantMax {
				t.Errorf("max output = %v, want %d", req.Sampling.MaxOutput, tc.wantMax)
			}
		})
	}
}

func TestTitleCleansTheReply(t *testing.T) {
	cases := []struct{ name, reply, want string }{
		{"plain", "Refactor the parser", "Refactor the parser"},
		{"quoted", `"Refactor the parser."`, "Refactor the parser"},
		{"curly quotes", "“Refactor the parser”", "Refactor the parser"},
		{"labelled", "Title: Refactor the parser", "Refactor the parser"},
		{"markdown heading", "## **Refactor the parser**", "Refactor the parser"},
		{"first line only", "\n\nRefactor the parser\nThis title covers...", "Refactor the parser"},
		{"reasoning left in the text", "<think>The user wants\na parser.</think>\nRefactor the parser", "Refactor the parser"},
		{"spaces collapsed", "Refactor   the\tparser", "Refactor the parser"},
		{"apostrophe kept", "Fix the user's login", "Fix the user's login"},
		{"long", strings.Repeat("word ", 30), strings.TrimSpace(strings.Repeat("word ", 16))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := providertest.New(providertest.Text(tc.reply))
			title, err := utility.Title(t.Context(), utility.Model{Provider: p, ID: "m"}, "hello")
			if err != nil {
				t.Fatalf("Title: %v", err)
			}
			if title != tc.want {
				t.Errorf("title = %q, want %q", title, tc.want)
			}
		})
	}
}

func TestTitleRefusesAnEmptyReply(t *testing.T) {
	for _, reply := range []string{"", "  \n", `""`, "<think>hmm</think>"} {
		p := providertest.New(providertest.Text(reply))
		if _, err := utility.Title(t.Context(), utility.Model{Provider: p, ID: "m"}, "hello"); !errors.Is(err, utility.ErrNoTitle) {
			t.Errorf("reply %q: err = %v, want ErrNoTitle", reply, err)
		}
	}
}

func TestTitleReportsAFailedCall(t *testing.T) {
	failure := errors.New("endpoint down")
	p := providertest.New(providertest.Fail(failure))
	if _, err := utility.Title(t.Context(), utility.Model{Provider: p, ID: "m"}, "hello"); !errors.Is(err, failure) {
		t.Errorf("err = %v, want the endpoint's failure", err)
	}
}

func TestTitleSendsTheStartOfALongMessage(t *testing.T) {
	p := providertest.New(providertest.Text("Read the log"))
	// A two-byte character straddles the bound, so a cut by bytes alone
	// would split it.
	message := strings.Repeat("a", utility.MaxTitleSource-1) + "é" + strings.Repeat("b", 100)
	if _, err := utility.Title(t.Context(), utility.Model{Provider: p, ID: "m"}, message); err != nil {
		t.Fatalf("Title: %v", err)
	}
	sent := p.Requests()[0].Messages[0].Content
	if sent != strings.Repeat("a", utility.MaxTitleSource-1) {
		t.Errorf("sent %d bytes ending %q, want the message cut before the split character", len(sent), sent[len(sent)-3:])
	}
}

func TestKnown(t *testing.T) {
	if !utility.Known(utility.TaskSessionTitle) || utility.Known("summarise") {
		t.Error("Known should accept session_title alone")
	}
}
