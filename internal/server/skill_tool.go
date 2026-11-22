package server

import (
	"context"
	"errors"

	"github.com/erlidev/eika/internal/skill"
)

var errNoSkills = errors.New("skills unavailable")

// skillService gives skill_use the skills rows. It reads the store, never the
// volume.
type skillService struct{ server func() *Server }

func (k skillService) store() (interface {
	GetSkill(context.Context, string) (skill.Record, error)
	ListSkills(context.Context) ([]skill.Record, error)
}, error) {
	if s := k.server(); s != nil && s.deps.Store != nil {
		return s.deps.Store, nil
	}
	return nil, errNoSkills
}

func (k skillService) GetSkill(ctx context.Context, name string) (skill.Record, error) {
	st, err := k.store()
	if err != nil {
		return skill.Record{}, err
	}
	return st.GetSkill(ctx, name)
}

func (k skillService) ListSkills(ctx context.Context) ([]skill.Record, error) {
	st, err := k.store()
	if err != nil {
		return nil, err
	}
	return st.ListSkills(ctx)
}
