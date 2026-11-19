package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// GlobalInstructions contains text written only by the user.
type GlobalInstructions struct {
	AboutMe string `json:"about_me"`
	Rules   string `json:"rules"`
}

// ErrInvalidInstructions reports invalid instruction text.
var ErrInvalidInstructions = errors.New("invalid instructions")

// ValidateInstructionText checks the byte limit and PostgreSQL text requirements.
func ValidateInstructionText(text string) error {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > 65536 {
		return fmt.Errorf("%w: text must be valid UTF-8 of at most 65536 bytes without NUL", ErrInvalidInstructions)
	}
	return nil
}

// Instructions returns the user's global instructions, empty on a new database.
func (s *Store) Instructions(ctx context.Context) (GlobalInstructions, error) {
	var out GlobalInstructions
	err := s.pool.QueryRow(ctx, `SELECT about_me, rules FROM instructions WHERE singleton`).Scan(&out.AboutMe, &out.Rules)
	if err == nil {
		return out, nil
	}
	return out, wrap("read instructions", err)
}

// SetInstructions replaces global instructions. Only the user API calls this method.
func (s *Store) SetInstructions(ctx context.Context, in GlobalInstructions) error {
	for _, text := range []string{in.AboutMe, in.Rules} {
		if err := ValidateInstructionText(text); err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx, `UPDATE instructions SET about_me=$1, rules=$2 WHERE singleton`, in.AboutMe, in.Rules)
	if err == nil {
		return nil
	}
	return wrap("write instructions", err)
}

// ProjectInstructions returns user text, or empty text for a project without instructions.
func (s *Store) ProjectInstructions(ctx context.Context, id string) (string, error) {
	var out string
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(i.instructions, '') FROM projects p
 LEFT JOIN project_instructions i ON i.project_id=p.id WHERE p.id=$1`, id).Scan(&out)
	if err == nil {
		return out, nil
	}
	return out, wrap("read project instructions", err)
}

// SetProjectInstructions replaces project instructions through the user API only.
// Empty text clears instructions. Project metadata methods cannot change them.
func (s *Store) SetProjectInstructions(ctx context.Context, id, text string) error {
	if err := ValidateInstructionText(text); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO project_instructions (project_id, instructions)
 SELECT id, $2 FROM projects WHERE id=$1
 ON CONFLICT (project_id) DO UPDATE SET instructions=EXCLUDED.instructions, version=project_instructions.version+1`, id, text)
	if err != nil {
		return wrap("write project instructions", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectInstructionVersion reads project instructions and their revision. A new project has revision zero.
func (s *Store) ProjectInstructionVersion(ctx context.Context, id string) (string, int64, error) {
	var text string
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(i.instructions, ''), COALESCE(i.version, 0) FROM projects p LEFT JOIN project_instructions i ON i.project_id=p.id WHERE p.id=$1`, id).Scan(&text, &version)
	if err != nil {
		return "", 0, wrap("read project instructions", err)
	}
	return text, version, nil
}

// ReplaceProjectInstructions writes only if the revision matches. A nil revision forces the write.
func (s *Store) ReplaceProjectInstructions(ctx context.Context, id, text string, version *int64) (int64, error) {
	if err := ValidateInstructionText(text); err != nil {
		return 0, err
	}
	var next int64
	err := s.pool.QueryRow(ctx, `INSERT INTO project_instructions (project_id, instructions, version)
 SELECT id, $2, 1 FROM projects WHERE id=$1 AND ($3::bigint IS NULL OR $3=0 OR EXISTS (SELECT 1 FROM project_instructions WHERE project_id=$1))
 ON CONFLICT (project_id) DO UPDATE SET instructions=EXCLUDED.instructions, version=project_instructions.version+1
 WHERE $3::bigint IS NULL OR project_instructions.version=$3 RETURNING version`, id, text, version).Scan(&next)
	if err == nil {
		return next, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, wrap("write project instructions", err)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id=$1)`, id).Scan(&exists); err != nil {
		return 0, wrap("read project", err)
	}
	if !exists {
		return 0, ErrNotFound
	}
	return 0, ErrConflict
}
