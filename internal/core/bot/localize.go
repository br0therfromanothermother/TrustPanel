package bot

import (
	"context"
	"fmt"
	"strings"
)

// Localization for the management bot. The language is resolved per Telegram
// update from the sender's language_code (Telegram-reported UI language) and
// carried request-scoped on the context, so command handlers translate via
// tr/trf without any signature changes. English is the source language and the
// fallback: a missing key returns the literal, so an untranslated string degrades
// to English rather than to a blank.

type ctxKey int

const langKey ctxKey = 0

// withLang stamps the resolved language onto the context for tr/trf to read.
func withLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, langKey, lang)
}

// langOf returns the request language ("ru" or "en"), defaulting to English
// (so direct unit-test calls with a bare context stay on the source strings).
func langOf(ctx context.Context) string {
	if l, ok := ctx.Value(langKey).(string); ok && l != "" {
		return l
	}
	return "en"
}

// normalizeLang maps a Telegram language_code to a supported UI language. Only
// Russian is translated; everything else falls back to English.
func normalizeLang(code string) string {
	if strings.HasPrefix(strings.ToLower(code), "ru") {
		return "ru"
	}
	return "en"
}

// updateLang extracts the sender's language from either an inline-button tap or a
// message.
func updateLang(u update) string {
	switch {
	case u.CallbackQuery != nil && u.CallbackQuery.From != nil:
		return normalizeLang(u.CallbackQuery.From.LanguageCode)
	case u.Message != nil && u.Message.From != nil:
		return normalizeLang(u.Message.From.LanguageCode)
	default:
		return "en"
	}
}

// translate looks up s for lang; an unknown language or key returns s unchanged.
func translate(lang, s string) string {
	if lang != "ru" {
		return s
	}
	if v, ok := ru[s]; ok {
		return v
	}
	return s
}

// tr translates a static string for the request language.
func tr(ctx context.Context, s string) string { return translate(langOf(ctx), s) }

// trf translates a format string, then applies Sprintf. RU format strings keep
// the same verbs and argument order as their English source.
func trf(ctx context.Context, format string, a ...any) string {
	return fmt.Sprintf(translate(langOf(ctx), format), a...)
}

// ru maps each English source string to its Russian translation. Keys must match
// the literals passed to tr/trf exactly (including punctuation and %-verbs).
var ru = map[string]string{
	// dispatch / generic
	"Cancelled.":                  "Отменено.",
	"Nothing to cancel.":          "Нечего отменять.",
	"Unknown command. Try /help.": "Неизвестная команда. Наберите /help.",
	"🔒 /config reveals connection credentials — message me privately (DM) to export it.": "🔒 /config показывает учётные данные подключения — напишите боту в личку, чтобы выгрузить его.",
	"🔒 Message the bot privately (DM) to export a config.":                               "🔒 Напишите боту в личку, чтобы выгрузить конфиг.",
	"Unknown action. Tap ⬅ Menu.":                                                        "Неизвестное действие. Нажмите ⬅ Меню.",

	// main menu + buttons
	"TrustPanel — main menu": "TrustPanel — главное меню",
	"👤 Users":                "👤 Клиенты",
	"📊 Traffic":              "📊 Трафик",
	"🌐 Nodes":                "🌐 Узлы",
	"⚙️ Status":              "⚙️ Статус",
	"➕ New user":             "➕ Новый клиент",
	"⬅ Menu":                 "⬅ Меню",
	"⬅ Users":                "⬅ Клиенты",
	"⬅ Nodes":                "⬅ Узлы",
	"⬅ Back":                 "⬅ Назад",
	"🟢 Enable":               "🟢 Включить",
	"⚪ Disable":              "⚪ Выключить",
	"⏳ Extend":               "⏳ Продлить",
	"📁 Group":                "📁 Группа",
	"📲 Config":               "📲 Конфиг",
	"▶️ Resume":              "▶️ Вернуть",
	"⏸ Drain":                "⏸ Вывести",

	// users menu
	"Clients (%d) — tap to manage":             "Клиенты (%d) — нажмите для управления",
	"Clients (%d) — page %d/%d, tap to manage": "Клиенты (%d) — стр. %d/%d, нажмите для управления",
	"‹ Prev":         "‹ Назад",
	"Next ›":         "Далее ›",
	"🔍 Search":       "🔍 Поиск",
	"🔍 Search again": "🔍 Искать ещё",
	"🔍 Send part of a client's name to search.":   "🔍 Отправьте часть имени клиента для поиска.",
	"No clients match %q.":                        "Нет клиентов по запросу %q.",
	"Matches for %q:":                             "Совпадения по %q:",
	"Matches for %q (first %d — narrow it down):": "Совпадения по %q (первые %d — уточните запрос):",
	"No users yet.":                               "Пока нет клиентов.",
	"No such user.":                               "Нет такого клиента.",
	"No such node.":                               "Нет такого узла.",
	"No users.":                                   "Нет клиентов.",
	"No nodes.":                                   "Нет узлов.",
	"Nodes — tap to manage":                       "Узлы — нажмите для управления",

	// user card / detail
	"%s %s\nusername: %s\nenabled: %s\ngroup: %s\nexpires: %s\ntraffic: ↑%s ↓%s (total %s)": "%s %s\nлогин: %s\nвключён: %s\nгруппа: %s\nистекает: %s\nтрафик: ↑%s ↓%s (всего %s)",
	"never": "никогда",
	"yes":   "да",
	"no":    "нет",

	// config export
	"Usage: /config <name>": "Использование: /config <имя>",
	"No such user: %s":      "Нет такого клиента: %s",
	"No entry nodes in the fleet yet — add one in the panel first.": "В сети пока нет входных узлов — сначала добавьте его в панели.",
	"📲 Config for %s — entry %s\n\n":                                "📲 Конфиг для %s — вход %s\n\n",
	"Deep link (open in TrustTunnel):\n%s\n\n":                      "Deep link (откройте в TrustTunnel):\n%s\n\n",
	"QR / share link:\n%s":                                          "QR / ссылка для передачи:\n%s",
	"📷 QR image":                                                    "📷 QR картинкой",
	"📄 TOML file":                                                   "📄 TOML-файл",
	"📷 QR for %s — scan in TrustTunnel":                             "📷 QR для %s — отсканируйте в TrustTunnel",
	"📄 %s":                                                          "📄 %s",

	// node card / detail
	"%s %s\nrole: %s\nhealth: %s\nstatus: %s":   "%s %s\nроль: %s\nздоровье: %s\nсостояние: %s",
	"\naddresses: %s":                           "\nадреса: %s",
	"\nagent: %s":                               "\nагент: %s",
	"\nlast seen: %s":                           "\nпоследний контакт: %s",
	"in rotation":                               "в ротации",
	"drained (maintenance)":                     "выведен (обслуживание)",
	"\n(read-only — another namespace)":         "\n(только чтение — чужой неймспейс)",
	"⛔ That node belongs to another namespace.": "⛔ Этот узел принадлежит другому неймспейсу.",
	"⏸ Drained %q — traffic that targets it egresses locally and new configs warn until you resume.": "⏸ Узел %q выведен — трафик к нему выходит локально, а новые конфиги предупреждают об этом до возврата.",
	"▶️ Resumed %q — back in rotation.":                                                              "▶️ Узел %q возвращён в ротацию.",
	"Usage: /%s <node id or name>":                                                                   "Использование: /%s <id или имя узла>",
	"No such node: %s":                                                                               "Нет такого узла: %s",

	// status
	"(none)": "(нет)",
	"active: %s\nnodes: %d (%d healthy, %d unhealthy)\nyour clients: %d · your groups: %d":    "активный: %s\nузлы: %d (%d здоровы, %d недоступны)\nваши клиенты: %d · ваши группы: %d",
	"Control plane\nactive: %s\nnodes: %d (%d healthy, %d unhealthy)\nusers: %d · groups: %d": "Контрольный узел\nактивный: %s\nузлы: %d (%d здоровы, %d недоступны)\nклиенты: %d · группы: %d",

	// node / user lists
	"Nodes:\n":      "Узлы:\n",
	"Users (%d):\n": "Клиенты (%d):\n",

	// traffic
	"Top users by traffic:\n":  "Топ клиентов по трафику:\n",
	"No traffic recorded yet.": "Трафик пока не зафиксирован.",

	// user detail command
	"Usage: /user <name>": "Использование: /user <имя>",

	// enable / disable
	"Usage: /%s <name>": "Использование: /%s <имя>",
	"%s is already %s.": "%s уже %s.",
	"✅ %s is now %s.":   "✅ %s теперь %s.",
	"enabled":           "включён",
	"disabled":          "выключен",

	// add-user wizard
	"➕ New user — step 1/3.\nSend a username.":                                               "➕ Новый клиент — шаг 1/3.\nОтправьте логин.",
	"\nReply with another username, or /cancel.":                                             "\nОтправьте другой логин или /cancel.",
	"That username already exists. Reply with another, or /cancel.":                          "Такой логин уже есть. Отправьте другой или /cancel.",
	"Step 2/3 — group.\nReply with one of these group names: %s":                             "Шаг 2/3 — группа.\nОтправьте одно из названий групп: %s",
	"Step 2/3 — group.\nReply with a group name, or '-' for the default.\nGroups: %s":        "Шаг 2/3 — группа.\nОтправьте название группы или '-' для группы по умолчанию.\nГруппы: %s",
	"\nReply with a group name, or /cancel.":                                                 "\nОтправьте название группы или /cancel.",
	"\nReply with days (e.g. 30) or 'never'.":                                                "\nОтправьте число дней (например, 30) или 'never'.",
	"I didn't understand that.":                                                              "Не понял ответ.",
	"no expiry":                                                                              "без срока",
	"expires %s":                                                                             "истекает %s",
	"✅ Created user %q (%s). Send /config %s to export their connection.":                    "✅ Клиент %q создан (%s). Отправьте /config %s, чтобы выгрузить подключение.",
	"✅ Created user %q in group %s. Send /config %s to export their connection.":             "✅ Клиент %q создан в группе %s. Отправьте /config %s, чтобы выгрузить подключение.",
	"Usage: /adduser <name> [group]":                                                         "Использование: /adduser <имя> [группа]",
	"User already exists: %s":                                                                "Клиент уже существует: %s",
	"Step 3/3 — expiry.\nTap a preset, or reply with a number of days (e.g. 30) or 'never'.": "Шаг 3/3 — срок.\nНажмите вариант ниже или отправьте число дней (например, 30) или 'never'.",
	"\nTap a group below, or reply with its name.":                                           "\nНажмите группу ниже или отправьте её название.",
	"That form expired. Tap ➕ New user to start over.":                                       "Форма устарела. Нажмите ➕ Новый клиент, чтобы начать заново.",
	"30 days": "30 дней",
	"90 days": "90 дней",
	"1 year":  "1 год",
	"♾ Never": "♾ Без срока",

	// client card: extend presets + change group
	"⏳ Extend %s\ncurrent expiry: %s\nPick how long to add:": "⏳ Продлить %s\nтекущий срок: %s\nНа сколько продлить:",
	"+30 days":                           "+30 дней",
	"+90 days":                           "+90 дней",
	"+1 year":                            "+1 год",
	"📁 Move %s to which group?":          "📁 В какую группу перенести %s?",
	"No groups exist; create one first.": "Групп ещё нет; сначала создайте одну.",

	// group resolution
	"No such group: %s": "Нет такой группы: %s",
	"No groups exist; create one in the panel first.": "Групп ещё нет; создайте группу в панели.",
	"Multiple groups — specify one: %s":               "Несколько групп — укажите одну: %s",
	"(none yet)":                                      "(пока нет)",

	// namespace product: groups
	"👥 Groups":               "👥 Группы",
	"⚙️ Account":             "⚙️ Аккаунт",
	"🧭 Routes":               "🧭 Маршруты",
	"🩺 Infra":                "🩺 Инфра",
	"➕ New group":            "➕ Новая группа",
	"Groups — tap to manage": "Группы — нажмите для управления",
	"No groups yet.":         "Групп пока нет.",
	"No such group.":         "Нет такой группы.",
	"not set":                "не задан",
	"✏️ Rename":              "✏️ Переименовать",
	"🎯 Default exit":         "🎯 Выход по умолчанию",
	"🗑 Delete":               "🗑 Удалить",
	"⬅ Groups":               "⬅ Группы",
	"👥 %s\ndefault exit: %s\nclients in group: %d":                    "👥 %s\nвыход по умолчанию: %s\nклиентов в группе: %d",
	"🎯 Default exit for %s":                                           "🎯 Выход по умолчанию для %s",
	"✖ Clear default":                                                 "✖ Сбросить",
	"➕ New group.\nSend a name.":                                      "➕ Новая группа.\nОтправьте название.",
	"Reply with a group name, or /cancel.":                            "Отправьте название группы или /cancel.",
	"✅ Group %q created. Open /menu → Groups to manage it.":           "✅ Группа %q создана. Откройте /menu → Группы для управления.",
	"\nReply with another name, or /cancel.":                          "\nОтправьте другое название или /cancel.",
	"✏️ Renaming %q.\nSend the new name.":                             "✏️ Переименование %q.\nОтправьте новое название.",
	"Reply with a new name, or /cancel.":                              "Отправьте новое название или /cancel.",
	"✅ Group renamed to %q.":                                          "✅ Группа переименована в %q.",
	"🚫 Cannot delete %q: %d client(s) still use it. Move them first.": "🚫 Нельзя удалить %q: её используют %d клиент(ов). Сначала перенесите их.",
	"⚠️ Delete group %q?":                                             "⚠️ Удалить группу %q?",
	"✅ Yes, delete":                                                   "✅ Да, удалить",
	"✖ Cancel":                                                        "✖ Отмена",

	// namespace product: routes
	"\n(no routes of your own yet)":   "\n(своих маршрутов пока нет)",
	"\n\nInfra baseline (read-only):": "\n\nИнфра-базис (только чтение):",
	"\n\nInfra baseline:":             "\n\nИнфра-базис:",
	"➕ New route":                     "➕ Новый маршрут",
	"No such route.":                  "Нет такого маршрута.",
	"⬅ Routes":                        "⬅ Маршруты",
	"on":                              "вкл",
	"off":                             "выкл",
	"🧭 %s\nmatch: %s\naction: %s\nstatus: %s": "🧭 %s\nсовпадение: %s\nдействие: %s\nстатус: %s",
	"\n(read-only — infra baseline)":          "\n(только чтение — инфра-базис)",
	"\ntier: %s":                              "\nуровень: %s",
	"\norder: %d/%d (earlier wins)":           "\nпорядок: %d/%d (раньше — приоритетнее)",
	"⛔ That route is read-only.":              "⛔ Этот маршрут только для чтения.",
	"⚠️ Delete route %q?":                     "⚠️ Удалить маршрут %q?",
	"✏️ Edit":                                 "✏️ Изменить",
	"⬆ Up":                                    "⬆ Выше",
	"⬇ Down":                                  "⬇ Ниже",
	"That form expired. Tap ➕ New route to start over.": "Форма устарела. Нажмите ➕ Новый маршрут, чтобы начать заново.",
	"✏️ Editing route %q.\n\n":                          "✏️ Изменение маршрута %q.\n\n",
	"🧭 New route — where should this rule apply?\n🚪 Exit — a routing rule of your own (most common)\n🌐 Network — applies to the whole fleet\n🛡 Guard — keeps matched traffic on the entry node": "🧭 Новый маршрут — где применять правило?\n🚪 Выход — ваше собственное правило маршрутизации (обычный выбор)\n🌐 Сеть — на всю сеть\n🛡 Guard — держит совпавший трафик на входном узле",
	"🚪 Exit":            "🚪 Выход",
	"🌐 Network":         "🌐 Сеть",
	"🛡 Guard":           "🛡 Guard",
	"🧭 Build the match": "🧭 Соберите совпадение",
	"\nAdd one or more kinds below. Several kinds combine in one rule (domain AND geosite AND …).": "\nДобавьте один или несколько типов ниже. Несколько типов объединяются в одном правиле (домен И geosite И …).",
	" — so far:\n": " — пока что:\n",
	"➡ Continue":   "➡ Далее",
	"🌐 Domain":     "🌐 Домен",
	"🗂 Geosite":    "🗂 Geosite",
	"📍 Geo-IP":     "📍 Geo-IP",
	"🔢 CIDR":       "🔢 CIDR",
	"🗂 Geosite — tap the common categories below, or type any category (comma-separated).\ne.g. google, netflix, category-ads": "🗂 Geosite — нажмите частые категории ниже или введите любую (через запятую).\nнапример: google, netflix, category-ads",
	"📍 Geo-IP — tap the common countries below, or type any ISO code (comma-separated).\ne.g. ru, us":                          "📍 Geo-IP — нажмите частые страны ниже или введите любой ISO-код (через запятую).\nнапример: ru, us",
	"🔢 Enter CIDR range(s), comma-separated for several.\ne.g. 10.0.0.0/8, 192.168.0.0/16":                                     "🔢 Введите CIDR-диапазон(ы) через запятую.\nнапример: 10.0.0.0/8, 192.168.0.0/16",
	"🌐 Enter domain(s), comma-separated for several.\ne.g. netflix.com, *.google.com":                                          "🌐 Введите домен(ы) через запятую.\nнапример: netflix.com, *.google.com",
	"added: %s": "добавлено: %s",
	"✅ Done":    "✅ Готово",
	"➡ Direct":  "➡ Напрямую",
	"🚫 Block":   "🚫 Блок",
	"Guard-tier rules can't route to an exit. Pick direct or block.": "Правила уровня guard не могут вести на выход. Выберите «напрямую» или «блок».",
	"Action?\nTap one, or reply `exit`, `direct` or `block`.":        "Действие?\nНажмите одно или отправьте `exit`, `direct` или `block`.",
	"Choose an exit.\nTap one, or reply with its number:\n":          "Выберите выход.\nНажмите один или отправьте его номер:\n",
	"Tap an action below, or reply `exit`, `direct` or `block`.":     "Нажмите действие ниже или отправьте `exit`, `direct` или `block`.",
	"Tap an exit below, or reply with its number.\n":                 "Нажмите выход ниже или отправьте его номер.\n",
	"🧭 Review the route:\nmatch:\n%s\naction: %s\ntier: %s":          "🧭 Проверьте маршрут:\nсовпадение:\n%s\nдействие: %s\nуровень: %s",
	"\n\nCreate this route?":                                         "\n\nСоздать маршрут?",
	"\n\nSave these changes?":                                        "\n\nСохранить изменения?",
	"✅ Create route":                                                 "✅ Создать маршрут",
	"✅ Save changes":                                                 "✅ Сохранить",
	"✅ Route created.":                                               "✅ Маршрут создан.",
	"✅ Route updated.":                                               "✅ Маршрут обновлён.",
	"(no exit nodes in the fleet yet — ask an admin)":                "(в сети пока нет выходных узлов — обратитесь к админу)",
	"exit %q": "выход %q",
	"direct":  "напрямую",
	"block":   "блок",

	// namespace product: account
	"this chat": "этот чат",
	"⚙️ Account\nlanguage: %s\nalert chat: %s": "⚙️ Аккаунт\nязык: %s\nчат оповещений: %s",
	"🔔 Send alerts here":                       "🔔 Слать оповещения сюда",
	"🔕 Off":                                    "🔕 Выключить",

	// namespace product: operator infra aggregate
	"✅ ok":        "✅ ок",
	"⚠️ degraded": "⚠️ деградация",
	"🩺 Infra\nnodes: %d (healthy %d · problems %d)\nactive exit: %s": "🩺 Инфра\nузлы: %d (здоровы %d · проблемы %d)\nактивный выход: %s",
	"🏠 %s · clients %d · groups %d\nInfra: %s (%d nodes)":            "🏠 %s · клиентов %d · групп %d\nИнфра: %s (узлов %d)",

	// client delete + lens
	"⚠️ Delete client %q?\nThis revokes its config for good.": "⚠️ Удалить клиента %q?\nЭто безвозвратно отзовёт его конфиг.",
	"All namespaces — tap to inspect (read-only)":             "Все неймспейсы — нажмите для просмотра (только чтение)",
	"No clients in any namespace yet.":                        "Пока нет клиентов ни в одном неймспейсе.",
	"🔭 %s — clients %d · groups %d · routes %d":               "🔭 %s — клиентов %d · групп %d · маршрутов %d",

	// start / welcome
	"TrustPanel bot — use the buttons below.": "Бот TrustPanel — пользуйтесь кнопками ниже.",
}
