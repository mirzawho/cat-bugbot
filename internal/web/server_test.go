package web_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bugbot/internal/domain"
	"bugbot/internal/store"
	"bugbot/internal/telegram"
	"bugbot/internal/web"
)

type fakeTG struct {
	mu            sync.Mutex
	sent          []string
	notifications []map[string]any
}

func (f *fakeTG) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]telegram.Update, error) {
	return nil, nil
}
func (f *fakeTG) SendMessage(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return 1, nil
}
func (f *fakeTG) EditMessageText(ctx context.Context, chatID, messageID int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	return nil
}
func (f *fakeTG) DeleteMessage(ctx context.Context, chatID, messageID int64) error { return nil }
func (f *fakeTG) AnswerCallback(ctx context.Context, id, text string) error        { return nil }
func (f *fakeTG) SendMessageWithReplyKeyboard(ctx context.Context, chatID int64, text string, kb *telegram.ReplyKeyboardMarkup) (int64, error) {
	return f.SendMessage(ctx, chatID, text, nil)
}
func (f *fakeTG) SendNotification(ctx context.Context, params map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifications == nil {
		f.notifications = []map[string]any{}
	}
	f.notifications = append(f.notifications, params)
	return nil
}

func (f *fakeTG) GetChat(ctx context.Context, userID int64) (telegram.User, error) {
	return telegram.User{ID: userID}, nil
}
func (f *fakeTG) GetUserProfilePhotos(ctx context.Context, userID int64) ([]string, error) {
	return nil, nil
}
func (f *fakeTG) SendMedia(ctx context.Context, chatID int64, mediaType, filename string, data []byte, caption string) (telegram.Message, error) {
	return telegram.Message{}, nil
}
func (f *fakeTG) FileFromMessage(m telegram.Message) (string, string, string, bool) {
	return "", "", "", false
}
func (f *fakeTG) GetMe(ctx context.Context) (telegram.User, error) {
	return telegram.User{ID: 1, Username: "bugbot", IsBot: true}, nil
}
func (f *fakeTG) GetFile(ctx context.Context, fileID string) (telegram.File, error) {
	return telegram.File{FilePath: "photos/file.jpg"}, nil
}
func (f *fakeTG) DownloadFile(ctx context.Context, path string) ([]byte, error) {
	return []byte("JPEGDATA"), nil
}

func (f *fakeTG) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

func seedReport(t *testing.T, st *store.Store, userID int64) domain.Report {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveDraft(ctx, domain.Draft{
		User:     domain.UserInfo{ID: userID, FirstName: "سارا", Username: "sara"},
		State:    domain.StateConfirmingSubmit,
		Media:    []domain.Media{{Type: domain.MediaImage, FileID: "f1"}},
		Category: "گروه‌ها",
		Desc:     "پیام نمی‌رود",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport(ctx, domain.Report{
		ID: "BUG-1", User: domain.UserInfo{ID: userID, FirstName: "سارا", Username: "sara"},
		Media: []domain.Media{{Type: domain.MediaImage, FileID: "f1"}}, Category: "گروه‌ها", Description: "پیام نمی‌رود",
	}); err != nil {
		t.Fatal(err)
	}
	r, err := st.Report(ctx, "BUG-1")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type env struct {
	t   *testing.T
	srv http.Handler
	cli *http.Client
	tg  *fakeTG
	st  *store.Store
}

func newEnv(t *testing.T, password string, adminID int64) *env {
	t.Helper()
	return newEnvWithOpts(t, web.Options{Password: password, AdminIDs: []int64{adminID}})
}

// newEnvWithOpts builds a panel with a temp database; DB may be nil.
func newEnvWithOpts(t *testing.T, opts web.Options) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tg := &fakeTG{}
	opts.DB = st
	opts.Telegram = tg
	if opts.Hooks == nil {
		opts.Hooks = &nopHooks{}
	}
	server, err := web.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	jar := &simpleJar{cookies: map[string]string{}}
	return &env{
		t:   t,
		srv: server.Handler(),
		cli: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		tg:  tg,
		st:  st,
	}
}

type nopHooks struct{}

func (nopHooks) NotifyResolved(ctx context.Context, id string) error { return nil }
func (nopHooks) NotifyRejected(ctx context.Context, id string) error { return nil }
func (nopHooks) SendChat(ctx context.Context, id, name, text string) (domain.ChatMessage, error) {
	return domain.ChatMessage{}, nil
}

type simpleJar struct {
	mu      sync.Mutex
	cookies map[string]string
}

func (j *simpleJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		j.cookies[c.Name] = c.Value
	}
}
func (j *simpleJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]*http.Cookie, 0, len(j.cookies))
	for k, v := range j.cookies {
		out = append(out, &http.Cookie{Name: k, Value: v})
	}
	return out
}

const origin = "http://bugbot.local"

// do runs one request through the panel handler without opening a socket.
func (e *env) do(method, path string, body io.Reader, ctype string) (*http.Response, []byte) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Host = "bugbot.local"
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	u, _ := url.Parse(origin)
	for _, c := range e.cli.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	resp := rec.Result()
	e.cli.Jar.SetCookies(u, resp.Cookies())
	return resp, rec.Body.Bytes()
}

func (e *env) login(password string) *http.Response {
	e.t.Helper()
	resp, _ := e.do(http.MethodPost, "/admin/login", strings.NewReader(url.Values{"token": {password}}.Encode()), "application/x-www-form-urlencoded")
	return resp
}

func (e *env) get(path string) (*http.Response, []byte) {
	e.t.Helper()
	return e.do(http.MethodGet, path, nil, "")
}

func (e *env) postJSON(path, body string) (*http.Response, []byte) {
	e.t.Helper()
	return e.do(http.MethodPost, path, strings.NewReader(body), "application/json")
}

func TestWrongPasswordIsRejected(t *testing.T) {
	e := newEnv(t, "secret", 1)
	resp := e.login("nope")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "error=") {
		t.Fatalf("expected redirect to error, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = e.get("/admin/api/reports")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("api without session = %d", resp.StatusCode)
	}
}

func TestAdminsRequirePanelPassword(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := web.New(web.Options{AdminIDs: []int64{1}, DB: st, Telegram: &fakeTG{}}); err == nil {
		t.Fatal("panel with admins but no password should refuse to start")
	}
	// Without admins the panel simply never lets anyone in.
	if _, err := web.New(web.Options{DB: st, Telegram: &fakeTG{}}); err != nil {
		t.Fatalf("open panel without admins: %v", err)
	}
}

func TestSessionCookieIsSigned(t *testing.T) {
	e := newEnv(t, "secret", 777)
	resp := e.login("secret")
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "bugbot_admin" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly {
		t.Fatalf("session cookie = %+v", cookie)
	}
	// Tamper with the admin id: the HMAC check must reject it.
	parts := strings.Split(cookie.Value, "|")
	if len(parts) != 3 {
		t.Fatalf("cookie = %q", cookie.Value)
	}
	forged := "999|" + parts[1] + "|" + parts[2]
	req := httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.AddCookie(&http.Cookie{Name: "bugbot_admin", Value: forged})
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("forged cookie accepted: %d", rec.Code)
	}
}

func TestPanelRequiresLogin(t *testing.T) {
	e := newEnv(t, "secret", 1)
	resp, _ := e.get("/admin/")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/admin/login") {
		t.Fatalf("panel without session = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestLoginAndTabsData(t *testing.T) {
	e := newEnv(t, "secret", 777)
	seedReport(t, e.st, 42)
	resp := e.login("secret")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasSuffix(resp.Header.Get("Location"), "/admin/") {
		t.Fatalf("login = %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = e.get("/admin/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("panel = %d", resp.StatusCode)
	}
	resp, body := e.get("/admin/api/reports")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api = %d %s", resp.StatusCode, body)
	}
	var data struct {
		Reports []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Media  []struct {
				URL string `json:"url"`
			} `json:"media"`
			User struct {
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"reports"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Reports) != 1 || data.Reports[0].ID != "BUG-1" || data.Reports[0].Status != "new" {
		t.Fatalf("reports = %+v", data.Reports)
	}
	if data.Reports[0].User.DisplayName != "سارا" || len(data.Reports[0].Media) != 1 {
		t.Fatalf("report payload = %+v", data.Reports[0])
	}
	resp, _ = e.get("/admin/static/app.js")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("static = %d", resp.StatusCode)
	}
}

func TestStatusWorkflow(t *testing.T) {
	e := newEnv(t, "secret", 777)
	e.login("secret")
	seedReport(t, e.st, 42)
	set := func(status string) (*http.Response, []byte) {
		return e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"`+status+`"}`)
	}

	// A new report is picked up for review first.
	resp, body := set("resolved")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("new → resolved must be refused, got %d %s", resp.StatusCode, body)
	}
	resp, body = set("review")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"review"`) {
		t.Fatalf("review = %d %s", resp.StatusCode, body)
	}

	// Invalid names and unknown reports.
	if resp, body := set("deleted"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status = %d %s", resp.StatusCode, body)
	}
	if resp, body := e.postJSON("/admin/api/reports/nope/status", `{"status":"review"}`); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing report = %d %s", resp.StatusCode, body)
	}

	// Reviewing can be closed either way…
	resp, body = set("resolved")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"resolved"`) {
		t.Fatalf("resolve = %d %s", resp.StatusCode, body)
	}
	// …and then the report is final: no reopening, no second decision.
	for _, next := range []string{"new", "review", "rejected"} {
		if resp, body := set(next); resp.StatusCode != http.StatusConflict {
			t.Fatalf("closed report accepted %q: %d %s", next, resp.StatusCode, body)
		}
	}

	// Tabs are filtered by status.
	_, body = e.get("/admin/api/reports?status=resolved")
	if !strings.Contains(string(body), `"BUG-1"`) {
		t.Fatalf("resolved tab = %s", body)
	}
	_, body = e.get("/admin/api/reports?status=review")
	if !strings.Contains(string(body), `"reports":[]`) {
		t.Fatalf("review tab = %s", body)
	}
}

func TestRejectStraightFromNew(t *testing.T) {
	e := newEnv(t, "secret", 777)
	e.login("secret")
	seedReport(t, e.st, 42)
	// Rejecting a fresh report is allowed, like the panel's "حذف گزارش" button.
	resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"rejected"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"rejected"`) {
		t.Fatalf("reject from new = %d %s", resp.StatusCode, body)
	}
	// And that is final.
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"review"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("closed report reopened: %d %s", resp.StatusCode, body)
	}
}

func TestReviewCanBeRejected(t *testing.T) {
	e := newEnv(t, "secret", 777)
	e.login("secret")
	seedReport(t, e.st, 42)
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"review"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("review = %d %s", resp.StatusCode, body)
	}
	resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"rejected"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"rejected"`) {
		t.Fatalf("reject = %d %s", resp.StatusCode, body)
	}
}

func TestChatMessages(t *testing.T) {
	e := newEnv(t, "secret", 777)
	e.login("secret")
	seedReport(t, e.st, 42)

	if resp, body := e.postJSON("/admin/api/reports/BUG-1/messages", `{"text":""}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty text = %d %s", resp.StatusCode, body)
	}
	// Chatting is only allowed while the report is under review.
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/messages", `{"text":"سلام"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("chat on a new report = %d %s", resp.StatusCode, body)
	}
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"review"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("review = %d %s", resp.StatusCode, body)
	}
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/messages", `{"text":"سلام"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("chat in review = %d %s", resp.StatusCode, body)
	}
	// Once decided, the conversation is closed for good.
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/status", `{"status":"resolved"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve = %d %s", resp.StatusCode, body)
	}
	if resp, body := e.postJSON("/admin/api/reports/BUG-1/messages", `{"text":"بعد از بسته شدن"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("chat on a resolved report = %d %s", resp.StatusCode, body)
	}

	ctx := context.Background()
	if _, err := e.st.AppendChat(ctx, domain.ChatMessage{ReportID: "BUG-1", Sender: domain.SenderUser, Text: "سلام"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.AppendChat(ctx, domain.ChatMessage{ReportID: "BUG-1", Sender: domain.SenderAdmin, AdminName: "تیم کت", Text: "در حال بررسی"}); err != nil {
		t.Fatal(err)
	}
	resp, body := e.get("/admin/api/reports/BUG-1/messages")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("messages = %d %s", resp.StatusCode, body)
	}
	var data struct {
		Messages []struct {
			Sender string `json:"sender"`
			Text   string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Messages) != 2 || data.Messages[0].Sender != "user" || data.Messages[1].Text != "در حال بررسی" {
		t.Fatalf("messages = %+v", data.Messages)
	}
}

func TestMediaProxy(t *testing.T) {
	e := newEnv(t, "secret", 777)
	seedReport(t, e.st, 42)
	resp, _ := e.get("/admin/api/reports/BUG-1/media/0")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("media without session = %d", resp.StatusCode)
	}
	e.login("secret")
	resp, body := e.get("/admin/api/reports/BUG-1/media/0")
	if resp.StatusCode != http.StatusOK || string(body) != "JPEGDATA" {
		t.Fatalf("media = %d %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %s", ct)
	}
	resp, _ = e.get("/admin/api/reports/BUG-1/media/9")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("out of range = %d", resp.StatusCode)
	}
}

// signInitData builds a valid Telegram.WebApp initData payload for tests.
func signInitData(t *testing.T, token string, userID int64, authDate time.Time, tamper bool) string {
	t.Helper()
	user := fmt.Sprintf(`{"id":%d,"first_name":"مدیر","username":"boss"}`, userID)
	params := url.Values{
		"auth_date": {strconv.FormatInt(authDate.Unix(), 10)},
		"query_id":  {"AAH"},
		"user":      {user},
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(parts, "\n")))
	params.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	if tamper {
		params.Set("user", fmt.Sprintf(`{"id":%d}`, userID+1))
	}
	return params.Encode()
}

func TestMiniAppSessionOnlyForAdmins(t *testing.T) {
	const token = "123:ABC"
	const adminID = int64(777)
	e := newEnvWithOpts(t, web.Options{BotToken: token, Password: "secret", AdminIDs: []int64{adminID}, DB: nil})

	post := func(initData string) (*http.Response, []byte) {
		return e.postJSON("/admin/miniapp/session", fmt.Sprintf(`{"initData":%q}`, initData))
	}

	resp, body := post(signInitData(t, token, adminID, time.Now(), false))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin session = %d %s", resp.StatusCode, body)
	}
	// The exchanged cookie authenticates the panel API.
	if resp, body := e.get("/admin/api/reports"); resp.StatusCode != http.StatusOK {
		t.Fatalf("api with mini app session = %d %s", resp.StatusCode, body)
	}

	resp, body = post(signInitData(t, token, 999, time.Now(), false))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin = %d %s", resp.StatusCode, body)
	}

	resp, body = post(signInitData(t, token, adminID, time.Now(), true))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tampered payload = %d %s", resp.StatusCode, body)
	}

	resp, body = post(signInitData(t, "wrong-token", adminID, time.Now(), false))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token signature = %d %s", resp.StatusCode, body)
	}

	resp, body = post(signInitData(t, token, adminID, time.Now().Add(-48*time.Hour), false))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale init data = %d %s", resp.StatusCode, body)
	}

	resp, body = post("")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("empty init data = %d %s", resp.StatusCode, body)
	}
}

func TestTelegramWebAppScript(t *testing.T) {
	e := newEnv(t, "secret", 777)
	resp, body := e.get("/telegram-web-app.js")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Fatalf("script = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), "window.Telegram") {
		t.Fatalf("unexpected script body")
	}
}

func TestHeaderAloneIsNotEnough(t *testing.T) {
	e := newEnv(t, "secret", 777)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.Header.Set("X-Cat-User-Id", "777")
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a user id header without proof must be rejected, got %d", rec.Code)
	}

	// Unknown ids are rejected even with valid init data.
	req = httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.Header.Set("X-Cat-User-Id", "123456")
	req.Header.Set("X-Cat-Init-Data", signInitData(t, "tok", 123456, time.Now(), false))
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin header got %d", rec.Code)
	}
}

func TestHeaderWithTelegramProof(t *testing.T) {
	const token = "123:ABC"
	const adminID = int64(777)
	e := newEnvWithOpts(t, web.Options{BotToken: token, Password: "secret", AdminIDs: []int64{adminID}})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.Header.Set("X-Cat-User-Id", "777")
	req.Header.Set("X-Cat-Init-Data", signInitData(t, token, adminID, time.Now(), false))
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed header = %d %s", rec.Code, rec.Body.String())
	}

	// The proof must belong to the same user.
	req = httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.Header.Set("X-Cat-User-Id", "777")
	req.Header.Set("X-Cat-Init-Data", signInitData(t, token, 888, time.Now(), false))
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched proof got %d", rec.Code)
	}
}

func TestHeaderTrustedThroughAllowlistedEdge(t *testing.T) {
	e := newEnvWithOpts(t, web.Options{
		Password:           "secret",
		AdminIDs:           []int64{777},
		EnforceIPAllowlist: true,
		AllowedIPs:         []string{"127.0.0.1"},
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/api/reports", nil)
	req.RemoteAddr = "127.0.0.1:5555" // the edge/proxy the allowlist trusts
	req.Header.Set("X-Cat-User-Id", "777")
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlisted edge = %d %s", rec.Code, rec.Body.String())
	}

	// Outside the allowlist the same header is refused.
	req.RemoteAddr = "192.0.2.99:5555"
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unknown edge = %d", rec.Code)
	}
}
