package probetest

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

//go:embed testdata
var files embed.FS

// Fixture returns a fixture by its path under testdata, such as
// "llamacpp/props.json".
func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := files.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// route is one answer: the method and path it serves and the fixture it
// serves them from.
type route struct {
	method, path, fixture string
}

// serve starts a server that answers the routes from fixtures, requires the
// bearer key when one is given, and answers everything else with the 404
// llama.cpp sends.
func serve(t testing.TB, key string, routes []route) *httptest.Server {
	t.Helper()
	type answer struct {
		body []byte
		typ  string
	}
	answers := map[string]answer{}
	for _, r := range routes {
		typ := "application/json"
		if strings.HasSuffix(r.fixture, ".txt") {
			typ = "text/plain; version=0.0.4"
		}
		answers[r.method+" "+r.path] = answer{body: Fixture(t, r.fixture), typ: typ}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			http.Error(w, `{"error":{"message":"Invalid API Key","type":"authentication_error","code":401}}`, http.StatusUnauthorized)
			return
		}
		a, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":{"message":"File Not Found","type":"not_found_error","code":404}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", a.typ)
		_, _ = w.Write(a.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// LlamaCpp serves llama.cpp b11942 as recorded on the bench: two slots
// sharing a 131072-token cache. With a key, every route requires it.
func LlamaCpp(t testing.TB, key string) *httptest.Server {
	return serve(t, key, []route{
		{"GET", "/props", "llamacpp/props.json"},
		{"GET", "/slots", "llamacpp/slots.json"},
		{"GET", "/v1/models", "llamacpp/models.json"},
		{"POST", "/apply-template", "llamacpp/apply_template.json"},
		{"POST", "/tokenize", "llamacpp/tokenize.json"},
	})
}

// VLLM serves vLLM as its documentation describes it (unverified).
func VLLM(t testing.TB, key string) *httptest.Server {
	return serve(t, key, []route{
		{"GET", "/version", "vllm/version.json"},
		{"GET", "/v1/models", "vllm/models.json"},
		{"GET", "/metrics", "vllm/metrics.txt"},
		{"POST", "/tokenize", "vllm/tokenize_chat.json"},
	})
}

// SGLang serves SGLang as its documentation describes it (unverified).
func SGLang(t testing.TB, key string) *httptest.Server {
	return serve(t, key, []route{
		{"GET", "/get_server_info", "sglang/get_server_info.json"},
		{"GET", "/v1/models", "sglang/models.json"},
		{"POST", "/tokenize", "sglang/tokenize.json"},
	})
}

// Tabby serves TabbyAPI as its documentation describes it (unverified).
func Tabby(t testing.TB, key string) *httptest.Server {
	return serve(t, key, []route{
		{"GET", "/v1/model", "tabby/model.json"},
		{"GET", "/v1/models", "tabby/models.json"},
		{"POST", "/v1/token/encode", "tabby/token_encode.json"},
	})
}

// Cloud serves a hosted API that has nothing but a model list (unverified).
func Cloud(t testing.TB, key string) *httptest.Server {
	return serve(t, key, []route{
		{"GET", "/v1/models", "cloud/models.json"},
	})
}
