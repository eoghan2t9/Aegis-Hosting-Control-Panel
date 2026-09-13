package svc

import (
	"html"
	"os"
	"path/filepath"
	"strings"
)

// placeholderPage is seeded as index.html into every newly created domain's
// document root so visitors see a styled "under construction" page instead of
// a directory listing or a 403. It mirrors the panel's own design system
// (web/css/aegis.css: near-black surfaces, engineering grid, acid-lime accent,
// mono details) so the hosting brand reads consistent from panel to sites.
//
// The web-app installer deliberately treats a seeded index.html as a
// placeholder and removes it on install (webapps.go), so this page is only
// ever a visitor's first impression, never an obstacle.
//
// The domain name is injected with html.EscapeString even though ValidDomain
// already restricts it to a safe charset — defense in depth.

const placeholderPageTmpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DOMAIN — under construction</title>
<style>
  :root {
    --bg0: #06080b; --bg1: #0b0f14; --bg2: #10151c;
    --line: #1e2732; --line-strong: #2a3644;
    --text-1: #e8edf2; --text-2: #9aa7b4; --text-3: #5c6a78;
    --accent: #c6f14e; --accent-ink: #0a0f04;
    --teal: #4ee6c2; --warn: #f2b64b;
    --mono: "SFMono-Regular", "JetBrains Mono", "Cascadia Mono", ui-monospace, Menlo, Consolas, monospace;
    --sans: "Inter", "SF Pro Text", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  }
  * { box-sizing: border-box; }
  html, body { margin: 0; min-height: 100%; }
  body {
    display: grid; place-items: center; padding: 24px;
    background: var(--bg0); color: var(--text-1);
    font-family: var(--sans); font-size: 15px; line-height: 1.5;
    background-image:
      radial-gradient(900px 500px at 12% -10%, rgba(198, 241, 78, 0.05), transparent 60%),
      radial-gradient(800px 420px at 100% 0%, rgba(78, 230, 194, 0.04), transparent 55%),
      linear-gradient(var(--line) 1px, transparent 1px),
      linear-gradient(90deg, var(--line) 1px, transparent 1px);
    background-size: auto, auto, 44px 44px, 44px 44px;
    background-attachment: fixed;
  }
  main { text-align: center; max-width: 640px; }
  .tag {
    display: inline-flex; align-items: center; gap: 7px;
    font-family: var(--mono); font-size: 11px; letter-spacing: 0.14em;
    text-transform: uppercase; padding: 4px 12px; border-radius: 20px;
    border: 1px solid rgba(242, 182, 75, 0.35);
    background: rgba(242, 182, 75, 0.12); color: var(--warn);
  }
  .dot {
    width: 7px; height: 7px; border-radius: 50%;
    background: var(--warn); animation: pulse 1.6s ease-in-out infinite;
  }
  @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.25; } }
  h1 {
    margin: 26px 0 10px; font-family: var(--mono); font-weight: 700;
    font-size: clamp(22px, 5.5vw, 40px); letter-spacing: 0.06em;
    color: var(--accent); overflow-wrap: anywhere;
  }
  p.sub { margin: 0 0 30px; color: var(--text-2); }
  .status {
    display: inline-flex; align-items: center; gap: 9px;
    font-family: var(--mono); font-size: 12.5px; letter-spacing: 0.04em;
    color: var(--teal); border: 1px solid var(--line-strong);
    background: var(--bg2); padding: 9px 16px; border-radius: 8px;
  }
  .status .dot { background: var(--teal); animation-duration: 2.2s; }
  footer { margin-top: 34px; font-family: var(--mono); font-size: 11px; color: var(--text-3); }
</style>
</head>
<body>
<main>
  <span class="tag"><span class="dot"></span>under construction</span>
  <h1>DOMAIN</h1>
  <p class="sub">This site is being built. The owner is still wiring things up &mdash; check back soon.</p>
  <span class="status"><span class="dot"></span>hosting provisioned &middot; awaiting content</span>
  <footer>DOMAIN</footer>
</main>
</body>
</html>
`

// writePlaceholderPage seeds the under-construction index page into a fresh
// document root. It never overwrites existing content: if index.html is
// already present the file is left untouched.
func writePlaceholderPage(root, domain string) error {
	dst := filepath.Join(root, "index.html")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	page := strings.ReplaceAll(placeholderPageTmpl, "DOMAIN", html.EscapeString(domain))
	return os.WriteFile(dst, []byte(page), 0o644)
}
