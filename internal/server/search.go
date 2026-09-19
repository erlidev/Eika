package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/search"
	"github.com/erlidev/eika/internal/secret"
	"github.com/erlidev/eika/internal/store"
)

// The settings keys search reads.
const (
	// settingSearchOrder lists the web providers, most preferred first.
	settingSearchOrder = "search_order"
	// settingSearchLimits overrides quota buckets' limits by bucket name.
	settingSearchLimits = "search_limits"
)

// maxSearchLimit bounds one quota. A bigger number is a typo, not a plan.
const maxSearchLimit = 10_000_000

// searchBackend is what the search engine reads through: the settings, the
// sealed keys, and the usage table. It is built in wiring, before the
// server, because the tools every run shares hold the engine.
type searchBackend struct {
	store   *store.Store
	secrets *secret.Box
	log     *slog.Logger
}

// SearchOptions reads the provider order and the quota overrides. A missing
// or malformed setting means the defaults.
func (b searchBackend) SearchOptions(ctx context.Context) (search.Options, error) {
	var opts search.Options
	readSetting(ctx, b.store, b.log, settingSearchOrder, &opts.Order)
	readSetting(ctx, b.store, b.log, settingSearchLimits, &opts.Limits)
	return opts, nil
}

// SearchKey opens the key stored under name, empty when there is none. A
// key sealed under a key file the harness no longer has reads as none, which
// the status report shows as unset so the user enters it again.
func (b searchBackend) SearchKey(ctx context.Context, name string) (string, error) {
	row, err := b.store.SearchKey(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	key, err := b.secrets.Open(row.Key)
	if errors.Is(err, secret.ErrCorrupt) {
		b.log.Error("open search key", "name", name, "error", err)
		return "", nil
	}
	return key, err
}

// LoadSearchUsage reads every bucket's counters.
func (b searchBackend) LoadSearchUsage(ctx context.Context) (map[string]search.Usage, error) {
	rows, err := b.store.SearchUsage(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]search.Usage, len(rows))
	for _, r := range rows {
		out[r.Name] = search.Usage{
			Day: r.Day, DayUsed: r.DayUsed, Month: r.Month, MonthUsed: r.MonthUsed,
			CooldownUntil: r.CooldownUntil, FailStreak: r.FailStreak,
		}
	}
	return out, nil
}

// SaveSearchUsage writes one bucket's counters.
func (b searchBackend) SaveSearchUsage(ctx context.Context, name string, u search.Usage) error {
	return b.store.SetSearchUsage(ctx, store.SearchUsage{
		Name: name, Day: u.Day, DayUsed: u.DayUsed, Month: u.Month, MonthUsed: u.MonthUsed,
		CooldownUntil: u.CooldownUntil, FailStreak: u.FailStreak,
	})
}

// searchStatusResponse is the body of GET /api/search/status.
type searchStatusResponse struct {
	search.Status
	Keys []searchKeyBody `json:"keys"`
	// CachedPages counts the pages web_fetch holds.
	CachedPages int `json:"cached_pages"`
	// SearxNGURL is where the deployment looks for SearXNG.
	SearxNGURL string `json:"searxng_url"`
}

// searchKeyBody is one search key on the wire. The key itself never leaves
// the harness.
type searchKeyBody struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
	// Hint is the last characters of a long key, so the user can tell which
	// key is stored.
	Hint      string     `json:"hint,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// searchKeyRequest is the body of PUT /api/search/keys/{name}. An empty key
// removes the stored one.
type searchKeyRequest struct {
	Key string `json:"key"`
}

// searchOutcomeBody is the body of POST /api/search: what the model would
// read, and the details the interface renders.
type searchOutcomeBody struct {
	Text    string         `json:"text"`
	IsError bool           `json:"is_error"`
	Details search.Details `json:"details"`
}

// handleSearchStatus reports every search backend's health, the stored keys,
// and the caches.
func (s *Server) handleSearchStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.deps.Search.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	keys, err := s.searchKeys(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body := searchStatusResponse{Status: st, Keys: keys, SearxNGURL: s.cfg.SearxNGURL}
	if s.deps.Pages != nil {
		body.CachedPages = s.deps.Pages.Cached()
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// searchKeys lists every key the backends use, stored or not.
func (s *Server) searchKeys(ctx context.Context) ([]searchKeyBody, error) {
	rows, err := s.deps.Store.SearchKeys(ctx)
	if err != nil {
		return nil, err
	}
	names := s.deps.Search.KeyNames()
	out := make([]searchKeyBody, 0, len(names))
	for _, name := range names {
		body := searchKeyBody{Name: name}
		if i := slices.IndexFunc(rows, func(k store.SearchKey) bool { return k.Name == name }); i >= 0 {
			body.Set, body.UpdatedAt = true, &rows[i].UpdatedAt
			if key, err := s.deps.Secrets.Open(rows[i].Key); err == nil && len(key) >= minHintedKey {
				body.Hint = key[len(key)-keyHintLength:]
			}
		}
		out = append(out, body)
	}
	return out, nil
}

// handlePutSearchKey seals and stores a search key, or removes it when the
// body's key is empty, and returns the keys as they now stand.
func (s *Server) handlePutSearchKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !slices.Contains(s.deps.Search.KeyNames(), name) {
		s.fail(w, r, notFoundf("no search backend uses a key called %q", name))
		return
	}
	req, err := decodeJSON[searchKeyRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	key := strings.TrimSpace(req.Key)
	switch {
	case key == "":
		err = s.deps.Store.DeleteSearchKey(r.Context(), name)
	case len(key) > maxAPIKey:
		err = invalidf("key must be at most %d bytes", maxAPIKey)
	default:
		var sealed []byte
		if sealed, err = s.deps.Secrets.Seal(key); err == nil {
			err = s.deps.Store.SetSearchKey(r.Context(), name, sealed)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("search key written", "name", name, "set", key != "")
	keys, err := s.searchKeys(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, map[string][]searchKeyBody{"keys": keys})
}

// handleSearch runs one search, as the settings page's try-it box does. It
// spends quota like any other search.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[search.Request](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.deps.Search.Search(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, searchOutcomeBody{Text: out.Text, IsError: out.IsError, Details: out.Details})
}

// validateSearchOrder accepts a list of known web providers, each once.
func (s *Server) validateSearchOrder(value json.RawMessage) error {
	var order []string
	if err := json.Unmarshal(value, &order); err != nil {
		return invalidf("%s must be a list of provider names", settingSearchOrder)
	}
	web := s.deps.Search.Web()
	for i, name := range order {
		if !slices.Contains(web, name) {
			return invalidf("%s names %q, which is not one of %s", settingSearchOrder, name, strings.Join(web, ", "))
		}
		if slices.Contains(order[:i], name) {
			return invalidf("%s names %q twice", settingSearchOrder, name)
		}
	}
	return nil
}

// validateSearchLimits accepts quotas for known buckets.
func (s *Server) validateSearchLimits(value json.RawMessage) error {
	var limits map[string]search.Limit
	if err := json.Unmarshal(value, &limits); err != nil {
		return invalidf(`%s must map bucket names to {"day": n, "month": n}, where n is a whole number`, settingSearchLimits)
	}
	buckets := s.deps.Search.DefaultLimits()
	for name, l := range limits {
		if _, ok := buckets[name]; !ok {
			return invalidf("%s names %q, which is not a search quota bucket", settingSearchLimits, name)
		}
		if l.Day < 0 || l.Month < 0 || l.Day > maxSearchLimit || l.Month > maxSearchLimit {
			return invalidf("the %s quota must be a whole number from 1 to %d, or 0 or left out for unlimited", name, maxSearchLimit)
		}
	}
	return nil
}
