package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

type fakeSettings struct {
	s      model.Settings
	err    error
	admins []model.AdminAccount
}

func (f fakeSettings) GetSettings(context.Context) (model.Settings, error) { return f.s, f.err }
func (f fakeSettings) ListAdmins(context.Context) ([]model.AdminAccount, error) {
	return f.admins, nil
}

func TestBetaAlerterPrefersReplicaCreds(t *testing.T) {
	a := betaAlerter{
		store:   fakeSettings{s: model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "db-tok", ChatID: "db-chat"}}},
		envTok:  "env-tok",
		envChat: "env-chat",
	}
	tg, ok := a.sender(context.Background()).(watchdog.TelegramAlerter)
	if !ok || tg.Token != "db-tok" || tg.ChatID != "db-chat" {
		t.Fatalf("want live replica creds, got %#v", a.sender(context.Background()))
	}
}

func TestBetaAlerterFallsBackToEnv(t *testing.T) {
	// Replica reachable but the alert bot is unconfigured there -> env fallback.
	a := betaAlerter{
		store:   fakeSettings{s: model.Settings{Alert: model.AlertSettings{Enabled: false}}},
		envTok:  "env-tok",
		envChat: "env-chat",
	}
	tg, ok := a.sender(context.Background()).(watchdog.TelegramAlerter)
	if !ok || tg.Token != "env-tok" || tg.ChatID != "env-chat" {
		t.Fatalf("want env fallback creds, got %#v", a.sender(context.Background()))
	}
}

func TestBetaAlerterFallsBackToEnvWhenReplicaDown(t *testing.T) {
	a := betaAlerter{
		store:   fakeSettings{err: context.DeadlineExceeded},
		envTok:  "env-tok",
		envChat: "env-chat",
	}
	tg, ok := a.sender(context.Background()).(watchdog.TelegramAlerter)
	if !ok || tg.Token != "env-tok" {
		t.Fatalf("want env fallback when replica errors, got %#v", a.sender(context.Background()))
	}
}

// TestBetaAlerterLocalizesToChatOwner reproduces backlog item #9: β
// (betaAlerter) previously implemented only the plain Alert(ctx, sev, string),
// so watchdog.deliver always rendered DefaultLang regardless of the recipient's
// own Locale. AlertLocalized must render in the language of whichever account
// owns the alert chat β sends to.
func TestBetaAlerterLocalizesToChatOwner(t *testing.T) {
	a := betaAlerter{store: fakeSettings{
		s: model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "db-tok", ChatID: "db-chat"}},
		admins: []model.AdminAccount{
			{Username: "other", AlertChatID: "someone-else-chat", Locale: "en"},
			{Username: "ru-op", AlertChatID: "db-chat", Locale: "ru"},
		},
	}}
	if lang := a.chatLang(context.Background()); lang != "ru" {
		t.Fatalf("chatLang = %q, want ru (the account bound to db-chat)", lang)
	}
}

// TestBetaAlerterLocalizeFallsBackToDefault covers: no store, a store error, and
// a chat with no bound account — all must fall back to DefaultLang rather than
// erroring or leaving the message unrendered.
func TestBetaAlerterLocalizeFallsBackToDefault(t *testing.T) {
	cases := []struct {
		name string
		a    betaAlerter
	}{
		{"no store", betaAlerter{}},
		{"settings error", betaAlerter{store: fakeSettings{err: context.DeadlineExceeded}}},
		{"chat with no bound account", betaAlerter{store: fakeSettings{
			s:      model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "db-tok", ChatID: "db-chat"}},
			admins: []model.AdminAccount{{Username: "other", AlertChatID: "unrelated-chat", Locale: "ru"}},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if lang := c.a.chatLang(context.Background()); lang != watchdog.DefaultLang {
				t.Fatalf("chatLang = %q, want DefaultLang (%q)", lang, watchdog.DefaultLang)
			}
		})
	}
}

func TestBetaAlerterWebhookAndNone(t *testing.T) {
	if s := (betaAlerter{webhook: "http://x"}).sender(context.Background()); s == nil {
		t.Fatal("want webhook sender")
	}
	if s := (betaAlerter{}).sender(context.Background()); s != nil {
		t.Fatalf("want nil sender when nothing configured, got %#v", s)
	}
}

type fakeHeartbeat struct {
	age time.Duration
	ok  bool
	err error
}

func (f fakeHeartbeat) AlertHeartbeatAge(context.Context) (time.Duration, bool, error) {
	return f.age, f.ok, f.err
}

func newDeadMan(hb heartbeatReader, a watchdog.Alerter) *deadManChecker {
	return &deadManChecker{store: hb, stale: 15 * time.Minute, target: "exit1", mon: watchdog.NewMonitor(a, 1)}
}

func TestDeadManFiresWhenStaleAndPrimaryUp(t *testing.T) {
	a := &recordAlerter{}
	d := newDeadMan(fakeHeartbeat{age: 30 * time.Minute, ok: true}, a)
	d.check(context.Background(), false) // primary not declared down
	if len(a.msgs) != 1 || !strings.Contains(a.msgs[0], "silent") {
		t.Fatalf("want one dead-man alert, got %v", a.msgs)
	}
	if a.sevs[0] != watchdog.SeverityCritical {
		t.Fatalf("dead-man must be critical, got %v", a.sevs[0])
	}
}

func TestDeadManQuietWhenFresh(t *testing.T) {
	a := &recordAlerter{}
	d := newDeadMan(fakeHeartbeat{age: time.Minute, ok: true}, a)
	d.check(context.Background(), false)
	if len(a.msgs) != 0 {
		t.Fatalf("fresh heartbeat must not alert, got %v", a.msgs)
	}
}

func TestDeadManSuppressedWhilePrimaryDown(t *testing.T) {
	a := &recordAlerter{}
	d := newDeadMan(fakeHeartbeat{age: 30 * time.Minute, ok: true}, a)
	// Primary already declared DOWN by the PG prober: the dead-man must stay quiet
	// (that alert covers it; avoids double-paging and mis-attribution).
	d.check(context.Background(), true)
	if len(a.msgs) != 0 {
		t.Fatalf("dead-man must be suppressed while primary is down, got %v", a.msgs)
	}
}

func TestDeadManNoStampYetIsQuiet(t *testing.T) {
	a := &recordAlerter{}
	d := newDeadMan(fakeHeartbeat{ok: false}, a) // never stamped
	d.check(context.Background(), false)
	if len(a.msgs) != 0 {
		t.Fatalf("no heartbeat yet must not alert, got %v", a.msgs)
	}
}

func TestDeadManRecoversSilently(t *testing.T) {
	a := &recordAlerter{}
	d := newDeadMan(fakeHeartbeat{age: 30 * time.Minute, ok: true}, a)
	d.check(context.Background(), false) // DOWN (critical)
	// Heartbeat fresh again -> recovery (silent).
	d.store = fakeHeartbeat{age: time.Minute, ok: true}
	d.check(context.Background(), false)
	if len(a.msgs) != 2 || a.sevs[1] != watchdog.SeverityLow {
		t.Fatalf("want silent recovery as 2nd alert, got msgs=%v sevs=%v", a.msgs, a.sevs)
	}
}
