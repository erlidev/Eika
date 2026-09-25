package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ProfileSettings are what a profile configures about a run, and what a
// session overrides of its profile, in the one shape both share. Every field
// is optional and a field left unset falls through to the layer below: the
// run request, then the session, the profile, the model, and the provider.
// It is also the JSON a session's overrides are stored as, where an absent
// key is inherited.
type ProfileSettings struct {
	// ModelID names the model a run uses. Empty is not set: the profile's,
	// or the default model.
	ModelID string `json:"model_id,omitempty"`
	// WorkspacePrompt and ChatPrompt replace the built-in base prompt of a
	// workspace session and of a chat when they are not nil, the empty
	// string included.
	WorkspacePrompt *string `json:"workspace_prompt,omitempty"`
	ChatPrompt      *string `json:"chat_prompt,omitempty"`
	// Instructions follow the base prompt and the context files.
	Instructions *string `json:"instructions,omitempty"`
	// ContextFiles says whether the workspace's context files are read.
	ContextFiles *bool `json:"context_files,omitempty"`
	// Sampling is the JSON object of a provider.Sampling, whose keys are the
	// parameters set here. The store keeps it as it is given, except that
	// an object with no keys reads back as nil: nil sets none.
	Sampling json.RawMessage `json:"sampling,omitempty"`
}

// Empty reports whether the settings set nothing.
func (p ProfileSettings) Empty() bool {
	return p.ModelID == "" && p.WorkspacePrompt == nil && p.ChatPrompt == nil && p.Instructions == nil &&
		p.ContextFiles == nil && (len(p.Sampling) == 0 || string(p.Sampling) == "{}")
}

// Profile is a named configuration of what a run sends. The migration
// creates one, named Default, that sets nothing.
type Profile struct {
	ID          string
	Name        string
	Description string
	ProfileSettings
	// Tools is the profile's choice of tools, in the shape of Session.Tools,
	// where an entry mcp__<server>__* stands for every tool of that server.
	// Nil is every tool the session can run; an empty, non-nil slice is
	// none.
	Tools     []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// profileColumns is the column list every profile query selects, in the
// order scanProfile reads them.
const profileColumns = `id, name, description, model_id, workspace_prompt, chat_prompt, instructions,
	context_files, tools, sampling, created_at, updated_at`

// CreateProfile inserts p and returns it as stored. An empty ID gets a fresh
// one; a name another profile has is ErrConflict and an unknown model is
// ErrNotFound.
func (s *Store) CreateProfile(ctx context.Context, p Profile) (Profile, error) {
	if p.ID == "" {
		p.ID = NewID()
	}
	const q = `INSERT INTO profiles (id, name, description, model_id, workspace_prompt, chat_prompt,
			instructions, context_files, tools, sampling)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + profileColumns
	out, err := scanProfile(s.pool.QueryRow(ctx, q, p.ID, p.Name, p.Description, nullable(p.ModelID),
		p.WorkspacePrompt, p.ChatPrompt, p.Instructions, p.ContextFiles, p.Tools, jsonObject(p.Sampling)))
	if err != nil {
		return Profile{}, wrapMissing("create profile "+p.Name, err)
	}
	return out, nil
}

// Profile returns the profile with the given id.
func (s *Store) Profile(ctx context.Context, id string) (Profile, error) {
	p, err := scanProfile(s.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM profiles WHERE id = $1`, id))
	if err != nil {
		return Profile{}, wrap("read profile "+id, err)
	}
	return p, nil
}

// Profiles returns every profile, oldest first. The first one is the
// default when the default_profile setting names none that exists.
func (s *Store) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+profileColumns+` FROM profiles ORDER BY created_at, id`)
	if err != nil {
		return nil, wrap("list profiles", err)
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, wrap("list profiles", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap("list profiles", err)
	}
	return out, nil
}

// UpdateProfile writes every field of p over the stored row and returns the
// row as it now stands. A name another profile has is ErrConflict and an
// unknown model is ErrNotFound.
func (s *Store) UpdateProfile(ctx context.Context, p Profile) (Profile, error) {
	const q = `UPDATE profiles SET name = $2, description = $3, model_id = $4, workspace_prompt = $5,
			chat_prompt = $6, instructions = $7, context_files = $8, tools = $9, sampling = $10,
			updated_at = now()
		WHERE id = $1
		RETURNING ` + profileColumns
	out, err := scanProfile(s.pool.QueryRow(ctx, q, p.ID, p.Name, p.Description, nullable(p.ModelID),
		p.WorkspacePrompt, p.ChatPrompt, p.Instructions, p.ContextFiles, p.Tools, jsonObject(p.Sampling)))
	if err != nil {
		return Profile{}, wrapMissing("update profile "+p.ID, err)
	}
	return out, nil
}

// DeleteProfile removes a profile. The sessions that chose it go back to the
// default profile. The last profile cannot be deleted, which is
// ErrConflict: a run always has one to resolve its configuration from.
func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	op := "delete profile " + id
	return s.tx(ctx, func(q querier) error {
		// Locking every row makes the count hold until the delete commits,
		// so two deletes at once cannot leave no profile at all.
		ids, err := lockedIDs(ctx, q, `SELECT id FROM profiles FOR UPDATE`)
		if err != nil {
			return wrap(op, err)
		}
		switch {
		case !slices.Contains(ids, id):
			return wrap(op, pgx.ErrNoRows)
		case len(ids) == 1:
			return fmt.Errorf("%s: it is the last profile: %w", op, ErrConflict)
		}
		if _, err := q.Exec(ctx, `DELETE FROM profiles WHERE id = $1`, id); err != nil {
			return wrap(op, err)
		}
		return nil
	})
}

// lockedIDs runs a query that selects one id column and returns the ids.
func lockedIDs(ctx context.Context, q querier, query string) ([]string, error) {
	rows, err := q.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SetSessionProfile chooses a session's profile. An empty profileID goes back
// to whichever profile is the default when a run starts; an unknown one is
// ErrNotFound.
func (s *Store) SetSessionProfile(ctx context.Context, id, profileID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET profile_id = $2, updated_at = now() WHERE id = $1`,
		id, nullable(profileID))
	if err != nil {
		return wrapMissing("set session profile "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set session profile "+id, pgx.ErrNoRows)
	}
	return nil
}

// SetSessionOverrides replaces the profile settings a session sets for
// itself. Settings that set nothing clear them.
func (s *Store) SetSessionOverrides(ctx context.Context, id string, overrides ProfileSettings) error {
	encoded, err := overridesJSON(overrides)
	if err != nil {
		return fmt.Errorf("set session overrides %s: %w", id, err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET overrides = $2, updated_at = now() WHERE id = $1`,
		id, encoded)
	if err != nil {
		return wrap("set session overrides "+id, err)
	}
	if tag.RowsAffected() == 0 {
		return wrap("set session overrides "+id, pgx.ErrNoRows)
	}
	return nil
}

// overridesJSON is the value a sessions.overrides column is written from:
// NULL for settings that set nothing, their JSON otherwise.
func overridesJSON(o ProfileSettings) ([]byte, error) {
	if o.Empty() {
		return nil, nil
	}
	encoded, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("encode overrides: %w", err)
	}
	return encoded, nil
}

// readOverrides decodes a sessions.overrides column; NULL overrides nothing.
func readOverrides(raw []byte) (ProfileSettings, error) {
	var o ProfileSettings
	if len(raw) == 0 {
		return o, nil
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return ProfileSettings{}, fmt.Errorf("decode overrides: %w", err)
	}
	return o, nil
}

// jsonObject is the value a jsonb object column is written from: nil or
// empty JSON is the empty object.
func jsonObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte(`{}`)
	}
	return raw
}

// wrapMissing is wrap for a write that names another row by a foreign key:
// a row that names one that does not exist is ErrNotFound.
func wrapMissing(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return fmt.Errorf("%s: %s: %w", op, pgErr.Detail, ErrNotFound)
	}
	return wrap(op, err)
}

// scanProfile reads one profile row.
func scanProfile(row pgx.Row) (Profile, error) {
	var (
		p        Profile
		model    *string
		sampling []byte
	)
	err := row.Scan(&p.ID, &p.Name, &p.Description, &model, &p.WorkspacePrompt, &p.ChatPrompt,
		&p.Instructions, &p.ContextFiles, &p.Tools, &sampling, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Profile{}, err
	}
	p.ModelID = text(model)
	if string(sampling) != "{}" {
		p.Sampling = json.RawMessage(sampling)
	}
	p.CreatedAt, p.UpdatedAt = p.CreatedAt.UTC(), p.UpdatedAt.UTC()
	return p, nil
}
