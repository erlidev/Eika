//go:build live

package live_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"net/http/httptest"
	"net/url"

	"github.com/erlidev/eika/evals/injection"
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
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Minute)
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

	fixtureLog := newFixtureLog()
	fixture := httptest.NewServer(fixtureLog)
	defer fixture.Close()
	fixtureURL, err := url.Parse(fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	fixtureURL.Host = injection.Host + ":" + fixtureURL.Port()
	injectionScenarios(t, report, target, fixtureURL.String(), env[live.GuardVar])
	additionalInjectionScenarios(t, report, target, fixtureURL.String(), env[live.GuardVar], fixtureLog)
	exitScenarios(t, report, target, fixtureURL.String(), env[live.GuardVar])
	memoryScenarios(t, report, target, env[live.GuardVar])
	ordinaryPreferenceScenarios(t, report, target, env[live.GuardVar])
	core := newLiveCore(t, target, env[live.GuardVar])
	t.Run("persistent_home_exit", func(t *testing.T) {
		started := time.Now()
		err := core.exitScenario()
		result := live.Result{Name: "persistent_home_exit", Passed: err == nil, Seconds: time.Since(started).Seconds(), Calls: []live.Call{}, Notes: []string{"The follow-up is immediate; the suite does not wait a day."}}
		if err != nil {
			result.Error = err.Error()
			t.Error(err)
		}
		report.Add(result)
	})

	for _, scenario := range []struct {
		name string
		run  func() error
	}{
		{"persistent_home_restart", core.restartScenario},
		{"persistent_home_compaction", func() error { return core.compactScenario(target.Server.Flags.Context) }},
		{"persistent_home_prefix", core.prefixScenario},
		{"persistent_home_queue", core.queueScenario},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			started := time.Now()
			err := scenario.run()
			result := live.Result{Name: scenario.name, Passed: err == nil, Seconds: time.Since(started).Seconds(), Calls: []live.Call{}, Notes: []string{}}
			if scenario.name == "persistent_home_compaction" {
				result.Notes = append(result.Notes, core.compactionNotes...)
			}
			if scenario.name == "persistent_home_prefix" {
				result.Notes = append(result.Notes, core.prefixNotes...)
			}
			if scenario.name == "persistent_home_restart" && err != nil {
				result.Notes = append(result.Notes, core.restartNotes...)
			}
			if scenario.name == "persistent_home_queue" {
				result.Notes = append(result.Notes, core.queueNotes...)
			}
			if err != nil {
				result.Error = err.Error()
				t.Error(err)
			}
			report.Add(result)
			for _, note := range result.Notes {
				t.Log(note)
			}
		})
	}

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
			if res.SkipReason != "" {
				t.Skip(res.SkipReason)
			}
			if res.Error != "" {
				t.Error(res.Error)
			}
		})
	}
}
