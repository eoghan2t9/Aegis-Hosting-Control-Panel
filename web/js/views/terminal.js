import { addRoute, me } from "../app.js";
import { api } from "../api.js";
import { esc, pageHead, toast } from "../ui.js";

addRoute("/terminal", {
  title: "Terminal",
  icon: "terminal",
  group: "Server",
  order: 5,
  render: async (view) => {
    const u = me();
    view.innerHTML = pageHead("Terminal", "Interactive shell inside your account (" + u.username + "). Use it to run composer, artisan, wp-cli or inspect your files.", `
      <button class="btn" id="term-reconnect">${"↻"} Reconnect</button>`);
    view.insertAdjacentHTML("beforeend", `<div class="term-wrap"><div id="term-mount" class="term-xterm"></div></div>
      <p class="small dim" id="term-note" style="margin-top:8px">Session runs as <b>${esc(u.username)}</b> in ${esc(u.home_dir)}.</p>`);

    document.getElementById("term-reconnect").onclick = () => { location.hash = "#/terminal"; location.reload(); };

    // xterm is loaded from a CDN; give a graceful fallback if unavailable.
    if (!window.Terminal) {
      await loadScripts([
        "https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js",
        "https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.min.css",
      ]);
    }
    if (!window.Terminal) {
      document.getElementById("term-mount").innerHTML = `<div class="empty-state"><p>Terminal emulator could not be loaded (no network). The API is ready — connect with any WebSocket client to /api/terminal.</p></div>`;
      return;
    }
    const term = new window.Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: '"JetBrains Mono", ui-monospace, Menlo, monospace',
      theme: {
        background: "#05070a", foreground: "#d7e0e8",
        cursor: "#c6f14e", cursorAccent: "#05070a",
        selectionBackground: "#33415c",
        black: "#05070a", red: "#ff6161", green: "#a8d75a", yellow: "#f2b64b",
        blue: "#6fb3f2", magenta: "#c79bf2", cyan: "#4ee6c2", white: "#d7e0e8",
      },
      convertEol: true,
    });
    term.open(document.getElementById("term-mount"));

    let ws;
    const connect = () => {
      const proto = location.protocol === "https:" ? "wss" : "ws";
      ws = new WebSocket(`${proto}://${location.host}/api/terminal?token=${encodeURIComponent(api.token)}`);
      term.reset();
      ws.onopen = () => term.write("\r\n\x1b[38;2;198;241;78mAegis terminal connected\x1b[0m — session as " + u.username + "\r\n");
      ws.onmessage = (e) => term.write(e.data);
      ws.onclose = () => term.write("\r\n\x1b[31mconnection closed\x1b[0m\r\n");
      ws.onerror = () => term.write("\r\n\x1b[31mwebsocket error — reconnect?\x1b[0m\r\n");
    };
    term.onData((data) => {
      if (ws && ws.readyState === 1) ws.send(data);
    });
    term.onResize(({ cols, rows }) => {
      if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: "resize", cols, rows }));
    });
    term.focus();
    connect();
    window.addEventListener("beforeunload", () => { try { ws.close(); } catch {} });
  },
});

async function loadScripts(urls) {
  for (const u of urls) {
    if (u.endsWith(".css")) {
      const l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = u;
      document.head.appendChild(l);
    } else {
      await new Promise((res) => {
        const s = document.createElement("script");
        s.src = u;
        s.onload = res;
        s.onerror = res;
        document.head.appendChild(s);
      });
    }
  }
}

void toast;
