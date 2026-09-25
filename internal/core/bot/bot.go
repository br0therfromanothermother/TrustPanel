// Package bot is the operator-facing Telegram management bot. It runs as a
// separate process on the active exit, alongside the panel and
// Postgres-primary, and reads/writes fleet state through the store directly
// (localhost, same host). It is the management channel; the watchdog's
// one-way alert bot is separate and is not allowed to mutate state. Access is
// restricted to an allowlist of Telegram user IDs.
package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"trustpanel/internal/core/authz"
	"trustpanel/internal/core/clientcfg"
	"trustpanel/internal/core/egress"
	"trustpanel/internal/core/journal"
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
	RuleSetSources(ctx context.Context) ([]model.RuleSetSource, error)
	RuleSets(ctx context.Context) ([]model.RuleSet, error)
	SetNodeMaintenance(ctx context.Context, id string, on bool) error
	UserTrafficTotals(ctx context.Context) ([]store.UserTrafficTotal, error)
	GetSettings(ctx context.Context) (model.Settings, error)
	AccountByTelegramID(ctx context.Context, telegramID int64) (model.AdminAccount, error)
	SetAccountLocale(ctx context.Context, username, locale string) error
	SetAccountTelegram(ctx context.Context, username string, telegramID *int64, alertChatID string) error
	AppendEvent(ctx context.Context, e model.Event) error
	IssueRecoveryCode(ctx context.Context, username, codeHash string, telegramID int64, expiresAt time.Time) error
	LastRecoveryIssue(ctx context.Context, username string) (time.Time, error)
}

// Bot dispatches Telegram commands to store operations. Its live token comes
// from the DB (panel Bots tab) and is refreshed each Run iteration. An
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
	login string
	group string
	exp   *time.Time
	// kRenameGroup
	groupID string
	// kNewRoute
	rt     routeDraft
	editID string // when set, the route wizard updates this policy instead of creating
	// startedAt is when the form was opened, for convoTTL.
	startedAt time.Time
	// token names this particular form. It goes into every inline result the
	// catalogue offers while the form is open, so a result picked after the form
	// was cancelled, or one issued for a different form, is refused rather than
	// applied to whatever is open now.
	token string
	// chatID is where the form was opened. A form waits for an answer in the chat
	// it was opened in and nowhere else: the same person typing an unrelated
	// sentence in a group the bot is also in was having it read as their next
	// answer, which is how a stray line became a client's name.
	chatID int64
}

// answersFrom reports whether a message from chatID belongs to this form. A form
// opened before chats were recorded, or a caller with no chat (the typed Dispatch
// path, a test), still gets its answer.
func (c *convo) answersFrom(chatID int64) bool {
	return c.chatID == 0 || chatID == 0 || c.chatID == chatID
}

// route wizard steps. Admins start at rsLevel (to author the shared baseline);
// operators start at rsMatch, since a rule of theirs only ever applies to their
// own clients. rsMatch is the "match builder" hub: it shows the match set
// accumulated so far and lets the operator add more kinds (domain/geosite/geoip/
// cidr, which combine as in the panel) or continue. rsMatchVals is the per-kind
// value screen, where common geosite categories and countries are one-tap
// quick-adds and anything else is typed. rsConfirm shows a summary before
// anything is written.
const (
	rsLevel     = iota
	rsMatch     // the match-builder hub
	rsMatchVals // entering values for the currently-picked kind
	rsAction
	rsExit
	rsConfirm
)

// routeDraft accumulates a route across the wizard steps. Each match kind has its
// own slot so a single rule can combine several (domains and geosite and geoip and
// cidr), mirroring the panel. editKind names the slot the value screen is editing.
type routeDraft struct {
	domains  []string
	geosite  []string
	geoip    []string
	cidrs    []string
	editKind string // slot the value screen is editing: "domain"|"geosite"|"geoip"|"cidr"
	// refused holds names typed on the value screen that the source's catalogue
	// does not offer, and refusedKind the kind they were typed for. They are not
	// written into the rule, since a category that does not exist stops the node's
	// next configuration push. They are kept only so the screen can say which name
	// it would not take.
	refused     []string
	refusedKind string
	action      model.RuleAction
	exitID      string
	// infra marks the level the admin picked: a rule for the whole network rather
	// than one for their own clients. Operators never see the question.
	infra bool
}

// tier is the tier this draft is stored under. The bot asks for a level, like
// the panel does, and works the tier out from that level and the action: a
// network-wide rule that routes to an exit is a mandate for the whole network,
// one that keeps the traffic here is the network's safety net. Nobody has to
// know those two words, and the combination the database refuses (a safety-net
// rule pointed at an exit) cannot be built at all.
func (d *routeDraft) tier() model.RuleTier {
	if d.infra {
		return model.TierFleet
	}
	return model.TierExit
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
// deliberately short. The bot is button-driven; the "/" list is just a handful
// of entry points (everything else is reachable by tapping) and not a wall
// mirroring every command.
const botCommandsJSON = `[` +
	`{"command":"menu","description":"open the interactive menu"},` +
	`{"command":"adduser","description":"create a client"},` +
	`{"command":"config","description":"export a client config: /config <name>"},` +
	`{"command":"help","description":"open the menu"}` +
	`]`

// removeKeyboardMarkup clears any persistent reply keyboard left on a client.
// The bot is inline-driven throughout. Sent once on /start.
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
			b.handleOne(ctx, u)
		}
	}
}

// handleOne runs one update and keeps a panic inside it from reaching Run.
// Telegram re-delivers a whole batch until the next getUpdates acknowledges it,
// so a crash here would take the service down with the batch unconfirmed, and
// the restart would be handed the same update again, together with the ones
// before it in the batch, whose deletes and extensions would be applied a second
// time on every lap. A logged stack and one broken reply is the cheaper end.
func (b *Bot) handleOne(ctx context.Context, u update) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		log.Printf("bot: update %d: %v\n%s", u.UpdateID, r, debug.Stack())
		// The tapped button is still spinning on the operator's screen.
		if cq := u.CallbackQuery; cq != nil {
			_ = b.client.answerCallbackQuery(ctx, cq.ID, tr(ctx, "Something went wrong. Try again, or open the panel."), true)
		}
	}()
	b.handleUpdate(ctx, u)
}

func (b *Bot) handleUpdate(ctx context.Context, u update) {
	ctx = withLang(ctx, b.langForUpdate(ctx, u))
	if u.Message != nil && u.Message.Chat != nil {
		ctx = withChat(ctx, u.Message.Chat.ID)
	}
	// Searching the geo catalogue and picking from it.
	if u.InlineQuery != nil {
		b.inlineCatalog(ctx, u.InlineQuery)
		return
	}
	if u.ChosenInlineResult != nil {
		b.inlinePicked(ctx, u.ChosenInlineResult)
		return
	}
	if u.Message != nil && u.Message.ViaBot != nil {
		// The message a pick leaves in the chat. The tag was added from the pick
		// itself; reading this as a typed answer would be a second, unchecked way
		// into the draft.
		return
	}
	// Inline-button tap: edit the message in place and ack.
	if cq := u.CallbackQuery; cq != nil && cq.From != nil {
		// An inline menu may live in a group. Anything that is not plain navigation
		// or management is refused there. A button that hands out a config or a
		// panel recovery code cannot be tapped in front of a room.
		if cq.Message != nil && cq.Message.Chat != nil && cq.Message.Chat.Type != "private" && !groupSafeCallback(cq.Data) {
			_ = b.client.answerCallbackQuery(ctx, cq.ID, tr(ctx, "🔒 This one only works privately — message the bot in a DM."), true)
			return
		}
		// QR/TOML export deliver a fresh photo/document message instead of editing
		// the menu in place, and are handled here instead of via Callback.
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
	// /config emits a credential-bearing deep link and /recover mints a panel
	// code; never let either land in a group.
	if isPrivateOnlyCmd(u.Message.Text) && u.Message.Chat.Type != "private" {
		_ = b.client.sendMessage(ctx, u.Message.Chat.ID,
			tr(ctx, "🔒 That command reveals credentials — message me privately (DM)."), "")
		return
	}
	// A persistent reply keyboard lingers on the client until explicitly removed,
	// so clear it once on /start. The inline menu follows from route() below.
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
// Telegram-reported UI language. The bot speaks whichever language the operator
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
		case "menu", "start", "help", "groups", "routes", "account", "recover":
			acct, ok := b.authorize(ctx, fromID)
			if !ok {
				return notAuthorized(ctx, fromID), ""
			}
			b.clearConvo(fromID) // a slash-command aborts any guided flow
			if cmd == "start" && len(fields) > 1 {
				// A deep link: /start carries the record to open, so a
				// notification can put its own subject one tap away.
				return b.openDeepLink(ctx, acct, fields[1])
			}
			switch cmd {
			case "groups":
				return b.menuGroups(ctx, acct)
			case "routes":
				return b.menuRoutes(ctx, acct)
			case "account":
				return b.menuAccount(ctx, acct)
			case "recover":
				return b.recoverConfirm(ctx, acct)
			default:
				return b.menuRoot(ctx, acct)
			}
		}
	}
	// A plain-text reply feeding an active guided flow is routed here (not just in
	// Dispatch) so the next prompt can carry inline buttons; Dispatch returns text
	// only and is kept for the typed/tested path.
	if !strings.HasPrefix(strings.TrimSpace(text), "/") {
		if c := b.convoFor(fromID); c != nil && c.answersFrom(chatOf(ctx)) {
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
		if c := b.convoFor(fromID); c != nil && c.answersFrom(chatOf(ctx)) {
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
	case "recover":
		// Panel-password recovery. route() answers this one with its confirm button
		// attached; the direct Dispatch path returns the text-only form.
		text, _ := b.recoverConfirm(ctx, acct)
		return text
	case "status":
		return b.cmdStatus(ctx, acct)
	case "nodes":
		return b.cmdNodes(ctx, acct)
	case "scopes", "namespaces", "lens":
		// Hidden bootstrap-only cross-scope lens (kept out of the "/" command
		// menu). lensList already denies non-bootstrap callers. The old
		// "namespaces" spelling still answers so a saved command keeps working.
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
// looks dead to anyone not bound in the panel: no id echo, no command-behavior
// leak, no probing surface. Operators learn their numeric id via a public id-bot
// (@userinfobot), not ours. Because both roles are bound in the panel, the bot never
// needs to talk to an unbound user.
func notAuthorized(ctx context.Context, fromID int64) string { return "" }

// openDeepLink opens the record a /start payload names. A notification is about
// one thing, a client whose access is ending or a server that stopped answering,
// and what to do about it is on that record's card; without an address for it,
// the reader has to go and find it by hand, which is the part nobody does at
// speed.
//
// The payload is a record id, which is also all Telegram's deep links carry
// (letters, digits, "-" and "_"). Nothing about it is trusted beyond its shape:
// the card it opens applies the same scoping as every other way in. A link to
// somebody else's client resolves as not found.
func (b *Bot) openDeepLink(ctx context.Context, acct model.AdminAccount, payload string) (string, string) {
	id := strings.TrimSpace(payload)
	switch {
	case strings.HasPrefix(id, "u-"):
		return b.userCard(ctx, acct, id)
	case strings.HasPrefix(id, "g-"):
		return b.groupCard(ctx, acct, id)
	case strings.HasPrefix(id, "n-"):
		return b.nodeCard(ctx, acct, strings.TrimPrefix(id, "n-"))
	case strings.HasPrefix(id, "rule-"):
		return b.routeCard(ctx, acct, strings.TrimPrefix(id, "rule-"))
	}
	// An unknown payload is not worth a screen of its own: the menu is where the
	// reader was going anyway.
	return b.menuRoot(ctx, acct)
}

// isPrivateOnlyCmd reports whether text is a command that hands out a credential
// (with an optional @BotName suffix), which lets it be gated to private chats.
func isPrivateOnlyCmd(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd == "config" || cmd == "recover"
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

// groupSafeCallbacks is every callback token the bot will answer outside a
// private chat: navigation and management, and nothing that puts a secret on the
// screen. It is written as an allowlist and not a list of the secret-bearing
// prefixes because the cost of missing an entry has to fall on the harmless
// side: a button left out of an allowlist is refused with "message me privately",
// while a button left out of a denylist is a config or a panel recovery code
// sitting in a group's history.
//
// An entry ending in ":" matches by prefix, the rest match exactly.
var groupSafeCallbacks = []string{
	"cx", "usrch", "gadd", "radd", "rcont", "rback", "rok", tooLongCallback,
	"auok", "auback", "aulog",
	"m:", "up:", "u:", "umore:", "ue:", "ud:", "uxm:", "uxp:", "uxe:", "uxk:",
	"ucg:", "ucgp:", "udel:", "udz:", "aug:", "aue:",
	"g:", "gcl:", "gmore:", "gdef:", "gdefp:", "gdefc:", "grn:", "gdel:", "gdz:",
	"r:", "re:", "rx:", "rt:", "ra:", "rmk:", "rqa:", "rxp:", "rup:", "rdn:",
	"redit:", "rdel:", "rdz:",
	"n:", "ndet:", "nd:", "ndg:", "nr:", "lens:", "lang:", "alert:",
}

// groupSafeCallback reports whether data is one of groupSafeCallbacks.
func groupSafeCallback(data string) bool {
	for _, s := range groupSafeCallbacks {
		if strings.HasSuffix(s, ":") {
			if strings.HasPrefix(data, s) {
				return true
			}
		} else if data == s {
			return true
		}
	}
	return false
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
	ex, err := b.findExportable(ctx, acct, func(st model.State) (model.User, bool) { return userByID(st, id) })
	switch {
	case errors.Is(err, errNoClient):
		fail(tr(ctx, "No such user."))
		return
	case errors.Is(err, errNoEntry):
		fail(noEntryMsg(ctx, acct))
		return
	case err != nil:
		fail(errMsg(err))
		return
	}
	u := ex.user
	cfg, err := clientcfg.Build(ex.state, u.ID, ex.entry.ID, clientcfg.Options{}, b.now())
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

// ikBtn is one inline button: its text and what tapping it does, either a
// callback the bot answers or, when data carries the searchMark, a search this
// chat's input box is pre-filled with. The mark rather than a third field because
// every
// button in the package is written as a two-value literal, and a button's kind is
// decided in one place (inlineKeyboard) either way.
type ikBtn struct{ text, data string }

// searchMark prefixes the payload of a button that opens the inline search. It
// is a control character, which no callback token is or can become.
const searchMark = "\x01"

// searchBtn opens the inline box in this chat, pre-filled with query. The
// catalogue is one tap from the step that needs it and nobody has to remember a
// command.
func searchBtn(text, query string) ikBtn { return ikBtn{text, searchMark + query} }

// inlineKeyboard renders rows of buttons as a Telegram reply_markup JSON value.
func inlineKeyboard(rows [][]ikBtn) string {
	type tgBtn struct {
		Text   string `json:"text"`
		Data   string `json:"callback_data,omitempty"`
		Inline string `json:"switch_inline_query_current_chat,omitempty"`
	}
	kb := make([][]tgBtn, 0, len(rows))
	for _, row := range rows {
		r := make([]tgBtn, 0, len(row))
		for _, btn := range row {
			if q, ok := strings.CutPrefix(btn.data, searchMark); ok {
				r = append(r, tgBtn{Text: btn.text, Inline: q})
				continue
			}
			data := btn.data
			if len(data) > maxCallbackLen {
				// Telegram refuses the whole message over this. One record with
				// an id from somewhere else (an import, an older build) would
				// blank the menu it appears in. Keep the button and say what is
				// wrong when it is tapped.
				data = tooLongCallback
			}
			r = append(r, tgBtn{Text: btn.text, Data: data})
		}
		kb = append(kb, r)
	}
	out, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
	return string(out)
}

// maxCallbackLen is Telegram's limit on callback_data, in bytes.
const maxCallbackLen = 64

// tooLongCallback stands in for a button whose real payload does not fit.
const tooLongCallback = "toolong"

// backRow is the standard "back to main menu" button row.
func backRow(ctx context.Context) []ikBtn { return backTo(ctx, "Menu", "m:root") }

// cancelRow is the standard "abort the active guided flow" button row. Every
// wizard prompt carries it, so a flow can always be left with one tap instead of
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
	case data == tooLongCallback:
		return tr(ctx, "This record's id is too long for a button here — open it in the panel."), inlineKeyboard([][]ikBtn{backRow(ctx)})
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
	case data == "pwrec": // explain before minting anything
		return b.recoverConfirm(ctx, acct)
	case data == "pwrec:go":
		return b.recoverIssue(ctx, acct, fromID)
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
	case strings.HasPrefix(data, "umore:"):
		return b.userMore(ctx, acct, strings.TrimPrefix(data, "umore:"))
	case strings.HasPrefix(data, "ue:"):
		return b.userAction(ctx, acct, strings.TrimPrefix(data, "ue:"), "enable")
	case strings.HasPrefix(data, "ud:"):
		return b.userAction(ctx, acct, strings.TrimPrefix(data, "ud:"), "disable")
	case strings.HasPrefix(data, "uxm:"):
		return b.userExtendMenu(ctx, acct, strings.TrimPrefix(data, "uxm:"))
	case strings.HasPrefix(data, "uxp:"):
		return b.userExtendApply(ctx, acct, strings.TrimPrefix(data, "uxp:"), extendAsk)
	case strings.HasPrefix(data, "uxe:"):
		return b.userExtendApply(ctx, acct, strings.TrimPrefix(data, "uxe:"), extendOn)
	case strings.HasPrefix(data, "uxk:"):
		return b.userExtendApply(ctx, acct, strings.TrimPrefix(data, "uxk:"), extendKeepOff)
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
	case data == "auok": // wizard: write the client the review shows
		return b.addUserCreate(ctx, acct, fromID)
	case data == "auback": // wizard: back to the access period
		return b.addUserBack(ctx, acct, fromID)
	case data == "aulog": // wizard: type a different login
		return b.addUserAskLogin(ctx, fromID)
	case strings.HasPrefix(data, "aug:"):
		return b.addUserPickGroup(ctx, acct, fromID, strings.TrimPrefix(data, "aug:"))
	case strings.HasPrefix(data, "aue:"):
		return b.addUserPickExpiry(ctx, acct, fromID, strings.TrimPrefix(data, "aue:"))
	// groups
	case data == "gadd":
		return b.startNewGroup(ctx, fromID), inlineKeyboard([][]ikBtn{cancelRow(ctx)})
	case strings.HasPrefix(data, "gcl:"):
		return b.groupClients(ctx, acct, strings.TrimPrefix(data, "gcl:"))
	case strings.HasPrefix(data, "gmore:"):
		return b.groupMore(ctx, acct, strings.TrimPrefix(data, "gmore:"))
	case strings.HasPrefix(data, "gdefc:"): // the confirmed move
		return b.groupSetDefault(ctx, acct, strings.TrimPrefix(data, "gdefc:"))
	case strings.HasPrefix(data, "gdefp:"): // a pick, which asks first
		return b.groupExitConfirm(ctx, acct, strings.TrimPrefix(data, "gdefp:"))
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
	case strings.HasPrefix(data, "rt:"): // wizard: level pick
		return b.routePickLevel(ctx, acct, fromID, strings.TrimPrefix(data, "rt:"))
	case strings.HasPrefix(data, "rmk:"): // wizard: pick a match kind to add
		return b.routePickMatchKind(ctx, acct, fromID, strings.TrimPrefix(data, "rmk:"))
	case strings.HasPrefix(data, "rqa:"): // wizard: quick-add/remove a match value
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
	case strings.HasPrefix(data, "ndet:"):
		return b.nodeDetails(ctx, acct, strings.TrimPrefix(data, "ndet:"))
	case strings.HasPrefix(data, "ndg:"): // drain, moving the dependent groups
		return b.nodeDrainTo(ctx, acct, strings.TrimPrefix(data, "ndg:"))
	case strings.HasPrefix(data, "nd:"):
		return b.nodeAction(ctx, acct, strings.TrimPrefix(data, "nd:"), true)
	case strings.HasPrefix(data, "nr:"):
		return b.nodeAction(ctx, acct, strings.TrimPrefix(data, "nr:"), false)
	default:
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
}

// menuRoot is the top-level inline menu. It doubles as the status card: the
// control-plane summary is its header, which is why there is no separate Status
// button duplicating the Nodes view. The buttons are the management entry
// points (open a user/node to act on it; ➕ starts the guided create). Branches
// are the same scoped views as the typed commands.
func (b *Bot) menuRoot(ctx context.Context, acct model.AdminAccount) (string, string) {
	header := tr(ctx, "TrustPanel — main menu") + "\n\n" + b.statusHeader(ctx, acct)
	rows := [][]ikBtn{
		{{tr(ctx, lblClients), "m:users"}, {tr(ctx, "📊 Traffic"), "m:traffic"}},
		{{tr(ctx, lblNewClient), "m:adduser"}, {tr(ctx, lblRoutes), "m:routes"}},
		{{tr(ctx, lblGroups), "m:groups"}, {tr(ctx, "⚙️ Account"), "m:acct"}},
	}
	if acct.CanManageInfra() {
		// Admins manage shared infra. The bootstrap owner's cross-namespace lens is
		// deliberately not surfaced here; it's a buried, occasional tool reached via
		// the hidden /namespaces command. The default menu stays scoped to one
		// tenant and nobody browses peers' clients by reflex.
		rows = append(rows, []ikBtn{{tr(ctx, lblServers), "m:nodes"}})
	} else {
		// Operators get a read-only infra-health aggregate, not node management.
		rows = append(rows, []ikBtn{{tr(ctx, "🩺 Infra") + " " + b.infraIcon(ctx), "m:infra"}})
	}
	return header, inlineKeyboard(rows)
}

// statusHeader is the root-screen status line. Admins (bootstrap or co-owner)
// keep infra in scope, and cmdStatus renders nodes directly. An operator's Nodes
// are nulled; its header takes node health from the infra aggregate: counts
// only, never identifiers.
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
// active-exit health bit. It is the only read that bypasses ScopeState, and it
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
// read is deliberate, because an operator's scoped state has no nodes.
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
// card, with ⬅ Prev / Next ➡ navigation and a 🔍 Search entry. Replaces the old
// hard cap of 30 that dead-ended into a plain-text /users list.
func (b *Bot) menuUsersPage(ctx context.Context, acct model.AdminAccount, page int) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	users := append([]model.User(nil), st.Users...)
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	if len(users) == 0 {
		rows := [][]ikBtn{{{tr(ctx, lblNewClient), "m:adduser"}}, backRow(ctx)}
		return tr(ctx, "No clients yet."), inlineKeyboard(rows)
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
		rows = append(rows, []ikBtn{{accessOf(u, b.now()).glyph + " " + displayName(u), "u:" + u.ID}})
	}
	if pages > 1 { // page navigation, edges omitted
		var nav []ikBtn
		if page > 0 {
			nav = append(nav, ikBtn{tr(ctx, "⬅ Prev"), "up:" + strconv.Itoa(page-1)})
		}
		if page < pages-1 {
			nav = append(nav, ikBtn{tr(ctx, "Next ➡"), "up:" + strconv.Itoa(page+1)})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []ikBtn{{tr(ctx, lblSearch), "usrch"}, {tr(ctx, lblNewClient), "m:adduser"}})
	rows = append(rows, backRow(ctx))
	text := trf(ctx, "Clients (%d) — tap to open", len(users))
	if pages > 1 {
		text = trf(ctx, "Clients (%d) — page %d/%d, tap to open", len(users), page+1, pages)
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
			inlineKeyboard([][]ikBtn{{{tr(ctx, "🔎 Search again"), "usrch"}}, backTo(ctx, "Clients", "m:users")})
	}
	const cap = 20 // a search should be specific; keep the keyboard bounded
	capped := len(matches) > cap
	if capped {
		matches = matches[:cap]
	}
	var rows [][]ikBtn
	for _, u := range matches {
		rows = append(rows, []ikBtn{{accessOf(u, b.now()).glyph + " " + displayName(u), "u:" + u.ID}})
	}
	rows = append(rows, []ikBtn{{tr(ctx, "🔎 Search again"), "usrch"}, backTo(ctx, "Clients", "m:users")[0]})
	text := trf(ctx, "Matches for %q:", query)
	if capped {
		text = trf(ctx, "Matches for %q (first %d — narrow it down):", query, cap)
	}
	return text, inlineKeyboard(rows)
}

// startUserSearch arms the one-shot search flow and prompts for a query.
func (b *Bot) startUserSearch(ctx context.Context, fromID int64) (string, string) {
	b.startConvo(ctx, fromID, &convo{kind: kSearchUsers})
	return tr(ctx, "🔎 Send part of a client's name to search."), inlineKeyboard([][]ikBtn{cancelRow(ctx)})
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
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	// Handing out a connection and setting how long it lasts are what this card
	// is opened for; switching the client off and deleting it are not, and they
	// are the two that cannot be taken back. They live one tap further in.
	first := []ikBtn{{tr(ctx, lblConnection), "cfg:" + u.ID}, {tr(ctx, lblAccess), "uxm:" + u.ID}}
	if accessOf(u, b.now()) == accessExpired {
		// Nothing on this card is any use until the access is renewed.
		first = []ikBtn{first[1], first[0]}
	}
	rows := [][]ikBtn{
		first,
		{{tr(ctx, lblChangeGroup), "ucg:" + u.ID}, {tr(ctx, lblMore), "umore:" + u.ID}},
		backTo(ctx, "Clients", "m:users"),
	}
	return b.userDetail(ctx, st, u), inlineKeyboard(rows)
}

// userMore is the rest of the card: the two actions that end a client's access.
// The body is the same card body, leaving the reader the record while
// deciding.
func (b *Bot) userMore(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	toggle := ikBtn{tr(ctx, lblSwitchOn), "ue:" + u.ID}
	if u.Enabled {
		toggle = ikBtn{tr(ctx, lblSwitchOff), "ud:" + u.ID}
	}
	rows := [][]ikBtn{
		{toggle},
		{{tr(ctx, lblDelete), "udel:" + u.ID}},
		backTo(ctx, "Back", "u:"+u.ID),
	}
	return b.userDetail(ctx, st, u), inlineKeyboard(rows)
}

// userDetail renders the shared user card body (used by the inline card and the
// /user command), localized for the request language.
func (b *Bot) userDetail(ctx context.Context, st model.State, u model.User) string {
	rx, tx := b.userTraffic(ctx, u.ID)
	var sb strings.Builder
	// The state first, in words, since the card is read for it. A green dot and
	// an "enabled: yes" can both be true of a client whose access ran out a week
	// ago, so neither is the answer on its own.
	sb.WriteString(accessOf(u, b.now()).line(ctx))
	sb.WriteString("\n" + displayName(u))
	if u.Username != displayName(u) {
		// The connection is issued under the login. It is worth a line
		// only when it is not simply the name again.
		sb.WriteString(trf(ctx, "\nlogin: %s", u.Username))
	}
	sb.WriteString(trf(ctx, "\ngroup: %s", groupNameByID(st, u.GroupID)))
	sb.WriteString("\n" + b.accessLine(ctx, u))
	sb.WriteString(trf(ctx, "\ntraffic, all time: ↑%s ↓%s", humanBytes(ctx, rx), humanBytes(ctx, tx)))
	return sb.String()
}

// accessLine says when the access ends the way someone asks it: the day it ends
// on, and how far away that is. "expires: never" answered neither.
func (b *Bot) accessLine(ctx context.Context, u model.User) string {
	if u.ExpiresAt == nil {
		return tr(ctx, "access period: no limit")
	}
	day := u.ExpiresAt.UTC().Format(dateOnly)
	switch left := daysUntil(*u.ExpiresAt, b.now()); {
	case left < 0:
		return trf(ctx, "access period: ended %s", day)
	case left == 0:
		return trf(ctx, "access period: until %s — the last day is today", day)
	default:
		return trf(ctx, "access period: until %s (days left: %d)", day, left)
	}
}

// daysUntil counts whole calendar days, because the answer is
// counted in: an access ending tomorrow evening has one day left however many
// hours that happens to be.
func daysUntil(when, now time.Time) int {
	y, m, d := when.UTC().Date()
	ny, nm, nd := now.UTC().Date()
	end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	today := time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC)
	return int(end.Sub(today).Hours() / 24)
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
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	// One link in the text, the one that can be passed to a person, and the QR and
	// the file as their own actions, sent as fresh messages by handleUpdate.
	text, ok := b.connectionScreen(ctx, acct, u.ID)
	if !ok {
		// Nothing to hand out: offering a QR and a file for a connection that
		// cannot be built is two more taps to the same answer.
		return text, inlineKeyboard([][]ikBtn{backTo(ctx, "Back", "u:"+u.ID)})
	}
	rows := [][]ikBtn{
		{{tr(ctx, "📷 QR code"), "cfgqr:" + u.ID}, {tr(ctx, "📄 File .toml"), "cfgtoml:" + u.ID}},
		backTo(ctx, "Back", "u:"+u.ID),
	}
	return text, inlineKeyboard(rows)
}

// userExtendMenu offers the expiry presets for an existing client: extend by a
// preset number of days, or clear the expiry to unlimited. A menu makes the
// intent explicit (♾ Never is right there), so limiting an unlimited config
// needs no separate confirmation.
func (b *Bot) userExtendMenu(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	text := trf(ctx, "%s — %s\n%s\nPick the new date:", tr(ctx, lblAccess), displayName(u), b.accessLine(ctx, u))
	// Each button carries the date it would set, not a number of days to add. It
	// says on its face where the access ends up, and tapping it twice sets the
	// same date twice instead of quietly adding another month.
	preset := func(label string, days int) []ikBtn {
		day := extendTo(u, days, b.now()).Format(dateOnly)
		return []ikBtn{{trf(ctx, "%s → %s", tr(ctx, label), day), "uxp:" + u.ID + ":" + day}}
	}
	rows := [][]ikBtn{
		preset("+30 days", 30),
		preset("+90 days", 90),
		preset("+1 year", 365),
		{{tr(ctx, "♾ No limit"), "uxp:" + u.ID + ":0"}},
		backTo(ctx, "Back", "u:"+u.ID),
	}
	return text, inlineKeyboard(rows)
}

// dateOnly is how a calendar day is written everywhere the bot shows or carries
// one.
const dateOnly = "2006-01-02"

// expiryOn turns a calendar day into the instant access ends: the end of that
// day, UTC, matching the panel's date field. A client whose access
// "ends on the 19th" then works through the 19th on either surface, instead of
// stopping at whatever o'clock the button happened to be tapped.
func expiryOn(day time.Time) time.Time {
	y, m, d := day.UTC().Date()
	return time.Date(y, m, d, 23, 59, 59, 0, time.UTC)
}

// extendTo is the day a preset moves a client's access to: counted from the later
// of today and the access it already has. A renewal adds to what is left
// instead of throwing it away.
func extendTo(u model.User, days int, now time.Time) time.Time {
	base := now.UTC()
	if u.ExpiresAt != nil && u.ExpiresAt.After(base) {
		base = u.ExpiresAt.UTC()
	}
	return expiryOn(base.AddDate(0, 0, days))
}

// What an extension does about a client that is switched off. Extending a lapsed
// client is a renewal, and whether the access comes back with it is the
// operator's answer to give, not ours to assume, so the preset asks and the
// answer arrives as its own callback.
const (
	extendAsk     = "ask"
	extendOn      = "on"
	extendKeepOff = "off"
)

// userExtendApply applies an extend preset "<id>:<YYYY-MM-DD>", or "<id>:0" to
// clear the expiry. The date is decided when the menu is drawn (see extendTo),
// so the same tap twice (a double tap, a re-sent message, a button somebody
// scrolls back to) lands on the same date instead of adding another month.
//
// A switched-off client whose new date reaches into the future gets the question
// first (mode extendAsk); extendOn and extendKeepOff are the two answers.
func (b *Bot) userExtendApply(ctx context.Context, acct model.AdminAccount, arg, mode string) (string, string) {
	id, when, ok := strings.Cut(arg, ":")
	if !ok {
		return b.userCard(ctx, acct, arg)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	u, ok := userByID(st, id)
	if !ok {
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	if when == "0" {
		u.ExpiresAt = nil
	} else {
		day, err := time.Parse(dateOnly, when)
		if err != nil {
			// A button from a build that sent day counts. Re-draw the menu rather
			// than guess which month its "+30" meant from here.
			return b.userExtendMenu(ctx, acct, id)
		}
		exp := expiryOn(day)
		u.ExpiresAt = &exp
	}
	turnedOn := false
	if !u.Enabled && !u.Expired(b.now()) {
		switch mode {
		case extendAsk:
			return b.userExtendAsk(ctx, u, when)
		case extendOn:
			u.Enabled = true
			turnedOn = true
		}
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	line := expiryLine(u)
	if turnedOn { // one tap, one line: the date and the switch came together
		line = journal.Join(line, journal.Line(", and the client was switched on"))
	}
	b.recordFor(ctx, model.SeverityInfo, line, acct)
	return b.userCard(ctx, acct, id)
}

// userExtendAsk shows the pending expiry and asks whether the client comes back
// on with it. Nothing is written until one of the two buttons is pressed.
func (b *Bot) userExtendAsk(ctx context.Context, u model.User, when string) (string, string) {
	until := tr(ctx, "no limit")
	if u.ExpiresAt != nil {
		until = trf(ctx, "until %s", u.ExpiresAt.UTC().Format(dateOnly))
	}
	text := trf(ctx, "⏸ %s is switched off.\nnew access period: %s\nSwitch the client on as well?", displayName(u), until)
	rows := [][]ikBtn{
		{{tr(ctx, "▶️ Renew and switch on"), "uxe:" + u.ID + ":" + when}},
		{{tr(ctx, "📅 Renew, leave it off"), "uxk:" + u.ID + ":" + when}},
		backTo(ctx, "Back", "u:"+u.ID),
	}
	return text, inlineKeyboard(rows)
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
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	groups := append([]model.Group(nil), st.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	var rows [][]ikBtn
	for _, g := range groups {
		label := "👥 " + g.Name
		if g.ID == u.GroupID {
			label = "✓ " + g.Name // the group it is in now
		}
		rows = append(rows, []ikBtn{{label, "ucgp:" + u.ID + ":" + g.ID}})
	}
	rows = append(rows, backTo(ctx, "Back", "u:"+u.ID))
	text := trf(ctx, "👥 Move %s to which group?", displayName(u))
	if len(groups) == 0 {
		text = tr(ctx, "No groups exist; create one first.")
	}
	return text, inlineKeyboard(rows)
}

// userChangeGroupApply moves a client to the chosen group "<id>:<groupID>" and
// re-renders the card. Because both lookups are scoped, a forged cross-namespace move
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
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	g, ok := groupByID(st, gid)
	if !ok {
		return tr(ctx, "No such group."), inlineKeyboard([][]ikBtn{backTo(ctx, "Back", "u:"+u.ID)})
	}
	u.GroupID = gid
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityInfo,
		journal.Line("client %s moved to group %s", ref(displayName(u), u.ID), ref(g.Name, g.ID)), acct)
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
		return tr(ctx, "No such client."), inlineKeyboard([][]ikBtn{backTo(ctx, "Clients", "m:users")})
	}
	switch action {
	case "enable":
		// Past the access date there is no config to switch on: the render leaves an
		// expired client out, and the panel's sweep puts the switch back where it
		// was. Say what to do instead of accepting a switch that does not hold.
		if u.Expired(b.now()) {
			text, kb := b.userCard(ctx, acct, id)
			return tr(ctx, "⚠️ Access has ended. Extend it first — then the client can be switched on.") + "\n\n" + text, kb
		}
		u.Enabled = true
	case "disable":
		u.Enabled = false
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityInfo, enabledLine(u), acct)
	return b.userCard(ctx, acct, id)
}

// menuNodes lists fleet nodes as buttons that open each node's card. Nodes are
// visible across namespaces (read-only on others), and every node is listed.
func (b *Bot) menuNodes(ctx context.Context, acct model.AdminAccount) (string, string) {
	// Per-node topology (names/roles/health/count) is infra; operators get zero
	// infra visibility beyond the health aggregate. Menu-hiding isn't a boundary
	// (a client can send any callback_data). Gate the read here and redirect
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
		st := nodeStateOf(n)
		rows = append(rows, []ikBtn{{st.glyph + " " + n.Name + " · " + nodeRole(ctx, nodes, n), "n:" + n.ID}})
	}
	rows = append(rows, backRow(ctx))
	if len(nodes) == 0 {
		return tr(ctx, "No servers."), inlineKeyboard(rows)
	}
	return tr(ctx, "Servers — tap to open"), inlineKeyboard(rows)
}

// nodeCard shows one node's detail with a drain/resume button when the viewer may
// manage it (own node, or admin). Others are shown read-only.
func (b *Bot) nodeCard(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	// A node card exposes name/role/health of a specific node: infra topology an
	// operator must not enumerate; the "n:<id>" callback is reachable regardless
	// of menu-hiding. Gate it and redirect to the aggregate.
	if !acct.CanManageInfra() {
		return b.menuInfra(ctx, acct)
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	n, ok := findNode(st, id)
	if !ok {
		return tr(ctx, "No such server."), inlineKeyboard([][]ikBtn{backTo(ctx, "Servers", "m:nodes")})
	}
	// What it is and what it is doing, in words and not as field names: a
	// server in maintenance is not "in rotation", whatever its health column says.
	text := nodeStateOf(n).line(ctx) + "\n" + n.Name + "\n" + nodeRole(ctx, st.Nodes, n)
	if st.ControlPlane.ActiveNodeID == n.ID {
		text += tr(ctx, ", and holds the panel")
	}
	text += "\n" + b.lastCheckLine(ctx, n)
	var rows [][]ikBtn
	if authz.CanWriteNode(acct, n) {
		if n.Maintenance {
			rows = append(rows, []ikBtn{{tr(ctx, "▶️ Return to service"), "nr:" + n.ID}})
		} else {
			rows = append(rows, []ikBtn{{tr(ctx, lblMaintenance), "nd:" + n.ID}})
		}
	} else {
		text += tr(ctx, "\n(read-only — admins manage servers)")
	}
	rows = append(rows, []ikBtn{{tr(ctx, "🔧 Details"), "ndet:" + n.ID}})
	rows = append(rows, backTo(ctx, "Servers", "m:nodes"))
	return text, inlineKeyboard(rows)
}

// lastCheckLine says how current the state above it is: a server that stopped
// answering an hour ago and one that answered a second ago are shown the same
// way otherwise.
func (b *Bot) lastCheckLine(ctx context.Context, n model.Node) string {
	if n.LastSeenAt == nil {
		return tr(ctx, "no answer from it yet")
	}
	d := b.now().UTC().Sub(n.LastSeenAt.UTC())
	switch {
	case d < time.Minute:
		return tr(ctx, "checked just now")
	case d < time.Hour:
		return trf(ctx, "checked %d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return trf(ctx, "checked %d h ago", int(d.Hours()))
	}
	return trf(ctx, "checked %s", n.LastSeenAt.UTC().Format("2006-01-02 15:04 UTC"))
}

// nodeDetails is the second level: the addresses and the agent, which are the
// answer to "why is it not answering" and to nothing else.
func (b *Bot) nodeDetails(ctx context.Context, acct model.AdminAccount, id string) (string, string) {
	if !acct.CanManageInfra() {
		return b.menuInfra(ctx, acct)
	}
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	n, ok := findNode(st, id)
	if !ok {
		return tr(ctx, "No such server."), inlineKeyboard([][]ikBtn{backTo(ctx, "Servers", "m:nodes")})
	}
	text := nodeStateOf(n).line(ctx) + "\n" + n.Name
	if len(n.PublicIPs) > 0 {
		text += trf(ctx, "\naddresses: %s", strings.Join(n.PublicIPs, ", "))
	}
	if n.AgentAddr != "" {
		text += trf(ctx, "\nagent: %s", n.AgentAddr)
	}
	text += trf(ctx, "\nid: %s", n.ID)
	text += "\n" + b.lastCheckLine(ctx, n)
	return text, inlineKeyboard([][]ikBtn{backTo(ctx, "Back", "n:"+n.ID)})
}

// nodeAction drains/resumes a node from its card and re-renders it. The ownership
// check mirrors the typed /drain command (an operator can only toggle its own).
//
// Draining an exit that groups leave by asks where they go first. Flipping the
// flag and stopping there would leave those groups pointing at a node in
// maintenance, sending their traffic out of the entry node with nobody told. The
// panel refuses the same way (handleNodeMaintenance).
func (b *Bot) nodeAction(ctx context.Context, acct model.AdminAccount, id string, drain bool) (string, string) {
	st, n, msg, kb := b.nodeForWrite(ctx, acct, id)
	if msg != "" {
		return msg, kb
	}
	if drain && !n.Maintenance {
		if deps := egress.Dependents(st, n.ID); len(deps) > 0 {
			return b.drainPicker(ctx, st, n, deps)
		}
	}
	return b.applyMaintenance(ctx, acct, st, n, drain, "")
}

// nodeForWrite resolves a node the caller may drain or resume. The node is read
// from the full state, since operators see every node, and the write is gated by
// ownership.
func (b *Bot) nodeForWrite(ctx context.Context, acct model.AdminAccount, id string) (model.State, model.Node, string, string) {
	st, err := b.store.LoadState(ctx)
	if err != nil {
		return st, model.Node{}, errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	n, ok := findNode(st, id)
	if !ok {
		return st, n, tr(ctx, "No such server."), inlineKeyboard([][]ikBtn{backTo(ctx, "Servers", "m:nodes")})
	}
	if !authz.CanWriteNode(acct, n) {
		return st, n, tr(ctx, "⛔ Node management is available to admins only."), inlineKeyboard([][]ikBtn{backTo(ctx, "Servers", "m:nodes")})
	}
	return st, n, "", ""
}

// drainPicker asks where the groups that leave by this exit should go, naming
// them, because "3 groups" is not something anyone can check.
func (b *Bot) drainPicker(ctx context.Context, st model.State, n model.Node, deps []model.Group) (string, string) {
	names := make([]string, 0, len(deps))
	for _, g := range deps {
		names = append(names, egress.NameOr(g.Name, g.ID))
	}
	shown := names
	tail := ""
	if len(shown) > 5 {
		shown, tail = shown[:5], trf(ctx, " and %d more", len(names)-5)
	}
	text := trf(ctx, "⏸ Drain %s?\n\nTraffic of these groups leaves by it: %s%s.\nWhere do they go instead?",
		n.Name, strings.Join(shown, ", "), tail)
	// Rules that name this exit are not moved with the groups; each follows what it
	// declares for its exit being out. The panel's own drain dialog says so too.
	if named := ruleNames(st, n.ID); named != "" {
		text += trf(ctx, "\n\nRules naming this exit: %s. Each then follows what it declares.", named)
	}

	var rows [][]ikBtn
	for _, x := range st.Nodes {
		if x.ID == n.ID || !x.IsExit() || x.Maintenance {
			continue
		}
		rows = append(rows, []ikBtn{{"🚪 " + x.Name, "ndg:" + n.ID + ":" + x.ID}})
	}
	rows = append(rows,
		[]ikBtn{{tr(ctx, "🏠 Out of the entry node"), "ndg:" + n.ID + ":" + model.ExitDirect}},
		[]ikBtn{{tr(ctx, "✖ Cancel"), "n:" + n.ID}})
	return text, inlineKeyboard(rows)
}

// ruleNames lists the rules that send traffic to a node, for the drain question.
func ruleNames(st model.State, nodeID string) string {
	var names []string
	for _, p := range egress.RulesNaming(st, nodeID) {
		names = append(names, egress.NameOr(p.Name, p.ID))
	}
	return strings.Join(names, ", ")
}

// nodeDrainTo is the ndg: tap: move the dependent groups to the chosen exit and
// drain the node, as one operation.
func (b *Bot) nodeDrainTo(ctx context.Context, acct model.AdminAccount, payload string) (string, string) {
	id, target, ok := strings.Cut(payload, ":")
	if !ok {
		return tr(ctx, "Unknown action. Tap ⬅ Menu."), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	st, n, msg, kb := b.nodeForWrite(ctx, acct, id)
	if msg != "" {
		return msg, kb
	}
	if target != model.ExitDirect {
		t, ok := findNode(st, target)
		if !ok || !t.IsExit() || t.Maintenance || t.ID == n.ID {
			// The card the tap came from may be older than the fleet is: ask again
			// against what is there now instead of moving groups onto a dead exit.
			return b.drainPicker(ctx, st, n, egress.Dependents(st, n.ID))
		}
	}
	return b.applyMaintenance(ctx, acct, st, n, true, target)
}

// applyMaintenance moves the dependent groups (when a target was chosen), sets
// the flag, and writes the whole thing to the journal as one operation: a line
// per group, in the namespace that owns it, plus a summary of its own.
func (b *Bot) applyMaintenance(ctx context.Context, acct model.AdminAccount, st model.State, n model.Node, drain bool, target string) (string, string) {
	opID := model.NewOpID()
	var moves []egress.Move
	if target != "" {
		var ids []string
		for _, g := range egress.Dependents(st, n.ID) {
			ids = append(ids, g.ID)
		}
		var err error
		moves, err = egress.Switch(ctx, b.store, ids, target)
		// Whatever moved before an error is still logged: a half-done move
		// nobody can see is a move nobody can undo.
		b.logEgressMoves(ctx, moves, journal.Line("maintenance of %s", egress.Label(st, n.ID)), acct.Username, opID)
		if err != nil {
			return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
		}
	}
	if err := b.store.SetNodeMaintenance(ctx, n.ID, drain); err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	line := nodeMaintLine(n, drain)
	if len(moves) > 0 {
		line = journal.Join(line, journal.Line(" — %s moved off it", journal.Groups(len(moves))))
	}
	b.recordOp(ctx, model.SeverityInfo, line, acct.Username, "", opID) // node work is infra
	return b.nodeCard(ctx, acct, n.ID)
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
// authority and carries the role/namespace; an unbound id is rejected. The flat
// allowlist in the panel's Bots tab grants nothing: it remains a read-only
// reference for copying an id into a binding.
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
// screens: its own namespace's clients/groups/exit-rules (plus the guard baseline;
// plus all nodes for an admin). The bootstrap owner is scoped to its own namespace
// here too (seeAll=false); cross-namespace browsing is the deliberate, read-only
// /namespaces lens (lensList/lensView, which read the store directly), not the
// default /users view. This mirrors the panel: peers' clients never show up by
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
		return trf(ctx, "serving from: %s\nservers: %d (%d working, %d not answering)\nyour clients: %d · your groups: %d",
			active, len(st.Nodes), up, down, len(st.Users), len(st.Groups))
	}
	return trf(ctx, "Control plane\nserving from: %s\nservers: %d (%d working, %d not answering)\nclients: %d · groups: %d",
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
		return tr(ctx, "No servers.")
	}
	nodes := append([]model.Node(nil), st.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	var b2 strings.Builder
	b2.WriteString(tr(ctx, "Servers:\n"))
	for _, n := range nodes {
		state := nodeStateOf(n)
		fmt.Fprintf(&b2, "%s %s · %s · %s\n", state.glyph, n.Name, nodeRole(ctx, nodes, n), tr(ctx, state.words))
	}
	return strings.TrimRight(b2.String(), "\n")
}

func (b *Bot) cmdUsers(ctx context.Context, acct model.AdminAccount) string {
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	if len(st.Users) == 0 {
		return tr(ctx, "No clients.")
	}
	users := append([]model.User(nil), st.Users...)
	sort.Slice(users, func(i, j int) bool { return displayName(users[i]) < displayName(users[j]) })
	var b2 strings.Builder
	b2.WriteString(trf(ctx, "Clients (%d):\n", len(users)))
	for _, u := range users {
		fmt.Fprintf(&b2, "%s %s\n", accessOf(u, b.now()).glyph, displayName(u))
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
	name := strings.Join(args, " ")
	u, ok := findUser(st, name)
	if !ok {
		return trf(ctx, "No such client: %s", name)
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
		fmt.Fprintf(&b2, "%s — %s\n", t.Username, humanBytes(ctx, t.TotalBytes))
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
	// The typed command has no screen to ask on, so it refuses instead of stranding
	// the groups; the card's ⏸ button asks and then does both.
	if on {
		if deps := egress.Dependents(st, n.ID); len(deps) > 0 && !n.Maintenance {
			names := make([]string, 0, len(deps))
			for _, g := range deps {
				names = append(names, egress.NameOr(g.Name, g.ID))
			}
			return trf(ctx, "⏸ %s is the exit for these groups: %s.\nDrain it from its card (/nodes) — you will be asked where they go.", n.Name, strings.Join(names, ", "))
		}
	}
	if err := b.store.SetNodeMaintenance(ctx, n.ID, on); err != nil {
		return errMsg(err)
	}
	b.record(ctx, model.SeverityInfo, nodeMaintLine(n, on), acct.Username, "")
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

// convoTTL is how long a half-finished form waits for its next answer.
//
// Without a limit, a wizard someone opened and walked away from would stay open,
// and the next plain sentence typed in that chat (an unrelated note, a forwarded
// name, an answer meant for a person) would be read as its next step and could
// create a client. Every prompt says it can be left with ✖ Cancel; this covers
// the case where nobody does.
const convoTTL = 15 * time.Minute

func (b *Bot) convoFor(fromID int64) *convo {
	b.convoMu.Lock()
	defer b.convoMu.Unlock()
	c, ok := b.convos[fromID]
	if !ok {
		return nil
	}
	if b.now().Sub(c.startedAt) > convoTTL {
		delete(b.convos, fromID)
		return nil
	}
	return c
}

func (b *Bot) clearConvo(fromID int64) bool {
	b.convoMu.Lock()
	defer b.convoMu.Unlock()
	_, ok := b.convos[fromID]
	delete(b.convos, fromID)
	return ok
}

// startConvo registers a fresh guided flow for the sender. The clock starts here
// and is not reset by the steps. A form is open for convoTTL from the moment
// it was opened, not from the last thing that touched it.
func (b *Bot) startConvo(ctx context.Context, fromID int64, c *convo) {
	b.convoMu.Lock()
	c.startedAt = b.now()
	c.token = randomHex(4)
	c.chatID = chatOf(ctx)
	b.convos[fromID] = c
	b.convoMu.Unlock()
}

// startAddUser begins the guided flow and prompts for the username.
func (b *Bot) startAddUser(ctx context.Context, fromID int64) string {
	b.startConvo(ctx, fromID, &convo{kind: kAddUser, step: 0})
	return tr(ctx, "➕ New client — step 1 of 3.\nSend the client's name, the way you will look for it later.")
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
	case 0: // the client's name
		if err := model.ValidateUsername(input); err != nil {
			return "⚠️ " + err.Error() + tr(ctx, "\nSend a different name, or /cancel."), ""
		}
		c.name = input
		// The connection is issued under the login, so it is derived here instead
		// of asked for: the operator has a name, and a name with
		// a space in it went straight into the client's credentials before.
		c.login = loginFor(st, input)
		c.step = 1
		return groupStepPrompt(ctx, st, c), b.groupPickKeyboard(ctx, st)
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
		return b.expiryStepPrompt(ctx, st, c), expiryPickKeyboard(ctx)
	case 2: // access period -> the review
		exp, perr := parseExpiryReply(input, b.now())
		if perr != "" {
			return "⚠️ " + tr(ctx, perr) + tr(ctx, "\nReply with days (e.g. 30) or “never”."), expiryPickKeyboard(ctx)
		}
		c.exp = exp
		c.step = 3
		return b.addUserReview(ctx, st, c)
	default: // the login, typed from the review
		if err := model.ValidateUsername(input); err != nil {
			return "⚠️ " + err.Error() + tr(ctx, "\nReply with another login, or tap ⬅ Back."), ""
		}
		if _, exists := findUser(st, input); exists {
			return tr(ctx, "That login is taken. Reply with another one."), ""
		}
		c.login = input
		return b.addUserReview(ctx, st, c)
	}
}

// addUserReview is the last look before the client exists: what it will be
// called, what it will connect as, and the date its access actually ends on,
// since a number of days is not something anyone can check against a calendar.
func (b *Bot) addUserReview(ctx context.Context, st model.State, c *convo) (string, string) {
	period := tr(ctx, "no limit")
	if c.exp != nil {
		period = trf(ctx, "until %s", c.exp.UTC().Format(dateOnly))
	}
	text := trf(ctx, "➕ New client\nname: %s\nlogin: %s\ngroup: %s\naccess period: %s",
		c.name, c.login, groupNameByID(st, c.group), period)
	rows := [][]ikBtn{
		{{tr(ctx, "✓ Create client"), "auok"}},
		{{tr(ctx, "✏️ Change login"), "aulog"}},
		{{tr(ctx, lblBack), "auback"}, {tr(ctx, lblCancel), "cx"}},
	}
	return text, inlineKeyboard(rows)
}

// loginFor derives a free login from the client's name: the name itself where it
// is already a usable login, transliterated where it is not, numbered where the
// obvious one is taken.
func loginFor(st model.State, name string) string {
	base := clampSlug(slugify(name))
	if base == "" {
		base = "client"
	}
	login := base
	for n := 2; n < 1000; n++ {
		if _, taken := findUser(st, login); !taken {
			return login
		}
		login = fmt.Sprintf("%s-%d", base, n)
	}
	return base + "-" + randomHex(3)
}

// createUserFromConvo writes the client the wizard accumulated and opens its
// card. The card carries 🔗 Connection, which is the next thing anybody wants,
// instead of leaving the operator to type /config from memory.
func (b *Bot) createUserFromConvo(ctx context.Context, acct model.AdminAccount, fromID int64, c *convo) (string, string) {
	login := c.login
	if login == "" {
		login = c.name
	}
	u := model.User{
		ID:          genID(c.name),
		Username:    login,
		Secret:      randomHex(16),
		DisplayName: c.name,
		Enabled:     true,
		GroupID:     c.group,
		OwnerID:     ownerFor(acct),
		ExpiresAt:   c.exp,
	}
	if err := u.Validate(); err != nil {
		b.clearConvo(fromID)
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	if err := b.store.UpsertUser(ctx, u); err != nil {
		b.clearConvo(fromID)
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	b.recordFor(ctx, model.SeverityInfo, journal.Line("created client %s", ref(u.DisplayName, u.ID)), acct)
	b.clearConvo(fromID)
	return b.userCard(ctx, acct, u.ID)
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
		{{tr(ctx, "1 year"), "aue:365"}, {tr(ctx, "♾ No limit"), "aue:0"}},
		{{tr(ctx, "✖ Cancel"), "cx"}},
	})
}

// addUserPickGroup handles an aug: tap: set the chosen group on the live wizard and
// move to the expiry step.
func (b *Bot) addUserPickGroup(ctx context.Context, acct model.AdminAccount, fromID int64, gid string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return b.formExpired(ctx)
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
	return b.expiryStepPrompt(ctx, st, c), expiryPickKeyboard(ctx)
}

// addUserPickExpiry handles an aue: tap: put the preset's date in the draft and
// show the review.
func (b *Bot) addUserPickExpiry(ctx context.Context, acct model.AdminAccount, fromID int64, daysStr string) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return b.formExpired(ctx)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	c.exp = nil
	if daysStr != "0" {
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			e := expiryOn(b.now().UTC().AddDate(0, 0, days))
			c.exp = &e
		}
	}
	c.step = 3
	return b.addUserReview(ctx, st, c)
}

// addUserCreate is the ✓ Create client tap.
func (b *Bot) addUserCreate(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser || c.step < 3 {
		return b.formExpired(ctx)
	}
	return b.createUserFromConvo(ctx, acct, fromID, c)
}

// addUserBack steps the wizard back to the access period, the answer above the
// review.
func (b *Bot) addUserBack(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return b.formExpired(ctx)
	}
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err), inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	c.step = 2
	return b.expiryStepPrompt(ctx, st, c), expiryPickKeyboard(ctx)
}

// addUserAskLogin asks for a login to replace the derived one. The review is one
// step away, so nothing is lost by changing it here instead of at the start.
func (b *Bot) addUserAskLogin(ctx context.Context, fromID int64) (string, string) {
	c := b.convoFor(fromID)
	if c == nil || c.kind != kAddUser {
		return b.formExpired(ctx)
	}
	c.step = 4
	return trf(ctx, "✏️ Send the login this client connects under.\nnow: %s", c.login),
		inlineKeyboard([][]ikBtn{{{tr(ctx, lblBack), "auback"}, {tr(ctx, lblCancel), "cx"}}})
}

// formExpired is the one answer for a tap on a form that is no longer open.
func (b *Bot) formExpired(ctx context.Context) (string, string) {
	return tr(ctx, "That form expired. Tap ➕ New client to start over."),
		inlineKeyboard([][]ikBtn{{{tr(ctx, lblNewClient), "m:adduser"}}, backRow(ctx)})
}

// groupStepPrompt is the wizard's "choose a group" prompt. It only offers the
// '-' default shortcut when a single group exists (so resolveGroup can actually
// honor it); with several groups it requires an explicit choice, rather than
// promising a default that then errors with "Multiple groups".
func groupStepPrompt(ctx context.Context, st model.State, c *convo) string {
	head := trf(ctx, "➕ New client — step 2 of 3: group.\nname: %s\n", c.name)
	if len(st.Groups) > 1 {
		return head + trf(ctx, "Tap a group below, or reply with its name: %s", groupNames(ctx, st))
	}
	return head + trf(ctx, "Tap a group below, or reply with its name, or “-” for the default.\nGroups: %s", groupNames(ctx, st))
}

// expiryStepPrompt keeps the two answers already given in view, without asking
// them again.
func (b *Bot) expiryStepPrompt(ctx context.Context, st model.State, c *convo) string {
	return trf(ctx, "➕ New client — step 3 of 3: access period.\nname: %s\ngroup: %s\nTap a preset, or reply with a number of days (e.g. 30) or “never”.",
		c.name, groupNameByID(st, c.group))
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
	// The prompt is read in the reader's language, and the answer arrives in it.
	case "", "never", "none", "no", "-", "никогда", "бессрочно", "без ограничения", "нет":
		return nil, ""
	}
	days, err := strconv.Atoi(s)
	if err != nil || days <= 0 {
		return nil, "I didn't understand that."
	}
	exp := expiryOn(now.UTC().AddDate(0, 0, days))
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
	b.recordFor(ctx, model.SeverityInfo, journal.Line("created client %s", ref(u.DisplayName, u.ID)), acct)
	return trf(ctx, "✓ Created client %s in group %s. Open 👤 Clients to hand out its connection.", name, groupNameByID(st, groupID))
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
// The lookup is namespace-scoped: an operator can export only its own clients.
// The reply reveals the secret, and handleUpdate refuses it outside a private chat.
func (b *Bot) cmdConfig(ctx context.Context, acct model.AdminAccount, args []string) string {
	if len(args) == 0 {
		return tr(ctx, "Usage: /config <name>")
	}
	// Names have spaces in them, and the bot's own "send /config <name>" hint after
	// creating a client hands back the name it was given. Taking only the first
	// word turned /config Anna Smith into a search for "Anna".
	name := strings.Join(args, " ")
	st, err := b.scoped(ctx, acct)
	if err != nil {
		return errMsg(err)
	}
	u, ok := findUser(st, name)
	if !ok {
		return trf(ctx, "No such client: %s", name)
	}
	text, _ := b.connectionScreen(ctx, acct, u.ID)
	return text
}

// connectionScreen renders handing out a connection, wherever it is
// asked for: who it is for, whether their access is actually live, which entry
// server it points at when there is a choice, and one link: the one that can be
// sent to a person. The QR and the file are actions of their own.
func (b *Bot) connectionScreen(ctx context.Context, acct model.AdminAccount, id string) (string, bool) {
	ex, err := b.findExportable(ctx, acct, func(st model.State) (model.User, bool) { return userByID(st, id) })
	switch {
	case errors.Is(err, errNoClient):
		return tr(ctx, "No such client."), false
	case errors.Is(err, errNoEntry):
		return noEntryMsg(ctx, acct), false
	case err != nil:
		return errMsg(err), false
	}
	u := ex.user
	cfg, err := clientcfg.Build(ex.state, u.ID, ex.entry.ID, clientcfg.Options{}, b.now())
	if err != nil {
		return errMsg(err), false
	}
	var sb strings.Builder
	sb.WriteString(trf(ctx, "%s — %s", tr(ctx, lblConnection), displayName(u)))
	sb.WriteString("\n" + accessOf(u, b.now()).line(ctx))
	if entries(ex.state) > 1 {
		// Which entry answers matters only where there is more than one; in a
		// single-server deployment it is noise about infrastructure nobody chose.
		sb.WriteString(trf(ctx, "\nentry server: %s", ex.entry.Name))
	}
	if cfg.QRLink != "" {
		sb.WriteString(trf(ctx, "\n\nLink to pass on — it opens the connection page:\n%s", cfg.QRLink))
	}
	for _, w := range cfg.Warnings {
		sb.WriteString("\n\n⚠️ " + configWarning(ctx, acct, w))
	}
	return sb.String(), true
}

// configWarning translates the warnings a config can carry. The two that name a
// node are infra detail and an answer an operator cannot act on. They become
// one sentence that says what to do about it.
func configWarning(ctx context.Context, acct model.AdminAccount, w string) string {
	switch w {
	case "entry is in maintenance; hand out a config for another entry until it returns",
		"user is disabled or expired; this config will not authenticate until re-enabled",
		"this client has no secret set — the deep link/QR are omitted and the config will not authenticate; set a password and re-export":
		return tr(ctx, w)
	}
	if acct.CanManageInfra() {
		return w
	}
	return tr(ctx, "This connection may not work yet — tell an admin.")
}

// entries counts the network's entry servers.
func entries(st model.State) int {
	n := 0
	for _, x := range st.Nodes {
		if x.IsEntry() {
			n++
		}
	}
	return n
}

// exportable is one client together with everything its connection is built from.
type exportable struct {
	user  model.User
	state model.State
	entry model.Node
}

// errNoClient and errNoEntry are the two ways an export can come up empty, kept
// apart so each call site can word them for the surface it answers on.
var (
	errNoClient = errors.New("no such client")
	errNoEntry  = errors.New("no entry node")
)

// findExportable resolves a client the caller may export and the state its config
// is built from.
//
// Ownership is settled in the caller's scoped view: a client an operator cannot
// see there is not theirs to hand out. The config is then built from the full
// state, because a connection is made of infrastructure an operator is never
// shown: which entry node answers and how to reach it. The scoped view cannot
// serve that half, since ScopeStateView blanks Nodes and leaves pickEntry nothing
// to choose from.
//
// The panel's own export is built the same way round, full state with ownership
// checked against it by hand. See handleClientConfig.
func (b *Bot) findExportable(ctx context.Context, acct model.AdminAccount, pick func(model.State) (model.User, bool)) (exportable, error) {
	full, err := b.store.LoadState(ctx)
	if err != nil {
		return exportable{}, err
	}
	u, ok := pick(authz.ScopeStateView(full, acct, false))
	if !ok {
		return exportable{}, errNoClient
	}
	entry, ok := pickEntry(full)
	if !ok {
		return exportable{}, errNoEntry
	}
	return exportable{user: u, state: full, entry: entry}, nil
}

// noEntryMsg says the network has nothing to connect to, in the terms of whoever
// is reading: an admin can go and add an entry node, an operator cannot.
func noEntryMsg(ctx context.Context, acct model.AdminAccount) string {
	if acct.CanManageInfra() {
		return tr(ctx, "No entry nodes in the network yet — add one in the panel first.")
	}
	return tr(ctx, "⚠️ The network has no entry node right now, so there is nothing to connect to. An admin has to bring one back.")
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
	b.recordFor(ctx, model.SeverityInfo, enabledLine(u), acct)
	return trf(ctx, "✓ %s is now %s.", displayName(u), enabledWord(ctx, enabled))
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

func errMsg(err error) string { return "⚠️ " + err.Error() }

// Base-1024 units are labeled in IEC binary form (KiB/MiB/…), matching the
// panel, and not the decimal KB/MB which would understate the magnitude.
// The panel writes them in the reader's alphabet. A Russian card saying
// "1.95 KiB" was the one Latin word left on it.
var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

func humanBytes(ctx context.Context, n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d %s", n, tr(ctx, byteUnits[0]))
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %s", float64(n)/float64(div), tr(ctx, byteUnits[exp+1]))
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
	return "u-" + clampSlug(slug) + "-" + randomHex(3)
}

// clampSlug keeps a generated id short enough that two of them still fit in one
// Telegram callback. The longest the bot builds is "ucgp:<client>:<group>", and
// Telegram rejects the whole message, not the one button, past 64 bytes. A
// client with a long name would take its entire menu down with it. The panel
// clamps its own slugs to the same shape (slug() in app.js).
func clampSlug(s string) string {
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	return s
}

// maxSlugLen leaves room for the two-character kind prefix, the separator and six
// hex digits, which puts a generated id at 29 bytes, the same as the panel's.
const maxSlugLen = 20

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
