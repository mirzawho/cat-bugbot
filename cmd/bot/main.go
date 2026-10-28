package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bugbot/internal/bot"
	"bugbot/internal/config"
	"bugbot/internal/store"
	"bugbot/internal/telegram"
	"bugbot/internal/web"
)

// loadDotEnv fills unset variables from a KEY=VALUE file so `./bin/bugbot`
// works without exporting the environment first. Real environment variables
// always win.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

// registerCommands fills the bot's command menu (the "☰" list in chats).
// /panel is only published in each admin's own chat: everybody else never sees
// the command, and sending it by hand gets no reply.
func registerCommands(ctx context.Context, api *telegram.Client, admins []int64) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	public := []telegram.BotCommand{
		{Command: "report", Description: "ثبت گزارش باگ"},
		{Command: "cancel", Description: "لغو یا پایان گفت‌وگو"},
	}
	if err := api.SetMyCommands(rctx, map[string]any{"commands": public}); err != nil {
		log.Printf("register commands: %v", err)
		return
	}
	admin := append(append([]telegram.BotCommand{}, public...), telegram.BotCommand{Command: "panel", Description: "پنل مدیریت"})
	for _, id := range admins {
		scope := map[string]any{"type": "chat", "chat_id": id}
		if err := api.SetMyCommands(rctx, map[string]any{"commands": admin, "scope": scope}); err != nil {
			log.Printf("register admin commands for %d: %v", id, err)
		}
	}
}

// healthcheck supports the container HEALTHCHECK directive (scratch image has no shell/curl).
func healthcheck() bool {
	flag := len(os.Args) > 1 && os.Args[1] == "-healthcheck"
	if !flag {
		return false
	}
	addr := os.Getenv("HEALTH_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s/healthz", addr))
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	resp.Body.Close()
	return true
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if p, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		// Look next to the binary (bin/.env), then the project root above it, so
		// the bot finds its settings no matter the current directory.
		loadDotEnv(filepath.Join(filepath.Dir(p), ".env"))
		loadDotEnv(filepath.Join(filepath.Dir(p), "..", ".env"))
		if !strings.Contains(p, string(filepath.Separator)+"go-build") {
			// Only an installed binary searches the current directory too; during
			// `go test` that would be the package folder.
			loadDotEnv(".env")
		}
	}
	if v := os.Getenv("DOTENV_PATH"); v != "" {
		loadDotEnv(v)
	}
	if healthcheck() {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}

	api := telegram.NewClientWithBase(cfg.TelegramAPI, cfg.BotToken)
	poller := &bot.Poller{
		API:     api,
		Timeout: cfg.PollTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	handler := bot.New(api, st, cfg.IsAdmin)
	panelPublicURL := cfg.ServiceURL
	if panelPublicURL == "" {
		panelPublicURL = "http://localhost" + cfg.HealthAddr
	}
	handler.SetPanelURL(panelPublicURL)
	if cfg.ReportChatID != 0 {
		handler.SetReportChat(cfg.ReportChatID)
		log.Printf("report media will be archived in chat %d", cfg.ReportChatID)
	}
	poller.Handler = handler

	mux := http.NewServeMux()
	pan, err := web.New(web.Options{
		BotToken:           cfg.BotToken,
		Password:           cfg.AdminPanelPassword,
		AdminIDs:           cfg.Admins,
		DB:                 st,
		Hooks:              handler,
		Telegram:           api,
		Lookups:            api,
		ServiceURL:         cfg.ServiceURL,
		EnforceIPAllowlist: cfg.EnforceAdminIPAllowlist,
		AllowedIPs:         cfg.AdminAllowedIPs,
	})
	if err != nil {
		log.Fatalf("admin panel: %v", err)
	}
	mux.Handle("/admin/", pan.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		hctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Ping(hctx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: cfg.HealthAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("health server: %v", err)
		}
	}()

	go registerCommands(ctx, api, cfg.Admins)

	pollErr := make(chan error, 1)
	go func() { pollErr <- poller.Run(ctx) }()

	log.Printf("BugBot is running (db: %s, listen: %s, telegram api: %s)", cfg.DatabasePath, cfg.HealthAddr, cfg.TelegramAPI)

	select {
	case <-ctx.Done():
		log.Println("shutdown signal received")
	case err := <-pollErr:
		if err != nil {
			log.Printf("poller stopped: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if err := st.Close(); err != nil {
		log.Printf("close database: %v", err)
	}
	log.Println("goodbye")
}
