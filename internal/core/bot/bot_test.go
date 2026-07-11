package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/store"
)

// fakeStore is an in-memory DataStore for dispatch tests.
type fakeStore struct {
	state    model.State
	totals   []store.UserTrafficTotal
	upsert   []model.User
	maint    []maintCall
	settings model.Settings
	accounts map[int64]model.AdminAccount // telegram_id -> account
}

type maintCall struct {
	id string
	on bool
}

func (f *fakeStore) GetSettings(context.Context) (model.Settings, error) { return f.settings, nil }
func (f *fakeStore) LoadState(context.Context) (model.State, error)      { return f.state, nil }
func (f *fakeStore) AccountByTelegramID(_ context.Context, id int64) (model.AdminAccount, error) {
	if a, ok := f.accounts[id]; ok {
		return a, nil
	}
	return model.AdminAccount{}, store.ErrNoAdmin
}
func (f *fakeStore) UpsertUser(_ context.Context, u model.User) error {
	f.upsert = append(f.upsert, u)
	// reflect into state so subsequent reads see it
	for i := range f.state.Users {
		if f.state.Users[i].ID == u.ID {
			f.state.Users[i] = u
			return nil
		}
	}
	f.state.Users = append(f.state.Users, u)
	return nil
}
func (f *fakeStore) UserTrafficTotals(context.Context) ([]store.UserTrafficTotal, error) {
	return f.totals, nil
}
func (f *fakeStore) SetNodeMaintenance(_ context.Context, id string, on bool) error {
	f.maint = append(f.maint, maintCall{id, on})
	for i := range f.state.Nodes {
		if f.state.Nodes[i].ID == id {
			f.state.Nodes[i].Maintenance = on
		}
	}
	return nil
}
func (f *fakeStore) DeleteUser(_ context.Context, id string) error {
	out := f.state.Users[:0]
	for _, u := range f.state.Users {
		if u.ID != id {
			out = append(out, u)
		}
	}
	f.state.Users = out
	return nil
}
func (f *fakeStore) UpsertGroup(_ context.Context, g model.Group) error {
	for i := range f.state.Groups {
		if f.state.Groups[i].ID == g.ID {
			f.state.Groups[i] = g
			return nil
		}
	}
	f.state.Groups = append(f.state.Groups, g)
	return nil
}
func (f *fakeStore) DeleteGroup(_ context.Context, id string) error {
	out := f.state.Groups[:0]
	for _, g := range f.state.Groups {
		if g.ID != id {
			out = append(out, g)
		}
	}
	f.state.Groups = out
	return nil
}
func (f *fakeStore) UpsertRoutePolicy(_ context.Context, p model.RoutePolicy) error {
	for i := range f.state.RoutePolicies {
		if f.state.RoutePolicies[i].ID == p.ID {
			f.state.RoutePolicies[i] = p
			return nil
		}
	}
	f.state.RoutePolicies = append(f.state.RoutePolicies, p)
	return nil
}
func (f *fakeStore) DeleteRoutePolicy(_ context.Context, id string) error {
	out := f.state.RoutePolicies[:0]
	for _, p := range f.state.RoutePolicies {
		if p.ID != id {
			out = append(out, p)
		}
	}
	f.state.RoutePolicies = out
	return nil
}
func (f *fakeStore) SetAccountLocale(_ context.Context, username, locale string) error {
	for id, a := range f.accounts {
		if a.Username == username {
			a.Locale = locale
			f.accounts[id] = a
		}
	}
	return nil
}
func (f *fakeStore) SetAccountTelegram(_ context.Context, username string, tg *int64, alertChatID string) error {
	for id, a := range f.accounts {
		if a.Username == username {
			a.TelegramID = tg
			a.AlertChatID = alertChatID
			f.accounts[id] = a
		}
	}
	return nil
}

func seedStore() *fakeStore {
	st := model.WorkedExample()
	return &fakeStore{
		state: st,
		totals: []store.UserTrafficTotal{
			{UserID: "u-alice", Username: "alice", RxBytes: 1000, TxBytes: 2000, TotalBytes: 3000},
			{UserID: "u-bob", Username: "bob", RxBytes: 0, TxBytes: 0, TotalBytes: 0},
		},
		// Authority is now the per-account Telegram binding (the flat allowlist no
		// longer grants access). The fleet owner is bound to id 42.
		accounts: map[int64]model.AdminAccount{
			admin: {Username: "owner", Role: model.RoleAdmin, NamespaceID: ""},
		},
	}
}

const admin = int64(42)

func newBot(fs *fakeStore) *Bot { return New(fs, "token", "") }

func TestDispatchAuthorization(t *testing.T) {
	b := newBot(seedStore())
	// Unbound senders get silence: empty reply, no id echo.
	if got := b.Dispatch(context.Background(), 999, "/status"); got != "" {
		t.Fatalf("unbound sender should get silence, got %q", got)
	}
	if got := b.Dispatch(context.Background(), admin, "/status"); got == "" {
		t.Fatalf("admin should be allowed, got empty")
	}
}

func TestDispatchRoleScoping(t *testing.T) {
	ctx := context.Background()
	fs := &fakeStore{
		state: model.State{
			Groups: []model.Group{{ID: "g-op", Name: "op", OwnerID: "op1"}, {ID: "g-adm", Name: "adm"}},
			Users: []model.User{
				{ID: "u-op", Username: "opclient", DisplayName: "opclient", Enabled: true, GroupID: "g-op", OwnerID: "op1"},
				{ID: "u-adm", Username: "admclient", DisplayName: "admclient", Enabled: true, GroupID: "g-adm"},
			},
		},
		totals: []store.UserTrafficTotal{
			{UserID: "u-op", Username: "opclient", TotalBytes: 500},
			{UserID: "u-adm", Username: "admclient", TotalBytes: 900},
		},
		accounts: map[int64]model.AdminAccount{
			7:     {Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"},
			admin: {Username: "owner", Role: model.RoleAdmin, NamespaceID: ""},
		},
	}
	b := newBot(fs) // admin id 42 bound as fleet owner

	// Operator sees only its own client.
	out := b.Dispatch(ctx, 7, "/users")
	if !strings.Contains(out, "opclient") || strings.Contains(out, "admclient") {
		t.Fatalf("operator /users should show only own client, got %q", out)
	}
	// Operator cannot see another namespace's client by name.
	if got := b.Dispatch(ctx, 7, "/user admclient"); !strings.Contains(got, "No such user") {
		t.Fatalf("operator should not resolve admin's client, got %q", got)
	}
	// Nor export another namespace's config.
	if got := b.Dispatch(ctx, 7, "/config admclient"); !strings.Contains(got, "No such user") {
		t.Fatalf("operator should not export admin's config, got %q", got)
	}
	// Operator traffic is scoped.
	if got := b.Dispatch(ctx, 7, "/traffic"); strings.Contains(got, "admclient") {
		t.Fatalf("operator /traffic leaked another namespace: %q", got)
	}
	// A new client created by the operator is stamped with its namespace.
	b.Dispatch(ctx, 7, "/adduser newone g-op")
	var stamped *model.User
	for i := range fs.upsert {
		if fs.upsert[i].Username == "newone" {
			stamped = &fs.upsert[i]
		}
	}
	if stamped == nil || stamped.OwnerID != "op1" {
		t.Fatalf("operator-created client should be owned by op1, got %+v", stamped)
	}
	// The bootstrap owner's everyday /users is now scoped to its OWN namespace
	// (mirrors the panel) — peers' clients are not listed by reflex. Cross-namespace
	// browsing is the explicit, read-only /namespaces lens.
	if out := b.Dispatch(ctx, admin, "/users"); !strings.Contains(out, "admclient") || strings.Contains(out, "opclient") {
		t.Fatalf("owner /users should show only its own namespace, got %q", out)
	}
	// The hidden lens still reaches another namespace's clients (read-only).
	if out, _ := b.Callback(ctx, admin, "lens:op1"); !strings.Contains(out, "opclient") {
		t.Fatalf("owner /namespaces lens should reach op1's clients, got %q", out)
	}
}

func TestDispatchReadCommands(t *testing.T) {
	b := newBot(seedStore())
	ctx := context.Background()

	// /help and /start open the inline menu, not a wall of commands.
	if got := b.Dispatch(ctx, admin, "/help"); !strings.Contains(got, "main menu") {
		t.Errorf("help should open the menu: %q", got)
	}
	if _, kb := b.route(ctx, admin, "/start"); !strings.Contains(kb, "m:users") {
		t.Errorf("/start should open the interactive menu: %q", kb)
	}
	if got := b.Dispatch(ctx, admin, "/status"); !strings.Contains(got, "users:") {
		t.Errorf("status: %q", got)
	}
	// The control-plane epoch is internal plumbing and must not leak to the bot.
	if got := b.Dispatch(ctx, admin, "/status"); strings.Contains(got, "epoch") {
		t.Errorf("status should not expose epoch: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/nodes"); !strings.Contains(got, "entry") && !strings.Contains(got, "exit") {
		t.Errorf("nodes: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/users"); !strings.Contains(strings.ToLower(got), "alice") {
		t.Errorf("users: %q", got)
	}
	// @BotName suffix is tolerated.
	if got := b.Dispatch(ctx, admin, "/user@TrustBot alice"); !strings.Contains(got, "↑1000 B") {
		t.Errorf("user detail traffic: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/traffic"); !strings.Contains(got, "alice") {
		t.Errorf("traffic: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/user nobody"); !strings.Contains(got, "No such user") {
		t.Errorf("unknown user: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/bogus"); !strings.Contains(got, "Unknown command") {
		t.Errorf("unknown command: %q", got)
	}
}

func TestDispatchEnableDisable(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	ctx := context.Background()

	got := b.Dispatch(ctx, admin, "/disable alice")
	if !strings.Contains(got, "disabled") {
		t.Fatalf("disable: %q", got)
	}
	if len(fs.upsert) != 1 || fs.upsert[0].ID != "u-alice" || fs.upsert[0].Enabled {
		t.Fatalf("disable did not persist Enabled=false: %+v", fs.upsert)
	}
	// Idempotent message when already disabled.
	if got := b.Dispatch(ctx, admin, "/disable alice"); !strings.Contains(got, "already disabled") {
		t.Fatalf("re-disable: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/enable alice"); !strings.Contains(got, "now enabled") {
		t.Fatalf("enable: %q", got)
	}
}

func TestDispatchAddUser(t *testing.T) {
	fs := seedStore()
	// WorkedExample has 2 groups; adduser without a group must ask which.
	b := newBot(fs)
	ctx := context.Background()
	if got := b.Dispatch(ctx, admin, "/adduser carol"); !strings.Contains(got, "Multiple groups") {
		t.Fatalf("ambiguous group should prompt: %q", got)
	}
	got := b.Dispatch(ctx, admin, "/adduser carol everyone")
	if !strings.Contains(got, "Created user") {
		t.Fatalf("adduser: %q", got)
	}
	if len(fs.upsert) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(fs.upsert))
	}
	u := fs.upsert[0]
	if u.Username != "carol" || u.GroupID != "everyone" || !u.Enabled || u.Secret == "" {
		t.Fatalf("created user wrong: %+v", u)
	}
	// Duplicate is rejected.
	if got := b.Dispatch(ctx, admin, "/adduser carol everyone"); !strings.Contains(got, "already exists") {
		t.Fatalf("duplicate add: %q", got)
	}
	// Unknown group rejected.
	if got := b.Dispatch(ctx, admin, "/adduser dave nogroup"); !strings.Contains(got, "No such group") {
		t.Fatalf("unknown group: %q", got)
	}
}

func TestDispatchConfig(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	ctx := context.Background()

	if got := b.Dispatch(ctx, admin, "/config"); !strings.Contains(got, "Usage") {
		t.Fatalf("bare /config should show usage: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/config nobody"); !strings.Contains(got, "No such user") {
		t.Fatalf("unknown user: %q", got)
	}
	got := b.Dispatch(ctx, admin, "/config alice")
	if !strings.Contains(got, "Deep link") || !strings.Contains(got, "tt://") {
		t.Fatalf("config export missing deep link: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "alice") {
		t.Fatalf("config should name the user: %q", got)
	}
	// /import is gone — it must not resolve to a command.
	if got := b.Dispatch(ctx, admin, "/import whatever"); !strings.Contains(got, "Unknown command") {
		t.Fatalf("/import should no longer exist: %q", got)
	}
}

// TestConfigPrivacyGuard drives the chat-type guard through handleUpdate: /config
// in a group is refused (the deep link carries the secret), while a private chat
// exports it.
func TestConfigPrivacyGuard(t *testing.T) {
	var sent []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sendMessage") {
			body, _ := io.ReadAll(r.Body)
			q, _ := url.ParseQuery(string(body))
			sent = append(sent, q)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	b := New(seedStore(), "token", srv.URL)

	// In a group chat: refused, no deep link leaked.
	b.handleUpdate(context.Background(), update{UpdateID: 1, Message: &tgMessage{
		From: &tgUser{ID: admin}, Chat: &tgChat{ID: 10, Type: "group"}, Text: "/config alice",
	}})
	if len(sent) != 1 || !strings.Contains(sent[0].Get("text"), "privately") {
		t.Fatalf("group /config should be refused privately, got %+v", sent)
	}
	if strings.Contains(sent[0].Get("text"), "tt://") {
		t.Fatalf("group /config leaked a deep link: %q", sent[0].Get("text"))
	}
	// In a private chat: exported.
	sent = nil
	b.handleUpdate(context.Background(), update{UpdateID: 2, Message: &tgMessage{
		From: &tgUser{ID: admin}, Chat: &tgChat{ID: 11, Type: "private"}, Text: "/config alice",
	}})
	if len(sent) != 1 || !strings.Contains(sent[0].Get("text"), "tt://") {
		t.Fatalf("private /config should export the deep link, got %+v", sent)
	}
}

func TestBotInlineMenu(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	fixed := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }
	ctx := context.Background()

	// Root menu carries the branch buttons, folds the status summary into its
	// header (so there is no separate, duplicate Status view), and offers create.
	text, kb := b.Callback(ctx, admin, "m:root")
	if !strings.Contains(text, "main menu") || !strings.Contains(kb, "m:users") || !strings.Contains(kb, "m:traffic") {
		t.Fatalf("root menu wrong: %q kb=%q", text, kb)
	}
	if !strings.Contains(text, "nodes:") || !strings.Contains(kb, "m:adduser") {
		t.Fatalf("root menu should show status + a create button: %q kb=%q", text, kb)
	}
	// ➕ New user starts the guided wizard (real management from the menu).
	if step, _ := b.Callback(ctx, admin, "m:adduser"); !strings.Contains(step, "step 1/3") {
		t.Fatalf("m:adduser should start the wizard, got %q", step)
	}
	if b.convoFor(admin) == nil {
		t.Fatalf("m:adduser should arm the add-user conversation")
	}
	b.clearConvo(admin)

	// Users list contains alice as a tappable card button.
	_, kb = b.Callback(ctx, admin, "m:users")
	if !strings.Contains(kb, "u:u-alice") {
		t.Fatalf("users menu should list alice: %q", kb)
	}

	// Alice's card (enabled) offers Disable + extend + group + back to users.
	text, kb = b.Callback(ctx, admin, "u:u-alice")
	if !strings.Contains(text, "username: alice") || !strings.Contains(kb, "ud:u-alice") ||
		!strings.Contains(kb, "uxm:u-alice") || !strings.Contains(kb, "ucg:u-alice") {
		t.Fatalf("user card wrong: %q kb=%q", text, kb)
	}

	// Tapping Disable persists Enabled=false and re-renders the card with Enable.
	_, kb = b.Callback(ctx, admin, "ud:u-alice")
	if len(fs.upsert) == 0 || fs.upsert[len(fs.upsert)-1].ID != "u-alice" || fs.upsert[len(fs.upsert)-1].Enabled {
		t.Fatalf("disable did not persist: %+v", fs.upsert)
	}
	if !strings.Contains(kb, "ue:u-alice") {
		t.Fatalf("disabled card should offer Enable: %q", kb)
	}

	// The extend menu offers presets and persists nothing until one is tapped.
	n := len(fs.upsert)
	if _, kb := b.Callback(ctx, admin, "uxm:u-alice"); !strings.Contains(kb, "uxp:u-alice:30") || !strings.Contains(kb, "uxp:u-alice:0") {
		t.Fatalf("extend menu should offer presets: %q", kb)
	}
	if len(fs.upsert) != n {
		t.Fatalf("opening the extend menu should not persist")
	}
	// A +30 preset on an unlimited config sets the expiry ~30 days out.
	b.Callback(ctx, admin, "uxp:u-alice:30")
	last := fs.upsert[len(fs.upsert)-1]
	if last.ExpiresAt == nil || !last.ExpiresAt.Equal(fixed.Add(30*24*time.Hour)) {
		t.Fatalf("extend expiry wrong: %v", last.ExpiresAt)
	}
	// ♾ Never clears the expiry back to unlimited.
	b.Callback(ctx, admin, "uxp:u-alice:0")
	if last := fs.upsert[len(fs.upsert)-1]; last.ExpiresAt != nil {
		t.Fatalf("never preset should clear expiry: %v", last.ExpiresAt)
	}
	// The group menu lists move targets; the current group is not a dead end.
	if _, kb := b.Callback(ctx, admin, "ucg:u-alice"); !strings.Contains(kb, "ucgp:u-alice:") {
		t.Fatalf("group menu should list move targets: %q", kb)
	}

	// Unauthorized sender gets silence at the callback boundary.
	if got, _ := b.Callback(ctx, 999, "m:root"); got != "" {
		t.Fatalf("unauthorized callback should be silent, got %q", got)
	}
}

func TestBotMenuOperatorScoping(t *testing.T) {
	ctx := context.Background()
	fs := &fakeStore{
		state: model.State{
			Groups: []model.Group{{ID: "g-op", Name: "op", OwnerID: "op1"}, {ID: "g-adm", Name: "adm"}},
			Users: []model.User{
				{ID: "u-op", Username: "opclient", DisplayName: "opclient", Enabled: true, GroupID: "g-op", OwnerID: "op1"},
				{ID: "u-adm", Username: "admclient", DisplayName: "admclient", Enabled: true, GroupID: "g-adm"},
			},
		},
		accounts: map[int64]model.AdminAccount{7: {Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"}},
	}
	b := newBot(fs)

	// Operator's users menu lists only its own client.
	_, kb := b.Callback(ctx, 7, "m:users")
	if !strings.Contains(kb, "u:u-op") || strings.Contains(kb, "u:u-adm") {
		t.Fatalf("operator users menu leaked: %q", kb)
	}
	// Operator tapping the admin's user id (out of namespace) resolves not found
	// and cannot mutate it.
	if got, _ := b.Callback(ctx, 7, "u:u-adm"); !strings.Contains(got, "No such user") {
		t.Fatalf("operator should not open admin's card: %q", got)
	}
	if got, _ := b.Callback(ctx, 7, "ud:u-adm"); !strings.Contains(got, "No such user") {
		t.Fatalf("operator should not disable admin's client: %q", got)
	}
	for _, u := range fs.upsert {
		if u.ID == "u-adm" {
			t.Fatal("operator must not have written the admin's client")
		}
	}
}

// TestHandleUpdateEndToEnd drives a full update through a fake Telegram server:
// the bot must reply to the originating chat via sendMessage.
func TestHandleUpdateEndToEnd(t *testing.T) {
	var sent url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sendMessage") {
			body, _ := io.ReadAll(r.Body)
			sent, _ = url.ParseQuery(string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	b := New(seedStore(), "token", srv.URL)
	u := update{UpdateID: 1, Message: &tgMessage{
		From: &tgUser{ID: admin},
		Chat: &tgChat{ID: 555, Type: "private"},
		Text: "/status",
	}}

	b.handleUpdate(context.Background(), u)
	if sent.Get("chat_id") != "555" {
		t.Fatalf("reply chat_id = %q, want 555", sent.Get("chat_id"))
	}
	if !strings.Contains(sent.Get("text"), "Control plane") {
		t.Fatalf("reply text = %q", sent.Get("text"))
	}
}

// TestHandleCallbackEndToEnd drives an inline-button tap through a fake Telegram
// server: the bot must ack the callback and edit the message in place.
func TestHandleCallbackEndToEnd(t *testing.T) {
	var edited url.Values
	var answered bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/editMessageText") {
			body, _ := io.ReadAll(r.Body)
			edited, _ = url.ParseQuery(string(body))
		}
		if strings.Contains(r.URL.Path, "/answerCallbackQuery") {
			answered = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()

	b := New(seedStore(), "token", srv.URL)
	u := update{UpdateID: 2, CallbackQuery: &tgCallback{
		ID:   "cbid",
		Data: "m:root",
		From: &tgUser{ID: admin},
		Message: &tgMessage{
			MessageID: 88,
			Chat:      &tgChat{ID: 555, Type: "private"},
		},
	}}

	b.handleUpdate(context.Background(), u)
	if !answered {
		t.Fatal("callback should be answered")
	}
	if edited.Get("chat_id") != "555" || edited.Get("message_id") != "88" {
		t.Fatalf("edit target wrong: chat=%q msg=%q", edited.Get("chat_id"), edited.Get("message_id"))
	}
	if !strings.Contains(edited.Get("text"), "main menu") {
		t.Fatalf("edited text = %q", edited.Get("text"))
	}
}

// TestLocalizationRussian drives a ru-language update through handleUpdate and
// confirms the reply is translated (the language is resolved from the sender's
// Telegram language_code; a missing/other code falls back to English).
func TestLocalizationRussian(t *testing.T) {
	var sent url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/sendMessage") {
			body, _ := io.ReadAll(r.Body)
			sent, _ = url.ParseQuery(string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	b := New(seedStore(), "token", srv.URL)

	b.handleUpdate(context.Background(), update{UpdateID: 1, Message: &tgMessage{
		From: &tgUser{ID: admin, LanguageCode: "ru"},
		Chat: &tgChat{ID: 9, Type: "private"}, Text: "/status",
	}})
	if !strings.Contains(sent.Get("text"), "Контрольный узел") {
		t.Fatalf("ru /status not localized: %q", sent.Get("text"))
	}

	// A non-ru language falls back to English.
	b.handleUpdate(context.Background(), update{UpdateID: 2, Message: &tgMessage{
		From: &tgUser{ID: admin, LanguageCode: "en-US"},
		Chat: &tgChat{ID: 9, Type: "private"}, Text: "/status",
	}})
	if !strings.Contains(sent.Get("text"), "Control plane") {
		t.Fatalf("en /status should stay English: %q", sent.Get("text"))
	}
}

// TestLangForUpdate confirms the account's saved locale wins over the Telegram
// language_code, and that an unbound sender falls back to language_code.
func TestLangForUpdate(t *testing.T) {
	fs := seedStore()
	fs.accounts[admin] = model.AdminAccount{Username: "owner", Role: model.RoleAdmin, Locale: "ru"}
	b := newBot(fs)
	// Bound account with locale=ru overrides an English Telegram UI.
	bound := update{Message: &tgMessage{From: &tgUser{ID: admin, LanguageCode: "en"}, Chat: &tgChat{ID: 1, Type: "private"}}}
	if got := b.langForUpdate(context.Background(), bound); got != "ru" {
		t.Fatalf("account locale should win, got %q", got)
	}
	// Unbound sender falls back to the Telegram language_code.
	unbound := update{Message: &tgMessage{From: &tgUser{ID: 999, LanguageCode: "ru"}, Chat: &tgChat{ID: 1, Type: "private"}}}
	if got := b.langForUpdate(context.Background(), unbound); got != "ru" {
		t.Fatalf("unbound sender should use language_code, got %q", got)
	}
}

// TestRefreshFromSettings confirms panel settings hot-swap the token and that a
// disabled setting falls back to the New() (flag/file) token.
func TestRefreshFromSettings(t *testing.T) {
	fs := seedStore()
	b := New(fs, "flag-token", "")
	if b.token != "flag-token" {
		t.Fatalf("fallback token not applied: token=%q", b.token)
	}
	// Enable a panel-configured bot with a different token.
	fs.settings = model.Settings{Bot: model.BotSettings{Enabled: true, Token: "db-token"}}
	b.refreshFromSettings(context.Background())
	if b.token != "db-token" {
		t.Fatalf("settings token not applied, got %q", b.token)
	}
	// Disable in settings → revert to fallback token.
	fs.settings = model.Settings{Bot: model.BotSettings{Enabled: false}}
	b.refreshFromSettings(context.Background())
	if b.token != "flag-token" {
		t.Fatalf("did not fall back to flag token: token=%q", b.token)
	}
}

// TestGetUpdatesParsing confirms the client decodes the Telegram envelope.
func TestGetUpdatesParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"ok": true, "result": []map[string]any{
			{"update_id": 7, "message": map[string]any{"message_id": 1, "text": "hi",
				"from": map[string]any{"id": 42}, "chat": map[string]any{"id": 9}}},
		}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	c := newClient("token", srv.URL)
	ups, err := c.getUpdates(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].UpdateID != 7 || ups[0].Message.Text != "hi" || ups[0].Message.From.ID != 42 {
		t.Fatalf("parsed update wrong: %+v", ups)
	}
}

func TestBotMaintenance(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	ctx := context.Background()

	if got := b.Dispatch(ctx, admin, "/drain entryA"); !strings.Contains(got, "Drained") {
		t.Fatalf("drain by id: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "/resume Entry A"); !strings.Contains(got, "Resumed") {
		t.Fatalf("resume by name: %q", got)
	}
	if len(fs.maint) != 2 || fs.maint[0] != (maintCall{"entryA", true}) || fs.maint[1] != (maintCall{"entryA", false}) {
		t.Fatalf("maintenance calls = %+v", fs.maint)
	}
	if got := b.Dispatch(ctx, admin, "/drain nope"); !strings.Contains(got, "No such node") {
		t.Fatalf("unknown node: %q", got)
	}
}

func TestBotMaintenanceOwnership(t *testing.T) {
	ctx := context.Background()
	fs := &fakeStore{
		state: model.State{
			Nodes: []model.Node{
				{ID: "op-node", Name: "Op Exit", PublicRole: model.RoleExit, OwnerID: "op1"},
				{ID: "adm-node", Name: "Admin Exit", PublicRole: model.RoleExit},
			},
		},
		accounts: map[int64]model.AdminAccount{
			7: {Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"},
		},
	}
	b := New(fs, "token", "") // op1 authorizes via account binding

	// Operators get zero infra now: they cannot drain any node, even one
	// historically stamped with their namespace.
	if got := b.Dispatch(ctx, 7, "/drain op-node"); !strings.Contains(got, "admins only") {
		t.Fatalf("operator draining a node should be refused: %q", got)
	}
	if got := b.Dispatch(ctx, 7, "/drain adm-node"); !strings.Contains(got, "admins only") {
		t.Fatalf("operator draining admin node should be refused: %q", got)
	}
	if len(fs.maint) != 0 {
		t.Fatalf("no node should be toggled by an operator: %+v", fs.maint)
	}
}

func TestAddUserWizard(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	b.now = func() time.Time { return time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	if got := b.Dispatch(ctx, admin, "/adduser"); !strings.Contains(got, "step 1") {
		t.Fatalf("wizard start: %q", got)
	}
	// Duplicate name is rejected without advancing.
	if got := b.Dispatch(ctx, admin, "alice"); !strings.Contains(got, "already exists") {
		t.Fatalf("dup name: %q", got)
	}
	got := b.Dispatch(ctx, admin, "charlie")
	if !strings.Contains(got, "group") {
		t.Fatalf("name->group prompt: %q", got)
	}
	// WorkedExample has >1 group, so the prompt must NOT promise a '-' default it
	// cannot honor (the old wizard lied here).
	if strings.Contains(got, "'-'") || strings.Contains(got, "default") {
		t.Fatalf("multi-group prompt should not offer a default: %q", got)
	}
	if got := b.Dispatch(ctx, admin, "Everyone"); !strings.Contains(got, "expiry") {
		t.Fatalf("group->expiry prompt: %q", got)
	}
	got = b.Dispatch(ctx, admin, "30")
	if !strings.Contains(got, "Created") || !strings.Contains(got, "2026-07-27") {
		t.Fatalf("wizard create: %q", got)
	}
	last := fs.upsert[len(fs.upsert)-1]
	if last.Username != "charlie" || last.GroupID != "everyone" || last.ExpiresAt == nil {
		t.Fatalf("created user wrong: %+v", last)
	}
	// Flow is cleared after completion.
	if b.convoFor(admin) != nil {
		t.Fatalf("convo not cleared after create")
	}
}

func TestAddUserWizardCancel(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	ctx := context.Background()
	b.Dispatch(ctx, admin, "/adduser")
	if got := b.Dispatch(ctx, admin, "/cancel"); !strings.Contains(got, "Cancelled") {
		t.Fatalf("cancel: %q", got)
	}
	if b.convoFor(admin) != nil {
		t.Fatalf("convo should be cleared after cancel")
	}
	// A slash-command mid-flow also aborts the wizard and runs normally.
	b.Dispatch(ctx, admin, "/adduser")
	if got := b.Dispatch(ctx, admin, "/status"); strings.Contains(got, "step") {
		t.Fatalf("slash command should abort wizard, got %q", got)
	}
	if b.convoFor(admin) != nil {
		t.Fatalf("convo should be cleared by a slash command")
	}
}

func TestBotNodeMenu(t *testing.T) {
	fs := seedStore()
	b := newBot(fs)
	ctx := context.Background()

	// m:nodes lists nodes as tappable buttons.
	text, inline := b.Callback(ctx, admin, "m:nodes")
	if !strings.Contains(text, "tap to manage") || !strings.Contains(inline, "n:entryA") {
		t.Fatalf("node menu: %q / %q", text, inline)
	}
	// A node card for an admin offers a Drain button.
	text, inline = b.Callback(ctx, admin, "n:entryA")
	if !strings.Contains(inline, "nd:entryA") {
		t.Fatalf("node card should offer drain: %q / %q", text, inline)
	}
	// Tapping it drains and re-renders with Resume.
	_, inline = b.Callback(ctx, admin, "nd:entryA")
	if !strings.Contains(inline, "nr:entryA") {
		t.Fatalf("after drain, card should offer resume: %q", inline)
	}
	if len(fs.maint) != 1 || fs.maint[0] != (maintCall{"entryA", true}) {
		t.Fatalf("drain not recorded: %+v", fs.maint)
	}
}

func TestBotNodeMenuOwnership(t *testing.T) {
	ctx := context.Background()
	fs := &fakeStore{
		state: model.State{Nodes: []model.Node{{ID: "adm-node", Name: "Admin Exit", PublicRole: model.RoleExit}}},
		accounts: map[int64]model.AdminAccount{
			7: {Username: "op1", Role: model.RoleOperator, NamespaceID: "op1"},
		},
	}
	b := New(fs, "token", "")
	// An operator must NOT see per-node topology at all: a forged node-card
	// callback is redirected to the infra aggregate, never leaking
	// the node's name/role/health.
	text, inline := b.Callback(ctx, 7, "n:adm-node")
	if strings.Contains(text, "Admin Exit") || strings.Contains(text, "read-only") {
		t.Fatalf("operator must not see the node card: %q / %q", text, inline)
	}
	if !strings.Contains(text, "Infra") {
		t.Fatalf("operator node-card callback should redirect to the infra aggregate: %q", text)
	}
	// The node menu and typed /nodes are likewise redirected, not the raw list.
	if text, _ := b.Callback(ctx, 7, "m:nodes"); strings.Contains(text, "Admin Exit") {
		t.Fatalf("operator must not enumerate nodes via m:nodes: %q", text)
	}
	if out := b.Dispatch(ctx, 7, "/nodes"); strings.Contains(out, "Admin Exit") {
		t.Fatalf("operator /nodes must not list nodes: %q", out)
	}
	// And a forced drain callback is refused (operators get zero infra now).
	if text, _ := b.Callback(ctx, 7, "nd:adm-node"); !strings.Contains(text, "admins only") {
		t.Fatalf("forced drain should be refused: %q", text)
	}
}

// The config export transport uploads the QR image and the .toml file as
// multipart/form-data with the expected file fields.
func TestSendPhotoDocumentMultipart(t *testing.T) {
	var photoOK, docOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if _, _, err := r.FormFile("photo"); err == nil {
			photoOK = true
		}
		if _, _, err := r.FormFile("document"); err == nil {
			docOK = true
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := newClient("token", srv.URL)
	ctx := context.Background()
	if err := c.sendPhoto(ctx, 1, "qr.png", []byte("\x89PNG\r\n"), "cap"); err != nil {
		t.Fatalf("sendPhoto: %v", err)
	}
	if err := c.sendDocument(ctx, 1, "client.toml", []byte("x = 1\n"), "cap"); err != nil {
		t.Fatalf("sendDocument: %v", err)
	}
	if !photoOK || !docOK {
		t.Fatalf("multipart upload missing file field: photo=%v doc=%v", photoOK, docOK)
	}
}

// The users list pages instead of capping at 30, and the 🔍 search flow filters
// the scoped clients by name.
func TestUsersPaginationAndSearch(t *testing.T) {
	fs := &fakeStore{
		state:    model.State{Groups: []model.Group{{ID: "g1", Name: "g"}}},
		accounts: map[int64]model.AdminAccount{admin: {Username: "owner", Role: model.RoleAdmin}},
	}
	for i := 0; i < 20; i++ { // 20 clients -> 3 pages of 8
		name := fmt.Sprintf("user%02d", i)
		fs.state.Users = append(fs.state.Users, model.User{ID: "u-" + name, Username: name, DisplayName: name, Enabled: true})
	}
	b := New(fs, "token", "")
	ctx := context.Background()

	// Page 1: offers Next, not Prev.
	text, kb := b.Callback(ctx, admin, "m:users")
	if !strings.Contains(text, "page 1/3") || !strings.Contains(kb, "up:1") || strings.Contains(kb, `"up:-1"`) {
		t.Fatalf("first page nav wrong: %q kb=%q", text, kb)
	}
	// Last page: offers Prev, not Next past the end.
	if _, kb := b.Callback(ctx, admin, "up:2"); !strings.Contains(kb, "up:1") || strings.Contains(kb, "up:3") {
		t.Fatalf("last page nav wrong: %q", kb)
	}

	// Search is a one-shot flow: usrch arms it, the reply is a filtered list.
	b.Callback(ctx, admin, "usrch")
	if b.convoFor(admin) == nil {
		t.Fatalf("search should arm a conversation")
	}
	if got, _ := b.route(ctx, admin, "user01"); !strings.Contains(got, "Matches") || !strings.Contains(got, "user01") {
		t.Fatalf("search result wrong: %q", got)
	}
	if b.convoFor(admin) != nil {
		t.Fatalf("search should be one-shot (convo cleared)")
	}
	// A no-match query is handled gracefully.
	b.Callback(ctx, admin, "usrch")
	if got, _ := b.route(ctx, admin, "zzz"); !strings.Contains(got, "No clients match") {
		t.Fatalf("no-match search wrong: %q", got)
	}
}
