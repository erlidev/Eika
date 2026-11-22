package screen

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type responseTransport string

func (r responseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(r))), Header: make(http.Header)}, nil
}

func TestScreenRejectsIncompleteWindow(t *testing.T) {
	for _, body := range []string{
		`{"results":[{"windows":[{"end":7,"score":0}]}]}`,
		`{"results":[{"windows":[{"start":0,"score":0}]}]}`,
		`{"results":[{"windows":[{"start":0,"end":7}]}]}`,
	} {
		client := New("http://guard")
		client.http = &http.Client{Transport: responseTransport(body)}
		if _, err := client.Screen(context.Background(), []string{"content"}); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}
