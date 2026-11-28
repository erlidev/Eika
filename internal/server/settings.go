package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/erlidev/eika/internal/inference"
	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/sentry"
	"github.com/erlidev/eika/internal/sentry/trip"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
)

// The settings keys the harness reads itself. Each is validated when it is
// written; every other key belongs to the UI and is stored as it comes.
const (
	// settingDefaultModel names the model a run uses when the request names
	// none.
	settingDefaultModel    = "default_model"
	settingScreenRequired  = "screen_required"
	settingScreenThreshold = "screen_threshold"
	// settingMemoryScreenThreshold is the score at or over which a memory
	// write goes pending. Ingest never reads it.
	settingMemoryScreenThreshold = "memory_screen_threshold"
	// settingWatchThreshold is the score at or over which ingest watches the
	// destinations in a window. It never changes what is removed.
	settingWatchThreshold = "watch_threshold"
	// settingSensitiveList is the list of regular expressions that mark a file
	// path or a bash command as a read of something sensitive.
	settingSensitiveList = "sensitive_list"
	// settingSinkLimits holds the numbers of the upload and fan-out marks.
	settingSinkLimits = "sink_limits"
	// settingTripwires holds each tripwire's switch and its numbers. Every
	// tripwire is off until it says otherwise.
	settingTripwires = "tripwires"
	// settingSandboxImage is the image the home computer's container is
	// created from. A container that exists keeps the image it was created
	// from.
	settingSandboxImage = "sandbox_image"
	// settingSandboxLimits are the home computer's limits. Saving them applies
	// them to its container.
	settingSandboxLimits = "sandbox_limits"
	// settingSandboxEgress is the home computer's egress mode and allowlist.
	// Saving them applies them to its container.
	settingSandboxEgress = "sandbox_egress"
	// settingSetupComplete records that the user finished or skipped the
	// guided setup, so the UI stops offering it.
	settingSetupComplete = "setup_complete"
	// settingIdleMinutes is how long a running computer may go without a
	// run before the harness stops it; 0 never stops one.
	settingIdleMinutes        = "computer_idle_minutes"
	settingCompactIdleFill    = "compact_idle_fill"
	settingCompactIdleMinutes = "compact_idle_minutes"
	// settingSubagentMaxRunning is how many subagents may run at once.
	settingSubagentMaxRunning = "subagent_max_running"
)

// The idle timeout until the user sets one, and the longest the settings
// accept: a week, past which a timeout is the same as none.
const (
	defaultIdleMinutes        = 15
	defaultCompactIdleFill    = 60
	defaultCompactIdleMinutes = 30
	maxIdleMinutes            = 7 * 24 * 60
)

// The most subagents the settings accept as running at once. The default is
// subagent.DefaultMaxRunning.
const maxSubagentMaxRunning = 32

// maxSettingKey bounds a setting key.
const maxSettingKey = 64

// settingsResponse is the body of GET and PUT /api/settings: the settings
// table as one object, and what the harness uses for its own keys while the
// table does not name them.
type settingsResponse struct {
	Settings map[string]json.RawMessage `json:"settings"`
	Defaults settingsDefaults           `json:"defaults"`
	// Classes is every model class and the model that serves it now, fallbacks
	// applied, so a client never repeats the fallback rule.
	Classes []classBody `json:"classes"`
	// TitlesModel is the name of the model that makes session titles, and
	// empty when none does.
	TitlesModel string `json:"titles_model"`
}

// settingsDefaults are the values the harness's own keys have until the user
// sets them.
type settingsDefaults struct {
	ScreenRequired  bool    `json:"screen_required"`
	ScreenThreshold float64 `json:"screen_threshold"`
	// MemoryScreenThreshold is the score at or over which a memory write goes pending.
	MemoryScreenThreshold float64 `json:"memory_screen_threshold"`
	SandboxImage          string  `json:"sandbox_image"`
	// SandboxLimits and SandboxEgress are what the home computer gets until
	// the user sets others.
	SandboxLimits limitsBody `json:"sandbox_limits"`
	SandboxEgress egressBody `json:"sandbox_egress"`
	// SearchOrder is every web provider in its default order.
	SearchOrder []string `json:"search_order"`
	// SearchLimits is every quota bucket's default limit.
	SearchLimits map[string]search.Limit `json:"search_limits"`
	// Compaction is the compaction setting, with the built-in prompts.
	Compaction compactionBody `json:"compaction"`
	// ComputerIdleMinutes is how long a running computer may go without a
	// run before the harness stops it.
	ComputerIdleMinutes int `json:"computer_idle_minutes"`
	CompactIdleFill     int `json:"compact_idle_fill"`
	CompactIdleMinutes  int `json:"compact_idle_minutes"`
	// SubagentMaxRunning is how many subagents may run at once.
	SubagentMaxRunning int `json:"subagent_max_running"`
	// WakeupsMax is the pending wakeup limit per continuing session.
	WakeupsMax int `json:"wakeups_max"`
	// MaintenanceIdleSeconds, AgingSeconds and PreemptRunning are the
	// inference queue's settings: how long a pool is idle before it admits
	// maintenance calls, how long a waiting call waits for each priority
	// level it gains, and whether an interactive call cancels running
	// background calls.
	MaintenanceIdleSeconds int  `json:"maintenance_idle_seconds"`
	AgingSeconds           int  `json:"aging_seconds"`
	PreemptRunning         bool `json:"preempt_running"`
}

// handleSettings returns every setting.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.writeSettings(w, r, http.StatusOK)
}

// handlePutSettings writes the keys the body carries, leaving the others
// alone, and returns the whole table as it now stands. The keys the harness
// reads are checked first, and the keys are written in one
// transaction, so a bad value or a failed write leaves the table as it was.
// A write that carries the sandbox limits or egress then applies them to the
// home computer; when its container refuses, the answer is an error and the
// values stay saved.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[map[string]json.RawMessage](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for key, value := range req {
		if key == ntfySetting {
			var topic string
			if string(value) != "null" && (json.Unmarshal(value, &topic) != nil || !validTopic(topic) || s.cfg.PublicURL == "") {
				s.fail(w, r, invalidf("ntfy topic requires a valid URL and public_url"))
				return
			}
			sealed, err := s.deps.Secrets.Seal(topic)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			req[key], err = json.Marshal(sealed)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			continue
		}
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
	if touchesInference(req) {
		s.syncInference(r.Context())
	}
	// The sandbox settings are the home computer's limits and egress. The
	// table is written first, so a container that refuses the change leaves
	// the new values saved, and the next start applies them.
	if touchesSandbox(req) {
		if err := s.applyHomeSandbox(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.writeSettings(w, r, http.StatusOK)
}

// touchesSandbox reports whether a settings write carries a key that is the
// home computer's limits or egress.
func touchesSandbox(req map[string]json.RawMessage) bool {
	_, limits := req[settingSandboxLimits]
	_, egress := req[settingSandboxEgress]
	return limits || egress
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
		if row.Key == canarySetting {
			continue
		}
		if row.Key == ntfySetting {
			var sealed []byte
			_ = json.Unmarshal(row.Value, &sealed)
			out[row.Key] = []byte(strconv.FormatBool(len(sealed) > 0))
		} else {
			out[row.Key] = row.Value
		}
	}
	sandbox := builtinSandboxDefaults()
	defaults := settingsDefaults{
		SandboxImage:          s.cfg.SandboxImage,
		ScreenThreshold:       s.cfg.ScreenThreshold,
		MemoryScreenThreshold: s.cfg.MemoryScreenThreshold,
		SandboxLimits:         sandbox.Limits,
		SandboxEgress:         sandbox.Egress,
		SearchOrder:           []string{},
		SearchLimits:          map[string]search.Limit{},
		Compaction:            compactionDefaults(),
		// The idle timeout is the harness's own, not the deployment's.
		ComputerIdleMinutes:    defaultIdleMinutes,
		CompactIdleFill:        defaultCompactIdleFill,
		CompactIdleMinutes:     defaultCompactIdleMinutes,
		SubagentMaxRunning:     subagent.DefaultMaxRunning,
		WakeupsMax:             store.DefaultWakeupsMax,
		MaintenanceIdleSeconds: int(inference.DefaultMaintenanceIdle / time.Second),
		AgingSeconds:           int(inference.DefaultAging / time.Second),
	}
	if s.deps.Search != nil {
		defaults.SearchOrder, defaults.SearchLimits = s.deps.Search.Web(), s.deps.Search.DefaultLimits()
	}
	classes, err := s.classBodies(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	titles, _, err := s.titleModel(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, status, settingsResponse{
		Settings: out, Defaults: defaults, Classes: classes, TitlesModel: titles.Name,
	})
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
	case "wakeups_max":
		var n int
		if json.Unmarshal(value, &n) != nil || n < 1 || n > 1000 {
			return invalidf("wakeups_max must be a whole number from 1 to 1000")
		}
	case "time_zone", "locale":
		var text string
		if json.Unmarshal(value, &text) != nil {
			return invalidf("%s must be a string", key)
		}
		if key == "time_zone" {
			if _, err := time.LoadLocation(text); err != nil || text == "" || text == "Local" {
				return invalidf("time_zone must be an IANA time zone")
			}
		} else if len(text) == 0 || len(text) > 64 || strings.IndexFunc(text, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
		}) >= 0 {
			return invalidf("locale must be 1 to 64 letters, digits or hyphens")
		}
	case settingScreenRequired:
		var on bool
		if json.Unmarshal(value, &on) != nil {
			return invalidf("%s must be true or false", key)
		}
	case settingScreenThreshold, settingMemoryScreenThreshold, settingWatchThreshold:
		var n float64
		if json.Unmarshal(value, &n) != nil || n <= 0 || n > 1 {
			return invalidf("%s must be above 0 and at most 1", key)
		}
	case settingSinkLimits:
		if _, err := sentry.ParseSinkLimits(value); err != nil {
			return invalidf("%s: %v", key, err)
		}
	case settingTripwires:
		if _, err := trip.Parse(value); err != nil {
			return invalidf("%s: %v", key, err)
		}
	case settingSensitiveList:
		var entries []string
		if json.Unmarshal(value, &entries) != nil {
			return invalidf("%s must be a list of strings", key)
		}
		if err := sentry.ValidateSensitiveList(entries); err != nil {
			return invalidf("%s: %v", key, err)
		}
	case settingDefaultModel:
		var name string
		if err := json.Unmarshal(value, &name); err != nil {
			return invalidf("%s must be a model name", key)
		}
		return s.checkChatModelName(ctx, key, name)
	case settingModelClasses:
		return s.validateModelClasses(ctx, value)
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
	case settingIdleMinutes:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 0 || n > maxIdleMinutes {
			return invalidf("%s must be a whole number of minutes from 0 to %d", key, maxIdleMinutes)
		}
	case settingCompactIdleFill:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 1 || n > 100 {
			return invalidf("%s must be a whole percentage from 1 to 100", key)
		}
	case settingCompactIdleMinutes:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 0 || n > maxIdleMinutes {
			return invalidf("%s must be a whole number of minutes from 0 to %d", key, maxIdleMinutes)
		}
	case settingSubagentMaxRunning:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 1 || n > maxSubagentMaxRunning {
			return invalidf("%s must be a whole number of subagents from 1 to %d", key, maxSubagentMaxRunning)
		}
	case settingMaintenanceIdle:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 0 || n > maxMaintenanceIdleSeconds {
			return invalidf("%s must be a whole number of seconds from 0 to %d", key, maxMaintenanceIdleSeconds)
		}
	case settingAging:
		var n int
		if err := json.Unmarshal(value, &n); err != nil || n < 1 || n > maxAgingSeconds {
			return invalidf("%s must be a whole number of seconds from 1 to %d", key, maxAgingSeconds)
		}
	case settingPreemptRunning:
		var on bool
		if err := json.Unmarshal(value, &on); err != nil {
			return invalidf("%s must be true or false", key)
		}
	case settingSearchOrder, settingSearchLimits:
		if s.deps.Search == nil {
			return nil
		}
		if key == settingSearchOrder {
			return s.validateSearchOrder(value)
		}
		return s.validateSearchLimits(value)
	case settingCompaction:
		return validateCompaction(value)
	case settingSetupComplete:
		var done bool
		if err := json.Unmarshal(value, &done); err != nil {
			return invalidf("%s must be true or false", key)
		}
	}
	return nil
}

// modelNamed returns the configured model called name, and an invalid-request
// error naming key when there is none.
func (s *Server) modelNamed(ctx context.Context, key, name string) (store.Model, error) {
	m, err := s.deps.Store.ModelByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Model{}, invalidf("%s names %q, which is not a configured model", key, name)
	}
	return m, err
}

// checkChatModelName accepts the name of a configured chat model, or the
// empty name for none. A decision model answers typed questions and cannot
// hold a conversation, so it is refused wherever a conversation is held.
func (s *Server) checkChatModelName(ctx context.Context, key, name string) error {
	if name == "" {
		return nil
	}
	m, err := s.modelNamed(ctx, key, name)
	if err != nil {
		return err
	}
	if m.Kind != store.ModelKindChat {
		return invalidf("%s names %q, a %s model, which cannot hold a conversation", key, name, m.Kind)
	}
	return nil
}

// strictJSON decodes a setting's value, refusing a field v does not have.
func strictJSON(value json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
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

// renameModelSettings makes the settings that name a model by oldName name
// it by newName, so that renaming a model keeps it the default and keeps the
// classes assigned to it.
func (s *Server) renameModelSettings(ctx context.Context, oldName, newName string) error {
	writes := map[string]json.RawMessage{}
	if s.defaultModelSetting(ctx) == oldName {
		writes[settingDefaultModel] = jsonString(newName)
	}
	var assigned map[modelClass]string
	if readSetting(ctx, s.deps.Store, s.log, settingModelClasses, &assigned) {
		renamed := false
		for c, name := range assigned {
			if name == oldName {
				assigned[c], renamed = newName, true
			}
		}
		if renamed {
			value, err := json.Marshal(assigned)
			if err != nil {
				return fmt.Errorf("encode %s: %w", settingModelClasses, err)
			}
			writes[settingModelClasses] = value
		}
	}
	if len(writes) == 0 {
		return nil
	}
	return s.deps.Store.SetSettings(ctx, writes)
}

// sandboxImage returns the image the home computer's container is created
// from: the settings' choice, or the deployment's default.
func (s *Server) sandboxImage(ctx context.Context) string {
	var image string
	if readSetting(ctx, s.deps.Store, s.log, settingSandboxImage, &image) && image != "" {
		return image
	}
	return s.cfg.SandboxImage
}

// idleTimeout returns how long a running computer may go without a run
// before the harness stops it, and 0 when the settings say never.
func (s *Server) idleTimeout(ctx context.Context) time.Duration {
	minutes := defaultIdleMinutes
	var n int
	if readSetting(ctx, s.deps.Store, s.log, settingIdleMinutes, &n) && n >= 0 {
		minutes = min(n, maxIdleMinutes)
	}
	return time.Duration(minutes) * time.Minute
}

func (s *Server) idleCompactionSettings(ctx context.Context) (int, time.Duration) {
	fill, minutes := defaultCompactIdleFill, defaultCompactIdleMinutes
	var n int
	if readSetting(ctx, s.deps.Store, s.log, settingCompactIdleFill, &n) && n >= 1 && n <= 100 {
		fill = n
	}
	if readSetting(ctx, s.deps.Store, s.log, settingCompactIdleMinutes, &n) && n >= 0 && n <= maxIdleMinutes {
		minutes = n
	}
	return fill, time.Duration(minutes) * time.Minute
}

// jsonString encodes a string as a JSON value.
func jsonString(v string) []byte {
	return fmt.Appendf(nil, "%q", v)
}

func (s *Server) screenSettings(ctx context.Context) (float64, bool, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return 0, false, err
	}
	return parseScreenSettings(rows, s.cfg.ScreenThreshold)
}

// memoryScreenThreshold is the threshold for a memory write: the stored
// memory_screen_threshold, else the deployment's.
func (s *Server) memoryScreenThreshold(ctx context.Context) (float64, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return 0, err
	}
	threshold := s.cfg.MemoryScreenThreshold
	for _, row := range rows {
		if row.Key == settingMemoryScreenThreshold && string(row.Value) != "null" {
			if err := json.Unmarshal(row.Value, &threshold); err != nil {
				return 0, err
			}
		}
	}
	return threshold, nil
}

// watchThreshold is the stored watch_threshold, else the default.
func (s *Server) watchThreshold(ctx context.Context) (float64, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return 0, err
	}
	threshold := sentry.DefaultWatchThreshold
	for _, row := range rows {
		if row.Key == settingWatchThreshold && string(row.Value) != "null" {
			if err := json.Unmarshal(row.Value, &threshold); err != nil {
				return 0, err
			}
		}
	}
	return threshold, nil
}

// sensitiveList is the stored sensitive_list, else the starting value.
func (s *Server) sensitiveList(ctx context.Context) ([]string, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Key == settingSensitiveList && string(row.Value) != "null" {
			var entries []string
			if err := json.Unmarshal(row.Value, &entries); err != nil {
				return nil, err
			}
			return entries, nil
		}
	}
	return sentry.DefaultSensitiveList, nil
}

// sinkLimits is the stored sink_limits, else the starting values.
func (s *Server) sinkLimits(ctx context.Context) (sentry.SinkLimits, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return sentry.SinkLimits{}, err
	}
	for _, row := range rows {
		if row.Key == settingSinkLimits && string(row.Value) != "null" {
			return sentry.ParseSinkLimits(row.Value)
		}
	}
	return sentry.DefaultSinkLimits, nil
}

// tripwires is the stored tripwires setting, else every tripwire off.
func (s *Server) tripwires(ctx context.Context) (trip.Settings, error) {
	rows, err := s.deps.Store.Settings(ctx)
	if err != nil {
		return trip.Settings{}, err
	}
	for _, row := range rows {
		if row.Key == settingTripwires && string(row.Value) != "null" {
			return trip.Parse(row.Value)
		}
	}
	return trip.Settings{BlockedLimit: trip.DefaultBlockedLimit}, nil
}

func parseScreenSettings(rows []store.Setting, defaultThreshold float64) (float64, bool, error) {
	threshold, required := defaultThreshold, false
	for _, row := range rows {
		var target any
		switch row.Key {
		case settingScreenThreshold:
			target = &threshold
		case settingScreenRequired:
			target = &required
		default:
			continue
		}
		if string(row.Value) != "null" {
			if err := json.Unmarshal(row.Value, target); err != nil {
				return 0, false, err
			}
		}
	}
	return threshold, required, nil
}
