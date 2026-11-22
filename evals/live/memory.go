package live

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MemoryReport is the memory state a Core scenario reports beside its
// result: the counts by state, whether the home preamble carries the memory,
// and what the run asked for and pushed.
type MemoryReport struct {
	// Active, Pending and Archived count the index rows by state.
	Active   int `json:"active"`
	Pending  int `json:"pending"`
	Archived int `json:"archived"`
	// InPreamble is whether the newest home preamble's memory section names
	// the memory the scenario saved, and Indexed is whether that section has
	// an index line (not only a pinned body) for it.
	InPreamble bool `json:"in_preamble"`
	Indexed    bool `json:"indexed"`
	// TaskDone is whether every home run of the scenario ended done.
	TaskDone bool `json:"task_done"`
	// Asks is how many ask_user calls home made; Pushes how many
	// notifications reached the fake topic.
	Asks   int `json:"asks"`
	Pushes int `json:"pushes"`
}

// PreambleMemory returns the memory section stored in a preamble entry's
// payload, empty when the preamble has none.
func PreambleMemory(payload []byte) (string, error) {
	var p struct {
		Memory *struct {
			Text string `json:"text"`
		} `json:"memory"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("decode preamble: %w", err)
	}
	if p.Memory == nil {
		return "", nil
	}
	return p.Memory.Text, nil
}

// SectionNames reports whether a memory section names the memory, and
// whether it does so as an index line ("- name: description").
func SectionNames(section, name string) (named, indexed bool) {
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "- "+name+":") {
			return true, true
		}
		if strings.HasPrefix(line, "["+name+"]") {
			named = true
		}
	}
	return named, false
}
