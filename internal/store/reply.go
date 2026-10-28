package store

import "context"

// ReporterMessage is the chat message that carries the report's media; admin
// replies are attached to it so the conversation is easy to follow.
type ReporterMessage struct {
	ChatID    int64
	MessageID int64
}

// RememberReporterMessage stores where a report's media message lives.
func (s *Store) RememberReporterMessage(ctx context.Context, reportID string, ref ReporterMessage) error {
	if ref.ChatID == 0 || ref.MessageID == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO report_messages(report_id, chat_id, message_id, updated_at)
		VALUES(?,?,?,?) ON CONFLICT(report_id) DO UPDATE SET
		chat_id = excluded.chat_id, message_id = excluded.message_id, updated_at = excluded.updated_at`,
		reportID, ref.ChatID, ref.MessageID, now())
	return err
}

// ReporterMessage returns the stored media message of a report, if known.
func (s *Store) ReporterMessage(ctx context.Context, reportID string) ReporterMessage {
	var ref ReporterMessage
	_ = s.db.QueryRowContext(ctx, `SELECT chat_id, message_id FROM report_messages WHERE report_id = ?`, reportID).
		Scan(&ref.ChatID, &ref.MessageID)
	return ref
}
