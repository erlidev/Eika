package server

import (
	"context"
	"fmt"

	"github.com/erlidev/eika/internal/provider"
	"github.com/erlidev/eika/internal/session"
	"github.com/erlidev/eika/internal/store"
)

const restartedToolResult = "Eika restarted while this ran; its outcome is unknown. Check before retrying."

// reconcileSessions runs before requests or background turns can start. Each
// repair commits with its notice; report rescans use the persisted notice id.
func (s *Server) reconcileSessions(ctx context.Context) error {
	sessions, err := s.deps.Store.Sessions(ctx, "")
	if err != nil {
		return err
	}
	for _, child := range sessions {
		if child.Kind != store.SessionSubagent || child.Agent == "" || child.Result == "" || child.ReportNoticeID != "" {
			continue
		}
		id, err := s.legacyReportNoticeID(ctx, child)
		if err != nil {
			return err
		}
		if _, err := s.deps.Store.PinReportNoticeID(ctx, child.ID, id); err != nil {
			return err
		}
	}
	for _, old := range sessions {
		if old.Agent == "" || (old.Kind != store.SessionHome && old.Kind != store.SessionSubagent) {
			continue
		}
		// A child's repair can append a notice to home earlier in this pass.
		sess, err := s.deps.Store.Session(ctx, old.ID)
		if err != nil {
			return err
		}
		runs, err := s.deps.Store.Runs(ctx, sess.ID)
		if err != nil {
			return err
		}
		interrupted := sess.Kind == store.SessionSubagent && (sess.State == store.SessionRunning || sess.State == store.SessionWaiting)
		for _, run := range runs {
			interrupted = interrupted || run.State == store.RunRunning
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
		target, text := sess.ID, "Eika restarted while replying. The reply was interrupted; the user's message is still in this conversation."
		if sess.Kind == store.SessionSubagent {
			target = sess.ParentSessionID
			text = fmt.Sprintf("%s (%s) was interrupted by an Eika restart and is now idle. Use subagent_message to resume it if needed. %s", sess.Name, sess.Agent, restartedToolResult)
		}
		notice, err := session.TextEntry(store.KindNotice, text)
		if err != nil {
			return err
		}
		if err := s.deps.Store.RecoverSession(ctx, store.SessionRecovery{
			SessionID: sess.ID, HeadEntryID: sess.HeadEntryID, Entries: entries,
			NoticeSessionID: target, Notice: notice,
		}); err != nil {
			return err
		}
	}
	// Legacy sessions retain their abort-only startup behaviour.
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
		if child.Result == "" {
			continue
		}
		notice, err := session.TextEntry(store.KindNotice, reportNotice(child))
		if err != nil {
			return err
		}
		notice.ID = reportNoticeID(child)
		if _, _, err := s.deps.Store.AppendEntryOnce(ctx, child.ParentSessionID, notice); err != nil {
			return err
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

// legacyReportNoticeID preserves an old notice even if the child was resumed
// after posting it. Before report ids were stored, the notice named a head
// that may now be earlier in the child's history. Equal report text reuses an
// existing legacy notice; old rows cannot distinguish identical reports.
func (s *Server) legacyReportNoticeID(ctx context.Context, child store.Session) (string, error) {
	notices, err := s.deps.Store.Entries(ctx, child.ParentSessionID)
	if err != nil {
		return "", err
	}
	entries, err := s.deps.Store.Entries(ctx, child.ID)
	if err != nil {
		return "", err
	}
	ids := make(map[string]bool, len(entries)+1)
	original := child
	original.HeadEntryID = ""
	ids[reportNoticeID(original)] = true
	for _, e := range entries {
		original.HeadEntryID = e.ID
		ids[reportNoticeID(original)] = true
	}
	for i := len(notices) - 1; i >= 0; i-- {
		e := notices[i]
		if e.Kind != store.KindNotice || !ids[e.ID] {
			continue
		}
		text, err := session.TextOf(e)
		if err != nil {
			return "", err
		}
		if text == reportNotice(child) {
			return e.ID, nil
		}
	}
	return reportNoticeID(child), nil
}
