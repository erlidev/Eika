package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
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
	// Cases is the per-statement record of a preference scenario.
	Cases []MemoryCase `json:"cases,omitempty"`
}

// MemoryCase is one ordinary preference stated as its own user turn: whether
// home wrote a memory, the highest guard score of any text screened in the
// turn (the guard scores the memory's description and body), and the state
// of the memory it wrote.
type MemoryCase struct {
	Text     string   `json:"text"`
	Wrote    bool     `json:"wrote"`
	Writes   int      `json:"writes"`
	Names    []string `json:"names,omitempty"`
	State    string   `json:"state,omitempty"`
	MaxScore float64  `json:"max_score"`
	Screened int      `json:"screened"`
}

// ScoreLog sits in front of the real guard and forwards every /screen call
// untouched, remembering the highest window score the guard returned. The
// product's own write path makes the calls; the log only watches them.
type ScoreLog struct {
	proxy *httputil.ReverseProxy
	mu    sync.Mutex
	max   float64
	texts int
}

// NewScoreLog returns a log forwarding to the guard at target.
func NewScoreLog(target string) (*ScoreLog, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse guard url: %w", err)
	}
	l := &ScoreLog{proxy: httputil.NewSingleHostReverseProxy(u)}
	l.proxy.ModifyResponse = func(resp *http.Response) error {
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(body))
		l.observe(body)
		return nil
	}
	return l, nil
}

func (l *ScoreLog) ServeHTTP(w http.ResponseWriter, r *http.Request) { l.proxy.ServeHTTP(w, r) }

func (l *ScoreLog) observe(body []byte) {
	var resp struct {
		Results []struct {
			Windows []struct {
				Score float64 `json:"score"`
			} `json:"windows"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &resp) != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range resp.Results {
		l.texts++
		for _, w := range r.Windows {
			l.max = max(l.max, w.Score)
		}
	}
}

// Take returns the highest score and the number of texts screened since the
// last call, and resets both.
func (l *ScoreLog) Take() (maxScore float64, texts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	maxScore, texts = l.max, l.texts
	l.max, l.texts = 0, 0
	return maxScore, texts
}

// Table renders the cases as a markdown table.
func Table(cases []MemoryCase) string {
	var b strings.Builder
	b.WriteString("| # | statement | write | state | max score |\n|---|---|---|---|---|\n")
	for i, c := range cases {
		write, state := "no", "-"
		if c.Wrote {
			write, state = fmt.Sprintf("%d", c.Writes), c.State
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %.3f |\n", i+1, strings.ReplaceAll(c.Text, "|", "/"), write, state, c.MaxScore)
	}
	return b.String()
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
