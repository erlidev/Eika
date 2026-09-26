package workspace

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

	"github.com/coder/websocket"
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
	"github.com/erlidev/eika/internal/workspace/hub"
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

// Environment variables the harness sets in every workspace container so that
// git inside the workspace can reach the hub without the token appearing in a
// remote URL or in .git/config.
const (
	hubUserEnv  = "EIKA_HUB_USER"
	hubTokenEnv = "EIKA_HUB_TOKEN"
)

// ErrNoWorkspace reports that no container carries the given workspace id.
var ErrNoWorkspace = errors.New("workspace not found")

// Options configures a Host.
type Options struct {
	// DockerSocket is the path to the Docker socket.
	DockerSocket string
	// Hub holds the project repositories workspaces clone from.
	Hub *hub.Hub
	// HubURL is the harness base URL a sandbox reaches the hub on, for
	// example http://eika:8080.
	HubURL string
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

// Host creates and runs workspaces on one Docker daemon.
type Host struct {
	docker *client.Client
	opts   Options
	log    *slog.Logger
	http   *http.Client
}

// NewHost connects to the Docker daemon named by the options.
func NewHost(opts Options, log *slog.Logger) (*Host, error) {
	switch {
	case opts.Hub == nil:
		return nil, errors.New("build workspace host: hub is nil")
	case opts.Image == "":
		return nil, errors.New("build workspace host: image is empty")
	case opts.EikadBinary == "":
		return nil, errors.New("build workspace host: eikad binary path is empty")
	}
	if opts.EgressProxyURL != "" {
		if u, err := url.Parse(opts.EgressProxyURL); err != nil || u.Host == "" {
			return nil, fmt.Errorf("build workspace host: egress proxy url %q is not an absolute url", opts.EgressProxyURL)
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

// Create builds the workspace's volume and container. The container is not
// started; call Start.
func (h *Host) Create(ctx context.Context, spec Spec) (Workspace, error) {
	if spec.Confinement.Proxied && !h.EgressControl() {
		return Workspace{}, ErrNoEgressControl
	}
	ws := Workspace{ID: spec.ID, State: StateCreating, CreatedAt: time.Now().UTC(), Proxied: spec.Confinement.Proxied}
	if ws.ID == "" {
		id, err := newID()
		if err != nil {
			return Workspace{}, err
		}
		ws.ID = id
	}
	var err error
	if ws.Token, err = newToken(); err != nil {
		return Workspace{}, err
	}
	if ws.HubToken, err = newToken(); err != nil {
		return Workspace{}, err
	}

	ws.Project = spec.Project
	ws.Image = orDefault(spec.Image, h.opts.Image)
	built := spec.BuildContext != ""
	if built {
		ws.Image = builtImageRef(ws.ID)
		if err := h.buildImage(ctx, spec, ws.Image); err != nil {
			return Workspace{}, err
		}
	}
	// Everything created from here on is rolled back if creation fails, so a
	// failed Create leaves nothing behind.
	rollback := func() { h.discard(ctx, ws, built) }

	mounts := make([]mount.Mount, 0, 2)
	if spec.HostPath != "" {
		ws.HostPath = spec.HostPath
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: spec.HostPath, Target: Root})
	} else {
		ws.Volume = VolumeName(ws.ID)
		if _, err := h.docker.VolumeCreate(ctx, volume.CreateOptions{
			Name:   ws.Volume,
			Labels: map[string]string{Label: ws.ID},
		}); err != nil {
			rollback()
			return Workspace{}, fmt.Errorf("create volume %s: %w", ws.Volume, err)
		}
	}

	labels := map[string]string{Label: ws.ID}
	if ws.Project != "" {
		labels[ProjectLabel] = ws.Project
	}
	maps.Copy(labels, spec.Labels)

	port := nat.Port(DaemonPort + "/tcp")
	cfg := &container.Config{
		Image:      ws.Image,
		Entrypoint: []string{eikad.BinaryPath, "-listen", ":" + DaemonPort, "-root", Root},
		Env: append([]string{
			eikad.TokenEnv + "=" + ws.Token,
			hubUserEnv + "=" + ws.ID,
			hubTokenEnv + "=" + ws.HubToken,
		}, spec.Env...),
		User:         orDefault(spec.User, DefaultUser),
		WorkingDir:   Root,
		Labels:       labels,
		ExposedPorts: nat.PortSet{port: struct{}{}},
	}
	hostCfg := &container.HostConfig{
		Mounts: mounts,
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
			h.networkFor(ws.Proxied): {Aliases: []string{ContainerName(ws.ID)}},
		}
	} else {
		hostCfg.PortBindings = nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}}}
	}

	created, err := h.createContainer(ctx, cfg, hostCfg, netCfg, ContainerName(ws.ID))
	if err != nil {
		rollback()
		return Workspace{}, fmt.Errorf("create container for workspace %s: %w", ws.ID, err)
	}
	ws.ContainerID = created.ID

	if err := h.injectDaemon(ctx, ws.ContainerID); err != nil {
		h.discard(ctx, ws, built)
		return Workspace{}, err
	}
	if ws.Project != "" {
		if err := h.opts.Hub.Grant(ws.ID, ws.Project, ws.HubToken); err != nil {
			h.discard(ctx, ws, built)
			return Workspace{}, err
		}
	}
	h.log.Info("workspace created", "workspace_id", ws.ID, "image", ws.Image)
	return ws, nil
}

// discard removes whatever of a workspace already exists. It is the rollback
// for a failed Create and the body of Destroy, and it ignores what is not
// there.
func (h *Host) discard(ctx context.Context, ws Workspace, builtImage bool) {
	if ws.ContainerID != "" {
		if err := h.docker.ContainerRemove(ctx, ws.ContainerID, container.RemoveOptions{
			Force: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove container", "workspace_id", ws.ID, "error", err)
		}
	}
	if ws.Volume != "" {
		if err := h.docker.VolumeRemove(ctx, ws.Volume, true); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove volume", "workspace_id", ws.ID, "error", err)
		}
	}
	if builtImage {
		if _, err := h.docker.ImageRemove(ctx, builtImageRef(ws.ID), image.RemoveOptions{
			Force:         true,
			PruneChildren: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			h.log.Warn("remove image", "workspace_id", ws.ID, "error", err)
		}
	}
	h.opts.Hub.Revoke(ws.ID)
}

// Start starts the workspace's container and waits until its daemon answers,
// filling in the address the harness reaches it on.
func (h *Host) Start(ctx context.Context, ws *Workspace) error {
	if err := h.docker.ContainerStart(ctx, ws.ContainerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start workspace %s: %w", ws.ID, err)
	}
	addr, err := h.address(ctx, *ws)
	if err != nil {
		return err
	}
	ws.Address = addr
	if err := h.waitReady(ctx, addr); err != nil {
		return fmt.Errorf("workspace %s did not become ready: %w", ws.ID, err)
	}
	// The daemon forgets the environment it was given when the container
	// stops, so every start gives it again.
	if err := h.setEnvironment(ctx, *ws); err != nil {
		return err
	}
	ws.State = StateRunning
	h.log.Info("workspace started", "workspace_id", ws.ID, "address", addr)
	return nil
}

// Stop stops the workspace's container, leaving its volume in place.
func (h *Host) Stop(ctx context.Context, ws *Workspace) error {
	timeout := int(stopTimeout.Seconds())
	if err := h.docker.ContainerStop(ctx, ws.ContainerID, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop workspace %s: %w", ws.ID, err)
	}
	ws.State = StateStopped
	h.log.Info("workspace stopped", "workspace_id", ws.ID)
	return nil
}

// Destroy removes the workspace's container and its volume. The files in a
// bind-mounted host directory are left alone.
func (h *Host) Destroy(ctx context.Context, ws *Workspace) error {
	if err := h.docker.ContainerRemove(ctx, ws.ContainerID, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: false,
	}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove container for workspace %s: %w", ws.ID, err)
	}
	if ws.Volume != "" {
		if err := h.docker.VolumeRemove(ctx, ws.Volume, true); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove volume %s: %w", ws.Volume, err)
		}
	}
	// An image built for this one workspace is named after it, so it goes too.
	if ws.Image == builtImageRef(ws.ID) {
		if _, err := h.docker.ImageRemove(ctx, ws.Image, image.RemoveOptions{
			Force:         true,
			PruneChildren: true,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove image %s: %w", ws.Image, err)
		}
	}
	h.opts.Hub.Revoke(ws.ID)
	ws.Address = ""
	ws.State = StateGone
	h.log.Info("workspace destroyed", "workspace_id", ws.ID)
	return nil
}

// List reports every workspace container the Docker daemon knows about,
// running or not. The harness calls it on startup to reconcile with reality.
func (h *Host) List(ctx context.Context) ([]Workspace, error) {
	summaries, err := h.docker.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", Label)),
	})
	if err != nil {
		return nil, fmt.Errorf("list workspace containers: %w", err)
	}
	out := make([]Workspace, 0, len(summaries))
	for _, s := range summaries {
		ws, err := h.Inspect(ctx, s.Labels[Label])
		if err != nil {
			return nil, fmt.Errorf("inspect listed workspace container %s: %w", s.ID, err)
		}
		out = append(out, ws)
	}
	return out, nil
}

// Inspect reports the current state of one workspace, recovering its tokens
// from the container so that the harness can keep working after a restart.
func (h *Host) Inspect(ctx context.Context, id string) (Workspace, error) {
	info, err := h.docker.ContainerInspect(ctx, ContainerName(id))
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return Workspace{}, fmt.Errorf("%w: %s", ErrNoWorkspace, id)
		}
		return Workspace{}, fmt.Errorf("inspect workspace %s: %w", id, err)
	}
	ws := Workspace{
		ID:          id,
		ContainerID: info.ID,
		Image:       info.Config.Image,
		State:       StateStopped,
		Token:       envValue(info.Config.Env, eikad.TokenEnv),
		HubToken:    envValue(info.Config.Env, hubTokenEnv),
		Project:     info.Config.Labels[ProjectLabel],
	}
	if created, err := time.Parse(time.RFC3339Nano, info.Created); err == nil {
		ws.CreatedAt = created.UTC()
	}
	if info.NetworkSettings != nil && h.EgressControl() {
		_, ws.Proxied = info.NetworkSettings.Networks[h.opts.InternalNetwork]
	}
	for _, m := range info.Mounts {
		if m.Destination != Root {
			continue
		}
		if m.Type == mount.TypeBind {
			ws.HostPath = m.Source
		} else {
			ws.Volume = m.Name
		}
	}
	// Hub access follows the container: only a running workspace holds it, so
	// a stopped one cannot be used to reach the hub from somewhere else.
	if info.State != nil && info.State.Running {
		ws.State = StateRunning
		if addr, err := h.address(ctx, ws); err == nil {
			ws.Address = addr
		}
	}
	if ws.State == StateRunning && ws.Project != "" && ws.HubToken != "" {
		if err := h.opts.Hub.Grant(ws.ID, ws.Project, ws.HubToken); err != nil {
			return Workspace{}, err
		}
	} else {
		h.opts.Hub.Revoke(ws.ID)
	}
	return ws, nil
}

// Executor returns the executor an agent uses to reach the workspace. The
// workspace must be running.
func (h *Host) Executor(ws Workspace) (executor.Executor, error) {
	if ws.Address == "" {
		return nil, fmt.Errorf("workspace %s has no address: it is not running", ws.ID)
	}
	return sandbox.New(sandbox.Options{BaseURL: ws.Address, Token: ws.Token, Root: Root})
}

// Terminal opens an interactive shell in a running workspace, for the person
// using it. It is kept apart from Executor so that nothing holding an
// executor, which is every tool, can reach a terminal.
func (h *Host) Terminal(ctx context.Context, ws Workspace, rows, cols uint16) (*websocket.Conn, error) {
	if ws.Address == "" {
		return nil, fmt.Errorf("workspace %s has no address: it is not running", ws.ID)
	}
	c, err := sandbox.New(sandbox.Options{BaseURL: ws.Address, Token: ws.Token, Root: Root})
	if err != nil {
		return nil, err
	}
	return c.Terminal(ctx, rows, cols)
}

// Process starts a long-lived process in a running workspace with its
// standard streams connected to the caller: how the harness runs a stdio MCP
// server where the agent's processes run. Like Terminal, it is kept apart
// from Executor, so no tool can hold a process of its own.
func (h *Host) Process(ctx context.Context, ws Workspace, spec sandbox.ProcessSpec, stderr func(string)) (io.ReadWriteCloser, error) {
	if ws.Address == "" {
		return nil, fmt.Errorf("workspace %s has no address: it is not running", ws.ID)
	}
	c, err := sandbox.New(sandbox.Options{BaseURL: ws.Address, Token: ws.Token, Root: Root})
	if err != nil {
		return nil, err
	}
	return c.Process(ctx, spec, stderr)
}

// address is the base URL the harness reaches the workspace's daemon on: the
// container name on the sandbox network, or the published port when the
// harness runs outside that network.
func (h *Host) address(ctx context.Context, ws Workspace) (string, error) {
	if h.opts.Network != "" {
		return "http://" + ContainerName(ws.ID) + ":" + DaemonPort, nil
	}
	info, err := h.docker.ContainerInspect(ctx, ws.ContainerID)
	if err != nil {
		return "", fmt.Errorf("inspect workspace %s: %w", ws.ID, err)
	}
	bindings := info.NetworkSettings.Ports[nat.Port(DaemonPort+"/tcp")]
	if len(bindings) == 0 {
		return "", fmt.Errorf("workspace %s publishes no daemon port", ws.ID)
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
