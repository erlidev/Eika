package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"path"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

// Projects supplies project metadata and user instructions to the project tools.
type Projects interface {
	Projects(context.Context) ([]store.Project, error)
	CreateProject(context.Context, string, store.ProjectFields) (store.Project, error)
	UpdateProject(context.Context, string, store.ProjectUpdate) (store.Project, error)
	ProjectInstructions(context.Context, string) (string, error)
}

type projectTool struct {
	name     string
	projects Projects
	home     func(context.Context, string) (executor.Executor, func(), error)
}

func (t projectTool) Name() string { return t.name }

// ReadOnly is true for project_list alone.
func (t projectTool) ReadOnly() bool { return t.name == "project_list" }
func (t projectTool) Standalone()    {}
func (t projectTool) AgentOnly()     {}
func (t projectTool) Description() string {
	switch t.name {
	case "project_list":
		return "List active projects."
	case "project_open":
		return "Open a project by slug and read its description, user instructions and STATUS.md."
	case "project_create":
		return "Create a project. User instructions can only be changed by the user."
	default:
		return "Update a project's name, description, status or directory by slug. User instructions can only be changed by the user."
	}
}
func (t projectTool) Schema() json.RawMessage {
	props := map[string]any{}
	required := []string{}
	fields := []string{}
	switch t.name {
	case "project_open":
		fields = []string{"project"}
		required = fields
	case "project_create":
		fields = []string{"slug", "name", "description", "status", "directory"}
		required = []string{"slug", "name"}
	case "project_update":
		fields = []string{"project", "name", "description", "status", "directory"}
		required = []string{"project"}
	}
	for _, f := range fields {
		props[f] = map[string]string{"type": "string"}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return b
}
func (t projectTool) Call(ctx context.Context, c tool.CallContext, raw json.RawMessage) (tool.Result, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(t.Schema(), &schema)
	args, err := stringArguments(raw, schema.Properties, schema.Required)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.projects == nil {
		return tool.Errorf("%s: project store unavailable", t.name), nil
	}
	if t.name == "project_create" {
		p, err := t.projects.CreateProject(ctx, args["slug"], store.ProjectFields{Name: args["name"], Description: args["description"], Status: args["status"], Directory: args["directory"]})
		if err != nil {
			return tool.Errorf("%s: %v", t.name, err), nil
		}
		return projectResult(p), nil
	}
	projects, err := t.projects.Projects(ctx)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.name == "project_list" {
		active := make([]map[string]string, 0)
		for _, p := range projects {
			if p.Status == "active" {
				active = append(active, map[string]string{"slug": p.Slug, "name": p.Name, "description": p.Description, "status": p.Status})
			}
		}
		b, _ := json.Marshal(active)
		return tool.Text(string(b)), nil
	}
	var found *store.Project
	for i := range projects {
		if projects[i].Slug == args["project"] {
			found = &projects[i]
			break
		}
	}
	if found == nil {
		return tool.Errorf("%s: project not found", t.name), nil
	}
	if t.name == "project_update" {
		ptr := func(k string) *string {
			v, ok := args[k]
			if !ok {
				return nil
			}
			return &v
		}
		p, err := t.projects.UpdateProject(ctx, found.ID, store.ProjectUpdate{Name: ptr("name"), Description: ptr("description"), Status: ptr("status"), Directory: ptr("directory")})
		if err != nil {
			return tool.Errorf("%s: %v", t.name, err), nil
		}
		return projectResult(p), nil
	}
	instructions, err := t.projects.ProjectInstructions(ctx, found.ID)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	if t.home == nil {
		return tool.Errorf("%s: home computer unavailable", t.name), nil
	}
	ex, release, err := t.home(ctx, c.SessionID)
	if err != nil {
		return tool.Errorf("%s: %v", t.name, err), nil
	}
	defer release()
	status, err := readTextFile(ctx, ex, path.Join(found.Directory, "STATUS.md"), maxOutputBytes)
	if err != nil && !errors.Is(err, executor.ErrNotFound) {
		return tool.Errorf("%s: read STATUS.md: %v", t.name, err), nil
	}
	b, _ := json.Marshal(map[string]any{"slug": found.Slug, "name": found.Name, "description": found.Description, "status": found.Status, "directory": found.Directory, "instructions": instructions, "status_file": string(status)})
	return tool.Text(string(b)), nil
}
func projectResult(p store.Project) tool.Result {
	b, _ := json.Marshal(map[string]string{"slug": p.Slug, "name": p.Name, "description": p.Description, "status": p.Status, "directory": p.Directory})
	return tool.Text(string(b))
}
