package bot_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"bugbot/internal/bot"
	"bugbot/internal/telegram"
)

func TestPollerProcessesDrainedUpdates(t *testing.T) {
	e := newEnv(t)
	e.api.updates = []telegram.Update{
		{UpdateID: 10, Message: &telegram.Message{
			MessageID: 1,
			From:      &telegram.User{ID: testUserID, FirstName: "علی"},
			Chat:      telegram.Chat{ID: testUserID, Type: "private"},
			Text:      "/start",
		}},
	}
	p := &bot.Poller{API: e.api, Handler: e.h, Timeout: 0}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = p.Run(ctx)
	if len(e.api.sent) != 1 || !strings.Contains(e.api.sent[0].Text, "خوش آمدی") {
		t.Fatalf("sent = %+v", e.api.sent)
	}
}
