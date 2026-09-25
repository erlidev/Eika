//go:build docker

package store_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// ptr returns a pointer to v, for the optional profile settings.
func ptr[T any](v T) *T { return &v }

// sameJSON reports whether two documents are equal as JSON, which is all a
// jsonb column promises about what it gives back.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// sameSettings reports whether two profile settings set the same things.
func sameSettings(t *testing.T, a, b store.ProfileSettings) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("encode %+v: %v", a, err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("encode %+v: %v", b, err)
	}
	return sameJSON(t, x, y)
}

// newModel creates a provider and one model on it.
func newModel(t *testing.T, st *store.Store, name string) store.Model {
	t.Helper()
	p, err := st.CreateProvider(t.Context(), store.Provider{Name: "p-" + name, Kind: "openai", BaseURL: "http://model.invalid"})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	m, err := st.CreateModel(t.Context(), store.Model{
		ProviderID: p.ID, Name: name, Model: name, ContextWindow: 8192, MaxOutput: 1024,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	return m
}

// newSession creates a session in a workspace of its own.
func newSession(t *testing.T, st *store.Store, sess store.Session) store.Session {
	t.Helper()
	ws, err := st.CreateWorkspace(t.Context(), store.Workspace{
		ProjectID: newProject(t, st).ID, Name: "main", Branch: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	sess.WorkspaceID = ws.ID
	out, err := st.CreateSession(t.Context(), sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return out
}

func TestTheMigrationSeedsADefaultProfileThatSetsNothing(t *testing.T) {
	st := storetest.Open(t)
	profiles, err := st.Profiles(t.Context())
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v, want the one the migration made", profiles)
	}
	p := profiles[0]
	if p.Name != "Default" || p.Description != "" || p.Tools != nil || !sameSettings(t, p.ProfileSettings, store.ProfileSettings{}) {
		t.Errorf("default profile = %+v, want Default setting nothing", p)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	if len(p.ID) != 20 || strings.Trim(p.ID, alphabet) != "" {
		t.Errorf("default profile id = %q, want the form store.NewID gives", p.ID)
	}
	if p.Sampling != nil {
		t.Errorf("default sampling = %s, want none", p.Sampling)
	}
}

func TestProfileSettingsRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	model := newModel(t, st, "gpt-x")
	cases := []struct {
		name  string
		given store.Profile
	}{
		{"nothing set", store.Profile{}},
		{
			"every field set",
			store.Profile{
				Description: "for reviews",
				ProfileSettings: store.ProfileSettings{
					ModelID:         model.ID,
					WorkspacePrompt: ptr("Review carefully."),
					ChatPrompt:      ptr("Chat briefly."),
					Instructions:    ptr("Cite files."),
					ContextFiles:    ptr(true),
					Sampling:        json.RawMessage(`{"temperature":0.2,"stop":["END"],"seed":7}`),
				},
				Tools: []string{"bash", "mcp__docs__*"},
			},
		},
		{
			"empty prompts, no context files, and no tools are values",
			store.Profile{
				ProfileSettings: store.ProfileSettings{
					WorkspacePrompt: ptr(""),
					ChatPrompt:      ptr(""),
					Instructions:    ptr(""),
					ContextFiles:    ptr(false),
				},
				Tools: []string{},
			},
		},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			given := c.given
			given.Name = "profile-" + string(rune('a'+i))
			created, err := st.CreateProfile(t.Context(), given)
			if err != nil {
				t.Fatalf("CreateProfile: %v", err)
			}
			read, err := st.Profile(t.Context(), created.ID)
			if err != nil {
				t.Fatalf("Profile: %v", err)
			}
			for name, got := range map[string]store.Profile{"created": created, "read": read} {
				if got.Name != given.Name || got.Description != given.Description ||
					!sameSettings(t, got.ProfileSettings, given.ProfileSettings) {
					t.Errorf("%s = %+v, want %+v", name, got, given)
				}
				if !reflect.DeepEqual(got.Tools, given.Tools) {
					t.Errorf("%s tools = %#v, want %#v", name, got.Tools, given.Tools)
				}
				if got.CreatedAt.IsZero() || got.CreatedAt.Location().String() != "UTC" {
					t.Errorf("%s created_at = %v, want a UTC time", name, got.CreatedAt)
				}
			}
		})
	}
}

func TestProfilesCanBeListedChangedAndDeleted(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	seeded, err := st.Profiles(ctx)
	if err != nil || len(seeded) != 1 {
		t.Fatalf("Profiles = %+v, %v", seeded, err)
	}

	review, err := st.CreateProfile(ctx, store.Profile{Name: "Review"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if _, err := st.CreateProfile(ctx, store.Profile{Name: "Review"}); !errors.Is(err, store.ErrConflict) {
		t.Errorf("a second profile named Review = %v, want ErrConflict", err)
	}
	if _, err := st.CreateProfile(ctx, store.Profile{Name: "Orphan", ProfileSettings: store.ProfileSettings{ModelID: "absent"}}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a profile of an unknown model = %v, want ErrNotFound", err)
	}
	if list, err := st.Profiles(ctx); err != nil || len(list) != 2 || list[0].ID != seeded[0].ID || list[1].ID != review.ID {
		t.Errorf("Profiles = %+v, %v; want Default and then Review", list, err)
	}

	review.Name, review.Instructions, review.Tools = "Careful review", ptr("Read twice."), []string{"bash"}
	updated, err := st.UpdateProfile(ctx, review)
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.Name != "Careful review" || updated.Instructions == nil || *updated.Instructions != "Read twice." ||
		len(updated.Tools) != 1 || !updated.UpdatedAt.After(review.UpdatedAt) {
		t.Errorf("updated profile = %+v", updated)
	}
	review.Name = "Default"
	if _, err := st.UpdateProfile(ctx, review); !errors.Is(err, store.ErrConflict) {
		t.Errorf("renaming to a name in use = %v, want ErrConflict", err)
	}
	if _, err := st.UpdateProfile(ctx, store.Profile{ID: "absent", Name: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdateProfile of an unknown profile = %v, want ErrNotFound", err)
	}

	if err := st.DeleteProfile(ctx, seeded[0].ID); err != nil {
		t.Fatalf("DeleteProfile of Default: %v", err)
	}
	if err := st.DeleteProfile(ctx, "absent"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DeleteProfile of an unknown profile = %v, want ErrNotFound", err)
	}
	if err := st.DeleteProfile(ctx, review.ID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("DeleteProfile of the last profile = %v, want ErrConflict", err)
	}
	if list, err := st.Profiles(ctx); err != nil || len(list) != 1 || list[0].ID != review.ID {
		t.Errorf("Profiles = %+v, %v; want the last one kept", list, err)
	}
}

func TestASessionChoosesAProfileAndOverridesIt(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	model := newModel(t, st, "gpt-x")
	review, err := st.CreateProfile(ctx, store.Profile{Name: "Review"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	overrides := store.ProfileSettings{
		ModelID:         model.ID,
		WorkspacePrompt: ptr(""),
		ContextFiles:    ptr(false),
		Sampling:        json.RawMessage(`{"top_k":20}`),
	}
	sess := newSession(t, st, store.Session{ProfileID: review.ID, Overrides: overrides})
	if sess.ProfileID != review.ID || !sameSettings(t, sess.Overrides, overrides) {
		t.Errorf("created session = profile %q overrides %+v, want %q %+v", sess.ProfileID, sess.Overrides, review.ID, overrides)
	}
	listed, err := st.Sessions(ctx, sess.WorkspaceID, true)
	if err != nil || len(listed) != 1 || listed[0].ProfileID != review.ID || !sameSettings(t, listed[0].Overrides, overrides) {
		t.Errorf("Sessions = %+v, %v; want the profile and overrides", listed, err)
	}

	cases := []struct {
		name      string
		profileID string
		overrides store.ProfileSettings
	}{
		{"the default profile and no overrides", "", store.ProfileSettings{}},
		{"a profile and one override", review.ID, store.ProfileSettings{Instructions: ptr("Be brief.")}},
		{"an explicitly empty prompt", review.ID, store.ProfileSettings{ChatPrompt: ptr("")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := st.SetSessionProfile(ctx, sess.ID, c.profileID); err != nil {
				t.Fatalf("SetSessionProfile: %v", err)
			}
			if err := st.SetSessionOverrides(ctx, sess.ID, c.overrides); err != nil {
				t.Fatalf("SetSessionOverrides: %v", err)
			}
			got, err := st.Session(ctx, sess.ID)
			if err != nil {
				t.Fatalf("Session: %v", err)
			}
			if got.ProfileID != c.profileID || !sameSettings(t, got.Overrides, c.overrides) {
				t.Errorf("session = profile %q overrides %+v, want %q %+v", got.ProfileID, got.Overrides, c.profileID, c.overrides)
			}
		})
	}

	if err := st.SetSessionProfile(ctx, sess.ID, "absent"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSessionProfile to an unknown profile = %v, want ErrNotFound", err)
	}
	if err := st.SetSessionProfile(ctx, "absent", review.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSessionProfile of an unknown session = %v, want ErrNotFound", err)
	}
	if err := st.SetSessionOverrides(ctx, "absent", store.ProfileSettings{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetSessionOverrides of an unknown session = %v, want ErrNotFound", err)
	}
	if _, err := st.CreateSession(ctx, store.Session{ProfileID: "absent"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session of an unknown profile = %v, want ErrNotFound", err)
	}
}

func TestDeletingAProfileOrAModelFallsBackToTheDefault(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	model := newModel(t, st, "gpt-x")
	review, err := st.CreateProfile(ctx, store.Profile{Name: "Review", ProfileSettings: store.ProfileSettings{ModelID: model.ID}})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	kept := newSession(t, st, store.Session{
		ProfileID: review.ID,
		Overrides: store.ProfileSettings{ModelID: model.ID, Instructions: ptr("Be brief.")},
	})
	emptied := newSession(t, st, store.Session{Overrides: store.ProfileSettings{ModelID: model.ID}})

	if err := st.DeleteModel(ctx, model.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if got, err := st.Profile(ctx, review.ID); err != nil || got.ModelID != "" {
		t.Errorf("profile after deleting its model = %+v, %v; want no model", got, err)
	}
	if got, err := st.Session(ctx, kept.ID); err != nil ||
		!sameSettings(t, got.Overrides, store.ProfileSettings{Instructions: ptr("Be brief.")}) {
		t.Errorf("overrides after deleting their model = %+v, %v; want the instructions alone", got.Overrides, err)
	}
	if got, err := st.Session(ctx, emptied.ID); err != nil || !sameSettings(t, got.Overrides, store.ProfileSettings{}) {
		t.Errorf("overrides that named only the model = %+v, %v; want none", got.Overrides, err)
	}

	if err := st.DeleteProfile(ctx, review.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if got, err := st.Session(ctx, kept.ID); err != nil || got.ProfileID != "" {
		t.Errorf("session after deleting its profile = %+v, %v; want the default profile", got, err)
	}
}

func TestAForkKeepsItsSourcesProfileAndOverrides(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	review, err := st.CreateProfile(ctx, store.Profile{Name: "Review"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	overrides := store.ProfileSettings{Instructions: ptr("Be brief."), Sampling: json.RawMessage(`{"temperature":0}`)}
	src := newSession(t, st, store.Session{ProfileID: review.ID, Overrides: overrides})
	entry, err := st.AppendEntry(ctx, src.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	fork, err := st.ForkSession(ctx, src.ID, entry.ID, store.ForkOptions{})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if fork.ProfileID != review.ID || !sameSettings(t, fork.Overrides, overrides) {
		t.Errorf("fork = profile %q overrides %+v, want %q %+v", fork.ProfileID, fork.Overrides, review.ID, overrides)
	}
	if got, err := st.Session(ctx, fork.ID); err != nil || got.ProfileID != review.ID || !sameSettings(t, got.Overrides, overrides) {
		t.Errorf("stored fork = %+v, %v", got, err)
	}
}

func TestModelRequestsAreRecordedPerSession(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	model := newModel(t, st, "gpt-x")
	sess := newSession(t, st, store.Session{})
	other := newSession(t, st, store.Session{})
	run, err := st.StartRun(ctx, sess.ID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	otherRun, err := st.StartRun(ctx, other.ID)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	entry, err := st.AppendEntry(ctx, sess.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	otherEntry, err := st.AppendEntry(ctx, other.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	given := store.ModelRequest{
		SessionID: sess.ID, RunID: run.ID, EntryID: entry.ID, ModelID: model.ID, Model: model.Name,
		Sections:      json.RawMessage(`[{"kind":"base","text":"You are Eika.","tokens":4}]`),
		Tools:         json.RawMessage(`[{"name":"bash","description":"run","schema":{"type":"object"},"source":"builtin","tokens":9}]`),
		Parameters:    json.RawMessage(`{"model":"gpt-x","sampling":{"temperature":0.2},"preserve_thinking":false}`),
		MessageTokens: 5, InputTokens: 120, OutputTokens: 30, TotalTokens: 150,
	}
	first, err := st.RecordModelRequest(ctx, given)
	if err != nil {
		t.Fatalf("RecordModelRequest: %v", err)
	}
	second, err := st.RecordModelRequest(ctx, store.ModelRequest{SessionID: sess.ID, RunID: run.ID, ModelID: "absent", Model: "gone"})
	if err != nil {
		t.Fatalf("RecordModelRequest without an entry: %v", err)
	}
	if second.ModelID != "" || second.Model != "gone" || string(second.Sections) != "[]" || string(second.Parameters) != "{}" {
		t.Errorf("record of an unknown model = %+v, want it kept by name with empty documents", second)
	}

	refused := []struct {
		name string
		r    store.ModelRequest
	}{
		{"a run of another session", store.ModelRequest{SessionID: sess.ID, RunID: otherRun.ID, Model: "m"}},
		{"an entry of another session", store.ModelRequest{SessionID: sess.ID, RunID: run.ID, EntryID: otherEntry.ID, Model: "m"}},
		{"an unknown session", store.ModelRequest{SessionID: "absent", RunID: run.ID, Model: "m"}},
	}
	for _, c := range refused {
		if _, err := st.RecordModelRequest(ctx, c.r); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("recording %s = %v, want ErrNotFound", c.name, err)
		}
	}

	got, err := st.ModelRequest(ctx, sess.ID, first.ID)
	if err != nil {
		t.Fatalf("ModelRequest: %v", err)
	}
	if got.RunID != run.ID || got.EntryID != entry.ID || got.ModelID != model.ID || got.Model != "gpt-x" ||
		got.MessageTokens != 5 || got.InputTokens != 120 || got.OutputTokens != 30 || got.TotalTokens != 150 {
		t.Errorf("record = %+v, want what was given", got)
	}
	for name, pair := range map[string][2]json.RawMessage{
		"sections":   {got.Sections, given.Sections},
		"tools":      {got.Tools, given.Tools},
		"parameters": {got.Parameters, given.Parameters},
	} {
		if !sameJSON(t, pair[0], pair[1]) {
			t.Errorf("record %s = %s, want %s", name, pair[0], pair[1])
		}
	}
	if _, err := st.ModelRequest(ctx, other.ID, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reading a record through another session = %v, want ErrNotFound", err)
	}

	list, err := st.ModelRequests(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ModelRequests: %v", err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("ModelRequests = %+v, want both records oldest first", list)
	}
	if list[0].Sections != nil || list[0].Tools != nil || !sameJSON(t, list[0].Parameters, given.Parameters) || list[0].InputTokens != 120 {
		t.Errorf("listed record = %+v, want a summary without sections or tools", list[0])
	}
	if others, err := st.ModelRequests(ctx, other.ID); err != nil || len(others) != 0 {
		t.Errorf("records of another session = %+v, %v; want none", others, err)
	}

	if err := st.DeleteModel(ctx, model.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if got, err := st.ModelRequest(ctx, sess.ID, first.ID); err != nil || got.ModelID != "" || got.Model != "gpt-x" {
		t.Errorf("record after deleting its model = %+v, %v; want the name kept", got, err)
	}
	if err := st.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.ModelRequest(ctx, sess.ID, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a record outlived its session: %v", err)
	}
	if list, err := st.ModelRequests(ctx, sess.ID); err != nil || len(list) != 0 {
		t.Errorf("records after deleting the session = %+v, %v; want none", list, err)
	}
}
