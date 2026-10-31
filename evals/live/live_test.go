//go:build live

package live_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/erlidev/eika/evals/live"
)

// resultsDir is where the report goes, relative to this package. The
// directory is ignored by git.
var resultsDir = filepath.Join("..", "results")

// TestScenarios plays every scenario against the bench's chat model and
// writes the report. It skips, saying why, when EIKA_LIVE_ENDPOINT is not
// set, and fails when it is set but the endpoint does not answer: a skipped
// live run would look like a pass.
func TestScenarios(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("find home directory: %v", err)
	}
	env, err := live.LoadEnv(live.EnvFile(home), os.LookupEnv)
	if err != nil {
		t.Fatalf("load live env: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	target, err := live.Connect(ctx, env)
	switch {
	case errors.Is(err, live.ErrNotConfigured):
		t.Skip(err)
	case err != nil:
		t.Fatalf("connect to the live endpoint: %v", err)
	}
	t.Logf("server %s, model %s (%s) revision %s", target.Server.Build, target.Server.Model,
		target.Server.ModelFile, target.Server.ModelRevision)

	report := live.NewReport(time.Now(), target.Server)
	defer func() {
		file, err := report.Write(resultsDir)
		if err != nil {
			t.Errorf("write report: %v", err)
			return
		}
		t.Logf("report: %s", file)
	}()

	for _, s := range live.Scenarios() {
		t.Run(s.Name, func(t *testing.T) {
			res := s.Execute(ctx, target)
			report.Add(res)
			for _, c := range res.Calls {
				t.Log(c)
			}
			for _, n := range res.Notes {
				t.Log(n)
			}
			if res.Error != "" {
				t.Error(res.Error)
			}
		})
	}
}
