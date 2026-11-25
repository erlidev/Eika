package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/erlidev/eika/internal/skill"
	"github.com/erlidev/eika/internal/tool"
)

// Skills is the skills store as skill_use reads it. The store implements it
// (through the server, which may not have one yet). It is rows only: the tool
// never reads the volume or a computer.
type Skills interface {
	GetSkill(ctx context.Context, name string) (skill.Record, error)
	ListSkills(ctx context.Context) ([]skill.Record, error)
}

const (
	maxSkillMatches   = 10
	maxSkillQueryRune = 200
)

// skillUseTool is skill_use. It needs no computer; only an agent definition
// lists it. The schema names no skill, so installing one never changes any
// session's tools.
type skillUseTool struct{ skills Skills }

func (skillUseTool) Name() string   { return "skill_use" }
func (skillUseTool) Standalone()    {}
func (skillUseTool) AgentOnly()     {}
func (skillUseTool) ReadOnly() bool { return true }
func (skillUseTool) Description() string {
	return "Load an installed skill by name, as listed in the skills catalog, or find skills with query. A name returns the skill's instructions and the files it ships; read those files and run its scripts on your computer under /skills/<name>/."
}

func (skillUseTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"name":{"type":"string","description":"Skill name to load."},` +
		`"query":{"type":"string","description":"Words to find in skill names and descriptions; up to 10 matches."}},` +
		`"additionalProperties":false}`)
}

func (t skillUseTool) Call(ctx context.Context, _ tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	if t.skills == nil {
		return tool.Errorf("skill_use: skills unavailable"), nil
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	fields, err := objectArguments(raw, schema.Properties, nil)
	if err != nil {
		return tool.Errorf("skill_use: %v", err), nil
	}
	strs := map[string]string{}
	for k, v := range fields {
		if strs[k], err = stringArgument(k, v); err != nil {
			return tool.Errorf("skill_use: %v", err), nil
		}
	}
	_, hasName := strs["name"]
	_, hasQuery := strs["query"]
	if hasName == hasQuery {
		return tool.Errorf("skill_use: give either name or query"), nil
	}
	if hasName {
		return t.load(ctx, strs["name"])
	}
	return t.find(ctx, strs["query"])
}

// load returns one enabled, sound skill. Disabled, broken and unknown names
// answer alike, so the reply says nothing about what is installed.
func (t skillUseTool) load(ctx context.Context, name string) (tool.Result, error) {
	rec, err := t.skills.GetSkill(ctx, name)
	if errors.Is(err, skill.ErrNotFound) {
		return noSkill(name), nil
	}
	if err != nil {
		return tool.Result{}, err
	}
	if !rec.Loadable() {
		return noSkill(name), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<skill name=%q dir=%q>\n%s\n</skill>\n", rec.Name, "/skills/"+rec.Name, strings.TrimSpace(rec.Body))
	var files []string
	for _, f := range rec.Files {
		if f.Path != "SKILL.md" {
			files = append(files, fmt.Sprintf("- %s (%d bytes)", f.Path, f.Size))
		}
	}
	if len(files) > 0 {
		b.WriteString("\nFiles in /skills/" + rec.Name + "/ (read them on your computer; not loaded here):\n")
		b.WriteString(strings.Join(files, "\n") + "\n")
	}
	return tool.Text(b.String()), nil
}

func noSkill(name string) tool.Result {
	return tool.Errorf("skill_use: no skill available named %q", name)
}

func (t skillUseTool) find(ctx context.Context, query string) (tool.Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return tool.Errorf("skill_use: query must not be empty"), nil
	}
	if utf8.RuneCountInString(query) > maxSkillQueryRune {
		return tool.Errorf("skill_use: query must be at most %d characters", maxSkillQueryRune), nil
	}
	recs, err := t.skills.ListSkills(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	q := strings.ToLower(query)
	var lines []string
	for _, r := range recs { // by name
		if !r.Loadable() || len(lines) == maxSkillMatches {
			continue
		}
		if strings.Contains(strings.ToLower(r.Name), q) || strings.Contains(strings.ToLower(r.Description), q) {
			lines = append(lines, "- "+r.Name+": "+r.Description)
		}
	}
	if len(lines) == 0 {
		return tool.Text("No skills match."), nil
	}
	return tool.Text(strings.Join(lines, "\n")), nil
}

var _ tool.AgentOnly = skillUseTool{}
