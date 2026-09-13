package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/erlidev/eika/internal/event"
	"github.com/erlidev/eika/internal/store"
)

// Bounds on one event stream connection.
const (
	// writeTimeout bounds one event write, so that a client that stops
	// reading its socket cannot hold a goroutine forever.
	writeTimeout = 10 * time.Second
	// maxClientMessage bounds a request from the client. Requests are a
	// topic list or a replay ask; neither is large.
	maxClientMessage = 64 * 1024
	// maxReplayTopics bounds how many topics one client subscribes to.
	maxReplayTopics = 64
)

// The request types a client sends on the event stream.
const (
	requestSubscribe = "subscribe"
	requestReplay    = "session.replay"
)

// streamRequest is a message from a client on the event stream. Type decides
// which of the other fields carry meaning.
type streamRequest struct {
	Type string `json:"type"`
	// Topics replaces the subscription's topics on a subscribe request.
	Topics []string `json:"topics,omitempty"`
	// SessionID is the session to replay on a session.replay request.
	SessionID string `json:"session_id,omitempty"`
	// Since replays only the entries after this one. Empty replays the whole
	// path from the session's root.
	Since string `json:"since,omitempty"`
}

// handleEvents serves the event stream: one WebSocket per client, subscribed
// to the topics it asks for, with replay of the messages it missed.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	// A same-origin handshake is always accepted; anything else has to be an
	// origin the deployment named, so that a page on another site cannot open
	// the stream with a token it tricked the browser into sending.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.cfg.AllowedOrigins,
	})
	if err != nil {
		s.log.Warn("accept event stream", "error", err)
		return
	}
	conn.SetReadLimit(maxClientMessage)
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub := s.deps.Bus.Subscribe(topicsOf(r.URL.Query().Get("topics"))...)
	defer sub.Close()

	if since := r.URL.Query().Get("since"); since != "" {
		if err := s.replayFrom(ctx, conn, since); err != nil {
			s.closeWith(conn, err)
			return
		}
	}

	// The reader owns the connection's requests and the writer owns its
	// events; whichever stops first cancels the other.
	go func() {
		defer cancel()
		s.readRequests(ctx, conn, sub)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.Events():
			if !ok {
				return
			}
			if err := s.writeEvent(ctx, conn, e); err != nil {
				return
			}
		}
	}
}

// readRequests handles the messages a client sends until it disconnects.
func (s *Server) readRequests(ctx context.Context, conn *websocket.Conn, sub *event.Subscription) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var req streamRequest
		if err := json.Unmarshal(data, &req); err != nil {
			s.closeWith(conn, invalidf("decode stream request: %v", err))
			return
		}
		switch req.Type {
		case requestSubscribe:
			if len(req.Topics) > maxReplayTopics {
				s.closeWith(conn, invalidf("at most %d topics may be subscribed", maxReplayTopics))
				return
			}
			sub.Subscribe(req.Topics...)
		case requestReplay:
			if err := s.replaySession(ctx, conn, req.SessionID, req.Since); err != nil {
				s.closeWith(conn, err)
				return
			}
		default:
			s.closeWith(conn, invalidf("unknown request type %q", req.Type))
			return
		}
	}
}

// writeEvent sends one event to the client.
func (s *Server) writeEvent(ctx context.Context, conn *websocket.Conn, e event.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		s.log.Error("encode event for the stream", "type", e.Type, "error", err)
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// replayFrom replays the entries of a session that follow one entry, which is
// what ?since=<entry_id> asks for. The entry names the session, so a client
// that knows where it stopped needs nothing else.
func (s *Server) replayFrom(ctx context.Context, conn *websocket.Conn, entryID string) error {
	if s.deps.Store == nil {
		return notFoundf("entry %s not found", entryID)
	}
	entry, err := s.deps.Store.Entry(ctx, entryID)
	if err != nil {
		return err
	}
	return s.replaySession(ctx, conn, entry.SessionID, entryID)
}

// replaySession sends the session's path, from the root or from the entry
// after since, as session.message events on this connection alone. It writes
// to the socket rather than through the subscription, so that a long history
// cannot overrun the subscriber buffer and be dropped as if the client were
// slow.
func (s *Server) replaySession(ctx context.Context, conn *websocket.Conn, sessionID, since string) error {
	if sessionID == "" {
		return invalidf("session_id is required to replay a session")
	}
	if s.deps.Store == nil {
		return notFoundf("session %s not found", sessionID)
	}
	entries, err := s.tree.Path(ctx, sessionID)
	if err != nil {
		return err
	}
	skipping := since != ""
	for _, e := range entries {
		if skipping {
			skipping = e.ID != since
			continue
		}
		replayed, err := event.New(event.TypeSessionMessage, event.SessionTopic(sessionID), event.SessionMessage{
			SessionID: sessionID,
			EntryID:   e.ID,
			ParentID:  e.ParentID,
			Kind:      string(e.Kind),
			Commit:    e.Commit,
			CreatedAt: e.CreatedAt,
			Message:   e.Payload,
		})
		if err != nil {
			return err
		}
		if err := s.writeEvent(ctx, conn, replayed); err != nil {
			return err
		}
	}
	if skipping {
		return notFoundf("entry %s is not on the current branch of session %s", since, sessionID)
	}
	return nil
}

// closeWith ends the connection with the status the error deserves, so that a
// client learns why it was cut off.
func (s *Server) closeWith(conn *websocket.Conn, err error) {
	status := websocket.StatusInternalError
	if code, _ := statusOf(err); code != http.StatusInternalServerError {
		status = websocket.StatusPolicyViolation
	}
	if errors.Is(err, store.ErrNotFound) {
		status = websocket.StatusPolicyViolation
	}
	_ = conn.Close(status, truncateReason(err.Error()))
}

// truncateReason keeps a close reason within the 123 bytes the protocol
// allows.
func truncateReason(reason string) string {
	const limit = 120
	if len(reason) <= limit {
		return reason
	}
	return reason[:limit] + "..."
}

// topicsOf splits the comma-separated topics of a query parameter.
func topicsOf(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
