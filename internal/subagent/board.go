package subagent

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/store"
)

// Bounds on what one board row repeats of a child's longer texts. The whole
// text stays with the child, where subagent_status shows it.
const (
	boardReasonRunes = 600
	boardReportRunes = 300
)

// boardTimeLayout writes a time on the board to the minute. The board's
// heading names the zone.
const boardTimeLayout = "2006-01-02 15:04"

// Board renders the [Subagents] section of a home compaction summary
// (runtime.md 11) from the children's stored rows: the open ones, running or
// waiting, then those active since the previous compaction at since, which is
// zero before the first. Each group is newest activity first. One line counts
// the children left out. projects maps project ids to slugs, and times are in
// loc. The text has no blank line, so it stays one section.
func Board(children []store.Session, projects map[string]string, since time.Time, loc *time.Location) string {
	var open, recent []store.Session
	for _, c := range children {
		switch {
		case c.State == store.SessionRunning || c.State == store.SessionWaiting:
			open = append(open, c)
		case c.LastActiveAt.After(since):
			recent = append(recent, c)
		}
	}
	var lines []string
	for _, group := range [][]store.Session{open, recent} {
		slices.SortStableFunc(group, func(a, b store.Session) int {
			if c := b.LastActiveAt.Compare(a.LastActiveAt); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
		for _, c := range group {
			lines = append(lines, boardRow(c, projects, loc)...)
		}
	}
	switch rest := len(children) - len(open) - len(recent); {
	case rest == 1:
		lines = append(lines, "1 other subagent has had no activity since the last compaction; subagent_status lists it.")
	case rest > 1:
		lines = append(lines, fmt.Sprintf("%d other subagents have had no activity since the last compaction; subagent_status lists them.", rest))
	case len(lines) == 0:
		lines = append(lines, "None.")
	}
	return strings.Join(lines, "\n")
}

// boardRow is one child on the board: name, agent and project, state and last
// activity, then what it waits on and the first line of its last report.
func boardRow(c store.Session, projects map[string]string, loc *time.Location) []string {
	who := c.Agent
	if c.ProjectID != "" {
		slug := projects[c.ProjectID]
		if slug == "" {
			slug = c.ProjectID
		}
		who += ", project " + slug
	}
	row := fmt.Sprintf("- %s (%s): %s", c.Name, who, c.State)
	if !c.LastActiveAt.IsZero() {
		row += ", last active " + c.LastActiveAt.In(loc).Format(boardTimeLayout)
	}
	lines := []string{row}
	label := "waiting on: "
	if c.State == store.SessionFailed || c.State == store.SessionCancelled {
		label = "ended because: "
	}
	if reason := strings.TrimSpace(c.WaitingReason); reason != "" {
		first := true
		for line := range strings.SplitSeq(clipRunes(reason, boardReasonRunes), "\n") {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			if first {
				lines = append(lines, "  "+label+line)
				first = false
			} else {
				lines = append(lines, "    "+line)
			}
		}
	}
	if report, _, _ := strings.Cut(strings.TrimSpace(c.Result), "\n"); report != "" {
		lines = append(lines, "  last report: "+clipRunes(strings.TrimSpace(report), boardReportRunes))
	}
	return lines
}

// clipRunes cuts text to n characters, marking the cut with an ellipsis.
func clipRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}
