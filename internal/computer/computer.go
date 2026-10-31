package computer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Label is the Docker label every computer container carries, so that the
// harness can find its containers again after a restart.
const Label = "eika.computer"

// DefaultUser is the user every computer container runs as unless the spec
// says otherwise. A sandbox never runs as root.
const DefaultUser = "1000:1000"

// DaemonPort is the port eikad listens on inside a computer container.
const DaemonPort = "7000"

// Root is where a computer's files live inside its container.
const Root = "/workspace"

// State is where a computer is in its lifecycle.
type State string

// The states a computer can be in. They mirror the container's state; a
// computer whose container is gone is StateGone.
const (
	StateCreating State = "creating"
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateGone     State = "gone"
)

// Limits bounds what a computer may consume. A zero field means no limit.
type Limits struct {
	// CPUs is the number of cores the container may use, as a fraction.
	CPUs float64
	// MemoryBytes is the memory limit. The container gets no swap beyond it,
	// so the limit is what the processes in it can hold.
	MemoryBytes int64
	// PIDs is the maximum number of processes and threads.
	PIDs int64
}

// Confinement is what a computer may consume and reach. It is set when the
// computer is created and can be changed while it exists, running or not,
// with Host.Confine.
type Confinement struct {
	// Limits bounds the container's resources.
	Limits Limits
	// Proxied puts the container on the internal sandbox network, which has
	// no route out, and points its processes at the harness's egress proxy,
	// which decides what they may reach. False gives it the open sandbox
	// network and a direct route to the internet.
	Proxied bool
}

// Spec describes a computer to create.
type Spec struct {
	// ID is the computer id. Empty generates one.
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
	// User is the user the container runs as. Empty means DefaultUser. A
	// custom image must have that uid, or name its own user here.
	User string
	// Confinement bounds what the container may consume and reach.
	Confinement Confinement
	// Labels are added to the container alongside the computer label.
	Labels map[string]string
}

// Computer is one sandbox container and its volume.
type Computer struct {
	// ID identifies the computer everywhere: container name, volume name,
	// and label value.
	ID string
	// ContainerID is the Docker container id.
	ContainerID string
	// Image is the image the container runs.
	Image string
	// State is the computer's lifecycle state.
	State State
	// Token is the computer's eikad token.
	Token string
	// ProxyToken is the password the computer presents to the egress proxy,
	// which knows it by its id and this token.
	ProxyToken string
	// Volume is the name of the volume mounted at the computer root.
	Volume string
	// Address is the base URL of the computer's eikad daemon as the harness
	// reaches it.
	Address string
	// Proxied reports that the container is on the internal sandbox network
	// and reaches out only through the egress proxy.
	Proxied bool
	// CreatedAt is when the container was created, in UTC.
	CreatedAt time.Time
}

// ContainerName is the Docker name of a computer's container. It doubles as
// the hostname other containers reach it on.
func ContainerName(id string) string { return "eika-cx-" + id }

// VolumeName is the Docker name of a computer's volume.
func VolumeName(id string) string { return "eika-cx-" + id }

// builtImageRef is the tag given to an image built for one computer from a
// spec's build context. It is removed together with the computer.
func builtImageRef(id string) string { return ContainerName(id) + ":latest" }

// newID returns a short random computer id.
func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate computer id: %w", err)
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
