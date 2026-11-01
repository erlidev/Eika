package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Options configure a run. The zero value of every field but JournalPath and
// Host is the bench's own setting.
type Options struct {
	// Host names the server for the report; the Transport already reaches it.
	Host string
	// Chat is the container of the chat model (llama-qwen), and Tabby the
	// second backend's (tabbyapi).
	Chat, Tabby string
	// Take names the containers besides Chat that the run may stop while it
	// holds the GPU and starts again at its end. Nothing else is ever stopped.
	Take []string
	// EnvFile is the file on the server that holds the chat model's API key,
	// read by Docker when it creates a variant: the runner never reads it.
	EnvFile string
	// TabbyConfig is TabbyAPI's config.yml on the server, of which the runner
	// reads the model section for the report. The key file is never read.
	TabbyConfig string
	// JournalPath is where the run's journal lives; empty means
	// DefaultJournalPath.
	JournalPath string
	// Logger receives the run's account of what it changes. Nil means
	// slog.Default.
	Logger *slog.Logger
	// NoSignals leaves SIGINT and SIGTERM alone; the tests use it.
	NoSignals bool
	// Timeouts bound the waits; zero fields take the defaults.
	Timeouts Timeouts
	// Now and Sleep replace the clock; nil means the real one.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
	// NewID makes the run id; nil means a random 6-digit hex string.
	NewID func() string
}

// Timeouts are the bounds of the runner's waits.
type Timeouts struct {
	// Health bounds the wait for a model to load and answer. A 27B model reads
	// 17 GB from disk, so the default is generous.
	Health time.Duration
	// GPUFree bounds the wait for the GPU to empty after a stop.
	GPUFree time.Duration
	// Poll is how often the runner looks while it waits.
	Poll time.Duration
	// Heartbeat is how often the run tells the server's watchdog it is alive,
	// and Stale how long the watchdog waits without hearing before it
	// restores the bench itself.
	Heartbeat, Stale time.Duration
	// Restore bounds a restore as a whole.
	Restore time.Duration
	// Grace is how long, after an interrupt, the runner waits for the code
	// that holds it to return and call Close before it restores on its own.
	Grace time.Duration
}

func (o *Options) defaults() {
	if o.Host == "" {
		o.Host = "ubuntu-5090"
	}
	if o.Chat == "" {
		o.Chat = "llama-qwen"
	}
	if o.Tabby == "" {
		o.Tabby = "tabbyapi"
	}
	if o.EnvFile == "" {
		o.EnvFile = "/opt/stacks/llama-qwen/.env"
	}
	if o.TabbyConfig == "" {
		o.TabbyConfig = "/opt/stacks/tabbyapi/config.yml"
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if o.NewID == nil {
		o.NewID = func() string { return strconv.FormatInt(time.Now().UnixNano()&0xffffff, 16) }
	}
	t := &o.Timeouts
	for _, d := range []struct {
		field *time.Duration
		value time.Duration
	}{
		{&t.Health, 10 * time.Minute}, {&t.GPUFree, time.Minute}, {&t.Poll, 3 * time.Second},
		{&t.Heartbeat, 20 * time.Second}, {&t.Stale, 3 * time.Minute}, {&t.Restore, 15 * time.Minute},
		{&t.Grace, 30 * time.Second},
	} {
		if *d.field == 0 {
			*d.field = d.value
		}
	}
}

// BusyError is the refusal to start a run: something other than the chat model
// holds the GPU.
type BusyError struct {
	// Holders are the processes found on the GPU that the run may not take.
	Holders []Holder
	// Allowed are the containers that may hold it.
	Allowed []string
}

// Error says what holds the GPU and how to have the run take it.
func (e *BusyError) Error() string {
	var what []string
	for _, h := range e.Holders {
		who := h.Container
		if who == "" {
			who = "a process outside Docker"
		}
		what = append(what, fmt.Sprintf("%s (%s, pid %d, %d MiB)", who, h.Process, h.PID, h.MiB))
	}
	return fmt.Sprintf("the GPU is held by %s, and a run may only find %s there. The run stops nothing on its own: "+
		"to have it stop a container and start it again at the end, name the container in EIKA_BENCH_TAKE (Options.Take)",
		strings.Join(what, ", "), strings.Join(e.Allowed, " or "))
}

// LeftModifiedError means an earlier run left the bench modified, or another
// run holds it.
type LeftModifiedError struct {
	// Reason says what was found.
	Reason string
	// Differences are what differs from the earlier run's snapshot.
	Differences []string
}

// Error says what was found and the command that puts the bench back.
func (e *LeftModifiedError) Error() string {
	msg := "the bench is not as a run found it: " + e.Reason
	if len(e.Differences) > 0 {
		msg += " (" + strings.Join(e.Differences, "; ") + ")"
	}
	return msg + ". Run `go run ./evals/benchctl restore` to put it back, or `status` to look first"
}

// RestoreError is a restore that did not put the bench back.
type RestoreError struct {
	// Failures are the steps that failed, and what still differs.
	Failures []string
	// Lost is true when the connection could not be reopened.
	Lost bool
	// WatchdogAt is when the server's watchdog restores the bench if the
	// heartbeat is not resumed; zero if the watchdog was not started.
	WatchdogAt time.Time
}

// Error is loud on purpose.
func (e *RestoreError) Error() string {
	msg := "BENCH NOT RESTORED: " + strings.Join(e.Failures, "; ")
	if e.Lost {
		msg += "; the connection to the server is lost"
	}
	if !e.WatchdogAt.IsZero() {
		msg += fmt.Sprintf("; the watchdog on the server restores it at about %s unless a later `benchctl restore` does", e.WatchdogAt.Format("15:04:05Z"))
	}
	return msg
}

// Runner holds the bench for one run. Open it, serve models with Serve, read
// the server with Logs and GPU, and Close it, also when the run failed.
type Runner struct {
	tr      Transport
	opt     Options
	run     string
	allowed map[string]bool
	before  Snapshot
	journal *journalFile
	ctx     context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	serving  *Serving
	opened   bool
	closing  bool
	closeErr error
	closed   chan struct{}
	beatStop chan struct{}
	beatDone chan struct{}
	sigStop  chan struct{}
}

// Serving is a model the runner has started and reached.
type Serving struct {
	// Spec is what was asked for.
	Spec Spec
	// Container is the container that serves it.
	Container string
	// URL is the base URL of its API on this machine, through the run's SSH
	// connection.
	URL string
	// Info describes what runs, for the report.
	Info Info

	created bool
	started bool
}

// Open connects the run to the bench. It reads and records what the server
// holds, refuses with a *BusyError when anything but the chat model holds the
// GPU, refuses with a *LeftModifiedError when an earlier run left the server
// changed or another run is on it, and only then takes the run's lock, writes
// its journal and starts the watchdog on the server. It stops no container.
func Open(ctx context.Context, tr Transport, opt Options) (*Runner, error) {
	opt.defaults()
	for _, n := range append([]string{opt.Chat, opt.Tabby}, opt.Take...) {
		if err := checkName(n); err != nil {
			return nil, err
		}
	}
	if opt.JournalPath == "" {
		p, err := DefaultJournalPath()
		if err != nil {
			return nil, err
		}
		opt.JournalPath = p
	}
	r := &Runner{tr: tr, opt: opt, run: opt.NewID(), allowed: map[string]bool{opt.Chat: true}, closed: make(chan struct{})}
	for _, n := range opt.Take {
		r.allowed[n] = true
	}
	if err := r.checkLeftover(ctx); err != nil {
		return nil, err
	}
	snap, err := r.snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("record the bench: %w", err)
	}
	r.before = snap
	r.log().Info("bench recorded", "run", r.run, "host", opt.Host, "gpu_used_mib", snap.GPU.UsedMiB, "holders", holderNames(snap.GPU), "running", runningNames(snap))
	if err := r.refuse(snap.GPU); err != nil {
		return nil, err
	}

	// The first change to the server: the lock, which says who holds it.
	if _, err := tr.Exec(ctx, "sh", "-c", "mkdir "+lockDir+" && echo "+r.run+" > "+lockDir+"/run"); err != nil {
		return nil, &LeftModifiedError{Reason: "another run took the bench's lock while this one opened (" + err.Error() + ")"}
	}
	r.journal = &journalFile{path: opt.JournalPath, j: Journal{
		Run: r.run, Host: opt.Host, StartedAt: opt.Now(), Before: snap, Chat: opt.Chat, Tabby: opt.Tabby,
	}}
	if err := r.journal.update(func(*Journal) {}); err != nil {
		_, _ = tr.Exec(ctx, "rm", "-rf", lockDir)
		return nil, err
	}
	if err := r.startWatchdog(ctx); err != nil {
		_, _ = tr.Exec(ctx, "rm", "-rf", lockDir)
		_ = r.journal.remove()
		return nil, err
	}
	r.opened = true
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.beatStop, r.beatDone = make(chan struct{}), make(chan struct{})
	go r.heartbeat()
	if !opt.NoSignals {
		r.sigStop = make(chan struct{})
		r.watchSignals()
	}
	return r, nil
}

func (r *Runner) log() *slog.Logger { return r.opt.Logger }

// Context is cancelled when the run is interrupted. Work done for the run
// should use it, so that an interrupt stops the work and lets Close run.
func (r *Runner) Context() context.Context { return r.ctx }

// Run returns the id of the run.
func (r *Runner) Run() string { return r.run }

// Before returns the snapshot the run will restore.
func (r *Runner) Before() Snapshot { return r.before }

// snapshot reads the running containers, the ones the options name and the
// GPU.
func (r *Runner) snapshot(ctx context.Context) (Snapshot, error) {
	running, err := r.running(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	names := map[string]bool{r.opt.Chat: true, r.opt.Tabby: true}
	for _, n := range r.opt.Take {
		names[n] = true
	}
	for _, c := range running {
		names[c.Name] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	s := Snapshot{At: r.opt.Now()}
	for _, n := range sorted {
		c, err := r.inspect(ctx, n)
		if err != nil {
			return Snapshot{}, err
		}
		s.Containers = append(s.Containers, c)
	}
	if s.GPU, err = r.gpu(ctx, running); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// refuse is the guard: only the chat model and the containers named in the
// options may hold the GPU.
func (r *Runner) refuse(g GPU) error {
	var bad []Holder
	for _, h := range g.Holders {
		if h.Container == "" || !r.allowed[h.Container] {
			bad = append(bad, h)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(r.allowed))
	for n := range r.allowed {
		allowed = append(allowed, n)
	}
	sort.Strings(allowed)
	return &BusyError{Holders: bad, Allowed: allowed}
}

// checkLeftover looks for a bench an earlier run left changed: a journal on
// this machine, the lock on the server, or a container a variant left.
func (r *Runner) checkLeftover(ctx context.Context) error {
	if j, err := ReadJournal(r.opt.JournalPath); err == nil {
		diffs, derr := r.differences(ctx, j.Before, j.Created)
		switch {
		case derr != nil:
			return fmt.Errorf("check the journal of run %s: %w", j.Run, derr)
		case len(diffs) > 0:
			return &LeftModifiedError{Reason: fmt.Sprintf("run %s (started %s) did not restore it", j.Run, j.StartedAt.Format(time.RFC3339)), Differences: diffs}
		}
		if _, lerr := r.tr.Exec(ctx, "test", "-e", lockDir); lerr == nil {
			return &LeftModifiedError{Reason: fmt.Sprintf("run %s still holds the lock on the server", j.Run)}
		}
		r.log().Warn("removing the journal of an earlier run, whose bench matches its snapshot again", "run", j.Run)
		if err := os.Remove(r.opt.JournalPath); err != nil {
			return fmt.Errorf("remove the old journal: %w", err)
		}
	} else if !errors.Is(err, ErrNoJournal) {
		return err
	}
	if out, err := r.tr.Exec(ctx, "cat", lockDir+"/run"); err == nil {
		return &LeftModifiedError{Reason: "run " + strings.TrimSpace(out) + " holds the bench's lock on the server"}
	}
	out, err := r.tr.Exec(ctx, "docker", "ps", "-a", "--filter", "label=eika.bench", "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("look for containers of an earlier run: %w", err)
	}
	if left := strings.Fields(out); len(left) > 0 {
		return &LeftModifiedError{Reason: "containers of an earlier run are still there: " + strings.Join(left, ", ")}
	}
	return nil
}

// differences compares the server with a snapshot: what ran then must run
// now as the same container with the same flags, what did not run must not,
// the GPU must be held by the same containers, and no container made by a run
// may be left.
func (r *Runner) differences(ctx context.Context, want Snapshot, created []string) ([]string, error) {
	got, err := r.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	var diffs []string
	for _, w := range want.Containers {
		g, ok := got.container(w.Name)
		switch {
		case !ok:
			diffs = append(diffs, w.Name+" is no longer listed")
		case w.Running() && !g.Running():
			diffs = append(diffs, w.Name+" ran and does not")
		case !w.Running() && g.Running():
			diffs = append(diffs, w.Name+" was stopped and runs")
		case !w.Absent && !g.Absent && w.ID != g.ID:
			diffs = append(diffs, w.Name+" is a different container ("+short(g.ID)+", was "+short(w.ID)+")")
		case !w.Absent && !g.Absent && !slices.Equal(w.Flags, g.Flags):
			diffs = append(diffs, w.Name+" has other flags")
		}
	}
	for _, c := range got.Containers {
		if _, ok := want.container(c.Name); !ok && c.Running() {
			diffs = append(diffs, c.Name+" runs and did not before")
		}
	}
	for _, name := range created {
		if c, err := r.inspect(ctx, name); err == nil && !c.Absent {
			diffs = append(diffs, name+" still exists")
		}
	}
	if a, b := holderNames(want.GPU), holderNames(got.GPU); !slices.Equal(a, b) {
		diffs = append(diffs, fmt.Sprintf("the GPU is held by %v, was %v", b, a))
	}
	return diffs, nil
}

// short abbreviates a container id for a message.
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// holderNames lists who holds the GPU, sorted, naming a process outside
// Docker by its executable.
func holderNames(g GPU) []string {
	var names []string
	for _, h := range g.Holders {
		n := h.Container
		if n == "" {
			n = h.Process
		}
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// runningNames lists the containers running in a snapshot.
func runningNames(s Snapshot) []string {
	var names []string
	for _, c := range s.Containers {
		if c.Running() {
			names = append(names, c.Name)
		}
	}
	return names
}

// startWatchdog writes the beat and starts the restore script on the server,
// detached, so it outlives the connection and the process.
func (r *Runner) startWatchdog(ctx context.Context) error {
	plan := watchdogPlan{Run: r.run, Created: variantName(r.run)}
	for _, n := range []string{r.opt.Chat, r.opt.Tabby} {
		if c, ok := r.before.container(n); ok && !c.Absent && !c.Running() {
			plan.Stop = append(plan.Stop, n)
		}
	}
	for _, c := range r.before.Containers {
		if c.Running() && r.allowed[c.Name] {
			plan.Start = append(plan.Start, c.Name)
		}
	}
	if _, err := r.tr.Exec(ctx, "touch", beatFile(r.run)); err != nil {
		return fmt.Errorf("write the heartbeat: %w", err)
	}
	launch := "nohup setsid sh -c " + quote(plan.script()) + " </dev/null >/dev/null 2>&1 &"
	if _, err := r.tr.Exec(ctx, "sh", "-c", launch); err != nil {
		return fmt.Errorf("start the watchdog: %w", err)
	}
	return nil
}

// heartbeat tells the watchdog the run is alive until the run is closed.
func (r *Runner) heartbeat() {
	defer close(r.beatDone)
	for {
		select {
		case <-r.beatStop:
			return
		case <-time.After(r.opt.Timeouts.Heartbeat):
		}
		ctx, cancel := context.WithTimeout(context.Background(), r.opt.Timeouts.Heartbeat)
		if _, err := r.tr.Exec(ctx, "touch", beatFile(r.run)); err != nil {
			r.log().Warn("heartbeat failed; the watchdog restores the bench if this goes on", "run", r.run, "error", err)
		}
		cancel()
	}
}

// watchSignals restores the bench when the process is interrupted: it cancels
// the run's context, gives the code that holds the runner a grace period to
// return and call Close, and otherwise restores and lets the signal take its
// course.
func (r *Runner) watchSignals() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(ch)
		select {
		case <-r.sigStop:
			return
		case sig := <-ch:
			r.log().Error("INTERRUPTED: stopping the run and restoring the bench", "run", r.run, "signal", sig.String())
			r.cancel()
			select {
			case <-r.closed:
			case <-time.After(r.opt.Timeouts.Grace):
				_ = r.Close()
			}
			// A second signal while restoring was swallowed on purpose; now
			// the first one takes its course.
			signal.Stop(ch)
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(sig)
			}
		}
	}()
}

// Serve makes the server serve spec and returns how to reach it. Anything
// the run served before is retired first. It stops the containers on the GPU
// that the run may take (the chat model, and the ones named in Options.Take),
// each recorded in the journal before it is stopped, and refuses with a
// *BusyError for any other holder, also one that appeared since Open.
func (r *Runner) Serve(ctx context.Context, spec Spec) (*Serving, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opened || r.closing {
		return nil, errors.New("serve: the run is closed")
	}
	if err := r.retire(ctx); err != nil {
		return nil, err
	}
	target := r.opt.Chat
	switch spec.Kind {
	case KindTabby:
		target = r.opt.Tabby
	case KindVariant:
		target = variantName(r.run)
	case KindChat:
	default:
		return nil, fmt.Errorf("serve %s: unknown kind %q", spec.Name, spec.Kind)
	}

	chat, err := r.inspect(ctx, r.opt.Chat)
	if err != nil {
		return nil, err
	}
	if spec.Kind != KindTabby && chat.Absent {
		return nil, fmt.Errorf("serve %s: there is no container %s to serve or to copy", spec.Name, r.opt.Chat)
	}
	var flags Flags
	if spec.Kind == KindVariant {
		if flags, err = chat.rawFlags.Apply(spec.Edits...); err != nil {
			return nil, fmt.Errorf("serve %s: %w", spec.Name, err)
		}
	}

	// Stop what holds the GPU, as far as the run may.
	running, err := r.running(ctx)
	if err != nil {
		return nil, err
	}
	g, err := r.gpu(ctx, running)
	if err != nil {
		return nil, err
	}
	if err := r.refuse(g); err != nil {
		return nil, err
	}
	stopped := false
	for _, name := range holderNames(g) {
		if name == target {
			continue
		}
		if err := r.stop(ctx, name); err != nil {
			return nil, err
		}
		stopped = true
	}
	if stopped {
		if err := r.waitGPU(ctx, func(g GPU) bool {
			return len(g.Holders) == 0 || (len(holderNames(g)) == 1 && holderNames(g)[0] == target)
		}); err != nil {
			return nil, err
		}
	}

	s := &Serving{Spec: spec, Container: target}
	was, err := r.inspect(ctx, target)
	if err != nil {
		return nil, err
	}
	switch spec.Kind {
	case KindChat, KindTabby:
		if was.Absent {
			return nil, fmt.Errorf("serve %s: there is no container %s to start", spec.Name, target)
		}
		if !was.Running() {
			if !r.before.wasRunning(target) {
				s.started = true
				if err := r.journal.update(func(j *Journal) { j.Started = appendOnce(j.Started, target) }); err != nil {
					return nil, err
				}
			} else if err := r.journal.update(func(j *Journal) { j.Stopped = appendOnce(j.Stopped, target) }); err != nil {
				return nil, err
			}
			r.log().Info("starting a container the run did not create", "container", target, "restart_policy", was.Restart)
			if _, err := r.tr.Exec(ctx, "docker", "start", target); err != nil {
				return nil, fmt.Errorf("start %s: %w", target, err)
			}
		}
	case KindVariant:
		if err := r.createVariant(ctx, s, chat, flags); err != nil {
			return nil, err
		}
	}
	r.serving = s

	port, path, anyAnswer := 8080, "/health", false
	if spec.Kind == KindTabby {
		port, path, anyAnswer = 5000, "/health", true
	} else if v, ok := chat.rawFlags.Value("--port"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	c, err := r.waitHealthy(ctx, target, port, path, anyAnswer)
	if err != nil {
		return nil, err
	}
	local, err := r.tr.Forward(ctx, fmt.Sprintf("%s:%d", c.IP, port))
	if err != nil {
		return nil, err
	}
	s.URL = "http://" + local
	s.Info = Info{
		Run: r.run, Host: r.opt.Host, Spec: spec.Name, Container: target, Image: c.Image, ImageID: c.ImageID,
		Flags: c.Flags,
	}
	for _, e := range spec.Edits {
		s.Info.Edits = append(s.Info.Edits, e.String())
	}
	if spec.Kind == KindTabby {
		if s.Info.Config, err = r.tabbyConfig(ctx); err != nil {
			return nil, err
		}
	}
	if g, err := r.gpu(ctx, nil); err == nil {
		s.Info.GPUUsedMiB = g.UsedMiB
	}
	r.log().Info("serving", "info", s.Info.String(), "url", s.URL)
	return s, nil
}

// wasRunning reports whether the container ran when the run began.
func (s Snapshot) wasRunning(name string) bool {
	c, ok := s.container(name)
	return ok && c.Running()
}

// appendOnce appends name to list unless it is there.
func appendOnce(list []string, name string) []string {
	if slices.Contains(list, name) {
		return list
	}
	return append(list, name)
}

// stop stops a container the run may take, writing it to the journal first.
func (r *Runner) stop(ctx context.Context, name string) error {
	if !r.allowed[name] {
		return &BusyError{Holders: []Holder{{Container: name}}, Allowed: r.allowedNames()}
	}
	if err := r.journal.update(func(j *Journal) { j.Stopped = appendOnce(j.Stopped, name) }); err != nil {
		return err
	}
	r.log().Warn("stopping a container to free the GPU; the run starts it again at its end", "container", name)
	if _, err := r.tr.Exec(ctx, "docker", "stop", "-t", "30", name); err != nil {
		return fmt.Errorf("stop %s: %w", name, err)
	}
	return nil
}

func (r *Runner) allowedNames() []string {
	var names []string
	for n := range r.allowed {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// createVariant runs the chat model's image and mounts as a container of its
// own with flags. The container publishes no port, has no restart policy, so
// that a reboot never brings it up beside the Deployment, and carries the
// run's label, which a later run finds if this one is lost.
func (r *Runner) createVariant(ctx context.Context, s *Serving, chat Container, flags Flags) error {
	out, err := r.tr.Exec(ctx, "docker", "inspect", chat.Name, "--format", "{{json .HostConfig.Binds}}|{{.HostConfig.NetworkMode}}")
	if err != nil {
		return fmt.Errorf("read the mounts of %s: %w", chat.Name, err)
	}
	binds, network, _ := strings.Cut(strings.TrimSpace(out), "|")
	var mounts []string
	if err := json.Unmarshal([]byte(binds), &mounts); err != nil && binds != "null" {
		return fmt.Errorf("read the mounts of %s: %w", chat.Name, err)
	}
	argv := []string{"docker", "run", "-d", "--name", s.Container, "--label", "eika.bench=" + r.run,
		"--network", network, "--restart", "no", "--gpus", "all", "--env-file", r.opt.EnvFile}
	for _, m := range mounts {
		argv = append(argv, "-v", m)
	}
	argv = append(argv, chat.Image)
	argv = append(argv, flags...)
	if err := r.journal.update(func(j *Journal) { j.Created = appendOnce(j.Created, s.Container) }); err != nil {
		return err
	}
	s.created = true
	r.log().Info("creating a container for a changed model", "container", s.Container, "edits", s.Spec.Edits)
	if _, err := r.tr.Exec(ctx, argv...); err != nil {
		return fmt.Errorf("create %s: %w", s.Container, err)
	}
	return nil
}

// retire ends what Serve started: a variant is removed, a container the run
// started that did not run before is stopped. The containers the run stopped
// stay stopped until Close.
func (r *Runner) retire(ctx context.Context) error {
	s := r.serving
	if s == nil {
		return nil
	}
	r.serving = nil
	switch {
	case s.created:
		if _, err := r.tr.Exec(ctx, "docker", "rm", "-f", s.Container); err != nil {
			return fmt.Errorf("remove %s: %w", s.Container, err)
		}
		return r.journal.update(func(j *Journal) {
			j.Created = slices.DeleteFunc(j.Created, func(n string) bool { return n == s.Container })
		})
	case s.started:
		if _, err := r.tr.Exec(ctx, "docker", "stop", "-t", "30", s.Container); err != nil {
			return fmt.Errorf("stop %s: %w", s.Container, err)
		}
		return r.journal.update(func(j *Journal) {
			j.Started = slices.DeleteFunc(j.Started, func(n string) bool { return n == s.Container })
		})
	}
	return nil
}

// waitGPU polls until done holds for the GPU or the bound passes.
func (r *Runner) waitGPU(ctx context.Context, done func(GPU) bool) error {
	deadline := r.opt.Now().Add(r.opt.Timeouts.GPUFree)
	for {
		running, err := r.running(ctx)
		if err != nil {
			return err
		}
		g, err := r.gpu(ctx, running)
		if err != nil {
			return err
		}
		if done(g) {
			return nil
		}
		if r.opt.Now().After(deadline) {
			return fmt.Errorf("the GPU did not empty within %s: held by %v", r.opt.Timeouts.GPUFree, holderNames(g))
		}
		if err := r.opt.Sleep(ctx, r.opt.Timeouts.Poll); err != nil {
			return err
		}
	}
}

// waitHealthy polls a model's health route, through the server's own network,
// until it answers or the bound passes, and fails at once if the container
// stops. anyAnswer accepts any HTTP status: TabbyAPI listens only once its
// model has loaded.
func (r *Runner) waitHealthy(ctx context.Context, name string, port int, path string, anyAnswer bool) (Container, error) {
	deadline := r.opt.Now().Add(r.opt.Timeouts.Health)
	for {
		c, err := r.inspect(ctx, name)
		if err != nil {
			return Container{}, err
		}
		if c.Absent || !c.Running() {
			return Container{}, fmt.Errorf("%s is %s while loading; its last log lines: %s", name, c.Status, r.tail(ctx, name))
		}
		if c.IP != "" {
			url := fmt.Sprintf("http://%s:%d%s", c.IP, port, path)
			argv := []string{"curl", "-sS", "-m", "5", "-o", "/dev/null", "-w", "%{http_code}", url}
			if !anyAnswer {
				argv = append(argv[:1], append([]string{"-f"}, argv[1:]...)...)
			}
			out, err := r.tr.Exec(ctx, argv...)
			if err == nil && (!anyAnswer || strings.TrimSpace(out) != "000") {
				return c, nil
			}
		}
		if r.opt.Now().After(deadline) {
			return Container{}, fmt.Errorf("%s did not answer on %s within %s; its last log lines: %s", name, path, r.opt.Timeouts.Health, r.tail(ctx, name))
		}
		if err := r.opt.Sleep(ctx, r.opt.Timeouts.Poll); err != nil {
			return Container{}, err
		}
	}
}

// tail reads the last lines of a container's log, for a message.
func (r *Runner) tail(ctx context.Context, name string) string {
	out, err := r.tr.Exec(ctx, "sh", "-c", "docker logs --tail 15 "+name+" 2>&1")
	if err != nil {
		return "(unreadable: " + err.Error() + ")"
	}
	return strings.ReplaceAll(strings.TrimSpace(out), "\n", " / ")
}

// Logs returns the log of a container since a time on the server's clock,
// with standard error merged in, where llama.cpp writes.
func (r *Runner) Logs(ctx context.Context, name string, since time.Time) (string, error) {
	if err := checkName(name); err != nil {
		return "", err
	}
	out, err := r.tr.Exec(ctx, "sh", "-c", "docker logs --since "+since.UTC().Format(time.RFC3339Nano)+" "+name+" 2>&1")
	if err != nil {
		return "", fmt.Errorf("read the log of %s: %w", name, err)
	}
	return out, nil
}

// ServerTime reads the server's clock, which docker logs --since is judged
// against.
func (r *Runner) ServerTime(ctx context.Context) (time.Time, error) {
	out, err := r.tr.Exec(ctx, "date", "-u", "+%Y-%m-%dT%H:%M:%SZ")
	if err != nil {
		return time.Time{}, fmt.Errorf("read the server's clock: %w", err)
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	if err != nil {
		return time.Time{}, fmt.Errorf("read the server's clock: %w", err)
	}
	return t, nil
}

// GPU reads the GPU now.
func (r *Runner) GPU(ctx context.Context) (GPU, error) {
	running, err := r.running(ctx)
	if err != nil {
		return GPU{}, err
	}
	return r.gpu(ctx, running)
}

// tabbyKeys are the lines of TabbyAPI's config.yml that go in the report. The
// file holds no key (they are in api_tokens.yml, which is never read), and the
// pattern names the settings anyway.
const tabbyKeys = `^[[:space:]]*(model_name|backend|max_seq_len|cache_size|cache_mode|chunk_size|max_batch_size|draft_mode|vision|port|disable_auth|reasoning):`

// tabbyConfig reads the model settings of TabbyAPI that the report states.
func (r *Runner) tabbyConfig(ctx context.Context) (map[string]string, error) {
	out, err := r.tr.Exec(ctx, "grep", "-E", tabbyKeys, r.opt.TabbyConfig)
	if err != nil {
		return nil, fmt.Errorf("read the model settings of %s: %w", r.opt.Tabby, err)
	}
	cfg := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(value, "#")
		cfg[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return cfg, nil
}

// Close puts the bench back as Open found it, and says loudly when it could
// not. It is safe to call twice and from a defer after a failure; the second
// call returns the first's result. It retires what the run served, removes
// the containers the run created, stops those it started that did not run
// before, waits for the GPU to empty, starts the containers it stopped and
// waits for them to answer, then compares the server with the snapshot. On
// success it deletes the journal and ends the watchdog; on failure it leaves
// both, and the watchdog restores the bench within Timeouts.Stale unless
// something does it sooner.
func (r *Runner) Close() error {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		<-r.closed
		return r.closeErr
	}
	r.closing = true
	r.mu.Unlock()
	defer close(r.closed)
	if r.sigStop != nil {
		defer close(r.sigStop)
	}
	r.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), r.opt.Timeouts.Restore)
	defer cancel()
	err := r.restore(ctx)
	if err != nil {
		var re *RestoreError
		if errors.As(err, &re) {
			re.WatchdogAt = r.opt.Now().Add(r.opt.Timeouts.Stale)
		}
		r.log().Error("BENCH NOT RESTORED", "run", r.run, "error", err)
		r.closeErr = err
		close(r.beatStop)
		<-r.beatDone
		return err
	}
	close(r.beatStop)
	<-r.beatDone
	r.finish(ctx)
	return nil
}

// finish ends the marks of a run whose restore was verified: the watchdog,
// the lock on the server and the journal here.
func (r *Runner) finish(ctx context.Context) {
	var tidy []error
	if _, e := r.tr.Exec(ctx, "touch", doneFile(r.run)); e != nil {
		tidy = append(tidy, e)
	}
	if _, e := r.tr.Exec(ctx, "rm", "-rf", lockDir); e != nil {
		tidy = append(tidy, e)
	}
	if e := r.journal.remove(); e != nil {
		tidy = append(tidy, e)
	}
	if len(tidy) > 0 {
		r.log().Warn("the bench is restored, but the run's marks were not all removed", "run", r.run, "error", errors.Join(tidy...))
	}
	r.log().Info("bench restored", "run", r.run)
}

// Recover puts the bench back from the journal an earlier run left, for a
// process that is not that run: `benchctl restore`. It does what Close does,
// from the journal's snapshot, and ends the earlier run's watchdog. It returns
// ErrNoJournal when no run left one.
func Recover(ctx context.Context, tr Transport, opt Options) (Journal, error) {
	opt.defaults()
	if opt.JournalPath == "" {
		p, err := DefaultJournalPath()
		if err != nil {
			return Journal{}, err
		}
		opt.JournalPath = p
	}
	j, err := ReadJournal(opt.JournalPath)
	if err != nil {
		return Journal{}, err
	}
	opt.Chat, opt.Tabby = j.Chat, j.Tabby
	r := &Runner{tr: tr, opt: opt, run: j.Run, before: j.Before, journal: &journalFile{path: opt.JournalPath, j: j}}
	ctx, cancel := context.WithTimeout(ctx, opt.Timeouts.Restore)
	defer cancel()
	if err := r.restore(ctx); err != nil {
		return j, err
	}
	r.finish(ctx)
	return j, nil
}

// restore is the restore of Close, and what `benchctl restore` runs for a
// journal. It is idempotent: it does only what the server still needs.
func (r *Runner) restore(ctx context.Context) error {
	return restoreFrom(ctx, r, r.journal.get())
}

// restoreFrom puts the server back to j.Before.
func restoreFrom(ctx context.Context, r *Runner, j Journal) error {
	var failures []string
	fail := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }
	lost := false

	// do runs one step, reopening the connection a few times when it is
	// lost, and records a failure that stays.
	do := func(what string, step func() error) {
		var err error
		for attempt := 0; attempt < 4; attempt++ {
			if err = step(); !Lost(err) {
				break
			}
			if rerr := r.tr.Reconnect(ctx); rerr != nil {
				err = rerr
			}
			if serr := r.opt.Sleep(ctx, r.opt.Timeouts.Poll); serr != nil {
				break
			}
		}
		if err != nil {
			if Lost(err) {
				lost = true
			}
			fail("%s: %v", what, err)
		}
	}

	do("retire the model served", func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.retire(ctx)
	})
	for _, name := range j.Created {
		do("remove "+name, func() error {
			_, err := r.tr.Exec(ctx, "docker", "rm", "-f", name)
			return err
		})
	}
	for _, name := range []string{j.Chat, j.Tabby} {
		if j.Before.wasRunning(name) {
			continue
		}
		do("stop "+name, func() error {
			c, err := r.inspect(ctx, name)
			if err != nil || c.Absent || !c.Running() {
				return err
			}
			_, err = r.tr.Exec(ctx, "docker", "stop", "-t", "30", name)
			return err
		})
	}
	// The GPU must hold nothing but what ran before.
	before := holderNames(j.Before.GPU)
	do("wait for the GPU to empty", func() error {
		return r.waitGPU(ctx, func(g GPU) bool {
			for _, n := range holderNames(g) {
				if !slices.Contains(before, n) {
					return false
				}
			}
			return true
		})
	})
	for _, c := range j.Before.Containers {
		if !c.Running() {
			continue
		}
		do("start "+c.Name, func() error {
			now, err := r.inspect(ctx, c.Name)
			if err != nil {
				return err
			}
			if now.Running() {
				return nil
			}
			if now.Absent {
				return fmt.Errorf("%s no longer exists and cannot be started", c.Name)
			}
			if _, err := r.tr.Exec(ctx, "docker", "start", c.Name); err != nil {
				return err
			}
			port, ok := 0, false
			if v, found := now.rawFlags.Value("--port"); found {
				port, _ = strconv.Atoi(v)
				ok = port > 0
			}
			if ok {
				_, err = r.waitHealthy(ctx, c.Name, port, "/health", false)
				return err
			}
			return nil
		})
	}
	diffs, err := r.differences(ctx, j.Before, j.Created)
	switch {
	case err != nil && Lost(err):
		lost = true
		fail("compare the server with its snapshot: %v", err)
	case err != nil:
		fail("compare the server with its snapshot: %v", err)
	default:
		for _, d := range diffs {
			fail("still differs: %s", d)
		}
	}
	if len(failures) > 0 {
		return &RestoreError{Failures: failures, Lost: lost}
	}
	return nil
}
