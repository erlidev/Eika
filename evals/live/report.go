package live

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Report is what one run of the suite measured, with the server it measured
// it on, so a regression can be told apart from a model or server change.
type Report struct {
	// StartedAt is when the run began, in UTC.
	StartedAt time.Time `json:"started_at"`
	// Server is the build, model and settings the endpoint reported.
	Server ServerInfo `json:"server"`
	// Scenarios are the results, in the order they ran.
	Scenarios []Result `json:"scenarios"`
}

// NewReport starts the report of a run against server.
func NewReport(now time.Time, server ServerInfo) *Report {
	return &Report{StartedAt: now.UTC(), Server: server, Scenarios: []Result{}}
}

// Add appends the result of one scenario.
func (r *Report) Add(res Result) { r.Scenarios = append(r.Scenarios, res) }

// unsafeName matches what a build string may not put in a file name.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Write saves the report as JSON in dir, creating it, under a name that sorts
// by time and says which build ran, and returns the file's path.
func (r *Report) Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create report directory: %w", err)
	}
	name := r.StartedAt.Format("20060102T150405Z")
	if build := unsafeName.ReplaceAllString(r.Server.Build, "_"); build != "" {
		name += "-" + build
	}
	file := filepath.Join(dir, name+".json")
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode report: %w", err)
	}
	if err := os.WriteFile(file, append(raw, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write report: %w", err)
	}
	return file, nil
}
