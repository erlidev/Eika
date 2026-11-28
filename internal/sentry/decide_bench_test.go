package sentry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/sentry/trip"
)

type benchAudit struct{}

func (benchAudit) AppendAudit(context.Context, AuditEntry) error { return nil }

func BenchmarkDecide(b *testing.B) {
	const size = 64 * 1024
	canary := "sk-proj-" + strings.Repeat("z", 156)
	cases := []struct {
		name   string
		action Action
	}{
		{"fetch", Action{Kind: KindTool, Name: "web_fetch", Args: benchArgs("url", "https://example.com/?q=", size)}},
		{"exec", Action{Kind: KindTool, Name: "bash", Args: benchArgs("command", "echo ", size)}},
		{"exec_canary", Action{Kind: KindTool, Name: "bash", Args: benchArgs("command", "echo ", size-len(canary))[:size-len(canary)-2]}},
	}
	// Put a canary at the end of a full-sized command.
	cases[2].action.Args = append(cases[2].action.Args, []byte(canary+`"}`)...)
	cases = append(cases, struct {
		name   string
		action Action
	}{"fetch_private", Action{Kind: KindTool, Name: "web_fetch", Principal: Principal{Session: "s", Run: "r", Computer: "c"},
		Args: benchArgs("url", "https://example.com/?q=", size)}})
	for _, tc := range cases {
		if len(tc.action.Args) != size {
			b.Fatalf("%s arguments have %d bytes, want %d", tc.name, len(tc.action.Args), size)
		}
		for _, halted := range []bool{false, true} {
			name := tc.name + "/running"
			if halted {
				name = tc.name + "/halted"
			}
			b.Run(name, func(b *testing.B) {
				g := New(Options{Audit: benchAudit{}, Canary: func() string { return canary }, Tripwires: func(context.Context) (trip.Settings, error) { return benchTrip, nil }, Halter: func(context.Context, string) error { return nil }}).(*gate)
				g.halt.set(HaltState{Halted: halted})
				if tc.action.Principal.Session != "" {
					g.Marks = benchMarks{}
					g.Observe(context.Background(), Observation{Kind: ObservePrivateRead, Session: "s",
						Text: strings.Repeat("a private note that is long enough to be fingerprinted by the gate ", 800)})
				}
				ctx := context.Background()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := g.Decide(ctx, tc.action); err != nil {
						b.Fatal(err)
					}
				}
				if b.N >= 200 && b.Elapsed()/time.Duration(b.N) >= time.Millisecond {
					b.Fatalf("Decide took %s per call; limit is under 1 ms", b.Elapsed()/time.Duration(b.N))
				}
			})
		}
	}
}

var benchTrip = trip.Settings{Followed: true, CarriedOut: true, Lifted: true, Drained: true, BlockedOften: true, BlockedLimit: 10}

type benchMarks struct{}

func (benchMarks) AppendMark(context.Context, Mark) error { return nil }

func benchArgs(key, prefix string, size int) json.RawMessage {
	// The JSON arguments, not just the value, are exactly 64 KiB.
	value := prefix + strings.Repeat("a", size-len(key)-len(prefix)-7)
	args, _ := json.Marshal(map[string]string{key: value})
	return args
}
