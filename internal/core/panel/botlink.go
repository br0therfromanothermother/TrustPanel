package panel

import (
	"context"
	"log"
	"strings"
	"sync"

	"trustpanel/internal/core/watchdog"
)

// An alert is about one record, and what to do about it is on that record's
// card. Without an address for the card the reader has to go and find it by
// hand (open the bot, find the list, page to the client) which at the moment
// an alert arrives is the part that does not happen.
//
// Telegram's deep link is the address: t.me/<bot>?start=<id> opens the
// management bot on that record (see openDeepLink in the bot package). It needs
// the bot's username, which only Telegram can say, so it is asked once per
// token and remembered.
type botNames struct {
	mu    sync.Mutex
	token string // the token the name was resolved for
	name  string
}

// botLink is the link that opens one record in the management bot, or "" when
// there is no bot to open, in which case the alert simply carries no link
// rather than a broken one.
func (p *Panel) botLink(ctx context.Context, payload string) string {
	if payload == "" {
		return ""
	}
	s, err := p.store.GetSettings(ctx)
	if err != nil || !s.Bot.Enabled || strings.TrimSpace(s.Bot.Token) == "" {
		return ""
	}
	token := s.Bot.Token

	p.botName.mu.Lock()
	name, known := p.botName.name, p.botName.token == token && p.botName.name != ""
	p.botName.mu.Unlock()

	if !known {
		got, err := watchdog.BotUsername(ctx, token, nil)
		if err != nil {
			// Telegram unreachable, or the token is not a bot's. Neither is worth
			// failing an alert over; it goes out without the link.
			log.Printf("bot link: %v", err)
			return ""
		}
		p.botName.mu.Lock()
		p.botName.token, p.botName.name = token, got
		p.botName.mu.Unlock()
		name = got
	}
	return "https://t.me/" + name + "?start=" + payload
}
