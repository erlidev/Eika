package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Model is one model the user configured on a provider.
type Model struct {
	ID         string
	ProviderID string
	// Name is what Eika calls the model, unique across every provider.
	Name string
	// Model is the identifier the provider's endpoint knows the model by.
	Model string
	// ContextWindow is the model's total token budget.
	ContextWindow int
	// MaxOutput is the most tokens one response may have.
	MaxOutput int
	// ReasoningEffort is the Chat Completions reasoning_effort value; empty
	// leaves it to the endpoint.
	ReasoningEffort string
	// PreserveThinking asks a compatible endpoint for reasoning content and
	// replays it on later turns.
	PreserveThinking bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// modelColumns is the column list every model query selects, in the order
// scanModel reads them.
const modelColumns = `id, provider_id, name, model, context_window, max_output,
	reasoning_effort, preserve_thinking, created_at, updated_at`

// CreateModel inserts m and returns it with the fields the database
// assigned. An empty ID gets a fresh one; a duplicate name is ErrConflict and
// an unknown provider is ErrNotFound.
func (s *Store) CreateModel(ctx context.Context, m Model) (Model, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	const q = `INSERT INTO models (id, provider_id, name, model, context_window, max_output,
			reasoning_effort, preserve_thinking)
		SELECT $1, id, $3, $4, $5, $6, $7, $8 FROM providers WHERE id = $2
		RETURNING ` + modelColumns
	out, err := scanModel(s.pool.QueryRow(ctx, q, m.ID, m.ProviderID, m.Name, m.Model,
		m.ContextWindow, m.MaxOutput, m.ReasoningEffort, m.PreserveThinking))
	if err != nil {
		return Model{}, wrap("create model "+m.Name, err)
	}
	return out, nil
}

// Model returns the model with the given id.
func (s *Store) Model(ctx context.Context, id string) (Model, error) {
	m, err := scanModel(s.pool.QueryRow(ctx, `SELECT `+modelColumns+` FROM models WHERE id = $1`, id))
	if err != nil {
		return Model{}, wrap("read model "+id, err)
	}
	return m, nil
}

// ModelByName returns the model with the given name.
func (s *Store) ModelByName(ctx context.Context, name string) (Model, error) {
	m, err := scanModel(s.pool.QueryRow(ctx, `SELECT `+modelColumns+` FROM models WHERE name = $1`, name))
	if err != nil {
		return Model{}, wrap("read model "+name, err)
	}
	return m, nil
}

// Models returns every model, oldest first, which is the order the UI lists
// them in and the order a run falls back through.
func (s *Store) Models(ctx context.Context) ([]Model, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+modelColumns+` FROM models ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list models", err)
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, wrap("list models", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list models", err)
	}
	return out, nil
}

// UpdateModel writes every field of m but its provider over the stored row
// and returns the row as it now stands. A name another model has is
// ErrConflict.
func (s *Store) UpdateModel(ctx context.Context, m Model) (Model, error) {
	const q = `UPDATE models SET name = $2, model = $3, context_window = $4, max_output = $5,
			reasoning_effort = $6, preserve_thinking = $7, updated_at = now()
		WHERE id = $1
		RETURNING ` + modelColumns
	out, err := scanModel(s.pool.QueryRow(ctx, q, m.ID, m.Name, m.Model, m.ContextWindow,
		m.MaxOutput, m.ReasoningEffort, m.PreserveThinking))
	if err != nil {
		return Model{}, wrap("update model "+m.ID, err)
	}
	return out, nil
}

// DeleteModel removes a model.
func (s *Store) DeleteModel(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM models WHERE id = $1`, id)
	if err != nil {
		return wrap("delete model "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("delete model "+id, pgx.ErrNoRows)
	}
	return nil
}

// scanModel reads one model row.
func scanModel(row pgx.Row) (Model, error) {
	var m Model
	if err := row.Scan(&m.ID, &m.ProviderID, &m.Name, &m.Model, &m.ContextWindow, &m.MaxOutput,
		&m.ReasoningEffort, &m.PreserveThinking, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return Model{}, err
	}
	m.CreatedAt, m.UpdatedAt = m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	return m, nil
}
