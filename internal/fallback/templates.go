package fallback

import (
	"html/template"
	"strings"
)

// HTML templates and legal copy for the camouflage site. Split out of
// server.go (it was ~60% of the file); these are pure string literals
// rendered by the handlers, with tmplFuncs/site defined alongside them.

var homeTemplate = template.Must(template.New("portal").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{brand}} Client Portal</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f5f7fb;
      --surface: #ffffff;
      --surface-2: #f0f4f9;
      --line: #d9e2ee;
      --text: #142033;
      --muted: #657386;
      --blue: #246bfe;
      --blue-2: #1756df;
      --cyan: #11b9cc;
      --navy: #10243d;
      --error: #b33a3a;
      --shadow: 0 22px 60px rgba(20, 32, 51, .13);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-width: 320px;
      min-height: 100vh;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: var(--bg);
      color: var(--text);
    }
    a { color: inherit; }
    .page {
      min-height: 100vh;
      display: grid;
      grid-template-rows: auto 1fr auto;
    }
    header, footer {
      width: min(1120px, calc(100% - 32px));
      margin: 0 auto;
    }
    header {
      min-height: 78px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 18px;
    }
    .brand { display: flex; align-items: center; gap: 10px; font-size: 20px; font-weight: 850; letter-spacing: 0; text-decoration: none; }
    .brand-mark { width: 38px; height: 38px; border-radius: 8px; background: var(--navy); position: relative; box-shadow: 0 12px 28px rgba(16, 36, 61, .18); }
    .brand-mark::after { content: ""; position: absolute; inset: 10px; border-radius: 5px; background: var(--cyan); }
    .top-links { display: flex; align-items: center; gap: 16px; color: var(--muted); font-size: 14px; font-weight: 700; }
    .top-links a { text-decoration: none; }
    .top-links .pill {
      min-height: 36px;
      display: inline-flex;
      align-items: center;
      padding: 0 12px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fff;
      color: var(--blue);
    }
    main {
      width: min(1120px, calc(100% - 32px));
      margin: 0 auto;
      padding: 36px 0 46px;
      display: grid;
      grid-template-columns: minmax(340px, 430px) minmax(0, 1fr);
      align-items: center;
      gap: 28px;
    }
    .login-card {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--surface);
      box-shadow: var(--shadow);
      padding: 34px;
    }
    .login-title { margin: 0 0 8px; font-size: 30px; line-height: 1.12; letter-spacing: 0; }
    .login-subtitle { margin: 0; color: var(--muted); line-height: 1.55; }
    form { margin-top: 26px; display: grid; gap: 14px; }
    label { display: grid; gap: 7px; color: #4f5d72; font-size: 13px; font-weight: 650; }
    input[type="email"], input[type="password"] {
      width: 100%;
      min-height: 46px;
      padding: 0 12px;
      border: 1px solid #cfd9e6;
      border-radius: 6px;
      background: #fff;
      color: var(--text);
      font: inherit;
    }
    input:focus { outline: 3px solid rgba(36, 107, 254, .16); border-color: var(--blue); }
    .form-row { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; }
    .remember { display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: 13px; font-weight: 500; }
    .help { color: var(--blue); font-size: 13px; font-weight: 750; text-decoration: none; }
    button {
      min-height: 48px;
      border: 0;
      border-radius: 6px;
      background: var(--blue);
      color: #fff;
      font: inherit;
      font-weight: 820;
      cursor: pointer;
      box-shadow: 0 10px 24px rgba(36, 107, 254, .20);
    }
    button:hover { background: var(--blue-2); }
    .message { min-height: 20px; color: var(--error); font-size: 13px; }
    .divider { display: grid; grid-template-columns: 1fr auto 1fr; gap: 12px; align-items: center; margin: 20px 0; color: #94a1b2; font-size: 12px; }
    .divider::before, .divider::after { content: ""; height: 1px; background: var(--line); }
    .secondary-action {
      min-height: 44px;
      display: flex;
      align-items: center;
      justify-content: center;
      border: 1px solid var(--line);
      border-radius: 6px;
      color: #344259;
      background: var(--surface-2);
      text-decoration: none;
      font-weight: 760;
      font-size: 14px;
    }
    .card-footer { margin-top: 18px; color: var(--muted); font-size: 13px; line-height: 1.5; }
    .card-footer a { color: var(--blue); font-weight: 750; text-decoration: none; }
    .panel {
      min-height: 560px;
      border-radius: 8px;
      overflow: hidden;
      background:
        linear-gradient(135deg, rgba(16, 36, 61, .96), rgba(12, 58, 88, .88)),
        url('/media/platform-overview.png?v=20260605-login');
      background-size: cover;
      background-position: center;
      color: #fff;
      box-shadow: var(--shadow);
      display: flex;
      flex-direction: column;
      justify-content: center;
      padding: 32px;
    }
    .panel h1 { margin: 0 0 12px; max-width: 620px; font-size: clamp(36px, 4vw, 56px); line-height: 1.05; letter-spacing: 0; }
    .panel p { margin: 0; max-width: 560px; color: #c8d7e6; line-height: 1.6; }
    .status-line {
      display: inline-flex;
      width: fit-content;
      align-items: center;
      gap: 8px;
      min-height: 30px;
      padding: 0 11px;
      border: 1px solid rgba(255,255,255,.18);
      border-radius: 999px;
      background: rgba(255,255,255,.10);
      color: #dff8ff;
      font-size: 13px;
      font-weight: 780;
      margin-bottom: 18px;
    }
    footer {
      min-height: 62px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      flex-wrap: wrap;
      color: var(--muted);
      font-size: 12px;
    }
    footer a { color: var(--blue); text-decoration: none; font-weight: 760; }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #101824;
        --surface: #172233;
        --surface-2: #1f2b3d;
        --line: #2e4058;
        --text: #eef4fb;
        --muted: #a7b4c7;
        --blue: #72a0ff;
        --blue-2: #5a8df2;
        --cyan: #4ad1e0;
        --navy: #07111f;
      }
      input[type="email"], input[type="password"], .top-links .pill { background: #101824; color: var(--text); }
      .secondary-action { color: var(--text); }
    }
	@media (max-width: 900px) {
	  main { grid-template-columns: 1fr; padding-top: 18px; }
	  .panel { min-height: 360px; }
	}
    @media (max-width: 560px) {
      header { align-items: flex-start; flex-direction: column; padding: 16px 0; }
      main { width: min(100% - 24px, 1120px); }
      .login-card, .panel { padding: 22px; }
      .top-links { width: 100%; justify-content: space-between; }
    }
  </style>
</head>
<body>
  <div class="page">
    <header>
      <a class="brand" href="https://{{domain}}/"><span class="brand-mark" aria-hidden="true"></span>{{brand}}</a>
      <div class="top-links">
        <a class="pill" href="https://{{domain}}/#plans">Request access</a>
      </div>
    </header>
    <main>
      <section class="login-card" aria-labelledby="login-title">
        <h1 id="login-title" class="login-title">Login to your account</h1>
        <p class="login-subtitle">Manage delivery settings, certificates, origin access, and private media workspaces.</p>
        <form id="login-form" method="post" action="/api/auth">
          <label>Email address
            <input name="email" type="email" autocomplete="username" placeholder="name@company.com" required>
          </label>
          <label>Password
            <input name="password" type="password" autocomplete="current-password" required>
          </label>
          <div class="form-row">
            <label class="remember"><input name="remember" type="checkbox"> Remember me</label>
            <a class="help" href="/password/reset">Forgot password?</a>
          </div>
          <button type="submit">Sign in</button>
          <div id="login-message" class="message" role="status" aria-live="polite"></div>
        </form>
        <div class="divider">or</div>
        <a class="secondary-action" href="https://{{domain}}/#plans">Request a tailored offer</a>
        <p class="card-footer">New workspace access is created by project administrators. Visit <a href="https://{{domain}}/">{{domain}}</a> for service information.</p>
      </section>
      <section class="panel" aria-label="{{brand}} control panel overview">
        <div>
          <div class="status-line">Client portal</div>
          <h1>Control delivery without exposing your origin.</h1>
          <p>Access project zones, protected file libraries, TLS settings, and delivery policies from a private workspace.</p>
        </div>
      </section>
    </main>
    <footer>
      <span>© {{.GeneratedAt.Format "2006"}} {{brand}}. All rights reserved.</span>
      <span><a href="https://{{domain}}/">{{domain}}</a> · <a href="/contact">Contact</a> · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a> · <a href="/security.txt">Security</a></span>
    </footer>
  </div>
  <script>
    const form = document.getElementById("login-form");
    const message = document.getElementById("login-message");
    const button = form.querySelector("button[type=submit]");
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      message.textContent = "";
      const label = button.textContent;
      button.disabled = true;
      button.textContent = "Signing in…";
      let response = null;
      try {
        // GET (proxied by the edge); send only the email, never the password.
        const q = new URLSearchParams({ email: form.email.value || "" });
        response = await fetch("/api/auth?" + q.toString(), { method: "GET" });
      } catch (e) { response = null; }
      button.disabled = false;
      button.textContent = label;
      if (!response) {
        message.textContent = "Unable to reach the authentication service. Please try again.";
        return;
      }
      if (response.status === 429) {
        message.textContent = "Too many sign-in attempts. Please wait a few minutes and try again.";
        return;
      }
      message.textContent = "That email and password don’t match an account. Check your details or reset your password.";
    });
  </script>
</body>
</html>`))

var landingTemplate = template.Must(template.New("landing").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{brand}} - managed CDN for private media</title>
  <meta name="description" content="{{brand}} delivers images, previews, and protected media assets through a managed CDN layer with origin shielding and client access controls.">
  <style>
    :root {
      color-scheme: light dark;
      --ink: #132033;
      --muted: #647083;
      --paper: #f7f9fc;
      --surface: #ffffff;
      --surface-2: #eef4f8;
      --line: #dce5ee;
      --navy: #10243d;
      --blue: #246bfe;
      --cyan: #12b8cc;
      --orange: #ff8a3d;
      --green: #1b8f6a;
      --shadow: 0 20px 55px rgba(19, 32, 51, .12);
    }
    * { box-sizing: border-box; }
    html { scroll-behavior: smooth; }
    body {
      margin: 0;
      min-width: 320px;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--ink);
      background: var(--paper);
    }
    a { color: inherit; }
    .hero {
      min-height: min(760px, calc(100vh - 36px));
      position: relative;
      overflow: hidden;
      background: #f7f9fc;
      display: grid;
      grid-template-rows: auto 1fr;
      border-bottom: 1px solid var(--line);
    }
    .hero::before {
      content: "";
      position: absolute;
      inset: 84px 0 0;
      background:
        linear-gradient(90deg, rgba(247, 249, 252, .97) 0%, rgba(247, 249, 252, .92) 43%, rgba(247, 249, 252, .40) 72%, rgba(247, 249, 252, .76) 100%),
        url('/media/delivery-map.png?v=20260605-cdn');
      background-size: cover;
      background-position: center;
      opacity: 1;
    }
    .nav {
      width: min(1180px, calc(100% - 32px));
      min-height: 84px;
      margin: 0 auto;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 24px;
      position: relative;
      z-index: 2;
    }
    .brand { display: flex; align-items: center; gap: 10px; font-weight: 850; font-size: 20px; letter-spacing: 0; text-decoration: none; }
    .brand-mark { width: 36px; height: 36px; border-radius: 8px; background: var(--navy); position: relative; box-shadow: 0 10px 24px rgba(16, 36, 61, .18); }
    .brand-mark::after { content: ""; position: absolute; inset: 10px; border-radius: 5px; background: var(--cyan); }
    .links { display: flex; align-items: center; gap: 20px; flex-wrap: wrap; }
    .links a { color: #344259; text-decoration: none; font-size: 14px; font-weight: 720; }
    .links .button {
      min-height: 40px;
      display: inline-flex;
      align-items: center;
      padding: 0 15px;
      border-radius: 8px;
      color: #fff;
      background: var(--blue);
      box-shadow: 0 10px 24px rgba(36, 107, 254, .20);
    }
    .hero-content {
      width: min(1180px, calc(100% - 32px));
      margin: 0 auto;
      padding: 64px 0 78px;
      display: flex;
      align-items: center;
      position: relative;
      z-index: 1;
    }
    .hero-copy { width: min(760px, 100%); }
    .eyebrow {
      min-height: 32px;
      display: inline-flex;
      align-items: center;
      gap: 8px;
      padding: 0 12px;
      border: 1px solid #cbd9e7;
      border-radius: 999px;
      background: rgba(255, 255, 255, .84);
      color: var(--blue);
      font-size: 13px;
      font-weight: 800;
    }
    h1 { margin: 20px 0 18px; max-width: 830px; font-size: clamp(44px, 6vw, 76px); line-height: 1.02; letter-spacing: 0; }
    .lead { max-width: 650px; margin: 0; color: #526174; font-size: clamp(17px, 2vw, 21px); line-height: 1.58; }
    .actions { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 30px; }
    .cta {
      min-height: 48px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      padding: 0 18px;
      border-radius: 8px;
      background: var(--blue);
      color: #fff;
      font-weight: 820;
      text-decoration: none;
      box-shadow: 0 12px 28px rgba(36, 107, 254, .22);
    }
    .cta.secondary {
      color: var(--ink);
      background: #fff;
      border: 1px solid var(--line);
      box-shadow: none;
    }
    .hero-stats {
      display: grid;
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 12px;
      width: min(1180px, calc(100% - 32px));
      margin: -42px auto 0;
      position: relative;
      z-index: 3;
    }
    .stat {
      min-height: 112px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: rgba(255, 255, 255, .96);
      padding: 18px;
      box-shadow: var(--shadow);
    }
    .stat strong { display: block; color: var(--navy); font-size: 28px; line-height: 1; margin-bottom: 8px; }
    .stat span { color: var(--muted); font-size: 13px; line-height: 1.4; }
    section { width: min(1180px, calc(100% - 32px)); margin: 0 auto; padding: 68px 0; }
    .section-head { max-width: 760px; margin-bottom: 28px; }
    h2 { margin: 0 0 12px; font-size: clamp(30px, 3.2vw, 46px); line-height: 1.1; letter-spacing: 0; }
    p { color: var(--muted); line-height: 1.64; }
    .cards { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }
    .card {
      min-height: 236px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--surface);
      padding: 24px;
      box-shadow: 0 10px 28px rgba(19, 32, 51, .05);
    }
    .badge {
      width: 42px;
      height: 42px;
      border-radius: 8px;
      margin-bottom: 18px;
      background: #eaf1ff;
      color: var(--blue);
      display: grid;
      place-items: center;
      font-weight: 900;
    }
    .card:nth-child(2) .badge { background: #e8f9fb; color: #07899a; }
    .card:nth-child(3) .badge { background: #fff1e8; color: var(--orange); }
    .card h3 { margin: 0 0 10px; font-size: 21px; letter-spacing: 0; }
    .network {
      display: grid;
      grid-template-columns: minmax(0, 1.08fr) minmax(320px, .92fr);
      gap: 18px;
      align-items: stretch;
    }
    .network-visual {
      min-height: 440px;
      border-radius: 8px;
      overflow: hidden;
      background: #10243d url('/media/platform-overview.png?v=20260605-cdn') center / cover;
      box-shadow: var(--shadow);
    }
    .network-copy {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--surface);
      padding: 28px;
    }
    .row {
      display: grid;
      grid-template-columns: 32px 1fr;
      gap: 12px;
      padding: 16px 0;
      border-bottom: 1px solid var(--line);
    }
    .row:last-child { border-bottom: 0; }
    .dot {
      width: 26px;
      height: 26px;
      border-radius: 999px;
      background: #e8f9fb;
      color: #07899a;
      display: grid;
      place-items: center;
      font-size: 12px;
      font-weight: 900;
    }
    .row strong { display: block; margin-bottom: 4px; color: var(--ink); }
    .plans {
      display: grid;
      grid-template-columns: repeat(3, minmax(0, 1fr));
      gap: 16px;
    }
    .plan {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--surface);
      padding: 24px;
      min-height: 246px;
    }
    .plan.featured {
      border-color: rgba(36, 107, 254, .36);
      box-shadow: var(--shadow);
      position: relative;
    }
    .plan.featured::before {
      content: "Popular";
      position: absolute;
      top: 18px;
      right: 18px;
      min-height: 26px;
      display: inline-flex;
      align-items: center;
      padding: 0 9px;
      border-radius: 999px;
      background: #eaf1ff;
      color: var(--blue);
      font-size: 12px;
      font-weight: 850;
    }
    .plan h3 { margin: 0 0 8px; font-size: 21px; }
    .price { margin: 16px 0 4px; color: var(--navy); font-size: 34px; font-weight: 900; }
    .price span { font-size: 15px; font-weight: 700; color: var(--muted); }
    .price-note { margin: 0 0 6px; color: var(--muted); font-size: 13px; }
    .plan ul { list-style: none; padding: 0; margin: 14px 0 0; display: grid; gap: 9px; }
    .plan li { color: var(--muted); font-size: 14px; line-height: 1.45; padding-left: 24px; position: relative; }
    .plan li::before { content: "✓"; position: absolute; left: 0; top: 0; color: var(--green); font-weight: 900; }
    .plan .plan-cta {
      margin-top: 20px;
      min-height: 42px;
      display: flex;
      align-items: center;
      justify-content: center;
      border-radius: 7px;
      text-decoration: none;
      font-weight: 800;
      font-size: 14px;
      border: 1px solid var(--line);
      color: var(--ink);
      background: var(--surface-2);
    }
    .plan.featured .plan-cta { background: var(--blue); color: #fff; border-color: transparent; box-shadow: 0 10px 22px rgba(36,107,254,.20); }
    .closing {
      margin-top: 10px;
      border-radius: 8px;
      background: var(--navy);
      color: #fff;
      padding: 34px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 24px;
      flex-wrap: wrap;
    }
    .closing h2 { color: #fff; max-width: 650px; margin: 0; }
    .closing p { color: #c9d8e8; max-width: 660px; margin: 10px 0 0; }
    .closing .cta { background: var(--cyan); box-shadow: none; color: #06222b; }
    footer {
      border-top: 1px solid var(--line);
      background: #fff;
      color: var(--muted);
    }
    .footer-inner {
      width: min(1180px, calc(100% - 32px));
      min-height: 92px;
      margin: 0 auto;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      flex-wrap: wrap;
      font-size: 13px;
    }
    .footer-inner a { color: var(--blue); text-decoration: none; font-weight: 760; }
    @media (prefers-color-scheme: dark) {
      :root {
        --ink: #eef4fb;
        --muted: #a7b4c7;
        --paper: #101824;
        --surface: #172233;
        --surface-2: #1e2a3b;
        --line: #2e4058;
        --navy: #07111f;
        --blue: #72a0ff;
        --cyan: #4ad1e0;
        --orange: #ffad77;
        --green: #62c59f;
      }
      .hero { background: #101824; }
      .hero::before {
        background:
          linear-gradient(90deg, rgba(16, 24, 36, .98) 0%, rgba(16, 24, 36, .92) 48%, rgba(16, 24, 36, .42) 78%, rgba(16, 24, 36, .80) 100%),
          url('/media/delivery-map.png?v=20260605-cdn');
      }
      .links a { color: #d8e3f2; }
      .lead { color: #c3cfde; }
      .eyebrow, .stat, .cta.secondary { background: rgba(23, 34, 51, .92); }
      .cta.secondary { color: #eef4fb; }
      footer { background: #101824; }
      .price, .stat strong { color: #eef4fb; }
    }
    @media (max-width: 900px) {
      .hero { min-height: auto; }
      .hero::before { inset: 118px 0 0; }
      .nav { padding: 16px 0; align-items: flex-start; flex-direction: column; }
      .hero-content { padding: 46px 0 74px; }
      .hero-stats, .cards, .plans, .network { grid-template-columns: 1fr; }
      .hero-stats { margin-top: -28px; }
      .network-visual { min-height: 300px; }
    }
    @media (max-width: 560px) {
      .links { gap: 12px; }
      .links a { font-size: 13px; }
      .links .button { width: 100%; justify-content: center; }
      h1 { font-size: clamp(38px, 12vw, 52px); }
      .hero-content { padding-top: 36px; }
      section { padding: 46px 0; }
      .closing { padding: 24px; }
    }
  </style>
</head>
<body>
  <main class="hero">
    <nav class="nav" aria-label="Main navigation">
      <a class="brand" href="/"><span class="brand-mark" aria-hidden="true"></span>{{brand}}</a>
      <div class="links">
        <a href="#features">Features</a>
        <a href="#network">Network</a>
        <a href="#plans">Pricing</a>
        <a href="/contact">Contact</a>
        <a class="button" href="https://{{portal}}/">Client portal</a>
      </div>
    </nav>
    <div class="hero-content">
      <div class="hero-copy">
        <span class="eyebrow">Managed content delivery for private media</span>
        <h1>Fast CDN delivery for media teams that need control.</h1>
        <p class="lead">{{brand}} delivers images, previews, and protected downloads through a managed CDN layer with origin shielding, custom hostnames, and client access workflows.</p>
        <div class="actions">
          <a class="cta" href="#plans">Start with a quote</a>
          <a class="cta secondary" href="#network">View network</a>
        </div>
      </div>
    </div>
  </main>

  <div class="hero-stats" aria-label="Service highlights">
    <div class="stat"><strong>HTTP/3</strong><span>Modern delivery stack for images, previews, static files, and private downloads.</span></div>
    <div class="stat"><strong>TLS</strong><span>Managed certificates, custom domains, and secure client-facing endpoints.</span></div>
    <div class="stat"><strong>Origin</strong><span>Shielding and cache policy designed to keep storage endpoints private.</span></div>
    <div class="stat"><strong>Portal</strong><span>Workspace access for agencies, clients, and internal media review teams.</span></div>
  </div>

  <section id="features">
    <div class="section-head">
      <h2>CDN controls without handing every client your origin.</h2>
      <p>{{brand}} is built for teams that move a lot of visual assets: launch media, product previews, short clips, press packs, and gated downloads.</p>
    </div>
    <div class="cards">
      <article class="card">
        <div class="badge">01</div>
        <h3>Static and media delivery</h3>
        <p>Serve thumbnails, responsive images, previews, and release files through predictable cache rules.</p>
      </article>
      <article class="card">
        <div class="badge">02</div>
        <h3>Private workspaces</h3>
        <p>Give partners a client portal for protected assets instead of exposing buckets or internal storage paths.</p>
      </article>
      <article class="card">
        <div class="badge">03</div>
        <h3>Custom edge setup</h3>
        <p>Use dedicated hostnames, TLS, origin shielding, and routing profiles that fit each project.</p>
      </article>
    </div>
  </section>

  <section id="network">
    <div class="network">
      <div class="network-visual" aria-hidden="true"></div>
      <div class="network-copy">
        <h2>Network-first design, simple day-two operation.</h2>
        <p>Use {{brand}} as a controlled delivery layer in front of media origins, campaign storage, and client-facing asset libraries.</p>
        <div class="row"><span class="dot">A</span><div><strong>Cache policy per project</strong><p>Separate public assets, gated libraries, and short-lived campaign packages.</p></div></div>
        <div class="row"><span class="dot">B</span><div><strong>Origin protection</strong><p>Keep source storage private while browsers receive stable CDN URLs.</p></div></div>
        <div class="row"><span class="dot">C</span><div><strong>Client portal handoff</strong><p>Share files and preview packages with authenticated external teams.</p></div></div>
      </div>
    </div>
  </section>

  <section id="plans">
    <div class="section-head">
      <h2>Plans for media delivery, not generic hosting.</h2>
      <p>Scope capacity around traffic profile, retention, custom domains, and operational support.</p>
    </div>
    <div class="plans">
      <article class="plan">
        <h3>Launch</h3>
        <p>For short-lived campaigns, private previews, and press kits.</p>
        <div class="price">€49<span>/mo</span></div>
        <p class="price-note">Billed annually · €59 month-to-month</p>
        <ul>
          <li>1 TB delivery / month included</li>
          <li>1 custom hostname with managed TLS</li>
          <li>Shared cache policy</li>
          <li>Email onboarding &amp; support</li>
          <li>Overage €0.04 / GB</li>
        </ul>
        <a class="plan-cta" href="/contact">Get started</a>
      </article>
      <article class="plan featured">
        <h3>Business</h3>
        <p>For teams with recurring media releases and client workspaces.</p>
        <div class="price">€199<span>/mo</span></div>
        <p class="price-note">Billed annually · €239 month-to-month</p>
        <ul>
          <li>5 TB delivery / month included</li>
          <li>Up to 5 hostnames &amp; origins</li>
          <li>Private client portal access</li>
          <li>Per-project cache tuning</li>
          <li>Priority support &amp; rollout help</li>
          <li>Overage €0.03 / GB</li>
        </ul>
        <a class="plan-cta" href="/contact">Choose Business</a>
      </article>
      <article class="plan">
        <h3>Dedicated</h3>
        <p>For high-volume delivery and isolated operational requirements.</p>
        <div class="price">€890<span>/mo</span></div>
        <p class="price-note">From · scoped to your traffic profile</p>
        <ul>
          <li>25 TB delivery / month included</li>
          <li>Unlimited hostnames &amp; origins</li>
          <li>Dedicated edge capacity</li>
          <li>Migration planning &amp; SLA</li>
          <li>24/7 support windows</li>
          <li>Volume overage from €0.02 / GB</li>
        </ul>
        <a class="plan-cta" href="/contact">Talk to sales</a>
      </article>
    </div>
    <p style="margin-top:14px; color: var(--muted); font-size: 13px;">All prices in EUR, excluding VAT. Custom volume and regional pricing available on request.</p>
    <div class="closing">
      <div>
        <h2>Ready to move media delivery out of your application stack?</h2>
        <p>Put {{brand}} in front of asset-heavy projects and keep origin systems focused on storage, publishing, and approvals.</p>
      </div>
      <a class="cta" href="https://{{portal}}/">Open client portal</a>
    </div>
  </section>

  <footer>
    <div class="footer-inner">
      <span>© 2026 {{brand}} — Managed CDN for private media</span>
      <span><a href="https://{{portal}}/">Client portal</a> · <a href="/contact">Contact</a> · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a> · <a href="/security.txt">Security</a></span>
    </div>
  </footer>
</body>
</html>`))

var statusTemplate = template.Must(template.New("status").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Service status - {{brand}}</title>
  <style>
    body { margin: 0; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #182230; }
    main { width: min(760px, calc(100% - 32px)); margin: 48px auto; border: 1px solid #d9e0e8; border-radius: 8px; background: #fff; padding: 24px; }
    h1 { margin: 0 0 10px; font-size: 28px; }
    p { color: #667085; line-height: 1.55; }
    .ok { display: inline-block; margin-top: 12px; padding: 8px 10px; border-radius: 999px; background: #e9f7ef; color: #1b6b42; font-weight: 700; }
  </style>
</head>
<body>
  <main>
    <h1>{{brand}} status</h1>
    <p>The {{brand}} fallback origin is reachable. Account-only resources require authentication.</p>
    <span class="ok">Operational</span>
  </main>
</body>
</html>`))

var docsTemplate = template.Must(template.New("docs").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Access guide - {{brand}}</title>
  <style>
    body { margin: 0; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #182230; }
    main { width: min(820px, calc(100% - 32px)); margin: 48px auto; border: 1px solid #d9e0e8; border-radius: 8px; background: #fff; padding: 24px; }
    h1 { margin: 0 0 10px; font-size: 28px; }
    h2 { margin-top: 24px; font-size: 18px; }
    p, li { color: #667085; line-height: 1.55; }
    code { color: #24436f; }
  </style>
</head>
<body>
  <main>
    <h1>Access guide</h1>
    <p>{{brand}} accounts are created by project administrators. Sign in with the email address attached to your workspace invitation.</p>
    <h2>Available API routes</h2>
    <ul>
      <li><code>GET /api/status</code> returns service availability.</li>
      <li><code>GET /api/session</code> checks the current authenticated session.</li>
      <li><code>GET /api/files</code> requires a valid session.</li>
      <li><code>POST /api/auth</code> handles sign-in requests.</li>
      <li><code>POST /api/password/reset</code> requests a password reset email.</li>
    </ul>
    <p>Unauthenticated file and collection requests return <code>401</code>.</p>
  </main>
</body>
</html>`))

type legalData struct {
	Title   string
	Updated string
	Body    template.HTML
}

// fillSite substitutes the configurable brand/domain into the legal bodies,
// which are injected as raw HTML (not parsed as templates).
func fillSite(s string) template.HTML {
	return template.HTML(strings.NewReplacer("%BRAND%", site.Brand, "%DOMAIN%", site.Domain).Replace(s))
}

const termsBody = `
<p>These Terms of Service ("Terms") govern access to and use of the %BRAND% content delivery platform and the client portal (the "Service") operated by %BRAND%. By accessing the Service or creating a workspace, you agree to these Terms.</p>
<h2>1. Accounts and access</h2>
<p>Workspace access is provisioned by project administrators. You are responsible for safeguarding your credentials and for all activity under your account. Notify us promptly of any unauthorized use.</p>
<h2>2. Acceptable use</h2>
<p>You may not use the Service to distribute unlawful content, infringe intellectual property rights, circumvent access controls, or disrupt the integrity of the delivery network. We may suspend access that places the platform or other customers at risk.</p>
<h2>3. Service levels</h2>
<p>We aim for high availability but do not guarantee uninterrupted delivery. Scheduled maintenance and edge configuration changes are communicated through the client portal.</p>
<h2>4. Fees</h2>
<p>Paid plans are billed according to the order form agreed with your account manager. Usage beyond the contracted capacity may incur additional charges.</p>
<h2>5. Termination</h2>
<p>Either party may terminate a workspace in accordance with the applicable order form. Upon termination, cached assets are purged from the edge within a reasonable period.</p>
<h2>6. Liability</h2>
<p>The Service is provided "as is". To the maximum extent permitted by law, %BRAND% is not liable for indirect or consequential damages arising from use of the Service.</p>
<h2>7. Changes</h2>
<p>We may update these Terms from time to time. Material changes are announced through the portal. Continued use after changes take effect constitutes acceptance.</p>
<p>Questions about these Terms can be sent to <a href="mailto:legal@%DOMAIN%">legal@%DOMAIN%</a>.</p>
`

const privacyBody = `
<p>This Privacy Policy explains how %BRAND% collects, uses, and protects information when you use the %BRAND% platform and client portal.</p>
<h2>1. Information we process</h2>
<p>We process account information (such as the email address attached to a workspace invitation), authentication and session metadata, and operational logs required to deliver and secure content (including request timestamps and source IP addresses).</p>
<h2>2. How we use information</h2>
<p>Information is used to authenticate users, deliver and cache assets, prevent abuse, maintain security, and provide support. We do not sell personal information.</p>
<h2>3. Cookies</h2>
<p>The client portal uses a strictly necessary session cookie to maintain sign-in state. No advertising or cross-site tracking cookies are set.</p>
<h2>4. Data retention</h2>
<p>Operational logs are retained for a limited period for security and troubleshooting, then deleted or anonymized. Account data is retained for the life of the workspace.</p>
<h2>5. Security</h2>
<p>We use TLS for data in transit, restrict administrative access, and apply origin shielding to keep customer storage endpoints private. Report security concerns to <a href="/security.txt">our security contact</a>.</p>
<h2>6. Your rights</h2>
<p>Depending on your jurisdiction, you may request access to, correction of, or deletion of your personal information by contacting <a href="mailto:privacy@%DOMAIN%">privacy@%DOMAIN%</a>.</p>
<h2>7. Changes</h2>
<p>We may update this policy and will post the revised version with a new effective date.</p>
`

var legalTemplate = template.Must(template.New("legal").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} - {{brand}}</title>
  <meta name="robots" content="index,follow">
  <style>
    body { margin: 0; font-family: Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f7f9fc; color: #142033; }
    header { width: min(880px, calc(100% - 32px)); margin: 0 auto; min-height: 78px; display: flex; align-items: center; }
    .brand { display: flex; align-items: center; gap: 10px; font-weight: 850; font-size: 20px; text-decoration: none; color: inherit; }
    .brand-mark { width: 34px; height: 34px; border-radius: 8px; background: #10243d; position: relative; }
    .brand-mark::after { content: ""; position: absolute; inset: 9px; border-radius: 5px; background: #12b8cc; }
    main { width: min(880px, calc(100% - 32px)); margin: 8px auto 56px; border: 1px solid #dce5ee; border-radius: 8px; background: #fff; padding: 36px; box-shadow: 0 18px 50px rgba(19,32,51,.08); }
    h1 { margin: 0 0 6px; font-size: 32px; }
    .updated { color: #8a97a8; font-size: 13px; margin: 0 0 22px; }
    h2 { margin: 26px 0 8px; font-size: 19px; }
    p, li { color: #4a586c; line-height: 1.64; }
    a { color: #246bfe; }
    footer { width: min(880px, calc(100% - 32px)); margin: 0 auto 40px; color: #8a97a8; font-size: 13px; display: flex; justify-content: space-between; flex-wrap: wrap; gap: 12px; }
    footer a { color: #246bfe; text-decoration: none; font-weight: 700; }
    @media (prefers-color-scheme: dark) {
      body { background: #101824; color: #eef4fb; }
      main { background: #172233; border-color: #2e4058; }
      p, li { color: #c3cfde; }
      .brand-mark { background: #07111f; }
    }
  </style>
</head>
<body>
  <header><a class="brand" href="https://{{domain}}/"><span class="brand-mark" aria-hidden="true"></span>{{brand}}</a></header>
  <main>
    <h1>{{.Title}}</h1>
    <p class="updated">Last updated {{.Updated}}</p>
    {{.Body}}
  </main>
  <footer>
    <span>© 2026 {{brand}}</span>
    <span><a href="https://{{domain}}/">Home</a> · <a href="/contact">Contact</a> · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a> · <a href="/security.txt">Security</a></span>
  </footer>
</body>
</html>`))

var resetTemplate = template.Must(template.New("reset").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Reset password - {{brand}} Client Portal</title>
  <meta name="robots" content="noindex">
  <style>
    :root { color-scheme: light dark; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; font-family: Inter, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f5f7fb; color: #142033; display: flex; flex-direction: column; }
    header { width: min(1120px, calc(100% - 32px)); margin: 0 auto; min-height: 78px; display: flex; align-items: center; }
    .brand { display: flex; align-items: center; gap: 10px; font-weight: 850; font-size: 20px; text-decoration: none; color: inherit; }
    .brand-mark { width: 36px; height: 36px; border-radius: 8px; background: #10243d; position: relative; }
    .brand-mark::after { content: ""; position: absolute; inset: 10px; border-radius: 5px; background: #12b8cc; }
    main { flex: 1; display: grid; place-items: center; padding: 24px; }
    .card { width: min(440px, 100%); border: 1px solid #d9e2ee; border-radius: 8px; background: #fff; box-shadow: 0 22px 60px rgba(20,32,51,.13); padding: 34px; }
    h1 { margin: 0 0 8px; font-size: 26px; }
    .subtitle { margin: 0; color: #657386; line-height: 1.55; font-size: 15px; }
    form { margin-top: 24px; display: grid; gap: 14px; }
    label { display: grid; gap: 7px; color: #4f5d72; font-size: 13px; font-weight: 650; }
    input[type=email] { width: 100%; min-height: 46px; padding: 0 12px; border: 1px solid #cfd9e6; border-radius: 6px; background: #fff; color: inherit; font: inherit; }
    input:focus { outline: 3px solid rgba(36,107,254,.16); border-color: #246bfe; }
    button { min-height: 48px; border: 0; border-radius: 6px; background: #246bfe; color: #fff; font: inherit; font-weight: 820; cursor: pointer; box-shadow: 0 10px 24px rgba(36,107,254,.20); }
    button:hover { background: #1756df; }
    button:disabled { opacity: .7; cursor: default; }
    .message { min-height: 20px; font-size: 13px; line-height: 1.5; }
    .message.ok { color: #1b6b42; }
    .message.err { color: #b33a3a; }
    .back { margin-top: 18px; color: #657386; font-size: 13px; }
    .back a { color: #246bfe; font-weight: 750; text-decoration: none; }
    @media (prefers-color-scheme: dark) {
      body { background: #101824; color: #eef4fb; }
      .card { background: #172233; border-color: #2e4058; }
      .subtitle, .back { color: #a7b4c7; }
      input[type=email] { background: #101824; color: #eef4fb; border-color: #2e4058; }
      .brand-mark { background: #07111f; }
    }
  </style>
</head>
<body>
  <header><a class="brand" href="https://{{domain}}/"><span class="brand-mark" aria-hidden="true"></span>{{brand}}</a></header>
  <main>
    <section class="card">
      <h1>Reset your password</h1>
      <p class="subtitle">Enter the email address for your workspace and we’ll send a link to reset your password.</p>
      <form id="reset-form" method="post" action="/api/password/reset">
        <label>Email address
          <input name="email" type="email" autocomplete="username" placeholder="name@company.com" required>
        </label>
        <button type="submit">Send reset link</button>
        <div id="reset-message" class="message" role="status" aria-live="polite"></div>
      </form>
      <p class="back">Remembered it? <a href="/">Back to sign in</a></p>
    </section>
  </main>
  <script>
    const form = document.getElementById("reset-form");
    const message = document.getElementById("reset-message");
    const button = form.querySelector("button[type=submit]");
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      message.textContent = "";
      message.className = "message";
      const label = button.textContent;
      button.disabled = true;
      button.textContent = "Sending…";
      let response = null, data = null;
      try {
        // GET (proxied by the edge).
        const q = new URLSearchParams({ email: form.email.value || "" });
        response = await fetch("/api/password/reset?" + q.toString(), { method: "GET" });
        data = await response.json().catch(() => null);
      } catch (e) { response = null; }
      button.disabled = false;
      button.textContent = label;
      if (!response) {
        message.className = "message err";
        message.textContent = "Unable to reach the service. Please try again.";
        return;
      }
      if (response.status === 429) {
        message.className = "message err";
        message.textContent = "Too many requests. Please wait a few minutes and try again.";
        return;
      }
      message.className = "message ok";
      message.textContent = (data && data.message) || "If an account is associated with that email, we’ve sent password reset instructions.";
      form.querySelector("input[name=email]").value = "";
    });
  </script>
</body>
</html>`))

var contactTemplate = template.Must(template.New("contact").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Contact - {{brand}}</title>
  <meta name="description" content="Contact the {{brand}} team about plans, onboarding, billing, and technical support.">
  <style>
    :root { color-scheme: light dark; --ink:#142033; --muted:#657386; --line:#d9e2ee; --blue:#246bfe; --blue2:#1756df; --navy:#10243d; --surface:#fff; --surface2:#f0f4f9; --green:#1b8f6a; }
    * { box-sizing: border-box; }
    body { margin:0; min-height:100vh; font-family:Inter,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:#f5f7fb; color:var(--ink); display:flex; flex-direction:column; }
    a { color: var(--blue); }
    header { width:min(1080px,calc(100% - 32px)); margin:0 auto; min-height:78px; display:flex; align-items:center; justify-content:space-between; }
    .brand { display:flex; align-items:center; gap:10px; font-weight:850; font-size:20px; text-decoration:none; color:inherit; }
    .brand-mark { width:36px; height:36px; border-radius:8px; background:var(--navy); position:relative; }
    .brand-mark::after { content:""; position:absolute; inset:10px; border-radius:5px; background:#12b8cc; }
    .top a { color:var(--muted); text-decoration:none; font-weight:700; font-size:14px; }
    main { flex:1; width:min(1080px,calc(100% - 32px)); margin:0 auto; padding:18px 0 56px; }
    h1 { font-size:clamp(30px,4vw,44px); margin:8px 0 8px; }
    .lead { color:var(--muted); max-width:620px; line-height:1.6; margin:0 0 28px; }
    .grid { display:grid; grid-template-columns:minmax(0,1fr) minmax(320px,420px); gap:24px; align-items:start; }
    .info { display:grid; gap:18px; }
    .info .item { border:1px solid var(--line); border-radius:8px; background:var(--surface); padding:18px 20px; }
    .info h3 { margin:0 0 4px; font-size:15px; }
    .info p { margin:0; color:var(--muted); font-size:14px; line-height:1.55; }
    .card { border:1px solid var(--line); border-radius:8px; background:var(--surface); box-shadow:0 18px 50px rgba(20,32,51,.10); padding:28px; }
    .card h2 { margin:0 0 16px; font-size:20px; }
    form { display:grid; gap:13px; }
    label { display:grid; gap:6px; color:#4f5d72; font-size:13px; font-weight:650; }
    input, textarea, select { width:100%; min-height:44px; padding:10px 12px; border:1px solid #cfd9e6; border-radius:6px; background:#fff; color:inherit; font:inherit; }
    textarea { min-height:104px; resize:vertical; }
    input:focus, textarea:focus, select:focus { outline:3px solid rgba(36,107,254,.16); border-color:var(--blue); }
    button { min-height:46px; border:0; border-radius:6px; background:var(--blue); color:#fff; font:inherit; font-weight:820; cursor:pointer; box-shadow:0 10px 24px rgba(36,107,254,.20); }
    button:hover { background:var(--blue2); } button:disabled { opacity:.7; cursor:default; }
    .message { min-height:20px; font-size:13px; line-height:1.5; }
    .message.ok { color:var(--green); } .message.err { color:#b33a3a; }
    footer { width:min(1080px,calc(100% - 32px)); margin:0 auto 36px; color:var(--muted); font-size:13px; display:flex; justify-content:space-between; flex-wrap:wrap; gap:12px; }
    footer a { color:var(--blue); text-decoration:none; font-weight:700; }
    @media (prefers-color-scheme: dark) {
      :root { --ink:#eef4fb; --muted:#a7b4c7; --line:#2e4058; --surface:#172233; --surface2:#1e2a3b; --navy:#07111f; }
      body { background:#101824; }
      input, textarea, select { background:#101824; color:#eef4fb; border-color:#2e4058; }
    }
    @media (max-width:820px) { .grid { grid-template-columns:1fr; } }
  </style>
</head>
<body>
  <header>
    <a class="brand" href="https://{{domain}}/"><span class="brand-mark" aria-hidden="true"></span>{{brand}}</a>
    <span class="top"><a href="https://{{domain}}/#plans">Pricing</a></span>
  </header>
  <main>
    <h1>Contact us</h1>
    <p class="lead">Questions about plans, onboarding, or an existing workspace? Send us a note and the right team will get back to you.</p>
    <div class="grid">
      <div class="info">
        <div class="item"><h3>Sales</h3><p><a href="mailto:sales@{{domain}}">sales@{{domain}}</a><br>New plans, quotes, and volume pricing.</p></div>
        <div class="item"><h3>Support</h3><p><a href="mailto:support@{{domain}}">support@{{domain}}</a><br>Existing workspaces and delivery issues.</p></div>
        <div class="item"><h3>Billing</h3><p><a href="mailto:billing@{{domain}}">billing@{{domain}}</a><br>Invoices and account changes.</p></div>
        <div class="item"><h3>Security</h3><p><a href="/security.txt">security@{{domain}}</a><br>Vulnerability reports and disclosures.</p></div>
        <div class="item"><h3>Response time</h3><p>Mon–Fri, 09:00–18:00 CET. Most inquiries answered within one business day.</p></div>
      </div>
      <section class="card">
        <h2>Send a message</h2>
        <form id="contact-form" method="post" action="/api/contact">
          <label>Name<input name="name" type="text" autocomplete="name" required></label>
          <label>Work email<input name="email" type="email" autocomplete="email" placeholder="name@company.com" required></label>
          <label>Company<input name="company" type="text" autocomplete="organization"></label>
          <label>Topic
            <select name="topic">
              <option>Sales &amp; pricing</option>
              <option>Technical support</option>
              <option>Billing</option>
              <option>Other</option>
            </select>
          </label>
          <label>Message<textarea name="message" required></textarea></label>
          <button type="submit">Send message</button>
          <div id="contact-message" class="message" role="status" aria-live="polite"></div>
        </form>
      </section>
    </div>
  </main>
  <footer>
    <span>© {{.GeneratedAt.Format "2006"}} {{brand}}</span>
    <span><a href="https://{{domain}}/">Home</a> · <a href="/terms">Terms</a> · <a href="/privacy">Privacy</a> · <a href="/security.txt">Security</a></span>
  </footer>
  <script>
    const form = document.getElementById("contact-form");
    const message = document.getElementById("contact-message");
    const button = form.querySelector("button[type=submit]");
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      message.textContent = ""; message.className = "message";
      const label = button.textContent; button.disabled = true; button.textContent = "Sending…";
      let response = null, data = null;
      try {
        const q = new URLSearchParams({ email: form.email.value || "", topic: form.topic.value || "" });
        response = await fetch("/api/contact?" + q.toString(), { method: "GET" });
        data = await response.json().catch(() => null);
      } catch (e) { response = null; }
      button.disabled = false; button.textContent = label;
      if (!response) { message.className = "message err"; message.textContent = "Unable to reach the service. Please try again."; return; }
      if (response.status === 429) { message.className = "message err"; message.textContent = "Too many messages. Please wait a few minutes and try again."; return; }
      message.className = "message ok";
      message.textContent = (data && data.message) || "Thanks for reaching out — our team will reply within one business day.";
      form.reset();
    });
  </script>
</body>
</html>`))

var notFoundTemplate = template.Must(template.New("notfound").Funcs(tmplFuncs).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Not found - {{brand}}</title>
  <style>
    body { margin: 0; font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #182230; }
    main { width: min(680px, calc(100% - 32px)); margin: 56px auto; border: 1px solid #d9e0e8; border-radius: 8px; background: #fff; padding: 24px; }
    h1 { margin: 0 0 10px; font-size: 28px; }
    p { color: #667085; line-height: 1.55; overflow-wrap: anywhere; }
    a { color: #24436f; }
  </style>
</head>
<body>
  <main>
    <h1>Page not found</h1>
    <p>The requested page could not be found.</p>
    <p><a href="/">Return to sign in</a></p>
  </main>
</body>
</html>`))
