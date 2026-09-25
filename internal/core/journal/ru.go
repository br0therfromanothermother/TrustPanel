package journal

// ru maps each English journal sentence to its Russian one. Keys must match the
// literals passed to Line exactly, including %-verbs and punctuation;
// TestEveryJournalLineIsTranslated reads the call sites and fails on a sentence
// that has no entry here.
var ru = map[string]string{
	// accounts
	"account %q created/updated":                                                 "аккаунт %q создан или изменён",
	"role of %q set to %s (scope %q)":                                            "роль %q изменена на %s (скоуп %q)",
	"Telegram binding updated for %q":                                            "привязка Telegram обновлена для %q",
	"account %q (%s, scope %q) deleted":                                          "аккаунт %q (%s, скоуп %q) удалён",
	"password changed for %q (other sessions revoked)":                           "пароль изменён для %q (остальные сеансы завершены)",
	"password reset for %q with a Telegram recovery code (all sessions revoked)": "пароль сброшен для %q по коду восстановления из Telegram (все сеансы завершены)",
	"cross-scope view enabled":                                                   "включён обзор всех скоупов",
	"panel recovery code issued for %q":                                          "выдан код восстановления доступа к панели для %q",
	"password reset for %q from the command line":                                "пароль сброшен для %q из командной строки",

	// geo databases
	"geo database %s now reads from %s":                                        "гео-база %s теперь читается из %s",
	"geo database %s could not be reached":                                     "гео-база %s недоступна",
	"no longer offered by the source: %s — routing keeps using the local copy": "источник больше не предлагает: %s — маршрутизация продолжает использовать локальную копию",
	"a newer version is available for %s":                                      "для %s доступна более новая версия",
	"geo database updated — %s: %s":                                            "гео-база обновлена — %s: %s",
	"geo database rolled back — %s: %s":                                        "гео-база возвращена к прежней копии — %s: %s",
	"geo databases that could not be updated: %s":                              "не удалось обновить гео-базы: %s",
	"geo databases set to follow their source could not be updated: %s":        "не удалось обновить гео-базы, которые следуют за источником: %s",
	"%s now follows its source automatically":                                  "%s теперь следует за источником автоматически",
	"%s no longer follows its source automatically":                            "%s больше не следует за источником автоматически",
	"%s, same size, different contents":                                        "%s, размер тот же, содержимое другое",

	// where a group's traffic leaves by
	"group %s (%s): egress %s → %s — %s":    "группа %s (%s): выход %s → %s — %s",
	"converted to a two-node deployment":    "переход на два сервера",
	"egress failover: %s stopped answering": "отказ выхода: %s перестал отвечать",
	"egress reassigned off %s":              "выход переназначен с %s",
	"maintenance of %s":                     "обслуживание %s",
	"changed by hand":                       "изменено вручную",
	"no exit (out of the entry node)":       "без выхода (с входного сервера)",
	"direct (out of the entry node)":        "напрямую (с входного сервера)",

	// egress failover
	"egress failover: exit %s down, drained — %s name it and route on without it":                               "отказ выхода: сервер %s недоступен, выведен из работы — на него ссылается правил: %s, дальше они идут без него",
	"egress failover: exit %s down, no healthy exit for %s — drained, their traffic leaves from the entry node": "отказ выхода: сервер %s недоступен, здорового выхода нет, групп затронуто: %s — сервер выведен из работы, их трафик идёт напрямую через входной сервер",
	"egress failover: auto-moved %s from %s to %s":                                                              "отказ выхода: групп переведено автоматически: %s — с %s на %s",
	"; %s name it and route on without it":                                                                      "; на него ссылается правил: %s, дальше они идут без него",

	// nodes, clients and everything the panel saves
	"auto-sync after a change failed: %s":     "автоматическая синхронизация после изменения не удалась: %s",
	"client %q switched off: access ended %s": "клиент %q выключен: доступ закончился %s",
	"reassigned egress of %s from %s to %s":   "выход переназначен с %[2]s на %[3]s, групп переведено: %[1]s",
	"drained node %s":                         "сервер %s выведен из работы",
	"resumed node %s":                         "сервер %s возвращён в работу",
	" — %s moved off it":                      " — групп переведено: %s",
	"bulk %s on %s":                           "массовое действие %s — клиентов: %s",
	"saved %s %s":                             "сохранено: %s %s",
	"deleted %s %s":                           "удалено: %s %s",

	// what the Telegram bot does. The marker is appended to every one of its
	// lines, so a change made from a phone is not mistaken for one typed into the
	// panel by the same person.
	" (via Telegram)":                           " (через Telegram)",
	"created client %s":                         "создан клиент %s",
	"deleted client %s":                         "удалён клиент %s",
	"client %s switched on":                     "клиент %s включён",
	"client %s switched off":                    "клиент %s выключен",
	"access for %s now ends %s":                 "доступ %s теперь до %s",
	"access for %s no longer expires":           "доступ %s стал бессрочным",
	", and the client was switched on":          ", клиент включён",
	"client %s moved to group %s":               "клиент %s переведён в группу %s",
	"created group %s":                          "создана группа %s",
	"group %s renamed to %q":                    "группа %s переименована в %q",
	"deleted group %s":                          "удалена группа %s",
	"exit of group %s set to %s":                "выход группы %s — %s",
	"group %s no longer has an exit of its own": "у группы %s больше нет своего выхода",
	"created rule %s":                           "создано правило %s",
	"updated rule %s":                           "изменено правило %s",
	"deleted rule %s":                           "удалено правило %s",
	"rule %s switched on":                       "правило %s включено",
	"rule %s switched off":                      "правило %s выключено",
	"rule %s now runs before %s":                "правило %s проверяется раньше, чем %s",
	"rule %s now runs after %s":                 "правило %s проверяется позже, чем %s",
	"alerts for %q now go to a Telegram chat":   "оповещения для %q теперь приходят в Telegram",
	"Telegram alerts for %q switched off":       "оповещения в Telegram для %q отключены",

	// the route tester and the renderer's compile-time warnings: whole sentences
	// with node and rule names inside them, which is why they are built here and
	// not assembled from words in the browser
	"entry node (out directly)":     "входной сервер (напрямую)",
	"blocked (connection rejected)": "заблокировано (соединение отклонено)",
	"exit node %s":                  "выходной сервер %s",

	"no conditions set — everything matches":             "условий нет — подходит всё",
	"%s (everyone outside %s)":                           "%s (для всех, кроме группы %s)",
	"%s (everyone else)":                                 "%s (для всех остальных)",
	"matched rule %s (%s)":                               "подошло правило %s (%s)",
	"sent out through exit %s":                           "уходит через выход %s",
	"the group's own exit":                               "выход группы по умолчанию",
	"the group's own exit (none set)":                    "выход группы по умолчанию (не задан)",
	"the group has no exit of its own":                   "у группы нет своего выхода",
	"no rule matched — the group leaves by its own exit": "ни одно правило не подошло — трафик уходит через выход группы по умолчанию",
	"no rule matched and the group has no exit of its own → it leaves directly from the entry node": "ни одно правило не подошло, а выход по умолчанию у группы не задан → трафик уходит напрямую через входной сервер",

	"geoip:%s ✓ (%s is in %s)":                       "geoip:%s ✓ (%s относится к %s)",
	"geoip: ✗ (the target did not resolve to an IP)": "geoip: ✗ (адрес не разрешился в IP)",
	"geoip: ✗ no country matched":                    "geoip: ✗ ни одна страна не подошла",
	"geosite: ✗ no category matched":                 "geosite: ✗ ни одна категория не подошла",
	"country/category lists: ✗ not available here":   "списки стран и категорий: ✗ здесь их нет",
	"rule %s: the country and category lists are not on this server, so they could not be checked — read here as no match; the node itself may decide otherwise": "правило %s: списков стран и категорий на этом сервере нет, проверить их не удалось — здесь считаем, что не подошло; сам сервер может решить иначе",
	"could not resolve %s: %s — conditions on countries and subnets cannot be checked":                                                                           "не удалось разрешить %s: %s — условия по странам и подсетям проверить нельзя",

	"exit %s is out for maintenance → its traffic leaves directly from the entry node until it returns":           "выход %s на обслуживании → его трафик уходит напрямую через входной сервер, пока сервер не вернётся",
	"exit %s is out of rotation; traffic that targets it leaves directly from the entry node until it returns":    "выход %s выведен из работы; трафик, который шёл через него, уходит напрямую через входной сервер, пока сервер не вернётся",
	"exit %s is out of rotation → rule %s falls back to exit %s":                                                  "выход %s выведен из работы → правило %s переходит на запасной выход %s",
	"exit %s is out of rotation; rule %s falls back to exit %s":                                                   "выход %s выведен из работы; правило %s переходит на запасной выход %s",
	"exit %s is out of rotation → rule %s refuses its traffic":                                                    "выход %s выведен из работы → правило %s отклоняет свой трафик",
	"exit %s is out of rotation; rule %s refuses its traffic until it returns":                                    "выход %s выведен из работы; правило %s отклоняет свой трафик, пока сервер не вернётся",
	"exit %s and its fallback %s are both out of rotation → rule %s refuses its traffic":                          "выход %s и его запасной %s оба выведены из работы → правило %s отклоняет свой трафик",
	"exit %s and its fallback %s are both out of rotation; rule %s refuses its traffic until one of them returns": "выход %s и его запасной %s оба выведены из работы; правило %s отклоняет свой трафик, пока не вернётся хотя бы один",

	"entry node %s has no connection domain set; using %s":                              "у входного сервера %s не задан домен подключения; используется %s",
	"entry node %s has no domain assigned; a placeholder hostname went into its config": "входному серверу %s не назначен ни один домен; в конфигурацию записан временный адрес",
}

// terms translates the single words that arrive as data: the role an account was
// given, the kind of record that was saved, the bulk action that was run. A word
// with no entry is left as it came, which is the right answer for an id.
var terms = map[string]string{
	"infra":    "инфраструктура",
	"admin":    "администратор",
	"operator": "оператор",

	"node":         "сервер",
	"user":         "клиент",
	"group":        "группа",
	"route policy": "правило маршрутизации",
	"domain":       "домен",
	"item":         "запись",

	// what a rule does with what it caught, and the kinds of condition it tests
	"exit":    "выход",
	"direct":  "напрямую",
	"block":   "блокировать",
	"domains": "домены",
	"cidr":    "подсети",
	"geoip":   "страны",
	"geosite": "категории",

	// the words that join one condition to the next, as the trace reads them
	"AND":    "И",
	"OR":     "ИЛИ",
	"EXCEPT": "КРОМЕ",

	"enable":       "включить",
	"disable":      "выключить",
	"delete":       "удалить",
	"set_group":    "сменить группу",
	"set_expiry":   "изменить срок доступа",
	"clear_expiry": "снять срок доступа",
}
