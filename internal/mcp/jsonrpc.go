package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The JSON-RPC error codes the client reads. The three in -32020..-32022 are
// the 2026-07-28 revision's own, and only a modern server sends them, which is
// how a client tells a modern server's refusal from a legacy one's.
const (
	codeMethodNotFound     = -32601
	codeInvalidParams      = -32602
	codeInternal           = -32603
	codeHeaderMismatch     = -32020
	codeMissingCapability  = -32021
	codeUnsupportedVersion = -32022
)

// message is one JSON-RPC 2.0 message in either direction: a request, a
// notification, or a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// hasID reports whether the message carries an id that is not null.
func (m *message) hasID() bool {
	return len(m.ID) > 0 && string(m.ID) != "null"
}

// isRequest reports whether m is a request the receiver must answer.
func (m *message) isRequest() bool { return m.Method != "" && m.hasID() }

// isNotification reports whether m is a one-way message.
func (m *message) isNotification() bool { return m.Method != "" && !m.hasID() }

// isResponse reports whether m answers a request.
func (m *message) isResponse() bool {
	return m.Method == "" && (m.Result != nil || m.Error != nil)
}

// RPCError is a JSON-RPC error a server answered with.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error returns the server's message with its code.
func (e *RPCError) Error() string {
	return fmt.Sprintf("%s (code %d)", strings.TrimSpace(e.Message), e.Code)
}

// modern reports whether the error is one only a 2026-07-28 server sends.
func (e *RPCError) modern() bool {
	switch e.Code {
	case codeHeaderMismatch, codeMissingCapability, codeUnsupportedVersion:
		return true
	}
	return false
}

// supportedVersions reads the versions an UnsupportedProtocolVersion error
// lists.
func (e *RPCError) supportedVersions() []string {
	var data struct {
		Supported []string `json:"supported"`
	}
	if e.Code != codeUnsupportedVersion || json.Unmarshal(e.Data, &data) != nil {
		return nil
	}
	return data.Supported
}

// idKey is the form of a request id the client matches responses by. The
// client issues integers; a server that echoes one as a string still matches.
func idKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(bytes.TrimSpace(raw))
}

// requestID renders a request id on the wire.
func requestID(n int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(n, 10))
}

// decodeMessages reads one message or a batch of them. The 2025-03-26
// revision allowed batches, so an older server may still send one.
func decodeMessages(data []byte) ([]*message, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("decode message: empty")
	}
	if data[0] == '[' {
		var batch []*message
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, fmt.Errorf("decode message batch: %w", err)
		}
		return batch, nil
	}
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}
	return []*message{&m}, nil
}
