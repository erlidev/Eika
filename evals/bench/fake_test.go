package bench_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/bench"
)

// fakeContainer is a container of the fake server.
type fakeContainer struct {
	id, name, image string
	running         bool
	restart         string
	cmd             []string
	ip              string
	gpuMiB          int
	labels          map[string]string
	network         string
	binds           []string
	// answers says whether the health route answers while it runs; false is
	// a model that never finishes loading.
	answers bool
	logs    string
}

// fakeHost is a Transport that behaves like the bench's server for the
// commands the runner sends, and records every command in order. It is the
// seam of the tests: nothing here touches a real machine.
type fakeHost struct {
	t *testing.T

	mu         sync.Mutex
	containers map[string]*fakeContainer
	next       int
	commands   []string
	files      map[string]string
	watchdogs  []string
	forwards   []string
	// fail returns an error for a command line to inject a failure.
	fail func(cmdline string) error
	// down is true while the connection is lost; reconnect brings it back
	// unless reconnectFails.
	down           bool
	reconnectFails bool
	reconnects     int
	// now is the fake clock: Sleep advances it.
	now time.Time
	// onStop runs when a container is stopped, with the command, for tests
	// that check what was written before it.
	onStop func(name string)
}

func newFakeHost(t *testing.T) *fakeHost {
	return &fakeHost{t: t, containers: map[string]*fakeContainer{}, files: map[string]string{}, now: time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)}
}

const (
	llamaImage = "llama-server-gpu:0.0.9"
	proxyNet   = "proxy"
)

var chatFlags = []string{"-m", "/models/qwen.gguf", "--host", "0.0.0.0", "--port", "8080", "-np", "2", "--kv-unified", "--ctx-size", "131072"}

// add creates a container on the server.
func (h *fakeHost) add(name, image string, running bool, gpuMiB int, cmd []string) *fakeContainer {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", name, h.next)))
	c := &fakeContainer{
		id: hex.EncodeToString(sum[:]), name: name, image: image, running: running, restart: "unless-stopped",
		cmd: cmd, ip: fmt.Sprintf("172.18.0.%d", 10+h.next), gpuMiB: gpuMiB, network: proxyNet,
		binds: []string{"/opt/models/huggingface:/root/.cache/huggingface"}, answers: true,
		labels: map[string]string{},
	}
	h.containers[name] = c
	return c
}

// normal is the bench as it should be: llama-qwen serving, the tabbyapi
// container stopped, a bystander running without the GPU.
func (h *fakeHost) normal() {
	h.add("llama-qwen", llamaImage, true, 28000, chatFlags)
	h.add("tabbyapi", "tabbyapi:0.0.8", false, 30000, []string{"main.py", "--host", "0.0.0.0"})
	h.add("caddy", "caddy:latest", true, 0, nil)
}

// swapped is the bench as it was found: llama-qwen stopped and another
// container on the GPU.
func (h *fakeHost) swapped() {
	h.add("llama-qwen", llamaImage, false, 28000, chatFlags)
	h.add("tabbyapi", "tabbyapi:0.0.8", false, 30000, []string{"main.py", "--host", "0.0.0.0"})
	h.add("llama-server", llamaImage, true, 28800, []string{"-hf", "unsloth/Qwen3.8-27B-GGUF:UD-Q4_K_XL", "--api-key", "sekret", "--port", "8080"})
	h.add("caddy", "caddy:latest", true, 0, nil)
}

func (h *fakeHost) get(name string) *fakeContainer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.containers[name]
}

// runningNames lists the containers running, sorted.
func (h *fakeHost) runningNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for n, c := range h.containers {
		if c.running {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

// ran returns the recorded commands that contain every part.
func (h *fakeHost) ran(parts ...string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.commands {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(c, p)
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// mutations are the commands that change the server: the docker verbs that
// stop, start, create or remove, and the runner's marks.
func (h *fakeHost) mutations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.commands {
		f := strings.Fields(c)
		switch {
		case len(f) > 1 && f[0] == "docker" && slices.Contains([]string{"stop", "start", "run", "rm", "restart", "kill", "update", "create", "rename"}, f[1]):
			out = append(out, c)
		case f[0] == "touch" || f[0] == "rm" || strings.HasPrefix(c, "sh -c mkdir") || strings.Contains(c, "nohup"):
			out = append(out, c)
		}
	}
	return out
}

// Exec implements bench.Transport.
func (h *fakeHost) Exec(_ context.Context, argv ...string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := strings.Join(argv, " ")
	h.commands = append(h.commands, line)
	if h.down {
		return "", &bench.ExecError{Argv: argv, ExitCode: 255, Stderr: "connection lost"}
	}
	if h.fail != nil {
		if err := h.fail(line); err != nil {
			return "", err
		}
	}
	out, err := h.exec(argv)
	if err != nil {
		return "", &bench.ExecError{Argv: argv, ExitCode: 1, Stderr: err.Error()}
	}
	return out, nil
}

// Forward implements bench.Transport.
func (h *fakeHost) Forward(_ context.Context, remote string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.forwards = append(h.forwards, remote)
	return "127.0.0.1:40000", nil
}

// Reconnect implements bench.Transport.
func (h *fakeHost) Reconnect(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconnects++
	if h.reconnectFails {
		return fmt.Errorf("no route to host")
	}
	h.down = false
	return nil
}

// Close implements bench.Transport.
func (h *fakeHost) Close() error { return nil }

// sleep is the Sleep option: it moves the fake clock instead of waiting.
func (h *fakeHost) sleep(_ context.Context, d time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
	return nil
}

func (h *fakeHost) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *fakeHost) holding() []*fakeContainer {
	var out []*fakeContainer
	for _, c := range h.containers {
		if c.running && c.gpuMiB > 0 {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b *fakeContainer) int { return strings.Compare(a.name, b.name) })
	return out
}

// exec runs one command against the fake state. The caller holds mu.
func (h *fakeHost) exec(argv []string) (string, error) {
	switch argv[0] {
	case "docker":
		return h.docker(argv[1:])
	case "nvidia-smi":
		return h.nvidia(argv[1:]), nil
	case "cat":
		switch {
		case strings.HasSuffix(argv[1], "/cgroup"):
			pid, _ := strconv.Atoi(strings.Split(argv[1], "/")[2])
			for i, c := range h.holding() {
				if 1000+i == pid {
					return "0::/user.slice/docker-" + c.id + ".scope\n", nil
				}
			}
			return "0::/user.slice\n", nil
		case argv[1] == "/tmp/eika-bench.lock/run":
			if v, ok := h.files["lock"]; ok {
				return v + "\n", nil
			}
		}
		return "", fmt.Errorf("cat: %s: No such file or directory", argv[1])
	case "test":
		if _, ok := h.files["lock"]; ok && argv[2] == "/tmp/eika-bench.lock" {
			return "", nil
		}
		return "", fmt.Errorf("not there")
	case "touch":
		h.files[argv[1]] = "touched"
		return "", nil
	case "rm":
		if slices.Contains(argv, "/tmp/eika-bench.lock") {
			delete(h.files, "lock")
		}
		return "", nil
	case "date":
		return h.now.Format(time.RFC3339) + "\n", nil
	case "grep":
		return "  model_name: Swift-1.5-Qwen3.8-27B-exl3-SC-4.50bpw\n  max_seq_len: 153600\n  cache_size: 307200 # tokens\n  cache_mode: \"8,8\"\n  port: 5000\n", nil
	case "curl":
		url := argv[len(argv)-1]
		for _, c := range h.containers {
			if strings.Contains(url, c.ip) {
				if c.running && c.answers {
					return "200", nil
				}
				if slices.Contains(argv, "-f") {
					return "", fmt.Errorf("curl: (22) 503")
				}
				return "000", nil
			}
		}
		return "", fmt.Errorf("curl: (7) no route")
	case "sh":
		script := argv[2]
		switch {
		case strings.HasPrefix(script, "mkdir /tmp/eika-bench.lock"):
			if _, ok := h.files["lock"]; ok {
				return "", fmt.Errorf("mkdir: cannot create directory: File exists")
			}
			h.files["lock"] = strings.Fields(strings.Split(script, "echo ")[1])[0]
			return "", nil
		case strings.HasPrefix(script, "nohup setsid sh -c"):
			h.watchdogs = append(h.watchdogs, script)
			return "", nil
		case strings.HasPrefix(script, "docker logs"):
			f := strings.Fields(script)
			name := f[len(f)-2]
			if c, ok := h.containers[name]; ok {
				return c.logs, nil
			}
			return "", fmt.Errorf("No such container: %s", name)
		}
	}
	return "", fmt.Errorf("fake host: unexpected command %v", argv)
}

func (h *fakeHost) nvidia(args []string) string {
	if strings.HasPrefix(args[0], "--query-gpu") {
		used := 0
		for _, c := range h.holding() {
			used += c.gpuMiB
		}
		return fmt.Sprintf("%d, 32607\n", used)
	}
	var b strings.Builder
	for i, c := range h.holding() {
		fmt.Fprintf(&b, "%d, /usr/local/bin/llama-server, %d\n", 1000+i, c.gpuMiB)
	}
	return b.String()
}

func (h *fakeHost) docker(args []string) (string, error) {
	switch args[0] {
	case "ps":
		all := slices.Contains(args, "-a")
		format := args[len(args)-1]
		label := ""
		for i, a := range args {
			if a == "--filter" {
				label = strings.TrimPrefix(args[i+1], "label=")
			}
		}
		var names []string
		for n := range h.containers {
			names = append(names, n)
		}
		slices.Sort(names)
		var b strings.Builder
		for _, n := range names {
			c := h.containers[n]
			if !c.running && !all {
				continue
			}
			if label != "" {
				if _, ok := c.labels[label]; !ok {
					continue
				}
			}
			if format == "{{.Names}}" {
				fmt.Fprintln(&b, c.name)
			} else {
				fmt.Fprintf(&b, "%s|%s|%s\n", c.id, c.name, c.image)
			}
		}
		return b.String(), nil
	case "inspect":
		c, ok := h.containers[args[1]]
		if !ok {
			return "", fmt.Errorf("Error: No such object: %s", args[1])
		}
		if strings.HasPrefix(args[3], "{{json .HostConfig.Binds}}") {
			binds, _ := json.Marshal(c.binds)
			return string(binds) + "|" + c.network + "\n", nil
		}
		status := "exited"
		if c.running {
			status = "running"
		}
		cmd, _ := json.Marshal(c.cmd)
		ip := c.ip
		if !c.running {
			ip = ""
		}
		return fmt.Sprintf("%s|/%s|%s|sha256:%s|%s|%s|%s=%s,|%s\n", c.id, c.name, c.image, c.id[:16], status, c.restart, c.network, ip, cmd), nil
	case "stop":
		name := args[len(args)-1]
		c, ok := h.containers[name]
		if !ok {
			return "", fmt.Errorf("Error: No such container: %s", name)
		}
		if h.onStop != nil {
			h.mu.Unlock()
			h.onStop(name)
			h.mu.Lock()
		}
		c.running = false
		return name + "\n", nil
	case "start":
		c, ok := h.containers[args[1]]
		if !ok {
			return "", fmt.Errorf("Error: No such container: %s", args[1])
		}
		c.running = true
		return c.name + "\n", nil
	case "rm":
		name := args[len(args)-1]
		delete(h.containers, name)
		return name + "\n", nil
	case "run":
		return h.run(args[1:])
	}
	return "", fmt.Errorf("fake host: unexpected docker %v", args)
}

func (h *fakeHost) run(args []string) (string, error) {
	c := &fakeContainer{running: true, labels: map[string]string{}, answers: true, gpuMiB: 28000}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-d":
			i++
		case "--name":
			c.name = args[i+1]
			i += 2
		case "--label":
			k, v, _ := strings.Cut(args[i+1], "=")
			c.labels[k] = v
			i += 2
		case "--network":
			c.network = args[i+1]
			i += 2
		case "--restart":
			c.restart = args[i+1]
			i += 2
		case "--gpus", "--env-file":
			i += 2
		case "-v":
			c.binds = append(c.binds, args[i+1])
			i += 2
		default:
			c.image, c.cmd = args[i], args[i+1:]
			i = len(args)
		}
	}
	if _, taken := h.containers[c.name]; taken {
		return "", fmt.Errorf("Conflict. The container name %q is already in use", c.name)
	}
	h.next++
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", c.name, h.next)))
	c.id, c.ip = hex.EncodeToString(sum[:]), fmt.Sprintf("172.18.0.%d", 10+h.next)
	if slices.Contains(c.cmd, "--bogus-flag") {
		c.running, c.gpuMiB = false, 0
		c.logs = "error: invalid argument: --bogus-flag\n"
	}
	h.containers[c.name] = c
	return c.id + "\n", nil
}

// journalPath is a journal file in a temporary directory.
func journalPath(t *testing.T) string { return filepath.Join(t.TempDir(), "journal.json") }

// openRunner opens a runner on the fake host with options the tests share.
func openRunner(t *testing.T, h *fakeHost, mod func(*bench.Options)) (*bench.Runner, error) {
	t.Helper()
	opt := bench.Options{
		Host: "fake", JournalPath: journalPath(t), NoSignals: true,
		Now: h.clock, Sleep: h.sleep, NewID: func() string { return "abc123" },
		Timeouts: bench.Timeouts{Heartbeat: 10 * time.Millisecond, Grace: 50 * time.Millisecond},
	}
	if mod != nil {
		mod(&opt)
	}
	return bench.Open(t.Context(), h, opt)
}

// readJournalFile reads the journal a test's runner wrote.
func readJournalFile(t *testing.T, path string) bench.Journal {
	t.Helper()
	j, err := bench.ReadJournal(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	return j
}
