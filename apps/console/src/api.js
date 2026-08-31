// Thin API client for the Ali MDM Console.
const TOKEN_KEY = "***";

export function getToken() { return localStorage.getItem(TOKEN_KEY) || ""; }
export function setToken(t) { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY); }

async function req(method, path, body) {
  const headers = { "Content-Type": "application/json" };
  const tok = getToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  const res = await fetch("/api/v1" + path, {
    method, headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data = null;
  if (text) { try { data = JSON.parse(text); } catch { data = text; } }
  if (!res.ok) {
    const msg = data && typeof data === "object" && data.error ? data.error : (typeof data === "string" ? data : res.status);
    const err = new Error(msg); err.status = res.status; throw err;
  }
  return data;
}

export const api = {
  login: (email, password) => req("POST", "/operator/login", { email, password }),
  logout: () => setToken(""),
  listDevices: () => req("GET", "/devices"),
  getDevice: (id) => req("GET", "/devices/" + id),
  sendCommand: (id, type, params = {}) => req("POST", "/devices/" + id + "/commands", { type, params }),
  // Forced unenrolment: deletes the device server-side, revoking its API key.
  // Works without the tablet — a dead or lost device never has to cooperate.
  unenroll: (id) => req("DELETE", "/devices/" + encodeURIComponent(id)),
  listGroups: () => req("GET", "/groups"),
  getGroup: (id) => req("GET", "/groups/" + id),
  updateGroup: (id, body) => req("PUT", "/groups/" + id, body),
  createGroup: (body) => req("POST", "/groups", body),
  deleteGroup: (id) => req("DELETE", "/groups/" + id),
  moveDeviceGroup: (id, group_id) => req("POST", "/devices/" + id + "/group", { group_id }),
  renameDevice: (id, name) => req("POST", "/devices/" + id + "/name", { name }),
  listAPKs: () => req("GET", "/apks"),
  // Setup-wizard provisioning payload. Operator-only: it carries the enrolment token.
  provisionQR: (query) => req("GET", "/provision/qr" + (query ? "?" + query : "")),

  // ── Own profile ──
  me: () => req("GET", "/me"),
  updateMe: (body) => req("PUT", "/me", body),
  changePassword: (current_password, new_password) =>
    req("POST", "/me/password", { current_password, new_password }),

  // ── User administration (admin role only) ──
  listUsers: () => req("GET", "/users"),
  createUser: (body) => req("POST", "/users", body),
  updateUser: (email, body) => req("PUT", "/users/" + encodeURIComponent(email), body),
  deleteUser: (email) => req("DELETE", "/users/" + encodeURIComponent(email)),
  resetUserPassword: (email, new_password) =>
    req("POST", "/users/" + encodeURIComponent(email) + "/password", { new_password }),
  installAPK: (name, body) => req("POST", `/apks/${encodeURIComponent(name)}/install`, body),
  deleteAPK: (name) => req("DELETE", `/apks/${encodeURIComponent(name)}`),

  // ── Agent (Ali MDM itself) OTA — separate from the managed-app catalogue ──
  getAgentRelease: () => req("GET", "/agent/release"),
  listAgentUpdates: () => req("GET", "/agent/updates"),
  rolloutAgent: (devices) => req("POST", "/agent/rollout", { devices }),
  // Multipart, so it bypasses req() (which JSON-encodes and would set the
  // wrong Content-Type) exactly like uploadAPK above.
  uploadAgentRelease: (file, versionCode, versionName) => {
    const tok = getToken();
    const fd = new FormData();
    fd.append("file", file);
    fd.append("version_code", String(versionCode));
    fd.append("version_name", versionName || "");
    return fetch("/api/v1/agent/release", {
      method: "POST",
      headers: tok ? { Authorization: "Bearer " + tok } : {},
      body: fd,
    }).then(async (r) => {
      const t = await r.text();
      let d = null; try { d = JSON.parse(t); } catch { d = t; }
      if (!r.ok) throw new Error(d && d.error ? d.error : (typeof d === "string" && d ? d : r.status));
      return d;
    });
  },
  uploadAPK: (file, name) => {
    const tok = getToken();
    const fd = new FormData();
    fd.append("file", file);
    if (name) fd.append("name", name);
    return fetch("/api/v1/apks", {
      method: "POST",
      headers: tok ? { Authorization: "Bearer " + tok } : {},
      body: fd,
    }).then(async (r) => {
      const t = await r.text();
      let d = null; try { d = JSON.parse(t); } catch { d = t; }
      if (!r.ok) throw new Error(d && d.error ? d.error : r.status);
      return d;
    });
  },
};
