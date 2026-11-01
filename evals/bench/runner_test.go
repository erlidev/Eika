package bench_test

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/bench"
)

func TestOpenRefusesWhenAnotherContainerHoldsTheGPUAndChangesNothing(t *testing.T) {
	h := newFakeHost(t)
	h.swapped()
	_, err := openRunner(t, h, nil)
	var busy *bench.BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("Open = %v, want a BusyError", err)
	}
	if !strings.Contains(err.Error(), "llama-server") || !strings.Contains(err.Error(), "EIKA_BENCH_TAKE") {
		t.Errorf("error = %q, want it to name llama-server and the option", err)
	}
	if m := h.mutations(); len(m) != 0 {
		t.Errorf("a refused run changed the server: %v", m)
	}
}

func TestATakenContainerIsStoppedAndStartedAgainAtTheEnd(t *testing.T) {
	h := newFakeHost(t)
	h.swapped()
	before := h.runningNames()
	path := journalPath(t)
	r, err := openRunner(t, h, func(o *bench.Options) { o.Take = []string{"llama-server"}; o.JournalPath = path })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.onStop = func(name string) {
		// Taking llama-server is journalled as stopped; llama-qwen, which the
		// run started and stops again, as started.
		if j := readJournalFile(t, path); !slices.Contains(j.Stopped, name) && !slices.Contains(j.Started, name) {
			t.Errorf("%s was stopped before the journal named it: stopped %v, started %v", name, j.Stopped, j.Started)
		}
	}
	s, err := r.Serve(t.Context(), bench.Baseline())
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := h.runningNames(); !slices.Equal(got, []string{"caddy", "llama-qwen"}) {
		t.Errorf("running while serving = %v", got)
	}
	if s.URL == "" || s.Info.Container != "llama-qwen" {
		t.Errorf("serving = %+v", s)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.runningNames(); !slices.Equal(got, before) {
		t.Errorf("running after Close = %v, want %v", got, before)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the journal survived a restore: %v", err)
	}
	if _, locked := h.files["lock"]; locked {
		t.Error("the lock survived a restore")
	}
}

func TestAVariantIsAContainerOfItsOwnAndTheChatContainerIsNeverEdited(t *testing.T) {
	h := newFakeHost(t)
	h.normal()
	chat := h.get("llama-qwen")
	id, cmd := chat.id, slices.Clone(chat.cmd)
	r, err := openRunner(t, h, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := r.Serve(t.Context(), bench.Variant("no-cache-idle-slots", bench.AddFlag("--no-cache-idle-slots"))); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	v := h.get("eika-bench-abc123")
	if v == nil || !v.running || v.restart != "no" || v.labels["eika.bench"] != "abc123" {
		t.Fatalf("variant = %+v, want a running container with no restart policy and the run's label", v)
	}
	if want := append(slices.Clone(cmd), "--no-cache-idle-slots"); !slices.Equal(v.cmd, want) {
		t.Errorf("variant flags = %v, want %v", v.cmd, want)
	}
	if chat.running {
		t.Error("llama-qwen kept running beside the variant")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.get("eika-bench-abc123") != nil {
		t.Error("the variant survived Close")
	}
	if now := h.get("llama-qwen"); now == nil || now.id != id || !now.running || !slices.Equal(now.cmd, cmd) {
		t.Errorf("llama-qwen = %+v, want the same container running with the same flags", now)
	}
	for _, c := range h.ran() {
		for _, banned := range []string{"command.txt", "komodo.toml", "docker rm llama-qwen", "docker rm -f llama-qwen", "docker update"} {
			if strings.Contains(c, banned) {
				t.Errorf("command %q touches the Deployment", c)
			}
		}
	}
}

func TestAVariantThatExitsFailsLoudlyAndTheBenchIsRestored(t *testing.T) {
	h := newFakeHost(t)
	h.normal()
	r, err := openRunner(t, h, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err = r.Serve(t.Context(), bench.Variant("bad", bench.AddFlag("--bogus-flag")))
	if err == nil || !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("Serve = %v, want the container's last log lines", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.runningNames(); !slices.Equal(got, []string{"caddy", "llama-qwen"}) || h.get("eika-bench-abc123") != nil {
		t.Errorf("after Close running = %v", got)
	}
}

func TestARestoreThatFailsSaysSoAndLeavesTheJournalForTheNextRun(t *testing.T) {
	h := newFakeHost(t)
	h.swapped()
	path := journalPath(t)
	open := func() (*bench.Runner, error) {
		return openRunner(t, h, func(o *bench.Options) { o.Take = []string{"llama-server"}; o.JournalPath = path })
	}
	r, err := open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := r.Serve(t.Context(), bench.Baseline()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	h.fail = func(c string) error {
		if strings.HasPrefix(c, "docker start llama-server") {
			return &bench.ExecError{ExitCode: 1, Stderr: "cannot start"}
		}
		return nil
	}
	err = r.Close()
	var re *bench.RestoreError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "BENCH NOT RESTORED") || !strings.Contains(err.Error(), "llama-server") || re.WatchdogAt.IsZero() {
		t.Fatalf("Close = %v, want a loud RestoreError naming llama-server and the watchdog", err)
	}
	h.fail = nil
	if _, err := open(); err == nil || !errors.As(err, new(*bench.LeftModifiedError)) {
		t.Fatalf("a later Open = %v, want a LeftModifiedError", err)
	}
	if _, err := bench.Recover(t.Context(), h, bench.Options{Host: "fake", JournalPath: path, Now: h.clock, Sleep: h.sleep}); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if got := h.runningNames(); !slices.Equal(got, []string{"caddy", "llama-server"}) {
		t.Errorf("running after Recover = %v", got)
	}
	if r2, err := open(); err != nil {
		t.Errorf("Open after Recover: %v", err)
	} else {
		_ = r2.Close()
	}
}

func TestALostConnectionIsReopenedToRestore(t *testing.T) {
	h := newFakeHost(t)
	h.normal()
	r, err := openRunner(t, h, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := r.Serve(t.Context(), bench.Variant("v", bench.AddFlag("--no-cache-idle-slots"))); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	h.mu.Lock()
	h.down = true
	h.mu.Unlock()
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if h.reconnects == 0 || !slices.Equal(h.runningNames(), []string{"caddy", "llama-qwen"}) {
		t.Errorf("reconnects = %d, running = %v", h.reconnects, h.runningNames())
	}
}

func TestAConnectionThatCannotBeReopenedLeavesTheWatchdogToRestore(t *testing.T) {
	h := newFakeHost(t)
	h.normal()
	r, err := openRunner(t, h, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(h.watchdogs) != 1 {
		t.Fatalf("watchdogs started = %d, want 1", len(h.watchdogs))
	}
	h.mu.Lock()
	h.down, h.reconnectFails = true, true
	h.mu.Unlock()
	var re *bench.RestoreError
	if err := r.Close(); !errors.As(err, &re) || !re.Lost || re.WatchdogAt.IsZero() {
		t.Fatalf("Close = %v, want a RestoreError that says the connection is lost", err)
	}
}

func TestASecondRunIsRefusedWhileTheFirstHoldsTheLock(t *testing.T) {
	h := newFakeHost(t)
	h.normal()
	r, err := openRunner(t, h, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()
	if _, err := openRunner(t, h, nil); !errors.As(err, new(*bench.LeftModifiedError)) {
		t.Fatalf("second Open = %v, want a LeftModifiedError", err)
	}
}

func TestTheWatchdogScriptIsValidShellAndNamesWhatItRestores(t *testing.T) {
	h := newFakeHost(t)
	h.swapped()
	r, err := openRunner(t, h, func(o *bench.Options) { o.Take = []string{"llama-server"} })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = r.Close() }()
	if len(h.watchdogs) != 1 {
		t.Fatalf("watchdogs = %d", len(h.watchdogs))
	}
	w := h.watchdogs[0]
	for _, want := range []string{"docker rm -f eika-bench-abc123", "docker stop -t 30 llama-qwen", "docker start llama-server"} {
		if !strings.Contains(w, want) {
			t.Errorf("watchdog lacks %q", want)
		}
	}
}

func TestFlagsEditWithoutChangingTheRecordedList(t *testing.T) {
	base := bench.Flags{"-np", "2", "--kv-unified", "--ctx-size", "131072"}
	got, err := base.Apply(bench.DropFlag("--kv-unified"), bench.AddFlag("--no-kv-unified"), bench.SetFlag("-np", "1"))
	if err != nil || !slices.Equal(got, bench.Flags{"-np", "1", "--ctx-size", "131072", "--no-kv-unified"}) {
		t.Errorf("Apply = %v, %v", got, err)
	}
	if !slices.Equal(base, bench.Flags{"-np", "2", "--kv-unified", "--ctx-size", "131072"}) {
		t.Errorf("the recorded flags changed: %v", base)
	}
	if _, err := base.Apply(bench.DropFlag("--nope")); err == nil {
		t.Error("dropping a flag that is not there succeeded")
	}
}

func TestRedactHidesTheValueOfACredentialFlag(t *testing.T) {
	got := bench.Flags{"--api-key", "sekret", "--api-key-file=/x", "--port", "8080"}.Redact()
	if strings.Contains(got.String(), "sekret") || strings.Contains(got.String(), "/x") || !strings.Contains(got.String(), "8080") {
		t.Errorf("Redact = %v", got)
	}
}

func TestCommandQuotesWhatTheShellWouldSplit(t *testing.T) {
	if got, want := bench.Command("docker", "ps", "--format", "{{.ID}}|x", "it's"), `docker ps --format '{{.ID}}|x' 'it'\''s'`; got != want {
		t.Errorf("Command = %s, want %s", got, want)
	}
}

func TestParseServerLogReadsFinishedTasks(t *testing.T) {
	log := `943.18.321.777 I slot launch_slot_: id  0 | task 53596 | processing task, is_child = 0
943.18.743.241 I slot print_timing: id  0 | task 53596 | prompt eval time =     143.32 ms /    57 tokens (    2.51 ms per token,   397.71 tokens per second)
943.18.743.243 I slot print_timing: id  0 | task 53596 |        eval time =     278.05 ms /    23 tokens (   12.64 ms per token,    79.12 tokens per second)
943.18.743.243 I slot print_timing: id  0 | task 53596 |       total time =     421.38 ms /    80 tokens
943.18.743.269 I slot      release: id  0 | task 53596 | stop processing: n_tokens = 462, truncated = 0
943.19.246.984 I slot      release: id  1 | task 53700 | stop processing: n_tokens = 9, truncated = 0
`
	got := bench.ParseServerLog(log)
	want := []bench.LogTask{{Task: 53596, Slot: 0, Prefilled: 57, Completion: 23, Held: 462}}
	if !slices.Equal(got, want) {
		t.Errorf("ParseServerLog = %+v, want %+v (a release without its counts is left out)", got, want)
	}
}
