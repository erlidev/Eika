package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Container is what the runner knows about one container of the server. The
// fields are the ones that decide whether a container was put back exactly as
// it was: its identity, whether it runs, and the flags it runs with. The
// environment is never read.
type Container struct {
	// ID is the full container id, which changes if a container is
	// recreated, so an unchanged one is the same container.
	ID string `json:"id"`
	// Name is the container name without the leading slash.
	Name string `json:"name"`
	// Image is the image reference the container was created from.
	Image string `json:"image"`
	// ImageID is the id of that image's content.
	ImageID string `json:"image_id"`
	// Status is Docker's state: running, exited, created and so on.
	Status string `json:"status"`
	// Restart is the restart policy.
	Restart string `json:"restart"`
	// Network is the first network the container is on, and IP its address
	// there; the runner reaches a container that publishes nothing by it.
	Network string `json:"network,omitempty"`
	IP      string `json:"ip,omitempty"`
	// Flags are the container's arguments, with credentials redacted.
	Flags Flags `json:"flags"`
	// Absent is true for a name that no container has.
	Absent bool `json:"absent,omitempty"`

	// rawFlags are Flags before redaction; they are never stored.
	rawFlags Flags
}

// Running reports whether the container is running now.
func (c Container) Running() bool { return c.Status == "running" }

// names a container may have, so that a name can be put in a command line
// and a shell script without quoting.
var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// checkName fails on a name Docker would not give a container.
func checkName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q is not a container name", name)
	}
	return nil
}

// inspectFormat prints one container on one line: id, name, image, image id,
// status, restart policy, networks with their addresses, and the arguments as
// JSON last, since they may hold the separator.
const inspectFormat = `{{.Id}}|{{.Name}}|{{.Config.Image}}|{{.Image}}|{{.State.Status}}|{{.HostConfig.RestartPolicy.Name}}|{{range $k, $v := .NetworkSettings.Networks}}{{$k}}={{$v.IPAddress}},{{end}}|{{json .Config.Cmd}}`

// parseInspect reads the line inspectFormat prints.
func parseInspect(line string) (Container, error) {
	parts := strings.SplitN(strings.TrimSpace(line), "|", 8)
	if len(parts) != 8 {
		return Container{}, fmt.Errorf("read inspect output %q: want 8 fields, got %d", line, len(parts))
	}
	c := Container{
		ID: parts[0], Name: strings.TrimPrefix(parts[1], "/"), Image: parts[2], ImageID: parts[3],
		Status: parts[4], Restart: parts[5],
	}
	if first, _, _ := strings.Cut(strings.TrimSuffix(parts[6], ","), ","); first != "" {
		c.Network, c.IP, _ = strings.Cut(first, "=")
	}
	var cmd []string
	if parts[7] != "null" && parts[7] != "" {
		if err := json.Unmarshal([]byte(parts[7]), &cmd); err != nil {
			return Container{}, fmt.Errorf("read the arguments of %s: %w", c.Name, err)
		}
	}
	c.rawFlags = cmd
	c.Flags = Flags(cmd).Redact()
	return c, nil
}

// inspect reads one container; a name no container has is Absent, not an
// error.
func (r *Runner) inspect(ctx context.Context, name string) (Container, error) {
	if err := checkName(name); err != nil {
		return Container{}, err
	}
	out, err := r.tr.Exec(ctx, "docker", "inspect", name, "--format", inspectFormat)
	if err != nil {
		var ee *ExecError
		if errors.As(err, &ee) && !Lost(err) && strings.Contains(ee.Stderr, "No such") {
			return Container{Name: name, Absent: true}, nil
		}
		return Container{}, fmt.Errorf("inspect %s: %w", name, err)
	}
	return parseInspect(out)
}

// running lists the containers that run now, as id, name and image.
func (r *Runner) running(ctx context.Context) ([]Container, error) {
	out, err := r.tr.Exec(ctx, "docker", "ps", "--no-trunc", "--format", "{{.ID}}|{{.Names}}|{{.Image}}")
	if err != nil {
		return nil, fmt.Errorf("list running containers: %w", err)
	}
	var cs []Container
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		p := strings.SplitN(line, "|", 3)
		if len(p) != 3 {
			return nil, fmt.Errorf("read docker ps line %q", line)
		}
		cs = append(cs, Container{ID: p[0], Name: p[1], Image: p[2], Status: "running"})
	}
	return cs, nil
}

// Holder is a process that holds memory on the GPU.
type Holder struct {
	// PID is the process id on the server.
	PID int `json:"pid"`
	// Process is its executable.
	Process string `json:"process"`
	// MiB is the GPU memory it holds.
	MiB int `json:"mib"`
	// Container is the container it runs in, empty for a process that is
	// not in a container the runner can name.
	Container string `json:"container,omitempty"`
}

// GPU is the state of the server's GPU.
type GPU struct {
	// UsedMiB and TotalMiB are the GPU memory in use and in all.
	UsedMiB  int `json:"used_mib"`
	TotalMiB int `json:"total_mib"`
	// Holders are the compute processes on it.
	Holders []Holder `json:"holders"`
}

// containerOfCgroup finds the 64-hex id of a container in the text of a
// process's /proc/<pid>/cgroup.
var cgroupID = regexp.MustCompile(`docker-([0-9a-f]{64})`)

// gpu reads the GPU and maps each compute process to its container through
// the cgroup of the process, against the running containers.
func (r *Runner) gpu(ctx context.Context, running []Container) (GPU, error) {
	var g GPU
	out, err := r.tr.Exec(ctx, "nvidia-smi", "--query-gpu=memory.used,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return g, fmt.Errorf("read the GPU: %w", err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	used, total, ok := strings.Cut(first, ",")
	if !ok {
		return g, fmt.Errorf("read the GPU memory from %q", first)
	}
	if g.UsedMiB, err = strconv.Atoi(strings.TrimSpace(used)); err != nil {
		return g, fmt.Errorf("read the GPU memory used: %w", err)
	}
	if g.TotalMiB, err = strconv.Atoi(strings.TrimSpace(total)); err != nil {
		return g, fmt.Errorf("read the GPU memory total: %w", err)
	}
	out, err = r.tr.Exec(ctx, "nvidia-smi", "--query-compute-apps=pid,process_name,used_memory", "--format=csv,noheader,nounits")
	if err != nil {
		return g, fmt.Errorf("read the GPU processes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, ",")
		if len(p) != 3 {
			return g, fmt.Errorf("read the GPU process line %q", line)
		}
		h := Holder{Process: strings.TrimSpace(p[1])}
		if h.PID, err = strconv.Atoi(strings.TrimSpace(p[0])); err != nil {
			return g, fmt.Errorf("read the GPU process id in %q: %w", line, err)
		}
		if h.MiB, err = strconv.Atoi(strings.TrimSpace(p[2])); err != nil {
			return g, fmt.Errorf("read the GPU process memory in %q: %w", line, err)
		}
		if cg, err := r.tr.Exec(ctx, "cat", fmt.Sprintf("/proc/%d/cgroup", h.PID)); err == nil {
			if m := cgroupID.FindStringSubmatch(cg); m != nil {
				for _, c := range running {
					if c.ID == m[1] {
						h.Container = c.Name
					}
				}
			}
		}
		g.Holders = append(g.Holders, h)
	}
	return g, nil
}
