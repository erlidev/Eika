package builtin_test

import (
	"strings"
	"testing"
	"time"
)

func TestBashRunsCommand(t *testing.T) {
	w := newComputer(t)
	w.write("a.txt", "x")

	res := w.call("bash", map[string]any{"command": "ls"})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "a.txt") {
		t.Errorf("content = %q, want it to list the computer", res.Content)
	}
	if len(w.outputs) == 0 {
		t.Error("no tool.output events were emitted")
	}
}

func TestBashReportsExitCode(t *testing.T) {
	w := newComputer(t)
	res := w.call("bash", map[string]any{"command": "exit 7"})
	if !res.IsError {
		t.Fatalf("bash reported success for a failing command: %s", res.Content)
	}
	if !strings.Contains(res.Content, "exit code 7") {
		t.Errorf("content = %q, want the exit code", res.Content)
	}
}

func TestBashTruncatesLongOutput(t *testing.T) {
	w := newComputer(t)
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
	w := newComputer(t)
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
	w := newComputer(t)
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
	w := newComputer(t)
	start := time.Now()
	res := w.call("bash", map[string]any{"command": "sleep 30 & sleep 30", "timeout": 1})
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("result = %+v, want a timeout", res)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the call took %s, want the process group killed promptly", elapsed)
	}
}
