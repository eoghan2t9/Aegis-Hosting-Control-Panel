// Panel base path. Injected by the server into index.html when the panel is
// served under a prefix (default "/aegis"); empty when served from the root.
const BASE = window.AEGIS_PANEL_BASE || "";

/**
 * Prefix a server path with the panel base: p("/api/users") -> "/aegis/api/users".
 * Pass through absolute URLs and data:/blob: URIs untouched.
 */
export function p(path) {
  if (!BASE || /^(https?:|data:|blob:)/i.test(path)) return path;
  return BASE + path;
}

export default p;
