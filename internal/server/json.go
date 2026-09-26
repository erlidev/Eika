package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/erlidev/eika/internal/mcp"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/tool/builtin"
	"github.com/erlidev/eika/internal/workspace"
	"github.com/erlidev/eika/internal/workspace/hub"
)

// maxRequestBytes bounds a JSON request body. Everything the API accepts as
// JSON is small; a file saved from the editor is a raw body with a bound of
// its own, maxFileBytes.
const maxRequestBytes = 1 << 20

// writeJSON writes v as a JSON response with the given status code. An
// encoding failure is logged rather than returned because the status line has
// already been sent.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error("write json response", "error", err)
	}
}

// decodeJSON reads a JSON request body. An unknown field is a client mistake,
// not something to ignore: it usually means the client and this version of
// the API disagree.
func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxRequestBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, invalidf("decode request body: %v", err)
	}
	return v, nil
}

// The error codes the API reports. One code per kind of failure, so a client
// can act on them without parsing messages.
const (
	codeInvalidRequest = "invalid_request"
	codeUnauthorized   = "unauthorized"
	codeForbidden      = "forbidden"
	codeNotFound       = "not_found"
	codeConflict       = "conflict"
	codeTooLarge       = "too_large"
	codeInternal       = "internal"
)

// errorBody is the only error shape the API returns.
type errorBody struct {
	Error errorDetail `json:"error"`
}

// errorDetail names what went wrong: a stable code and a message for a human.
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// apiError is a failure whose HTTP status and code a handler chose itself.
// Everything else is mapped from the sentinel errors the other packages
// return.
type apiError struct {
	status  int
	code    string
	message string
}

// Error returns the message the client sees.
func (e apiError) Error() string { return e.message }

// invalidf reports a request the API will not act on.
func invalidf(format string, args ...any) error {
	return apiError{status: http.StatusBadRequest, code: codeInvalidRequest, message: fmt.Sprintf(format, args...)}
}

// forbiddenf reports a request for something the API will not reach, such as
// a path outside the workspace.
func forbiddenf(format string, args ...any) error {
	return apiError{status: http.StatusForbidden, code: codeForbidden, message: fmt.Sprintf(format, args...)}
}

// tooLargef reports a request body over the route's bound.
func tooLargef(format string, args ...any) error {
	return apiError{status: http.StatusRequestEntityTooLarge, code: codeTooLarge, message: fmt.Sprintf(format, args...)}
}

// notFoundf reports that the addressed thing does not exist.
func notFoundf(format string, args ...any) error {
	return apiError{status: http.StatusNotFound, code: codeNotFound, message: fmt.Sprintf(format, args...)}
}

// conflictf reports a request that collides with the current state, such as a
// second run on a session that is already running one.
func conflictf(format string, args ...any) error {
	return apiError{status: http.StatusConflict, code: codeConflict, message: fmt.Sprintf(format, args...)}
}

// fail writes err as the API's error body, mapping the sentinel errors of the
// packages the handlers call onto status codes. An unmapped error is the
// harness's own failure: it is logged in full and reported as internal, so
// that a database message never reaches a client.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, code := statusOf(err)
	message := err.Error()
	if status == http.StatusInternalServerError {
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		message = "internal error"
	} else {
		s.log.Info("request rejected", "method", r.Method, "path", r.URL.Path,
			"status", status, "error", err)
	}
	writeJSON(w, s.log, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// statusOf maps an error onto the status and code the API reports for it.
func statusOf(err error) (int, string) {
	var api apiError
	switch {
	case errors.As(err, &api):
		return api.status, api.code
	case errors.Is(err, store.ErrNotFound), errors.Is(err, workspace.ErrNoWorkspace),
		errors.Is(err, hub.ErrNoProject), errors.Is(err, builtin.ErrNoQuestion),
		errors.Is(err, mcp.ErrNoElicitation), errors.Is(err, mcp.ErrNoAuthorization):
		return http.StatusNotFound, codeNotFound
	case errors.Is(err, store.ErrConflict), errors.Is(err, mcp.ErrDisabled):
		return http.StatusConflict, codeConflict
	case errors.Is(err, hub.ErrBadProject), errors.Is(err, workspace.ErrBadBranch),
		errors.Is(err, builtin.ErrBadAnswer), errors.Is(err, mcp.ErrBadElicitationAnswer),
		errors.Is(err, mcp.ErrNeedsWorkspace), errors.Is(err, workspace.ErrNoEgressControl):
		return http.StatusBadRequest, codeInvalidRequest
	default:
		return http.StatusInternalServerError, codeInternal
	}
}
