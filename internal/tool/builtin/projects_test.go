package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/executor"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool"
)

type projectFake struct {
	rows         []store.Project
	instructions map[string]string
}

func (f *projectFake) Projects(context.Context) ([]store.Project, error) { return f.rows, nil }
func (f *projectFake) CreateProject(_ context.Context, slug string, fields store.ProjectFields) (store.Project, error) {
	if fields.Directory == "" {
		fields.Directory = "/home/eika/projects/" + slug
	}
	if fields.Status == "" {
		fields.Status = "active"
	}
	p := store.Project{ID: slug, Slug: slug, ProjectFields: fields}
	f.rows = append(f.rows, p)
	return p, nil
}
func (f *projectFake) UpdateProject(_ context.Context, id string, u store.ProjectUpdate) (store.Project, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			if u.Description != nil {
				f.rows[i].Description = *u.Description
			}
			return f.rows[i], nil
		}
	}
	return store.Project{}, store.ErrNotFound
}
func (f *projectFake) ProjectInstructions(_ context.Context, id string) (string, error) {
	return f.instructions[id], nil
}
func TestProjectTools(t *testing.T) {
	ctx := context.Background()
	ex, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &projectFake{instructions: map[string]string{"one": "private instruction", "two": "other instruction"}, rows: []store.Project{{ID: "one", Slug: "one", ProjectFields: store.ProjectFields{Name: "One", Status: "active", Directory: ex.Root() + "/one"}}, {ID: "two", Slug: "two", ProjectFields: store.ProjectFields{Name: "Two", Status: "paused", Directory: ex.Root() + "/two"}}}}
	home := func(context.Context, string) (executor.Executor, func(), error) { return ex, func() {}, nil }
	call := func(name, args string) tool.Result {
		t.Helper()
		result, err := (projectTool{name: name, projects: f, home: home}).Call(ctx, tool.CallContext{}, json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if r := call("project_create", `{"slug":"new","name":"New"}`); r.IsError || !strings.Contains(r.Content, "/home/eika/projects/new") {
		t.Fatal(r)
	}
	if r := call("project_list", `{}`); r.IsError || strings.Contains(r.Content, "Two") {
		t.Fatal(r)
	}
	if r := call("project_open", `{"project":"one"}`); r.IsError || !strings.Contains(r.Content, "private instruction") || strings.Contains(r.Content, "other instruction") {
		t.Fatal(r)
	}
	if err := ex.WriteFile(ctx, "one/STATUS.md", []byte("working")); err != nil {
		t.Fatal(err)
	}
	if r := call("project_open", `{"project":"one"}`); r.IsError || !strings.Contains(r.Content, "working") {
		t.Fatal(r)
	}
	if r := call("project_update", `{"project":"one","instructions":"replace"}`); !r.IsError {
		t.Fatal(r)
	}
	if r := call("project_update", `{"project":"one","description":"changed"}`); r.IsError || !strings.Contains(r.Content, "changed") {
		t.Fatal(r)
	}
	for _, raw := range []string{`{"project":"one","project":"two"}`, `{"project":"\ud800"}`, "{\"project\":\"\xff\"}"} {
		if r := call("project_open", raw); !r.IsError {
			t.Fatalf("accepted %q", raw)
		}
	}
}
