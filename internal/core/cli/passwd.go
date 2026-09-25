package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/panel"
	"trustpanel/internal/core/store"
)

// RunPasswd resets a panel account's password from the box itself. It is the
// break-glass path under every other one: it needs no bot, no Telegram and no
// working login, only root on the control plane and the database it already
// talks to. Use it when the Telegram binding is gone, the bot token is dead, or
// the account that could recover has been lost.
func RunPasswd(args []string) {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	username := fs.String("username", "", "panel account to reset (required)")
	password := fs.String("password", "", "new password (min 8 chars; default: generate one and print it)")
	dsn := fs.String("dsn", "", "Postgres DSN (or set TRUSTPANEL_DSN, or /etc/trustpanel/serve.env)")
	_ = fs.Parse(args)

	if strings.TrimSpace(*username) == "" {
		log.Fatal("passwd: --username is required")
	}
	dbDSN := connDSN(*dsn)
	if dbDSN == "" {
		log.Fatal("passwd: --dsn, TRUSTPANEL_DSN, or /etc/trustpanel/serve.env is required")
	}

	ctx := context.Background()
	st, err := store.Open(ctx, dbDSN)
	if err != nil {
		log.Fatalf("passwd: open store: %v", err)
	}
	defer st.Close()

	// Refuse to invent an account: a typo in --username must not quietly mint a
	// second admin with a password the operator just chose.
	acct, err := st.AdminByUsername(ctx, *username)
	if err != nil {
		if errors.Is(err, store.ErrNoAdmin) {
			log.Fatalf("passwd: no account %q (this command resets an existing account, it does not create one)", *username)
		}
		log.Fatalf("passwd: look up account: %v", err)
	}

	pw := *password
	generated := false
	if pw == "" {
		if pw, err = generatePassword(); err != nil {
			log.Fatalf("passwd: generate password: %v", err)
		}
		generated = true
	}
	if len(pw) < 8 {
		log.Fatal("passwd: password must be at least 8 characters")
	}

	hash, err := panel.HashPassword(pw)
	if err != nil {
		log.Fatalf("passwd: hash password: %v", err)
	}
	if err := st.UpsertAdmin(ctx, acct.Username, hash); err != nil {
		log.Fatalf("passwd: store password: %v", err)
	}
	// Same audit trail the panel and bot write, so an out-of-band reset is not
	// invisible in the operator log.
	line := journal.Line("password reset for %q from the command line", acct.Username)
	_ = st.AppendEvent(ctx, model.Event{
		Kind:      model.EventAdmin,
		Severity:  model.SeverityWarn,
		Message:   journal.Render(line, journal.Lang),
		MessageRU: journal.Render(line, "ru"),
		Actor:     acct.Username,
		OwnerID:   acct.Namespace(),
	})

	if generated {
		fmt.Printf("password for %q reset to: %s\n", acct.Username, pw)
	} else {
		fmt.Printf("password for %q reset\n", acct.Username)
	}
	fmt.Fprintln(os.Stderr, "note: existing browser sessions stay valid until the panel restarts")
}

// pwAlphabet leaves out the characters that are misread when a password is
// copied off a terminal by eye (0/O, 1/l/I).
const pwAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"

// generatePassword returns a 20-character random password.
func generatePassword() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = pwAlphabet[int(b)%len(pwAlphabet)]
	}
	return string(out), nil
}
