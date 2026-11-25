package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/skill"
)

// The user's skills API: install by zip upload, list, read, enable, disable
// and delete. The skill installer is the only writer; every change publishes
// skills.changed after it is durable. A skill is never enabled by an install,
// and no agent endpoint exists.

const (
	// catalogCountSession names the token counter that sizes the skills
	// catalog on enable. It is not a session row.
	catalogCountSession = "skills-catalog"
	// multipartOverhead is what a multipart body may hold beyond the zip
	// itself: boundaries and part headers.
	multipartOverhead  = 64 << 10
	maxSkillFieldBytes = 1 << 10
)

func errSkillsBusy(why string) error {
	return apiError{status: http.StatusServiceUnavailable, code: codeInternal, message: why}
}

// skillInstallerFor returns the installer, or 503 until the skills rows have
// been reconciled with the volume (or on a host that cannot run it).
func (s *Server) skillInstallerFor() (*skill.Installer, error) {
	if in := s.SkillInstaller(); in != nil {
		return in, nil
	}
	return nil, errSkillsBusy("skills are not available right now: the skills installer is not up")
}

// skillError maps what the installer returns onto the API's errors.
func skillError(err error) error {
	var invalid *skill.InvalidError
	switch {
	case errors.As(err, &invalid):
		return invalidf("%s", invalid.Error())
	case errors.Is(err, skill.ErrNotFound):
		return notFoundf("no skill with that name")
	case errors.Is(err, skill.ErrBroken):
		return conflictf("this skill is broken (%s); install it again to repair it", brokenReason(err))
	case errors.Is(err, skill.ErrCatalogOverBudget):
		return conflictf("%s; disable another skill first", err.Error())
	case errors.Is(err, skill.ErrNotExact):
		return errSkillsBusy("the catalog cannot be checked right now: the model's token count is not exact")
	}
	return err
}

func brokenReason(err error) string {
	msg := err.Error()
	if prefix := skill.ErrBroken.Error() + ": "; len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return msg[len(prefix):]
	}
	return "its files do not match"
}

func skillName(r *http.Request) (string, error) {
	name := r.PathValue("name")
	if !skill.ValidName(name) {
		return "", notFoundf("no skill with that name")
	}
	return name, nil
}

func skillView(rec skill.Record) skillBody {
	b := skillBody{
		Name: rec.Name, Description: rec.Description, License: rec.License, Compatibility: rec.Compatibility,
		Metadata: rec.Metadata, AllowedTools: rec.AllowedTools, Body: rec.Body, Enabled: rec.Enabled,
		Broken: rec.Broken, SHA: rec.SHA, InstalledAt: rec.InstalledAt, Files: rec.Files,
		Warnings: rec.Warnings, Screen: rec.Screen,
	}
	if b.Metadata == nil {
		b.Metadata = map[string]string{}
	}
	if b.Files == nil {
		b.Files = []skill.FileInfo{}
	}
	if b.Warnings == nil {
		b.Warnings = []string{}
	}
	return b
}

func skillListItem(rec skill.Record) skillItem {
	it := skillItem{
		Name: rec.Name, Description: rec.Description, Enabled: rec.Enabled, Broken: rec.Broken,
		InstalledAt: rec.InstalledAt, SHA: rec.SHA, FileCount: len(rec.Files), WarningCount: len(rec.Warnings),
	}
	if rec.Screen != nil {
		it.SpanCount, it.Unscanned = len(rec.Screen.Spans), rec.Screen.Unscanned
	}
	return it
}

// publishSkillsChange publishes skills.changed once the change is durable.
func (s *Server) publishSkillsChange(ctx context.Context, name, change string) {
	if s.deps.Bus == nil {
		return
	}
	if e, err := event.New(event.TypeSkillsChanged, event.TopicSkills, event.SkillsChanged{Name: name, Change: change}); err == nil {
		s.deps.Bus.Emit(ctx, e)
	}
}

// auditSkill records a user's change in Sentry's audit log, without content.
func (s *Server) auditSkill(ctx context.Context, name, action string) {
	b, _ := json.Marshal(map[string]any{})
	err := s.deps.Store.AppendAudit(ctx, sentry.AuditEntry{Event: "setting", Kind: "skill", Name: name, Reason: action, Detail: b})
	if err != nil {
		s.log.Error("audit skill change", "skill", name, "action", action, "error", err)
	}
}

// readSkillUpload reads the one file part of a multipart body, bounded: the
// whole body by zip size plus framing, the zip by skill.MaxZipBytes.
func readSkillUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		return nil, invalidf("send the zip as multipart/form-data in a part named file")
	}
	limit := int64(skill.MaxZipBytes + multipartOverhead)
	if r.ContentLength > limit {
		return nil, tooLargef("the upload is over the limit of %d bytes", skill.MaxZipBytes)
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, invalidf("read upload: %v", err)
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var zip []byte
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, uploadReadError(err)
		}
		if part.FormName() != "file" || zip != nil {
			return nil, invalidf("the upload takes one part, named file")
		}
		zip, err = io.ReadAll(io.LimitReader(part, skill.MaxZipBytes+1))
		if err != nil {
			return nil, uploadReadError(err)
		}
		if len(zip) > skill.MaxZipBytes {
			return nil, tooLargef("the zip is over the limit of %d bytes", skill.MaxZipBytes)
		}
	}
	if len(zip) == 0 {
		return nil, invalidf("the upload has no file part, or it is empty")
	}
	return zip, nil
}

func uploadReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return tooLargef("the upload is over the limit of %d bytes", skill.MaxZipBytes)
	}
	return invalidf("read upload: %v", err)
}

func (s *Server) handleInstallSkill(w http.ResponseWriter, r *http.Request) {
	in, err := s.skillInstallerFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	zip, err := readSkillUpload(w, r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	res, err := in.Install(r.Context(), zip)
	if err != nil {
		s.fail(w, r, skillError(err))
		return
	}
	if !res.Unchanged {
		s.auditSkill(r.Context(), res.Record.Name, "install")
		s.publishSkillsChange(r.Context(), res.Record.Name, "installed")
	}
	writeJSON(w, s.log, http.StatusCreated, skillInstallResponse{Skill: skillView(res.Record), Unchanged: res.Unchanged})
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	if _, err := s.skillInstallerFor(); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.deps.Store.ListSkills(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := skillsResponse{Skills: []skillItem{}}
	for _, rec := range rows {
		out.Skills = append(out.Skills, skillListItem(rec))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	name, err := skillName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.skillInstallerFor(); err != nil {
		s.fail(w, r, err)
		return
	}
	rec, err := s.deps.Store.GetSkill(r.Context(), name)
	if err != nil {
		s.fail(w, r, skillError(err))
		return
	}
	writeJSON(w, s.log, http.StatusOK, skillView(rec))
}

// handleSetSkillEnabled enables or disables a skill. Enabling is the user's
// explicit act and checks the catalog against its exact token budget with the
// model's own counter; no exact count is a 503 and nothing changes.
func (s *Server) handleSetSkillEnabled(enable bool) http.HandlerFunc {
	action := "disable"
	if enable {
		action = "enable"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		name, err := skillName(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		in, err := s.skillInstallerFor()
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var check func([]skill.Record) error
		if enable {
			count, release := s.exactCounter(catalogCountSession)
			defer release()
			check = func(after []skill.Record) error {
				ctx, cancel := context.WithTimeout(r.Context(), 2*agent.DefaultCountTimeout)
				defer cancel()
				sec, err := skill.BuildCatalog(ctx, count, after)
				if err == nil && sec.Overflow > 0 {
					err = fmt.Errorf("%w: %d skills do not fit in %d tokens", skill.ErrCatalogOverBudget, sec.Overflow, skill.CatalogBudgetTokens)
				}
				return err
			}
		}
		before, _ := s.deps.Store.GetSkill(r.Context(), name)
		rec, err := in.SetEnabled(r.Context(), name, enable, check)
		if err != nil {
			s.fail(w, r, skillError(err))
			return
		}
		if before.Enabled != rec.Enabled {
			s.auditSkill(r.Context(), name, action)
			s.publishSkillsChange(r.Context(), name, action+"d")
		}
		writeJSON(w, s.log, http.StatusOK, skillView(rec))
	}
}

func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	name, err := skillName(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in, err := s.skillInstallerFor()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := in.Delete(r.Context(), name); err != nil {
		s.fail(w, r, skillError(err))
		return
	}
	s.auditSkill(r.Context(), name, "delete")
	s.publishSkillsChange(r.Context(), name, "deleted")
	w.WriteHeader(http.StatusNoContent)
}
