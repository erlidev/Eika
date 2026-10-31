package store

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Provider is one model provider the user configured: an endpoint of one
// provider kind and the key it authenticates with.
type Provider struct {
	ID   string
	Name string
	// Kind names the provider implementation, such as "openai".
	Kind    string
	BaseURL string
	// APIKey is the key as the harness sealed it, nil when the provider has
	// none. The store never sees it in the clear.
	APIKey []byte
	// Backend is the kind of server behind the endpoint: llamacpp, vllm,
	// sglang, tabby, openai or other. A probe sets it, and the user may
	// override it.
	Backend string
	// Pool is the hardware group the endpoint belongs to. Empty means the
	// provider's own name; use PoolName.
	Pool string
	// Slots is the concurrent requests the endpoint takes.
	Slots int
	// KV is how the endpoint's KV cache is divided: shared, partitioned or
	// unknown. KVTokens is the size of a shared cache, zero when unknown.
	KV       string
	KVTokens int
	// Capabilities is the last successful probe's findings that are not
	// settings, as the probe package encodes them.
	Capabilities json.RawMessage
	// Overridden names the fields the user set (backend, slots, kv), which a
	// probe leaves alone.
	Overridden []string
	// ProbedAt is when a probe last succeeded, nil if none has. ProbeError is
	// why the latest probe failed, empty after a success.
	ProbedAt   *time.Time
	ProbeError string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PoolName is the provider's hardware group: the pool the user set, or the
// provider's own name.
func (p Provider) PoolName() string {
	if p.Pool != "" {
		return p.Pool
	}
	return p.Name
}

// providerColumns is the column list every provider query selects, in the
// order scanProvider reads them.
const providerColumns = `id, name, kind, base_url, api_key, backend, pool, slots, kv, kv_tokens,
	capabilities, overridden, probed_at, probe_error, created_at, updated_at`

// CreateProvider inserts p and returns it with the fields the database
// assigned. An empty ID gets a fresh one; a duplicate name is ErrConflict.
func (s *Store) CreateProvider(ctx context.Context, p Provider) (Provider, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	const q = `INSERT INTO providers (id, name, kind, base_url, api_key)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + providerColumns
	out, err := scanProvider(s.pool.QueryRow(ctx, q, p.ID, p.Name, p.Kind, p.BaseURL, p.APIKey))
	if err != nil {
		return Provider{}, wrap("create provider "+p.Name, err)
	}
	return out, nil
}

// Provider returns the provider with the given id.
func (s *Store) Provider(ctx context.Context, id string) (Provider, error) {
	p, err := scanProvider(s.pool.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id = $1`, id))
	if err != nil {
		return Provider{}, wrap("read provider "+id, err)
	}
	return p, nil
}

// Providers returns every provider, oldest first.
func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+providerColumns+` FROM providers ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list providers", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, wrap("list providers", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list providers", err)
	}
	return out, nil
}

// UpdateProvider writes p's name, base URL, and key over the stored row and
// returns the row as it now stands. A name another provider has is
// ErrConflict.
func (s *Store) UpdateProvider(ctx context.Context, p Provider) (Provider, error) {
	const q = `UPDATE providers SET name = $2, base_url = $3, api_key = $4, updated_at = now()
		WHERE id = $1
		RETURNING ` + providerColumns
	out, err := scanProvider(s.pool.QueryRow(ctx, q, p.ID, p.Name, p.BaseURL, p.APIKey))
	if err != nil {
		return Provider{}, wrap("update provider "+p.ID, err)
	}
	return out, nil
}

// DeleteProvider removes a provider and its models.
func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, id)
	if err != nil {
		return wrap("delete provider "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete provider "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanProvider reads one provider row.
func scanProvider(row pgx.Row) (Provider, error) {
	var p Provider
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Backend, &p.Pool, &p.Slots, &p.KV, &p.KVTokens,
		&p.Capabilities, &p.Overridden, &p.ProbedAt, &p.ProbeError, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return Provider{}, err
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	if p.ProbedAt != nil {
		at := p.ProbedAt.UTC()
		p.ProbedAt = &at
	}
	return p, nil
}

// Fields of a provider the user can set in place of what a probe finds, as
// Provider.Overridden names them.
const (
	OverrideBackend = "backend"
	OverrideSlots   = "slots"
	OverrideKV      = "kv"
	// OverridePool is accepted by ProviderOverride.Reset only: whether the
	// pool is set shows in Provider.Pool itself.
	OverridePool = "pool"
)

// ProbeResult is what a probe found, as RecordProbe stores it.
type ProbeResult struct {
	Backend      string
	Slots        int
	KV           string
	KVTokens     int
	Capabilities json.RawMessage
	At           time.Time
}

// RecordProbe stores a probe's findings on a provider and returns the row. A
// field the user overrode keeps its value; the check is made in the statement
// itself, so an override written while a probe runs is not lost. A success
// clears the last probe error.
func (s *Store) RecordProbe(ctx context.Context, id string, r ProbeResult) (Provider, error) {
	const q = `UPDATE providers SET
			backend   = CASE WHEN 'backend' = ANY(overridden) THEN backend ELSE $2 END,
			slots     = CASE WHEN 'slots' = ANY(overridden) THEN slots ELSE $3 END,
			kv        = CASE WHEN 'kv' = ANY(overridden) THEN kv ELSE $4 END,
			kv_tokens = CASE WHEN 'kv' = ANY(overridden) THEN kv_tokens ELSE $5 END,
			capabilities = $6, probed_at = $7, probe_error = ''
		WHERE id = $1
		RETURNING ` + providerColumns
	if r.Capabilities == nil {
		r.Capabilities = json.RawMessage(`{}`)
	}
	out, err := scanProvider(s.pool.QueryRow(ctx, q, id, r.Backend, r.Slots, r.KV, r.KVTokens, r.Capabilities, r.At))
	if err != nil {
		return Provider{}, wrap("record probe of provider "+id, err)
	}
	return out, nil
}

// RecordProbeFailure stores why the latest probe of a provider failed and
// leaves every earlier result as it was.
func (s *Store) RecordProbeFailure(ctx context.Context, id, message string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE providers SET probe_error = $2 WHERE id = $1`, id, message)
	if err != nil {
		return wrap("record probe failure of provider "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("record probe failure of provider "+id, pgx.ErrNoRows)
	}
	return nil
}

// ProviderOverride is what the user sets on a provider in place of a probe's
// findings. A nil field is left alone.
type ProviderOverride struct {
	Backend *string
	// Pool is the hardware group; the empty string means the provider's name.
	Pool  *string
	Slots *int
	// KV and KVTokens are set together: a value for either overrides the
	// cache, and KVTokens is kept only for a shared one.
	KV       *string
	KVTokens *int
	// Reset hands the named fields (the Override constants) back to the
	// probe. Their values stay until the next probe writes them.
	Reset []string
}

// OverrideProvider records the user's settings for a provider's probed fields
// and returns the row. A field set here is named in Overridden and is not
// written by RecordProbe until it is reset.
func (s *Store) OverrideProvider(ctx context.Context, id string, o ProviderOverride) (Provider, error) {
	var out Provider
	err := s.tx(ctx, func(q querier) error {
		p, err := scanProvider(q.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return wrap("read provider "+id, err)
		}
		applyOverride(&p, o)
		if p.Overridden == nil {
			p.Overridden = []string{}
		}
		const update = `UPDATE providers SET backend = $2, pool = $3, slots = $4, kv = $5, kv_tokens = $6,
			overridden = $7, updated_at = now()
			WHERE id = $1
			RETURNING ` + providerColumns
		out, err = scanProvider(q.QueryRow(ctx, update, id, p.Backend, p.Pool, p.Slots, p.KV, p.KVTokens, p.Overridden))
		if err != nil {
			return wrap("override provider "+id, err)
		}
		return nil
	})
	return out, err
}

// applyOverride changes p as o says.
func applyOverride(p *Provider, o ProviderOverride) {
	mark := func(field string) {
		if !slices.Contains(p.Overridden, field) {
			p.Overridden = append(p.Overridden, field)
		}
	}
	if o.Backend != nil {
		p.Backend = *o.Backend
		mark(OverrideBackend)
	}
	if o.Pool != nil {
		p.Pool = *o.Pool
	}
	if o.Slots != nil {
		p.Slots = *o.Slots
		mark(OverrideSlots)
	}
	if o.KV != nil || o.KVTokens != nil {
		if o.KV != nil {
			p.KV = *o.KV
		}
		if o.KVTokens != nil {
			p.KVTokens = *o.KVTokens
		}
		if p.KV != "shared" {
			p.KVTokens = 0
		}
		mark(OverrideKV)
	}
	for _, field := range o.Reset {
		if field == OverridePool {
			p.Pool = ""
		}
		p.Overridden = slices.DeleteFunc(p.Overridden, func(f string) bool { return f == field })
	}
}
