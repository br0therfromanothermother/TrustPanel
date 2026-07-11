/* TrustPanel operator UI — React (via htm, no build step). */
const { useState, useEffect, useCallback, useRef } = React;
const html = htm.bind(React.createElement);

class ErrorBoundary extends React.Component {
  constructor(p){ super(p); this.state = { err: null }; }
  static getDerivedStateFromError(e){ return { err: e }; }
  render(){ return this.state.err
    ? html`<pre style=${{ color: "#f87171", padding: "1rem", whiteSpace: "pre-wrap" }}>${String(this.state.err && this.state.err.stack || this.state.err)}</pre>`
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
function setLangGlobal(l) { LANG = l; try { localStorage.setItem("tp_lang", l); } catch {} }
function t(s) { if (LANG === "en") return s; const d = RU[s]; return d == null ? s : d; }
// tf translates a template with {name} placeholders, then fills them in — so a
// localized sentence can keep its own word order around interpolated values.
function tf(s, vars) { let out = t(s); for (const k in vars) out = out.split("{" + k + "}").join(vars[k]); return out; }

// PasswordInput is a text input with a reveal toggle. Defaults to masked; the eye
// button flips it to plain text so the typist can check what they entered.
function PasswordInput({ value, onChange, placeholder = "", autocomplete }) {
  const [show, setShow] = useState(false);
  return html`<div class=pw>
    <input type=${show ? "text" : "password"} value=${value} placeholder=${placeholder}
      autocomplete=${autocomplete} onInput=${e => onChange(e.target.value)} />
    <button type=button class=reveal tabindex=-1 onClick=${() => setShow(!show)}
      aria-label=${show ? t("Hide") : t("Show")}>${show ? t("Hide") : t("Show")}</button>
  </div>`;
}
const RU = {
  // ---- nav / topbar ----
  "Overview": "Обзор", "Nodes": "Узлы", "HA": "Отказоустойчивость", "Clients": "Клиенты",
  "Groups": "Группы", "Users": "Пользователи", "Routing": "Маршрутизация", "Domains": "Домены",
  "Traffic": "Трафик", "Settings": "Настройки", "Logs": "Журнал",
  "Sync now": "Синхронизировать",
  "Syncing…": "Синхронизация…",
  "Rebuild config from the database and push it to every node now": "Пересобрать конфигурацию из базы и разослать её на все узлы",
  "Background sync cycles since the panel started": "Циклов фоновой синхронизации с запуска панели",
  "auto-syncs": "синхронизаций", "Log out": "Выйти", "Sign in": "Вход", "Log in": "Войти",
  // ---- cross-namespace lens (buried bootstrap toggle) ----
  "Advanced": "Расширенное",
  "Cross-namespace view": "Обзор всех неймспейсов",
  "Cross-namespace view on": "Обзор всех неймспейсов включён",
  "Cross-namespace view off": "Обзор всех неймспейсов выключен",
  "On — you are seeing every namespace.": "Включено — вы видите все неймспейсы.",
  "Off — you see only your own namespace.": "Выключено — вы видите только свой неймспейс.",
  "Turn on": "Включить", "Turn off": "Выключить",
  "Show every namespace?": "Показать все неймспейсы?",
  "Shows other tenants' clients, groups and routes across the panel. Turns off when you sign out.": "Показывает клиентов, группы и маршруты других владельцев во всей панели. Выключается при выходе из панели.",
  "Show all namespaces": "Показать все неймспейсы",
  "Viewing every namespace — click to return to your own": "Виден каждый неймспейс — нажмите, чтобы вернуться к своему",
  "Username": "Имя пользователя", "Password": "Пароль", "Loading…": "Загрузка…",
  "Language": "Язык",
  // ---- common actions ----
  "Save": "Сохранить", "Saving…": "Сохранение…", "Saved": "Сохранено", "Cancel": "Отмена",
  "Close": "Закрыть", "Add": "Добавить", "Edit": "Изменить", 
  "Delete": "Удалить", "Deleted": "Удалено", "Refresh": "Обновить", "Refreshed": "Обновлено",
  "Done": "Готово", "Back": "Назад", "Enabled": "Включено", "Disabled": "Выключено",
  "enable": "включить", "disable": "выключить", "Working…": "Выполняется…", "Starting…": "Запуск…",
  "Generate": "Сгенерировать", "Copy": "Копировать", "Copied": "Скопировано", "Test": "Проверить",
  "Testing…": "Проверка…", "yes": "да", "no": "нет", "none": "нет", "never": "никогда",
  // ---- status / health ----
  "no report yet": "нет данных", "status: OK": "статус: OK", "healthy": "Работает",
  "degraded": "Сбой", "unknown": "неизвестно",
  "Collecting metrics…": "Сбор метрик…", "no metrics yet": "пока нет метрик",
  // ---- overview ----
  "nodes": "узлы", "entry": "вход", "exit": "выход", "groups": "группы", "users": "пользователи",
  "policies": "правила", "Node monitoring": "Мониторинг узлов", "Control plane": "Управляющий узел",
  "Standby": "Резерв",
  "CPU": "CPU", "Memory": "Память",
  "Disk": "Диск", "updated": "обновлено", "up": "аптайм",
  // ---- needs attention ----
  "Needs attention": "Требует внимания", "All systems normal": "Всё в порядке",
  "agent unreachable": "агент недоступен", "external :443 unreachable": "внешний :443 недоступен",
  "staging TLS certificate (not browser-trusted)": "staging-сертификат TLS (не доверенный браузером)",
  "replication degraded": "репликация деградировала",
  "VPS payment overdue": "оплата VPS просрочена", "VPS payment due soon": "скоро оплата VPS",
  "management bot unreachable": "бот управления недоступен",
  "alert bot unreachable": "бот алертов недоступен",
  // ---- traffic ----
  "Per-user traffic": "Трафик по пользователям", "User": "Пользователь",
  "No users yet": "Пока нет пользователей",
  "All servers": "Все серверы",
  "cumulative since each node started, polled from the entry nodes.": "суммарно с момента запуска узлов, опрашивается с входных узлов.",
  "Other namespaces": "Другие неймспейсы", "Online": "Онлайн",
  // ---- nodes ----
  "Name": "Название", "Role": "Роль", "Public IPs": "Публичные IP", "Agent": "Агент",
  "Add server": "Добавить сервер",
  "Limits": "Лимиты", "Config": "Конфиг",
  "Convert to two-node": "Разделить на два узла",
  "Add an entry in front and flip this box to the exit (control plane stays here)": "Добавить вход перед этим узлом и сделать его выходом (управление остаётся здесь)",
  // ---- domains (subsection in nodes) ----
  "Issuer": "Издатель", "Purpose": "Назначение", "Node": "Узел", "Hostname": "Хост",
  "TLS": "TLS", "→ prod": "→ prod",
  "Switch this node's cert from staging to Let's Encrypt production and reissue": "Переключить сертификат узла со staging на боевой Let's Encrypt и перевыпустить",
  "Staging cert — not browser-trusted; promote to production": "Staging-сертификат — не доверен браузером; переключите на боевой",
  // ---- entity panel generic ----
  "How this works": "Как это работает",
  // ---- settings ----
  "Bots & alerts": "Боты и оповещения", "Account & security": "Аккаунт и безопасность",
  "Server": "Сервер", "Backup": "Бэкап",
  "Server defaults": "Настройки нового сервера",
  "Panel & schedules": "Панель и расписания",
  "Management bot": "Бот управления", "Alerts": "Оповещения",
  "Backup schedule & retention": "Расписание и хранение бэкапов",
  "Backup to Telegram": "Бэкап в Telegram",
  "Runs the commands and, by default, sends the alerts.": "Выполняет команды и по умолчанию шлёт оповещения.",
  "From @BotFather.": "От @BotFather.",
  "Infra alerts (payments, watchdog) — sent by the management bot.": "Инфра-оповещения (оплаты, watchdog) — шлёт бот управления.",
  "My bot access": "Мой доступ к боту",
  "Your Telegram id, so the bot answers you.": "Ваш Telegram id, чтобы бот вам отвечал.",
  "Backup alert bot": "Запасной бот оповещений",
  "The standby uses it to page you if the primary goes down.": "Резервный узел достучится им до вас, если основной упадёт.",
  "No separate management bot is set, so primary and backup alerts use this same bot — both test buttons check one channel. Set a management bot above for two independent channels.": "Отдельный бот управления не задан, поэтому основные и запасные оповещения используют этого же бота — обе кнопки «Тест» проверяют один канал. Задайте бота управления выше для двух независимых каналов.",
  "Backup bot token": "Токен запасного бота",
  "Age-encrypted snapshots sent to a private Telegram channel, so a copy survives losing every server.": "Снимки шифруются age и уходят в приватный Telegram-канал — копия переживёт потерю всех серверов.",
  "Generate with age-keygen; paste only the public key (age1…). Keep the private key OFFLINE.": "Сгенерируйте через age-keygen; вставьте только публичный ключ (age1…). Приватный ключ держите ОФЛАЙН.",
  "Show": "Показать", "Hide": "Скрыть",
  "Change your password": "Смена пароля", "Current password": "Текущий пароль",
  "New password": "Новый пароль", "Change password": "Сменить пароль",
  "Confirm new password": "Подтвердите новый пароль", "passwords do not match": "пароли не совпадают",
  "Confirm password": "Подтвердите пароль", "{label} does not match": "{label} не совпадает",
  "re-enter the password (leave both blank to auto-generate)": "повторите пароль (оставьте оба поля пустыми для автогенерации)",
  "master": "мастер", "standby": "резерв",
  "ACME contact email": "ACME-контакт (email)", "Default Reality SNI": "Reality SNI по умолчанию",
  "Apex domain": "Корневой домен",
  "Reconcile interval (seconds)": "Интервал синхронизации (секунды)",
  "Stats poll interval (seconds)": "Интервал опроса статистики (секунды)",
  "Billing check interval (hours)": "Интервал проверки оплат (часы)",
  "Warn N days before payment due": "Предупреждать за N дней до оплаты",
  "Warn N days before a client config expires": "Предупреждать за N дней до истечения конфига клиента",
  "Session lifetime (hours)": "Время жизни сессии (часы)",
  "Auto egress-failover after exit down (seconds)": "Авто-переключение выхода при отказе (секунды)",
  "A dead exit's groups auto-move to a healthy exit after this long. Min 60s.": "Группы отказавшего выхода автоматически переносятся на здоровый через это время. Минимум 60с.",
  "Online threshold (KB/min)": "Порог «онлайн» (КБ/мин)",
  "A client counts as online only above this traffic rate.": "Клиент считается онлайн только выше этой скорости трафика.",
  "this account": "этот аккаунт",
  // ---- logs ----
  "Event log": "Журнал событий", "All": "Все", "alert": "алерт", "admin": "действие",
  "backup": "бэкап", "system": "система", "No events yet": "Пока нет событий",
  "When": "Когда", "Kind": "Тип", "Message": "Сообщение", "Actor": "Кто",
  "critical": "критический", "warn": "предупреждение", "info": "инфо",
  "operator": "оператор", "read-only": "только чтение", "some online": "кто-то онлайн",
  "Plus {n} clients in other namespaces": "Ещё {n} клиентов в других неймспейсах",
  "Account": "Аккаунт", "Accounts": "Аккаунты", "Admin": "Админ", "Operator": "Оператор",
  "Members": "Участники", "Add member": "Добавить участника",
  "Make admin": "Сделать админом", "Make operator": "Сделать оператором",
  "Switch between admin and operator": "Переключить между админом и оператором",
  "Members of your namespace ({ns}) share its clients. Admins can manage members; operators only the clients.": "Участники вашего неймспейса ({ns}) делят его клиентов. Админы управляют участниками; операторы — только клиентами.",
  "Admins share the infrastructure. Each operator's namespace is isolated from the rest.": "Админы делят общую инфраструктуру. Неймспейс каждого оператора изолирован от остальных.",
  "Telegram": "Telegram", "Created": "Создан", "Change role": "Сменить роль",
  "Change {name}'s role to {role}?": "Сменить роль «{name}» на {role}?",
  "A password is required to make this account an admin.": "Чтобы сделать этот аккаунт админом, нужен пароль.",
  "A Telegram id is required to make this account an operator.": "Чтобы сделать этот аккаунт оператором, нужен Telegram id.",
  "username is required": "нужно указать имя пользователя",
  "password must be at least 8 characters": "пароль должен быть не короче 8 символов",
  "telegram id is required for the operator role": "для роли «оператор» обязателен Telegram id",
  "password is required to make this account an admin": "чтобы сделать аккаунт админом, укажите пароль",
  "telegram id is required to make this account an operator": "чтобы сделать аккаунт оператором, укажите Telegram id",
  "Telegram id": "Telegram id", "optional": "необязательно",
  "Your Telegram id": "Ваш Telegram id",
  "Save Telegram binding": "Сохранить привязку Telegram", "Add account": "Добавить аккаунт",
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
  "This domain has no node to promote": "У этого домена нет узла для переключения",
  "Switch this node's certificate from staging to Let's Encrypt production and reissue now?": "Переключить сертификат узла со staging на боевой Let's Encrypt и перевыпустить сейчас?",
  "Reissued from production": "Перевыпущено с боевого УЦ",
  // ---- schemas: singulars ----
  "node": "узел", "group": "группу", "user": "пользователя", "route policy": "правило",
  "domain": "домен",
  // ---- schemas: nodes ----
  "Agent address": "Адрес агента",
  "Reality port": "Порт Reality", "VLESS UUID": "VLESS UUID",
  "Reality target SNI (borrowed CDN)": "Reality target SNI (заимствованный CDN)",
  "Reality public key": "Публичный ключ Reality", "Reality private key": "Приватный ключ Reality",
  "Reality short_id": "Reality short_id",
  "host:8443 (the panel reaches the agent here)": "host:8443 (по этому адресу панель связывается с агентом)",
  "server's public IP address(es)": "публичный(е) IP сервера",
  // ---- schemas: groups ----
  "Default exit": "Выход по умолчанию", "Default exit node": "Узел-выход по умолчанию", "local": "локально",
  "local (exit from the entry node)": "локально (выход с входного узла)",
  "where the group exits by default; “local” = direct egress from the entry node": "куда группа выходит по умолчанию; «локально» = прямой выход с входного узла",
  // ---- schemas: users ----
  "Display name": "Отображаемое имя", "Group": "Группа",
  "the join-key (TrustTunnel credentials + sing-box auth_user)": "ключ подключения (учётка TrustTunnel + auth_user sing-box)",
  "auto-generated on create; on edit leave blank to keep the current password (min 12 chars if set explicitly)": "генерируется при создании; при изменении оставьте пустым, чтобы сохранить текущий пароль (минимум 12 символов, если задаёте вручную)",
  "Regenerate password (issue a new secret)": "Перевыпустить пароль (новый секрет)",
  // ---- schemas: routing ----
  "Status": "Статус", "Action": "Действие", "Match": "Совпадение",
  "Exit": "Выход", "Prio": "Приоритет", "enabled": "включено", "disabled": "выключено",
  "Priority": "Приоритет", "Applies to group": "Применять к группе",
  "Match domains / zones": "Совпадение по доменам / зонам", "Match countries (geoip)": "Совпадение по странам (geoip)",
  "Match geosite categories": "Совпадение по категориям geosite", "Match CIDRs": "Совпадение по CIDR",
  "Except domains": "Кроме доменов", "Exit node": "Узел-выход", "Fallback": "Запасной маршрут",
  "Fallback exit": "Запасной выход",
  "Rules": "Правила", "Show routing rules targeting this group": "Показать правила маршрутизации для этой группы",
  "higher = evaluated first": "выше = проверяется раньше",
  "default = all users; or pick a specific group": "по умолчанию = все пользователи; либо выберите группу",
  "all users": "все пользователи", "— all users (any group) —": "— все пользователи (любая группа) —",
  "all my clients": "все мои клиенты", "— all my clients (any group) —": "— все мои клиенты (любая группа) —",
  "default = all my clients; or pick a specific group": "по умолчанию = все мои клиенты; либо выберите группу",
  "required for action=exit": "обязательно для действия exit",
  "by destination IP, which can't be faked — search for a country": "по IP назначения — его не подделать; начните вводить страну",
  "suffix match — “example.com” also matches *.example.com; “ru” matches *.ru. No wildcards/regex.": "совпадение по суффиксу: «example.com» ловит и *.example.com, «ru» — все *.ru. Без масок и регулярок.",
  "by domain category — search e.g. google, netflix, category-ads, geolocation-cn": "по категории доменов — попробуйте google, netflix, category-ads, geolocation-cn",
  "e.g. 10.0.0.0/8, 1.1.1.1": "например, 10.0.0.0/8, 1.1.1.1",
  "domains that bypass this policy and take the group's normal exit (e.g. bank.ru). Needs a group default exit.": "домены, которые обходят это правило и идут через обычный выход группы (например, bank.ru). Нужен выход по умолчанию у группы.",
  // ---- routing help (the ? panel) ----
  "How a routing rule is read": "Как читается правило маршрутизации",
  "Infra rules (network-wide) run before namespace rules. Within a level the higher priority wins, and the first rule that matches a request decides.": "Инфра-правила (на всю сеть) проверяются раньше правил неймспейса. Внутри уровня выигрывает больший приоритет, а решает первое правило, подошедшее под запрос.",
  "Infra rules apply across the whole network (admin only); namespace rules apply to your own clients.": "Инфра-правила действуют на всю сеть (только админ); правила неймспейса — на ваших клиентов.",
  // ---- routing levels (variant A) ----
  "Level": "Уровень",
  "Infra": "Инфра",
  "Namespace": "Неймспейс",
  "All namespaces": "Все неймспейсы",
  "Namespace (this tenant)": "Неймспейс (ваш)",
  "Infra (network-wide)": "Инфра (вся сеть)",
  "Namespace = applies to your clients; Infra = applies network-wide (admin only)": "Неймспейс = действует на ваших клиентов; Инфра = на всю сеть (только админ)",
  "(infra mandate — overrides every namespace)": "(инфра-правило — важнее любого неймспейса)",
  "(infra safety-net — checked before namespace rules)": "(инфра-страховка — проверяется раньше правил неймспейса)",
  "Rules of this level checked first: {n}.": "Правил этого уровня проверяется раньше: {n}.",
  "Inside one rule the match types combine with AND (domains AND countries AND CIDRs); values within a single type are OR. Need OR across types? Make two rules.": "Внутри одного правила типы условий объединяются по И (домены И страны И CIDR), а значения внутри одного типа — по ИЛИ. Нужно ИЛИ между типами — сделайте два правила.",
  "geoip matches the destination IP (resolved over DNS), not the domain text. A .ru domain on a foreign IP won't match geoip:ru — use “Match domains” for that.": "geoip смотрит на IP назначения (его резолвит DNS), а не на текст домена. Домен .ru на зарубежном IP под geoip:ru не попадёт — для этого есть «Совпадение по доменам».",
  "“Except domains” lets the listed hosts skip this rule and use the group's normal exit (the group needs a default exit).": "«Кроме доменов» позволяет перечисленным хостам пропустить это правило и уйти через обычный выход группы (у группы должен быть выход по умолчанию).",
  "Use the Route tester to check which rule wins for a given group and host.": "Кнопка «Тест маршрута» покажет, какое правило выиграет для конкретной группы и хоста.",
  // ---- routing live summary ----
  "all destinations": "любые адреса",
  "go direct, bypassing the VPN": "пойдёт напрямую, мимо VPN",
  "be blocked": "будет заблокирован",
  "exit via {name}": "выйдет через {name}",
  "(pick an exit node)": "(выберите узел-выход)",
  "will {how}": "{how}",
  "Traffic that matches nothing takes the group's default exit.": "Трафик, не подошедший ни под одно правило, идёт через выход по умолчанию для группы.",
  "“{name}” has a higher priority and also catches {on}{more} — that traffic may go there first.": "«{name}» с более высоким приоритетом тоже ловит {on}{more} — этот трафик может уйти туда раньше.",
  "This rule is off and won't be applied.": "Правило выключено и применяться не будет.",
  // ---- route tester ----
  "Which rule decides a group's traffic to a host.": "Какое правило решает трафик группы к хосту.",
  "Host to test (IP or domain)": "Хост для проверки (IP или домен)",
  "Enter an IP or domain to test": "Введите IP или домен для проверки",
  "e.g.": "напр.",
  "Goes out via": "Выходит через",
  "Decided by": "Решило",
  "Resolved to": "Разрешилось в",
  "How it was decided (top to bottom, first match wins)": "Как принято решение (сверху вниз, побеждает первое совпадение)",
  // ---- schemas: domains ----
  "Entry node": "Входной узел", "main-fallback": "main-fallback", "fallback-site": "fallback-site",
  // ---- settings extra ----
  "Total": "Всего", "set": "задано", "leave blank to keep": "оставьте пустым, чтобы сохранить",
  "paste bot token": "вставьте токен бота",
  "Pre-filled when you add a new server.": "Подставляются при добавлении нового сервера.",
  "Applied live — no restart needed. Blank/0 uses the built-in default.": "Применяется на лету — без перезапуска. Пусто/0 = встроенное значение по умолчанию.",
  "Send alerts": "Слать оповещения",
  "Bot token": "Токен бота",
  "Alert chat ID": "Chat ID для оповещений",
  "Local snapshots (database + CA), one timer per node.": "Локальные снапшоты (база + CA), по таймеру на каждом узле.",
  "Local backup enabled": "Локальный бэкап включён", "Run every (hours)": "Запускать каждые (часы)",
  "Copies to keep": "Сколько копий хранить", "Verify-restore drill enabled": "Проверка восстановления включена",
  "Verify every (days)": "Проверять каждые (дни)",
  "Backup chat ID": "Chat ID для бэкапов", "age recipient (public key)": "age-получатель (публичный ключ)",
  "Part size (bytes, 0 = default ~45 MiB)": "Размер части (байты, 0 = ~45 МиБ по умолчанию)",
  "Test uses the saved settings — save first, then test.": "Проверка использует сохранённые настройки — сначала сохраните, потом проверяйте.",
  // ---- account validation/notify ----
  "new password must be at least 8 characters": "новый пароль должен быть не короче 8 символов",
  "username required and password must be at least 8 characters": "нужно имя пользователя и пароль не короче 8 символов",
  // ---- status chip ----
  "reachable": "доступен", "token rejected": "токен отклонён", "unreachable": "недоступен",
  "not configured": "не настроено",
  // ---- form modal ----
  "What this rule does": "Что делает это правило",
  // ---- HA ----
  "High availability": "Отказоустойчивость (HA)",
  "The master runs the control plane — Postgres, the CA, the panel. The standby is a second exit node, ready to take over. Failover is manual: once the master goes down, the backup alert bot sends the exact promote command. RECOVERY.md, shipped with every backup, has the same steps in case the alert doesn't arrive.": "Мастер обслуживает управляющий узел — Postgres, CA, панель. Резерв — второй выходной узел, готовый его заменить. Переключение ручное: как только основной падает, запасной бот оповещений присылает точную команду promote. В RECOVERY.md, который кладётся в каждый бэкап, те же шаги — на случай, если алерт не дойдёт.",
  "Sets up a Postgres replica + CA on an exit so it can take over. The data plane on that exit keeps running.": "Создаёт на выходе реплику Postgres + CA, чтобы он мог взять управление на себя. Трафик на этом выходе продолжает идти.",
  "Active control plane": "Активный управляющий узел",
  "no primary set — mark one, or this deployment doesn't use a standby": "основной не задан — отметьте узел как primary, либо в этом развёртывании резерв не используется",
  "No standby — failover not possible. Add one below.": "Резерва нет — переключение невозможно. Добавьте его ниже.",
  "Ready to fail over": "Готов к переключению", "Standby degraded": "Резерв деградировал",
  "Standby — replication starting": "Резерв — репликация запускается",
  "How to fail over": "Как переключиться",
  "If the primary fails, fail over to this standby — only once the primary is confirmed gone:": "Если основной узел откажет, переключитесь на этот резерв — только когда primary точно недоступен:",
  "SSH into {name} ({ip}).": "Зайдите по SSH на {name} ({ip}).",
  "Run as root:": "Выполните под root:",
  "The panel comes back up on this node; point your tunnel at it.": "Панель поднимется на этом узле; направьте на него своё подключение.",
  "{name} is now a standby": "{name} теперь резерв", "{name} replica rebuilt": "Реплика на {name} пересоздана",
  "Add standby failed": "Не удалось добавить резерв", "Rebuild failed": "Не удалось пересоздать реплику",
  "replication: checking…": "репликация: проверка…", "warning": "предупреждение",
  "streaming": "стриминг", "behind": "отставание",
  "Wipe this replica's PGDATA and re-seed it from the current primary (use if its Postgres died or diverged)": "Стереть PGDATA реплики и пересоздать её с текущего primary (если её Postgres умер или разошёлся)",
  "Rebuild replica": "Пересоздать реплику", "Add a standby": "Добавить резерв",
  "No eligible exit nodes. Add a second exit node first (Nodes → Add server), then come back here.": "Нет подходящих выходных узлов. Сначала добавьте второй выход (Узлы → Добавить сервер), затем вернитесь сюда.",
  "Make standby": "Сделать резервом", "Hide CLI": "Скрыть CLI", "Manual (CLI)": "Вручную (CLI)",
  "Break-glass: run this as root on the primary if the panel is unavailable. Same result as the button.": "На крайний случай: выполните это под root на primary, если панель недоступна. Результат тот же, что у кнопки.",
  "Copy command": "Скопировать команду", "Add-standby status": "Статус добавления резерва",
  "Dismiss": "Скрыть",
  // ---- modals chrome ----
  "Node specs": "Ресурсы узла", "Client config": "Конфиг клиента",
  "Adding the server": "Добавление сервера", "Add a new server": "Добавление нового сервера",
  "Converting to two-node": "Разделение на два узла",
  "Test route": "Проверить маршрут", "Download .toml": "Скачать .toml", "Link": "Ссылка",
  "Add server (submit)": "Добавить", "Convert": "Разделить", "Adding…": "Добавление…",
  // ---- generic confirm ----
  "OK": "ОК",
  // ---- t() gaps ----
  "Synced — rev {revision}": "Синхронизировано — ревизия {revision}", "no nodes": "нет узлов",
  // ---- inline confirmations (HA / delete) ----
  "Make “{name}” a control-plane standby?": "Сделать «{name}» резервом управляющего узла?",
  "Sets up replication on this exit and copies the CA private key onto it.": "Настраивает репликацию на этом узле и копирует на него приватный ключ CA.",
  "The primary's Postgres restarts briefly; this exit's client traffic isn't affected.": "Postgres на основном узле ненадолго перезапустится; клиентский трафик этого узла не будет затронут.",
  "The node becomes a standby once this finishes.": "После этого узел будет считаться резервным.",
  "Rebuild the replica on “{name}”?": "Пересоздать реплику на «{name}»?",
  "Wipes this replica and re-seeds it from the current primary — use if its Postgres died or diverged.": "Стирает данные этого резерва и пересоздаёт их с основного узла — используйте, если его Postgres упал или разошёлся с основным.",
  "Data is re-copied fresh from the primary; this exit's client traffic isn't affected.": "Данные будут заново скопированы с основного узла; клиентский трафик этого узла не будет затронут.",
  "Delete {name}?": "Удалить {name}?",
  // ---- ConfigModal ----
  "no entry nodes": "нет входных узлов",
  "landing link (renders QR in browser)": "ссылка-лендинг (рисует QR в браузере)",
  "QR code": "QR-код",
  "Scan with the TrustTunnel app to import.": "Отсканируйте приложением TrustTunnel для импорта.",
  "Link too long to render as a QR": "Ссылка слишком длинная для QR",
  "No entry nodes yet — add one before this config can connect.": "В сети нет входных узлов — добавьте узел, иначе этот конфиг не подключится.",
  "This client is disabled — the config builds but will be rejected until you enable it.": "Клиент выключен — конфиг соберётся, но не подключится, пока вы его не включите.",
  "This client has expired — extend its expiry or it cannot connect.": "Срок клиента истёк — продлите срок, иначе подключение невозможно.",
  "The selected entry is unhealthy — the config builds but may not connect until it recovers.": "Выбранный входной узел нездоров — конфиг соберётся, но может не подключиться, пока узел не восстановится.",
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
  "Disable password auth (needs the key above)": "Запретить вход по паролю (нужен ключ выше)",
  "Install fail2ban": "Установить fail2ban",
  "Enable firewall (ssh / 443 / 80)": "Включить файрвол (ssh / 443 / 80)",
  "used once for install, not stored": "используется один раз для установки, не сохраняется",
  "required before password auth can be disabled": "нужен, прежде чем отключать вход по паролю",
  // ---- ProvisionModal ----
  "Reality SNI is required for an exit": "Для выхода нужен Reality SNI",
  "Node installed": "Узел установлен",
  "the server's public IP(s)": "публичный(е) IP сервера",
  "TLS hostname served by TrustTunnel": "хост TLS, который обслуживает TrustTunnel",
  "borrowed third-party domain; Reality keys + uuid are generated automatically": "заимствованный сторонний домен; ключи Reality и uuid генерируются автоматически",
  "Also make this exit a control-plane standby (HA)": "Сделать этот выход ещё и резервом управления (HA)",
  "After install, layers a Postgres replica + CA on this exit via the primary's agent, so it can take over the panel. Staged serve stays disabled until you promote.": "После установки разворачивает на этом выходе реплику Postgres + CA через агент primary, чтобы он мог взять панель на себя. Застейдженный serve остаётся выключенным до повышения.",
  "Harden server": "Усилить защиту сервера",
  // ---- ConvertModal ----
  "Reality SNI is required (A becomes the exit)": "Нужен Reality SNI (узел A становится выходом)",
  "Variant 1 needs at least one domain for B's certificate": "Для варианта 1 нужен хотя бы один домен для сертификата B",
  "Converted to two-node": "Разделено на два узла",
  "this box": "этот узел",
  "Adds a new entry B in front and flips {name} to the exit. The control plane (panel, database, CA) stays here — nothing moves. B is brought up and verified before the flip, so if it fails this box keeps serving.": "Добавляет новый вход B перед текущим узлом и превращает {name} в выход. Управляющий узел (панель, база, CA) остаётся здесь — ничего не переносится. B поднимается и проверяется до переключения, поэтому при сбое этот узел продолжает обслуживать клиентов.",
  "New entry name": "Имя нового входа", "New entry public IPs": "Публичные IP нового входа",
  "the new server's public IP(s)": "публичный(е) IP нового сервера",
  "Migration": "Миграция",
  "variant 1 — new hostname for B (re-issue client configs)": "вариант 1 — новый хост для B (перевыпуск клиентских конфигов)",
  "variant 2 — reuse the same domain (move DNS to B first; no re-issue)": "вариант 2 — тот же домен (сначала переключите DNS на B; без перевыпуска)",
  "Reuses A's existing hostnames — move BOTH the endpoint + apex DNS records to B's IP before running. No domain input needed.": "Переиспользует существующие хосты A — перед запуском переключите ОБЕ DNS-записи (endpoint и apex) на IP B. Поле домена не нужно.",
  "Add B's DNS record(s) before running; clients get new configs/QR pointing at B.": "Перед запуском добавьте DNS-записи для B; клиенты получат новые конфиги/QR на B.",
  "New entry domain(s)": "Домен(ы) нового входа", "+ add domain": "+ добавить домен",
  "first = client endpoint (main-fallback); the rest are extra SANs in the same certificate": "первый = клиентский endpoint (main-fallback); остальные — дополнительные SAN в том же сертификате",
  "Reality SNI for the exit": "Reality SNI для выхода",
  "borrowed third-party SNI for A's new Reality inbound; keys are generated": "заимствованный сторонний SNI для нового Reality-входа A; ключи генерируются",
  "Egress groups": "Группы выхода",
  "All groups tunnel through the new exit": "Все группы выходят через новый выход",
  "SSH host (B)": "SSH-хост (B)", "Harden the new entry": "Усилить защиту нового входа",
  // ---- LimitsModal ----
  "VPS limits": "Лимиты VPS",
  "Shown on Overview. Blank = unset.": "Отображается на «Обзоре». Пусто = не задано.",
  "CPU cores (vCPU)": "Ядра CPU (vCPU)", "Memory (GB)": "Память (ГБ)", "Disk (GB)": "Диск (ГБ)",
  "Monthly traffic (TB)": "Трафик в месяц (ТБ)",
  "e.g. 4 for 4 GB": "например, 4 — это 4 ГБ", "e.g. 6 for 6 TB/mo": "например, 6 — это 6 ТБ/мес",
  "Billing": "Оплата", "Currently paid until {date}.": "Сейчас оплачено до {date}.",
  "Payment term": "Срок оплаты", "— no billing —": "— без оплаты —",
  "1 month": "1 месяц", "3 months": "3 месяца", "6 months": "6 месяцев", "12 months": "12 месяцев",
  "Paid on": "Оплачено", "paid until {x} (+{term} mo)": "оплачено до {x} (+{term} мес)",
  "Enter the paid-on date as dd/mm/yyyy": "Введите дату оплаты в формате дд/мм/гггг",
  "unset": "не задано", "dd/mm/yyyy": "дд/мм/гггг",
  "Expires": "Истекает", "blank = never; access stops at the end of this day (UTC)": "пусто = бессрочно; доступ прекращается в конце этого дня (UTC)",
  "Enter {label} as dd/mm/yyyy": "Введите «{label}» в формате дд/мм/гггг",
  "Drain": "Вывести", "Resume": "Вернуть", "Drained": "Выведен на обслуживание", "Resumed": "Возвращён в работу",
  "drain": "обслуж.", "in maintenance": "на обслуживании",
  "in maintenance — drained from rotation": "на обслуживании — выведен из ротации",
  "Take this node out of rotation for maintenance (or put it back)": "Вывести узел из ротации на обслуживание (или вернуть)",
  "Drain {name}? Traffic that targets it egresses locally and new client configs warn until you resume.": "Вывести {name}? Трафик к нему пойдёт через локальный выход, а новые клиентские конфиги будут с предупреждением, пока не вернёте.",
  "Reassign egress": "Переназначить выход", "Egress reassigned": "Выход переназначен", "choose exit…": "выберите выход…", "Reassign": "Переназначить",
  "{n} group(s) egress here — move to:": "{n} групп(ы) выходят здесь — перенести на:",
  "Move egress of {n} group(s) from {name} to another exit and drain this node?": "Перенести выход {n} групп(ы) с {name} на другой узел и вывести этот из ротации?",
  "Now": "Сейчас", "active now": "активен сейчас", "idle": "простаивает",
  "{n} active now": "активны сейчас: {n}",
  "“now” = last {m} min": "«сейчас» = за последние {m} мин",
  "no traffic in this window": "нет трафика за этот период",
  "no metrics in this window": "нет метрик за этот период",
  "peak {b} per bucket": "пик {b} на интервал",
  "peak {v}": "пик {v}",
  "CPU load": "Загрузка CPU", "Memory used": "Занято памяти",
  "Uploaded": "Отправлено", "Downloaded": "Получено", "Inbound": "Входящий", "Outbound": "Исходящий",
  "Click the row to show this client's traffic graph": "Нажмите на строку, чтобы открыть график трафика клиента",
  "Show graph": "Показать график", "Hide graph": "Скрыть график",
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
  "account not found": "аккаунт не найден",
  "a control-plane standby must be an exit node": "резерв управляющего узла должен быть выходным узлом",
  "alert channel is not configured (enable it and set a chat id)": "канал оповещений не настроен (включите его и задайте chat id)",
  "authentication required": "требуется аутентификация",
  "backup channel is not configured (set the alert bot token and a backup chat id)": "канал бэкапов не настроен (задайте токен бота оповещений и chat id для бэкапов)",
  "bot unreachable": "бот недоступен",
  "cannot create accounts in another namespace": "нельзя создавать аккаунты в чужом неймспейсе",
  "cannot demote the last admin of a namespace while members remain": "нельзя понизить последнего админа неймспейса, пока в нём есть участники",
  "cannot demote the last owner": "нельзя понизить последнего владельца",
  "confirm must equal node_id (the UI confirmation step was not completed)": "подтверждение должно совпадать с node_id (шаг подтверждения в UI не выполнен)",
  "could not resolve the control-plane primary node": "не удалось определить основной управляющий узел",
  "current password is incorrect": "текущий пароль неверный",
  "decode body": "не удалось разобрать тело запроса",
  "domain not found": "домен не найден",
  "entry query parameter is required": "требуется параметр entry",
  "network and guard policies are managed by admins": "правила уровня сети и guard управляются администраторами",
  "generate reality keys": "не удалось сгенерировать ключи Reality",
  "invalid credentials": "неверные учётные данные",
  "invalid or expired session": "сессия недействительна или истекла",
  "job not found": "задача не найдена",
  "make_standby: could not resolve the control-plane primary to replicate from": "make_standby: не удалось определить основной узел для репликации",
  "make_standby requires an exit node (a standby is a control-plane replica + exit)": "make_standby требует выходной узел (резерв — это реплика управления + выход)",
  "management bot is not configured": "бот управления не настроен",
  "member management requires an admin role": "управление участниками требует роли админа",
  "missing or invalid CSRF token": "отсутствует или неверный CSRF-токен",
  "name, public_ips and ssh.host are required": "обязательны name, public_ips и ssh.host",
  "node not found": "узел не найден",
  "node_id is required": "требуется node_id",
  "node query parameter is required": "требуется параметр node",
  "no single entry node to convert (expected one entry node holding the control plane)": "нет одиночного входного узла для разделения (ожидался один вход с управляющим узлом)",
  "no users selected": "не выбрано ни одного пользователя",
  "only admins can install a node as a control-plane standby": "только админы могут добавить узел как резерв управления",
  "operators cannot create nodes directly — use Add server": "операторы не могут создавать узлы напрямую — используйте «Добавить сервер»",
  "primary and standby must both have a public IP": "у основного и резервного узла должен быть публичный IP",
  "reality_sni is required for an exit node": "для выходного узла обязателен reality_sni",
  "reality_sni is required (the SNI A's new Reality inbound borrows)": "обязателен reality_sni (SNI, который займёт новый Reality-inbound узла A)",
  "remote provisioning is not configured on this panel": "удалённый провижининг не настроен на этой панели",
  "role must be admin or operator": "роль должна быть admin или operator",
  "role must be entry or exit": "роль должна быть entry или exit",
  "send failed": "не удалось отправить",
  "session no longer valid": "сессия больше недействительна",
  "standby node has no agent address": "у резервного узла нет адреса агента",
  "target (IP or domain) is required": "требуется цель (IP или домен)",
  "that client belongs to another namespace": "этот клиент принадлежит другому неймспейсу",
  "that group belongs to another namespace": "эта группа принадлежит другому неймспейсу",
  "that policy belongs to another namespace": "это правило принадлежит другому неймспейсу",
  "the standby must differ from the primary": "резерв должен отличаться от основного узла",
  "this action is available to admins only": "это действие доступно только администраторам",
  "too many failed login attempts; try again later": "слишком много неудачных попыток входа; попробуйте позже",
  "unknown channel": "неизвестный канал",
  "username required and password must be at least 8 chars": "нужно имя пользователя, пароль не короче 8 символов",
  "user query parameter is required": "требуется параметр user",
  "variant 1 (new hostname) requires at least one domain for B's certificate": "вариант 1 (новый хост) требует хотя бы один домен для сертификата B",
  "variant 2 (reuse domain) needs A to already serve at least one domain to hand over": "вариант 2 (переиспользование домена) требует, чтобы у A уже был хотя бы один домен для передачи",
  "variant must be 1 (new hostname) or 2 (reuse domain)": "вариант должен быть 1 (новый хост) или 2 (переиспользование домена)",
  "you cannot delete the account you are signed in as": "нельзя удалить аккаунт, под которым вы вошли",
  "you do not own this node": "этот узел вам не принадлежит",
  "you cannot manage members of that namespace": "вы не можете управлять участниками этого неймспейса",
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
// can't wander to the page behind. The returned ref goes on the .modal element
// (also tagged role=dialog/aria-modal for screen readers).
function useModal(onClose) {
  const ref = useRef(null);
  useEffect(() => {
    const focusable = () => ref.current
      ? Array.from(ref.current.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])')).filter(el => el.offsetParent !== null)
      : [];
    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); onClose(); return; }
      if (e.key !== "Tab") return;
      const els = focusable();
      if (els.length === 0) return;
      const first = els[0], last = els[els.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    const els = focusable();
    const target = els.find(el => ["INPUT", "SELECT", "TEXTAREA"].includes(el.tagName)) || els[0];
    if (target) setTimeout(() => { try { target.focus(); } catch {} }, 0);
    return () => document.removeEventListener("keydown", onKey, true);
  }, []);
  return ref;
}

function ConfirmModal({ req, onClose }) {
  const done = (v) => { onClose(); req.resolve(v); };
  const ref = useModal(() => done(false));
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && done(false)}>
      <div class=modal ref=${ref} role=dialog aria-modal=true>
        <header><h3>${req.title}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${() => done(false)}>✕</button></header>
        <div class=body>
          ${(req.lines || []).map((l, i) => html`<p key=${i} style=${{ margin: ".35rem 0", lineHeight: 1.45 }}>${l}</p>`)}
        </div>
        <div class=foot>
          <button class=ghost onClick=${() => done(false)}>${t("Cancel")}</button>
          <button class=${req.danger ? "danger" : ""} onClick=${() => done(true)}>${req.confirmLabel || t("OK")}</button>
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
const slug = (s) => String(s || "").toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 24);
const rand4 = () => Math.random().toString(16).slice(2, 6);
const genId = (base, fb) => (slug(base) || fb) + "-" + rand4();
const countryCode = (t) => { const m = String(t).match(/\(([a-zA-Z]{2})\)\s*$/); return (m ? m[1] : t).toLowerCase().trim(); };
function nameOf(state, kind, id) {
  if (!id) return "";
  const it = (state[kind] || []).find(x => x.id === id);
  return it ? (it.name || it.display_name || it.username || it.hostname || id) : id;
}
// Match tokens of a route policy (domains/geoip/geosite/cidrs), normalized.
function policyMatchTokens(p) {
  return [
    ...(p.match_domains || []),
    ...(p.match_geoip || []).map(g => "geoip:" + g),
    ...(p.match_geosite || []).map(g => "geosite:" + g),
    ...(p.match_cidrs || []),
  ].map(x => String(x).trim().toLowerCase()).filter(Boolean);
}
// Two policies' group scopes can match the same traffic when either is "all users" ("").
const policyGroupsOverlap = (a, b) => !a || !b || a === b;
const COUNTRIES = [["ru","Russia"],["us","United States"],["de","Germany"],["nl","Netherlands"],["gb","United Kingdom"],["fr","France"],["fi","Finland"],["se","Sweden"],["pl","Poland"],["ua","Ukraine"],["ir","Iran"],["cn","China"],["hk","Hong Kong"],["jp","Japan"],["sg","Singapore"],["kr","South Korea"],["in","India"],["tr","Turkey"],["ae","United Arab Emirates"],["ca","Canada"],["br","Brazil"],["au","Australia"],["it","Italy"],["es","Spain"],["ch","Switzerland"],["at","Austria"],["cz","Czechia"],["ro","Romania"],["kz","Kazakhstan"],["by","Belarus"],["ge","Georgia"],["am","Armenia"],["az","Azerbaijan"],["lt","Lithuania"],["lv","Latvia"],["ee","Estonia"],["no","Norway"],["dk","Denmark"],["be","Belgium"],["ie","Ireland"],["pt","Portugal"],["gr","Greece"],["bg","Bulgaria"],["rs","Serbia"],["md","Moldova"],["il","Israel"],["sa","Saudi Arabia"],["eg","Egypt"],["za","South Africa"],["mx","Mexico"],["ar","Argentina"],["th","Thailand"],["vn","Vietnam"],["id","Indonesia"],["my","Malaysia"],["ph","Philippines"],["tw","Taiwan"],["uz","Uzbekistan"],["kg","Kyrgyzstan"],["tj","Tajikistan"],["hu","Hungary"],["sk","Slovakia"],["hr","Croatia"]];
// Common sing-geosite / v2ray geosite categories (domain-category lists).
// Curated list of REAL sing-geosite rule-set categories (verified to exist as
// geosite-<name>.srs in SagerNet/sing-geosite). Free text is still allowed; an
// unknown category surfaces as a clear reconcile error.
const COUNTRY_LABELS = COUNTRIES.map(([c, n]) => n + " (" + c + ")");
const GEOSITE = ["google","youtube","telegram","signal","whatsapp","discord","twitter","x","facebook","instagram","meta","tiktok","netflix","disney","hbo","spotify","twitch","reddit","linkedin","pinterest","github","gitlab","openai","anthropic","cloudflare","apple","microsoft","amazon","oracle","paypal","steam","epicgames","line","bbc","cnn","bilibili","baidu","cn","yandex","vk","rutracker","category-ru","category-gov-ru","category-ads","category-ads-all","category-porn","category-games","category-media","category-social-media-!cn","category-public-tracker","category-dev","category-scholar-!cn","category-ai-!cn","geolocation-cn","geolocation-!cn","private"];

/* ---------------- entity field schemas ---------------- */
// A standby is considered "far behind" past this many bytes of WAL lag — generous
// so a brief catch-up after a restart doesn't flap the warning dot.
const REPL_LAG_WARN_BYTES = 128 * 1024 * 1024;

// replWarning returns a warning message if a node's replication slot is unhealthy
// (down / missing / far behind), else "". The data plane is unaffected by any of
// these — they are HA-readiness concerns — so callers render them yellow.
function replWarning(rep) {
  if (!rep) return "";
  if (rep.missing) return "replication slot missing (never provisioned or dropped)";
  if (!rep.active) return "replica not streaming (Postgres down or disconnected)";
  if (rep.bytes_behind != null && rep.bytes_behind > REPL_LAG_WARN_BYTES) return "replica " + humanBytes(rep.bytes_behind) + " behind";
  return "";
}

// statusDot renders a coloured liveness dot with a hover tooltip, from the node's
// agent health, the external :443 edge probe, and standby replication health (all
// from /api/overview). Three states, by the rule "does it break what a VPN user
// sees?": green ("status: OK") = all good; red ("error: …") = data plane affected
// (agent down/degraded, or entry :443 unreachable); yellow ("warning: …") = data
// plane fine but an HA concern (a standby's replica is down/behind). Grey = no
// report yet.
function statusDot(node, ov) {
  const health = (ov && ov.health) || node.health || "unknown";
  const edge = ov && ov.edge;
  const warn = replWarning(ov && ov.replication);
  let color = "#6b7280", title = "no report yet";
  // Red first — a data-plane problem outranks any HA warning.
  if (edge && edge.ok === false) { color = "#f87171"; title = "error: external :443 unreachable" + (edge.error ? " — " + edge.error : ""); }
  else if (health === "degraded" || health === "unhealthy") { color = "#f87171"; title = "error: agent " + health + " (node unreachable or unhealthy)"; }
  else if (health === "healthy" && warn) { color = "#f59e0b"; title = "warning: " + warn; }
  else if (health === "healthy") {
    const extra = [edge && edge.ok ? ":443 reachable" : "", (ov && ov.replication) ? "replica streaming" : ""].filter(Boolean);
    color = "#34d399"; title = "status: OK" + (extra.length ? " · " + extra.join(" · ") : "");
  }
  return html`<span title=${title} style=${{ display: "inline-block", width: "10px", height: "10px", borderRadius: "50%", background: color }}></span>`;
}

// ownsInfra = may manage shared infra (any admin); seesAll = sees every
// namespace's clients (only the bootstrap owner). They diverge for a co-owner
// admin: it writes infra routes but is scoped to its own clients, so the
// resource labels must read "my clients", not "all users".
function schemas(state, overview, ownsInfra = false, me = "", seesAll = ownsInfra) {
  const node = (n) => ({ value: n.id, label: (n.name || n.id) });
  const exitOpts = (state.nodes || []).filter(n => n.public_role === "exit").map(node);
  const groupOpts = (state.groups || []).map(g => ({ value: g.id, label: g.name || g.id }));
  // Routing "variant A": the UI shows two conceptual LEVELS, not raw tiers. A
  // namespace rule is exit-tier; an infra rule's tier is derived from its action
  // (exit → fleet mandate, direct/block → guard safety-net), which the DB CHECK
  // guard_not_exit also guarantees. Operators only ever write namespace rules, so
  // they get no level control at all (this also fixes the old bug where the tier
  // selector offered them "guard", which the server then rejected with 403).
  const levelOpts = [
    { value: "namespace", label: t("Namespace (this tenant)") },
    { value: "infra", label: t("Infra (network-wide)") },
  ];
  const levelOf = (tier) => (tier === "fleet" || tier === "guard") ? "infra" : "namespace";
  const levelLabel = (tier) => levelOf(tier) === "infra" ? t("Infra") : t("Namespace");
  // effTier maps the form's level+action to the stored tier (operators are always
  // namespace-level).
  const effTier = (v) => {
    const lvl = ownsInfra ? (v.level || "namespace") : "namespace";
    if (lvl === "infra") return v.action === "exit" ? "fleet" : "guard";
    return "exit";
  };
  // For an operator "all users" means "all of MY clients" — its rule is scoped to
  // its namespace in the compiler, never the whole entry. Reflect that in the UI.
  const allLabel = seesAll ? t("all users") : t("all my clients");
  const allSelectLabel = seesAll ? t("— all users (any group) —") : t("— all my clients (any group) —");
  const groupHint = seesAll ? t("default = all users; or pick a specific group") : t("default = all my clients; or pick a specific group");
  const nodeOpts = (state.nodes || []).map(n => ({ value: n.id, label: (n.name || n.id) + " (" + t(n.public_role) + ")" }));
  // A domain attaches to a node, so an operator may only attach it to one of its
  // own nodes; the fleet owner picks any. (The server enforces this regardless.)
  const domainNodeOpts = (state.nodes || [])
    .filter(n => ownsInfra || n.owner_id === me)
    .map(n => ({ value: n.id, label: (n.name || n.id) + " (" + t(n.public_role) + ")" }));
  const none = (arr) => [{ value: "", label: "— none —" }, ...arr];
  const ovById = {};
  ((overview && overview.nodes) || []).forEach(n => { ovById[n.id] = n; });
  return {
    nodes: {
      title: "Nodes", path: "/api/nodes", singular: "node", provision: true, series: true, ownerGated: true,
      columns: [
        ["id", "", (v, it) => statusDot(it, ovById[it.id])],
        ["name", "Name"],
        ["public_role", "Role", (v, it) => html`<span class=${"pill " + v}>${t(v)}</span>${it.maintenance ? html`<span class="pill warn" title=${t("in maintenance — drained from rotation")} style=${{ marginLeft: ".3rem" }}>⏸ ${t("drain")}</span>` : ""}`],
        ["public_ips", "Public IPs", (v) => (v || []).join(", ")],
        ["agent_addr", "Agent"],
        ["id", "Control plane", (v, it, st) => cpBadge(cpRoleOf((st && st.control_plane) || {}, it.id))],
      ],
      fields: [
        { name: "name", label: "Name", required: true },
        { name: "public_role", label: "Role", type: "select", options: ["entry", "exit"], required: true },
        { name: "public_ips", label: "Public IPs", type: "tags", hint: "server's public IP address(es)" },
        { name: "agent_addr", label: "Agent address", hint: "host:8443 (the panel reaches the agent here)" },
        { name: "dial_in.port", label: "Reality port", type: "number", def: 443, showIf: v => v.public_role === "exit" },
        { name: "dial_in.uuid", label: "VLESS UUID", showIf: v => v.public_role === "exit" },
        { name: "dial_in.target_sni", label: "Reality target SNI (borrowed CDN)", showIf: v => v.public_role === "exit" },
        { name: "dial_in.public_key", label: "Reality public key", showIf: v => v.public_role === "exit" },
        { name: "dial_in.priv_key", label: "Reality private key", showIf: v => v.public_role === "exit" },
        { name: "dial_in.short_id", label: "Reality short_id", showIf: v => v.public_role === "exit" },
      ],
      onSubmit: (v) => { if (v.public_role === "exit" && v.dial_in) v.dial_in.proto = "vless-reality"; else delete v.dial_in; return v; },
      rowActions: ["limits"],
      maintenanceAction: true, // drain/resume lives inside the node edit panel
    },
    groups: {
      title: "Groups", path: "/api/groups", singular: "group",
      columns: [["name", "Name"], ["default_exit_id", "Default exit", (v, it, st) => nameOf(st, "nodes", v) || html`<span class=muted>local</span>`],
        // Rule count targeting this group, linking into Routing pre-filtered by name.
        ["id", "Rules", (v, it, st) => {
          const n = (st.route_policies || []).filter(p => p.applies_to_group_id === it.id).length;
          if (!n) return html`<span class=muted>0</span>`;
          return html`<a href="#" title=${t("Show routing rules targeting this group")} onClick=${e => { e.preventDefault(); navigateTo("route_policies", it.name); }}>${tf("{n} →", { n })}</a>`;
        }]],
      fields: [
        { name: "name", label: "Name", required: true },
        { name: "default_exit_id", label: "Default exit node", type: "select", options: [{ value: "", label: "local (exit from the entry node)" }, ...exitOpts], hint: "where the group exits by default; “local” = direct egress from the entry node" },
      ],
    },
    users: {
      title: "Users", path: "/api/users", singular: "user", bulk: true,
      filter: (it, q) => (it.display_name || "").toLowerCase().includes(q) || (it.username || "").toLowerCase().includes(q),
      columns: [
        ["display_name", "Name", (v, it) => v || it.username],
        ["username", "Username"],
        ["group_id", "Group", (v, it, st) => nameOf(st, "groups", v)],
        ["enabled", "Enabled", (v) => v ? html`<span class=ok>✓</span>` : html`<span class=err>✗</span>`],
        ["expires_at", "Expires", (v) => !v ? html`<span class=muted>—</span>` : html`<span class=${isExpired(v) ? "err" : ""}>${fmtDMY(v)}${isExpired(v) ? " ⚠" : ""}</span>`],
      ],
      fields: [
        { name: "display_name", label: "Display name", required: true },
        { name: "username", label: "Username", required: true, hint: "the join-key (TrustTunnel credentials + sing-box auth_user)" },
        { name: "password", label: "Password", type: "password", hint: "auto-generated on create; on edit leave blank to keep the current password (min 12 chars if set explicitly)" },
        { name: "password_confirm", label: "Confirm password", type: "password", createOnly: true, matchField: "password", hint: "re-enter the password (leave both blank to auto-generate)" },
        { name: "regenerate", label: "Regenerate password (issue a new secret)", type: "bool", editOnly: true },
        { name: "group_id", label: "Group", type: "select", options: groupOpts.length ? groupOpts : [{ value: "", label: "— create a group first —" }], required: true, hint: groupOpts.length ? undefined : "No groups yet — add a group on the Groups tab before creating users." },
        { name: "enabled", label: "Enabled", type: "bool", def: true },
        { name: "expires_at", label: "Expires", type: "date", hint: "blank = never; access stops at the end of this day (UTC)" },
      ],
      rowActions: ["config"],
    },
    route_policies: {
      title: "Routing", path: "/api/route-policies", singular: "route policy", tester: true, ownerGated: true,
      filter: (it, q, st) => {
        if ((it.name || "").toLowerCase().includes(q) || (it.tier || "").toLowerCase().includes(q)) return true;
        if (it.applies_to_group_id) return String(nameOf(st, "groups", it.applies_to_group_id)).toLowerCase().includes(q);
        // No explicit group = applies to every group. Match the "all users" label,
        // and also surface it whenever the query names a real group — a catch-all
        // rule targets that group too (so a Groups → Routing jump shows it).
        if ("all users".includes(q) || t("all users").toLowerCase().includes(q)) return true;
        return (st.groups || []).some(g => (g.name || "").toLowerCase().includes(q));
      },
      help: {
        title: "How a routing rule is read",
        points: [
          "Infra rules (network-wide) run before namespace rules. Within a level the higher priority wins, and the first rule that matches a request decides.",
          "Inside one rule the match types combine with AND (domains AND countries AND CIDRs); values within a single type are OR. Need OR across types? Make two rules.",
          "geoip matches the destination IP (resolved over DNS), not the domain text. A .ru domain on a foreign IP won't match geoip:ru — use “Match domains” for that.",
          "Infra rules apply across the whole network (admin only); namespace rules apply to your own clients.",
          "“Except domains” lets the listed hosts skip this rule and use the group's normal exit (the group needs a default exit).",
          "Use the Route tester to check which rule wins for a given group and host.",
        ],
      },
      // Live, plain-language description of the rule being edited — what it does,
      // how it sits among the others, and where the rest of the traffic goes.
      summary: (v, st) => {
        const grp = v.applies_to_group_id ? nameOf(st, "groups", v.applies_to_group_id) : t("all users");
        const mine = policyMatchTokens(v);
        const what = mine.length ? mine.join(", ") : t("all destinations");
        let how;
        if (v.action === "direct") how = t("go direct, bypassing the VPN");
        else if (v.action === "block") how = t("be blocked");
        else how = tf("exit via {name}", { name: v.exit_node_id ? nameOf(st, "nodes", v.exit_node_id) : t("(pick an exit node)") });

        const prio = Number(v.priority) || 0;
        const et = effTier(v);
        const others = (st.route_policies || []).filter(p => p.tier === et && !p.disabled && p.id !== v.id);
        // Rules checked before this one: higher priority, ties broken by id (as the server does).
        const before = others.filter(p => {
          const pp = Number(p.priority) || 0;
          return pp > prio || (pp === prio && (p.id || "") < (v.id || "~"));
        });
        // Shadow: an earlier rule whose group overlaps and that matches some of the same tokens.
        let shadow = null;
        const mineSet = new Set(mine);
        for (const p of before) {
          if (!policyGroupsOverlap(v.applies_to_group_id || "", p.applies_to_group_id || "")) continue;
          const hit = policyMatchTokens(p).filter(t => mineSet.has(t));
          if (hit.length) { shadow = { name: p.name, on: hit.slice(0, 3).join(", "), more: hit.length > 3 }; break; }
        }

        const ln = { margin: ".3rem 0 0", fontSize: "12px", lineHeight: 1.4 };
        const warn = { ...ln, color: "#f59e0b" };
        const tierNote = et === "fleet" ? " " + t("(infra mandate — overrides every namespace)")
          : et === "guard" ? " " + t("(infra safety-net — checked before namespace rules)") : "";
        return html`
          <div><b>“${grp}”</b>: ${what} → ${tf("will {how}", { how })}${tierNote}.</div>
          <div class=muted style=${ln}>${tf("Rules of this level checked first: {n}.", { n: before.length })} ${t("Traffic that matches nothing takes the group's default exit.")}</div>
          ${shadow && html`<div style=${warn}>⚠ ${tf("“{name}” has a higher priority and also catches {on}{more} — that traffic may go there first.", { name: shadow.name, on: shadow.on, more: shadow.more ? "…" : "" })}</div>`}
          ${v.disabled && html`<div style=${warn}>⚠ ${t("This rule is off and won't be applied.")}</div>`}`;
      },
      toggle: {
        field: "disabled",
        label: (it) => it.disabled ? "enable" : "disable",
        next: (it) => ({ ...it, disabled: !it.disabled }),
      },
      columns: [
        ["disabled", "Status", (v) => {
          const [c, t] = v ? ["#9ca3af", "disabled"] : ["#34d399", "enabled"];
          return html`<span style=${{ display: "inline-flex", alignItems: "center", gap: ".4rem" }}><span style=${{ width: "8px", height: "8px", borderRadius: "50%", background: c, display: "inline-block" }}></span>${t}</span>`;
        }],
        ["name", "Name"],
        ["tier", "Level", (v) => html`<span class=${"pill " + (levelOf(v) === "infra" ? "fleet" : "exit")}>${levelLabel(v)}</span>`],
        ["applies_to_group_id", "Group", (v, it, st) => v ? nameOf(st, "groups", v) : html`<span class=muted>${allLabel}</span>`],
        ["action", "Action", (v) => html`<span class=${"pill " + v}>${v}</span>`],
        ["match_domains", "Match", (v, it) => [...(it.match_domains||[]), ...(it.match_geoip||[]).map(g=>"geoip:"+g), ...(it.match_geosite||[]).map(g=>"geosite:"+g)].join(", ")],
        ["exit_node_id", "Exit", (v, it, st) => nameOf(st, "nodes", v)],
        ["priority", "Prio"],
      ],
      fields: [
        { name: "name", label: "Name", required: true },
        { name: "priority", label: "Priority", type: "number", def: 100, hint: "higher = evaluated first" },
        { name: "level", label: "Level", type: "select", options: levelOpts, required: true, def: "namespace", deriveInitial: (it) => levelOf(it.tier), showIf: () => ownsInfra, hint: "Namespace = applies to your clients; Infra = applies network-wide (admin only)" },
        { name: "applies_to_group_id", label: "Applies to group", type: "select", options: [{ value: "", label: allSelectLabel }, ...groupOpts], hint: groupHint },
        { name: "match_domains", label: "Match domains / zones", type: "tags", hint: "suffix match — “example.com” also matches *.example.com; “ru” matches *.ru. No wildcards/regex." },
        { name: "match_geoip", label: "Match countries (geoip)", type: "country", hint: "by destination IP, which can't be faked — search for a country" },
        { name: "match_geosite", label: "Match geosite categories", type: "tags", list: "geosite-dl", hint: "by domain category — search e.g. google, netflix, category-ads, geolocation-cn" },
        { name: "match_cidrs", label: "Match CIDRs", type: "tags", hint: "e.g. 10.0.0.0/8, 1.1.1.1" },
        { name: "exclude_domains", label: "Except domains", type: "tags", hint: "domains that bypass this policy and take the group's normal exit (e.g. bank.ru). Needs a group default exit." },
        { name: "action", label: "Action", type: "select", options: ["exit", "direct", "block"], required: true },
        { name: "exit_node_id", label: "Exit node", type: "select", options: none(exitOpts), showIf: v => v.action === "exit", hint: "required for action=exit" },
        { name: "fallback_kind", label: "Fallback", type: "select", options: none(["block", "direct", "exit"]), showIf: v => v.action === "exit" },
        { name: "fallback_exit_id", label: "Fallback exit", type: "select", options: none(exitOpts), showIf: v => v.fallback_kind === "exit" },
      ],
      onSubmit: (v) => { v.tier = effTier(v); delete v.level; return v; },
    },
    domains: {
      title: "Domains", path: "/api/domains", singular: "domain",
      // A domain has no owner_id of its own — it is owned by its node. Gate edit
      // rights by the node's owner so an operator manages its own domains/TLS and
      // sees the rest read-only.
      ownerGated: true,
      ownerOf: (it, st) => { const n = (st.nodes || []).find(x => x.id === it.node_id); return n ? (n.owner_id || "") : ""; },
      columns: [["hostname", "Hostname"], ["purpose", "Purpose"], ["node_id", "Node", (v, it, st) => nameOf(st, "nodes", v)], ["tls_status", "TLS"], ["tls_issuer", "Issuer", v => isStagingIssuer(v) ? html`<span title="Staging cert — not browser-trusted; promote to production">⚠ ${v}</span>` : (v || "—")]],
      fields: [
        { name: "hostname", label: "Hostname", required: true },
        { name: "purpose", label: "Purpose", type: "select", options: ["main-fallback", "fallback-site"], required: true },
        { name: "node_id", label: "Entry node", type: "select", options: domainNodeOpts, required: true },
      ],
      rowActions: ["promote-tls"],
    },
  };
}
const TABS = ["overview", "nodes", "clients", "traffic", "route_policies", "logs", "settings"];
// Computed per render (not a frozen const) so the language switch relabels tabs.
function tabLabel(tab) {
  return ({ overview: t("Overview"), nodes: t("Nodes"), clients: t("Clients"), traffic: t("Traffic"),
    route_policies: t("Routing"), members: t("Members"), logs: t("Logs"), settings: t("Settings"), account: t("Account") })[tab] || tab;
}
// Tabs whose header carries no per-row count badge. "clients" aggregates two
// entities (users+groups) so no single count fits; "traffic"/"members" have no
// state[tab] array to count.
const NO_COUNT = { overview: true, clients: true, traffic: true, members: true, logs: true, settings: true, account: true };

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
// page refresh — previously every refresh dropped back to the first tab.
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
  const [lang, setLang] = useState(LANG);
  const [confirmReq, setConfirmReq] = useState(null);
  const [routeFilter, setRouteFilter] = useState(""); // pre-fill for the Routing table after a cross-tab jump
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
  const goTab = (tb) => { setRouteFilter(""); setTab(tb); pushHash(tb); };

  // Register the themed-confirm host so uiConfirm() anywhere can open a modal.
  useEffect(() => { _confirmHost = setConfirmReq; return () => { _confirmHost = null; }; }, []);
  // Register the cross-tab navigation host (Groups → Routing with a filter).
  useEffect(() => { _navHost = (tb, filter) => { if (tb === "route_policies") setRouteFilter(filter); setTab(tb); pushHash(tb); }; return () => { _navHost = null; }; }, []);
  // Back/forward navigates the hash without a page load — follow it.
  useEffect(() => { const onHash = () => setTab(parseHash().tab || "overview"); window.addEventListener("hashchange", onHash); return () => window.removeEventListener("hashchange", onHash); }, []);

  // A fresh id per call so the Toast resets its dismissal timer; the Toast owns
  // the auto-hide (paused on hover, errors stay until closed).
  const notify = useCallback((msg, kind) => {
    // Localize messages — backend errors arrive in English. An exact dictionary
    // hit wins; otherwise translate the part before a ": <detail>" suffix (e.g.
    // "bot unreachable: …"). Already-translated success strings pass through.
    let m = String(msg == null ? "" : msg);
    const tr = t(m);
    if (tr !== m) m = tr;
    else { const i = m.indexOf(": "); if (i > 0) { const head = t(m.slice(0, i)); if (head !== m.slice(0, i)) m = head + m.slice(i); } }
    setToast({ msg: m, kind, id: Date.now() + Math.random() });
  }, []);

  const loadState = useCallback(async () => {
    const s = await api("GET", "/api/state"); setState(s);
    try { setHealth(await api("GET", "/api/health")); } catch {}
    try { setTraffic(await api("GET", "/api/traffic")); } catch {}
  }, []);

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
      notify(on ? t("Cross-namespace view on") : t("Cross-namespace view off"), "ok");
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
    let live = true;
    // Skip the poll on a hidden tab (no point fetching what nobody sees); refresh
    // at once when the tab becomes visible again.
    const load = async () => { if (document.hidden) return; try { const d = await api("GET", "/api/overview"); if (live) setOverview(d); } catch {} };
    load();
    const t = setInterval(load, 15000);
    const onVis = () => { if (!document.hidden) load(); };
    document.addEventListener("visibilitychange", onVis);
    return () => { live = false; clearInterval(t); document.removeEventListener("visibilitychange", onVis); };
  }, [phase]);

  if (phase === "loading") return html`<div class=login><div class=card>${t("Loading…")}</div></div>`;
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
    <div>
      <div class=stickyhead>
        <div class=topbar>
          <div class=brand>Trust<span>Panel</span></div>
          <div class=spacer></div>
          ${crossNs && html`<button class="pill warn" title=${t("Viewing every namespace — click to return to your own")} onClick=${() => toggleCrossNs(false)}>🌐 ${t("All namespaces")} ✕</button>`}
          <${LangToggle} lang=${lang} switchLang=${switchLang} />
          ${user && html`<span class=muted>${user}${!isAdmin ? " · " + (ns || t("operator")) : ""}</span>`}
          <button class=ghost onClick=${async () => { try { await api("POST", "/api/auth/logout"); } catch {}; location.reload(); }}>${t("Log out")}</button>
        </div>
        <div class=tabs>
          ${visibleTabs.map(tb => html`<div class=${"tab" + (tb === curTab ? " active" : "")} role=tab tabindex=0 aria-selected=${tb === curTab} onClick=${() => goTab(tb)} onKeyDown=${e => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); goTab(tb); } }}>
            ${tabLabel(tb)}${!NO_COUNT[tb] && html`<span class=count>${(state[tb] || []).length}</span>`}
          </div>`)}
        </div>
      </div>
      <main>
        <${ErrorBoundary} key=${curTab}>
          ${curTab === "overview"
            ? html`<${Overview} state=${state} health=${health} overview=${overview} traffic=${traffic} setTab=${setTab} onReconcile=${reconcileNow} isAdmin=${isAdmin} seesAll=${isBootstrap} />`
            : curTab === "clients"
            ? html`<${Clients} state=${state} reload=${loadState} notify=${notify} setModal=${setModal} />`
            : curTab === "traffic"
            ? html`<${Traffic} data=${traffic} reload=${loadState} notify=${notify} />`
            : curTab === "logs"
            ? html`<${Logs} notify=${notify} isAdmin=${isAdmin} />`
            : curTab === "settings"
            ? html`<${SettingsPanel} notify=${notify} state=${state} isBootstrap=${isBootstrap} canManage=${canManage} myNs=${ns} crossNs=${crossNs} toggleCrossNs=${toggleCrossNs} />`
            : curTab === "nodes"
            ? html`<${NodesPanel} sc=${sc} state=${state} overview=${overview} reload=${loadState} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${ns} />`
            : html`<${EntityPanel} cfg=${sc[curTab]} rows=${state[curTab] || []} state=${state} reload=${loadState} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${ns} initialFilter=${curTab === "route_policies" ? routeFilter : ""} />`}
        <//>
      </main>
      ${modal && modal.kind === "form" && html`<${FormModal} ...${modal} state=${state} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "config" && html`<${ConfigModal} userId=${modal.userId} username=${modal.username} user=${modal.user} entries=${(state.nodes||[]).filter(n=>n.public_role==='entry')} onClose=${() => setModal(null)} notify=${notify} />`}
      ${modal && modal.kind === "provision" && html`<${ProvisionModal} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} isAdmin=${isAdmin} />`}
      ${modal && modal.kind === "convert" && html`<${ConvertModal} state=${state} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "limits" && html`<${LimitsModal} node=${modal.node} onClose=${() => setModal(null)} reload=${loadState} notify=${notify} />`}
      ${modal && modal.kind === "routetest" && html`<${RouteTester} state=${state} onClose=${() => setModal(null)} notify=${notify} />`}
      ${confirmReq && html`<${ConfirmModal} req=${confirmReq} onClose=${() => setConfirmReq(null)} />`}
      ${toast && html`<${Toast} toast=${toast} setToast=${setToast} />`}
    </div>`;
}

function reconcileText(r) {
  const lines = Object.entries(r.nodes || {}).map(([k, v]) => {
    const warn = (v.warnings || []).map(w => "\n  ⚠ " + w).join("");
    return `${k}: ${v.outcome}${v.error ? " — " + v.error : ""}${warn}`;
  });
  return tf("Synced — rev {revision}", { revision: r.revision }) + "\n" + (lines.join("\n") || t("no nodes"));
}
function reconcileOk(r) {
  return Object.values(r.nodes || {}).every(v => !v.error && (v.outcome === "applied" || v.outcome === "no-change"));
}

/* ---------------- overview ---------------- */
function Bar({ label, value, max, text }) {
  const pct = max > 0 ? Math.min(100, Math.max(0, value / max * 100)) : 0;
  const cls = "bar-fill" + (pct >= 90 ? " red" : pct >= 70 ? " amber" : "");
  return html`
    <div class=bar-row>
      <div class=bar-label><span>${label}</span><span class=muted>${text}</span></div>
      <div class=bar><div class=${cls} style=${{ width: pct.toFixed(1) + "%" }}></div></div>
    </div>`;
}

const GIB = 1024 * 1024 * 1024;

// cpRoleOf returns "master"/"standby"/"" for a node id, derived from the live
// control-plane state — so the master and standbys are tagged inline wherever a
// node is shown, instead of in a separate Control-plane block.
function cpRoleOf(cp, id) {
  if (!cp) return "";
  if (id === cp.active_node_id) return "master";
  if ((cp.standby_node_ids || []).includes(id)) return "standby";
  return "";
}
function cpBadge(role) {
  if (!role) return "";
  const color = role === "master" ? "#f59e0b" : "#a78bfa";
  return html`<span style=${{ display: "inline-block", fontSize: "10px", fontWeight: 700, padding: "1px 6px", borderRadius: "8px", letterSpacing: ".03em", background: color + "22", color, border: "1px solid " + color + "55" }}>${t(role)}</span>`;
}

function NodeMonitor({ n, cp }) {
  const s = n.system;
  const lim = n.limits || {};
  const stale = n.metrics_age_sec >= 0 && n.metrics_age_sec > 180;
  const cpRole = cpRoleOf(cp, n.id);
  return html`
    <div class=monitor-card>
      <div class=between>
        <div><strong>${n.name || n.id}</strong> <span class=${"pill " + n.role}>${t(n.role)}</span>${cpRole ? html` ${cpBadge(cpRole)}` : ""}</div>
        <span class=${n.health === "healthy" ? "ok" : "err"}>${t(n.health)}</span>
      </div>
      ${!s ? html`<p class=muted style=${{ margin: ".5rem 0 0" }}>${t("no metrics yet")}</p>` : html`
        <div class=bars>
          ${Bar({ label: t("CPU"), value: s.load1, max: s.cpu_cores || 1, text: (s.load1 || 0).toFixed(2) + " " + t("load") + " / " + s.cpu_cores + " " + t("cores") })}
          ${Bar({ label: t("Memory"), value: s.mem_used_mb, max: lim.memory_mb || s.mem_total_mb || 1,
                  text: (s.mem_used_mb / 1024).toFixed(1) + " / " + ((lim.memory_mb || s.mem_total_mb) / 1024).toFixed(1) + " GB" + (lim.memory_mb ? " (" + t("plan") + ")" : "") })}
          ${Bar({ label: t("Disk"), value: s.disk_used_gb, max: lim.disk_gb || s.disk_total_gb || 1,
                  text: s.disk_used_gb + " / " + (lim.disk_gb || s.disk_total_gb) + " GB" + (lim.disk_gb ? " (" + t("plan") + ")" : "") })}
          ${(() => {
            const usedBytes = n.traffic_rx_bytes + n.traffic_tx_bytes;
            if (lim.traffic_gb) return Bar({ label: t("Traffic (mo)"), value: usedBytes, max: lim.traffic_gb * GIB,
                                             text: humanBytes(usedBytes) + " / " + humanBytes(lim.traffic_gb * GIB) });
            return html`<div class=bar-row><div class=bar-label><span>${t("Traffic (mo)")}</span><span class=muted>${humanBytes(n.traffic_rx_bytes + n.traffic_tx_bytes)} · ${t("no limit set")}</span></div></div>`;
          })()}
        </div>
        <p class=muted style=${{ margin: ".5rem 0 0", fontSize: "12px" }}>
          ${t("updated")} ${n.metrics_age_sec < 0 ? t("never") : n.metrics_age_sec + "s " + t("ago")}${stale ? " · " + t("stale") : ""}
          ${s.uptime_sec > 0 ? " · " + t("up") + " " + Math.floor(s.uptime_sec / 86400) + t("d") : ""}
        </p>`}
      ${n.reconcile && (() => {
        const rc = n.reconcile;
        const cls = rc.ok ? "muted" : "err";
        const age = rc.age_sec >= 0 ? " · " + rc.age_sec + "s " + t("ago") : "";
        return html`<p class=${cls} style=${{ margin: ".35rem 0 0", fontSize: "12px" }}>
          ${rc.ok ? "🔄 " + t("config synced") : "⚠ " + t("sync") + " " + t(rc.outcome)}${age}${rc.error ? html` — ${rc.error}` : ""}
        </p>
        ${(rc.warnings || []).map((wmsg, i) => html`<p key=${i} class=warn style=${{ margin: ".2rem 0 0", fontSize: "12px" }}>⚠ ${wmsg}</p>`)}`;
      })()}
      ${n.edge && (() => {
        const e = n.edge;
        const cls = e.ok ? "muted" : "err";
        const age = e.age_sec >= 0 ? " · " + e.age_sec + "s " + t("ago") : "";
        return html`<p class=${cls} style=${{ margin: ".35rem 0 0", fontSize: "12px" }}>
          ${e.ok ? "🌐 " + t("edge :443 reachable") : "⚠ " + t("edge :443 unreachable")}${age}${e.error ? html` — ${e.error}` : ""}
        </p>`;
      })()}
      ${n.billing && n.billing.paid_until && (() => {
        const dl = n.billing_days_left;
        const cls = dl == null ? "muted" : dl < 0 ? "err" : dl <= 7 ? "warn" : "muted";
        const txt = dl == null ? "" : dl < 0 ? (" · " + (-dl) + t("d") + " " + t("overdue")) : (" · " + dl + t("d") + " " + t("left"));
        return html`<p class=${cls} style=${{ margin: ".35rem 0 0", fontSize: "12px" }}>💳 ${t("paid until")} ${fmtDMY(n.billing.paid_until)}${txt}</p>`;
      })()}
    </div>`;
}

// computeProblems derives the live "needs attention" list from the current state,
// the /api/overview snapshot, and settings — the signals otherwise scattered
// across Nodes/HA/Domains/Settings, gathered into one board. Each item is
// {sev:'critical'|'warn', text}. Used by both the Overview banner and the Logs tab.
function computeProblems(state, overview, settings) {
  const out = [];
  const ovById = {};
  ((overview && overview.nodes) || []).forEach(n => { ovById[n.id] = n; });
  for (const n of (state.nodes || [])) {
    const ov = ovById[n.id] || {};
    const nm = n.name || n.id;
    if (ov.edge && ov.edge.ok === false) out.push({ sev: "critical", text: nm + " — " + t("external :443 unreachable") });
    const h = ov.health || n.health;
    if (h === "degraded" || h === "unhealthy") out.push({ sev: "critical", text: nm + " — " + t("agent unreachable") });
    const w = replWarning(ov.replication);
    if (w) out.push({ sev: "warn", text: nm + " — " + t("replication degraded") });
    if (ov.billing_days_left != null) {
      if (ov.billing_days_left < 0) out.push({ sev: "warn", text: nm + " — " + t("VPS payment overdue") });
      else if (ov.billing_days_left <= 7) out.push({ sev: "warn", text: nm + " — " + t("VPS payment due soon") });
    }
  }
  for (const d of (state.domains || [])) {
    if (isStagingIssuer(d.tls_issuer)) out.push({ sev: "warn", text: d.hostname + " — " + t("staging TLS certificate (not browser-trusted)") });
  }
  if (settings) {
    if (settings.bot && settings.bot.status && settings.bot.status !== "ok" && settings.bot.status !== "unconfigured")
      out.push({ sev: "warn", text: t("management bot unreachable") });
    if (settings.alert && settings.alert.status && settings.alert.status !== "ok" && settings.alert.status !== "unconfigured")
      out.push({ sev: "warn", text: t("alert bot unreachable") });
  }
  return out;
}

// NeedsAttention shows the live problem board. It fetches settings itself (the
// off-site-backup and bot-status signals live there) and refreshes with the page.
function NeedsAttention({ state, overview }) {
  const [settings, setSettings] = useState(null);
  useEffect(() => { let live = true; api("GET", "/api/settings").then(s => live && setSettings(s)).catch(() => {}); return () => { live = false; }; }, []);
  const problems = computeProblems(state, overview, settings);
  if (problems.length === 0) {
    return html`<div class=card style=${{ borderLeft: "3px solid #34d399" }}><strong class=ok>✓ ${t("All systems normal")}</strong></div>`;
  }
  return html`<div class=card style=${{ borderLeft: "3px solid #f59e0b" }}>
    <strong>⚠ ${t("Needs attention")} (${problems.length})</strong>
    <div style=${{ marginTop: ".5rem", display: "flex", flexDirection: "column", gap: ".3rem" }}>
      ${problems.map((p, i) => html`<div key=${i} style=${{ fontSize: "13px" }}>
        <span style=${{ color: p.sev === "critical" ? "#f87171" : "#f59e0b" }}>${p.sev === "critical" ? "🔴" : "⚠"}</span> ${p.text}
      </div>`)}
    </div>
  </div>`;
}

function Overview({ state, health, overview, traffic, setTab, onReconcile, isAdmin, seesAll }) {
  const ov = overview;
  const [syncing, setSyncing] = useState(false);
  const sync = async () => { setSyncing(true); try { await onReconcile(); } finally { setSyncing(false); } };
  const agg = state.aggregate || {};
  const counts = [
    [t("nodes"), (state.nodes || []).length],
    [t("entry"), (state.nodes || []).filter(n => n.public_role === "entry").length],
    [t("exit"), (state.nodes || []).filter(n => n.public_role === "exit").length],
    [t("groups"), (state.groups || []).length],
    [t("users"), (state.users || []).length],
    [t("policies"), (state.route_policies || []).length],
  ];
  const cp = state.control_plane || {};
  const nodes = ov ? ov.nodes || [] : [];
  return html`
    <${NeedsAttention} state=${state} overview=${overview} />
    <div class=stats>
      ${counts.map(([l, n]) => html`<div class=stat><div class=n>${n}</div><div class=l>${l}</div></div>`)}
    </div>
    ${!seesAll && agg.other_users > 0 && html`<p class=muted style=${{ marginTop: "-.4rem" }}>${tf("Plus {n} clients in other namespaces", { n: agg.other_users })}${agg.any_online ? " · " + t("some online") : ""}</p>`}
    <div class=card>
      <div class=between><h2 style=${{ margin: 0 }}>${t("Node monitoring")}</h2>
        <div class=row style=${{ alignItems: "center", gap: ".6rem" }}>
          ${health && html`<span class=muted title=${t("Background sync cycles since the panel started")}>${t("auto-syncs")}: ${health.reconcile_count || 0}</span>`}
          ${isAdmin && html`<button class="ghost sm" disabled=${syncing} title=${t("Rebuild config from the database and push it to every node now")} onClick=${sync}>${syncing ? t("Syncing…") : t("Sync now")}</button>`}
        </div>
      </div>
      ${nodes.length === 0 ? html`<p class=muted>${t("Collecting metrics…")}</p>` : html`
        <div class=monitor-grid>${nodes.map(n => html`<${NodeMonitor} key=${n.id} n=${n} cp=${cp} />`)}</div>`}
    </div>`;
}

/* ---------------- traffic ---------------- */
function humanBytes(n) {
  n = Number(n) || 0;
  // IEC units (base-1024): the loop divides by 1024, so the labels must be KiB/
  // MiB/… not KB/MB (which are base-1000) — keeps the figure and its unit honest.
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

function Traffic({ data, reload, notify }) {
  if (data == null) return html`<div class=card>${t("Loading…")}</div>`;
  const rows = data.users || [];
  const win = data.window_seconds || 360;
  const activeCount = data.active_count || 0;
  const total = rows.reduce((s, r) => s + (Number(r.total_bytes) || 0), 0);
  const [open, setOpen] = useState(null); // user_id whose sparkline is expanded
  return html`
    <div class=card>
      <div class=between>
        <h2 style=${{ margin: 0 }}>${t("Per-user traffic")}</h2>
        <div class=row style=${{ alignItems: "center", gap: ".6rem" }}>
          <span class=muted>${tf("{n} active now", { n: activeCount })}</span>
          <button class=ghost onClick=${async () => { try { await reload(); notify(t("Refreshed"), "ok"); } catch (e) { notify(e.message, "err"); } }}>${t("Refresh")}</button>
        </div>
      </div>
      <table>
        <thead><tr><th style=${{ width: "1.6rem" }}></th><th>${t("User")}</th><th>${t("Now")}</th><th>↑</th><th>↓</th><th>${t("Total")}</th></tr></thead>
        <tbody>
          ${rows.length === 0 && html`<tr><td class=empty colspan=6>${t("No users yet")}</td></tr>`}
          ${rows.map(r => html`
          <tr key=${r.user_id} style=${{ cursor: "pointer" }} title=${t("Click the row to show this client's traffic graph")} onClick=${() => setOpen(o => o === r.user_id ? null : r.user_id)}>
            <td class=muted style=${{ textAlign: "center", width: "1.6rem" }} aria-label=${open === r.user_id ? t("Hide graph") : t("Show graph")}>${open === r.user_id ? "▾" : "▸"}</td>
            <td><span title=${r.active ? t("active now") : t("idle")} style=${{ display: "inline-block", width: "8px", height: "8px", borderRadius: "50%", marginRight: ".45rem", verticalAlign: "middle", background: r.active ? "#34d399" : "var(--border)" }}></span>${r.username}</td>
            <td class=muted>${r.active ? humanRate((r.recent_rx_bytes || 0) + (r.recent_tx_bytes || 0), win) : "—"}</td>
            <td>${humanBytes(r.rx_bytes)}</td>
            <td>${humanBytes(r.tx_bytes)}</td>
            <td>${humanBytes(r.total_bytes)}</td>
          </tr>
          ${open === r.user_id && html`<tr key=${r.user_id + "-s"}><td colspan=6 style=${{ background: "var(--bg-soft, transparent)" }}><${TrafficSpark} userId=${r.user_id} /></td></tr>`}`)}
        </tbody>
      </table>
      <p class=muted>${t("All servers")}: ${humanBytes(total)} · ${t("cumulative since each node started, polled from the entry nodes.")} · ${tf("“now” = last {m} min", { m: Math.round(win / 60) })}</p>
    </div>
    ${(data.others || []).length > 0 && html`<${OtherNamespaceTraffic} others=${data.others} />`}`;
}

// OtherNamespaceTraffic summarizes the namespaces the viewer can't see in detail:
// online count and volume per namespace, never a username. Clients stay private;
// the operator still sees how much load the rest of the fleet carries.
function OtherNamespaceTraffic({ others }) {
  return html`
    <div class=card>
      <h3 style=${{ marginTop: 0 }}>${t("Other namespaces")}</h3>
      <table>
        <thead><tr><th>${t("Namespace")}</th><th>${t("Online")}</th><th>${t("Clients")}</th><th>${t("Total")}</th></tr></thead>
        <tbody>
          ${others.map(o => html`<tr key=${o.namespace}>
            <td>${o.namespace || t("infra")}</td>
            <td>${o.online}</td>
            <td class=muted>${o.users}</td>
            <td>${humanBytes(o.total_bytes)}</td>
          </tr>`)}
        </tbody>
      </table>
    </div>`;
}

// Time ranges offered by every traffic graph (minutes → label). Week and month
// are backed by the ~31-day sample retention.
const CHART_RANGES = [[60, "1h"], [360, "6h"], [1440, "24h"], [10080, "7d"], [43200, "30d"]];
const RX_COLOR = "#34d399", TX_COLOR = "#60a5fa"; // ↑ upload/out, ↓ download/in

function RangePicker({ mins, setMins }) {
  return html`<span style=${{ display: "inline-flex", gap: "2px", marginLeft: ".5rem", flexWrap: "wrap" }}>
    ${CHART_RANGES.map(([m, lab]) => html`<button key=${m} class=${"ghost sm" + (mins === m ? " active" : "")} onClick=${e => { e.stopPropagation(); setMins(m); }}>${lab}</button>`)}</span>`;
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
// axis gridlines/labels and a hover readout (value-at-time) — shared by the
// per-user and per-node graphs (no chart library). upLabel/downLabel name the two
// series (client up/down vs node out/in). Moving the cursor over the plot snaps to
// the nearest bucket and shows a guide line, dots on each series and a tooltip.
function SeriesChart({ series, upLabel, downLabel, compact = false }) {
  const W = 600, H = compact ? 48 : 90, padX = 4, padY = compact ? 5 : 10;
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
  // just the clock — otherwise "03:00" is ambiguous across the 7d/30d ranges.
  const firstAt = n ? series[0].at : null, lastAt = n ? series[n - 1].at : null;
  const spanMs = firstAt && lastAt ? (new Date(lastAt) - new Date(firstAt)) : 0;
  const multiDay = spanMs > 24 * 3600 * 1000;
  const fmtAt = (at) => {
    if (!at) return "";
    const d = new Date(at);
    return multiDay
      ? d.toLocaleString([], { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" })
      : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
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
  return html`<div style=${{ padding: compact ? "0" : ".5rem" }} onClick=${e => e.stopPropagation()}>
    <div class=muted style=${{ marginBottom: ".3rem", display: "flex", alignItems: "center", gap: ".9rem", flexWrap: "wrap", fontSize: "11px" }}>
      ${swatch(RX_COLOR, "↑ " + t(upLabel))}${swatch(TX_COLOR, "↓ " + t(downLabel))}
      ${!compact && html`<span>${tf("peak {b} per bucket", { b: humanBytes(peak) })}</span>`}
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
      ${hv && html`<div style=${{ position: "absolute", top: 0, left: hvLeftPct + "%", transform: (hvLeftPct > 60 ? "translateX(-100%)" : "translateX(4px)"), pointerEvents: "none", background: "var(--bg, #111)", border: "1px solid var(--border)", borderRadius: "4px", padding: ".25rem .4rem", fontSize: "11px", whiteSpace: "nowrap", zIndex: 5 }}>
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
// memory MB) instead of a paired rx/tx byte counter — same axis/hover mechanics,
// but the Y axis and hover readout go through `format` instead of the
// bytes-only humanBytes, and there is one line instead of two.
function GaugeChart({ series, valueKey, color, label, format }) {
  const W = 600, H = 90, padX = 4, padY = 10;
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
    return multiDay
      ? d.toLocaleString([], { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" })
      : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
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
  return html`<div style=${{ padding: ".5rem" }} onClick=${e => e.stopPropagation()}>
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
      ${hv && html`<div style=${{ position: "absolute", top: 0, left: hvLeftPct + "%", transform: (hvLeftPct > 60 ? "translateX(-100%)" : "translateX(4px)"), pointerEvents: "none", background: "var(--bg, #111)", border: "1px solid var(--border)", borderRadius: "4px", padding: ".25rem .4rem", fontSize: "11px", whiteSpace: "nowrap", zIndex: 5 }}>
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
  const [mins, setMins] = useState(60);
  const series = useSeriesAt(path, mins);
  return [series, mins, setMins];
}

// TrafficSpark: one user's recent rx/tx, as lines with a legend and range picker.
function TrafficSpark({ userId }) {
  const path = useCallback(m => `/api/traffic/series?user=${encodeURIComponent(userId)}&minutes=${m}`, [userId]);
  const [series, mins, setMins] = useSeries(path);
  return html`<div style=${{ padding: ".3rem .5rem" }} onClick=${e => e.stopPropagation()}>
    <div class=muted style=${{ display: "flex", justifyContent: "flex-end" }}><${RangePicker} mins=${mins} setMins=${setMins} /></div>
    ${series == null ? html`<div class=muted style=${{ padding: ".5rem" }}>${t("Loading…")}</div>`
      : series.length === 0 ? html`<div class=muted style=${{ padding: ".5rem" }}>${t("no traffic in this window")}</div>`
      : html`<${SeriesChart} series=${series} upLabel="Uploaded" downLabel="Downloaded" />`}
  </div>`;
}

// NodeSparkTabs: the Nodes-tab row-expand graph, with Traffic/CPU/Memory as
// sibling views sharing one range control (metric switcher on the left, the
// existing 1h–30d RangePicker stays on the right) instead of three separate
// graphs. CPU is rendered as 0-100% utilization (load1/cores); Memory as used
// MB against total. Both series hooks run unconditionally every render (Rules
// of Hooks) — only the inactive one's result goes unused.
function NodeSparkTabs({ nodeId }) {
  const [view, setView] = useState("traffic");
  const [mins, setMins] = useState(60);
  const trafficPath = useCallback(m => `/api/nodes/series?node=${encodeURIComponent(nodeId)}&minutes=${m}`, [nodeId]);
  const resourcePath = useCallback(m => `/api/nodes/resource-series?node=${encodeURIComponent(nodeId)}&minutes=${m}`, [nodeId]);
  const trafficSeries = useSeriesAt(trafficPath, mins);
  const resourceSeries = useSeriesAt(resourcePath, mins);
  const views = [["traffic", t("Traffic")], ["cpu", t("CPU")], ["mem", t("Memory")]];
  const empty = (msg) => html`<div class=muted style=${{ padding: ".5rem" }}>${msg}</div>`;
  return html`<div style=${{ padding: ".3rem .5rem" }} onClick=${e => e.stopPropagation()}>
    <div style=${{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: ".3rem" }}>
      <span style=${{ display: "inline-flex", gap: "2px" }}>
        ${views.map(([k, lab]) => html`<button key=${k} class=${"ghost sm" + (view === k ? " active" : "")} onClick=${e => { e.stopPropagation(); setView(k); }}>${lab}</button>`)}
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

/* ---------------- language toggle ---------------- */
function LangToggle({ lang, switchLang }) {
  const btn = (l, label) => html`<button class=${"ghost sm" + (lang === l ? " active" : "")}
    style=${lang === l ? { fontWeight: 700 } : {}} onClick=${() => switchLang(l)}>${label}</button>`;
  return html`<span title=${t("Language")} style=${{ display: "inline-flex", gap: "2px" }}>${btn("en", "EN")}${btn("ru", "RU")}</span>`;
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
function NodesPanel({ sc, state, overview, reload, notify, setModal, isAdmin, me }) {
  // Operators manage their own nodes (provision/drain/decommission) and their own
  // domains/TLS, viewing the rest read-only; HA is infra-only (admin).
  const tabs = isAdmin
    ? [["nodes", t("Nodes")], ["domains", t("Domains")], ["ha", t("High availability")]]
    : [["nodes", t("Nodes")], ["domains", t("Domains")]];
  const [sub, setSub] = useSubtab("nodes", "nodes", tabs.map(([k]) => k));
  const cur = tabs.some(([k]) => k === sub) ? sub : "nodes";
  return html`
    <${SubTabs} tabs=${tabs} active=${cur} onSelect=${setSub} />
    ${cur === "nodes"
      ? html`<${EntityPanel} cfg=${sc.nodes} rows=${state.nodes || []} state=${state} reload=${reload} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${me} />`
      : cur === "domains"
      ? html`<${EntityPanel} cfg=${sc.domains} rows=${state.domains || []} state=${state} reload=${reload} notify=${notify} setModal=${setModal} isAdmin=${isAdmin} me=${me} />`
      : html`<${HA} state=${state} overview=${overview} notify=${notify} reload=${reload} />`}`;
}

/* ---------------- clients tab (users + groups) ---------------- */
// Users is the default subtab (the day-to-day surface); Groups is the policy
// scaffolding behind them. Traffic moved out to its own top-level tab.
function Clients({ state, reload, notify, setModal }) {
  const sc = schemas(state, null);
  const [sub, setSub] = useSubtab("clients", "users", ["users", "groups"]);
  // Visibility is enforced server-side: the state endpoint returns only this
  // account's namespace (the bootstrap owner sees every namespace ONLY while the
  // buried cross-namespace lens is on). So the table simply renders what it got —
  // no client-side namespace filter, no cross-tenant rows to leak.
  const users = state.users || [];
  const groups = state.groups || [];
  return html`
    <${SubTabs} tabs=${[["users", t("Users")], ["groups", t("Groups")]]} active=${sub} onSelect=${setSub} />
    ${sub === "users"
      ? html`<${EntityPanel} cfg=${sc.users} rows=${users} state=${state} reload=${reload} notify=${notify} setModal=${setModal} />`
      : html`<${EntityPanel} cfg=${sc.groups} rows=${groups} state=${state} reload=${reload} notify=${notify} setModal=${setModal} />`}`;
}

/* ---------------- logs tab (problems board + event feed) ---------------- */
// The fleet owner gets the full board (infra problems + the infra event feed);
// an operator gets only its own namespace's journal (the event feed) — the
// problems board and the overview/settings it needs are infra-only.
function Logs({ notify, isAdmin = true }) {
  const [events, setEvents] = useState(null);
  const [kind, setKind] = useState("");
  const [state, setState] = useState({});
  const [overview, setOverview] = useState(null);
  const [settings, setSettings] = useState(null);
  const load = useCallback(async () => {
    try {
      const q = kind ? "?kind=" + encodeURIComponent(kind) : "";
      if (!isAdmin) { const e = await api("GET", "/api/events" + q); setEvents(e.events || []); return; }
      const [e, s, ov, set] = await Promise.all([
        api("GET", "/api/events" + q),
        api("GET", "/api/state"),
        api("GET", "/api/overview").catch(() => null),
        api("GET", "/api/settings").catch(() => null),
      ]);
      setEvents(e.events || []); setState(s); setOverview(ov); setSettings(set);
    } catch (e) { notify(e.message, "err"); }
  }, [kind, notify, isAdmin]);
  useEffect(() => { load(); }, [load]);

  const problems = isAdmin ? computeProblems(state, overview, settings) : [];
  const kinds = ["", "alert", "admin", "backup", "system"];
  const kindLabel = (k) => k === "" ? t("All") : t(k);
  const sevColor = (s) => s === "critical" ? "#f87171" : s === "warn" ? "#f59e0b" : "#9ca3af";

  return html`
    ${isAdmin && html`<div class=card style=${{ borderLeft: problems.length ? "3px solid #f59e0b" : "3px solid #34d399" }}>
      <strong>${problems.length ? "⚠ " + t("Needs attention") + " (" + problems.length + ")" : "✓ " + t("All systems normal")}</strong>
      ${problems.length > 0 && html`<div style=${{ marginTop: ".5rem", display: "flex", flexDirection: "column", gap: ".3rem" }}>
        ${problems.map((p, i) => html`<div key=${i} style=${{ fontSize: "13px" }}>
          <span style=${{ color: p.sev === "critical" ? "#f87171" : "#f59e0b" }}>${p.sev === "critical" ? "🔴" : "⚠"}</span> ${p.text}</div>`)}
      </div>`}
    </div>`}
    <div class=card>
      <div class=between>
        <h2 style=${{ margin: 0 }}>${t("Event log")}</h2>
        <div class=row>
          <select value=${kind} onChange=${e => setKind(e.target.value)}>
            ${kinds.map(k => html`<option key=${k} value=${k}>${kindLabel(k)}</option>`)}
          </select>
          <button class=ghost onClick=${load}>${t("Refresh")}</button>
        </div>
      </div>
      <table>
        <thead><tr><th>${t("When")}</th><th>${t("Kind")}</th><th>${t("Message")}</th><th>${t("Actor")}</th></tr></thead>
        <tbody>
          ${events == null && html`<tr><td class=empty colspan=4>${t("Loading…")}</td></tr>`}
          ${events != null && events.length === 0 && html`<tr><td class=empty colspan=4>${t("No events yet")}</td></tr>`}
          ${(events || []).map(ev => html`<tr key=${ev.id}>
            <td style=${{ whiteSpace: "nowrap" }} class=muted>${fmtTime(ev.at)}</td>
            <td><span style=${{ display: "inline-flex", alignItems: "center", gap: ".4rem", whiteSpace: "nowrap" }}><span title=${t(ev.severity)} style=${{ color: sevColor(ev.severity), lineHeight: 1 }}>●</span>${t(ev.kind)}</span></td>
            <td>${ev.message}</td>
            <td class=muted>${ev.actor || ""}</td>
          </tr>`)}
        </tbody>
      </table>
    </div>`;
}
function fmtTime(s) {
  try { const d = new Date(s); return d.toLocaleString(LANG === "ru" ? "ru-RU" : "en-US"); } catch { return String(s || ""); }
}

/* ---------------- HA / standby ---------------- */
// The control plane (Postgres primary + CA + panel) runs on one exit. A standby
// is a second exit kept master-ready: a streaming Postgres replica that also
// holds the CA key, with the panel staged but stopped. Failover is manual
// (promote) — the operator is the fence against split-brain.
//
// serve runs unprivileged (User=trustpanel, NoNewPrivileges, ProtectSystem=strict)
// so it cannot edit pg_hba.conf, run `sudo -u postgres`, or restart Postgres. The
// "Make standby" button therefore routes the privileged work through the per-node
// root AGENTS: the primary's agent does the primary-side Postgres work and
// forwards the secret bundle (incl. the CA key) straight to the standby's agent
// over mTLS — the panel never handles the CA key. The trigger is guarded by the
// session + a CSRF token + an explicit "are you sure?" confirmation. The CLI
// (shown collapsed) stays as the break-glass path when the panel is down.
function HA({ state, overview, notify, reload }) {
  const [open, setOpen] = useState(null);   // node id whose break-glass box is expanded
  const [howto, setHowto] = useState(null); // standby id whose fail-over runbook is expanded
  const [showHelp, setShowHelp] = useState(false);
  const [job, setJob] = useState(null);     // running add-standby job snapshot
  const [busy, setBusy] = useState(null);   // node id currently being provisioned
  const nodes = state.nodes || [];
  const cp = state.control_plane || {};
  const ipOf = n => (n && n.public_ips && n.public_ips[0]) || "";
  const copy = v => navigator.clipboard.writeText(v).then(() => notify(t("Copied"), "ok"), () => {});
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
        t("The node becomes a standby once this finishes."),
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

  const pre = { background: "#0b1020", color: "#cbd5e1", padding: ".7rem .8rem", borderRadius: "8px", overflowX: "auto", fontSize: "12.5px", lineHeight: 1.5, whiteSpace: "pre", margin: ".4rem 0" };
  // Self-resolving promote (the binary fills in this node's id from its local
  // replica), so the runbook is one command with no id to paste. Mirrors #12.
  const promoteCmd = "sudo trustpanel promote --pg-promote --start-serve";

  return html`
    <div class=card>
      <div class=between>
        <h2 style=${{ margin: 0 }}>${t("High availability")}</h2>
        <button class=${"ghost" + (showHelp ? " active" : "")} title=${t("How this works")} aria-label=${t("How this works")} onClick=${() => setShowHelp(h => !h)}>?</button>
      </div>
      ${showHelp && html`<div class=muted style=${{ margin: ".2rem 0 .8rem", padding: ".6rem .8rem", border: "1px solid var(--border)", borderRadius: "8px", lineHeight: 1.5 }}>${t("The master runs the control plane — Postgres, the CA, the panel. The standby is a second exit node, ready to take over. Failover is manual: once the master goes down, the backup alert bot sends the exact promote command. RECOVERY.md, shipped with every backup, has the same steps in case the alert doesn't arrive.")}</div>`}

      ${!primary
        ? html`<div class=muted>${t("no primary set — mark one, or this deployment doesn't use a standby")}</div>`
        : standbys.length === 0
        ? html`<div class=ha-status><span class=ha-dot style=${{ background: "#6b7280" }}></span><span>${t("No standby — failover not possible. Add one below.")}</span></div>`
        : html`
          <div class=muted style=${{ marginBottom: ".5rem" }}>${t("Active control plane")}: <b>${primary.name}</b></div>
          ${standbys.map(sb => {
            const rep = replOf(sb.id);
            const warn = replWarning(rep);
            const checking = rep === undefined;
            const ready = !!rep && !warn;
            const dot = ready ? "#34d399" : warn ? "#f59e0b" : "#6b7280";
            const head = checking ? t("Standby — replication starting") : warn ? t("Standby degraded") : t("Ready to fail over");
            const detail = checking ? t("replication: checking…")
              : warn ? t("warning") + ": " + warn
              : t("streaming") + (rep.bytes_behind != null ? " · " + t("behind") + " " + humanBytes(rep.bytes_behind) : "");
            return html`<div style=${{ borderTop: "1px solid #1f2937", padding: ".7rem 0 .3rem" }}>
              <div class=ha-status><span class=ha-dot style=${{ background: dot }}></span><span>${head}</span></div>
              <div class=muted style=${{ margin: ".25rem 0 .1rem" }}>${t("Standby")}: <b>${sb.name}</b> · ${detail}</div>
              <div style=${{ display: "flex", gap: ".5rem", alignItems: "center", marginTop: ".35rem" }}>
                <button class="ghost sm" onClick=${() => setHowto(howto === sb.id ? null : sb.id)}>${t("How to fail over")} ${howto === sb.id ? "▴" : "▾"}</button>
                <button class=${warn ? "sm" : "ghost sm"} disabled=${!!busy} title=${t("Wipe this replica's PGDATA and re-seed it from the current primary (use if its Postgres died or diverged)")} onClick=${() => rebuildStandby(sb)}>${busy === sb.id ? t("Working…") : t("Rebuild replica")}</button>
              </div>
              ${howto === sb.id && html`<div style=${{ marginTop: ".5rem" }}>
                <div class=muted style=${{ marginBottom: ".3rem", fontSize: "13px" }}>${t("If the primary fails, fail over to this standby — only once the primary is confirmed gone:")}</div>
                <ol class=ha-steps>
                  <li>${tf("SSH into {name} ({ip}).", { name: sb.name, ip: ipOf(sb) })}</li>
                  <li>${t("Run as root:")}<div style=${pre}>${promoteCmd}</div><button class="ghost sm" onClick=${() => copy(promoteCmd)}>${t("Copy command")}</button></li>
                  <li>${t("The panel comes back up on this node; point your tunnel at it.")}</li>
                </ol>
              </div>`}
            </div>`;
          })}`}
    </div>

    <div class=card>
      <h3 style=${{ margin: "0 0 .3rem" }}>${t("Add a standby")}</h3>
      <div class=muted style=${{ marginBottom: ".6rem" }}>${t("Sets up a Postgres replica + CA on an exit so it can take over. The data plane on that exit keeps running.")}</div>
      ${eligible.length === 0
        ? html`<div class=muted>${t("No eligible exit nodes. Add a second exit node first (Nodes → Add server), then come back here.")}</div>`
        : eligible.map(sb => html`<div style=${{ borderTop: "1px solid #1f2937", padding: ".6rem 0" }}>
            <div style=${{ display: "flex", alignItems: "center", gap: ".6rem" }}>
              <div><b>${sb.name}</b> <span class=muted>(${ipOf(sb)})</span></div>
              <div style=${{ flex: 1 }}></div>
              <button class="sm" disabled=${!!busy} onClick=${() => makeStandby(sb)}>${busy === sb.id ? t("Working…") : t("Make standby")}</button>
              <button class="ghost sm" onClick=${() => setOpen(open === sb.id ? null : sb.id)}>${open === sb.id ? t("Hide CLI") : t("Manual (CLI)")}</button>
            </div>
            ${open === sb.id && html`<div style=${{ marginTop: ".5rem" }}>
              <div class=muted style=${{ marginBottom: ".3rem", fontSize: "13px" }}>${t("Break-glass: run this as root on the primary if the panel is unavailable. Same result as the button.")}</div>
              <div style=${pre}>${addCmd(sb)}</div>
              <button class="ghost sm" onClick=${() => copy(addCmd(sb))}>${t("Copy command")}</button>
            </div>`}
          </div>`)}
      ${job && html`<div style=${{ marginTop: ".8rem", borderTop: "1px solid #1f2937", paddingTop: ".6rem" }}>
        <div class=field><label>${t("Add-standby status")}</label><span class=${job.status === "succeeded" ? "ok" : job.status === "failed" ? "err" : "muted"}>${t(job.status)}</span></div>
        <pre style=${pre}>${(job.log || []).join("\n")}${job.error ? "\nERROR: " + job.error : ""}</pre>
        ${job.status !== "running" && html`<button class="ghost sm" onClick=${() => setJob(null)}>${t("Dismiss")}</button>`}
      </div>`}
    </div>`;
}

/* ---------------- bots / alerts ---------------- */
function StatusChip({ s }) {
  const map = {
    ok: ["#34d399", "reachable"],
    unauthorized: ["#f87171", "token rejected"],
    unreachable: ["#f59e0b", "unreachable"],
    unconfigured: ["#6b7280", "not configured"],
  };
  const [color, label] = map[s && s.status] || map.unconfigured;
  return html`<span style=${{ display: "inline-flex", alignItems: "center", gap: ".4rem", fontSize: "12px", color }}>
    <span style=${{ width: "9px", height: "9px", borderRadius: "50%", background: color, display: "inline-block" }}></span>
    ${t(label)}${s && s.detail ? " — " + s.detail : ""}
  </span>`;
}

// TelegramBinding: bind the logged-in account's own Telegram id so the bot answers
// them. The alert destination is the single infra "Alert chat ID" — not repeated
// here — so this stays one field. The stored per-account alert chat is preserved
// as-is across a save.
function TelegramBinding({ notify }) {
  const [tg, setTg] = useState(""); const [chat, setChat] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    (async () => {
      try {
        const r = await api("GET", "/api/admins"); const me = (r.admins || []).find(a => a.is_current);
        if (me) setChat(me.alert_chat_id || "");
      } catch {}
    })();
  }, []);
  const save = async () => {
    const id = tg.trim() === "" ? 0 : Number(tg.trim());
    if (tg.trim() !== "" && !Number.isInteger(id)) { notify(t("Telegram id must be a number"), "err"); return; }
    setBusy(true);
    try { await api("POST", "/api/account/telegram", { telegram_id: id, alert_chat_id: chat }); notify(t("Saved"), "ok"); setTg(""); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };
  return html`
    <div class=field><label>${t("Your Telegram id")}</label><input value=${tg} onInput=${e => setTg(e.target.value)} placeholder=${t("leave blank to keep / 0 to unbind")} /></div>
    <button class=ghost disabled=${busy} onClick=${save}>${t("Save Telegram binding")}</button>`;
}

// AccountSettings: self-service for the logged-in account — just the password.
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
    <div class=card>
      <h2 style=${{ marginTop: 0 }}>${t("Account & security")}</h2>
      <h3 style=${{ margin: "1rem 0 .3rem" }}>${t("Change your password")}</h3>
      <div class=field><label>${t("Current password")}</label><${PasswordInput} value=${cur} onChange=${setCur} autocomplete="current-password" /></div>
      <div class=field><label>${t("New password")}</label><${PasswordInput} value=${nw} onChange=${setNw} autocomplete="new-password" /></div>
      <div class=field><label>${t("Confirm new password")}</label><${PasswordInput} value=${nw2} onChange=${setNw2} autocomplete="new-password" /></div>
      <button disabled=${busy} onClick=${changePw}>${t("Change password")}</button>
    </div>`;
}

// Members: account/namespace membership management for the bootstrap owner —
// add/remove accounts and flip their role. Lives as a subtab under Settings
// (visible only when can_manage_members), kept out of one's personal account tab.
function Members({ notify, isAdmin = true, myNs = "" }) {
  const [admins, setAdmins] = useState([]);
  const [newUser, setNewUser] = useState(""); const [newPw, setNewPw] = useState(""); const [newPw2, setNewPw2] = useState(""); const [newRole, setNewRole] = useState("operator"); const [newTg, setNewTg] = useState("");
  const [busy, setBusy] = useState(false);
  // roleDraft holds an in-progress role change that is missing a field the new
  // role requires (a password for a fresh admin, a Telegram id for a fresh
  // operator) — collected inline before the confirm dialog fires. null when no
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
    try { await api("DELETE", "/api/admins/" + encodeURIComponent(u)); notify(t("Deleted"), "ok"); await load(); }
    catch (e) { notify(e.message, "err"); }
  };
  // changeRole always confirms (a role change is a privilege change), then
  // submits whatever extra field (password/telegram_id) the new role needed.
  const changeRole = async (username, next, extra) => {
    const label = next === "admin" ? t("Admin") : t("Operator");
    if (!await uiConfirm({ title: tf("Change {name}'s role to {role}?", { name: username, role: label }), confirmLabel: t("Change role") })) return;
    try {
      await api("POST", "/api/admins/" + encodeURIComponent(username) + "/role", { role: next, ...extra });
      notify(t("Saved"), "ok"); setRoleDraft(null); await load();
    } catch (e) { notify(e.message, "err"); }
  };
  // startRoleChange decides whether the target already has what its new role
  // needs (an admin-to-be may already carry a password from before, an
  // operator-to-be may already have Telegram bound) — if so it confirms right
  // away, otherwise it opens the inline field first.
  const startRoleChange = (a) => {
    const next = a.role === "operator" ? "admin" : "operator";
    const needPw = next === "admin" && !a.has_password;
    const needTg = next === "operator" && !a.has_telegram;
    if (!needPw && !needTg) { changeRole(a.username, next, {}); return; }
    setRoleDraft({ username: a.username, next, needPw, needTg, value: "" });
  };
  const submitRoleDraft = async () => {
    const d = roleDraft;
    if (d.needPw) {
      if (d.value.length < 8) { notify(t("password must be at least 8 characters"), "err"); return; }
      await changeRole(d.username, d.next, { password: d.value });
      return;
    }
    const tg = Number(d.value.trim());
    if (!d.value.trim() || !Number.isInteger(tg)) { notify(t("Telegram id must be a number"), "err"); return; }
    await changeRole(d.username, d.next, { telegram_id: tg });
  };
  const nsLabel = (a) => a.namespace || "private";
  const cols = isAdmin ? 5 : 4;
  return html`
    <div class=card>
      <h2 style=${{ marginTop: 0 }}>${isAdmin ? t("Accounts") : t("Members")}</h2>
      ${!isAdmin && html`<p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${tf("Members of your namespace ({ns}) share its clients. Admins can manage members; operators only the clients.", { ns: myNs })}</p>`}
      <table>
        <thead><tr>
          <th>${t("Username")}</th>
          ${isAdmin && html`<th>${t("Namespace")}</th>`}
          <th>${t("Telegram")}</th>
          <th>${t("Created")}</th>
          <th></th>
        </tr></thead>
        <tbody>
          ${admins.flatMap(a => [
            html`<tr key=${a.username}>
              <td>${a.username} <span class=${"pill " + (a.role === "operator" ? "direct" : "exit")}>${a.role === "operator" ? t("Operator") : t("Admin")}</span> ${a.is_current && html`<span class=muted>(${t("this account")})</span>`}</td>
              ${isAdmin && html`<td class=muted>${nsLabel(a)}</td>`}
              <td class=muted>${a.has_telegram ? "TG ✓" : ""}</td>
              <td class=muted>${a.created_at}</td>
              <td style=${{ textAlign: "right" }}>
                ${!a.is_current && html`<button class="ghost sm" title=${t("Switch between admin and operator")} onClick=${() => startRoleChange(a)}>${a.role === "operator" ? t("Make admin") : t("Make operator")}</button>`}
                ${!a.is_current && html`<button class="danger sm" onClick=${() => delAdmin(a.username)}>${t("Delete")}</button>`}
              </td>
            </tr>`,
            roleDraft && roleDraft.username === a.username ? html`<tr key=${a.username + "-rc"}>
              <td colspan=${cols}>
                <div class=row style=${{ alignItems: "flex-end", gap: ".5rem" }}>
                  <span class=muted style=${{ fontSize: "12px" }}>${roleDraft.needPw ? t("A password is required to make this account an admin.") : t("A Telegram id is required to make this account an operator.")}</span>
                  ${roleDraft.needPw
                    ? html`<div class=field style=${{ margin: 0 }}><label>${t("New password")}<span class=err> *</span></label><${PasswordInput} value=${roleDraft.value} onChange=${v => setRoleDraft({ ...roleDraft, value: v })} autocomplete="new-password" /></div>`
                    : html`<div class=field style=${{ margin: 0 }}><label>${t("Telegram id")}<span class=err> *</span></label><input value=${roleDraft.value} onInput=${e => setRoleDraft({ ...roleDraft, value: e.target.value })} /></div>`}
                  <button class=ghost onClick=${submitRoleDraft}>${t("Change role")}</button>
                  <button class=ghost onClick=${() => setRoleDraft(null)}>${t("Cancel")}</button>
                </div>
              </td>
            </tr>` : null,
          ])}
        </tbody>
      </table>
      <div class=row style=${{ marginTop: ".5rem", alignItems: "flex-end" }}>
        <div class=field style=${{ flex: 1, margin: 0 }}><label>${t("Username")}</label><input value=${newUser} onInput=${e => setNewUser(e.target.value)} /></div>
        ${newRole === "admin" && html`<div class=field style=${{ flex: 1, margin: 0 }}><label>${t("New password")}<span class=err> *</span></label><${PasswordInput} value=${newPw} onChange=${setNewPw} autocomplete="new-password" /></div>`}
        ${newRole === "admin" && html`<div class=field style=${{ flex: 1, margin: 0 }}><label>${t("Confirm password")}<span class=err> *</span></label><${PasswordInput} value=${newPw2} onChange=${setNewPw2} autocomplete="new-password" /></div>`}
        <div class=field style=${{ margin: 0 }}><label>${t("Role")}</label><select value=${newRole} onChange=${e => { const r = e.target.value; setNewRole(r); if (r === "operator") { setNewPw(""); setNewPw2(""); } }}><option value=operator>${t("Operator")}</option><option value=admin>${t("Admin")}</option></select></div>
        <div class=field style=${{ flex: 1, margin: 0 }}><label>${t("Telegram id")}${newRole === "operator" && html`<span class=err> *</span>`}</label><input value=${newTg} onInput=${e => setNewTg(e.target.value)} placeholder=${newRole === "operator" ? "" : t("optional")} /></div>
        <button class=ghost disabled=${busy} onClick=${addAdmin}>${isAdmin ? t("Add account") : t("Add member")}</button>
      </div>
      ${isAdmin && html`<p class=muted style=${{ fontSize: "12px" }}>${t("Admins share the infrastructure. Each operator's namespace is isolated from the rest.")}</p>`}
    </div>`;
}

// FleetAndPanelSettings: fleet-wide provisioning defaults + panel runtime tunables.
function FleetAndPanelSettings({ fleet, setFleet, panel, setPanel }) {
  const numField = (label, key, hint) => html`<div class=field><label>${label}</label>
    <input type=number min=0 value=${panel[key]} onInput=${e => setPanel({ ...panel, [key]: e.target.value })} />
    ${hint && html`<div class=hint>${hint}</div>`}</div>`;
  return html`
    <div class=card>
      <h2 style=${{ margin: 0 }}>${t("Server defaults")}</h2>
      <p class=muted style=${{ marginTop: ".3rem", fontSize: "12px" }}>${t("Pre-filled when you add a new server.")}</p>
      <div class=field><label>${t("ACME contact email")}</label><input value=${fleet.acme_email} onInput=${e => setFleet({ ...fleet, acme_email: e.target.value })} placeholder="admin@example.com" /></div>
      <div class=field><label>${t("Default Reality SNI")}</label><input value=${fleet.reality_sni} onInput=${e => setFleet({ ...fleet, reality_sni: e.target.value })} placeholder="www.example-cdn.com" /></div>
      <div class=field><label>${t("Apex domain")}</label><input value=${fleet.apex} onInput=${e => setFleet({ ...fleet, apex: e.target.value })} placeholder="example.com" /></div>

      <h2 style=${{ margin: "1.4rem 0 .3rem" }}>${t("Panel & schedules")}</h2>
      <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Applied live — no restart needed. Blank/0 uses the built-in default.")}</p>
      ${numField(t("Reconcile interval (seconds)"), "reconcile_seconds")}
      ${numField(t("Stats poll interval (seconds)"), "stats_seconds")}
      ${numField(t("Billing check interval (hours)"), "billing_alert_hours")}
      ${numField(t("Warn N days before payment due"), "billing_alert_days")}
      ${numField(t("Warn N days before a client config expires"), "expiry_alert_days")}
      ${numField(t("Session lifetime (hours)"), "session_hours")}
      ${numField(t("Auto egress-failover after exit down (seconds)"), "egress_failover_seconds", t("A dead exit's groups auto-move to a healthy exit after this long. Min 60s."))}
      ${numField(t("Online threshold (KB/min)"), "activity_kb_per_min", t("A client counts as online only above this traffic rate."))}
    </div>`;
}

function SettingsPanel({ notify, isBootstrap = false, canManage = false, myNs = "", crossNs = false, toggleCrossNs = () => {} }) {
  const subKeys = ["server", "bots", "backup", "account"].concat(canManage ? ["members"] : []).concat(isBootstrap ? ["advanced"] : []);
  const [sub, setSub] = useSubtab("settings", "server", subKeys);
  const [cfg, setCfg] = useState(null);
  const [bot, setBot] = useState({ enabled: false, token: "" });
  const [alert, setAlert] = useState({ enabled: false, token: "", chat_id: "" });
  const [backup, setBackup] = useState({ enabled: false, chat_id: "", age_recipient: "", chunk_bytes: 0, local_enabled: true, interval_hours: 24, keep: 14, verify_enabled: true, verify_interval_days: 7 });
  const [fleet, setFleet] = useState({ acme_email: "", reality_sni: "", brand: "", apex: "" });
  const [panel, setPanel] = useState({ reconcile_seconds: 0, stats_seconds: 0, billing_alert_hours: 0, billing_alert_days: 0, expiry_alert_days: 0, session_hours: 0, egress_failover_seconds: 0, activity_kb_per_min: 0 });
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await api("GET", "/api/settings");
      setCfg(s);
      setBot({ enabled: s.bot.enabled, token: "" });
      setAlert({ enabled: s.alert.enabled, token: "", chat_id: s.alert.chat_id || "" });
      const b = s.backup || {};
      setBackup({ enabled: b.enabled || false, chat_id: b.chat_id || "", age_recipient: b.age_recipient || "", chunk_bytes: b.chunk_bytes || 0, local_enabled: b.local_enabled !== false, interval_hours: b.interval_hours || 24, keep: b.keep || 14, verify_enabled: b.verify_enabled !== false, verify_interval_days: b.verify_interval_days || 7 });
      setFleet({ acme_email: (s.fleet && s.fleet.acme_email) || "", reality_sni: (s.fleet && s.fleet.reality_sni) || "", brand: (s.fleet && s.fleet.brand) || "", apex: (s.fleet && s.fleet.apex) || "" });
      const pp = s.panel || {};
      setPanel({ reconcile_seconds: pp.reconcile_seconds || 0, stats_seconds: pp.stats_seconds || 0, billing_alert_hours: pp.billing_alert_hours || 0, billing_alert_days: pp.billing_alert_days || 0, expiry_alert_days: pp.expiry_alert_days || 0, session_hours: pp.session_hours || 0, egress_failover_seconds: pp.egress_failover_seconds || 0, activity_kb_per_min: pp.activity_kb_per_min || 0 });
      return s;
    } catch (e) { notify(e.message, "err"); return null; }
  }, [notify]);
  useEffect(() => { load(); }, [load]);

  const save = async () => {
    setBusy(true);
    const before = cfg ? { bot: cfg.bot.checked_at || 0, alert: cfg.alert.checked_at || 0 } : { bot: 0, alert: 0 };
    let ok = false;
    try {
      await api("POST", "/api/settings", {
        bot: { enabled: bot.enabled, token: bot.token },
        alert: { enabled: alert.enabled, token: alert.token, chat_id: (alert.chat_id || "").trim() },
        backup: { enabled: backup.enabled, chat_id: (backup.chat_id || "").trim(), age_recipient: (backup.age_recipient || "").trim(), chunk_bytes: parseInt(backup.chunk_bytes, 10) || 0, local_enabled: backup.local_enabled, interval_hours: parseInt(backup.interval_hours, 10) || 0, keep: parseInt(backup.keep, 10) || 0, verify_enabled: backup.verify_enabled, verify_interval_days: parseInt(backup.verify_interval_days, 10) || 0 },
        fleet: { acme_email: (fleet.acme_email || "").trim(), reality_sni: (fleet.reality_sni || "").trim(), brand: (fleet.brand || "").trim(), apex: (fleet.apex || "").trim() },
        panel: { reconcile_seconds: parseInt(panel.reconcile_seconds, 10) || 0, stats_seconds: parseInt(panel.stats_seconds, 10) || 0, billing_alert_hours: parseInt(panel.billing_alert_hours, 10) || 0, billing_alert_days: parseInt(panel.billing_alert_days, 10) || 0, expiry_alert_days: parseInt(panel.expiry_alert_days, 10) || 0, session_hours: parseInt(panel.session_hours, 10) || 0, egress_failover_seconds: parseInt(panel.egress_failover_seconds, 10) || 0, activity_kb_per_min: parseInt(panel.activity_kb_per_min, 10) || 0 },
      });
      ok = true;
    } catch (e) { notify(e.message, "err"); }
    setBusy(false);
    if (!ok) return;
    notify(t("Saved"), "ok");
    // Saving fires a live Telegram getMe check for both channels in the background
    // (it can't block the save — a dead token stalls the request up to 10s). The
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

  if (!cfg) return html`<div class=card>${t("Loading…")}</div>`;
  const tokenPlaceholder = (set, last4) => set ? t("set") + " ••••" + (last4 || "") + " — " + t("leave blank to keep") : t("paste bot token");
  const saveBar = () => html`<div class=savebar><button disabled=${busy} onClick=${save}>${busy ? t("Saving…") : t("Save")}</button></div>`;

  // Subtabs keep the settings focused: one section per screen instead of one long
  // scroll. Every section still saves through the same endpoint (blank tokens are
  // preserved server-side), so a Save on any tab persists the whole form safely.
  const tabs = [["server", t("Server")], ["bots", t("Bots & alerts")], ["backup", t("Backup")], ["account", t("Account")]];
  if (canManage) tabs.push(["members", t("Members")]);
  if (isBootstrap) tabs.push(["advanced", t("Advanced")]);
  const cur = tabs.some(([k]) => k === sub) ? sub : "server";

  return html`
    <${SubTabs} tabs=${tabs} active=${cur} onSelect=${setSub} />
    ${cur === "server" && html`
      <${FleetAndPanelSettings} fleet=${fleet} setFleet=${setFleet} panel=${panel} setPanel=${setPanel} />
      ${saveBar()}`}

    ${cur === "bots" && html`
      <div class=card>
        <h3 style=${{ marginTop: 0 }}>${t("Management bot")} <span style=${{ fontWeight: 400 }}>${html`<${StatusChip} s=${cfg.bot} />`}</span>${" "}<button class="ghost sm" onClick=${() => testChannel("bot")}>${t("Test")}</button></h3>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Runs the commands and, by default, sends the alerts.")}</p>
        <div class="field check"><input type=checkbox checked=${bot.enabled} onChange=${e => setBot({ ...bot, enabled: e.target.checked })} /><label style=${{ margin: 0 }}>${t("Enabled")}</label></div>
        <div class=field><label>${t("Bot token")}</label>
          <input type=password value=${bot.token} placeholder=${tokenPlaceholder(cfg.bot.token_set, cfg.bot.token_last4)} onInput=${e => setBot({ ...bot, token: e.target.value })} />
          <div class=hint>${t("From @BotFather.")}</div>
        </div>

        <h3 style=${{ margin: "1.4rem 0 .3rem" }}>${t("My bot access")}</h3>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Your Telegram id, so the bot answers you.")}</p>
        <${TelegramBinding} notify=${notify} />

        <h3 style=${{ margin: "1.4rem 0 .3rem" }}>${t("Alerts")} <span style=${{ fontWeight: 400 }}>${html`<${StatusChip} s=${cfg.alert} />`}</span>${" "}<button class="ghost sm" onClick=${() => testChannel("alert")}>${t("Test")}</button></h3>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Infra alerts (payments, watchdog) — sent by the management bot.")}</p>
        <div class="field check"><input type=checkbox checked=${alert.enabled} onChange=${e => setAlert({ ...alert, enabled: e.target.checked })} /><label style=${{ margin: 0 }}>${t("Send alerts")}</label></div>
        <div class=field><label>${t("Alert chat ID")}</label>
          <input value=${alert.chat_id} placeholder="-1001234567890" onInput=${e => setAlert({ ...alert, chat_id: e.target.value })} />
        </div>

        <h4 style=${{ margin: "1.2rem 0 .3rem" }}>${t("Backup alert bot")}${" "}<button class="ghost sm" onClick=${() => testChannel("alert_backup")}>${t("Test")}</button></h4>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("The standby uses it to page you if the primary goes down.")}</p>
        <div class=field><label>${t("Backup bot token")}</label>
          <input type=password value=${alert.token} placeholder=${tokenPlaceholder(cfg.alert.token_set, cfg.alert.token_last4)} onInput=${e => setAlert({ ...alert, token: e.target.value })} />
        </div>
        ${cfg.alert.shares_bot_channel && html`<p class=warn style=${{ marginTop: ".5rem", fontSize: "12px" }}>⚠ ${t("No separate management bot is set, so primary and backup alerts use this same bot — both test buttons check one channel. Set a management bot above for two independent channels.")}</p>`}
        <p class=muted style=${{ marginTop: "1rem", fontSize: "12px" }}>${t("Test uses the saved settings — save first, then test.")}</p>
      </div>
      ${saveBar()}`}

    ${cur === "backup" && html`
      <div class=card>
        <h3 style=${{ marginTop: 0 }}>${t("Backup schedule & retention")}</h3>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Local snapshots (database + CA), one timer per node.")}</p>
        <div class="field check"><input type=checkbox checked=${backup.local_enabled} onChange=${e => setBackup({ ...backup, local_enabled: e.target.checked })} /><label style=${{ margin: 0 }}>${t("Local backup enabled")}</label></div>
        <div class=field><label>${t("Run every (hours)")}</label>
          <input type=number min=1 value=${backup.interval_hours} onInput=${e => setBackup({ ...backup, interval_hours: e.target.value })} />
        </div>
        <div class=field><label>${t("Copies to keep")}</label>
          <input type=number min=1 value=${backup.keep} onInput=${e => setBackup({ ...backup, keep: e.target.value })} />
        </div>
        <div class="field check"><input type=checkbox checked=${backup.verify_enabled} onChange=${e => setBackup({ ...backup, verify_enabled: e.target.checked })} /><label style=${{ margin: 0 }}>${t("Verify-restore drill enabled")}</label></div>
        <div class=field><label>${t("Verify every (days)")}</label>
          <input type=number min=1 value=${backup.verify_interval_days} onInput=${e => setBackup({ ...backup, verify_interval_days: e.target.value })} />
        </div>

        <h3 style=${{ margin: "1.4rem 0 .3rem" }}>${t("Backup to Telegram")}${" "}<button class="ghost sm" onClick=${() => testChannel("backup")}>${t("Test")}</button></h3>
        <p class=muted style=${{ marginTop: 0, fontSize: "12px" }}>${t("Age-encrypted snapshots sent to a private Telegram channel, so a copy survives losing every server.")}</p>
        <div class="field check"><input type=checkbox checked=${backup.enabled} onChange=${e => setBackup({ ...backup, enabled: e.target.checked })} /><label style=${{ margin: 0 }}>${t("Enabled")}</label></div>
        <div class=field><label>${t("Backup chat ID")}</label>
          <input value=${backup.chat_id} placeholder="-1009876543210" onInput=${e => setBackup({ ...backup, chat_id: e.target.value })} />
        </div>
        <div class=field><label>${t("age recipient (public key)")}</label>
          <input value=${backup.age_recipient} placeholder="age1…" onInput=${e => setBackup({ ...backup, age_recipient: e.target.value })} />
          <div class=hint>${t("Generate with age-keygen; paste only the public key (age1…). Keep the private key OFFLINE.")}</div>
        </div>
        <div class=field><label>${t("Part size (bytes, 0 = default ~45 MiB)")}</label>
          <input type=number value=${backup.chunk_bytes} onInput=${e => setBackup({ ...backup, chunk_bytes: e.target.value })} />
        </div>
        <p class=muted style=${{ marginTop: "1rem", fontSize: "12px" }}>${t("Test uses the saved settings — save first, then test.")}</p>
      </div>
      ${saveBar()}`}

    ${cur === "account" && html`<${AccountSettings} notify=${notify} />`}

    ${cur === "members" && canManage && html`<${Members} notify=${notify} isAdmin=${true} myNs=${myNs} />`}

    ${cur === "advanced" && isBootstrap && html`<${AdvancedSettings} crossNs=${crossNs} toggleCrossNs=${toggleCrossNs} />`}`;
}

// AdvancedSettings is the buried developer surface (bootstrap owner only). Its
// sole control is the cross-namespace lens — a deliberate, occasional act — kept
// out of the everyday settings behind its own "Advanced" subtab.
function AdvancedSettings({ crossNs, toggleCrossNs }) {
  const flip = async () => {
    if (!crossNs) {
      const ok = await uiConfirm({
        title: t("Show every namespace?"),
        lines: [t("Shows other tenants' clients, groups and routes across the panel. Turns off when you sign out.")],
        confirmLabel: t("Show all namespaces"),
        danger: true,
      });
      if (!ok) return;
    }
    toggleCrossNs(!crossNs);
  };
  return html`
    <div class=card>
      <div class=between style=${{ alignItems: "center" }}>
        <div>
          <div style=${{ fontWeight: 600 }}>${t("Cross-namespace view")}</div>
          <div class=muted style=${{ fontSize: "12px" }}>${crossNs ? t("On — you are seeing every namespace.") : t("Off — you see only your own namespace.")}</div>
        </div>
        <button class=${crossNs ? "danger" : "ghost"} onClick=${flip}>${crossNs ? t("Turn off") : t("Turn on")}</button>
      </div>
    </div>`;
}

/* ---------------- entity table ---------------- */
function EntityPanel({ cfg, rows, state, reload, notify, setModal, isAdmin = true, me = "", initialFilter = "" }) {
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
  const [showHelp, setShowHelp] = useState(false);
  const [spark, setSpark] = useState(null); // row id whose throughput graph is expanded
  const [q, setQ] = useState(initialFilter); // seeded after a cross-tab jump (Groups → Routing)
  const query = q.trim().toLowerCase();
  const shown = cfg.filter && query ? rows.filter(it => cfg.filter(it, query, state)) : rows;
  const del = async (it) => {
    const name = it.name || it.display_name || it.username || it.hostname || it.id;
    if (!await uiConfirm({ title: tf("Delete {name}?", { name }), confirmLabel: t("Delete"), danger: true })) return;
    try { await api("DELETE", cfg.path + "/" + encodeURIComponent(it.id)); reload(); notify(t("Deleted"), "ok"); }
    catch (e) { notify(e.message, "err"); }
  };
  const toggle = async (it) => {
    try { await api("POST", cfg.path, cfg.toggle.next(it)); reload(); notify(it[cfg.toggle.field] ? t("Enabled") : t("Disabled"), "ok"); }
    catch (e) { notify(e.message, "err"); }
  };
  const promoteTLS = async (it) => {
    if (!it.node_id) { notify(t("This domain has no node to promote"), "err"); return; }
    if (!await uiConfirm({ title: t("Switch this node's certificate from staging to Let's Encrypt production and reissue now?"), confirmLabel: t("→ prod") })) return;
    try { await api("POST", "/api/nodes/" + encodeURIComponent(it.node_id) + "/promote-tls"); reload(); notify(t("Reissued from production"), "ok"); }
    catch (e) { notify(e.message, "err"); }
  };
  // ---- bulk multi-select (proposals §5; opt-in via cfg.bulk) ----
  const [sel, setSel] = useState(() => new Set());
  const [bulkGroup, setBulkGroup] = useState("");
  const [bulkExp, setBulkExp] = useState("");
  const shownIds = shown.map(it => it.id);
  const allSel = shownIds.length > 0 && shownIds.every(id => sel.has(id));
  const toggleAll = () => setSel(allSel ? new Set() : new Set(shownIds));
  const toggleOne = (id) => setSel(s => { const n = new Set(s); n.has(id) ? n.delete(id) : n.add(id); return n; });
  const groups = state && state.groups || [];
  const bulk = async (action, extra) => {
    if (sel.size === 0) return;
    try {
      const res = await api("POST", cfg.path + "/bulk", { ids: [...sel], action, ...(extra || {}) });
      setSel(new Set());
      await reload();
      notify(tf("{n} updated", { n: res.applied }) + (res.failed ? " · " + tf("{n} failed", { n: res.failed }) : ""), res.failed ? "err" : "ok");
    } catch (e) { notify(e.message, "err"); }
  };
  const bulkConfirm = async (action, label) => {
    if (sel.size === 0) return;
    if (!await uiConfirm({ title: tf("{label} {n} selected?", { label, n: sel.size }), confirmLabel: label, danger: action === "delete" })) return;
    bulk(action);
  };
  const bulkSetExpiry = async () => {
    const iso = parseDMY(bulkExp);
    if (!iso) { notify(tf("Enter {label} as dd/mm/yyyy", { label: t("Expires") }), "err"); return; }
    bulk("set_expiry", { expires_at: iso + "T23:59:59Z" });
  };
  return html`
    <div class=card>
      <div class=between>
        <h2 style=${{ margin: 0 }}>${t(cfg.title)}</h2>
        <div class=row>
          ${cfg.filter && html`<input class=filter value=${q} placeholder=${t("Filter…")} onInput=${e => setQ(e.target.value)} />`}
          ${cfg.help && html`<button class=${"ghost" + (showHelp ? " active" : "")} title=${t("How this works")} aria-label=${t("How this works")} onClick=${() => setShowHelp(h => !h)}>?</button>`}
          ${cfg.tester && html`<button class=ghost onClick=${() => setModal({ kind: "routetest" })}>${t("Route tester")}</button>`}
          ${cfg.provision && isAdmin && rows.length === 1 && rows[0].public_role === "entry" && html`<button class=ghost title=${t("Add an entry in front and flip this box to the exit (control plane stays here)")} onClick=${() => setModal({ kind: "convert" })}>${t("Convert to two-node")}</button>`}
          ${cfg.provision && html`<button onClick=${() => setModal({ kind: "provision" })}>+ ${t("Add server")}</button>`}
          ${!cfg.provision && html`<button onClick=${() => setModal({ kind: "form", cfg, initial: null })}>+ ${t("Add")} ${t(cfg.singular)}</button>`}
        </div>
      </div>
      ${cfg.help && showHelp && html`<div style=${{ margin: ".2rem 0 .9rem", padding: ".6rem .8rem", border: "1px solid var(--border)", borderRadius: "8px", background: "var(--bg-soft, transparent)" }}>
        <strong>${t(cfg.help.title)}</strong>
        <ul style=${{ margin: ".5rem 0 0", paddingLeft: "1.1rem" }}>
          ${cfg.help.points.map((p, i) => html`<li key=${i} class=muted style=${{ margin: ".25rem 0", fontSize: "12px", lineHeight: 1.4 }}>${t(p)}</li>`)}
        </ul>
      </div>`}
      ${cfg.bulk && sel.size > 0 && html`<div class=row style=${{ alignItems: "center", gap: ".5rem", flexWrap: "wrap", margin: ".2rem 0 .7rem", padding: ".5rem .7rem", border: "1px solid var(--accent, var(--border))", borderRadius: "8px", background: "var(--bg-soft, transparent)" }}>
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
        <input style=${{ width: "8rem" }} type=text inputmode=numeric placeholder=${t("dd/mm/yyyy")} value=${bulkExp} onInput=${e => setBulkExp(e.target.value)} />
        <button class="ghost sm" onClick=${bulkSetExpiry}>${t("Set expiry")}</button>
        <button class="ghost sm" onClick=${() => bulk("clear_expiry")}>${t("Clear expiry")}</button>
        <span class=muted>·</span>
        <button class="danger sm" onClick=${() => bulkConfirm("delete", t("Delete"))}>${t("Delete")}</button>
        <button class="ghost sm" onClick=${() => setSel(new Set())}>${t("Clear selection")}</button>
      </div>`}
      <table>
        <thead><tr>${cfg.bulk ? html`<th style=${{ width: "2.4rem", textAlign: "center" }}><input type=checkbox style=${selChkStyle} checked=${allSel} onChange=${toggleAll} title=${t("Select all shown")} /></th>` : ""}${cfg.columns.map(c => html`<th>${t(c[1])}</th>`)}<th></th></tr></thead>
        <tbody>
          ${rows.length === 0 && html`<tr><td class=empty colspan=${cfg.columns.length + 1 + (cfg.bulk ? 1 : 0)}>${t("No entries yet")}</td></tr>`}
          ${rows.length > 0 && shown.length === 0 && html`<tr><td class=empty colspan=${cfg.columns.length + 1 + (cfg.bulk ? 1 : 0)}>${t("No matches")}</td></tr>`}
          ${shown.map(it => [html`<tr key=${it.id}>
            ${cfg.bulk ? html`<td style=${{ textAlign: "center" }}><input type=checkbox style=${selChkStyle} checked=${sel.has(it.id)} onChange=${() => toggleOne(it.id)} title=${t("Select this row for bulk actions")} /></td>` : ""}
            ${cfg.columns.map(c => html`<td>${c[2] ? c[2](it[c[0]], it, state) : fmt(it[c[0]])}</td>`)}
            <td style=${{ textAlign: "right", whiteSpace: "nowrap" }}>
              ${cfg.series && html`<button class=${"ghost sm" + (spark === it.id ? " active" : "")} title=${t("Throughput graph")} aria-label=${t("Throughput graph")} onClick=${() => setSpark(s => s === it.id ? null : it.id)}>📈</button> `}
              ${(cfg.rowActions || []).includes("config") && html`<button class="ghost sm" onClick=${() => setModal({ kind: "config", userId: it.id, username: it.username, user: it })}>${t("Config")}</button> `}
              ${ownsRow(it) && (cfg.rowActions || []).includes("limits") && html`<button class="ghost sm" onClick=${() => setModal({ kind: "limits", node: it })}>${t("Limits")}</button> `}
              ${ownsRow(it) && (cfg.rowActions || []).includes("promote-tls") && isStagingIssuer(it.tls_issuer) && html`<button class="ghost sm" title=${t("Switch this node's cert from staging to Let's Encrypt production and reissue")} onClick=${() => promoteTLS(it)}>${t("→ prod")}</button> `}
              ${ownsRow(it) && cfg.toggle && html`<button class="ghost sm" onClick=${() => toggle(it)}>${ucfirst(t(cfg.toggle.label(it)))}</button> `}
              ${ownsRow(it)
                ? html`<button class="ghost sm" onClick=${() => setModal({ kind: "form", cfg, initial: it })}>${t("Edit")}</button>
                       <button class="danger sm" onClick=${() => del(it)}>${t("Delete")}</button>`
                : html`<span class=muted style=${{ fontSize: "11px" }}>${t("read-only")}</span>`}
            </td>
          </tr>`,
          cfg.series && spark === it.id ? html`<tr key=${it.id + "-spark"}><td colspan=${cfg.columns.length + 1 + (cfg.bulk ? 1 : 0)} style=${{ background: "var(--bg-soft, transparent)" }}><${NodeSparkTabs} nodeId=${it.id} /></td></tr>` : null,
          ])}
        </tbody>
      </table>
    </div>`;
}
function ucfirst(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
// Row/select-all checkbox: enlarged with an accent tint so it reads clearly as a
// multi-row selector (not a stray tick).
const selChkStyle = { width: "17px", height: "17px", cursor: "pointer", accentColor: "var(--accent, #6366f1)", verticalAlign: "middle" };
function fmt(v) { return Array.isArray(v) ? v.join(", ") : (v === true ? "✓" : v === false ? "" : (v == null ? "" : String(v))); }
// A staging leaf is reported with a "(STAGING)" issuer by Let's Encrypt; only
// then is "→ prod" meaningful (a production cert needs no promotion).
function isStagingIssuer(v) { return /staging/i.test(v || ""); }

/* ---------------- generic form modal ---------------- */
function FormModal({ cfg, initial, state, onClose, reload, notify }) {
  const firstOpt = (f) => { const o = (f.options || [])[0]; return o == null ? "" : (typeof o === "object" ? o.value : o); };
  const init = () => {
    const v = {};
    for (const f of cfg.fields) {
      let cur = initial ? getPath(initial, f.name) : undefined;
      // A synthetic field (e.g. routing "level") has no stored column — derive its
      // edit value from the rest of the saved record.
      if (cur === undefined && initial && f.deriveInitial) cur = f.deriveInitial(initial);
      // Defaults apply only on CREATE — on edit an absent (omitempty) field means
      // the saved value was empty and must be shown as-is, not overwritten.
      if (cur === undefined && !initial && f.def !== undefined) cur = f.def;
      // On CREATE a select with no value defaults to its first option, so the
      // shown value matches state (no spurious "field required" on submit).
      if (cur === undefined && !initial && f.type === "select") cur = firstOpt(f);
      if (f.type === "tags" || f.type === "country") v[f.name] = Array.isArray(cur) ? cur : (cur ? [cur] : []);
      else if (f.type === "bool") v[f.name] = !!cur;
      else if (f.type === "date") v[f.name] = cur ? fmtDMY(cur) : "";
      else v[f.name] = cur == null ? "" : cur;
    }
    return v;
  };
  const [vals, setVals] = useState(init);
  const [busy, setBusy] = useState(false);
  const [reassignTo, setReassignTo] = useState(""); // "" = picker hidden
  const ref = useModal(onClose);
  const set = (name, val) => setVals(s => ({ ...s, [name]: val }));

  const submit = async () => {
    const obj = {};
    for (const f of cfg.fields) {
      if (f.showIf && !f.showIf(vals)) continue;
      // A confirmation field (e.g. "confirm password") must equal its target and
      // is never sent to the backend.
      if (f.matchField) {
        if (String(vals[f.name] || "") !== String(vals[f.matchField] || "")) {
          notify(tf("{label} does not match", { label: t(f.label) }), "err"); return;
        }
        continue;
      }
      if (f.createOnly && initial) continue;
      let val = vals[f.name];
      if (f.type === "tags" || f.type === "country") val = Array.isArray(val) ? val : [];
      else if (f.type === "number") val = val === "" ? 0 : parseInt(val, 10);
      else if (f.type === "bool") val = !!val;
      else if (f.type === "date") {
        // Blank = no expiry (omitted → forever). A set date means access through
        // the END of that day UTC, so store 23:59:59Z (matches the date shown back).
        if (String(val).trim() === "") continue;
        const iso = parseDMY(val);
        if (!iso) { notify(tf("Enter {label} as dd/mm/yyyy", { label: t(f.label) }), "err"); return; }
        val = iso + "T23:59:59Z";
      }
      if (f.required && (val == null || val === "" || (Array.isArray(val) && val.length === 0))) {
        notify(tf("{label} is required", { label: t(f.label) }), "err"); return;
      }
      if (!f.required && (val === "" || (Array.isArray(val) && val.length === 0))) continue;
      setPath(obj, f.name, val);
    }
    // id is hidden: preserved on edit, auto-generated on create.
    obj.id = initial ? initial.id : genId(obj.name || obj.username || obj.hostname, cfg.singular.replace(/\s+/g, ""));
    const payload = cfg.onSubmit ? cfg.onSubmit(obj) : obj;
    setBusy(true);
    try { await api("POST", cfg.path, payload); onClose(); reload(); notify(t("Saved"), "ok"); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };

  // Drain/resume a node from inside its edit panel (maintenance is a control-plane
  // action, not a saved field — so it has its own button next to Save).
  const toggleMaint = async () => {
    const on = !initial.maintenance;
    if (on && !await uiConfirm({ title: tf("Drain {name}? Traffic that targets it egresses locally and new client configs warn until you resume.", { name: initial.name || initial.id }), confirmLabel: t("Drain") })) return;
    setBusy(true);
    try { await api("POST", cfg.path + "/" + encodeURIComponent(initial.id) + "/maintenance", { maintenance: on }); onClose(); reload(); notify(on ? t("Drained") : t("Resumed"), "ok"); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };

  // Reassign egress: move every group that egresses via THIS exit onto a chosen
  // healthy exit, then drain this node. The one-click recovery for a dead exit
  // (F-025) — otherwise egress is stranded until each group is edited by hand.
  const otherHealthyExits = cfg.maintenanceAction && initial && initial.public_role === "exit"
    ? ((state && state.nodes) || []).filter(n => n.id !== initial.id && n.public_role === "exit" && !n.maintenance)
    : [];
  const groupsHere = cfg.maintenanceAction && initial
    ? ((state && state.groups) || []).filter(g => g.default_exit_id === initial.id)
    : [];
  const doReassign = async () => {
    if (!reassignTo) return;
    if (!await uiConfirm({ title: tf("Move egress of {n} group(s) from {name} to another exit and drain this node?", { n: groupsHere.length, name: initial.name || initial.id }), confirmLabel: t("Reassign") })) return;
    setBusy(true);
    try { await api("POST", cfg.path + "/" + encodeURIComponent(initial.id) + "/reassign-egress", { target_exit_id: reassignTo }); onClose(); reload(); notify(t("Egress reassigned"), "ok"); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };

  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class=modal ref=${ref} role=dialog aria-modal=true>
        <header><h3>${(initial ? t("Edit") : t("Add")) + " " + t(cfg.singular)}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          <datalist id="countries-dl">${COUNTRIES.map(([c, n]) => html`<option key=${c} value=${n + " (" + c + ")"}></option>`)}</datalist>
          <datalist id="geosite-dl">${GEOSITE.map(g => html`<option key=${g} value=${g}></option>`)}</datalist>
          ${cfg.fields.filter(f => (!f.editOnly || initial) && (!f.createOnly || !initial) && (!f.showIf || f.showIf(vals))).map(f => html`<${Field} key=${f.name} f=${f} value=${vals[f.name]} onChange=${v => set(f.name, v)} />`)}
          ${cfg.summary && html`<div style=${{ margin: ".4rem 0 0", padding: ".6rem .8rem", border: "1px solid var(--accent, var(--border))", borderRadius: "8px", background: "var(--bg-soft, transparent)", fontSize: "13px" }}>
            <div class=muted style=${{ fontSize: "11px", textTransform: "uppercase", letterSpacing: ".05em", marginBottom: ".3rem" }}>${t("What this rule does")}</div>
            ${cfg.summary(vals, state || {})}
          </div>`}
        </div>
        ${groupsHere.length > 0 && otherHealthyExits.length > 0 && html`<div style=${{ padding: ".5rem .8rem", borderTop: "1px solid var(--border)", display: "flex", gap: ".4rem", alignItems: "center", flexWrap: "wrap", fontSize: "13px" }}>
          <span class=muted>${tf("{n} group(s) egress here — move to:", { n: groupsHere.length })}</span>
          <select value=${reassignTo} onChange=${e => setReassignTo(e.target.value)}>
            <option value="">${t("choose exit…")}</option>
            ${otherHealthyExits.map(n => html`<option key=${n.id} value=${n.id}>${n.name || n.id}</option>`)}
          </select>
          <button class="ghost sm" disabled=${busy || !reassignTo} onClick=${doReassign}>${t("Reassign egress")}</button>
        </div>`}
        <div class=foot>
          ${cfg.maintenanceAction && initial && html`<button class=${initial.maintenance ? "sm" : "ghost sm"} style=${{ marginRight: "auto" }} disabled=${busy} title=${t("Take this node out of rotation for maintenance (or put it back)")} onClick=${toggleMaint}>${initial.maintenance ? t("Resume") : t("Drain")}</button>`}
          <button class=ghost onClick=${onClose}>${t("Cancel")}</button>
          <button disabled=${busy} onClick=${submit}>${busy ? t("Saving…") : t("Save")}</button>
        </div>
      </div>
    </div>`;
}

/* ---------------- node VPS limits modal ---------------- */
function todayISO() { return new Date().toISOString().slice(0, 10); }
// Dates are shown to operators as dd/mm/yyyy; stored/computed as ISO yyyy-mm-dd.
function fmtDMY(s) { if (!s) return ""; const p = String(s).slice(0, 10).split("-"); return p.length === 3 ? p[2] + "/" + p[1] + "/" + p[0] : String(s); }
function parseDMY(s) { const m = String(s || "").trim().match(/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/); return m ? m[3] + "-" + m[2].padStart(2, "0") + "-" + m[1].padStart(2, "0") : ""; }
// A user is expired (no working config) once now is past their expires_at — mirrors
// model.User.Expired server-side; here it only drives the table badge.
function isExpired(v) { return !!v && new Date(v).getTime() <= Date.now(); }
function addMonthsISO(fromYMD, term) {
  const d = fromYMD ? new Date(fromYMD + "T00:00:00Z") : new Date();
  d.setUTCMonth(d.getUTCMonth() + Number(term || 0));
  return d.toISOString();
}

function LimitsModal({ node, onClose, reload, notify }) {
  const lim = node.limits || {};
  const bill = node.billing || {};
  const [cpu, setCpu] = useState(lim.cpu_cores || "");
  const [mem, setMem] = useState(lim.memory_mb ? lim.memory_mb / 1024 : "");   // shown in GB
  const [disk, setDisk] = useState(lim.disk_gb || "");
  const [traffic, setTraffic] = useState(lim.traffic_gb ? lim.traffic_gb / 1024 : ""); // shown in TB
  const [term, setTerm] = useState(bill.term_months || 0);
  const [paid, setPaid] = useState(fmtDMY(todayISO())); // dd/mm/yyyy
  const [busy, setBusy] = useState(false);
  const ref = useModal(onClose);
  const num = (v) => v === "" ? 0 : Number(v) || 0;
  const paidISO = parseDMY(paid);
  const paidUntilPreview = term > 0 && paidISO ? fmtDMY(addMonthsISO(paidISO, term)) : null;

  const save = async () => {
    if (term > 0 && !paidISO) { notify(t("Enter the paid-on date as dd/mm/yyyy"), "err"); return; }
    setBusy(true);
    try {
      const id = encodeURIComponent(node.id);
      await api("POST", "/api/nodes/" + id + "/limits",
        { cpu_cores: num(cpu), memory_mb: Math.round(num(mem) * 1024), disk_gb: num(disk), traffic_gb: Math.round(num(traffic) * 1024) });
      const billing = term > 0 ? { paid_until: addMonthsISO(paidISO, term), term_months: Number(term) } : {};
      await api("POST", "/api/nodes/" + id + "/billing", billing);
      onClose(); reload(); notify(t("Saved"), "ok");
    } catch (e) { notify(e.message, "err"); setBusy(false); }
  };
  const field = (label, val, set, hint) => html`
    <div class=field>
      <label>${label}</label>
      <input type=number min=0 step=any value=${val} onChange=${e => set(e.target.value)} placeholder=${t("unset")} />
      ${hint && html`<span class=hint>${hint}</span>`}
    </div>`;
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class=modal ref=${ref} role=dialog aria-modal=true>
        <header><h3>${t("Node specs")} — ${node.name || node.id}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          <h4 style=${{ margin: "0 0 .4rem" }}>${t("VPS limits")}</h4>
          <p class=muted style=${{ marginTop: 0 }}>${t("Shown on Overview. Blank = unset.")}</p>
          ${field(t("CPU cores (vCPU)"), cpu, setCpu)}
          ${field(t("Memory (GB)"), mem, setMem, t("e.g. 4 for 4 GB"))}
          ${field(t("Disk (GB)"), disk, setDisk)}
          ${field(t("Monthly traffic (TB)"), traffic, setTraffic, t("e.g. 6 for 6 TB/mo"))}
          <h4 style=${{ margin: "1rem 0 .4rem" }}>${t("Billing")}</h4>
          ${bill.paid_until && html`<p class=muted style=${{ marginTop: 0 }}>${tf("Currently paid until {date}.", { date: fmtDMY(bill.paid_until) })}</p>`}
          <div class=field>
            <label>${t("Payment term")}</label>
            <select value=${term} onChange=${e => setTerm(Number(e.target.value))}>
              <option value=0>${t("— no billing —")}</option>
              <option value=1>${t("1 month")}</option>
              <option value=3>${t("3 months")}</option>
              <option value=6>${t("6 months")}</option>
              <option value=12>${t("12 months")}</option>
            </select>
          </div>
          ${term > 0 && html`
            <div class=field>
              <label>${t("Paid on")}</label>
              <input type=text inputmode=numeric placeholder=${t("dd/mm/yyyy")} value=${paid} onChange=${e => setPaid(e.target.value)} />
              <span class=hint>${tf("paid until {x} (+{term} mo)", { x: paidUntilPreview || "—", term })}</span>
            </div>`}
        </div>
        <div class=foot>
          <button class=ghost onClick=${onClose}>${t("Cancel")}</button>
          <button disabled=${busy} onClick=${save}>${busy ? t("Saving…") : t("Save")}</button>
        </div>
      </div>
    </div>`;
}

/* ---------------- route tester ---------------- */
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
      <div class=modal ref=${ref} role=dialog aria-modal=true>
        <header><h3>🧪 ${t("Route tester")}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          <p class=muted style=${{ marginTop: 0 }}>${t("Which rule decides a group's traffic to a host.")}</p>
          <div class=field>
            <label>${t("Group")}</label>
            <select value=${groupId} onChange=${e => setGroupId(e.target.value)}>
              ${groups.map(g => html`<option key=${g.id} value=${g.id}>${g.name || g.id}</option>`)}
            </select>
          </div>
          <div class=field>
            <label>${t("Host to test (IP or domain)")}</label>
            <input value=${target} placeholder="${t("e.g.")} ya.ru, 8.8.8.8"
              onInput=${e => setTarget(e.target.value)} onKeyDown=${e => { if (e.key === "Enter") run(); }} />
          </div>
          <button disabled=${busy} onClick=${run}>${busy ? t("Testing…") : t("Test route")}</button>
          ${res && html`<div style=${{ marginTop: "1rem" }}>
            <div class=between>
              <strong>${t("Goes out via")}</strong><span class=${cls(res.decision)} style=${{ fontWeight: 800, textTransform: "uppercase" }}>${res.decision}</span>
            </div>
            <p style=${{ margin: ".3rem 0" }}>${res.egress}</p>
            <p class=muted style=${{ margin: ".2rem 0" }}>${t("Decided by")}: <strong>${res.decided_by}</strong> — ${res.reason}</p>
            ${res.resolved_ips && res.resolved_ips.length > 0 && html`<p class=muted style=${{ margin: ".2rem 0" }}>${t("Resolved to")}: ${res.resolved_ips.join(", ")}</p>`}
            ${(res.warnings || []).map((w, i) => html`<p key=${i} class=warn style=${{ margin: ".2rem 0", fontSize: "12px" }}>⚠ ${w}</p>`)}
            <h4 style=${{ margin: ".7rem 0 .3rem" }}>${t("How it was decided (top to bottom, first match wins)")}</h4>
            <table><tbody>
              ${(res.trace || []).map((s, i) => html`<tr key=${i}>
                <td style=${{ whiteSpace: "nowrap", verticalAlign: "top" }}><span class=muted>${s.stage}</span></td>
                <td style=${{ verticalAlign: "top" }}>${s.matched ? "✓" : "·"} ${s.rule}${s.action ? html` <span class=muted>(${s.action})</span>` : ""}</td>
                <td class=muted style=${{ fontSize: "12px" }}>${s.detail}</td>
              </tr>`)}
            </tbody></table>
          </div>`}
        </div>
        <div class=foot><button class=ghost onClick=${onClose}>${t("Close")}</button></div>
      </div>
    </div>`;
}

function Field({ f, value, onChange }) {
  if (f.type === "bool") return html`<div class="field check"><input type=checkbox checked=${!!value} onChange=${e => onChange(e.target.checked)} /><label style=${{ margin: 0 }}>${t(f.label)}</label></div>`;
  let input;
  if (f.type === "select") input = html`<select value=${value} onChange=${e => onChange(e.target.value)}>${f.options.map(o => {
    const val = typeof o === "object" ? o.value : o, lab = typeof o === "object" ? o.label : (o === "" ? "— none —" : o);
    return html`<option key=${val} value=${val}>${lab}</option>`;
  })}</select>`;
  else if (f.type === "tags") input = html`<${TagInput} value=${value} onChange=${onChange} list=${f.list} options=${f.list === "geosite-dl" ? GEOSITE : null} spaceAdds=${true} />`;
  else if (f.type === "country") input = html`<${TagInput} value=${value} onChange=${onChange} list="countries-dl" transform=${countryCode} options=${COUNTRY_LABELS} spaceAdds=${false} />`;
  else if (f.type === "password") input = html`<${PasswordInput} value=${value} onChange=${onChange} placeholder=${f.placeholder || ""} />`;
  else if (f.type === "date") input = html`<input type=text inputmode=numeric value=${value} placeholder=${f.placeholder || t("dd/mm/yyyy")} onInput=${e => onChange(e.target.value)} />`;
  else input = html`<input type=${f.type === "number" ? "number" : "text"} value=${value} onInput=${e => onChange(e.target.value)} />`;
  return html`<div class=field><label>${t(f.label)}${f.required ? html`<span class=err> *</span>` : ""}</label>${input}${f.hint && html`<div class=hint>${t(f.hint)}</div>`}</div>`;
}

function TagInput({ value, onChange, list, transform, options, spaceAdds }) {
  const [draft, setDraft] = useState("");
  const arr = value || [];
  const add = (raw) => { const t = (transform || (x => String(x).trim()))(raw); if (t && !arr.includes(t)) onChange([...arr, t]); setDraft(""); };
  // Picking a datalist suggestion (or typing an exact known value) commits at
  // once — no Enter/comma needed.
  const onInput = (e) => { const v = e.target.value; if (options && options.includes(v)) add(v); else setDraft(v); };
  return html`<div class=taginput>
    ${arr.map((t, i) => html`<span class=chip key=${t}>${t}<b onClick=${() => onChange(arr.filter((_, j) => j !== i))}>✕</b></span>`)}
    <input list=${list || undefined} value=${draft} placeholder=${t("add…")}
      onInput=${onInput}
      onKeyDown=${e => { if (e.key === "Enter" || e.key === "," || (spaceAdds && e.key === " ")) { e.preventDefault(); add(draft); } else if (e.key === "Backspace" && !draft && arr.length) onChange(arr.slice(0, -1)); }}
      onBlur=${() => { if (draft) add(draft); }} />
  </div>`;
}

/* ---------------- client-config modal ---------------- */
// QRImage renders a QR for text via the vendored qrcode-generator global (from
// /vendor/qrcode.js). Fully local — nothing leaves the panel, works offline. The
// type-number 0 auto-fits the version; Byte mode handles arbitrary URLs.
function QRImage({ text, cell = 6 }) {
  if (!text || typeof qrcode !== "function") return null;
  let url;
  try { const q = qrcode(0, "M"); q.addData(text); q.make(); url = q.createDataURL(cell, 2); }
  catch (e) { return html`<div class=muted style=${{ fontSize: "12px", textAlign: "center" }}>${t("Link too long to render as a QR")}</div>`; }
  return html`<img src=${url} alt=${t("QR code")} width=220 height=220 style=${{ imageRendering: "pixelated", background: "#fff", padding: "10px", borderRadius: "10px", display: "block", margin: "0 auto" }} />`;
}

function ConfigModal({ userId, username, user, entries, onClose, notify }) {
  const [entry, setEntry] = useState(entries[0] ? entries[0].id : "");
  const [cfg, setCfg] = useState(null);
  const [dl, setDl] = useState(false);
  const [busy, setBusy] = useState(false);
  const ref = useModal(onClose);
  // Surface why a freshly generated config might not actually connect: a config
  // builds regardless of these, but the client won't get through. The selected
  // entry being in maintenance or unhealthy is shown inline on the option itself
  // AND as a banner (maintenance is a deliberate drain; unhealthy is the entry
  // failing its own health check — both mean the same "won't connect" outcome).
  const noEntries = entries.length === 0;
  const userDisabled = !!user && user.enabled === false;
  const userExpired = !!user && isExpired(user.expires_at);
  const selectedEntry = entries.find(n => n.id === entry);
  const entryUnhealthy = !!selectedEntry && selectedEntry.health && selectedEntry.health !== "healthy";
  const fetchCfg = async () => {
    setBusy(true); setDl(false);
    try { const c = await api("GET", `/api/users/${encodeURIComponent(userId)}/client-config?entry=${encodeURIComponent(entry)}`); setCfg(c); }
    catch (e) { notify(e.message, "err"); }
    setBusy(false);
  };
  const download = () => {
    const blob = new Blob([cfg.config_toml], { type: "text/plain" });
    const a = document.createElement("a"); a.href = URL.createObjectURL(blob);
    a.download = `trusttunnel-${username}-${entry}.toml`; a.click();
  };
  const copy = (v) => { navigator.clipboard.writeText(v).then(() => notify(t("Copied"), "ok"), () => {}); };
  return html`
    <div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}>
      <div class=modal ref=${ref} role=dialog aria-modal=true>
        <header><h3>${t("Client config")} — ${username}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
        <div class=body>
          ${(noEntries || userDisabled || userExpired || entryUnhealthy) && html`<div class=card style=${{ borderLeft: "3px solid #f59e0b", marginBottom: ".8rem", fontSize: "13px" }}>
            ${noEntries && html`<div>⚠ ${t("No entry nodes yet — add one before this config can connect.")}</div>`}
            ${userDisabled && html`<div>⚠ ${t("This client is disabled — the config builds but will be rejected until you enable it.")}</div>`}
            ${userExpired && html`<div>⚠ ${t("This client has expired — extend its expiry or it cannot connect.")}</div>`}
            ${entryUnhealthy && html`<div>⚠ ${t("The selected entry is unhealthy — the config builds but may not connect until it recovers.")}</div>`}
          </div>`}
          <div class=field><label>${t("Entry node")}</label>
            <select value=${entry} onChange=${e => { setEntry(e.target.value); setCfg(null); setDl(false); }}>
              ${entries.length === 0 && html`<option value="">${t("no entry nodes")}</option>`}
              ${entries.map(n => html`<option value=${n.id}>${n.name || n.id} (${(n.public_ips || []).join(",")})${n.maintenance ? " — " + t("in maintenance") : n.health && n.health !== "healthy" ? " — " + t("unhealthy") : ""}</option>`)}
            </select>
          </div>
          <button disabled=${!entry || busy} onClick=${fetchCfg}>${busy ? "…" : t("Generate")}</button>
          ${cfg && html`<div style=${{ marginTop: ".8rem" }}>
            <pre class=config>${cfg.config_toml}</pre>
            <div style=${{ display: "flex", gap: ".5rem" }}>
              <button class=ghost onClick=${download}>${t("Download .toml")}</button>
              <button class=ghost onClick=${() => setDl(d => !d)}>${t("Link")}</button>
            </div>
            ${dl && html`<div style=${{ marginTop: ".8rem" }}>
              <${QRImage} text=${cfg.deep_link} />
              <p class=muted style=${{ textAlign: "center", fontSize: "12px", margin: ".4rem 0 .8rem" }}>${t("Scan with the TrustTunnel app to import.")}</p>
              <div class=field><label>${t("Link")}</label>
                <textarea readonly rows=2 onClick=${e => e.target.select()} value=${cfg.deep_link}></textarea>
                <button class="ghost sm" onClick=${() => copy(cfg.deep_link)}>${t("Copy")}</button>
              </div>
              <div class=field><label>${t("landing link (renders QR in browser)")}</label>
                <textarea readonly rows=2 onClick=${e => e.target.select()} value=${cfg.qr_link}></textarea>
                <button class="ghost sm" onClick=${() => copy(cfg.qr_link)}>${t("Copy")}</button>
              </div>
            </div>`}
          </div>`}
        </div>
      </div>
    </div>`;
}

/* ---------------- provision (remote install) modal ---------------- */
function ProvisionModal({ onClose, reload, notify, isAdmin = true }) {
  const [f, setF] = useState({ role: "exit", ssh_port: 22, ssh_user: "root", harden: true, h_sudo: "user", h_port: 3222, h_root: true, h_pw: true, h_f2b: true, h_fw: true });
  const set = (k, v) => setF(s => ({ ...s, [k]: v }));
  const [ips, setIps] = useState([]);
  const [auth, setAuth] = useState("password");
  const [job, setJob] = useState(null);
  const [busy, setBusy] = useState(false);
  // Escape must not close the dialog mid-install (matches the hidden ✕ / disabled
  // Done while running). jobRef tracks the latest status for the captured handler.
  const jobRef = useRef(null); jobRef.current = job;
  const ref = useModal(() => { if (!jobRef.current || jobRef.current.status !== "running") onClose(); });

  const start = async () => {
    const sshHost = (f.ssh_host || ips[0] || "").trim(); // default to the first public IP shown in the placeholder
    if (!f.name || ips.length === 0 || !sshHost) { notify(t("Name and at least one public IP are required"), "err"); return; }
    if (f.role === "exit" && !f.reality_sni) { notify(t("Reality SNI is required for an exit"), "err"); return; }
    setBusy(true);
    const body = {
      name: f.name, role: f.role, public_ips: ips,
      domain: f.role === "entry" ? (f.domain || "") : "",
      reality_sni: f.role === "exit" ? (f.reality_sni || "") : "",
      make_standby: f.role === "exit" && !!f.make_standby,
      ssh: { host: sshHost, port: parseInt(f.ssh_port) || 22, user: f.ssh_user, password: auth === "password" ? (f.ssh_password || "") : "", key_pem: auth === "key" ? (f.ssh_key || "") : "" },
      hardening: f.harden
        ? { enabled: true, sudo_user: f.h_sudo || "", ssh_pubkey: f.h_pubkey || "", ssh_port: parseInt(f.h_port) || 0, disable_root_login: !!f.h_root, disable_password_auth: !!f.h_pw, fail2ban: !!f.h_f2b, firewall: !!f.h_fw }
        : { enabled: false },
    };
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

  const fld = (label, node, hint) => html`<div class=field><label>${label}</label>${node}${hint && html`<div class=hint>${hint}</div>`}</div>`;
  const txt = (k, ph) => html`<input value=${f[k] || ""} placeholder=${ph || ""} onInput=${e => set(k, e.target.value)} />`;
  const chk = (k, label) => html`<div class="field check"><input type=checkbox checked=${!!f[k]} onChange=${e => set(k, e.target.checked)} /><label style=${{ margin: 0 }}>${label}</label></div>`;
  const hr = html`<hr style=${{ border: 0, borderTop: "1px solid var(--border)", margin: ".7rem 0" }} />`;

  if (job) {
    return html`<div class=overlay><div class=modal ref=${ref} role=dialog aria-modal=true>
      <header><h3>${t("Adding the server")}</h3>${job.status !== "running" && html`<button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button>`}</header>
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

  return html`<div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}><div class=modal ref=${ref} role=dialog aria-modal=true>
    <header><h3>${t("Add a new server")}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
    <div class=body>
      ${fld(t("Name"), txt("name", "Exit NL"))}
      ${fld(t("Role"), html`<select value=${f.role} onChange=${e => set("role", e.target.value)}><option value=exit>${t("exit")}</option><option value=entry>${t("entry")}</option></select>`)}
      ${fld(t("Public IPs"), html`<${TagInput} value=${ips} onChange=${setIps} />`, t("the server's public IP(s)"))}
      ${f.role === "entry" && fld(t("Domain"), txt("domain", "cdn.example.com"), t("TLS hostname served by TrustTunnel"))}
      ${f.role === "exit" && fld(t("Reality SNI"), txt("reality_sni", "www.example-cdn.com"), t("borrowed third-party domain; Reality keys + uuid are generated automatically"))}
      ${isAdmin && f.role === "exit" && chk("make_standby", t("Also make this exit a control-plane standby (HA)"))}
      ${isAdmin && f.role === "exit" && f.make_standby && html`<div class=hint style=${{ marginTop: "-.3rem" }}>${t("After install, layers a Postgres replica + CA on this exit via the primary's agent, so it can take over the panel. Staged serve stays disabled until you promote.")}</div>`}
      ${hr}
      ${fld(t("SSH host"), txt("ssh_host", ips[0] || ""))}
      <div class=row>
        <div style=${{ flex: 1 }}>${fld(t("SSH user"), txt("ssh_user"))}</div>
        <div style=${{ width: "110px" }}>${fld(t("Port"), txt("ssh_port"))}</div>
      </div>
      ${fld(t("Auth"), html`<select value=${auth} onChange=${e => setAuth(e.target.value)}><option value=password>${t("password")}</option><option value=key>${t("private key")}</option></select>`)}
      ${auth === "password"
        ? fld(t("SSH password"), html`<input type=password value=${f.ssh_password || ""} onInput=${e => set("ssh_password", e.target.value)} />`, t("used once for install, not stored"))
        : fld(t("SSH private key (PEM)"), html`<textarea value=${f.ssh_key || ""} onInput=${e => set("ssh_key", e.target.value)}></textarea>`, t("used once for install, not stored"))}
      ${hr}
      ${chk("harden", t("Harden server"))}
      ${f.harden && html`<div style=${{ paddingLeft: ".6rem", borderLeft: "2px solid var(--border)" }}>
        ${fld(t("Sudo user to create"), txt("h_sudo"))}
        ${fld(t("SSH public key for the sudo user"), html`<textarea value=${f.h_pubkey || ""} onInput=${e => set("h_pubkey", e.target.value)} placeholder="ssh-ed25519 AAAA…"></textarea>`, t("required before password auth can be disabled"))}
        ${fld(t("New SSH port"), txt("h_port"))}
        ${chk("h_root", t("Disable root login"))}
        ${chk("h_pw", t("Disable password auth (needs the key above)"))}
        ${chk("h_f2b", t("Install fail2ban"))}
        ${chk("h_fw", t("Enable firewall (ssh / 443 / 80)"))}
      </div>`}
    </div>
    <div class=foot>
      <button class=ghost onClick=${onClose}>${t("Cancel")}</button>
      <button disabled=${busy} onClick=${start}>${busy ? t("Starting…") : t("Add server (submit)")}</button>
    </div>
  </div></div>`;
}

/* ---------------- convert to two-node (add entry, flip self to exit) ---------------- */
function ConvertModal({ state, onClose, reload, notify }) {
  const single = (state.nodes || []).find(n => n.public_role === "entry");
  const groups = state.groups || [];
  const [f, setF] = useState({ ssh_port: 22, ssh_user: "root", variant: 1, reality_sni: "", harden: true, h_sudo: "user", h_port: 3222, h_root: true, h_pw: true, h_f2b: true, h_fw: true });
  const set = (k, v) => setF(s => ({ ...s, [k]: v }));
  const [ips, setIps] = useState([]);
  const [doms, setDoms] = useState([""]); // variant 1: B's cert domains (first = client endpoint)
  const [auth, setAuth] = useState("password");
  const [allGroups, setAllGroups] = useState(true);
  const [gsel, setGsel] = useState([]);
  const [job, setJob] = useState(null);
  const [busy, setBusy] = useState(false);
  // Escape must not close the dialog mid-conversion (matches the hidden ✕ /
  // disabled Done while running).
  const jobRef = useRef(null); jobRef.current = job;
  const ref = useModal(() => { if (!jobRef.current || jobRef.current.status !== "running") onClose(); });

  const start = async () => {
    const sshHost = (f.ssh_host || ips[0] || "").trim(); // default to the first public IP shown in the placeholder
    const v = parseInt(f.variant) || 1;
    const domList = doms.map(d => d.trim()).filter(Boolean);
    if (!f.name || ips.length === 0 || !sshHost) { notify(t("Name and at least one public IP are required"), "err"); return; }
    if (!f.reality_sni) { notify(t("Reality SNI is required (A becomes the exit)"), "err"); return; }
    if (v === 1 && domList.length === 0) { notify(t("Variant 1 needs at least one domain for B's certificate"), "err"); return; }
    setBusy(true);
    const body = {
      name: f.name, public_ips: ips,
      reality_sni: f.reality_sni || "", variant: v,
      group_ids: allGroups ? [] : gsel,
      ssh: { host: sshHost, port: parseInt(f.ssh_port) || 22, user: f.ssh_user, password: auth === "password" ? (f.ssh_password || "") : "", key_pem: auth === "key" ? (f.ssh_key || "") : "" },
      hardening: f.harden
        ? { enabled: true, sudo_user: f.h_sudo || "", ssh_pubkey: f.h_pubkey || "", ssh_port: parseInt(f.h_port) || 0, disable_root_login: !!f.h_root, disable_password_auth: !!f.h_pw, fail2ban: !!f.h_f2b, firewall: !!f.h_fw }
        : { enabled: false },
    };
    if (v === 1) body.domains = domList; // variant 2 reuses A's existing hostnames — no domain input
    try { const r = await api("POST", "/api/cluster/add-entry", body); poll(r.job_id); }
    catch (e) { notify(e.message, "err"); setBusy(false); }
  };
  const poll = (id) => {
    const tick = async () => {
      try {
        const s = await api("GET", "/api/jobs/" + id); setJob(s);
        if (s.status === "running") setTimeout(tick, 1500);
        else { setBusy(false); if (s.status === "succeeded") { notify(t("Converted to two-node"), "ok"); reload(); } }
      } catch (e) { notify(e.message, "err"); setBusy(false); }
    };
    tick();
  };

  const fld = (label, node, hint) => html`<div class=field><label>${label}</label>${node}${hint && html`<div class=hint>${hint}</div>`}</div>`;
  const txt = (k, ph) => html`<input value=${f[k] || ""} placeholder=${ph || ""} onInput=${e => set(k, e.target.value)} />`;
  const chk = (k, label) => html`<div class="field check"><input type=checkbox checked=${!!f[k]} onChange=${e => set(k, e.target.checked)} /><label style=${{ margin: 0 }}>${label}</label></div>`;
  const hr = html`<hr style=${{ border: 0, borderTop: "1px solid var(--border)", margin: ".7rem 0" }} />`;

  if (job) {
    return html`<div class=overlay><div class=modal ref=${ref} role=dialog aria-modal=true>
      <header><h3>${t("Converting to two-node")}</h3>${job.status !== "running" && html`<button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button>`}</header>
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

  return html`<div class=overlay onClick=${e => e.target.classList.contains("overlay") && onClose()}><div class=modal ref=${ref} role=dialog aria-modal=true>
    <header><h3>${t("Convert to two-node")}</h3><button class="ghost sm" aria-label=${t("Close")} title=${t("Close")} onClick=${onClose}>✕</button></header>
    <div class=body>
      <div class=hint style=${{ marginBottom: ".6rem" }}>${tf("Adds a new entry B in front and flips {name} to the exit. The control plane (panel, database, CA) stays here — nothing moves. B is brought up and verified before the flip, so if it fails this box keeps serving.", { name: single ? single.name : t("this box") })}</div>
      ${fld(t("New entry name"), txt("name", "Entry FR"))}
      ${fld(t("New entry public IPs"), html`<${TagInput} value=${ips} onChange=${setIps} />`, t("the new server's public IP(s)"))}
      ${fld(t("Migration"), html`<select value=${f.variant} onChange=${e => set("variant", e.target.value)}>
        <option value=1>${t("variant 1 — new hostname for B (re-issue client configs)")}</option>
        <option value=2>${t("variant 2 — reuse the same domain (move DNS to B first; no re-issue)")}</option>
      </select>`, parseInt(f.variant) === 2 ? t("Reuses A's existing hostnames — move BOTH the endpoint + apex DNS records to B's IP before running. No domain input needed.") : t("Add B's DNS record(s) before running; clients get new configs/QR pointing at B."))}
      ${parseInt(f.variant) !== 2 && fld(t("New entry domain(s)"), html`<div>
        ${doms.map((d, i) => html`<div key=${i} style=${{ display: "flex", gap: ".4rem", marginBottom: ".3rem" }}>
          <input style=${{ flex: 1 }} value=${d} placeholder=${i === 0 ? "lk.example.com (client endpoint)" : "extra SAN, e.g. example.com"} onInput=${e => { const val = e.target.value; setDoms(s => s.map((x, j) => j === i ? val : x)); }} />
          ${doms.length > 1 && html`<button class="ghost sm" onClick=${() => setDoms(s => s.filter((_, j) => j !== i))}>−</button>`}
        </div>`)}
        <button class="ghost sm" onClick=${() => setDoms(s => [...s, ""])}>${t("+ add domain")}</button>
      </div>`, t("first = client endpoint (main-fallback); the rest are extra SANs in the same certificate"))}
      ${hr}
      ${fld(t("Reality SNI for the exit"), txt("reality_sni", "www.example-cdn.com"), t("borrowed third-party SNI for A's new Reality inbound; keys are generated"))}
      ${fld(t("Egress groups"), html`<div>
        <div class="field check"><input type=checkbox checked=${allGroups} onChange=${e => setAllGroups(e.target.checked)} /><label style=${{ margin: 0 }}>${t("All groups tunnel through the new exit")}</label></div>
        ${!allGroups && html`<div style=${{ display: "flex", flexWrap: "wrap", gap: ".4rem", marginTop: ".3rem" }}>${groups.map(g => html`<label key=${g.id} class=tag style=${{ cursor: "pointer" }}><input type=checkbox checked=${gsel.includes(g.id)} onChange=${e => setGsel(s => e.target.checked ? [...s, g.id] : s.filter(x => x !== g.id))} /> ${g.name}</label>`)}</div>`}
      </div>`)}
      ${hr}
      ${fld(t("SSH host (B)"), txt("ssh_host", ips[0] || ""))}
      <div class=row>
        <div style=${{ flex: 1 }}>${fld(t("SSH user"), txt("ssh_user"))}</div>
        <div style=${{ width: "110px" }}>${fld(t("Port"), txt("ssh_port"))}</div>
      </div>
      ${fld(t("Auth"), html`<select value=${auth} onChange=${e => setAuth(e.target.value)}><option value=password>${t("password")}</option><option value=key>${t("private key")}</option></select>`)}
      ${auth === "password"
        ? fld(t("SSH password"), html`<input type=password value=${f.ssh_password || ""} onInput=${e => set("ssh_password", e.target.value)} />`, t("used once for install, not stored"))
        : fld(t("SSH private key (PEM)"), html`<textarea value=${f.ssh_key || ""} onInput=${e => set("ssh_key", e.target.value)}></textarea>`, t("used once for install, not stored"))}
      ${hr}
      ${chk("harden", t("Harden the new entry"))}
      ${f.harden && html`<div style=${{ paddingLeft: ".6rem", borderLeft: "2px solid var(--border)" }}>
        ${fld(t("Sudo user to create"), txt("h_sudo"))}
        ${fld(t("SSH public key for the sudo user"), html`<textarea value=${f.h_pubkey || ""} onInput=${e => set("h_pubkey", e.target.value)} placeholder="ssh-ed25519 AAAA…"></textarea>`, t("required before password auth can be disabled"))}
        ${fld(t("New SSH port"), txt("h_port"))}
        ${chk("h_root", t("Disable root login"))}
        ${chk("h_pw", t("Disable password auth (needs the key above)"))}
        ${chk("h_f2b", t("Install fail2ban"))}
        ${chk("h_fw", t("Enable firewall (ssh / 443 / 80)"))}
      </div>`}
    </div>
    <div class=foot>
      <button class=ghost onClick=${onClose}>${t("Cancel")}</button>
      <button disabled=${busy} onClick=${start}>${busy ? t("Starting…") : t("Convert")}</button>
    </div>
  </div></div>`;
}

/* ---------------- login ---------------- */
function Login({ onDone, lang, switchLang }) {
  const [u, setU] = useState(""); const [p, setP] = useState(""); const [err, setErr] = useState("");
  const submit = async (e) => {
    e.preventDefault();
    try { const r = await api("POST", "/api/auth/login", { username: u, password: p }); setCsrfToken(r.csrf_token); onDone(); }
    catch (e) { setErr(e.message); }
  };
  return html`<div class=login><form class=card onSubmit=${submit}>
    <div class=between><h2 style=${{ margin: 0 }}>${t("Sign in")}</h2>${switchLang && html`<${LangToggle} lang=${lang} switchLang=${switchLang} />`}</div>
    <div class=field><label>${t("Username")}</label><input value=${u} onInput=${e => setU(e.target.value)} autocomplete=username /></div>
    <div class=field><label>${t("Password")}</label><${PasswordInput} value=${p} onChange=${setP} autocomplete="current-password" /></div>
    ${err && html`<div class="err" style=${{ marginBottom: ".6rem" }}>${err}</div>`}
    <button type=submit>${t("Log in")}</button>
  </form></div>`;
}

ReactDOM.createRoot(document.getElementById("root")).render(html`<${App} />`);
