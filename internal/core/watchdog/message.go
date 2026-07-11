package watchdog

import (
	"fmt"
	"strings"
)

// Operator alert messages live here so every notification the fleet sends —
// node liveness, entry reachability, replication, bot-channel health, config
// health, billing, expiry, backups and the standby (β) watchdog — shares one
// voice: one emoji vocabulary, monospace code formatting for the technical bits
// (node names, commands, identifiers, errors), and localization that follows the
// reader's interface language.
//
// Language model: a builder returns a MsgFunc — a renderer that takes the
// recipient's UI language ("ru"/"en") and produces the final text. The Telegram
// leg renders it once per recipient in that account's Locale, so each operator
// reads alerts in their own interface language; the process log and event log
// render the canonical source language (DefaultLang). English is the source and
// fallback: an untranslated key degrades to English rather than to a blank.
//
// Formatting model: the rendered string is Telegram HTML. TelegramAlerter sends
// it with parse_mode=HTML; every value that comes from data (a name, an error, a
// command) is wrapped in Code/Pre or escaped, so a stray '<' (e.g. an error
// printed as "<nil>") can never break HTML parsing. Non-Telegram sinks run the
// text through PlainText first so they store a clean, tag-free line.
//
// Emoji vocabulary — every alert leads with exactly one:
//
//	❗️ down / failing / cannot serve (a critical, service-impacting condition)
//	⚠️ degraded — a non-fatal problem; service continues (HA at risk, due dates)
//	✅ recovered / healthy again / a positive confirmation (a test succeeded)
const (
	GlyphDown     = "❗️"
	GlyphDegraded = "⚠️"
	GlyphUp       = "✅"
)

// DefaultLang is the source/fallback language, used for the log and event-log
// legs and whenever a recipient's Locale is unknown.
const DefaultLang = "en"

// MsgFunc renders an alert in a given UI language. Builders return one so the
// Telegram fan-out can localize per recipient while the log/event legs render
// DefaultLang.
type MsgFunc func(lang string) string

// Render is a nil-safe helper: it renders m in lang, or "" if m is nil.
func Render(m MsgFunc, lang string) string {
	if m == nil {
		return ""
	}
	return m(lang)
}

// Code wraps s as inline monospace for the Telegram HTML parse mode, escaping it
// so names, commands and identifiers render as code and never break parsing.
func Code(s string) string { return "<code>" + escHTML(s) + "</code>" }

// Pre wraps a multi-line block (e.g. the break-glass promote command) as a
// monospace code block.
func Pre(s string) string { return "<pre>" + escHTML(s) + "</pre>" }

func escHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// esc escapes a dynamic value shown as plain (non-monospace) text.
func esc(s string) string { return escHTML(s) }

// PlainText strips the HTML an alert carries for Telegram, so the process log and
// the event log store a clean, tag-free line.
func PlainText(s string) string {
	s = strings.NewReplacer("<code>", "", "</code>", "", "<pre>", "", "</pre>", "").Replace(s)
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

// tr returns the format string for lang: the Russian translation when lang="ru"
// and a translation exists, else the English source (which doubles as the key).
func tr(lang, en string) string {
	if lang == "ru" {
		if v, ok := alertRU[en]; ok {
			return v
		}
	}
	return en
}

// alertRU maps each English alert format to its Russian translation. Keys must
// match the literals passed to tr exactly, including %-verbs and punctuation.
var alertRU = map[string]string{
	"%s Node %s (%s) is DOWN — agent unreachable: %s":                                                                                  "%s Узел %s (%s) НЕДОСТУПЕН — агент не отвечает: %s",
	"%s EXIT node %s is DOWN — agent unreachable: %s. Its groups lose egress until failover reassigns them or you do.":                 "%s ВЫХОДНОЙ узел %s НЕДОСТУПЕН — агент не отвечает: %s. Его группы теряют выход, пока их не переназначит отказоустойчивость или вы сами.",
	"%s Egress failover: exit %s is down — group(s) %s were moved to exit %s. Revert manually if you want them back once it recovers.": "%s Отказоустойчивость выхода: узел %s недоступен — группы %s перенесены на выход %s. Верните вручную, если захотите обратно после восстановления.",
	"%s Exit %s is down and no healthy exit is available — group(s) %s have no egress until one recovers or you reassign them.":        "%s Выход %s недоступен, и нет ни одного здорового выхода — группы %s остаются без выхода, пока один не восстановится или вы не переназначите их.",
	"%s Node %s (%s) is back — agent reachable again.":                                                                                 "%s Узел %s (%s) снова на связи — агент доступен.",
	"%s Entry node %s is unreachable from the internet on :443: %s":                                                                    "%s Входной узел %s недоступен из интернета по :443: %s",
	"%s Entry node %s is reachable again on :443.":                                                                                     "%s Входной узел %s снова доступен по :443.",
	"— HA at risk, the data plane is unaffected":                                                                                       "— HA под угрозой, на трафик клиентов это не влияет",
	"— HA at risk; this node is also the live exit for %s, so check its node health for data-plane impact":                             "— HA под угрозой; этот узел также обслуживает выход для %s, поэтому проверьте его здоровье на предмет влияния на трафик",
	"%s Replication to standby %s is degraded (%s) %s":                                                                                 "%s Репликация на резерв %s деградировала (%s) %s",
	"%s Replication to standby %s is healthy again.":                                                                                   "%s Репликация на резерв %s снова в норме.",
	"%s Telegram channel %s is %s (%s) — alert delivery degraded":                                                                      "%s Telegram-канал %s в состоянии %s (%s) — доставка оповещений деградировала",
	"%s Telegram channel %s recovered (%s)":                                                                                            "%s Telegram-канал %s восстановлен (%s)",
	"%s CONFIG: %d client(s) cannot get a working config — no healthy entry node in the fleet.":                                        "%s КОНФИГ: %d клиент(ов) не могут получить рабочий конфиг — в сети нет здорового входного узла.",
	"%s CONFIG: entry node health restored — clients can get configs again.":                                                           "%s КОНФИГ: здоровье входных узлов восстановлено — клиенты снова получают конфиги.",
	"overdue by %d day(s)": "просрочена на %d дн.",
	"due in %d day(s)":     "истекает через %d дн.",
	"%s VPS payment for node %s is %s (paid until %s).": "%s Оплата VPS для узла %s — %s (оплачено до %s).",
	"has EXPIRED":                     "истёк",
	"expires today":                   "истекает сегодня",
	"expires in %d day(s)":            "истекает через %d дн.",
	"%s Client config %s %s (%s).":    "%s Конфиг клиента %s %s (%s).",
	"%s TrustPanel backup FAILED: %s": "%s Резервное копирование TrustPanel НЕ УДАЛОСЬ: %s",
	"%s TrustPanel off-site backup delivery FAILED (local snapshot OK): %s":                                                                                                   "%s Off-site доставка резервной копии НЕ УДАЛАСЬ (локальный снимок в порядке): %s",
	"%s Primary %s failed %d consecutive health check(s) (last: %s).\n\nIf it is truly dead, run this AS ROOT on this standby (you are the fence against split-brain):\n\n%s": "%s Основной узел %s не прошёл %d проверок(и) здоровья подряд (последняя: %s).\n\nЕсли он действительно мёртв, выполните ОТ ROOT на этом резерве (вы — защита от split-brain):\n\n%s",
	"%s Primary %s is healthy again.": "%s Основной узел %s снова здоров.",
	"%s Node %s is alive but its alert system is silent — no Telegram heartbeat for %s. The control plane can no longer publish alerts (serve down or Telegram unreachable). Check the primary.": "%s Узел %s жив, но система оповещений молчит — нет Telegram-heartbeat уже %s. Контрольный узел больше не может публиковать оповещения (serve упал или Telegram недоступен). Проверьте основной узел.",
	"%s Node %s alert heartbeat is fresh again.":                                     "%s Heartbeat оповещений узла %s снова свежий.",
	"%s TrustPanel primary alert test — delivery works.":                             "%s Проверка основного канала оповещений TrustPanel — доставка работает.",
	"%s TrustPanel backup alert test — the standby's failover bot can reach you.":    "%s Проверка резервного канала оповещений TrustPanel — бот отказоустойчивости на резерве может до вас достучаться.",
	"%s TrustPanel backup channel test — the off-site delivery target is reachable.": "%s Проверка off-site канала бэкапов TrustPanel — точка доставки за пределами узла доступна.",
}

// --- node liveness (α, panel) ---

// MsgNodeDown reports a node whose agent stopped answering. name is the display
// name (never the raw id); role is the node's public role label.
func MsgNodeDown(name, role, err string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Node %s (%s) is DOWN — agent unreachable: %s"), GlyphDown, Code(name), esc(role), Code(err))
	}
}

// MsgExitDown reports a dead exit. It is broadcast to every alert chat, so it
// deliberately does NOT name the dependent groups (those are namespace-private):
// the per-namespace egress-failover alert names each owner's own groups to them.
func MsgExitDown(name, err string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s EXIT node %s is DOWN — agent unreachable: %s. Its groups lose egress until failover reassigns them or you do."),
			GlyphDown, Code(name), Code(err))
	}
}

// MsgEgressFailover reports that a dead exit's groups were auto-reassigned to a
// healthy one. It is delivered per-namespace (owner + admin), so groups names
// only the caller's own groups. No auto-revert happens: the operator moves them
// back manually if they want to once the old exit recovers.
func MsgEgressFailover(deadExit, targetExit, groups string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Egress failover: exit %s is down — group(s) %s were moved to exit %s. Revert manually if you want them back once it recovers."),
			GlyphDegraded, Code(deadExit), Code(groups), Code(targetExit))
	}
}

// MsgEgressBlackholed reports that a dead exit's groups could not be failed over
// because no healthy exit was available. Delivered per-namespace like MsgEgressFailover.
func MsgEgressBlackholed(deadExit, groups string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Exit %s is down and no healthy exit is available — group(s) %s have no egress until one recovers or you reassign them."),
			GlyphDown, Code(deadExit), Code(groups))
	}
}

// MsgNodeUp reports a node's agent reachable again (silent recovery).
func MsgNodeUp(name, role string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Node %s (%s) is back — agent reachable again."), GlyphUp, Code(name), esc(role))
	}
}

// --- entry edge reachability (α, panel) ---

func MsgEntryUnreachable(name, err string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Entry node %s is unreachable from the internet on :443: %s"), GlyphDown, Code(name), Code(err))
	}
}

func MsgEntryReachable(name string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Entry node %s is reachable again on :443."), GlyphUp, Code(name))
	}
}

// --- replication health (α, panel) ---

// MsgReplicationDegraded reports a lagging/broken standby slot. liveExitFor, when
// non-empty, names the groups this node is also the live exit for, so the alert
// does not falsely reassure that the data plane is unaffected.
func MsgReplicationDegraded(name, why, liveExitFor string) MsgFunc {
	return func(lang string) string {
		tail := tr(lang, "— HA at risk, the data plane is unaffected")
		if liveExitFor != "" {
			tail = fmt.Sprintf(tr(lang, "— HA at risk; this node is also the live exit for %s, so check its node health for data-plane impact"), Code(liveExitFor))
		}
		return fmt.Sprintf(tr(lang, "%s Replication to standby %s is degraded (%s) %s"), GlyphDegraded, Code(name), esc(why), tail)
	}
}

func MsgReplicationRestored(name string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Replication to standby %s is healthy again."), GlyphUp, Code(name))
	}
}

// --- bot-channel health (α, panel) ---

func MsgBotChannelDegraded(label, status, detail string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Telegram channel %s is %s (%s) — alert delivery degraded"), GlyphDegraded, esc(label), esc(status), Code(detail))
	}
}

func MsgBotChannelRestored(label, status string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Telegram channel %s recovered (%s)"), GlyphUp, esc(label), esc(status))
	}
}

// --- config health (α, panel) ---

func MsgConfigCannotBuild(clients int) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s CONFIG: %d client(s) cannot get a working config — no healthy entry node in the fleet."), GlyphDown, clients)
	}
}

func MsgConfigRestored() MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s CONFIG: entry node health restored — clients can get configs again."), GlyphUp)
	}
}

// --- billing / expiry (α, panel) ---

// MsgBilling reports a node's VPS payment due date. name is the display name (the
// raw node id is intentionally omitted). daysLeft < 0 means overdue.
func MsgBilling(name string, daysLeft int, until string) MsgFunc {
	return func(lang string) string {
		var phrase string
		if daysLeft < 0 {
			phrase = fmt.Sprintf(tr(lang, "overdue by %d day(s)"), -daysLeft)
		} else {
			phrase = fmt.Sprintf(tr(lang, "due in %d day(s)"), daysLeft)
		}
		return fmt.Sprintf(tr(lang, "%s VPS payment for node %s is %s (paid until %s)."), GlyphDegraded, Code(name), phrase, Code(until))
	}
}

// MsgConfigExpiry reports a client config nearing (or past) its expiry date.
func MsgConfigExpiry(user string, expired bool, daysLeft int, until string) MsgFunc {
	return func(lang string) string {
		var phrase string
		switch {
		case expired:
			phrase = tr(lang, "has EXPIRED")
		case daysLeft <= 0:
			phrase = tr(lang, "expires today")
		default:
			phrase = fmt.Sprintf(tr(lang, "expires in %d day(s)"), daysLeft)
		}
		return fmt.Sprintf(tr(lang, "%s Client config %s %s (%s)."), GlyphDegraded, Code(user), phrase, Code(until))
	}
}

// --- backups (backup subcommand) ---

func MsgBackupFailed(err string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel backup FAILED: %s"), GlyphDown, Code(err))
	}
}

func MsgBackupOffsiteFailed(err string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel off-site backup delivery FAILED (local snapshot OK): %s"), GlyphDegraded, Code(err))
	}
}

// --- standby watchdog (β) ---

// MsgWatchdogDown reports the primary failing its health checks, with the
// break-glass promote command the operator runs as root on the standby. target
// is the primary's node id (the β watchdog has no display name at hand).
func MsgWatchdogDown(target string, fails int, err, cmd string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Primary %s failed %d consecutive health check(s) (last: %s).\n\nIf it is truly dead, run this AS ROOT on this standby (you are the fence against split-brain):\n\n%s"),
			GlyphDown, Code(target), fails, Code(err), Pre(cmd))
	}
}

func MsgWatchdogUp(target string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Primary %s is healthy again."), GlyphUp, Code(target))
	}
}

// MsgDeadmanDown reports the primary alive but silent on its alert heartbeat.
func MsgDeadmanDown(target, age string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Node %s is alive but its alert system is silent — no Telegram heartbeat for %s. The control plane can no longer publish alerts (serve down or Telegram unreachable). Check the primary."),
			GlyphDown, Code(target), esc(age))
	}
}

func MsgDeadmanUp(target string) MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s Node %s alert heartbeat is fresh again."), GlyphUp, Code(target))
	}
}

// --- delivery tests (Settings) ---

func MsgTestPrimaryAlert() MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel primary alert test — delivery works."), GlyphUp)
	}
}

func MsgTestBackupAlert() MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel backup alert test — the standby's failover bot can reach you."), GlyphUp)
	}
}

func MsgTestBackupChannel() MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel backup channel test — the off-site delivery target is reachable."), GlyphUp)
	}
}
