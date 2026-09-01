// Thin API client for the Ali MDM Console.

/** Finds an 0xFF <marker> pair, used to locate JPEG frame boundaries. */
function indexOfMarker(buf, marker, from) {
  for (let i = from; i < buf.length - 1; i++) {
    if (buf[i] === 0xff && buf[i + 1] === marker) return i;
  }
  return -1;
}
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
  listEvents: (limit = 50) => req("GET", "/events?limit=" + limit),
  markEventsRead: (upTo) => req("POST", "/events/read", { up_to: upTo }),
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

  // ── Server-wide alerting (admin role only) ──
  getAlertSettings: () => req("GET", "/settings/alerts"),
  updateAlertSettings: (body) => req("PUT", "/settings/alerts", body),
  testAlertWebhook: () => req("POST", "/settings/alerts/test"),

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
  // Version is read from the APK server-side; nothing to pass but the file.
  uploadAgentRelease: (file) => {
    const tok = getToken();
    const fd = new FormData();
    fd.append("file", file);
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

  // ── Live view ──────────────────────────────────────────────────────────────
  startStream: (id) => req("POST", "/devices/" + encodeURIComponent(id) + "/stream/start", {}),
  stopStream: (id) => req("POST", "/devices/" + encodeURIComponent(id) + "/stream/stop", {}),
  streamStatus: (id) => req("GET", "/devices/" + encodeURIComponent(id) + "/stream/status"),
  /**
   * Open the MJPEG stream and hand each JPEG to onFrame as a Blob.
   *
   * Read with fetch rather than pointed at with <img src>, because an <img>
   * cannot send an Authorization header — the alternative would be a token in
   * the URL, which ends up in logs and browser history.
   */
  openStream: (id, onFrame, onError, signal) => {
    const tok = getToken();
    return fetch("/api/v1/devices/" + encodeURIComponent(id) + "/stream.mjpeg", {
      headers: tok ? { Authorization: "Bearer " + tok } : {},
      signal,
    }).then(async (res) => {
      if (!res.ok) throw new Error("Stream failed: HTTP " + res.status);
      const reader = res.body.getReader();
      // Frames are found by scanning for JPEG start/end markers rather than by
      // parsing MIME boundaries: it is fewer moving parts, and a boundary can
      // straddle two chunks whereas the markers are self-synchronising.
      let buf = new Uint8Array(0);
      for (;;) {
        const { done, value } = await reader.read();
        if (done) return;
        const next = new Uint8Array(buf.length + value.length);
        next.set(buf); next.set(value, buf.length);
        buf = next;

        for (;;) {
          const start = indexOfMarker(buf, 0xd8, 0);
          if (start < 0) break;
          const end = indexOfMarker(buf, 0xd9, start + 2);
          if (end < 0) break;
          onFrame(new Blob([buf.slice(start, end + 2)], { type: "image/jpeg" }));
          buf = buf.slice(end + 2);
        }
        // A frame that never completes must not grow without bound.
        if (buf.length > 8 * 1024 * 1024) buf = new Uint8Array(0);
      }
    }).catch((e) => {
      if (e.name !== "AbortError") onError(e);
    });
  },

  // ── Per-device file manager ────────────────────────────────────────────────
  deviceInbox: (id) => req("GET", "/devices/" + encodeURIComponent(id) + "/inbox"),
  refreshDeviceInbox: (id) => req("POST", "/devices/" + encodeURIComponent(id) + "/inbox/refresh", {}),
  deleteDeviceFile: (id, name) =>
    req("DELETE", "/devices/" + encodeURIComponent(id) + "/inbox/" + encodeURIComponent(name)),

  // ── File library (console → device inbox) ──────────────────────────────────
  listLibraryFiles: () => req("GET", "/files"),
  deleteLibraryFile: (name) => req("DELETE", "/files/" + encodeURIComponent(name)),
  pushLibraryFile: (name, body) => req("POST", "/files/" + encodeURIComponent(name) + "/push", body),
  fileDeliveries: (name) => req("GET", "/files/" + encodeURIComponent(name) + "/deliveries"),
  uploadLibraryFile: (file, name) => {
    const tok = getToken();
    const fd = new FormData();
    fd.append("file", file);
    if (name) fd.append("name", name);
    return fetch("/api/v1/files", {
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
