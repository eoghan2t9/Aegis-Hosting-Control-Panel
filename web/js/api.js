// Thin JSON API client. Stores the JWT in memory + localStorage for reloads.
import { p } from "./base.js";

const TOKEN_KEY = "aegis.token";

export const api = {
  token: localStorage.getItem(TOKEN_KEY) || "",

  async request(method, path, body, raw) {
    const headers = {};
    if (this.token) headers.Authorization = "Bearer " + this.token;
    let payload = body;
    if (body !== undefined && !(body instanceof FormData)) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    }
    const res = await fetch(p("/api") + path, { method, headers, body: payload ?? (body instanceof FormData ? body : undefined) });
    if (res.status === 401) {
      this.token = "";
      localStorage.removeItem(TOKEN_KEY);
      window.dispatchEvent(new CustomEvent("aegis:logout"));
      throw Object.assign(new Error("Session expired — please log in again"), { status: 401 });
    }
    if (raw) return res;
    let data = null;
    const text = await res.text();
    if (text) { try { data = JSON.parse(text); } catch { data = text; } }
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText || "Request failed";
      throw Object.assign(new Error(msg), { status: res.status });
    }
    return data;
  },

  get: (p, raw) => api.request("GET", p, undefined, raw),
  post: (p, b) => api.request("POST", p, b ?? {}),
  patch: (p, b) => api.request("PATCH", p, b ?? {}),
  put: (p, b) => api.request("PUT", p, b ?? {}),
  del: (p) => api.request("DELETE", p),

  setToken(t) {
    this.token = t;
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  },
};

export function qs(obj) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined && v !== null && v !== "") p.set(k, v);
  }
  const s = p.toString();
  return s ? "?" + s : "";
}
