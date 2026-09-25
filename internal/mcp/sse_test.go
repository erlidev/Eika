package mcp

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func readEvents(t *testing.T, r io.Reader) ([]sseEvent, error) {
	t.Helper()
	s := newSSEReader(r)
	var out []sseEvent
	for {
		ev, err := s.next()
		if err != nil {
			return out, err
		}
		out = append(out, ev)
	}
}

func TestSSEReader(t *testing.T) {
	tests := []struct {
		name, stream string
		want         []sseEvent
	}{
		{
			name:   "one message",
			stream: "data: {\"a\":1}\n\n",
			want:   []sseEvent{{name: "message", data: `{"a":1}`}},
		},
		{
			name:   "named event, comment, and multi-line data",
			stream: ": keepalive\nevent: endpoint\ndata: first\ndata:second\n\n",
			want:   []sseEvent{{name: "endpoint", data: "first\nsecond"}},
		},
		{
			name:   "CRLF and CR endings",
			stream: "data: one\r\n\r\ndata: two\r\rdata: three\n\n",
			want:   []sseEvent{{name: "message", data: "one"}, {name: "message", data: "two"}, {name: "message", data: "three"}},
		},
		{
			name:   "an event with no data is not dispatched",
			stream: "event: ping\n\ndata: real\n\n",
			want:   []sseEvent{{name: "message", data: "real"}},
		},
		{
			name:   "ids and retries are ignored",
			stream: "id: 7\nretry: 1000\ndata: x\n\n",
			want:   []sseEvent{{name: "message", data: "x"}},
		},
		{
			name:   "an unfinished event is dropped",
			stream: "data: whole\n\ndata: half",
			want:   []sseEvent{{name: "message", data: "whole"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// One byte at a time, so a CRLF split across reads is covered.
			got, err := readEvents(t, iotest.OneByteReader(strings.NewReader(tt.stream)))
			if !errors.Is(err, io.EOF) {
				t.Fatalf("stream ended with %v, want io.EOF", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("events = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSSEReaderRefusesHugeEvents(t *testing.T) {
	line := "data: " + strings.Repeat("x", 1<<20) + "\n"
	stream := strings.NewReader(strings.Repeat(line, maxMessageBytes/(1<<20)+1) + "\n")
	if _, err := readEvents(t, stream); !errors.Is(err, errEventTooLarge) {
		t.Errorf("err = %v, want errEventTooLarge", err)
	}
}
