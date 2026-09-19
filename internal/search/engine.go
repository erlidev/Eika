package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Bounds on a search. They are constants rather than settings: each is a
// token budget or a courtesy to the services, not a preference.
const (
	// DefaultCount is how many results a search returns unless asked.
	DefaultCount = 10
	// MaxCount is the most results one search returns.
	MaxCount = 25
	// MaxQueryRunes bounds a query. No backend ranks a longer one usefully.
	MaxQueryRunes = 500
	// descriptionTokens is each result's description budget.
	descriptionTokens = 100
	// poolSize is how many candidates a web search asks its provider for and
	// caches. The model sees the first count of them; the rest make a later,
	// larger count for the same query a cache hit.
	poolSize = 30
	// cacheTTL is how long a search's results are served from the cache.
	cacheTTL = 24 * time.Hour
	// cacheSize bounds the searches the cache holds.
	cacheSize = 512
)

// SourceWeb is the source that searches the web through the provider chain.
// Every other source is one backend by name.
const SourceWeb = "web"

// Backend registers one search backend with the Engine: a web provider in
// the failover chain, or a source the model names directly.
type Backend struct {
	// Name identifies the backend in the settings, the API, and, for a
	// source, the tool's source argument. It never changes once shipped.
	Name     string
	Searcher Searcher
	// Web puts the backend in the web provider chain.
	Web bool
	// Key names the API key the backend is sent, empty for none. Backends
	// can share a key, as the GitHub sources do.
	Key string
	// KeyRequired skips the backend, or refuses the search, without its key.
	KeyRequired bool
	// Bucket is the quota and cooldown bucket the backend counts against. A
	// web provider always has one, defaulting to its name. A source without
	// one is not tracked, and one with a bucket is cooled down only by a rate
	// limit: its answers are the model's query, not an outage.
	Bucket string
	// Limit is the bucket's default quota.
	Limit Limit
	// Pool makes a source ask for this many results and cache them, so a
	// repeat is free. Zero asks for what the search wants and caches nothing.
	Pool int
	// Interval spaces a source's requests: one at a time, their starts at
	// least this far apart, as arXiv's terms ask.
	Interval time.Duration
}

// Options are the search settings in force.
type Options struct {
	// Order lists the web providers, most preferred first. Exactly one is
	// queried per search: the first that has its key, quota, and no
	// cooldown. Nil (the setting unset) means every web provider in
	// registration order; an empty list turns web search off.
	Order []string `json:"order"`
	// Limits overrides bucket quotas by bucket name.
	Limits map[string]Limit `json:"limits"`
}

// Settings reads the search settings. The server implements it over the
// settings table.
type Settings interface {
	SearchOptions(ctx context.Context) (Options, error)
}

// Keys reads API keys. The server implements it over the sealed key table.
type Keys interface {
	// SearchKey returns the key stored under name, empty when there is none.
	SearchKey(ctx context.Context, name string) (string, error)
}

// Config is what an Engine is built from.
type Config struct {
	Backends []Backend
	Tracker  *Tracker
	// Settings and Keys may be nil: the defaults, and no keys.
	Settings Settings
	Keys     Keys
	// SearxNGURL is where SearXNG is looked for, which the notices name.
	SearxNGURL string
	// Now is the clock, time.Now when nil.
	Now func() time.Time
	Log *slog.Logger
}

// Engine runs searches: the web through its provider chain, every other
// source directly, with quotas, cooldowns, pacing, and a result cache.
type Engine struct {
	cfg      Config
	backends map[string]*backend
	cache    *Cache[cached]
}

// backend is a registered Backend with its pacing state.
type backend struct {
	Backend
	// turn holds a token while a paced request runs.
	turn chan struct{}
	// next is when the next paced request may start. It is guarded by turn.
	next time.Time
}

// cached is a search's result pool and the provider that answered it.
type cached struct {
	provider string
	results  []Result
}

// NewEngine returns an Engine over cfg's backends. Backend names must be
// unique and must not be "web".
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Tracker == nil {
		cfg.Tracker, _ = NewTracker(context.Background(), nil, cfg.Log)
	}
	cfg.Backends = slices.Clone(cfg.Backends)
	e := &Engine{cfg: cfg, backends: make(map[string]*backend), cache: NewCache[cached](cacheSize, cacheTTL)}
	for i, b := range cfg.Backends {
		switch {
		case b.Name == "" || b.Name == SourceWeb:
			return nil, fmt.Errorf("search backend %d: invalid name %q", i, b.Name)
		case e.backends[b.Name] != nil:
			return nil, fmt.Errorf("search backend %q registered twice", b.Name)
		case b.Searcher == nil:
			return nil, fmt.Errorf("search backend %q has no searcher", b.Name)
		}
		if b.Web && b.Bucket == "" {
			b.Bucket = b.Name
		}
		cfg.Backends[i] = b
		e.backends[b.Name] = &backend{Backend: b, turn: make(chan struct{}, 1)}
	}
	return e, nil
}

// Web returns the web providers' names in registration order.
func (e *Engine) Web() []string {
	var names []string
	for _, b := range e.cfg.Backends {
		if b.Web {
			names = append(names, b.Name)
		}
	}
	return names
}

// Sources returns what a search may name as its source: "web" and every
// backend that is not a web provider.
func (e *Engine) Sources() []string {
	names := []string{SourceWeb}
	for _, b := range e.cfg.Backends {
		if !b.Web {
			names = append(names, b.Name)
		}
	}
	return names
}

// DefaultLimits returns every bucket's default quota, keyed by bucket.
func (e *Engine) DefaultLimits() map[string]Limit {
	limits := make(map[string]Limit)
	for _, b := range e.cfg.Backends {
		if b.Bucket != "" {
			limits[b.Bucket] = b.Limit
		}
	}
	return limits
}

// KeyNames returns the names of the API keys the backends use, each once.
func (e *Engine) KeyNames() []string {
	var names []string
	for _, b := range e.cfg.Backends {
		if b.Key != "" && !slices.Contains(names, b.Key) {
			names = append(names, b.Key)
		}
	}
	return names
}

// Request is one search.
type Request struct {
	Query string `json:"query"`
	// Source is "web" or a source backend's name; empty means "web".
	Source string `json:"source,omitempty"`
	// Count is how many results to return, clamped to 1..MaxCount; zero
	// means DefaultCount.
	Count int `json:"count,omitempty"`
}

// Outcome is a finished search. Text is what the model reads; IsError marks
// a search the model should see as failed. Details are for the interface.
type Outcome struct {
	Text    string
	IsError bool
	Details Details
}

// Details describe how a search went. They never reach the model, which is
// what makes the diagnostics in them free.
type Details struct {
	Source string `json:"source"`
	Query  string `json:"query"`
	Count  int    `json:"count"`
	// Providers names the web provider that answered.
	Providers []string `json:"providers,omitempty"`
	// Attempts lists the web providers skipped or failed on the way.
	Attempts []Attempt `json:"attempts,omitempty"`
	Cached   bool      `json:"cached,omitempty"`
	// Pool is how many results the answering provider returned.
	Pool    int      `json:"pool,omitempty"`
	Results []Result `json:"results,omitempty"`
	Ms      int64    `json:"ms"`
}

// Attempt is a web provider the chain did not get results from, and why.
type Attempt struct {
	Provider string `json:"provider"`
	Error    string `json:"error"`
}

// Search runs one search. A failure the model can act on is an Outcome with
// IsError set; the error is only for a cancelled search or a harness fault,
// such as unreadable settings.
func (e *Engine) Search(ctx context.Context, req Request) (Outcome, error) {
	started := e.cfg.Now()
	source := cmp.Or(strings.TrimSpace(req.Source), SourceWeb)
	query := strings.TrimSpace(req.Query)
	count := req.Count
	if count == 0 {
		count = DefaultCount
	}
	count = max(1, min(count, MaxCount))
	d := Details{Source: source, Query: query, Count: count}
	fail := func(format string, args ...any) (Outcome, error) {
		return Outcome{Text: source + " search failed: " + fmt.Sprintf(format, args...), IsError: true, Details: d}, nil
	}

	switch {
	case query == "":
		return fail("query is empty.")
	case utf8.RuneCountInString(query) > MaxQueryRunes:
		return fail("query is longer than %d characters.", MaxQueryRunes)
	case source != SourceWeb && (e.backends[source] == nil || e.backends[source].Web):
		return fail("unknown source; use one of %s.", strings.Join(e.Sources(), ", "))
	}

	opts, err := e.options(ctx)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	if source == SourceWeb {
		out, err = e.searchWeb(ctx, query, count, opts, d)
	} else {
		out, err = e.searchSource(ctx, e.backends[source], query, count, opts, d)
	}
	if err != nil {
		return Outcome{}, err
	}
	out.Details.Ms = e.cfg.Now().Sub(started).Milliseconds()
	return out, nil
}

// options reads the settings in force, with the defaults filled in.
func (e *Engine) options(ctx context.Context) (Options, error) {
	var opts Options
	if e.cfg.Settings != nil {
		var err error
		if opts, err = e.cfg.Settings.SearchOptions(ctx); err != nil {
			return Options{}, fmt.Errorf("read search settings: %w", err)
		}
	}
	if opts.Order == nil {
		opts.Order = e.Web()
	}
	limits := e.DefaultLimits()
	for name, l := range opts.Limits {
		limits[name] = l
	}
	opts.Limits = limits
	return opts, nil
}

// Limit returns a bucket's quota under the settings in force. Fetch reads it
// for the GitHub bucket its reads share with the GitHub sources.
func (e *Engine) Limit(ctx context.Context, bucket string) (Limit, error) {
	opts, err := e.options(ctx)
	if err != nil {
		return Limit{}, err
	}
	return opts.Limits[bucket], nil
}

// key reads a backend's API key, empty when it uses none or none is stored.
func (e *Engine) key(ctx context.Context, b *backend) (string, error) {
	if b.Key == "" || e.cfg.Keys == nil {
		return "", nil
	}
	k, err := e.cfg.Keys.SearchKey(ctx, b.Key)
	if err != nil {
		return "", fmt.Errorf("read the %s search key: %w", b.Key, err)
	}
	return k, nil
}

// searchWeb queries one web provider: the first in order that is usable.
//
// It is deliberately not a fan-out. Every extra provider costs quota and
// buys little, because the pool is already larger than the model will see.
// The chain moves on only when a provider cannot answer at all: no key,
// spent quota, a cooldown, a transport failure, a rejection, or no results.
// Every skip and failure lands in the attempts, so the model gets an error it
// can act on.
func (e *Engine) searchWeb(ctx context.Context, query string, count int, opts Options, d Details) (Outcome, error) {
	if len(opts.Order) == 0 {
		return Outcome{Text: "web search is turned off: no web provider is selected under Settings, Search. " +
			"The other sources still work.", IsError: true, Details: d}, nil
	}
	cacheKey := SourceWeb + "|" + query
	if hit, ok := e.cache.Get(cacheKey, e.cfg.Now()); ok {
		d.Providers, d.Cached, d.Pool = []string{hit.provider}, true, len(hit.results)
		return e.webOutcome(hit.results, count, opts, d), nil
	}

	var found []Result
	for _, name := range opts.Order {
		b := e.backends[name]
		if b == nil || !b.Web {
			d.Attempts = append(d.Attempts, Attempt{name, "unknown provider"})
			continue
		}
		key, err := e.key(ctx, b)
		if err != nil {
			return Outcome{}, err
		}
		if b.KeyRequired && key == "" {
			d.Attempts = append(d.Attempts, Attempt{name, "no API key"})
			continue
		}
		if blocked := e.cfg.Tracker.Blocked(b.Bucket, opts.Limits[b.Bucket], e.cfg.Now()); blocked != "" {
			d.Attempts = append(d.Attempts, Attempt{name, blocked})
			continue
		}

		results, err := b.Searcher.Search(ctx, Query{Text: query, Limit: poolSize, Key: key})
		if err != nil {
			if ctx.Err() != nil {
				return Outcome{}, ctx.Err()
			}
			now := e.cfg.Now()
			e.cfg.Tracker.RecordFailure(ctx, b.Bucket, now, RetryDeadline(err, now))
			d.Attempts = append(d.Attempts, Attempt{name, failureReason(err)})
			continue
		}
		e.cfg.Tracker.RecordUse(ctx, b.Bucket, e.cfg.Now())
		if found = Dedupe(results); len(found) == 0 {
			d.Attempts = append(d.Attempts, Attempt{name, "no results"})
			continue
		}
		d.Providers = []string{name}
		break
	}

	if len(found) == 0 {
		return Outcome{Text: e.noProvider(d.Attempts), IsError: true, Details: d}, nil
	}
	e.cache.Put(cacheKey, cached{provider: d.Providers[0], results: found}, e.cfg.Now())
	d.Pool = len(found)
	return e.webOutcome(found, count, opts, d), nil
}

// failureReason says in a few words why a provider failed.
func failureReason(err error) string {
	var h *HTTPError
	switch {
	case errors.As(err, &h) && h.Status == http.StatusTooManyRequests:
		return "rate limited"
	case errors.As(err, &h) && h.Status > 0:
		return fmt.Sprintf("HTTP %d", h.Status)
	}
	return Describe(err)
}

// webOutcome renders the first count results of a web search, with any
// notice about how the chain degraded.
func (e *Engine) webOutcome(pool []Result, count int, opts Options, d Details) Outcome {
	results := pool[:min(count, len(pool))]
	d.Results = results
	text := FormatResults(results, descriptionTokens)
	if notices := e.notices(d.Providers[0], d.Attempts, opts.Order); len(notices) > 0 {
		lines := make([]string, len(notices))
		for i, n := range notices {
			lines[i] = "Notice: " + n
		}
		text = strings.Join(lines, "\n") + "\n\n" + text
	}
	return Outcome{Text: text, Details: d}
}

// recovery is the advice every degraded-search message ends with. It names
// both routes back: the self-hosted SearXNG and the keyed providers.
func (e *Engine) recovery() string {
	return fmt.Sprintf("Set EIKA_SEARXNG_URL to a reachable SearXNG JSON API (currently %s), "+
		"or add an Exa, Tavily, or Brave API key under Settings, Search.", e.cfg.SearxNGURL)
}

// noProvider is the error for a web search no provider answered.
func (e *Engine) noProvider(attempts []Attempt) string {
	parts := make([]string, len(attempts))
	for i, a := range attempts {
		parts[i] = a.Provider + ": " + a.Error
	}
	detail := cmp.Or(strings.Join(parts, "; "), "no providers configured")
	return fmt.Sprintf("web search failed: no provider returned results (%s). %s", detail, e.recovery())
}

// notices are the degradation the model should hear about, because the
// details it would otherwise be in never reach it: SearXNG failing over to a
// fallback, now or when the cached answer was fetched.
func (e *Engine) notices(provider string, attempts []Attempt, order []string) []string {
	if provider == "searxng" {
		return nil
	}
	for _, a := range attempts {
		if a.Provider == "searxng" {
			return []string{fmt.Sprintf("SearXNG unavailable (%s); used %s fallback. %s", a.Error, provider, e.recovery())}
		}
	}
	if s, p := slices.Index(order, "searxng"), slices.Index(order, provider); len(attempts) == 0 && s >= 0 && s < p {
		return []string{fmt.Sprintf("Using cached %s fallback results; SearXNG did not answer when this query was cached. %s", provider, e.recovery())}
	}
	return nil
}

// searchSource queries a source backend directly.
func (e *Engine) searchSource(ctx context.Context, b *backend, query string, count int, opts Options, d Details) (Outcome, error) {
	fail := func(format string, args ...any) (Outcome, error) {
		return Outcome{Text: b.Name + " search failed: " + fmt.Sprintf(format, args...), IsError: true, Details: d}, nil
	}
	answer := func(results []Result) (Outcome, error) {
		results = results[:min(count, len(results))]
		d.Results = results
		return Outcome{Text: FormatResults(results, descriptionTokens), Details: d}, nil
	}

	key, err := e.key(ctx, b)
	if err != nil {
		return Outcome{}, err
	}
	if b.KeyRequired && key == "" {
		return fail("%s needs a %s key; add one under Settings, Search.", b.Name, b.Key)
	}
	if b.Bucket != "" {
		if blocked := e.cfg.Tracker.Blocked(b.Bucket, opts.Limits[b.Bucket], e.cfg.Now()); blocked != "" {
			return fail("%s %s.", b.Bucket, blocked)
		}
	}

	cacheKey := b.Name + "|" + query
	if b.Pool > 0 {
		if hit, ok := e.cache.Get(cacheKey, e.cfg.Now()); ok {
			d.Cached = true
			return answer(hit.results)
		}
	}
	if b.Interval > 0 {
		if err := e.pace(ctx, b); err != nil {
			return Outcome{}, err
		}
		defer func() { <-b.turn }()
		// A search queued behind the one that filled the cache needs no
		// request of its own.
		if hit, ok := e.cache.Get(cacheKey, e.cfg.Now()); ok && b.Pool > 0 {
			d.Cached = true
			return answer(hit.results)
		}
	}

	limit := count
	if b.Pool > 0 {
		limit = b.Pool
	}
	results, err := b.Searcher.Search(ctx, Query{Text: query, Limit: limit, Key: key})
	if err != nil {
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}
		var h *HTTPError
		if b.Bucket != "" && errors.As(err, &h) && (h.Status == http.StatusForbidden || h.Status == http.StatusTooManyRequests) {
			now := e.cfg.Now()
			e.cfg.Tracker.RecordFailure(ctx, b.Bucket, now, RetryDeadline(err, now))
		}
		return fail("%s", Describe(err))
	}
	if b.Bucket != "" {
		e.cfg.Tracker.RecordUse(ctx, b.Bucket, e.cfg.Now())
	}
	if b.Pool > 0 {
		e.cache.Put(cacheKey, cached{provider: b.Name, results: results}, e.cfg.Now())
	}
	return answer(results)
}

// pace takes the backend's turn and waits out its interval. The caller
// releases the turn when its request is done.
func (e *Engine) pace(ctx context.Context, b *backend) error {
	select {
	case b.turn <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if wait := b.next.Sub(e.cfg.Now()); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			<-b.turn
			return ctx.Err()
		}
	}
	b.next = e.cfg.Now().Add(b.Interval)
	return nil
}

// Status is the health of every backend, for the settings page.
type Status struct {
	// Order is the web provider order in force.
	Order    []string        `json:"order"`
	Backends []BackendStatus `json:"backends"`
	// CachedSearches counts the searches the cache holds.
	CachedSearches int `json:"cached_searches"`
}

// BackendStatus is one backend's health.
type BackendStatus struct {
	Name        string `json:"name"`
	Web         bool   `json:"web"`
	Key         string `json:"key,omitempty"`
	KeyRequired bool   `json:"key_required,omitempty"`
	KeySet      bool   `json:"key_set"`
	Bucket      string `json:"bucket,omitempty"`
	// State is "ready", "no API key", or why the bucket is blocked.
	State string `json:"state"`
	Usage Usage  `json:"usage"`
	Limit Limit  `json:"limit"`
	// Probe says whether a self-hosted backend answers: "up", "HTTP 403",
	// "connection refused".
	Probe string `json:"probe,omitempty"`
}

// Status reports every backend's health.
func (e *Engine) Status(ctx context.Context) (Status, error) {
	opts, err := e.options(ctx)
	if err != nil {
		return Status{}, err
	}
	now := e.cfg.Now()
	st := Status{Order: opts.Order, CachedSearches: e.cache.Len()}
	for _, reg := range e.cfg.Backends {
		b := e.backends[reg.Name]
		key, err := e.key(ctx, b)
		if err != nil {
			return Status{}, err
		}
		bs := BackendStatus{
			Name: b.Name, Web: b.Web, Key: b.Key, KeyRequired: b.KeyRequired, KeySet: key != "",
			Bucket: b.Bucket, State: "ready", Limit: opts.Limits[b.Bucket],
		}
		if b.Bucket != "" {
			bs.Usage = e.cfg.Tracker.Usage(b.Bucket, now)
			if blocked := e.cfg.Tracker.Blocked(b.Bucket, bs.Limit, now); blocked != "" {
				bs.State = blocked
			}
		}
		if b.KeyRequired && key == "" {
			bs.State = "no API key"
		}
		if p, ok := b.Searcher.(Prober); ok {
			bs.Probe = p.Probe(ctx)
		}
		st.Backends = append(st.Backends, bs)
	}
	return st, nil
}
