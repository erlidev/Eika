package builtin_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashRunsCommand(t *testing.T) {
	w := newWorkspace(t)
	w.write("a.txt", "x")

	res := w.call("bash", map[string]any{"command": "ls"})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.txt") {
		t.Errorf("content = %q, want it to list the workspace", res.Content)
	}
	if len(w.outputs) == 0 {
		t.Error("no tool.output events were emitted")
	}
}

func TestBashCombinesStreams(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": "echo out; echo err 1>&2"})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "out") || !strings.Contains(res.Content, "err") {
		t.Errorf("content = %q, want both streams", res.Content)
	}
}

func TestBashReportsExitCode(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": "exit 7"})
	if !res.IsError {
		t.Fatalf("bash reported success for a failing command: %s", res.Content)
	}
	if !strings.Contains(res.Content, "exit code 7") {
		t.Errorf("content = %q, want the exit code", res.Content)
	}
	var details struct {
		ExitCode int  `json:"exit_code"`
		TimedOut bool `json:"timed_out"`
	}
	if err := json.Unmarshal(res.Details, &details); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if details.ExitCode != 7 || details.TimedOut {
		t.Errorf("details = %+v", details)
	}
}

func TestBashTimesOut(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": "sleep 5", "timeout": 1})
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("result = %+v, want a timeout", res)
	}
}

func TestBashTruncatesLongOutput(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": `head -c 200000 /dev/zero | tr '\0' 'a'`})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if len(res.Content) > 64*1024 {
		t.Errorf("content length = %d, want it truncated", len(res.Content))
	}
	if !strings.Contains(res.Content, "bytes truncated") {
		t.Error("content does not say that it was truncated")
	}
	if !strings.HasPrefix(res.Content, "aaa") || !strings.HasSuffix(res.Content, "aaa") {
		t.Error("truncation did not keep both the start and the end")
	}
}

func TestBashDoesNotAnnounceTruncationItDidNotDo(t *testing.T) {
	w := newWorkspace(t)
	// 25 KB is past the head budget but inside the total budget, so nothing
	// is dropped.
	res := w.call("bash", map[string]any{"command": `head -c 25000 /dev/zero | tr '\0' 'a'`})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if strings.Contains(res.Content, "truncated") {
		t.Error("content claims truncation that did not happen")
	}
	if len(res.Content) != 25000 {
		t.Errorf("content length = %d, want the whole output", len(res.Content))
	}
}

func TestBashStopsStreamingPastTheOutputBudget(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": `head -c 200000 /dev/zero | tr '\0' 'a'`})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	streamed := 0
	notices := 0
	for _, chunk := range w.outputs {
		streamed += len(chunk)
		if strings.Contains(chunk, "output continues") {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("streamed %d continuation notices, want 1", notices)
	}
	if streamed > 64*1024 {
		t.Errorf("streamed %d bytes, want the stream bounded", streamed)
	}
}

func TestBashTimeoutKillsBackgroundChildren(t *testing.T) {
	w := newWorkspace(t)
	start := time.Now()
	res := w.call("bash", map[string]any{"command": "sleep 30 & sleep 30", "timeout": 1})
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("result = %+v, want a timeout", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the call took %s, want the process group killed promptly", elapsed)
	}
}

func TestBashRequiresCommand(t *testing.T) {
	w := newWorkspace(t)
	res := w.call("bash", map[string]any{"command": "  "})
	if !res.IsError || !strings.Contains(res.Content, "command is required") {
		t.Errorf("result = %+v", res)
	}
}
