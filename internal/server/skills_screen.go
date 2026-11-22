package server

import (
	"bytes"
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/skill"
)

// skillScreener is the installer's screen (P6-05): Sentry's skill check, never
// Gate.Ingest. A credential pattern in any text file rejects the install and
// names the pattern and file, never the value. Otherwise the description and
// SKILL.md body are screened and the result is stored with the row; a hit
// never blocks and never enables, and a guard that did not answer stores
// unscanned.
func skillScreener(c sentry.SkillChecker) skill.Screener {
	return func(ctx context.Context, a skill.Archive, s skill.Skill) (*skill.ScreenResult, error) {
		cand := sentry.SkillCandidate{Name: s.Name, Description: s.Description}
		for _, f := range a.Files {
			if !utf8.Valid(f.Data) || bytes.IndexByte(f.Data, 0) >= 0 {
				continue
			}
			if f.Path == "SKILL.md" {
				cand.SKILL = string(f.Data)
				cand.BodyStart = len(f.Data) - len(s.Body)
				continue
			}
			cand.Files = append(cand.Files, sentry.SkillFile{Path: f.Path, Text: string(f.Data)})
		}
		v, err := c.CheckSkill(ctx, cand)
		if err != nil {
			return nil, fmt.Errorf("screen skill: %w", err)
		}
		if v.Pattern != "" {
			return nil, fmt.Errorf("%s contains a credential (%s); remove it and install again", v.Path, v.Pattern)
		}
		res := &skill.ScreenResult{Spans: []skill.ScreenSpan{}, MaxScore: v.MaxScore, Unscanned: v.Unscanned}
		for _, sp := range v.Spans {
			res.Spans = append(res.Spans, skill.ScreenSpan(sp))
		}
		return res, nil
	}
}

// screenSkills points the installer at Sentry's check. With no gate that can
// check skills the default stays: unscanned.
func (s *Server) screenSkills(in *skill.Installer) {
	if c, ok := s.gate.(sentry.SkillChecker); ok {
		in.SetScreener(skillScreener(c))
	}
}
