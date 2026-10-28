package store

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id          INTEGER PRIMARY KEY,
		username    TEXT NOT NULL DEFAULT '',
		first_name  TEXT NOT NULL DEFAULT '',
		last_name   TEXT NOT NULL DEFAULT '',
		updated_at  TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS drafts (
		user_id       INTEGER PRIMARY KEY,
		state         TEXT NOT NULL,
		media_json    TEXT NOT NULL DEFAULT '[]',
		category      TEXT NOT NULL DEFAULT '',
		description   TEXT NOT NULL DEFAULT '',
		last_update_id INTEGER NOT NULL DEFAULT 0,
		updated_at    TEXT NOT NULL,
		created_at    TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS reports (
		id          TEXT PRIMARY KEY,
		user_id     INTEGER NOT NULL,
		username    TEXT NOT NULL DEFAULT '',
		first_name  TEXT NOT NULL DEFAULT '',
		last_name   TEXT NOT NULL DEFAULT '',
		media_json  TEXT NOT NULL,
		category    TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'new',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_reports_user ON reports(user_id);
	CREATE INDEX IF NOT EXISTS idx_reports_created ON reports(created_at);`,
	`ALTER TABLE drafts ADD COLUMN prompt_message_id INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE drafts ADD COLUMN report_id TEXT NOT NULL DEFAULT '';`,
	// Admin workflow: resolve/reject timestamps and per-report conversations.
	`ALTER TABLE reports ADD COLUMN resolved_at TEXT;
	ALTER TABLE reports ADD COLUMN rejected_at TEXT;

	CREATE TABLE IF NOT EXISTS admin_chats (
		report_id   TEXT PRIMARY KEY,
		user_id     INTEGER NOT NULL,
		opened_at   TEXT NOT NULL,
		last_at     TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS chat_messages (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		report_id   TEXT NOT NULL,
		sender      TEXT NOT NULL,
		admin_id    INTEGER NOT NULL DEFAULT 0,
		admin_name  TEXT NOT NULL DEFAULT '',
		text        TEXT NOT NULL,
		created_at  TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_chat_messages_report ON chat_messages(report_id, id);`,
	`CREATE TABLE IF NOT EXISTS admin_chat_ids (
		user_id  INTEGER PRIMARY KEY,
		chat_id  INTEGER NOT NULL
	);`,
	`ALTER TABLE drafts ADD COLUMN media_message_id INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE IF NOT EXISTS report_messages (
		report_id   TEXT PRIMARY KEY,
		chat_id     INTEGER NOT NULL,
		message_id  INTEGER NOT NULL,
		updated_at  TEXT NOT NULL
	);`,
}
