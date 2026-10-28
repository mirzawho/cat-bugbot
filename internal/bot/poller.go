package bot

import (
	"context"
	"errors"
	"log"
	"time"

	"bugbot/internal/telegram"
)

type Poller struct {
	API     telegram.API
	Handler *Handler
	Timeout time.Duration
}

func (p *Poller) Run(ctx context.Context) error {
	var offset int64
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		updates, err := p.API.GetUpdates(ctx, offset, p.Timeout)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			log.Printf("getUpdates: %v (retrying in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			p.Handler.HandleUpdate(ctx, u)
		}
	}
}
