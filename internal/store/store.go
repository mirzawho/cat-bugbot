package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"bugbot/internal/domain"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA synchronous=FULL"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var current int
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current)
	for i := current; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)`,
			i+1, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

type ErrNoDraft struct{}

func (ErrNoDraft) Error() string { return "no active draft" }

var ErrNoDraftSentinel = ErrNoDraft{}

// DraftFor returns the active draft for the user, or ErrNoDraftSentinel.
func (s *Store) DraftFor(ctx context.Context, userID int64) (domain.Draft, error) {
	row := s.db.QueryRowContext(ctx, `SELECT d.state, d.media_json, d.category, d.description, d.prompt_message_id,
			d.report_id, d.media_message_id, d.updated_at, COALESCE(u.username,''), COALESCE(u.first_name,''), COALESCE(u.last_name,'')
		FROM drafts d LEFT JOIN users u ON u.id = d.user_id WHERE d.user_id = ?`, userID)
	var (
		st                  string
		mediaJSON           string
		cat, desc           string
		promptID            int64
		reportID            string
		mediaMsg            int64
		upd                 string
		uname, fname, lname string
	)
	if err := row.Scan(&st, &mediaJSON, &cat, &desc, &promptID, &reportID, &mediaMsg, &upd, &uname, &fname, &lname); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Draft{}, ErrNoDraftSentinel
		}
		return domain.Draft{}, err
	}
	d := domain.Draft{
		User:            domain.UserInfo{ID: userID, Username: uname, FirstName: fname, LastName: lname},
		State:           domain.State(st),
		Category:        cat,
		Desc:            desc,
		PromptMessageID: promptID,
		ReportID:        reportID,
		MediaMessageID:  mediaMsg,
	}
	if err := json.Unmarshal([]byte(mediaJSON), &d.Media); err != nil {
		return domain.Draft{}, err
	}
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, upd)
	return d, nil
}

func (s *Store) CreateDraft(ctx context.Context, u domain.UserInfo, media []domain.Media) error {
	return s.SaveDraft(ctx, domain.Draft{User: u, State: domain.StateAwaitingMedia, Media: media})
}

func (s *Store) SaveDraft(ctx context.Context, d domain.Draft) error {
	media := d.Media
	if media == nil {
		media = []domain.Media{}
	}
	raw, err := json.Marshal(media)
	if err != nil {
		return err
	}
	t := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, first_name, last_name, updated_at)
		VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		username=excluded.username, first_name=excluded.first_name,
		last_name=excluded.last_name, updated_at=excluded.updated_at`,
		d.User.ID, d.User.Username, d.User.FirstName, d.User.LastName, t); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drafts(user_id, state, media_json, category, description, prompt_message_id, report_id, media_message_id, updated_at, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET
		state=excluded.state, media_json=excluded.media_json, category=excluded.category,
		description=excluded.description, prompt_message_id=excluded.prompt_message_id,
		report_id=excluded.report_id, media_message_id=excluded.media_message_id,
		updated_at=excluded.updated_at`,
		d.User.ID, string(d.State), string(raw), d.Category, d.Desc, d.PromptMessageID, d.ReportID, d.MediaMessageID, t, t); err != nil {
		return err
	}
	return tx.Commit()
}

// EndChat drops the lingering chat state after /cancel or a closed conversation.
func (s *Store) EndChat(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE user_id = ? AND state = ?`,
		userID, string(domain.StateChatting))
	return err
}

func (s *Store) DeleteDraft(ctx context.Context, userID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE user_id = ?`, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// InsertReport atomically converts an active draft into a report and removes the draft.
// It also remembers the media message so admin replies can point at the report.
// Returns ErrNoDraftSentinel if there is no draft left (e.g. a double confirm).
func (s *Store) InsertReport(ctx context.Context, r domain.Report) error {
	raw, err := json.Marshal(r.Media)
	if err != nil {
		return err
	}
	t := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var mediaMessageID int64
	_ = tx.QueryRowContext(ctx, `SELECT media_message_id FROM drafts WHERE user_id = ?`, r.User.ID).Scan(&mediaMessageID)
	res, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE user_id = ?`, r.User.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNoDraftSentinel
	}
	if mediaMessageID != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO report_messages(report_id, chat_id, message_id, updated_at)
			VALUES(?,?,?,?) ON CONFLICT(report_id) DO UPDATE SET
			chat_id = excluded.chat_id, message_id = excluded.message_id, updated_at = excluded.updated_at`,
			r.ID, r.User.ID, mediaMessageID, t); err != nil {
			return err
		}
	}
	// The profile arrives in the same transaction as the report: the users row is
	// refreshed here and the report copies it, so a report can never be saved
	// with an empty name/username when Telegram already told us who the user is.
	if _, err := tx.ExecContext(ctx, `INSERT INTO users(id, username, first_name, last_name, updated_at)
		VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		username = CASE WHEN excluded.username <> '' THEN excluded.username ELSE users.username END,
		first_name = CASE WHEN excluded.first_name <> '' THEN excluded.first_name ELSE users.first_name END,
		last_name = CASE WHEN excluded.last_name <> '' THEN excluded.last_name ELSE users.last_name END,
		updated_at = excluded.updated_at`,
		r.User.ID, r.User.Username, r.User.FirstName, r.User.LastName, t); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reports(id, user_id, username, first_name, last_name, media_json, category, description, created_at, updated_at)
		SELECT ?, id, username, first_name, last_name, ?, ?, ?, ?, ? FROM users WHERE id = ?`,
		r.ID, string(raw), r.Category, r.Description, t, t, r.User.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// QueryRow is a small test helper for asserting raw rows.
func (s *Store) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *Store) CountReports(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports`).Scan(&n)
	return n, err
}

func NewReportID() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "BUG-" + hex.EncodeToString(b), nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
