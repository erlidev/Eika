package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/subagent"
)

const restartedToolResult = "Eika restarted while this ran; its outcome is unknown. Check before retrying."

// reconcileSessions runs before requests or background turns can start. Each
// repair commits with its notice when needed; report rescans use the stored id.
func (s *Server) reconcileSessions(ctx context.Context) error {
	sessions, err := s.deps.Store.Sessions(ctx, "")
	if err != nil {
		return err
	}
	for _, child := range sessions {
		if child.Kind != store.SessionSubagent || child.Agent == "" || child.Result == "" || child.ReportNoticeID != "" {
			continue
		}
		path, err := s.deps.Store.SessionPath(ctx, child.ID)
		if err != nil {
			return err
		}
		notices, err := s.deps.Store.Entries(ctx, child.ParentSessionID)
		if err != nil {
			return err
		}
		id, err := legacyReportNoticeID(child, path, notices)
		if err != nil {
			return err
		}
		if _, err := s.deps.Store.PinReportNoticeID(ctx, child.ID, id); err != nil {
			return err
		}
	}
	for _, old := range sessions {
		// A child's repair can append a notice to home earlier in this pass.
		sess, err := s.deps.Store.Session(ctx, old.ID)
		if err != nil {
			return err
		}
		runs, err := s.deps.Store.Runs(ctx, sess.ID)
		if err != nil {
			return err
		}
		agentSession := sess.Agent != "" && (sess.Kind == store.SessionHome || sess.Kind == store.SessionSubagent)
		interrupted := agentSession && sess.Kind == store.SessionSubagent && (sess.State == store.SessionRunning || sess.State == store.SessionWaiting)
		for _, run := range runs {
			interrupted = interrupted || (agentSession && run.State == store.RunRunning)
		}
		path, err := s.deps.Store.SessionPath(ctx, sess.ID)
		if err != nil {
			return err
		}
		entries, err := unansweredResults(path)
		if err != nil {
			return err
		}
		if !interrupted && len(entries) == 0 {
			continue
		}
		repair := store.SessionRecovery{SessionID: sess.ID, HeadEntryID: sess.HeadEntryID, Entries: entries}
		if agentSession {
			target, text := sess.ID, "Eika restarted while replying. The reply was interrupted; the user's message is still in this conversation."
			if sess.Kind == store.SessionSubagent {
				target = sess.ParentSessionID
				text = fmt.Sprintf("%s (%s) was interrupted by an Eika restart and is now idle. Use subagent_message to resume it if needed. %s", sess.Name, sess.Agent, restartedToolResult)
			}
			notice, err := session.TextEntry(store.KindNotice, text)
			if err != nil {
				return err
			}
			repair.NoticeSessionID, repair.Notice = target, notice
		}
		if err := s.deps.Store.RecoverSession(ctx, repair); err != nil {
			return err
		}
	}
	// Abort runs that needed no session repair.
	if _, err := s.deps.Store.AbortRunningRuns(ctx); err != nil {
		return err
	}
	for _, old := range sessions {
		if old.Kind != store.SessionSubagent || old.Agent == "" {
			continue
		}
		child, err := s.deps.Store.Session(ctx, old.ID)
		if err != nil {
			return err
		}
		type posted struct{ id, text string }
		var due []posted
		if child.Result != "" {
			due = append(due, posted{reportNoticeID(child), reportNotice(child)})
		}
		if child.State == store.SessionFailed || (child.State == store.SessionCancelled && child.WaitingReason == subagent.ShutdownReason) {
			// An assignment that failed or was cut off by a shutdown: the
			// notice a failure's live write may have lost, or the only one
			// a shutdown gets.
			due = append(due, posted{endedNoticeID(child), endedNotice(child)})
		}
		for _, p := range due {
			notice, err := session.TextEntry(store.KindNotice, p.text)
			if err != nil {
				return err
			}
			notice.ID = p.id
			if _, _, err := s.deps.Store.AppendEntryOnce(ctx, child.ParentSessionID, notice); err != nil {
				return err
			}
		}
	}
	return nil
}

// unansweredResults fills only missing results. It uses the folded current
// branch, so results on another branch do not answer this branch's calls.
func unansweredResults(path []store.Entry) ([]store.Entry, error) {
	messages, err := session.Messages(path)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, m := range messages {
		if m.Role == provider.RoleAssistant {
			for _, call := range m.ToolCalls {
				pending = append(pending, call.ID)
			}
		}
		if m.Role == provider.RoleTool {
			for i, id := range pending {
				if id == m.ToolCallID {
					pending = append(pending[:i], pending[i+1:]...)
					break
				}
			}
		}
	}
	var entries []store.Entry
	for _, id := range pending {
		e, err := session.MessageEntry(provider.ToolResultMessage(id, restartedToolResult, true))
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// legacyReportNoticeID locates a report in the child's path and matches only
// ids from that assignment. Without a report anchor, an existing legacy notice
// is reused; otherwise a fixed child id records the one recovery notice.
func legacyReportNoticeID(child store.Session, path, notices []store.Entry) (string, error) {
	noticeIDs := make(map[string]bool)
	for _, e := range notices {
		if e.Kind == store.KindNotice {
			noticeIDs[e.ID] = true
		}
	}
	messages := make([]provider.Message, len(path))
	for i, e := range path {
		m, _, err := session.Message(e)
		if err != nil {
			return "", err
		}
		messages[i] = m
	}
	anchor := -1
	draft := false
	for i, m := range messages {
		if m.Role == provider.RoleUser {
			draft = m.Content == subagent.DraftReport
		}
		if m.Role != provider.RoleAssistant {
			continue
		}
		for _, call := range m.ToolCalls {
			if call.Name == "report" {
				succeeded := false
				for _, result := range messages[i+1:] {
					if result.Role == provider.RoleUser {
						break
					}
					if result.Role == provider.RoleTool && result.ToolCallID == call.ID {
						succeeded = !result.IsError
						break
					}
				}
				if succeeded {
					anchor = i
				}
			}
		}
		if draft && len(m.ToolCalls) == 0 {
			finished := child.State == store.SessionIdle
			for _, later := range messages[i+1:] {
				if later.Role == provider.RoleUser && later.Content != subagent.DraftReport {
					finished = true
					break
				}
			}
			if finished {
				anchor = i
			}
		}
	}
	candidate := child
	candidate.ReportNoticeID = ""
	start, end := 0, len(path)
	if anchor >= 0 {
		start = anchor
		for i := anchor + 1; i < len(path); i++ {
			if messages[i].Role == provider.RoleUser && messages[i].Content != subagent.DraftReport {
				end = i
				break
			}
		}
	} else {
		candidate.HeadEntryID = ""
		id := reportNoticeID(candidate)
		if noticeIDs[id] {
			return id, nil
		}
	}
	// Search backwards because a notice named the head at assignment end.
	for i := end - 1; i >= start; i-- {
		candidate.HeadEntryID = path[i].ID
		id := reportNoticeID(candidate)
		if noticeIDs[id] {
			return id, nil
		}
	}
	if anchor >= 0 {
		candidate.HeadEntryID = path[anchor].ID
		return reportNoticeID(candidate), nil
	}
	sum := sha256.Sum256([]byte("legacy-report\x00" + child.ID))
	return "notice-" + hex.EncodeToString(sum[:12]), nil
}
