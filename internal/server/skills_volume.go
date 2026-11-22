package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/erlidev/eika/internal/computer"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/skill/volume"
)

// The private skills installer computer. It mounts the skills volume and is
// how Core stages, swaps, removes and hashes skill directories without
// writing the volume itself. Like the memory computer it is not a row in
// computers, so the computers API, the UI, the idle stopper and every tool's
// computer argument never see it, and no agent run is given its executor. Its
// id is generated once per install and recorded in skills_computer, so its
// container and volume (eika-cx-<id>) belong to this install alone. It sits
// on the internal sandbox network with egress none.
const skillsRole = "skills"

// EnsureSkillsInstaller makes the installer computer exist and run. Calling
// it again changes nothing. A host that cannot give a computer an internal
// network returns computer.ErrNoEgressControl rather than run one with a
// route out.
func (s *Server) EnsureSkillsInstaller(ctx context.Context) error {
	s.skillsMu.Lock()
	defer s.skillsMu.Unlock()

	id, err := s.deps.Store.EnsureSkillsComputerID(ctx)
	if err != nil {
		return err
	}
	s.skillsID.Store(&id)
	host, err := s.deps.Computers.Inspect(ctx, id)
	switch {
	case err == nil:
	case errors.Is(err, computer.ErrNoComputer):
		if host, err = s.deps.Computers.Create(ctx, computer.Spec{
			ID:    id,
			Image: s.sandboxImage(ctx),
			Root:  volume.Root,
			Role:  skillsRole,
			Confinement: computer.Confinement{
				Limits:  computer.Limits{CPUs: 1, MemoryBytes: 256 << 20, PIDs: 256},
				Proxied: true,
			},
		}); err != nil {
			return fmt.Errorf("create the skills installer: %w", err)
		}
		s.forgetEgress(id)
		s.log.Info("skills installer created")
	default:
		return err
	}
	if host.State != computer.StateRunning {
		if err := s.deps.Computers.Start(ctx, &host); err != nil {
			return fmt.Errorf("start the skills installer: %w", err)
		}
	}
	return nil
}

// SkillsVolume returns the skills volume's operations, starting the
// installer if it is stopped. It is for Core's skill installer alone.
func (s *Server) SkillsVolume(ctx context.Context) (*volume.Volume, error) {
	if err := s.EnsureSkillsInstaller(ctx); err != nil {
		return nil, err
	}
	id, err := s.deps.Store.SkillsComputerID(ctx)
	if err != nil {
		return nil, err
	}
	host, err := s.deps.Computers.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	ex, err := s.deps.Computers.Executor(host)
	if err != nil {
		return nil, err
	}
	return volume.New(ex), nil
}

// isSkillsInstaller is whether id is the installer computer's. Only a
// recorded id can match, so a lookup never records one.
func (s *Server) isSkillsInstaller(ctx context.Context, id string) bool {
	if known := s.skillsID.Load(); known != nil {
		return *known == id
	}
	rec, err := s.deps.Store.SkillsComputerID(ctx)
	if err != nil {
		return false
	}
	s.skillsID.Store(&rec)
	return rec == id
}

// keepSkillsInstaller ensures the installer, trying again after delay and
// then after longer ones, until that works or ctx is cancelled.
func (s *Server) keepSkillsInstaller(ctx context.Context, delay time.Duration) {
	for {
		err := s.EnsureSkillsInstaller(ctx)
		if err == nil {
			err = s.recoverSkills(ctx)
		}
		if err == nil || ctx.Err() != nil {
			return
		}
		if errors.Is(err, computer.ErrNoEgressControl) {
			s.log.Warn("skills installer not started: it needs the internal sandbox network", "error", err)
			return
		}
		s.log.Error("ensure skills installer", "error", err, "retry_in", delay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, longestHomeRetry)
	}
}

// SkillInstaller returns Core's skill installer, or nil until the skills rows
// have been reconciled with the volume. Nothing may install or load a skill
// before then.
func (s *Server) SkillInstaller() *skill.Installer { return s.skillInstaller.Load() }

// recoverSkills reconciles the skill rows with the volume, then publishes the
// installer. Rows whose directory is missing or changed are marked broken;
// unknown directories are logged and left alone.
func (s *Server) recoverSkills(ctx context.Context) error {
	in := skill.NewInstaller(skillsFiles{s}, s.deps.Store)
	rep, err := in.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("reconcile skills: %w", err)
	}
	s.log.Info("skills reconciled", "broken", rep.Broken, "repaired", rep.Repaired, "unknown_directories", rep.Unknown)
	s.skillInstaller.Store(in)
	return nil
}

// skillsFiles runs each volume operation through a fresh view of the
// installer computer, starting it if it stopped.
type skillsFiles struct{ s *Server }

func (f skillsFiles) vol(ctx context.Context) (*volume.Volume, error) { return f.s.SkillsVolume(ctx) }

func (f skillsFiles) Stage(ctx context.Context, name string, files []skill.File) error {
	v, err := f.vol(ctx)
	if err != nil {
		return err
	}
	return v.Stage(ctx, name, files)
}

func (f skillsFiles) HashStaged(ctx context.Context, name string) (string, error) {
	v, err := f.vol(ctx)
	if err != nil {
		return "", err
	}
	return v.HashStaged(ctx, name)
}

func (f skillsFiles) Replace(ctx context.Context, name string) error {
	v, err := f.vol(ctx)
	if err != nil {
		return err
	}
	return v.Replace(ctx, name)
}

func (f skillsFiles) Remove(ctx context.Context, name string) error {
	v, err := f.vol(ctx)
	if err != nil {
		return err
	}
	return v.Remove(ctx, name)
}

func (f skillsFiles) Hash(ctx context.Context, name string) (string, error) {
	v, err := f.vol(ctx)
	if err != nil {
		return "", err
	}
	return v.Hash(ctx, name)
}

func (f skillsFiles) List(ctx context.Context) ([]string, error) {
	v, err := f.vol(ctx)
	if err != nil {
		return nil, err
	}
	return v.List(ctx)
}
