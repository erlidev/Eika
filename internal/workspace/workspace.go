package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Label is the Docker label every workspace container carries, so that the
// harness can find its containers again after a restart.
const Label = "eika.workspace"

// DaemonPort is the port eikad listens on inside a workspace container.
const DaemonPort = "7000"

// Root is where a workspace's files live inside its container.
const Root = "/workspace"

// State is where a workspace is in its lifecycle.
type State string

// The states a workspace can be in. They mirror the container's state; a
// workspace whose container is gone is StateGone.
const (
	StateCreating State = "creating"
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateGone     State = "gone"
)

// Limits bounds what a workspace may consume. A zero field means no limit.
type Limits struct {
	// CPUs is the number of cores the container may use, as a fraction.
	CPUs float64
	// MemoryBytes is the memory limit.
	MemoryBytes int64
	// PIDs is the maximum number of processes.
	PIDs int64
}

// Spec describes a workspace to create.
type Spec struct {
	// ID is the workspace id. Empty generates one.
	ID string
	// Image is the container image. Empty uses the host's default image.
	// Ignored when BuildContext is set.
	Image string
	// BuildContext is a directory holding a Dockerfile and its context. When
	// set, the image is built from it before the container is created.
	BuildContext string
	// Dockerfile is the Dockerfile's name within BuildContext. Empty means
	// "Dockerfile".
	Dockerfile string
	// Env holds additional KEY=VALUE entries for every process in the
	// container.
	Env []string
	// HostPath bind-mounts a host directory at the workspace root instead of
	// creating a volume. This is local project mode; the path is resolved by
	// the Docker daemon, so it is a path on the host, not in the harness.
	HostPath string
	// Limits bounds the container's resources.
	Limits Limits
	// Labels are added to the container alongside the workspace label.
	Labels map[string]string
}

// Workspace is one sandbox container and its volume.
type Workspace struct {
	// ID identifies the workspace everywhere: container name, volume name,
	// label value, and hub user.
	ID string
	// ContainerID is the Docker container id.
	ContainerID string
	// Image is the image the container runs.
	Image string
	// State is the workspace's lifecycle state.
	State State
	// Token is the workspace's eikad token.
	Token string
	// HubToken is the token the workspace uses to clone from and push to the
	// hub.
	HubToken string
	// Volume is the name of the volume mounted at the workspace root, empty
	// in local project mode.
	Volume string
	// HostPath is the bind-mounted host directory in local project mode.
	HostPath string
	// Address is the base URL of the workspace's eikad daemon as the harness
	// reaches it.
	Address string
	// CreatedAt is when the container was created, in UTC.
	CreatedAt time.Time
}

// ContainerName is the Docker name of a workspace's container. It doubles as
// the hostname other containers reach it on.
func ContainerName(id string) string { return "eika-ws-" + id }

// VolumeName is the Docker name of a workspace's volume.
func VolumeName(id string) string { return "eika-ws-" + id }

// builtImageRef is the tag given to an image built for one workspace from a
// spec's build context. It is removed together with the workspace.
func builtImageRef(id string) string { return ContainerName(id) + ":latest" }

// newID returns a short random workspace id.
func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate workspace id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newToken returns a random bearer token.
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
