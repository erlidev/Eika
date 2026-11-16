package computer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/executor/sandbox"
)

// Label is the Docker label every computer container and volume carries,
// naming the computer it belongs to.
const Label = "eika.computer"

// The uid and gid every computer container runs as. A sandbox never runs as
// root.
const (
	userID  = 1000
	groupID = 1000
)

// DaemonPort is the port eikad listens on inside a computer container.
const DaemonPort = "7000"

// Root is where a computer's files live inside its container: the home
// directory of the user it runs as.
const Root = sandbox.DefaultRoot

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
	Image string
	// Confinement bounds what the container may consume and reach.
	Confinement Confinement
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
