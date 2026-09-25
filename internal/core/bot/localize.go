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
// to English and not to a blank.

type ctxKey int

const (
	langKey ctxKey = 0
	chatKey ctxKey = 1
)

// withChat stamps the chat an update came from, so a guided form can belong to
// the chat it was opened in.
func withChat(ctx context.Context, chatID int64) context.Context {
	return context.WithValue(ctx, chatKey, chatID)
}

// chatOf returns the chat of the update being handled, or 0 when there is none
// (the typed Dispatch path and the tests).
func chatOf(ctx context.Context) int64 {
	id, _ := ctx.Value(chatKey).(int64)
	return id
}

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
	"The category list could not be read":           "Список категорий не прочитан",
	"There is no category list yet":                 "Списка категорий пока нет",
	"The panel writes it on its next source check.": "Панель запишет его при ближайшей проверке источника.",
	"✓ Added": "✓ Добавлено",
	"That form is no longer open, so nothing was added.": "Эта форма уже закрыта — ничего не добавлено.",
	"🗂 Geosite catalogue":                                "🗂 Каталог Geosite",
	"📍 Country catalogue":                                "📍 Каталог стран",
	"EXCEPT the domains":                                 "КРОМЕ доменов",
	"EXCEPT the geosite categories":                      "КРОМЕ категорий geosite",
	"EXCEPT the countries":                               "КРОМЕ стран",
	"EXCEPT the IP ranges":                               "КРОМЕ диапазонов IP",
	"\n(read-only — a network-wide rule)":                "\n(только просмотр — общее правило сети)",
	" — switched off":                                    " — выключено",
	"catches:":                                           "ловит:",
	"\nreserved for the group: %s":                       "\nзакреплено за группой: %s",
	"\nsends it: %s":                                     "\nнаправляет: %s",
	"\nif that exit is out: %s":                          "\nесли этот выход недоступен: %s",
	"\nfor everyone else: %s":                            "\nдля остальных: %s",
	"\nlevel: %s":                                        "\nуровень: %s",
	"(nothing — this rule catches no traffic)":           "(ничего — правило не ловит трафик)",
	"domains":                        "домены",
	"geosite categories":             "категории geosite",
	"countries":                      "страны",
	"IP ranges":                      "диапазоны IP",
	"OR":                             "ИЛИ",
	"EXCEPT":                         "КРОМЕ",
	"AND":                            "И",
	"the whole network":              "вся сеть",
	"your own rules":                 "ваши правила",
	"through %s":                     "через %s",
	"out of the entry server itself": "с самого входного сервера",
	"refused":                        "отказ",
	"📷 QR code":                      "📷 QR-код",
	"📄 File .toml":                   "📄 Файл .toml",
	"%s — %s":                        "%s — %s",
	"\nentry server: %s":             "\nвходной сервер: %s",
	"\n\nLink to pass on — it opens the connection page:\n%s":                                                                         "\n\nСсылка для передачи — открывает страницу подключения:\n%s",
	"This connection may not work yet — tell an admin.":                                                                               "Подключение может пока не работать — сообщите администратору.",
	"entry is in maintenance; hand out a config for another entry until it returns":                                                   "входной сервер выведен из работы — выдайте подключение с другого входа, пока он не вернётся",
	"user is disabled or expired; this config will not authenticate until re-enabled":                                                 "клиент отключён или у него истёк срок — подключение не пройдёт проверку, пока доступ не вернут",
	"this client has no secret set — the deep link/QR are omitted and the config will not authenticate; set a password and re-export": "у клиента не задан пароль — ссылка и QR не выдаются, подключение не пройдёт проверку; задайте пароль и выгрузите заново",
	"👥 %s\nclients: %d\n%s":                                                 "👥 %s\nклиентов: %d\n%s",
	"👤 Clients in the group":                                                "👤 Клиенты группы",
	"🚪 Change the exit":                                                     "🚪 Изменить выход",
	"traffic leaves: from the entry server itself":                          "трафик уходит: с самого входного сервера",
	"traffic leaves: through %s":                                            "трафик уходит: через %s",
	" (in maintenance — until it returns, from the entry server)":           " (на обслуживании — пока не вернётся, трафик идёт с входного сервера)",
	" (in maintenance)":                                                     " (на обслуживании)",
	"👥 %s — no clients in it yet.":                                          "👥 %s — клиентов пока нет.",
	"👥 %s — clients (%d):":                                                  "👥 %s — клиенты (%d):",
	"🏠 From the entry server itself":                                        "🏠 С самого входного сервера",
	"🚪 Where should the traffic of %s leave by?\n%s":                        "🚪 Через что должен уходить трафик группы %s?\n%s",
	"from the entry server itself":                                          "с самого входного сервера",
	"🚪 Move the traffic of %s?\nnow: %s\nbecomes: %s\nclients affected: %d": "🚪 Перевести трафик группы %s?\nсейчас: %s\nстанет: %s\nзатронуто клиентов: %d",
	"\n\n⏸ That server is in maintenance: until it returns, this traffic leaves from the entry server.": "\n\n⏸ Этот сервер на обслуживании: пока он не вернётся, трафик будет уходить с входного сервера.",
	"✓ Move it": "✓ Перевести",
	"B":         "Б",
	"KiB":       "КиБ",
	"MiB":       "МиБ",
	"GiB":       "ГиБ",
	"TiB":       "ТиБ",
	"PiB":       "ПиБ",
	"EiB":       "ЭиБ",
	"Menu":      "Меню",
	"Groups":    "Группы",
	"Routes":    "Маршруты",
	"Account":   "Аккаунт",
	// --- the screens-and-texts pass: one word per state, one label per action ---
	"Access is live":       "Доступ действует",
	"Switched off":         "Отключён",
	"Access has expired":   "Срок доступа истёк",
	"Working":              "Работает",
	"Not answering":        "Не отвечает",
	"In maintenance":       "На обслуживании",
	"clients connect here": "сюда подключаются клиенты",
	"clients connect here, and their traffic leaves from here": "сюда подключаются клиенты, отсюда же уходит их трафик",
	"traffic leaves through it":                                "через него уходит трафик",
	"server":                                                   "сервер",
	", and holds the panel":                                    ", здесь же работает панель",
	"no answer from it yet":                                    "ответа от него ещё не было",
	"checked just now":                                         "проверен только что",
	"checked %d min ago":                                       "проверен %d мин назад",
	"checked %d h ago":                                         "проверен %d ч назад",
	"checked %s":                                               "проверен %s",
	"Clients":                                                  "Клиенты",
	"Servers":                                                  "Серверы",
	"Back":                                                     "Назад",
	"✓ Save":                                                   "✓ Сохранить",
	"⬅ Back":                                                   "⬅ Назад",
	"✓ Done":                                                   "✓ Готово",
	"⋯ More":                                                   "⋯ Ещё",
	"👤 Clients":                                                "👤 Клиенты",
	"🌐 Servers":                                                "🌐 Серверы",
	"👥 Change group":                                           "👥 Сменить группу",
	"🔗 Connection":                                             "🔗 Подключение",
	"📅 Access period":                                          "📅 Срок доступа",
	"🔎 Search":                                                 "🔎 Поиск",
	"🔎 Search again":                                           "🔎 Искать снова",
	"🔎 Send part of a client's name to search.": "🔎 Отправьте часть имени клиента для поиска.",
	"🛠 Maintenance":                           "🛠 Обслуживание",
	"🔧 Details":                               "🔧 Подробности",
	"➕ New client":                            "➕ Новый клиент",
	"➕ New rule":                              "➕ Новое правило",
	"🗑 Delete client":                         "🗑 Удалить клиента",
	"🗑 Delete group":                          "🗑 Удалить группу",
	"🗑 Delete rule":                           "🗑 Удалить правило",
	"⏸ Switch off":                            "⏸ Отключить",
	"▶️ Switch on":                            "▶️ Включить",
	"▶️ Return to service":                    "▶️ Вернуть в работу",
	"♾ No limit":                              "♾ Без ограничения",
	"no limit":                                "без ограничения",
	"until %s":                                "до %s",
	"\nlogin: %s":                             "\nлогин: %s",
	"\ngroup: %s":                             "\nгруппа: %s",
	"\nid: %s":                                "\nid: %s",
	"\ntraffic, all time: ↑%s ↓%s":            "\nтрафик за всё время: ↑%s ↓%s",
	"access period: no limit":                 "срок доступа: без ограничения",
	"access period: ended %s":                 "срок доступа: закончился %s",
	"access period: until %s (days left: %d)": "срок доступа: до %s (осталось дней: %d)",
	"access period: until %s — the last day is today": "срок доступа: до %s — сегодня последний день",
	"%s — %s\n%s\nPick the new date:":                 "%s — %s\n%s\nВыберите новую дату:",
	"%s → %s":                                         "%s → %s",
	"⏸ %s is switched off.\nnew access period: %s\nSwitch the client on as well?": "⏸ %s отключён.\nновый срок доступа: %s\nВключить клиента?",
	"▶️ Renew and switch on":                "▶️ Продлить и включить",
	"📅 Renew, leave it off":                 "📅 Продлить, не включая",
	"👥 Move %s to which group?":             "👥 В какую группу перевести %s?",
	"No clients yet.":                       "Клиентов пока нет.",
	"No clients.":                           "Клиентов нет.",
	"No servers.":                           "Серверов нет.",
	"No such client.":                       "Такого клиента нет.",
	"No such client: %s":                    "Такого клиента нет: %s",
	"No such server.":                       "Такого сервера нет.",
	"Clients (%d):\n":                       "Клиенты (%d):\n",
	"Servers:\n":                            "Серверы:\n",
	"Servers — tap to open":                 "Серверы — нажмите, чтобы открыть",
	"\n(read-only — admins manage servers)": "\n(только просмотр — серверами управляют администраторы)",
	"Control plane\nserving from: %s\nservers: %d (%d working, %d not answering)\nclients: %d · groups: %d":                               "Управляющий сервер\nобслуживает: %s\nсерверы: %d (работают %d, не отвечают %d)\nклиенты: %d · группы: %d",
	"serving from: %s\nservers: %d (%d working, %d not answering)\nyour clients: %d · your groups: %d":                                    "обслуживает: %s\nсерверы: %d (работают %d, не отвечают %d)\nваши клиенты: %d · ваши группы: %d",
	"➕ New client — step 1 of 3.\nSend the client's name, the way you will look for it later.":                                            "➕ Новый клиент — шаг 1 из 3.\nОтправьте имя клиента — такое, по которому вы будете его искать.",
	"➕ New client — step 2 of 3: group.\nname: %s\n":                                                                                      "➕ Новый клиент — шаг 2 из 3: группа.\nимя: %s\n",
	"Tap a group below, or reply with its name: %s":                                                                                       "Нажмите группу ниже или отправьте её название: %s",
	"Tap a group below, or reply with its name, or “-” for the default.\nGroups: %s":                                                      "Нажмите группу ниже или отправьте её название, либо «-» для группы по умолчанию.\nГруппы: %s",
	"➕ New client — step 3 of 3: access period.\nname: %s\ngroup: %s\nTap a preset, or reply with a number of days (e.g. 30) or “never”.": "➕ Новый клиент — шаг 3 из 3: срок доступа.\nимя: %s\nгруппа: %s\nНажмите вариант ниже, или отправьте число дней (например, 30), или «никогда».",
	"\nReply with days (e.g. 30) or “never”.":                                                                                             "\nОтправьте число дней (например, 30) или «никогда».",
	"➕ New client\nname: %s\nlogin: %s\ngroup: %s\naccess period: %s":                                                                     "➕ Новый клиент\nимя: %s\nлогин: %s\nгруппа: %s\nсрок доступа: %s",
	"✓ Create client": "✓ Создать клиента",
	"✏️ Change login": "✏️ Изменить логин",
	"✏️ Send the login this client connects under.\nnow: %s":                      "✏️ Отправьте логин, под которым клиент подключается.\nсейчас: %s",
	"That login is taken. Reply with another one.":                                "Такой логин уже занят. Отправьте другой.",
	"\nReply with another login, or tap ⬅ Back.":                                  "\nОтправьте другой логин или нажмите ⬅ Назад.",
	"\nSend a different name, or /cancel.":                                        "\nОтправьте другое имя или /cancel.",
	"That form expired. Tap ➕ New client to start over.":                          "Форма устарела. Нажмите ➕ Новый клиент, чтобы начать заново.",
	"✓ Created client %s in group %s. Open 👤 Clients to hand out its connection.": "✓ Клиент %s создан в группе %s. Откройте 👤 Клиенты, чтобы выдать подключение.",
	"✓ %s is now %s.": "✓ %s теперь %s.",
	"⚠️ Delete %s?\nThe access ends at once and the connection cannot be given back.": "⚠️ Удалить «%s»?\nДоступ прекратится сразу, вернуть подключение будет нельзя.",
	"⚠️ Delete group %s?":                                   "⚠️ Удалить группу «%s»?",
	"⚠️ Delete rule %s?":                                    "⚠️ Удалить правило «%s»?",
	"✓ Group %s created. Open /menu → Groups to manage it.": "✓ Группа %s создана. Откройте /menu → Группы для управления.",
	"✓ Group renamed to %s.":                                "✓ Группа переименована в %s.",
	"✓ Rule created.":                                       "✓ Правило создано.",
	"✓ Rule updated.":                                       "✓ Правило обновлено.",
	"✓ Create rule":                                         "✓ Создать правило",
	"\n\nNetwork-wide rules:":                               "\n\nОбщие правила сети:",
	"\n\nNetwork-wide rules (read-only):":                   "\n\nОбщие правила сети (только просмотр):",
	"🧭 What traffic should this rule catch?":                "🧭 Какие ресурсы направлять по этому правилу?",
	"🧭 New rule — who does it apply to?\n🚪 My rules — a rule of your own (most common)\n🌐 Whole network — applies to every group": "🧭 Новое правило — на кого оно распространяется?\n🚪 Мои правила — правило вашей области (обычный случай)\n🌐 Вся сеть — действует для всех групп",
	"🚪 My rules":      "🚪 Мои правила",
	"🌐 Whole network": "🌐 Вся сеть",
	"🔭 %s — clients %d · groups %d · rules %d": "🔭 %s — клиентов %d · групп %d · правил %d",

	// dispatch / generic
	// panel password recovery
	"🔑 Recover panel access": "🔑 Восстановить доступ к панели",
	"✅ Issue code":           "✅ Выдать код",
	"The panel is an admin-only surface — your account manages clients through this bot.": "Панель доступна только администраторам — ваш аккаунт управляет клиентами через этого бота.",
	"A code was just issued. Try again in %d seconds.":                                    "Код только что выдан. Повторите через %d сек.",
	"🔑 Recover panel access\n\nThis issues a one-time code for \"%s\", valid %d minutes. You enter it on the panel's sign-in screen to set a new password.\n\nAny code issued earlier stops working.":                                                                            "🔑 Восстановление доступа к панели\n\nБудет выдан одноразовый код для «%s», действителен %d мин. Введите его на экране входа в панель, чтобы задать новый пароль.\n\nРанее выданный код перестанет работать.",
	"🔑 Recovery code for \"%s\"\n\n%s\n\nValid %d minutes, single use. On the panel sign-in screen choose \"Forgot password\", then enter this code and your new password.\n\nIf you did not ask for this, someone has access to this chat — rotate the bot token in the panel.": "🔑 Код восстановления для «%s»\n\n%s\n\nДействителен %d мин, одноразовый. На экране входа в панель выберите «Забыли пароль», затем введите этот код и новый пароль.\n\nЕсли вы этого не запрашивали — к чату есть посторонний доступ, смените токен бота в панели.",

	"Cancelled.":                  "Отменено.",
	"Nothing to cancel.":          "Нечего отменять.",
	"Unknown command. Try /help.": "Неизвестная команда. Наберите /help.",
	"🔒 That command reveals credentials — message me privately (DM).":        "🔒 Эта команда показывает учётные данные — напишите мне в личку.",
	"🔒 This one only works privately — message the bot in a DM.":             "🔒 Это работает только в личном чате с ботом.",
	"Something went wrong. Try again, or open the panel.":                    "Что-то пошло не так. Повторите или откройте панель.",
	"This record's id is too long for a button here — open it in the panel.": "У этой записи слишком длинный id для кнопки — откройте её в панели.",
	"Unknown action. Tap ⬅ Menu.":                                            "Неизвестное действие. Нажмите ⬅ Меню.",

	// main menu + buttons
	"TrustPanel — main menu": "TrustPanel — главное меню",
	"📊 Traffic":              "📊 Трафик",

	// users menu
	"Clients (%d) — tap to open":             "Клиенты (%d) — нажмите, чтобы открыть",
	"Clients (%d) — page %d/%d, tap to open": "Клиенты (%d) — стр. %d/%d, нажмите, чтобы открыть",
	"⬅ Prev":               "⬅ Назад",
	"Next ➡":               "Далее ➡",
	"No clients match %q.": "Нет клиентов по запросу %q.",
	"Matches for %q:":      "Совпадения по %q:",
	"Matches for %q (first %d — narrow it down):": "Совпадения по %q (первые %d — уточните запрос):",
	"No such user.": "Нет такого клиента.",

	// user card / detail
	"never": "никогда",
	"yes":   "да",
	"no":    "нет",

	// config export
	"Usage: /config <name>": "Использование: /config <имя>",
	"No such user: %s":      "Нет такого клиента: %s",
	"No entry nodes in the network yet — add one in the panel first.":                                                "В сети пока нет входных серверов — сначала добавьте его в панели.",
	"⚠️ The network has no entry node right now, so there is nothing to connect to. An admin has to bring one back.": "⚠️ Сейчас в сети нет входного сервера, подключаться некуда. Вернуть его может только админ.",
	"📷 QR for %s — scan in TrustTunnel":                                                                              "📷 QR для %s — отсканируйте в TrustTunnel",
	"📄 %s":                                                                                                           "📄 %s",

	// node card / detail
	"\naddresses: %s": "\nадреса: %s",
	"\nagent: %s":     "\nагент: %s",
	"in rotation":     "в ротации",
	"⏸ Drained %q — traffic that targets it egresses locally and new configs warn until you resume.": "⏸ Сервер %q выведен — трафик к нему выходит локально, а новые конфиги предупреждают об этом до возврата.",
	"⏸ Drain %s?\n\nTraffic of these groups leaves by it: %s%s.\nWhere do they go instead?":          "⏸ Вывести %s?\n\nЧерез него выходит трафик групп: %s%s.\nКуда их перевести?",
	" and %d more": " и ещё %d",
	"\n\nRules naming this exit: %s. Each then follows what it declares.": "\n\nЭтот выход указан в правилах: %s. Каждое из них дальше делает то, что в нём записано.",
	"🏠 Out of the entry node":                                             "🏠 Напрямую с входного сервера",
	"⏸ %s is the exit for these groups: %s.\nDrain it from its card (/nodes) — you will be asked where they go.": "⏸ %s — выход для групп: %s.\nВыводите его с карточки (/nodes), там спросят, куда их перевести.",
	"▶️ Resumed %q — back in rotation.": "▶️ Сервер %q возвращён в ротацию.",
	"Usage: /%s <node id or name>":      "Использование: /%s <id или имя сервера>",
	"No such node: %s":                  "Нет такого сервера: %s",

	// status
	"(none)": "(нет)",

	// node / user lists

	// traffic
	"Top users by traffic:\n":  "Топ клиентов по трафику:\n",
	"No traffic recorded yet.": "Трафик пока не зафиксирован.",

	// user detail command
	"Usage: /user <name>": "Использование: /user <имя>",

	// enable / disable
	"Usage: /%s <name>": "Использование: /%s <имя>",
	"%s is already %s.": "%s уже %s.",
	"enabled":           "включён",
	"disabled":          "выключен",

	// add-user wizard
	"I didn't understand that.":                    "Не понял ответ.",
	"Usage: /adduser <name> [group]":               "Использование: /adduser <имя> [группа]",
	"User already exists: %s":                      "Клиент уже существует: %s",
	"\nTap a group below, or reply with its name.": "\nНажмите группу ниже или отправьте её название.",
	"30 days": "30 дней",
	"90 days": "90 дней",
	"1 year":  "1 год",

	// client card: extend presets + change group
	"⚠️ Access has ended. Extend it first — then the client can be switched on.": "⚠️ Срок доступа закончился. Сначала продлите его — потом клиента можно включить.",
	"+30 days":                           "+30 дней",
	"+90 days":                           "+90 дней",
	"+1 year":                            "+1 год",
	"No groups exist; create one first.": "Групп ещё нет; сначала создайте одну.",

	// group resolution
	"No such group: %s": "Нет такой группы: %s",
	"No groups exist; create one in the panel first.": "Групп ещё нет; создайте группу в панели.",
	"Multiple groups — specify one: %s":               "Несколько групп — укажите одну: %s",
	"(none yet)":                                      "(пока нет)",

	// namespace product: groups
	"👥 Groups":                               "👥 Группы",
	"⚙️ Account":                             "⚙️ Аккаунт",
	"🧭 Routes":                               "🧭 Маршруты",
	"🩺 Infra":                                "🩺 Инфра",
	"➕ New group":                            "➕ Новая группа",
	"Groups — tap to open":                   "Группы — нажмите, чтобы открыть",
	"No groups yet.":                         "Групп пока нет.",
	"No such group.":                         "Нет такой группы.",
	"not set":                                "не задан",
	"✏️ Rename":                              "✏️ Переименовать",
	"🗑 Delete":                               "🗑 Удалить",
	"➕ New group.\nSend a name.":             "➕ Новая группа.\nОтправьте название.",
	"Reply with a group name, or /cancel.":   "Отправьте название группы или /cancel.",
	"\nReply with another name, or /cancel.": "\nОтправьте другое название или /cancel.",
	"✏️ Renaming %q.\nSend the new name.":    "✏️ Переименование %q.\nОтправьте новое название.",
	"Reply with a new name, or /cancel.":     "Отправьте новое название или /cancel.",
	"🚫 Cannot delete %q: %d client(s) still use it. Move them first.": "🚫 Нельзя удалить %q: её используют %d клиент(ов). Сначала перенесите их.",
	"✖ Cancel": "✖ Отмена",

	// namespace product: routes
	"\n(no routes of your own yet)": "\n(своих маршрутов пока нет)",
	"No such route.":                "Нет такого маршрута.",
	"on":                            "вкл",
	"off":                           "выкл",
	"\norder: %d/%d (earlier wins)": "\nпорядок: %d/%d (раньше — приоритетнее)",
	"⛔ That route is read-only.":    "⛔ Этот маршрут только для чтения.",
	"🔒 This rule is edited in the panel.\n\nIt carries settings this bot has no screen for — a group it is reserved for, an exception, a reserve exit, or a separate answer for everyone else — and saving it from here would drop them. Turning it on or off, moving it and deleting it still work from the rule's card.": "🔒 Это правило правится в панели.\n\nВ нём есть настройки, для которых в боте нет экрана: группа, для которой оно зарезервировано, исключение, запасной выход или отдельный ответ для всех остальных. Сохранение отсюда их потеряет. Включить, выключить, переставить и удалить правило можно на его карточке.",
	"\n(edited in the panel — this rule uses settings the bot has no screen for)": "\n(правится в панели — у правила есть настройки, для которых в боте нет экрана)",
	"✏️ Edit": "✏️ Изменить",
	"⬆ Up":    "⬆ Выше",
	"⬇ Down":  "⬇ Ниже",
	"That form expired. Tap ➕ New route to start over.": "Форма устарела. Нажмите ➕ Новый маршрут, чтобы начать заново.",
	"✏️ Editing route %q.\n\n":                          "✏️ Изменение маршрута %q.\n\n",
	"🚪 Exit":                                            "🚪 Выход",
	"\nAdd one or more kinds below. Several kinds combine in one rule (domain AND geosite AND …).": "\nДобавьте один или несколько типов ниже. Несколько типов объединяются в одном правиле (домен И geosite И …).",
	" — so far:\n": " — пока что:\n",
	"➡ Continue":   "➡ Далее",
	"🌐 Domain":     "🌐 Домен",
	"🗂 Geosite":    "🗂 Geosite",
	"📍 Geo-IP":     "📍 Geo-IP",
	"🔢 CIDR":       "🔢 CIDR",
	"🗂 Geosite — type a category name, comma-separated for several.\ne.g. google, netflix, category-ads": "🗂 Geosite — введите название категории, через запятую для нескольких.\nнапример: google, netflix, category-ads",
	"📍 Geo-IP — type a country code, comma-separated for several.\ne.g. ru, us":                          "📍 Geo-IP — введите код страны, через запятую для нескольких.\nнапример: ru, us",
	"The source offers %d categories": "Категорий в источнике: %d",
	"The source offers %d countries":  "Стран в источнике: %d",
	", checked %s":                    ", проверено %s",
	"The panel has no list from the source yet, so a name typed here cannot be checked against it. It takes one by itself within minutes of starting.": "Списка из источника у панели пока нет, сверять введённое название не с чем. Она возьмёт его сама в первые минуты после запуска.",
	"The last check of the source failed, so the panel has no list to check a name against. The Geo databases tab says why.":                           "Последняя проверка источника не удалась, сверять введённое название панели не с чем. Причина — во вкладке «Гео-базы».",
	"The check since then did not get through, so the source may have moved on.":                                                                       "Более поздняя проверка не прошла, так что источник мог с тех пор измениться.",
	"⛔ Node management is available to admins only.":                                                                                                   "⛔ Управление серверами доступно только администраторам.",
	"⚠ Withdrawn from the source: %s": "⚠ Убрана из источника: %s",
	"The rule works on the copy already downloaded, and that copy will not be updated again.":                    "Правило работает на уже скачанной копии, и эта копия больше не обновится.",
	"⚠ Not in the source, and no copy is held: %s":                                                               "⚠ Нет в источнике, и копии нет: %s",
	"⚠ Not added — the source has no %s.":                                                                        "⚠ Не добавлено — в источнике нет: %s.",
	"Check the spelling. If the category is newer than the list, a check on the Geo databases tab refreshes it.": "Проверьте написание. Если категория новее списка, проверка во вкладке «Гео-базы» его обновит.",
	"A node whose rule names it receives no configuration updates at all.":                                       "Сервер, в правиле которого она стоит, вообще не получает обновлений конфигурации.",
	"While this rule is on, the nodes it applies to receive no configuration updates.":                           "Пока правило включено, серверы, которых оно касается, не получают обновлений конфигурации.",
	"🔢 Enter CIDR range(s), comma-separated for several.\ne.g. 10.0.0.0/8, 192.168.0.0/16":                       "🔢 Введите CIDR-диапазон(ы) через запятую.\nнапример: 10.0.0.0/8, 192.168.0.0/16",
	"🌐 Enter domain(s), comma-separated for several.\ne.g. netflix.com, *.google.com":                            "🌐 Введите домен(ы) через запятую.\nнапример: netflix.com, *.google.com",
	"added: %s": "добавлено: %s",
	"➡ Direct":  "➡ Напрямую",
	"🚫 Block":   "🚫 Блок",
	"Action?\nTap one, or reply `exit`, `direct` or `block`.":    "Действие?\nНажмите одно или отправьте `exit`, `direct` или `block`.",
	"Choose an exit.\nTap one, or reply with its number:\n":      "Выберите выход.\nНажмите один или отправьте его номер:\n",
	"Tap an action below, or reply `exit`, `direct` or `block`.": "Нажмите действие ниже или отправьте `exit`, `direct` или `block`.",
	"Tap an exit below, or reply with its number.\n":             "Нажмите выход ниже или отправьте его номер.\n",
	"🧭 Review the route:\nmatch:\n%s\nsends it: %s":              "🧭 Проверьте маршрут:\nловит:\n%s\nнаправляет: %s",
	"\n\nCreate this route?":                                     "\n\nСоздать маршрут?",
	"\n\nSave these changes?":                                    "\n\nСохранить изменения?",
	"(no exit nodes in the network yet — ask an admin)":          "(в сети пока нет выходных серверов — обратитесь к админу)",
	"direct": "напрямую",
	"block":  "блок",

	// namespace product: account
	"this chat": "этот чат",
	"⚙️ Account\nlanguage: %s\nalert chat: %s": "⚙️ Аккаунт\nязык: %s\nчат оповещений: %s",
	"🔔 Send alerts here":                       "🔔 Слать оповещения сюда",
	"🔕 Off":                                    "🔕 Выключить",

	// namespace product: operator infra aggregate
	"✅ ok":        "✅ ок",
	"⚠️ degraded": "⚠️ деградация",
	"🩺 Infra\nnodes: %d (healthy %d · problems %d)\nactive exit: %s": "🩺 Инфра\nсерверы: %d (здоровы %d · проблемы %d)\nактивный выход: %s",
	"🏠 %s · clients %d · groups %d\nInfra: %s (%d nodes)":            "🏠 %s · клиентов %d · групп %d\nИнфра: %s (серверов %d)",

	// client delete + lens
	"All scopes — tap to inspect (read-only)": "Все скоупы — нажмите для просмотра (только чтение)",
	"No clients in any scope yet.":            "Пока нет клиентов ни в одном скоупе.",

	// start / welcome
	"TrustPanel bot — use the buttons below.": "Бот TrustPanel — пользуйтесь кнопками ниже.",
}
