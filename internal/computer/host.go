package computer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/erlidev/eika/internal/eikad"
	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/sandbox"
)

// useInit asks Docker to run its own init as PID 1 in a sandbox, which reaps
// the processes a shell orphans. It is addressable because the Docker API
// takes a pointer.
var useInit = true

// Bounds on the operations Host performs on the Docker daemon.
const (
	// readyTimeout is how long Start waits for eikad to answer.
	readyTimeout = 60 * time.Second
	// readyInterval is how often Start polls eikad while it comes up.
	readyInterval = 200 * time.Millisecond
	// stopTimeout is how long a container gets to exit before it is killed.
	stopTimeout = 5 * time.Second
)

// proxyTokenEnv carries a computer's egress proxy token in its container's
// configuration, where Inspect recovers it after a harness restart. The
// daemon's processes receive it only inside the proxy URL.
const proxyTokenEnv = "EIKA_PROXY_TOKEN"

// ErrNoComputer reports that no container carries the given computer id.
var ErrNoComputer = errors.New("computer not found")

// Options configures a Host.
type Options struct {
	// DockerSocket is the path to the Docker socket.
	DockerSocket string
	// Image is the image used by a Spec that names none.
	Image string
	// Network is the Docker network sandboxes join, which is how the harness
	// resolves them by container name. Empty publishes the daemon port on
	// 127.0.0.1 instead, which is what a harness running outside compose
	// needs.
	Network string
	// EikadBinary is the path to the static eikad binary in the harness
	// filesystem. It is copied into every container before it starts.
	EikadBinary string
	// InternalNetwork is the Docker network a proxied sandbox joins instead
	// of Network. It must be internal, so that it has no route out, and the
	// harness must be on it too. Empty, or an empty Network, means no
	// sandbox can be proxied.
	InternalNetwork string
	// EgressProxyURL is the harness's egress proxy as a proxied sandbox
	// reaches it, for example http://eika:3128. Empty leaves a proxied
	// sandbox with no way out at all.
	EgressProxyURL string
}

// Host creates and runs computers on one Docker daemon.
type Host struct {
	docker *client.Client
	opts   Options
	log    *slog.Logger
	http   *http.Client
}

// NewHost connects to the Docker daemon named by the options.
func NewHost(opts Options, log *slog.Logger) (*Host, error) {
	switch {
	case opts.Image == "":
		return nil, errors.New("build computer host: image is empty")
	case opts.EikadBinary == "":
		return nil, errors.New("build computer host: eikad binary path is empty")
	}
	if opts.EgressProxyURL != "" {
		if u, err := url.Parse(opts.EgressProxyURL); err != nil || u.Host == "" {
			return nil, fmt.Errorf("build computer host: egress proxy url %q is not an absolute url", opts.EgressProxyURL)
		}
	}
	docker, err := client.NewClientWithOpts(
		client.WithHost("unix://"+opts.DockerSocket),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to docker at %s: %w", opts.DockerSocket, err)
	}
	return &Host{
		docker: docker,
		opts:   opts,
		log:    log,
		http:   &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// Close releases the connection to the Docker daemon.
func (h *Host) Close() error { return h.docker.Close() }

// Create builds the computer's volume and container. The container is not
// started; call Start.
func (h *Host) Create(ctx context.Context, spec Spec) (Computer, error) {
	if spec.Confinement.Proxied && !h.EgressControl() {
		return Computer{}, ErrNoEgressControl
	}
	cx := Computer{ID: spec.ID, State: StateCreating, CreatedAt: time.Now().UTC(), Proxied: spec.Confinement.Proxied}
	if cx.ID == "" {
		id, err := newID()
		if err != nil {
			return Computer{}, err
		}
		cx.ID = id
	}
	var err error
	if cx.Token, err = newToken(); err != nil {
		return Computer{}, err
	}
	if cx.ProxyToken, err = newToken(); err != nil {
		return Computer{}, err
	}

	cx.Image = orDefault(spec.Image, h.opts.Image)
	built := spec.BuildContext != ""
	if built {
		cx.Image = builtImageRef(cx.ID)
		if err := h.buildImage(ctx, spec, cx.Image); err != nil {
			return Computer{}, err
		}
	}
	// Everything created from here on is rolled back if creation fails, so a
	// failed Create leaves nothing behind.
	rollback := func() { h.discard(ctx, cx, built) }

	cx.Volume = VolumeName(cx.ID)
	if _, err := h.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name:   cx.Volume,
		Labels: map[string]string{Label: cx.ID},
	}); err != nil {
		rollback()
		return Computer{}, fmt.Errorf("create volume %s: %w", cx.Volume, err)
	}

	labels := map[string]string{Label: cx.ID}
	maps.Copy(labels, spec.Labels)

	port := nat.Port(DaemonPort + "/tcp")
	cfg := &container.Config{
		Image:      cx.Image,
		Entrypoint: []string{eikad.BinaryPath, "-listen", ":" + DaemonPort, "-root", Root},
		Env: append([]string{
			eikad.TokenEnv + "=" + cx.Token,
			proxyTokenEnv + "=" + cx.ProxyToken,
		}, spec.Env...),
		User:         orDefault(spec.User, DefaultUser),
		WorkingDir:   Root,
		Labels:       labels,
		ExposedPorts: nat.PortSet{port: struct{}{}},
	}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: cx.Volume, Target: Root}},
		// Docker's own init is PID 1 and reaps the orphans a shell leaves
		// behind; eikad runs under it as an ordinary process.
		Init: &useInit,
		// A sandbox runs arbitrary code, so it keeps no capabilities and
		// cannot gain privileges through a setuid binary.
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		Resources:   createResources(spec.Confinement.Limits),
	}
	netCfg := &network.NetworkingConfig{}
	if h.opts.Network != "" {
		netCfg.EndpointsConfig = map[string]*network.EndpointSettings{
			h.networkFor(cx.Proxied): {Aliases: []string{ContainerName(cx.ID)}},
		}
	} else {
		hostCfg.PortBindings = nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}}}
	}

	created, err := h.createContainer(ctx, cfg, hostCfg, netCfg, ContainerName(cx.ID))
	if err != nil {
		rollback()
		return Computer{}, fmt.Errorf("create container for computer %s: %w", cx.ID, err)
	}
	cx.ContainerID = created.ID

	if err := h.injectDaemon(ctx, cx.ContainerID); err != nil {
		h.discard(ctx, cx, built)
		return Computer{}, err
	}
	h.log.Info("computer created", "computer_id", cx.ID, "image", cx.Image)
	return cx, nil
}

// discard removes whatever of a computer already exists. It is the rollback
// for a failed Create and the body of Destroy, and it ignores what is not
// there.
func (h *Host) discard(ctx context.Context, cx Computer, builtImage bool) {
	if cx.ContainerID != "" {
		if err := h.docker.ContainerRemove(ctx, cx.ContainerID, container.RemoveOptions{
			Force: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove container", "computer_id", cx.ID, "error", err)
		}
	}
	if cx.Volume != "" {
		if err := h.docker.VolumeRemove(ctx, cx.Volume, true); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove volume", "computer_id", cx.ID, "error", err)
		}
	}
	if builtImage {
		if _, err := h.docker.ImageRemove(ctx, builtImageRef(cx.ID), image.RemoveOptions{
			Force:         true,
			PruneChildren: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove image", "computer_id", cx.ID, "error", err)
		}
	}
}

// Start starts the computer's container and waits until its daemon answers,
// filling in the address the harness reaches it on. It first puts the
// container back on its sandbox network, in case that network was recreated
// since the container last ran.
func (h *Host) Start(ctx context.Context, cx *Computer) error {
	if h.opts.Network != "" {
		if err := h.moveTo(ctx, *cx, h.networkFor(cx.Proxied)); err != nil {
			return err
		}
	}
	if err := h.docker.ContainerStart(ctx, cx.ContainerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start computer %s: %w", cx.ID, err)
	}
	addr, err := h.address(ctx, *cx)
	if err != nil {
		return err
	}
	cx.Address = addr
	if err := h.waitReady(ctx, addr); err != nil {
		return fmt.Errorf("computer %s did not become ready: %w", cx.ID, err)
	}
	// The daemon forgets the environment it was given when the container
	// stops, so every start gives it again.
	if err := h.setEnvironment(ctx, *cx); err != nil {
		return err
	}
	cx.State = StateRunning
	h.log.Info("computer started", "computer_id", cx.ID, "address", addr)
	return nil
}

// Stop stops the computer's container, leaving its volume in place.
func (h *Host) Stop(ctx context.Context, cx *Computer) error {
	timeout := int(stopTimeout.Seconds())
	if err := h.docker.ContainerStop(ctx, cx.ContainerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop computer %s: %w", cx.ID, err)
	}
	cx.State = StateStopped
	h.log.Info("computer stopped", "computer_id", cx.ID)
	return nil
}

// Destroy removes the computer's container and its volume. The files in a
// bind-mounted host directory are left alone.
func (h *Host) Destroy(ctx context.Context, cx *Computer) error {
	if err := h.docker.ContainerRemove(ctx, cx.ContainerID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: false,
	}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container for computer %s: %w", cx.ID, err)
	}
	if cx.Volume != "" {
		if err := h.docker.VolumeRemove(ctx, cx.Volume, true); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove volume %s: %w", cx.Volume, err)
		}
	}
	// An image built for this one computer is named after it, so it goes too.
	if cx.Image == builtImageRef(cx.ID) {
		if _, err := h.docker.ImageRemove(ctx, cx.Image, image.RemoveOptions{
			Force:         true,
			PruneChildren: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove image %s: %w", cx.Image, err)
		}
	}
	cx.Address = ""
	cx.State = StateGone
	h.log.Info("computer destroyed", "computer_id", cx.ID)
	return nil
}

// List reports every computer container the Docker daemon knows about,
// running or not. The harness calls it on startup to reconcile with reality.
func (h *Host) List(ctx context.Context) ([]Computer, error) {
	summaries, err := h.docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", Label)),
	})
	if err != nil {
		return nil, fmt.Errorf("list computer containers: %w", err)
	}
	out := make([]Computer, 0, len(summaries))
	for _, s := range summaries {
		cx, err := h.Inspect(ctx, s.Labels[Label])
		if err != nil {
			return nil, fmt.Errorf("inspect listed computer container %s: %w", s.ID, err)
		}
		out = append(out, cx)
	}
	return out, nil
}

// Inspect reports the current state of one computer, recovering its tokens
// from the container so that the harness can keep working after a restart.
func (h *Host) Inspect(ctx context.Context, id string) (Computer, error) {
	info, err := h.docker.ContainerInspect(ctx, ContainerName(id))
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return Computer{}, fmt.Errorf("%w: %s", ErrNoComputer, id)
		}
		return Computer{}, fmt.Errorf("inspect computer %s: %w", id, err)
	}
	cx := Computer{
		ID:          id,
		ContainerID: info.ID,
		Image:       info.Config.Image,
		State:       StateStopped,
		Token:       envValue(info.Config.Env, eikad.TokenEnv),
		ProxyToken:  envValue(info.Config.Env, proxyTokenEnv),
	}
	if created, err := time.Parse(time.RFC3339Nano, info.Created); err == nil {
		cx.CreatedAt = created.UTC()
	}
	if info.NetworkSettings != nil && h.EgressControl() {
		_, cx.Proxied = info.NetworkSettings.Networks[h.opts.InternalNetwork]
	}
	for _, m := range info.Mounts {
		if m.Destination != Root {
			continue
		}
		cx.Volume = m.Name
	}
	if info.State != nil && info.State.Running {
		cx.State = StateRunning
		if addr, err := h.address(ctx, cx); err == nil {
			cx.Address = addr
		}
	}
	return cx, nil
}

// Executor returns the executor an agent uses to reach the computer. The
// computer must be running.
func (h *Host) Executor(cx Computer) (executor.Executor, error) {
	return h.client(cx)
}

// Process starts a long-lived process in a running computer with its
// standard streams connected to the caller: how the harness runs a stdio MCP
// server where the agent's processes run. It is kept apart from
// Executor, so no tool can hold a process of its own.
func (h *Host) Process(ctx context.Context, cx Computer, spec sandbox.ProcessSpec, stderr func(string)) (io.ReadWriteCloser, error) {
	c, err := h.client(cx)
	if err != nil {
		return nil, err
	}
	return c.Process(ctx, spec, stderr)
}

// client returns a client for a running computer's daemon, narrowed to the
// directory the computer's files are in.
func (h *Host) client(cx Computer) (*sandbox.Client, error) {
	if cx.Address == "" {
		return nil, fmt.Errorf("computer %s has no address: it is not running", cx.ID)
	}
	return sandbox.New(sandbox.Options{BaseURL: cx.Address, Token: cx.Token, Root: Root})
}

// address is the base URL the harness reaches the computer's daemon on: the
// container name on the sandbox network, or the published port when the
// harness runs outside that network.
func (h *Host) address(ctx context.Context, cx Computer) (string, error) {
	if h.opts.Network != "" {
		return "http://" + ContainerName(cx.ID) + ":" + DaemonPort, nil
	}
	info, err := h.docker.ContainerInspect(ctx, cx.ContainerID)
	if err != nil {
		return "", fmt.Errorf("inspect computer %s: %w", cx.ID, err)
	}
	bindings := info.NetworkSettings.Ports[nat.Port(DaemonPort+"/tcp")]
	if len(bindings) == 0 {
		return "", fmt.Errorf("computer %s publishes no daemon port", cx.ID)
	}
	return "http://127.0.0.1:" + bindings[0].HostPort, nil
}

// waitReady polls the daemon's health check until it answers.
func (h *Host) waitReady(ctx context.Context, addr string) error {
	deadline := time.Now().Add(readyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := h.http.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for the sandbox daemon")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyInterval):
		}
	}
}

// envValue returns the value of name in a container's environment.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// orDefault returns value, or fallback when value is empty.
func orDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
