package server

import (
	"time"

	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/store"
)

type routineRequest struct {
	Name              *string `json:"name"`
	Description       *string `json:"description"`
	ScheduleKind      *string `json:"schedule_kind"`
	ScheduleExpr      *string `json:"schedule_expr"`
	Timezone          *string `json:"timezone"`
	Agent             *string `json:"agent"`
	ProjectID         *string `json:"project_id"`
	Prompt            *string `json:"prompt"`
	Enabled           *bool   `json:"enabled"`
	MaxRuntimeSeconds *int64  `json:"max_runtime_seconds"`
	GraceSeconds      *int64  `json:"grace_seconds"`
	Missed            *string `json:"missed"`
}
type routineBody struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Description       string                `json:"description"`
	Enabled           bool                  `json:"enabled"`
	ScheduleKind      string                `json:"schedule_kind"`
	ScheduleExpr      string                `json:"schedule_expr"`
	Timezone          string                `json:"timezone"`
	Agent             string                `json:"agent"`
	ProjectID         string                `json:"project_id"`
	Prompt            string                `json:"prompt"`
	MaxRuntimeSeconds int64                 `json:"max_runtime_seconds"`
	GraceSeconds      int64                 `json:"grace_seconds"`
	Missed            string                `json:"missed"`
	CreatedBy         string                `json:"created_by"`
	ApprovedAt        *time.Time            `json:"approved_at"`
	NextRunAt         *time.Time            `json:"next_run_at"`
	ScheduleWords     string                `json:"schedule_words"`
	NextFive          []time.Time           `json:"next_five"`
	Warning           sentry.RoutineWarning `json:"warning"`
	LastRunAt         *time.Time            `json:"last_run_at"`
	LastOutcome       string                `json:"last_outcome"`
	LastReason        string                `json:"last_reason"`
}
type routinesResponse struct {
	Routines []routineBody `json:"routines"`
}
type routineRunsResponse struct {
	Runs []store.RoutineRun `json:"runs"`
}
type wakeupsResponse struct {
	Wakeups []store.WakeupRecord `json:"wakeups"`
}

type schedulePreviewRequest struct {
	ScheduleKind string `json:"schedule_kind"`
	ScheduleExpr string `json:"schedule_expr"`
	Timezone     string `json:"timezone"`
}
type schedulePreviewResponse struct {
	ScheduleWords string      `json:"schedule_words"`
	NextFive      []time.Time `json:"next_five"`
}
