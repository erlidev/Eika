//go:build docker

package store_test

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

// upgradeFrom0007 opens a database as migration 0007 left it, with one
// provider and one model, after setup.
func upgradeFrom0007(t *testing.T, setup ...string) *store.Store {
	t.Helper()
	st, _ := upgradeFrom(t, "0007_provider_api", append([]string{
		`INSERT INTO providers (id, name, kind, base_url) VALUES ('p', 'bench', 'openai', 'http://m.invalid/v1')`,
		`INSERT INTO models (id, provider_id, name, model, context_window, max_output) VALUES ('m', 'p', 'small', 'vendor/small', 8192, 512)`,
	}, setup...)...)
	return st
}

// A model that exists before kinds is a chat model, and the model a user
// assigned the session title task becomes the fast class.
func TestUpgradeFrom0007CarriesTheTitleModelToTheFastClass(t *testing.T) {
	ctx := t.Context()
	st := upgradeFrom0007(t, `INSERT INTO settings (key, value) VALUES ('utility_models', '{"session_title": "small"}'), ('default_model', '"small"')`)

	m, err := st.Model(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != store.ModelKindChat {
		t.Errorf("kind after the upgrade = %q, want chat", m.Kind)
	}
	classes, err := st.Setting(ctx, "model_classes")
	if err != nil || string(classes.Value) != `{"fast": "small", "main": "small"}` {
		t.Errorf("model_classes = %s, %v, want the title model as fast and the default as main", classes.Value, err)
	}
	if _, err := st.Setting(ctx, "utility_models"); err == nil {
		t.Error("utility_models is still stored after the upgrade")
	}
	if def, err := st.Setting(ctx, "default_model"); err != nil || string(def.Value) != `"small"` {
		t.Errorf("default_model = %s, %v, want it untouched", def.Value, err)
	}
}

// A populated installation keeps its classes and uses the same default model after upgrade.
func TestUpgradeAssignsMainClass(t *testing.T) {
	cases := []struct{ name, defaults, classes, want string }{
		{"stored default", `('default_model', '"second"')`, `('model_classes', '{"fast":"first"}')`, `{"fast": "first", "main": "second"}`},
		{"first model fallback", `('default_model', '"missing"')`, `('model_classes', '{"fast":"second"}')`, `{"fast": "second", "main": "first"}`},
		{"existing main", `('default_model', '"second"')`, `('model_classes', '{"main":"first","fast":"second"}')`, `{"fast": "second", "main": "first"}`},
		{"empty main", `('default_model', '"second"')`, `('model_classes', '{"main":"","deep":"first"}')`, `{"deep": "first", "main": "second"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := upgradeFrom(t, "0018_project_instruction_version",
				`INSERT INTO providers (id, name, kind, base_url) VALUES ('p', 'bench', 'openai', 'http://m.invalid/v1')`,
				`INSERT INTO models (id, provider_id, name, model, context_window, max_output, created_at) VALUES ('a', 'p', 'first', 'first', 8192, 512, '2020-01-01'), ('b', 'p', 'second', 'second', 8192, 512, '2021-01-01')`,
				`INSERT INTO settings (key, value) VALUES `+tc.defaults+`, `+tc.classes,
				`INSERT INTO sessions (id, title) VALUES ('saved', 'Saved conversation')`,
			)
			setting, err := st.Setting(t.Context(), "model_classes")
			if err != nil || string(setting.Value) != tc.want {
				t.Fatalf("model classes = %s, %v; want %s", setting.Value, err, tc.want)
			}
			if session, err := st.Session(t.Context(), "saved"); err != nil || session.Title != "Saved conversation" {
				t.Fatalf("saved session = %+v, %v", session, err)
			}
		})
	}
}
