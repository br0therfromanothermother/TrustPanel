package panel

import (
	"context"
	"log"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

// audience selects who receives the Telegram push for a routed alert. The event
// log and console log are audience-agnostic (the panel records everything); only
// the Telegram fan-out is scoped, so an operator is paged about the data plane
// they share but never about control-plane internals (HA, backups) they cannot
// touch. Per-owner alerts (billing, config expiry) resolve recipients per message
// and use sendOwnerAlert directly rather than a fixed-audience alerter.
type audience int

const (
	// audAdmin: control-plane internals — HA/replication, bot-channel health,
	// backups. Only the global admin alert chat is paged.
	audAdmin audience = iota
	// audBroadcast: data-plane status — a node going down/recovering, an entry's
	// public edge dropping. Everyone with an alert chat is paged, since the data
	// plane is shared and node status is already visible to all in the panel.
	audBroadcast
)

// routedTelegram is a watchdog.Alerter that pushes to a fixed audience's chats,
// reloading creds + recipients from the store on each send so edits in the Bots
// tab / account settings take effect without a restart. It is the Telegram leg
// of the background monitors; the log + event-log legs are wired separately.
type routedTelegram struct {
	p   *Panel
	aud audience
}

// Alert satisfies watchdog.Alerter for an already-rendered string (no per-recipient
// language). Prefer AlertLocalized, which the monitors use.
func (rt routedTelegram) Alert(ctx context.Context, sev watchdog.Severity, msg string) error {
	return rt.AlertLocalized(ctx, sev, func(string) string { return msg })
}

// AlertLocalized renders msg once per recipient in that account's interface
// language, so each operator reads the alert in their own UI language.
func (rt routedTelegram) AlertLocalized(ctx context.Context, sev watchdog.Severity, msg watchdog.MsgFunc) error {
	if rt.p == nil || rt.p.store == nil {
		return nil // no store wired (e.g. unit tests) -> the recorder leg still fires
	}
	s, err := rt.p.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	tok, _ := alertSenderCreds(s)
	if tok == "" {
		return nil // alert channel disabled/unconfigured -> silent (single switch)
	}
	return sendToChats(ctx, tok, rt.p.audienceChats(ctx, s, rt.aud), sev, msg)
}

// chatTarget is one alert recipient: the Telegram chat and the interface language
// to render the message in, so each recipient reads it in their own UI language.
type chatTarget struct {
	chatID string
	lang   string
}

// localeLang normalizes an account Locale to a supported alert language, falling
// back to the source language when unset/unknown.
func localeLang(locale string) string {
	if locale == "ru" || locale == "en" {
		return locale
	}
	return watchdog.DefaultLang
}

// audienceChats resolves the deduped recipient targets for a fixed audience. The
// global admin chat is rendered in the fleet owner's interface language (or an
// account that owns that chat); each account chat in that account's language.
func (p *Panel) audienceChats(ctx context.Context, s model.Settings, aud audience) []chatTarget {
	accts, err := p.store.ListAdmins(ctx)
	if err != nil {
		log.Printf("alert routing: list accounts: %v", err)
	}
	byChat, adminLang := chatLangIndex(accts)
	seen := map[string]bool{}
	var out []chatTarget
	add := func(c, lang string) {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, chatTarget{chatID: c, lang: lang})
		}
	}
	add(s.Alert.ChatID, langForChat(byChat, s.Alert.ChatID, adminLang))
	if aud == audBroadcast {
		// Every account with a bound alert chat — operators learn that a shared
		// node went down, each in their own language.
		for _, a := range accts {
			add(a.AlertChatID, localeLang(a.Locale))
		}
	}
	return out
}

// ownerChats resolves recipients for a namespace-scoped alert (config expiry,
// node billing): the owning account's alert chat plus the global admin chat,
// deduped. An owner with no chat bound falls back to admin-only.
func (p *Panel) ownerChats(ctx context.Context, s model.Settings, ownerID string) []chatTarget {
	accts, _ := p.store.ListAdmins(ctx)
	byChat, adminLang := chatLangIndex(accts)
	seen := map[string]bool{}
	var out []chatTarget
	add := func(c, lang string) {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, chatTarget{chatID: c, lang: lang})
		}
	}
	add(s.Alert.ChatID, langForChat(byChat, s.Alert.ChatID, adminLang))
	if ownerID != "" {
		if a, err := p.store.AdminByUsername(ctx, ownerID); err == nil {
			add(a.AlertChatID, localeLang(a.Locale))
		}
	}
	return out
}

// ownerOnlyChats resolves recipients for an alert that names a hidden resource (a
// client config): the owning operator's chat ONLY, so the client identity does
// not cross into the admin namespace. An admin-namespace resource (ownerID "")
// or an operator with no chat bound falls back to the admin chat — better the
// admin (who already has DB access) hears it than nobody.
func (p *Panel) ownerOnlyChats(ctx context.Context, s model.Settings, ownerID string) []chatTarget {
	if ownerID != "" {
		if a, err := p.store.AdminByUsername(ctx, ownerID); err == nil && a.AlertChatID != "" {
			return []chatTarget{{chatID: a.AlertChatID, lang: localeLang(a.Locale)}}
		}
	}
	if s.Alert.ChatID != "" {
		accts, _ := p.store.ListAdmins(ctx)
		byChat, adminLang := chatLangIndex(accts)
		return []chatTarget{{chatID: s.Alert.ChatID, lang: langForChat(byChat, s.Alert.ChatID, adminLang)}}
	}
	return nil
}

// chatLangIndex maps each account's alert chat to its interface language and
// returns the fleet owner's (infra admin's) language as the default for chats
// not bound to a specific account (e.g. a manually-set global admin chat).
func chatLangIndex(accts []model.AdminAccount) (byChat map[string]string, adminLang string) {
	byChat = map[string]string{}
	adminLang = watchdog.DefaultLang
	for _, a := range accts {
		if a.AlertChatID != "" {
			byChat[a.AlertChatID] = localeLang(a.Locale)
		}
		if a.IsBootstrapOwner() {
			adminLang = localeLang(a.Locale)
		}
	}
	return byChat, adminLang
}

// langForChat returns the language bound to chatID, or fallback when the chat is
// not tied to a specific account.
func langForChat(byChat map[string]string, chatID, fallback string) string {
	if l, ok := byChat[chatID]; ok {
		return l
	}
	return fallback
}

// sendNamespaceAlert delivers an alert that names a hidden resource to the owner
// only (admin fallback when the owner has no chat). Used for config-expiry, which
// names a client. The log + event-log records are made by the caller.
func (p *Panel) sendNamespaceAlert(ctx context.Context, ownerID string, sev watchdog.Severity, msg watchdog.MsgFunc) {
	if p.store == nil {
		return
	}
	s, err := p.store.GetSettings(ctx)
	if err != nil {
		return
	}
	tok, _ := alertSenderCreds(s)
	if tok == "" {
		return
	}
	_ = sendToChats(ctx, tok, p.ownerOnlyChats(ctx, s, ownerID), sev, msg)
}

// sendOwnerAlert delivers a namespace-scoped Telegram alert to the owner (+admin
// fallback). The log + event-log records are made by the caller's recorder so
// this only handles the Telegram leg; it is always safe to call.
func (p *Panel) sendOwnerAlert(ctx context.Context, ownerID string, sev watchdog.Severity, msg watchdog.MsgFunc) {
	if p.store == nil {
		return
	}
	s, err := p.store.GetSettings(ctx)
	if err != nil {
		return
	}
	tok, _ := alertSenderCreds(s)
	if tok == "" {
		return
	}
	_ = sendToChats(ctx, tok, p.ownerChats(ctx, s, ownerID), sev, msg)
}

// sendToChats posts msg to each chat with the shared alert token, rendered in
// that recipient's interface language. A per-chat failure is logged but never
// aborts the rest of the fan-out.
func sendToChats(ctx context.Context, token string, chats []chatTarget, sev watchdog.Severity, msg watchdog.MsgFunc) error {
	for _, c := range chats {
		if err := (watchdog.TelegramAlerter{Token: token, ChatID: c.chatID}).Alert(ctx, sev, watchdog.Render(msg, c.lang)); err != nil {
			log.Printf("alert routing: deliver to %s: %v", c.chatID, err)
		}
	}
	return nil
}
