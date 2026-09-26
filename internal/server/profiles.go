package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
)

// Bounds on what a profile and a session's overrides accept.
const (
	maxProfileName        = 64
	maxProfileDescription = 500
	// maxPromptBytes bounds a base prompt and the extra instructions. A
	// prompt this long already costs a large share of most context windows.
	maxPromptBytes = 64 << 10
)

// profileSettingsBody is what a profile sets, and what a session overrides
// of its profile, on the wire. A null field, an empty model_id, and a
// sampling parameter left out are not set, and fall through to the layer
// below.
type profileSettingsBody struct {
	ModelID string `json:"model_id,omitempty"`
	// WorkspacePrompt and ChatPrompt replace the built-in base prompts of a
	// workspace session and of a chat, the empty string included.
	WorkspacePrompt *string `json:"workspace_prompt"`
	ChatPrompt      *string `json:"chat_prompt"`
	Instructions    *string `json:"instructions"`
	ContextFiles    *bool   `json:"context_files"`
	// PreserveThinking says whether earlier reasoning is replayed to the
	// model, over the model's own switch.
	PreserveThinking *bool `json:"preserve_thinking"`
	// Sampling holds the sampling parameters set here.
	Sampling provider.Sampling `json:"sampling"`
}

// profileBody is one profile on the wire.
type profileBody struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	profileSettingsBody
	// Tools is the profile's tool choice, where mcp__<server>__* is every
	// tool of that server; null is every tool.
	Tools []string `json:"tools"`
	// Inherited is what the profile's unset values fall through to: the
	// model row of the model it resolves to, and the defaults.
	Inherited configurationBody `json:"inherited"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// builtinPrompts are the base prompts a profile that sets none uses, which
// the editor shows, compares an override with, and resets to.
type builtinPrompts struct {
	Workspace string `json:"workspace"`
	Chat      string `json:"chat"`
}

// profilesResponse is the body of GET /api/profiles.
type profilesResponse struct {
	Profiles []profileBody `json:"profiles"`
	// Default is the id of the profile a session that chose none runs with.
	Default string         `json:"default"`
	Prompts builtinPrompts `json:"prompts"`
}

// profileRequest is the body of POST /api/profiles and PUT
// /api/profiles/{id}: the whole profile, which replaces what is stored.
type profileRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	profileSettingsBody
	// Tools is the tool choice; null is every tool and an empty list none.
	Tools []string `json:"tools"`
}

// setSessionProfileRequest is the body of PUT /api/sessions/{id}/profile.
type setSessionProfileRequest struct {
	// ProfileID is the profile the session runs with; empty is whichever
	// profile is the default.
	ProfileID string `json:"profile_id"`
}

// sessionConfigurationResponse is the body of GET
// /api/sessions/{id}/configuration and of the PUTs of a session's profile
// and overrides: what the session sets itself and what its next run
// resolves to.
type sessionConfigurationResponse struct {
	SessionID string `json:"session_id"`
	// ProfileID is the profile the session chose, absent for the default.
	ProfileID string `json:"profile_id,omitempty"`
	// Overrides are the profile settings the session sets itself.
	Overrides profileSettingsBody `json:"overrides"`
	// Tools is the session's own tool choice, null when it has made none.
	Tools []string `json:"tools"`
	// Resolved is the configuration the session's next run uses when the
	// request names no model.
	Resolved configurationBody `json:"resolved"`
	// Inherited is what the session's overrides fall through to: the same
	// configuration as if the session set nothing itself.
	Inherited configurationBody `json:"inherited"`
}

// handleListProfiles lists every profile, the default one, and the
// built-in prompts.
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	base, err := s.loadConfigBase(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := profilesResponse{
		Profiles: make([]profileBody, 0, len(base.profiles)),
		Default:  base.defaultProfile.ID,
		Prompts:  builtinPrompts{Workspace: agent.WorkspacePrompt, Chat: agent.ChatPrompt},
	}
	for _, p := range base.profiles {
		body, err := asProfile(p, base)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out.Profiles = append(out.Profiles, body)
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleCreateProfile adds a profile.
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[profileRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.checkProfile(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	created, err := s.deps.Store.CreateProfile(r.Context(), p)
	if err != nil {
		s.fail(w, r, profileFailure(p, err))
		return
	}
	s.log.Info("profile created", "profile_id", created.ID, "name", created.Name)
	s.writeProfile(w, r, http.StatusCreated, created)
}

// handleUpdateProfile replaces a profile with the one the body describes.
// Runs already going keep the configuration they started with.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[profileRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.checkProfile(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p.ID = r.PathValue("id")
	updated, err := s.deps.Store.UpdateProfile(r.Context(), p)
	if err != nil {
		s.fail(w, r, profileFailure(p, err))
		return
	}
	s.log.Info("profile updated", "profile_id", updated.ID, "name", updated.Name)
	s.writeProfile(w, r, http.StatusOK, updated)
}

// handleDeleteProfile removes a profile. The sessions that chose it run
// with the default profile, and when it was the default, the first
// profile is. The last profile cannot be deleted.
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deps.Store.DeleteProfile(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrConflict) {
			err = conflictf("profile %s is the last one, and a run always needs a profile", id)
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("profile deleted", "profile_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// handleSessionConfiguration reports what a session sets itself and what its
// next run resolves to. A profile_id or model_id in the query is what the
// session's editor has chosen and not saved yet: inherited is then what the
// overrides would fall through to with that profile or model, so the editor
// shows their values as they are chosen. Empty is the default profile, or
// no model of the session's own.
func (s *Server) handleSessionConfiguration(w http.ResponseWriter, r *http.Request) {
	sess, err := s.deps.Store.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var draft editorDraft
	q := r.URL.Query()
	if q.Has("profile_id") {
		draft.profileID = new(q.Get("profile_id"))
	}
	if q.Has("model_id") {
		draft.modelID = new(q.Get("model_id"))
	}
	s.writeSessionConfiguration(w, r, sess, draft)
}

// editorDraft is what a session's editor has chosen and not saved: a
// profile and a model, each nil when the editor keeps the saved one.
type editorDraft struct {
	profileID *string
	modelID   *string
}

// apply returns sess as the draft would leave it, refusing a profile or a
// model that does not exist.
func (d editorDraft) apply(b configBase, sess store.Session) (store.Session, error) {
	if d.profileID != nil {
		if *d.profileID != "" && !slices.ContainsFunc(b.profiles, func(p store.Profile) bool { return p.ID == *d.profileID }) {
			return store.Session{}, invalidf("profile %s does not exist", *d.profileID)
		}
		sess.ProfileID = *d.profileID
	}
	if d.modelID != nil {
		if _, ok := b.model(*d.modelID); *d.modelID != "" && !ok {
			return store.Session{}, invalidf("model %s does not exist", *d.modelID)
		}
		sess.Overrides.ModelID = *d.modelID
	}
	return sess, nil
}

// handleInheritedProfile reports what a profile that chooses the model
// model_id (none when it is absent or empty) falls through to: the model's
// own values and the defaults. A profile is the only layer it describes, so
// nothing else about the profile changes the answer, and a profile editor
// asks with the model it has chosen before saving it.
func (s *Server) handleInheritedProfile(w http.ResponseWriter, r *http.Request) {
	base, err := s.loadConfigBase(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	modelID := r.URL.Query().Get("model_id")
	if _, ok := base.model(modelID); modelID != "" && !ok {
		s.fail(w, r, invalidf("model %s does not exist", modelID))
		return
	}
	layers := []configLayer{{name: layerProfile, settings: store.ProfileSettings{ModelID: modelID}}}
	inherited, err := base.inherited(base.defaultProfile, layerDefault, layers, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asConfiguration(inherited))
}

// handleSetSessionProfile chooses the profile a session's next run starts
// from. The session's overrides stay: they are its own.
func (s *Server) handleSetSessionProfile(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[setSessionProfileRequest](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	if err := s.deps.Store.SetSessionProfile(r.Context(), id, req.ProfileID); err != nil {
		if errors.Is(err, store.ErrNotFound) && req.ProfileID != "" {
			if _, missing := s.deps.Store.Session(r.Context(), id); missing == nil {
				err = invalidf("profile %s does not exist", req.ProfileID)
			}
		}
		s.fail(w, r, err)
		return
	}
	s.log.Info("session profile set", "session_id", id, "profile_id", req.ProfileID)
	s.writeSessionConfigurationOf(w, r, id)
}

// handleSetSessionOverrides replaces what a session sets over its profile.
// Settings that set nothing clear them.
func (s *Server) handleSetSessionOverrides(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[profileSettingsBody](r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	overrides, err := s.checkSettings(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("id")
	if err := s.deps.Store.SetSessionOverrides(r.Context(), id, overrides); err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("session overrides set", "session_id", id)
	s.writeSessionConfigurationOf(w, r, id)
}

// writeSessionConfigurationOf reads a session and answers with its
// configuration.
func (s *Server) writeSessionConfigurationOf(w http.ResponseWriter, r *http.Request, id string) {
	sess, err := s.deps.Store.Session(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSessionConfiguration(w, r, sess, editorDraft{})
}

// writeSessionConfiguration answers with what a session sets itself, what
// that falls through to, and what its next run resolves to. What falls
// through is with the draft's profile and model in place of the saved ones.
func (s *Server) writeSessionConfiguration(w http.ResponseWriter, r *http.Request, sess store.Session, draft editorDraft) {
	base, err := s.loadConfigBase(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	layers, profile, source, err := base.sessionLayers(sess, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	resolved, err := base.resolve(profile, source, layers)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	drafted, err := draft.apply(base, sess)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	layers, profile, source, err = base.sessionLayers(drafted, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// With no model in the request, the session is the top layer.
	inherited, err := base.inherited(profile, source, layers, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	overrides, err := asSettings(sess.Overrides)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, sessionConfigurationResponse{
		SessionID: sess.ID,
		ProfileID: sess.ProfileID,
		Overrides: overrides,
		Tools:     sess.Tools,
		Resolved:  asConfiguration(resolved),
		Inherited: asConfiguration(inherited),
	})
}

// writeProfile answers with one profile.
func (s *Server) writeProfile(w http.ResponseWriter, r *http.Request, status int, p store.Profile) {
	base, err := s.loadConfigBase(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	body, err := asProfile(p, base)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, status, body)
}

// checkProfile validates a profile request and returns the profile it
// describes.
func (s *Server) checkProfile(ctx context.Context, req profileRequest) (store.Profile, error) {
	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	switch {
	case name == "" || len(name) > maxProfileName || strings.IndexFunc(name, unicode.IsControl) >= 0:
		return store.Profile{}, invalidf("name must be 1 to %d characters", maxProfileName)
	case len(description) > maxProfileDescription:
		return store.Profile{}, invalidf("description must be at most %d characters", maxProfileDescription)
	}
	settings, err := s.checkSettings(ctx, req.profileSettingsBody)
	if err != nil {
		return store.Profile{}, err
	}
	p := store.Profile{Name: name, Description: description, ProfileSettings: settings}
	if req.Tools != nil {
		// A profile serves chats and workspace sessions alike, so it may
		// choose a tool that needs a workspace; a chat's runs leave it out.
		if p.Tools, err = s.checkToolChoice(ctx, req.Tools, false); err != nil {
			return store.Profile{}, err
		}
	}
	return p, nil
}

// checkSettings validates what a profile sets, or a session overrides, and
// returns it in the form the store keeps.
func (s *Server) checkSettings(ctx context.Context, b profileSettingsBody) (store.ProfileSettings, error) {
	for field, text := range map[string]*string{
		"workspace_prompt": b.WorkspacePrompt,
		"chat_prompt":      b.ChatPrompt,
		"instructions":     b.Instructions,
	} {
		if text != nil && len(*text) > maxPromptBytes {
			return store.ProfileSettings{}, invalidf("%s must be at most %d bytes", field, maxPromptBytes)
		}
	}
	if err := b.Sampling.Validate(); err != nil {
		return store.ProfileSettings{}, invalidf("%v", err)
	}
	if b.ModelID != "" {
		if _, err := s.deps.Store.Model(ctx, b.ModelID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.ProfileSettings{}, invalidf("model %s does not exist", b.ModelID)
			}
			return store.ProfileSettings{}, err
		}
	}
	sampling, err := json.Marshal(b.Sampling)
	if err != nil {
		return store.ProfileSettings{}, err
	}
	return store.ProfileSettings{
		ModelID:          b.ModelID,
		WorkspacePrompt:  b.WorkspacePrompt,
		ChatPrompt:       b.ChatPrompt,
		Instructions:     b.Instructions,
		ContextFiles:     b.ContextFiles,
		PreserveThinking: b.PreserveThinking,
		Sampling:         sampling,
	}, nil
}

// profileFailure words what the store refused of a profile write.
func profileFailure(p store.Profile, err error) error {
	switch {
	case errors.Is(err, store.ErrConflict):
		return conflictf("a profile named %q already exists", p.Name)
	case errors.Is(err, store.ErrNotFound) && p.ID == "":
		return invalidf("model %s does not exist", p.ModelID)
	}
	return err
}

// asProfile renders a profile on the wire, with what its unset values fall
// through to.
func asProfile(p store.Profile, base configBase) (profileBody, error) {
	settings, err := asSettings(p.ProfileSettings)
	if err != nil {
		return profileBody{}, err
	}
	layers := []configLayer{{name: layerProfile, settings: p.ProfileSettings, tools: p.Tools}}
	inherited, err := base.inherited(p, layerDefault, layers, 0)
	if err != nil {
		return profileBody{}, err
	}
	return profileBody{
		ID:                  p.ID,
		Name:                p.Name,
		Description:         p.Description,
		profileSettingsBody: settings,
		Tools:               p.Tools,
		Inherited:           asConfiguration(inherited),
		CreatedAt:           p.CreatedAt,
		UpdatedAt:           p.UpdatedAt,
	}, nil
}

// asSettings renders stored settings on the wire.
func asSettings(p store.ProfileSettings) (profileSettingsBody, error) {
	body := profileSettingsBody{
		ModelID:          p.ModelID,
		WorkspacePrompt:  p.WorkspacePrompt,
		ChatPrompt:       p.ChatPrompt,
		Instructions:     p.Instructions,
		ContextFiles:     p.ContextFiles,
		PreserveThinking: p.PreserveThinking,
	}
	if len(p.Sampling) > 0 {
		if err := json.Unmarshal(p.Sampling, &body.Sampling); err != nil {
			return profileSettingsBody{}, err
		}
	}
	return body, nil
}
