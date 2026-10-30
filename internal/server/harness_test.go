package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/erlidev/eika/internal/config"
	"github.com/erlidev/eika/internal/server"
	"github.com/erlidev/eika/internal/server/servertest"
)

// testToken is the bearer token every test server is configured with.
const testToken = "test-token"

// testConfig returns a configuration with the test API token.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.AuthToken = testToken
	return cfg
}

// requestWith sends one API request with the given Authorization header,
// which may be empty.
func requestWith(t *testing.T, s *server.Server, method, path string, body any, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, reader)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)
	checkContract(t, r, rec)
	return rec
}

// apiContract is docs/api/contract.json, read once.
var apiContract = sync.OnceValues(func() (servertest.Contract, error) {
	return servertest.Load("../../docs/api/contract.json")
})

// checkContract fails the test when a response breaks the API's wire
// contract. Every request helper calls it, so the handler tests also check
// that each route answers with the types TestContractFile wrote down.
func checkContract(t *testing.T, r *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	c, err := apiContract()
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	for _, problem := range c.CheckResponse(r.Method, r.URL.Path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes()) {
		t.Errorf("breaks the API contract: %s", problem)
	}
}

// errorOf decodes the API's error body.
func errorOf(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code, body.Error.Message
}
