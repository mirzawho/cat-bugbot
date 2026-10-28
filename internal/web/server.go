package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bugbot/internal/domain"
	"bugbot/internal/store"
	"bugbot/internal/telegram"
)

//go:embed templates/*.html static/*
var content embed.FS

const sessionCookie = "bugbot_admin"
const sessionTTL = 7 * 24 * time.Hour

// BotHooks is the Telegram side of the admin workflow (implemented by bot.Handler).
type BotHooks interface {
	NotifyResolved(ctx context.Context, reportID string) error
	NotifyRejected(ctx context.Context, reportID string) error
	SendChat(ctx context.Context, reportID, adminName, text string) (domain.ChatMessage, error)
}

type Options struct {
	// BotToken is the same token the bot uses; it authenticates Mini App
	// initData so only ADMINS can open the panel from inside Telegram.
	BotToken   string
	Password   string
	AdminIDs   []int64
	DB         *store.Store
	Hooks      BotHooks
	Telegram   telegram.API
	ServiceURL string
	// Lookups, when set, are used for live reporter profiles.
	Lookups telegram.API
	// AutoLoginID, when non-zero, is the admin identity used when the panel
	// password is empty (local development only).
	AutoLoginID int64
	// EnforceIPAllowlist only accepts requests from the given remote IPs (used
	// for Telegram Mini Apps, whose webview may drop the session cookie).
	EnforceIPAllowlist bool
	// Allowlist interoperates with Cloudflare (CF-Connecting-IP).
	AllowedIPs []string
}

type Server struct {
	opts     Options
	secret   []byte
	adminIDs map[int64]bool
	tmpl     *template.Template
	profiles profileCache
}

func New(opts Options) (*Server, error) {
	if opts.DB == nil || opts.Telegram == nil {
		return nil, errors.New("web: db and telegram client are required")
	}
	if opts.Password == "" && len(opts.AdminIDs) > 0 && opts.AutoLoginID == 0 {
		return nil, errors.New("web: admin panel password is not set")
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, id := range opts.AdminIDs {
		ids[id] = true
	}
	srv := &Server{opts: opts, secret: secret, adminIDs: ids, tmpl: tmpl}
	srv.profiles = profileCache{entries: map[int64]profileEntry{}}
	return srv, nil
}

func parseTemplates() (*template.Template, error) {
	return template.ParseFS(content, "templates/*.html")
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	statics, _ := fs.Sub(content, "static")
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", http.FileServer(http.FS(statics))))
	// Mini Apps are opened inside Telegram; a plain server-render is enough.
	mux.Handle("GET /telegram-web-app.js", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = io.WriteString(w, telegramWebAppJS)
	}))

	mux.HandleFunc("GET /admin/login", s.loginPage)
	mux.HandleFunc("POST /admin/login", s.login)
	mux.HandleFunc("POST /admin/logout", s.logout)
	mux.HandleFunc("POST /admin/miniapp/session", s.miniAppSession)
	mux.Handle("GET /admin/{$}", s.authed(http.HandlerFunc(s.panel)))
	mux.Handle("GET /admin/api/reports", s.authed(http.HandlerFunc(s.listReports)))
	mux.Handle("POST /admin/api/reports/{id}/status", s.authed(http.HandlerFunc(s.setStatus)))
	mux.Handle("GET /admin/api/reports/{id}/messages", s.authed(http.HandlerFunc(s.listMessages)))
	mux.Handle("POST /admin/api/reports/{id}/messages", s.authed(http.HandlerFunc(s.postMessage)))
	mux.Handle("GET /admin/api/reports/{id}/media/{index}", s.authed(http.HandlerFunc(s.media)))
	mux.Handle("GET /admin/api/users/{id}/photo/{index}", s.authed(http.HandlerFunc(s.profilePhoto)))
	return s.allowlist(mux)
}

func (s *Server) allowlist(next http.Handler) http.Handler {
	if !s.opts.EnforceIPAllowlist {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ipAllowed(r, s.opts.AllowedIPs) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ipAllowed(r *http.Request, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	ip := clientIP(r)
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == ip || (a == "localhost" && (ip == "127.0.0.1" || ip == "::1")) {
			return true
		}
	}
	return false
}

// clientIP prefers the edge header Cloudflare sets, then the TCP peer.
func clientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
		return strings.TrimSpace(cf)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// MediaURL is the same-origin path the panel uses for a report's media.
func MediaURL(reportID string, index int) string {
	return "/admin/api/reports/" + reportID + "/media/" + strconv.Itoa(index)
}

// IsAdmin reports whether a Telegram user may use the panel.
func (s *Server) IsAdmin(id int64) bool { return s.adminIDs[id] }

// --- sessions (HMAC-signed cookie: adminID|expiry|signature) ---

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) issueCookie(w http.ResponseWriter, id int64) {
	exp := time.Now().Add(sessionTTL).Unix()
	payload := fmt.Sprintf("%d|%d", id, exp)
	v := payload + "|" + s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    v,
		Path:     "/admin",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(s.opts.ServiceURL, "https://"),
		Expires:  time.Unix(exp, 0),
	})
}

// session resolves the acting admin from, in order:
//  1. the signed session cookie (browser login);
//  2. the X-Cat-User-Id header. The header alone is not a credential: it is
//     only trusted from a server the admin also proves (the
//     X-Cat-Init-Data header, verified with the bot token), or from the
//     deployment's own edge (ADMIN_PANEL_IP_ALLOWLIST_ENFORCED with allowed IPs).
func (s *Server) session(r *http.Request) (int64, bool) {
	if id, ok := s.sessionFromCookie(r); ok {
		return id, true
	}
	if id, ok := s.sessionFromHeader(r); ok {
		return id, true
	}
	if s.opts.Password == "" && s.opts.AutoLoginID != 0 {
		return s.opts.AutoLoginID, true
	}
	return 0, false
}

// sessionFromHeader authenticates X-Cat-User-Id for API calls made outside a
// browser (native clients/tests) and for Mini App calls.
func (s *Server) sessionFromHeader(r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("X-Cat-User-Id"))
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || !s.adminIDs[id] {
		return 0, false
	}
	if initData := r.Header.Get("X-Cat-Init-Data"); initData != "" {
		tu, err := VerifyInitData(initData, s.opts.BotToken)
		if err != nil || tu.ID != id {
			return 0, false
		}
		return id, true
	}
	if s.trustingProxy() {
		return id, true
	}
	return 0, false
}

// trustingProxy reports whether the request comes through the deployment's edge
// exactly once (direct client IP listed). On this path the edge is expected to
// strip client-supplied X-Cat-User-Id headers.
func (s *Server) trustingProxy() bool {
	return s.opts.EnforceIPAllowlist && len(s.opts.AllowedIPs) > 0
}

func (s *Server) sessionFromCookie(r *http.Request) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	parts := strings.Split(c.Value, "|")
	if len(parts) != 3 {
		return 0, false
	}
	payload := parts[0] + "|" + parts[1]
	if subtle.ConstantTimeCompare([]byte(s.sign(payload)), []byte(parts[2])) != 1 {
		return 0, false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || !s.adminIDs[id] {
		return 0, false
	}
	return id, true
}

func (s *Server) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.EnforceIPAllowlist && !ipAllowed(r, s.opts.AllowedIPs) {
			writeErr(w, http.StatusForbidden, "دسترسی از این آدرس مجاز نیست")
			return
		}
		id, ok := s.session(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/admin/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminIDKey{}, id)))
	})
}

type adminIDKey struct{}

func adminFrom(r *http.Request) int64 {
	id, _ := r.Context().Value(adminIDKey{}).(int64)
	return id
}

// --- pages ---

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.session(r); ok {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", map[string]any{"Error": r.URL.Query().Get("error")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	given := r.PostFormValue("token")
	if s.opts.Password == "" || subtle.ConstantTimeCompare([]byte(given), []byte(s.opts.Password)) != 1 {
		http.Redirect(w, r, "/admin/login?error="+"نام+کاربری+یا+رمز+عبور+نامعتبر+است", http.StatusSeeOther)
		return
	}
	id := s.opts.AutoLoginID
	if id == 0 && len(s.opts.AdminIDs) > 0 {
		id = s.opts.AdminIDs[0]
	}
	s.issueCookie(w, id)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/admin", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) panel(w http.ResponseWriter, r *http.Request) {
	name := "مدیر کت"
	if id := adminFrom(r); id != 0 {
		name = "مدیر " + strconv.FormatInt(id, 10)
	}
	s.render(w, "index.html", map[string]any{"AdminName": name})
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
	}
}

// --- API ---

type userDTO struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DisplayName string `json:"display_name"`
	PhotoURL    string `json:"photo_url,omitempty"`
}

type mediaDTO struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type reportDTO struct {
	ID          string     `json:"id"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	User        userDTO    `json:"user"`
	Media       []mediaDTO `json:"media"`
}

func (s *Server) toDTO(r domain.Report) reportDTO {
	d := reportDTO{
		ID:          r.ID,
		Category:    r.Category,
		Description: r.Description,
		Status:      string(r.Status),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
		ResolvedAt:  r.ResolvedAt,
		User: userDTO{
			ID:          r.User.ID,
			Username:    r.User.Username,
			FirstName:   r.User.FirstName,
			LastName:    r.User.LastName,
			DisplayName: r.User.DisplayName(),
		},
		Media: make([]mediaDTO, 0, len(r.Media)),
	}
	for i, m := range r.Media {
		d.Media = append(d.Media, mediaDTO{Type: string(m.Type), URL: MediaURL(r.ID, i)})
	}
	return d
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	status := domain.ReportStatus(r.URL.Query().Get("status"))
	if status != "" && !status.Valid() {
		writeErr(w, http.StatusBadRequest, "وضعیت نامعتبر است")
		return
	}
	reports, err := s.opts.DB.ListReports(r.Context(), status)
	if err != nil {
		log.Printf("web: list reports: %v", err)
		writeErr(w, http.StatusInternalServerError, "خطا در خواندن گزارش‌ها")
		return
	}
	out := make([]reportDTO, 0, len(reports))
	for _, rep := range reports {
		d := s.toDTO(rep)
		p, ok := s.getProfile(r.Context(), rep.User.ID)
		d.User.DisplayName = displayedName(rep.User.FirstName, rep.User.LastName, rep.User.Username, p, ok)
		if ok && len(p.Photos) > 0 {
			d.User.PhotoURL = "/admin/api/users/" + strconv.FormatInt(rep.User.ID, 10) + "/photo/0"
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": out})
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "درخواست نامعتبر است")
		return
	}
	status := domain.ReportStatus(body.Status)
	if !status.Valid() {
		writeErr(w, http.StatusBadRequest, "وضعیت نامعتبر است")
		return
	}
	rep, err := s.opts.DB.SetReportStatus(r.Context(), r.PathValue("id"), status)
	if errors.Is(err, store.ErrReportNotFound) {
		writeErr(w, http.StatusNotFound, "گزارش پیدا نشد")
		return
	}
	if errors.Is(err, store.ErrStatusNotAllowed) {
		writeErr(w, http.StatusConflict, "این تغییر وضعیت مجاز نیست؛ گزارش بسته شده است.")
		return
	}
	if err != nil {
		log.Printf("web: set status: %v", err)
		writeErr(w, http.StatusInternalServerError, "خطا در تغییر وضعیت")
		return
	}
	if status == domain.StatusResolved || status == domain.StatusRejected {
		// A decided report is closed: stop the conversation on the Telegram side too.
		if _, err := s.opts.DB.CloseChat(r.Context(), rep.User.ID); err != nil {
			log.Printf("web: close chat for %s: %v", rep.ID, err)
		}
	}
	// The bot notification is best effort: the report state is already saved,
	// and the bot may briefly be offline.
	if s.opts.Hooks != nil {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		switch status {
		case domain.StatusResolved:
			if err := s.opts.Hooks.NotifyResolved(notifyCtx, rep.ID); err != nil {
				log.Printf("web: notify resolved %s: %v", rep.ID, err)
			}
		case domain.StatusRejected:
			if err := s.opts.Hooks.NotifyRejected(notifyCtx, rep.ID); err != nil {
				log.Printf("web: notify rejected %s: %v", rep.ID, err)
			}
		}
		cancel()
	}
	writeJSON(w, http.StatusOK, s.toDTO(rep))
}

type chatDTO struct {
	ID        int64     `json:"id"`
	Sender    string    `json:"sender"`
	AdminName string    `json:"admin_name,omitempty"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	if _, err := s.opts.DB.Report(r.Context(), r.PathValue("id")); errors.Is(err, store.ErrReportNotFound) {
		writeErr(w, http.StatusNotFound, "گزارش پیدا نشد")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "خطا در خواندن گزارش")
		return
	}
	msgs, err := s.opts.DB.ChatMessages(r.Context(), r.PathValue("id"))
	if err != nil {
		log.Printf("web: chat messages: %v", err)
		writeErr(w, http.StatusInternalServerError, "خطا در خواندن پیام‌ها")
		return
	}
	out := make([]chatDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, chatDTO{ID: m.ID, Sender: string(m.Sender), AdminName: m.AdminName, Text: m.Text, CreatedAt: m.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "درخواست نامعتبر است")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len([]rune(text)) > 2000 {
		writeErr(w, http.StatusBadRequest, "متن پیام باید بین ۱ تا ۲۰۰۰ کاراکتر باشد")
		return
	}
	if s.opts.Hooks == nil {
		writeErr(w, http.StatusServiceUnavailable, "امکان ارسال پیام نیست")
		return
	}
	id := r.PathValue("id")
	rep, err := s.opts.DB.Report(r.Context(), id)
	if errors.Is(err, store.ErrReportNotFound) {
		writeErr(w, http.StatusNotFound, "گزارش پیدا نشد")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "خطا در خواندن گزارش")
		return
	}
	if rep.Status != domain.StatusReview {
		// Chatting belongs to an open report: start reviewing it first.
		writeErr(w, http.StatusConflict, "برای چت با گزارش‌دهنده ابتدا گزارش را بررسی کنید؛ گزارش‌های بسته‌شده چت ندارند.")
		return
	}
	adminName := "تیم کت"
	_, err = s.opts.Hooks.SendChat(r.Context(), id, adminName, text)
	if errors.Is(err, store.ErrReportNotFound) {
		writeErr(w, http.StatusNotFound, "گزارش پیدا نشد")
		return
	}
	if err != nil {
		log.Printf("web: send chat %s: %v", id, err)
		writeErr(w, http.StatusBadGateway, "ارسال پیام به کاربر ناموفق بود")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	rep, err := s.opts.DB.Report(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrReportNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= len(rep.Media) {
		http.NotFound(w, r)
		return
	}
	f, err := s.opts.Telegram.GetFile(r.Context(), rep.Media[index].FileID)
	if errors.Is(err, telegram.ErrFileIDInvalid) {
		// The file was received by a different bot (e.g. before the token changed).
		log.Printf("web: file_id of report %s is not valid for the current bot (stale row from an earlier bot/database)", rep.ID)
		http.Error(w, "این فایل متعلق به ربات فعلی نیست (file_id نامعتبر). گزارش‌های قدیمی دیتابیس یا توکن قبلی قابل نمایش نیستند؛ با همین ربات گزارش جدید بفرستید.", http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		log.Printf("web: getFile for report %s (%s file_id %q): %v", rep.ID, rep.Media[index].Type, rep.Media[index].FileID, err)
		http.Error(w, "getFile failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if f.FilePath == "" {
		log.Printf("web: getFile for report %s returned no file_path", rep.ID)
		http.Error(w, "getFile returned no file_path", http.StatusBadGateway)
		return
	}
	raw, err := s.opts.Telegram.DownloadFile(r.Context(), f.FilePath)
	if err != nil {
		log.Printf("web: download %s: %v", f.FilePath, err)
		http.Error(w, "download failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", mediaType(rep.Media[index].Type, f.FilePath))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(raw)
}

func mediaType(kind domain.MediaType, path string) string {
	if kind == domain.MediaVideo {
		switch {
		case strings.HasSuffix(path, ".webm"):
			return "video/webm"
		case strings.HasSuffix(path, ".mov"):
			return "video/quicktime"
		default:
			return "video/mp4"
		}
	}
	if strings.HasSuffix(path, ".png") {
		return "image/png"
	}
	return "image/jpeg"
}
