package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/sentry/scan"
)

// SkillChecker is the install screen of docs/design/skills-and-plugins.md. Like
// MemoryChecker it never goes through Gate.Ingest: a skill must stay intact
// for the user to read. The gate returned by New implements it.
type SkillChecker interface {
	CheckSkill(ctx context.Context, c SkillCandidate) (SkillVerdict, error)
}

// SkillFile is one text file of a skill, for the credential scan.
type SkillFile struct {
	Path string
	Text string
}

// SkillCandidate is a skill about to be installed. SKILL is the stored
// SKILL.md and BodyStart the byte offset in it where the body begins. Files
// are the other text files (binary files are not passed).
type SkillCandidate struct {
	Name        string
	Description string
	SKILL       string
	BodyStart   int
	Files       []SkillFile
}

// SkillSpan is a screened window at or over the threshold. Field is
// "description" or "body". For "description", Start and End are byte offsets
// into the description; for "body" they are byte offsets into the stored
// SKILL.md, whatever invisible characters it holds. Text is the stored slice.
type SkillSpan struct {
	Field string  `json:"field"`
	Start int     `json:"start"`
	End   int     `json:"end"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

// SkillVerdict is CheckSkill's answer. A hit or an unavailable guard never
// blocks: only Pattern (a credential) rejects the install.
type SkillVerdict struct {
	// Pattern is the credential kind found, never its value; Path is the file.
	Pattern, Path string
	Spans         []SkillSpan
	Unscanned     bool
	MaxScore      float64
}

// EventSkillScreen is the audit event of a skill screened with a hit or
// without the guard. A credential reject reuses EventDeny.
const EventSkillScreen = "skill_screen"

// CheckSkill rejects a credential pattern in any text file, then screens the
// description and the SKILL.md body. The threshold is the memory one when set:
// skill text reaches the model's context the way a memory does. An error means
// the check could not run. Non-clean outcomes are audited without text.
func (g *gate) CheckSkill(ctx context.Context, c SkillCandidate) (SkillVerdict, error) {
	if err := ctx.Err(); err != nil {
		return SkillVerdict{}, fmt.Errorf("sentry: check skill: %w", err)
	}
	all := append([]SkillFile{{Path: "description", Text: c.Description}, {Path: "SKILL.md", Text: c.SKILL}}, c.Files...)
	for _, f := range all {
		if kind := scan.Find(scan.StripInvisible(strings.ToValidUTF8(f.Text, "�"))); kind != "" {
			v := SkillVerdict{Pattern: kind, Path: f.Path}
			return v, g.auditSkill(ctx, c, EventDeny, v)
		}
	}
	threshold := g.Threshold
	if g.ScreenSettings != nil {
		var err error
		if threshold, _, err = g.ScreenSettings(ctx); err != nil {
			return SkillVerdict{}, fmt.Errorf("sentry: read screen settings: %w", err)
		}
	}
	if g.MemoryThreshold != nil {
		var err error
		if threshold, err = g.MemoryThreshold(ctx); err != nil {
			return SkillVerdict{}, fmt.Errorf("sentry: read memory threshold: %w", err)
		}
	}
	body := c.SKILL[min(max(c.BodyStart, 0), len(c.SKILL)):]
	base := len(c.SKILL) - len(body)
	fields := []string{"description", "body"}
	stored := []string{c.Description, body}
	bases := []int{0, base}
	texts := make([]string, 2)
	maps := make([][]int, 2)
	for i, t := range stored {
		texts[i], maps[i] = scan.StripInvisibleMap(strings.ToValidUTF8(t, "�"))
	}
	windows, unscanned := g.score(ctx, []Segment{{Source: Source{Kind: SourceKind("skill"), Name: c.Name}}}, texts)
	if err := ctx.Err(); err != nil {
		return SkillVerdict{}, fmt.Errorf("sentry: check skill: %w", err)
	}
	if unscanned {
		v := SkillVerdict{Unscanned: true}
		return v, g.auditSkill(ctx, c, EventSkillScreen, v)
	}
	var v SkillVerdict
	for i, ws := range windows {
		for _, w := range ws {
			if w.Score < threshold {
				continue
			}
			s, e := runeStart(texts[i], max(w.Start, 0)), runeStart(texts[i], min(w.End, len(texts[i])))
			if s >= e {
				continue
			}
			os := maps[i][s]
			last := maps[i][e-1]
			_, n := utf8.DecodeRuneInString(stored[i][last:])
			oe := last + n
			v.Spans = append(v.Spans, SkillSpan{Field: fields[i], Start: bases[i] + os, End: bases[i] + oe, Score: w.Score, Text: stored[i][os:oe]})
			v.MaxScore = max(v.MaxScore, w.Score)
		}
	}
	if len(v.Spans) == 0 {
		return v, nil
	}
	sort.SliceStable(v.Spans, func(a, b int) bool {
		if v.Spans[a].Field != v.Spans[b].Field {
			return v.Spans[a].Field == "description"
		}
		return v.Spans[a].Start < v.Spans[b].Start
	})
	return v, g.auditSkill(ctx, c, EventSkillScreen, v)
}

func (g *gate) auditSkill(ctx context.Context, c SkillCandidate, event string, v SkillVerdict) error {
	d := map[string]any{}
	reason := ""
	switch {
	case v.Pattern != "":
		d["decision"], d["pattern"], d["file"] = "reject", v.Pattern, v.Path
		reason = "credential pattern in a skill file"
	case v.Unscanned:
		d["decision"], d["unscanned"] = "unscanned", true
	default:
		d["decision"], d["spans"], d["score"] = "flagged", len(v.Spans), v.MaxScore
	}
	detail, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("sentry: encode skill decision: %w", err)
	}
	if err := g.Audit.AppendAudit(ctx, AuditEntry{Event: event, Kind: "skill", Name: c.Name, Reason: reason, Detail: detail}); err != nil {
		return fmt.Errorf("sentry: audit skill: %w", err)
	}
	return nil
}
