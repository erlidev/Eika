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
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

type preambleData struct {
	global              store.GlobalInstructions
	projectInstructions string
	projects            []store.Project
	project             *store.Project
	brief               string
	home                bool
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
	return strings.Join(parts, "\n\n")
}

func (s *Server) buildPreamble(ctx context.Context, sess store.Session) (string, error) {
	d := preambleData{brief: sess.Brief, home: sess.Kind == store.SessionHome, zone: "UTC", locale: "unspecified"}
	var err error
	if d.global, err = s.deps.Store.Instructions(ctx); err != nil {
		return "", err
	}
	if d.home {
		if d.projects, err = s.deps.Store.Projects(ctx); err != nil {
			return "", err
		}
	}
	if sess.ProjectID != "" {
		p, err := s.deps.Store.Project(ctx, sess.ProjectID)
		if err != nil {
			return "", err
		}
		d.project = &p
		if d.projectInstructions, err = s.deps.Store.ProjectInstructions(ctx, sess.ProjectID); err != nil {
			return "", err
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
			return "", err
		}
		var value string
		if json.Unmarshal(setting.Value, &value) == nil && value != "" {
			*item.target = value
		}
	}
	if _, err := time.LoadLocation(d.zone); err != nil {
		d.zone = "UTC"
	}
	return renderPreamble(d), nil
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

// refreshSnapshot stores a compaction of session id together with its
// replacement snapshot, built from what the preamble and the agent
// definition say now, and returns the snapshot with the tools a run of the
// session offers from it.
func (s *Server) refreshSnapshot(ctx context.Context, id string, c agent.Compaction) (agent.Snapshot, error) {
	sess, err := s.deps.Store.Session(ctx, id)
	if err != nil {
		return agent.Snapshot{}, err
	}
	text, err := s.buildPreamble(ctx, sess)
	if err != nil {
		return agent.Snapshot{}, err
	}
	cfg, err := s.configure(ctx, sess, "")
	if err != nil {
		return agent.Snapshot{}, err
	}
	prompt, tools := cfg.pinned()
	entries, err := preambleCompactionEntries(text, prompt, tools, c)
	if err != nil {
		return agent.Snapshot{}, err
	}
	if err := s.deps.Store.AppendEntries(ctx, sess.ID, entries...); err != nil {
		return agent.Snapshot{}, err
	}
	return agent.Snapshot{Preamble: text, Prompt: &prompt, Tools: s.runTools(sess, tools, nil)}, nil
}

func preambleCompactionEntries(text, prompt string, tools []string, c agent.Compaction) ([]store.Entry, error) {
	preamble, err := session.PreambleEntry(text, &prompt, tools)
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
func (s *Server) ensurePreamble(ctx context.Context, sess store.Session, loaded *agent.Session, cfg runConfig) error {
	if sess.Agent == "" || (loaded.Preamble != "" && loaded.Prompt != nil) {
		return nil
	}
	text := loaded.Preamble
	if text == "" {
		var err error
		if text, err = s.buildPreamble(ctx, sess); err != nil {
			return err
		}
	}
	prompt, tools := cfg.pinned()
	e, err := session.PreambleEntry(text, &prompt, tools)
	if err != nil {
		return err
	}
	if _, err := s.tree.Append(ctx, sess.ID, e); err != nil {
		return err
	}
	loaded.Preamble, loaded.Prompt, loaded.Tools = text, &prompt, tools
	return nil
}
