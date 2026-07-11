// Package bot is the operator-facing Telegram management bot. It runs as a
// separate process on the active exit, alongside the panel and
// Postgres-primary, and reads/writes fleet state through the store directly
// (localhost, same host). It is the management channel; the watchdog's
// one-way alert bot is separate and is NOT allowed to mutate state. Access is
// restricted to an allowlist of Telegram user IDs.
package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/clientcfg"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/store"
)

// DataStore is the slice of the store the bot needs. *store.Store satisfies it;
// tests use a fake.
type DataStore interface {
	LoadState(ctx context.Context) (model.State, error)
	UpsertUser(ctx context.Context, u model.User) error
	DeleteUser(ctx context.Context, id string) error
	UpsertGroup(ctx context.Context, g model.Group) error
	DeleteGroup(ctx context.Context, id string) error
	UpsertRoutePolicy(ctx context.Context, p model.RoutePolicy) error
	DeleteRoutePolicy(ctx context.Context, id string) error
	SetNodeMaintenance(ctx context.Context, id string, on bool) error
	UserTrafficTotals(ctx context.Context) ([]store.UserTrafficTotal, error)
	GetSettings(ctx context.Context) (model.Settings, error)
	AccountByTelegramID(ctx context.Context, telegramID int64) (model.AdminAccount, error)
	SetAccountLocale(ctx context.Context, username, locale string) error
	SetAccountTelegram(ctx context.Context, username string, telegramID *int64, alertChatID string) error
}

// Bot dispatches Telegram commands to store operations. Its live token comes
// from the DB (panel Bots tab) and is refreshed each Run iteration, so an
// operator can connect or rotate the bot from the panel without a restart. The
// token passed to New is the fallback used when settings are disabled. Who may
// use the bot is decided per-account by the Telegram binding (see authorize).
type Bot struct {
	store   DataStore
	client  *client
	now     func() time.Time
	baseURL string
	token   string // currently-applied token ("" = idle)

	initToken string // fallback token (from flags/file)

	idleLogged      bool
	commandsSet     bool // setMyCommands registered for the current token
	lastSettingsErr string

	// convos holds in-progress guided flows (the text wizards), keyed by the
	// sender's Telegram id. A slash-command or /cancel clears the flow.
	convoMu sync.Mutex
	convos  map[int64]*convo
}

// convoKind discriminates the guided text flows that all share the same "consume
// a plain-text reply; any /cmd resets" machinery.
type convoKind int

const (
	kAddUser convoKind = iota
	kNewGroup
	kRenameGroup
	kNewRoute
	kSearchUsers
)

// convo is the state of a step-by-step guided flow. The fields used depend on
// kind. Match input is typed; the other route/user choices are tapped from
// inline buttons that feed the same flow.
type convo struct {
	kind convoKind
	step int
	// kAddUser
	name  string
	group string
	// kRenameGroup
	groupID string
	// kNewRoute
	rt     routeDraft
	editID string // when set, the route wizard updates this policy instead of creating
}

// route wizard steps. Admins start at rsTier (to author the infra baseline);
// operators start at rsMatch (their routes are always exit-tier). rsMatch is the
// "match builder" hub: it shows the match set accumulated so far and lets the
// operator add more kinds (domain/geosite/geoip/cidr — they combine, like the
// panel) or continue. rsMatchVals is the per-kind value screen, where common
// geosite categories and countries are one-tap quick-adds and anything else is
// typed. rsConfirm shows a summary before anything is written.
const (
	rsTier      = iota
	rsMatch     // the match-builder hub
	rsMatchVals // entering values for the currently-picked kind
	rsAction
	rsExit
	rsConfirm
)

// routeDraft accumulates a route across the wizard steps. Each match kind has its
// own slot so a single rule can combine several (domains AND geosite AND geoip AND
// cidr), mirroring the panel. editKind names the slot the value screen is editing.
type routeDraft struct {
	domains  []string
	geosite  []string
	geoip    []string
	cidrs    []string
	editKind string // slot the value screen is editing: "domain"|"geosite"|"geoip"|"cidr"
	action   model.RuleAction
	exitID   string
	tier     model.RuleTier
}

// slot returns a pointer to the draft's value list for a match kind.
func (d *routeDraft) slot(kind string) *[]string {
	switch kind {
	case "geosite":
		return &d.geosite
	case "geoip":
		return &d.geoip
	case "cidr":
		return &d.cidrs
	default:
		return &d.domains
	}
}

// toggle adds val to a kind's slot if absent, removes it if present (for the
// one-tap quick-add buttons).
func (d *routeDraft) toggle(kind, val string) {
	s := d.slot(kind)
	for i, v := range *s {
		if v == val {
			*s = append((*s)[:i], (*s)[i+1:]...)
			return
		}
	}
	*s = append(*s, val)
}

// addValues appends the typed values to a kind's slot, skipping duplicates.
func (d *routeDraft) addValues(kind string, vals []string) {
	s := d.slot(kind)
	for _, v := range vals {
		if !containsStr(*s, v) {
			*s = append(*s, v)
		}
	}
}

// has reports whether val is already in a kind's slot (for the ✓ toggle marks).
func (d *routeDraft) has(kind, val string) bool { return containsStr(*d.slot(kind), val) }

// total counts the match values across all kinds.
func (d *routeDraft) total() int {
	return len(d.domains) + len(d.geosite) + len(d.geoip) + len(d.cidrs)
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// botCommandsJSON registers the "/" command menu in the Telegram UI. It is kept
// deliberately short: the bot is button-driven, so the "/" list is just a handful
// of entry points (everything else is reachable by tapping) rather than a wall
// mirroring every command.
const botCommandsJSON = `[` +
	`{"command":"menu","description":"open the interactive menu"},` +
	`{"command":"adduser","description":"create a client"},` +
	`{"command":"config","description":"export a client config: /config <name>"},` +
	`{"command":"help","description":"open the menu"}` +
	`]`

// removeKeyboardMarkup clears any persistent reply keyboard a previous build may
// have set (the bot is now purely inline-driven). Sent once on /start.
const removeKeyboardMarkup = `{"remove_keyboard":true}`

// New builds a bot. baseURL is "" for the real Telegram API (override in tests).
// token is the fallback config used when panel settings are disabled.
func New(st DataStore, token, baseURL string) *Bot {
	b := &Bot{store: st, now: time.Now, baseURL: baseURL, initToken: token, convos: map[int64]*convo{}}
	b.apply(token)
	return b
}

// apply swaps in a token, recreating the Telegram client only when the token
// actually changes (so hot-reload is cheap and connection-stable).
func (b *Bot) apply(token string) {
	if b.client == nil || token != b.token {
		b.token = token
		b.client = newClient(token, b.baseURL)
		b.commandsSet = false // re-register the command menu for the new token
	}
}

// refreshFromSettings reloads bot config from the DB. Panel settings (when
// enabled with a token) win; otherwise it falls back to the flag/file token.
func (b *Bot) refreshFromSettings(ctx context.Context) {
	s, err := b.store.GetSettings(ctx)
	if err != nil {
		if msg := err.Error(); msg != b.lastSettingsErr { // log once per distinct error
			log.Printf("bot: load settings: %v", err)
			b.lastSettingsErr = msg
		}
		return // keep whatever config we last had
	}
	b.lastSettingsErr = ""
	if s.Bot.Enabled && s.Bot.Token != "" {
		prev := b.token
		b.apply(s.Bot.Token)
		if b.token != prev {
			log.Printf("bot: token applied from panel settings")
		}
		return
	}
	b.apply(b.initToken)
}

// Run long-polls Telegram and handles updates until ctx is cancelled. Config is
// re-read from the DB each iteration; with no token configured it idles quietly
// (so an unconfigured bot service is harmless) and picks up a token the moment
// one is saved in the panel.
func (b *Bot) Run(ctx context.Context) {
	var offset int64
	log.Printf("bot: started")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		b.refreshFromSettings(ctx)
		if b.token == "" {
			if !b.idleLogged {
				log.Printf("bot: no token configured — idle (set one in the panel Bots tab)")
				b.idleLogged = true
			}
			if sleepCtx(ctx, 15*time.Second) {
				return
			}
			continue
		}
		b.idleLogged = false
		if !b.commandsSet {
			if err := b.client.setMyCommands(ctx, botCommandsJSON); err == nil {
				b.commandsSet = true
			}
		}
		updates, err := b.client.getUpdates(ctx, offset, 50)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("bot: getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			b.handleUpdate(ctx, u)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, u update) {
	ctx = withLang(ctx, b.langForUpdate(ctx, u))
	// Inline-button tap: edit the message in place and ack.
	if cq := u.CallbackQuery; cq != nil && cq.From != nil {
		// The config buttons reveal connection credentials — refuse them outside a
		// private chat (an inline menu may live in a group).
		if isCredentialCallback(cq.Data) && cq.Message != nil && cq.Message.Chat != nil && cq.Message.Chat.Type != "private" {
			_ = b.client.answerCallbackQuery(ctx, cq.ID, tr(ctx, "🔒 Message the bot privately (DM) to export a config."), true)
			return
		}
		// QR/TOML export deliver a fresh photo/document message rather than editing
		// the menu in place, so they are handled here instead of via Callback.
		if strings.HasPrefix(cq.Data, "cfgqr:") || strings.HasPrefix(cq.Data, "cfgtoml:") {
			if cq.Message != nil && cq.Message.Chat != nil {
				b.sendConfigArtifact(ctx, cq.Message.Chat.ID, cq.From.ID, cq.Data)
			}
			_ = b.client.answerCallbackQuery(ctx, cq.ID, "", false)
			return
		}
		text, inline := b.Callback(ctx, cq.From.ID, cq.Data)
		_ = b.client.answerCallbackQuery(ctx, cq.ID, "", false)
		if text != "" && cq.Message != nil && cq.Message.Chat != nil {
			if err := b.client.editMessageText(ctx, cq.Message.Chat.ID, cq.Message.MessageID, text, inline); err != nil {
				log.Printf("bot: editMessageText: %v", err)
			}
		}
		return
	}
	if u.Message == nil || u.Message.Chat == nil || u.Message.From == nil {
		return
	}
	// /config emits a credential-bearing deep link; never let it land in a group.
	if isConfigCmd(u.Message.Text) && u.Message.Chat.Type != "private" {
		_ = b.client.sendMessage(ctx, u.Message.Chat.ID,
			tr(ctx, "🔒 /config reveals connection credentials — message me privately (DM) to export it."), "")
		return
	}
	// A persistent reply keyboard from an older build lingers on the client until
	// explicitly removed; clear it once on /start (the bot is now inline-only). The
	// inline menu follows from route() below.
	if isStartCmd(u.Message.Text) {
		_ = b.client.sendMessage(ctx, u.Message.Chat.ID, tr(ctx, "TrustPanel bot — use the buttons below."), removeKeyboardMarkup)
	}
	reply, inline := b.route(ctx, u.Message.From.ID, u.Message.Text)
	if reply == "" {
		return
	}
	// The interactive surface is the inline tiled menu (/menu); a reply only carries
	// a keyboard when the command/flow produced one. There is no persistent reply
	// keyboard.
	if err := b.client.sendMessage(ctx, u.Message.Chat.ID, reply, inline); err != nil {
		log.Printf("bot: sendMessage: %v", err)
	}
}

// langForUpdate prefers the sender account's saved panel locale over the
// Telegram-reported UI language, so the bot speaks the language the operator
// picked in the panel. Falls back to the Telegram language_code when unset.
func (b *Bot) langForUpdate(ctx context.Context, u update) string {
	var fromID int64
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.From != nil:
		fromID = u.CallbackQuery.From.ID
	case u.Message != nil && u.Message.From != nil:
		fromID = u.Message.From.ID
	}
	if fromID != 0 {
		if a, err := b.store.AccountByTelegramID(ctx, fromID); err == nil && a.Locale != "" {
			return normalizeLang(a.Locale)
		}
	}
	return updateLang(u)
}

// route handles message text: /menu opens the interactive inline menu (returning
// an inline keyboard); everything else falls through to Dispatch as plain text.
func (b *Bot) route(ctx context.Context, fromID int64, text string) (reply, inline string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) > 0 {
		cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
		if i := strings.IndexByte(cmd, '@'); i >= 0 {
			cmd = cmd[:i]
		}
		switch cmd {
		case "menu", "start", "help", "groups", "routes", "account":
			acct, ok := b.authorize(ctx, fromID)
			if !ok {
				return notAuthorized(ctx, fromID), ""
			}
			b.clearConvo(fromID) // a slash-command aborts any guided flow
			switch cmd {
			case "groups":
				return b.menuGroups(ctx, acct)
			case "routes":
				return b.menuRoutes(ctx, acct)
			case "account":
				return b.menuAccount(ctx, acct)
			default:
				return b.menuRoot(ctx, acct)
			}
		}
	}
	// A plain-text reply feeding an active guided flow is routed here (not just in
	// Dispatch) so the next prompt can carry inline buttons — Dispatch returns text
	// only and is kept for the typed/tested path.
	if !strings.HasPrefix(strings.TrimSpace(text), "/") {
		if c := b.convoFor(fromID); c != nil {
			acct, ok := b.authorize(ctx, fromID)
			if !ok {
				return notAuthorized(ctx, fromID), ""
			}
			return b.advanceConvo(ctx, acct, fromID, c, strings.TrimSpace(text))
		}
	}
	return b.Dispatch(ctx, fromID, text), ""
}

// Dispatch authorizes the sender, parses one command and returns the reply text
// (the unit tested directly). An empty reply means "say nothing".
func (b *Bot) Dispatch(ctx context.Context, fromID int64, text string) string {
	acct, ok := b.authorize(ctx, fromID)
	if !ok {
		return notAuthorized(ctx, fromID)
	}
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return ""
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	// Strip a @BotName suffix Telegram appends in groups.
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}

	// Guided flows (the /adduser wizard) consume plain-text replies. /cancel always
	// aborts; any other slash-command also aborts and is then handled normally.
	if cmd == "cancel" {
		if b.clearConvo(fromID) {
			return tr(ctx, "Cancelled.")
		}
		return tr(ctx, "Nothing to cancel.")
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "/") {
		if c := b.convoFor(fromID); c != nil {
			reply, _ := b.advanceConvo(ctx, acct, fromID, c, strings.TrimSpace(text))
			return reply
		}
	} else {
		b.clearConvo(fromID)
	}

	args := fields[1:]

	switch cmd {
	case "start", "help":
		// The interactive surface is the inline menu; /start and /help open it rather
		// than printing a wall of commands (route() attaches the keyboard).
		text, _ := b.menuRoot(ctx, acct)
		return text
	case "menu":
		// /menu is normally handled by route() (it carries an inline keyboard); if
		// it reaches here (e.g. direct Dispatch call) reply with the menu text.
		text, _ := b.menuRoot(ctx, acct)
		return text
	case "groups":
		text, _ := b.menuGroups(ctx, acct)
		return text
	case "routes":
		text, _ := b.menuRoutes(ctx, acct)
		return text
	case "account":
		text, _ := b.menuAccount(ctx, acct)
		return text
	case "status":
		return b.cmdStatus(ctx, acct)
	case "nodes":
		return b.cmdNodes(ctx, acct)
	case "namespaces", "lens":
		// Hidden bootstrap-only cross-namespace lens (kept out of the "/" command
		// menu). lensList already denies non-bootstrap callers.
		text, _ := b.lensList(ctx, acct)
		return text
	case "users":
		return b.cmdUsers(ctx, acct)
	case "user":
		return b.cmdUser(ctx, acct, args)
	case "traffic":
		return b.cmdTraffic(ctx, acct)
	case "adduser":
		if len(args) == 0 {
			return b.startAddUser(ctx, fromID) // no args -> guided wizard
		}
		return b.cmdAddUser(ctx, acct, args)
	case "config":
		return b.cmdConfig(ctx, acct, args)
	case "enable":
		return b.cmdSetEnabled(ctx, acct, args, true)
	case "disable":
		return b.cmdSetEnabled(ctx, acct, args, false)
	case "drain":
		return b.cmdMaintenance(ctx, acct, args, true)
	case "resume":
		return b.cmdMaintenance(ctx, acct, args, false)
	default:
		return tr(ctx, "Unknown command. Try /help.")
	}
}

// notAuthorized is the reply to an unbound sender: silence. The bot
// looks dead to anyone not bound in the panel — no id echo, no command-behavior
// leak, no probing surface. Operators learn their numeric id via a public id-bot
// (@userinfobot), not ours; both roles are bound in the panel, so the bot never
// needs to talk to an unbound user.
func notAuthorized(ctx context.Context, fromID int64) string { return "" }

// isConfigCmd reports whether text is the /config command (with an optional
// @BotName suffix). Used to gate credential export to private chats.
func isConfigCmd(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd == "config"
}

// isStartCmd reports whether text is the /start command (with an optional @BotName
// suffix). Used to clear a stale reply keyboard on first contact.
func isStartCmd(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd == "start"
}

// isCredentialCallback reports whether a callback token exports connection
// credentials (the config card or its QR/TOML buttons), so handleUpdate can gate
// all three to private chats.
func isCredentialCallback(data string) bool {
	return strings.HasPrefix(data, "cfg:") ||
		strings.HasPrefix(data, "cfgqr:") ||
		strings.HasPrefix(data, "cfgtoml:")
}

// sendConfigArtifact answers a QR/TOML export tap by building the client config and
// delivering it as a fresh message: a scannable QR image, or the importable .toml
// file. The lookup is namespace-scoped (an operator exports only its own clients)
// and the private-chat guard in handleUpdate keeps the secret off shared chats.
func (b *Bot) sendConfigArtifact(ctx context.Context, chatID, fromID int64, data string) {
	acct, ok := b.authorize(ctx, fromID)
	if !ok {
		return
	}
	id, qr := "", false
	switch {
	case strings.HasPrefix(data, "cfgqr:"):
		id, qr = strings.TrimPrefix(data, "cfgqr:"), true
	case strings.HasPrefix(data, "cfgtoml:"):
		id = strings.TrimPrefix(data, "cfgtoml:")
	default:
		return
	}
	fail := func(msg string) { _ = b.client.sendMessage(ctx, chatID, msg, "") }
	st, err := b.scoped(ctx, acct)
	if err != nil {
		fail(errMsg(err))
		return
	}
	u, ok := userByID(st, id)
	if !ok {
		fail(tr(ctx, "No such user."))
		return
	}
	entry, ok := pickEntry(st)
	if !ok {
		fail(tr(ctx, "No entry nodes in the fleet yet — add one in the panel first."))
		return
	}
	cfg, err := clientcfg.Build(st, u.ID, entry.ID, clientcfg.Options{}, b.now())
	if err != nil {
		fail(errMsg(err))
		return
	}
	if qr {
		png, err := qrcode.Encode(cfg.DeepLink, qrcode.Medium, 512)
		if err != nil {
			fail(errMsg(err))
			return
		}
		if err := b.client.sendPhoto(ctx, chatID, "qr.png", png, trf(ctx, "📷 QR for %s — scan in TrustTunnel", displayName(u))); err != nil {
			log.Printf("bot: sendPhoto: %v", err)
		}
		return
	}
	if err := b.client.sendDocument(ctx, chatID, cfg.Filename, []byte(cfg.TOML), trf(ctx, "📄 %s", cfg.Filename)); err != nil {
		log.Printf("bot: sendDocument: %v", err)
	}
}

// ---- inline menu levels ----

// ikBtn is one inline-keyboard button: a label and the callback token it sends.
type ikBtn struct{ text, data string }

// inlineKeyboard renders rows of buttons as a Telegram reply_markup JSON value.
func inlineKeyboard(rows [][]ikBtn) string {
	type tgBtn struct {
		Text string `json:"text"`
		Data string `json:"callback_data"`
	}
	kb := make([][]tgBtn, 0, len(rows))
	for _, row := range rows {
		r := make([]tgBtn, 0, len(row))
		for _, btn := range row {
			r = append(r, tgBtn{btn.text, btn.data})
		}
		kb = append(kb, r)
	}
	out, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
	return string(out)
}

// backRow is the standard "back to main menu" button row.
func backRow(ctx context.Context) []ikBtn { return []ikBtn{{tr(ctx, "⬅ Menu"), "m:root"}} }

// cancelRow is the standard "abort the active guided flow" button row. Every
// wizard prompt carries it, so a flow can always be left with one tap rather than
// by typing /cancel (which still works as a fallback).
func cancelRow(ctx context.Context) []ikBtn { return []ikBtn{{tr(ctx, "✖ Cancel"), "cx"}} }

// Callback handles an inline-button tap: it authorizes the sender, routes the
// callback token, and returns the new message text + inline keyboard to render in
// place. Like Dispatch, it is pure and unit-tested. All reads/writes go through
// the same namespace scoping as the typed commands.
func (b *Bot) Callback(ctx context.Context, fromID int64, data string) (text, inline string) {
	acct, ok := b.authorize(ctx, fromID)
	if !ok {
		return notAuthorized(ctx, fromID), ""
	}
	switch {
	case data == "cx": // cancel the active guided flow
		b.clearConvo(fromID)
		return b.menuRoot(ctx, acct)
	case data == "m:root":
		return b.menuRoot(ctx, acct)
	case data == "m:users":
		return b.menuUsers(ctx, acct)
	case strings.HasPrefix(data, "up:"): // users list, page N
		page, _ := strconv.Atoi(strings.TrimPrefix(data, "up:"))
		return b.menuUsersPage(ctx, acct, page)
	case data == "usrch": // start the client search flow
		return b.startUserSearch(ctx, fromID)
	case data == "m:nodes":
		return b.menuNodes(ctx, acct)
	case data == "m:traffic":
		return b.cmdTraffic(ctx, acct), inlineKeyboard([][]ikBtn{backRow(ctx)})
	case data == "m:groups":
		return b.menuGroups(ctx, acct)
	case data == "m:routes":
		return b.menuRoutes(ctx, acct)
	case data == "m:acct":
		return b.menuAccount(ctx, acct)
	case data == "m:infra":
		return b.menuInfra(ctx, acct)
	case data == "m:lens":
		return b.lensList(ctx, acct)
	case data == "m:status": // legacy button on older inline messages; status now lives in the root header
		return b.menuRoot(ctx, acct)
	case data == "m:adduser":
		return b.startAddUser(ctx, fromID), inlineKeyboard([][]ikBtn{cancelRow(ctx)})
	case strings.HasPrefix(data, "u:"):
		return b.userCard(ctx, acct, strings.TrimPrefix(data, "u:"))
	case strings.HasPrefix(data, "ue:"):
		return b.userAction(ctx, acct, strings.TrimPrefix(data, "ue:"), "enable")
	case strings.HasPrefix(data, "ud:"):
		return b.userAction(ctx, acct, strings.TrimPrefix(data, "ud:"), "disable")
	case strings.HasPrefix(data, "uxm:"):
		return b.userExtendMenu(ctx, acct, strings.TrimPrefix(data, "uxm:"))
	case strings.HasPrefix(data, "uxp:"):
		return b.userExtendApply(ctx, acct, strings.TrimPrefix(data, "uxp:"))
	case strings.HasPrefix(data, "ucgp:"):
		return b.userChangeGroupApply(ctx, acct, strings.TrimPrefix(data, "ucgp:"))
	case strings.HasPrefix(data, "ucg:"):
		return b.userChangeGroupMenu(ctx, acct, strings.TrimPrefix(data, "ucg:"))
	case strings.HasPrefix(data, "udel:"):
		return b.confirmDeleteUser(ctx, acct, strings.TrimPrefix(data, "udel:"))
	case strings.HasPrefix(data, "udz:"):
		return b.userDelete(ctx, acct, strings.TrimPrefix(data, "udz:"))
	case strings.HasPrefix(data, "cfg:"):
		return b.userConfig(ctx, acct, strings.TrimPrefix(data, "cfg:"))
	// add-user wizard picks
	case strings.HasPrefix(data, "aug:"):
		return b.addUserPickGroup(ctx, acct, fromID, strings.TrimPrefix(data, "aug:"))
	case strings.HasPrefix(data, "aue:"):
		return b.addUserPickExpiry(ctx, acct, fromID, strings.TrimPrefix(data, "aue:"))
	// groups
	case data == "gadd":
		return b.startNewGroup(ctx, fromID), inlineKeyboard([][]ikBtn{cancelRow(ctx)})
	case strings.HasPrefix(data, "gdefp:"):
		return b.groupSetDefault(ctx, acct, strings.TrimPrefix(data, "gdefp:"))
	case strings.HasPrefix(data, "gdef:"):
		return b.groupDefaultPicker(ctx, acct, strings.TrimPrefix(data, "gdef:"))
	case strings.HasPrefix(data, "grn:"):
		return b.startRenameGroup(ctx, acct, fromID, strings.TrimPrefix(data, "grn:"))
	case strings.HasPrefix(data, "gdel:"):
		return b.confirmDeleteGroup(ctx, acct, strings.TrimPrefix(data, "gdel:"))
	case strings.HasPrefix(data, "gdz:"):
		return b.groupDelete(ctx, acct, strings.TrimPrefix(data, "gdz:"))
	case strings.HasPrefix(data, "g:"):
		return b.groupCard(ctx, acct, strings.TrimPrefix(data, "g:"))
	// routes
	case data == "radd":
		return b.startNewRoute(ctx, acct, fromID)
	case strings.HasPrefix(data, "redit:"):
		return b.startEditRoute(ctx, acct, fromID, strings.TrimPrefix(data, "redit:"))
	case strings.HasPrefix(data, "rt:"): // wizard: tier pick
		return b.routePickTier(ctx, acct, fromID, strings.TrimPrefix(data, "rt:"))
	case strings.HasPrefix(data, "rmk:"): // wizard: pick a match kind to add
		return b.routePickMatchKind(ctx, acct, fromID, strings.TrimPrefix(data, "rmk:"))
	case strings.HasPrefix(data, "rqa:"): // wizard: quick-add/remove a curated match value
		return b.routeQuickAdd(ctx, acct, fromID, strings.TrimPrefix(data, "rqa:"))
	case data == "rcont": // wizard: match set done, continue to the action step
		return b.routeContinue(ctx, acct, fromID)
	case data == "rback": // wizard: step back one screen (distinct from ✖ Cancel)
		return b.routeBack(ctx, acct, fromID)
	case strings.HasPrefix(data, "ra:"): // wizard: action pick
		return b.routePickAction(ctx, acct, fromID, strings.TrimPrefix(data, "ra:"))
	case strings.HasPrefix(data, "rxp:"): // wizard: exit pick
		return b.routePickExit(ctx, acct, fromID, strings.TrimPrefix(data, "rxp:"))
	case data == "rok": // wizard: confirm & create
		return b.routeConfirm(ctx, acct, fromID)
	case strings.HasPrefix(data, "rup:"): // reorder: move earlier
		return b.routeReorder(ctx, acct, strings.TrimPrefix(data, "rup:"), true)
	case strings.HasPrefix(data, "rdn:"): // reorder: move later
		return b.routeReorder(ctx, acct, strings.TrimPrefix(data, "rdn:"), false)
	case strings.HasPrefix(data, "re:"):
		return b.routeToggle(ctx, acct, strings.TrimPrefix(data, "re:"), true)
	case strings.HasPrefix(data, "rx:"):
		return b.routeToggle(ctx, acct, strings.TrimPrefix(data, "rx:"), false)
	case strings.HasPrefix(data, "rdel:"):
		return b.confirmDeleteRoute(ctx, acct, strings.TrimPrefix(data, "rdel:"))
	case strings.HasPrefix(data, "rdz:"):
		return b.routeDelete(ctx, acct, strings.TrimPrefix(data, "rdz:"))
	case strings.HasPrefix(data, "r:"):
		return b.routeCard(ctx, acct, strings.TrimPrefix(data, "r:"))
	// account
	case strings.HasPrefix(data, "lang:"):
		return b.accountSetLang(ctx, acct, strings.TrimPrefix(data, "lang:"))
	case strings.HasPrefix(data, "alert:"):
		return b.accountSetAlert(ctx, acct, fromID, strings.TrimPrefix(data, "alert:"))
	// lens (bootstrap)
	case strings.HasPrefix(data, "lens:"):
		return b.lensView(ctx, acct, strings.TrimPrefix(data, "lens:"))
	// nodes
	case strings.HasPrefix(data, "n:"):
		return b.nodeCard(ctx, acct, strings.TrimPrefix(data, "n:"))
	case strings.HasPrefix(data, "nd:"):
		return b.nodeAction(ctx, acct, strings.TrimPrefix(data, "nd:"), true)
	case strings.HasPrefix(data, "nr:"):
		return b.nodeAction(ctx, acct, strings.TrimPrefix(data, "nr:"), false)
	default:
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
}

// menuRoot is the top-level inline menu. It doubles as the status card — the
// control-plane summary is the header, so "Status" is no longer a separate button
// that just duplicates the Nodes view. The buttons are the management entry
// points (open a user/node to act on it; ➕ starts the guided create). Branches
// are the same scoped views as the typed commands.
func (b *Bot) menuRoot(ctx context.Context, acct model.AdminAccount) (string, string) {
	header := tr(ctx, "TrustPanel — main menu") + "\n\n" + b.statusHeader(ctx, acct)
	rows := [][]ikBtn{
		{{tr(ctx, "👤 Users"), "m:users"}, {tr(ctx, "📊 Traffic"), "m:traffic"}},
		{{tr(ctx, "➕ New user"), "m:adduser"}, {tr(ctx, "🧭 Routes"), "m:routes"}},
		{{tr(ctx, "👥 Groups"), "m:groups"}, {tr(ctx, "⚙️ Account"), "m:acct"}},
	}
	if acct.CanManageInfra() {
		// Admins manage shared infra. The bootstrap owner's cross-namespace lens is
		// deliberately NOT surfaced here — it's a buried, occasional tool reached via
		// the hidden /namespaces command, so the default menu stays scoped to one
		// tenant and nobody browses peers' clients by reflex.
		rows = append(rows, []ikBtn{{tr(ctx, "🌐 Nodes"), "m:nodes"}})
	} else {
		// Operators get a read-only infra-health aggregate, not node management.
		rows = append(rows, []ikBtn{{tr(ctx, "🩺 Infra") + " " + b.infraIcon(ctx), "m:infra"}})
	}
	return header, inlineKeyboard(rows)
}

// statusHeader is the root-screen status line. Admins (bootstrap or co-owner)
// keep infra in scope, so cmdStatus renders nodes directly. An operator's Nodes
// are nulled, so its header takes node health from the infra aggregate —
// counts only, never identifiers.
func (b *Bot) statusHeader(ctx context.Context, acct model.AdminAccount) string {
	if acct.CanManageInfra() {
		return b.cmdStatus(ctx, acct)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	agg := b.infraAgg(ctx)
	ns := acct.Namespace()
	if ns == "" {
		ns = "—"
	}
	return trf(ctx, "🏠 %s · clients %d · groups %d\nInfra: %s (%d nodes)",
		ns, len(st.Users), len(st.Groups), aggIcon(agg), agg.Total)
}

// infraHealth is the non-identifying fleet summary: counts and a single
// active-exit health bit. It is the ONLY read that bypasses ScopeState, and it
// emits no node names/IPs.
type infraHealth struct {
	Total, Healthy, Unhealthy int
	ActiveOK                  bool
}

func infraAggregate(st model.State) infraHealth {
	var h infraHealth
	for _, n := range st.Nodes {
		h.Total++
		if n.Health == model.HealthHealthy {
			h.Healthy++
		} else {
			h.Unhealthy++
		}
	}
	if a, ok := st.NodeByID(st.ControlPlane.ActiveNodeID); ok {
		h.ActiveOK = a.Health == model.HealthHealthy && !a.Maintenance
	}
	return h
}

// infraAgg loads the full (unscoped) state and computes the aggregate. The raw
// read is deliberate — an operator's scoped state has no nodes.
func (b *Bot) infraAgg(ctx context.Context) infraHealth {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return infraHealth{}
	}
	return infraAggregate(st)
}

func aggIcon(h infraHealth) string {
	if h.Total == 0 || h.Unhealthy > 0 || !h.ActiveOK {
		return "⚠️"
	}
	return "✅"
}

func (b *Bot) infraIcon(ctx context.Context) string { return aggIcon(b.infraAgg(ctx)) }

// usersPerPage bounds one page of the users list so the keyboard stays well within
// Telegram's limits while the whole list stays reachable by paging.
const usersPerPage = 8

// menuUsers opens the first page of the scoped users list.
func (b *Bot) menuUsers(ctx context.Context, acct model.AdminAccount) (string, string) {
	return b.menuUsersPage(ctx, acct, 0)
}

// menuUsersPage lists one page of the scoped users as buttons that open each user's
// card, with ‹ Prev / Next › navigation and a 🔍 Search entry. Replaces the old
// hard cap of 30 that dead-ended into a plain-text /users list.
func (b *Bot) menuUsersPage(ctx context.Context, acct model.AdminAccount, page int) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	users := append([]model.User(nil), st.Users...)
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	if len(users) == 0 {
		rows := [][]ikBtn{{{tr(ctx, "➕ New user"), "m:adduser"}}, backRow(ctx)}
		return tr(ctx, "No users yet."), inlineKeyboard(rows)
	}
	pages := (len(users) + usersPerPage - 1) / usersPerPage
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	start := page * usersPerPage
	end := start + usersPerPage
	if end > len(users) {
		end = len(users)
	}
	var rows [][]ikBtn
	for _, u := range users[start:end] {
		rows = append(rows, []ikBtn{{enabledIcon(u.Enabled) + " " + displayName(u), "u:" + u.ID}})
	}
	if pages > 1 { // page navigation, edges omitted
		var nav []ikBtn
		if page > 0 {
			nav = append(nav, ikBtn{tr(ctx, "‹ Prev"), "up:" + strconv.Itoa(page-1)})
		}
		if page < pages-1 {
			nav = append(nav, ikBtn{tr(ctx, "Next ›"), "up:" + strconv.Itoa(page+1)})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []ikBtn{{tr(ctx, "🔍 Search"), "usrch"}, {tr(ctx, "➕ New user"), "m:adduser"}})
	rows = append(rows, backRow(ctx))
	text := trf(ctx, "Clients (%d) — tap to manage", len(users))
	if pages > 1 {
		text = trf(ctx, "Clients (%d) — page %d/%d, tap to manage", len(users), page+1, pages)
	}
	return text, inlineKeyboard(rows)
}

// menuUsersSearch renders the scoped clients whose name matches query as buttons.
// It is the terminal step of the 🔍 Search flow (a one-shot filter, not a mode).
func (b *Bot) menuUsersSearch(ctx context.Context, acct model.AdminAccount, query string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var matches []model.User
	for _, u := range st.Users {
		if strings.Contains(strings.ToLower(displayName(u)), q) || strings.Contains(strings.ToLower(u.Username), q) {
			matches = append(matches, u)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return displayName(matches[i]) < displayName(matches[j]) })
	if len(matches) == 0 {
		return trf(ctx, "No clients match %q.", query),
			inlineKeyboard([][]ikBtn{{{tr(ctx, "🔍 Search again"), "usrch"}}, {{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	const cap = 20 // a search should be specific; keep the keyboard bounded
	capped := len(matches) > cap
	if capped {
		matches = matches[:cap]
	}
	var rows [][]ikBtn
	for _, u := range matches {
		rows = append(rows, []ikBtn{{enabledIcon(u.Enabled) + " " + displayName(u), "u:" + u.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "🔍 Search again"), "usrch"}, {tr(ctx, "⬅ Users"), "m:users"}})
	text := trf(ctx, "Matches for %q:", query)
	if capped {
		text = trf(ctx, "Matches for %q (first %d — narrow it down):", query, cap)
	}
	return text, inlineKeyboard(rows)
}

// startUserSearch arms the one-shot search flow and prompts for a query.
func (b *Bot) startUserSearch(ctx context.Context, fromID int64) (string, string) {
	b.startConvo(fromID, &convo{kind: kSearchUsers})
	return tr(ctx, "🔍 Send part of a client's name to search."), inlineKeyboard([][]ikBtn{cancelRow(ctx)})
}

// userCard shows one user's detail with action buttons. The lookup is scoped, so
// an operator can only open its own clients (others resolve as not found).
func (b *Bot) userCard(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	text := b.userDetail(ctx, st, u)
	toggle := ikBtn{tr(ctx, "🟢 Enable"), "ue:" + u.ID}
	if u.Enabled {
		toggle = ikBtn{tr(ctx, "⚪ Disable"), "ud:" + u.ID}
	}
	rows := [][]ikBtn{
		{toggle, {tr(ctx, "⏳ Extend"), "uxm:" + u.ID}},
		{{tr(ctx, "📁 Group"), "ucg:" + u.ID}, {tr(ctx, "📲 Config"), "cfg:" + u.ID}},
		{{tr(ctx, "🗑 Delete"), "udel:" + u.ID}},
		{{tr(ctx, "⬅ Users"), "m:users"}},
	}
	return text, inlineKeyboard(rows)
}

// userDetail renders the shared user card body (used by the inline card and the
// /user command), localized for the request language.
func (b *Bot) userDetail(ctx context.Context, st model.State, u model.User) string {
	rx, tx := b.userTraffic(ctx, u.ID)
	group := groupNameByID(st, u.GroupID)
	exp := tr(ctx, "never")
	if u.ExpiresAt != nil {
		exp = u.ExpiresAt.UTC().Format("2006-01-02")
	}
	return trf(ctx, "%s %s\nusername: %s\nenabled: %s\ngroup: %s\nexpires: %s\ntraffic: ↑%s ↓%s (total %s)",
		enabledIcon(u.Enabled), displayName(u), u.Username, yesNo(ctx, u.Enabled), group, exp,
		humanBytes(rx), humanBytes(tx), humanBytes(rx+tx))
}

// userConfig renders a user's exportable client config (deep link + QR) from the
// card. The lookup is scoped; the privacy guard in handleUpdate keeps this off
// shared chats since the deep link carries the secret.
func (b *Bot) userConfig(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	// The text carries the deep link + QR landing link; the buttons deliver the QR
	// as an image and the config as an importable .toml file (both sent as fresh
	// messages by handleUpdate, not an in-place edit).
	rows := [][]ikBtn{
		{{tr(ctx, "📷 QR image"), "cfgqr:" + u.ID}, {tr(ctx, "📄 TOML file"), "cfgtoml:" + u.ID}},
		{{tr(ctx, "⬅ Back"), "u:" + u.ID}},
	}
	return b.cmdConfig(ctx, acct, []string{u.Username}), inlineKeyboard(rows)
}

// userExtendMenu offers the expiry presets for an existing client: extend by a
// preset number of days, or clear the expiry to unlimited. Unlike the old single
// "+30 days" button, the menu makes the intent explicit (♾ Never is right there),
// so limiting a previously-unlimited config no longer needs a separate confirm.
func (b *Bot) userExtendMenu(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	cur := tr(ctx, "never")
	if u.ExpiresAt != nil {
		cur = u.ExpiresAt.UTC().Format("2006-01-02")
	}
	text := trf(ctx, "⏳ Extend %s\ncurrent expiry: %s\nPick how long to add:", displayName(u), cur)
	rows := [][]ikBtn{
		{{tr(ctx, "+30 days"), "uxp:" + u.ID + ":30"}, {tr(ctx, "+90 days"), "uxp:" + u.ID + ":90"}},
		{{tr(ctx, "+1 year"), "uxp:" + u.ID + ":365"}, {tr(ctx, "♾ Never"), "uxp:" + u.ID + ":0"}},
		{{tr(ctx, "⬅ Back"), "u:" + u.ID}},
	}
	return text, inlineKeyboard(rows)
}

// userExtendApply applies an extend preset "<id>:<days>". A positive value pushes
// the expiry out from the later of now and the current expiry; 0 clears it
// (unlimited). Re-renders the card.
func (b *Bot) userExtendApply(ctx context.Context, acct model.AdminAccount, arg string) (string, string) {
	id, daysStr, ok := strings.Cut(arg, ":")
	if !ok {
		return b.userCard(ctx, acct, arg)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	if daysStr == "0" {
		u.ExpiresAt = nil
	} else {
		days, err := strconv.Atoi(daysStr)
		if err != nil || days <= 0 {
			return b.userCard(ctx, acct, id)
		}
		base := b.now().UTC()
		if u.ExpiresAt != nil && u.ExpiresAt.After(base) {
			base = u.ExpiresAt.UTC()
		}
		exp := base.Add(time.Duration(days) * 24 * time.Hour)
		u.ExpiresAt = &exp
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.userCard(ctx, acct, id)
}

// userChangeGroupMenu lists the caller's groups as one-tap targets to move a client
// (the current group is marked). Closes the gap where a client's group could be set
// only at creation.
func (b *Bot) userChangeGroupMenu(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	groups := append([]model.Group(nil), st.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	var rows [][]ikBtn
	for _, g := range groups {
		label := "👥 " + g.Name
		if g.ID == u.GroupID {
			label = "✅ " + g.Name // current group
		}
		rows = append(rows, []ikBtn{{label, "ucgp:" + u.ID + ":" + g.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Back"), "u:" + u.ID}})
	text := trf(ctx, "📁 Move %s to which group?", displayName(u))
	if len(groups) == 0 {
		text = tr(ctx, "No groups exist; create one first.")
	}
	return text, inlineKeyboard(rows)
}

// userChangeGroupApply moves a client to the chosen group "<id>:<groupID>" and
// re-renders the card. Both lookups are scoped, so a forged cross-namespace move
// resolves as not found.
func (b *Bot) userChangeGroupApply(ctx context.Context, acct model.AdminAccount, arg string) (string, string) {
	id, gid, ok := strings.Cut(arg, ":")
	if !ok {
		return b.userCard(ctx, acct, arg)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	if _, ok := groupByID(st, gid); !ok {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Back"), "u:" + u.ID}}})
	}
	u.GroupID = gid
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.userCard(ctx, acct, id)
}

// userAction performs an enable/disable on a user from its card and re-renders the
// card. Scoped lookup enforces the namespace (an operator's tap on another
// namespace's id resolves as not found).
func (b *Bot) userAction(ctx context.Context, acct model.AdminAccount, id, action string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such user."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
	}
	switch action {
	case "enable":
		u.Enabled = true
	case "disable":
		u.Enabled = false
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.userCard(ctx, acct, id)
}

// menuNodes lists fleet nodes as buttons that open each node's card. Nodes are
// visible across namespaces (read-only on others), so every node is listed.
func (b *Bot) menuNodes(ctx context.Context, acct model.AdminAccount) (string, string) {
	// Per-node topology (names/roles/health/count) is infra — operators get zero
	// infra visibility beyond the health aggregate. Menu-hiding isn't a boundary
	// (a client can send any callback_data), so gate the read here and redirect
	// operators to the aggregate.
	if !acct.CanManageInfra() {
		return b.menuInfra(ctx, acct)
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	nodes := append([]model.Node(nil), st.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	var rows [][]ikBtn
	for _, n := range nodes {
		label := healthIcon(n.Health) + " " + n.Name + " · " + string(n.PublicRole)
		if n.Maintenance {
			label += " ⏸"
		}
		rows = append(rows, []ikBtn{{label, "n:" + n.ID}})
	}
	rows = append(rows, backRow(ctx))
	if len(nodes) == 0 {
		return tr(ctx, "No nodes."), inlineKeyboard(rows)
	}
	return tr(ctx, "Nodes — tap to manage"), inlineKeyboard(rows)
}

// nodeCard shows one node's detail with a drain/resume button when the viewer may
// manage it (own node, or admin). Others are shown read-only.
func (b *Bot) nodeCard(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	// A node card exposes name/role/health of a specific node — infra topology an
	// operator must not enumerate; the "n:<id>" callback is reachable regardless
	// of menu-hiding, so gate it and redirect to the aggregate.
	if !acct.CanManageInfra() {
		return b.menuInfra(ctx, acct)
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	n, ok := findNode(st, id)
	if !ok {
		return tr(ctx, "No such node."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Nodes"), "m:nodes"}}})
	}
	rot := tr(ctx, "in rotation")
	if n.Maintenance {
		rot = tr(ctx, "drained (maintenance)")
	}
	text := trf(ctx, "%s %s\nrole: %s\nhealth: %s\nstatus: %s", healthIcon(n.Health), n.Name, n.PublicRole, n.Health, rot)
	// Admins manage the fleet, so they see the node's addresses; operators get the
	// health line only (they never manage nodes).
	if acct.CanManageInfra() {
		if len(n.PublicIPs) > 0 {
			text += trf(ctx, "\naddresses: %s", strings.Join(n.PublicIPs, ", "))
		}
		if n.AgentAddr != "" {
			text += trf(ctx, "\nagent: %s", n.AgentAddr)
		}
		if n.LastSeenAt != nil {
			text += trf(ctx, "\nlast seen: %s", n.LastSeenAt.UTC().Format("2006-01-02 15:04 UTC"))
		}
	}
	var rows [][]ikBtn
	if authz.CanWriteNode(acct, n) {
		if n.Maintenance {
			rows = append(rows, []ikBtn{{tr(ctx, "▶️ Resume"), "nr:" + n.ID}})
		} else {
			rows = append(rows, []ikBtn{{tr(ctx, "⏸ Drain"), "nd:" + n.ID}})
		}
	} else {
		text += tr(ctx, "\n(read-only — admins manage nodes)")
	}
	rows = append(rows, []ikBtn{{tr(ctx, "⬅ Nodes"), "m:nodes"}})
	return text, inlineKeyboard(rows)
}

// nodeAction drains/resumes a node from its card and re-renders it. The ownership
// check mirrors the typed /drain command (an operator can only toggle its own).
func (b *Bot) nodeAction(ctx context.Context, acct model.AdminAccount, id string, drain bool) (string, string) {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	n, ok := findNode(st, id)
	if !ok {
		return tr(ctx, "No such node."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Nodes"), "m:nodes"}}})
	}
	if !authz.CanWriteNode(acct, n) {
		return tr(ctx, "⛔ Node management is available to admins only."), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Nodes"), "m:nodes"}}})
	}
	if err := b.store.SetNodeMaintenance(ctx, n.ID, drain); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	return b.nodeCard(ctx, acct, id)
}

// userByID finds a user by id within the (already scoped) state.
func userByID(st model.State, id string) (model.User, bool) {
	for _, u := range st.Users {
		if u.ID == id {
			return u, true
		}
	}
	return model.User{}, false
}

// authorize resolves the Telegram sender to an account. A per-account Telegram
// binding (telegram_id -> account, set in the panel Account tab) is the sole
// authority and carries the role/namespace; an unbound id is rejected. The
// legacy flat allowlist (panel Bots tab) no longer grants access — it survived
// only as a migration fallback and is retired here. The Bots-tab field remains
// a read-only reference for copying an id into a
// binding.
func (b *Bot) authorize(ctx context.Context, fromID int64) (model.AdminAccount, bool) {
	a, err := b.store.AccountByTelegramID(ctx, fromID)
	if err != nil {
		return model.AdminAccount{}, false
	}
	if !a.Role.Valid() {
		a.Role = model.RoleAdmin
	}
	return a, true
}

// scoped loads the fleet state filtered to what acct may see in the everyday bot
// screens: its OWN namespace's clients/groups/exit-rules (plus the guard baseline;
// plus all nodes for an admin). The bootstrap owner is scoped to its own namespace
// here too (seeAll=false) — cross-namespace browsing is the deliberate, read-only
// /namespaces lens (lensList/lensView, which read the store directly), not the
// default /users view. This mirrors the panel, so peers' clients never show up by
// reflex on either surface.
func (b *Bot) scoped(ctx context.Context, acct model.AdminAccount) (model.State, error) {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return model.State{}, err
	}
	return authz.ScopeStateView(st, acct, false), nil
}

// ownerFor is the owner_id to stamp on a resource acct creates: its own namespace
// (the bootstrap owner's namespace is "" = infra).
func ownerFor(acct model.AdminAccount) string {
	return acct.Namespace()
}

func (b *Bot) cmdStatus(ctx context.Context, acct model.AdminAccount) string {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	var up, down int
	for _, n := range st.Nodes {
		if n.Health == model.HealthHealthy {
			up++
		} else {
			down++
		}
	}
	active := st.ControlPlane.ActiveNodeID
	if active == "" {
		active = tr(ctx, "(none)")
	} else if n, ok := st.NodeByID(active); ok {
		active = n.Name
	}
	// Scoped accounts see their own client/group counts; the fleet-wide totals are
	// the bootstrap owner's. The epoch is internal control-plane plumbing, not shown.
	if !acct.IsBootstrapOwner() {
		return trf(ctx, "active: %s\nnodes: %d (%d healthy, %d unhealthy)\nyour clients: %d · your groups: %d",
			active, len(st.Nodes), up, down, len(st.Users), len(st.Groups))
	}
	return trf(ctx, "Control plane\nactive: %s\nnodes: %d (%d healthy, %d unhealthy)\nusers: %d · groups: %d",
		active, len(st.Nodes), up, down, len(st.Users), len(st.Groups))
}

func (b *Bot) cmdNodes(ctx context.Context, acct model.AdminAccount) string {
	// /nodes lists every node's name/role/health = fleet topology; operators must
	// not see it. Give them the infra aggregate text instead.
	if !acct.CanManageInfra() {
		text, _ := b.menuInfra(ctx, acct)
		return text
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err)
	}
	if len(st.Nodes) == 0 {
		return tr(ctx, "No nodes.")
	}
	nodes := append([]model.Node(nil), st.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	var b2 strings.Builder
	b2.WriteString(tr(ctx, "Nodes:\n"))
	for _, n := range nodes {
		fmt.Fprintf(&b2, "%s %s · %s · %s\n", healthIcon(n.Health), n.Name, n.PublicRole, n.Health)
	}
	return strings.TrimRight(b2.String(), "\n")
}

func (b *Bot) cmdUsers(ctx context.Context, acct model.AdminAccount) string {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	if len(st.Users) == 0 {
		return tr(ctx, "No users.")
	}
	users := append([]model.User(nil), st.Users...)
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	var b2 strings.Builder
	b2.WriteString(trf(ctx, "Users (%d):\n", len(users)))
	for _, u := range users {
		fmt.Fprintf(&b2, "%s %s\n", enabledIcon(u.Enabled), displayName(u))
	}
	return strings.TrimRight(b2.String(), "\n")
}

func (b *Bot) cmdUser(ctx context.Context, acct model.AdminAccount, args []string) string {
	if len(args) == 0 {
		return tr(ctx, "Usage: /user <name>")
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	u, ok := findUser(st, args[0])
	if !ok {
		return trf(ctx, "No such user: %s", args[0])
	}
	return b.userDetail(ctx, st, u)
}

func (b *Bot) cmdTraffic(ctx context.Context, acct model.AdminAccount) string {
	totals, err := b.store.UserTrafficTotals(ctx)
	if err != nil {
		return errMsg(err)
	}
	// Scoped accounts see traffic only for their own clients.
	if !acct.IsBootstrapOwner() {
		st, err := b.scoped(ctx, acct)
		if err != nil {
			return errMsg(err)
		}
		owned := map[string]bool{}
		for _, u := range st.Users {
			owned[u.ID] = true
		}
		kept := totals[:0]
		for _, t := range totals {
			if owned[t.UserID] {
				kept = append(kept, t)
			}
		}
		totals = kept
	}
	sort.Slice(totals, func(i, j int) bool { return totals[i].TotalBytes > totals[j].TotalBytes })
	var b2 strings.Builder
	b2.WriteString(tr(ctx, "Top users by traffic:\n"))
	n := 0
	for _, t := range totals {
		if t.TotalBytes == 0 {
			continue
		}
		fmt.Fprintf(&b2, "%s — %s\n", t.Username, humanBytes(t.TotalBytes))
		if n++; n >= 15 {
			break
		}
	}
	if n == 0 {
		return tr(ctx, "No traffic recorded yet.")
	}
	return strings.TrimRight(b2.String(), "\n")
}

// cmdMaintenance drains (on) or resumes a node by id or name. Node lifecycle is
// ownable: an operator may toggle only its own nodes, an admin any. The node is
// resolved from the full (unscoped) state since operators see every node, but the
// write is gated by ownership.
func (b *Bot) cmdMaintenance(ctx context.Context, acct model.AdminAccount, args []string, on bool) string {
	verb := "resume"
	if on {
		verb = "drain"
	}
	if len(args) == 0 {
		return trf(ctx, "Usage: /%s <node id or name>", verb)
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err)
	}
	n, ok := findNode(st, strings.Join(args, " "))
	if !ok {
		return trf(ctx, "No such node: %s", strings.Join(args, " "))
	}
	if !authz.CanWriteNode(acct, n) {
		return tr(ctx, "⛔ Node management is available to admins only.")
	}
	if err := b.store.SetNodeMaintenance(ctx, n.ID, on); err != nil {
		return errMsg(err)
	}
	if on {
		return trf(ctx, "⏸ Drained %q — traffic that targets it egresses locally and new configs warn until you resume.", n.Name)
	}
	return trf(ctx, "▶️ Resumed %q — back in rotation.", n.Name)
}

// findNode resolves a node by exact id, then case-insensitive name.
func findNode(st model.State, q string) (model.Node, bool) {
	for _, n := range st.Nodes {
		if n.ID == q {
			return n, true
		}
	}
	for _, n := range st.Nodes {
		if strings.EqualFold(n.Name, q) {
			return n, true
		}
	}
	return model.Node{}, false
}

// ---- guided /adduser wizard ----

func (b *Bot) convoFor(fromID int64) *convo {
	b.convoMu.Lock()
	defer b.convoMu.Unlock()
	return b.convos[fromID]
}

func (b *Bot) clearConvo(fromID int64) bool {
	b.convoMu.Lock()
	defer b.convoMu.Unlock()
	_, ok := b.convos[fromID]
	delete(b.convos, fromID)
	return ok
}

// startConvo registers a fresh guided flow for the sender.
func (b *Bot) startConvo(fromID int64, c *convo) {
	b.convoMu.Lock()
	b.convos[fromID] = c
	b.convoMu.Unlock()
}

// startAddUser begins the guided flow and prompts for the username.
func (b *Bot) startAddUser(ctx context.Context, fromID int64) string {
	b.startConvo(fromID, &convo{kind: kAddUser, step: 0})
	return tr(ctx, "➕ New user — step 1/3.\nSend a username.")
}

// advanceConvo feeds one plain-text reply into whichever guided flow is active and
// returns the next prompt plus an optional inline keyboard (the group/expiry/action
// pickers). Group/rename flows are pure text and carry no keyboard.
func (b *Bot) advanceConvo(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) (string, string) {
	switch c.kind {
	case kNewGroup:
		return b.advanceNewGroup(ctx, acct, fromID, c, input), ""
	case kRenameGroup:
		return b.advanceRenameGroup(ctx, acct, fromID, c, input), ""
	case kNewRoute:
		return b.advanceNewRoute(ctx, acct, fromID, c, input)
	case kSearchUsers:
		b.clearConvo(fromID) // a search is one-shot: the query yields a result list
		return b.menuUsersSearch(ctx, acct, input)
	default:
		return b.advanceAddUser(ctx, acct, fromID, c, input)
	}
}

// advanceAddUser feeds one plain-text reply into the wizard. The username is typed;
// the group and expiry steps return inline pickers (tapping them is handled by the
// aug:/aue: callbacks), but a typed reply still works. Reads/writes are scoped.
func (b *Bot) advanceAddUser(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, input string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		b.clearConvo(fromID)
		return errMsg(err), ""
	}
	switch c.step {
	case 0: // username
		if err := model.ValidateUsername(input); err != nil {
			return "⚠️ " + err.Error() + tr(ctx, "\nReply with another username, or /cancel."), ""
		}
		if _, exists := findUser(st, input); exists {
			return tr(ctx, "That username already exists. Reply with another, or /cancel."), ""
		}
		c.name = input
		c.step = 1
		return groupStepPrompt(ctx, st), b.groupPickKeyboard(ctx, st)
	case 1: // group
		groupArgs := []string{c.name}
		if g := strings.TrimSpace(input); g != "" && g != "-" && !strings.EqualFold(g, "default") {
			groupArgs = append(groupArgs, g)
		}
		gid, gerr := resolveGroup(ctx, st, groupArgs)
		if gerr != "" {
			return "⚠️ " + gerr + tr(ctx, "\nTap a group below, or reply with its name."), b.groupPickKeyboard(ctx, st)
		}
		c.group = gid
		c.step = 2
		return tr(ctx, "Step 3/3 — expiry.\nTap a preset, or reply with a number of days (e.g. 30) or 'never'."), expiryPickKeyboard(ctx)
	default: // expiry -> create
		exp, perr := parseExpiryReply(input, b.now())
		if perr != "" {
			return "⚠️ " + tr(ctx, perr) + tr(ctx, "\nReply with days (e.g. 30) or 'never'."), expiryPickKeyboard(ctx)
		}
		return b.createUserFromConvo(ctx, acct, fromID, c, exp), ""
	}
}

// createUserFromConvo persists the user the wizard accumulated. Shared by the typed
// expiry step and the aue: preset buttons.
func (b *Bot) createUserFromConvo(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo, exp *time.Time) string {
	u := model.User{
		ID:          genID(c.name),
		Username:    c.name,
		Secret:      randomHex(16),
		DisplayName: c.name,
		Enabled:     true,
		GroupID:     c.group,
		OwnerID:     ownerFor(acct),
		ExpiresAt:   exp,
	}
	if err := u.Validate(); err != nil {
		b.clearConvo(fromID)
		return errMsg(err)
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		b.clearConvo(fromID)
		return errMsg(err)
	}
	b.clearConvo(fromID)
	when := tr(ctx, "no expiry")
	if exp != nil {
		when = trf(ctx, "expires %s", exp.UTC().Format("2006-01-02"))
	}
	return trf(ctx, "✅ Created user %q (%s). Send /config %s to export their connection.", c.name, when, c.name)
}

// groupPickKeyboard offers the scoped groups as one-tap choices for the add-user
// wizard (aug:<groupID>), plus a Cancel that aborts the flow.
func (b *Bot) groupPickKeyboard(ctx context.Context, st model.State) string {
	groups := append([]model.Group(nil), st.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	var rows [][]ikBtn
	for _, g := range groups {
		rows = append(rows, []ikBtn{{"👥 " + g.Name, "aug:" + g.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "✖ Cancel"), "cx"}})
	return inlineKeyboard(rows)
}

// expiryPickKeyboard offers the common expiry presets for the add-user wizard
// (aue:<days>, 0 = never).
func expiryPickKeyboard(ctx context.Context) string {
	return inlineKeyboard([][]ikBtn{
		{{tr(ctx, "30 days"), "aue:30"}, {tr(ctx, "90 days"), "aue:90"}},
		{{tr(ctx, "1 year"), "aue:365"}, {tr(ctx, "♾ Never"), "aue:0"}},
		{{tr(ctx, "✖ Cancel"), "cx"}},
	})
}

// addUserPickGroup handles an aug: tap: set the chosen group on the live wizard and
// move to the expiry step.
func (b *Bot) addUserPickGroup(ctx context.Context, acct model.AdminAccount, fromID int64, gid string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return tr(ctx, "That form expired. Tap ➕ New user to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if _, ok := groupByID(st, gid); !ok {
		return tr(ctx, "No such group."), b.groupPickKeyboard(ctx, st)
	}
	c.group = gid
	c.step = 2
	return tr(ctx, "Step 3/3 — expiry.\nTap a preset, or reply with a number of days (e.g. 30) or 'never'."), expiryPickKeyboard(ctx)
}

// addUserPickExpiry handles an aue: tap: apply the preset and create the user.
func (b *Bot) addUserPickExpiry(ctx context.Context, acct model.AdminAccount, fromID int64, daysStr string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return tr(ctx, "That form expired. Tap ➕ New user to start over."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	var exp *time.Time
	if daysStr != "0" {
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			e := b.now().UTC().Add(time.Duration(days) * 24 * time.Hour)
			exp = &e
		}
	}
	return b.createUserFromConvo(ctx, acct, fromID, c, exp), inlineKeyboard([][]ikBtn{{{tr(ctx, "⬅ Users"), "m:users"}}})
}

// groupStepPrompt is the wizard's "choose a group" prompt. It only offers the
// '-' default shortcut when a single group exists (so resolveGroup can actually
// honor it); with several groups it requires an explicit choice, rather than
// promising a default that then errors with "Multiple groups".
func groupStepPrompt(ctx context.Context, st model.State) string {
	if len(st.Groups) > 1 {
		return trf(ctx, "Step 2/3 — group.\nReply with one of these group names: %s", groupNames(ctx, st))
	}
	return trf(ctx, "Step 2/3 — group.\nReply with a group name, or '-' for the default.\nGroups: %s", groupNames(ctx, st))
}

// groupNames lists the scoped groups' names for a wizard prompt.
func groupNames(ctx context.Context, st model.State) string {
	if len(st.Groups) == 0 {
		return tr(ctx, "(none yet)")
	}
	names := make([]string, 0, len(st.Groups))
	for _, g := range st.Groups {
		names = append(names, g.Name)
	}
	return strings.Join(names, ", ")
}

// parseExpiryReply reads a wizard expiry answer: 'never'/'none'/” -> no expiry;
// a positive integer -> that many days from now; anything else is an error.
func parseExpiryReply(input string, now time.Time) (*time.Time, string) {
	s := strings.ToLower(strings.TrimSpace(input))
	switch s {
	case "", "never", "none", "no", "-":
		return nil, ""
	}
	days, err := strconv.Atoi(s)
	if err != nil || days <= 0 {
		return nil, "I didn't understand that."
	}
	exp := now.UTC().Add(time.Duration(days) * 24 * time.Hour)
	return &exp, ""
}

func (b *Bot) cmdAddUser(ctx context.Context, acct model.AdminAccount, args []string) string {
	if len(args) == 0 {
		return tr(ctx, "Usage: /adduser <name> [group]")
	}
	name := args[0]
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	if _, exists := findUser(st, name); exists {
		return trf(ctx, "User already exists: %s", name)
	}
	groupID, gerr := resolveGroup(ctx, st, args)
	if gerr != "" {
		return gerr
	}
	u := model.User{
		ID:          genID(name),
		Username:    name,
		Secret:      randomHex(16),
		DisplayName: name,
		Enabled:     true,
		GroupID:     groupID,
		OwnerID:     ownerFor(acct),
	}
	if err := u.Validate(); err != nil {
		return errMsg(err)
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err)
	}
	return trf(ctx, "✅ Created user %q in group %s. Send /config %s to export their connection.", name, groupNameByID(st, groupID), name)
}

// groupNameByID resolves a group's display name from its id, falling back to the
// id when no group matches.
func groupNameByID(st model.State, id string) string {
	for _, g := range st.Groups {
		if g.ID == id {
			return g.Name
		}
	}
	return id
}

// cmdConfig exports a TrustTunnel client config for one of the caller's users:
// the deep link (which carries the credentials) plus a QR/share landing link.
// The lookup is namespace-scoped, so an operator can export only its own clients.
// The reply reveals the secret, so handleUpdate refuses it outside a private chat.
func (b *Bot) cmdConfig(ctx context.Context, acct model.AdminAccount, args []string) string {
	if len(args) == 0 {
		return tr(ctx, "Usage: /config <name>")
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	u, ok := findUser(st, args[0])
	if !ok {
		return trf(ctx, "No such user: %s", args[0])
	}
	entry, ok := pickEntry(st)
	if !ok {
		return tr(ctx, "No entry nodes in the fleet yet — add one in the panel first.")
	}
	cfg, err := clientcfg.Build(st, u.ID, entry.ID, clientcfg.Options{}, b.now())
	if err != nil {
		return errMsg(err)
	}
	var sb strings.Builder
	sb.WriteString(trf(ctx, "📲 Config for %s — entry %s\n\n", displayName(u), entry.Name))
	sb.WriteString(trf(ctx, "Deep link (open in TrustTunnel):\n%s\n\n", cfg.DeepLink))
	sb.WriteString(trf(ctx, "QR / share link:\n%s", cfg.QRLink))
	for _, w := range cfg.Warnings {
		fmt.Fprintf(&sb, "\n⚠️ %s", w)
	}
	return sb.String()
}

// pickEntry chooses which entry node to point a client config at: the active
// control plane if it is itself an in-rotation entry, else the first healthy
// in-rotation entry, else any entry (so even a degraded fleet yields a config,
// with the build's own warnings carrying the caveats).
func pickEntry(st model.State) (model.Node, bool) {
	if a, ok := st.NodeByID(st.ControlPlane.ActiveNodeID); ok && a.IsEntry() && !a.Maintenance && a.Health == model.HealthHealthy {
		return a, true
	}
	entries := make([]model.Node, 0, len(st.Nodes))
	for _, n := range st.Nodes {
		if n.IsEntry() {
			entries = append(entries, n)
		}
	}
	if len(entries) == 0 {
		return model.Node{}, false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, n := range entries {
		if !n.Maintenance && n.Health == model.HealthHealthy {
			return n, true
		}
	}
	return entries[0], true
}

func (b *Bot) cmdSetEnabled(ctx context.Context, acct model.AdminAccount, args []string, enabled bool) string {
	if len(args) == 0 {
		verb := "enable"
		if !enabled {
			verb = "disable"
		}
		return trf(ctx, "Usage: /%s <name>", verb)
	}
	// Scoped lookup: an operator can only toggle its own clients (others are not
	// found here), and the loaded row carries its owner_id so the upsert preserves
	// ownership.
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	u, ok := findUser(st, args[0])
	if !ok {
		return trf(ctx, "No such user: %s", args[0])
	}
	if u.Enabled == enabled {
		return trf(ctx, "%s is already %s.", displayName(u), enabledWord(ctx, enabled))
	}
	u.Enabled = enabled
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err)
	}
	return trf(ctx, "✅ %s is now %s.", displayName(u), enabledWord(ctx, enabled))
}

func (b *Bot) userTraffic(ctx context.Context, userID string) (rx, tx int64) {
	totals, err := b.store.UserTrafficTotals(ctx)
	if err != nil {
		return 0, 0
	}
	for _, t := range totals {
		if t.UserID == userID {
			return t.RxBytes, t.TxBytes
		}
	}
	return 0, 0
}

// ---- helpers ----

func findUser(st model.State, name string) (model.User, bool) {
	name = strings.ToLower(name)
	for _, u := range st.Users {
		if strings.ToLower(u.Username) == name || strings.ToLower(u.DisplayName) == name {
			return u, true
		}
	}
	return model.User{}, false
}

// resolveGroup returns the target group id from args[1] (by name or id), or the
// sole group when omitted. Returns a user-facing error string otherwise.
func resolveGroup(ctx context.Context, st model.State, args []string) (string, string) {
	if len(args) >= 2 {
		want := strings.ToLower(args[1])
		for _, g := range st.Groups {
			if strings.ToLower(g.Name) == want || strings.ToLower(g.ID) == want {
				return g.ID, ""
			}
		}
		return "", trf(ctx, "No such group: %s", args[1])
	}
	switch len(st.Groups) {
	case 0:
		return "", tr(ctx, "No groups exist; create one in the panel first.")
	case 1:
		return st.Groups[0].ID, ""
	default:
		var names []string
		for _, g := range st.Groups {
			names = append(names, g.Name)
		}
		return "", trf(ctx, "Multiple groups — specify one: %s", strings.Join(names, ", "))
	}
}

func displayName(u model.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// enabledWord is the localized "enabled"/"disabled" word for status messages.
func enabledWord(ctx context.Context, b bool) string {
	if b {
		return tr(ctx, "enabled")
	}
	return tr(ctx, "disabled")
}

// yesNo is the localized boolean used in the user card's "enabled:" line.
func yesNo(ctx context.Context, b bool) string {
	if b {
		return tr(ctx, "yes")
	}
	return tr(ctx, "no")
}

func enabledIcon(b bool) string {
	if b {
		return "🟢"
	}
	return "⚪"
}

func healthIcon(h model.NodeHealth) string {
	if h == model.HealthHealthy {
		return "🟢"
	}
	return "🔴"
}

func errMsg(err error) string { return "⚠️ " + err.Error() }

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	// Base-1024 units are labeled in IEC binary form (KiB/MiB/…), matching the
	// panel, rather than the decimal KB/MB which would understate the magnitude.
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func genID(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "user"
	}
	return "u-" + slug + "-" + randomHex(3)
}

// sleepCtx waits for d or ctx cancellation; returns true if cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

// randomHex returns n cryptographically-random bytes as hex. It panics if the
// system RNG fails: that is unrecoverable, and the previous behaviour (returning
// a constant) would have silently minted a guessable user Secret.
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}
