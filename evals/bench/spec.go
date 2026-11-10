package bench

import "strings"

// Kind says which model a Spec serves and how.
type Kind string

const (
	// KindChat is the chat model's own container, started if it is stopped
	// and used as it is.
	KindChat Kind = "chat"
	// KindVariant is the chat model's image and mounts run as a container of
	// its own with other flags.
	KindVariant Kind = "variant"
	// KindTabby is the second backend's existing container, started as it is.
	KindTabby Kind = "tabby"
)

// Spec says what the server should serve.
type Spec struct {
	// Name labels the spec in the report, such as no-cache-idle-slots.
	Name string
	// Kind is the model to serve.
	Kind Kind
	// Edits are the changes a KindVariant makes to the chat model's recorded
	// flags.
	Edits []Edit
}

// Baseline serves the chat model as its Deployment starts it.
func Baseline() Spec { return Spec{Name: "baseline", Kind: KindChat} }

// Variant serves the chat model with its recorded flags edited. It never
// edits the Deployment, command.txt or komodo.toml: the changed model runs as
// a separate container with its own name, and the chat model's container is
// only stopped while it runs.
func Variant(name string, edits ...Edit) Spec {
	return Spec{Name: name, Kind: KindVariant, Edits: edits}
}

// Tabby serves the second backend from its existing container.
func Tabby() Spec { return Spec{Name: "tabbyapi", Kind: KindTabby} }

// Info is what the report records about the server a suite ran on, beside the
// build and model revision the endpoint states itself (docs/PLAN.md, "Live
// test bench": every report records the build, the model file's revision and
// the flags).
type Info struct {
	// Run identifies the runner's run, Host the server and Spec the spec.
	Run  string `json:"run"`
	Host string `json:"host"`
	Spec string `json:"spec"`
	// Container, Image and ImageID are what ran: the container, the image
	// reference and the id of the image's content.
	Container string `json:"container"`
	Image     string `json:"image"`
	ImageID   string `json:"image_id"`
	// Flags are the arguments the container ran with, credentials redacted.
	Flags []string `json:"flags"`
	// Edits are the changes made to the chat model's flags, empty for a spec
	// that changes none.
	Edits []string `json:"edits,omitempty"`
	// Config holds the settings of a backend that keeps them in a file:
	// TabbyAPI's model section.
	Config map[string]string `json:"config,omitempty"`
	// GPUUsedMiB is the GPU memory in use once the model had loaded.
	GPUUsedMiB int `json:"gpu_used_mib,omitempty"`
}

// String is the one-line form for a log.
func (i Info) String() string {
	s := i.Spec + ": container " + i.Container + " from " + i.Image
	if len(i.Edits) > 0 {
		s += " with " + strings.Join(i.Edits, ", ")
	}
	return s
}
