package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/erlidev/eika/internal/schedule"
	"github.com/jackc/pgx/v5"
)

// RoutineRecord adds the last run and revision to a stored routine.
type RoutineRecord struct {
	Routine
	Revision                int64
	LastRunAt               *time.Time
	LastOutcome, LastReason string
}

const routineColumns = `id,name,description,enabled,schedule_kind,schedule_expr,timezone,agent,project_id,prompt,max_runtime_seconds,missed,grace_seconds,created_by,approved_at,next_run_at,warning,revision,last_run_at,last_outcome,last_reason`

func scanRoutine(row pgx.Row) (RoutineRecord, error) {
	var r RoutineRecord
	var project *string
	var approved, next *time.Time
	var maxSeconds, grace int64
	var warning []byte
	err := row.Scan(&r.ID, &r.Name, &r.Description, &r.Enabled, &r.ScheduleKind, &r.ScheduleExpr, &r.Timezone, &r.Agent, &project, &r.Prompt, &maxSeconds, &r.Missed, &grace, &r.CreatedBy, &approved, &next, &warning, &r.Revision, &r.LastRunAt, &r.LastOutcome, &r.LastReason)
	if err != nil {
		return r, wrap("read routine", err)
	}
	r.ProjectID = text(project)
	r.MaxRuntime = time.Duration(maxSeconds) * time.Second
	r.Grace = time.Duration(grace) * time.Second
	if approved != nil {
		r.ApprovedAt = *approved
	}
	if next != nil {
		r.NextRunAt = *next
	}
	if err = json.Unmarshal(warning, &r.Warning); err != nil {
		return r, err
	}
	return r, nil
}

// RoutineRecord returns one routine.
func (s *Store) RoutineRecord(ctx context.Context, id string) (RoutineRecord, error) {
	return scanRoutine(s.pool.QueryRow(ctx, `SELECT `+routineColumns+` FROM routines WHERE id=$1 AND deleted_at IS NULL`, id))
}

// RoutineRecords lists routines.
func (s *Store) RoutineRecords(ctx context.Context) ([]RoutineRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+routineColumns+` FROM routines WHERE deleted_at IS NULL ORDER BY name,id`)
	if err != nil {
		return nil, wrap("list routines", err)
	}
	defer rows.Close()
	out := []RoutineRecord{}
	for rows.Next() {
		r, e := scanRoutine(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRoutine replaces a routine only if its revision is unchanged.
func (s *Store) SaveRoutine(ctx context.Context, r RoutineRecord) (RoutineRecord, error) {
	_, err := schedule.Parse(r.ScheduleKind, r.ScheduleExpr, r.Timezone)
	if err != nil {
		return r, err
	}
	next := r.NextRunAt
	var nextPtr *time.Time
	if !next.IsZero() {
		nextPtr = &next
	}
	warning, err := json.Marshal(r.Warning)
	if err != nil {
		return r, err
	}
	var approved *time.Time
	if !r.ApprovedAt.IsZero() {
		approved = &r.ApprovedAt
	}
	tag, err := s.pool.Exec(ctx, `UPDATE routines SET name=$3,description=$4,enabled=$5,schedule_kind=$6,schedule_expr=$7,timezone=$8,agent=$9,project_id=$10,prompt=$11,max_runtime_seconds=$12,missed=$13,grace_seconds=$14,approved_at=$15,next_run_at=$16,warning=$17,revision=revision+1 WHERE id=$1 AND revision=$2 AND deleted_at IS NULL`, r.ID, r.Revision, r.Name, r.Description, r.Enabled, r.ScheduleKind, r.ScheduleExpr, r.Timezone, r.Agent, nullable(r.ProjectID), r.Prompt, int64(r.MaxRuntime/time.Second), r.Missed, int64(r.Grace/time.Second), approved, nextPtr, warning)
	if err != nil {
		return r, wrap("save routine", err)
	}
	if tag.RowsAffected() == 0 {
		return r, ErrConflict
	}
	return s.RoutineRecord(ctx, r.ID)
}

// DeleteRoutine hides a routine while retaining its run history.
func (s *Store) DeleteRoutine(ctx context.Context, id, creator string) error {
	return s.tx(ctx, func(q querier) error {
		var owner string
		if err := q.QueryRow(ctx, `SELECT created_by FROM routines WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&owner); err != nil {
			return wrap("read routine", err)
		}
		if creator != "" && owner != creator {
			return ErrNotFound
		}
		var running bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM routine_runs WHERE routine_id=$1 AND outcome='running')`, id).Scan(&running); err != nil {
			return err
		}
		if running {
			return ErrConflict
		}
		if _, err := q.Exec(ctx, `DELETE FROM routine_runs WHERE routine_id=$1`, id); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `DELETE FROM routines WHERE id=$1 AND deleted_at IS NULL`, id)
		return err
	})
}

// RoutineRun is one recorded execution of a routine.
type RoutineRun struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	Outcome     string     `json:"outcome"`
	Reason      string     `json:"reason"`
	Tokens      *int64     `json:"tokens"`
	DurationMS  *int64     `json:"duration_ms"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// RoutineRuns lists a routine's history.
func (s *Store) RoutineRuns(ctx context.Context, id string) ([]RoutineRun, error) {
	if _, err := s.RoutineRecord(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,COALESCE(session_id,''),scheduled_at,outcome,reason,tokens,duration_ms,started_at,finished_at FROM routine_runs WHERE routine_id=$1 ORDER BY started_at DESC,id DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RoutineRun{}
	for rows.Next() {
		var r RoutineRun
		if err = rows.Scan(&r.ID, &r.SessionID, &r.ScheduledAt, &r.Outcome, &r.Reason, &r.Tokens, &r.DurationMS, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// WakeupRecord is a wakeup and its current state.
type WakeupRecord struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Agent     string    `json:"agent"`
	Note      string    `json:"note"`
	DueAt     time.Time `json:"due_at"`
	State     string    `json:"state"`
}

// WakeupRecords lists pending and claimed wakeups.
func (s *Store) WakeupRecords(ctx context.Context) ([]WakeupRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,session_id,agent,note,due_at,state FROM wakeups WHERE state IN ('pending','claimed') ORDER BY due_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WakeupRecord{}
	for rows.Next() {
		var w WakeupRecord
		if err = rows.Scan(&w.ID, &w.SessionID, &w.Agent, &w.Note, &w.DueAt, &w.State); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CancelWakeup cancels a pending wakeup before it is claimed.
func (s *Store) CancelWakeup(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE wakeups SET state='cancelled' WHERE id=$1 AND state='pending'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
