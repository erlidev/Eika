package store

import (
	"context"
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
	APIKey    []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// providerColumns is the column list every provider query selects, in the
// order scanProvider reads them.
const providerColumns = `id, name, kind, base_url, api_key, created_at, updated_at`

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
	if err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return Provider{}, err
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}
