package cli

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"trustpanel/internal/core/bot"
	"trustpanel/internal/core/store"
)

// RunBot runs the operator Telegram management bot. It lives on the active
// exit beside the panel and Postgres and talks to the store directly over
// localhost. Access is limited to --admins.
func RunBot(args []string) {
	fs := flag.NewFlagSet("bot", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (or set TRUSTPANEL_DSN)")
	tokenFile := fs.String("token-file", "/etc/trustpanel/telegram.token", "file with the Telegram bot token")
	apiBase := fs.String("telegram-api", "", "override Telegram API base URL (testing)")
	_ = fs.Parse(args)

	dbDSN := connDSN(*dsn)
	if dbDSN == "" {
		log.Fatal("bot: --dsn, TRUSTPANEL_DSN, or /etc/trustpanel/serve.env is required")
	}
	// The token is now primarily managed in the panel (Bots tab) and read from the
	// DB at runtime; the flag/file value is an optional fallback. Who may use the
	// bot is decided per-account by the Telegram binding (Account tab), not a flat
	// allowlist. The bot idles harmlessly until a token exists, so missing flags
	// are not fatal.
	token, _ := readToken(*tokenFile)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, dbDSN)
	if err != nil {
		log.Fatalf("bot: open store: %v", err)
	}
	defer st.Close()

	b := bot.New(st, token, *apiBase)
	b.Run(ctx)
}

func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
