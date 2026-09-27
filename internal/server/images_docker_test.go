//go:build docker

package server_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/providertest"
)

// pngOf returns a PNG of w by h pixels.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// letModelReadImages turns image input on for the test model.
func (a *api) letModelReadImages(t *testing.T) {
	t.Helper()
	models := decodeBody[modelsWire](t, request(t, a.Server, "GET", "/api/models", nil), 200)
	updated := decodeBody[modelWire](t, request(t, a.Server, "PATCH", "/api/models/"+models.Models[0].ID,
		map[string]any{"image_input": true}), 200)
	if !updated.ImageInput {
		t.Fatalf("model = %+v, want image input on", updated)
	}
}

// imageMessage is the body of a message that carries images.
func imageMessage(text, mode string, images ...[]byte) map[string]any {
	list := make([]map[string]any, 0, len(images))
	for _, data := range images {
		list = append(list, map[string]any{"data": data})
	}
	body := map[string]any{"text": text, "images": list}
	if mode != "" {
		body["mode"] = mode
	}
	return body
}

// lastUserImages returns the images of the last user message a request sent.
func lastUserImages(req provider.Request) []provider.Image {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			return req.Messages[i].Images
		}
	}
	return nil
}

func TestImagesReachAModelThatReadsThem(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.letModelReadImages(t)
	p := a.script(askStep("c1", "anything else?"), providertest.Text("a grey square"))

	// A 3000 by 2000 screenshot is fitted within 1080p before anything sees it.
	messages := "/api/sessions/" + sess.ID + "/messages"
	decodeBody[runWire](t, request(t, a.Server, "POST", messages, imageMessage("what is this?", "", pngOf(t, 3000, 2000))), 202)
	question := a.waitQuestion(t, sess.ID)

	decodeBody[runWire](t, request(t, a.Server, "POST", messages, imageMessage("", "steer", pngOf(t, 40, 30))), 202)
	state := a.runState(t, sess.ID)
	if len(state.PendingSteering) != 1 || state.PendingSteering[0] != (queuedWire{Images: 1}) {
		t.Errorf("pending steering = %+v, want one message of one image", state.PendingSteering)
	}
	request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer", map[string]any{"answer": "no"})
	if final := a.waitIdle(t, sess.ID); final.Run == nil || final.Run.State != "done" {
		t.Fatalf("run = %+v, want done", final.Run)
	}

	requests := p.Requests()
	first := lastUserImages(requests[0])
	if len(first) != 1 || first[0].Width != 1620 || first[0].Height != 1080 || first[0].MediaType != "image/png" {
		t.Fatalf("first call's images = %+v, want one 1620×1080 PNG", describe(first))
	}
	if steered := lastUserImages(requests[1]); len(steered) != 1 || steered[0].Width != 40 {
		t.Errorf("second call's images = %+v, want the steered 40×30 image", describe(steered))
	}

	// The session keeps the images as the model was sent them, so a replay
	// shows them and a later run sends them again.
	path := decodeBody[pathWire](t, request(t, a.Server, "GET", "/api/sessions/"+sess.ID+"/path", nil), 200)
	stored := path.Entries[0].Message
	if stored.Content != "what is this?" || len(stored.Images) != 1 || !bytes.Equal(stored.Images[0].Data, first[0].Data) {
		t.Errorf("first entry = %q with %+v, want the text and the image sent", stored.Content, describe(stored.Images))
	}
}

func TestImagesAreRefusedForAModelThatDoesNotReadThem(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	p := a.script(askStep("c1", "wait here"), providertest.Text("done"))
	messages := "/api/sessions/" + sess.ID + "/messages"

	rec := request(t, a.Server, "POST", messages, imageMessage("look", "", pngOf(t, 10, 10)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "does not accept images") {
		t.Fatalf("a run with an image = %d %s, want 400 naming image input", rec.Code, rec.Body.String())
	}
	if state := a.runState(t, sess.ID); state.Active || state.Run != nil || p.Calls() != 0 {
		t.Fatalf("a refused message started a run: %+v", state)
	}

	a.postMessage(t, sess.ID, "start", "", 202)
	question := a.waitQuestion(t, sess.ID)
	rec = request(t, a.Server, "POST", messages, imageMessage("and this", "steer", pngOf(t, 10, 10)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "test-model does not accept images") {
		t.Errorf("steering with an image = %d %s, want 400 naming the model", rec.Code, rec.Body.String())
	}
	if state := a.runState(t, sess.ID); len(state.PendingSteering) != 0 {
		t.Errorf("pending steering = %+v, want the refused message left out", state.PendingSteering)
	}
	request(t, a.Server, "POST", "/api/questions/"+question.ID+"/answer", map[string]any{"answer": "ok"})
	a.waitIdle(t, sess.ID)
}

func TestRunRejectsBadImages(t *testing.T) {
	a := newAPI(t)
	sess := a.session(t)
	a.letModelReadImages(t)
	messages := "/api/sessions/" + sess.ID + "/messages"
	small := pngOf(t, 4, 4)
	eleven := make([][]byte, 11)
	for i := range eleven {
		eleven[i] = small
	}
	cases := []struct {
		name     string
		body     map[string]any
		wantText string
	}{
		{"a file that is not an image", imageMessage("hi", "", small, []byte("GIF? no, text")), "image 2: unsupported image format"},
		{"a truncated image", imageMessage("hi", "", small[:len(small)/2]), "image 1:"},
		{"too many images", imageMessage("hi", "", eleven...), "at most 10 images"},
		{"data that is not base64", map[string]any{"text": "hi", "images": []map[string]any{{"data": "%%%"}}}, "decode request body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := request(t, a.Server, "POST", messages, c.body)
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), c.wantText) {
				t.Errorf("status = %d %s, want 400 saying %q", rec.Code, rec.Body.String(), c.wantText)
			}
		})
	}

	huge := `{"text":"` + strings.Repeat("a", 64<<20) + `"}`
	r := httptest.NewRequest("POST", messages, strings.NewReader(huge))
	r.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, r)
	if rec.Code != 413 {
		t.Errorf("a body over the limit = %d, want 413", rec.Code)
	}

	// A message may be images alone.
	p := a.script(providertest.Text("a small grey square"))
	decodeBody[runWire](t, request(t, a.Server, "POST", messages, imageMessage("", "", small)), 202)
	a.waitIdle(t, sess.ID)
	if sent := p.Requests()[0].Messages[0]; sent.Content != "" || len(sent.Images) != 1 {
		t.Errorf("sent %q with %d images, want the image alone", sent.Content, len(sent.Images))
	}
}

func TestModelTestSendsAnImageWhenAskedTo(t *testing.T) {
	a := newAPI(t)
	p := a.script(providertest.Text("ready"))
	decodeBody[struct {
		Reply string `json:"reply"`
	}](t, request(t, a.Server, "POST", "/api/models/test", map[string]any{
		"provider_id": a.testProvider.ID, "model": "vision", "image_input": true,
	}), 200)
	if images := lastUserImages(p.Requests()[0]); len(images) != 1 || images[0].MediaType != "image/png" {
		t.Errorf("the test sent %+v, want one PNG", describe(images))
	}
}

// describe lists images without their data, for failure messages.
func describe(images []provider.Image) []string {
	var out []string
	for _, img := range images {
		out = append(out, fmt.Sprintf("%s %d×%d", img.MediaType, img.Width, img.Height))
	}
	return out
}
