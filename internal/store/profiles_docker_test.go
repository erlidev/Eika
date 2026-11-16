//go:build docker

package store_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

// ptr returns a pointer to v, for the optional profile settings.
func ptr[T any](v T) *T { return &v }

// stated returns a stored count, or -1 for one that is NULL, which no stored
// count is.
func stated(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

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

// newSession creates a session in a computer of its own.
func newSession(t *testing.T, st *store.Store, sess store.Session) store.Session {
	t.Helper()
	cx, err := st.CreateComputer(t.Context(), store.Computer{
		Name: "main", State: "running",
	})
	if err != nil {
		t.Fatalf("create computer: %v", err)
	}
	sess.ComputerID = cx.ID
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
	if p.Sampling != nil {
		t.Errorf("default sampling = %s, want none", p.Sampling)
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
		ModelID:    model.ID,
		ChatPrompt: ptr(""),
		Sampling:   json.RawMessage(`{"top_k":20}`),
	}
	sess := newSession(t, st, store.Session{ProfileID: review.ID, Overrides: overrides})
	if sess.ProfileID != review.ID || !sameSettings(t, sess.Overrides, overrides) {
		t.Errorf("created session = profile %q overrides %+v, want %q %+v", sess.ProfileID, sess.Overrides, review.ID, overrides)
	}
	// The default profile, and overrides that set an empty prompt and turn
	// thinking off: both are values, not the absence of one.
	set := store.ProfileSettings{ChatPrompt: ptr(""), PreserveThinking: ptr(false)}
	if err := st.SetSessionProfile(ctx, sess.ID, ""); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	if err := st.SetSessionOverrides(ctx, sess.ID, set); err != nil {
		t.Fatalf("SetSessionOverrides: %v", err)
	}
	if got, err := st.Session(ctx, sess.ID); err != nil || got.ProfileID != "" || !sameSettings(t, got.Overrides, set) {
		t.Errorf("session = profile %q overrides %+v, %v; want none and %+v", got.ProfileID, got.Overrides, err, set)
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

// A model call is recorded against its own session, run and entry only, keeps
// a count that was not stated as NULL, outlives its model by name, and goes
// with its session.
func TestModelCallsStayWithTheirSession(t *testing.T) {
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
	otherEntry, err := st.AppendEntry(ctx, other.ID, store.Entry{Kind: store.KindUser, Payload: json.RawMessage(`{"role":"user","content":"hi"}`)})
	if err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	first, err := st.RecordModelCall(ctx, store.ModelCall{
		SessionID: sess.ID, RunID: run.ID, ModelID: model.ID, Model: model.Name, PromptTokens: new(120), CachedTokens: new(0),
	})
	if err != nil {
		t.Fatalf("RecordModelCall: %v", err)
	}
	second, err := st.RecordModelCall(ctx, store.ModelCall{SessionID: sess.ID, RunID: run.ID, Model: "m"})
	if err != nil || second.PromptTokens != nil || second.CachedTokens != nil {
		t.Errorf("record of a call that stated no usage = %+v, %v; want its counts NULL, not 0", second, err)
	}
	for name, c := range map[string]store.ModelCall{
		"a run of another session":    {SessionID: sess.ID, RunID: otherRun.ID, Model: "m"},
		"an entry of another session": {SessionID: sess.ID, RunID: run.ID, EntryID: otherEntry.ID, Model: "m"},
		"an unknown session":          {SessionID: "absent", RunID: run.ID, Model: "m"},
	} {
		if _, err := st.RecordModelCall(ctx, c); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("recording %s = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := st.ModelCall(ctx, other.ID, first.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reading a record through another session = %v, want ErrNotFound", err)
	}
	if list, err := st.ModelCalls(ctx, sess.ID); err != nil || len(list) != 2 || list[0].ID != first.ID {
		t.Errorf("ModelCalls = %+v, %v; want both records oldest first", list, err)
	}

	if err := st.DeleteModel(ctx, model.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	if got, err := st.ModelCall(ctx, sess.ID, first.ID); err != nil || got.ModelID != "" || got.Model != "gpt-x" || stated(got.CachedTokens) != 0 {
		t.Errorf("record after deleting its model = %+v, %v; want the name and counts kept", got, err)
	}
	if err := st.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if list, err := st.ModelCalls(ctx, sess.ID); err != nil || len(list) != 0 {
		t.Errorf("records after deleting the session = %+v, %v; want none", list, err)
	}
}
