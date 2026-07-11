package cli

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
)

// RunWatchdog runs the mutual watchdog on the standby: it watches the
// active control plane and alerts an operator (one-way, via the standby's own
// alert bot β) so they can run `promote`. Two independent checks:
//
//   - the active node's Postgres :5432 (primary down -> Critical), and
//   - the alert dead-man: the primary stamps a heartbeat in the DB whenever it
//     can reach Telegram; read off the local replica, a stale stamp while the box
//     is still alive means the primary can no longer publish alerts (serve dead /
//     Telegram unreachable / α token revoked) -> Critical. Suppressed while the
//     primary is already declared down (that alert covers it).
//
// β's own alert-bot token is resolved live from the same local replica (the
// dedicated alert bot the panel manages), so rotating it in the panel needs no
// watchdog.env edit; the env token/chat remain an optional fallback.
func RunWatchdog(args []string) {
	fs := flag.NewFlagSet("watchdog", flag.ExitOnError)
	target := fs.String("target", "peer", "name of the watched node (for alert text)")
	watchURL := fs.String("watch-url", "", "HTTP health URL to probe (e.g. agent /v1/status)")
	watchTCP := fs.String("watch-tcp", "", "TCP address to probe (e.g. active Postgres host:5432)")
	interval := fs.Duration("interval", 30*time.Second, "probe interval")
	threshold := fs.Int("threshold", 3, "consecutive failures before alerting")
	tgToken := fs.String("alert-telegram-token", "", "dedicated alert bot token (one-way)")
	tgChat := fs.String("alert-chat-id", "", "telegram chat id for alerts")
	webhook := fs.String("alert-webhook", "", "generic webhook URL for alerts")
	deadmanDSN := fs.String("deadman-dsn", "", "local replica DSN to read the alert dead-man heartbeat (enables the cross-observation check)")
	deadmanStale := fs.Duration("deadman-stale", 15*time.Minute, "heartbeat age past which the primary's alert system is considered silent")
	_ = fs.Parse(args)

	var prober watchdog.Prober
	switch {
	case *watchURL != "":
		prober = watchdog.HTTPProber{URL: *watchURL}
	case *watchTCP != "":
		prober = watchdog.TCPProber{Address: *watchTCP}
	default:
		log.Fatal("watchdog: one of --watch-url or --watch-tcp is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Open the local replica store once (when a DSN is available): it backs BOTH
	// the dead-man heartbeat read AND β's live alert-bot creds, so a token
	// rotation in the panel propagates via Postgres replication with no
	// watchdog.env edit needed.
	var st *store.Store
	if *deadmanDSN != "" {
		s, err := store.Open(ctx, *deadmanDSN)
		if err != nil {
			log.Printf("watchdog: replica store unavailable (%v) — dead-man off, alert creds from env only", err)
		} else {
			st = s
			defer st.Close()
		}
	}

	// β alert sender: prefer the dedicated alert-bot creds from the replica's
	// settings (kept in sync with the panel), fall back to static env creds /
	// webhook, always record locally via the log.
	alerter := betaAlerter{envTok: *tgToken, envChat: *tgChat, webhook: *webhook}
	if st != nil {
		alerter.store = st
		log.Printf("watchdog: β alert creds resolved live from the local replica (env is fallback)")
	}

	w := &watchdog.Watcher{
		Target: *target, Prober: prober, Alerter: alerter,
		Threshold: *threshold, Interval: *interval,
	}

	var dm *deadManChecker
	if st != nil {
		dm = &deadManChecker{
			store: st, stale: *deadmanStale, target: *target,
			mon: watchdog.NewMonitor(alerter, 2),
		}
		log.Printf("watchdog: dead-man enabled (stale after %s)", *deadmanStale)
	}

	log.Printf("watchdog watching %q every %s (threshold %d)", *target, *interval, *threshold)
	tick := func() {
		w.Tick(ctx)
		if dm != nil {
			dm.check(ctx, w.Down())
		}
	}
	tick()
	t := time.NewTicker(*interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// heartbeatReader is the slice of the store the dead-man needs (a single read),
// so the transition logic can be unit-tested without a database.
type heartbeatReader interface {
	AlertHeartbeatAge(ctx context.Context) (time.Duration, bool, error)
}

// settingsReader is the slice of the store β needs to resolve its alert-bot
// creds live (so a panel token rotation propagates via replication) and to
// localize its alerts to the recipient's interface language.
type settingsReader interface {
	GetSettings(ctx context.Context) (model.Settings, error)
	ListAdmins(ctx context.Context) ([]model.AdminAccount, error)
}

// betaAlerter is the standby watchdog's (β) alert sender. It prefers the
// dedicated alert-bot creds from the local replica's settings — kept in sync
// with the panel, so rotating the token in the panel takes effect without
// editing watchdog.env, and α always getMe's the very token β actually uses.
// It falls back to static env creds (watchdog.env) or a webhook when the replica
// is unreachable or the token is unset there, and always records via the log.
type betaAlerter struct {
	store   settingsReader // nil when no replica DSN is available
	envTok  string
	envChat string
	webhook string
}

func (a betaAlerter) Alert(ctx context.Context, sev watchdog.Severity, msg string) error {
	_ = watchdog.LogAlerter{}.Alert(ctx, sev, msg) // always record locally
	if s := a.sender(ctx); s != nil {
		return s.Alert(ctx, sev, msg)
	}
	return nil
}

// AlertLocalized renders msg in the interface language of the account bound to
// the alert chat β sends to, so a promote-me alert reads in the same language as
// everything else that operator sees — mirroring the panel's routedTelegram
// (internal/core/panel/alertroute.go), which β has no access to (different
// process, standby-side). Without this, watchdog.deliver falls back to always
// rendering DefaultLang regardless of the recipient's own Locale setting.
func (a betaAlerter) AlertLocalized(ctx context.Context, sev watchdog.Severity, msg watchdog.MsgFunc) error {
	_ = watchdog.LogAlerter{}.Alert(ctx, sev, watchdog.Render(msg, watchdog.DefaultLang)) // always record locally
	s := a.sender(ctx)
	if s == nil {
		return nil
	}
	return s.Alert(ctx, sev, watchdog.Render(msg, a.chatLang(ctx)))
}

// chatLang resolves the interface language of the account whose alert chat
// matches the one β is about to send to. Falls back to DefaultLang when the
// replica is unavailable or no account is bound to that chat (env/webhook
// fallback creds, or a manually-set chat id with no owning account).
func (a betaAlerter) chatLang(ctx context.Context) string {
	if a.store == nil {
		return watchdog.DefaultLang
	}
	s, err := a.store.GetSettings(ctx)
	if err != nil || s.Alert.ChatID == "" {
		return watchdog.DefaultLang
	}
	accts, err := a.store.ListAdmins(ctx)
	if err != nil {
		return watchdog.DefaultLang
	}
	for _, acct := range accts {
		if acct.AlertChatID == s.Alert.ChatID {
			if acct.Locale == "ru" {
				return "ru"
			}
			return watchdog.DefaultLang
		}
	}
	return watchdog.DefaultLang
}

// sender resolves the live delivery channel: the panel-managed alert bot from
// the replica first, then env Telegram creds, then a webhook; nil if none.
func (a betaAlerter) sender(ctx context.Context) watchdog.Alerter {
	if a.store != nil {
		if s, err := a.store.GetSettings(ctx); err == nil &&
			s.Alert.Enabled && s.Alert.Token != "" && s.Alert.ChatID != "" {
			return watchdog.TelegramAlerter{Token: s.Alert.Token, ChatID: s.Alert.ChatID}
		}
	}
	if a.envTok != "" && a.envChat != "" {
		return watchdog.TelegramAlerter{Token: a.envTok, ChatID: a.envChat}
	}
	if a.webhook != "" {
		return watchdog.WebhookAlerter{URL: a.webhook}
	}
	return nil
}

// deadManChecker reads the alert heartbeat off the local replica and raises β
// (via the monitor) when it goes stale while the primary box is otherwise alive.
type deadManChecker struct {
	store  heartbeatReader
	stale  time.Duration
	target string
	mon    *watchdog.Monitor
}

func (d *deadManChecker) check(ctx context.Context, primaryDown bool) {
	if primaryDown {
		// The primary is already declared DOWN; the dead-man would be redundant and
		// would mis-attribute a dead box as "alive but silent". Reset (not Observe)
		// so it re-arms cleanly without emitting a spurious empty-text recovery
		// notice, and can still fire on a later half-recovery (PG back, serve dead).
		d.mon.Reset("deadman")
		return
	}
	age, ok, err := d.store.AlertHeartbeatAge(ctx)
	if err != nil {
		// Can't read the replica (e.g. it is itself down) — the PG prober covers
		// that case; don't flap the dead-man here.
		log.Printf("watchdog: dead-man read: %v", err)
		return
	}
	if !ok {
		return // never stamped yet — nothing to judge
	}
	healthy := age <= d.stale
	down := watchdog.MsgDeadmanDown(d.target, age.Round(time.Second).String())
	up := watchdog.MsgDeadmanUp(d.target)
	d.mon.Observe(ctx, "deadman", healthy, watchdog.SeverityCritical, down, up)
}
