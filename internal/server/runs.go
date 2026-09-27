package server

import (
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/imaging"
	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// The modes a message can be posted in. A run starts a turn; steering and
// follow-up messages join the run that is already going.
const (
	modeRun      = "run"
	modeSteer    = "steer"
	modeFollowUp = "follow_up"
)

// maxMessageImages bounds how many images one message carries.
const maxMessageImages = 10

// maxMessageBytes bounds the body of a posted message, which carries its
// images base64 encoded: two images as large as imaging reads, or many
// ordinary ones.
const maxMessageBytes = 64 << 20

// handlePostMessage delivers a message to a session: starting a run, steering
// the run that is going, or queueing a follow-up for after it.
func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONWithin[messageRequest](r, maxMessageBytes)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if strings.TrimSpace(req.Text) == "" && len(req.Images) == 0 {
		s.fail(w, r, invalidf("text is required"))
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = modeRun
	}
	if mode != modeRun && mode != modeSteer && mode != modeFollowUp {
		s.fail(w, r, invalidf("mode must be %q, %q, or %q", modeRun, modeSteer, modeFollowUp))
		return
	}
	images, err := prepareImages(req.Images)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	text := req.Text
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	message := provider.Message{Role: provider.RoleUser, Content: text, Images: images}
	id := r.PathValue("id")
	var run store.Run
	if mode == modeRun {
		run, err = s.runs.start(r.Context(), id, message, req.Model)
	} else {
		run, err = s.runs.enqueue(r.Context(), id, message, mode)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusAccepted, asRun(run))
}

// prepareImages fits the images of a posted message for a model. A file that
// is not an image the harness reads is the client's mistake, and says which
// one it was.
func prepareImages(in []messageImage) ([]provider.Image, error) {
	if len(in) > maxMessageImages {
		return nil, invalidf("a message carries at most %d images, not %d", maxMessageImages, len(in))
	}
	var out []provider.Image
	for i, img := range in {
		prepared, err := imaging.Prepare(img.Data)
		if err != nil {
			return nil, invalidf("image %d: %v", i+1, err)
		}
		out = append(out, prepared)
	}
	return out, nil
}

// noImageInput refuses images for a model that does not read them.
func noImageInput(model string) error {
	return invalidf("model %s does not accept images; turn on Image input for it under Settings, Models, "+
		"or choose a model that has it", model)
}

// handleSessionRun reports what a session is doing and what is queued for it.
func (s *Server) handleSessionRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.Session(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	body := runStateResponse{
		SessionID:        id,
		PendingSteering:  []queuedMessage{},
		PendingFollowUps: []queuedMessage{},
		Questions:        []builtin.Question{},
		Elicitations:     []mcp.Elicitation{},
	}
	if s.deps.Questions != nil {
		for _, q := range s.deps.Questions.Pending() {
			if q.SessionID == id {
				body.Questions = append(body.Questions, q)
			}
		}
	}
	if s.deps.MCP != nil {
		for _, e := range s.deps.MCP.Elicitations().Pending() {
			if e.SessionID == id {
				body.Elicitations = append(body.Elicitations, e)
			}
		}
	}
	// A run that has only just been claimed has no row and no queues yet, so
	// it reports as active with nothing in it rather than as no run at all.
	if active := s.runs.active(id); active != nil {
		body.Active = true
		if loop := active.loop(); loop != nil {
			body.PendingSteering = append(body.PendingSteering, asQueued(loop.PendingSteering())...)
			body.PendingFollowUps = append(body.PendingFollowUps, asQueued(loop.PendingFollowUps())...)
		}
		if runID := active.runID(); runID != "" {
			run, err := s.deps.Store.Run(r.Context(), runID)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			reply := asRun(run)
			body.Run = &reply
		}
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	queued := s.runs.pendingFor(id)
	body.PendingSteering = append(body.PendingSteering, asQueued(queued.steering)...)
	body.PendingFollowUps = append(body.PendingFollowUps, asQueued(queued.followUps)...)
	previous, err := s.deps.Store.Runs(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(previous) > 0 {
		reply := asRun(previous[len(previous)-1])
		body.Run = &reply
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleAbortRun stops a run and reports how it ended.
func (s *Server) handleAbortRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.abort(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, asRun(run))
}
