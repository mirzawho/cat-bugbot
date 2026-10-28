package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"bugbot/internal/domain"
)

// ErrReportNotFound is returned when a report ID does not exist.
var ErrReportNotFound = errors.New("report not found")

// ErrStatusNotAllowed is returned for status changes the workflow does not allow.
var ErrStatusNotAllowed = errors.New("status change not allowed")

const reportColumns = `id, user_id, username, first_name, last_name, media_json, category, description, status, created_at, updated_at, resolved_at, rejected_at`

func scanReport(sc interface{ Scan(...any) error }) (domain.Report, error) {
	var (
		r          domain.Report
		mediaJSON  string
		status     string
		created    string
		updated    string
		resolvedAt sql.NullString
		rejectedAt sql.NullString
	)
	if err := sc.Scan(&r.ID, &r.User.ID, &r.User.Username, &r.User.FirstName, &r.User.LastName,
		&mediaJSON, &r.Category, &r.Description, &status, &created, &updated, &resolvedAt, &rejectedAt); err != nil {
		return domain.Report{}, err
	}
	if err := json.Unmarshal([]byte(mediaJSON), &r.Media); err != nil {
		return domain.Report{}, fmt.Errorf("report %s media: %w", r.ID, err)
	}
	r.Status = domain.ReportStatus(status)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if resolvedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, resolvedAt.String); err == nil {
			r.ResolvedAt = &t
		}
	}
	if rejectedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, rejectedAt.String); err == nil {
			r.RejectedAt = &t
		}
	}
	return r, nil
}

// Report returns one report by ID.
func (s *Store) Report(ctx context.Context, id string) (domain.Report, error) {
	r, err := scanReport(s.db.QueryRowContext(ctx, `SELECT `+reportColumns+` FROM reports WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Report{}, ErrReportNotFound
	}
	return r, err
}

// ListReports returns reports newest first. status filters by workflow state
// (new / resolved / rejected); empty means all.
func (s *Store) ListReports(ctx context.Context, status domain.ReportStatus) ([]domain.Report, error) {
	q := `SELECT ` + reportColumns + ` FROM reports`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, string(status))
	}
	q += ` ORDER BY created_at DESC, rowid DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Report{}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetReportStatus moves a report to a workflow status. resolved/rejected are
// recorded with a timestamp; the report can be moved back to new.
func (s *Store) SetReportStatus(ctx context.Context, id string, status domain.ReportStatus) (domain.Report, error) {
	if !status.Valid() {
		return domain.Report{}, fmt.Errorf("invalid status %q", status)
	}
	current, err := s.Report(ctx, id)
	if err != nil {
		return domain.Report{}, err
	}
	// A decided report is final: resolved/rejected cannot be changed again.
	if current.Status == domain.StatusResolved || current.Status == domain.StatusRejected {
		return domain.Report{}, fmt.Errorf("%w: report is closed", ErrStatusNotAllowed)
	}
	// "حذف" is available at any time before the report is closed (from a new or
	// a reviewed report); accepting (resolved) only makes sense after review.
	if status == domain.StatusRejected && current.Status == domain.StatusNew {
		// allowed
	} else if !current.Status.CanMoveTo(status) {
		return domain.Report{}, fmt.Errorf("%w: %s → %s", ErrStatusNotAllowed, current.Status, status)
	}
	t := now()
	res, err := s.db.ExecContext(ctx, `UPDATE reports SET status = ?, updated_at = ?,
		resolved_at = CASE WHEN ? = 'resolved' THEN ? ELSE resolved_at END,
		rejected_at = CASE WHEN ? = 'rejected' THEN ? ELSE rejected_at END
		WHERE id = ?`,
		string(status), t, string(status), t, string(status), t, id)
	if err != nil {
		return domain.Report{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.Report{}, ErrReportNotFound
	}
	return s.Report(ctx, id)
}

// OpenChat registers the conversation for the report. It deliberately does not
// touch the user's draft: a running report flow keeps working, and a user who
// cancelled (/cancel) is not pulled back into chatting. It also returns whether
// the user's draft was already in chatting mode.
func (s *Store) OpenChat(ctx context.Context, r domain.Report) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO admin_chats(report_id, user_id, opened_at, last_at)
		VALUES(?,?,?,?) ON CONFLICT(report_id) DO UPDATE SET last_at = excluded.last_at`,
		r.ID, r.User.ID, now(), now()); err != nil {
		return false, err
	}
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM drafts WHERE user_id = ?`, r.User.ID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return state == string(domain.StateChatting), err
}

// LinkChat puts the user into a conversation for reportID and records the real
// Telegram chat_id when it differs from the user id (privacy relay).
func (s *Store) LinkChat(ctx context.Context, userID, chatID int64, reportID string) error {
	t := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, first_name, last_name, updated_at)
		VALUES(?, '', '', '', ?) ON CONFLICT(id) DO NOTHING`, userID, t); err != nil {
		return err
	}
	if chatID != 0 && chatID != userID {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_chat_ids(user_id, chat_id) VALUES(?,?)
			ON CONFLICT(user_id) DO UPDATE SET chat_id = excluded.chat_id`, userID, chatID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drafts(user_id, state, media_json, category, description, prompt_message_id, report_id, updated_at, created_at)
		VALUES(?,?,'[]','','',0,?,?,?) ON CONFLICT(user_id) DO UPDATE SET
		state = excluded.state, report_id = excluded.report_id, media_json = '[]',
		category = '', description = '', prompt_message_id = 0, updated_at = excluded.updated_at`,
		userID, string(domain.StateChatting), reportID, t, t); err != nil {
		return err
	}
	return tx.Commit()
}

// AdminChatID returns the chat_id to message the user at (their real chat, if known).
func (s *Store) AdminChatID(ctx context.Context, userID int64) int64 {
	var chatID int64
	if err := s.db.QueryRowContext(ctx, `SELECT chat_id FROM admin_chat_ids WHERE user_id = ?`, userID).Scan(&chatID); err == nil {
		return chatID
	}
	return userID
}

// ChatReport returns the report ID of the user's conversation, if any.
func (s *Store) ChatReport(ctx context.Context, userID int64) string {
	var id string
	_ = s.db.QueryRowContext(ctx, `SELECT report_id FROM drafts WHERE user_id = ? AND state = ?`,
		userID, string(domain.StateChatting)).Scan(&id)
	return id
}

// CloseChat ends the conversation (the report itself stays untouched); the
// reporter can start a new report right away.
func (s *Store) CloseChat(ctx context.Context, userID int64) (string, error) {
	var reportID string
	err := s.db.QueryRowContext(ctx, `SELECT report_id FROM drafts WHERE user_id = ? AND state = ?`,
		userID, string(domain.StateChatting)).Scan(&reportID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE user_id = ? AND state = ?`,
		userID, string(domain.StateChatting)); err != nil {
		return "", err
	}
	return reportID, nil
}

// ChatOpen reports whether an admin conversation is active for the report.
func (s *Store) ChatOpen(ctx context.Context, reportID string) (int64, bool, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM admin_chats WHERE report_id = ?`, reportID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return userID, true, nil
}

// AppendChat stores one message of a report conversation.
func (s *Store) AppendChat(ctx context.Context, m domain.ChatMessage) (domain.ChatMessage, error) {
	t := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO chat_messages(report_id, sender, admin_id, admin_name, text, created_at)
		VALUES(?,?,?,?,?,?)`, m.ReportID, string(m.Sender), m.AdminID, m.AdminName, m.Text, t)
	if err != nil {
		return m, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE admin_chats SET last_at = ? WHERE report_id = ?`, t, m.ReportID); err != nil {
		return m, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return m, err
	}
	m.ID = id
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, t)
	return m, nil
}

// ChatMessages lists a report conversation oldest first.
func (s *Store) ChatMessages(ctx context.Context, reportID string) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, report_id, sender, admin_id, admin_name, text, created_at
		FROM chat_messages WHERE report_id = ? ORDER BY id`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ChatMessage{}
	for rows.Next() {
		var (
			m     domain.ChatMessage
			sndr  string
			creat string
		)
		if err := rows.Scan(&m.ID, &m.ReportID, &sndr, &m.AdminID, &m.AdminName, &m.Text, &creat); err != nil {
			return nil, err
		}
		m.Sender = domain.Sender(sndr)
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, creat)
		out = append(out, m)
	}
	return out, rows.Err()
}
