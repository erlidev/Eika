package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/erlidev/eika/internal/agent"
	"github.com/erlidev/eika/internal/executor/local"
	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/provider/counter"
	"github.com/erlidev/eika/internal/provider/providertest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

// ptr returns a pointer to v, for the optional fields of the options.
func ptr[T any](v T) *T { return &v }

// computer returns an executor over a temporary directory holding files,
// keyed by computer path.
func computer(t *testing.T, files map[string]string) *local.Executor {
	t.Helper()
	e, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	for path, content := range files {
		if err := e.WriteFile(context.Background(), path, []byte(content)); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return e
}

// registry returns the built-in tools plus extra.
func registry(t *testing.T, extra ...tool.Tool) *tool.Registry {
	t.Helper()
	r, err := builtin.Registry(builtin.Deps{})
	if err != nil {
		t.Fatalf("builtin.Registry: %v", err)
	}
	for _, x := range extra {
		if err := r.Register(x); err != nil {
			t.Fatalf("register %s: %v", x.Name(), err)
		}
	}
	return r
}

// jsonOf renders v for a failure message.
func jsonOf(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

// screenshot is an image message of the size the harness sends, with data far
// larger than the tokens a model reads it as.
func screenshot(text string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Content: text, Images: []provider.Image{
		{MediaType: "image/png", Data: make([]byte, 3_000_000), Width: 1920, Height: 1080},
	}}
}

func TestImagesAreSentOnlyToAModelThatAcceptsThem(t *testing.T) {
	cases := []struct {
		name        string
		imageInput  bool
		wantImages  int
		wantContent string
	}{
		{"a vision model gets the images", true, 1, "what is wrong here?"},
		{
			"any other model gets a note in their place", false, 0,
			"what is wrong here?\n\n[1 image omitted: the model in use does not accept images]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := providertest.New(providertest.Text("ok"))
			store := newMemoryStore()
			a := agent.New(p, nil, agent.Options{
				ImageInput: c.imageInput,
				Store:      store,
				Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			s := agent.NewSession("s1", "")
			if err := a.Run(context.Background(), s, screenshot("what is wrong here?")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			sent := p.Requests()[0].Messages[0]
			if len(sent.Images) != c.wantImages || sent.Content != c.wantContent {
				t.Errorf("sent %q with %d images, want %q with %d", sent.Content, len(sent.Images), c.wantContent, c.wantImages)
			}
			// What is stored is what the user sent, whatever this model
			// reads: a later run on a vision model sees the picture.
			if kept := store.Messages("s1")[0]; len(kept.Images) != 1 || kept.Content != "what is wrong here?" {
				t.Errorf("stored %q with %d images, want the message as sent", kept.Content, len(kept.Images))
			}
		})
	}
}

func TestImagesAreEstimatedByTheirSizeNotTheirData(t *testing.T) {
	a := agent.New(providertest.New(), nil, agent.Options{
		ImageInput: true,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	s := agent.NewSession("s1", "")
	s.Conversation.Append(screenshot("look"))
	preview, err := a.Preview(s)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	image := counter.EstimatedImageTokens(1920, 1080)
	if got := preview.MessageSizes[0]; got < image || got > image+50 {
		t.Errorf("an image message is estimated at %d tokens, want about %d for the image", got, image)
	}
}

// The memory section is listed apart, and the system prompt is the same bytes
// either way.
func TestPreambleSectionsSplitTheMemorySectionOff(t *testing.T) {
	mem := agent.MemorySnapshot{Revision: "r", Text: "## Memory\n- a: b\n"}
	preamble := "Time zone\nUTC\n\n" + mem.Text
	got := agent.PreambleSections(preamble, mem)
	if len(got) != 2 || got[0].Kind != agent.SectionPreamble || got[1].Kind != agent.SectionMemory || got[1].Text != mem.Text {
		t.Fatalf("sections = %+v", got)
	}
	if joined := got[0].Text + "\n\n" + got[1].Text; joined != preamble {
		t.Fatalf("joined = %q, want %q", joined, preamble)
	}
	if only := agent.PreambleSections(mem.Text, mem); len(only) != 1 || only[0].Kind != agent.SectionMemory {
		t.Fatalf("memory alone = %+v", only)
	}
	if none := agent.PreambleSections("plain", agent.MemorySnapshot{}); len(none) != 1 || none[0].Kind != agent.SectionPreamble {
		t.Fatalf("no memory = %+v", none)
	}
}
