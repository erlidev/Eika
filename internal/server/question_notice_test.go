package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/tool/builtin"
)

type questionStore struct {
	mu  sync.Mutex
	row store.Session
}

func (s *questionStore) Session(_ context.Context, id string) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "home" {
		return store.Session{ID: "home", Kind: store.SessionHome}, nil
	}
	if id == "child" {
		return s.row, nil
	}
	return store.Session{}, store.ErrNotFound
}
func (s *questionStore) CreateSession(_ context.Context, row store.Session) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row.ID = "child"
	s.row = row
	return row, nil
}
func (s *questionStore) Subagent(_ context.Context, parent, name string) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row.ParentSessionID != parent || s.row.Name != name {
		return store.Session{}, store.ErrNotFound
	}
	return s.row, nil
}
func (s *questionStore) Subagents(context.Context, string) ([]store.Session, error) { return nil, nil }
func (s *questionStore) SetSubagentState(_ context.Context, _ string, state store.SessionState, reason string) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row.State = state
	s.row.WaitingReason = reason
	return s.row, nil
}
func (s *questionStore) SetSubagentResult(_ context.Context, _ string, text string) (store.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row.Result = text
	return s.row, nil
}

func TestQuestionNoticeAfterAnswerOrCancel(t *testing.T) {
	for _, action := range []string{"answer", "cancel"} {
		for _, stampedFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stamped-first-%t", action, stampedFirst), func(t *testing.T) {
				st := &questionStore{}
				ready := make(chan string, 2)
				var service *subagent.Service
				service = subagent.New(t.Context(), st, subagent.Options{
					Run: func(ctx context.Context, row store.Session, _ string, _ inference.Priority) error {
						for _, id := range []string{"question-first", "question-second"} {
							_, err := service.Ask(ctx, row.ID, id, "Which?", builtin.Question{}.ValidateAnswer, func(context.Context, store.Session) { ready <- id })
							if err != nil {
								return err
							}
						}
						return nil
					},
					Final: func(context.Context, store.Session) (string, error) { return "done", nil },
				})
				t.Cleanup(service.Close)
				_, _, err := service.Start(t.Context(), "home", store.Session{Name: "child", Agent: "researcher", Brief: "Find it"}, inference.Background)
				if err != nil {
					t.Fatal(err)
				}
				id := <-ready
				n := &notices{server: &Server{subagents: service}, sessions: map[string]*sessionNotices{"home": {}}}
				original := agent.Notice{ID: id, Text: "child asks: Which?"}
				if stampedFirst {
					n.sessions["home"].waiting = []agent.Notice{original}
				}
				var message provider.Message
				stamp := func() {
					taken := n.take("home")
					if len(taken) != 1 {
						t.Fatalf("taken = %+v", taken)
					}
					message = provider.Message{Notices: []string{id}, Context: "<context>\nnotice: " + taken[0].Text + "\n</context>"}
				}
				if stampedFirst {
					stamp()
					if got := n.refresh("home", message); strings.Contains(got.Context, "no longer pending") {
						t.Fatal(got.Context)
					}
				}
				if action == "answer" {
					if _, ok, err := service.Message(t.Context(), "home", "child", "A", inference.Background, id); err != nil || !ok {
						t.Fatalf("answer = %v, %v", ok, err)
					}
					if got := <-ready; got != "question-second" {
						t.Fatal(got)
					}
					if _, ok, err := service.Message(t.Context(), "home", "child", "stale", inference.Background, id); err != nil || ok {
						t.Fatalf("stale answer reached next question: %v, %v", ok, err)
					}
					if _, ok, err := service.Message(t.Context(), "home", "child", "missing id", inference.Background, ""); err != nil || ok {
						t.Fatalf("answer without id accepted: %v, %v", ok, err)
					}
				} else {
					if _, err := service.Cancel(t.Context(), "home", "child"); err != nil {
						t.Fatal(err)
					}
					if _, ok, err := service.Message(t.Context(), "home", "child", "stale", inference.Background, id); err != nil || ok {
						t.Fatalf("stale answer restarted child: %v, %v", ok, err)
					}
				}
				// This also models a failed notice write that succeeds after the wait ended.
				if !stampedFirst {
					n.sessions["home"].waiting = []agent.Notice{original}
					stamp()
				}
				got := n.refresh("home", message)
				if !strings.Contains(got.Context, "question "+id+" is no longer pending") || !strings.Contains(got.Context, "Do not answer it") {
					t.Fatalf("context = %q", got.Context)
				}
				if again := n.refresh("home", got); again.Context != got.Context {
					t.Fatalf("warning repeated: %q", again.Context)
				}
			})
		}
	}
}

func TestQuestionReasonKeepsOptionsWithSemicolons(t *testing.T) {
	q := builtin.Question{Text: "Which?", Options: []string{"one; two", "three\nfour"}}
	reason := questionReason(q, "question-1")
	line := strings.Split(reason, "\n")[2]
	var options []string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "Options (JSON): ")), &options); err != nil {
		t.Fatal(err)
	}
	if len(options) != 2 || options[0] != q.Options[0] || options[1] != q.Options[1] {
		t.Fatalf("options = %#v", options)
	}
}

func TestLegacyQuestionNoticeIsMarkedAfterRestart(t *testing.T) {
	const suffix = "\nAnswer with subagent_message, or ask the user in your reply and relay the answer."
	for _, tc := range []struct {
		name, text string
		stale      bool
	}{
		{"question", "flights (researcher) asks: Which day?" + suffix, true},
		{"report", "flights (researcher) reported: Which day?" + suffix, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notice := agent.Notice{ID: "notice-legacy", Text: tc.text}
			n := &notices{server: &Server{}, sessions: map[string]*sessionNotices{"home": {held: []agent.Notice{notice}}}}
			m := provider.Message{Notices: []string{notice.ID}, Context: "<context>\nnotice: old\n</context>"}
			got := n.refresh("home", m)
			if strings.Contains(got.Context, "no longer pending") != tc.stale {
				t.Fatalf("context = %q", got.Context)
			}
		})
	}
}

func TestRefreshOnlyReplacesStaleQuestionInAMixedContext(t *testing.T) {
	n := &notices{server: &Server{}, sessions: map[string]*sessionNotices{"home": {}}}
	m := provider.Message{Notices: []string{"report-1", "question-1", "report-2"}, Context: "<context>\ntime: now\nnotice: first report\nnotice: child asks\n  continuation\n\n  more\nnotice: second report\n</context>"}
	got := n.refresh("home", m)
	want := "<context>\ntime: now\nnotice: first report\nnotice: Subagent question question-1 is no longer pending. Do not answer it.\nnotice: second report\n</context>"
	if got.Context != want {
		t.Fatalf("context = %q, want %q", got.Context, want)
	}
	if again := n.refresh("home", got); again.Context != want {
		t.Fatalf("second refresh = %q", again.Context)
	}
}
