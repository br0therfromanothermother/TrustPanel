package watchdog

import (
	"fmt"
	"strings"
)

// Operator alert messages live here so every notification the fleet sends (node
// liveness, entry reachability, replication, bot-channel health, config health,
// billing, expiry, backups and the standby (β) watchdog) shares one
// voice: one emoji vocabulary, monospace code formatting for the technical bits
// (node names, commands, identifiers, errors), and localization that follows the
// reader's interface language.
//
// Language model: a builder returns a MsgFunc, a renderer that takes the
// recipient's UI language ("ru"/"en") and produces the final text. The Telegram
// leg renders it once per recipient in that account's Locale, leaving each operator
// reads alerts in their own interface language; the process log and event log
// render the canonical source language (DefaultLang). English is the source and
// fallback: an untranslated key degrades to English rather than to a blank.
//
// Formatting model: the rendered string is Telegram HTML. TelegramAlerter sends
// it with parse_mode=HTML; every value that comes from data (a name, an error, a
// command) is wrapped in Code/Pre or escaped. A stray '<' (e.g. an error
// printed as "<nil>") can never break HTML parsing. Non-Telegram sinks run the
// text through PlainText first so they store a clean, tag-free line.
//
// Emoji vocabulary, of which every alert leads with exactly one:
//
//	❗️ down / failing / cannot serve (a critical, service-impacting condition)
//	⚠️ degraded: a non-fatal problem, service continues (HA at risk, due dates)
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

// Link renders an anchor for the Telegram HTML parse mode. The URL is built by
// the panel (a t.me deep link), never by a message's own data.
func Link(text, href string) string {
	return "<a href=\"" + escHTML(href) + "\">" + escHTML(text) + "</a>"
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

// PlainText strips the HTML an alert carries for Telegram, leaving the process log and
// the event log store a clean, tag-free line.
func PlainText(s string) string {
	// An anchor becomes its text plus the address, which keeps both in a log line.
	for {
		i := strings.Index(s, "<a href=\"")
		if i < 0 {
			break
		}
		rest := s[i+len("<a href=\""):]
		j := strings.Index(rest, "\">")
		k := strings.Index(rest, "</a>")
		if j < 0 || k < 0 || k < j {
			break
		}
		s = s[:i] + rest[j+2:k] + " (" + rest[:j] + ")" + rest[k+len("</a>"):]
	}
	s = strings.NewReplacer(
		"<code>", "", "</code>", "",
		"<pre>", "", "</pre>", "",
		"<b>", "", "</b>", "", // the headline's emphasis, which a log line does not carry
	).Replace(s)
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
	"%s <b>The standby %s is behind</b>":                                       "%s <b>Резерв %s отстаёт</b>",
	"Replication to it is degraded: %s":                                        "Репликация на него деградировала: %s",
	"Clients are unaffected, but there is no up-to-date copy to fail over to.": "Клиентов это не затрагивает, но свежей копии для переключения сейчас нет.",
	"There is no up-to-date copy to fail over to, and this server is also the live exit for %s — check its own health for traffic impact.": "Свежей копии для переключения нет, и через этот сервер выходит трафик групп: %s — проверьте его состояние отдельно.",
	"%s <b>The standby %s is caught up</b>":                                                          "%s <b>Резерв %s догнал основной</b>",
	"There is an up-to-date copy to fail over to again.":                                             "Свежая копия для переключения снова есть.",
	"%s <b>Alerts may not reach you through %s</b>":                                                  "%s <b>Оповещения через %s могут не доходить</b>",
	"Telegram answers %s: %s":                                                                        "Telegram отвечает %s: %s",
	"This message got through; the next one may not. Check the token and the chat.":                  "Это сообщение дошло, следующее может не дойти. Проверьте токен и чат.",
	"%s <b>Alerts get through %s again</b>":                                                          "%s <b>Оповещения через %s снова доходят</b>",
	"Telegram answers %s.":                                                                           "Telegram отвечает %s.",
	"%s <b>The bill for %s is %s</b>":                                                                "%s <b>Оплата сервера %s %s</b>",
	"It is paid until %s.":                                                                           "Оплачено до %s.",
	"An unpaid server is switched off by the host, with whatever traffic goes through it.":           "Неоплаченный сервер хостер выключит вместе с трафиком, который через него идёт.",
	"%s <b>The panel's own management certificate has expired</b>":                                   "%s <b>Собственный управляющий сертификат панели истёк</b>",
	"%s <b>The panel's own management certificate expires on %s</b>":                                 "%s <b>Собственный управляющий сертификат панели истекает %s</b>",
	"%s <b>The panel's own management certificate was reissued</b>":                                  "%s <b>Собственный управляющий сертификат панели перевыпущен</b>",
	"%s <b>The root certificate behind every server has expired</b>":                                 "%s <b>Корневой сертификат, которым подписаны все остальные, истёк</b>",
	"%s <b>The root certificate behind every server expires on %s</b>":                               "%s <b>Корневой сертификат, которым подписаны все остальные, истекает %s</b>",
	"%s <b>The root certificate was reissued</b>":                                                    "%s <b>Корневой сертификат перевыпущен</b>",
	"%s <b>The management certificate of %s has expired</b>":                                         "%s <b>Управляющий сертификат сервера «%s» истёк</b>",
	"%s <b>The management certificate of %s expires on %s</b>":                                       "%s <b>Управляющий сертификат сервера «%s» истекает %s</b>",
	"%s <b>The management certificate of %s was reissued</b>":                                        "%s <b>Управляющий сертификат сервера «%s» перевыпущен</b>",
	"%d day(s) left, and nothing renews it by itself.":                                               "Осталось дней: %d, и сам он не продлевается.",
	"It was valid until %s.":                                                                         "Он действовал до %s.",
	"It is now valid until %s.":                                                                      "Теперь он действует до %s.",
	"Clients are unaffected, but the panel can no longer change anything on that server.":            "Клиентов это не затрагивает, но менять что-либо на этом сервере панель больше не может.",
	"Clients are unaffected, but the panel can no longer change anything on any server.":             "Клиентов это не затрагивает, но менять что-либо на серверах панель больше не может.",
	"%s <b>No backup was made</b>":                                                                   "%s <b>Резервная копия не создана</b>",
	"Until this is fixed the newest copy is the last one that worked — check the disk and the log.":  "Пока это не исправлено, самая свежая копия — та, что получилась в прошлый раз; проверьте место на диске и журнал.",
	"%s <b>The backup did not leave the server</b>":                                                  "%s <b>Копия не уехала с сервера</b>",
	"The local copy was made, so nothing is lost yet — but a copy on the same server is not a copy.": "Локальная копия создана, так что пока ничего не потеряно — но копия на том же сервере копией не считается.",
	"%s <b>The control-plane server %s is not answering</b>":                                         "%s <b>Управляющий сервер %s не отвечает</b>",
	"It failed %d health checks in a row; the last said: %s":                                         "Проверок здоровья подряд не прошло: %d, последняя сказала: %s",
	"The panel and the management bot are down with it. This message comes from the standby.":        "Панель и бот управления недоступны вместе с ним. Это сообщение прислал резерв.",
	"1. Check the server yourself — ping it, open the panel, try SSH. If it is alive, do nothing: switching over would leave two databases taking writes.": "1. Проверьте сервер сами — ping, панель, SSH. Если он жив, ничего не делайте: после переключения записи будут принимать две базы сразу.",
	"2. If it is really gone, run this on the standby as root:":                                              "2. Если он действительно недоступен, выполните на резерве от root:",
	"3. The panel comes back up on the standby. Point your connection at it.":                                "3. Панель поднимется на резерве. Направьте на него своё подключение.",
	"%s <b>Alerts from %s have stopped</b>":                                                                  "%s <b>Оповещения с %s прекратились</b>",
	"The server is alive, but nothing has come through Telegram for %s.":                                     "Сервер жив, но через Telegram ничего не приходило уже %s.",
	"Until it speaks again you will not be told about anything else — check the panel service and Telegram.": "Пока он молчит, об остальных событиях вы не узнаете — проверьте службу панели и Telegram.",
	"%s <b>The category %s is no longer published</b>":                                                       "%s <b>Категория %s больше не публикуется</b>",
	"Rules using it: %s.": "Её используют правила: %s.",
	"They keep working on the copy from %s, which will not be updated again.": "Они продолжают работать на копии от %s, и эта копия больше не обновится.",
	"%s <b>The category %s is gone, and no copy is held</b>":                  "%s <b>Категории %s нет, и копии у нас нет</b>",
	"Rules naming it: %s.": "На неё ссылаются правила: %s.",
	"The servers they apply to receive no configuration updates at all until those rules are fixed.": "Серверы, к которым они относятся, не получают обновлений конфигурации вообще, пока эти правила не исправят.",
	"Clients connect to it":                              "К нему подключаются клиенты",
	"Traffic leaves through it":                          "Через него уходит трафик",
	"It is part of the network":                          "Это сервер сети",
	"%s <b>%s is not answering</b>":                      "%s <b>%s не отвечает</b>",
	"%s, and the panel cannot reconfigure it: %s":        "%s, и панель не может его перенастроить: %s",
	"It keeps running the configuration it already has.": "Он продолжает работать с той конфигурацией, которую уже получил.",
	"%s <b>Traffic is not leaving through %s</b>":        "%s <b>Трафик не уходит через %s</b>",
	"The exit server stopped answering: %s":              "Выходной сервер перестал отвечать: %s",
	"The groups behind it are being moved to another exit; if there is none, their traffic leaves from the entry server.": "Группы за ним переводятся на другой выход; если свободного нет — их трафик пойдёт с входного сервера.",
	"%s <b>Groups moved to %s</b>": "%s <b>Группы переведены на %s</b>",
	"%s is not answering.":         "%s не отвечает.",
	"%s: %s → %s.":                 "%s: %s → %s.",
	"They stay on %s — nothing moves them back when the old exit returns.":                                       "Они останутся на %s — обратно их ничто не переведёт, когда прежний выход вернётся.",
	"%s <b>Traffic of %s leaves from the entry server</b>":                                                       "%s <b>Трафик групп %s уходит с входного сервера</b>",
	"%s is not answering, and there is no healthy exit to move them to.":                                         "%s не отвечает, а здорового выхода для перевода нет.",
	"Until an exit comes back, or you move them, these groups appear from the entry server's address.":           "Пока выход не вернётся или вы не переведёте их сами, эти группы видны с адреса входного сервера.",
	"%s <b>%s is answering again</b>":                                                                            "%s <b>%s снова отвечает</b>",
	"The panel can reconfigure it again.":                                                                        "Панель снова может его настраивать.",
	"Groups that were moved off it are still on their new exit — move them back yourself if you want them here.": "Группы, переведённые с него, остались на новом выходе — верните их вручную, если нужно.",
	"%s <b>Clients cannot connect through %s</b>":                                                                "%s <b>Клиенты не могут подключиться через %s</b>",
	"It does not answer on :443 from the internet: %s":                                                           "Он не отвечает на :443 из интернета: %s",
	"Everyone whose connection points at it is offline until it answers again.":                                  "Все, чьё подключение идёт через него, без связи, пока он не ответит.",
	"%s <b>%s answers on :443 again</b>":                                                                         "%s <b>%s снова отвечает на :443</b>",
	"Clients can connect through it again.":                                                                      "Клиенты снова могут через него подключаться.",
	"%s <b>No connection can be handed out right now</b>":                                                        "%s <b>Выдать подключение сейчас нельзя</b>",
	"The network has no healthy entry server, and %d client(s) need one.":                                        "В сети нет здорового входного сервера, а он нужен клиентам: %d.",
	"Connections already in use are unaffected; new ones have to wait for an entry server.":                      "Уже работающие подключения не затронуты; новые придётся подождать до возвращения входного сервера.",
	"%s <b>Connections can be handed out again</b>":                                                              "%s <b>Подключения снова можно выдавать</b>",
	"An entry server is healthy again.":                                                                          "Входной сервер снова здоров.",
	"%s <b>Access for “%s” ends in %d day(s)</b>":                                                                "%s <b>Доступ «%s» заканчивается через %d дн.</b>",
	"It runs until %s inclusive.":                                                                                "Действует до %s включительно.",
	"%s <b>Access for “%s” has ended</b>":                                                                        "%s <b>Доступ «%s» закончился</b>",
	"It ran until %s.":                                   "Действовал до %s.",
	"%s <b>Access for “%s” ends today</b>":               "%s <b>Доступ «%s» заканчивается сегодня</b>",
	"Today, %s, is the last day.":                        "Сегодня, %s, последний день.",
	"Group: %s.":                                         "Группа: %s.",
	"Open the client":                                    "Открыть карточку клиента",
	"overdue by %d day(s)":                               "просрочена на %d дн.",
	"due in %d day(s)":                                   "истекает через %d дн.",
	"%s Primary %s is healthy again.":                    "%s Основной сервер %s снова здоров.",
	"%s Node %s alert heartbeat is fresh again.":         "%s Heartbeat оповещений сервера %s снова свежий.",
	"%s TrustPanel primary alert test — delivery works.": "%s Проверка основного канала оповещений TrustPanel — доставка работает.",
	"directory":                                          "каталог",
	"your age key":                                       "ваш ключ age",
	"%.1f GiB":                                           "%.1f ГиБ",
	"%.1f MiB":                                           "%.1f МиБ",
	"%.1f KiB":                                           "%.1f КиБ",
	"%d bytes":                                           "%d Б",
	"%s · sha256 %s":                                     "%s · sha256 %s",
	"part %d of %d · %s · sha256 %s":                     "часть %d из %d · %s · sha256 %s",
	"%s <b>Backup copy delivered</b>":                    "%s <b>Резервная копия доставлена</b>",
	"%s, in one file.":                                   "%s, одним файлом.",
	"%s, in %d parts — download all of them.":            "%s, частями: %d — скачивать нужно все.",
	"Encrypted with age. The whole thing should hash to %s":                          "Зашифровано age. Хеш целого: %s",
	"To restore, put everything from here into one directory and run:":               "Для восстановления сложите всё отсюда в один каталог и выполните:",
	"%s TrustPanel management bot test — this is the bot reaching you.":              "%s Проверка основного бота TrustPanel — это он до вас дошёл.",
	"%s TrustPanel backup alert test — the standby's failover bot can reach you.":    "%s Проверка резервного канала оповещений TrustPanel — бот отказоустойчивости на резерве может до вас достучаться.",
	"%s TrustPanel backup channel test — the off-site delivery target is reachable.": "%s Проверка off-site канала бэкапов TrustPanel — точка доставки за пределами сервера доступна.",
}

// --- node liveness (α, panel) ---

// An alert is read in a hurry, usually on a phone, by someone deciding whether
// they have to do something right now. So each one is written in the same three
// movements: what happened, what it means for the people using the service, and
// what was done about it or what to do next. A single sentence carrying all
// three ("EXIT node X is DOWN, agent unreachable: dial tcp…, its groups lose
// egress until failover reassigns them or you do") makes the reader parse it
// before they can act.
//
// lines joins the movements; the first one is the headline.
func lines(parts ...string) string { return strings.Join(parts, "\n") }

// roleWords names what a server does for the network, in words rather than in
// the enum the database stores.
func roleWords(lang, role string) string {
	switch role {
	case "entry":
		return tr(lang, "Clients connect to it")
	case "exit":
		return tr(lang, "Traffic leaves through it")
	}
	return tr(lang, "It is part of the network")
}

// MsgNodeDown reports a node whose agent stopped answering. name is the display
// name (never the raw id); role is the node's public role label.
func MsgNodeDown(name, role, err string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>%s is not answering</b>"), GlyphDown, esc(name)),
			fmt.Sprintf(tr(lang, "%s, and the panel cannot reconfigure it: %s"), roleWords(lang, role), Code(err)),
			tr(lang, "It keeps running the configuration it already has."),
		)
	}
}

// MsgExitDown reports a dead exit. It is broadcast to every alert chat, and
// deliberately does not name the dependent groups (those are namespace-private):
// the per-namespace egress-failover alert names each owner's own groups to them.
func MsgExitDown(name, err string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Traffic is not leaving through %s</b>"), GlyphDown, esc(name)),
			fmt.Sprintf(tr(lang, "The exit server stopped answering: %s"), Code(err)),
			tr(lang, "The groups behind it are being moved to another exit; if there is none, their traffic leaves from the entry server."),
		)
	}
}

// MsgEgressFailover reports that a dead exit's groups were auto-reassigned to a
// healthy one. It is delivered per-namespace (owner + admin), where groups names
// only the caller's own groups. No auto-revert happens: the operator moves them
// back manually if they want to once the old exit recovers.
func MsgEgressFailover(deadExit, targetExit, groups string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Groups moved to %s</b>"), GlyphDegraded, esc(targetExit)),
			fmt.Sprintf(tr(lang, "%s is not answering."), esc(deadExit)),
			fmt.Sprintf(tr(lang, "%s: %s → %s."), Code(groups), esc(deadExit), esc(targetExit)),
			fmt.Sprintf(tr(lang, "They stay on %s — nothing moves them back when the old exit returns."), esc(targetExit)),
		)
	}
}

// MsgEgressBlackholed reports that a dead exit's groups could not be failed over
// because no healthy exit was available. Delivered per-namespace like MsgEgressFailover.
func MsgEgressBlackholed(deadExit, groups string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Traffic of %s leaves from the entry server</b>"), GlyphDown, Code(groups)),
			fmt.Sprintf(tr(lang, "%s is not answering, and there is no healthy exit to move them to."), esc(deadExit)),
			tr(lang, "Until an exit comes back, or you move them, these groups appear from the entry server's address."),
		)
	}
}

// MsgNodeUp reports a node's agent reachable again (silent recovery).
func MsgNodeUp(name, role string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>%s is answering again</b>"), GlyphUp, esc(name)),
			tr(lang, "The panel can reconfigure it again."),
		)
	}
}

// MsgExitUp is the recovery of an exit that groups were leaving by. Coming back
// is not the same event as their traffic coming back to it: nothing moves them
// back, and a recovery notice that let the reader assume otherwise would be read
// as "it is over" when it is not.
func MsgExitUp(name string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>%s is answering again</b>"), GlyphUp, esc(name)),
			tr(lang, "Groups that were moved off it are still on their new exit — move them back yourself if you want them here."),
		)
	}
}

// --- entry edge reachability (α, panel) ---

func MsgEntryUnreachable(name, err string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Clients cannot connect through %s</b>"), GlyphDown, esc(name)),
			fmt.Sprintf(tr(lang, "It does not answer on :443 from the internet: %s"), Code(err)),
			tr(lang, "Everyone whose connection points at it is offline until it answers again."),
		)
	}
}

func MsgEntryReachable(name string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>%s answers on :443 again</b>"), GlyphUp, esc(name)),
			tr(lang, "Clients can connect through it again."),
		)
	}
}

// --- replication health (α, panel) ---

// MsgReplicationDegraded reports a lagging/broken standby slot. liveExitFor, when
// non-empty, names the groups this node is also the live exit for, letting the alert
// does not falsely reassure that the data plane is unaffected.
func MsgReplicationDegraded(name, why, liveExitFor string) MsgFunc {
	return func(lang string) string {
		impact := tr(lang, "Clients are unaffected, but there is no up-to-date copy to fail over to.")
		if liveExitFor != "" {
			impact = fmt.Sprintf(tr(lang, "There is no up-to-date copy to fail over to, and this server is also the live exit for %s — check its own health for traffic impact."), esc(liveExitFor))
		}
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The standby %s is behind</b>"), GlyphDegraded, esc(name)),
			fmt.Sprintf(tr(lang, "Replication to it is degraded: %s"), esc(why)),
			impact,
		)
	}
}

func MsgReplicationRestored(name string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The standby %s is caught up</b>"), GlyphUp, esc(name)),
			tr(lang, "There is an up-to-date copy to fail over to again."),
		)
	}
}

// --- bot-channel health (α, panel) ---

func MsgBotChannelDegraded(label, status, detail string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Alerts may not reach you through %s</b>"), GlyphDegraded, esc(label)),
			fmt.Sprintf(tr(lang, "Telegram answers %s: %s"), esc(status), Code(detail)),
			tr(lang, "This message got through; the next one may not. Check the token and the chat."),
		)
	}
}

func MsgBotChannelRestored(label, status string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Alerts get through %s again</b>"), GlyphUp, esc(label)),
			fmt.Sprintf(tr(lang, "Telegram answers %s."), esc(status)),
		)
	}
}

// --- config health (α, panel) ---

func MsgConfigCannotBuild(clients int) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>No connection can be handed out right now</b>"), GlyphDown),
			fmt.Sprintf(tr(lang, "The network has no healthy entry server, and %d client(s) need one."), clients),
			tr(lang, "Connections already in use are unaffected; new ones have to wait for an entry server."),
		)
	}
}

func MsgConfigRestored() MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Connections can be handed out again</b>"), GlyphUp),
			tr(lang, "An entry server is healthy again."),
		)
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
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The bill for %s is %s</b>"), GlyphDegraded, esc(name), phrase),
			fmt.Sprintf(tr(lang, "It is paid until %s."), esc(until)),
			tr(lang, "An unpaid server is switched off by the host, with whatever traffic goes through it."),
		)
	}
}

// MsgConfigExpiry reports a client whose access is running out (or has). What
// runs out is the access, not a file: "the config expires" sent whoever read it
// looking for a config to re-issue.
func MsgConfigExpiry(client, group string, expired bool, daysLeft int, until string, link string) MsgFunc {
	return func(lang string) string {
		head := fmt.Sprintf(tr(lang, "%s <b>Access for “%s” ends in %d day(s)</b>"), GlyphDegraded, esc(client), daysLeft)
		body := fmt.Sprintf(tr(lang, "It runs until %s inclusive."), esc(until))
		switch {
		case expired:
			head = fmt.Sprintf(tr(lang, "%s <b>Access for “%s” has ended</b>"), GlyphDegraded, esc(client))
			body = fmt.Sprintf(tr(lang, "It ran until %s."), esc(until))
		case daysLeft <= 0:
			head = fmt.Sprintf(tr(lang, "%s <b>Access for “%s” ends today</b>"), GlyphDegraded, esc(client))
			body = fmt.Sprintf(tr(lang, "Today, %s, is the last day."), esc(until))
		}
		out := []string{head, body}
		if group != "" {
			out = append(out, fmt.Sprintf(tr(lang, "Group: %s."), esc(group)))
		}
		if link != "" {
			// Renewing is a decision about a date. The link opens the client's
			// card, where the new date is shown before it is set, rather than
			// applying anything from the notification itself.
			out = append(out, Link(tr(lang, "Open the client"), link))
		}
		return lines(out...)
	}
}

// --- management certificates (α, panel) ---

// CertSubject names whose management certificate an alert is about. Each one
// gets its own sentence rather than a noun dropped into a shared one: a name
// interpolated into "the certificate for %s" reads as a grammatical accident in
// every language that inflects, and these alerts are read in two.
type CertSubject int

const (
	CertOfServer CertSubject = iota // a server, named
	CertOfPanel                     // the panel's own client certificate
	CertOfRoot                      // the authority that signs them all
)

// MsgCertExpiry reports management mTLS material running out. These certificates
// are issued once, when a server is enrolled, and nothing renews them; when one
// lapses the panel and the server stop recognising each other, which is invisible
// from outside: clients keep connecting through a server the panel can no longer
// change. daysLeft < 0 means it has already lapsed.
func MsgCertExpiry(subject CertSubject, name string, daysLeft int, until string) MsgFunc {
	return func(lang string) string {
		expired := daysLeft < 0
		var head string
		switch {
		case subject == CertOfPanel && expired:
			head = fmt.Sprintf(tr(lang, "%s <b>The panel's own management certificate has expired</b>"), GlyphDown)
		case subject == CertOfPanel:
			head = fmt.Sprintf(tr(lang, "%s <b>The panel's own management certificate expires on %s</b>"), GlyphDegraded, esc(until))
		case subject == CertOfRoot && expired:
			head = fmt.Sprintf(tr(lang, "%s <b>The root certificate behind every server has expired</b>"), GlyphDown)
		case subject == CertOfRoot:
			head = fmt.Sprintf(tr(lang, "%s <b>The root certificate behind every server expires on %s</b>"), GlyphDegraded, esc(until))
		case expired:
			head = fmt.Sprintf(tr(lang, "%s <b>The management certificate of %s has expired</b>"), GlyphDown, esc(name))
		default:
			head = fmt.Sprintf(tr(lang, "%s <b>The management certificate of %s expires on %s</b>"), GlyphDegraded, esc(name), esc(until))
		}
		body := fmt.Sprintf(tr(lang, "%d day(s) left, and nothing renews it by itself."), daysLeft)
		if expired {
			body = fmt.Sprintf(tr(lang, "It was valid until %s."), esc(until))
		}
		tail := tr(lang, "Clients are unaffected, but the panel can no longer change anything on that server.")
		if subject != CertOfServer {
			tail = tr(lang, "Clients are unaffected, but the panel can no longer change anything on any server.")
		}
		return lines(head, body, tail)
	}
}

// MsgCertRenewed confirms the certificate was reissued and the warning is over.
func MsgCertRenewed(subject CertSubject, name, until string) MsgFunc {
	return func(lang string) string {
		var head string
		switch subject {
		case CertOfPanel:
			head = fmt.Sprintf(tr(lang, "%s <b>The panel's own management certificate was reissued</b>"), GlyphUp)
		case CertOfRoot:
			head = fmt.Sprintf(tr(lang, "%s <b>The root certificate was reissued</b>"), GlyphUp)
		default:
			head = fmt.Sprintf(tr(lang, "%s <b>The management certificate of %s was reissued</b>"), GlyphUp, esc(name))
		}
		return lines(head, fmt.Sprintf(tr(lang, "It is now valid until %s."), esc(until)))
	}
}

// --- backups (backup subcommand) ---

func MsgBackupFailed(err string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>No backup was made</b>"), GlyphDown),
			Code(err),
			tr(lang, "Until this is fixed the newest copy is the last one that worked — check the disk and the log."),
		)
	}
}

func MsgBackupOffsiteFailed(err string) MsgFunc {
	return func(lang string) string {
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The backup did not leave the server</b>"), GlyphDegraded),
			Code(err),
			tr(lang, "The local copy was made, so nothing is lost yet — but a copy on the same server is not a copy."),
		)
	}
}

// --- standby watchdog (β) ---

// MsgWatchdogDown reports the primary failing its health checks, with the
// break-glass promote command the operator runs as root on the standby. target
// is the primary's node id (the β watchdog has no display name at hand).
func MsgWatchdogDown(target string, fails int, err, cmd string) MsgFunc {
	return func(lang string) string {
		// This one is read when the panel and the management bot are gone, with the
		// standby talking. It carries the whole procedure rather than pointing
		// anywhere. The check before the command is the point of it: two
		// live databases are worse than an hour of downtime, and nothing here
		// switches over on its own.
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The control-plane server %s is not answering</b>"), GlyphDown, esc(target)),
			fmt.Sprintf(tr(lang, "It failed %d health checks in a row; the last said: %s"), fails, Code(err)),
			tr(lang, "The panel and the management bot are down with it. This message comes from the standby."),
			"",
			tr(lang, "1. Check the server yourself — ping it, open the panel, try SSH. If it is alive, do nothing: switching over would leave two databases taking writes."),
			tr(lang, "2. If it is really gone, run this on the standby as root:"),
			Pre(cmd),
			tr(lang, "3. The panel comes back up on the standby. Point your connection at it."),
		)
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
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Alerts from %s have stopped</b>"), GlyphDown, esc(target)),
			fmt.Sprintf(tr(lang, "The server is alive, but nothing has come through Telegram for %s."), esc(age)),
			tr(lang, "Until it speaks again you will not be told about anything else — check the panel service and Telegram."),
		)
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

// MsgTestManagementBot is the message the management bot sends to the operator who
// asked for the test. It goes to their own chat, because that is the only place
// this bot is ever supposed to deliver anything.
func MsgTestManagementBot() MsgFunc {
	return func(lang string) string {
		return fmt.Sprintf(tr(lang, "%s TrustPanel management bot test — this is the bot reaching you."), GlyphUp)
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

// --- off-site backup delivery (β, the backup job) ---

// humanSize renders a byte count the way a person reads one. Each unit is its
// own literal so the translation check can see it; a unit picked out of a slice
// would be invisible to it and the Russian would rot unnoticed.
func humanSize(lang string, n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf(tr(lang, "%.1f GiB"), float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf(tr(lang, "%.1f MiB"), float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf(tr(lang, "%.1f KiB"), float64(n)/(1<<10))
	default:
		return fmt.Sprintf(tr(lang, "%d bytes"), n)
	}
}

// MsgBackupPart is the caption under one uploaded piece of a snapshot. The
// checksum rides with the file it belongs to rather than in a list somewhere
// else: verifying a part means looking just above it.
func MsgBackupPart(name string, index, total int, size int64, sum string) MsgFunc {
	return func(lang string) string {
		if total <= 1 {
			return lines(Code(name), fmt.Sprintf(tr(lang, "%s · sha256 %s"), humanSize(lang, size), Code(sum)))
		}
		return lines(Code(name),
			fmt.Sprintf(tr(lang, "part %d of %d · %s · sha256 %s"), index, total, humanSize(lang, size), Code(sum)))
	}
}

// MsgBackupDelivered closes out a delivery. It is written to be read months
// later, in a chat nobody has opened since, by someone whose panel is gone, so
// it says what arrived, how big it is, what it should hash to, and the one
// command that turns it back into a database.
func MsgBackupDelivered(snapshot string, size int64, sum string, parts int) MsgFunc {
	return func(lang string) string {
		shape := fmt.Sprintf(tr(lang, "%s, in one file."), humanSize(lang, size))
		if parts > 1 {
			shape = fmt.Sprintf(tr(lang, "%s, in %d parts — download all of them."), humanSize(lang, size), parts)
		}
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>Backup copy delivered</b>"), GlyphUp),
			Code(snapshot),
			shape,
			"",
			fmt.Sprintf(tr(lang, "Encrypted with age. The whole thing should hash to %s"), Code(sum)),
			"",
			tr(lang, "To restore, put everything from here into one directory and run:"),
			Code(fmt.Sprintf("trustpanel restore --from-file <%s> --identity <%s>",
				tr(lang, "directory"), tr(lang, "your age key"))),
		)
	}
}

// --- geo databases (α, panel) ---

// MsgGeoCategoryGone reports a geo category the routing depends on that its
// source has stopped publishing.
//
// Two different pieces of news share one shape, because they are the same
// discovery with different consequences. With a copy on hand nothing breaks and
// nothing will: the rule is frozen at the version we hold, and a domain added to
// that category upstream will never be matched by it: worth knowing, not worth
// waking anyone. With no copy the rule cannot be compiled onto a node at all,
// and every configuration update to the nodes it applies to stops until someone
// edits it.
func MsgGeoCategoryGone(tag, rules, since string, haveCopy bool) MsgFunc {
	return func(lang string) string {
		if haveCopy {
			return lines(
				fmt.Sprintf(tr(lang, "%s <b>The category %s is no longer published</b>"), GlyphDegraded, esc(tag)),
				fmt.Sprintf(tr(lang, "Rules using it: %s."), esc(rules)),
				fmt.Sprintf(tr(lang, "They keep working on the copy from %s, which will not be updated again."), esc(since)),
			)
		}
		return lines(
			fmt.Sprintf(tr(lang, "%s <b>The category %s is gone, and no copy is held</b>"), GlyphDown, esc(tag)),
			fmt.Sprintf(tr(lang, "Rules naming it: %s."), esc(rules)),
			tr(lang, "The servers they apply to receive no configuration updates at all until those rules are fixed."),
		)
	}
}
