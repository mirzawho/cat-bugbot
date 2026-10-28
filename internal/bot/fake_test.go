package bot_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"bugbot/internal/bot"
	"bugbot/internal/store"
	"bugbot/internal/telegram"
)

var errNotImplemented = errors.New("not implemented in the fake bot API")

type sentMsg struct {
	ChatID int64
	Text   string
	KB     *telegram.InlineKeyboardMarkup
	msgID  int64
}

// fakeAPI emulates a single chat: bot messages and user messages share one ID space.
type fakeAPI struct {
	mu             sync.Mutex
	sent           []sentMsg
	edited         []sentMsg
	replyKeyboards []*telegram.ReplyKeyboardMarkup
	uploads        []string
	notifications  []map[string]any
	deleted        []int64
	answered       []string
	updates        []telegram.Update
	drain          int
	nextMsg        int64
	clock          int
	editAt         int
	sentAt         int
	editErr        error
	errIDs         []int64

	sendErr error
}

func (f *fakeAPI) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]telegram.Update, error) {
	f.mu.Lock()
	if f.drain > 0 {
		f.drain--
		f.mu.Unlock()
		return nil, nil
	}
	defer f.mu.Unlock()
	var out []telegram.Update
	for _, u := range f.updates {
		if u.UpdateID >= offset {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeAPI) SendMessage(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	f.nextMsg++
	f.clock++
	f.sentAt = f.clock
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text, KB: kb, msgID: f.nextMsg})
	return f.nextMsg, nil
}

func (f *fakeAPI) SendMessageWithReplyKeyboard(ctx context.Context, chatID int64, text string, kb *telegram.ReplyKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replyKeyboards = append(f.replyKeyboards, kb)
	f.nextMsg++
	f.sent = append(f.sent, sentMsg{ChatID: chatID, Text: text, msgID: f.nextMsg})
	return f.nextMsg, nil
}

func (f *fakeAPI) EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil && (f.errIDs == nil || slices.Contains(f.errIDs, messageID)) {
		return f.editErr
	}
	f.clock++
	f.editAt = f.clock
	f.edited = append(f.edited, sentMsg{ChatID: chatID, Text: text, KB: kb, msgID: messageID})
	return nil
}

func (f *fakeAPI) DeleteMessage(ctx context.Context, chatID, messageID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, messageID)
	return nil
}

func (f *fakeAPI) SendMedia(ctx context.Context, chatID int64, mediaType, filename string, data []byte, caption string) (telegram.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, mediaType+":"+filename)
	if mediaType == "video" {
		return telegram.Message{MessageID: 1, Video: &telegram.Video{FileID: "fresh-v-1", FileUniqueID: "fresh-vu-1"}}, nil
	}
	return telegram.Message{MessageID: 1, Photo: []telegram.Photo{{FileID: "fresh-p-1", FileUniqueID: "fresh-pu-1"}}}, nil
}

func (f *fakeAPI) FileFromMessage(m telegram.Message) (string, string, string, bool) {
	if len(m.Photo) > 0 {
		p := m.Photo[len(m.Photo)-1]
		return "image", p.FileID, p.FileUniqueID, true
	}
	if m.Video != nil {
		return "video", m.Video.FileID, m.Video.FileUniqueID, true
	}
	return "", "", "", false
}

func (f *fakeAPI) SendNotification(ctx context.Context, params map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifications == nil {
		f.notifications = []map[string]any{}
	}
	f.notifications = append(f.notifications, params)
	return nil
}

func (f *fakeAPI) GetChat(ctx context.Context, userID int64) (telegram.User, error) {
	return telegram.User{ID: userID}, nil
}
func (f *fakeAPI) GetUserProfilePhotos(ctx context.Context, userID int64) ([]string, error) {
	return nil, nil
}
func (f *fakeAPI) GetMe(ctx context.Context) (telegram.User, error) {
	return telegram.User{ID: 1, Username: "bugbot", IsBot: true}, nil
}

func (f *fakeAPI) GetFile(ctx context.Context, fileID string) (telegram.File, error) {
	return telegram.File{FileID: fileID, FilePath: "photos/" + fileID + ".jpg", FileSize: 12}, nil
}

func (f *fakeAPI) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	return []byte("JPEG"), nil
}

func (f *fakeAPI) AnswerCallback(ctx context.Context, id, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered = append(f.answered, id)
	return nil
}

// editErrIDs scopes editErr to specific messages, e.g. a message that has no caption.
func (f *fakeAPI) editErrIDs(ids ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errIDs = ids
}

// userMessage is the ID of a message the user sent. Real chats number bot and
// user messages on one sequence, so user ids skip ahead and never collide with
// prompts the bot posted.
func (f *fakeAPI) userMessage() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextMsg++
	return f.nextMsg + userIDOffset
}

// nextUserID is the ID the next user message will get.
func (f *fakeAPI) nextUserID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextMsg + 1 + userIDOffset
}

// bot messages always land below userIDOffset, so prompt ids never collide with
// user message ids. Keeps fake chats unambiguous.
const userIDOffset = 1_000_000

// promptID is the ID of the latest prompt, the message whose button the user taps next.
func (f *fakeAPI) promptID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := len(f.sent); n > 0 {
		return f.sent[n-1].msgID
	}
	return f.nextMsg
}

func (f *fakeAPI) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	for i, m := range f.sent {
		out[i] = m.Text
	}
	return out
}

func (f *fakeAPI) lastEdit(t *testing.T) sentMsg {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.edited) == 0 {
		t.Fatal("no edit calls")
	}
	return f.edited[len(f.edited)-1]
}

// lastVisible is the latest thing the bot showed: an edited message or a page
// after any edits the user made to their own message (media edits come later
// in time, even though user ids sit in their own range).
func (f *fakeAPI) lastVisible(t *testing.T) sentMsg {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var last sentMsg
	seen := false
	if n := len(f.edited); n > 0 {
		last, seen = f.edited[n-1], true
	}
	if n := len(f.sent); n > 0 && (!seen || f.sentAt > f.editAt) {
		last, seen = f.sent[n-1], true
	}
	if seen {
		return last
	}
	t.Fatal("no messages sent or edited")
	return sentMsg{}
}

const testUserID = int64(42)

type env struct {
	t     *testing.T
	api   *fakeAPI
	db    *store.Store
	h     *bot.Handler
	path  string
	msgID int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, api: &fakeAPI{}, db: db, path: path}
	e.h = bot.New(e.api, db)
	return e
}

// reopen simulates a service restart against the same database file.
// The fake API and its chat outlive the restart, like the real chat does.
func (e *env) reopen() {
	e.t.Helper()
	if err := e.db.Close(); err != nil {
		e.t.Fatalf("close: %v", err)
	}
	db, err := store.Open(e.path)
	if err != nil {
		e.t.Fatalf("reopen: %v", err)
	}
	e.t.Cleanup(func() { db.Close() })
	e.db = db
	e.h = bot.New(e.api, db)
}

func (e *env) dispatch(u telegram.Update) {
	e.t.Helper()
	e.h.HandleUpdate(context.Background(), u)
}

func (e *env) text(text string) {
	e.t.Helper()
	e.msgID++
	e.dispatch(telegram.Update{
		UpdateID: e.msgID,
		Message: &telegram.Message{
			MessageID: e.api.userMessage(),
			From:      &telegram.User{ID: testUserID, FirstName: "علی", Username: "ali"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Text:      text,
		},
	})
}

func (e *env) photo(id string) {
	e.t.Helper()
	e.msgID++
	e.dispatch(telegram.Update{
		UpdateID: e.msgID,
		Message: &telegram.Message{
			MessageID: e.api.userMessage(),
			From:      &telegram.User{ID: testUserID, FirstName: "علی", Username: "ali"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Photo:     []telegram.Photo{{FileID: "small"}, {FileID: id, FileUniqueID: "u-" + id}},
		},
	})
}

func (e *env) video(id string) {
	e.t.Helper()
	e.msgID++
	e.dispatch(telegram.Update{
		UpdateID: e.msgID,
		Message: &telegram.Message{
			MessageID: e.api.userMessage(),
			From:      &telegram.User{ID: testUserID, FirstName: "علی", Username: "ali"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Video:     &telegram.Video{FileID: id, FileUniqueID: "u-" + id},
		},
	})
}

func (e *env) callback(data string) {
	e.t.Helper()
	e.msgID++
	e.dispatch(telegram.Update{
		UpdateID: e.msgID,
		CallbackQuery: &telegram.CallbackQuery{
			ID:      "cb-" + data,
			From:    telegram.User{ID: testUserID, FirstName: "علی", Username: "ali"},
			Message: &telegram.Message{MessageID: e.api.promptID(), Chat: telegram.Chat{ID: testUserID, Type: "private"}},
			Data:    data,
		},
	})
}

// last is the current prompt: what the user sees on the wizard bubble.
func (e *env) last() sentMsg { return e.api.lastVisible(e.t) }

func (e *env) reportCount() int {
	e.t.Helper()
	n, err := e.db.CountReports(context.Background())
	if err != nil {
		e.t.Fatalf("count: %v", err)
	}
	return n
}

func (e *env) reportRow(t *testing.T) (mediaJSON, category string) {
	t.Helper()
	err := e.db.QueryRow(context.Background(),
		`SELECT media_json, category FROM reports ORDER BY created_at DESC LIMIT 1`,
	).Scan(&mediaJSON, &category)
	if err != nil {
		t.Fatalf("query report: %v", err)
	}
	return mediaJSON, category
}

// goToCategory reaches the "selecting_category" state.
func (e *env) goToCategory() {
	e.t.Helper()
	e.text("/start")
	e.photo("photo-1")
	e.callback("media:yes")
}

// goToDescription reaches the "awaiting_description" state.
func (e *env) goToDescription() {
	e.t.Helper()
	e.goToCategory()
	e.callback("cat:1") // گروه‌ها
	e.callback("cat:yes")
}
