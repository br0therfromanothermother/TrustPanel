package panel

import (
	"context"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
)

// seedAccounts creates an admin (with alert chat) and an operator (with its own
// alert chat) plus the global alert chat in settings.
func seedAccounts(t *testing.T, st storeWriter, ctx context.Context) {
	t.Helper()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAccount(ctx, "op1", hash, model.RoleOperator); err != nil {
		t.Fatal(err)
	}
	opTG := int64(111)
	if err := st.SetAccountTelegram(ctx, "op1", &opTG, "-100op"); err != nil {
		t.Fatal(err)
	}
}

type storeWriter interface {
	UpsertAdmin(context.Context, string, string) error
	CreateAccount(context.Context, string, string, model.Role) error
	SetAccountTelegram(context.Context, string, *int64, string) error
}

func TestAudienceChats(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seedAccounts(t, st, ctx)

	s := model.Settings{Alert: model.AlertSettings{Enabled: true, ChatID: "-100admin"}}

	admin := p.audienceChats(ctx, s, audAdmin)
	if len(admin) != 1 || admin[0].chatID != "-100admin" {
		t.Fatalf("audAdmin = %v, want [-100admin]", admin)
	}

	bc := chatIDs(p.audienceChats(ctx, s, audBroadcast))
	if !contains(bc, "-100admin") || !contains(bc, "-100op") {
		t.Fatalf("audBroadcast = %v, want both admin + operator chats", bc)
	}
	if len(bc) != 2 {
		t.Fatalf("audBroadcast = %v, want exactly 2 (deduped)", bc)
	}
}

func TestOwnerChats(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seedAccounts(t, st, ctx)
	s := model.Settings{Alert: model.AlertSettings{Enabled: true, ChatID: "-100admin"}}

	// Owner with a bound chat -> admin + owner, deduped.
	got := chatIDs(p.ownerChats(ctx, s, "op1"))
	if !contains(got, "-100admin") || !contains(got, "-100op") || len(got) != 2 {
		t.Fatalf("ownerChats(op1) = %v, want admin + op chats", got)
	}

	// Unknown / empty owner -> admin only (fallback).
	if got := p.ownerChats(ctx, s, ""); len(got) != 1 || got[0].chatID != "-100admin" {
		t.Fatalf("ownerChats(\"\") = %v, want admin only", got)
	}
	if got := p.ownerChats(ctx, s, "nobody"); len(got) != 1 || got[0].chatID != "-100admin" {
		t.Fatalf("ownerChats(nobody) = %v, want admin only", got)
	}

	// Owner == admin namespace and admin has the same chat -> no duplicate.
	if got := p.ownerChats(ctx, model.Settings{Alert: model.AlertSettings{Enabled: true, ChatID: "-100op"}}, "op1"); len(got) != 1 {
		t.Fatalf("ownerChats with coinciding chats = %v, want deduped to 1", got)
	}
}

func TestCheckExpiringConfigs(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	seedAccounts(t, st, ctx)

	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	if err := st.UpsertGroup(ctx, model.Group{ID: "g-op", Name: "op grp", OwnerID: "op1"}); err != nil {
		t.Fatal(err)
	}
	soon := now.Add(48 * time.Hour) // within the default 3-day window
	far := now.Add(30 * 24 * time.Hour)
	mk := func(id, name string, exp time.Time, enabled bool) {
		if err := st.UpsertUser(ctx, model.User{ID: id, Username: name, GroupID: "g-op", OwnerID: "op1", Enabled: enabled, ExpiresAt: &exp}); err != nil {
			t.Fatal(err)
		}
	}
	mk("u-soon", "soonclient", soon, true)
	mk("u-far", "farclient", far, true)
	mk("u-off", "offclient", soon, false) // disabled: nothing to warn about

	rec := &captureAlerter{}
	p.SetExpiryAlerts(rec, 3)

	p.CheckExpiringConfigs(ctx)
	msgs := rec.msgs
	if len(msgs) != 1 || !strings.Contains(msgs[0], "soonclient") {
		t.Fatalf("first pass = %v, want exactly one warning for soonclient", msgs)
	}

	// Idempotent: a second pass for the same expiry date warns nothing more.
	p.CheckExpiringConfigs(ctx)
	if got := rec.msgs; len(got) != 1 {
		t.Fatalf("second pass added warnings: %v", got)
	}

	// Extending the config re-arms the warning for the new date.
	newExp := now.Add(24 * time.Hour)
	mk("u-soon", "soonclient", newExp, true)
	p.CheckExpiringConfigs(ctx)
	if got := rec.msgs; len(got) != 2 {
		t.Fatalf("after extend = %v, want a fresh warning for the new date", got)
	}
}

// TestEventLogNamespacedHTTP proves an operator's resource events stay in the
// operator namespace: the admin Logs view never surfaces the operator's client.
func TestEventLogNamespacedHTTP(t *testing.T) {
	p, st, _ := newPanel(t)
	ctx := context.Background()
	hash, _ := HashPassword("supersecret")
	if err := st.UpsertAdmin(ctx, "boss", hash); err != nil {
		t.Fatal(err)
	}
	// A co-owner admin (scoped to "team") — events are namespace-scoped the same way
	// for any scoped account.
	coOwner(t, st, ctx, "lead", "leadpassw1", "team")

	// The co-owner creates its own group + user -> events recorded in the "team" namespace.
	op, opURL := newClient(t, p)
	opTok := login(t, op, opURL, "lead", "leadpassw1")
	if code, body := doTok(t, op, "POST", opURL+"/api/groups", opTok, model.Group{ID: "g-op", Name: "op grp"}); code != 200 {
		t.Fatalf("co-owner create group: %d %s", code, body)
	}
	if code, body := doTok(t, op, "POST", opURL+"/api/users", opTok,
		userUpsertRequest{ID: "u-op", Username: "secretclient", Enabled: true, GroupID: "g-op"}); code != 200 {
		t.Fatalf("co-owner create user: %d %s", code, body)
	}

	// The bootstrap owner's Logs view must not contain the co-owner's client name.
	adm, admURL := newClient(t, p)
	admTok := login(t, adm, admURL, "boss", "supersecret")
	code, body := doTok(t, adm, "GET", admURL+"/api/events", admTok, nil)
	if code != 200 {
		t.Fatalf("admin events: %d %s", code, body)
	}
	if strings.Contains(string(body), "secretclient") || strings.Contains(string(body), "u-op") {
		t.Fatalf("operator client leaked into admin event log: %s", body)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func chatIDs(ts []chatTarget) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.chatID
	}
	return out
}
