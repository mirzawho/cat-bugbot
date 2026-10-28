package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken     string
	DatabasePath string
	HealthAddr   string
	TelegramAPI  string
	PollTimeout  time.Duration
	// Admins are Telegram user IDs allowed into the web panel and the in-bot
	// report workflow. ADMINS="12345,67890".
	Admins []int64
	// AdminPanelPassword is required to open the web panel.
	AdminPanelPassword string
	// ServiceURL is the public address of this service, used to show media in
	// the web panel (the bot token never leaves the server).
	ServiceURL string
	// EnforceAdminIPAllowlist limits the panel to the deployment's egress IPs
	// (Telegram Mini Apps may drop cookies inside their webview).
	EnforceAdminIPAllowlist bool
	AdminAllowedIPs         []string
	// ReportChatID is the chat file IDs for report media are negotiated with
	// (a group/channel the bot can post to), so the panel can always load them.
	ReportChatID int64
	// Warnings is populated (not an error) for settings that look wrong but
	// are still usable, e.g. an API base with a path.
	Warnings []string
}

func Load() (Config, error) {
	cfg := Config{
		BotToken:                os.Getenv("BOT_TOKEN"),
		DatabasePath:            envOr("DATABASE_PATH", "./data/bugbot.db"),
		HealthAddr:              envOr("HEALTH_ADDR", ":8080"),
		TelegramAPI:             telegramAPIBase(os.Getenv("TELEGRAM_API_BASE")),
		PollTimeout:             30 * time.Second,
		AdminPanelPassword:      os.Getenv("ADMIN_PANEL_PASSWORD"),
		ServiceURL:              strings.TrimSuffix(envOr("SERVICE_URL", ""), "/"),
		EnforceAdminIPAllowlist: envOr("ADMIN_PANEL_IP_ALLOWLIST_ENFORCED", "false") == "true",
		AdminAllowedIPs:         splitList(os.Getenv("ADMIN_PANEL_ALLOWED_IPS")),
		ReportChatID:            parseIntOrZero(os.Getenv("REPORT_CHAT_ID")),
	}
	if cfg.BotToken == "" {
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	}
	ids, err := parseIDs(os.Getenv("ADMINS"))
	if err != nil {
		return Config{}, fmt.Errorf("ADMINS: %w", err)
	}
	cfg.Admins = ids
	if w := warnAPIBase(os.Getenv("TELEGRAM_API_BASE"), cfg.TelegramAPI); w != "" {
		cfg.Warnings = append(cfg.Warnings, w)
	}
	if len(ids) > 0 && cfg.AdminPanelPassword == "" {
		return Config{}, fmt.Errorf("ADMIN_PANEL_PASSWORD is required when ADMINS is set")
	}
	return cfg, nil
}

// parseIDs accepts "1,2, 3" or space separated numbers.
func parseIDs(raw string) ([]int64, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	out := make([]int64, 0, len(fields))
	seen := map[int64]bool{}
	for _, f := range fields {
		f = strings.Trim(f, `"'`) // tolerate quoted values from .env files
		id, err := strconv.ParseInt(f, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid Telegram user ID %q", f)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

const officialTelegramAPI = "https://api.telegram.org"

// telegramAPIBase is the base URL the bot talks to. It expects a Bot API
// server root (a proxy/mirror of Telegram): requests go to
// base + "/bot<token>/<method>" and files to base + "/file/bot<token>/<path>".
func telegramAPIBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return officialTelegramAPI
	}
	return strings.TrimSuffix(raw, "/")
}

// warnAPIBase flags values that do not look like a Bot API root, e.g. an
// address with a path (https://example.com/telegram) which the bot would
// then extend to /telegram/bot<token>/getUpdates.
func warnAPIBase(raw, resolved string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || resolved == officialTelegramAPI {
		return ""
	}
	if u, err := url.Parse(resolved); err != nil || u.Path != "" && u.Path != "/" {
		return fmt.Sprintf("TELEGRAM_API_BASE=%q: the bot calls <base>/bot<token>/getUpdates, so <base> must be the server root (e.g. https://cr0.ir, not https://cr0.ir/telegram)", raw)
	}
	return ""
}

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.TrimSpace(f))
	}
	return out
}

func parseIntOrZero(raw string) int64 {
	raw = strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), `"'`))
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// IsAdmin reports whether the Telegram user may use the admin features.
func (c Config) IsAdmin(id int64) bool {
	for _, a := range c.Admins {
		if a == id {
			return true
		}
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
