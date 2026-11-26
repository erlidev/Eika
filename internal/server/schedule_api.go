package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/schedule"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

func routineView(r store.RoutineRecord) routineBody {
	spec, _ := schedule.Parse(r.ScheduleKind, r.ScheduleExpr, r.Timezone)
	next := spec.NextN(time.Now(), 5)
	if next == nil {
		next = []time.Time{}
	}
	var approved, due *time.Time
	if !r.ApprovedAt.IsZero() {
		approved = &r.ApprovedAt
	}
	if !r.NextRunAt.IsZero() {
		due = &r.NextRunAt
	}
	return routineBody{ID: r.ID, Name: r.Name, Description: r.Description, Enabled: r.Enabled, ScheduleKind: r.ScheduleKind, ScheduleExpr: r.ScheduleExpr, Timezone: r.Timezone, Agent: r.Agent, ProjectID: r.ProjectID, Prompt: r.Prompt, MaxRuntimeSeconds: int64(r.MaxRuntime / time.Second), GraceSeconds: int64(r.Grace / time.Second), Missed: r.Missed, CreatedBy: r.CreatedBy, ApprovedAt: approved, NextRunAt: due, ScheduleWords: spec.Words(), NextFive: next, Warning: r.Warning, LastRunAt: r.LastRunAt, LastOutcome: r.LastOutcome, LastReason: r.LastReason}
}
func (s *Server) screenRoutine(ctx context.Context, prompt string) (sentry.RoutineWarning, error) {
	if c, ok := s.gate.(sentry.RoutineChecker); ok {
		return c.CheckRoutine(ctx, prompt)
	}
	return sentry.RoutineWarning{Spans: []sentry.RoutineSpan{}, Unscanned: true}, nil
}
func applyRoutine(r *store.Routine, q routineRequest) {
	if q.Name != nil {
		r.Name = *q.Name
	}
	if q.Description != nil {
		r.Description = *q.Description
	}
	if q.ScheduleKind != nil {
		r.ScheduleKind = *q.ScheduleKind
	}
	if q.ScheduleExpr != nil {
		r.ScheduleExpr = *q.ScheduleExpr
	}
	if q.Timezone != nil {
		r.Timezone = *q.Timezone
	}
	if q.Agent != nil {
		r.Agent = *q.Agent
	}
	if q.ProjectID != nil {
		r.ProjectID = *q.ProjectID
	}
	if q.Prompt != nil {
		r.Prompt = *q.Prompt
	}
	if q.MaxRuntimeSeconds != nil {
		r.MaxRuntime = time.Duration(*q.MaxRuntimeSeconds) * time.Second
	}
	if q.GraceSeconds != nil {
		r.Grace = time.Duration(*q.GraceSeconds) * time.Second
	}
	if q.Missed != nil {
		r.Missed = *q.Missed
	}
}
func validateRoutine(r store.Routine) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 200 || len(r.Description) > 4000 || strings.TrimSpace(r.Prompt) == "" || len(r.Prompt) > 32000 || len(r.ScheduleExpr) > 256 || len(r.Timezone) > 128 || len(r.ProjectID) > 128 {
		return invalidf("invalid routine name, description or prompt")
	}
	if _, ok := agentdef.Lookup(r.Agent); !ok {
		return invalidf("unknown agent")
	}
	if r.MaxRuntime < time.Second || r.MaxRuntime > 30*24*time.Hour || r.Grace < 0 || r.Grace > 30*24*time.Hour || r.Missed != "run_once" {
		return invalidf("invalid runtime, grace or missed policy")
	}
	if _, err := schedule.Parse(r.ScheduleKind, r.ScheduleExpr, r.Timezone); err != nil {
		return invalidf("invalid schedule: %v", err)
	}
	return nil
}
func (s *Server) validateRoutineProject(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := s.deps.Store.Project(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return invalidf("project not found")
	}
	return err
}
func validateRoutineRequest(q routineRequest) error {
	if q.MaxRuntimeSeconds != nil && (*q.MaxRuntimeSeconds < 1 || *q.MaxRuntimeSeconds > 30*24*3600) {
		return invalidf("invalid max runtime")
	}
	if q.GraceSeconds != nil && (*q.GraceSeconds < 0 || *q.GraceSeconds > 30*24*3600) {
		return invalidf("invalid grace")
	}
	return nil
}
func (s *Server) createRoutine(ctx context.Context, q routineRequest, creator string) (routineBody, error) {
	if err := validateRoutineRequest(q); err != nil {
		return routineBody{}, err
	}
	r := store.Routine{CreatedBy: creator, MaxRuntime: 24 * time.Hour, Grace: 6 * time.Hour, Missed: "run_once"}
	applyRoutine(&r, q)
	r.GraceSet = q.GraceSeconds != nil
	if err := validateRoutine(r); err != nil {
		return routineBody{}, err
	}
	if err := s.validateRoutineProject(ctx, r.ProjectID); err != nil {
		return routineBody{}, err
	}
	var err error
	r.Warning, err = s.screenRoutine(ctx, r.Prompt)
	if err != nil {
		return routineBody{}, err
	}
	if creator == "user" {
		r.ApprovedAt = time.Now().UTC()
		r.Enabled = q.Enabled != nil && *q.Enabled
	}
	r, err = s.deps.Store.CreateRoutine(ctx, r)
	if err != nil {
		return routineBody{}, err
	}
	if creator != "user" {
		s.notifications.send("A routine awaits approval")
	}
	return routineView(store.RoutineRecord{Routine: r}), nil
}
func (s *Server) updateRoutine(ctx context.Context, id, creator string, q routineRequest) (routineBody, error) {
	if err := validateRoutineRequest(q); err != nil {
		return routineBody{}, err
	}
	r, err := s.deps.Store.RoutineRecord(ctx, id)
	if err != nil {
		return routineBody{}, err
	}
	if creator != "user" && r.CreatedBy != creator {
		return routineBody{}, store.ErrNotFound
	}
	if creator != "user" && q.Enabled != nil {
		return routineBody{}, invalidf("only the user can enable a routine")
	}
	oldEnabled := r.Enabled
	applyRoutine(&r.Routine, q)
	if err = validateRoutine(r.Routine); err != nil {
		return routineBody{}, err
	}
	if q.ProjectID != nil {
		if err = s.validateRoutineProject(ctx, r.ProjectID); err != nil {
			return routineBody{}, err
		}
	}
	if q.Prompt != nil {
		r.Warning, err = s.screenRoutine(ctx, r.Prompt)
		if err != nil {
			return routineBody{}, err
		}
	}
	if creator != "user" {
		r.Enabled = false
		r.ApprovedAt = time.Time{}
	} else if q.Enabled != nil {
		r.Enabled = *q.Enabled
		if *q.Enabled && r.ApprovedAt.IsZero() {
			r.ApprovedAt = time.Now().UTC()
		}
	}
	if q.ScheduleKind != nil || q.ScheduleExpr != nil || q.Timezone != nil || (!oldEnabled && r.Enabled) {
		spec, _ := schedule.Parse(r.ScheduleKind, r.ScheduleExpr, r.Timezone)
		r.NextRunAt = spec.Next(time.Now())
	}
	r, err = s.deps.Store.SaveRoutine(ctx, r)
	if err != nil {
		return routineBody{}, err
	}
	return routineView(r), nil
}
func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	rs, err := s.deps.Store.RoutineRecords(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := routinesResponse{Routines: []routineBody{}}
	for _, v := range rs {
		out.Routines = append(out.Routines, routineView(v))
	}
	writeJSON(w, s.log, 200, out)
}
func (s *Server) handleRoutine(w http.ResponseWriter, r *http.Request) {
	v, err := s.deps.Store.RoutineRecord(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, 200, routineView(v))
}
func (s *Server) handleCreateRoutine(w http.ResponseWriter, r *http.Request) {
	q, err := decodeJSON[*routineRequest](r)
	if err == nil && q == nil {
		err = invalidf("routine must be an object")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.createRoutine(r.Context(), *q, "user")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, 201, v)
}
func (s *Server) handleUpdateRoutine(w http.ResponseWriter, r *http.Request) {
	q, err := decodeJSON[*routineRequest](r)
	if err == nil && q == nil {
		err = invalidf("routine must be an object")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.updateRoutine(r.Context(), r.PathValue("id"), "user", *q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, 200, v)
}
func (s *Server) handleDeleteRoutine(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.DeleteRoutine(r.Context(), r.PathValue("id"), ""); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) handleRoutineEnable(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.deps.Store.RoutineRecord(r.Context(), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		wasEnabled := v.Enabled
		v.Enabled = enabled
		if enabled {
			if !wasEnabled {
				spec, _ := schedule.Parse(v.ScheduleKind, v.ScheduleExpr, v.Timezone)
				v.NextRunAt = spec.Next(time.Now())
			}
			if v.ApprovedAt.IsZero() {
				v.ApprovedAt = time.Now().UTC()
			}
		}
		v, err = s.deps.Store.SaveRoutine(r.Context(), v)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, s.log, 200, routineView(v))
	}
}
func (s *Server) handleRoutineRuns(w http.ResponseWriter, r *http.Request) {
	v, err := s.deps.Store.RoutineRuns(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, 200, routineRunsResponse{Runs: v})
}
func (s *Server) handleWakeups(w http.ResponseWriter, r *http.Request) {
	v, err := s.deps.Store.WakeupRecords(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, 200, wakeupsResponse{Wakeups: v})
}
func (s *Server) handleCancelWakeup(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.CancelWakeup(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) scheduleTool(ctx context.Context, sessionID, name string, raw json.RawMessage) (string, error) {
	sess, err := s.deps.Store.Session(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if (name == "routine_create" || name == "wake_me") && sess.Kind == store.SessionRoutine {
		return "", fmt.Errorf("routine sessions cannot create routines or wakeups beyond their own session")
	}
	var result any
	switch name {
	case "routine_list":
		rs, e := s.deps.Store.RoutineRecords(ctx)
		if e != nil {
			return "", e
		}
		v := []routineBody{}
		for _, r := range rs {
			v = append(v, routineView(r))
		}
		result = v
	case "routine_create":
		var q routineRequest
		if err = json.Unmarshal(raw, &q); err != nil {
			return "", err
		}
		result, err = s.createRoutine(ctx, q, sessionID)
	case "routine_update":
		var m map[string]json.RawMessage
		if err = json.Unmarshal(raw, &m); err != nil {
			return "", err
		}
		var id string
		_ = json.Unmarshal(m["id"], &id)
		delete(m, "id")
		b, _ := json.Marshal(m)
		var q routineRequest
		if err = json.Unmarshal(b, &q); err != nil {
			return "", err
		}
		result, err = s.updateRoutine(ctx, id, sessionID, q)
	case "routine_delete":
		var q struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(raw, &q); err != nil {
			return "", err
		}
		err = s.deps.Store.DeleteRoutine(ctx, q.ID, sessionID)
		result = "deleted"
	case "wake_me":
		if sess.Kind != store.SessionHome && sess.Kind != store.SessionSubagent {
			return "", fmt.Errorf("wakeups require a continuing session")
		}
		var q struct {
			At   string `json:"at"`
			In   string `json:"in"`
			Note string `json:"note"`
		}
		if err = json.Unmarshal(raw, &q); err != nil {
			return "", err
		}
		if (q.At == "") == (q.In == "") || strings.TrimSpace(q.Note) == "" || len(q.Note) > 4000 {
			return "", fmt.Errorf("provide one time and a note")
		}
		var due time.Time
		if q.At != "" {
			due, err = time.Parse(time.RFC3339, q.At)
		} else {
			var d time.Duration
			d, err = time.ParseDuration(q.In)
			if err == nil {
				due = time.Now().Add(d)
			}
		}
		if err != nil || !due.After(time.Now()) {
			return "", fmt.Errorf("wakeup time must be in the future")
		}
		result, err = s.deps.Store.CreateWakeup(ctx, schedule.Wakeup{SessionID: sessionID, Agent: sess.Agent, Note: q.Note, DueAt: due.UTC()})
	default:
		return "", fmt.Errorf("unknown schedule tool")
	}
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(result)
	return string(b), err
}
