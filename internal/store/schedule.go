package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/schedule"
	"github.com/jackc/pgx/v5"
)

// Routine is the stored definition of a scheduled job.
type Routine struct {
	ID, Name, Description, ScheduleKind, ScheduleExpr, Timezone, Agent, ProjectID, Prompt, Missed, CreatedBy string
	Enabled                                                                                                  bool
	MaxRuntime, Grace                                                                                        time.Duration
	ApprovedAt, NextRunAt                                                                                    time.Time
}

// CreateRoutine stores a routine and calculates its first occurrence.
func (s *Store) CreateRoutine(ctx context.Context, r Routine) (Routine, error) {
	spec, err := schedule.Parse(r.ScheduleKind, r.ScheduleExpr, r.Timezone)
	if err != nil {
		return Routine{}, err
	}
	if r.ID == "" {
		r.ID = NewID()
	}
	if r.MaxRuntime == 0 {
		r.MaxRuntime = 24 * time.Hour
	}
	if r.Grace == 0 {
		r.Grace = 6 * time.Hour
	}
	if r.Missed == "" {
		r.Missed = "run_once"
	}
	if r.NextRunAt.IsZero() {
		r.NextRunAt = spec.Next(time.Now())
	}
	var approved, next *time.Time
	if !r.ApprovedAt.IsZero() {
		approved = &r.ApprovedAt
	}
	if !r.NextRunAt.IsZero() {
		next = &r.NextRunAt
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO routines (id,name,description,enabled,schedule_kind,schedule_expr,timezone,agent,project_id,prompt,max_runtime_seconds,missed,grace_seconds,created_by,approved_at,next_run_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, r.ID, r.Name, r.Description, r.Enabled, r.ScheduleKind, r.ScheduleExpr, r.Timezone, r.Agent, nullable(r.ProjectID), r.Prompt, int64(r.MaxRuntime/time.Second), r.Missed, int64(r.Grace/time.Second), r.CreatedBy, approved, next)
	if err != nil {
		return Routine{}, wrap("create routine", err)
	}
	return r, nil
}

// CreateWakeup stores a request to resume a session.
func (s *Store) CreateWakeup(ctx context.Context, w schedule.Wakeup) (schedule.Wakeup, error) {
	if w.ID == "" {
		w.ID = NewID()
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO wakeups (id,session_id,agent,note,due_at) VALUES ($1,$2,$3,$4,$5)`, w.ID, w.SessionID, w.Agent, w.Note, w.DueAt)
	if err != nil {
		return schedule.Wakeup{}, wrap("create wakeup", err)
	}
	return w, nil
}

// RecoverSchedule records interrupted runs and releases wakeup claims at startup.
func (s *Store) RecoverSchedule(ctx context.Context) error {
	return s.tx(ctx, func(q querier) error {
		_, err := q.Exec(ctx, `UPDATE routine_runs SET outcome='interrupted',reason='server restarted',finished_at=now(),duration_ms=GREATEST(0, EXTRACT(EPOCH FROM (now()-started_at))*1000)::bigint WHERE outcome='running'`)
		if err != nil {
			return wrap("interrupt routine runs", err)
		}
		_, err = q.Exec(ctx, `UPDATE routines r SET last_run_at=x.started_at,last_outcome=x.outcome,last_reason=x.reason FROM (SELECT DISTINCT ON (routine_id) routine_id,started_at,outcome,reason FROM routine_runs ORDER BY routine_id,started_at DESC,id DESC) x WHERE r.id=x.routine_id`)
		if err != nil {
			return wrap("recover routine summaries", err)
		}
		_, err = q.Exec(ctx, `UPDATE wakeups SET state='pending' WHERE state='claimed'`)
		if err != nil {
			return wrap("recover wakeups", err)
		}
		return nil
	})
}

// ClaimRoutine advances one due schedule and records either its run or its skip.
func (s *Store) ClaimRoutine(ctx context.Context, now time.Time) (schedule.Routine, bool, error) {
	for {
		var out schedule.Routine
		found, skipped := false, false
		err := s.tx(ctx, func(q querier) error {
			var kind, expr, zone string
			var project *string
			var due time.Time
			var maxSeconds, grace int64
			var running bool
			err := q.QueryRow(ctx, `SELECT r.id,r.name,r.agent,r.project_id,r.prompt,r.schedule_kind,r.schedule_expr,r.timezone,r.next_run_at,r.max_runtime_seconds,r.grace_seconds,EXISTS (SELECT 1 FROM routine_runs x WHERE x.routine_id=r.id AND x.outcome='running') FROM routines r WHERE r.enabled AND r.approved_at IS NOT NULL AND r.next_run_at <= $1 AND NOT (SELECT halted FROM halt) ORDER BY r.next_run_at,r.id FOR UPDATE OF r SKIP LOCKED LIMIT 1`, now).Scan(&out.ID, &out.Name, &out.Agent, &project, &out.Prompt, &kind, &expr, &zone, &due, &maxSeconds, &grace, &running)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return wrap("claim routine", err)
			}
			found = true
			out.ProjectID = text(project)
			out.MaxRuntime = time.Duration(maxSeconds) * time.Second
			spec, err := schedule.Parse(kind, expr, zone)
			if err != nil {
				return fmt.Errorf("parse routine %s: %w", out.ID, err)
			}
			next := spec.Next(now)
			var nextPtr *time.Time
			if !next.IsZero() {
				nextPtr = &next
			}
			if _, err = q.Exec(ctx, `UPDATE routines SET next_run_at=$2 WHERE id=$1`, out.ID, nextPtr); err != nil {
				return wrap("advance routine", err)
			}
			outcome, reason := "running", ""
			if now.Sub(due) > time.Duration(grace)*time.Second {
				outcome, reason = "skipped", "missed grace window"
			} else if running {
				outcome, reason = "skipped", "previous run still running"
			}
			out.RunID = NewID()
			skipped = outcome == "skipped"
			_, err = q.Exec(ctx, `INSERT INTO routine_runs (id,routine_id,scheduled_at,outcome,reason,started_at,finished_at,duration_ms) VALUES ($1,$2,$3,$4::text,$5,$6::timestamptz,CASE WHEN $4::text='skipped' THEN $6::timestamptz END,CASE WHEN $4::text='skipped' THEN 0 END)`, out.RunID, out.ID, due, outcome, reason, now)
			if err != nil {
				return wrap("record routine run", err)
			}
			_, err = q.Exec(ctx, `UPDATE routines SET last_run_at=$2,last_outcome=$3,last_reason=$4 WHERE id=$1`, out.ID, now, outcome, reason)
			if err != nil {
				return wrap("update routine summary", err)
			}
			return nil
		})
		if err != nil || !found {
			return schedule.Routine{}, false, err
		}
		if !skipped {
			return out, true, nil
		}
	}
}

// ClaimWakeup reserves one due wakeup for delivery.
func (s *Store) ClaimWakeup(ctx context.Context, now time.Time) (schedule.Wakeup, bool, error) {
	var w schedule.Wakeup
	err := s.pool.QueryRow(ctx, `UPDATE wakeups SET state='claimed' WHERE id=(SELECT id FROM wakeups WHERE state='pending' AND due_at <= $1 AND NOT (SELECT halted FROM halt) ORDER BY due_at,id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,session_id,agent,note,due_at`, now).Scan(&w.ID, &w.SessionID, &w.Agent, &w.Note, &w.DueAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, false, nil
	}
	if err != nil {
		return w, false, wrap("claim wakeup", err)
	}
	return w, true, nil
}

// AttachRoutineSession records the session before starting its agent run.
func (s *Store) AttachRoutineSession(ctx context.Context, runID, sessionID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE routine_runs SET session_id=$2 WHERE id=$1 AND outcome='running'`, runID, sessionID)
	if err != nil {
		return wrap("attach routine session", err)
	}
	return nil
}

// FinishRoutine records a completed run and its token count.
func (s *Store) FinishRoutine(ctx context.Context, id, sessionID, outcome, reason string, now time.Time) error {
	return s.tx(ctx, func(q querier) error {
		var routineID string
		var started time.Time
		err := q.QueryRow(ctx, `UPDATE routine_runs rr SET session_id=COALESCE($2::text,rr.session_id),outcome=$3,reason=$4,finished_at=$5::timestamptz,duration_ms=GREATEST(0,EXTRACT(EPOCH FROM ($5::timestamptz-rr.started_at))*1000)::bigint,tokens=(SELECT SUM(total_tokens) FROM model_calls WHERE session_id=COALESCE($2::text,rr.session_id)) WHERE rr.id=$1 AND rr.outcome='running' RETURNING rr.routine_id,rr.started_at`, id, nullable(sessionID), outcome, reason, now).Scan(&routineID, &started)
		if err != nil {
			return wrap("finish routine", err)
		}
		_, err = q.Exec(ctx, `UPDATE routines SET last_run_at=$2::timestamptz,last_outcome=$3,last_reason=$4 WHERE id=$1 AND last_run_at=$2::timestamptz`, routineID, started, outcome, reason)
		if err != nil {
			return wrap("finish routine summary", err)
		}
		return nil
	})
}

// FinishWakeup completes a wakeup or returns it to the pending queue.
func (s *Store) FinishWakeup(ctx context.Context, id string, done bool) error {
	if done {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE wakeups SET state='pending' WHERE id=$1 AND state='claimed'`, id)
	if err != nil {
		return wrap("release wakeup", err)
	}
	return nil
}

// AppendWakeup stores a wakeup message and completes its claim atomically.
func (s *Store) AppendWakeup(ctx context.Context, id, sessionID string, entry Entry) error {
	return s.tx(ctx, func(q querier) error {
		var state string
		err := q.QueryRow(ctx, `SELECT state FROM wakeups WHERE id=$1 AND session_id=$2 FOR UPDATE`, id, sessionID).Scan(&state)
		if err != nil {
			return wrap("read wakeup", err)
		}
		if state == "done" {
			return nil
		}
		if state != "claimed" {
			return fmt.Errorf("wakeup is not claimed")
		}
		head, err := lockSessionHead(ctx, q, sessionID)
		if err != nil {
			return err
		}
		entry.ID = "wakeup-" + id
		added, err := insertEntry(ctx, q, sessionID, head, entry)
		if err != nil {
			return err
		}
		if err = setHead(ctx, q, sessionID, added.ID); err != nil {
			return err
		}
		_, err = q.Exec(ctx, `UPDATE wakeups SET state='done' WHERE id=$1`, id)
		if err != nil {
			return wrap("complete wakeup", err)
		}
		return nil
	})
}

// CompletedRoutine identifies a finished routine result for notice recovery.
type CompletedRoutine struct{ RunID, Name, SessionID string }

// CompletedRoutines returns the results whose home notices can be recovered.
func (s *Store) CompletedRoutines(ctx context.Context) ([]CompletedRoutine, error) {
	rows, err := s.pool.Query(ctx, `SELECT x.id,r.name,x.session_id FROM routine_runs x JOIN routines r ON r.id=x.routine_id WHERE x.outcome='done' AND x.session_id IS NOT NULL`)
	if err != nil {
		return nil, wrap("list routine results", err)
	}
	defer rows.Close()
	var out []CompletedRoutine
	for rows.Next() {
		var r CompletedRoutine
		if err = rows.Scan(&r.RunID, &r.Name, &r.SessionID); err != nil {
			return nil, wrap("scan routine result", err)
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, wrap("list routine results", err)
	}
	return out, nil
}
