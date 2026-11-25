package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/memory"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/store"
)

type preambleData struct {
	global              store.GlobalInstructions
	projectInstructions string
	projects            []store.Project
	project             *store.Project
	brief               string
	home                bool
	skills              string // the skill catalog, home and worker; already labelled
	memory              string // the memory section, home only; already labelled
	zone, locale        string
}

func renderPreamble(d preambleData) string {
	var parts []string
	add := func(label, text string) {
		if text != "" {
			parts = append(parts, label+"\n"+text)
		}
	}
	add("About me", d.global.AboutMe)
	add("Rules", d.global.Rules)
	add("Project instructions", d.projectInstructions)
	if d.home {
		sort.Slice(d.projects, func(i, j int) bool { return d.projects[i].Slug < d.projects[j].Slug })
		var projects []string
		for _, p := range d.projects {
			if p.Status == "active" {
				projects = append(projects, fmt.Sprintf("- %s (%s): %s", p.Name, p.Slug, p.Description))
			}
		}
		add("Active projects", strings.Join(projects, "\n"))
		var agents []string
		for _, def := range agentdef.Builtins() {
			if def.Name != "eika" {
				agents = append(agents, "- "+def.Name+": "+def.Description)
			}
		}
		add("Delegable agents", strings.Join(agents, "\n"))
	}
	if d.project != nil {
		add("Project", fmt.Sprintf("%s (%s): %s\nDirectory: %s", d.project.Name, d.project.Slug, d.project.Description, d.project.Directory))
	}
	add("Brief", d.brief)
	add("Time zone", d.zone)
	add("Locale", d.locale)
	if d.skills != "" {
		parts = append(parts, strings.TrimRight(d.skills, "\n"))
	}
	if d.memory != "" {
		parts = append(parts, d.memory)
	}
	return strings.Join(parts, "\n\n")
}

// textCounter adapts the model's token counter to memory.CountFunc. The count
// is the endpoint's own or it is not exact; nothing here estimates.
func textCounter(c *counter.Counter) memory.CountFunc {
	return func(ctx context.Context, text string) (int, bool, error) {
		n, exact := c.CountText(ctx, text)
		return n, exact, nil
	}
}

// buildMemory reads one revision of the active memories and builds the home
// session's memory section from it. Pending, archived and expired memories
// never load. With no eligible memory there is no section.
func (s *Server) buildMemory(ctx context.Context, count memory.CountFunc) (agent.MemorySnapshot, error) {
	memories, seq, err := s.deps.Store.ActiveMemories(ctx)
	if err != nil {
		return agent.MemorySnapshot{}, err
	}
	now := time.Now()
	rev := memory.Revision(memories, now)
	if rev == "" {
		return agent.MemorySnapshot{ChangeSeq: seq}, nil
	}
	sec, err := memory.BuildSection(ctx, count, memories, now)
	if err != nil {
		return agent.MemorySnapshot{}, err
	}
	return agent.MemorySnapshot{Revision: rev, Text: sec.Text, ChangeSeq: seq}, nil
}

// memorySource supplies the memory section of a preamble. It is called for
// the home session only; nil means no section.
type memorySource func(ctx context.Context) (agent.MemorySnapshot, error)

// skillSource supplies the skill catalog of a preamble: the section text, or
// empty for none. It is called for sessions that may use skills (home and
// worker); nil means no catalog.
type skillSource func(ctx context.Context) (string, error)

// hasSkills is whether a session's snapshot carries the skill catalog.
func hasSkills(sess store.Session) bool {
	return sess.Kind == store.SessionHome || sess.Agent == "worker"
}

// buildSkills builds the catalog of the enabled, loadable skills, counted
// exactly with count. No loadable skill means no section.
func (s *Server) buildSkills(ctx context.Context, count memory.CountFunc) (string, error) {
	records, err := s.deps.Store.ListSkills(ctx)
	if err != nil {
		return "", err
	}
	c, err := skill.BuildCatalog(ctx, skill.CountFunc(count), records)
	return c.Text, err
}

// catalogOf returns the catalog section a stored preamble holds, or "". The
// memory section that ends the preamble is set aside first, and a catalog has
// no blank line inside, so it ends at the next one.
func catalogOf(preamble string, mem agent.MemorySnapshot) string {
	if mem.Text != "" {
		preamble = strings.TrimSuffix(preamble, mem.Text)
	}
	i := strings.LastIndex("\n\n"+preamble, "\n\n"+skill.CatalogHeading+"\n")
	if i < 0 {
		return ""
	}
	text, _, _ := strings.Cut(("\n\n" + preamble)[i+2:], "\n\n")
	return text + "\n"
}

// buildPreamble builds the snapshot text of sess, and the memory section it
// ends with. Only the home session carries one (subagent prefixes have none).
// The skill catalog precedes it, for home and worker sessions.
func (s *Server) buildPreamble(ctx context.Context, sess store.Session, mem memorySource, skills skillSource) (string, agent.MemorySnapshot, error) {
	d := preambleData{brief: sess.Brief, home: sess.Kind == store.SessionHome, zone: "UTC", locale: "unspecified"}
	var (
		err  error
		snap agent.MemorySnapshot
	)
	if d.global, err = s.deps.Store.Instructions(ctx); err != nil {
		return "", agent.MemorySnapshot{}, err
	}
	if d.home {
		if d.projects, err = s.deps.Store.Projects(ctx); err != nil {
			return "", agent.MemorySnapshot{}, err
		}
		if mem != nil {
			if snap, err = mem(ctx); err != nil {
				return "", agent.MemorySnapshot{}, err
			}
			d.memory = snap.Text
		}
	}
	if skills != nil && hasSkills(sess) {
		if d.skills, err = skills(ctx); err != nil {
			return "", agent.MemorySnapshot{}, err
		}
	}
	if sess.ProjectID != "" {
		p, err := s.deps.Store.Project(ctx, sess.ProjectID)
		if err != nil {
			return "", agent.MemorySnapshot{}, err
		}
		d.project = &p
		if d.projectInstructions, err = s.deps.Store.ProjectInstructions(ctx, sess.ProjectID); err != nil {
			return "", agent.MemorySnapshot{}, err
		}
	}
	for _, item := range []struct {
		key    string
		target *string
	}{{"time_zone", &d.zone}, {"locale", &d.locale}} {
		setting, err := s.deps.Store.Setting(ctx, item.key)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", agent.MemorySnapshot{}, err
		}
		var value string
		if json.Unmarshal(setting.Value, &value) == nil && value != "" {
			*item.target = value
		}
	}
	if _, err := time.LoadLocation(d.zone); err != nil {
		d.zone = "UTC"
	}
	return renderPreamble(d), snap, nil
}

// pinned is the part of the agent definition c resolved that a snapshot
// pins: its base prompt and its tool names.
func (c runConfig) pinned() (string, []string) {
	prompt := agent.ChatPrompt
	if c.chatPrompt != nil {
		prompt = *c.chatPrompt
	}
	tools := slices.Clone(c.tools)
	if tools == nil {
		tools = []string{}
	}
	return prompt, tools
}

// withSnapshot is c with the base prompt and tools of the snapshot a session
// loaded, when it pins a definition: an agent session keeps them until a
// compaction replaces the snapshot, whatever the definition says now
// (runtime.md 4).
func (c runConfig) withSnapshot(loaded *agent.Session) runConfig {
	if loaded.Prompt == nil {
		return c
	}
	c.chatPrompt, c.tools = loaded.Prompt, slices.Clone(loaded.Tools)
	return c
}

// refreshSnapshot builds the replacement snapshot of session id from what
// the preamble and the agent definition say now, stores it together with
// the compaction record measures against it, and returns it with the tools a
// run of the session offers from it.
func (s *Server) refreshSnapshot(ctx context.Context, id string, record func(agent.Snapshot) agent.Compaction) (agent.Snapshot, error) {
	sess, err := s.deps.Store.Session(ctx, id)
	if err != nil {
		return agent.Snapshot{}, err
	}
	cfg, err := s.configure(ctx, sess, "")
	if err != nil {
		return agent.Snapshot{}, err
	}
	// The memory section is counted with the model's own counter. When it
	// cannot be counted exactly the previous snapshot's section stays, and so
	// does its revision, so the next notice still compares against what the
	// prefix holds.
	ctr, release := s.counterFor(ctx, sess.ID, cfg.model, cfg.modelOK)
	defer release()
	previous, err := session.NewStore(s.tree).Load(ctx, sess.ID)
	if err != nil {
		return agent.Snapshot{}, err
	}
	text, mem, err := s.buildPreamble(ctx, sess, func(ctx context.Context) (agent.MemorySnapshot, error) {
		m, err := s.buildMemory(ctx, textCounter(ctr))
		if err != nil {
			s.log.Warn("keep the previous memory section: count failed", "session_id", sess.ID, "error", err)
			return previous.Memory, nil
		}
		return m, nil
	}, func(ctx context.Context) (string, error) {
		text, err := s.buildSkills(ctx, textCounter(ctr))
		if err != nil {
			s.log.Warn("keep the previous skill catalog: count failed", "session_id", sess.ID, "error", err)
			return catalogOf(previous.Preamble, previous.Memory), nil
		}
		return text, nil
	})
	if err != nil {
		return agent.Snapshot{}, err
	}
	prompt, tools := cfg.pinned()
	snap := agent.Snapshot{Preamble: text, Memory: mem, Prompt: &prompt, Tools: s.runTools(sess, tools, nil)}
	entries, err := preambleCompactionEntries(text, mem, prompt, tools, record(snap))
	if err != nil {
		return agent.Snapshot{}, err
	}
	if err := s.deps.Store.AppendEntries(ctx, sess.ID, entries...); err != nil {
		return agent.Snapshot{}, err
	}
	if sess.Kind == store.SessionHome {
		s.notices.setSnapshotSeq(sess.ID, mem.ChangeSeq)
	}
	return snap, nil
}

func preambleCompactionEntries(text string, mem agent.MemorySnapshot, prompt string, tools []string, c agent.Compaction) ([]store.Entry, error) {
	preamble, err := session.PreambleEntryWithMemory(text, mem, &prompt, tools)
	if err != nil {
		return nil, err
	}
	compact, err := session.CompactionEntry(c)
	if err != nil {
		return nil, err
	}
	return []store.Entry{preamble, compact}, nil
}

// ensurePreamble stores the first snapshot of an agent session, pinning the
// definition cfg resolved. A snapshot stored before definitions were pinned
// is stored again with the same text and cfg's definition, so the prefix it
// sent is unchanged and later edits wait for a compaction.
func (s *Server) ensurePreamble(ctx context.Context, sess store.Session, loaded *agent.Session, cfg runConfig, ctr *counter.Counter) error {
	if sess.Agent == "" || (loaded.Preamble != "" && loaded.Prompt != nil) {
		return nil
	}
	text, mem := loaded.Preamble, loaded.Memory
	if text == "" {
		var err error
		// A first snapshot that cannot count its memory exactly has no
		// memory section; the first compaction adds one.
		if text, mem, err = s.buildPreamble(ctx, sess, func(ctx context.Context) (agent.MemorySnapshot, error) {
			m, err := s.buildMemory(ctx, textCounter(ctr))
			if err != nil {
				s.log.Warn("first snapshot has no memory section: count failed", "session_id", sess.ID, "error", err)
				return agent.MemorySnapshot{}, nil
			}
			return m, nil
		}, func(ctx context.Context) (string, error) {
			text, err := s.buildSkills(ctx, textCounter(ctr))
			if err != nil {
				s.log.Warn("first snapshot has no skill catalog: count failed", "session_id", sess.ID, "error", err)
				return "", nil
			}
			return text, nil
		}); err != nil {
			return err
		}
	}
	prompt, tools := cfg.pinned()
	e, err := session.PreambleEntryWithMemory(text, mem, &prompt, tools)
	if err != nil {
		return err
	}
	if _, err := s.tree.Append(ctx, sess.ID, e); err != nil {
		return err
	}
	loaded.Preamble, loaded.Memory, loaded.Prompt, loaded.Tools = text, mem, &prompt, tools
	return nil
}
