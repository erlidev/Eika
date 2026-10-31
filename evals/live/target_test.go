package live_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/erlidev/eika/evals/live"
)

const testKey = "test-bench-key"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// benchServer serves the descriptions recorded from llama.cpp b11942 behind a
// bearer key, as the bench does.
func benchServer(t *testing.T) *httptest.Server {
	t.Helper()
	props, models := fixture(t, "props.json"), fixture(t, "models.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/props":
			_, _ = w.Write(props)
		case "/v1/models":
			_, _ = w.Write(models)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestConnectWithoutAnEndpointIsNotConfigured(t *testing.T) {
	_, err := live.Connect(context.Background(), live.Env{live.KeyVar: testKey})
	if !errors.Is(err, live.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), live.EndpointVar) {
		t.Errorf("the skip reason %q does not name the variable", err)
	}
}

func TestConnectToAnEndpointThatDoesNotAnswerFailsWithAPointerToTheReadme(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := live.Connect(context.Background(), live.Env{live.EndpointVar: url, live.KeyVar: testKey})
	if !errors.Is(err, live.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if errors.Is(err, live.ErrNotConfigured) {
		t.Error("an endpoint that is set but silent must not read as unconfigured, which skips")
	}
	if !strings.Contains(err.Error(), "evals/models/README.md") {
		t.Errorf("error %q does not point to the README", err)
	}
}

func TestConnectDescribesTheServer(t *testing.T) {
	srv := benchServer(t)
	// The endpoint may be given with the /v1 the provider adds.
	for _, endpoint := range []string{srv.URL, srv.URL + "/", srv.URL + "/v1"} {
		target, err := live.Connect(context.Background(), live.Env{live.EndpointVar: endpoint, live.KeyVar: testKey})
		if err != nil {
			t.Fatalf("endpoint %q: %v", endpoint, err)
		}
		want := live.ServerInfo{
			Build:         "b11942-338fce651",
			Model:         "qwen3.8-27b",
			ModelFile:     "Qwen3.8-27B-UD-Q4_K_XL.gguf",
			ModelRevision: "4ca720788d1e01f1bff70c033e0d0028fd02e502",
			Quantisation:  "Q4_K - Medium",
			Parameters:    27320697856,
			SizeBytes:     17548181504,
			Flags: live.Flags{
				Slots: 2, Context: 131072, TrainContext: 262144,
				Vision: true, SlotsEndpoint: true, MetricsEndpoint: true,
			},
		}
		if target.Server != want {
			t.Errorf("endpoint %q: server = %+v, want %+v", endpoint, target.Server, want)
		}
		if target.Model != "qwen3.8-27b" || target.Provider == nil {
			t.Errorf("endpoint %q: model %q, provider %v", endpoint, target.Model, target.Provider)
		}
	}
}

func TestConnectWithTheWrongKeyNamesTheKey(t *testing.T) {
	srv := benchServer(t)
	_, err := live.Connect(context.Background(), live.Env{live.EndpointVar: srv.URL, live.KeyVar: "wrong"})
	if err == nil {
		t.Fatal("a refused key connected")
	}
	if errors.Is(err, live.ErrUnreachable) || !strings.Contains(err.Error(), live.KeyVar) {
		t.Errorf("err = %v, want a refusal that names %s and is not 'unreachable'", err, live.KeyVar)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error %q repeats the key", err)
	}
}

func TestConnectRejectsAnEndpointThatIsNotAURL(t *testing.T) {
	_, err := live.Connect(context.Background(), live.Env{live.EndpointVar: "127.0.0.1:8091"})
	if err == nil || errors.Is(err, live.ErrNotConfigured) || errors.Is(err, live.ErrUnreachable) {
		t.Fatalf("err = %v, want a plain refusal of the value", err)
	}
}
