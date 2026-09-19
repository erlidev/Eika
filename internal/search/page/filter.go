package page

import "time"

// FilterTimeout bounds one filter's run.
const FilterTimeout = 2 * time.Second

// FilterRequest is what a filter runs over: the page, or the section of it
// the read selected, and the JavaScript the model wrote. It is the stdin of
// `eikad filter`, which runs the filter inside the workspace sandbox.
type FilterRequest struct {
	Markdown string `json:"markdown"`
	Source   string `json:"source"`
	// TimeoutMS bounds the run, FilterTimeout when zero.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// Tokens is the budget the filter's answer is cut to, ContentTokens
	// when zero.
	Tokens int `json:"tokens,omitempty"`
}

// The kinds of filter outcome.
const (
	// FilterOK is a filter that selected something.
	FilterOK = "ok"
	// FilterEmpty is a filter that ran and selected nothing. Its text is a
	// map of the page, which is what the model writes its retry against, so
	// it is not a failure.
	FilterEmpty = "empty"
	// FilterError is a filter that did not compile, threw, timed out, or
	// returned something that cannot be rendered.
	FilterError = "error"
)

// FilterOutcome is what a filter produced. It is the stdout of `eikad
// filter`.
type FilterOutcome struct {
	Kind string `json:"kind"`
	// Text is the answer cut to the budget for FilterOK, and the message
	// for the other kinds.
	Text string `json:"text"`
	// Footer states the answer's coordinate space. It is set only when the
	// answer fit the budget; a cut answer ends with its truncation note.
	Footer string `json:"footer,omitempty"`
	// Truncated reports that the answer was cut to the budget.
	Truncated bool        `json:"truncated,omitempty"`
	Stats     FilterStats `json:"stats"`
}

// FilterStats measure a filter's run.
type FilterStats struct {
	Sections int `json:"sections"`
	// KeptSections is how many sections the answer holds, known only when
	// the filter returned sections.
	KeptSections *int  `json:"kept_sections,omitempty"`
	Lines        int   `json:"lines"`
	TotalTokens  int   `json:"total_tokens"`
	KeptTokens   int   `json:"kept_tokens"`
	SandboxMS    int64 `json:"sandbox_ms"`
}
