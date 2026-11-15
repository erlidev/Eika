package store

import "context"

// SessionRecovery is the atomic repair of one interrupted session.
// HeadEntryID is the head from which Entries were computed. If NoticeSessionID
// is set, Notice is appended to that session, which can be the child's home.
type SessionRecovery struct {
	SessionID       string
	HeadEntryID     string
	Entries         []Entry
	NoticeSessionID string
	Notice          Entry
}

// RecoverSession stores tool results and an optional notice, idles an agent
// child when a notice is present, and aborts old runs in one transaction.
// Call it at startup before any new run starts.
// A changed head refuses the repair instead of appending to a different path.
func (s *Store) RecoverSession(ctx context.Context, r SessionRecovery) error {
	err := s.tx(ctx, func(q querier) error {
		head, err := lockSessionHead(ctx, q, r.SessionID)
		if err != nil {
			return err
		}
		if head != r.HeadEntryID {
			return ErrConflict
		}
		for _, e := range r.Entries {
			added, err := insertEntry(ctx, q, r.SessionID, head, e)
			if err != nil {
				return err
			}
			head = added.ID
		}
		if head != r.HeadEntryID {
			if err := setHead(ctx, q, r.SessionID, head); err != nil {
				return err
			}
		}
		if r.NoticeSessionID != "" {
			noticeHead := head
			if r.NoticeSessionID != r.SessionID {
				noticeHead, err = lockSessionHead(ctx, q, r.NoticeSessionID)
				if err != nil {
					return err
				}
			}
			added, err := insertEntry(ctx, q, r.NoticeSessionID, noticeHead, r.Notice)
			if err != nil {
				return err
			}
			if err := setHead(ctx, q, r.NoticeSessionID, added.ID); err != nil {
				return err
			}
			if _, err := q.Exec(ctx, `UPDATE sessions SET state = 'idle', waiting_reason = '',
                last_active_at = now(), updated_at = now()
                WHERE id = $1 AND kind = 'subagent'`, r.SessionID); err != nil {
				return err
			}
		}

		_, err = q.Exec(ctx, `UPDATE runs SET state = 'aborted', error = '', finished_at = now()
            WHERE session_id = $1 AND state = 'running'`, r.SessionID)
		return err
	})
	if err != nil {
		return wrap("recover session "+r.SessionID, err)
	}
	return nil
}
