//go:build docker

package server_test

import (
	"net/http/httptest"
	"testing"
)

// refused checks that a request was answered with an error status.
func refused(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("status = %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
}
