package panel

import (
	"context"
	"net/http"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

// settingsView is the browser-facing shape of settings: tokens are NEVER sent in
// the clear — only a "set" flag and the last 4 chars — plus live channel status.
type settingsView struct {
	Bot    botSettingsView     `json:"bot"`
	Alert  alertSettingsView   `json:"alert"`
	Backup backupSettingsView  `json:"backup"`
	Fleet  model.FleetSettings `json:"fleet"`
	Panel  panelSettingsView   `json:"panel"`
}

// panelSettingsView reports the EFFECTIVE tunables (defaults applied) so the UI
// shows real numbers rather than bare zeros.
type panelSettingsView struct {
	ReconcileSeconds      int `json:"reconcile_seconds"`
	StatsSeconds          int `json:"stats_seconds"`
	BillingAlertHours     int `json:"billing_alert_hours"`
	BillingAlertDays      int `json:"billing_alert_days"`
	ExpiryAlertDays       int `json:"expiry_alert_days"`
	SessionHours          int `json:"session_hours"`
	EgressFailoverSeconds int `json:"egress_failover_seconds"`
	ActivityKBPerMin      int `json:"activity_kb_per_min"`
}

type botSettingsView struct {
	Enabled    bool   `json:"enabled"`
	TokenSet   bool   `json:"token_set"`
	TokenLast4 string `json:"token_last4,omitempty"`
	Status     string `json:"status"` // ok|unauthorized|unreachable|unconfigured
	Detail     string `json:"detail,omitempty"`
	// CheckedAt is the unix-ms time of the last live getMe probe (0 if never). The
	// UI polls it after a save to know when the freshly-triggered check has landed.
	CheckedAt int64 `json:"checked_at"`
}

type alertSettingsView struct {
	Enabled    bool   `json:"enabled"`
	TokenSet   bool   `json:"token_set"`
	TokenLast4 string `json:"token_last4,omitempty"`
	ChatID     string `json:"chat_id"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	CheckedAt  int64  `json:"checked_at"`
	// SharesBotChannel is true when the primary (α) and backup (β) alert senders
	// resolve to the SAME Telegram bot — i.e. no distinct management bot is set, so
	// α falls back to the alert token. Both test buttons then exercise one channel,
	// which looks like two independent ones. The UI warns on this.
	SharesBotChannel bool `json:"shares_bot_channel"`
}

// backupSettingsView is the browser-facing shape of off-site backup config. The
// age recipient is a PUBLIC key, so it is shown in the clear; the bot token is
// reused from the alert channel and is not duplicated here.
type backupSettingsView struct {
	Enabled      bool   `json:"enabled"`
	ChatID       string `json:"chat_id"`
	AgeRecipient string `json:"age_recipient"`
	ChunkBytes   int    `json:"chunk_bytes"`
	// Schedule & retention, shown as effective values (defaults applied) so the
	// fields never render as a bare 0.
	LocalEnabled       bool `json:"local_enabled"`
	IntervalHours      int  `json:"interval_hours"`
	Keep               int  `json:"keep"`
	VerifyEnabled      bool `json:"verify_enabled"`
	VerifyIntervalDays int  `json:"verify_interval_days"`
}

func last4(s string) string {
	if len(s) <= 4 {
		return ""
	}
	return s[len(s)-4:]
}

// unixMS renders a probe time as unix milliseconds, 0 for the zero value (never
// checked), so the browser can compare timestamps without a date parse.
func unixMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (p *Panel) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := p.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	botStatus := p.botHealthFor("bot")
	alertStatus := p.botHealthFor("alert")
	view := settingsView{
		Bot: botSettingsView{
			Enabled: s.Bot.Enabled, TokenSet: s.Bot.Token != "", TokenLast4: last4(s.Bot.Token),
			Status: botStatus.Status, Detail: botStatus.Detail, CheckedAt: unixMS(botStatus.At),
		},
		Alert: alertSettingsView{
			Enabled: s.Alert.Enabled, TokenSet: s.Alert.Token != "", TokenLast4: last4(s.Alert.Token),
			ChatID: s.Alert.ChatID, Status: alertStatus.Status, Detail: alertStatus.Detail, CheckedAt: unixMS(alertStatus.At),
			// α uses the mgmt bot token when set, else falls back to the alert token,
			// which β also uses — so with no distinct mgmt bot the two channels coincide.
			SharesBotChannel: s.Alert.Enabled && s.Alert.Token != "" && !(s.Bot.Enabled && s.Bot.Token != ""),
		},
		Backup: backupSettingsView{
			Enabled: s.Backup.Enabled, ChatID: s.Backup.ChatID,
			AgeRecipient: s.Backup.AgeRecipient, ChunkBytes: s.Backup.ChunkBytes,
			LocalEnabled:       s.Backup.LocalOn(),
			IntervalHours:      int(s.Backup.BackupInterval().Hours()),
			Keep:               s.Backup.KeepOrDefault(),
			VerifyEnabled:      s.Backup.VerifyOn(),
			VerifyIntervalDays: int(s.Backup.VerifyInterval().Hours() / 24),
		},
	}
	view.Fleet = s.Fleet
	view.Panel = panelSettingsView{
		ReconcileSeconds:      int(s.Panel.ReconcileInterval().Seconds()),
		StatsSeconds:          int(s.Panel.StatsInterval().Seconds()),
		BillingAlertHours:     int(s.Panel.BillingInterval().Hours()),
		BillingAlertDays:      s.Panel.BillingDays(),
		ExpiryAlertDays:       s.Panel.ExpiryDays(),
		SessionHours:          int(s.Panel.SessionTTL().Hours()),
		EgressFailoverSeconds: int(s.Panel.EgressFailover().Seconds()),
		ActivityKBPerMin:      s.Panel.ActivityKBMin(),
	}
	writeJSON(w, http.StatusOK, view)
}

// settingsUpdate is the POST body. A nil/empty Token means "keep the existing
// one" so the browser (which only ever sees the mask) cannot blank a secret.
type settingsUpdate struct {
	Bot struct {
		Enabled *bool  `json:"enabled"` // omitted -> keep current (don't silently disable)
		Token   string `json:"token"`
	} `json:"bot"`
	Alert struct {
		Enabled *bool   `json:"enabled"` // omitted -> keep current
		Token   string  `json:"token"`
		ChatID  *string `json:"chat_id"` // omitted -> keep current (don't silently blank)
	} `json:"alert"`
	Backup struct {
		Enabled            bool   `json:"enabled"`
		ChatID             string `json:"chat_id"`
		AgeRecipient       string `json:"age_recipient"`
		ChunkBytes         int    `json:"chunk_bytes"`
		LocalEnabled       *bool  `json:"local_enabled"`  // omitted -> keep current (don't silently disable)
		IntervalHours      int    `json:"interval_hours"` // 0 -> model default (24h)
		Keep               int    `json:"keep"`           // 0 -> model default (14)
		VerifyEnabled      *bool  `json:"verify_enabled"` // omitted -> keep current
		VerifyIntervalDays int    `json:"verify_interval_days"`
	} `json:"backup"`
	Fleet model.FleetSettings `json:"fleet"`
	Panel model.PanelSettings `json:"panel"`
}

func (p *Panel) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsUpdate
	if !decode(w, r, &in) {
		return
	}
	cur, err := p.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	next := model.Settings{
		Bot: model.BotSettings{
			Enabled: keepBoolVal(cur.Bot.Enabled, in.Bot.Enabled),
			Token:   keepOrSet(cur.Bot.Token, in.Bot.Token),
		},
		Alert: model.AlertSettings{
			Enabled: keepBoolVal(cur.Alert.Enabled, in.Alert.Enabled),
			Token:   keepOrSet(cur.Alert.Token, in.Alert.Token),
			ChatID:  keepStrVal(cur.Alert.ChatID, in.Alert.ChatID),
		},
		Backup: model.BackupSettings{
			Enabled:            in.Backup.Enabled,
			ChatID:             in.Backup.ChatID,
			AgeRecipient:       in.Backup.AgeRecipient,
			ChunkBytes:         in.Backup.ChunkBytes,
			LocalEnabled:       keepBool(cur.Backup.LocalEnabled, in.Backup.LocalEnabled),
			IntervalHours:      in.Backup.IntervalHours,
			Keep:               in.Backup.Keep,
			VerifyEnabled:      keepBool(cur.Backup.VerifyEnabled, in.Backup.VerifyEnabled),
			VerifyIntervalDays: in.Backup.VerifyIntervalDays,
		},
		Fleet: in.Fleet,
		Panel: in.Panel,
	}
	if err := p.store.SaveSettings(r.Context(), next); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Apply the new session lifetime to sessions created from now on (live).
	if p.sessions != nil {
		p.sessions.SetTTL(next.Panel.SessionTTL())
	}
	// Re-check channel health immediately so the UI reflects the new config.
	go p.CheckBotHealthOnce(context.Background())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// keepOrSet returns the new value unless it is empty, in which case the existing
// secret is preserved (a blank token field means "unchanged", not "clear").
func keepOrSet(existing, incoming string) string {
	if incoming == "" {
		return existing
	}
	return incoming
}

// keepBool preserves the stored toggle when the request omits it (incoming nil),
// so a client that does not send local_enabled/verify_enabled cannot silently
// flip a backup off; an explicit value overrides.
func keepBool(existing, incoming *bool) *bool {
	if incoming != nil {
		return incoming
	}
	return existing
}

// keepBoolVal is keepBool for a plain-bool stored field: a nil incoming (the
// section was omitted from a partial POST) keeps the stored value, so an omitted
// alert/bot section can't silently disable it. Backup already did this.
func keepBoolVal(existing bool, incoming *bool) bool {
	if incoming != nil {
		return *incoming
	}
	return existing
}

// keepStrVal keeps the stored string when the request omits the field (nil); an
// explicit value (including "") overrides, so a partial POST can't silently blank
// the alert chat_id.
func keepStrVal(existing string, incoming *string) string {
	if incoming != nil {
		return *incoming
	}
	return existing
}

// handleTestChannel sends a live test through a configured Telegram channel so
// the operator can confirm token+chat work without waiting for a real alert. It
// tests the SAVED settings (save first, then test). channel: bot|alert|backup.
func (p *Panel) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if !decode(w, r, &req) {
		return
	}
	s, err := p.store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ctx := r.Context()
	lang := localeLang(p.account(r).Locale) // the tester reads the result in their own UI language
	switch req.Channel {
	case "bot":
		if !s.Bot.Enabled || s.Bot.Token == "" {
			writeErr(w, http.StatusBadRequest, "management bot is not configured")
			return
		}
		status, detail := telegramGetMe(ctx, s.Bot.Token)
		if status != "ok" {
			writeErr(w, http.StatusBadGateway, "bot unreachable: "+status+" "+detail)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "detail": "getMe ok"})
	case "alert":
		// Primary (α) alert path: exactly the bot the real sender uses
		// (alertSenderCreds → the management bot), so a green test means production
		// alerts land.
		tok, chat := alertSenderCreds(s)
		if !s.Alert.Enabled || tok == "" || chat == "" {
			writeErr(w, http.StatusBadRequest, "alert channel is not configured (enable it and set a chat id)")
			return
		}
		if err := (watchdog.TelegramAlerter{Token: tok, ChatID: chat}).Alert(ctx, watchdog.SeverityLow, watchdog.Render(watchdog.MsgTestPrimaryAlert(), lang)); err != nil {
			writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "detail": "test message sent to alert chat"})
	case "alert_backup":
		// Backup (β) alert path: the standby's failover bot (s.Alert.Token) posting
		// to the same alert chat. Verifies the redundant sender that pages you when
		// the primary/management bot is down.
		if !s.Alert.Enabled || s.Alert.Token == "" || s.Alert.ChatID == "" {
			writeErr(w, http.StatusBadRequest, "backup alert bot is not configured (enable alerts, set the backup bot token and the alert chat)")
			return
		}
		if err := (watchdog.TelegramAlerter{Token: s.Alert.Token, ChatID: s.Alert.ChatID}).Alert(ctx, watchdog.SeverityLow, watchdog.Render(watchdog.MsgTestBackupAlert(), lang)); err != nil {
			writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "detail": "test message sent via the backup bot"})
	case "backup":
		// Off-site delivery uses the alert-bot token + the dedicated backup chat
		// (matches cli/backup.go DeliverTelegram).
		if s.Alert.Token == "" || s.Backup.ChatID == "" {
			writeErr(w, http.StatusBadRequest, "backup channel is not configured (set the alert bot token and a backup chat id)")
			return
		}
		if err := (watchdog.TelegramAlerter{Token: s.Alert.Token, ChatID: s.Backup.ChatID}).Alert(ctx, watchdog.SeverityLow, watchdog.Render(watchdog.MsgTestBackupChannel(), lang)); err != nil {
			writeErr(w, http.StatusBadGateway, "send failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "detail": "test message sent to backup chat"})
	default:
		writeErr(w, http.StatusBadRequest, "unknown channel")
	}
}

func (p *Panel) botHealthFor(key string) botHealthRecord {
	p.botHealthMu.Lock()
	defer p.botHealthMu.Unlock()
	if rec, ok := p.botHealth[key]; ok {
		return rec
	}
	return botHealthRecord{Status: "unconfigured"}
}

// alertSenderCreds resolves the α (primary-side) alert sender: it posts to the
// alert chat, but its token is the management bot's by default (one bot does
// double duty — operator commands + sending alerts), falling back to a dedicated
// alert-bot token if no management token is set. Returns empty when the alert
// channel is disabled or unconfigured.
func alertSenderCreds(s model.Settings) (token, chatID string) {
	if !s.Alert.Enabled {
		return "", ""
	}
	chatID = s.Alert.ChatID
	switch {
	case s.Bot.Enabled && s.Bot.Token != "":
		token = s.Bot.Token
	default:
		token = s.Alert.Token
	}
	return token, chatID
}
