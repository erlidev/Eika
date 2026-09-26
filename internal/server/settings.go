package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/erlidev/eika/internal/egress"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
	"github.com/erlidev/eika/internal/utility"
	"github.com/erlidev/eika/internal/workspace"
)

// The settings keys the harness reads itself. Each is validated when it is
// written; every other key belongs to the UI and is stored as it comes.
const (
	// settingDefaultModel names the model a run uses when the request names
	// none.
	settingDefaultModel = "default_model"
	// settingDefaultProfile is the id of the profile a session runs with
	// when it chose none.
	settingDefaultProfile = "default_profile"
	// settingSandboxImage is the image a workspace runs when it names none.
	settingSandboxImage = "sandbox_image"
	// settingSubagentDepth is how many levels of children a session may have.
	settingSubagentDepth = "subagent_max_depth"
	// settingSubagentChildren is how many children of one session may run at
	// a time.
	settingSubagentChildren = "subagent_max_children"
	// settingSandboxLimits are the limits a new workspace gets when the
	// request that creates it names none.
	settingSandboxLimits = "sandbox_limits"
	// settingSandboxEgress is the egress mode and allowlist a new workspace
	// gets when the request that creates it names none.
	settingSandboxEgress = "sandbox_egress"
	// settingSetupComplete records that the user finished or skipped the
	// guided setup, so the UI stops offering it.
	settingSetupComplete = "setup_complete"
	// settingUtilityModels assigns the harness's own tasks the names of the
	// models they are sent to; a task it assigns none does not run.
	settingUtilityModels = "utility_models"
)

// The subagent limits until the user sets them, and the most the settings
// accept: a deeper or wider tree than this is a runaway, not a plan.
const (
	defaultSubagentDepth    = 2
	defaultSubagentChildren = 4
	maxSubagentDepth        = 8
	maxSubagentChildren     = 16
)

// maxSettingKey bounds a setting key.
const maxSettingKey = 64

// settingsResponse is the body of GET and PUT /api/settings: the settings
// table as one object, and what the harness uses for its own keys while the
// table does not name them.
type settingsResponse struct {
	Settings map[string]json.RawMessage `json:"settings"`
	Defaults settingsDefaults           `json:"defaults"`
}

// settingsDefaults are the values the harness's own keys have until the user
// sets them.
type settingsDefaults struct {
	SandboxImage        string `json:"sandbox_image"`
	SubagentMaxDepth    int    `json:"subagent_max_depth"`
	SubagentMaxChildren int    `json:"subagent_max_children"`
	// SandboxLimits and SandboxEgress are what a new workspace gets.
	SandboxLimits limitsBody `json:"sandbox_limits"`
	SandboxEgress egressBody `json:"sandbox_egress"`
	// SearchOrder is every web provider in its default order.
	SearchOrder []string `json:"search_order"`
	// SearchLimits is every quota bucket's default limit.
	SearchLimits map[string]search.Limit `json:"search_limits"`
}

// handleSettings returns every setting.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.writeSettings(w, r, http.StatusOK)
}

// handlePutSettings writes the keys the body carries, leaving the others
// alone, and returns the whole table as it now stands. The keys the harness
// reads are checked first, and the keys are written in one
// transaction, so a bad value or a failed write leaves the table as it was.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[map[string]json.RawMessage](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for key, value := range req {
		if err := s.validateSetting(r.Context(), key, value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := s.deps.Store.SetSettings(r.Context(), req); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("settings written", "keys", len(req))
	s.writeSettings(w, r, http.StatusOK)
}

// writeSettings answers with the settings table and the defaults.
func (s *Server) writeSettings(w http.ResponseWriter, r *http.Request, status int) {
	rows, err := s.deps.Store.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	sandbox := builtinSandboxDefaults()
	defaults := settingsDefaults{
		SandboxImage:        s.cfg.SandboxImage,
		SubagentMaxDepth:    defaultSubagentDepth,
		SubagentMaxChildren: defaultSubagentChildren,
		SandboxLimits:       sandbox.Limits,
		SandboxEgress:       sandbox.Egress,
		SearchOrder:         []string{},
		SearchLimits:        map[string]search.Limit{},
	}
	if s.deps.Search != nil {
		defaults.SearchOrder, defaults.SearchLimits = s.deps.Search.Web(), s.deps.Search.DefaultLimits()
	}
	writeJSON(w, s.log, status, settingsResponse{Settings: out, Defaults: defaults})
}

// validateSetting rejects a value the harness could not use for one of its
// own keys. JSON null is accepted for every key and means "use the default".
func (s *Server) validateSetting(ctx context.Context, key string, value json.RawMessage) error {
	if key == "" || len(key) > maxSettingKey {
		return invalidf("a setting key must be 1 to %d characters", maxSettingKey)
	}
	if string(value) == "null" {
		return nil
	}
	switch key {
	case settingDefaultModel:
		var name string
		if err := json.Unmarshal(value, &name); err != nil {
			return invalidf("%s must be a model name", key)
		}
		return s.checkModelName(ctx, key, name)
	case settingUtilityModels:
		var assigned map[utility.Task]string
		if err := json.Unmarshal(value, &assigned); err != nil {
			return invalidf("%s must be an object of task names to model names", key)
		}
		for task, name := range assigned {
			if !utility.Known(task) {
				return invalidf("%s names %q, which is not a task", key, task)
			}
			if err := s.checkModelName(ctx, key, name); err != nil {
				return err
			}
		}
	case settingDefaultProfile:
		var id string
		if err := json.Unmarshal(value, &id); err != nil {
			return invalidf("%s must be a profile id", key)
		}
		if id == "" {
			return nil
		}
		if _, err := s.deps.Store.Profile(ctx, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return invalidf("%s names %q, which is not a profile", key, id)
			}
			return err
		}
	case settingSandboxImage:
		var image string
		if err := json.Unmarshal(value, &image); err != nil {
			return invalidf("%s must be an image reference", key)
		}
		if image == "" || len(image) > 255 || strings.IndexFunc(image, unicode.IsSpace) >= 0 {
			return invalidf("%s must be an image reference such as eika-sandbox:latest", key)
		}
	case settingSandboxLimits:
		var limits limitsBody
		if err := strictJSON(value, &limits); err != nil {
			return invalidf("%s must be an object of cpus, memory_mb, and pids", key)
		}
		return s.checkLimits(ctx, limits)
	case settingSandboxEgress:
		var e egressBody
		if err := strictJSON(value, &e); err != nil {
			return invalidf("%s must be an object of mode and allow", key)
		}
		if _, err := checkEgress(e); err != nil {
			return err
		}
		if e.Mode != string(egress.ModeOpen) && !s.deps.Workspaces.EgressControl() {
			return workspace.ErrNoEgressControl
		}
	case settingSubagentDepth:
		return validateCount(key, value, maxSubagentDepth)
	case settingSubagentChildren:
		return validateCount(key, value, maxSubagentChildren)
	case settingSearchOrder, settingSearchLimits:
		if s.deps.Search == nil {
			return nil
		}
		if key == settingSearchOrder {
			return s.validateSearchOrder(value)
		}
		return s.validateSearchLimits(value)
	case settingSetupComplete:
		var done bool
		if err := json.Unmarshal(value, &done); err != nil {
			return invalidf("%s must be true or false", key)
		}
	}
	return nil
}

// checkModelName accepts the name of a configured model, or the empty name
// for none.
func (s *Server) checkModelName(ctx context.Context, key, name string) error {
	if name == "" {
		return nil
	}
	if _, err := s.deps.Store.ModelByName(ctx, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return invalidf("%s names %q, which is not a configured model", key, name)
		}
		return err
	}
	return nil
}

// strictJSON decodes a setting's value, refusing a field v does not have.
func strictJSON(value json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// validateCount accepts a whole number from one to limit.
func validateCount(key string, value json.RawMessage, limit int) error {
	var n int
	if err := json.Unmarshal(value, &n); err != nil || n < 1 || n > limit {
		return invalidf("%s must be a whole number from 1 to %d", key, limit)
	}
	return nil
}

// readSetting decodes one setting into v and reports whether it held a
// usable value. A missing key, JSON null, and a value of the wrong shape all
// mean "use the default"; only the last is worth a log line.
func readSetting(ctx context.Context, st *store.Store, log *slog.Logger, key string, v any) bool {
	setting, err := st.Setting(ctx, key)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error("read setting", "key", key, "error", err)
		}
		return false
	}
	if string(setting.Value) == "null" {
		return false
	}
	if err := json.Unmarshal(setting.Value, v); err != nil {
		log.Warn("setting has the wrong shape", "key", key, "value", string(setting.Value))
		return false
	}
	return true
}

// defaultModelSetting returns the model the settings name as the default, or
// an empty string when they name none.
func (s *Server) defaultModelSetting(ctx context.Context) string {
	var name string
	readSetting(ctx, s.deps.Store, s.log, settingDefaultModel, &name)
	return name
}

// utilityModel returns the model the settings assign task, and false when
// they assign none or name a model that no longer exists.
func (s *Server) utilityModel(ctx context.Context, task utility.Task) (store.Model, bool, error) {
	var assigned map[utility.Task]string
	readSetting(ctx, s.deps.Store, s.log, settingUtilityModels, &assigned)
	name := assigned[task]
	if name == "" {
		return store.Model{}, false, nil
	}
	m, err := s.deps.Store.ModelByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		s.log.Warn("utility model setting names no model", "task", task, "model", name)
		return store.Model{}, false, nil
	}
	if err != nil {
		return store.Model{}, false, err
	}
	return m, true, nil
}

// renameModelSettings makes the settings that name a model by oldName name
// it by newName, so that renaming a model keeps it the default and keeps the
// tasks assigned to it.
func (s *Server) renameModelSettings(ctx context.Context, oldName, newName string) error {
	writes := map[string]json.RawMessage{}
	if s.defaultModelSetting(ctx) == oldName {
		writes[settingDefaultModel] = jsonString(newName)
	}
	var assigned map[utility.Task]string
	if readSetting(ctx, s.deps.Store, s.log, settingUtilityModels, &assigned) {
		renamed := false
		for task, name := range assigned {
			if name == oldName {
				assigned[task], renamed = newName, true
			}
		}
		if renamed {
			value, err := json.Marshal(assigned)
			if err != nil {
				return fmt.Errorf("encode %s: %w", settingUtilityModels, err)
			}
			writes[settingUtilityModels] = value
		}
	}
	if len(writes) == 0 {
		return nil
	}
	return s.deps.Store.SetSettings(ctx, writes)
}

// sandboxImage returns the image a new workspace runs when the request names
// none: the settings' choice, or the deployment's default.
func (s *Server) sandboxImage(ctx context.Context) string {
	var image string
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxImage, &image) && image != "" {
		return image
	}
	return s.cfg.SandboxImage
}

// subagentLimits returns the function the spawner reads its limits with,
// which consults the settings at every spawn.
func subagentLimits(st *store.Store, log *slog.Logger) func(context.Context) subagent.Limits {
	return func(ctx context.Context) subagent.Limits {
		limits := subagent.Limits{MaxDepth: defaultSubagentDepth, MaxChildren: defaultSubagentChildren}
		var n int
		if readSetting(ctx, st, log, settingSubagentDepth, &n) && n >= 1 {
			limits.MaxDepth = min(n, maxSubagentDepth)
		}
		if readSetting(ctx, st, log, settingSubagentChildren, &n) && n >= 1 {
			limits.MaxChildren = min(n, maxSubagentChildren)
		}
		return limits
	}
}
