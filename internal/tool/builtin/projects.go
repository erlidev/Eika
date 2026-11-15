package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"unicode/utf8"

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
func (t projectTool) Standalone()  {}
func (t projectTool) AgentOnly()   {}
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
func stringArguments(raw []byte, allowed map[string]json.RawMessage, required []string) (map[string]string, error) {
	fields, err := objectArguments(raw, allowed, required)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(fields))
	for key, v := range fields {
		s, err := stringArgument(key, v)
		if err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, nil
}

// objectArguments splits a call's arguments into the raw values of an object
// whose keys are allowed, each at most once. Arguments that are not valid
// UTF-8 are refused here: decoding would replace the bad bytes silently.
func objectArguments(raw []byte, allowed map[string]json.RawMessage, required []string) (map[string]json.RawMessage, error) {
	if len(raw) > maxFileArgsBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("arguments must be valid UTF-8 within the size limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("arguments must be an object")
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, fmt.Errorf("invalid argument")
		}
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("unknown argument %q", key)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("duplicate argument %q", key)
		}
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return nil, err
		}
		out[key] = v
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after arguments")
	}
	for _, k := range required {
		if _, ok := out[k]; !ok {
			return nil, fmt.Errorf("%s is required", k)
		}
	}
	return out, nil
}

// stringArgument decodes one argument that must be a string without an
// unpaired surrogate escape, which decoding would turn into U+FFFD.
func stringArgument(key string, v json.RawMessage) (string, error) {
	var s *string
	if json.Unmarshal(v, &s) != nil || s == nil {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if err := validStringEscapes(v); err != nil {
		return "", err
	}
	return *s, nil
}
