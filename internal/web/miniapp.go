package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// miniAppMaxAge is how old a WebApp initData payload may be. Telegram recommends
// rejecting stale payloads; one day is generous for an admin opening a link.
const miniAppMaxAge = 24 * time.Hour

// TelegramWebApp is the subset of window.Telegram.WebApp the panel uses.
// It lets the page work both inside a Mini App and in a normal browser.
const telegramWebAppJS = `(function () {
  if (window.Telegram && window.Telegram.WebApp) return;
  var tg = window.Telegram || (window.Telegram = {});
  var app = tg.WebApp = tg.WebApp || {};
  app.initData = "";
  app.initDataUnsafe = app.initDataUnsafe || {};
  app.ready = function () {};
  app.expand = function () {};
  app.disableVerticalSwipes = function () {};
  app.close = function () { if (window.history.length > 1) window.history.back(); };
})();`

// VerifyInitData validates Telegram.WebApp.initData:
//
//	hash = HMAC_SHA256(key=HMAC_SHA256("WebAppData", botToken), data_check_string)
//
// and returns the authenticated user (and auth_date).
func VerifyInitData(initData, botToken string) (telegramUser, error) {
	if initData == "" || botToken == "" {
		return telegramUser{}, errors.New("empty init data")
	}
	params, err := url.ParseQuery(initData)
	if err != nil {
		return telegramUser{}, errors.New("malformed init data")
	}
	gotHash := params.Get("hash")
	if gotHash == "" {
		return telegramUser{}, errors.New("missing hash")
	}
	params.Del("hash")
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(params.Get(k))
	}

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(b.String()))
	want := hex.EncodeToString(mac.Sum(nil))
	if !subtleEqualHex(want, gotHash) {
		return telegramUser{}, errors.New("invalid hash")
	}

	authDate, _ := strconv.ParseInt(params.Get("auth_date"), 10, 64)
	if authDate == 0 || time.Since(time.Unix(authDate, 0)) > miniAppMaxAge {
		return telegramUser{}, errors.New("init data expired")
	}

	var u telegramUser
	if raw := params.Get("user"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &u); err != nil {
			return telegramUser{}, errors.New("invalid user payload")
		}
	}
	if u.ID == 0 {
		return telegramUser{}, errors.New("no user in init data")
	}
	return u, nil
}

func subtleEqualHex(a, b string) bool {
	ab, err1 := hex.DecodeString(a)
	bb, err2 := hex.DecodeString(b)
	if err1 != nil || err2 != nil || len(ab) != len(bb) {
		return false
	}
	return hmac.Equal(ab, bb)
}

type telegramUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// miniAppSession exchanges a Mini App initData payload for the normal panel
// session cookie, but only for users listed in ADMINS.
func (s *Server) miniAppSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InitData string `json:"initData"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "درخواست نامعتبر است")
		return
	}
	tu, err := VerifyInitData(body.InitData, s.opts.BotToken)
	if err != nil {
		log.Printf("web: miniapp init data rejected: %v", err)
		writeErr(w, http.StatusUnauthorized, "احراز هویت تلگرام نامعتبر است")
		return
	}
	if !s.IsAdmin(tu.ID) {
		writeErr(w, http.StatusForbidden, "شما مدیر کت نیستید")
		return
	}
	s.issueCookie(w, tu.ID)
	name := tu.FirstName
	if name == "" {
		name = tu.Username
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": tu.ID, "name": name})
}
