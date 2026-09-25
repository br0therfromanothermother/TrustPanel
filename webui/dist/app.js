/* TrustPanel operator UI: React (via htm, no build step). */
const { useState, useEffect, useCallback, useRef } = React;
const html = htm.bind(React.createElement);

class ErrorBoundary extends React.Component {
  constructor(p){ super(p); this.state = { err: null }; }
  static getDerivedStateFromError(e){ return { err: e }; }
  render(){ return this.state.err
    ? html`<pre class=err style=${{ padding: "1rem", whiteSpace: "pre-wrap" }}>${String(this.state.err && this.state.err.stack || this.state.err)}</pre>`
    : this.props.children; }
}

/* ---------------- API ---------------- */
// Per-session CSRF token, set from /api/session and login. Sent on
// state-changing requests (the server requires it on sensitive endpoints).
let CSRF_TOKEN = "";
function setCsrfToken(t) { CSRF_TOKEN = t || ""; }
async function api(method, path, body) {
  const headers = body ? { "Content-Type": "application/json" } : {};
  if (CSRF_TOKEN && method !== "GET" && method !== "HEAD") headers["X-CSRF-Token"] = CSRF_TOKEN;
  const r = await fetch(path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  const txt = await r.text();
  let data; try { data = txt ? JSON.parse(txt) : {}; } catch { data = { raw: txt }; }
  if (!r.ok) throw new Error(data.error || ("HTTP " + r.status));
  return data;
}

/* ---------------- i18n (en / ru) ---------------- */
// Translation is keyed by the English source string: t("Save"). Missing keys
// fall back to English, so an untranslated string degrades gracefully rather
// than showing a blank. The choice persists in localStorage and is switchable
// from the top bar; the whole app re-renders on change (App holds `lang`).
function detectLang() {
  try {
    const saved = localStorage.getItem("tp_lang");
    if (saved === "ru" || saved === "en") return saved;
    return (navigator.language || "").toLowerCase().startsWith("ru") ? "ru" : "en";
  } catch { return "en"; }
}
let LANG = detectLang();
// The page has to say which language it is in: a screen reader pronounces it,
// and hyphenation and quotation follow it. It said "en" whatever was on screen.
// (The date field's own format is the browser's choice, not the document's.)
function setLangGlobal(l) {
  LANG = l;
  try { document.documentElement.lang = l; } catch {}
  try { localStorage.setItem("tp_lang", l); } catch {}
}
try { document.documentElement.lang = LANG; } catch {}
function t(s) { if (LANG === "en") return s; const d = RU[s]; return d == null ? s : d; }
// tf translates a template with {name} placeholders, then fills them in, so a
// localized sentence can keep its own word order around interpolated values.
function tf(s, vars) { let out = t(s); for (const k in vars) out = out.split("{" + k + "}").join(vars[k]); return out; }
// ph reads a sentence the server built in both languages. The route tester and
// the renderer speak in whole sentences with node and rule names inside them,
// which is exactly what a per-string dictionary here cannot reach, so they send
// {text, ru} and this picks the one being read.
function ph(p) {
  if (p == null) return "";
  if (typeof p === "string") return p; // a plain string from an older panel
  return (LANG === "ru" && p.ru) || p.text || "";
}

// PasswordInput is a text input with a reveal toggle. Defaults to masked; the eye
// button flips it to plain text so the typist can check what they entered.
function PasswordInput({ value, onChange, placeholder = "", autocomplete, inputId, describedBy, invalid, required }) {
  const [show, setShow] = useState(false);
  return html`<div class=pw>
    <input type=${show ? "text" : "password"} value=${value} placeholder=${placeholder}
      id=${inputId || undefined} aria-describedby=${describedBy || undefined} aria-invalid=${invalid ? "true" : undefined} aria-required=${required || undefined}
      autocomplete=${autocomplete} onInput=${e => onChange(e.target.value)} />
    <button type=button class=reveal tabindex=-1 onClick=${() => setShow(!show)}
      aria-label=${show ? t("Hide") : t("Show")}>${show ? t("Hide") : t("Show")}</button>
  </div>`;
}
const RU = {
  // ---- nav / topbar ----
  "Overview": "Обзор", "HA": "Отказоустойчивость", "Clients": "Клиенты",
  "Groups": "Группы", "Users": "Пользователи", "Routing": "Маршрутизация",
  "Traffic": "Трафик", "Settings": "Настройки", "Logs": "Журнал",
  "Sync now": "Синхронизировать",
  "Syncing…": "Синхронизация…",
  "Rebuild config from the database and push it to every node now": "Пересобрать конфигурацию из базы и разослать её на все серверы",
  "Background sync cycles since the panel started": "Циклов фоновой синхронизации с запуска панели",
  "Graph": "График", "Allow connections": "Разрешить подключение",
  "Entry points": "Точки входа", "Exit points": "Точки выхода",
  "Summary": "Сводка","Recent events": "События",
  "Full log": "Весь журнал", "this month": "за месяц", "of {n} GiB": "из {n} GiB",
  "synced {n}s ago": "синхронизация {n} с назад", "{n} need attention": "{n} требуют внимания",
  "s": "с", "m": "мин", "h": "ч",
  "On": "Вкл", "user is disabled or expired; this config will not authenticate until re-enabled": "клиент выключен или истёк — конфиг не пройдёт проверку, пока доступ не вернут",
  "entry is in maintenance; hand out a config for another entry until it returns": "входной сервер выведен из работы — выдайте конфиг с другого входа, пока он не вернётся",
  "this client has no secret set — the deep link/QR are omitted and the config will not authenticate; set a password and re-export": "у клиента не задан пароль — ссылка и QR не выдаются, конфиг не пройдёт проверку; задайте пароль и выгрузите заново",
  "All kinds": "Все виды", "All levels": "Все уровни", "Reset": "Сбросить", "Filter by text or kind": "Фильтр по тексту или виду", "Filter the log": "Фильтр журнала", "Link & QR": "Ссылка и QR", "Undo edits": "Вернуть как было", "My clients": "Мои клиенты", "Clients of other scopes": "Чужие клиенты", "online": "онлайн", "{clients} online through this node right now — draining it cuts them off.": "Сейчас через сервер работает клиентов: {clients} — вывод их отключит.", "Warnings": "Предупреждения", "Errors": "Ошибки", "telemetry {age} ago": "телеметрия {age} назад", "{clients} in {groups}": "клиентов: {clients}, групп: {groups}","{entries} shown": "показано {entries}", "1h": "1 ч", "6h": "6 ч", "24h": "24 ч", "7d": "7 д", "30d": "30 д", "↑ from clients": "↑ от клиентов", "↓ to clients": "↓ клиентам", "Nothing here yet": "Здесь пока пусто",
  "A client is one person's access: a login, the group whose routing it follows, and an optional expiry date.": "Клиент — это доступ одного человека: логин, группа с её маршрутизацией и необязательный срок действия.",
  "A server is either an entry that clients connect to or an exit their traffic leaves from.": "Сервер бывает входным, к которому подключаются клиенты, или выходным, через который уходит их трафик.",
  "A group is a set of clients that share one route: where their traffic leaves by, and which rules apply to it.": "Группа — это набор клиентов с общим маршрутом: через какой выход уходит их трафик и какие правила к нему применяются.",
  "A domain is a name a server answers for, with the certificate that proves it.": "Домен — это имя, на которое отвечает сервер, вместе с сертификатом, который это подтверждает.",
  "A rule decides where matching traffic goes: out of a chosen exit, out of the entry node it is already on, or nowhere at all.": "Правило решает, куда идёт подошедший трафик: через выбранный выход, с того же входного сервера или никуда.", "Figures are not refreshing": "Показатели не обновляются",
  "{what} last updated {m} min ago, so what you see below may be out of date.": "{what}: последнее обновление {m} мин назад, данные ниже могут быть устаревшими.",
  "Try again": "Повторить", "Panel health": "Состояние панели", "Traffic counters": "Счётчики трафика",
  "{what} stopped updating, so what you see below may be out of date.": "{what}: обновление прекратилось, данные ниже могут быть устаревшими.", "Your network, your rules.": "Своя сеть — свои правила.", "auto-syncs": "синхронизаций", "Log out": "Выйти", "Sign in": "Вход", "Log in": "Войти",
  // ---- cross-namespace lens (buried bootstrap toggle) ----
  "Advanced": "Расширенное",
  "Cross-scope view": "Обзор всех скоупов",
  "Cross-scope view on": "Обзор всех скоупов включён",
  "Cross-scope view off": "Обзор всех скоупов выключен",
  "On — you are seeing every scope.": "Включено — вы видите все скоупы.",
  "Off — you see only your own scope.": "Выключено — вы видите только свой скоуп.",
  "Turn on": "Включить", "Turn off": "Выключить",
  "Show every scope?": "Показать все скоупы?",
  "Shows other tenants' clients, groups and routes across the panel. Turns off when you sign out.": "Показывает клиентов, группы и маршруты других владельцев во всей панели. Выключается при выходе из панели.",
  "Show all scopes": "Показать все скоупы",
  "Viewing every scope, not just your own": "Виден каждый скоуп, а не только свой",
  "Return to my own": "Вернуться к своему",
  "Username": "Имя пользователя", "Password": "Пароль", "Loading…": "Загрузка…",
  "Language": "Язык",
  // ---- common actions ----
  "Save": "Сохранить", "Saving…": "Сохранение…", "Saved": "Сохранено", "Cancel": "Отмена",
  "Close": "Закрыть", "Add": "Добавить", "Edit": "Изменить", 
  "Delete": "Удалить", "Deleted": "Удалено", "Refresh": "Обновить", "Refreshed": "Обновлено",
  "Done": "Готово", "Enabled": "Включено", "Disabled": "Выключено",
  "enable": "включить", "disable": "выключить", "Working…": "Выполняется…", "Starting…": "Запуск…",
  "Generate": "Сгенерировать", "Copy": "Копировать", "Copied": "Скопировано", "Could not copy — select the text and copy it by hand": "Не удалось скопировать — выделите текст и скопируйте вручную", "Test": "Проверить",
  "Testing…": "Проверка…", "yes": "да", "no": "нет", "none": "нет", "never": "никогда",
  // ---- status / health ----
  "no report yet": "нет данных", "status: OK": "статус: OK", "healthy": "Работает",
  "degraded": "Сбой", "unknown": "неизвестно",
  "Collecting metrics…": "Сбор метрик…", "no metrics yet": "пока нет метрик",
  // ---- overview ----
  "nodes": "серверы", "entry": "вход", "exit": "выход", "groups": "группы", "users": "пользователи",
  "policies": "правила", "Node monitoring": "Мониторинг серверов", "Panel": "Панель",
  "Standby": "Резерв",
  "CPU": "ЦП", "Memory": "Память", "State": "Состояние", "Memory, GiB": "Память, ГиБ", "Traffic, GiB": "Трафик, ГиБ", "Total, GiB": "Всего, ГиБ", "of {n}": "из {n}",
  "Disk": "Диск", "updated": "обновлено", "up": "аптайм",
  // ---- needs attention ----
  "Needs attention": "Требует внимания", "All systems normal": "Всё в порядке",
  "agent unreachable": "агент недоступен",
  "staging TLS certificate (not browser-trusted)": "staging-сертификат TLS (не доверенный браузером)",
  "replication slot missing (never provisioned or dropped)": "слот репликации отсутствует (не создан или удалён)",
  "replica not streaming (Postgres down or disconnected)": "реплика не получает данные (Postgres не отвечает или отключён)",
  "replica {n} behind": "реплика отстаёт на {n}",
  "replica streaming": "реплика получает данные",
  "the agent is not answering — the server is unreachable": "агент не отвечает — сервер недоступен",
  "{n} in maintenance": "{n} на обслуживании",
  "VPS payment overdue": "оплата VPS просрочена", "VPS payment due soon": "скоро оплата VPS",
  "management bot unreachable": "бот управления недоступен",
  "alert bot unreachable": "бот алертов недоступен",
  // ---- traffic ----
  "All servers": "Все серверы",
  "directly from the entry node": "напрямую с входного сервера",
  "Nothing has gone out yet this month": "В этом месяце трафика ещё не было",
  "cumulative since each node started, polled from the entry nodes.": "суммарно с момента запуска серверов, опрашивается с входных серверов.",
  "Online": "Онлайн",
  // ---- nodes ----
  "Name": "Название", "Role": "Роль", "Public IPs": "Публичные IP", "Agent": "Агент",
  "Add server": "Добавить сервер",
  "The plan's figures, for reference. No limit is applied to the server.":
    "Справочные цифры тарифа. Ограничения на сервер не устанавливаются.", "Config": "Конфиг", "More actions": "Ещё действия", "Hide graph": "Скрыть график",
  "Open": "Открыть", "Log": "Журнал", "Node log": "Журнал сервера", "Nothing about this node in the journal yet": "Про этот сервер в журнале пока пусто",
  "Issue a production certificate": "Выпустить боевой сертификат",
  "Split into two servers": "Разделить на два сервера",
  "Put a new server in front of this one and turn this one into the exit": "Поставить новый сервер перед этим и сделать этот выходом",
  // ---- domains (subsection in nodes) ----
  "Issuer": "Издатель", "Purpose": "Назначение", "Hostname": "Доменное имя",
  "TLS": "TLS", "→ prod": "→ prod", "ready": "готов",
  "Switch this node's cert from staging to Let's Encrypt production and reissue": "Переключить сертификат сервера со staging на боевой Let's Encrypt и перевыпустить",
  "Staging cert — not browser-trusted; promote to production": "Staging-сертификат — не доверен браузером; переключите на боевой",
  // ---- entity panel generic ----
  "How this works": "Как это работает",
  "Lines are read top to bottom: each one joins to everything above it, so “A and B or C” is “(A and B) or C”. The form writes the brackets as you build it.": "Условия объединяются сверху вниз: «A и B или C» означает «(A и B) или C». Полное условие со скобками показано под списком.",
  "Infra rules run before scope rules. Within a level, priority decides and the first match wins; the list is shown in that order.": "Правила уровня «Инфра» проверяются раньше правил скоупа. Внутри уровня учитывается приоритет; применяется первое совпавшее правило. Список показывает порядок проверки.",
  // ---- settings ----
  "Bots & alerts": "Боты и оповещения", "Account & security": "Аккаунт и безопасность",
  "Server": "Сервер", "Backup": "Бэкап",
  "Server defaults": "Настройки нового сервера",
  "Panel & schedules": "Панель и расписания",
  "Management bot": "Бот управления", "Alerts": "Оповещения",
  "From @BotFather. For the Geosite catalogue search, switch on /setinline and /setinlinefeedback there as well.":
    "Из @BotFather. Чтобы работал поиск по каталогу Geosite, включите там же /setinline и /setinlinefeedback.",
  "Schedule and retention": "Расписание и хранение копий",
  "Proving they restore": "Проверка восстановления",
  "A copy is restored into a scratch database on a schedule, so a backup that cannot be read is found before it is needed.":
    "По расписанию проверяется восстановление копии во временную базу.",
  "Copies in Telegram": "Копии в Telegram",
  "Runs the commands and, by default, sends the alerts.": "Выполняет команды и по умолчанию шлёт оповещения.",
  "Infra alerts (payments, watchdog) — sent by the management bot.": "Инфра-оповещения (оплаты, watchdog) — шлёт бот управления.",
  "My bot access": "Мой доступ к боту",
  "Backup alert bot": "Запасной бот оповещений",
  "Sends word when the primary node stops answering.": "Бот отправит уведомление, когда основной сервер станет недоступен.",
  "Backup bot token": "Токен запасного бота",
  "Generate with age-keygen; paste only the public key (age1…). Keep the private key OFFLINE.": "Сгенерируйте через age-keygen; вставьте только публичный ключ (age1…). Приватный ключ держите ОФЛАЙН.",
  "Show": "Показать", "Hide": "Скрыть",
  // ---- locked-out recovery (one-time code from the bot) ----
  "Forgot password?": "Забыли пароль?", "Recover access": "Восстановление доступа",
  "Recovery code": "Код восстановления", "Set new password": "Задать новый пароль",
  "Back to sign in": "Назад ко входу",
  "Ask the Telegram bot for a code: /recover": "Запросите код у Telegram-бота: /recover",
  "Password changed — sign in with the new password.": "Пароль изменён — войдите с новым паролем.",
  "Change your password": "Смена пароля", "Current password": "Текущий пароль",
  "Confirm new password": "Подтвердите новый пароль", "passwords do not match": "пароли не совпадают",
  "Confirm password": "Подтвердите пароль", "{label} does not match": "{label} не совпадает",
  "re-enter the password (leave both blank to auto-generate)": "повторите пароль (оставьте оба поля пустыми для автогенерации)",
  "master": "мастер", "standby": "резерв",
  "ACME contact email": "ACME-контакт (email)", "Default Reality SNI": "Reality SNI по умолчанию",
  "Apex domain": "Корневой домен", "Connect subdomain": "Поддомен подключения", "Brand": "Бренд",
  "Reconcile interval (seconds)": "Синхронизация, с",
  "Stats poll interval (seconds)": "Обновление статистики, с",
  "Billing check interval (hours)": "Проверка оплаты, ч",
  "Warn N days before payment due": "До оплаты, дней",
  "Warn N days before a client config expires": "До окончания доступа клиента, дней",
  "Session lifetime (hours)": "Длительность сессии, ч",
  "Auto egress-failover after exit down (seconds)": "Смена выхода при отказе, с",
  "min 60 s": "минимум 60 с",
  "Online threshold (KB/min)": "Порог «онлайн», КБ/мин",
  "this account": "этот аккаунт",
  // ---- logs ----
  "Event log": "Журнал событий", "All": "Все", "alert": "алерт", "an action": "действие",
  "system": "система", "No events yet": "Пока нет событий",
  "When": "Когда", "Kind": "Тип", "Message": "Сообщение", "Actor": "Кто",
  "critical": "критический", "warn": "предупреждение", "info": "инфо",
  "operator": "оператор", "read-only": "только чтение", 
  "Account": "Аккаунт", "Accounts": "Аккаунты", "Admin": "Админ", "Operator": "Оператор",
  "Members": "Участники", "Add member": "Добавить участника",
  "Make admin": "Сделать админом", "Make operator": "Сделать оператором",
  "Switch between admin and operator": "Переключить между админом и оператором",
  "Members of your scope ({ns}) share its clients. Admins can manage members; operators only the clients.": "Участники вашего скоупа ({ns}) делят его клиентов. Админы управляют участниками; операторы — только клиентами.",
  "Admins share the infrastructure. Each operator's scope is isolated from the rest.": "Админы делят общую инфраструктуру. Скоуп каждого оператора изолирован от остальных.",
  "Telegram": "Telegram", "Created": "Создан", "Change role": "Сменить роль",
  "{name}: {role}": "{name}: {role}",
  "Works through the Telegram bot: its own clients, nobody else's.":
    "Работает через Telegram-бот: свои клиенты, чужих не видит.",
  "Signs in to the panel and manages the whole infrastructure.":
    "Входит в панель и управляет всей инфраструктурой.",
  "for alerts; not needed to sign in": "для оповещений; для входа не нужен",
  "username is required": "нужно указать имя пользователя",
  "password must be at least 8 characters": "пароль должен быть не короче 8 символов",
  "telegram id is required for the operator role": "для роли «оператор» обязателен Telegram id",
  "password is required to make this account an admin": "чтобы сделать аккаунт админом, укажите пароль",
  "telegram id is required to make this account an operator": "чтобы сделать аккаунт оператором, укажите Telegram id",
  "Telegram id": "Telegram id",
  "Your Telegram id": "Ваш Telegram id",
  "Add account": "Добавить аккаунт", "expired": "истёк", "Login": "Логин", "Client": "Клиент",
  "My account": "Мой аккаунт",
  "Actions": "Действия", "Routes": "Маршруты", "Servers": "Серверы",
  "Filter by name or address": "Фильтр по имени или адресу",
  "Filter by name": "Фильтр по названию",
  "Filter by hostname": "Фильтр по имени хоста",
  "Ivan Petrov": "Иван Петров",
  "Groups by exit": "Группы по выходам", "Traffic this month, GiB": "Трафик за месяц, ГиБ", "admin": "администратор",
  "End of the journal": "Конец журнала", "Sort by this column": "Сортировать по этой колонке", "Showing the most recent entries — narrow the filter to see further back": "Показаны самые свежие записи — сузьте фильтр, чтобы заглянуть дальше",
  "Germany (de)": "Германия (de)",
  "first at {time}": "первый раз в {time}",
  "Filter by name or login": "Фильтр по имени или логину", "Filter by name or group": "Фильтр по названию или группе",
  "Failover is manual. The backup bot sends the command; the same steps are in RECOVERY.md inside every backup.": "Переключение ручное. Команду пришлёт запасной бот; она же лежит в RECOVERY.md внутри бэкапа.",
  "A standby is a second exit node. There is none yet.": "Резерв — это второй выходной сервер. Пока его нет.",
  "The same thing from a console:": "То же самое из консоли:",
  "Save first — a test uses the saved settings": "Сначала сохраните — тест идёт по сохранённым настройкам",
  "One bot on both channels. Set a management bot to split them.": "Один бот на оба канала. Задайте бота управления, чтобы разделить.",
  "Part size (MiB)": "Размер части (МиБ)",
  "blank or 0 = one part, up to Telegram's own limit": "Пусто или 0 — один файл, в пределах лимита Telegram.",
  "Encrypted with age, sent to a private Telegram channel.": "Шифруются age, уходят в приватный Telegram-канал.",
  "Telegram id must be a number": "Telegram id должен быть числом",
  "leave blank to keep / 0 to unbind": "пусто — оставить / 0 — отвязать",
  // ---- monitoring card ----
  "Traffic (mo)": "Трафик (мес)", "no limit set": "лимит не задан", "ago": "назад",
  "stale": "устарело", "config synced": "конфиг синхронизирован", "sync": "синхронизация",
  "edge :443 reachable": ":443 доступен извне", "edge :443 unreachable": ":443 недоступен извне",
  "paid until": "оплачено до", "overdue": "просрочено", "left": "осталось",
  // ---- entity table chrome ----
  "No entries yet": "Пока нет записей", "No matches": "Ничего не найдено",
  "Filter…": "Фильтр…", "Route tester": "Тест маршрута",
  "This domain has no node to promote": "У этого домена нет сервера для переключения",
  "Switch this node's certificate from staging to Let's Encrypt production and reissue now?": "Переключить сертификат сервера со staging на боевой Let's Encrypt и перевыпустить сейчас?",
  "Reissued from production": "Перевыпущено с боевого УЦ",
  // ---- schemas: singulars ----
  "node": "сервер", "group": "группу", "client": "клиента", "rule": "правило",
  "domain": "домен",
  // ---- schemas: nodes ----
  "Agent address": "Адрес агента",
  "Reality port": "Порт Reality", "VLESS UUID": "VLESS UUID",
  "a third-party domain this server's traffic is made to look like":
    "сторонний домен, под который маскируется трафик этого сервера",
  "Reality public key": "Публичный ключ Reality", "Reality private key": "Приватный ключ Reality",
  "Reality parameters": "Параметры Reality", "not set yet": "пока не задан",
  "Keep the current key": "Оставить текущий", "Replace the key": "Заменить ключ",
  "New private key": "Новый приватный ключ",
  "{n} pointing at it": "доменов на нём: {n}", "{n} leaving through it": "групп уходит через него: {n}",
  "{n} naming it": "правил ссылается на него: {n}",
  "Nothing is pointed at this server yet.": "На этот сервер пока ничто не указывает.",
  "It stops taking connections and starts letting traffic out.":
    "Он перестанет принимать подключения и начнёт выпускать трафик.",
  "It stops letting traffic out and starts taking connections.":
    "Он перестанет выпускать трафик и начнёт принимать подключения.",
  "Today {what} — each of those needs pointing somewhere else.":
    "Сейчас {what} — каждое из этого придётся перенести.",
  "The servers are reconfigured at the next sync.":
    "Серверы будут перенастроены при ближайшей синхронизации.",
  "Reality short_id": "Reality short_id",
  "host:8443 (the panel reaches the agent here)": "Адрес для связи панели с агентом, например host:8443.",
  // ---- schemas: groups ----
  "Default exit": "Выход по умолчанию", "Default exit node": "Сервер-выход по умолчанию", "direct": "напрямую", "block": "блокировка", "default": "по умолчанию",
  "direct (egress from the entry node)": "напрямую (выход с входного сервера)",
  // ---- schemas: users ----
  "Display name": "Отображаемое имя", "Group": "Группа",
  "the join-key (TrustTunnel credentials + sing-box auth_user)": "ключ подключения (учётка TrustTunnel + auth_user sing-box)",
  "auto-generated on create; on edit leave blank to keep the current password (min 12 chars if set explicitly)": "генерируется при создании; при изменении оставьте пустым, чтобы сохранить текущий пароль (минимум 12 символов, если задаёте вручную)",
  "Regenerate password (issue a new secret)": "Перевыпустить пароль (новый секрет)",
  // ---- schemas: routing ----
  "Status": "Статус", "Action": "Действие", "Match": "Совпадение",
  "Exit": "Выход", "Prio": "Приоритет", "all": "все", "enabled": "включено", "disabled": "выключено",
  "Priority": "Приоритет", "Applies to group": "Применять к группе",
  "Exit node": "Сервер-выход",
  "Rules": "Правила", "Show routing rules targeting this group": "Показать правила маршрутизации для этой группы",
  "Geo databases": "Гео-базы", "Update at every check": "Обновлять при каждой проверке",
  "catch": "ловим", "condition": "Условие",
  "how condition {n} joins the ones above": "как условие {n} соединяется с тем, что выше",
  "what condition {n} looks at": "что смотрит условие {n}",
  "remove condition {n}": "убрать условие {n}",
  "Remove {value}": "убрать {value}",
  "— none —": "— нет —",
  "out through an exit node": "через сервер-выход", "out from the entry node itself": "с самого входного сервера",
  "refuse it": "отказать",
  "a higher number is checked earlier at the same level": "больше число — раньше проверяется на своём уровне",
  "Everyone else": "Остальные", "Their exit node": "Сервер для остальных",
  "If that exit goes down": "Если выход отвалится", "Fall back to": "Уходить на",
  "takes effect once the panel has taken that exit out of rotation": "сработает, когда панель выведет сервер из ротации",
  "If that exit goes down, this traffic is refused.": "Если сервер отвалится — отказ.",
  "If that exit goes down, it moves to {name} — and is refused if that one is out too.":
    "Если сервер отвалится — уйдёт через {name}, а если и тот выведен — откажет.",
  "if this one is out of rotation too, the traffic is refused rather than leaving from the entry node":
    "если и он выведен из ротации — трафику будет отказано, а не выпущен с входного сервера",
  "as usual, by the rules below": "как обычно, по правилам ниже",
  "out through another exit node": "через другой сервер-выход",
  "Everyone else leaves from the entry node.": "Остальные выйдут с входного сервера.",
  "Everyone else exits via {name}.": "Остальные выйдут через {name}.",
  "reserved": "закреплено", "this rule decides for its group and for everyone else": "правило решает и за свою группу, и за всех остальных",
  "Everyone else is refused.": "Остальным — отказ.",
  "and": "и", "or": "или", "except": "кроме",
  "domains": "домены", "country": "страна", "category": "категория", "CIDR": "CIDR",
  "Add at least one condition": "Добавьте хотя бы одно условие",
  "Rule": "Правило", "From the entry node": "Напрямую через входной сервер",
  "Client connection": "Подключение", "App link": "Ссылка для приложения",
  "Sync and statistics": "Синхронизация и статистика", "Expiry reminders": "Напоминания о сроках",
  "Panel access": "Доступ к панели",
  // ---- add-server wizard ----
  "Step {n} of {total}": "Шаг {n} из {total}", "The server": "Сервер", "SSH access": "SSH-доступ",
  "Access after the install": "Доступ после установки", "Next": "Далее",
  "Install and add the server": "Установить и добавить сервер",
  "How the panel reaches this server to install it. Used once, and not stored.":
    "SSH-доступ для установки. Пароль или ключ не сохраняется в панели.",
  "How the server should be reachable once it is installed, instead of the access used to install it.":
    "Настройки SSH и защиты сервера после установки.",
  "After the install: log in as {user} on port {port}.": "После установки: вход под {user} на порт {port}.",
  "After the install: the SSH access stays as it is now.": "После установки: доступ по SSH останется прежним.",
  "This server": "Этот сервер",
  "Give the server a name": "Дайте серверу название",
  "Add at least one public IP": "Добавьте хотя бы один публичный IP",
  "Clients need a hostname to connect to": "Клиентам нужен адрес, по которому подключаться",
  "Pick the domain this node's traffic looks like": "Укажите домен, под который маскируется трафик сервера",
  "Say where to reach the server over SSH": "Укажите, куда подключаться по SSH",
  "Say which user to log in as": "Укажите пользователя для входа",
  "The install needs the SSH password once": "Для установки нужен пароль SSH — один раз",
  "The install needs the SSH private key once": "Для установки нужен приватный ключ SSH — один раз",
  "Password login cannot be turned off without a key to log in with":
    "Нельзя отключить вход по паролю, не оставив ключа для входа",
  "Resources & payment": "Ресурсы и оплата", "Select {name}": "Выбрать {name}",
  "Reality domain (SNI)": "Домен для Reality (SNI)",
  "a third-party domain this node's traffic is made to look like; keys are generated for you":
    "сторонний домен, под который маскируется трафик сервера; ключи создаются автоматически",
  // ---- client form ----
  "Connection details": "Данные подключения", "Connection login": "Логин для подключения",
  "the name this client authenticates with": "имя, которым клиент подключается",
  "Change password": "Изменить пароль", "Keep the current password": "Отменить изменение",
  "New password": "Новый пароль", "Repeat the password": "Повторите пароль",
  "at least 12 characters": "не короче 12 символов",
  "once saved, the old password stops working": "после сохранения старый пароль перестанет работать",
  "cannot be changed": "изменить нельзя",
  "Generate automatically": "Сгенерировать автоматически", "Set a password": "Задать пароль",
  "Generate one": "Сгенерировать", "Access until": "Доступ до", "No end date": "Без срока",
  "empty = no end date; access stops at the end of the chosen day (UTC)":
    "Пусто — бессрочно. Доступ до конца выбранного дня по UTC.",
  // ---- access that has run out ----
  "access ended": "срок закончился",
  "Access ended {date}": "Срок доступа закончился {date}",
  "The client stays off until the date is extended — its config would not connect.":
    "Пока срок не продлён, клиент остаётся выключенным — его конфигурация не подключится.",
  "Extend access": "Продлить срок",
  "This date has passed — extend it to switch the client on":
    "эта дата уже прошла — продлите срок, чтобы включить клиента",
  "This client is switched off": "Клиент выключен",
  "Switched off: {n}": "Выключено: {n}",
  "Access now runs to {date}.": "Теперь доступ до {date}.",
  "Access now has no end date.": "Теперь доступ без срока.",
  "Switch the client on as well?": "Включить клиента вместе с продлением?",
  "Switch them on as well?": "Включить их вместе с продлением?",
  "Switch on and save": "Включить и сохранить",
  "Save, leave it off": "Сохранить выключенным",
  "Save, leave them off": "Сохранить выключенными",
  "{n} past the access date — they stay off until it is extended":
    "клиентов с истёкшим сроком: {n} — они останутся выключенными, пока срок не продлён",
  "access has already ended; extend the access date to switch this client on":
    "срок доступа уже закончился; продлите его, чтобы включить клиента",
  // ---- routing rule form ----
  "All clients": "Все клиенты",
  "All network groups": "Все группы сети",
  "All my groups": "Все мои группы",
  "Choose a level": "Выберите уровень",
  "Choose a group": "Выберите группу",
  "Choose a group first": "Сначала выберите группу",
  "Choose a shared route": "Выберите общий маршрут",
  "Choose a route": "Выберите маршрут",
  "Choose level": "Выбрать уровень",
  "Choose node": "Выбрать сервер",
  "Choose the rule level.": "Выберите уровень правила.",
  "Choose one group for an exclusive rule.": "Выберите одну группу для эксклюзивного правила.",
  "This group uses a direct connection. Choose an exit server to enable an exclusive rule.": "У группы прямой выход. Выберите сервер, чтобы включить эксклюзивное правило.",
  "Choose an exit node for this group.": "Выберите выходной сервер для этой группы.",
  "For other network groups": "Для остальных групп сети",
  "For my other groups": "Для остальных моих групп",
  "Choose an action for others": "Выберите действие для остальных",
  "Unsaved changes will be deleted.": "Несохранённые изменения будут удалены.",
  "Cancel changes to conditions?": "Отменить изменения условий?",
  "Changes in this section will be cancelled. Other settings will be kept.": "Изменения в этом разделе будут отменены. Остальные настройки сохранятся.",
  "Cancel changes": "Отменить изменения",
  "Edit rule": "Изменить правило",
  "Choose the level and group first. They determine which requests the rule handles and its order.": "Сначала выберите уровень и группу. От них зависит, какие запросы обработает правило и в каком порядке.",
  "Add a category, country, domain or IP address.": "Добавьте хотя бы одно условие: категорию, страну, домен или IP-адрес.",
  "Route not selected": "Маршрут не выбран",
  "The rule applies when a request matches its conditions and no earlier rule has handled it.": "Правило сработает, если запрос подходит под условия и его не обработало правило выше.",
  "For an exclusive rule, choose an action for the other groups in More settings.": "Для эксклюзивного правила выберите действие для остальных групп в дополнительных настройках.",
  "Excluded requests skip this rule. Later rules apply, followed by the group's default exit.": "Исключения это правило пропускает. Для них действуют следующие правила, а затем — выход группы по умолчанию.",
  "Choose how to handle requests from other groups.": "Выберите, как обрабатывать запросы остальных групп.",
  "Choose action for others": "Выбрать действие для остальных",
  "The VPN connection stays active. The site sees the entry server's IP address.": "VPN-подключение сохраняется. Ресурс увидит IP входного сервера.",
  "Rule level": "Уровень правила", "Say which level this rule is written at": "Укажите уровень правила", "— pick one —": "— выберите —", "— pick where it goes —": "— куда отправить —",
  "Route": "Маршрут", "New rule": "Новое правило",
  "Rule name": "Название правила", "Rename": "Переименовать",
  "Through {name}": "Через {name}", "Through {name} — the group's own exit": "Через {name} — выход группы",
  "Out of the entry node it is already on": "Напрямую через входной сервер",
  "Out of the entry node itself": "Напрямую через входной сервер",
  "out of the entry node": "напрямую через входной сервер",
  "Blocked": "Заблокировать",
  "Back to the group's own exit: {name}": "Вернуть выход группы: {name}",
  "Pick a group above and its own exit is filled in here.": "Выберите группу — подставится её выходной сервер.",
  "Your groups leave by different exits, so there is none to fill in — name the one this rule uses.":
    "У ваших групп разные выходы, подставить нечего — укажите, каким пойдёт это правило.",
  "More settings": "Дополнительные настройки", "What this rule does": "Что делает правило",
  "Back": "Назад", "exclusive": "Эксклюзивный", "has a fallback": "есть резерв", "off": "выключено",
  "Exclusive rule": "Эксклюзивное правило",
  "Only a rule written for one group can decide for everyone else.":
    "Решать за остальных может только правило, написанное для одной группы.",
  "For everyone outside {name}": "Для всех, кроме группы «{name}»",
  "Say what everyone else gets": "Укажите, что получают остальные",
  "Pick a group": "Выбрать группу", "Rule is on": "Правило включено",
  "If {name} is unavailable": "При недоступности {name}",
  "If that exit is unavailable": "При недоступности выхода",
  "Say who this rule is for": "Укажите, для кого это правило",
  "Say where this traffic goes": "Укажите, куда идёт этот трафик",
  "Pick the exit everyone else takes": "Выберите сервер для остальных",
  "Pick the exit to fall back to": "Выберите запасной сервер",
  "A whole number, please": "Нужно целое число",
  "Add more…": "Добавить ещё…", "exclusion": "Исключение", "Create": "Создать",
  // what a rule looks at, and the field that collects it
  "geosite categories": "Категории geosite", "geoip countries": "Страны geoip",
  "Domains": "Домены", "IPs and subnets": "IP и подсети",
  "Find a category: youtube, telegram…": "Найти категорию: youtube, telegram…",
  "Country name or code: Germany, de…": "Название страны или код: Германия, de…",
  "example.com — no https:// and no path": "example.com — без https:// и пути",
  "203.0.113.0/24 or 2001:db8::/32": "203.0.113.0/24 или 2001:db8::/32",
  "no categories yet": "категории не выбраны", "no countries yet": "страны не выбраны",
  "no domains yet": "домены не добавлены", "no addresses yet": "адреса не добавлены",
  "nothing yet": "ещё ничего не выбрано",
  "What this line looks at": "На что смотрит эта строка",
  "{kind}: add a value": "{kind}: добавить значение",
  "Add “{v}” as typed": "Добавить «{v}» как есть",
  "“{v}” is not in the source — check the spelling": "«{v}» нет в источнике — проверьте написание",
  "Suggestions": "Подсказки", "private addresses": "Частные адреса",
  "Press Enter to add what is typed, or clear the search": "Нажмите Enter, чтобы добавить набранное, или очистите поиск",
  // the conditions screen
  "Rule conditions": "Условия правила", "Edit conditions": "Изменить условия",
  "Lines are read top to bottom: each one joins to everything above it.":
    "Условия объединяются сверху вниз.",
  "Resources": "Ресурсы", "The whole condition": "Итоговое условие",
  "nothing is filled in yet": "ещё ничего не заполнено",
  "Apply conditions": "Применить условия",
  "how line {n} joins the ones above it": "как строка {n} соединяется с теми, что выше",
  "remove line {n}": "удалить строку {n}",
  "Leave the conditions as they were?": "Оставить условия прежними?",
  "What was changed here has not been applied to the rule.": "Изменения здесь не применены к правилу.",
  "Discard the changes": "Отменить изменения",
  "Saved: {sections}": "Сохранено: {sections}", "Nothing had changed": "Ничего не менялось",
  "Will save: {sections}": "Будет сохранено: {sections}", "unsaved changes": "есть несохранённое",
  "bot": "бот", "alerts": "оповещения", "backup": "бэкап", "server defaults": "настройки серверов",
  "panel & schedules": "панель и расписания", "your Telegram": "ваш Telegram",
  "{name} is off": "{name} отключено", "{name} is on": "{name} включено", "{name} deleted": "{name} удалено",
  "{name} is out of rotation": "{name} выведен из ротации", "{name} is back in rotation": "{name} вернулся в ротацию",
  "{n} moved": "переведено {n}",
  "{node} — the default exit": "{node} — выход по умолчанию",
  "out of {node} itself": "с самого сервера {node}",
  "out of the entry node itself": "с самого входного сервера",
  "unless a routing rule sends it somewhere else": "если правило маршрутизации не отправит трафик иначе",
  "Overlaps between categories are not checked here. Test a specific address in the route tester.":
    "Пересечения между категориями здесь не проверяются. Проверьте конкретный адрес в тестере маршрута.",
  "Checked after: {names}.": "Проверяется после: {names}.", "and {n} more": "и ещё {n}",
  "The exit is written into the rule. Changing the group's own exit later does not move this rule.":
    "В правиле сохраняется выбранный выход. Изменение выхода группы не изменит этот маршрут.",
  "If that exit is unavailable, this traffic is refused.": "При недоступности этого выхода трафик блокируется.",
  "If that exit is unavailable, it moves to {name} — and is refused if that one is out too.":
    "При недоступности этого выхода трафик перейдёт на {name}, а если и тот недоступен — будет заблокирован.",
  "If that exit is unavailable, this traffic leaves from the entry node.":
    "При недоступности этого выхода трафик пойдёт напрямую через входной сервер.",
  "Close without saving?": "Закрыть без сохранения?", "Discard": "Отбросить",
  "Your changes will be lost.": "Изменения будут потеряны.",
  "Fill this condition in or remove it": "Заполните это условие или удалите строку",
  "The first condition cannot be an exclusion": "Первое условие не может быть исключением",
  "Remove": "Убрать", "but not": "но не",
  "Check sources": "Проверять источники", "Next check {due}": "Следующая проверка {due}",
  "in {n}": "через {n}", "any moment": "с минуты на минуту",
  "The first check runs shortly.": "Первая проверка пройдёт скоро.",
  "{n} on auto": "с автообновлением: {n}",
  "nothing updates on its own": "само ничего не обновляется",
  "Auto": "Авто", "Update": "Обновить", "Updating…": "Обновляю…", "Put back": "Вернуть",
  "Update all ({n})": "Обновить все ({n})", "Databases updated": "Базы обновлены",
  "{name} updated": "{name} обновлён", "{name} put back": "{name} возвращён",
  "Update {n} from their sources?": "Обновить из источников: {n}?",
  "The new lists reach the nodes and start deciding where traffic goes.": "Новые списки уедут на серверы и начнут решать, куда идёт трафик.",
  "Each replaced copy is kept and can be put back.": "Прошлые копии сохранятся, любую можно вернуть.",
  "Put back the previous {name}?": "Вернуть прошлую версию {name}?",
  "The previous copy replaces the current one and reaches the nodes.": "Прошлая копия заменит текущую и уедет на серверы.",
  "The source keeps the newer version; the next check offers it again.": "В источнике останется версия новее, проверка предложит её снова.",
  "Put it back": "Вернуть копию", "Update all": "Обновить все",
  "Previous copy, {size}": "Прошлая копия, {size}",
  "{n} categories in the source, or type your own": "{n} категорий в источнике, можно вписать своё",
  "a short built-in list; the full one arrives after a check on the Geo databases tab": "короткий встроенный список, полный — после проверки на вкладке «Гео-базы»",
  "Size": "Размер",
  "Countries (geoip)": "Страны (geoip)", "Domain categories (geosite)": "Категории доменов (geosite)",
  "Source": "Источник", "Address": "Адрес", "another address": "другой адрес",
  "Save source": "Сохранить источник", "Source saved": "Источник сохранён",
  "Files already downloaded stay as they are.": "Уже скачанные файлы остаются как есть.",
  "The sing-box project's own lists.": "Собственные списки проекта sing-box.",
  "The same categories plus the blocked-domain registry. Rebuilt every 6 hours.": "Те же категории плюс реестр заблокированного. Пересобирается каждые 6 часов.",
  "An address holding .srs files, one per category. Other formats will not load.": "Адрес с файлами .srs, по одному на категорию. Другие форматы не подойдут.",
  "Check for updates": "Проверить обновления", "Checking…": "Проверяю…", "Sources checked": "Источники проверены",
  "Check": "Проверять", "every 6 hours": "раз в 6 часов", "daily": "раз в сутки",
  "every 3 days": "раз в 3 дня", "weekly": "раз в неделю", "monthly": "раз в месяц",
  "Last checked {age} ago": "Проверено {age} назад", "Never checked": "Ещё не проверялось",
  "Last check failed": "Прошлая проверка не удалась",
  "{n} categories offered": "{n} категорий в источнике",
  "{a} new, {d} withdrawn": "{a} появилось, {d} убрано",
  "No category list yet — the first check runs shortly.": "Списка категорий пока нет — первая проверка пройдёт в ближайшие минуты.",
  "Categories the rules use": "Категории в правилах",
  "Nothing yet. A category appears here when a rule first names it.": "Пока пусто. Категория появляется здесь, когда правило впервые её называет.",
  "Category": "Категория", "Used by": "Используют", "Fetched": "Скачан",
  "{n} rules": "правил: {n}", "no rules": "нет правил",
  "up to date": "актуален", "update available": "есть новая версия",
  "frozen — withdrawn upstream": "заморожена — убрана из источника",
  "The source stopped publishing it. The copy here keeps working and will not be updated again.": "Источник перестал её публиковать. Местная копия продолжает работать и больше не обновится.",
  "no copy": "копии нет",
  "A rule names this category and there is nothing to give the nodes. They stop receiving configuration updates until the rule is fixed.": "Правило называет эту категорию, а отдать серверам нечего. Они не получают обновлений конфигурации, пока правило не исправят.",
  "Not checked against the source yet": "С источником ещё не сверялся",
  "The source holds different content": "В источнике другое содержимое",
  "Byte for byte what the source holds": "Совпадает с источником",
  "higher = evaluated first": "выше = проверяется раньше",
  "all users": "все пользователи", "— all users (any group) —": "— все пользователи (любая группа) —",
  "all my clients": "все мои клиенты", "— all my clients (any group) —": "— все мои клиенты (любая группа) —",
    "by destination IP · start typing a country": "по IP назначения · начните вводить страну",
  "e.g. google, netflix, category-ads, geolocation-cn": "например google, netflix, category-ads, geolocation-cn",
  "e.g. 10.0.0.0/8, 1.1.1.1": "например, 10.0.0.0/8, 1.1.1.1",
  // ---- routing help (the ? panel) ----
  "How a rule works": "Как работает правило",
  "“or” groups alternatives, “and” narrows them: “A or B and C” catches either A or B, when it is also C.":
    "«или» объединяет варианты, «и» их сужает: «A или B и C» поймает A или B, если это ещё и C.",
  "geoip looks at the destination IP, not at the domain text. A .ru domain hosted abroad is not geoip:ru.": "geoip смотрит на IP назначения, а не на текст домена: домен .ru на зарубежном хостинге под geoip:ru не попадёт.",
  "Infra rules run before scope rules. Within a level, priority decides and the first match wins.": "Инфра-правила проверяются раньше правил скоупа. Внутри уровня решает приоритет, побеждает первое совпадение.",
  "A rule can answer for everyone else too: another exit, direct, or refused. It is then checked before the ordinary rules of its level, otherwise a category listing the same domain would answer for them first.": "Правило может решить и за остальных: другой сервер, напрямую или отказ. Тогда оно проверяется раньше обычных правил своего уровня, иначе за них первой ответит категория, где есть тот же домен.",
  "Infra applies network-wide, admin only. Scope applies to your clients.": "Инфра — на всю сеть, только админ. Скоуп — на ваших клиентов.",
  // ---- routing levels (variant A) ----
  "Level": "Уровень",
  "network rule": "правило сети",
  "scope rule": "правило скоупа", "group default exit": "выход группы по умолчанию",
  "Infra": "Infra",
  "Scope": "Scope",
  "All scopes": "Все скоупы",
  "Scope — my clients": "Scope — мои клиенты",
  "Infra — the whole network": "Infra — общая инфраструктура",
  "Scope: your clients. Infra: the whole network, admin only.": "Скоуп — ваши клиенты. Инфра — вся сеть, только админ.",
  "It is the first rule checked.": "Проверяется первым.",
  "Checked after {n}.": "Перед ним проверяется {n}.",
  "Use the Route tester to check which rule wins for a given group and host.": "Кнопка «Тест маршрута» покажет, какое правило выиграет для конкретной группы и хоста.",
  // ---- routing live summary ----
  "Nothing is picked yet — add what this rule catches.": "Пока ничего не выбрано — добавьте, что правило ловит.",
  "leave from the entry node, which is the address the site sees": "выйдет с входного сервера, его адрес ресурс и увидит",
  "be blocked": "будет заблокирован",
  "exit via {name}": "выйдет через {name}",
  "(pick an exit node)": "(выберите сервер-выход)",
  "will {how}": "{how}",
  "Everyone else would take the same exit as the group, so nothing is reserved.": "Остальные пойдут через тот же сервер, что и группа — это ничего не закрепляет.",
  "“{name}” is checked earlier and also catches {on}{more} — that traffic may go there first.": "Правило «{name}» проверяется раньше и тоже подходит для {on}{more}. Оно может сработать первым.",
  "This rule is off and won't be applied.": "Правило выключено и применяться не будет.",
  // ---- route tester ----
  "Host to test (IP or domain)": "Хост для проверки (IP или домен)",
  "Enter an IP or domain to test": "Введите IP или домен для проверки",
  "e.g.": "напр.",
  "Goes out via": "Выходит через",
  "Decided by": "Решило",
  "Resolved to": "Разрешилось в",
  "How it was decided (top to bottom, first match wins)": "Как принято решение (сверху вниз, побеждает первое совпадение)",
  // ---- schemas: domains ----
  "Entry node": "Входной сервер", "endpoint": "endpoint", "decoy": "decoy",
  "VPN connection": "Подключение к VPN", "Cover site": "Сайт прикрытия",
  "the name clients connect to; anyone else who opens it gets the cover site":
    "Адрес VPN. В браузере открывает сайт прикрытия.",
  "an ordinary site on this server; nobody connects through this name":
    "Сайт прикрытия без VPN-подключения.",
  "the server that answers on this address": "сервер, который отвечает по этому адресу",
  "The DNS record to add shows up here once the name and the server are known.":
    "DNS-запись появится здесь, когда будут известны имя и сервер.",
  "The certificate is issued once the name resolves here.":
    "Сертификат выпустится, когда имя начнёт разрешаться в этот адрес.",
  // ---- settings extra ----
  "Total": "Всего", "set": "задано", "leave blank to keep": "оставьте пустым, чтобы сохранить",
  "paste bot token": "вставьте токен бота",
  "Send alerts": "Слать оповещения",
  "Bot token": "Токен бота",
  "Alert chat ID": "Chat ID для оповещений",
  "Local snapshots (database + CA), one timer per node.": "Локальные снапшоты (база + CA), по таймеру на каждом сервере.",
  "Local backup enabled": "Локальный бэкап включён", "Run every (hours)": "Интервал копирования, ч",
  "Copies to keep": "Сколько копий хранить", "Verify-restore drill enabled": "Проверка восстановления включена",
  "Verify every (days)": "Интервал проверки, дни",
  "Backup chat ID": "Chat ID для бэкапов", "age recipient (public key)": "age-получатель (публичный ключ)",
  // ---- account validation/notify ----
  "new password must be at least 8 characters": "новый пароль должен быть не короче 8 символов",
  "username required and password must be at least 8 characters": "нужно имя пользователя и пароль не короче 8 символов",
  // ---- status chip ----
  "reachable": "доступен", "token rejected": "токен отклонён", "unreachable": "недоступен",
  "not configured": "не настроено",
  // ---- form modal ----
  // ---- HA ----
  "High availability": "Отказоустойчивость (HA)",
  "A Postgres replica + CA on an exit. That exit's traffic is not interrupted.": "Копия базы и сертификатов панели на выходном сервере. Работа VPN продолжается.",
  "Panel standby · switched by hand": "Резерв панели · ручное переключение",
  "Switching is done by hand. The backup bot sends word when the primary stops answering.":
    "Переключение выполняется вручную. Запасной бот сообщит, когда основной сервер перестанет отвечать.",
  "Primary": "Основной",
  "no primary set — mark one, or this deployment doesn't use a standby": "основной не задан — отметьте сервер как primary, либо в этом развёртывании резерв не используется",
  "No standby — failover not possible. Add one below.": "Резерв не настроен. Выберите сервер ниже.",
  "Ready to fail over": "Готов к переключению", "Standby degraded": "Резерв деградировал",
  "Standby — replication starting": "Резерв — репликация запускается",
  "How to fail over": "Как переключиться",
  "If the primary fails, fail over to this standby — only once the primary is confirmed gone:": "Если основной сервер откажет, переключитесь на этот резерв — только когда primary точно недоступен:",
  "SSH into {name} ({ip}).": "Зайдите по SSH на {name} ({ip}).",
  "Run as root:": "Выполните под root:",
  "The panel comes back up on this node; point your tunnel at it.": "Панель поднимется на этом сервере; направьте на него своё подключение.",
  "{name} is now a standby": "{name} теперь резерв", "{name} replica rebuilt": "Реплика на {name} пересоздана",
  "Add standby failed": "Не удалось добавить резерв", "Rebuild failed": "Не удалось пересоздать реплику",
  "replication: checking…": "репликация: проверка…", "warning": "предупреждение",
  "streaming": "стриминг", "behind": "отставание",
  "Wipe this replica's PGDATA and re-seed it from the current primary (use if its Postgres died or diverged)": "Стереть PGDATA реплики и пересоздать её с текущего primary (если её Postgres умер или разошёлся)",
  "Rebuild replica": "Пересоздать реплику", "Add a standby": "Добавить резерв",
  "Make standby": "Сделать резервом", "Hide CLI": "Скрыть CLI", "Manual (CLI)": "Вручную (CLI)",
  "Copy command": "Скопировать команду", "Add-standby status": "Статус добавления резерва",
  "Dismiss": "Скрыть",
  // ---- modals chrome ----
  "Node specs": "Ресурсы сервера", "Client config": "Конфиг клиента",
  "Adding the server": "Добавление сервера", "Add a new server": "Добавление нового сервера",
  "Test route": "Проверить маршрут", "Download .toml": "Скачать .toml", "Link": "Ссылка",
  "Add server (submit)": "Добавить", "Adding…": "Добавление…",
  // ---- generic confirm ----
  "OK": "ОК",
  // ---- t() gaps ----
  "Synced — rev {revision}": "Синхронизировано — ревизия {revision}", "no nodes": "нет серверов",
  // ---- inline confirmations (HA / delete) ----
  "Make “{name}” a control-plane standby?": "Сделать «{name}» резервом управляющего сервера?",
  "Sets up replication on this exit and copies the CA private key onto it.": "Настраивает репликацию на этом сервере и копирует на него приватный ключ CA.",
  "The primary's Postgres restarts briefly; this exit's client traffic isn't affected.": "Postgres на основном сервере ненадолго перезапустится; клиентский трафик этого сервера не будет затронут.",
  "Rebuild the replica on “{name}”?": "Пересоздать реплику на «{name}»?",
  "Wipes this replica and re-seeds it from the current primary — use if its Postgres died or diverged.": "Стирает данные этого резерва и пересоздаёт их с основного сервера — используйте, если его Postgres упал или разошёлся с основным.",
  "Data is re-copied fresh from the primary; this exit's client traffic isn't affected.": "Данные будут заново скопированы с основного сервера; клиентский трафик этого сервера не будет затронут.",
  "Delete {name}?": "Удалить {name}?",
  "This cannot be undone.": "Отменить это будет нельзя.",
  "Traffic still routes through this server: {names}. Point those rules elsewhere first.": "На этот сервер ссылаются правила: {names}. Сперва переведите их на другой выход.",
  "There are still {n} in this group. Move the clients to another group first.": "В этой группе ещё {n}. Сперва переведите клиентов в другую группу.",
  "Its domains and their certificates go with it: {names}": "Вместе с ним удалятся его домены и их сертификаты: {names}",
  "These groups leave by it and will go out from the entry server instead: {names}": "Через него уходят группы — их трафик пойдёт напрямую с входного сервера: {names}",
  "Its traffic history goes with it.": "История трафика этого сервера удалится вместе с ним.",
  "The rules aimed at this group go with it: {names}": "Вместе с ней удалятся правила, нацеленные на эту группу: {names}",
  "Access ends at once, and the traffic history goes with it.": "Доступ прекратится сразу, история трафика удалится вместе с клиентом.",
  "Its certificate goes with it, and the server stops answering for this name.": "Сертификат удалится вместе с доменом, сервер перестанет отвечать на это имя.",
  // ---- ConfigModal ----
  "no entry nodes": "нет входных серверов",
  "connection page": "Страница подключения",
  "QR code": "QR-код",
  "Scan with the TrustTunnel app to import.": "Отсканируйте приложением TrustTunnel для импорта.",
  "Link too long to render as a QR": "Ссылка слишком длинная для QR",
  "No entry nodes yet — add one before this config can connect.": "В сети нет входных серверов — добавьте сервер, иначе этот конфиг не подключится.",
  "This client is disabled — the config builds but will be rejected until you enable it.": "Клиент выключен — конфиг соберётся, но не подключится, пока вы его не включите.",
  "This client has expired — extend its expiry or it cannot connect.": "Срок клиента истёк — продлите срок, иначе подключение невозможно.",
  "The selected entry is unhealthy — the config builds but may not connect until it recovers.": "Выбранный входной сервер нездоров — конфиг соберётся, но может не подключиться, пока сервер не восстановится.",
  "unhealthy": "нездоров",
  // ---- ProvisionModal / ConvertModal shared ----
  "Name and at least one public IP are required": "Нужны имя и хотя бы один публичный IP",
  "Domain": "Домен", "Port": "Порт", "Auth": "Аутентификация",
  "password": "пароль", "private key": "приватный ключ",
  "SSH host": "SSH-хост", "SSH user": "Пользователь SSH", "SSH password": "Пароль SSH",
  "SSH private key (PEM)": "Приватный ключ SSH (PEM)",
  "Sudo user to create": "Создать sudo-пользователя",
  "SSH public key for the sudo user": "Публичный ключ SSH для sudo-пользователя",
  "New SSH port": "Новый порт SSH", "Disable root login": "Запретить вход под root",
  "Disable password login": "Запретить вход по паролю",
  "Install fail2ban": "Установить fail2ban",
  "Enable firewall (ssh / 443 / 80)": "Включить файрвол (ssh / 443 / 80)",
  "used once for install, not stored": "используется один раз для установки, не сохраняется",
  "required before password auth can be disabled": "нужен, прежде чем отключать вход по паролю",
  // ---- ProvisionModal ----
  "Reality SNI is required for an exit": "Для выхода нужен Reality SNI",
  "Endpoint hostname is required for an entry": "Для входа нужен хост эндпоинта",
  "Node installed": "Сервер установлен",
  "Endpoint hostname": "Хост эндпоинта",
  "The VPN connection endpoint served on this node.": "Хост, на котором этот сервер принимает VPN-подключения.",
  "Set once, reused for later servers.": "Указывается один раз и подставляется для следующих серверов.",
  "Also serve the apex landing site on this node": "Разместить сайт-заглушку на корневом домене здесь",
  "Add a DNS record": "Заведите DNS-запись",
  "this server's IP": "IP этого сервера",
  "TLS hostname served by TrustTunnel": "хост TLS, который обслуживает TrustTunnel",
  "borrowed third-party domain; Reality keys + uuid are generated automatically": "заимствованный сторонний домен; ключи Reality и uuid генерируются автоматически",
  "Also make this exit a control-plane standby (HA)": "Сделать этот выход ещё и резервом управления (HA)",
  "After install, layers a Postgres replica + CA on this exit via the primary's agent, so it can take over the panel. Staged serve stays disabled until you promote.": "После установки разворачивает на этом выходе реплику Postgres + CA через агент primary, чтобы он мог взять панель на себя. Панель на нём остаётся выключенной, пока вы её не поднимете.",
  "Harden server": "Усилить защиту сервера",
  // ---- migration: one server becomes two ----
  "One server becomes two": "Один сервер становится двумя",
  "Splitting the deployment": "Разделение",
  "The deployment now runs on two servers": "Теперь работают два сервера",
  "Start the migration": "Начать миграцию",
  "this server": "этот сервер", "the new server": "новый сервер",
  "the new server's public IP(s)": "публичный(е) IP нового сервера",
  "The new server": "Новый сервер",
  "A new server goes in front and takes the connections. {name} keeps the panel, the database and the CA, and becomes the exit its traffic leaves from.":
    "Новый сервер встаёт впереди и принимает подключения. {name} сохраняет панель, базу и CA и становится выходом, через который уходит трафик.",
  "Give the new server a name": "Дайте новому серверу название",
  "Connection address": "Адрес подключения",
  "The address clients use today belongs to this server. It can move to the new one, or the new one can get an address of its own.":
    "Адрес, по которому клиенты подключаются сейчас, принадлежит этому серверу. Его можно перенести на новый — или дать новому свой.",
  "Keep the connection address": "Сохранить адрес подключения",
  "New connection address": "Новый адрес подключения",
  "The existing hostnames move to the new server, so every client keeps working with the config it already has.":
    "Существующие имена переезжают на новый сервер — все клиенты продолжают работать с теми конфигурациями, которые у них уже есть.",
  "Their DNS records have to point at the new server before this starts.":
    "DNS-записи должны указывать на новый сервер до начала миграции.",
  "New hostnames": "Новые доменные имена",
  "the first one is what clients connect to; the rest go in the same certificate":
    "первое — то, к чему подключаются клиенты; остальные попадут в тот же сертификат",
  "+ add hostname": "+ ещё имя",
  "A new address needs a hostname for the certificate": "Для нового адреса нужно доменное имя — на него выпускается сертификат",
  "This server has no hostname to hand over — give the new one an address of its own.":
    "У этого сервера нет имени, которое можно передать, — дайте новому серверу собственный адрес.",
  "Every client gets a new config: the link or QR has to be handed out and imported again.":
    "Каждому клиенту нужна новая конфигурация: ссылку или QR придётся выдать и импортировать заново.",
  "The exit and its groups": "Выход и его группы",
  "{name} becomes the exit. Its Reality inbound is new, and the groups named here leave through it.":
    "{name} становится выходом. Reality-вход на нём создаётся заново, а через него выходят указанные здесь группы.",
  "a third-party domain this server's traffic is made to look like; keys are generated for you":
    "сторонний домен, под который маскируется трафик этого сервера; ключи создаются автоматически",
  "Pick the domain this server's traffic will look like": "Выберите домен, под который будет маскироваться трафик",
  "Pick the groups that leave through the new exit, or take all of them":
    "Выберите группы, которые пойдут через новый выход, или возьмите все",
  "All groups tunnel through the new exit": "Все группы выходят через новый выход",
  "Access": "Доступ",
  "How the panel reaches the new server to install it. Used once, and not stored.":
    "Как панель достучится до нового сервера, чтобы его установить. Используется один раз и не сохраняется.",
  "How it should be reachable once it is installed. This replaces the access above.":
    "Как до него можно будет достучаться после установки. Это заменит доступ выше.",
  "Harden the new server": "Усилить защиту нового сервера",
  "DNS and what changes": "DNS и что изменится",
  "Check the DNS first: the install asks for a certificate, and that only works once the name resolves to the right server.":
    "Сначала проверьте DNS: при установке выпускается сертификат, а это получится только когда имя разрешается в нужный сервер.",
  "No hostname yet — go back and name the address clients connect to.":
    "Имени пока нет — вернитесь и укажите адрес, по которому подключаются клиенты.",
  "Point these at the new server before starting:": "Переведите их на новый сервер до начала:",
  "These have to resolve to the new server:": "Они должны разрешаться в новый сервер:",
  "the new server's IP": "IP нового сервера",
  "New entry": "Новый вход", "Becomes the exit": "Становится выходом",
  "Clients connect to": "Клиенты подключаются к", "Client configs": "Конфигурации клиентов",
  "keep working as they are": "продолжают работать как есть",
  "have to be handed out and imported again": "нужно выдать и импортировать заново",
  "Leaving through {name}": "Выходят через {name}",
  "all groups": "все группы", "no groups": "никакие",
  "After the install": "После установки",
  "log in as {user} on port {port}": "вход под {user} на порту {port}",
  "the SSH access stays as it is now": "доступ по SSH остаётся прежним",
  // ---- LimitsModal ----
  "VPS limits": "Лимиты VPS",
  "CPU cores (vCPU)": "Ядра CPU (vCPU)", "Memory (GB)": "Память (ГБ)", "Disk (GB)": "Диск (ГБ)",
  "Monthly traffic (TB)": "Трафик в месяц (ТБ)",
  "Billing": "Оплата",
  "Payment term": "Срок оплаты", "— no billing —": "— без оплаты —",
  "1 month": "1 месяц", "3 months": "3 месяца", "6 months": "6 месяцев", "12 months": "12 месяцев",
  "Enter the date this node is paid through": "Укажите дату, по которую сервер оплачен",
  "{n} left": "осталось {n}", "paid through today": "оплачено по сегодня", "overdue by {n}": "просрочено на {n}",
  "Paid through": "Оплачено до", "Extend by {n}": "Продлить на {n}", "was {date}": "было {date}",
  "{label} is not a date": "«{label}» — не дата",
  "unset": "не задано",
  "Expires": "Истекает",
  "Drain": "Вывести", "Resume": "Вернуть", "Drained": "Выведен на обслуживание", "Resumed": "Возвращён в работу",
  "drain": "обслуж.", "in maintenance": "на обслуживании",
  "in maintenance — drained from rotation": "на обслуживании — выведен из ротации",
  "Take this node out of rotation for maintenance (or put it back)": "Вывести сервер из ротации на обслуживание (или вернуть)",
  "Drain {name}?": "Вывести {name}?",
  "It leaves rotation, and new client configs carry a warning until you resume it.": "Сервер выйдет из ротации, а новые клиентские конфиги будут с предупреждением, пока вы его не вернёте.",
  "This exit carries {n}: {names}. Route them to:": "Через этот выход уходит групп: {n} — {names}. Перенаправить их на:",
  "Take out for maintenance": "Вывести на обслуживание",
  "Direct (local egress)": "Напрямую (локальный выход)",
  "Reassign egress": "Переназначить выход", "Egress reassigned": "Выход переназначен", "choose exit…": "выберите выход…", "Reassign": "Переназначить",
  "{n} group(s) egress here — move to:": "{n} групп(ы) выходят здесь — перенести на:",
  "Move egress of {n} group(s) from {name} to another exit and drain this node?": "Перенести выход {n} групп(ы) с {name} на другой сервер и вывести этот из ротации?",
  "Now": "Сейчас", "active now": "активен сейчас", "idle": "простаивает",
  "{n} active now": "активны сейчас: {n}",
  "“now” = last {m} min": "«сейчас» = за последние {m} мин",
  "no traffic in this window": "нет трафика за этот период",
  "no metrics in this window": "нет метрик за этот период",
  "peak {v}": "пик {v}",
  "CPU load": "Загрузка CPU", "Memory used": "Занято памяти",
  "Uploaded": "Отправлено", "Downloaded": "Получено", "Inbound": "Входящий", "Outbound": "Исходящий",
  "Throughput graph": "График пропускной способности",
  "Enable": "Включить", "Disable": "Выключить", "Apply": "Применить",
  "{n} selected": "выбрано: {n}", "Select all shown": "Выбрать все показанные",
  "Select this row for bulk actions": "Выбрать строку для массовых действий",
  "Move to group…": "Перенести в группу…", "Set expiry": "Задать срок", "Clear expiry": "Снять срок",
  "Clear selection": "Снять выделение",
  "{n} updated": "обновлено: {n}", "{n} failed": "ошибок: {n}",
  "{label} {n} selected?": "{label} выбранные ({n})?",
  // ---- Overview i18n tails + statuses (audit §9.2) ----
  "load": "загрузка", "cores": "ядер", "plan": "план", "d": "д", "add…": "добавить…",
  "applied": "применено", "no-change": "без изменений", "succeeded": "успешно",
  "failed": "не удалось", "running": "выполняется", "error": "ошибка",
  "skipped": "пропущено", "pending": "ожидание", "queued": "в очереди",
  // ---- form validation + empty-state hints (audit §9.4) ----
  "{label} is required": "Поле «{label}» обязательно",
  "— create a group first —": "— сначала создайте группу —",
  "No groups yet — add a group on the Groups tab before creating users.":
    "Групп пока нет — создайте группу на вкладке «Группы», прежде чем заводить пользователей.",
  // ---- backend error messages (shown via notify; matched exactly or by ": " prefix) ----
  "a control-plane standby must be an exit node": "резерв управляющего сервера должен быть выходным сервером",
  "account creation is available to the owner only": "создавать аккаунты может только владелец",
  "the panel already has an account; new ones are added in Accounts": "у панели уже есть учётная запись — новые заводятся в разделе «Аккаунты»",
  "account management is available to the owner only": "управлять аккаунтами может только владелец",
  "account not found": "аккаунт не найден",
  "alert channel is not configured (enable it and set a chat id)": "канал оповещений не настроен (включите его и задайте chat id)",
  "authentication required": "требуется аутентификация",
  "backup alert bot is not configured (enable alerts, set the backup bot token and the alert chat)": "резервный бот оповещений не настроен (включите оповещения, задайте токен резервного бота и чат оповещений)",
  "backup channel is not configured (set a bot token and a backup chat id)": "канал копий не настроен (задайте токен бота и chat id для копий)",
  "Sent by": "Кем отправлять",
  "Choose automatically": "Выбрать автоматически",
  "The spare alerts bot": "Запасным ботом оповещений",
  "The management bot": "Ботом управления",
  "No spare alerts bot is set up, so the management bot will send them.": "Запасной бот оповещений не настроен — отправлять будет бот управления.",
  "The spare alerts bot is set up, so it will send them.": "Настроен запасной бот оповещений — отправлять будет он.",
  "No bot is set up yet, so nothing can be sent.": "Пока не настроен ни один бот — отправлять нечем.",
  "token accepted by Telegram; link your account to the bot to receive a test message": "Telegram принял токен; привяжите учётную запись к боту, чтобы получать тестовое сообщение",
  "test message sent to your chat with the bot": "тестовое сообщение отправлено вам в чат с ботом",
  "bot unreachable": "бот недоступен",
  "cannot create accounts in another scope": "нельзя создавать аккаунты в чужом скоупе",
  "cannot demote the last admin of a scope while members remain": "нельзя понизить последнего админа скоупа, пока в нём есть участники",
  "cannot demote the last owner": "нельзя понизить последнего владельца",
  "confirm must equal node_id (the UI confirmation step was not completed)": "подтверждение должно совпадать с node_id (шаг подтверждения в UI не выполнен)",
  "could not resolve the control-plane primary node": "не удалось определить основной управляющий сервер",
  "cross-scope view is available to the bootstrap owner only": "обзор всех скоупов доступен только владельцу установки",
  "current password is incorrect": "текущий пароль неверный",
  "decode body": "не удалось разобрать тело запроса",
  "domain management is available to admins only": "управлять доменами могут только администраторы",
  "domain not found": "домен не найден",
  "egress_target is drained — pick a node in rotation": "выбранный выход выведен из работы — укажите сервер в работе",
  "egress_target is required: groups egress through this exit — choose another exit or direct": "нужно указать, куда перевести выход: через этот сервер выходят группы — выберите другой выход или выход с входного сервера",
  "egress_target must be a different exit node or direct": "выход должен быть другим выходным сервером или выходом с входного сервера",
  "endpoint hostname is required for an entry node": "для входного сервера нужно доменное имя подключения",
  "entry query parameter is required": "требуется параметр entry",
  "generate reality keys": "не удалось сгенерировать ключи Reality",
  "geo databases are not enabled on this panel": "гео-базы на этой панели не включены",
  "invalid credentials": "неверные учётные данные",
  "invalid or expired session": "сессия недействительна или истекла",
  "job not found": "задача не найдена",
  "locale must be en or ru": "язык может быть en или ru",
  "make_standby requires an exit node (a standby is a control-plane replica + exit)": "make_standby требует выходной сервер (резерв — это реплика управления + выход)",
  "make_standby: could not resolve the control-plane primary to replicate from": "make_standby: не удалось определить основной сервер для репликации",
  "management bot is not configured": "бот управления не настроен",
  "member management requires an admin role": "управление участниками требует роли админа",
  "missing or invalid CSRF token": "отсутствует или неверный CSRF-токен",
  "name, public_ips and ssh.host are required": "обязательны name, public_ips и ssh.host",
  "network-wide rules are managed by admins": "правила уровня сети управляются администраторами",
  "no groups egress via that node": "через этот сервер не выходит ни одна группа",
  "no session": "сеанс не найден",
  "no single entry node to convert (expected one entry node holding the control plane)": "нет одиночного входного сервера для разделения (ожидался один вход с управляющим сервером)",
  "no such group in your scope": "в вашем скоупе нет такой группы",
  "no users selected": "не выбрано ни одного пользователя",
  "node management is available to admins only": "управлять серверами могут только администраторы",
  "node not found": "сервер не найден",
  "node query parameter is required": "требуется параметр node",
  "node_id is required": "требуется node_id",
  "none of the databases could be updated": "ни одну из баз обновить не удалось",
  "only admins can install a node as a control-plane standby": "только админы могут добавить сервер как резерв управления",
  "operators cannot create nodes directly — use Add server": "операторы не могут создавать серверы напрямую — используйте «Добавить сервер»",
  "operators manage clients through the Telegram bot, not the panel": "оператор управляет клиентами через бота в Telegram, а не через панель",
  "primary and standby must both have a public IP": "у основного и резервного сервера должен быть публичный IP",
  "reality_sni is required (the SNI A's new Reality inbound borrows)": "обязателен reality_sni (SNI, который займёт новый Reality-inbound сервера A)",
  "reality_sni is required for an exit node": "для выходного сервера обязателен reality_sni",
  "remote provisioning is not configured on this panel": "удалённый провижининг не настроен на этой панели",
  "role must be admin or operator": "роль должна быть admin или operator",
  "role must be entry or exit": "роль должна быть entry или exit",
  "send failed": "не удалось отправить",
  "session expired": "сеанс истёк",
  "session no longer valid": "сессия больше недействительна",
  "standby node has no agent address": "у резервного сервера нет адреса агента",
  "target (IP or domain) is required": "требуется цель (IP или домен)",
  "target exit is drained — pick a node in rotation": "выбранный выход выведен из работы — укажите сервер в работе",
  "target_exit_id is not an exit node": "указанный сервер не является выходным",
  "target_exit_id must be a different exit node": "нужно выбрать другой выходной сервер",
  "that client belongs to another scope": "этот клиент принадлежит другому скоупу",
  // what an exit carries, on its card and in the drain question
  "What leaves through this exit": "Что уходит через этот выход",
  // an operation in the journal: one action, its lines in several scopes
  "the whole operation ({n})": "вся операция ({n})",
  "hide the operation": "свернуть операцию",
  "This operation wrote nothing else.": "Больше эта операция ничего не записала.",
  "{n} in other scopes, not shown here.": "Ещё записей в других скоупах: {n} — их здесь не видно.",
  "an operation id is required": "нужен идентификатор операции",
  "All the rules that name this exit": "Все правила, которые ссылаются на этот выход",
  "Filter by name, group or exit": "Фильтр по названию, группе или выходу",
  "Nothing points at this exit — taking it out changes nothing.": "На этот выход ничто не ссылается — вывод из работы ничего не изменит.",
  "moves to another exit in rotation, or leaves from the entry node": "перейдёт на другой выход в работе, а если такого нет — напрямую через входной сервер",
  "its own exit still carries this, but if that one goes too the traffic is refused": "сейчас работает основной выход правила, но если откажет и он — трафик будет отклонён",
  "moves to {name}": "перейдёт на {name}",
  "the traffic is refused": "трафик будет отклонён",
  "leaves from the entry node": "уйдёт напрямую через входной сервер",
  "its exit": "выход правила",
  "everyone else": "для остальных",
  "fallback": "запасной",
  "a group": "группа",
  "a rule": "правило",
  "This exit is named by {n}: {names}. Each then follows what it declares.": "На этот выход ссылается правил: {n} — {names}. Каждое пойдёт тем путём, который в нём указан.",
  // a save that the database refused, and the form refusals the server names
  // in words of its own so they can be looked up here
  "that id is already in use": "такой идентификатор уже занят",
  "that username is already taken": "такое имя пользователя уже занято",
  "this group still has clients — reassign or remove them first": "в группе ещё есть клиенты — сначала переведите или удалите их",
  "this item is still referenced by others — remove or reassign them first": "на запись ещё есть ссылки — сначала уберите или переназначьте их",
  "a required field is missing": "не заполнено обязательное поле",
  "a value is invalid": "недопустимое значение",
  "the change could not be saved": "не удалось сохранить изменение",
  "the username cannot be empty": "имя пользователя не может быть пустым",
  "the username cannot start or end with a space": "имя пользователя не может начинаться или заканчиваться пробелом",
  "the username has a character in it that is not allowed": "в имени пользователя есть недопустимый символ",
  "a client has to belong to a group": "клиент должен состоять в группе",
  "a group has to have a name": "у группы должно быть название",
  "a rule has to say what traffic it catches": "правило должно указывать, какой трафик оно ловит",
  "the figures of a plan cannot be negative": "значения тарифа не могут быть отрицательными",
  "the payment term has to be 1, 3, 6 or 12 months": "срок оплаты — 1, 3, 6 или 12 месяцев",
  "that group belongs to another scope": "эта группа принадлежит другому скоупу",
  "that policy belongs to another scope": "это правило принадлежит другому скоупу",
  "the source holds the same copy — nothing to update": "в источнике та же копия — обновлять нечего",
  "the standby must differ from the primary": "резерв должен отличаться от основного сервера",
  "this action is available to admins only": "это действие доступно только администраторам",
  "too many attempts; try again later": "слишком много попыток, попробуйте позже",
  "too many failed login attempts; try again later": "слишком много неудачных попыток входа; попробуйте позже",
  "unknown channel": "неизвестный канал",
  "user query parameter is required": "требуется параметр user",
  "username and a valid recovery code are required": "нужны логин и действующий код восстановления",
  "username required and password must be at least 8 chars": "нужно имя пользователя, пароль не короче 8 символов",
  "variant 1 (new hostname) requires at least one domain for B's certificate": "для нового адреса подключения нужен хотя бы один домен — на него выпускается сертификат второго сервера",
  "variant 2 (reuse domain) needs A to already serve at least one domain to hand over": "чтобы сохранить адрес подключения, у текущего сервера должен быть хотя бы один домен — передавать нечего",
  "variant must be 1 (new hostname) or 2 (reuse domain)": "выберите: новый адрес подключения или сохранить текущий",
  "you cannot delete the account you are signed in as": "нельзя удалить аккаунт, под которым вы вошли",
  "you cannot manage members of that scope": "вы не можете управлять участниками этого скоупа",
  "you do not own this node": "этот сервер вам не принадлежит",
};

/* ---------------- themed confirm ---------------- */
// uiConfirm replaces window.confirm with an in-app modal that follows the dark
// theme and is localized. It returns a promise<boolean>. A single host lives in
// App; if it isn't mounted yet (shouldn't happen in practice) it degrades to the
// native confirm so a confirmation is never silently skipped.
let _confirmHost = null;
function uiConfirm(opts) {
  return new Promise((resolve) => {
    if (!_confirmHost) { resolve(window.confirm([opts.title, ...(opts.lines || [])].join("\n"))); return; }
    _confirmHost({ ...opts, resolve });
  });
}
// navigateTo lets a deep widget (e.g. a Groups-table cell) jump to another top
// tab with a pre-filled table filter, without threading setTab through every
// generic component. App registers the host; a no-op before mount.
let _navHost = null;
function navigateTo(tab, filter) { if (_navHost) _navHost(tab, filter || ""); }
// useModal gives every dialog the same keyboard affordances mouse users already
// have: Escape closes it, focus moves into the dialog on open (the first field,
// or the ✕ when there are no fields), and Tab/Shift-Tab are trapped so focus
// can't wander to the page behind. On close, focus goes back to whatever opened
// the dialog, so the keyboard lands where it left off instead of at the top of
// the page. The returned ref goes on the .modal element (also tagged
// role=dialog/aria-modal for screen readers).
//
// isDirty, when given, is asked before an Escape or a click outside throws the
// form away; ref.close is the same question for the ✕ and Cancel buttons.
//
// Escape belongs to the innermost thing that is open. The window listens in the
// capture phase (it has to, or a stray focus would swallow the key) so a
// suggestion list or a rename box cannot claim the key by listening closer to
// it. They register here instead, and are asked first, innermost first.
const ESCAPES = [];
function useEscape(handler) {
  const h = useRef(handler);
  h.current = handler;
  useEffect(() => {
    const fn = () => h.current();
    ESCAPES.push(fn);
    return () => { const i = ESCAPES.indexOf(fn); if (i >= 0) ESCAPES.splice(i, 1); };
  }, []);
}
function escapeHandled() {
  for (let i = ESCAPES.length - 1; i >= 0; i--) { try { if (ESCAPES[i]()) return true; } catch { /* a closed one is no answer */ } }
  return false;
}
function useModal(onClose, isDirty, options = {}) {
  const opts = useRef(options);
  opts.current = options;
  const ref = useRef(null);
  const dirty = useRef(isDirty);
  dirty.current = isDirty;
  const asking = useRef(false);
  ref.close = async () => {
    if (!dirty.current || !dirty.current()) { onClose(); return; }
    if (asking.current) return; // the question is already on screen
    asking.current = true;
    const yes = await uiConfirm({
      variant: "compact",
      title: t("Close without saving?"),
      lines: [t("Unsaved changes will be deleted.")],
      confirmLabel: t("Close"),
      ...(opts.current.confirm || {}),
    });
    asking.current = false;
    if (yes) onClose();
  };
  useEffect(() => {
    const focusable = () => ref.current
      ? Array.from(ref.current.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])')).filter(el => el.offsetParent !== null)
      : [];
    const onKey = (e) => {
      // Only the top dialog handles keys; confirmations may be mounted before
      // the editor in the DOM but are drawn above it.
      const dialogs = Array.from(document.querySelectorAll('[role="dialog"][aria-modal="true"]'));
      dialogs.sort((a, b) => Number(getComputedStyle(a.closest(".overlay") || a).zIndex) - Number(getComputedStyle(b.closest(".overlay") || b).zIndex));
      if (dialogs.at(-1) !== ref.current) return;
      if (e.key === "Escape") {
        if (asking.current) return; // belongs to the question on top of us
        e.preventDefault(); e.stopPropagation();
        if (opts.current.escapeLayers !== false && escapeHandled()) return; // a list or a rename box was open
        ref.close(); return;
      }
      if (e.key !== "Tab") return;
      const els = focusable();
      if (els.length === 0) return;
      const first = els[0], last = els[els.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    const opener = document.activeElement;
    const els = focusable();
    // A form can say where it starts; otherwise it starts at its first control.
    const first = ref.current && ref.current.querySelector("[data-autofocus]");
    const target = (first && first.offsetParent !== null && first)
      || els.find(el => ["INPUT", "SELECT", "TEXTAREA"].includes(el.tagName)) || els[0];
    if (target) setTimeout(() => { try { target.focus(); } catch {} }, 0);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      if (opener && opener.isConnected && opener.focus) setTimeout(() => { try { opener.focus(); } catch {} }, 0);
    };
  }, []);
  return ref;
}

function ConfirmModal({ req, onClose }) {
  // With req.select the dialog also carries a choice: confirming resolves the
  // picked value (a string) instead of true; cancelling always resolves false.
  // req.alt adds a second way to say yes, "save it, but leave the client off",
  // which resolves "alt". Three answers need three buttons: hiding one of them
  // behind Cancel would make declining the extra step read as declining the save.
  const [sel, setSel] = useState(req.select ? (req.select.value || "") : "");
  const done = (v) => { onClose(); req.resolve(v); };
  const ref = useModal(() => done(false), null, { escapeLayers: false });
  if (req.variant === "compact") return html`
    <div class="overlay compact-confirm-overlay" onClick=${e => e.target === e.currentTarget && done(false)}>
      <div class="modal compact-confirm" ref=${ref} role=dialog aria-modal=true aria-labelledby=compact-confirm-title aria-describedby=compact-confirm-text>
        <h2 id=compact-confirm-title>${req.title}</h2>
        <div id=compact-confirm-text>${(req.lines || []).map((line, i) => html`<p key=${i}>${line}</p>`)}</div>
        <div class=buttons>
          <button class=ghost data-autofocus onClick=${() => done(false)}>${t("Cancel")}</button>
          <button onClick=${() => done(true)}>${req.confirmLabel || t("OK")}</button>
        </div>
      </div>
    </div>`;
  return html`
    <div class="overlay confirm-overlay" onClick=${e => e.target.classList.contains("overlay") && done(false)}>
      <div class=modal ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${req.title}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${() => done(false)}>✕</button></header>
        <div class=body>
          ${req.warn && html`<div class="note warn">⚠ ${req.warn}</div>`}
          ${(req.lines || []).map((l, i) => html`<p key=${i} style=${{ margin: ".35rem 0", lineHeight: 1.45 }}>${l}</p>`)}
          ${req.select && html`<select value=${sel} onChange=${e => setSel(e.target.value)} style=${{ width: "100%", marginTop: ".4rem" }}>
            ${req.select.options.map(o => html`<option key=${o.value} value=${o.value}>${o.label}</option>`)}
          </select>`}
        </div>
        <div class=foot>
          ${req.blocked
            ? html`<button class=ghost data-autofocus onClick=${() => done(false)}>${t("Close")}</button>`
            : html`<${React.Fragment}>
                <button class=ghost data-autofocus onClick=${() => done(false)}>${t("Cancel")}</button>
                ${req.alt && html`<button class=ghost onClick=${() => done("alt")}>${req.alt.label}</button>`}
                <button class=${req.danger ? "danger" : ""} onClick=${() => done(req.select ? sel : true)}>${req.confirmLabel || t("OK")}</button>
              <//>`}
        </div>
      </div>
    </div>`;
}

// Toast notification. Success/info auto-dismiss after a few seconds; the timer
// pauses while hovered so a long reconcile result can be read; errors do not
// auto-hide (they stay until dismissed). A close button is always available.
function Toast({ toast, setToast }) {
  const { id, kind, msg } = toast;
  const [paused, setPaused] = useState(false);
  const close = () => setToast(null);
  useEffect(() => {
    if (kind === "err" || paused) return;
    const h = setTimeout(() => setToast(null), 4500);
    return () => clearTimeout(h);
  }, [id, kind, paused]);
  return html`<div class=${"toast " + (kind || "")}
      onMouseEnter=${() => setPaused(true)} onMouseLeave=${() => setPaused(false)}>
    <button class=toast-x aria-label=${t("Close")} title=${t("Close")} onClick=${close}>✕</button>
    <div class=toast-msg>${msg}</div>
  </div>`;
}

/* ---------------- helpers: ids, names, countries ---------------- */
// Dates and clocks are written the same way whatever the interface language:
// digits, day before month, as the date fields ask for. A locale that puts the
// month first reads as a different day, not as a different style.
const pad2 = (n) => String(n).padStart(2, "0");
const dayMonth = (d) => pad2(d.getDate()) + "/" + pad2(d.getMonth() + 1);
const clock = (d, secs) => pad2(d.getHours()) + ":" + pad2(d.getMinutes()) + (secs ? ":" + pad2(d.getSeconds()) : "");
const slug = (s) => String(s || "").toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 24);
const rand4 = () => Math.random().toString(16).slice(2, 6);
// A password the operator can see and copy, rather than one the server invents
// and nobody reads back. No look-alike characters: these get typed by hand off a
// screen often enough that 0/O and 1/l/I are a support call waiting to happen.
function randomSecret(len = 20) {
  const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const buf = new Uint32Array(len);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => alphabet[n % alphabet.length]).join("");
}
const genId = (base, fb) => (slug(base) || fb) + "-" + rand4();
const countryCode = (t) => { const m = String(t).match(/\(([a-zA-Z]{2})\)\s*$/); return (m ? m[1] : t).toLowerCase().trim(); };
function nameOf(state, kind, id) {
  if (!id) return "";
  const it = (state[kind] || []).find(x => x.id === id);
  return it ? (it.name || it.display_name || it.username || it.hostname || id) : id;
}
// What a server is carrying today, for the screen where its role is changed.
// The role decides what the machine answers with on :443, and these are the
// things that were told to use it, which is the point of asking for the change in a
// place of its own rather than in a select next to the name.
function roleChangeNote(v, st) {
  const id = v.id;
  const was = ((st.nodes || []).find(n => n.id === id) || {}).public_role || "";
  const domains = (st.domains || []).filter(d => d.node_id === id).length;
  const groups = (st.groups || []).filter(g => g.default_exit_id === id).length;
  const rules = (st.route_policies || []).filter(p => exitRefs(p, id).any).length;
  const carries = [];
  if (domains) carries.push(tf("{n} pointing at it", { n: counted(domains, "domain") }));
  if (groups) carries.push(tf("{n} leaving through it", { n: counted(groups, "group") }));
  if (rules) carries.push(tf("{n} naming it", { n: counted(rules, "rule") }));
  if (v.public_role === was) {
    return carries.length ? carries.join(" · ") : t("Nothing is pointed at this server yet.");
  }
  const lines = [t(v.public_role === "exit"
    ? "It stops taking connections and starts letting traffic out."
    : "It stops letting traffic out and starts taking connections.")];
  if (carries.length) lines.push(tf("Today {what} — each of those needs pointing somewhere else.", { what: carries.join(", ") }));
  lines.push(t("The servers are reconfigured at the next sync."));
  return html`${lines.map((l, i) => html`<div key=${i}>${l}</div>`)}`;
}

// endpoint/decoy is what the record stores; what the name is for is what the
// panel says out loud.
function purposeLabel(p) { return p === "decoy" ? t("Cover site") : t("VPN connection"); }

// The part size is stored in bytes and asked for in MiB: nobody types 47185920.
// 0 stays 0: it means "one part", not "zero bytes".
function mib(bytes) { const n = Number(bytes) || 0; return n ? Math.round(n / MIB) : ""; }
function bytesFromMiB(v) { const n = Number(String(v).trim()); return !n || n < 0 ? 0 : Math.round(n * MIB); }

// senderHint says out loud which bot will carry the copies. Left on automatic,
// the choice depends on which bots happen to be configured, and an installation
// running a single bot needs to be told that the one it has will do the job;
// otherwise the setting reads as a requirement for a second bot it doesn't have.
function senderHint(b) {
  if (b.sender) return "";
  switch (b.sender_resolved) {
    case "bot": return t("No spare alerts bot is set up, so the management bot will send them.");
    case "alert": return t("The spare alerts bot is set up, so it will send them.");
    default: return t("No bot is set up yet, so nothing can be sent.");
  }
}

// policyConditions is the rule's match, falling back to the four per-kind fields
// for a rule saved before conditions existed. One reader, so the table, the
// summary and the shadow check can never disagree about what a rule catches.
function policyConditions(p) {
  if (Array.isArray(p.conditions) && p.conditions.length) return p.conditions;
  const out = [];
  const add = (kind, vals) => { const v = (vals || []).filter(x => String(x).trim()); if (v.length) out.push({ join: "and", kind, values: v }); };
  add("domain", p.match_domains); add("geoip", p.match_geoip);
  add("geosite", p.match_geosite); add("cidr", p.match_cidrs);
  if (out.length && (p.exclude_domains || []).length) out.push({ join: "except", kind: "domain", values: p.exclude_domains });
  return out;
}

// condToken renders one value the way it is written in a rule.
const condToken = (kind, v) => (kind === "geoip" || kind === "geosite" ? kind + ":" + v : String(v));

// Match tokens of a route policy, normalized. Exclusions are not tokens: they
// say what a rule does not catch, and counting them as catches would make the
// shadow warning fire on the one rule that was written to avoid the overlap.
function policyMatchTokens(p) {
  const out = [];
  for (const c of policyConditions(p)) {
    if (c.join === "except") continue;
    for (const v of c.values || []) out.push(condToken(c.kind, v));
  }
  return out.map(x => String(x).trim().toLowerCase()).filter(Boolean);
}

// condRows drops the lines that hold nothing yet, and a leading exclusion: a
// rule that opens with "but not" would catch everything else, which validation
// refuses on both sides.
function condRows(conds) {
  const rows = (conds || []).filter(c => (c.values || []).some(v => String(v).trim() !== ""));
  return rows.length && rows[0].join === "except" ? rows.slice(1) : rows;
}

// condAlternatives reads one line as the alternatives it holds: "a.com or b.com".
function condAlternatives(c, brackets) {
  const vals = (c.values || []).map(v => condToken(c.kind, v)).filter(x => String(x).trim() !== "");
  const text = vals.join(" " + t("or") + " ");
  return brackets && vals.length > 1 ? "(" + text + ")" : text;
}

// condSentence reads a rule's match back as a line, folding it the way the
// backend does (model.MatchTree): top to bottom, each line joining to everything
// above it. Every join is bracketed as it is made, so "a and b or c" is written
// out as "(a and b) or c" rather than left to be guessed.
function condSentence(conds) {
  const rows = condRows(conds);
  if (!rows.length) return "";
  const many = rows.length > 1;
  let out = condAlternatives(rows[0], many);
  let last = null;
  for (const c of rows.slice(1)) {
    const join = c.join === "or" || c.join === "except" ? c.join : "and";
    const word = join === "or" ? t("or") : join === "except" ? t("but not") : t("and");
    // Brackets go where the reading turns. A run of the same word does not need
    // them; a change of word does, and gets them as the line is built.
    const head = last === null || (join === last && join !== "except") ? out : "(" + out + ")";
    out = head + " " + word + " " + condAlternatives(c, true);
    last = join;
  }
  return out;
}

// Two policies' group scopes can match the same traffic when either is "all users" ("").
const policyGroupsOverlap = (a, b) => !a || !b || a === b;

// The order the nodes read the rules in: the network's rules, then a scope's; inside
// one level a rule that answers for everyone else leads, then the higher
// priority, ties settled by id. The table and the live summary both read it from
// here, since neither can promise an order the nodes do not follow.
//
// This is a copy of ComparePolicies in internal/core/model, which is what the
// renderer and the bot use; there is no build step here to share the one
// definition. TestPanelPolicyOrderMatchesTheModel pins this copy, so changing it
// fails a Go test until the two are read side by side again.
const TIER_RANK = { fleet: 0, exit: 1 };
function policyOrder(a, b) {
  const rank = (x) => (TIER_RANK[x.tier] === undefined ? TIER_RANK.exit : TIER_RANK[x.tier]);
  const band = (x) => (x.others_action ? 0 : 1);
  const ia = String(a.id || ""), ib = String(b.id || "");
  return rank(a) - rank(b) || band(a) - band(b)
    || (Number(b.priority) || 0) - (Number(a.priority) || 0)
    || (ia < ib ? -1 : ia > ib ? 1 : 0);
}
const COUNTRIES = [["ru","Russia"],["us","United States"],["de","Germany"],["nl","Netherlands"],["gb","United Kingdom"],["fr","France"],["fi","Finland"],["se","Sweden"],["pl","Poland"],["ua","Ukraine"],["ir","Iran"],["cn","China"],["hk","Hong Kong"],["jp","Japan"],["sg","Singapore"],["kr","South Korea"],["in","India"],["tr","Turkey"],["ae","United Arab Emirates"],["ca","Canada"],["br","Brazil"],["au","Australia"],["it","Italy"],["es","Spain"],["ch","Switzerland"],["at","Austria"],["cz","Czechia"],["ro","Romania"],["kz","Kazakhstan"],["by","Belarus"],["ge","Georgia"],["am","Armenia"],["az","Azerbaijan"],["lt","Lithuania"],["lv","Latvia"],["ee","Estonia"],["no","Norway"],["dk","Denmark"],["be","Belgium"],["ie","Ireland"],["pt","Portugal"],["gr","Greece"],["bg","Bulgaria"],["rs","Serbia"],["md","Moldova"],["il","Israel"],["sa","Saudi Arabia"],["eg","Egypt"],["za","South Africa"],["mx","Mexico"],["ar","Argentina"],["th","Thailand"],["vn","Vietnam"],["id","Indonesia"],["my","Malaysia"],["ph","Philippines"],["tw","Taiwan"],["uz","Uzbekistan"],["kg","Kyrgyzstan"],["tj","Tajikistan"],["hu","Hungary"],["sk","Slovakia"],["hr","Croatia"]];
// Common sing-geosite / v2ray geosite categories (domain-category lists).
// Curated list of REAL sing-geosite rule-set categories (verified to exist as
// geosite-<name>.srs in SagerNet/sing-geosite). Free text is still allowed; an
// unknown category surfaces as a clear reconcile error.
const GEOSITE = ["google","youtube","telegram","signal","whatsapp","discord","twitter","x","facebook","instagram","meta","tiktok","netflix","disney","hbo","spotify","twitch","reddit","linkedin","pinterest","github","gitlab","openai","anthropic","cloudflare","apple","microsoft","amazon","oracle","paypal","steam","epicgames","line","bbc","cnn","bilibili","baidu","cn","yandex","vk","rutracker","category-ru","category-gov-ru","category-ads","category-ads-all","category-porn","category-games","category-media","category-social-media-!cn","category-public-tracker","category-dev","category-scholar-!cn","category-ai-!cn","geolocation-cn","geolocation-!cn","private"];

// GEO_CATALOG holds the category names the sources offered at the last check, so
// the rule form suggests what actually exists rather than a list written by hand
// months ago. It is a module-level cache because the form that needs it sits far
// from the fetch, and because it is the same answer for every form on the page.
let GEO_CATALOG = { geoip: [], geosite: [] };

// noteGeoCatalog takes the category names out of a /api/rulesets payload, so the
// Geo databases tab and the app's own load share one path into the cache and a
// check on that tab refreshes the rule form's suggestions at once.
function noteGeoCatalog(d) {
  const by = {};
  ((d && d.sources) || []).forEach(src => { by[src.kind] = (src.catalog || []).map(x => x.replace(/^geo(ip|site)-/, "")); });
  GEO_CATALOG = { geoip: by.geoip || [], geosite: by.geosite || [] };
}

async function loadGeoCatalog() {
  try { noteGeoCatalog(await api("GET", "/api/rulesets")); }
  catch { /* the shortlist below stands in; a form must offer something */ }
}

// geositeHint says where the suggestions come from, so an operator who does not
// find a category knows whether the list is stale or the category is not there.
function geositeHint() {
  const n = GEO_CATALOG.geosite.length;
  return n ? tf("{n} categories in the source, or type your own", { n })
           : t("a short built-in list; the full one arrives after a check on the Geo databases tab");
}

// geositeOptions puts the everyday categories first and the rest of the
// catalogue after them: a common one is a keystroke away, a rare one is still
// there. Before any check has run there is no catalogue, and the shortlist is
// the whole answer.
function geositeOptions() {
  const all = GEO_CATALOG.geosite;
  if (!all.length) return GEOSITE;
  const have = new Set(all), short = GEOSITE.filter(g => have.has(g)), seen = new Set(short);
  return short.concat(all.filter(g => !seen.has(g)));
}

/* ---------------- entity field schemas ---------------- */
// A standby is considered "far behind" past this many bytes of WAL lag, generous
// so a brief catch-up after a restart doesn't flap the warning dot.
const REPL_LAG_WARN_BYTES = 128 * 1024 * 1024;

// replWarning returns a warning message if a node's replication slot is unhealthy
// (down / missing / far behind), else "". The data plane is unaffected by any of
// these, as they are HA-readiness concerns, so callers render them yellow.
function replWarning(rep) {
  if (!rep) return "";
  if (rep.missing) return t("replication slot missing (never provisioned or dropped)");
  if (!rep.active) return t("replica not streaming (Postgres down or disconnected)");
  if (rep.bytes_behind != null && rep.bytes_behind > REPL_LAG_WARN_BYTES) return tf("replica {n} behind", { n: humanBytes(rep.bytes_behind) });
  return "";
}

// nodeStatus condenses a node into one state word plus the colour that carries it,
// from the agent's health, the external :443 edge probe and standby replication
// health (all from /api/overview). The rule is "does it break what a VPN user
// sees?": red = data plane affected (agent down/degraded, or entry :443
// unreachable); yellow = data plane fine but an HA concern (a standby's replica is
// down/behind); green = all good; grey = no report yet. A drained node says so
// instead: it is out of rotation on purpose, not broken.
function nodeStatus(node, ov) {
  const health = (ov && ov.health) || node.health || "unknown";
  const edge = ov && ov.edge;
  const warn = replWarning(ov && ov.replication);
  // rank orders a column of states worst-first, which is why anyone sorts one.
  if (node.maintenance) return { rank: 1, color: "var(--warn)", label: "in maintenance", title: t("in maintenance — drained from rotation") };
  // Red first: a data-plane problem outranks any HA warning.
  if (edge && edge.ok === false) return { rank: 0, color: "var(--err)", label: "degraded", title: t("edge :443 unreachable") + (edge.error ? " — " + edge.error : "") };
  if (health === "degraded" || health === "unhealthy") return { rank: 0, color: "var(--err)", label: "degraded", title: t("the agent is not answering — the server is unreachable") };
  if (health === "healthy" && warn) return { rank: 1, color: "var(--warn)", label: "healthy", title: warn };
  if (health === "healthy") {
    const extra = [edge && edge.ok ? t("edge :443 reachable") : "", (ov && ov.replication) ? t("replica streaming") : ""].filter(Boolean);
    return { rank: 2, color: "var(--ok)", label: "healthy", title: t("status: OK") + (extra.length ? " · " + extra.join(" · ") : "") };
  }
  return { rank: 3, color: "var(--text-meta)", label: "unknown", title: t("no report yet") };
}

function statusDot(node, ov) {
  const st = nodeStatus(node, ov);
  return html`<span title=${st.title} style=${{ display: "inline-block", width: "10px", height: "10px", borderRadius: "50%", background: st.color }}></span>`;
}

// The dot alone says a node is off without saying what "off" means, so the state
// column spells it: colour for the glance, word for the answer.
function StateCell({ node, ov }) {
  const st = nodeStatus(node, ov);
  return html`<span class=nstate title=${st.title}><span class=dot style=${{ background: st.color }}></span>${t(st.label)}</span>`;
}

// An agent is always reached on its own port, so the host repeats the address
// already in the row. Show the port; keep the whole thing in the tooltip.
function agentPort(addr) {
  const m = String(addr || "").match(/:(\d+)$/);
  return m ? ":" + m[1] : String(addr || "");
}

// Groups whose default egress is this exit, since draining it has to send them
// somewhere, so the confirmation asks where.
function groupsEgressingVia(state, node) {
  return ((state && state.groups) || []).filter(g => g.default_exit_id === node.id);
}

// The ways one rule can point traffic at one exit: as its own action, as the
// answer it gives everyone else, or as the exit it falls back to. The three are
// asked together because any one of them can be the only thing pointing at a
// node; a rule that names an exit as nothing but its fallback carries no
// traffic today and hands it everything the moment its own exit goes.
//
// This is RoutePolicy.NamesExit in internal/core/model said again in the
// browser. The two have to answer the same: that one is what takes a dead exit
// out of rotation, this one is what the panel promises beforehand, and an
// operator who is told nothing depends on a node must not be told it by a page
// that asks a narrower question than the controller does.
function exitRefs(pol, nodeId) {
  const none = { action: false, others: false, fallback: false, any: false };
  if (!nodeId || !pol || pol.disabled) return none;
  const r = {
    action: pol.action === "exit" && pol.exit_node_id === nodeId,
    others: pol.others_action === "exit" && pol.others_exit_id === nodeId,
    fallback: pol.fallback_kind === "exit" && pol.fallback_exit_id === nodeId,
  };
  r.any = r.action || r.others || r.fallback;
  return r;
}

// deleteBlocker names the reference the database will refuse to break, so the
// refusal arrives before the button rather than as an error afterwards; the
// foreign keys it reads are RESTRICT ones (a rule's exit, a group's clients).
function deleteBlocker(cfg, it, state) {
  if (cfg.singular === "node") {
    const rules = dependentsOfExit(state, it.id).rules;
    if (rules.length > 0) return tf("Traffic still routes through this server: {names}. Point those rules elsewhere first.",
      { names: rules.map(r => r.pol.name || r.pol.id).join(", ") });
  }
  if (cfg.singular === "group") {
    const users = ((state && state.users) || []).filter(u => u.group_id === it.id);
    if (users.length > 0) return tf("There are still {n} in this group. Move the clients to another group first.", { n: plural(users.length, "client") });
  }
  return "";
}

// deleteLines says what else goes at the same time. These are cascades, not
// guesses: a server's domains and a group's rules are deleted with their parent,
// and a group that loses its exit starts leaving from the entry server.
function deleteLines(cfg, it, state) {
  const lines = [];
  if (cfg.singular === "node") {
    const doms = ((state && state.domains) || []).filter(d => d.node_id === it.id);
    if (doms.length > 0) lines.push(tf("Its domains and their certificates go with it: {names}",
      { names: doms.map(d => d.hostname).join(", ") }));
    const gs = groupsEgressingVia(state, it);
    if (gs.length > 0) lines.push(tf("These groups leave by it and will go out from the entry server instead: {names}",
      { names: gs.map(g => g.name || g.id).join(", ") }));
    lines.push(t("Its traffic history goes with it."));
  } else if (cfg.singular === "group") {
    const pols = ((state && state.route_policies) || []).filter(p => p.applies_to_group_id === it.id);
    if (pols.length > 0) lines.push(tf("The rules aimed at this group go with it: {names}",
      { names: pols.map(p => p.name || p.id).join(", ") }));
  } else if (cfg.singular === "client") {
    lines.push(t("Access ends at once, and the traffic history goes with it."));
  } else if (cfg.singular === "domain") {
    lines.push(t("Its certificate goes with it, and the server stops answering for this name."));
  }
  lines.push(t("This cannot be undone."));
  return lines;
}

// Everything that would have to be answered for if this exit went away: the
// groups that leave by it, and the rules that name it.
function dependentsOfExit(state, nodeId) {
  const rules = [];
  ((state && state.route_policies) || []).forEach(pol => {
    const refs = exitRefs(pol, nodeId);
    if (refs.any) rules.push({ pol, refs });
  });
  return { groups: groupsEgressingVia(state, { id: nodeId }), rules };
}

// What one rule says should happen while this exit is out of rotation. It is the
// rule's own declaration, read the same way the renderer reads it (policyExit):
// a rule with no answer of its own leaves from the entry node.
function whenExitIsOut(pol, refs, state) {
  if (refs.fallback && !refs.action && !refs.others) {
    // Its own exit still carries the traffic; what it loses is the safety net.
    return t("its own exit still carries this, but if that one goes too the traffic is refused");
  }
  if (pol.fallback_kind === "exit") {
    return tf("moves to {name}", { name: exitNodeLabel(state, pol.fallback_exit_id) });
  }
  if (pol.fallback_kind === "block") return t("the traffic is refused");
  return t("leaves from the entry node");
}

// Which half of a rule names the exit, in the words the rule form uses.
function refWords(refs) {
  const out = [];
  if (refs.action) out.push(t("its exit"));
  if (refs.others) out.push(t("everyone else"));
  if (refs.fallback) out.push(t("fallback"));
  return out.join(" · ");
}

function exitNodeLabel(state, id) {
  const n = ((state && state.nodes) || []).find(x => x.id === id);
  return (n && (n.name || n.id)) || id;
}

// Who a drain would actually cut off. An entry knows its own clients (the traffic
// samples carry the entry they came through); an exit does not, so there it is the
// online clients of the groups that egress through it.
function clientsOnlineVia(node, state, overview, traffic) {
  if (node.public_role === "entry") {
    const ov = ((overview && overview.nodes) || []).find(n => n.id === node.id);
    return ov ? (ov.active_clients || 0) : 0;
  }
  const ids = new Set(groupsEgressingVia(state, node).map(g => g.id));
  if (ids.size === 0) return 0;
  const groupOf = new Map(((state && state.users) || []).map(u => [u.id, u.group_id]));
  return ((traffic && traffic.users) || []).filter(u => u.active && ids.has(groupOf.get(u.user_id))).length;
}

// Draining is a control-plane action, not a saved field, so it belongs to the row
// rather than to the edit form's footer, where it sat one slip away from Cancel.
// The confirmation says who is online through the node, and, when groups egress
// through this exit, makes the operator name where that traffic goes instead of
// dropping it to direct implicitly.
async function drainNode({ node, state, overview, traffic, path, reload, notify }) {
  const on = !node.maintenance;
  const body = { maintenance: on };
  if (on) {
    const name = node.name || node.id;
    const online = clientsOnlineVia(node, state, overview, traffic);
    const warn = online > 0 ? tf("{clients} online through this node right now — draining it cuts them off.", { clients: counted(online, "client") }) : null;
    const { groups: groupsHere, rules } = dependentsOfExit(state, node.id);
    // A rule that names this exit is not reassigned; it follows what it
    // declares for its exit being out. Naming them is the difference between an
    // operator knowing that and finding out afterwards.
    const rulesLine = rules.length === 0 ? null
      : tf("This exit is named by {n}: {names}. Each then follows what it declares.",
          { n: counted(rules.length, "rule"), names: rules.map(r => r.pol.name || r.pol.id).join(", ") });
    if (groupsHere.length > 0) {
      const others = ((state && state.nodes) || []).filter(n => n.id !== node.id && n.public_role === "exit" && !n.maintenance);
      const target = await uiConfirm({
        title: tf("Drain {name}?", { name }),
        warn,
        lines: [tf("This exit carries {n}: {names}. Route them to:",
          { n: counted(groupsHere.length, "group"), names: groupsHere.map(g => g.name || g.id).join(", ") }),
          ...(rulesLine ? [rulesLine] : [])],
        confirmLabel: t("Take out for maintenance"),
        select: {
          value: "direct",
          options: [{ value: "direct", label: t("Direct (local egress)") },
            ...others.map(n => ({ value: n.id, label: n.name || n.id }))],
        },
      });
      if (!target) return;
      body.egress_target = target;
    } else if (!await uiConfirm({ title: tf("Drain {name}?", { name }), warn, confirmLabel: t("Take out for maintenance"),
        lines: [...(rulesLine ? [rulesLine] : []),
          t("It leaves rotation, and new client configs carry a warning until you resume it.")] })) {
      return;
    }
  }
  const label = node.name || node.id;
  try {
    const res = await api("POST", path + "/" + encodeURIComponent(node.id) + "/maintenance", body);
    reload();
    const moved = res && res.groups_moved ? " · " + tf("{n} moved", { n: plural(res.groups_moved, "group") }) : "";
    notify(on ? tf("{name} is out of rotation", { name: label }) + moved : tf("{name} is back in rotation", { name: label }), on ? "info" : "ok");
  }
  catch (e) { notify(e.message, "err"); }
}

// ownsInfra = may manage shared infra (any admin); seesAll = sees every
// namespace's clients (only the bootstrap owner). They diverge for a co-owner
// admin: it writes infra routes but is scoped to its own clients, so the
// resource labels must read "my clients", not "all users".
function schemas(state, overview, ownsInfra = false, me = "", seesAll = ownsInfra) {
  const node = (n) => ({ value: n.id, label: (n.name || n.id) });
  const exitOpts = (state.nodes || []).filter(n => n.public_role === "exit").map(node);
  const groupOpts = (state.groups || []).map(g => ({ value: g.id, label: g.name || g.id }));
  // Where a group's traffic leaves when nothing else is chosen, said by name.
  // The single-server deployment is the case this matters in: there its one
  // server both takes the connection and lets the traffic out, and "the entry
  // node" is a description of a machine the operator knows by its name.
  const entries = (state.nodes || []).filter(n => n.public_role === "entry");
  const entryEgressLabel = entries.length === 1
    ? tf("out of {node} itself", { node: entries[0].name || entries[0].id })
    : t("out of the entry node itself");
  // Mirrors defaultEgressExit on the server: the exit the catch-all group uses,
  // else the lowest-id exit still in rotation. A new group is given this one,
  // the form says which it is rather than calling it "the same as the rest".
  const inheritedExit = (() => {
    const liveExit = (id) => !!id && id !== "direct" && (state.nodes || []).some(n => n.id === id && n.public_role === "exit");
    const def = (state.groups || []).find(g => g.id === "default");
    if (def && liveExit(def.default_exit_id)) return def.default_exit_id;
    const exits = (state.nodes || []).filter(n => n.public_role === "exit").sort((a, b) => a.id < b.id ? -1 : 1);
    const rotating = exits.filter(n => !n.maintenance);
    const pick = rotating[0] || exits[0];
    return pick ? pick.id : "";
  })();
  const inheritedExitLabel = inheritedExit
    ? tf("{node} — the default exit", { node: nameOf(state, "nodes", inheritedExit) })
    : entryEgressLabel;
  // Routing "variant A": the UI shows two levels, and they are the two a rule is
  // stored under, a rule of the whole network's or one of a single scope's. What
  // a network rule does is its action, not a level of its own. Operators only
  // ever write scope rules, so they get no level control at all.
  const levelOpts = [
    { value: "namespace", label: t("Scope — my clients") },
    { value: "infra", label: t("Infra — the whole network") },
  ];
  const levelOf = (tier) => tier === "fleet" ? "infra" : "namespace";
  const levelLabel = (tier) => levelOf(tier) === "infra" ? t("Infra") : t("Scope");
  // effTier maps the form's level to the stored tier (operators are always
  // scope-level).
  const effTier = (v) => {
    const lvl = ownsInfra ? (v.level || "namespace") : "namespace";
    return lvl === "infra" ? "fleet" : "exit";
  };
  // For an operator "all users" means "all of its own clients", so its rule is scoped to
  // its namespace in the compiler, never the whole entry. Reflect that in the UI.
  const allLabel = seesAll ? t("all users") : t("all my clients");
  const allSelectLabel = seesAll ? t("— all users (any group) —") : t("— all my clients (any group) —");
  const groupHint = seesAll ? null : null;
  const nodeOpts = (state.nodes || []).map(n => ({ value: n.id, label: (n.name || n.id) + " (" + t(n.public_role) + ")" }));
  // A domain attaches to a node, so an operator may only attach it to one of its
  // own nodes; the fleet owner picks any. (The server enforces this regardless.)
  // A domain is a TLS binding on the listener that terminates the connection,
  // and that listener belongs to an entry node. An exit has nothing to answer
  // with, so it is not on the list; in a single-server deployment that one
  // server is the entry, and it is the only answer there is.
  const domainNodeOpts = (state.nodes || [])
    .filter(n => n.public_role === "entry" && (ownsInfra || n.owner_id === me))
    .map(n => ({ value: n.id, label: n.name || n.id }));
  const none = (arr) => [{ value: "", label: t("— none —") }, ...arr];
  const ovById = {};
  ((overview && overview.nodes) || []).forEach(n => { ovById[n.id] = n; });
  return {
    nodes: {
      title: "Servers", formClass: "node-form", fixedHeight: true, path: "/api/nodes", singular: "node", provision: true, series: "node", ownerGated: true,
      blank: "A server is either an entry that clients connect to or an exit their traffic leaves from.",
      filterLabel: "Filter by name or address",
      filter: (it, q) => (it.name || "").toLowerCase().includes(q)
        || (it.id || "").toLowerCase().includes(q)
        || (it.public_ips || []).some(ip => String(ip).toLowerCase().includes(q)),
      // A server is watched, not just listed: what it is doing now belongs in the
      // row, not two clicks away. Uptime and the last good check ride under the
      // name, the readings sit in their own numeric columns.
      columns: [
        // The address is the node's second name, so it belongs under the first
        // rather than in a column of its own.
        ["name", "Server", (v, it) => html`<div class=cell-main>${v || it.id}</div>
          <div class=cell-sub>${(it.public_ips || []).join(", ")}</div>`, "", (it) => it.name || it.id],
        ["id", "State", (v, it) => {
          const s = (ovById[it.id] || {}).system;
          return html`<${StateCell} node=${it} ov=${ovById[it.id]} />${s && s.uptime_sec > 0
            ? html`<div class=cell-sub>${t("up")} ${Math.floor(s.uptime_sec / 86400)}${t("d")}</div>` : null}`;
        }, "", (it) => nodeStatus(it, ovById[it.id]).rank],
        // The control-plane badge rides with the role: both answer "what is this
        // server", and a column of its own was empty on every ordinary node.
        ["public_role", "Role", (v, it, st) => html`<span class=${"pill " + v}>${t(v)}</span>${cpBadge(cpRoleOf((st && st.control_plane) || {}, it.id))}`, "", (it) => it.public_role],
        ["agent_addr", "Agent", (v) => html`<span class=mono title=${v || ""}>${agentPort(v)}</span>`],
        // The reading leads and what it is measured against sits under it, the
        // same shape as the traffic column: one figure per cell, so a column of
        // them can be read down.
        ["id", "CPU load", (v, it) => {
          const s = (ovById[it.id] || {}).system;
          if (!s) return html`<span class=muted>—</span>`;
          return html`<div class=cell-main>${(s.load1 || 0).toFixed(2)}</div>
            <div class=cell-sub>${tf("of {n}", { n: s.cpu_cores || 1 })}</div>`;
        }, "num", (it) => { const s = (ovById[it.id] || {}).system; return s ? (s.load1 || 0) / (s.cpu_cores || 1) : null; }],
        ["id", "Memory, GiB", (v, it) => {
          const ov = ovById[it.id] || {}, s = ov.system;
          if (!s) return html`<span class=muted>—</span>`;
          const cap = ((ov.limits || {}).memory_mb || s.mem_total_mb || 0) / 1024;
          return html`<div class=cell-main>${(s.mem_used_mb / 1024).toFixed(1)}</div>
            ${cap > 0 ? html`<div class=cell-sub>${tf("of {n}", { n: cap.toFixed(1) })}</div>` : null}`;
        }, "num", (it) => { const s = (ovById[it.id] || {}).system; return s ? s.mem_used_mb : null; }],
        ["id", "Traffic, GiB", (v, it) => {
          const ov = ovById[it.id] || {};
          const used = (ov.traffic_rx_bytes || 0) + (ov.traffic_tx_bytes || 0);
          if (!ov.system && !used) return html`<span class=muted>—</span>`;
          const cap = (ov.limits || {}).traffic_gb;
          return html`<div class=cell-main>${(used / GIB).toFixed(2)}</div>${cap ? html`<div class=cell-sub>${tf("of {n}", { n: cap })}</div>` : null}`;
        }, "num", (it) => { const ov = ovById[it.id] || {}; return (ov.traffic_rx_bytes || 0) + (ov.traffic_tx_bytes || 0); }],
      ],
      // What a server is called, where it answers, and how the panel reaches it.
      // Role belongs with the node's basic identity; its changes show their
      // consequences before saving. Reality credentials stay in their own view.
      fields: [
        { name: "name", label: "Name", required: true, placeholder: "ams-edge-01" },
        { name: "public_role", label: "Role", type: "select", options: ["entry", "exit"], required: true, editOnly: true },
        { name: "public_role", label: "Role", type: "select", options: ["entry", "exit"], required: true, createOnly: true },
        { name: "public_ips", label: "Public IPs", type: "tags" },
        { name: "agent_addr", label: "Agent address", placeholder: "203.0.113.7:8443", hint: "host:8443 (the panel reaches the agent here)" },
        { name: "dial_in.port", label: "Reality port", type: "number", def: 443, screen: "reality", showIf: v => v.public_role === "exit" },
        { name: "dial_in.target_sni", label: "Reality domain (SNI)", screen: "reality", wide: true, showIf: v => v.public_role === "exit",
          hint: "a third-party domain this server's traffic is made to look like" },
        { name: "dial_in.uuid", label: "VLESS UUID", screen: "reality", wide: true, showIf: v => v.public_role === "exit" },
        { name: "dial_in.public_key", label: "Reality public key", screen: "reality", wide: true, showIf: v => v.public_role === "exit" },
        // The stored private key never reaches the browser, so there is nothing
        // to show but a mask, and an ordinary edit leaves it alone. Replacing it
        // is a button press, and what is typed is masked like any other secret.
        { name: "key_edit", label: "Reality private key", type: "passtoggle", screen: "reality", virtual: true, editOnly: true,
          keepLabel: "Keep the current key", changeLabel: "Replace the key",
          showIf: v => v.public_role === "exit" },
        { name: "dial_in.priv_key", label: "New private key", type: "password", screen: "reality", wide: true,
          showIf: v => v.public_role === "exit" && v.key_edit === true },
        { name: "dial_in.short_id", label: "Reality short_id", screen: "reality", showIf: v => v.public_role === "exit" },
      ],
      screens: [
        { key: "reality", label: "Reality parameters",
          summary: (v) => (v["dial_in.target_sni"] || t("not set yet")) },
      ],
      summary: (v, st) => {
        const saved = (st.nodes || []).find(n => n.id === v.id);
        return saved && saved.public_role !== v.public_role ? roleChangeNote(v, st) : null;
      },
      onSubmit: (v) => { if (v.public_role === "exit" && v.dial_in) v.dial_in.proto = "vless-reality"; else delete v.dial_in; return v; },
      rowActions: ["open", "limits"],
      maintenanceAction: true, // drain/resume, from the row menu
    },
    groups: {
      title: "Groups", path: "/api/groups", singular: "group",
      blank: "A group is a set of clients that share one route: where their traffic leaves by, and which rules apply to it.",
      filterLabel: "Filter by name",
      filter: (it, q) => (it.name || "").toLowerCase().includes(q),
      // An unset exit is not a "default" that follows anything: the group's
      // traffic leaves from the entry node, the same as the direct sentinel.
      // The table said one word and the form another for the same record.
      columns: [["name", "Name"],
        ["default_exit_id", "Default exit", (v, it, st) => (!v || v === "direct")
          ? html`<span class=muted>${t("direct")}</span>` : nameOf(st, "nodes", v),
          "", (it, st) => (!it.default_exit_id || it.default_exit_id === "direct") ? "direct" : nameOf(st, "nodes", it.default_exit_id)],
        // Rule count targeting this group, linking into Routing pre-filtered by name.
        ["id", "Rules", (v, it, st) => {
          const n = (st.route_policies || []).filter(p => p.applies_to_group_id === it.id).length;
          if (!n) return html`<span class=muted>0</span>`;
          return html`<a href="#" title=${t("Show routing rules targeting this group")} onClick=${e => { e.preventDefault(); navigateTo("route_policies", it.name); }}>${n}</a>`;
        }, "num", (it, st) => (st.route_policies || []).filter(p => p.applies_to_group_id === it.id).length]],
      // Two questions, one under the other. Both answers name a node: a group
      // whose exit reads "the same as the rest" leaves the operator to go and
      // look up which node that is, and the exit is the whole point of a group.
      fields: [
        { name: "name", label: "Name", required: true, wide: true, placeholder: "office" },
        // On create the empty value is not "no exit": the panel fills in the one
        // the deployment already uses, so the choice says which node that is.
        { name: "default_exit_id", label: "Default exit", type: "select", createOnly: true, wide: true,
          options: [{ value: "", label: inheritedExitLabel }, { value: "direct", label: entryEgressLabel }, ...exitOpts],
          hint: "unless a routing rule sends it somewhere else" },
        // On an existing group the stored empty value means the same thing as
        // the direct sentinel, where traffic leaves from the entry node, so the form
        // offers that answer once, under the name of the node it happens on.
        { name: "default_exit_id", label: "Default exit", type: "select", editOnly: true, wide: true,
          deriveInitial: () => "direct",
          options: [{ value: "direct", label: entryEgressLabel }, ...exitOpts],
          hint: "unless a routing rule sends it somewhere else" },
      ],
    },
    users: {
      title: "Clients", formClass: "client-form", fixedHeight: true, path: "/api/users", singular: "client", bulk: true, series: "user",
      // Enabled is the row's own switch, the way the mockup has it; flipping one
      // row goes through the same bulk endpoint the toolbar uses.
      toggle: {
        field: "enabled",
        on: (it) => it.enabled !== false,
        label: (it) => it.enabled === false ? "enable" : "disable",
        bulkAction: (on) => on ? "disable" : "enable",
        // Past the access date the switch has nothing to turn on: the client is
        // left out of every config until the date moves, and the panel switches
        // it back off by itself. Offer the date instead of a switch that undoes
        // itself a minute later.
        refuseOn: (it) => isExpired(it.expires_at) ? {
          title: tf("Access ended {date}", { date: fmtDMY(it.expires_at) }),
          lines: [t("The client stays off until the date is extended — its config would not connect.")],
          confirmLabel: t("Extend access"),
        } : null,
      },
      // Extending a client that is switched off is a renewal, and whether access
      // comes back with the date is the operator's answer to give: paying for
      // another month and staying switched off is the failure this asks about.
      confirmSave: async (obj, initial) => {
        if (!initial || initial.enabled !== false || obj.enabled) return true;
        if (!accessExtended(initial.expires_at, obj.expires_at)) return true;
        const ans = await uiConfirm({
          title: t("This client is switched off"),
          lines: [
            obj.expires_at ? tf("Access now runs to {date}.", { date: fmtDMY(obj.expires_at) }) : t("Access now has no end date."),
            t("Switch the client on as well?"),
          ],
          confirmLabel: t("Switch on and save"),
          alt: { label: t("Save, leave it off") },
        });
        if (!ans) return false;
        if (ans !== "alt") obj.enabled = true;
        return true;
      },
      // Switching on is refused past the access date, so say how many of the
      // selected clients that is before the action runs, not afterwards, as a
      // count of failures nobody can act on.
      bulkNote: (action, items) => {
        if (action !== "enable") return null;
        const n = items.filter(it => isExpired(it.expires_at)).length;
        return n ? tf("{n} past the access date — they stay off until it is extended", { n: counted(n, "client") }) : null;
      },
      check: (obj) => obj.enabled && isExpired(obj.expires_at)
        ? { name: "expires_at", msg: t("This date has passed — extend it to switch the client on") } : null,
      blank: "A client is one person's access: a login, the group whose routing it follows, and an optional expiry date.",
      filterLabel: "Filter by name or login",
      filter: (it, q) => (it.display_name || "").toLowerCase().includes(q) || (it.username || "").toLowerCase().includes(q),
      columns: [
        // Name and login share a cell; that frees a column for the traffic figures.
        ["display_name", "Client", (v, it) => html`<div class=cell-main>${v || it.username}</div>${v && v !== it.username ? html`<div class=cell-sub>${it.username}</div>` : null}`, "", (it) => it.display_name || it.username],
        ["group_id", "Group", (v, it, st) => nameOf(st, "groups", v), "", (it, st) => nameOf(st, "groups", it.group_id)],
        // The figures belong next to the client they describe rather than in a
        // tab of their own. _tr is joined on by Clients from /api/traffic.
        ["_tr", "Now", (v) => !v ? html`<span class=muted>—</span>` : v.active
          ? html`<span class=dot></span> <span class=ok>${t("online")}</span> <span class=rate>${humanRate((v.recent_rx_bytes || 0) + (v.recent_tx_bytes || 0), v.window_seconds)}</span>`
          : html`<span class=muted>${t("idle")}</span>`, "",
          (it) => { const r = it._tr; return r && r.active ? (r.recent_rx_bytes || 0) + (r.recent_tx_bytes || 0) : (r ? 0 : null); }],
        // One month, one column: the total leads and the two directions sit under
        // it, so three headers stop competing for the width the dates need.
        ["_tr", "Total, GiB", (v) => !v ? html`<span class=muted>—</span>`
          : html`<div class=cell-main>${(v.total_bytes / GIB).toFixed(2)}</div>
                 <div class=cell-sub>↑ ${(v.rx_bytes / GIB).toFixed(2)} ↓ ${(v.tx_bytes / GIB).toFixed(2)}</div>`, "num",
          (it) => (it._tr ? it._tr.total_bytes : null)],
        // An ended date is why the switch beside it is off, so the row says so
        // rather than leaving the red text to be read as a warning about a date
        // that is merely close.
        ["expires_at", "Access until", (v) => !v ? html`<span class=muted>—</span>` : isExpired(v)
          ? html`<div class=err>${fmtDMY(v)}</div><div class=cell-sub>${t("access ended")}</div>`
          : html`<span>${fmtDMY(v)}</span>`],
      ],
      // Who the client is comes first; what it connects with comes after, in a
      // block of its own. The password was three controls on screen at all
      // times: a box, a confirmation and a "regenerate" switch that could be
      // set at the same time as a typed password, with only the server knowing
      // which of the two won.
      fields: [
        { name: "display_name", label: "Display name", required: true, placeholder: "Ivan Petrov" },
        { name: "group_id", label: "Group", type: "select", options: groupOpts.length ? groupOpts : [{ value: "", label: "— create a group first —" }], required: true, hint: groupOpts.length ? undefined : "No groups yet — add a group on the Groups tab before creating users." },
        { name: "expires_at", label: "Access until", type: "date", clearable: "No end date", hint: "empty = no end date; access stops at the end of the chosen day (UTC)" },
        { name: "enabled", label: "Allow connections", type: "bool", def: true, alignInput: true },

        { name: "username", label: "Connection login", group: "Connection details", required: true, createOnly: true, placeholder: "i.petrov" },
        { name: "username", label: "Connection login", group: "Connection details", editOnly: true, type: "static",
          text: v => v.username },
        // Create: generated unless the operator says otherwise. Edit: nothing
        // about the password is in the request until "Change password" is
        // pressed, so an ordinary edit cannot rewrite a working secret.
        { name: "pw_mode", label: "Password", type: "select", group: "Connection details", createOnly: true, virtual: true, def: "auto",
          options: [{ value: "auto", label: "Generate automatically" }, { value: "manual", label: "Set a password" }] },
        { name: "pw_edit", label: "Password", type: "passtoggle", group: "Connection details", editOnly: true, virtual: true },
        { name: "password", label: "New password", type: "password", group: "Connection details", required: true,
          showIf: v => (v.pw_mode === "manual" || v.pw_edit === true),
          generate: (set) => { const pw = randomSecret(); set("password", pw); set("password_confirm", pw); },
          hint: "at least 12 characters" },
        { name: "password_confirm", label: "Repeat the password", type: "password", group: "Connection details", required: true, matchField: "password",
          showIf: v => (v.pw_mode === "manual" || v.pw_edit === true),
          hint: v => (v.pw_edit === true ? "once saved, the old password stops working" : "") },
      ],
      rowActions: ["config"], rowPrimary: ["config"], // issuing access is the daily act
    },
    route_policies: {
      title: "Routing", path: "/api/route-policies", singular: "rule", tester: true, ownerGated: true,
      blank: "A rule decides where matching traffic goes: out of a chosen exit, out of the entry node it is already on, or nowhere at all.",
      filterLabel: "Filter by name, group or exit",
      filter: (it, q, st) => {
        if ((it.name || "").toLowerCase().includes(q) || (it.tier || "").toLowerCase().includes(q)) return true;
        // A rule can also be looked up by the exit it names: its own, the one
        // it gives everyone else, or the one it falls back to, so "everything
        // that points at this node" is a question the list can answer, and a
        // node's card can hand the question over.
        const exits = [it.exit_node_id, it.others_exit_id, it.fallback_kind === "exit" ? it.fallback_exit_id : ""].filter(Boolean);
        if (exits.some(id => id.toLowerCase().includes(q) || String(nameOf(st, "nodes", id)).toLowerCase().includes(q))) return true;
        if (it.applies_to_group_id) return String(nameOf(st, "groups", it.applies_to_group_id)).toLowerCase().includes(q);
        // No explicit group = applies to every group. Match the "all users" label,
        // and also surface it whenever the query names a real group, a catch-all
        // rule targets that group too (so a Groups → Routing jump shows it).
        if ("all users".includes(q) || t("all users").toLowerCase().includes(q)) return true;
        return (st.groups || []).some(g => (g.name || "").toLowerCase().includes(q));
      },
      help: {
        title: "How a rule works",
        points: [
          "Lines are read top to bottom: each one joins to everything above it, so “A and B or C” is “(A and B) or C”. The form writes the brackets as you build it.",
          "geoip looks at the destination IP, not at the domain text. A .ru domain hosted abroad is not geoip:ru.",
          "Infra rules run before scope rules. Within a level, priority decides and the first match wins; the list is shown in that order.",
          "A rule can answer for everyone else too: another exit, direct, or refused. It is then checked before the ordinary rules of its level, otherwise a category listing the same domain would answer for them first.",
          "Use the Route tester to check which rule wins for a given group and host.",
        ],
      },
      // Live, plain-language description of the rule being edited: what it does,
      // how it sits among the others, and where the rest of the traffic goes.
      summary: (v, st, mode) => {
        const mine = policyMatchTokens(v);
        // An empty draft catches nothing and cannot be saved, so it must not be
        // read back as if it caught everything.
        const what = condSentence(policyConditions(v));
        const et = v.tier || effTier(v);
        // Where this rule sits in the node's order, counted across every level:
        // an infra rule above answers first whatever this one's priority says.
        // A rule being created has no id yet, so it sorts last among equals.
        const self = { tier: et, others_action: v.others_action, priority: Number(v.priority) || 0, id: v.id || "\uffff" };
        const before = (st.route_policies || [])
          .filter(p => !p.disabled && p.id !== v.id && policyOrder(p, self) < 0)
          .sort(policyOrder);
        // Shadow: an earlier rule whose group overlaps and that matches some of the same tokens.
        let shadow = null;
        const mineSet = new Set(mine);
        for (const p of before) {
          if (!policyGroupsOverlap(v.applies_to_group_id || "", p.applies_to_group_id || "")) continue;
          const hit = policyMatchTokens(p).filter(t => mineSet.has(t));
          if (hit.length) { shadow = { name: p.name, on: hit.slice(0, 3).join(", "), more: hit.length > 3 }; break; }
        }

        let rest = "";
        if (v.applies_to_group_id && v.others_action === "block") rest = t("Everyone else is refused.");
        else if (v.applies_to_group_id && v.others_action === "direct") rest = t("Everyone else leaves from the entry node.");
        else if (v.applies_to_group_id && v.others_action === "exit") {
          rest = tf("Everyone else exits via {name}.", { name: v.others_exit_id ? nameOf(st, "nodes", v.others_exit_id) : t("(pick an exit node)") });
        }
        let whenDown = "";
        const namesExit = v.action === "exit" || v.others_action === "exit";
        if (namesExit && v.fallback_kind === "block") whenDown = t("If that exit is unavailable, this traffic is refused.");
        else if (namesExit && v.fallback_kind === "exit") {
          whenDown = tf("If that exit is unavailable, it moves to {name} — and is refused if that one is out too.",
            { name: v.fallback_exit_id ? nameOf(st, "nodes", v.fallback_exit_id) : t("(pick an exit node)") });
        }
        // The unset answer is an answer, and the one nobody reads the form for.
        else if (namesExit) whenDown = t("If that exit is unavailable, this traffic leaves from the entry node.");
        const sameExit = v.action === "exit" && v.others_action === "exit"
          && v.others_exit_id && v.others_exit_id === v.exit_node_id;
        // The overlap check above matches the text of the conditions. Category
        // names are text too: two lists holding the same site read as unrelated,
        // so the absence of a warning is not the absence of an overlap.
        const usesCategories = policyConditions(v).some(c => c.kind === "geoip" || c.kind === "geosite")
          || before.some(p => policyConditions(p).some(c => c.kind === "geoip" || c.kind === "geosite"));
        const flags = html`
          ${sameExit && html`<p class=warn>${t("Everyone else would take the same exit as the group, so nothing is reserved.")}</p>`}
          ${shadow && html`<p class=warn>${tf("“{name}” is checked earlier and also catches {on}{more} — that traffic may go there first.", { name: shadow.name, on: shadow.on, more: shadow.more ? "…" : "" })}</p>`}
          ${v.disabled && html`<p class=warn>${t("This rule is off and won't be applied.")}</p>`}`;
        if (mode === "short") return (sameExit || shadow || v.disabled) ? flags : null;
        if (v.level === "" || v.group_pending) return html`<p>${t("Choose the level and group first. They determine which requests the rule handles and its order.")}</p>`;
        if (!what) return html`<p>${t("Add a category, country, domain or IP address.")}</p>`;
        const routeLabel = v.action === "exit" ? tf("Through {name}", { name: nameOf(st, "nodes", v.exit_node_id) })
          : v.action === "block" ? t("Blocked") : v.action === "direct" ? t("Out of the entry node itself") : t("Route not selected");
        return html`
          <p>${condFormula(policyConditions(v))}</p>
          <p>${t("Route")}: <strong>${routeLabel}</strong>.</p>
          <p>${t("The rule applies when a request matches its conditions and no earlier rule has handled it.")}</p>
          ${v.from_group_exit && v.action === "exit" && html`<p>${t("The exit is written into the rule. Changing the group's own exit later does not move this rule.")}</p>`}
          ${v.exclusive && !v.others_action ? html`<p>${t("For an exclusive rule, choose an action for the other groups in More settings.")}</p>`
            : rest && html`<p>${rest}</p>`}
          ${whenDown && html`<p>${whenDown}</p>`}
          ${policyConditions(v).some(c => c.join === "except") && html`<p>${t("Excluded requests skip this rule. Later rules apply, followed by the group's default exit.")}</p>`}
          <p>${before.length === 0 ? t("It is the first rule checked.")
            : tf("Checked after: {names}.", { names: before.map(p => "«" + (p.name || p.id) + "» (" + levelLabel(p.tier) + ")").join(", ") })}</p>
          ${flags}
          ${usesCategories && html`<p class=rr-boundary>${t("Overlaps between categories are not checked here. Test a specific address in the route tester.")}</p>`}`;
      },
      // Routing has a form of its own (RouteRuleModal): a rule is a sentence,
      // not a column of fields. What that form needs from the schema lives here
      // so the two cannot drift apart.
      route: { ownsInfra, levelOpts, levelOf, effTier, exitOpts, groupOpts, allSelectLabel },
      // The default order is the order the nodes read the rules in, one
      // comparator, shared with the summary, so the two cannot disagree.
      order: policyOrder,
      toggle: {
        field: "disabled",
        label: (it) => it.disabled ? "enable" : "disable",
        next: (it) => ({ ...it, disabled: !it.disabled }),
      },
      // Four columns: what the rule is, who it is for, where it sends them. The
      // level and the match read under the name, where they belong to it,
      // instead of as two more columns competing for the width the match needs.
      columns: [
        ["name", "Rule", (v, it, st) => {
          const m = condSentence(policyConditions(it));
          return html`
            <div class=cell-main><span class=rule-name>${v}</span>${it.others_action
              ? html`<span class=state-label title=${t("this rule decides for its group and for everyone else")}>${t("exclusive")}</span>` : ""}</div>
            <div class=cell-sub title=${m}>${levelLabel(it.tier)} · ${m}</div>`;
        }, "", (it) => it.name || ""],
        ["applies_to_group_id", "Group", (v, it, st) => v ? nameOf(st, "groups", v) : levelOf(it.tier) === "infra" ? t("All network groups") : t("All my groups")],
        ["action", "Route", (v, it, st) => v === "exit"
          ? tf("Through {name}", { name: nameOf(st, "nodes", it.exit_node_id) })
          : v === "block" ? t("Blocked") : t("From the entry node"),
          "", (it) => it.action === "exit" ? nameOf(state, "nodes", it.exit_node_id) : it.action],
      ],
    },
    domains: {
      title: "Domains", path: "/api/domains", singular: "domain",
      blank: "A domain is a name a server answers for, with the certificate that proves it.",
      filterLabel: "Filter by hostname",
      filter: (it, q, st) => (it.hostname || "").toLowerCase().includes(q)
        || String(nameOf(st, "nodes", it.node_id) || "").toLowerCase().includes(q),
      // A domain has no owner_id of its own; it is owned by its node. Gate edit
      // rights by the node's owner so an operator manages its own domains/TLS and
      // sees the rest read-only.
      ownerGated: true,
      ownerOf: (it, st) => { const n = (st.nodes || []).find(x => x.id === it.node_id); return n ? (n.owner_id || "") : ""; },
      columns: [
        ["hostname", "Hostname"],
        ["purpose", "Purpose", (v) => html`<span class=${"pill " + (v === "endpoint" ? "exit" : "direct")}>${purposeLabel(v)}</span>`,
          "", (it) => purposeLabel(it.purpose)],
        ["node_id", "Server", (v, it, st) => nameOf(st, "nodes", v), "", (it, st) => nameOf(st, "nodes", it.node_id)],
        ["tls_status", "TLS", (v) => html`<span class=${v === "ready" ? "ok" : v === "failed" ? "err" : "muted"}>${t(v)}</span>`],
        ["tls_issuer", "Issuer", v => !v ? html`<span class=muted>—</span>`
          : isStagingIssuer(v)
          ? html`<span class=warn title=${t("Staging cert — not browser-trusted; promote to production")}>⚠ ${v}</span>`
          : html`<span title=${v}>${v}</span>`, "ellip"],
      ],
      // What the name is for, then the name, then the server that answers for
      // it. The purpose is first because it is the answer the other two are
      // read against: a connection endpoint and a cover site are two different
      // jobs done with the same kind of name.
      fields: [
        { name: "purpose", label: "Purpose", type: "select", required: true, wide: true,
          options: [{ value: "endpoint", label: "VPN connection" }, { value: "decoy", label: "Cover site" }],
          hint: (v) => v.purpose === "decoy"
            ? "an ordinary site on this server; nobody connects through this name"
            : "the name clients connect to; anyone else who opens it gets the cover site" },
        { name: "hostname", label: "Hostname", required: true, wide: true, placeholder: "vpn.example.com" },
        // One eligible server is not a question. It is still shown, because the
        // DNS record below is about that machine and the operator is about to
        // go and write it down.
        ...(domainNodeOpts.length === 1
          ? [{ name: "node_id", label: "Server", type: "static", wide: true, def: domainNodeOpts[0].value,
               text: (v) => nameOf(state, "nodes", v.node_id) || domainNodeOpts[0].label }]
          : [{ name: "node_id", label: "Server", type: "select", options: domainNodeOpts, required: true, wide: true,
               hint: "the server that answers on this address" }]),
      ],
      // The record to add, spelled out. Until both halves are known it says so
      // rather than showing half of one.
      summary: (v, st) => {
        const n = (st.nodes || []).find(x => x.id === v.node_id);
        const host = String(v.hostname || "").trim();
        const ip = n ? (n.public_ips || [])[0] : "";
        if (!host || !ip) return null;
        return html`${t("Add a DNS record")}: <code>${String(ip).includes(":") ? "AAAA" : "A"} ${host} → ${ip}</code>. ${t("The certificate is issued once the name resolves here.")}`;
      },
      rowActions: ["promote-tls"],
    },
  };
}
const TABS = ["overview", "nodes", "clients", "route_policies", "logs", "settings"];
// Computed per render (not a frozen const) so the language switch relabels tabs.
function tabLabel(tab) {
  return ({ overview: t("Overview"), nodes: t("Servers"), clients: t("Clients"),
    route_policies: t("Routing"), members: t("Accounts"), logs: t("Logs"), settings: t("Settings"), account: t("Account") })[tab] || tab;
}
// Section names agree between the rail and the page heading.
function railLabel(tab) { return tabLabel(tab); }
// Tabs whose header carries no per-row count badge. "clients" aggregates two
// entities (users+groups) so no single count fits; "traffic"/"members" have no
// state[tab] array to count.
const NO_COUNT = { overview: true, members: true, logs: true, settings: true, account: true };
// The clients tab reads a differently-named slice of state; everything else
// matches its tab key.
const COUNT_KEY = { clients: "users" };

/* ---------------- nested helpers ---------------- */
function setPath(obj, path, val) {
  const parts = path.split("."); let o = obj;
  for (let i = 0; i < parts.length - 1; i++) { o[parts[i] || ""] = o[parts[i]] || {}; o = o[parts[i]]; }
  o[parts[parts.length - 1]] = val;
}
function getPath(obj, path) {
  return path.split(".").reduce((o, k) => (o == null ? undefined : o[k]), obj);
}

/* ---------------- hash routing ---------------- */
// Plain `#tab` or `#tab/subtab` routing (no library): keeps the current tab, and
// for tabs that carry subtabs (Nodes, Clients, Settings) the subtab too, across a
// page refresh, instead of dropping back to the first tab.
function parseHash() {
  const h = (location.hash || "").replace(/^#/, "");
  const [tab, sub] = h.split("/");
  return { tab: tab || "", sub: sub || "" };
}
function pushHash(tab, sub) {
  const next = "#" + tab + (sub ? "/" + sub : "");
  if (location.hash !== next) history.replaceState(null, "", next);
}
// useSubtab: a panel's subtab state, synced to `#tabKey/subtab`. Restores from the
// hash on first render (falling back to `def` when absent/invalid) and pushes a
// new hash entry on every change, so Nodes/Clients/Settings each keep their own
// subtab position independent of one another.
function useSubtab(tabKey, def, valid) {
  const initial = (() => { const h = parseHash(); return h.tab === tabKey && valid.includes(h.sub) ? h.sub : def; })();
  const [sub, setSubRaw] = useState(initial);
  const setSub = useCallback(s => { setSubRaw(s); pushHash(tabKey, s); }, [tabKey]);
  return [sub, setSub];
}

/* ---------------- app ---------------- */
function App() {
  const [phase, setPhase] = useState("loading"); // loading | login | ready
  const [user, setUser] = useState("");
  const [role, setRole] = useState("admin"); // "admin" | "operator"; gates infra UI
  const [ownsInfra, setOwnsInfra] = useState(true); // any admin: shared-infra surfaces
  const [settings, setSettings] = useState(null); // bot health, for the Overview board
  const [isBootstrap, setIsBootstrap] = useState(true); // infra-namespace admin: sees ALL clients + account mgmt
  const [canManage, setCanManage] = useState(true); // bootstrap owner: account/member mgmt
  const [crossNs, setCrossNs] = useState(false); // bootstrap: buried cross-namespace lens (off by default)
  const [ns, setNs] = useState(""); // own namespace ('' = infra); the ownership key
  const [tab, setTab] = useState(() => parseHash().tab || "overview");
  const [state, setState] = useState({});
  const [traffic, setTraffic] = useState(null);
  const [health, setHealth] = useState(null);
  const [overview, setOverview] = useState(null);
  const [toast, setToast] = useState(null);
  const [modal, setModal] = useState(null); // {kind:'form'|'config', ...}
  // Read by the background refresh, which must not redraw a table while its
  // editor is open. A ref rather than the value itself: the refresh has no other
  // reason to restart its timer every time a dialog opens or closes.
  const modalRef = useRef(null);
  useEffect(() => { modalRef.current = modal; }, [modal]);
  const [lang, setLang] = useState(LANG);
  const [confirmReq, setConfirmReq] = useState(null);
  const [routeFilter, setRouteFilter] = useState(""); // pre-fill for the Routing table after a cross-tab jump
  const [logFilter, setLogFilter] = useState(""); // same, for the journal a node hands over
  const [live, setLive] = useState({ fails: 0, okAt: Date.now(), what: "", error: "" }); // live-telemetry health
  // Switch the UI language and, when signed in, persist it to the account so the
  // choice follows the operator across devices and reaches the bot.
  const switchLang = (l) => {
    setLangGlobal(l); setLang(l);
    if (user) api("POST", "/api/account/locale", { locale: l }).catch(() => {});
  };
  // A manual tab click starts clean: drop any cross-tab filter a Groups → Routing
  // jump left behind (navigateTo sets it again on its own path), and the hash
  // resets to the new tab's default subtab (each panel restores its own from the
  // hash on mount via useSubtab).
  const goTab = (tb) => { setRouteFilter(""); setLogFilter(""); setTab(tb); pushHash(tb); };
  // Same jump, landing on a named subtab: the hash is written before the panel
  // mounts, which is where useSubtab reads it from.
  const goSub = (tb, sub) => { setRouteFilter(""); setLogFilter(""); pushHash(tb, sub); setTab(tb); };

  // Register the themed-confirm host so uiConfirm() anywhere can open a modal.
  useEffect(() => { _confirmHost = setConfirmReq; return () => { _confirmHost = null; }; }, []);
  // Register the cross-tab navigation host (Groups → Routing with a filter).
  useEffect(() => { _navHost = (tb, filter) => { if (tb === "route_policies") setRouteFilter(filter); if (tb === "logs") setLogFilter(filter || ""); setTab(tb); pushHash(tb); }; return () => { _navHost = null; }; }, []);
  // Back/forward navigates the hash without a page load, so follow it.
  useEffect(() => { const onHash = () => setTab(parseHash().tab || "overview"); window.addEventListener("hashchange", onHash); return () => window.removeEventListener("hashchange", onHash); }, []);

  // A fresh id per call so the Toast resets its dismissal timer; the Toast owns
  // the auto-hide (paused on hover, errors stay until closed).
  const notify = useCallback((msg, kind) => {
    // Localize messages, since backend errors arrive in English. An exact dictionary
    // hit wins; otherwise translate the part before a ": <detail>" suffix (e.g.
    // "bot unreachable: …"). Already-translated success strings pass through.
    let m = String(msg == null ? "" : msg);
    const tr = t(m);
    if (tr !== m) m = tr;
    else { const i = m.indexOf(": "); if (i > 0) { const head = t(m.slice(0, i)); if (head !== m.slice(0, i)) m = head + m.slice(i); } }
    setToast({ msg: m, kind, id: Date.now() + Math.random() });
  }, []);

  // The telemetry endpoints are allowed to fail without taking the page down
  // with them, but a failure must not pass unnoticed either: a node that has
  // just died would otherwise keep rendering its last good figures forever.
  // noteLive collects what stopped refreshing so the page can say so.
  const noteLive = useCallback((what, err) => {
    setLive(s => err
      ? { fails: s.fails + 1, okAt: s.okAt, what, error: err.message || String(err) }
      : (s.fails === 0 ? s : { fails: 0, okAt: Date.now(), what: "", error: "" }));
  }, []);

  const loadState = useCallback(async () => {
    // Both in flight at once: the rule form's category suggestions are built
    // during the render that setState triggers, so they have to be here by then.
    const [s] = await Promise.all([api("GET", "/api/state"), loadGeoCatalog()]);
    setState(s);
    try { setHealth(await api("GET", "/api/health")); noteLive("health", null); } catch (e) { noteLive(t("Panel health"), e); }
    try { setTraffic(await api("GET", "/api/traffic")); noteLive("traffic", null); } catch (e) { noteLive(t("Traffic counters"), e); }
  }, [noteLive]);

  const reconcileNow = useCallback(async () => {
    try { const r = await api("POST", "/api/reconcile"); notify(reconcileText(r), reconcileOk(r) ? "ok" : "err"); loadState(); }
    catch (e) { notify(e.message, "err"); }
  }, [loadState]);

  // Flip the buried cross-namespace lens (bootstrap only). Server-enforced and
  // session-scoped, so state reloads to bring in / drop the other tenants' rows.
  const toggleCrossNs = useCallback(async (on) => {
    try {
      await api("POST", "/api/dev/cross-namespace-view", { enabled: on });
      setCrossNs(on); await loadState();
      notify(on ? t("Cross-scope view on") : t("Cross-scope view off"), "ok");
    } catch (e) { notify(e.message, "err"); }
  }, [loadState, notify]);

  const boot = useCallback(async () => {
    try {
      const s = await api("GET", "/api/session");
      if (s.authenticated) { setCsrfToken(s.csrf_token); setUser(s.username || ""); setRole(s.role || "admin"); setOwnsInfra(s.owns_infra !== false); setIsBootstrap(!!s.is_bootstrap); setCanManage(!!s.can_manage_members); setCrossNs(!!s.cross_namespace_view); setNs(s.namespace || ""); if (s.locale) { setLangGlobal(s.locale); setLang(s.locale); } await loadState(); return setPhase("ready"); }
    } catch {}
    try { await loadState(); return setPhase("ready"); } catch {} // bootstrap-open
    setPhase("login");
  }, [loadState]);

  useEffect(() => { boot(); }, [boot]);

  // Poll the live overview once at app level (drives both the Overview cards and
  // the per-node status dots in the Nodes table).
  useEffect(() => {
    if (phase !== "ready") return;
    let running = true;
    // Skip the poll on a hidden tab (no point fetching what nobody sees); refresh
    // at once when the tab becomes visible again.
    const load = async () => {
      if (document.hidden) return;
      try { const d = await api("GET", "/api/overview"); if (running) { setOverview(d); noteLive("overview", null); } }
      catch (e) { if (running) noteLive(t("Node monitoring"), e); }
    };
    load();
    const timer = setInterval(load, 15000);
    const onVis = () => { if (!document.hidden) load(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { running = false; clearInterval(timer); document.removeEventListener("visibilitychange", onVis); };
  }, [phase, noteLive]);

  // The tables were fetched once at sign-in and then only after an action taken
  // here, so a client switched off from the bot, a subscription running out or a
  // counter moving stayed invisible until someone reloaded the page. They now
  // refresh on the same cadence as the overview. Held back while a form is open:
  // re-rendering a table under an editor is how a half-typed change disappears.
  useEffect(() => {
    if (phase !== "ready") return;
    let running = true;
    const load = async () => {
      if (document.hidden || modalRef.current) return;
      try { const s = await api("GET", "/api/state"); if (running) setState(s); } catch {}
      try { const tr = await api("GET", "/api/traffic"); if (running) setTraffic(tr); } catch {}
    };
    const timer = setInterval(load, 15000);
    const onVis = () => { if (!document.hidden) load(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { running = false; clearInterval(timer); document.removeEventListener("visibilitychange", onVis); };
  }, [phase]);

  // A bot that has stopped answering is a problem for the board, and the board
  // could never hear about it: the branch that reports it had no settings to
  // read. Infra-only, like the endpoint; a failure here is not worth a banner.
  useEffect(() => {
    if (phase !== "ready" || !ownsInfra) return;
    let running = true;
    const load = () => { if (!document.hidden) api("GET", "/api/settings").then(d => { if (running) setSettings(d); }, () => {}); };
    load();
    const timer = setInterval(load, 60000);
    return () => { running = false; clearInterval(timer); };
  }, [phase, ownsInfra]);

  if (phase === "loading") return html`<div class=booting><div class=card><${Skeleton} rows=${3} /></div></div>`;
  if (phase === "login") return html`<${Login} lang=${lang} switchLang=${switchLang} onDone=${() => location.reload()} />`;

  const sc = schemas(state, overview, ownsInfra, ns, isBootstrap);
  // isAdmin means "fleet owner" (owns infra): only it renders infra surfaces and
  // edits across namespaces. A namespace admin is scoped exactly like an operator
  // for resources/infra; its one extra power (member management) is canManage.
  const isAdmin = ownsInfra;
  // Non-infra accounts don't see infra-only surfaces (global settings, the fleet
  // event log). The server enforces this regardless; this just keeps the UI honest.
  const visibleTabs = TABS.filter(tb => {
    if (tb === "settings") return isAdmin; // infra-only surface (Members lives here as a subtab)
    return true; // logs is namespace-scoped (operator sees its own journal)
  });
  const curTab = visibleTabs.includes(tab) ? tab : "overview";
  return html`
    <div class=shell>
      <aside class=rail>
        <div class=wordmark>Trust<span>Panel</span></div>
        <nav>
          ${visibleTabs.map(tb => html`<a key=${tb} href=${"#" + tb} class=${tb === curTab ? "active" : ""} aria-current=${tb === curTab ? "page" : null}
            onClick=${e => { if (e.metaKey || e.ctrlKey || e.shiftKey) return; e.preventDefault(); goTab(tb); }}>
            <span>${railLabel(tb)}</span>${!NO_COUNT[tb] && html`<span class=count>${(state[COUNT_KEY[tb] || tb] || []).length}</span>`}
          </a>`)}
        </nav>
        <div class=rl-foot>
          <${LangToggle} lang=${lang} switchLang=${switchLang} />
          ${user && html`<span class=who>${user}<span class=muted>${isAdmin ? t("admin") : (ns || t("operator"))}</span></span>`}
          <a class=more role=link tabindex=0 onClick=${async () => { try { await api("POST", "/api/auth/logout"); } catch {}; location.reload(); }}
            onKeyDown=${e => { if (e.key === "Enter") e.target.click(); }}>${t("Log out")}</a>
        </div>
      </aside>
      <main class=panel-page>
        ${live.fails >= 2 && html`<${StaleBanner} live=${live} onRetry=${loadState} />`}
        ${crossNs && html`<div class=nslens>
          <span>${t("All scopes")}</span>
          <span class="muted spacer">${t("Viewing every scope, not just your own")}</span>
          <button class="ghost sm" onClick=${() => toggleCrossNs(false)}>${t("Return to my own")}</button>
        </div>`}
        <${ErrorBoundary} key=${curTab}>
          ${curTab === "overview"
            ? html`<${Overview} state=${state} health=${health} overview=${overview} traffic=${traffic} settings=${settings} setTab=${goTab} goSub=${goSub} setModal=${setModal} onReconcile=${reconcileNow} isAdmin=${isAdmin} seesAll=${isBootstrap} />`
            : curTab === "clients"
            ? html`<${Clients} state=${state} traffic=${traffic} reload=${loadState} notify=${notify} setModal=${setModal} />`
            : curTab === "logs"
            ? html`<${Logs} notify=${notify} initialFilter=${logFilter} />`
            : curTab === "settings"
            ? html`<${SettingsPanel} notify=${notify} state=${state} isBootstrap=${isBootstrap} canManage=${canManage} myNs=${ns} crossNs=${crossNs} toggleCrossNs=${toggleCrossNs} />`
            : curTab === "nodes"
            ? html`<${NodesPanel} sc=${sc} state=${state} overview=${overview} traffic=${traffic} reload=${loadState} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${ns} />`
            : curTab === "route_policies"
            ? html`<${RoutingPanel} sc=${sc} state=${state} reload=${loadState} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${ns} initialFilter=${routeFilter} />`
            : html`<${EntityPanel} cfg=${sc[curTab]} rows=${state[curTab] || []} state=${state} reload=${loadState} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${ns}
                head=${{ title: tabLabel(curTab) }} />`}
        <//>
      </main>
      ${modal && modal.kind === "form" && (modal.cfg.route
        ? html`<${RouteRuleModal} ...${modal} state=${state} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`
        : html`<${FormModal} ...${modal} state=${state} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`)}
      ${modal && modal.kind === "graph" && html`<${GraphModal} ...${modal} onClose=${() => setModal(null)} />`}
      ${modal && modal.kind === "config" && html`<${ConfigModal} userId=${modal.userId} username=${modal.username} user=${modal.user} entries=${(state.nodes||[]).filter(n=>n.public_role==='entry')} onClose=${() => setModal(null)} notify=${notify} />`}
      ${modal && modal.kind === "provision" && html`<${ProvisionModal} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} isAdmin=${isAdmin} state=${state} />`}
      ${modal && modal.kind === "convert" && html`<${ConvertModal} state=${state} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "limits" && html`<${LimitsModal} node=${modal.node} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "nodecard" && html`<${NodeCard} node=${modal.node} cfg=${sc.nodes} state=${state} overview=${overview} traffic=${traffic} onClose=${() => setModal(null)} setModal=${setModal} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "routetest" && html`<${RouteTester} state=${state} onClose=${() => setModal(null)} notify=${notify} />`}
      ${confirmReq && html`<${ConfirmModal} req=${confirmReq} onClose=${() => setConfirmReq(null)} />`}
      ${toast && html`<${Toast} toast=${toast} setToast=${setToast} />`}
    </div>`;
}

// What kind of entry a journal line is. "admin" is a role in the rest of the
// panel, so the column asks for the kind under a name of its own.
const EVENT_KIND = { admin: "an action", alert: "alert", backup: "backup", system: "system" };
function eventKind(k) { return t(EVENT_KIND[k] || k); }

function reconcileText(r) {
  const lines = Object.entries(r.nodes || {}).map(([k, v]) => {
    const warn = (v.warnings || []).map(w => "\n  ⚠ " + ph(w)).join("");
    return `${k}: ${v.outcome}${v.error ? " — " + v.error : ""}${warn}`;
  });
  return tf("Synced — rev {revision}", { revision: r.revision }) + "\n" + (lines.join("\n") || t("no nodes"));
}
function reconcileOk(r) {
  return Object.values(r.nodes || {}).every(v => !v.error && (v.outcome === "applied" || v.outcome === "no-change"));
}

/* ---------------- overview ---------------- */
// computeProblems derives the live "needs attention" list from the current state,
// the /api/overview snapshot, and settings: the signals otherwise scattered
// across Nodes/HA/Domains/Settings, gathered into one board. Each item is
// {sev:'critical'|'warn', text} plus where it is answered: the node it is about,
// or the tab that holds it.
function computeProblems(state, overview, settings) {
  const out = [];
  const ovById = {};
  ((overview && overview.nodes) || []).forEach(n => { ovById[n.id] = n; });
  for (const n of (state.nodes || [])) {
    // A server drained on purpose is not a fault. It is out of rotation because
    // someone put it there, and its own state column says so; counting it here
    // raised an alarm about the operator's own decision and left the two screens
    // contradicting each other. drainedNodes() names them instead.
    if (n.maintenance) continue;
    const ov = ovById[n.id] || {};
    const nm = n.name || n.id;
    if (ov.edge && ov.edge.ok === false) out.push({ sev: "critical", text: nm + " — " + t("edge :443 unreachable"), node: n });
    const h = ov.health || n.health;
    if (h === "degraded" || h === "unhealthy") out.push({ sev: "critical", text: nm + " — " + t("agent unreachable"), node: n });
    // Which of the three replication faults it is, not the word "degraded" for
    // all three: a missing slot and a replica 400 MiB behind are not one problem.
    const w = replWarning(ov.replication);
    if (w) out.push({ sev: "warn", text: nm + " — " + w, node: n });
    if (ov.billing_days_left != null) {
      if (ov.billing_days_left < 0) out.push({ sev: "warn", text: nm + " — " + t("VPS payment overdue"), node: n });
      else if (ov.billing_days_left <= 7) out.push({ sev: "warn", text: nm + " — " + t("VPS payment due soon"), node: n });
    }
  }
  for (const d of (state.domains || [])) {
    if (isStagingIssuer(d.tls_issuer)) out.push({ sev: "warn", text: d.hostname + " — " + t("staging TLS certificate (not browser-trusted)"), tab: "domains" });
  }
  if (settings) {
    if (settings.bot && settings.bot.status && settings.bot.status !== "ok" && settings.bot.status !== "unconfigured")
      out.push({ sev: "warn", text: t("management bot unreachable"), tab: "settings" });
    if (settings.alert && settings.alert.status && settings.alert.status !== "ok" && settings.alert.status !== "unconfigured")
      out.push({ sev: "warn", text: t("alert bot unreachable"), tab: "settings" });
  }
  return out;
}

// The servers deliberately out of rotation, so the status line can name them as
// a fact about the deployment rather than as something that went wrong.
function drainedNodes(state) { return (state.nodes || []).filter(n => n.maintenance); }

const MIB = 1024 * 1024;
const GIB = 1024 * MIB;

// Which node currently holds the control plane, and how it is labelled.
function cpRoleOf(cp, id) {
  if (!cp) return "";
  if (id === cp.active_node_id) return "master";
  if ((cp.standby_node_ids || []).includes(id)) return "standby";
  return "";
}
function cpBadge(role) {
  if (!role) return "";
  return html`<span class=${"pill " + role}>${t(role)}</span>`;
}

// PageHead: every screen opens with its name and its actions. A line under the
// heading explaining what the screen is got read once and then sat there for
// good; what the screen counts or when it was last refreshed belongs with the
// thing it describes: over the table, or in the status line.
function PageHead({ title, actions }) {
  return html`<div class=page-head>
    <h1>${title}</h1>
    ${actions && html`<div class=actions>${actions}</div>`}
  </div>`;
}

// Meter: one resource on a node, its name, the reading, and how full it is.
function Meter({ label, value, max, text }) {
  const pct = max > 0 ? Math.min(100, Math.round((value / max) * 100)) : 0;
  const cls = pct >= 90 ? "bar err" : pct >= 70 ? "bar warn" : "bar";
  return html`<div>
    <div class=meter-top><span class=caps>${label}</span><span class="mono mval">${text}</span></div>
    <div class=${cls}><span style=${{ width: pct + "%" }}></span></div>
  </div>`;
}

// nodeChecks splits a node's background checks into what is wrong (worst first)
// and the quiet line of what is fine, stated once rather than repeated per check.
// Shared by the Overview row and the node card, so one place decides what counts.
function nodeChecks(n) {
  const facts = [];
  if (n.reconcile) {
    const rc = n.reconcile;
    const age = rc.age_sec >= 0 ? " · " + rc.age_sec + t("s") + " " + t("ago") : "";
    if (!rc.ok) facts.push({ cls: "err", text: t("sync") + " " + t(rc.outcome) + age + (rc.error ? " — " + rc.error : "") });
    (rc.warnings || []).forEach(w => facts.push({ cls: "warn", text: ph(w) }));
  }
  if (n.edge && n.edge.ok === false) {
    const age = n.edge.age_sec >= 0 ? " · " + n.edge.age_sec + t("s") + " " + t("ago") : "";
    facts.push({ cls: "err", text: t("edge :443 unreachable") + age + (n.edge.error ? " — " + n.edge.error : "") });
  }
  if (n.billing && n.billing.paid_until) {
    const dl = n.billing_days_left;
    if (dl != null && dl <= 7) facts.push({ cls: dl < 0 ? "err" : "warn", text: t("paid until") + " " + fmtDMY(n.billing.paid_until) + " · " + (dl < 0 ? (-dl) + t("d") + " " + t("overdue") : dl + t("d") + " " + t("left")) });
  }
  if (n.metrics_age_sec >= 0 && n.metrics_age_sec > 180) facts.push({ cls: "warn", text: t("stale") });
  const okBits = [];
  if (n.reconcile && n.reconcile.ok) okBits.push(t("config synced") + (n.reconcile.age_sec >= 0 ? " · " + n.reconcile.age_sec + t("s") : ""));
  if (n.edge && n.edge.ok) okBits.push(t("edge :443 reachable") + (n.edge.age_sec >= 0 ? " · " + n.edge.age_sec + t("s") : ""));
  return { facts, okBits };
}

// NodeRow: a node at full width, identity and health on one line, the three
// resources beneath it, then the month's traffic and anything wrong.
function NodeRow({ n, cp, onOpen }) {
  const s = n.system;
  const lim = n.limits || {};
  const cpRole = cpRoleOf(cp, n.id);
  const used = (n.traffic_rx_bytes || 0) + (n.traffic_tx_bytes || 0);
  const { facts, okBits } = nodeChecks(n);

  return html`<article class="node node-click" tabindex=0 role=link
      onClick=${onOpen} onKeyDown=${e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen(); } }}>
    <div class=node-head>
      <span class=nm>${n.name || n.id}</span>
      <span class=${"pill " + n.role}>${t(n.role)}</span>
      ${cpRole ? cpBadge(cpRole) : ""}
      <${StateCell} node=${n} ov=${n} />
      <span class=meta>${t("updated")} ${n.metrics_age_sec < 0 ? t("never") : n.metrics_age_sec + t("s") + " " + t("ago")}${s && s.uptime_sec > 0 ? " · " + t("up") + " " + Math.floor(s.uptime_sec / 86400) + t("d") : ""}</span>
      <span class=chev aria-hidden=true>›</span>
    </div>
    ${!s ? html`<p class=meta style=${{ margin: ".7rem 0 0" }}>${t("no metrics yet")}</p>` : html`
      <div class=meters>
        ${Meter({ label: t("CPU load"), value: s.load1, max: s.cpu_cores || 1, text: (s.load1 || 0).toFixed(2) + " / " + (s.cpu_cores || 1) })}
        ${Meter({ label: t("Memory"), value: s.mem_used_mb, max: lim.memory_mb || s.mem_total_mb || 1,
                  text: (s.mem_used_mb / 1024).toFixed(1) + " / " + ((lim.memory_mb || s.mem_total_mb) / 1024).toFixed(1) + " GiB" })}
        ${Meter({ label: t("Disk"), value: s.disk_used_gb, max: lim.disk_gb || s.disk_total_gb || 1,
                  text: s.disk_used_gb + " / " + (lim.disk_gb || s.disk_total_gb) + " GiB" })}
      </div>`}
    <div class=node-foot>
      <span class=tv>${humanBytes(used)} <small>${t("this month")}</small></span>
      <span class=meta>${lim.traffic_gb ? tf("of {n} GiB", { n: lim.traffic_gb }) : t("no limit set")}</span>
      ${okBits.length > 0 && html`<span class=meta>${okBits.join(" · ")}</span>`}
    </div>
    ${facts.length > 0 && html`<div class=node-facts>${facts.map((f, i) => html`<p key=${i} class=${f.cls}>${f.text}</p>`)}</div>`}
  </article>`;
}

// NodeCard: one server opened up: its state and load, what it moved this month,
// its graphs, and the last things the journal heard from it. The Overview promises
// "graphs and limits" behind a node; this is what that promise opens.
// What an exit carries, which is the question the Drain button asks and the one
// the panel had no answer to: the groups that leave by it and the rules that
// name it were only ever counted inside the drain confirmation, and a rule that
// names an exit as its fallback was not counted at all. Each line says what the
// rule itself declares should happen while the node is out, so the decision is
// made against the answer rather than against a guess.
function ExitDependents({ state, node, traffic, onClose }) {
  const { groups, rules } = dependentsOfExit(state, node.id);
  const groupOf = new Map(((state && state.users) || []).map(u => [u.id, u.group_id]));
  const onlineIn = (gid) => ((traffic && traffic.users) || []).filter(u => u.active && groupOf.get(u.user_id) === gid).length;
  const toRule = (name) => { onClose(); navigateTo("route_policies", name); };
  if (groups.length === 0 && rules.length === 0) {
    return html`<div class=nc-dep>
      <span class=caps>${t("What leaves through this exit")}</span>
      <p class=meta>${t("Nothing points at this exit — taking it out changes nothing.")}</p>
    </div>`;
  }
  return html`<div class=nc-dep>
    <span class=caps>${t("What leaves through this exit")}</span>
    ${groups.map(g => html`<div key=${g.id} class=dep-row>
      <span class=dep-what>${g.name || g.id}</span>
      <span class=meta>${t("a group")} · ${plural(onlineIn(g.id), "client")} ${t("online")}</span>
      <span class=meta>${t("moves to another exit in rotation, or leaves from the entry node")}</span>
    </div>`)}
    ${rules.map(({ pol, refs }) => html`<div key=${pol.id} class=dep-row>
      <a class=dep-what role=link tabindex=0 onClick=${() => toRule(pol.name || pol.id)}
         onKeyDown=${e => { if (e.key === "Enter") toRule(pol.name || pol.id); }}>${pol.name || pol.id}</a>
      <span class=meta>${t("a rule")} · ${refWords(refs)}</span>
      <span class=meta>${whenExitIsOut(pol, refs, state)}</span>
    </div>`)}
    ${rules.length > 0 && html`<a class=more role=link tabindex=0 onClick=${() => toRule(node.name || node.id)}
       onKeyDown=${e => { if (e.key === "Enter") toRule(node.name || node.id); }}>${t("All the rules that name this exit")} ›</a>`}
  </div>`;
}

function NodeCard({ node, cfg, state, overview, traffic, onClose, setModal, reload, notify }) {
  const ref = useModal(onClose);
  const [events, setEvents] = useState(null);
  const ov = ((overview && overview.nodes) || []).find(x => x.id === node.id) || {};
  const n = { ...node, ...ov, role: node.public_role };
  const name = n.name || n.id;
  const cp = (state && state.control_plane) || {};
  // Events carry no node column, so the node's journal is the journal filtered by
  // its name, the same filter the "full log" link hands over.
  useEffect(() => {
    let live = true;
    const needles = [name, node.id].filter(Boolean);
    api("GET", "/api/events?lang=" + LANG).then(r => {
      if (!live) return;
      // The watchdog repeats an alert every cycle it still holds, so keep the
      // newest of each distinct message; five copies of one line say no more.
      const seen = new Set();
      const mine = (r.events || []).filter(e => needles.some(x => String(e.message || "").includes(x)));
      setEvents(mine.filter(e => { const m = eventText(e.message); if (seen.has(m)) return false; seen.add(m); return true; }).slice(0, 5));
    }, () => { if (live) setEvents([]); });
    return () => { live = false; };
  }, [node.id, name]);

  const sys = n.system;
  const lim = n.limits || {};
  const used = (n.traffic_rx_bytes || 0) + (n.traffic_tx_bytes || 0);
  const { facts, okBits } = nodeChecks(n);
  const st = nodeStatus(n, n);
  const cpRole = cpRoleOf(cp, n.id);
  const toLog = () => { onClose(); navigateTo("logs", name); };

  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class="modal wide" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header>
          <h3 id=modal-title>${name}</h3>
          <button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button>
        </header>
        <div class=body>
          <div class=nc-head>
            <span class=${"pill " + n.role}>${t(n.role)}</span>
            ${cpRole ? cpBadge(cpRole) : ""}
            <span class=nstate title=${st.title}><span class=dot style=${{ background: st.color }}></span>${t(st.label)}</span>
            <span class=meta>${(n.public_ips || []).join(", ")}</span>
            <span class="meta nc-upd">${t("updated")} ${n.metrics_age_sec < 0 ? t("never") : n.metrics_age_sec + t("s") + " " + t("ago")}${sys && sys.uptime_sec > 0 ? " · " + t("up") + " " + Math.floor(sys.uptime_sec / 86400) + t("d") : ""}</span>
          </div>
          <div class=nc-acts>
            <button class="ghost sm" onClick=${() => setModal({ kind: "limits", node })}>${t("Resources & payment")}</button>
            <button class="ghost sm" onClick=${() => setModal({ kind: "form", cfg, initial: node })}>${t("Edit")}</button>
            <button class="ghost sm" onClick=${toLog}>${t("Log")}</button>
            ${cfg && cfg.maintenanceAction && html`<button class="ghost sm" onClick=${async () => { onClose(); await drainNode({ node, state, overview, traffic, path: cfg.path, reload, notify }); }}>${node.maintenance ? t("Resume") : t("Drain")}</button>`}
          </div>
          ${!sys ? html`<p class=meta>${t("no metrics yet")}</p>` : html`
            <div class=meters>
              ${Meter({ label: t("CPU load"), value: sys.load1, max: sys.cpu_cores || 1, text: (sys.load1 || 0).toFixed(2) + " / " + (sys.cpu_cores || 1) })}
              ${Meter({ label: t("Memory"), value: sys.mem_used_mb, max: lim.memory_mb || sys.mem_total_mb || 1,
                        text: (sys.mem_used_mb / 1024).toFixed(1) + " / " + ((lim.memory_mb || sys.mem_total_mb) / 1024).toFixed(1) + " GiB" })}
              ${Meter({ label: t("Disk"), value: sys.disk_used_gb, max: lim.disk_gb || sys.disk_total_gb || 1,
                        text: sys.disk_used_gb + " / " + (lim.disk_gb || sys.disk_total_gb) + " GiB" })}
            </div>`}
          <div class=node-foot>
            <span class=tv>${humanBytes(used)} <small>${t("this month")}</small></span>
            <span class=meta>${lim.traffic_gb ? tf("of {n} GiB", { n: lim.traffic_gb }) : t("no limit set")}</span>
            ${okBits.length > 0 && html`<span class=meta>${okBits.join(" · ")}</span>`}
          </div>
          ${facts.length > 0 && html`<div class=node-facts>${facts.map((f, i) => html`<p key=${i} class=${f.cls}>${f.text}</p>`)}</div>`}
          ${n.role === "exit" && html`<${ExitDependents} state=${state} node=${node} traffic=${traffic} onClose=${onClose} />`}
          <${NodeSparkTabs} nodeId=${n.id} />
          <div class=nc-log>
            <span class=caps>${t("Node log")}</span>
            ${events == null ? html`<${Skeleton} rows=${3} />`
              : events.length === 0 ? html`<p class=meta>${t("Nothing about this node in the journal yet")}</p>`
              : events.map(ev => html`<div key=${ev.id} class=evrow style=${{ cursor: "default" }}>
                  <span class=${"dot " + sevDot(ev.severity)}></span>
                  <span>${eventText(ev.message)}</span>
                  <span class=meta>${fmtAge(ev.at)}</span>
                </div>`)}
            <a class=more role=link tabindex=0 onClick=${toLog} onKeyDown=${e => { if (e.key === "Enter") toLog(); }}>${t("Full log")} ›</a>
          </div>
        </div>
        <div class=foot>
          <button class=ghost onClick=${onClose}>${t("Close")}</button>
        </div>
      </div>
    </div>`;
}

function Overview({ state, health, overview, traffic, settings, setTab, goSub, setModal, onReconcile, isAdmin, seesAll }) {
  const ov = overview;
  const [syncing, setSyncing] = useState(false);
  const [events, setEvents] = useState(null);
  const sync = async () => { setSyncing(true); try { await onReconcile(); } finally { setSyncing(false); } };
  // A way into the journal rather than a copy of it; the log tab remains the
  // place to actually read it. The sentences are built by the server in the
  // language it is asked for, so a language switch has to ask again.
  useEffect(() => { let live = true; api("GET", "/api/events?lang=" + LANG).then(r => { if (live) setEvents(r.events || []); }, () => {}); return () => { live = false; }; }, [LANG]);

  const nodes = state.nodes || [];
  const cp = state.control_plane || {};
  const ovById = {};
  ((ov && ov.nodes) || []).forEach(n => { ovById[n.id] = n; });
  // The monitor rows come from the live snapshot; fall back to the stored node
  // so a node still lists while its first telemetry is on its way.
  const rows = nodes.map(n => ({ ...n, ...(ovById[n.id] || {}), role: n.public_role, name: n.name || n.id }));
  const problems = computeProblems(state, ov, settings);
  const crit = problems.filter(p => p.sev === "critical").length;
  const drained = drainedNodes(state);
  const openNode = (n) => setModal({ kind: "nodecard", node: nodes.find(x => x.id === n.id) || n });
  // Where a problem is answered. The five most urgent lines on the page were the
  // only text on it that could not be clicked: reading them cost nothing and
  // acting on them cost four clicks through the navigation.
  const goProblem = (p) => { if (p.node) openNode(p.node); else if (p.tab === "domains") goSub("nodes", "domains"); else if (p.tab) setTab(p.tab); };

  // Which exit each group's traffic leaves by. The rail already counts servers,
  // clients and rules down the left edge, so repeating those three totals here
  // said nothing the navigation had not; this is what it cannot answer.
  const exits = nodes.filter(n => n.public_role === "exit")
    .map(n => ({ node: n, groups: groupsEgressingVia(state, n).length }))
    .sort((a, b) => b.groups - a.groups);
  const direct = (state.groups || []).filter(g => !g.default_exit_id || g.default_exit_id === "direct").length;

  const byTraffic = rows.map(n => ({ n, v: (n.traffic_rx_bytes || 0) + (n.traffic_tx_bytes || 0) }))
                        .sort((a, b) => b.v - a.v);
  const peak = byTraffic.length ? byTraffic[0].v : 0;
  const age = health && health.last_reconcile_at ? Math.max(0, Math.round((Date.now() - new Date(health.last_reconcile_at)) / 1000)) : null;

  // An event names its subject inside its own sentence, so a click can open the
  // journal already narrowed to it.
  const subjectOf = (msg) => { const hit = nodes.find(n => String(msg || "").includes(n.name || n.id)); return hit ? (hit.name || hit.id) : ""; };
  // A server that is already a red line at the top of the page does not need to
  // say the same thing again further down, in the watchdog's words this time.
  const alarmed = new Set(problems.filter(p => p.sev === "critical" && p.node).map(p => p.node.name || p.node.id));
  const seen = new Set();
  const feed = [];
  for (const ev of (events || [])) {
    if (feed.length >= 3) break;
    if (ev.severity === "critical" && alarmed.has(subjectOf(ev.message))) continue;
    // A watchdog says the same sentence every cycle; the journal is where the
    // repeats are counted, not here.
    if (seen.has(ev.message)) continue;
    seen.add(ev.message);
    feed.push(ev);
  }

  return html`
    <${PageHead}
      title=${t("Overview")}
      actions=${isAdmin && html`<button class=ghost disabled=${syncing} title=${t("Rebuild config from the database and push it to every node now")} onClick=${sync}>${syncing ? t("Syncing…") : t("Sync now")}</button>`} />

    <div class=statusline>
      <span class=${"dot " + (crit ? "err" : problems.length ? "warn" : "")}></span>
      <span class=st-text>${problems.length === 0 ? t("All systems normal") : tf("{n} need attention", { n: problems.length })}</span>
      ${drained.length > 0 && html`<span class=muted>${tf("{n} in maintenance", { n: counted(drained.length, "server") })} · ${drained.map(n => n.name || n.id).join(", ")}</span>`}
      ${age != null && html`<span class=meta>${tf("synced {n}s ago", { n: age })}</span>`}
    </div>
    ${problems.length > 0 && html`<div class="node-facts problems">
      ${problems.map((p, i) => html`<a key=${i} class=${p.sev === "critical" ? "err" : "warn"} role=link tabindex=0
          onClick=${() => goProblem(p)} onKeyDown=${e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); goProblem(p); } }}>${p.text}</a>`)}
    </div>`}

    <div class=ov-grid>
      <div>
        <div class=section>
          <h2>${t("Servers")}</h2>
          ${rows.length === 0
            ? html`<p class=meta>${t("Collecting metrics…")}</p>`
            : rows.map(n => html`<${NodeRow} key=${n.id} n=${n} cp=${cp} onOpen=${() => openNode(n)} />`)}
        </div>
      </div>

      <div class=aside>
        ${exits.length > 0 && html`<div class=section>
          <h2>${t("Groups by exit")}</h2>
          <div class=kvwrap>
            ${exits.map(e => html`<a key=${e.node.id} class=kvrow role=link tabindex=0
                onClick=${() => openNode(e.node)} onKeyDown=${ev => { if (ev.key === "Enter") openNode(e.node); }}>
              <span>${e.node.name || e.node.id}${e.node.maintenance ? html` <span class=muted>· ${t("in maintenance")}</span>` : null}</span> <b>${e.groups}</b></a>`)}
            ${direct > 0 && html`<div class="kvrow static"><span>${t("directly from the entry node")}</span><b>${direct}</b></div>`}
          </div>
        </div>`}

        ${traffic && html`<div class=section>
          <h2>${t("Online")}</h2>
          <div class=kvwrap>
            <div class="kvrow static"><span>${t("My clients")}</span><b>${traffic.active_count || 0}</b></div>
            ${(traffic.others || []).length > 0 && html`<div class="kvrow static">
              <span>${t("Clients of other scopes")}</span><b>${traffic.others.reduce((n, o) => n + (o.online || 0), 0)}</b>
            </div>`}
          </div>
        </div>`}

        ${byTraffic.length > 0 && html`<div class=section>
          <h2>${t("Traffic this month, GiB")}</h2>
          ${peak === 0
            ? html`<p class=meta style=${{ margin: ".7rem 0 0" }}>${t("Nothing has gone out yet this month")}</p>`
            : html`<div style=${{ paddingTop: ".9rem" }}>
                ${byTraffic.map(({ n, v }) => html`<a key=${n.id} class=trow role=link tabindex=0 onClick=${() => openNode(n)}
                    onKeyDown=${e => { if (e.key === "Enter") openNode(n); }}>
                  <span class=tname title=${n.name || n.id}>${n.name || n.id}</span>
                  <div class=tbar><span style=${{ width: Math.max(2, Math.round((v / peak) * 100)) + "%" }}></span></div>
                  <b>${(v / GIB).toFixed(2)}</b>
                </a>`)}
              </div>`}
        </div>`}

        <div class=section>
          <h2>${t("Recent events")}</h2>
          <div style=${{ paddingTop: ".7rem" }}>
            ${events == null ? html`<${Skeleton} rows=${3} />`
              : feed.length === 0 ? html`<p class=meta>${t("No events yet")}</p>`
              : feed.map(ev => html`<a key=${ev.id} class=evrow role=link tabindex=0 onClick=${() => navigateTo("logs", subjectOf(ev.message))}
                  onKeyDown=${e => { if (e.key === "Enter") navigateTo("logs", subjectOf(ev.message)); }}>
                <span class=${"dot " + sevDot(ev.severity)}></span>
                <span>${eventText(ev.message)}</span>
                <span class=meta>${fmtAge(ev.at)}</span>
              </a>`)}
          </div>
          <a class=more role=link tabindex=0 onClick=${() => setTab("logs")} onKeyDown=${e => { if (e.key === "Enter") setTab("logs"); }}>${t("Full log")} ›</a>
        </div>

      </div>
    </div>`;
}

// fmtAge renders how long ago something happened, in the shortest honest unit.
// fmtDue reads a future moment the way a schedule is spoken about: how long
// until it, or "any moment" once it has come round and the loop has not woken
// yet. A wall-clock date would make the reader do the subtraction.
function fmtDue(at) {
  const secs = Math.round((new Date(at) - Date.now()) / 1000);
  if (secs <= 60) return t("any moment");
  if (secs < 3600) return tf("in {n}", { n: Math.round(secs / 60) + t("m") });
  if (secs < 86400) return tf("in {n}", { n: Math.round(secs / 3600) + t("h") });
  return tf("in {n}", { n: Math.round(secs / 86400) + t("d") });
}

function fmtAge(at) {
  const secs = Math.max(0, Math.round((Date.now() - new Date(at)) / 1000));
  if (secs < 60) return secs + t("s");
  if (secs < 3600) return Math.round(secs / 60) + t("m");
  if (secs < 86400) return Math.round(secs / 3600) + t("h");
  return Math.round(secs / 86400) + t("d");
}

/* ---------------- traffic ---------------- */
function humanBytes(n) {
  n = Number(n) || 0;
  // IEC units (base-1024): the loop divides by 1024, so the labels must be KiB/
  // MiB/… not KB/MB (which are base-1000), keeping figure and unit honest.
  const u = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : +n.toFixed(2)) + " " + u[i]; // +toFixed strips trailing zeros: 6.00→6, 3.20→3.2
}

// humanRate renders a byte/second figure for the activity column.
function humanRate(bytes, secs) {
  if (!secs || secs <= 0) return "";
  return humanBytes(Number(bytes || 0) / secs) + "/s";
}


// Time ranges offered by every traffic graph (minutes → label). Week and month
// are backed by the ~31-day sample retention.
const CHART_RANGES = [[60, "1h"], [360, "6h"], [1440, "24h"], [10080, "7d"], [43200, "30d"]];
const RX_COLOR = "var(--ok)", TX_COLOR = "var(--hue-entry)"; // ↑ upload/out, ↓ download/in

function RangePicker({ mins, setMins }) {
  return html`<span class=seg>
    ${CHART_RANGES.map(([m, lab]) => html`<button key=${m} class=${mins === m ? "on" : ""} aria-pressed=${mins === m} onClick=${e => { e.stopPropagation(); setMins(m); }}>${t(lab)}</button>`)}</span>`;
}

// niceStep returns a "round" tick step (1/2/5 × 10ⁿ) so axis labels land on clean
// values (5 MB, 10 MB…) instead of arbitrary fractions of the peak.
function niceStep(max, targetTicks) {
  if (max <= 0) return 1;
  const raw = max / Math.max(1, targetTicks);
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const norm = raw / mag;
  const mult = norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 5 ? 5 : 10;
  return mult * mag;
}

// SeriesChart draws rx/tx as two scaled line series with a legend, fixed-step
// axis gridlines/labels and a hover readout (value-at-time), shared by the
// per-user and per-node graphs (no chart library). upLabel/downLabel name the two
// series (client up/down vs node out/in). Moving the cursor over the plot snaps to
// the nearest bucket and shows a guide line, dots on each series and a tooltip.
function SeriesChart({ series, upLabel, downLabel, compact = false }) {
  const W = 600, H = compact ? 48 : 160, padX = 4, padY = compact ? 5 : 12;
  const [hover, setHover] = useState(null); // bucket index under the cursor, or null
  const rx = series.map(s => Number(s.rx_bytes) || 0);
  const tx = series.map(s => Number(s.tx_bytes) || 0);
  const peak = Math.max(1, ...rx, ...tx);
  const n = series.length;
  const xAt = i => padX + (n <= 1 ? (W - 2 * padX) / 2 : i * (W - 2 * padX) / (n - 1));
  const yAt = v => H - padY - (v / peak) * (H - 2 * padY);
  const poly = (arr, stroke) => html`<polyline points=${arr.map((v, i) => xAt(i) + "," + yAt(v).toFixed(1)).join(" ")} fill=none stroke=${stroke} stroke-width=1.5 stroke-linejoin=round vector-effect="non-scaling-stroke" />`;
  const swatch = (c, lab) => html`<span style=${{ display: "inline-flex", alignItems: "center", gap: ".3rem" }}><span style=${{ width: "14px", height: "0", borderTop: "2px solid " + c, display: "inline-block" }}></span>${lab}</span>`;
  // Map the cursor to the nearest bucket. viewBox is 0..W but the SVG stretches to
  // the container width (preserveAspectRatio=none), so scale by the real rect.
  const onMove = (e) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width <= 0 || n === 0) return;
    const frac = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    setHover(Math.round(frac * (n - 1)));
  };
  // A window spanning more than a day needs the date on the x-axis and hover, not
  // just the clock; otherwise "03:00" is ambiguous across the 7d/30d ranges.
  const firstAt = n ? series[0].at : null, lastAt = n ? series[n - 1].at : null;
  const spanMs = firstAt && lastAt ? (new Date(lastAt) - new Date(firstAt)) : 0;
  const multiDay = spanMs > 24 * 3600 * 1000;
  const fmtAt = (at) => {
    if (!at) return "";
    const d = new Date(at);
    return multiDay ? dayMonth(d) + " " + clock(d) : clock(d);
  };
  const hv = hover != null && hover >= 0 && hover < n ? series[hover] : null;
  const hvLeftPct = hv ? (xAt(hover) / W) * 100 : 0;
  // Fixed-step ticks: y at round byte steps (0, step, 2·step… ≤ peak); x at evenly
  // spaced buckets. Labels are HTML overlays (SVG text would stretch under
  // preserveAspectRatio=none); gridlines stay in the SVG.
  const yStep = niceStep(peak, 3);
  const yTicks = [];
  for (let v = 0; v <= peak + 1e-9 && yTicks.length < 6; v += yStep) yTicks.push(v);
  const xCount = compact || n <= 1 ? 0 : Math.min(5, n);
  const xTicks = [];
  for (let k = 0; k < xCount; k++) xTicks.push(Math.round(k * (n - 1) / (xCount - 1)));
  const yLabel = (v) => ({ position: "absolute", left: "2px", top: (yAt(v) / H * 100) + "%", transform: "translateY(-50%)", fontSize: "10px", pointerEvents: "none", color: "var(--muted, #888)", background: "var(--bg, #111)", padding: "0 2px", lineHeight: 1 });
  return html`<div class=${compact ? "" : "chartbox"} style=${{ padding: compact ? "0" : ".5rem" }} onClick=${e => e.stopPropagation()}>
    <div class=muted style=${{ marginBottom: ".3rem", display: "flex", alignItems: "center", gap: ".9rem", flexWrap: "wrap", fontSize: "11px" }}>
      ${swatch(RX_COLOR, "↑ " + t(upLabel))}${swatch(TX_COLOR, "↓ " + t(downLabel))}
    </div>
    <div style=${{ position: "relative" }} onMouseMove=${onMove} onMouseLeave=${() => setHover(null)}>
      <svg viewBox=${`0 0 ${W} ${H}`} style=${{ width: "100%", height: H + "px", display: "block" }} preserveAspectRatio="none">
        ${!compact && yTicks.map(v => html`<line key=${"y" + v} x1=0 y1=${yAt(v)} x2=${W} y2=${yAt(v)} stroke="var(--border)" stroke-width=${v === 0 ? 1 : 0.5} stroke-dasharray=${v === 0 ? "none" : "3 3"} vector-effect="non-scaling-stroke" />`)}
        ${compact && html`<line x1=0 y1=${H - padY} x2=${W} y2=${H - padY} stroke="var(--border)" stroke-width=1 vector-effect="non-scaling-stroke" />`}
        ${!compact && xTicks.map(i => html`<line key=${"x" + i} x1=${xAt(i)} y1=${padY} x2=${xAt(i)} y2=${H - padY} stroke="var(--border)" stroke-width=0.5 stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />`)}
        ${poly(rx, RX_COLOR)}${poly(tx, TX_COLOR)}
        ${hv && html`<line x1=${xAt(hover)} y1=${padY} x2=${xAt(hover)} y2=${H - padY} stroke="var(--fg, #ccc)" stroke-width=1 vector-effect="non-scaling-stroke" />`}
        ${hv && html`<circle cx=${xAt(hover)} cy=${yAt(rx[hover])} r=2.5 fill=${RX_COLOR} vector-effect="non-scaling-stroke" />`}
        ${hv && html`<circle cx=${xAt(hover)} cy=${yAt(tx[hover])} r=2.5 fill=${TX_COLOR} vector-effect="non-scaling-stroke" />`}
      </svg>
      ${!compact && yTicks.map(v => html`<span key=${"yl" + v} style=${yLabel(v)}>${humanBytes(v)}</span>`)}
      ${hv && html`<div class=chart-tip style=${{ left: hvLeftPct + "%", transform: (hvLeftPct > 60 ? "translateX(-100%)" : "translateX(4px)") }}>
        ${hv.at && html`<div class=muted>${fmtAt(hv.at)}</div>`}
        <div><span style=${{ color: RX_COLOR }}>↑</span> ${humanBytes(rx[hover])}</div>
        <div><span style=${{ color: TX_COLOR }}>↓</span> ${humanBytes(tx[hover])}</div>
      </div>`}
    </div>
    ${!compact && xCount > 0 && html`<div style=${{ position: "relative", height: "14px", marginTop: "2px" }}>
      ${xTicks.map((i, k) => html`<span key=${"xl" + i} style=${{ position: "absolute", left: (xAt(i) / W * 100) + "%", transform: k === 0 ? "translateX(0)" : k === xTicks.length - 1 ? "translateX(-100%)" : "translateX(-50%)", fontSize: "10px", color: "var(--muted, #888)", whiteSpace: "nowrap" }}>${fmtAt(series[i].at)}</span>`)}
    </div>`}
  </div>`;
}

// GaugeChart is SeriesChart's single-line sibling for a gauge metric (CPU %,
// memory MB) instead of a paired rx/tx byte counter, with the same mechanics,
// but the Y axis and hover readout go through `format` instead of the
// bytes-only humanBytes, and there is one line instead of two.
function GaugeChart({ series, valueKey, color, label, format }) {
  const W = 600, H = 160, padX = 4, padY = 12;
  const [hover, setHover] = useState(null);
  const vals = series.map(s => Number(s[valueKey]) || 0);
  const peak = Math.max(1, ...vals);
  const n = series.length;
  const xAt = i => padX + (n <= 1 ? (W - 2 * padX) / 2 : i * (W - 2 * padX) / (n - 1));
  const yAt = v => H - padY - (v / peak) * (H - 2 * padY);
  const onMove = (e) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (rect.width <= 0 || n === 0) return;
    const frac = Math.min(1, Math.max(0, (e.clientX - rect.left) / rect.width));
    setHover(Math.round(frac * (n - 1)));
  };
  const firstAt = n ? series[0].at : null, lastAt = n ? series[n - 1].at : null;
  const spanMs = firstAt && lastAt ? (new Date(lastAt) - new Date(firstAt)) : 0;
  const multiDay = spanMs > 24 * 3600 * 1000;
  const fmtAt = (at) => {
    if (!at) return "";
    const d = new Date(at);
    return multiDay ? dayMonth(d) + " " + clock(d) : clock(d);
  };
  const hv = hover != null && hover >= 0 && hover < n ? series[hover] : null;
  const hvLeftPct = hv ? (xAt(hover) / W) * 100 : 0;
  const yStep = niceStep(peak, 3);
  const yTicks = [];
  for (let v = 0; v <= peak + 1e-9 && yTicks.length < 6; v += yStep) yTicks.push(v);
  const xCount = n <= 1 ? 0 : Math.min(5, n);
  const xTicks = [];
  for (let k = 0; k < xCount; k++) xTicks.push(Math.round(k * (n - 1) / (xCount - 1)));
  const yLabel = (v) => ({ position: "absolute", left: "2px", top: (yAt(v) / H * 100) + "%", transform: "translateY(-50%)", fontSize: "10px", pointerEvents: "none", color: "var(--muted, #888)", background: "var(--bg, #111)", padding: "0 2px", lineHeight: 1 });
  return html`<div class=chartbox style=${{ padding: ".5rem" }} onClick=${e => e.stopPropagation()}>
    <div class=muted style=${{ marginBottom: ".3rem", display: "flex", alignItems: "center", gap: ".9rem", flexWrap: "wrap", fontSize: "11px" }}>
      <span style=${{ display: "inline-flex", alignItems: "center", gap: ".3rem" }}><span style=${{ width: "14px", height: "0", borderTop: "2px solid " + color, display: "inline-block" }}></span>${label}</span>
      <span>${tf("peak {v}", { v: format(peak) })}</span>
    </div>
    <div style=${{ position: "relative" }} onMouseMove=${onMove} onMouseLeave=${() => setHover(null)}>
      <svg viewBox=${`0 0 ${W} ${H}`} style=${{ width: "100%", height: H + "px", display: "block" }} preserveAspectRatio="none">
        ${yTicks.map(v => html`<line key=${"y" + v} x1=0 y1=${yAt(v)} x2=${W} y2=${yAt(v)} stroke="var(--border)" stroke-width=${v === 0 ? 1 : 0.5} stroke-dasharray=${v === 0 ? "none" : "3 3"} vector-effect="non-scaling-stroke" />`)}
        ${xTicks.map(i => html`<line key=${"x" + i} x1=${xAt(i)} y1=${padY} x2=${xAt(i)} y2=${H - padY} stroke="var(--border)" stroke-width=0.5 stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />`)}
        <polyline points=${vals.map((v, i) => xAt(i) + "," + yAt(v).toFixed(1)).join(" ")} fill=none stroke=${color} stroke-width=1.5 stroke-linejoin=round vector-effect="non-scaling-stroke" />
        ${hv && html`<line x1=${xAt(hover)} y1=${padY} x2=${xAt(hover)} y2=${H - padY} stroke="var(--fg, #ccc)" stroke-width=1 vector-effect="non-scaling-stroke" />`}
        ${hv && html`<circle cx=${xAt(hover)} cy=${yAt(vals[hover])} r=2.5 fill=${color} vector-effect="non-scaling-stroke" />`}
      </svg>
      ${yTicks.map(v => html`<span key=${"yl" + v} style=${yLabel(v)}>${format(v)}</span>`)}
      ${hv && html`<div class=chart-tip style=${{ left: hvLeftPct + "%", transform: (hvLeftPct > 60 ? "translateX(-100%)" : "translateX(4px)") }}>
        ${hv.at && html`<div class=muted>${fmtAt(hv.at)}</div>`}
        <div><span style=${{ color }}>●</span> ${format(vals[hover])}</div>
      </div>`}
    </div>
    ${xCount > 0 && html`<div style=${{ position: "relative", height: "14px", marginTop: "2px" }}>
      ${xTicks.map((i, k) => html`<span key=${"xl" + i} style=${{ position: "absolute", left: (xAt(i) / W * 100) + "%", transform: k === 0 ? "translateX(0)" : k === xTicks.length - 1 ? "translateX(-100%)" : "translateX(-50%)", fontSize: "10px", color: "var(--muted, #888)", whiteSpace: "nowrap" }}>${fmtAt(series[i].at)}</span>`)}
    </div>`}
  </div>`;
}

// useSeriesAt fetches a bucketed series for an externally-controlled range, so
// several views (e.g. Traffic/CPU/Memory) can share one range control instead of
// each resetting its own on a view switch. useSeries (below) is the common case
// of a view that owns its own range.
function useSeriesAt(path, mins) {
  const [series, setSeries] = useState(null);
  useEffect(() => {
    let live = true;
    setSeries(null);
    api("GET", path(mins)).then(d => { if (live) setSeries(d.series || []); }).catch(() => { if (live) setSeries([]); });
    return () => { live = false; };
  }, [path, mins]);
  return series;
}

// useSeries fetches a bucketed traffic series for the chosen range.
function useSeries(path) {
  const [mins, setMins] = useState(1440);
  const series = useSeriesAt(path, mins);
  return [series, mins, setMins];
}

// TrafficSpark: one user's recent rx/tx, as lines with a legend and range picker.
function TrafficSpark({ userId }) {
  const path = useCallback(m => `/api/traffic/series?user=${encodeURIComponent(userId)}&minutes=${m}`, [userId]);
  const [series, mins, setMins] = useSeries(path);
  return html`<div style=${{ padding: ".3rem .5rem" }} onClick=${e => e.stopPropagation()}>
    <div class=muted style=${{ display: "flex", justifyContent: "flex-end" }}><${RangePicker} mins=${mins} setMins=${setMins} /></div>
    ${series == null ? html`<div class=muted style=${{ padding: "2.2rem .5rem", textAlign: "center" }}>${t("Loading…")}</div>`
      : series.length === 0 ? html`<div class=muted style=${{ padding: "2.2rem .5rem", textAlign: "center" }}>${t("no traffic in this window")}</div>`
      : html`<${SeriesChart} series=${series} upLabel="Uploaded" downLabel="Downloaded" />`}
  </div>`;
}

// NodeSparkTabs: the node graph dialog, with Traffic/CPU/Memory as
// sibling views sharing one range control (metric switcher on the left, the
// existing 1h–30d RangePicker stays on the right) instead of three separate
// graphs. CPU is rendered as 0-100% utilization (load1/cores); Memory as used
// MB against total. Both series hooks run unconditionally every render (Rules
// of Hooks); only the inactive one's result goes unused.
function NodeSparkTabs({ nodeId }) {
  const [view, setView] = useState("traffic");
  const [mins, setMins] = useState(1440);
  const trafficPath = useCallback(m => `/api/nodes/series?node=${encodeURIComponent(nodeId)}&minutes=${m}`, [nodeId]);
  const resourcePath = useCallback(m => `/api/nodes/resource-series?node=${encodeURIComponent(nodeId)}&minutes=${m}`, [nodeId]);
  const trafficSeries = useSeriesAt(trafficPath, mins);
  const resourceSeries = useSeriesAt(resourcePath, mins);
  const views = [["traffic", t("Traffic")], ["cpu", t("CPU")], ["mem", t("Memory")]];
  const empty = (msg) => html`<div class=muted style=${{ padding: "2.2rem .5rem", textAlign: "center" }}>${msg}</div>`;
  return html`<div style=${{ padding: ".3rem .5rem" }} onClick=${e => e.stopPropagation()}>
    <div style=${{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: ".3rem" }}>
      <span class=seg style=${{ marginLeft: 0 }}>
        ${views.map(([k, lab]) => html`<button key=${k} class=${view === k ? "on" : ""} aria-pressed=${view === k} onClick=${e => { e.stopPropagation(); setView(k); }}>${lab}</button>`)}
      </span>
      <${RangePicker} mins=${mins} setMins=${setMins} />
    </div>
    ${view === "traffic"
      ? (trafficSeries == null ? empty(t("Loading…"))
        : trafficSeries.length === 0 ? empty(t("no traffic in this window"))
        : html`<${SeriesChart} series=${trafficSeries} upLabel="Inbound" downLabel="Outbound" />`)
      : resourceSeries == null ? empty(t("Loading…"))
      : resourceSeries.length === 0 ? empty(t("no metrics in this window"))
      : view === "cpu"
      ? html`<${GaugeChart} series=${resourceSeries} valueKey="cpu_pct" color=${RX_COLOR} label=${t("CPU load")} format=${v => v.toFixed(0) + "%"} />`
      : html`<${GaugeChart} series=${resourceSeries} valueKey="mem_used_mb" color=${TX_COLOR} label=${t("Memory used")} format=${v => humanBytes(v * 1024 * 1024)} />`}
  </div>`;
}

function GraphModal({ series, entityId, name, onClose }) {
  const ref = useModal(onClose);
  return html`<div class=overlay onClick=${e => e.target === e.currentTarget && ref.close()}>
    <div class="modal graph-modal" ref=${ref} role=dialog aria-modal=true aria-labelledby=graph-title>
      <header><h3 id=graph-title>${t("Graph")} — ${name}</h3><button class="ghost sm" aria-label=${t("Close")} onClick=${ref.close}>✕</button></header>
      <div class=body>${series === "node" ? html`<${NodeSparkTabs} nodeId=${entityId} />` : html`<${TrafficSpark} userId=${entityId} />`}</div>
      <div class=foot><button class=ghost onClick=${ref.close}>${t("Close")}</button></div>
    </div>
  </div>`;
}

/* ---------------- language toggle ---------------- */
function LangToggle({ lang, switchLang }) {
  const btn = (l, label) => html`<button class=${"ghost sm" + (lang === l ? " active" : "")}
    style=${lang === l ? { fontWeight: 700 } : {}} onClick=${() => switchLang(l)}>${label}</button>`;
  return html`<span title=${t("Language")} style=${{ display: "inline-flex", gap: "2px" }}>${btn("ru", "RU")}${btn("en", "EN")}</span>`;
}

// StaleBanner: the panel polls its telemetry, and when those calls start
// failing the figures on screen quietly freeze at their last good values. Two
// consecutive failures is enough to say so; one is usually a restart.
function StaleBanner({ live, onRetry }) {
  const [busy, setBusy] = useState(false);
  const mins = Math.max(0, Math.round((Date.now() - live.okAt) / 60000));
  const retry = async () => { setBusy(true); try { await onRetry(); } catch {} finally { setBusy(false); } };
  return html`<div class=banner>
    <h3>${t("Figures are not refreshing")}</h3>
    <p>${mins >= 1
      ? tf("{what} last updated {m} min ago, so what you see below may be out of date.", { what: live.what, m: mins })
      : tf("{what} stopped updating, so what you see below may be out of date.", { what: live.what })}</p>
    ${live.error && html`<p class=detail>${live.error}</p>`}
    <button class="ghost sm" disabled=${busy} onClick=${retry}>${busy ? t("Working…") : t("Try again")}</button>
  </div>`;
}

// Skeleton: a placeholder shaped like what is coming, so the layout does not
// jump when the data lands. Widths vary a little so it reads as content.
function Skeleton({ rows = 4, cols = 1 }) {
  const w = (r, c) => 55 + ((r * 7 + c * 23) % 40) + "%";
  return html`<div aria-hidden=true style=${{ display: "grid", gap: ".85rem", padding: ".5rem 0" }}>
    ${Array.from({ length: rows }, (_, r) => html`<div key=${r} style=${{ display: "grid", gridTemplateColumns: `repeat(${cols}, 1fr)`, gap: "1.2rem" }}>
      ${Array.from({ length: cols }, (_, c) => html`<span key=${c} class=skel style=${{ width: w(r, c) }}></span>`)}
    </div>`)}
  </div>`;
}

/* ---------------- subtabs ---------------- */
// A within-page section switcher, so a tab that carries several entities (Nodes,
// Access) shows one section at a time instead of one long scroll.
function SubTabs({ tabs, active, onSelect }) {
  return html`<div class=subtabs>
    ${tabs.map(([key, label]) => html`<button key=${key} class=${"subtab" + (key === active ? " active" : "")} onClick=${() => onSelect(key)}>${label}</button>`)}
  </div>`;
}

/* ---------------- nodes tab (nodes + domains + HA) ---------------- */
// Domains are a property of entry nodes and HA is about the exits, so both live
// under Nodes as sibling sections, switched with subtabs.
function NodesPanel({ sc, state, overview, traffic, reload, notify, setModal, isAdmin, me }) {
  // Operators manage their own nodes (provision/drain/decommission) and their own
  // domains/TLS, viewing the rest read-only; HA is infra-only (admin).
  const tabs = isAdmin
    ? [["nodes", t("Servers")], ["domains", t("Domains")], ["ha", t("High availability")]]
    : [["nodes", t("Servers")], ["domains", t("Domains")]];
  const [sub, setSub] = useSubtab("nodes", "nodes", tabs.map(([k]) => k));
  const cur = tabs.some(([k]) => k === sub) ? sub : "nodes";
  const nodes = state.nodes || [];
  // Freshness of the telemetry is the fact worth carrying here; "reached over
  // mTLS" never changes and told nobody anything.
  const freshest = ((overview && overview.nodes) || [])
    .map(n => n.metrics_age_sec).filter(v => typeof v === "number" && v >= 0).sort((a, b) => a - b)[0];
  const summaryOf = {
    nodes: plural(nodes.length, "server") + (freshest == null ? "" : " · " + tf("telemetry {age} ago", { age: fmtAge(new Date(Date.now() - freshest * 1000).toISOString()) })),
    domains: plural((state.domains || []).length, "domain"),
    ha: "",
  };
  const head = { title: t("Servers"), summary: summaryOf[cur] };
  const nav = html`<${SubTabs} tabs=${tabs} active=${cur} onSelect=${setSub} />`;
  return html`
    ${cur === "nodes"
      ? html`<${EntityPanel} head=${head} subnav=${nav} cfg=${sc.nodes} rows=${nodes} state=${state} overview=${overview} traffic=${traffic} reload=${reload} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${me} />`
      : cur === "domains"
      ? html`<${EntityPanel} head=${head} subnav=${nav} cfg=${sc.domains} rows=${state.domains || []} state=${state} reload=${reload} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${me} />`
      : html`<${PageHead} title=${head.title} />${nav}<div class=ha-panel><${HA} state=${state} overview=${overview} notify=${notify} reload=${reload} setModal=${setModal} /></div>`}`;
}

/* ---------------- routing tab (rules + geo databases) ---------------- */
// The categories a rule names live in files the panel downloads, so where those
// files come from belongs beside the rules that name them, not in settings.
function RoutingPanel({ sc, state, reload, notify, setModal, isAdmin, me, initialFilter }) {
  const tabs = [["rules", t("Rules")], ["geo", t("Geo databases")]];
  const [sub, setSub] = useSubtab("route_policies", "rules", tabs.map(([k]) => k));
  const cur = tabs.some(([k]) => k === sub) ? sub : "rules";
  const head = { title: t("Routing"), summary: plural((state.route_policies || []).length, "rule") };
  const nav = html`<${SubTabs} tabs=${tabs} active=${cur} onSelect=${setSub} />`;
  if (cur === "rules") {
    return html`<div class=routing-page><${EntityPanel} head=${head} subnav=${nav} cfg=${sc.route_policies} rows=${state.route_policies || []}
      state=${state} reload=${reload} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${me} initialFilter=${initialFilter} /></div>`;
  }
  return html`<div class=routing-page><${PageHead} title=${head.title} />${nav}<${GeoBases} state=${state} notify=${notify} isAdmin=${isAdmin} /></div>`;
}

// A tag is a file name: "geosite:youtube" in a rule is geosite-youtube.srs in a
// repository. These two turn one into the other.
const geoTag = (kind, name) => kind + "-" + String(name || "").toLowerCase();
const geoName = (tag) => tag.replace(/^geo(ip|site)-/, "");

// geoUsage counts the rules that name each tag, so the table can say which of
// these files are actually load-bearing and which are leftovers of a route test.
function geoUsage(policies) {
  const n = {};
  (policies || []).forEach(p => {
    // A rule written in the condition editor carries its categories in
    // conditions; the four legacy lists answer for everything older. The server
    // counts "in use" the same way (model.Match), and a count that disagreed
    // with it would read as a row nothing uses.
    const conds = (p.conditions || []).filter(c => c && (c.kind === "geoip" || c.kind === "geosite"));
    if (conds.length) {
      conds.forEach(c => (c.values || []).forEach(v => { const k = geoTag(c.kind, v); n[k] = (n[k] || 0) + 1; }));
      return;
    }
    (p.match_geoip || []).forEach(v => { const k = geoTag("geoip", v); n[k] = (n[k] || 0) + 1; });
    (p.match_geosite || []).forEach(v => { const k = geoTag("geosite", v); n[k] = (n[k] || 0) + 1; });
  });
  return n;
}

// GeoBases: where each database is fetched from, what the last look at the
// source found, and which of the downloaded files it makes out of date.
function GeoBases({ state, notify, isAdmin }) {
  const tbl = useScrollEdges();
  const [data, setData] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState("");
  const load = useCallback(async () => {
    try { const d = await api("GET", "/api/rulesets"); noteGeoCatalog(d); setData(d); setErr(""); }
    catch (e) { setErr(e.message); }
  }, []);
  useEffect(() => { load(); }, [load]);

  if (err) return html`<div class=card><div class=blank>${err}</div></div>`;
  if (!data) return html`<div class=card><div class=blank>${t("Loading…")}</div></div>`;

  // Every write here answers with the whole view, so one path handles all of
  // them and the table can never drift from what the server just did.
  const act = async (key, method, path, body, okMsg) => {
    setBusy(key);
    try {
      const d = await api(method, path, body);
      noteGeoCatalog(d); setData(d);
      if (okMsg) notify(okMsg, "ok");
    } catch (e) { notify(e.message, "err"); }
    setBusy("");
  };
  const check = () => act("check", "POST", "/api/rulesets/check", null, t("Sources checked"));
  const setSchedule = async (hours) => {
    try { setData(await api("POST", "/api/rulesets/schedule", { hours: Number(hours) })); }
    catch (e) { notify(e.message, "err"); }
  };

  const usage = geoUsage(state.route_policies);
  const sets = data.sets || [];
  const stale = sets.filter(s => !s.upstream_gone && s.checked_at && s.upstream_id && s.upstream_id !== s.content_id);
  const updateAll = async () => {
    const ok = await uiConfirm({
      title: tf("Update {n} from their sources?", { n: stale.length }),
      lines: [
        t("The new lists reach the nodes and start deciding where traffic goes."),
        t("Each replaced copy is kept and can be put back."),
      ],
      confirmLabel: t("Update all"),
    });
    if (ok) act("all", "POST", "/api/rulesets/update", null, t("Databases updated"));
  };
  const sources = data.sources || [];
  const SCHEDULE = [[6, t("every 6 hours")], [24, t("daily")], [72, t("every 3 days")], [168, t("weekly")], [720, t("monthly")]];
  // The oldest of the two, because a pass that reached one source and not the
  // other has not answered the question.
  const checkedAt = sources.length && sources.every(s => s.checked_at) ? sources.map(s => s.checked_at).sort()[0] : null;
  const following = sets.filter(s => s.auto_update);

  return html`
    <div class="between geo-toolbar">
        <div class=geo-when>
          <div>${checkedAt
            ? tf("Last checked {age} ago", { age: fmtAge(checkedAt) })
            : t("Never checked")}</div>
          <div class=geo-note>${data.next_check_at
            ? tf("Next check {due}", { due: fmtDue(data.next_check_at) })
            : t("The first check runs shortly.")}
            ${following.length > 0
              ? " · " + tf("{n} on auto", { n: plural(following.length, "base") })
              : " · " + t("nothing updates on its own")}</div>
        </div>
        <div class=geo-acts>
          <label class=geo-sched>${t("Check sources")}
            <select value=${String(data.check_hours)} disabled=${!isAdmin} onChange=${e => setSchedule(e.target.value)}>
              ${SCHEDULE.map(([h, label]) => html`<option key=${h} value=${String(h)}>${label}</option>`)}
            </select>
          </label>
          <button class=ghost disabled=${!isAdmin || !!busy} onClick=${check}>${busy === "check" ? t("Checking…") : t("Check for updates")}</button>
          ${stale.length > 0 && html`<button disabled=${!isAdmin || !!busy} onClick=${updateAll}>${busy === "all" ? t("Updating…") : tf("Update all ({n})", { n: stale.length })}</button>`}
        </div>
    </div>
    ${sources.map(src => html`<${GeoSource} key=${src.kind} src=${src} presets=${(data.presets || {})[src.kind] || []}
      isAdmin=${isAdmin} notify=${notify} onSaved=${setData} />`)}
    <section class="section geo-files">
      <h2 class=geo-h2>${t("Categories the rules use")}</h2>
      ${sets.length === 0
        ? html`<div class=blank>${t("Nothing yet. A category appears here when a rule first names it.")}</div>`
        : html`<div class=tblwrap ref=${tbl}><table class="data-table geo-tbl">
            <thead><tr>
              <th>${t("Category")}</th><th class=num>${t("Rules")}</th><th class=num>${t("Size")}</th>
              <th>${t("Fetched")}</th><th>${t("State")}</th>
              <th class=mid title=${t("Update at every check")}>${t("Auto")}</th>
              <th class=acts>${t("Actions")}</th>
            </tr></thead>
            <tbody>${sets.map(s => html`<${GeoRow} key=${s.tag} set=${s} used=${usage[s.tag] || 0}
              isAdmin=${isAdmin} busy=${busy} act=${act} />`)}</tbody>
          </table></div>`}
    </section>`;
}

// geoState turns one rule-set into the single word its row should carry, plus
// the colour that word is worth. "Not looked at yet" is deliberately its own
// state: it must not read as "current".
function geoState(s) {
  // No copy at all is the one state worth shouting about: a rule names a
  // category the panel cannot hand to a node, and until it is edited the nodes
  // it applies to receive no configuration updates whatsoever.
  if (!s.content_id) return { cls: "err", label: t("no copy"), title: t("A rule names this category and there is nothing to give the nodes. They stop receiving configuration updates until the rule is fixed.") };
  if (s.upstream_gone) return { cls: "warn", label: t("frozen — withdrawn upstream"), title: t("The source stopped publishing it. The copy here keeps working and will not be updated again.") };
  if (!s.checked_at || !s.upstream_id) return { cls: "muted", label: "—", title: t("Not checked against the source yet") };
  if (s.upstream_id !== s.content_id) {
    const d = (Number(s.upstream_size) || 0) - (Number(s.size) || 0);
    const delta = d === 0 ? "" : " · " + (d > 0 ? "+" : "−") + humanBytes(Math.abs(d));
    return { cls: "ok-new", label: t("update available") + delta, title: t("The source holds different content") };
  }
  return { cls: "muted", label: t("up to date"), title: t("Byte for byte what the source holds") };
}

function GeoRow({ set, used, isAdmin, busy, act }) {
  const st = geoState(set);
  const tag = set.tag, name = geoName(tag);
  const stale = !set.upstream_gone && set.checked_at && set.upstream_id && set.upstream_id !== set.content_id;
  const update = () => act(tag, "POST", "/api/rulesets/" + encodeURIComponent(tag) + "/update", null,
    tf("{name} updated", { name }));
  const rollback = async () => {
    const ok = await uiConfirm({
      title: tf("Put back the previous {name}?", { name }),
      lines: [
        t("The previous copy replaces the current one and reaches the nodes."),
        t("The source keeps the newer version; the next check offers it again."),
      ],
      confirmLabel: t("Put it back"),
      danger: true,
    });
    if (ok) act(tag, "POST", "/api/rulesets/" + encodeURIComponent(tag) + "/rollback", null, tf("{name} put back", { name }));
  };
  const toggleAuto = () => act(tag, "POST", "/api/rulesets/" + encodeURIComponent(tag) + "/auto-update", { on: !set.auto_update });
  const working = busy === tag;
  return html`<tr>
    <td><div class=cell-main>${name}</div><div class=cell-sub>${set.kind}</div></td>
    <td class=num>${used > 0 ? used : html`<span class=muted>—</span>`}</td>
    <td class=num>${humanBytes(set.size)}</td>
    <td>${set.fetched_at ? fmtAge(set.fetched_at) + " " + t("ago") : "—"}</td>
    <td><span class=${"nstate " + st.cls} title=${st.title}>${st.label}</span></td>
    <td class=mid><${Switch} on=${set.auto_update} disabled=${!isAdmin || !!busy}
      label=${"geo-auto-" + tag} title=${t("Update at every check")} onChange=${toggleAuto} /></td>
    <td class=acts><div class=rowacts>
      ${stale && html`<button class="sm" disabled=${!isAdmin || !!busy} onClick=${update}>${working ? t("Updating…") : t("Update")}</button>`}
      ${set.prev_content_id && html`<button class="ghost sm" disabled=${!isAdmin || !!busy}
        title=${tf("Previous copy, {size}", { size: humanBytes(set.prev_size) })}
        onClick=${rollback}>${t("Put back")}</button>`}
    </div></td>
  </tr>`;
}

// GeoSource is one database's address: a known repository or one typed in, plus
// what its last listing contained.
function GeoSource({ src, presets, isAdmin, notify, onSaved }) {
  const [preset, setPreset] = useState(src.preset);
  const [url, setUrl] = useState(src.url);
  const [busy, setBusy] = useState(false);
  useEffect(() => { setPreset(src.preset); setUrl(src.url); }, [src.preset, src.url]);

  const custom = preset === "custom";
  const effective = custom ? url : ((presets.find(p => p.id === preset) || {}).url || url);
  const dirty = preset !== src.preset || (custom && url !== src.url);
  const title = src.kind === "geoip" ? t("Countries (geoip)") : t("Domain categories (geosite)");
  const catalog = src.catalog || [];
  const prev = src.catalog_prev || [];
  const added = prev.length ? catalog.filter(x => !prev.includes(x)).length : 0;
  const dropped = prev.length ? prev.filter(x => !catalog.includes(x)).length : 0;

  const save = async () => {
    setBusy(true);
    try {
      const d = await api("POST", "/api/rulesets/source", { kind: src.kind, preset, url: custom ? url : "" });
      noteGeoCatalog(d); onSaved(d);
      notify(t("Source saved"), "ok");
    } catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };

  return html`<section class="section geo-source">
    <h2 class=geo-h2>${title}</h2>
    <div class=geo-src>
      <label class=geo-field><span class=caps>${t("Source")}</span>
        <select value=${preset} disabled=${!isAdmin} aria-label=${"geo-source-" + src.kind} onChange=${e => setPreset(e.target.value)}>
          ${presets.map(p => html`<option key=${p.id} value=${p.id}>${geoPresetLabel(p.id)}</option>`)}
          <option value="custom">${t("another address")}</option>
        </select>
      </label>
      <label class=geo-field><span class=caps>${t("Address")}</span>
        <input class=mono value=${custom ? url : effective} readonly=${!custom || !isAdmin}
          aria-label=${"geo-url-" + src.kind} placeholder="https://example.com/rule-set/" onInput=${e => setUrl(e.target.value)} />
      </label>
    </div>
    <div class=geo-note>${geoPresetNote(preset)}</div>
    ${dirty && html`<div class=geo-acts>
      <button disabled=${busy} onClick=${save}>${busy ? t("Saving…") : t("Save source")}</button>
      <button class=ghost onClick=${() => { setPreset(src.preset); setUrl(src.url); }}>${t("Cancel")}</button>
      <span class=geo-note>${t("Files already downloaded stay as they are.")}</span>
    </div>`}
    ${src.check_error
      ? html`<div class="note warn">${t("Last check failed")}: ${src.check_error}</div>`
      : catalog.length > 0
      ? html`<div class=geo-note>${tf("{n} categories offered", { n: catalog.length })}${src.catalog_at ? " · " + fmtAge(src.catalog_at) + " " + t("ago") : ""}${added || dropped ? " · " + tf("{a} new, {d} withdrawn", { a: added, d: dropped }) : ""}</div>`
      : html`<div class=geo-note>${t("No category list yet — the first check runs shortly.")}</div>`}
  </section>`;
}

// A preset's name and what taking it costs or buys. Kept in the interface rather
// than shipped from the server, because it is prose and prose gets translated.
function geoPresetLabel(id) {
  return { sagernet: "SagerNet", runetfreedom: "runetfreedom", custom: t("another address") }[id] || id;
}
function geoPresetNote(id) {
  if (id === "sagernet") return t("The sing-box project's own lists.");
  if (id === "runetfreedom") return t("The same categories plus the blocked-domain registry. Rebuilt every 6 hours.");
  return t("An address holding .srs files, one per category. Other formats will not load.");
}

/* ---------------- clients tab (users + groups) ---------------- */
// Users is the default subtab (the day-to-day surface); Groups is the policy
// scaffolding behind them. Traffic reads as a property of a client, so its
// figures sit in the Users table rather than in a tab of their own.
function Clients({ state, traffic, reload, notify, setModal }) {
  const sc = schemas(state, null);
  const [sub, setSub] = useSubtab("clients", "users", ["users", "groups"]);
  // Visibility is enforced server-side: the state endpoint returns only this
  // account's namespace (the bootstrap owner sees every namespace only while the
  // buried cross-namespace lens is on). So the table simply renders what it got,
  // no client-side namespace filter, no cross-tenant rows to leak.
  const win = (traffic && traffic.window_seconds) || 360;
  // /api/traffic is a separate call, so join it onto the rows by id and carry the
  // sampling window along, as the "Now" column needs it to turn bytes into a rate.
  const byUser = new Map(((traffic && traffic.users) || []).map(r => [r.user_id, { ...r, window_seconds: win }]));
  const users = (state.users || []).map(u => ({ ...u, _tr: byUser.get(u.id) || null }));
  const groups = state.groups || [];
  const total = ((traffic && traffic.users) || []).reduce((sum, r) => sum + (Number(r.total_bytes) || 0), 0);
  const head = { title: t("Clients"), summary: sub === "groups"
    ? plural(groups.length, "group")
    : tf("{clients} in {groups}", { clients: counted(users.length, "client"), groups: counted(groups.length, "group") }) };
  const nav = html`<${SubTabs} tabs=${[["users", t("Clients")], ["groups", t("Groups")]]} active=${sub} onSelect=${setSub} />`;
  return html`
    ${sub === "users"
      ? html`<${EntityPanel} head=${head} subnav=${nav} cfg=${sc.users} rows=${users} state=${state} reload=${reload} notify=${notify} setModal=${setModal} />
             ${traffic && html`<p class=muted title=${t("cumulative since each node started, polled from the entry nodes.") + " " + tf("“now” = last {m} min", { m: Math.round(win / 60) })}>${tf("{n} active now", { n: traffic.active_count || 0 })} · ${humanBytes(total)}</p>`}`
      : html`<${EntityPanel} head=${head} subnav=${nav} cfg=${sc.groups} rows=${groups} state=${state} reload=${reload} notify=${notify} setModal=${setModal} />`}`;
}

/* ---------------- logs tab (problems board + event feed) ---------------- */
// The fleet owner gets the full board (infra problems + the infra event feed);
// an operator gets only its own namespace's journal (the event feed); the
// problems board and the overview/settings it needs are infra-only.
// Watchdog alerts lead with an emoji because they are written for Telegram.
// The event log already shows severity as a dot, so the glyph would only
// duplicate it, in a font the interface does not bundle.
const ALERT_GLYPHS = ["\u2757\ufe0f", "\u26a0\ufe0f", "\u2705", "\u2757", "\u26a0"];
function eventText(msg) {
  let m = String(msg || "");
  for (const g of ALERT_GLYPHS) if (m.startsWith(g)) { m = m.slice(g.length); break; }
  return m.trim();
}
// Russian counts take three noun forms, English two. Kept out of the dictionary
// because a plural is a table, not a string.
const PLURALS = {
  server: { en: ["server", "servers"], ru: ["сервер", "сервера", "серверов"] },
  client: { en: ["client", "clients"], ru: ["клиент", "клиента", "клиентов"] },
  group: { en: ["group", "groups"], ru: ["группа", "группы", "групп"] },
  rule: { en: ["rule", "rules"], ru: ["правило", "правила", "правил"] },
  entry: { en: ["entry", "entries"], ru: ["запись", "записи", "записей"] },
  domain: { en: ["domain", "domains"], ru: ["домен", "домена", "доменов"] },
  base: { en: ["list", "lists"], ru: ["список", "списка", "списков"] },
  day: { en: ["day", "days"], ru: ["день", "дня", "дней"] },
  month: { en: ["month", "months"], ru: ["месяц", "месяца", "месяцев"] },
};
// counted is a number for a sentence that has to be right for every count.
//
// English carries the noun next to the numeral and the sentence around it can be
// written once. Russian cannot: the noun beside a numeral changes form with the
// number and the verb changes with it in turn, so "{n} выходят" reads wrong for
// one group however "{n}" is spelled. The Russian sentences that use this name
// the thing they are counting themselves and end with the number ("групп: 1",
// "групп: 5") which is right for every count there is, and is the same split
// the journal makes server-side (journal.Count).
function counted(n, key) { return LANG === "ru" ? String(n) : plural(n, key); }

function plural(n, key) {
  const f = PLURALS[key];
  if (!f) return String(n);
  if (LANG !== "ru") return n + " " + f.en[n === 1 ? 0 : 1];
  const m10 = n % 10, m100 = n % 100;
  const i = (m10 === 1 && m100 !== 11) ? 0 : (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) ? 1 : 2;
  return n + " " + f.ru[i];
}

function sevDot(sev) { return sev === "error" || sev === "critical" ? "err" : sev === "warn" || sev === "warning" ? "warn" : "muted"; }
// A short token keeps the level column narrow; the word set out at full length
// painted a vertical band of amber down the whole feed.
// Kept out of the dictionary on purpose: the same key would have to render long
// in the tooltip and short in the column.
const SEV_SHORT = {
  info: { en: "ok", ru: "ок" },
  warn: { en: "warn", ru: "предупр" },
  error: { en: "err", ru: "ошибка" },
  critical: { en: "crit", ru: "сбой" },
};
function sevLabel(sev) { const m = SEV_SHORT[sev]; return m ? (LANG === "ru" ? m.ru : m.en) : sev; }
// Absolute time, which is what the lede promises. Same-day entries need only the
// clock; older ones carry the date.
function logTime(at) {
  try {
    const d = new Date(at);
    return d.toDateString() === new Date().toDateString() ? clock(d, true) : dayMonth(d) + " " + clock(d);
  } catch { return String(at || ""); }
}

// How many events one request brings back, and how many the screen will hold
// before it stops asking for more; a journal is a record, not a place to sit and
// scroll for an hour.
const LOG_PAGE = 100, LOG_MAX = 2000;
// One line of the journal. A line that is part of an operation (draining a
// shared exit is one action that moves a group in every scope that used it)
// can be opened to show the rest of that operation, which until it had a name
// was a scatter of rows an operator could only match up by their timestamps.
//
// Opening it crosses nothing: the server answers with the lines of this
// reader's own scope and says how many it is not showing. The number is no news
// (the summary line above already states it in words) but seeing it beside
// the parts is the difference between "three groups moved" and "three groups
// moved, two of them somewhere I am not shown".
function LogLine({ ev, notify }) {
  const [open, setOpen] = useState(false);
  const [lines, setLines] = useState(null);
  const [hidden, setHidden] = useState(0);
  const toggle = async () => {
    if (open) { setOpen(false); return; }
    setOpen(true);
    if (lines) return;
    try {
      const r = await api("GET", "/api/events/operation?lang=" + LANG + "&id=" + encodeURIComponent(ev.op_id));
      setLines((r.events || []).filter(x => x.id !== ev.id));
      setHidden(r.hidden || 0);
    } catch (e) { notify(e.message, "err"); setOpen(false); }
  };
  return html`<div class=logwrap>
    <div class=logline>
      <span class=lg-t title=${fmtTime(ev.at) + " · " + fmtAge(ev.at)}>${logTime(ev.at)}</span>
      <span class=${"lv " + sevDot(ev.severity)} title=${t(ev.severity)}>${sevLabel(ev.severity)}</span>
      <span class=lg-s title=${ev.kind + (ev.actor ? " · " + ev.actor : "")}>${eventKind(ev.kind)}${ev.actor ? " · " + ev.actor : ""}</span>
      <span>${eventText(ev.message)}${ev._runs > 1 ? html` <span class=runs title=${tf("first at {time}", { time: fmtTime(ev._since) })}>×${ev._runs}</span>` : null}
        ${ev.op_id && ev.op_size > 1 ? html` <button class=opmore aria-expanded=${open ? "true" : "false"} onClick=${toggle}>${open ? t("hide the operation") : tf("the whole operation ({n})", { n: ev.op_size })}</button>` : null}</span>
    </div>
    ${open && html`<div class=logop>
      ${lines == null ? html`<p class=meta>${t("Loading…")}</p>`
        : lines.length === 0 && hidden === 0 ? html`<p class=meta>${t("This operation wrote nothing else.")}</p>`
        : lines.map(x => html`<div key=${x.id} class=logop-row>
            <span class=${"dot " + sevDot(x.severity)}></span>
            <span>${eventText(x.message)}</span>
            <span class=meta>${logTime(x.at)}</span>
          </div>`)}
      ${hidden > 0 && html`<p class=meta>${tf("{n} in other scopes, not shown here.", { n: counted(hidden, "entry") })}</p>`}
    </div>`}
  </div>`;
}

function Logs({ notify, initialFilter = "" }) {
  const [events, setEvents] = useState(null);
  const [kind, setKind] = useState("");
  // Filtering by kind happens server-side, so the tail is per-kind rather than
  // whatever survived a global cut. The option list is kept from an unfiltered
  // load, otherwise picking a kind would collapse the list to that one kind.
  const [kinds, setKinds] = useState([]);
  const [q, setQ] = useState(initialFilter); // seeded when a node hands over its journal
  const [sev, setSev] = useState("");
  const [end, setEnd] = useState(false);   // the server has no older events left
  const [more, setMore] = useState(false); // a page is on its way
  const tail = useRef(null);               // sentinel below the last row
  // What needs attention lives on the Overview; this screen is the record.
  // The cursor is the position of the last row shown: its timestamp and its id
  // together, which is what the journal is ordered by.
  // The journal is written where the action happens, in both languages the panel
  // speaks; the tab says which of the two it is being read in.
  const path = useCallback((last) => "/api/events?lang=" + LANG + "&limit=" + LOG_PAGE
    + (kind ? "&kind=" + encodeURIComponent(kind) : "")
    + (last ? "&before=" + encodeURIComponent(last.id) + "&before_at=" + encodeURIComponent(last.at) : ""), [kind]);
  const load = useCallback(async () => {
    try {
      const e = await api("GET", path(null));
      const list = e.events || [];
      setEvents(list); setEnd(list.length < LOG_PAGE);
      if (!kind) setKinds([...new Set(list.map(x => x.kind).filter(Boolean))].sort());
    } catch (e) { notify(e.message, "err"); }
  }, [path, kind, notify]);
  useEffect(() => { load(); }, [load]);

  // The next page is fetched when the bottom of the list comes into view. A
  // narrow filter can leave that bottom on screen from the start, so this keeps
  // pulling until the screen fills or the journal runs out.
  const loadMore = useCallback(async () => {
    if (more || end || !events || events.length === 0 || events.length >= LOG_MAX) return;
    setMore(true);
    try {
      const e = await api("GET", path(events[events.length - 1]));
      const list = e.events || [];
      if (list.length < LOG_PAGE) setEnd(true);
      // Belt and braces: a page that somehow overlaps the previous one must not
      // duplicate rows (React would also object to the repeated keys).
      const have = new Set(events.map(x => x.id));
      const fresh = list.filter(x => !have.has(x.id));
      if (fresh.length > 0) setEvents(prev => [...prev, ...fresh]);
      else setEnd(true);
    } catch (e) { notify(e.message, "err"); setEnd(true); }
    setMore(false);
  }, [path, events, more, end, notify]);
  useEffect(() => {
    const el = tail.current;
    if (!el || typeof IntersectionObserver !== "function") return;
    const io = new IntersectionObserver(es => { if (es.some(x => x.isIntersecting)) loadMore(); }, { rootMargin: "300px" });
    io.observe(el);
    return () => io.disconnect();
  }, [loadMore]);

  const kindLabel = (k) => k === "" ? t("All kinds") : eventKind(k);
  // warn covers everything above info; err is the subset that already broke.
  const sevRank = { info: 0, warn: 1, error: 2, critical: 2 };
  const needle = q.trim().toLowerCase();
  const matched = (events || []).filter(ev => {
    if (sev === "warn" && ev.severity !== "warn") return false;
    if (sev === "err" && sevRank[ev.severity] !== 2) return false;
    if (!needle) return true;
    return [ev.message, ev.kind, ev.actor].some(v => (v || "").toLowerCase().includes(needle));
  });
  // The watchdog re-states an alert every cycle the condition holds, and two
  // conditions interleave into a wall. Identical messages collapse onto their
  // newest line, which carries the repeat count and, on hover, the first time.
  // One operation is one thing: its lines fold under the newest of them, which
  // is the summary the panel wrote last. They are still there and the row opens,
  // but the journal stops reading as a scatter of rows that happen to share a
  // second.
  const shown = [];
  const byMsg = new Map();
  const byOp = new Set();
  for (const ev of matched) {
    if (ev.op_id && ev.op_size > 1) {
      if (byOp.has(ev.op_id)) continue;
      byOp.add(ev.op_id);
      // Two runs of the same action read alike but are not the same operation,
      // and each opens onto its own lines, so they are not collapsed together
      // the way a repeated alert is.
      shown.push({ ...ev });
      continue;
    }
    const key = ev.severity + "\u0000" + ev.kind + "\u0000" + ev.message;
    const first = byMsg.get(key);
    if (first) { first._runs = (first._runs || 1) + 1; first._since = ev.at; continue; }
    const row = { ...ev };
    byMsg.set(key, row);
    shown.push(row);
  }

  return html`
    <${PageHead} title=${t("Logs")}
      actions=${html`<button class=ghost onClick=${load}>${t("Refresh")}</button>`} />

    <div class=section>
      <div class=filters>
        <input class=filter type=search placeholder=${t("Filter by text or kind")} aria-label=${t("Filter the log")} value=${q} onInput=${e => setQ(e.target.value)} />
        <select aria-label=${t("Kind")} value=${kind} onChange=${e => setKind(e.target.value)}>
          ${["", ...kinds].map(k => html`<option key=${k} value=${k}>${kindLabel(k)}</option>`)}
        </select>
        <select aria-label=${t("Level")} value=${sev} onChange=${e => setSev(e.target.value)}>
          <option value="">${t("All levels")}</option>
          <option value="warn">${t("Warnings")}</option>
          <option value="err">${t("Errors")}</option>
        </select>
        ${(needle || sev || kind) && html`<button class="ghost sm" onClick=${() => { setQ(""); setSev(""); setKind(""); }}>${t("Reset")}</button>`}
        <span class=shown>${tf("{entries} shown", { entries: plural(shown.length, "entry") })}</span>
      </div>
      <div class=logpanel>
        ${events == null && html`<${Skeleton} rows=${6} cols=${3} />`}
        ${events != null && events.length === 0 && html`<p class=empty>${t("No events yet")}</p>`}
        ${events != null && events.length > 0 && shown.length === 0 && html`<p class=empty>${t("No matches")}</p>`}
        ${shown.map(ev => html`<${LogLine} key=${ev.id} ev=${ev} notify=${notify} />`)}
        <div ref=${tail} class=logtail>
          ${more ? t("Loading…")
            : events != null && events.length >= LOG_MAX ? t("Showing the most recent entries — narrow the filter to see further back")
            : end && shown.length > 0 ? t("End of the journal") : ""}
        </div>
      </div>
    </div>`;
}
function fmtTime(s) {
  try { const d = new Date(s); return dayMonth(d) + "/" + d.getFullYear() + " " + clock(d, true); } catch { return String(s || ""); }
}

/* ---------------- HA / standby ---------------- */
// The control plane (Postgres primary + CA + panel) runs on one exit. A standby
// is a second exit kept master-ready: a streaming Postgres replica that also
// holds the CA key, with the panel staged but stopped. Failover is manual
// (promote): the operator is the fence against split-brain.
//
// serve runs unprivileged (User=trustpanel, NoNewPrivileges, ProtectSystem=strict)
// so it cannot edit pg_hba.conf, run `sudo -u postgres`, or restart Postgres. The
// "Make standby" button therefore routes the privileged work through the per-node
// root AGENTS: the primary's agent does the primary-side Postgres work and
// forwards the secret bundle (incl. the CA key) straight to the standby's agent
// over mTLS, so the panel never handles the CA key. The trigger is guarded by the
// session + a CSRF token + an explicit "are you sure?" confirmation. The CLI
// (shown collapsed) stays as the break-glass path when the panel is down.
function HA({ state, overview, notify, reload, setModal }) {
  const [open, setOpen] = useState(null);   // node id whose break-glass box is expanded
  const [howto, setHowto] = useState(null); // standby id whose fail-over runbook is expanded
  const [showHelp, setShowHelp] = useState(false);
  const [job, setJob] = useState(null);     // running add-standby job snapshot
  const [busy, setBusy] = useState(null);   // node id currently being provisioned
  const nodes = state.nodes || [];
  const cp = state.control_plane || {};
  const ipOf = n => (n && n.public_ips && n.public_ips[0]) || "";
  const copy = v => navigator.clipboard.writeText(v).then(() => notify(t("Copied"), "ok"), () => notify(t("Could not copy — select the text and copy it by hand"), "err"));
  const ovById = {};
  ((overview && overview.nodes) || []).forEach(n => { ovById[n.id] = n; });
  const replOf = id => (ovById[id] || {}).replication;

  const pollJob = (id, sb, verb) => {
    const tick = async () => {
      try {
        const s = await api("GET", "/api/jobs/" + id); setJob(s);
        if (s.status === "running") return setTimeout(tick, 1500);
        setBusy(null);
        if (s.status === "succeeded") { notify(tf(verb === "rebuild" ? "{name} replica rebuilt" : "{name} is now a standby", { name: sb.name }), "ok"); reload && reload(); }
        else notify(t(verb === "rebuild" ? "Rebuild failed" : "Add standby failed"), "err");
      } catch (e) { notify(e.message, "err"); setBusy(null); }
    };
    tick();
  };
  const makeStandby = async (sb) => {
    // The explicit human confirmation is required before the privileged command
    // is formed; the API also takes `confirm` (the node id) as a second gate.
    const ok = await uiConfirm({
      title: tf("Make “{name}” a control-plane standby?", { name: sb.name }),
      lines: [
        t("Sets up replication on this exit and copies the CA private key onto it."),
        t("The primary's Postgres restarts briefly; this exit's client traffic isn't affected."),
      ],
      confirmLabel: t("Make standby"),
    });
    if (!ok) return;
    setJob(null); setBusy(sb.id);
    try {
      const r = await api("POST", "/api/cluster/add-standby", { node_id: sb.id, confirm: sb.id });
      pollJob(r.job_id, sb, "add");
    } catch (e) { notify(e.message, "err"); setBusy(null); }
  };
  // Rebuild re-runs the same (idempotent) add-standby against a node that is
  // already a standby: it wipes the replica's PGDATA and re-seeds it from the
  // CURRENT primary via pg_basebackup. Use when its Postgres died or diverged
  // (there is no in-place repair / pg_rewind). The data plane is untouched.
  const rebuildStandby = async (sb) => {
    const ok = await uiConfirm({
      title: tf("Rebuild the replica on “{name}”?", { name: sb.name }),
      lines: [
        t("Wipes this replica and re-seeds it from the current primary — use if its Postgres died or diverged."),
        t("Data is re-copied fresh from the primary; this exit's client traffic isn't affected."),
      ],
      confirmLabel: t("Rebuild replica"),
      danger: true,
    });
    if (!ok) return;
    setJob(null); setBusy(sb.id);
    try {
      const r = await api("POST", "/api/cluster/add-standby", { node_id: sb.id, confirm: sb.id });
      pollJob(r.job_id, sb, "rebuild");
    } catch (e) { notify(e.message, "err"); setBusy(null); }
  };

  // Resolve the primary: an explicit pg_role=primary, else the active control
  // plane, else the sole mgmt-capable node.
  let primary = nodes.find(n => n.pg_role === "primary");
  if (!primary && cp.active_node_id) primary = nodes.find(n => n.id === cp.active_node_id);
  if (!primary) { const mc = nodes.filter(n => n.mgmt_capable); if (mc.length === 1) primary = mc[0]; }

  const standbyIds = new Set([...(cp.standby_node_ids || []), ...nodes.filter(n => n.pg_role === "replica").map(n => n.id)]);
  const standbys = nodes.filter(n => standbyIds.has(n.id));
  const eligible = nodes.filter(n => n.public_role === "exit" && (!primary || n.id !== primary.id) && !standbyIds.has(n.id));

  const addCmd = sb => [
    "sudo trustpanel cluster add-standby \\",
    `  --node-id ${sb.id} \\`,
    `  --primary-id ${primary ? primary.id : "<PRIMARY_ID>"} \\`,
    `  --primary-ip ${primary ? ipOf(primary) : "<PRIMARY_IP>"} \\`,
    `  --standby-ip ${ipOf(sb) || "<STANDBY_IP>"} \\`,
    `  --standby-ssh-host ${ipOf(sb) || "<STANDBY_HOST>"} \\`,
    "  --standby-ssh-user user --standby-ssh-port 3222 \\",
    "  --known-hosts /tmp/fleet_known_hosts \\",
    "  --standby-ssh-key /path/to/fleet_key",
  ].join("\n");

  // Self-resolving promote (the binary fills in this node's id from its local
  // replica), so the runbook is one command with no id to paste. Mirrors #12.
  const promoteCmd = "sudo trustpanel promote --pg-promote --start-serve";

  // The two machines and whether the second one is ready is what this screen is
  // about. Postgres and the CA are how it is done, so they belong in the
  // confirmation that does it, not in the opening sentence.
  return html`
    <div class=card>
      <div class=between>
        <h2 style=${{ margin: 0 }}>${t("Panel standby · switched by hand")}</h2>
        <button class="ghost sm help-button" aria-expanded=${showHelp} onClick=${() => setShowHelp(h => !h)}>${t("How this works")}</button>
      </div>
      <p class=sec-note style=${{ margin: ".35rem 0 1rem", maxWidth: "62ch" }}>
        ${t("Switching is done by hand. The backup bot sends word when the primary stops answering.")}</p>
      ${showHelp && html`<div class=note style=${{ marginBottom: "1rem" }}>${t("Failover is manual. The backup bot sends the command; the same steps are in RECOVERY.md inside every backup.")}</div>`}
      <div class=ha-pair>
        <div>
          <label>${t("Primary")}</label>
          ${primary
            ? html`<div><b>${primary.name}</b> <span class=muted>${ipOf(primary)}</span></div>`
            : html`<div class=muted>${t("no primary set — mark one, or this deployment doesn't use a standby")}</div>`}
        </div>
        <div>
          <label>${t("Standby")}</label>
          ${standbys.length === 0
            ? html`<div class=ha-status><span class=ha-dot></span><span>${t("No standby — failover not possible. Add one below.")}</span></div>`
            : html`<div><b>${standbys.map(sb => sb.name).join(", ")}</b> <span class=muted>${standbys.length === 1 ? ipOf(standbys[0]) : ""}</span></div>`}
        </div>
      </div>
      ${primary && standbys.length > 0 && html`
          ${standbys.map(sb => {
            const rep = replOf(sb.id);
            const warn = replWarning(rep);
            const checking = rep === undefined;
            const ready = !!rep && !warn;
            const dot = ready ? "ha-dot ok" : warn ? "ha-dot warn" : "ha-dot";
            const head = checking ? t("Standby — replication starting") : warn ? t("Standby degraded") : t("Ready to fail over");
            const detail = checking ? t("replication: checking…")
              : warn ? t("warning") + ": " + warn
              : t("streaming") + (rep.bytes_behind != null ? " · " + t("behind") + " " + humanBytes(rep.bytes_behind) : "");
            return html`<div class=ha-row>
              <div class=ha-status><span class=${dot}></span><span><b>${sb.name}</b> — ${head}</span></div>
              <div class=muted style=${{ margin: ".25rem 0 .1rem" }}>${detail}</div>
              <div style=${{ display: "flex", gap: ".5rem", alignItems: "center", marginTop: ".35rem" }}>
                <button class="ghost sm" onClick=${() => setHowto(howto === sb.id ? null : sb.id)}>${t("How to fail over")} ${howto === sb.id ? "▴" : "▾"}</button>
                <button class="ghost sm" disabled=${!!busy} title=${t("Wipe this replica's PGDATA and re-seed it from the current primary (use if its Postgres died or diverged)")} onClick=${() => rebuildStandby(sb)}>${busy === sb.id ? t("Working…") : t("Rebuild replica")}</button>
              </div>
              ${howto === sb.id && html`<div style=${{ marginTop: ".5rem" }}>
                <div class=muted style=${{ marginBottom: ".3rem", fontSize: "13px" }}>${t("If the primary fails, fail over to this standby — only once the primary is confirmed gone:")}</div>
                <ol class=ha-steps>
                  <li>${tf("SSH into {name} ({ip}).", { name: sb.name, ip: ipOf(sb) })}</li>
                  <li>${t("Run as root:")}<div class=cmd>${promoteCmd}</div><button class="ghost sm" onClick=${() => copy(promoteCmd)}>${t("Copy command")}</button></li>
                  <li>${t("The panel comes back up on this node; point your tunnel at it.")}</li>
                </ol>
              </div>`}
            </div>`;
          })}`}
    </div>

    <div class=card>
      <h2 style=${{ margin: "0 0 .3rem" }}>${t("Add a standby")}</h2>
      <div class=muted style=${{ marginBottom: ".8rem", fontSize: "13px" }}>${t("A Postgres replica + CA on an exit. That exit's traffic is not interrupted.")}</div>
      ${eligible.length === 0
        ? html`<div class=blank style=${{ padding: "1.2rem 0" }}>
            <p>${t("A standby is a second exit node. There is none yet.")}</p>
            <button onClick=${() => setModal({ kind: "provision" })}>+ ${t("Add server")}</button>
          </div>`
        : eligible.map(sb => html`<div class=ha-row>
            <div style=${{ display: "flex", alignItems: "center", gap: ".6rem" }}>
              <div><b>${sb.name}</b> <span class=muted>(${ipOf(sb)})</span></div>
              <div style=${{ flex: 1 }}></div>
              <button class="sm" disabled=${!!busy} onClick=${() => makeStandby(sb)}>${busy === sb.id ? t("Working…") : t("Make standby")}</button>
              <button class="ghost sm" onClick=${() => setOpen(open === sb.id ? null : sb.id)}>${open === sb.id ? t("Hide CLI") : t("Manual (CLI)")}</button>
            </div>
            ${open === sb.id && html`<div style=${{ marginTop: ".5rem" }}>
              <div class=muted style=${{ marginBottom: ".3rem", fontSize: "13px" }}>${t("The same thing from a console:")}</div>
              <div class=cmd>${addCmd(sb)}</div>
              <button class="ghost sm" onClick=${() => copy(addCmd(sb))}>${t("Copy command")}</button>
            </div>`}
          </div>`)}
      ${job && html`<div class=ha-row style=${{ marginTop: ".8rem" }}>
        <div class=field><label>${t("Add-standby status")}</label><span class=${job.status === "succeeded" ? "ok" : job.status === "failed" ? "err" : "muted"}>${t(job.status)}</span></div>
        <pre class=cmd>${(job.log || []).join("\n")}${job.error ? "\nERROR: " + job.error : ""}</pre>
        ${job.status !== "running" && html`<button class="ghost sm" onClick=${() => setJob(null)}>${t("Dismiss")}</button>`}
      </div>`}
    </div>`;
}

/* ---------------- bots / alerts ---------------- */
function StatusChip({ s }) {
  const map = {
    ok: ["var(--ok)", "reachable"],
    unauthorized: ["var(--err)", "token rejected"],
    unreachable: ["var(--warn)", "unreachable"],
    unconfigured: ["var(--text-meta)", "not configured"],
  };
  const [color, label] = map[s && s.status] || map.unconfigured;
  return html`<span class=status-chip style=${{ color }}>
    <span style=${{ width: "9px", height: "9px", borderRadius: "50%", background: color, display: "inline-block" }}></span>
    ${t(label)}${s && s.detail ? " — " + s.detail : ""}
  </span>`;
}

// TelegramBinding: bind the logged-in account's own Telegram id so the bot answers
// them. The alert destination is the single infra "Alert chat ID", not repeated
// here, so this stays one field. It is part of the settings form, not a form of
// its own: the tab has one Save, and this saves with it.
function TelegramBinding({ value, onChange }) {
  return html`<${LF} label=${t("Your Telegram id")} hint=${t("leave blank to keep / 0 to unbind")}>
    <input value=${value} onInput=${e => onChange(e.target.value)} placeholder="123456789" /><//>`;
}

// AccountSettings: self-service for the logged-in account, just the password.
// Bot/Telegram binding lives with the rest of the bot config (Settings → Bots);
// language is the topbar toggle. Kept single-purpose so nothing is duplicated.
function AccountSettings({ notify }) {
  const [cur, setCur] = useState(""); const [nw, setNw] = useState(""); const [nw2, setNw2] = useState("");
  const [busy, setBusy] = useState(false);

  const changePw = async () => {
    if (nw.length < 8) { notify(t("new password must be at least 8 characters"), "err"); return; }
    if (nw !== nw2) { notify(t("passwords do not match"), "err"); return; }
    setBusy(true);
    try { await api("POST", "/api/account/password", { current: cur, new: nw }); setCur(""); setNw(""); setNw2(""); notify(t("Saved"), "ok"); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };
  return html`
    <${FormSec} title=${t("Change your password")} layout=password>
      <${LF} label=${t("Current password")}><${PasswordInput} value=${cur} onChange=${setCur} autocomplete="current-password" /><//>
      <${LF} label=${t("New password")}><${PasswordInput} value=${nw} onChange=${setNw} autocomplete="new-password" /><//>
      <${LF} label=${t("Confirm new password")}><${PasswordInput} value=${nw2} onChange=${setNw2} autocomplete="new-password" /><//>
      <div class=f-wide><button disabled=${busy} onClick=${changePw}>${t("Change password")}</button></div>
    <//>`;
}

// A role is what an account can reach, so changing one is asked in a window of
// its own: it says what the new role gets, collects the one field that role
// needs when the account does not have it yet, and is the confirmation itself
// rather than a strip inside the table followed by a second question.
function RoleChangeModal({ draft, onClose, onApply }) {
  const [value, setValue] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const ref = useModal(onClose);
  const toAdmin = draft.next === "admin";
  const submit = async () => {
    const v = value.trim();
    if (draft.needPw) {
      if (value.length < 8) return setErr(t("password must be at least 8 characters"));
    } else if (draft.needTg) {
      if (!v || !Number.isInteger(Number(v))) return setErr(t("Telegram id must be a number"));
    }
    setBusy(true);
    const extra = draft.needPw ? { password: value } : draft.needTg ? { telegram_id: Number(v) } : {};
    if (!await onApply(extra)) setBusy(false);
  };
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && ref.close()}>
      <div class=modal ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${tf("{name}: {role}", { name: draft.username, role: toAdmin ? t("Admin") : t("Operator") })}</h3>
          <button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button></header>
        <div class=body>
          <p class=hint style=${{ marginTop: 0 }}>${toAdmin ? t(ROLE_NOTE.admin) : t(ROLE_NOTE.operator)}</p>
          ${draft.needPw && html`<${LF} label=${t("New password")} required=${true} error=${err || null}>
            <${PasswordInput} value=${value} onChange=${v => { setValue(v); setErr(""); }} autocomplete="new-password" /><//>`}
          ${draft.needTg && html`<${LF} label=${t("Telegram id")} required=${true} error=${err || null}>
            <input value=${value} onInput=${e => { setValue(e.target.value); setErr(""); }} /><//>`}
        </div>
        <div class=foot>
          <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
          <button disabled=${busy} onClick=${submit}>${t("Change role")}</button>
        </div>
      </div>
    </div>`;
}

// What each role can reach, in one sentence. The form asks for the role first
// and everything else follows from it: a password is what an admin signs in
// with, a Telegram id is the operator's only way in, so this is the sentence
// that decides, and it belongs beside the choice.
const ROLE_NOTE = {
  operator: "Works through the Telegram bot: its own clients, nobody else's.",
  admin: "Signs in to the panel and manages the whole infrastructure.",
};

// Members: account/namespace membership management for the bootstrap owner,
// add/remove accounts and flip their role. Lives as a subtab under Settings
// (visible only when can_manage_members), kept out of one's personal account tab.
function Members({ notify, isAdmin = true, myNs = "" }) {
  const tbl = useScrollEdges();
  const [admins, setAdmins] = useState([]);
  const [newUser, setNewUser] = useState(""); const [newPw, setNewPw] = useState(""); const [newPw2, setNewPw2] = useState(""); const [newRole, setNewRole] = useState("operator"); const [newTg, setNewTg] = useState("");
  const [busy, setBusy] = useState(false);
  // roleDraft holds an in-progress role change that is missing a field the new
  // role requires (a password for a fresh admin, a Telegram id for a fresh
  // operator), collected inline before the confirm dialog fires. null when no
  // row is mid-change.
  const [roleDraft, setRoleDraft] = useState(null);
  const load = useCallback(async () => {
    try { const r = await api("GET", "/api/admins"); setAdmins(r.admins || []); }
    catch (e) { notify(e.message, "err"); }
  }, [notify]);
  useEffect(() => { load(); }, [load]);

  const addAdmin = async () => {
    if (!newUser.trim()) { notify(t("username is required"), "err"); return; }
    if (newRole === "admin") {
      if (newPw.length < 8) { notify(t("password must be at least 8 characters"), "err"); return; }
      if (newPw !== newPw2) { notify(t("passwords do not match"), "err"); return; }
    }
    const tgId = newTg.trim() === "" ? 0 : Number(newTg.trim());
    if (newTg.trim() !== "" && !Number.isInteger(tgId)) { notify(t("Telegram id must be a number"), "err"); return; }
    if (newRole === "operator" && !tgId) { notify(t("telegram id is required for the operator role"), "err"); return; }
    setBusy(true);
    try { await api("POST", "/api/admins", { username: newUser.trim(), password: newPw, role: newRole, telegram_id: tgId }); setNewUser(""); setNewPw(""); setNewPw2(""); setNewTg(""); notify(t("Saved"), "ok"); await load(); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };
  const delAdmin = async (u) => {
    if (!await uiConfirm({ title: tf("Delete {name}?", { name: u }), confirmLabel: t("Delete"), danger: true })) return;
    try { await api("DELETE", "/api/admins/" + encodeURIComponent(u)); notify(tf("{name} deleted", { name: u }), "info"); await load(); }
    catch (e) { notify(e.message, "err"); }
  };
  // The window states the new access and carries the field the new role needs,
  // so it is the confirmation; there is no second question behind it. Returns
  // true when the change went through and the window may close.
  const applyRoleChange = async (extra) => {
    const d = roleDraft;
    try {
      await api("POST", "/api/admins/" + encodeURIComponent(d.username) + "/role", { role: d.next, ...extra });
      notify(t("Saved"), "ok"); setRoleDraft(null); await load();
      return true;
    } catch (e) { notify(e.message, "err"); return false; }
  };
  // An admin-to-be may already carry a password from before and an operator-to-be
  // may already have Telegram bound; the window then only asks the question.
  const startRoleChange = (a) => {
    const next = a.role === "operator" ? "admin" : "operator";
    setRoleDraft({
      username: a.username, next,
      needPw: next === "admin" && !a.has_password,
      needTg: next === "operator" && !a.has_telegram,
    });
  };
  const nsLabel = (a) => a.namespace || "private";
  return html`
    <div class=section style=${{ marginTop: "1.5rem" }}>
      ${!isAdmin && html`<p class=muted style=${{ marginTop: "-.6rem", fontSize: "12px" }}>${tf("Members of your scope ({ns}) share its clients. Admins can manage members; operators only the clients.", { ns: myNs })}</p>`}
      <div class=tblwrap ref=${tbl}><table class="data-table members-table">
        <thead><tr>
          <th>${t("Login")}</th>
          ${isAdmin && html`<th>${t("Scope")}</th>`}
          <th>${t("Telegram")}</th>
          <th>${t("Created")}</th>
          <th class=acts>${t("Actions")}</th>
        </tr></thead>
        <tbody>
          ${admins.map(a => html`<tr key=${a.username}>
              <td>${a.username} <span class=${"pill " + (a.role === "operator" ? "direct" : "exit")}>${a.role === "operator" ? t("Operator") : t("Admin")}</span> ${a.is_current && html`<span class=muted>(${t("this account")})</span>`}</td>
              ${isAdmin && html`<td class=muted>${nsLabel(a)}</td>`}
              <td class=muted>${a.has_telegram ? "TG ✓" : ""}</td>
              <td class=muted>${a.created_at}</td>
              <td class=acts><div class=rowacts>
                ${!a.is_current && html`<button class="ghost sm" title=${t("Switch between admin and operator")} onClick=${() => startRoleChange(a)}>${a.role === "operator" ? t("Make admin") : t("Make operator")}</button>`}
                ${!a.is_current && html`<button class="danger sm" onClick=${() => delAdmin(a.username)}>${t("Delete")}</button>`}
              </div></td>
            </tr>`)}
        </tbody>
      </table></div>
    </div>
    ${roleDraft && html`<${RoleChangeModal} draft=${roleDraft} onClose=${() => setRoleDraft(null)} onApply=${applyRoleChange} />`}
    <${FormSec} title=${isAdmin ? t("Add account") : t("Add member")}
                note=${isAdmin ? t("Admins share the infrastructure. Each operator's scope is isolated from the rest.") : ""}>
      <${LF} label=${t("Role")} wide=${true} hint=${newRole === "admin" ? t(ROLE_NOTE.admin) : t(ROLE_NOTE.operator)}>
        <select value=${newRole} onChange=${e => { const r = e.target.value; setNewRole(r); if (r === "operator") { setNewPw(""); setNewPw2(""); } }}>
          <option value=operator>${t("Operator")}</option><option value=admin>${t("Admin")}</option>
        </select><//>
      <${LF} label=${t("Login")} required=${true} wide=${newRole === "admin"}><input value=${newUser} onInput=${e => setNewUser(e.target.value)} placeholder="i.petrov" /><//>
      ${newRole === "operator"
        ? html`<${LF} label=${t("Telegram id")} required=${true}><input value=${newTg} onInput=${e => setNewTg(e.target.value)} placeholder="123456789" /><//>`
        : html`<${React.Fragment}>
            <${LF} label=${t("New password")} required=${true}><${PasswordInput} value=${newPw} onChange=${setNewPw} autocomplete="new-password" /><//>
            <${LF} label=${t("Confirm password")} required=${true}><${PasswordInput} value=${newPw2} onChange=${setNewPw2} autocomplete="new-password" /><//>
            <${LF} label=${t("Telegram id")} wide=${true} hint=${t("for alerts; not needed to sign in")}><input value=${newTg} onInput=${e => setNewTg(e.target.value)} placeholder="123456789" /><//>
          <//>`}
      <div class=f-wide><button disabled=${busy} onClick=${addAdmin}>${isAdmin ? t("Add account") : t("Add member")}</button></div>
    <//>`;
}

// The blocks a Save can write, and what each is called when the message names
// the ones it wrote.
const SETTINGS_SECTIONS = ["bot", "alert", "backup", "fleet", "panel"];
const SETTINGS_SECTION_LABEL = {
  bot: "bot", alert: "alerts", backup: "backup",
  fleet: "server defaults", panel: "panel & schedules", tg: "your Telegram",
};

// FormSec: one settings block. The heading and its note sit beside the fields
// rather than above them, so a long form reads as a few blocks instead of one
// column that has to be scrolled past.
function FormSec({ title, note, status, action, layout, children }) {
  return html`<div class=${"form-sec" + (layout ? " form-sec-" + layout : "")}>
    <div class=sec-intro><div class=sec-head><h3>${title}</h3>${action}</div>
      ${status && html`<div class=sec-status>${status}</div>`}
      ${note && html`<p class=sec-note>${note}</p>`}</div>
    <div class=fields>${children}</div>
  </div>`;
}

// FleetAndPanelSettings: fleet-wide provisioning defaults + panel runtime tunables.
function FleetAndPanelSettings({ fleet, setFleet, panel, setPanel }) {
  const numField = (label, key, hint) => html`<${LF} label=${label} hint=${hint}>
    <input type=number min=0 value=${panel[key]} onInput=${e => setPanel({ ...panel, [key]: e.target.value })} /><//>`;
  const txtField = (label, key, placeholder) => html`<${LF} label=${label}>
    <input value=${fleet[key]} onInput=${e => setFleet({ ...fleet, [key]: e.target.value })} placeholder=${placeholder} /><//>`;
  return html`
    <${FormSec} title=${t("Server defaults")}>
      ${txtField(t("ACME contact email"), "acme_email", "admin@example.com")}
      ${txtField(t("Default Reality SNI"), "reality_sni", "www.example-cdn.com")}
      ${txtField(t("Apex domain"), "apex", "example.com")}
      ${txtField(t("Connect subdomain"), "connect_subdomain", "portal")}
      ${txtField(t("Brand"), "brand", "ExampleCDN")}
    <//>
    <${FormSec} title=${t("Panel & schedules")}>
      <fieldset class=settings-group><legend>${t("Sync and statistics")}</legend>
        ${numField(t("Reconcile interval (seconds)"), "reconcile_seconds")}
        ${numField(t("Stats poll interval (seconds)"), "stats_seconds")}
        ${numField(t("Online threshold (KB/min)"), "activity_kb_per_min")}
        ${numField(t("Auto egress-failover after exit down (seconds)"), "egress_failover_seconds", t("min 60 s"))}
      </fieldset>
      <fieldset class=settings-group><legend>${t("Expiry reminders")}</legend>
        ${numField(t("Billing check interval (hours)"), "billing_alert_hours")}
        ${numField(t("Warn N days before payment due"), "billing_alert_days")}
        ${numField(t("Warn N days before a client config expires"), "expiry_alert_days")}
      </fieldset>
      <fieldset class=settings-group><legend>${t("Panel access")}</legend>
        ${numField(t("Session lifetime (hours)"), "session_hours")}
      </fieldset>
    <//>`;
}

function SettingsPanel({ notify, isBootstrap = false, canManage = false, myNs = "", crossNs = false, toggleCrossNs = () => {} }) {
  const subKeys = ["server", "bots", "backup", "account"].concat(canManage ? ["members"] : []).concat(isBootstrap ? ["advanced"] : []);
  const [sub, setSub] = useSubtab("settings", "server", subKeys);
  const [cfg, setCfg] = useState(null);
  const [bot, setBot] = useState({ enabled: false, token: "" });
  const [alert, setAlert] = useState({ enabled: false, token: "", chat_id: "" });
  const [backup, setBackup] = useState({ enabled: false, chat_id: "", age_recipient: "", chunk_bytes: 0, local_enabled: true, interval_hours: 24, keep: 14, verify_enabled: true, verify_interval_days: 7, sender: "", sender_resolved: "" });
  const [fleet, setFleet] = useState({ acme_email: "", reality_sni: "", brand: "", apex: "", connect_subdomain: "" });
  const [panel, setPanel] = useState({ reconcile_seconds: 0, stats_seconds: 0, billing_alert_hours: 0, billing_alert_days: 0, expiry_alert_days: 0, session_hours: 0, egress_failover_seconds: 0, activity_kb_per_min: 0 });
  const [tg, setTg] = useState({ id: "", chat: "" });
  // What the server last handed us. Everything on the tab compares against it, so
  // Save and Cancel light up only when there is something to save or undo, and a
  // channel cannot be tested against settings that exist only in the browser.
  const [snap, setSnap] = useState(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await api("GET", "/api/settings");
      setCfg(s);
      let myChat = "";
      try {
        const r = await api("GET", "/api/admins");
        const me = (r.admins || []).find(a => a.is_current);
        if (me) myChat = me.alert_chat_id || "";
      } catch {}
      setTg({ id: "", chat: myChat });
      setBot({ enabled: s.bot.enabled, token: "" });
      setAlert({ enabled: s.alert.enabled, token: "", chat_id: s.alert.chat_id || "" });
      const b = s.backup || {};
      setBackup({ enabled: b.enabled || false, chat_id: b.chat_id || "", age_recipient: b.age_recipient || "", chunk_bytes: b.chunk_bytes || 0, local_enabled: b.local_enabled !== false, interval_hours: b.interval_hours || 24, keep: b.keep || 14, verify_enabled: b.verify_enabled !== false, verify_interval_days: b.verify_interval_days || 7, sender: b.sender || "", sender_resolved: b.sender_resolved || "" });
      setFleet({ acme_email: (s.fleet && s.fleet.acme_email) || "", reality_sni: (s.fleet && s.fleet.reality_sni) || "", brand: (s.fleet && s.fleet.brand) || "", apex: (s.fleet && s.fleet.apex) || "", connect_subdomain: (s.fleet && s.fleet.connect_subdomain) || "" });
      const pp = s.panel || {};
      const nextPanel = { reconcile_seconds: pp.reconcile_seconds || 0, stats_seconds: pp.stats_seconds || 0, billing_alert_hours: pp.billing_alert_hours || 0, billing_alert_days: pp.billing_alert_days || 0, expiry_alert_days: pp.expiry_alert_days || 0, session_hours: pp.session_hours || 0, egress_failover_seconds: pp.egress_failover_seconds || 0, activity_kb_per_min: pp.activity_kb_per_min || 0 };
      setPanel(nextPanel);
      setSnap(JSON.stringify({
        bot: { enabled: s.bot.enabled, token: "" },
        alert: { enabled: s.alert.enabled, token: "", chat_id: s.alert.chat_id || "" },
        backup: { enabled: b.enabled || false, chat_id: b.chat_id || "", age_recipient: b.age_recipient || "", chunk_bytes: b.chunk_bytes || 0, local_enabled: b.local_enabled !== false, interval_hours: b.interval_hours || 24, keep: b.keep || 14, verify_enabled: b.verify_enabled !== false, verify_interval_days: b.verify_interval_days || 7, sender: b.sender || "" },
        fleet: { acme_email: (s.fleet && s.fleet.acme_email) || "", reality_sni: (s.fleet && s.fleet.reality_sni) || "", brand: (s.fleet && s.fleet.brand) || "", apex: (s.fleet && s.fleet.apex) || "", connect_subdomain: (s.fleet && s.fleet.connect_subdomain) || "" },
        panel: nextPanel,
        tg: { id: "", chat: myChat },
      }));
      return s;
    } catch (e) { notify(e.message, "err"); return null; }
  }, [notify]);
  useEffect(() => { load(); }, [load]);

  // What the form holds right now, in the shape the endpoint takes.
  const sectionsNow = () => ({
    bot: { enabled: bot.enabled, token: bot.token },
    alert: { enabled: alert.enabled, token: alert.token, chat_id: (alert.chat_id || "").trim() },
    backup: { enabled: backup.enabled, chat_id: (backup.chat_id || "").trim(), age_recipient: (backup.age_recipient || "").trim(), chunk_bytes: parseInt(backup.chunk_bytes, 10) || 0, local_enabled: backup.local_enabled, interval_hours: parseInt(backup.interval_hours, 10) || 0, keep: parseInt(backup.keep, 10) || 0, verify_enabled: backup.verify_enabled, verify_interval_days: parseInt(backup.verify_interval_days, 10) || 0, sender: backup.sender || "" },
    fleet: { acme_email: (fleet.acme_email || "").trim(), reality_sni: (fleet.reality_sni || "").trim(), brand: (fleet.brand || "").trim(), apex: (fleet.apex || "").trim(), connect_subdomain: (fleet.connect_subdomain || "").trim().toLowerCase() },
    panel: { reconcile_seconds: parseInt(panel.reconcile_seconds, 10) || 0, stats_seconds: parseInt(panel.stats_seconds, 10) || 0, billing_alert_hours: parseInt(panel.billing_alert_hours, 10) || 0, billing_alert_days: parseInt(panel.billing_alert_days, 10) || 0, expiry_alert_days: parseInt(panel.expiry_alert_days, 10) || 0, session_hours: parseInt(panel.session_hours, 10) || 0, egress_failover_seconds: parseInt(panel.egress_failover_seconds, 10) || 0, activity_kb_per_min: parseInt(panel.activity_kb_per_min, 10) || 0 },
  });
  // The sections this Save would write, whichever tab they were edited on.
  const pending = () => {
    if (snap === null) return [];
    const was = JSON.parse(snap || "{}"), now = sectionsNow();
    const out = SETTINGS_SECTIONS.filter(k => JSON.stringify(now[k]) !== JSON.stringify(was[k] === undefined ? null : was[k]));
    if (JSON.stringify(tg) !== JSON.stringify(was.tg === undefined ? null : was.tg)) out.push("tg");
    return out;
  };

  const save = async () => {
    const tgId = tg.id.trim() === "" ? 0 : Number(tg.id.trim());
    if (tg.id.trim() !== "" && !Number.isInteger(tgId)) { notify(t("Telegram id must be a number"), "err"); return; }
    setBusy(true);
    const before = cfg ? { bot: cfg.bot.checked_at || 0, alert: cfg.alert.checked_at || 0 } : { bot: 0, alert: 0 };
    let ok = false;
    const was = JSON.parse(snap || "{}");
    const now = sectionsNow();
    // Only the sections that were actually edited go in the request. One Save
    // must not write all five, including the tabs nobody has opened, or a change
    // to an interval would also rewrite the backup block as the browser happened
    // to hold it. Which ones those are is now said on the button as well, because a
    // section edited two tabs ago is still a section this Save writes.
    const body = {};
    const changed = [];
    for (const k of SETTINGS_SECTIONS) {
      if (JSON.stringify(now[k]) !== JSON.stringify(was[k] === undefined ? null : was[k])) { body[k] = now[k]; changed.push(k); }
    }
    try {
      if (changed.length) await api("POST", "/api/settings", body);
      // The Telegram binding belongs to the account, not to the infra settings, so
      // it has its own endpoint, but the same Save.
      if (tg.id.trim() !== "" || tg.chat !== (was.tg || {}).chat) {
        await api("POST", "/api/account/telegram", { telegram_id: tgId, alert_chat_id: tg.chat });
        changed.push("tg");
      }
      ok = true;
    } catch (e) { notify(e.message, "err"); }
    setBusy(false);
    if (!ok) return;
    // The message says which parts were written, because "Saved" on a page of
    // five blocks does not say what was saved.
    notify(changed.length
      ? tf("Saved: {sections}", { sections: changed.map(k => t(SETTINGS_SECTION_LABEL[k])).join(", ") })
      : t("Nothing had changed"), changed.length ? "ok" : "info");
    // Saving fires a live Telegram getMe check for both channels in the background
    // (it can't block the save: a dead token stalls the request up to 10s). The
    // result lands asynchronously, so poll the settings until each channel's
    // checked_at advances past what we saw before the save; the status light then
    // updates on its own instead of waiting for a manual page refresh. Bounded
    // (~12s) so an unreachable token can't poll forever.
    for (let i = 0; i < 10; i++) {
      const s = await load();
      if (s && (s.bot.checked_at || 0) > before.bot && (s.alert.checked_at || 0) > before.alert) break;
      await new Promise(r => setTimeout(r, 1200));
    }
  };
  const testChannel = async (channel) => {
    try { const r = await api("POST", "/api/settings/test-channel", { channel }); notify((r.detail || "ok"), "ok"); }
    catch (e) { notify(e.message, "err"); }
  };

  if (!cfg) return html`<div class=card><${Skeleton} rows=${5} cols=${2} /></div>`;
  const tokenPlaceholder = (set, last4) => set ? t("set") + " ••••" + (last4 || "") + " — " + t("leave blank to keep") : t("paste bot token");
  const dirty = snap !== null && snap !== JSON.stringify({ bot, alert, backup, fleet, panel, tg });
  const revert = () => { load(); };
  const saveBar = () => {
    const will = pending();
    return html`<div class=savebar>
      ${will.length > 0 && html`<span class=savescope>${tf("Will save: {sections}", { sections: will.map(k => t(SETTINGS_SECTION_LABEL[k])).join(", ") })}</span>`}
      <button class=ghost disabled=${busy || !dirty} onClick=${revert}>${t("Cancel")}</button>
      <button disabled=${busy || !dirty} onClick=${save}>${busy ? t("Saving…") : t("Save")}</button>
    </div>`;
  };
  // A yes/no answer looks the same everywhere in the panel: the switch from the
  // tables and the forms, not a checkbox only this page has.
  const chk = (id, on, set, label) => html`<div class="field check f-wide">
    <${Switch} id=${id} on=${!!on} label=${label} onChange=${e => set(e.target.checked)} />
    <label for=${id} style=${{ margin: 0 }}>${label}</label>
  </div>`;
  // A test sends through the SAVED token, so testing an edited-but-unsaved form
  // would check the old channel and report the wrong answer.
  const testBtn = (channel) => html`<button class="ghost sm" disabled=${dirty}
    title=${dirty ? t("Save first — a test uses the saved settings") : ""} onClick=${() => testChannel(channel)}>${t("Test")}</button>`;

  // Subtabs keep the settings focused: one section per screen instead of one long
  // scroll. Every section still saves through the same endpoint (blank tokens are
  // preserved server-side), so a Save on any tab persists the whole form safely.
  // A tab that holds an unsaved change says so, so the Save on this screen is
  // never a surprise about another one.
  const TAB_SECTIONS = { server: ["fleet", "panel"], bots: ["bot", "alert", "tg"], backup: ["backup"] };
  const waiting = pending();
  const tabLabel = (key, label) => ((TAB_SECTIONS[key] || []).some(k => waiting.includes(k))
    ? html`${label} <span class=tabdot title=${t("unsaved changes")} aria-hidden=true>•</span>`
    : label);
  const tabs = [["server", tabLabel("server", t("Server"))], ["bots", tabLabel("bots", t("Bots & alerts"))],
    ["backup", tabLabel("backup", t("Backup"))], ["account", t("My account")]];
  if (canManage) tabs.push(["members", t("Accounts")]);
  if (isBootstrap) tabs.push(["advanced", t("Advanced")]);
  const cur = tabs.some(([k]) => k === sub) ? sub : "server";

  return html`<div class=settings-page>
    <${PageHead} title=${t("Settings")} />
    <${SubTabs} tabs=${tabs} active=${cur} onSelect=${setSub} />
    ${cur === "server" && html`
      <${FleetAndPanelSettings} fleet=${fleet} setFleet=${setFleet} panel=${panel} setPanel=${setPanel} />
      ${saveBar()}`}

    ${cur === "bots" && html`
      <${FormSec} title=${t("Management bot")} status=${html`<${StatusChip} s=${cfg.bot} />`} action=${testBtn("bot")}
                  note=${t("Runs the commands and, by default, sends the alerts.")}>
        ${chk("sc-bot-on", bot.enabled, v => setBot({ ...bot, enabled: v }), t("Enabled"))}
        <${LF} label=${t("Bot token")} hint=${t("From @BotFather. For the Geosite catalogue search, switch on /setinline and /setinlinefeedback there as well.")} wide=${true}>
          <input type=password value=${bot.token} placeholder=${tokenPlaceholder(cfg.bot.token_set, cfg.bot.token_last4)} onInput=${e => setBot({ ...bot, token: e.target.value })} /><//>
      <//>
      <${FormSec} title=${t("My bot access")}>
        <${TelegramBinding} value=${tg.id} onChange=${v => setTg({ ...tg, id: v })} />
      <//>
      <${FormSec} title=${t("Alerts")} status=${html`<${StatusChip} s=${cfg.alert} />`} action=${testBtn("alert")}
                  note=${t("Infra alerts (payments, watchdog) — sent by the management bot.")}>
        ${chk("sc-alert-on", alert.enabled, v => setAlert({ ...alert, enabled: v }), t("Send alerts"))}
        <${LF} label=${t("Alert chat ID")}>
          <input value=${alert.chat_id} placeholder="-1001234567890" onInput=${e => setAlert({ ...alert, chat_id: e.target.value })} /><//>
      <//>
      <${FormSec} title=${t("Backup alert bot")} action=${testBtn("alert_backup")}
                  note=${t("Sends word when the primary node stops answering.")}>
        <${LF} label=${t("Backup bot token")} wide=${true}>
          <input type=password value=${alert.token} placeholder=${tokenPlaceholder(cfg.alert.token_set, cfg.alert.token_last4)} onInput=${e => setAlert({ ...alert, token: e.target.value })} /><//>
        ${cfg.alert.shares_bot_channel && html`<p class="warn f-wide" style=${{ margin: 0, fontSize: "12px" }}>${t("One bot on both channels. Set a management bot to split them.")}</p>`}
      <//>
      ${saveBar()}`}

    ${cur === "backup" && html`
      <${FormSec} title=${t("Schedule and retention")} note=${t("Local snapshots (database + CA), one timer per node.")}>
        ${chk("sc-bk-local", backup.local_enabled, v => setBackup({ ...backup, local_enabled: v }), t("Local backup enabled"))}
        <${LF} label=${t("Run every (hours)")}>
          <input type=number min=1 value=${backup.interval_hours} onInput=${e => setBackup({ ...backup, interval_hours: e.target.value })} /><//>
        <${LF} label=${t("Copies to keep")}>
          <input type=number min=1 value=${backup.keep} onInput=${e => setBackup({ ...backup, keep: e.target.value })} /><//>
      <//>
      <${FormSec} title=${t("Proving they restore")} note=${t("A copy is restored into a scratch database on a schedule, so a backup that cannot be read is found before it is needed.")}>
        ${chk("sc-bk-verify", backup.verify_enabled, v => setBackup({ ...backup, verify_enabled: v }), t("Verify-restore drill enabled"))}
        <${LF} label=${t("Verify every (days)")}>
          <input type=number min=1 value=${backup.verify_interval_days} onInput=${e => setBackup({ ...backup, verify_interval_days: e.target.value })} /><//>
      <//>
      <${FormSec} title=${t("Copies in Telegram")} action=${testBtn("backup")}
                  note=${t("Encrypted with age, sent to a private Telegram channel.")}>
        ${chk("sc-bk-on", backup.enabled, v => setBackup({ ...backup, enabled: v }), t("Enabled"))}
        <${LF} label=${t("Sent by")} hint=${senderHint(backup)}>
          <select value=${backup.sender || ""} onChange=${e => setBackup({ ...backup, sender: e.target.value })}>
            <option value="">${t("Choose automatically")}</option>
            <option value="alert">${t("The spare alerts bot")}</option>
            <option value="bot">${t("The management bot")}</option>
          </select><//>
        <${LF} label=${t("Backup chat ID")}>
          <input value=${backup.chat_id} placeholder="-1009876543210" onInput=${e => setBackup({ ...backup, chat_id: e.target.value })} /><//>
        <${LF} label=${t("Part size (MiB)")} hint=${t("blank or 0 = one part, up to Telegram's own limit")}>
          <input type=number min=0 step=1 value=${mib(backup.chunk_bytes)} placeholder="45"
            onInput=${e => setBackup({ ...backup, chunk_bytes: bytesFromMiB(e.target.value) })} /><//>
        <${LF} label=${t("age recipient (public key)")} wide=${true}
          hint=${t("Generate with age-keygen; paste only the public key (age1…). Keep the private key OFFLINE.")}>
          <input value=${backup.age_recipient} placeholder="age1…" onInput=${e => setBackup({ ...backup, age_recipient: e.target.value })} /><//>
      <//>
      ${saveBar()}`}

    ${cur === "account" && html`<${AccountSettings} notify=${notify} />`}

    ${cur === "members" && canManage && html`<${Members} notify=${notify} isAdmin=${true} myNs=${myNs} />`}

    ${cur === "advanced" && isBootstrap && html`<${AdvancedSettings} crossNs=${crossNs} toggleCrossNs=${toggleCrossNs} />`}</div>`;
}

// AdvancedSettings is the buried developer surface (bootstrap owner only). Its
// sole control is the cross-namespace lens, a deliberate and occasional act, kept
// out of the everyday settings behind its own "Advanced" subtab.
function AdvancedSettings({ crossNs, toggleCrossNs }) {
  const flip = async () => {
    if (!crossNs) {
      const ok = await uiConfirm({
        title: t("Show every scope?"),
        lines: [t("Shows other tenants' clients, groups and routes across the panel. Turns off when you sign out.")],
        confirmLabel: t("Show all scopes"),
        danger: true,
      });
      if (!ok) return;
    }
    toggleCrossNs(!crossNs);
  };
  return html`
    <div class=section style=${{ marginTop: "1.5rem" }}>
      <h2>${t("Cross-scope view")}</h2>
      <p class=lede>${crossNs ? t("On — you are seeing every scope.") : t("Off — you see only your own scope.")}</p>
      <button class=${crossNs ? "danger" : "ghost"} onClick=${flip}>${crossNs ? t("Turn off") : t("Turn on")}</button>
    </div>`;
}

/* ---------------- entity table ---------------- */
// RowMenu: everything a row can do apart from its main action, behind one control.
// The list is rendered into the body in viewport coordinates; inside the table it
// would sit under the pinned action cells of the rows below it.
// A table too wide for its window scrolls, and nothing said so: the column that
// answers "is this server working" was simply not on screen. This marks which
// side is holding something back, and the stylesheet draws the edge.
function useScrollEdges() {
  const ref = useRef(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const mark = () => {
      const left = el.scrollLeft > 1;
      const right = el.scrollWidth - el.clientWidth - el.scrollLeft > 1;
      el.dataset.scroll = [left ? "left" : "", right ? "right" : ""].filter(Boolean).join(" ");
    };
    mark();
    el.addEventListener("scroll", mark, { passive: true });
    const ro = typeof ResizeObserver === "function" ? new ResizeObserver(mark) : null;
    if (ro) ro.observe(el);
    return () => { el.removeEventListener("scroll", mark); if (ro) ro.disconnect(); };
  });
  return ref;
}

function RowMenu({ items }) {
  const [at, setAt] = useState(null); // {top,right} while open
  const btn = useRef(null);
  // A separator only earns its place between two real entries.
  const list = items.filter(Boolean).filter((m, i, a) => !m.sep || (i > 0 && i < a.length - 1));
  useEffect(() => {
    if (!at) return;
    const close = () => setAt(null);
    const onKey = (e) => { if (e.key === "Escape") close(); };
    window.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    document.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
      document.removeEventListener("keydown", onKey);
    };
  }, [at]);
  if (list.length === 0) return null;
  const open = () => {
    if (at) { setAt(null); return; }
    const r = btn.current.getBoundingClientRect();
    setAt({ top: Math.round(r.bottom + 4), right: Math.round(window.innerWidth - r.right) });
  };
  return html`<span class=rowmenu>
    <button ref=${btn} class=${"ghost sm" + (at ? " active" : "")} aria-haspopup=menu aria-expanded=${!!at}
      aria-label=${t("More actions")} title=${t("More actions")} onClick=${open}>⋯</button>
    ${at && ReactDOM.createPortal(html`<${React.Fragment}>
      <div class=menu-scrim onClick=${() => setAt(null)}></div>
      <div class=rowmenu-list role=menu style=${{ top: at.top + "px", right: at.right + "px" }}>
        ${list.map((m, i) => m.sep
          ? html`<hr key=${i} class=menu-sep />`
          : html`<button key=${i} role=menuitem class=${m.danger ? "danger" : ""}
              onClick=${() => { btn.current?.focus(); setAt(null); m.onClick(); }}>${m.label}</button>`)}
      </div>
    <//>`, document.body)}
  </span>`;
}

function EntityPanel({ cfg, rows, state, reload, notify, setModal, isAdmin = true, me = "", initialFilter = "", head = null, subnav = null, overview = null, traffic = null }) {
  // Node lifecycle is ownable: an operator manages its own nodes and views the
  // rest read-only (the server gates writes regardless; this hides dead buttons).
  // Non-node panels (users/groups/policies) are server-scoped, so every shown row
  // is writable. Two-node convert + HA stay admin-only.
  // ownerGated panels (nodes, route policies) are row-scoped in the UI: an
  // operator edits only its own rows and sees the rest (e.g. admin guard rules)
  // read-only. Non-gated panels (users/groups) are already server-scoped, so
  // every shown row is the viewer's. The server enforces all of this regardless.
  // Most ownerGated panels carry owner_id on the row; some (domains) own through
  // a related record, so cfg.ownerOf resolves the effective owner.
  const ownerOf = (it) => cfg.ownerOf ? cfg.ownerOf(it, state) : it.owner_id;
  const ownsRow = (it) => isAdmin || !cfg.ownerGated || ownerOf(it) === me;
  const tbl = useScrollEdges();
  const [showHelp, setShowHelp] = useState(false);
  const [q, setQ] = useState(initialFilter); // seeded after a cross-tab jump (Groups → Routing)
  const [sort, setSort] = useState(null);    // {i, dir} — the column index and direction
  const query = q.trim().toLowerCase();
  const matched = cfg.filter && query ? rows.filter(it => cfg.filter(it, query, state)) : rows;
  // A routing table's order is the answer it gives ("top to bottom, first match
  // wins"), so it is not the reader's to rearrange. Every other table sorts.
  const sortable = !cfg.order;
  // A column sorts by its own value; where the cell is computed from elsewhere
  // (a node's load, a client's traffic) the column carries an accessor.
  const sortVal = (c, it) => (typeof c[4] === "function" ? c[4](it, state) : it[c[0]]);
  const shown = (() => {
    if (cfg.order) return [...matched].sort(cfg.order);
    if (!sort || !cfg.columns[sort.i]) return matched;
    const c = cfg.columns[sort.i], dir = sort.dir === "desc" ? -1 : 1;
    const rank = (v) => (v == null || v === "" ? 1 : 0); // empties last, either way
    return [...matched].sort((a, b) => {
      const va = sortVal(c, a), vb = sortVal(c, b);
      const ra = rank(va), rb = rank(vb);
      if (ra !== rb) return ra - rb;
      if (typeof va === "number" && typeof vb === "number") return (va - vb) * dir;
      if (typeof va === "boolean" && typeof vb === "boolean") return ((va ? 1 : 0) - (vb ? 1 : 0)) * dir;
      return String(va).localeCompare(String(vb), LANG === "ru" ? "ru" : "en", { numeric: true }) * dir;
    });
  })();
  const toggleSort = (i) => setSort(s2 => s2 && s2.i === i ? (s2.dir === "asc" ? { i, dir: "desc" } : null) : { i, dir: "asc" });
  // One main action stays on the row and the rest fold into a menu: five text
  // buttons pinned to the right ate the columns beside them at 1280. For most
  // tables the main action is Edit; a client's is issuing its config, which is the
  // daily act there.
  const editAction = (it) => ({ label: t("Edit"), onClick: () => setModal({ kind: "form", cfg, initial: it }) });
  const configAction = (it) => ({ label: t("Config"), onClick: () => setModal({ kind: "config", userId: it.id, username: it.username, user: it }) });
  const primaryIsConfig = (cfg.rowPrimary || []).includes("config");
  const rowPrimary = (it) => primaryIsConfig ? configAction(it) : ownsRow(it) ? editAction(it) : null;
  const rowMenu = (it) => {
    const acts = cfg.rowActions || [];
    const mine = ownsRow(it);
    return [
      acts.includes("open") && { label: t("Open"), onClick: () => setModal({ kind: "nodecard", node: it }) },
      mine && primaryIsConfig && editAction(it),
      cfg.series && { label: t("Graph"), onClick: () => setModal({ kind: "graph", series: cfg.series, entityId: it.id, name: it.name || it.display_name || it.username || it.id }) },
      acts.includes("config") && !primaryIsConfig && configAction(it),
      mine && acts.includes("limits") && { label: t("Resources & payment"), onClick: () => setModal({ kind: "limits", node: it }) },
      mine && cfg.maintenanceAction && { label: it.maintenance ? t("Resume") : t("Drain"), onClick: () => drainNode({ node: it, state, overview, traffic, path: cfg.path, reload, notify }) },
      mine && cfg.toggle && cfg.toggleInMenu && { label: ucfirst(t(cfg.toggle.label(it))), onClick: () => toggle(it) },
      mine && acts.includes("promote-tls") && isStagingIssuer(it.tls_issuer) && { label: t("Issue a production certificate"), onClick: () => promoteTLS(it) },
      mine && { sep: true },
      mine && { label: t("Delete"), danger: true, onClick: () => del(it) },
    ];
  };
  const del = async (it) => {
    const name = it.name || it.display_name || it.username || it.hostname || it.id;
    const blocked = deleteBlocker(cfg, it, state);
    if (!await uiConfirm({ title: tf("Delete {name}?", { name }), warn: blocked || null, blocked: !!blocked,
      lines: blocked ? [] : deleteLines(cfg, it, state), confirmLabel: t("Delete"), danger: true })) return;
    try { await api("DELETE", cfg.path + "/" + encodeURIComponent(it.id)); reload(); notify(tf("{name} deleted", { name: it.name || it.username || it.hostname || it.id }), "info"); }
    catch (e) { notify(e.message, "err"); }
  };
  // isOn reads the switch: a policy carries "disabled", a client "enabled".
  const isOn = (it) => cfg.toggle ? (cfg.toggle.on ? cfg.toggle.on(it) : !it[cfg.toggle.field]) : false;
  const toggle = async (it) => {
    const on = isOn(it);
    // Some rows cannot be switched on at all (a client past its access date).
    // The question that replaces the switch carries the way out of it: the form
    // where the date is.
    const refuse = !on && cfg.toggle.refuseOn && cfg.toggle.refuseOn(it);
    if (refuse) {
      if (await uiConfirm(refuse)) setModal({ kind: "form", cfg, initial: it });
      return;
    }
    try {
      if (cfg.toggle.bulkAction) await api("POST", cfg.path + "/bulk", { ids: [it.id], action: cfg.toggle.bulkAction(on) });
      else await api("POST", cfg.path, cfg.toggle.next(it));
      reload();
      // Switching something off on purpose is neither an error nor a success:
      // beside a switch, a green light read as "it is on now". The message names
      // what changed instead, so the colour is not carrying the meaning.
      const label = it.name || it.username || it.hostname || it.id;
      notify(on ? tf("{name} is off", { name: label }) : tf("{name} is on", { name: label }), on ? "info" : "ok");
    } catch (e) { notify(e.message, "err"); }
  };
  const promoteTLS = async (it) => {
    if (!it.node_id) { notify(t("This domain has no node to promote"), "err"); return; }
    if (!await uiConfirm({ title: t("Switch this node's certificate from staging to Let's Encrypt production and reissue now?"), confirmLabel: t("→ prod") })) return;
    try { await api("POST", "/api/nodes/" + encodeURIComponent(it.node_id) + "/promote-tls"); reload(); notify(t("Reissued from production"), "ok"); }
    catch (e) { notify(e.message, "err"); }
  };
  // ---- bulk multi-select (proposals §5; opt-in via cfg.bulk) ----
  // The checkbox, the on/off switch and the action cell sit outside cfg.columns.
  const cols = cfg.columns.length + 1 + (cfg.bulk ? 1 : 0) + (cfg.toggle && !cfg.toggleInMenu ? 1 : 0);
  const [sel, setSel] = useState(() => new Set());
  const [bulkGroup, setBulkGroup] = useState("");
  const [bulkExp, setBulkExp] = useState("");
  const shownIds = shown.map(it => it.id);
  const allSel = shownIds.length > 0 && shownIds.every(id => sel.has(id));
  const toggleAll = () => setSel(allSel ? new Set() : new Set(shownIds));
  const toggleOne = (id) => setSel(s => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const groups = state && state.groups || [];
  const bulk = async (action, extra, after) => {
    if (sel.size === 0) return;
    try {
      const res = await api("POST", cfg.path + "/bulk", { ids: [...sel], action, ...(extra || {}) });
      // A second action the first one had to happen for: switching clients back
      // on is refused while their access date is still the old one, so the
      // extension goes first and the switch follows on the same button press.
      if (after && after.ids.length) await api("POST", cfg.path + "/bulk", { ids: after.ids, action: after.action });
      setSel(new Set());
      await reload();
      notify(tf("{n} updated", { n: res.applied }) + (res.failed ? " · " + tf("{n} failed", { n: res.failed }) : ""), res.failed ? "err" : "ok");
    } catch (e) { notify(e.message, "err"); }
  };
  const picked = () => rows.filter(it => sel.has(it.id));
  const bulkConfirm = async (action, label) => {
    if (sel.size === 0) return;
    const note = cfg.bulkNote ? cfg.bulkNote(action, picked()) : null;
    if (!await uiConfirm({ title: tf("{label} {n} selected?", { label, n: sel.size }), lines: note ? [note] : [], confirmLabel: label, danger: action === "delete" })) return;
    bulk(action);
  };
  const bulkSetExpiry = async () => {
    if (!isYMD(bulkExp)) { notify(tf("{label} is not a date", { label: t("Expires") }), "err"); return; }
    // The same question the single-client form asks, for the whole selection:
    // extending the ones that are switched off says nothing about whether they
    // are meant to come back on.
    const off = picked().filter(it => it.enabled === false).map(it => it.id);
    let alsoOn = false;
    if (off.length) {
      const ans = await uiConfirm({
        title: tf("Switched off: {n}", { n: plural(off.length, "client") }),
        lines: [tf("Access now runs to {date}.", { date: fmtDMY(bulkExp) }), t("Switch them on as well?")],
        confirmLabel: t("Switch on and save"),
        alt: { label: t("Save, leave them off") },
      });
      if (!ans) return;
      alsoOn = ans !== "alt";
    }
    bulk("set_expiry", { expires_at: bulkExp + "T23:59:59Z" }, alsoOn ? { ids: off, action: "enable" } : null);
  };
  // The screen's own actions sit in its head, above the section navigation;
  // the filter belongs with the rows it filters, so it stays over the table.
  const actions = html`
    ${cfg.tester && html`<button class=ghost onClick=${() => setModal({ kind: "routetest" })}>${t("Route tester")}</button>`}
    ${cfg.provision && isAdmin && rows.length === 1 && rows[0].public_role === "entry" && html`<button class=ghost title=${t("Put a new server in front of this one and turn this one into the exit")} onClick=${() => setModal({ kind: "convert" })}>${t("Split into two servers")}</button>`}
    ${cfg.provision && html`<button onClick=${() => setModal({ kind: "provision" })}>+ ${t("Add server")}</button>`}
    ${!cfg.provision && html`<button onClick=${() => setModal({ kind: "form", cfg, initial: null })}>${cfg.route ? t("New rule") : "+ " + t("Add") + " " + t(cfg.singular)}</button>`}`;

  return html`
    ${head && html`<${PageHead} title=${head.title} actions=${actions} />`}
    ${subnav}
    <div class="section table-section">
      <div class="between table-toolbar">
        ${cfg.filter ? html`<input class=filter type=search value=${q} aria-label=${t(cfg.filterLabel || "Filter…")} placeholder=${t(cfg.filterLabel || "Filter…")} onInput=${e => setQ(e.target.value)} />` : html`<span></span>`}
        <div class=table-toolbar-end>
          ${cfg.route ? html`<span class=table-summary>${plural(shown.length, "rule")}</span>` : head && head.summary && html`<span class=table-summary>${head.summary}</span>`}
          ${cfg.help && html`<button type=button class="ghost sm help-button" aria-expanded=${showHelp} onClick=${() => setShowHelp(h => !h)}>${t("How this works")}</button>`}
        </div>
        ${!head && html`<div class=row>${actions}</div>`}
      </div>
      ${cfg.help && showHelp && html`<div class=note>
        <strong>${t(cfg.help.title)}</strong>
        <ul>${cfg.help.points.map((p, i) => html`<li key=${i}>${t(p)}</li>`)}</ul>
      </div>`}
      ${cfg.bulk && sel.size > 0 && html`<div class=bulkbar>
        <strong>${tf("{n} selected", { n: sel.size })}</strong>
        <button class="ghost sm" onClick=${() => bulkConfirm("enable", t("Enable"))}>${t("Enable")}</button>
        <button class="ghost sm" onClick=${() => bulkConfirm("disable", t("Disable"))}>${t("Disable")}</button>
        <span class=muted>·</span>
        <select value=${bulkGroup} onChange=${e => setBulkGroup(e.target.value)}>
          <option value="">${t("Move to group…")}</option>
          ${groups.map(g => html`<option key=${g.id} value=${g.id}>${g.name || g.id}</option>`)}
        </select>
        <button class="ghost sm" disabled=${!bulkGroup} onClick=${() => bulk("set_group", { group_id: bulkGroup })}>${t("Apply")}</button>
        <span class=muted>·</span>
        <input style=${{ width: "9.5rem" }} type=date aria-label=${t("Expires")} value=${bulkExp} onInput=${e => setBulkExp(e.target.value)} />
        <button class="ghost sm" onClick=${bulkSetExpiry}>${t("Set expiry")}</button>
        <button class="ghost sm" onClick=${() => bulk("clear_expiry")}>${t("Clear expiry")}</button>
        <span class=muted>·</span>
        <button class="danger sm" onClick=${() => bulkConfirm("delete", t("Delete"))}>${t("Delete")}</button>
        <button class="ghost sm" onClick=${() => setSel(new Set())}>${t("Clear selection")}</button>
      </div>`}
      <div class=tblwrap ref=${tbl}><table class=${"data-table " + (cfg.route ? "route-table" : "entity-table")}>
        <thead><tr>${cfg.bulk ? html`<th class=select-cell><input type=checkbox style=${selChkStyle} checked=${allSel} onChange=${toggleAll} aria-label=${t("Select all shown")} title=${t("Select all shown")} /></th>` : ""}${cfg.toggle && !cfg.toggleInMenu ? html`<th class="sw-h control-cell">${t("On")}</th>` : ""}${cfg.columns.map((c, i) => {
          const on = sort && sort.i === i;
          if (!sortable || !c[1]) return html`<th key=${i} class=${c[3] || ""}>${t(c[1])}</th>`;
          return html`<th key=${i} class=${(c[3] || "") + " sortable" + (on ? " on" : "")}
            aria-sort=${on ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}>
            <button onClick=${() => toggleSort(i)} title=${t("Sort by this column")}>${t(c[1])}<span class=sortmark aria-hidden=true>${on ? (sort.dir === "asc" ? "↑" : "↓") : "↕"}</span></button>
          </th>`;
        })}<th class=acts>${t("Actions")}</th></tr></thead>
        <tbody>
          ${rows.length === 0 && html`<tr><td colspan=${cols}>
            <div class=blank>
              <h3>${t("Nothing here yet")}</h3>
              ${cfg.blank && html`<p>${t(cfg.blank)}</p>`}
              ${cfg.provision
                ? html`<button onClick=${() => setModal({ kind: "provision" })}>+ ${t("Add server")}</button>`
                : html`<button onClick=${() => setModal({ kind: "form", cfg, initial: null })}>+ ${t("Add")} ${t(cfg.singular)}</button>`}
            </div></td></tr>`}
          ${rows.length > 0 && shown.length === 0 && html`<tr><td class=empty colspan=${cols}>${t("No matches")}</td></tr>`}
          ${shown.map(it => html`<tr key=${it.id} class=${cfg.toggle && !isOn(it) ? "row-off" : ""}>
            ${cfg.bulk ? html`<td class=select-cell><input type=checkbox style=${selChkStyle} checked=${sel.has(it.id)} onChange=${() => toggleOne(it.id)}
              aria-label=${tf("Select {name}", { name: it.name || it.display_name || it.username || it.hostname || it.id })}
              title=${t("Select this row for bulk actions")} /></td>` : ""}
            ${cfg.toggle && !cfg.toggleInMenu ? html`<td class=control-cell><label class=switch title=${ucfirst(t(cfg.toggle.label(it)))}><input type=checkbox disabled=${!ownsRow(it)} checked=${isOn(it)} aria-label=${ucfirst(t(cfg.toggle.label(it)))} onChange=${() => toggle(it)} /><i></i></label></td>` : ""}
            ${cfg.columns.map(c => html`<td class=${c[3] || ""}>${c[2] ? c[2](it[c[0]], it, state) : fmt(it[c[0]])}</td>`)}
            <td class=acts><div class=rowacts>
              ${(() => { const a = rowPrimary(it); return a
                ? html`<button class="ghost sm" onClick=${a.onClick}>${a.label}</button>`
                : html`<span class=muted style=${{ fontSize: "11px" }}>${t("read-only")}</span>`; })()}
              <${RowMenu} items=${rowMenu(it)} />
            </div></td>
          </tr>`)}
        </tbody>
      </table></div>
    </div>`;
}

function ucfirst(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
// Row/select-all checkbox: enlarged with an accent tint so it reads clearly as a
// multi-row selector (not a stray tick).
const selChkStyle = { width: "17px", height: "17px", cursor: "pointer", verticalAlign: "middle" };
function fmt(v) { return Array.isArray(v) ? v.join(", ") : (v === true ? "✓" : v === false ? "" : (v == null ? "" : String(v))); }
// A staging leaf is reported with a "(STAGING)" issuer by Let's Encrypt; only
// then is "→ prod" meaningful (a production cert needs no promotion).
function isStagingIssuer(v) { return /staging/i.test(v || ""); }

/* ---------------- generic form modal ---------------- */
function FormModal({ cfg, initial, state, onClose, reload, notify }) {
  // A select's choices can depend on the rest of the form: the exit left to
  // everyone else must not be the one the rule's own group uses.
  const optionsOf = (f, v) => (typeof f.options === "function" ? f.options(v || {}) : f.options);
  const firstOpt = (f) => { const o = (optionsOf(f, {}) || [])[0]; return o == null ? "" : (typeof o === "object" ? o.value : o); };
  const init = () => {
    const v = {};
    for (const f of cfg.fields) {
      // A field that is not on this screen has no starting value to take. Two
      // fields can share a name, the same question asked differently on create
      // and on edit, and the one that is not shown must not answer for the one
      // that is by being read second.
      if (f.createOnly && initial) continue;
      if (f.editOnly && !initial) continue;
      let cur = initial ? getPath(initial, f.name) : undefined;
      // A synthetic field (e.g. routing "level") has no stored column, so derive its
      // edit value from the rest of the saved record.
      if (cur === undefined && initial && f.deriveInitial) cur = f.deriveInitial(initial);
      // Defaults apply only on create; on edit an absent (omitempty) field means
      // the saved value was empty and must be shown as-is, not overwritten.
      if (cur === undefined && !initial && f.def !== undefined) cur = f.def;
      // On create a select with no value defaults to its first option, so the
      // shown value matches state (no spurious "field required" on submit).
      if (cur === undefined && !initial && f.type === "select") cur = firstOpt(f);
      if (f.type === "tags" || f.type === "country") v[f.name] = Array.isArray(cur) ? cur : (cur ? [cur] : []);
      else if (f.type === "bool") v[f.name] = !!cur;
      else if (f.type === "date") v[f.name] = ymd(cur);
      else v[f.name] = cur == null ? "" : cur;
    }
    // The row's own id is not a field, but the live summary needs it to leave
    // this rule out of "what else catches the same traffic"; without it every
    // rule warns that it shadows itself.
    v.id = initial ? initial.id : "";
    return v;
  };
  const [vals, setVals] = useState(init);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(null);
  // Answers that are occasional live one step in: the window keeps its size and
  // nothing that decides anything sits below the fold. Same behaviour as the
  // routing form: "‹ Back" at the top of the part that was redrawn, under a
  // header that does not move.
  const [screen, setScreen] = useState("main");
  const opener = useRef(null);
  const screenOf = (f) => f.screen || "main";
  // The button that opened a screen is a different element once the main screen
  // is built again, so remember its name and find it after the screen is back.
  const openScreen = (name) => { opener.current = name; setScreen(name); };
  const backToMain = () => setScreen("main");
  React.useLayoutEffect(() => {
    const body = ref.current && ref.current.querySelector(".body");
    if (!body) return;
    body.scrollTop = 0;
    const target = screen !== "main" ? body.querySelector(".inner-back")
      : opener.current ? document.getElementById("door-" + opener.current) : null;
    if (target) target.focus();
  }, [screen]);
  // Dirty = the form no longer holds what it was opened with. A new form counts
  // as dirty as soon as anything is typed into it.
  const ref = useModal(onClose, () => JSON.stringify(vals) !== JSON.stringify(init()));
  // A refused save used to be a message in the corner while the form sat there
  // looking fine. It now names the field, marks it, and puts the cursor in it.
  const fail = (name, msg, index) => {
    setErr({ name, msg, index });
    // The field being complained about may be behind a door; open it, or the
    // message would be marking something nobody can see.
    const f = cfg.fields.find(x => x.name === name);
    if (f && screenOf(f) !== screen) setScreen(screenOf(f));
    setTimeout(() => {
      const el = document.getElementById(index == null ? "fld-" + name : "cond-" + index);
      if (el) el.focus();
    }, 0);
  };
  const set = (name, val) => setVals(s => {
    if (err && err.name === name) setErr(null);
    const next = { ...s, [name]: val };
    // One answer can strand another: sending everyone else out of the exit the
    // group itself just took leaves nothing reserved. Drop the stranded choice
    // so the form asks for it again instead of saving a contradiction.
    return cfg.onFieldChange ? cfg.onFieldChange(name, next) : next;
  });

  const submit = async () => {
    const obj = {};
    for (const f of cfg.fields) {
      if (f.showIf && !f.showIf(vals)) continue;
      // A create-only field is not on screen while editing, so nothing about it
      // can be asked of the operator, including that it match another field.
      // Judging it first made editing a client demand a confirmation box the
      // same form had just hidden.
      if (f.createOnly && initial) continue;
      if (f.editOnly && !initial) continue;
      // A field the form uses to decide what to ask is not part of the record.
      if (f.virtual) continue;
      // A confirmation field (e.g. "confirm password") must equal its target and
      // is never sent to the backend.
      if (f.matchField) {
        if (String(vals[f.name] || "") !== String(vals[f.matchField] || "")) {
          return fail(f.name, tf("{label} does not match", { label: t(f.label) }));
        }
        continue;
      }
      let val = vals[f.name];
      if (f.type === "tags" || f.type === "country") val = Array.isArray(val) ? val : [];
      else if (f.type === "number") val = val === "" ? 0 : parseInt(val, 10);
      else if (f.type === "bool") val = !!val;
      else if (f.type === "date") {
        // Blank = no expiry (omitted → forever). A set date means access through
        // the END of that day UTC, so store 23:59:59Z (matches the date shown back).
        if (String(val).trim() === "") continue;
        if (!isYMD(val)) return fail(f.name, tf("{label} is not a date", { label: t(f.label) }));
        // A date nobody touched keeps the exact moment it was saved with.
        // Rewriting it to the end of the day turned an ordinary password change
        // into an extension of access for any record set to another time.
        const was = initial ? getPath(initial, f.name) : "";
        val = was && ymd(was) === val ? was : val + "T23:59:59Z";
      }
      if (f.required && (val == null || val === "" || (Array.isArray(val) && val.length === 0))) {
        return fail(f.name, tf("{label} is required", { label: t(f.label) }));
      }
      if (!f.required && (val === "" || (Array.isArray(val) && val.length === 0))) continue;
      setPath(obj, f.name, val);
    }
    // The switch in the table is not a field here, so it never reaches the
    // payload and the record comes back with it cleared: editing a parked rule
    // used to put it straight back on the air. Carry the saved value over.
    if (initial && cfg.toggle && !cfg.fields.some(f => f.name === cfg.toggle.field)) {
      obj[cfg.toggle.field] = initial[cfg.toggle.field];
    }
    // id is hidden: preserved on edit, auto-generated on create.
    obj.id = initial ? initial.id : genId(obj.name || obj.username || obj.hostname, cfg.singular.replace(/\s+/g, ""));
    const payload = cfg.onSubmit ? cfg.onSubmit(obj) : obj;
    // A rule no single field can judge: what one answer means depends on another
    // (an access date that has passed only matters when the client is switched on).
    const bad = cfg.check ? cfg.check(payload, initial) : null;
    if (bad) return fail(bad.name, bad.msg);
    // A save that has a decision left in it asks before it goes out, and may
    // answer for itself by changing the payload.
    if (cfg.confirmSave && !(await cfg.confirmSave(payload, initial))) return;
    setBusy(true);
    try { await api("POST", cfg.path, payload); onClose(); reload(); notify(t("Saved"), "ok"); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };

  // A door is offered only when it leads somewhere: a screen with no field to
  // show on it (an entry node has no Reality parameters) is not a door.
  const doors = (cfg.screens || []).filter(d =>
    (!d.editOnly || initial) && (!d.createOnly || !initial) &&
    cfg.fields.some(f => screenOf(f) === d.key && (!f.editOnly || initial) && (!f.createOnly || !initial) && (!f.showIf || f.showIf(vals))));
  // The window is as wide as what it shows at once, and a door is one line.
  const mainCount = cfg.fields.filter(f => screenOf(f) === "main" && (!f.editOnly || initial) && (!f.createOnly || !initial)).length;
  // A form that grows when an answer is given, such as the two password fields
  // a "Change password" opens, keeps its rectangle instead and scrolls inside.
  const grows = cfg.fixedHeight || doors.length > 0 || cfg.fields.some(f => f.type === "passtoggle" && (!f.editOnly || initial));
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && ref.close()}>
      <div class=${"modal" + (mainCount > 6 ? " wide" : "") + (grows ? " fixed-h" : "") + (cfg.formClass ? " " + cfg.formClass : "")} data-screen=${screen} ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${(initial ? t("Edit") : t("Add")) + " " + t(cfg.singular)}${initial ? " — " + (initial.name || initial.display_name || initial.username || initial.hostname || initial.id) : ""}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button></header>
        <div class=body>
          ${screen !== "main" && html`<div class=inner-head>
            <button class="ghost sm inner-back" type=button onClick=${backToMain}><span aria-hidden=true>‹</span> ${t("Back")}</button>
            <h4>${t((doors.find(d => d.key === screen) || {}).label || "")}</h4>
          </div>`}
          <div class=mfields>${(() => {
            const shownFields = cfg.fields.filter(f => screenOf(f) === screen && (!f.editOnly || initial) && (!f.createOnly || !initial) && (!f.showIf || f.showIf(vals)));
            let group = null;
            return shownFields.map(f => {
              const head = f.group && f.group !== group ? (group = f.group) : null;
              return html`<${React.Fragment} key=${f.name}>
                ${head && html`<div class="caps fgroup">${t(head)}</div>`}
                <${Field} f=${resolveField(f, vals)}
                  value=${vals[f.name]} onChange=${v => set(f.name, v)} setAll=${set}
                  error=${err && err.name === f.name ? err : null} />
              <//>`;
            });
          })()}</div>
          ${screen !== "main" && (() => {
            const note = (doors.find(d => d.key === screen) || {}).note;
            return note ? html`<div class=note style=${{ borderLeftColor: "var(--accent)" }}>${note(vals, state || {})}</div>` : null;
          })()}
          ${screen === "main" && doors.length > 0 && html`<div class=doors>
            ${doors.map(d => html`<button key=${d.key} id=${"door-" + d.key} class=door type=button onClick=${() => openScreen(d.key)}>
              <span>${t(d.label)}${d.summary && html`<small>${d.summary(vals, state || {})}</small>`}</span><span aria-hidden=true>›</span>
            </button>`)}
          </div>`}
          ${screen === "main" && cfg.summary && (() => {
            const content = cfg.summary(cfg.toggle && initial ? { ...vals, [cfg.toggle.field]: initial[cfg.toggle.field] } : vals, state || {});
            return content ? html`<div class=note style=${{ borderLeftColor: "var(--accent)" }}>${content}</div>` : null;
          })()}
        </div>
        <div class=foot>
          <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
          <button disabled=${busy} onClick=${submit}>${busy ? t("Saving…") : initial ? t("Save") : t("Create")}</button>
        </div>
      </div>
    </div>`;
}

/* ---------------- routing rule form ---------------- */
// Routing is where one generic form stopped being enough. A rule is a sentence,
// these clients catching this go there, and everything else it can say is
// occasional. So the name, the level and the group are a header that does not
// move; the sentence is the first screen; the occasional answers are behind two
// doors that open inside the same window. Nothing that decides anything sits
// below the fold, and the window does not change size as answers appear.
const ALL_GROUPS = "__all"; // a real choice; "" means nothing has been picked yet

// A route is one answer, not an action plus a node: "through fra-out-07", "out
// of the entry node", "refused". These pack that into one select value.
const routeValue = (action, exitID) => (action === "exit" ? "exit:" + (exitID || "") : (action || ""));
const routeExit = (val) => (String(val || "").startsWith("exit:") ? String(val).slice(5) : "");
const routeAction = (val) => (String(val || "").startsWith("exit:") ? "exit" : String(val || ""));

function RouteRuleModal({ cfg, initial, state, onClose, reload, notify }) {
  const R = cfg.route;
  const groups = state.groups || [];
  const groupExitOf = (gid) => { const g = groups.find(x => x.id === gid); return g ? (g.default_exit_id || "") : ""; };
  // A group with no exit is not a group with no answer: its traffic leaves from
  // the entry node, which is what "direct" says.
  const exitAsRoute = (exitID) => (!exitID || exitID === "direct" ? ["direct", ""] : ["exit", exitID]);
  // The exit a group already uses is the answer nine times out of ten. For "all
  // groups" it is only an answer when they agree; when they do not, the form
  // asks rather than quietly taking the first one's.
  const suggestedRoute = (gid) => {
    if (!gid) return "";
    if (gid === ALL_GROUPS) {
      const exits = new Set(groups.map(g => g.default_exit_id || ""));
      return exits.size === 1 ? routeValue(...exitAsRoute([...exits][0])) : "";
    }
    return routeValue(...exitAsRoute(groupExitOf(gid)));
  };
  const groupsDisagree = () => new Set(groups.map(g => g.default_exit_id || "")).size > 1;

  const init = () => {
    const p = initial || {};
    const conds = policyConditions(p).map(c => ({ join: c.join, kind: c.kind, values: (c.values || []).slice() }));
    return {
      name: p.name || "",
      level: R.ownsInfra ? (initial ? R.levelOf(p.tier) : "") : "namespace",
      group: initial ? (p.applies_to_group_id || ALL_GROUPS) : "",
      conditions: conds.length ? conds : [{ kind: "geosite", values: [] }],
      route: initial ? routeValue(p.action, p.exit_node_id) : "",
      // Reserving a rule is a decision of its own, and so is what everyone else
      // then gets: switching it on does not answer the second question.
      exclusive: !!p.others_action,
      others: p.others_action ? routeValue(p.others_action, p.others_exit_id) : "",
      fallback: p.fallback_kind ? routeValue(p.fallback_kind, p.fallback_exit_id) : "",
      priority: p.priority == null ? 100 : p.priority,
      disabled: !!p.disabled,
    };
  };
  const [v, setV] = useState(init);
  const [screen, setScreen] = useState("main");
  const [err, setErr] = useState(null);   // { name, msg, index }
  const [busy, setBusy] = useState(false);
  // An existing rule's route is its own; a new one follows the group until the
  // operator picks something else.
  const [routeTouched, setRouteTouched] = useState(!!initial);
  const [renaming, setRenaming] = useState(false);
  const [draftName, setDraftName] = useState("");
  // What is half-typed in a search, and what was filled in for a kind before
  // another one was looked at. Both live here rather than inside the field, so
  // walking into the conditions screen and back does not throw them away.
  const [drafts, setDrafts] = useState({});
  const [stash, setStash] = useState({});
  const [cdraft, setCdraft] = useState(null); // the conditions screen's working copy
  const [active, setActive] = useState(0);    // the condition being edited there
  const opener = useRef(null);                // the button a screen was opened from

  // A rule is named after what it catches and who it is for, so nothing has to
  // be invented before anything is known. Renaming overrides it for good.
  const autoName = () => {
    const rows = condRows(v.conditions);
    const vals = rows.length ? (rows[0].values || []).filter(x => String(x).trim() !== "") : [];
    if (!vals.length) return "";
    const head = vals.slice(0, 2).join(", ") + (vals.length > 2 ? "…" : "");
    const grp = v.group && v.group !== ALL_GROUPS ? nameOf(state, "groups", v.group) : "";
    return grp ? head + " · " + grp : head;
  };
  const effName = () => String(v.name || "").trim() || autoName();

  const conditionsBefore = useRef({});
  const ref = useModal(onClose, () => JSON.stringify(v) !== JSON.stringify(init())
    || Object.values(drafts).some(x => String(x).trim())
    || (cdraft && JSON.stringify(cdraft) !== JSON.stringify(v.conditions)));
  // Escape while renaming cancels the rename and nothing else. Leaving the box
  // by any other route keeps what was typed, so the flag says which of the two
  // closed it: the box is gone by the time the blur would be read.
  const dropRename = useRef(false);
  useEscape(() => { if (!renaming) return false; dropRename.current = true; setRenaming(false); return true; });
  const endRename = (keep) => {
    if (dropRename.current) { dropRename.current = false; setRenaming(false); return; }
    if (keep) set("name", draftName.trim());
    setRenaming(false);
  };

  const fail = (name, msg, index) => {
    setErr({ name, msg, index });
    if (name === "others" || name === "fallback" || name === "priority") setScreen("advanced");
    else if (name === "conditions" && index > 0) openConditions(index);
    else if (screen !== "main") setScreen("main");
    setTimeout(() => {
      const el = document.getElementById(index == null ? "rr-" + name : "cond-" + index);
      if (el) el.focus();
    }, 40);
  };
  const set = (k, val) => setV(s => {
    if (err && err.name === k) setErr(null);
    const n = { ...s, [k]: val };
    if (k === "group") {
      if (!routeTouched) n.route = suggestedRoute(n.group);
      // A reservation answers for everyone outside one group; with no group
      // named there is nobody it could be reserved from.
      if (!n.group || n.group === ALL_GROUPS) { n.exclusive = false; n.others = ""; }
    }
    // Whatever the route became, chosen by hand or filled in from the group,
    // the exit it names is not an alternative to itself. Checking this in one
    // place is what keeps the automatic fill from building a request the server
    // has to refuse.
    if (k === "group" || k === "route") {
      const ex = routeExit(n.route);
      if (ex && routeExit(n.others) === ex) n.others = "";
      if (ex && routeExit(n.fallback) === ex) n.fallback = "";
    }
    if (k === "exclusive" && !val) n.others = "";
    return n;
  });

  const namesExit = routeAction(v.route) === "exit" || routeAction(v.others) === "exit";
  const exitName = (id) => (id ? nameOf(state, "nodes", id) : "");
  // One question, one field: a route is an answer ("through fra-out-07"), not an
  // action plus a node. The same list answers for everyone else and for the
  // exit going down, minus the exit this rule already names.
  const routeOpts = (opts) => {
    const o = opts || {};
    const suggested = v.group ? suggestedRoute(v.group) : "";
    const out = R.exitOpts.filter(x => !o.not || x.value !== o.not).map(x => ({
      value: "exit:" + x.value,
      label: !o.plain && "exit:" + x.value === suggested
        ? tf("Through {name} — the group's own exit", { name: x.label })
        : tf("Through {name}", { name: x.label }),
    }));
    out.push({ value: "direct", label: o.plain ? t("Out of the entry node itself") : t("Out of the entry node it is already on"), disabled: !o.plain && v.exclusive });
    out.push({ value: "block", label: t("Blocked"), disabled: !o.plain && v.exclusive });
    return out;
  };

  // What a rule does while the exit it names is out of rotation. "Out of the
  // entry node" is stored as an empty kind, so it is an option of its own rather
  // than a blank the browser fills with whichever node happens to be first.
  const fallbackOpts = () => [{ value: "", label: t("Out of the entry node itself") }]
    .concat(R.exitOpts.filter(x => x.value !== routeExit(v.route)).map(x => ({ value: "exit:" + x.value, label: tf("Through {name}", { name: x.label }) })))
    .concat([{ value: "block", label: t("Blocked") }]);

  // A draft rule, in the shape the summary and the ordering comparator read.
  const asPolicy = () => ({
    id: initial ? initial.id : "",
    name: effName(), level: v.level, group_pending: !v.group, exclusive: v.exclusive,
    tier: R.effTier({ level: v.level, action: routeAction(v.route) }),
    applies_to_group_id: v.group === ALL_GROUPS ? "" : v.group,
    conditions: v.conditions,
    action: routeAction(v.route),
    exit_node_id: routeExit(v.route),
    others_action: v.exclusive ? routeAction(v.others) : "",
    others_exit_id: v.exclusive ? routeExit(v.others) : "",
    fallback_kind: routeAction(v.fallback), fallback_exit_id: routeExit(v.fallback),
    from_group_exit: !routeTouched && !!routeExit(v.route),
    priority: Number(v.priority) || 0,
    disabled: v.disabled,
  });

  // Coming back has to land on the button that opened the screen. That button is
  // a different element by then, the main screen having been unmounted and built
  // again, so what is remembered is its name, and it is found after the screen is
  // back on the page.
  const openSection = (name, from) => { opener.current = from; setScreen(name); };
  React.useLayoutEffect(() => {
    if (ref.current) ref.current.querySelector(".body").scrollTop = 0;
    const target = screen === "conditions" ? document.getElementById("cond-" + active)
      : screen !== "main" ? ref.current?.querySelector(".inner-back") : opener.current && document.getElementById(opener.current);
    if (target) target.focus({ preventScroll: true });
  }, [screen, active]);
  useEscape(() => {
    if (renaming || screen === "main" || ref.current?.querySelector(".tag-opts")) return false;
    if (screen === "conditions") leaveConditions(); else back();
    return true;
  });
  const back = () => {
    setScreen("main");
  };
  const openConditions = (i, from) => {
    opener.current = from;
    conditionsBefore.current = { ...drafts };
    setCdraft(v.conditions.map(c => ({ ...c, values: (c.values || []).slice() })));
    setActive(i == null ? 0 : i);
    setScreen("conditions");
  };
  const addCondition = (join, from) => {
    opener.current = from;
    conditionsBefore.current = { ...drafts };
    const rows = v.conditions.map(c => ({ ...c, values: (c.values || []).slice() }));
    rows.push({ join, kind: join === "except" ? "domain" : rows[0].kind === "geosite" ? "geoip" : "geosite", values: [] });
    setCdraft(rows);
    setActive(rows.length - 1);
    setScreen("conditions");
  };
  const applyConditions = () => {
    const pending = cdraft.findIndex((c, i) => String(drafts[i] || "").trim());
    const blank = cdraft.findIndex(c => !c.values.length);
    const index = pending >= 0 ? pending : blank;
    if (index >= 0) {
      setErr({ name: "conditions", index, msg: t(pending >= 0 ? "Press Enter to add what is typed, or clear the search" : "Fill this condition in or remove it") });
      setActive(index);
      setTimeout(() => document.getElementById("cond-" + index)?.focus(), 0);
      return;
    }
    setErr(null); set("conditions", cdraft); setCdraft(null); back();
  };
  const leaveConditions = async () => {
    if (JSON.stringify(cdraft) !== JSON.stringify(v.conditions) || JSON.stringify(drafts) !== JSON.stringify(conditionsBefore.current)) {
      const yes = await uiConfirm({ variant: "compact",
        title: t("Cancel changes to conditions?"),
        lines: [t("Changes in this section will be cancelled. Other settings will be kept.")],
        confirmLabel: t("Cancel changes"),
      });
      if (!yes) return;
    }
    setCdraft(null); setDrafts(conditionsBefore.current); setErr(null); back();
  };

  const submit = async () => {
    if (R.ownsInfra && !v.level) return fail("level", t("Say which level this rule is written at"));
    if (!v.group) return fail("group", t("Say who this rule is for"));
    const rows = v.conditions;
    // Something is still in a search box. It is not a value until it is added,
    // and saving around it would drop what the operator thinks they typed, so
    // it is asked about before the emptier "there is nothing here" would be.
    const pending = rows.findIndex((r, i) => String(drafts[i] || "").trim() !== "");
    if (pending >= 0) return fail("conditions", t("Press Enter to add what is typed, or clear the search"), pending);
    const blank = rows.findIndex(r => (r.values || []).filter(x => String(x).trim() !== "").length === 0);
    if (blank >= 0) return fail("conditions", rows.length > 1 ? t("Fill this condition in or remove it") : t("Add at least one condition"), blank);
    if (rows[0].join === "except") return fail("conditions", t("The first condition cannot be an exclusion"), 0);
    if (!v.route) return fail("route", t("Say where this traffic goes"));
    if (v.exclusive && !v.others) return fail("others", t("Say what everyone else gets"));
    if (v.exclusive && routeAction(v.others) === "exit" && !routeExit(v.others)) return fail("others", t("Pick the exit everyone else takes"));
    if (routeAction(v.fallback) === "exit" && !routeExit(v.fallback)) return fail("fallback", t("Pick the exit to fall back to"));
    if (!/^-?\d+$/.test(String(v.priority).trim())) return fail("priority", t("A whole number, please"));
    const name = effName();

    const action = routeAction(v.route);
    const conds = rows.map(r => ({
      join: r.join || "and", kind: r.kind, values: (r.values || []).filter(x => String(x).trim() !== ""),
    }));
    const payload = {
      id: initial ? initial.id : genId(name, "rule"),
      name,
      tier: R.effTier({ level: v.level, action }),
      applies_to_group_id: v.group === ALL_GROUPS ? "" : v.group,
      conditions: conds,
      action,
      exit_node_id: routeExit(v.route),
      others_action: v.exclusive ? routeAction(v.others) : "",
      others_exit_id: v.exclusive ? routeExit(v.others) : "",
      fallback_kind: namesExit ? routeAction(v.fallback) : "",
      fallback_exit_id: namesExit ? routeExit(v.fallback) : "",
      priority: Number(v.priority) || 0,
      disabled: !!v.disabled,
    };
    setBusy(true);
    try { await api("POST", cfg.path, payload); onClose(); reload(); notify(t("Saved"), "ok"); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };

  // ---- pieces of the form
  const errFor = (name) => (err && err.name === name ? err : null);
  // A select is "empty" only when nothing has been picked. A real answer that
  // happens to be stored as "" (a fallback of "out of the entry node") is a
  // filled field and must not be dressed as a prompt.
  const sel = (name, value, opts, onChange, o) => {
    const bad = errFor(name);
    const cfgo = o || {};
    const empty = !cfgo.optional && (value === "" || value == null);
    return html`<div class=${"field" + (cfgo.inline ? " rr-setting" : "") + (bad ? " bad" : "")}>
      ${cfgo.label && html`<label for=${"rr-" + name}>${cfgo.label}${cfgo.required ? html`<span class=required-mark aria-hidden=true>*</span>` : ""}</label>`}
      <select id=${"rr-" + name} class=${empty ? "sel-empty" : ""} value=${value == null ? "" : value} aria-required=${cfgo.required || undefined}
        aria-invalid=${bad ? "true" : undefined} aria-describedby=${bad ? "rr-" + name + "-err" : undefined}
        data-autofocus=${name === "level" && !v.level ? true : undefined}
        onChange=${e => onChange(e.target.value)}>
        ${cfgo.placeholder !== undefined && html`<option value="" disabled>${cfgo.placeholder}</option>`}
        ${opts.map(x => html`<option key=${x.value} value=${x.value} disabled=${!!x.disabled}>${x.label}</option>`)}
      </select>
      ${cfgo.under}
      ${bad && html`<div class=fielderr id=${"rr-" + name + "-err"} role=alert>${bad.msg}</div>`}
    </div>`;
  };

  // Level and group stay above every screen: they decide what the rest of the
  // form means, and the answer has to be visible while it is being given.
  const context = html`
    <div class=rr-head>
      ${R.ownsInfra ? sel("level", v.level, R.levelOpts, x => set("level", x),
          { label: LANG === "ru" ? html`Уровень<span class=context-label-extra> правила</span>` : t("Rule level"), required: true, placeholder: t("Choose a level") }) : ""}
      ${sel("group", v.group, [{ value: ALL_GROUPS, label: t(v.level === "infra" ? "All network groups" : "All my groups"), disabled: v.exclusive }, ...R.groupOpts], x => set("group", x),
          { label: t("Group"), required: true, placeholder: t("Choose a group") })}
    </div>`;

  // One condition: the kind names the field, the field is the width of the form.
  const kindSelect = (i, c, onKind) => html`<select class=cond-kind id=${"cond-kind-" + i}
    aria-label=${t("What this line looks at")} value=${c.kind} onChange=${e => onKind(e.target.value)}>
    ${COND_KINDS.map(([val, lab]) => html`<option key=${val} value=${val}>${t(lab)}</option>`)}
  </select>`;
  const valueField = (i, c, onValues, bad) => html`<${TagInput}
    value=${c.values || []} onChange=${onValues}
    draft=${drafts[i] || ""} onDraft=${x => setDrafts(s => ({ ...s, [i]: x }))}
    options=${condCatalog(c.kind)} transform=${c.kind === "geoip" ? countryCode : undefined}
    free=${condIsFree(c.kind)} placeholder=${condHint(c.kind)} floating
    inputId=${"cond-" + i} required=${true} describedBy=${bad ? "rr-conditions-err" : undefined} invalid=${!!bad} autofocus=${i === 0 && (!R.ownsInfra || !!v.level)}
    ariaLabel=${tf("{kind}: add a value", { kind: condTitle(c.kind) })} />`;
  // Switching what a line looks at keeps what was filled in for the old kind:
  // one look at the country list should not cost a list of domains.
  const switchKind = (rows, i, kind, write) => {
    const from = rows[i];
    setStash(s => ({ ...s, [i + ":" + from.kind]: (from.values || []).slice() }));
    setDrafts(s => ({ ...s, [i]: "" }));
    write(rows.map((r, j) => (j === i ? { ...r, kind, values: (stash[i + ":" + kind] || []).slice() } : r)));
  };

  const condErr = errFor("conditions");
  const mainScreen = html`
    <div class=body>
      <section class=rr-res>
        <div class=rr-restop>
          ${kindSelect(0, v.conditions[0], k => switchKind(v.conditions, 0, k, x => set("conditions", x)))}
          <span class=required-mark aria-hidden=true>*</span>
        </div>
        ${valueField(0, v.conditions[0], x => set("conditions", v.conditions.map((r, j) => (j === 0 ? { ...r, values: x } : r))),
          condErr && condErr.index === 0)}
        ${condErr && condErr.index === 0 && html`<div class=fielderr id=rr-conditions-err role=alert>${condErr.msg}</div>`}
        ${v.conditions.slice(1).map((c, i) => html`<div class=rr-more key=${i + 1}>
          <span class=rr-join>${joinWord(c.join)}</span>
          <button type=button class=rr-sum id=${"rr-open-" + (i + 1)} onClick=${() => openConditions(i + 1, "rr-open-" + (i + 1))}>${condShort(c)} ↗</button>
        </div>`)}
        ${condErr && condErr.index > 0 && html`<div class=fielderr role=alert>${condErr.msg}</div>`}
        <div class=rr-links>
          <button type=button class="linkish sm" id=rr-add-cond onClick=${() => addCondition("and", "rr-add-cond")}>+ ${t("condition")}</button>
          <button type=button class="linkish sm" id=rr-add-exc onClick=${() => addCondition("except", "rr-add-exc")}>+ ${t("exclusion")}</button>
        </div>
      </section>
      ${sel("route", v.route, routeOpts(), x => { setRouteTouched(true); set("route", x); },
          {
            label: t("Route"), required: true, placeholder: t(!v.group ? "Choose a group first" : v.group === ALL_GROUPS ? "Choose a shared route" : "Choose a route"),
            under: html`
              <div class=rr-origin>
                ${!v.group ? t("Pick a group above and its own exit is filled in here.")
                  : v.group === ALL_GROUPS && groupsDisagree() && !v.route
                  ? t("Your groups leave by different exits, so there is none to fill in — name the one this rule uses.")
                  : suggestedRoute(v.group) && v.route && v.route !== suggestedRoute(v.group)
                  ? html`<button class="linkish sm" type=button onClick=${() => { setRouteTouched(false); set("route", suggestedRoute(v.group)); }}>
                      ${tf("Back to the group's own exit: {name}", { name: exitName(routeExit(suggestedRoute(v.group))) || t("out of the entry node") })}
                    </button>`
                  : ""}
              </div>
              ${v.route === "direct" && html`<p class=rr-route-minor>${t("The VPN connection stays active. The site sees the entry server's IP address.")}</p>`}`,
          })}
      <div class=doors>
        <button class=door id=rr-door-advanced type=button onClick=${() => openSection("advanced", "rr-door-advanced")}>
          <span>
            ${t("More settings")}
            ${(v.exclusive || v.fallback || v.disabled) && html`<small>${[
              v.exclusive ? t("exclusive") : "",
              v.fallback ? t("has a fallback") : "",
              v.disabled ? t("off") : "",
            ].filter(Boolean).join(" · ")}</small>`}
          </span><span aria-hidden=true>›</span>
        </button>
        <button class=door id=rr-door-explain type=button onClick=${() => openSection("explain", "rr-door-explain")}>
          <span>${t("What this rule does")}</span><span aria-hidden=true>›</span>
        </button>
      </div>
      ${v.exclusive && !v.others && v.level && v.group
        ? html`<div class="rr-note rr-incomplete">${t("Choose how to handle requests from other groups.")} <button class=linkish type=button onClick=${() => openSection("advanced", "rr-door-advanced")}>${t("Choose action for others")}</button></div>`
        : cfg.summary && html`<div class=rr-note>${cfg.summary(asPolicy(), state, "short")}</div>`}
    </div>`;

  const section = (title, inner, onBack) => html`
    <div class=body>
      <div class=inner-head>
        <button class="ghost inner-back" type=button onClick=${onBack || back}>
          <svg width=12 height=12 viewBox="0 0 12 12" fill=none aria-hidden=true><path d="M7.5 2.5 4 6l3.5 3.5" stroke=currentColor stroke-width=1.5 stroke-linecap=round stroke-linejoin=round /></svg>
          <span>${t("Back")}</span>
        </button>
        <h4>${title}</h4>
      </div>
      ${inner}
    </div>`;

  const conditionsScreen = () => {
    const rows = cdraft || [];
    const write = (next) => { setCdraft(next); setErr(null); };
    return section(t("Rule conditions"), html`
      <p class=hint>${t("Lines are read top to bottom: each one joins to everything above it.")}</p>
      <div class=cond-cards>
        ${rows.map((c, i) => html`<section class=${"cond-card" + (i === active ? " on" : "")} key=${i}>
          <div class=cond-head>
            ${i === 0
              ? html`<span class=cond-first>${t("Resources")}</span>`
              : html`<select class=cond-join value=${c.join || "and"}
                  aria-label=${tf("how line {n} joins the ones above it", { n: i + 1 })}
                  onChange=${e => write(rows.map((r, j) => (j === i ? { ...r, join: e.target.value } : r)))}>
                  ${COND_JOINS.map(([val, lab]) => html`<option key=${val} value=${val}>${t(lab)}</option>`)}
                </select>`}
            <button type=button class=cond-sum onClick=${() => setActive(i)} aria-expanded=${i === active ? "true" : "false"}>${condShort(c)}</button>
            <button type=button class="ghost sm" aria-label=${tf("remove line {n}", { n: i + 1 })} title=${t("Remove")}
              disabled=${rows.length < 2} onClick=${() => {
                const left = rows.filter((_, j) => j !== i);
                if (left.length && left[0].join === "except") left[0] = { ...left[0], join: "and" };
                setDrafts(d => Object.fromEntries(Object.entries(d).filter(([k]) => Number(k) !== i).map(([k, value]) => [Number(k) > i ? Number(k) - 1 : k, value])));
                setStash(old => Object.fromEntries(Object.entries(old).filter(([k]) => Number(k.split(":")[0]) !== i).map(([k, value]) => {
                  const [index, kind] = k.split(":"); return [(Number(index) > i ? Number(index) - 1 : index) + ":" + kind, value];
                })));
                write(left); setActive(0);
              }}>×</button>
          </div>
          ${i === active && html`<div class=cond-body>
            <div class=rr-restop>${kindSelect(i, c, k => switchKind(rows, i, k, write))}</div>
            ${valueField(i, c, x => write(rows.map((r, j) => (j === i ? { ...r, values: x } : r))), condErr && condErr.index === i)}
            ${condErr && condErr.index === i && html`<div class=fielderr id=rr-conditions-err role=alert>${condErr.msg}</div>`}
          </div>`}
        </section>`)}
      </div>
      <div class=rr-links>
        <button type=button class="linkish sm" onClick=${() => { write([...rows, { join: "and", kind: rows[0].kind === "geosite" ? "geoip" : "geosite", values: [] }]); setActive(rows.length); }}>+ ${t("condition")}</button>
        <button type=button class="linkish sm" onClick=${() => { write([...rows, { join: "except", kind: "domain", values: [] }]); setActive(rows.length); }}>+ ${t("exclusion")}</button>
      </div>
      <p class=cond-formula><small>${t("The whole condition")}</small><span>${condFormula(rows)}</span></p>`,
      leaveConditions);
  };

  const exclusiveReason = !v.level ? { field: "level", text: t("Choose the rule level."), label: t("Choose level") }
    : !v.group || v.group === ALL_GROUPS ? { field: "group", text: t("Choose one group for an exclusive rule."), label: t("Pick a group") }
    : !routeExit(v.route) ? { field: "route", text: t(v.route === "direct" ? "This group uses a direct connection. Choose an exit server to enable an exclusive rule." : "Choose an exit node for this group."), label: t("Choose node") }
    : null;
  const advanced = section(t("More settings"), html`
    <div class=rr-rows>
      <div class=rr-excl>
        <div class=form-check>
          <label for=rr-exclusive><input id=rr-exclusive type=checkbox checked=${v.exclusive} disabled=${!!exclusiveReason && !v.exclusive}
            onChange=${e => set("exclusive", e.target.checked)} /><span>${t("Exclusive rule")}</span></label>
        </div>
        ${exclusiveReason && html`<div class=hint>${exclusiveReason.text} <button class="linkish sm" type=button onClick=${() => {
            if (exclusiveReason.field === "route") setScreen("main");
            setTimeout(() => document.getElementById("rr-" + exclusiveReason.field)?.focus(), 30);
          }}>${exclusiveReason.label}</button></div>`}
        ${v.exclusive && sel("others", v.others, routeOpts({ not: routeExit(v.route), plain: true }), x => set("others", x),
          { label: t(v.level === "infra" ? "For other network groups" : "For my other groups"), required: true,
            placeholder: t("Choose an action for others"), wide: true })}
      </div>
      ${namesExit && sel("fallback", v.fallback, fallbackOpts(), x => set("fallback", x),
        { optional: true, inline: true,
          label: routeExit(v.route) ? tf("If {name} is unavailable", { name: exitName(routeExit(v.route)) }) : t("If that exit is unavailable") })}
      <div class="rr-row rr-priority-row">
        <label for=rr-priority>${t("Priority")}</label>
        <input id=rr-priority type=number step=1 class=${"rr-num" + (errFor("priority") ? " bad" : "")}
          aria-invalid=${errFor("priority") ? "true" : undefined}
          value=${v.priority} onInput=${e => set("priority", e.target.value)} />
      </div>
      ${errFor("priority") && html`<div class=fielderr role=alert>${errFor("priority").msg}</div>`}
      <div class="form-check rr-status">
        <label for=rr-enabled><input id=rr-enabled type=checkbox checked=${!v.disabled} onChange=${e => set("disabled", !e.target.checked)} /><span>${t("Rule is on")}</span></label>
      </div>
    </div>`);

  const explain = section(t("What this rule does"), html`
    <div class=rr-explain>${cfg.summary ? cfg.summary(asPolicy(), state) : ""}</div>`);

  const shown = v.name.trim();
  return html`
    <div class="overlay rr-overlay" onClick=${e => e.target.classList.contains("overlay") && ref.close()}>
      <div class="modal rr-modal" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header class=rr-header>
          <div class=rr-heading>
          <h3 id=modal-title class=rr-title>
            ${renaming
              ? html`<input id=rr-name class=rr-name value=${draftName} maxLength=100 autoFocus
                  aria-label=${t("Rule name")} placeholder=${t("Rule name")}
                  onInput=${e => setDraftName(e.target.value)}
                  onBlur=${() => endRename(true)}
                  onKeyDown=${e => { if (e.key === "Enter") { e.preventDefault(); endRename(true); } }} />`
              : html`<button type=button class=${"rr-titlebtn" + (!shown ? " rr-title-default" : "")} title=${t("Rename")}
                  onClick=${() => { dropRename.current = false; setDraftName(shown); setRenaming(true); }}>
                  <span>${shown || t(initial ? "Edit rule" : "New rule")}</span>
                  <span class=rr-pencil aria-hidden=true>✎</span>
                  <span class=sr-only>${t("Rename")}</span>
                </button>`}
          </h3>
          <button class="ghost rr-close" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>×</button>
          </div>
          ${context}
        </header>
        ${screen === "main" ? mainScreen
          : screen === "conditions" ? conditionsScreen()
          : screen === "advanced" ? advanced : explain}
        <div class=foot>
          <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
          ${screen === "conditions"
            ? html`<button onClick=${applyConditions}>${t("Apply")}</button>`
            : html`<button disabled=${busy} onClick=${submit}>${busy ? t("Saving…") : initial ? t("Save") : t("Create")}</button>`}
        </div>
      </div>
    </div>`;
}

/* ---------------- node VPS limits modal ---------------- */
function todayISO() { return new Date().toISOString().slice(0, 10); }
// Dates travel as ISO yyyy-mm-dd, which is what a date input holds; they are
// shown back as dd/mm/yyyy wherever they are text rather than a field.
function ymd(s) { return String(s || "").slice(0, 10); }
function fmtDMY(s) { if (!s) return ""; const p = ymd(s).split("-"); return p.length === 3 ? p[2] + "/" + p[1] + "/" + p[0] : String(s); }
function isYMD(s) { return /^\d{4}-\d{2}-\d{2}$/.test(String(s || "").trim()) && !isNaN(Date.parse(s + "T00:00:00Z")); }
// Whole days from today to a date, by calendar day in UTC: one rule everywhere
// so "12 days left" never depends on the hour it is read at.
function daysUntil(dateYMD) {
  if (!isYMD(dateYMD)) return null;
  return Math.round((Date.parse(dateYMD + "T00:00:00Z") - Date.parse(todayISO() + "T00:00:00Z")) / 86400000);
}
// Reads a payment date back as a fact: how long is left, or how long it is
// overdue. Undated billing gets no countdown rather than a made-up one.
function paidUntilNote(dateYMD) {
  const d = daysUntil(dateYMD);
  if (d == null) return "";
  if (d > 0) return tf("{n} left", { n: plural(d, "day") });
  if (d === 0) return t("paid through today");
  return tf("overdue by {n}", { n: plural(-d, "day") });
}
// A user is expired (no working config) once now is past their expires_at, mirroring
// model.User.Expired server-side; here it only drives the table badge.
function isExpired(v) { return !!v && new Date(v).getTime() <= Date.now(); }
// Access moved forward: the day the client may connect through is later than it
// was, and it reaches into the future. A date pulled back, or one that lands in
// the past either way, extends nothing and is nothing to ask about.
function accessExtended(was, now) {
  if (now && new Date(now).getTime() <= Date.now()) return false;
  if (!was) return false; // it had no end date to begin with
  return !now || new Date(now).getTime() > new Date(was).getTime();
}
// Adding months keeps the day of the month, or the last day of the month it
// lands in when that one is shorter: 31 January plus a month is 28 February.
// Rolling over into March instead is what made reopening this window move the
// payment date, because the way back out was not the way in.
function addMonthsISO(fromYMD, term) {
  const parts = ymd(fromYMD || todayISO()).split("-").map(Number);
  const first = new Date(Date.UTC(parts[0], parts[1] - 1 + Number(term || 0), 1));
  const last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate();
  first.setUTCDate(Math.min(parts[2], last));
  return first.toISOString().slice(0, 10);
}

function LimitsModal({ node, onClose, reload, notify }) {
  const lim = node.limits || {};
  const bill = node.billing || {};
  const [cpu, setCpu] = useState(lim.cpu_cores || "");
  const [mem, setMem] = useState(lim.memory_mb ? lim.memory_mb / 1024 : "");   // shown in GB
  const [disk, setDisk] = useState(lim.disk_gb || "");
  const [traffic, setTraffic] = useState(lim.traffic_gb ? lim.traffic_gb / 1024 : ""); // shown in TB
  const [term, setTerm] = useState(bill.term_months || 0);
  // What is stored is the date the node is paid through. It is shown back as
  // it was saved and nothing recomputes it on open: deriving a start date and
  // adding the term back on moved the payment date every time this window was
  // opened to change a memory figure, because month arithmetic does not undo.
  const savedUntil = ymd(bill.paid_until);
  const [until, setUntil] = useState(savedUntil);
  const [busy, setBusy] = useState(false);
  const [dateErr, setDateErr] = useState("");
  const num = (v) => v === "" ? 0 : Number(v) || 0;
  // Only an edit to the payment fields is a payment edit. Resources and billing
  // share one window; they must not share one decision.
  const billingTouched = Number(term) !== Number(bill.term_months || 0) || until !== savedUntil;
  const note = term > 0 ? paidUntilNote(until) : "";
  const ref = useModal(onClose, () => billingTouched
    || num(cpu) !== (lim.cpu_cores || 0) || Math.round(num(mem) * 1024) !== (lim.memory_mb || 0)
    || num(disk) !== (lim.disk_gb || 0) || Math.round(num(traffic) * 1024) !== (lim.traffic_gb || 0));

  const save = async () => {
    // A refused save marks the field it was refused for and puts the cursor in
    // it; a message in the corner left the empty date looking fine.
    if (term > 0 && !isYMD(until)) {
      setDateErr(t("Enter the date this node is paid through"));
      setTimeout(() => { const el = document.getElementById("lim-until"); if (el) el.focus(); }, 0);
      return;
    }
    setDateErr("");
    setBusy(true);
    try {
      const id = encodeURIComponent(node.id);
      await api("POST", "/api/nodes/" + id + "/limits",
        { cpu_cores: num(cpu), memory_mb: Math.round(num(mem) * 1024), disk_gb: num(disk), traffic_gb: Math.round(num(traffic) * 1024) });
      if (billingTouched) {
        await api("POST", "/api/nodes/" + id + "/billing",
          term > 0 ? { paid_until: until + "T00:00:00Z", term_months: Number(term) } : {});
      }
      onClose(); reload(); notify(t("Saved"), "ok");
    } catch (e) { notify(e.message, "err"); setBusy(false); }
  };
  const field = (id, label, val, set, hint) => html`
    <div class=field>
      <label for=${id}>${label}</label>
      <input id=${id} type=number min=0 step=any value=${val} onChange=${e => set(e.target.value)}
        placeholder=${t("unset")} aria-describedby=${hint ? id + "-hint" : undefined} />
      ${hint && html`<span class=hint id=${id + "-hint"}>${hint}</span>`}
    </div>`;
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && ref.close()}>
      <div class=modal ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${t("Resources & payment")} — ${node.name || node.id}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button></header>
        <div class=body>
          <div class=mfields>
            ${field("lim-cpu", t("CPU cores (vCPU)"), cpu, setCpu)}
            ${field("lim-mem", t("Memory (GB)"), mem, setMem)}
            ${field("lim-disk", t("Disk (GB)"), disk, setDisk)}
            ${field("lim-traffic", t("Monthly traffic (TB)"), traffic, setTraffic)}
          </div>
          <p class=hint style=${{ marginTop: ".6rem" }}>${t("The plan's figures, for reference. No limit is applied to the server.")}</p>
          <h4 style=${{ margin: "1.4rem 0 .8rem" }}>${t("Billing")}</h4>
          <div class=mfields>
            <div class=field>
              <label for=lim-term>${t("Payment term")}</label>
              <select id=lim-term value=${term} onChange=${e => setTerm(Number(e.target.value))}>
                <option value=0>${t("— no billing —")}</option>
                <option value=1>${t("1 month")}</option>
                <option value=3>${t("3 months")}</option>
                <option value=6>${t("6 months")}</option>
                <option value=12>${t("12 months")}</option>
              </select>
            </div>
            ${term > 0 && html`
              <div class=${"field" + (dateErr ? " bad" : "")}>
                <label for=lim-until>${t("Paid through")}</label>
                <input id=lim-until type=date value=${until} onInput=${e => { setUntil(e.target.value); setDateErr(""); }}
                  aria-invalid=${dateErr ? "true" : undefined}
                  aria-describedby=${dateErr ? "lim-until-err" : "lim-until-note"} />
                ${dateErr
                  ? html`<div class=fielderr id=lim-until-err role=alert>${dateErr}</div>`
                  : html`<span class=hint id=lim-until-note>${note}</span>`}
              </div>`}
          </div>
          ${term > 0 && html`
            <button class="ghost sm" onClick=${() => setUntil(addMonthsISO(until || todayISO(), term))}>
              ${tf("Extend by {n}", { n: plural(Number(term), "month") })}
            </button>
            ${billingTouched && savedUntil && html`<span class=hint style=${{ marginLeft: ".6rem" }}>${tf("was {date}", { date: fmtDMY(savedUntil) })}</span>`}`}
        </div>
        <div class=foot>
          <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
          <button disabled=${busy} onClick=${save}>${busy ? t("Saving…") : t("Save")}</button>
        </div>
      </div>
    </div>`;
}

/* ---------------- route tester ---------------- */
// The trace comes back tagged with the level each rule was read at. Those tags
// are how the rules are stored, not how the panel talks about them: the same
// words the rule form uses go on screen.
const STAGE_LABEL = {
  fleet: "network rule", exit: "scope rule",
  "group-default": "group default exit",
};
function stageLabel(s) { return t(STAGE_LABEL[s] || s); }


function RouteTester({ state, onClose, notify }) {
  const groups = state.groups || [];
  const [groupId, setGroupId] = useState(groups.some(g => g.id === "default") ? "default" : (groups[0] ? groups[0].id : ""));
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState(null);
  const ref = useModal(onClose);
  const run = async () => {
    if (!target.trim()) { notify(t("Enter an IP or domain to test"), "err"); return; }
    setBusy(true); setRes(null);
    try { setRes(await api("POST", "/api/route-test", { group_id: groupId, target: target.trim() })); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };
  const cls = (d) => d === "block" ? "err" : d === "direct" ? "warn" : "ok";
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class="modal rt-modal" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${t("Route tester")}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          <div class=mfields>
            <${LF} label=${t("Group")}>
              <select value=${groupId} onChange=${e => setGroupId(e.target.value)}>
                ${groups.map(g => html`<option key=${g.id} value=${g.id}>${g.name || g.id}</option>`)}
              </select><//>
            <${LF} label=${t("Host to test (IP or domain)")}>
              <input value=${target} placeholder="${t("e.g.")} ya.ru, 8.8.8.8"
                onInput=${e => setTarget(e.target.value)} onKeyDown=${e => { if (e.key === "Enter") run(); }} /><//>
          </div>
          ${res && html`<div style=${{ marginTop: "1rem" }}>
            <div class=between>
              <strong>${t("Goes out via")}</strong><span class=${cls(res.decision)} style=${{ fontWeight: 800, textTransform: "uppercase" }}>${t(res.decision)}</span>
            </div>
            <p style=${{ margin: ".3rem 0" }}>${ph(res.egress)}</p>
            <p class=muted style=${{ margin: ".2rem 0" }}>${t("Decided by")}: <strong>${ph(res.decided_by)}</strong> — ${ph(res.reason)}</p>
            ${res.resolved_ips && res.resolved_ips.length > 0 && html`<p class=muted style=${{ margin: ".2rem 0" }}>${t("Resolved to")}: ${res.resolved_ips.join(", ")}</p>`}
            ${(res.warnings || []).map((w, i) => html`<p key=${i} class=warn style=${{ margin: ".2rem 0", fontSize: "12px" }}>⚠ ${ph(w)}</p>`)}
            <h4 style=${{ margin: ".7rem 0 .3rem" }}>${t("How it was decided (top to bottom, first match wins)")}</h4>
            <div class=rt-trace><table><tbody>
              ${(res.trace || []).map((s, i) => html`<tr key=${i}>
                <td><span class=muted>${stageLabel(s.stage)}</span></td>
                <td>${s.matched ? "✓" : "·"} ${ph(s.rule)}${s.action ? html` <span class=muted>(${t(s.action)})</span>` : ""}</td>
                <td class=muted style=${{ fontSize: "12px" }}>${ph(s.detail)}</td>
              </tr>`)}
            </tbody></table></div>
          </div>`}
        </div>
        <div class=foot>
          <button class=ghost onClick=${onClose}>${t("Close")}</button>
          <button disabled=${busy} onClick=${run}>${busy ? t("Testing…") : t("Test route")}</button>
        </div>
      </div>
    </div>`;
}

// ---- labelled fields for the forms that are not built from a schema ----
// A caption over a control is decoration until it points at the control. The
// schema-driven forms do that in Field; these do it here, so the server wizard,
// the settings blocks and the sign-in boxes are not a page of captions that
// belong to nothing.
//
// The id comes from the caption text: stable across renders, unique inside one
// form, and nothing to keep in sync by hand.
function fieldId(label) {
  const s = String(label);
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
  return "f" + h.toString(36);
}
// bindLabel hands the id to whatever kind of control it was given: a plain
// input takes id, the password and chip inputs take their own prop, and a value
// that is not a control at all is left alone: a caption over a status line is
// not a label and must not claim to be one.
function bindLabel(node, id, hintId, required, invalid) {
  if (!node || typeof node !== "object" || !node.type) return null;
  if (typeof node.type === "string") {
    if (!["input", "select", "textarea"].includes(node.type)) return null;
    return React.cloneElement(node, { id, "aria-describedby": hintId, "aria-required": required || undefined, "aria-invalid": invalid || undefined });
  }
  if (node.type === PasswordInput || node.type === TagInput) {
    return React.cloneElement(node, { inputId: id, describedBy: hintId, required, invalid });
  }
  return null;
}
function LF({ label, hint, required, wide, error, empty: emptyOverride, children }) {
  const one = Array.isArray(children) ? children[0] : children;
  const id = fieldId(String(label) + "|" + String(hint || ""));
  const hintId = hint ? id + "-h" : undefined;
  const bound = bindLabel(one, id, error ? id + "-err" : hintId, required, !!error);
  const empty = required && (emptyOverride ?? (one?.props && fieldIsEmpty(one.props.value)));
  const cap = html`${label}${required ? html`<span class=required-mark aria-hidden=true>*</span>` : ""}`;
  return html`<div class=${"field" + (wide ? " f-wide" : "") + (empty ? " required-empty" : "") + (error ? " bad" : "")}>
    ${bound ? html`<label for=${id}>${cap}</label>` : html`<label>${cap}</label>`}
    ${bound || children}
    ${hint && html`<div class=hint id=${hintId}>${hint}</div>`}
    ${error && html`<div class=fielderr id=${id + "-err"} role=alert>${error}</div>`}
  </div>`;
}

// Parts of a field can depend on the rest of the form: which exits are still
// free, what the chosen purpose means, which server a fixed answer names.
function resolveField(f, vals) {
  let out = f;
  for (const k of ["options", "hint", "text"]) {
    if (typeof f[k] === "function") out = { ...out, [k]: f[k](vals) };
  }
  return out;
}

// A field takes half the form width unless it holds chips or asks to be wide:
// two short inputs side by side beat one column of 500px boxes.
const fieldIsEmpty = value => value == null || (Array.isArray(value) ? value.length === 0 : String(value).trim() === "");
function fieldWide(f) { return f.wide === true || f.type === "tags" || f.type === "country" || f.type === "textarea"; }
function Field({ f, value, onChange, error, setAll }) {
  const w = fieldWide(f) ? " f-wide" : "";
  const bad = !!error;
  // One form is open at a time, so the field's own name is id enough. Without it
  // the visible caption was decoration: nothing tied it to the control, and a
  // screen reader read the control alone.
  const id = "fld-" + f.name;
  const errId = bad ? id + "-err" : undefined;
  // The message is the field's description while it stands, so a screen reader
  // reads the reason with the control rather than leaving it on screen alone.
  const hintId = [f.hint ? id + "-hint" : "", errId || ""].filter(Boolean).join(" ") || undefined;
  const invalid = bad ? "true" : undefined;
  if (f.type === "bool") return html`<div class=${"field check form-check" + w + (f.alignInput ? " align-input" : "")}>
    <label for=${id}><input id=${id} type=checkbox checked=${!!value} aria-describedby=${hintId} onChange=${e => onChange(e.target.checked)} /><span>${t(f.label)}</span></label>
    ${f.hint && html`<div class=hint id=${hintId}>${t(f.hint)}</div>`}
  </div>`;
  // A question with one possible answer is not a question. It is still on the
  // form, because what it answers is a fact the operator needs while reading
  // the rest; the value travels with the form as if it had been picked.
  if (f.type === "static") return html`<div class=${"field" + w}>
    <label>${t(f.label)}</label>
    <div class=staticval>${f.text}</div>
    ${f.hint && html`<div class=hint>${t(f.hint)}</div>`}
  </div>`;
  let input;
  // The saved password is never sent to the browser, so there is nothing to show
  // but a mask. Pressing the button is what puts a password in the request at
  // all: until then an edit cannot touch it.
  if (f.type === "passtoggle") return html`<div class=${"field" + w}>
    <label>${t(f.label)}</label>
    <div class=pwrow>
      <span class=pwmask aria-hidden=true>••••••••</span>
      <button type=button class="ghost sm" onClick=${() => onChange(!value)}>
        ${value ? t(f.keepLabel || "Keep the current password") : t(f.changeLabel || "Change password")}
      </button>
    </div>
    ${f.hint && html`<div class=hint>${t(f.hint)}</div>`}
  </div>`;
  if (f.type === "select") input = html`<select id=${id} aria-describedby=${hintId} aria-invalid=${invalid} aria-required=${f.required || undefined} value=${value} onChange=${e => onChange(e.target.value)}>${f.options.map(o => {
    const val = typeof o === "object" ? o.value : o, lab = typeof o === "object" ? o.label : (o === "" ? "— none —" : o);
    return html`<option key=${val} value=${val}>${t(lab)}</option>`;
  })}</select>`;
  else if (f.type === "tags") input = html`<${TagInput} value=${value} onChange=${onChange} options=${f.catalog ? condCatalog(f.catalog) : null} free=${!f.catalog} placeholder=${f.placeholder ? t(f.placeholder) : undefined} inputId=${id} describedBy=${hintId} required=${f.required} invalid=${bad} />`;
  else if (f.type === "country") input = html`<${TagInput} value=${value} onChange=${onChange} options=${countryCatalog()} transform=${countryCode} free=${false} placeholder=${f.placeholder ? t(f.placeholder) : undefined} inputId=${id} describedBy=${hintId} required=${f.required} invalid=${bad} />`;
  else if (f.type === "password") input = html`<${React.Fragment}>
    <${PasswordInput} value=${value} onChange=${onChange} placeholder=${f.placeholder || ""} inputId=${id} describedBy=${bad ? errId : hintId} invalid=${!!bad} required=${f.required} />
  <//>`;
  else if (f.type === "date") input = html`<${React.Fragment}>
    <input id=${id} aria-describedby=${hintId} aria-invalid=${invalid} aria-required=${f.required || undefined} type=date value=${value} onInput=${e => onChange(e.target.value)} />
    ${f.clearable && value && html`<button type=button class="linkish sm" onClick=${() => onChange("")}>${t(f.clearable)}</button>`}
  <//>`;
  else input = html`<input id=${id} aria-describedby=${hintId} aria-invalid=${invalid} aria-required=${f.required || undefined} type=${f.type === "number" ? "number" : "text"} value=${value} placeholder=${f.placeholder ? t(f.placeholder) : ""} readOnly=${!!f.readOnly} onInput=${e => onChange(e.target.value)} />`;
  return html`<div class=${"field" + w + (f.required && fieldIsEmpty(value) ? " required-empty" : "") + (bad ? " bad" : "")}>
    ${f.label ? html`<div class=field-cap><label for=${id}>${t(f.label)}${f.required ? html`<span class=required-mark aria-hidden=true>*</span>` : ""}</label>
      ${f.generate && setAll && html`<button type=button class="linkish sm" onClick=${() => f.generate(setAll)}>${t("Generate one")}</button>`}
    </div>` : ""}
    ${input}
    ${f.hint && html`<div class=hint id=${id + "-hint"}>${t(f.hint)}</div>`}
    ${bad && html`<div class=fielderr id=${errId} role=alert>${error.msg}</div>`}
  </div>`;
}

// Switch reads as a state across a column, where a row of checkmarks has to be
// read one line at a time. It is a checkbox underneath, so the keyboard and a
// screen reader treat it as the checkbox it is.
function Switch({ on, disabled, label, title, id, onChange }) {
  return html`<label class=switch title=${title || undefined}>
    <input type=checkbox id=${id || undefined} checked=${!!on} disabled=${!!disabled} aria-label=${label} onChange=${onChange} />
    <span class=track><span class=knob></span></span>
  </label>`;
}

// The four things a rule can look at. The label is the question the field asks,
// because the field is the question: a rule starts by naming what it catches,
// not by declaring a type and then filling in a box.
const COND_KINDS = [
  ["geosite", "geosite categories", "Find a category: youtube, telegram…"],
  ["geoip", "geoip countries", "Country name or code: Germany, de…"],
  ["domain", "Domains", "example.com — no https:// and no path"],
  ["cidr", "IPs and subnets", "203.0.113.0/24 or 2001:db8::/32"],
];
// How a line joins to everything above it. The list folds top to bottom, which
// is the order it was written in, mirroring model.MatchTree.
const COND_JOINS = [["and", "and"], ["or", "or"], ["except", "but not"]];
const condKindDef = (k) => COND_KINDS.find(x => x[0] === k) || COND_KINDS[0];
const condTitle = (k) => t(condKindDef(k)[1]);
const condHint = (k) => t(condKindDef(k)[2]);
const joinWord = (j) => t(j === "or" ? "or" : j === "except" ? "but not" : "and");
// A domain or an address is whatever was typed; a category or a country is a
// name out of a catalogue, where a half-typed search is not an answer.
const condIsFree = (k) => k === "domain" || k === "cidr";
const condEmpty = (k) => t({ geosite: "no categories yet", geoip: "no countries yet", domain: "no domains yet", cidr: "no addresses yet" }[k] || "nothing yet");

// countryCatalog: the geoip sets the source actually offers, named in the
// language the panel is being read in. Before any check has run, the built-in
// shortlist is the whole answer.
let COUNTRY_CACHE = { key: "", list: [] };
function countryCatalog() {
  const codes = GEO_CATALOG.geoip.length ? GEO_CATALOG.geoip : COUNTRIES.map(c => c[0]);
  const key = LANG + ":" + codes.length;
  if (COUNTRY_CACHE.key === key) return COUNTRY_CACHE.list;
  let names = null;
  try { names = new Intl.DisplayNames([LANG === "ru" ? "ru" : "en"], { type: "region" }); } catch { /* older engine: the English list stands in */ }
  const english = new Map(COUNTRIES);
  const list = codes.map(code => {
    const cc = String(code).toLowerCase().trim();
    let name = english.get(cc) || cc;
    if (cc === "private") name = t("private addresses");
    else if (names && /^[a-z]{2}$/.test(cc)) { try { name = names.of(cc.toUpperCase()) || name; } catch { /* not a region code */ } }
    return { value: cc, label: name + " (" + cc + ")", search: (name + " " + (english.get(cc) || "") + " " + cc).toLowerCase() };
  }).sort((a, b) => a.label.localeCompare(b.label, LANG === "ru" ? "ru" : "en"));
  COUNTRY_CACHE = { key, list };
  return list;
}
function condCatalog(kind) {
  if (kind === "geoip") return countryCatalog();
  if (kind === "geosite") return geositeOptions().map(v => ({ value: v, label: v, search: v }));
  return null;
}
// A country is stored as its code and shown as its name: "ru" is what the node
// is given, "Россия (ru)" is what the rule is read as.
function condValueLabel(kind, v) {
  if (kind !== "geoip") return String(v);
  const hit = countryCatalog().find(o => o.value === String(v).toLowerCase());
  return hit ? hit.label : String(v);
}
// condShort is one line of a match, read back in a few words.
function condShort(c) {
  const vals = (c.values || []).filter(x => String(x).trim() !== "");
  if (!vals.length) return condEmpty(c.kind);
  const text = vals.map(v => condValueLabel(c.kind, v)).join(" " + t("or") + " ");
  const prefix = c.kind === "geosite" ? "geosite: " : c.kind === "geoip" ? "geoip: " : c.kind === "cidr" ? "IP: " : "";
  return prefix + (vals.length > 1 ? "(" + text + ")" : text);
}
// condFormula is the whole match, bracketed as it folds. The editor shows it
// while the lines are being written, so the grouping is read rather than
// guessed: "a and b or c" is (a and b) or c, and it says so.
function condFormula(rows) {
  const filled = condRows(rows);
  if (!filled.length) return t("nothing is filled in yet");
  let out = condShort(filled[0]);
  let last = null;
  for (const c of filled.slice(1)) {
    const join = c.join === "or" || c.join === "except" ? c.join : "and";
    const head = last === null || (join === last && join !== "except") ? out : "(" + out + ")";
    out = head + " " + joinWord(join) + " " + condShort(c);
    last = join;
  }
  return out;
}

// TagInput collects the values of one field: chips for what is chosen, a search
// for what is not yet. With a catalogue behind it, it is a combobox: the
// suggestions are real options, reachable by keyboard, and picking one is what
// adds it. Walking away from a half-typed search adds nothing: "ger" is not a
// country, and a field that quietly turned it into one wrote a rule-set name
// that no node has.
function TagInput({ value, onChange, draft, onDraft, options, transform, free, placeholder, inputId, describedBy, invalid, required, ariaLabel, autofocus, floating }) {
  const arr = value || [];
  const [own, setOwn] = useState("");
  const q = String((onDraft ? draft : own) || "");
  const setQ = onDraft || setOwn;
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const box = useRef(null);
  const inp = useRef(null);
  const popup = useRef(null);
  const [popupStyle, setPopupStyle] = useState(null);
  const norm = transform || ((x) => String(x).trim());
  const id = inputId || "tags";
  const optId = (i) => id + "-opt-" + i;

  const add = (raw) => {
    const val = norm(raw);
    if (val && !arr.includes(val)) onChange([...arr, val]);
    setQ(""); setOpen(false); setActive(-1);
    if (inp.current) inp.current.focus();
  };
  // Matches that start with what was typed come first: "ru" should offer Russia
  // before Belarus, even though both hold the letters.
  const matches = () => {
    const s = q.trim().toLowerCase();
    if (!options || !s) return [];
    const head = [], tail = [];
    for (const o of options) {
      const i = o.search.indexOf(s);
      if (i === 0) head.push(o);
      else if (i > 0) tail.push(o);
    }
    return head.concat(tail).slice(0, 40);
  };
  const list = open ? matches() : [];
  const typed = norm(q);
  const known = !!options && !!typed && options.some(o => o.value === typed);
  // The catalogue is the whole source, mirrored here, so a name it does not
  // have is a name the nodes cannot be given: the rule-set behind it cannot be
  // fetched, the node's configuration push fails as a whole, and that server
  // stops receiving updates entirely until the rule is fixed. So it is refused
  // here rather than offered "as typed".
  //
  // With no catalogue at all, a source never checked, nothing is known, and
  // refusing would be a verdict from ignorance; then what was typed goes in.
  const haveCatalog = !!options && options.length > 0;
  // While the search still matches something, the matches are the answer: the
  // half-typed text is on its way to one of them, not a decision yet.
  const rows = !free && open && typed && !known && !list.length
    ? [haveCatalog
        ? { value: typed, label: tf("“{v}” is not in the source — check the spelling", { v: typed }), refused: true }
        : { value: typed, label: tf("Add “{v}” as typed", { v: typed }), manual: true }]
    : list;

  // The routing body scrolls independently of its header and footer. Place
  // suggestions in the room above or below the field, without clipping them
  // behind the footer. Keep the list in this control's DOM for keyboard focus.
  React.useLayoutEffect(() => {
    if (!floating || !rows.length) { setPopupStyle(null); return; }
    const place = (e) => {
      if (e && popup.current && popup.current.contains(e.target)) return;
      if (!box.current) return;
      const field = box.current.getBoundingClientRect();
      const body = box.current.closest(".body");
      const bounds = body ? body.getBoundingClientRect() : { top: 12, bottom: window.innerHeight - 12 };
      const top = Math.max(12, bounds.top), bottom = Math.min(window.innerHeight - 12, bounds.bottom);
      if (field.bottom <= top || field.top >= bottom) { setOpen(false); return; }
      const below = bottom - field.bottom - 4, above = field.top - top - 4;
      const up = below < Math.min(180, rows.length * 44) && above > below;
      setPopupStyle({ left: field.left, width: field.width,
        top: up ? undefined : field.bottom + 4,
        bottom: up ? window.innerHeight - field.top + 4 : undefined,
        maxHeight: Math.max(24, Math.min(240, up ? above : below)) });
    };
    place();
    window.addEventListener("resize", place);
    document.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      document.removeEventListener("scroll", place, true);
    };
  }, [floating, rows.length, q, value]);

  const focusOpt = (i) => { setActive(i); document.getElementById(optId(i))?.focus(); };
  const move = (d) => { if (!rows.length) return; const n = active < 0 ? (d > 0 ? 0 : rows.length - 1) : (active + d + rows.length) % rows.length; focusOpt(n); };
  const close = (toInput) => { setOpen(false); setActive(-1); if (toInput && inp.current) inp.current.focus(); };
  useEscape(() => { if (!open || !rows.length) return false; close(true); return true; });

  const onInput = (e) => { setQ(e.target.value); setOpen(true); setActive(-1); };
  const onKey = (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setOpen(true); setTimeout(() => move(1), 0); return; }
    if (e.key === "ArrowUp") { e.preventDefault(); setOpen(true); setTimeout(() => move(-1), 0); return; }
    if (e.key === "Enter" || e.key === "," || (free && e.key === " ")) {
      if (e.key === "Enter" && !q.trim()) return;
      e.preventDefault();
      if (rows.length) { if (!rows[0].refused) add(rows[0].value); }
      else if (free) add(q);
      return;
    }
    if (e.key === "Tab" && !e.shiftKey && rows.length) { e.preventDefault(); focusOpt(0); return; }
    if (e.key === "Backspace" && !q && arr.length) onChange(arr.slice(0, -1));
  };
  // Focus leaving the whole control closes the list. It does not commit what is
  // in the search: that is the point.
  const leave = (e) => { if (box.current && e.relatedTarget && box.current.contains(e.relatedTarget)) return; close(false); };
  const optKey = (i, o) => (e) => {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); if (!o.refused) add(o.value); }
    else if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); i === 0 ? close(true) : move(-1); }
    else if (e.key === "Home") { e.preventDefault(); focusOpt(0); }
    else if (e.key === "End") { e.preventDefault(); focusOpt(rows.length - 1); }
    else if (e.key === "Tab" && e.shiftKey && i === 0) { e.preventDefault(); inp.current?.focus(); }
  };

  return html`<div class=${"taginput" + (required && !arr.length ? " required-empty" : "")} ref=${box} onBlur=${leave}>
    <div class=tag-box>
      ${arr.map((val, i) => html`<span class=chip key=${val}><span>${condValueLabelOf(options, val)}</span><button type=button
        aria-label=${tf("Remove {value}", { value: val })} title=${t("Remove")}
        onClick=${() => onChange(arr.filter((_, j) => j !== i))}>×</button></span>`)}
      <input ref=${inp} value=${q} id=${id}
        placeholder=${arr.length ? t("Add more…") : placeholder || t("add…")}
        aria-describedby=${describedBy || undefined} aria-invalid=${invalid ? "true" : undefined} aria-required=${required || undefined}
        aria-label=${ariaLabel || undefined} autocomplete=off spellcheck=false data-autofocus=${autofocus ? "1" : undefined}
        role=${options ? "combobox" : undefined} aria-expanded=${options ? (rows.length > 0 ? "true" : "false") : undefined}
        aria-controls=${options ? id + "-opts" : undefined}
        aria-activedescendant=${active >= 0 ? optId(active) : undefined}
        onInput=${onInput} onKeyDown=${onKey} />
    </div>
    ${rows.length > 0 && html`<ul class=${"tag-opts" + (floating ? " floating" : "")} style=${floating ? popupStyle : undefined}
      ref=${popup} id=${id + "-opts"} role=listbox aria-label=${t("Suggestions")}>
      ${rows.map((o, i) => html`<li key=${o.value + ":" + i} id=${optId(i)} role=option tabIndex=0
        aria-selected=${i === active ? "true" : "false"} aria-disabled=${o.refused ? "true" : undefined}
        class=${(i === active ? "on" : "") + (o.manual ? " manual" : "") + (o.refused ? " refused" : "")}
        onKeyDown=${optKey(i, o)} onClick=${() => { if (!o.refused) add(o.value); }}>${o.label}</li>`)}
    </ul>`}
  </div>`;
}
// A chip shows the name the value was picked by, not the code it is stored as.
function condValueLabelOf(options, val) {
  const hit = options && options.find(o => o.value === val);
  return hit ? hit.label : String(val);
}

/* ---------------- client-config modal ---------------- */
// QRImage renders a QR for text via the vendored qrcode-generator global (from
// /vendor/qrcode.js). Fully local: nothing leaves the panel, works offline. The
// type-number 0 auto-fits the version; Byte mode handles arbitrary URLs.
function QRImage({ text, cell = 6 }) {
  if (!text || typeof qrcode !== "function") return null;
  let url;
  try { const q = qrcode(0, "M"); q.addData(text); q.make(); url = q.createDataURL(cell, 2); }
  catch (e) { return html`<div class=muted style=${{ fontSize: "12px", textAlign: "center" }}>${t("Link too long to render as a QR")}</div>`; }
  return html`<img src=${url} alt=${t("QR code")} width=220 height=220 style=${{ imageRendering: "pixelated", background: "#fff", padding: "10px", borderRadius: "var(--radius)", display: "block", margin: "0 auto" }} />`;
}

// LinkRow: a generated link, shown as selectable text rather than in an input,
// there is nothing here to type into.
function LinkRow({ label, value, onCopy }) {
  return html`<div class=linkrow>
    ${label && html`<label>${label}</label>`}
    <div class=linkval><code title=${value}>${value}</code><button class="ghost sm" onClick=${() => onCopy(value)}>${t("Copy")}</button></div>
  </div>`;
}

function ConfigModal({ userId, username, user, entries, onClose, notify }) {
  const [entry, setEntry] = useState(entries[0] ? entries[0].id : "");
  const [cfg, setCfg] = useState(null);
  // Two ways of handing the same config over, and the link is the one that is
  // used: it opens the app on the client's phone. The file is for the cases it
  // cannot, so it is behind the second tab, with its own button under the
  // editor rather than in the row the window is closed from.
  const [tab, setTab] = useState("link");
  const [toml, setToml] = useState("");
  const [busy, setBusy] = useState(false);
  const ref = useModal(onClose);
  React.useLayoutEffect(() => {
    const body = ref.current && ref.current.querySelector(".body");
    if (body) body.scrollTop = 0;
  }, [tab]);
  // Surface why a freshly generated config might not actually connect: a config
  // builds regardless of these, but the client won't get through. The selected
  // entry being in maintenance or unhealthy is shown inline on the option itself
  // and as a banner (maintenance is a deliberate drain; unhealthy is the entry
  // failing its own health check, both meaning "won't connect").
  const noEntries = entries.length === 0;
  const userDisabled = !!user && user.enabled === false;
  const userExpired = !!user && isExpired(user.expires_at);
  const selectedEntry = entries.find(n => n.id === entry);
  const entryUnhealthy = !!selectedEntry && selectedEntry.health && selectedEntry.health !== "healthy";
  // Rendering a config is a read: nothing is issued or rotated by asking for it.
  // So it is fetched as the dialog opens; pressing "Generate" chose nothing.
  const fetchCfg = useCallback(async () => {
    if (!entry) return;
    setBusy(true); setTab("link");
    try { const c = await api("GET", `/api/users/${encodeURIComponent(userId)}/client-config?entry=${encodeURIComponent(entry)}`); setCfg(c); setToml(c.config_toml || ""); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  }, [userId, entry, notify]);
  useEffect(() => { fetchCfg(); }, [fetchCfg]);
  const download = () => {
    const blob = new Blob([toml], { type: "text/plain" });
    const a = document.createElement("a"); a.href = URL.createObjectURL(blob);
    a.download = `trusttunnel-${username}-${entry}.toml`; a.click();
  };
  const copy = (v) => { navigator.clipboard.writeText(v).then(() => notify(t("Copied"), "ok"), () => notify(t("Could not copy — select the text and copy it by hand"), "err")); };
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class="modal connect-modal fixed-h" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
        <header><h3 id=modal-title>${t("Client connection")} — ${username}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          ${(noEntries || userDisabled || userExpired || entryUnhealthy) && html`<div class="note warn">
            ${noEntries && html`<div>⚠ ${t("No entry nodes yet — add one before this config can connect.")}</div>`}
            ${userDisabled && html`<div>⚠ ${t("This client is disabled — the config builds but will be rejected until you enable it.")}</div>`}
            ${userExpired && html`<div>⚠ ${t("This client has expired — extend its expiry or it cannot connect.")}</div>`}
            ${entryUnhealthy && html`<div>⚠ ${t("The selected entry is unhealthy — the config builds but may not connect until it recovers.")}</div>`}
          </div>`}
          ${entries.length > 1 && html`<${LF} label=${t("Entry node")}>
            <select value=${entry} onChange=${e => { setEntry(e.target.value); setCfg(null); }}>
              ${entries.map(n => html`<option value=${n.id}>${n.name || n.id} (${(n.public_ips || []).join(",")})${n.maintenance ? " — " + t("in maintenance") : n.health && n.health !== "healthy" ? " — " + t("unhealthy") : ""}</option>`)}
            </select><//>`}
          ${busy && !cfg && html`<${Skeleton} rows=${2} />`}
          ${cfg && html`<${React.Fragment}>
            ${(cfg.warnings || []).length > 0 && html`<div class="note warn">
              ${cfg.warnings.map((w, i) => html`<div key=${i}>⚠ ${t(w)}</div>`)}
            </div>`}
            <${SubTabs} tabs=${(cfg.deep_link ? [["link", t("Link & QR")]] : []).concat([["toml", t("Config")]])}
              active=${cfg.deep_link ? tab : "toml"} onSelect=${setTab} />
            ${cfg.deep_link && tab === "link" && html`<div class=connect-link-view>
              <div class=connect-qr><${QRImage} text=${cfg.deep_link} />
              <p class=muted style=${{ textAlign: "center", fontSize: "14px", margin: ".5rem 0 0" }}>${t("Scan with the TrustTunnel app to import.")}</p></div>
              <div class=connect-options><${LinkRow} label=${t("App link")} value=${cfg.deep_link} onCopy=${copy} />
              ${cfg.qr_link && html`<details class=connect-page>
                <summary>${t("connection page")}</summary>
                <${LinkRow} value=${cfg.qr_link} onCopy=${copy} />
              </details>`}</div>
            </div>`}
            ${(!cfg.deep_link || tab === "toml") && html`<div class=connect-config-view>
              <textarea class=config rows=14 spellcheck=false value=${toml} onInput=${e => setToml(e.target.value)} aria-label=${t("Client config")}></textarea>
              <div class=row style=${{ marginTop: ".7rem", flexWrap: "wrap" }}>
                <button class="ghost sm" onClick=${() => copy(toml)}>${t("Copy")}</button>
                ${toml !== cfg.config_toml && html`<button class="ghost sm" onClick=${() => setToml(cfg.config_toml || "")}>${t("Undo edits")}</button>`}
              </div>
              <div class=connect-download>
                <button class=ghost onClick=${download}>${t("Download .toml")}</button>
              </div>
            </div>`}
          <//>`}
        </div>
        <div class=foot><button class=ghost onClick=${onClose}>${t("Close")}</button></div>
      </div>
    </div>`;
}

/* ---------------- provision (remote install) modal ---------------- */
// What the wizard holds before anything is answered, kept so that closing it
// can tell "nothing was typed" from "three screens of answers".
const PROVISION_BLANK = { role: "exit", ssh_port: 22, ssh_user: "root", harden: true, h_sudo: "user", h_port: 3222, h_root: true, h_pw: true, h_f2b: true, h_fw: true, host_landing: true };
function ProvisionModal({ onClose, reload, notify, isAdmin = true, state = {} }) {
  const [f, setF] = useState(PROVISION_BLANK);
  const set = (k, v) => setF(s => ({ ...s, [k]: v }));
  const [ips, setIps] = useState([]);
  // The deployment apex (a fleet-wide singleton) lives in settings. A distinct
  // node fronts it as the decoy; later entries only carry their own endpoint.
  const [apex, setApex] = useState("");
  useEffect(() => { (async () => {
    try { const s = await api("GET", "/api/settings"); setApex(((s.fleet && s.fleet.apex) || "").trim()); } catch (e) { /* apex may be unset on the first entry */ }
  })(); }, []);
  const apexHosted = !!apex && (state.domains || []).some(d => (d.hostname || "").toLowerCase() === apex.toLowerCase());
  // The apex decoy is carried by exactly one node: offer it only while unclaimed.
  const canHostLanding = f.role === "entry" && !apexHosted && (apex || (f.apex || "").trim());
  const landingActive = canHostLanding && !!f.host_landing;
  // Hostnames whose DNS must point at this node before install.
  const dnsHosts = [(f.endpoint || "").trim(), landingActive ? (apex || (f.apex || "").trim()) : ""].filter(Boolean);
  const [auth, setAuth] = useState("password");
  const [job, setJob] = useState(null);
  const [busy, setBusy] = useState(false);
  // Three questions, asked one at a time: what the server is, how to reach it
  // now, and how it should be reachable afterwards. They used to be one column
  // 1147px tall in an 858px window, with the SSH access for the install and the
  // SSH access after hardening in the same run of fields.
  const STEPS = ["server", "ssh", "after"];
  const [step, setStep] = useState(0);
  const [err, setErr] = useState(null); // { key, msg }
  const bad = (key) => (err && err.key === key ? err.msg : null);
  const fail = (key, msg) => {
    setErr({ key, msg });
    setTimeout(() => {
      const el = ref.current && ref.current.querySelector(".field.bad input, .field.bad select, .field.bad textarea");
      if (el) el.focus();
    }, 0);
    return false;
  };
  const stepOK = (i) => {
    if (i === 0) {
      if (!String(f.name || "").trim()) return fail("name", t("Give the server a name"));
      if (ips.length === 0) return fail("ips", t("Add at least one public IP"));
      if (f.role === "entry" && !String(f.endpoint || "").trim()) return fail("endpoint", t("Clients need a hostname to connect to"));
      if (f.role === "exit" && !String(f.reality_sni || "").trim()) return fail("sni", t("Pick the domain this node's traffic looks like"));
      return true;
    }
    if (i === 1) {
      if (!String(f.ssh_host || ips[0] || "").trim()) return fail("ssh_host", t("Say where to reach the server over SSH"));
      if (!String(f.ssh_user || "").trim()) return fail("ssh_user", t("Say which user to log in as"));
      if (auth === "password" && !f.ssh_password) return fail("ssh_secret", t("The install needs the SSH password once"));
      if (auth === "key" && !f.ssh_key) return fail("ssh_secret", t("The install needs the SSH private key once"));
      return true;
    }
    if (f.harden && f.h_pw && !String(f.h_pubkey || "").trim()) {
      return fail("h_pubkey", t("Password login cannot be turned off without a key to log in with"));
    }
    return true;
  };
  const next = () => { setErr(null); if (stepOK(step)) setStep(s => Math.min(s + 1, STEPS.length - 1)); };
  const prev = () => { setErr(null); setStep(s => Math.max(s - 1, 0)); };
  // Escape must not close the dialog mid-install (matches the hidden ✕ / disabled
  // Done while running). jobRef tracks the latest status for the captured handler.
  const jobRef = useRef(null); jobRef.current = job;
  const ref = useModal(
    () => { if (!jobRef.current || jobRef.current.status !== "running") onClose(); },
    () => !job && JSON.stringify({ f, ips, auth }) !== JSON.stringify({ f: PROVISION_BLANK, ips: [], auth: "password" }));

  const start = async () => {
    const sshHost = (f.ssh_host || ips[0] || "").trim(); // default to the first public IP shown in the placeholder
    if (!f.name || ips.length === 0 || !sshHost) { notify(t("Name and at least one public IP are required"), "err"); return; }
    if (f.role === "exit" && !f.reality_sni) { notify(t("Reality SNI is required for an exit"), "err"); return; }
    if (f.role === "entry" && !(f.endpoint || "").trim()) { notify(t("Endpoint hostname is required for an entry"), "err"); return; }
    setBusy(true);
    const body = {
      name: f.name, role: f.role, public_ips: ips,
      endpoint: f.role === "entry" ? (f.endpoint || "").trim() : "",
      apex: f.role === "entry" ? (f.apex || "").trim() : "",
      host_landing: f.role === "entry" && !apexHosted && !!f.host_landing,
      reality_sni: f.role === "exit" ? (f.reality_sni || "") : "",
      make_standby: false, // its own action on a ready exit (Settings → panel standby)
      ssh: { host: sshHost, port: parseInt(f.ssh_port) || 22, user: f.ssh_user, password: auth === "password" ? (f.ssh_password || "") : "", key_pem: auth === "key" ? (f.ssh_key || "") : "" },
      hardening: f.harden
        ? { enabled: true, sudo_user: f.h_sudo || "", ssh_pubkey: f.h_pubkey || "", ssh_port: parseInt(f.h_port) || 0, disable_root_login: !!f.h_root, disable_password_auth: !!f.h_pw, fail2ban: !!f.h_f2b, firewall: !!f.h_fw }
        : { enabled: false },
    };
    if (!stepOK(0)) { setStep(0); setBusy(false); return; }
    if (!stepOK(1)) { setStep(1); setBusy(false); return; }
    if (!stepOK(2)) { setBusy(false); return; }
    try { const r = await api("POST", "/api/nodes/provision", body); poll(r.job_id); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };
  const poll = (id) => {
    const tick = async () => {
      try {
        const s = await api("GET", "/api/jobs/" + id); setJob(s);
        if (s.status === "running") setTimeout(tick, 1000);
        else { setBusy(false); if (s.status === "succeeded") { notify(t("Node installed"), "ok"); reload(); } }
      } catch (e) { notify(e.message, "err"); setBusy(false); }
    };
    tick();
  };

  // Two fields per row, as everywhere else; anything holding chips, a textarea or
  // a sentence takes the whole width.
  const fld = (label, node, hint, wide, key) => html`<${LF} label=${label} hint=${hint} wide=${wide}
    required=${["name", "role", "ips", "endpoint", "sni", "doms", "ssh_user", "ssh_secret"].includes(key) || (key === "h_pubkey" && f.harden && f.h_pw)}
    error=${key ? bad(key) : null}>${node}<//>`;
  const txt = (k, ph) => html`<input value=${f[k] || ""} placeholder=${ph || ""} onInput=${e => set(k, e.target.value)} />`;
  const chk = (k, label, half) => {
    const id = fieldId("chk|" + k);
    return html`<div class=${"field check form-check" + (half ? "" : " f-wide")}>
      <label for=${id}><input id=${id} type=checkbox checked=${!!f[k]} onChange=${e => set(k, e.target.checked)} /><span>${label}</span></label></div>`;
  };
  const hr = html`<hr style=${{ border: 0, borderTop: "1px solid var(--line)", margin: ".2rem 0 1.2rem" }} />`;

  if (job) {
    return html`<div class=overlay><div class=modal ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
      <header><h3 id=modal-title>${t("Adding the server")}</h3>${job.status !== "running" && html`<button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button>`}</header>
      <div class=body>
        <div class=field><label>${t("Status")}</label><span class=${job.status === "succeeded" ? "ok" : job.status === "failed" ? "err" : "muted"}>${t(job.status)}</span></div>
        <pre class=config>${(job.log || []).join("\n")}${job.error ? "\nERROR: " + job.error : ""}</pre>
      </div>
      <div class=foot>
        ${job.status === "failed" && html`<button class=ghost onClick=${() => { setJob(null); setBusy(false); }}>${t("Back")}</button>`}
        <button disabled=${job.status === "running"} onClick=${onClose}>${job.status === "running" ? t("Adding…") : t("Done")}</button>
      </div>
    </div></div>`;
  }

  const stepTitle = [t("The server"), t("SSH access"), t("Access after the install")][step];
  const serverStep = html`
    <div class=mfields>
      ${fld(t("Name"), txt("name", "Exit NL"), null, false, "name")}
      ${fld(t("Role"), html`<select value=${f.role} onChange=${e => set("role", e.target.value)}><option value=exit>${t("exit")}</option><option value=entry>${t("entry")}</option></select>`, null, false, "role")}
      ${fld(t("Public IPs"), html`<${TagInput} value=${ips} onChange=${setIps} />`, null, true, "ips")}
      ${f.role === "entry" && fld(t("Endpoint hostname"), txt("endpoint", apex ? "vpn." + apex : "vpn.example.com"), t("The VPN connection endpoint served on this node."), false, "endpoint")}
      ${f.role === "entry" && !apex && fld(t("Apex domain"), txt("apex", "example.com"), t("Set once, reused for later servers."))}
      ${canHostLanding && chk("host_landing", t("Also serve the apex landing site on this node"))}
      ${f.role === "entry" && dnsHosts.length > 0 && html`<div class="hint f-wide">${t("Add a DNS record")}: ${dnsHosts.join(", ")} → ${ips[0] || t("this server's IP")}</div>`}
      ${f.role === "exit" && fld(t("Reality domain (SNI)"), txt("reality_sni", "www.example-cdn.com"), t("a third-party domain this node's traffic is made to look like; keys are generated for you"), true, "sni")}
    </div>`;

  const sshStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("How the panel reaches this server to install it. Used once, and not stored.")}</p>
    <div class=mfields>
      ${fld(t("SSH host"), txt("ssh_host", ips[0] || ""), null, false, "ssh_host")}
      ${fld(t("Auth"), html`<select value=${auth} onChange=${e => setAuth(e.target.value)}><option value=password>${t("password")}</option><option value=key>${t("private key")}</option></select>`)}
      ${fld(t("SSH user"), txt("ssh_user"), null, false, "ssh_user")}
      ${fld(t("Port"), txt("ssh_port"))}
      ${auth === "password"
        ? fld(t("SSH password"), html`<input type=password value=${f.ssh_password || ""} onInput=${e => set("ssh_password", e.target.value)} />`, null, true, "ssh_secret")
        : fld(t("SSH private key (PEM)"), html`<textarea value=${f.ssh_key || ""} onInput=${e => set("ssh_key", e.target.value)}></textarea>`, null, true, "ssh_secret")}
    </div>`;

  const afterStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("How the server should be reachable once it is installed, instead of the access used to install it.")}</p>
    <div class=mfields>
      ${chk("harden", t("Harden server"))}
      ${f.harden && html`<${React.Fragment}>
        ${fld(t("Sudo user to create"), txt("h_sudo"))}
        ${fld(t("New SSH port"), txt("h_port"))}
        ${fld(t("SSH public key for the sudo user"), html`<textarea value=${f.h_pubkey || ""} onInput=${e => set("h_pubkey", e.target.value)} placeholder="ssh-ed25519 AAAA…"></textarea>`, t("required before password auth can be disabled"), true, "h_pubkey")}
        ${chk("h_root", t("Disable root login"), true)}
        ${chk("h_pw", t("Disable password login"), true)}
        ${chk("h_f2b", t("Install fail2ban"), true)}
        ${chk("h_fw", t("Enable firewall (ssh / 443 / 80)"), true)}
      <//>`}
    </div>
    <div class=rr-note style=${{ marginTop: "1.2rem" }}>
      <div><b>${f.name || t("This server")}</b> — ${f.role === "exit" ? t("exit") : t("entry")}${ips.length ? " · " + ips.join(", ") : ""}</div>
      <div class=muted style=${{ fontSize: "14px", marginTop: ".3rem" }}>
        ${f.harden
          ? tf("After the install: log in as {user} on port {port}.", { user: f.h_sudo || "—", port: f.h_port || 22 })
          : t("After the install: the SSH access stays as it is now.")}
      </div>
    </div>`;

  return html`<div class=overlay onClick=${e => e.target.classList.contains("overlay") && ref.close()}><div class="modal wide wizard-modal" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
    <header>
      <h3 id=modal-title>${t("Add a new server")}
        <span class=step-of>${tf("Step {n} of {total}", { n: step + 1, total: STEPS.length })} · ${stepTitle}</span>
      </h3>
      <button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button>
    </header>
    <div class=body>
      ${step === 0 ? serverStep : step === 1 ? sshStep : afterStep}
    </div>
    <div class=foot>
      <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
      ${step > 0 && html`<button class=ghost onClick=${prev}>${t("Back")}</button>`}
      ${step < STEPS.length - 1
        ? html`<button onClick=${next}>${t("Next")}</button>`
        : html`<button disabled=${busy} onClick=${start}>${busy ? t("Starting…") : t("Install and add the server")}</button>`}
    </div>
  </div></div>`;
}

/* ---------------- migration: one server becomes two ---------------- */
// A single-box deployment takes the connection and lets the traffic out on the
// same machine. Splitting it puts a new entry in front and turns this one into
// the exit: a migration, not a setting, since domains move or are re-issued, groups
// change where they leave from, and the client's own address may change with
// them. So it is asked as a migration: one question per screen, and the last
// screen is what will change and what has to be true before it starts.
function ConvertModal({ state, onClose, reload, notify }) {
  const single = (state.nodes || []).find(n => n.public_role === "entry");
  const groups = state.groups || [];
  // Variant 1 gives the new entry its own hostname (clients get new configs);
  // variant 2 moves the existing hostnames onto it (clients notice nothing).
  const KEEP = 2, MOVE = 1;
  const BLANK = { ssh_port: 22, ssh_user: "root", variant: KEEP, reality_sni: "", harden: true, h_sudo: "user", h_port: 3222, h_root: true, h_pw: true, h_f2b: true, h_fw: true };
  const [f, setF] = useState(BLANK);
  const set = (k, v) => setF(s => ({ ...s, [k]: v }));
  const [ips, setIps] = useState([]);
  const [doms, setDoms] = useState([""]); // variant 1: B's cert domains (first = client endpoint)
  const [auth, setAuth] = useState("password");
  const [allGroups, setAllGroups] = useState(true);
  const [gsel, setGsel] = useState([]);
  const [job, setJob] = useState(null);
  const [busy, setBusy] = useState(false);
  const variant = parseInt(f.variant) || KEEP;
  const keepsAddress = variant === KEEP;

  const STEPS = ["entry", "address", "egress", "ssh", "after", "check"];
  const [step, setStep] = useState(0);
  const [err, setErr] = useState(null); // { key, msg }
  const bad = (key) => (err && err.key === key ? err.msg : null);
  const fail = (key, msg) => {
    setErr({ key, msg });
    setTimeout(() => {
      const el = ref.current && ref.current.querySelector(".field.bad input, .field.bad select, .field.bad textarea");
      if (el) el.focus();
    }, 0);
    return false;
  };
  const stepOK = (i) => {
    if (i === 0) {
      if (!String(f.name || "").trim()) return fail("name", t("Give the new server a name"));
      if (ips.length === 0) return fail("ips", t("Add at least one public IP"));
      return true;
    }
    if (i === 1) {
      // Keeping an address there is none of is not an answer: the server refuses
      // it too, and it would refuse it after the install had already started.
      if (keepsAddress && currentHosts.length === 0) {
        return fail("variant", t("This server has no hostname to hand over — give the new one an address of its own."));
      }
      if (!keepsAddress && doms.map(d => d.trim()).filter(Boolean).length === 0) {
        return fail("doms", t("A new address needs a hostname for the certificate"));
      }
      return true;
    }
    if (i === 2) {
      if (!String(f.reality_sni || "").trim()) return fail("sni", t("Pick the domain this server's traffic will look like"));
      if (!allGroups && gsel.length === 0) return fail("groups", t("Pick the groups that leave through the new exit, or take all of them"));
      return true;
    }
    if (i === 3) {
      if (!String(f.ssh_host || ips[0] || "").trim()) return fail("ssh_host", t("Say where to reach the server over SSH"));
      if (!String(f.ssh_user || "").trim()) return fail("ssh_user", t("Say which user to log in as"));
      if (auth === "password" && !f.ssh_password) return fail("ssh_secret", t("The install needs the SSH password once"));
      if (auth === "key" && !f.ssh_key) return fail("ssh_secret", t("The install needs the SSH private key once"));
      return true;
    }
    if (i === 4 && f.harden && f.h_pw && !String(f.h_pubkey || "").trim()) {
      return fail("h_pubkey", t("Password login cannot be turned off without a key to log in with"));
    }
    return true;
  };
  const next = () => { setErr(null); if (stepOK(step)) setStep(s => Math.min(s + 1, STEPS.length - 1)); };
  const prev = () => { setErr(null); setStep(s => Math.max(s - 1, 0)); };
  // Escape must not close the dialog mid-migration (matches the hidden ✕ /
  // disabled Done while running).
  const jobRef = useRef(null); jobRef.current = job;
  const ref = useModal(
    () => { if (!jobRef.current || jobRef.current.status !== "running") onClose(); },
    () => !job && JSON.stringify({ f, ips, doms, auth, allGroups, gsel })
      !== JSON.stringify({ f: BLANK, ips: [], doms: [""], auth: "password", allGroups: true, gsel: [] }));

  // The hostnames clients connect to today, carried by the box that is about to
  // become the exit. Keeping the address means these records move to the new
  // server; changing it means new ones are added for it.
  const currentHosts = (state.domains || [])
    .filter(d => single && d.node_id === single.id)
    .map(d => d.hostname).filter(Boolean);
  const newHosts = doms.map(d => d.trim()).filter(Boolean);
  const dnsHosts = keepsAddress ? currentHosts : newHosts;
  const movingGroups = allGroups ? groups : groups.filter(g => gsel.includes(g.id));

  const start = async () => {
    for (let i = 0; i < STEPS.length - 1; i++) {
      if (!stepOK(i)) { setStep(i); return; }
    }
    setBusy(true);
    const body = {
      name: f.name, public_ips: ips,
      reality_sni: f.reality_sni || "", variant,
      group_ids: allGroups ? [] : gsel,
      ssh: { host: (f.ssh_host || ips[0] || "").trim(), port: parseInt(f.ssh_port) || 22, user: f.ssh_user, password: auth === "password" ? (f.ssh_password || "") : "", key_pem: auth === "key" ? (f.ssh_key || "") : "" },
      hardening: f.harden
        ? { enabled: true, sudo_user: f.h_sudo || "", ssh_pubkey: f.h_pubkey || "", ssh_port: parseInt(f.h_port) || 0, disable_root_login: !!f.h_root, disable_password_auth: !!f.h_pw, fail2ban: !!f.h_f2b, firewall: !!f.h_fw }
        : { enabled: false },
    };
    if (!keepsAddress) body.domains = newHosts; // keeping the address reuses the existing hostnames
    try { const r = await api("POST", "/api/cluster/add-entry", body); poll(r.job_id); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };
  const poll = (id) => {
    const tick = async () => {
      try {
        const s = await api("GET", "/api/jobs/" + id); setJob(s);
        if (s.status === "running") setTimeout(tick, 1500);
        else { setBusy(false); if (s.status === "succeeded") { notify(t("The deployment now runs on two servers"), "ok"); reload(); } }
      } catch (e) { notify(e.message, "err"); setBusy(false); }
    };
    tick();
  };

  const fld = (label, node, hint, wide, key) => html`<${LF} label=${label} hint=${hint} wide=${wide}
    empty=${key === "doms" ? newHosts.length === 0 : undefined}
    required=${["name", "role", "ips", "endpoint", "sni", "doms", "ssh_user", "ssh_secret"].includes(key) || (key === "h_pubkey" && f.harden && f.h_pw)}
    error=${key ? bad(key) : null}>${node}<//>`;
  const txt = (k, ph) => html`<input value=${f[k] || ""} placeholder=${ph || ""} onInput=${e => set(k, e.target.value)} />`;
  const chk = (k, label, half) => {
    const id = fieldId("chk2|" + k);
    return html`<div class=${"field check form-check" + (half ? "" : " f-wide")}>
      <label for=${id}><input id=${id} type=checkbox checked=${!!f[k]} onChange=${e => set(k, e.target.checked)} /><span>${label}</span></label></div>`;
  };

  if (job) {
    return html`<div class=overlay><div class=modal ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
      <header><h3 id=modal-title>${t("Splitting the deployment")}</h3>${job.status !== "running" && html`<button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button>`}</header>
      <div class=body>
        <div class=field><label>${t("Status")}</label><span class=${job.status === "succeeded" ? "ok" : job.status === "failed" ? "err" : "muted"}>${t(job.status)}</span></div>
        <pre class=config>${(job.log || []).join("\n")}${job.error ? "\nERROR: " + job.error : ""}</pre>
      </div>
      <div class=foot>
        ${job.status === "failed" && html`<button class=ghost onClick=${() => { setJob(null); setBusy(false); }}>${t("Back")}</button>`}
        <button disabled=${job.status === "running"} onClick=${onClose}>${job.status === "running" ? t("Working…") : t("Done")}</button>
      </div>
    </div></div>`;
  }

  const aName = single ? (single.name || single.id) : t("this server");
  const bName = f.name || t("the new server");

  const entryStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${tf("A new server goes in front and takes the connections. {name} keeps the panel, the database and the CA, and becomes the exit its traffic leaves from.", { name: aName })}</p>
    <div class=mfields>
      ${fld(t("Name"), txt("name", "Entry FR"), null, false, "name")}
      ${fld(t("Public IPs"), html`<${TagInput} value=${ips} onChange=${setIps} />`, t("the new server's public IP(s)"), true, "ips")}
    </div>`;

  const addressStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("The address clients use today belongs to this server. It can move to the new one, or the new one can get an address of its own.")}</p>
    <div class=mfields>
      ${fld(t("Connection address"), html`<select value=${variant} onChange=${e => set("variant", e.target.value)}>
        <option value=${KEEP}>${t("Keep the connection address")}</option>
        <option value=${MOVE}>${t("New connection address")}</option>
      </select>`, null, true, "variant")}
      ${keepsAddress
        ? html`<div class="note f-wide">
            <div>${t("The existing hostnames move to the new server, so every client keeps working with the config it already has.")}</div>
            <div style=${{ marginTop: ".35rem" }}>${t("Their DNS records have to point at the new server before this starts.")}</div>
            ${currentHosts.length > 0 && html`<div class=mono style=${{ marginTop: ".35rem" }}>${currentHosts.join(", ")}</div>`}
          </div>`
        : html`<${React.Fragment}>
            ${fld(t("New hostnames"), html`<div class=hostname-inputs>
              ${doms.map((d, i) => html`<div key=${i} style=${{ display: "flex", gap: ".4rem", marginBottom: ".3rem" }}>
                <input style=${{ flex: 1 }} value=${d} aria-label=${t("Hostname") + " " + (i + 1)} aria-required=${i === 0 && !newHosts.length || undefined} aria-invalid=${bad("doms") ? true : undefined} placeholder=${i === 0 ? "lk.example.com" : "example.com"} onInput=${e => { const val = e.target.value; setDoms(s => s.map((x, j) => j === i ? val : x)); }} />
                ${doms.length > 1 && html`<button class="ghost sm" onClick=${() => setDoms(s => s.filter((_, j) => j !== i))}>−</button>`}
              </div>`)}
              <button class="linkish sm" type=button onClick=${() => setDoms(s => [...s, ""])}>${t("+ add hostname")}</button>
            </div>`, t("the first one is what clients connect to; the rest go in the same certificate"), true, "doms")}
            <div class="note warn f-wide">${t("Every client gets a new config: the link or QR has to be handed out and imported again.")}</div>
          <//>`}
    </div>`;

  const egressStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${tf("{name} becomes the exit. Its Reality inbound is new, and the groups named here leave through it.", { name: aName })}</p>
    <div class=mfields>
      ${fld(t("Reality domain (SNI)"), txt("reality_sni", "www.example-cdn.com"), t("a third-party domain this server's traffic is made to look like; keys are generated for you"), true, "sni")}
      <div class=f-wide>
        <div class="field check form-check">
          <label for=cv-allgroups><input id=cv-allgroups type=checkbox checked=${allGroups} onChange=${e => setAllGroups(e.target.checked)} /><span>${t("All groups tunnel through the new exit")}</span></label>
        </div>
        ${!allGroups && html`<div class=${"chipset" + (bad("groups") ? " bad" : "")} style=${{ display: "flex", flexWrap: "wrap", gap: ".4rem", marginTop: ".3rem" }}>
          ${groups.map(g => html`<label key=${g.id} class=tag style=${{ cursor: "pointer" }}>
            <input type=checkbox checked=${gsel.includes(g.id)} onChange=${e => setGsel(s => e.target.checked ? [...s, g.id] : s.filter(x => x !== g.id))} /> ${g.name}</label>`)}
        </div>`}
        ${bad("groups") && html`<div class=fielderr role=alert>${bad("groups")}</div>`}
      </div>
    </div>`;

  const sshStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("How the panel reaches the new server to install it. Used once, and not stored.")}</p>
    <div class=mfields>
      ${fld(t("SSH host"), txt("ssh_host", ips[0] || ""), null, false, "ssh_host")}
      ${fld(t("Auth"), html`<select value=${auth} onChange=${e => setAuth(e.target.value)}><option value=password>${t("password")}</option><option value=key>${t("private key")}</option></select>`)}
      ${fld(t("SSH user"), txt("ssh_user"), null, false, "ssh_user")}
      ${fld(t("Port"), txt("ssh_port"))}
      ${auth === "password"
        ? fld(t("SSH password"), html`<input type=password value=${f.ssh_password || ""} onInput=${e => set("ssh_password", e.target.value)} />`, null, true, "ssh_secret")
        : fld(t("SSH private key (PEM)"), html`<textarea value=${f.ssh_key || ""} onInput=${e => set("ssh_key", e.target.value)}></textarea>`, null, true, "ssh_secret")}
    </div>`;

  const afterStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("How the server should be reachable once it is installed, instead of the access used to install it.")}</p>
    <div class=mfields>
      ${chk("harden", t("Harden the new server"))}
      ${f.harden && html`<${React.Fragment}>
        ${fld(t("Sudo user to create"), txt("h_sudo"))}
        ${fld(t("New SSH port"), txt("h_port"))}
        ${fld(t("SSH public key for the sudo user"), html`<textarea value=${f.h_pubkey || ""} onInput=${e => set("h_pubkey", e.target.value)} placeholder="ssh-ed25519 AAAA…"></textarea>`, t("required before password auth can be disabled"), true, "h_pubkey")}
        ${chk("h_root", t("Disable root login"), true)}
        ${chk("h_pw", t("Disable password login"), true)}
        ${chk("h_f2b", t("Install fail2ban"), true)}
        ${chk("h_fw", t("Enable firewall (ssh / 443 / 80)"), true)}
      <//>`}
    </div>
    <p class=hint style=${{ marginTop: "1.2rem" }}>${f.harden
      ? tf("After the install: log in as {user} on port {port}.", { user: f.h_sudo || "—", port: f.h_port || 22 })
      : t("After the install: the SSH access stays as it is now.")}</p>`;

  const line = (label, value) => html`<div class=sum-row><span class=muted>${label}</span><span>${value}</span></div>`;
  const checkStep = html`
    <p class=hint style=${{ marginTop: 0 }}>${t("Check the DNS first: the install asks for a certificate, and that only works once the name resolves to the right server.")}</p>
    <div class=note style=${{ borderLeftColor: "var(--accent)" }}>
      ${dnsHosts.length === 0
        ? html`<div>${t("No hostname yet — go back and name the address clients connect to.")}</div>`
        : html`<${React.Fragment}>
            <div>${keepsAddress ? t("Point these at the new server before starting:") : t("These have to resolve to the new server:")}</div>
            ${dnsHosts.map(h => html`<div key=${h} class=mono>${(ips[0] || "").includes(":") ? "AAAA" : "A"} ${h} → ${ips[0] || t("the new server's IP")}</div>`)}
          <//>`}
    </div>
    <div class=sum>
      ${line(t("New entry"), html`<b>${bName}</b>${ips.length ? " · " + ips.join(", ") : ""}`)}
      ${line(t("Becomes the exit"), html`<b>${aName}</b>${f.reality_sni ? " · " + f.reality_sni : ""}`)}
      ${line(t("Clients connect to"), dnsHosts.length ? dnsHosts[0] : "—")}
      ${line(t("Client configs"), keepsAddress
        ? t("keep working as they are")
        : html`<span class=warn>${t("have to be handed out and imported again")}</span>`)}
      ${line(tf("Leaving through {name}", { name: aName }), allGroups
        ? t("all groups")
        : (movingGroups.length ? movingGroups.map(g => g.name).join(", ") : t("no groups")))}
      ${line(t("After the install"), f.harden
        ? tf("log in as {user} on port {port}", { user: f.h_sudo || "—", port: f.h_port || 22 })
        : t("the SSH access stays as it is now"))}
    </div>`;

  const stepTitle = [t("The new server"), t("Connection address"), t("The exit and its groups"), t("SSH access"), t("Access after the install"), t("DNS and what changes")][step];
  const screen = [entryStep, addressStep, egressStep, sshStep, afterStep, checkStep][step];
  return html`<div class=overlay onClick=${e => e.target.classList.contains("overlay") && ref.close()}><div class="modal wide wizard-modal" ref=${ref} role=dialog aria-modal=true aria-labelledby=modal-title>
    <header>
      <h3 id=modal-title>${t("One server becomes two")}
        <span class=step-of>${tf("Step {n} of {total}", { n: step + 1, total: STEPS.length })} · ${stepTitle}</span>
      </h3>
      <button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${ref.close}>✕</button>
    </header>
    <div class=body>${screen}</div>
    <div class=foot>
      <button class=ghost onClick=${ref.close}>${t("Cancel")}</button>
      ${step > 0 && html`<button class=ghost onClick=${prev}>${t("Back")}</button>`}
      ${step < STEPS.length - 1
        ? html`<button onClick=${next}>${t("Next")}</button>`
        : html`<button disabled=${busy} onClick=${start}>${busy ? t("Starting…") : t("Start the migration")}</button>`}
    </div>
  </div></div>`;
}

/* ---------------- login ---------------- */
function Login({ onDone, lang, switchLang }) {
  const [u, setU] = useState(""); const [p, setP] = useState(""); const [err, setErr] = useState("");
  // "recover" swaps the form for the one-time-code one; "note" carries the
  // success line back to the sign-in view after a reset.
  const [mode, setMode] = useState("login");
  const [note, setNote] = useState("");
  const [code, setCode] = useState(""); const [np, setNp] = useState(""); const [np2, setNp2] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e) => {
    e.preventDefault();
    try { const r = await api("POST", "/api/auth/login", { username: u, password: p }); setCsrfToken(r.csrf_token); onDone(); }
    catch (e) { setErr(e.message); }
  };

  const recover = async (e) => {
    e.preventDefault();
    setErr("");
    if (np !== np2) { setErr(t("passwords do not match")); return; }
    setBusy(true);
    try {
      await api("POST", "/api/auth/recover", { username: u, code, new_password: np });
      setCode(""); setNp(""); setNp2(""); setP("");
      setNote(t("Password changed — sign in with the new password."));
      setMode("login");
    } catch (e) { setErr(e.message); }
    finally { setBusy(false); }
  };

  const brand = html`<div class=login-l>
    <div class=wordmark>Trust<span>Panel</span></div>
    <p class=lede>${t("Your network, your rules.")}</p>
  </div>`;

  const form = mode === "recover"
    ? html`<form class="card login-card" onSubmit=${recover}>
        <h2>${t("Recover access")}</h2>
        <p class=hint style=${{ margin: "-1.1rem 0 1.4rem" }}>${t("Ask the Telegram bot for a code: /recover")}</p>
        <${LF} label=${t("Username")}><input value=${u} onInput=${e => setU(e.target.value)} autocomplete=username /><//>
        <${LF} label=${t("Recovery code")}><input value=${code} onInput=${e => setCode(e.target.value)} placeholder="XXXX-XXXX" autocomplete=one-time-code /><//>
        <${LF} label=${t("New password")}><${PasswordInput} value=${np} onChange=${setNp} autocomplete="new-password" /><//>
        <${LF} label=${t("Confirm new password")}><${PasswordInput} value=${np2} onChange=${setNp2} autocomplete="new-password" /><//>
        ${err && html`<div class=err style=${{ marginBottom: ".6rem" }}>${err}</div>`}
        <button type=submit disabled=${busy}>${busy ? t("Working…") : t("Set new password")}</button>
        <div class=after>
          <button type=button class=link onClick=${() => { setMode("login"); setErr(""); }}>${t("Back to sign in")}</button>
          ${switchLang && html`<${LangToggle} lang=${lang} switchLang=${switchLang} />`}
        </div>
      </form>`
    : html`<form class="card login-card" onSubmit=${submit}>
        <h2>${t("Sign in")}</h2>
        <${LF} label=${t("Username")}><input value=${u} onInput=${e => setU(e.target.value)} autocomplete=username /><//>
        <${LF} label=${t("Password")}><${PasswordInput} value=${p} onChange=${setP} autocomplete="current-password" /><//>
        ${note && html`<div class=ok style=${{ marginBottom: ".6rem" }}>${note}</div>`}
        ${err && html`<div class=err style=${{ marginBottom: ".6rem" }}>${err}</div>`}
        <button type=submit>${t("Log in")}</button>
        <div class=after>
          <button type=button class=link onClick=${() => { setMode("recover"); setErr(""); setNote(""); }}>${t("Forgot password?")}</button>
          ${switchLang && html`<${LangToggle} lang=${lang} switchLang=${switchLang} />`}
        </div>
      </form>`;

  return html`<div class=login>${brand}<div class=login-r>${form}</div></div>`;
}

ReactDOM.createRoot(document.getElementById("root")).render(html`<${App} />`);
