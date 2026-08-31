import React, { useEffect, useState, useCallback, useMemo } from "react";
import { createRoot } from "react-dom/client";
import { api, getToken, setToken } from "./api.js";
import PolicyEditor from "./PolicyEditor";
import logoLockup from "./assets/ali-mdm-lockup-h.svg";
import logoMark from "./assets/ali-mdm-logo.svg";
import {
  IconDevices, IconGroups, IconEnroll, IconPackage, IconSignOut, IconChevronLeft,
  IconRefresh, IconPlus, IconTrash, IconEdit, IconPower, IconLock, IconUnlock,
  IconEject, IconCopy, IconCheck, IconUpload, IconInfo, IconWarning,
  IconUser, IconUsers, IconKey, IconSave, IconShield,
} from "./icons.jsx";

// Compact "time ago" so the Last seen column stays narrow; the cell keeps the
// full timestamp in its title attribute.
function timeAgo(iso) {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return null;
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
  if (secs < 60) return secs + "s ago";
  const mins = Math.round(secs / 60);
  if (mins < 60) return mins + "m ago";
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return hrs + "h ago";
  return Math.round(hrs / 24) + "d ago";
}

let toastTimer;
function toast(msg, ok = true) {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = msg;
  el.className = "toast " + (ok ? "ok" : "err");
  el.style.display = "flex";
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (el.style.display = "none"), 3200);
}

/* ── Shared presentational bits ─────────────────────────────────────────── */

function Card({ title, actions, children, flush, footer }) {
  return (
    <div className="card">
      {(title || actions) && (
        <div className="card-header">
          <h3 className="card-title">{title}</h3>
          {actions && <div className="card-toolbar">{actions}</div>}
        </div>
      )}
      <div className={"card-content" + (flush ? " flush" : "")}>{children}</div>
      {footer && <div className="card-footer">{footer}</div>}
    </div>
  );
}

function CardTable({ title, actions, children, note }) {
  return (
    <div className="card">
      <div className="card-header">
        <h3 className="card-title">{title}</h3>
        {actions && <div className="card-toolbar">{actions}</div>}
      </div>
      {note && <div className="card-content" style={{ paddingBottom: 0 }}>{note}</div>}
      <div className="card-table">
        <div className="table-scroll">{children}</div>
      </div>
    </div>
  );
}

function Empty({ icon: Icon = IconInfo, title, desc, action }) {
  return (
    <div className="empty">
      <div className="empty-icon"><Icon /></div>
      <div className="empty-title">{title}</div>
      {desc && <div className="empty-desc">{desc}</div>}
      {action}
    </div>
  );
}

function Alert({ tone = "", icon: Icon = IconInfo, title, children }) {
  return (
    <div className={"alert " + tone}>
      <span className="alert-icon"><Icon /></span>
      <div>
        {title && <div className="alert-title">{title}</div>}
        <div>{children}</div>
      </div>
    </div>
  );
}

function Loading({ label }) {
  return (
    <div className="card">
      <div className="card-content">
        <span className="muted small">{label}</span>
      </div>
    </div>
  );
}

/* ── Login ──────────────────────────────────────────────────────────────── */

function Login({ onLogin }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit(e) {
    e.preventDefault(); setBusy(true); setErr("");
    try { const d = await api.login(email, password); setToken(d.token); onLogin(); }
    catch (e) { setErr(e.message); }
    setBusy(false);
  }

  return (
    <div className="login-wrap">
      <div className="card login-card">
        <form className="card-content" onSubmit={submit}>
          <div className="login-head">
            <img className="login-logo" src={logoMark} alt="" width="48" height="45" />
            <div className="login-title">Sign in to Ali MDM Console</div>
            <div className="login-sub">Operator console for your tablet fleet</div>
          </div>

          <div className="field">
            <label className="form-label" htmlFor="login-email">Email</label>
            <input id="login-email" type="email" placeholder="operator@example.com"
              value={email} onChange={(e) => setEmail(e.target.value)} autoFocus required />
          </div>

          <div className="field">
            <label className="form-label" htmlFor="login-pw">Password</label>
            <input id="login-pw" type="password" placeholder="Enter your password"
              value={password} onChange={(e) => setPassword(e.target.value)} required />
          </div>

          {err && <div className="login-error"><IconWarning style={{ width: 16, height: 16, flexShrink: 0 }} />{err}</div>}

          <button className="btn lg block" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
        </form>
      </div>
    </div>
  );
}

/* ── Groups ─────────────────────────────────────────────────────────────── */

function Groups({ onErr }) {
  const [groups, setGroups] = useState(null);
  const [devices, setDevices] = useState([]);
  const [busy, setBusy] = useState(false);
  const [newId, setNewId] = useState("");
  const [newName, setNewName] = useState("");
  const [copyFrom, setCopyFrom] = useState("");
  const [editing, setEditing] = useState("");   // group id whose policy editor is open
  const [adding, setAdding] = useState(false);

  const load = useCallback(async () => {
    try {
      const [g, d] = await Promise.all([api.listGroups(), api.listDevices()]);
      setGroups(g); setDevices(d);
    } catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); }, [load]);

  const countFor = (gid) => devices.filter((d) => d.group_id === gid).length;

  async function create() {
    if (!newId.trim()) { onErr("Enter a group id"); return; }
    setBusy(true);
    try {
      const body = { id: newId.trim(), name: newName.trim() || newId.trim() };
      if (copyFrom) body.copy_from = copyFrom;
      await api.createGroup(body);
      toast(`Created group "${newId.trim()}"`);
      setNewId(""); setNewName(""); setCopyFrom(""); setAdding(false);
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function remove(g) {
    if (g.id === "default") { onErr("The default group can't be deleted"); return; }
    if (!confirm(`Delete group "${g.name}"? Its ${countFor(g.id)} device(s) will move to "default".`)) return;
    setBusy(true);
    try { await api.deleteGroup(g.id); toast(`Deleted group "${g.name}"`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  if (!groups) return <Loading label="Loading groups…" />;

  return (
    <div className="stack">
      <CardTable
        title="Policy groups"
        actions={<>
          <button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>
          <button className="btn sm" onClick={() => setAdding((v) => !v)}><IconPlus />New group</button>
        </>}
        note={
          <Alert>
            A group is one policy — apps, kiosk mode, lockdown — applied to every device in it.
            Create a group, give it a policy, then move devices into it from the Devices page.
          </Alert>
        }
      >
        <table>
          <thead>
            <tr>
              <th>Group</th><th>ID</th><th>Devices</th><th>Config version</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((g) => (
              <tr key={g.id}>
                <td><span className="strong">{g.name}</span></td>
                <td className="mono muted">{g.id}</td>
                <td><span className="badge off">{countFor(g.id)}</span></td>
                <td className="subtle">v{g.config_version}</td>
                <td>
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    <button className="btn outline sm" disabled={busy}
                      onClick={() => setEditing(editing === g.id ? "" : g.id)}>
                      <IconEdit />{editing === g.id ? "Close editor" : "Edit policy"}
                    </button>
                    {g.id !== "default" && (
                      <button className="btn danger-outline sm" disabled={busy} onClick={() => remove(g)}>
                        <IconTrash />Delete
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
            {groups.length === 0 && (
              <tr><td colSpan="5" style={{ padding: 0 }}>
                <Empty icon={IconGroups} title="No groups yet"
                  desc="Create your first policy group to start managing devices." />
              </td></tr>
            )}
          </tbody>
        </table>
      </CardTable>

      {adding && (
        <Card
          title="New group"
          actions={<button className="btn outline sm" onClick={() => setAdding(false)}>Cancel</button>}
          footer={<>
            <button className="btn" disabled={busy} onClick={create}>
              <IconPlus />{busy ? "Creating…" : "Create group"}
            </button>
            <span className="muted small">“Start from” copies an existing group's policy so you can tweak it.</span>
          </>}
        >
          <div className="field-grid">
            <div className="form-row">
              <label className="form-label" htmlFor="g-id">Group ID</label>
              <div className="form-control">
                <input id="g-id" placeholder="e.g. classroom-a" value={newId}
                  onChange={(e) => setNewId(e.target.value)} style={{ maxWidth: 320 }} />
              </div>
            </div>
            <div className="form-row">
              <label className="form-label" htmlFor="g-name">Display name</label>
              <div className="form-control">
                <input id="g-name" placeholder="optional" value={newName}
                  onChange={(e) => setNewName(e.target.value)} style={{ maxWidth: 320 }} />
              </div>
            </div>
            <div className="form-row">
              <label className="form-label" htmlFor="g-copy">Start from</label>
              <div className="form-control">
                <select id="g-copy" value={copyFrom} onChange={(e) => setCopyFrom(e.target.value)}
                  style={{ maxWidth: 320 }}>
                  <option value="">Empty policy</option>
                  {groups.map((g) => <option key={g.id} value={g.id}>{g.name} ({g.id})</option>)}
                </select>
              </div>
            </div>
          </div>
        </Card>
      )}

      {editing && <PolicyEditor groupId={editing} onErr={onErr} onClose={() => setEditing("")} />}
    </div>
  );
}

/* ── Devices ────────────────────────────────────────────────────────────── */

function Devices({ onErr }) {
  const [devices, setDevices] = useState(null);
  const [groups, setGroups] = useState([]);
  const [busy, setBusy] = useState(false);
  const [q, setQ] = useState("");

  const load = useCallback(async () => {
    try {
      const [d, g] = await Promise.all([api.listDevices(), api.listGroups()]);
      setDevices(d); setGroups(g);
    } catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);

  async function cmd(id, type) {
    if (!confirm(`Send "${type}" to ${id}?`)) return;
    setBusy(true);
    try { await api.sendCommand(id, type); toast(`Sent ${type} to ${id}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // Releasing Device Owner needs the tablet to be reachable: only the app can
  // surrender it, so this is queued as a command rather than done server-side.
  // Unlike unenrolling, it cannot be done for a device that is gone.
  async function releaseOwner(d) {
    if (!d.online && !confirm(
      `${d.name || d.id} is offline.\n\n` +
      "Only the app itself can surrender Device Owner, so this command sits queued " +
      "until the tablet checks in. If it never does, nothing happens.\n\nQueue it anyway?",
    )) return;
    if (!confirm(
      `Release Device Owner on ${d.name || d.id}?\n\n` +
      "The tablet leaves kiosk mode and loses lockdown: no app whitelist, no " +
      "navigation blocking, no factory-reset protection.\n\n" +
      "This is one-way. Nothing on the device can grant it back — restoring " +
      "management needs physical ADB access to the tablet.",
    )) return;
    setBusy(true);
    try {
      await api.sendCommand(d.id, "release_device_owner");
      toast(`Queued Device Owner release for ${d.name || d.id}`);
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function unenroll(id) {
    if (!confirm(
      `Unenroll ${id}?\n\n` +
      "This removes the device here and revokes its key. It does not need the " +
      "tablet to be reachable, so it works for one that is lost or broken.\n\n" +
      "If the tablet ever checks in again it will be rejected and will wipe its " +
      "own cloud settings — but it stays locked down locally until Device Owner " +
      "is removed on the device itself.",
    )) return;
    setBusy(true);
    try { await api.unenroll(id); toast(`Unenrolled ${id}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function moveGroup(id, newGroup) {
    setBusy(true);
    try { await api.moveDeviceGroup(id, newGroup); toast(`Moved ${id} to ${newGroup}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // Commit a label edit. Skips the round-trip when nothing changed so that
  // simply tabbing through the field does not spam the API on every 10s reload.
  async function renameDevice(id, name, previous) {
    const next = name.trim().slice(0, 64);
    if (next === (previous || "")) return;
    setBusy(true);
    try { await api.renameDevice(id, next); toast(next ? `Renamed ${id} to "${next}"` : `Cleared label on ${id}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const filtered = useMemo(() => {
    if (!devices) return [];
    const s = q.trim().toLowerCase();
    if (!s) return devices;
    return devices.filter((d) =>
      [d.id, d.model, d.android_ver, d.group_id].some((v) => (v || "").toLowerCase().includes(s)));
  }, [devices, q]);

  if (!devices) return <Loading label="Loading devices…" />;
  const online = devices.filter((d) => d.online).length;

  return (
    <div className="stack">
      <div className="stat-grid">
        <div className="stat">
          <div className="stat-head">
            <div className="stat-num">{devices.length}</div>
            <span className="stat-icon"><IconDevices /></span>
          </div>
          <div className="stat-lbl">Total devices</div>
        </div>
        <div className="stat">
          <div className="stat-head">
            <div className="stat-num">{online}</div>
            <span className="stat-icon ok"><IconCheck /></span>
          </div>
          <div className="stat-lbl">Online now</div>
        </div>
        <div className="stat">
          <div className="stat-head">
            <div className="stat-num">{devices.length - online}</div>
            <span className="stat-icon warn"><IconWarning /></span>
          </div>
          <div className="stat-lbl">Offline</div>
        </div>
        <div className="stat">
          <div className="stat-head">
            <div className="stat-num">{groups.length}</div>
            <span className="stat-icon"><IconGroups /></span>
          </div>
          <div className="stat-lbl">Policy groups</div>
        </div>
      </div>

      <CardTable
        title="Devices"
        actions={<>
          <input placeholder="Search devices…" value={q} onChange={(e) => setQ(e.target.value)}
            style={{ width: 200, height: 34 }} />
          <button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>
        </>}
      >
        <table>
          <thead>
            <tr>
              <th>Device</th><th>Status</th><th>Battery</th><th>Android</th>
              <th>Last seen</th><th>Group</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((d) => (
              <tr key={d.id}>
                <td className="nowrap">
                  <input
                    defaultValue={d.name || ""}
                    placeholder="Add a label…"
                    maxLength={64}
                    disabled={busy}
                    title="Shown in the corner of this tablet's kiosk screen"
                    style={{ width: 180, height: 28, marginBottom: 4 }}
                    onBlur={(e) => renameDevice(d.id, e.target.value, d.name)}
                    onKeyDown={(e) => { if (e.key === "Enter") e.target.blur(); }}
                  />
                  <div className="mono small subtle">{d.id}</div>
                  {d.model && <div className="small muted">{d.model}</div>}
                </td>
                <td>
                  <span className={"badge " + (d.online ? "on" : "off")}>
                    <span className="dot" />{d.online ? "Online" : "Offline"}
                  </span>
                </td>
                <td className="nowrap">{d.battery != null ? d.battery + "%" : <span className="muted">—</span>}</td>
                <td className="nowrap">{d.android_ver || <span className="muted">—</span>}</td>
                <td className="small subtle nowrap"
                  title={d.last_seen ? new Date(d.last_seen).toLocaleString() : ""}>
                  {d.last_seen ? timeAgo(d.last_seen) : <span className="muted">Never</span>}
                </td>
                <td>
                  <select value={d.group_id || ""} onChange={(e) => moveGroup(d.id, e.target.value)}
                    style={{ minWidth: 140, maxWidth: 180 }}>
                    {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                  </select>
                </td>
                <td>
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    <button className="btn outline sm icon" title="Reboot" disabled={busy}
                      onClick={() => cmd(d.id, "reboot")}><IconPower /></button>
                    <button className="btn outline sm icon" title="Lock" disabled={busy}
                      onClick={() => cmd(d.id, "lock")}><IconLock /></button>
                    <button className="btn outline sm icon" title="Unlock" disabled={busy}
                      onClick={() => cmd(d.id, "unlock")}><IconUnlock /></button>
                    <button className="btn danger-outline sm icon" title="Release Device Owner (needs the tablet online)"
                      disabled={busy} onClick={() => releaseOwner(d)}><IconShield /></button>
                    <button className="btn danger-outline sm icon" title="Unenroll" disabled={busy}
                      onClick={() => unenroll(d.id)}><IconEject /></button>
                  </div>
                </td>
              </tr>
            ))}
            {filtered.length === 0 && (
              <tr><td colSpan="7" style={{ padding: 0 }}>
                <Empty
                  icon={IconDevices}
                  title={devices.length === 0 ? "No devices enrolled yet" : "No devices match that search"}
                  desc={devices.length === 0
                    ? "Enroll a tablet over ADB and it will appear here within a few seconds."
                    : "Try a different device id, model, or Android version."} />
              </td></tr>
            )}
          </tbody>
        </table>
      </CardTable>
    </div>
  );
}

/* ── App update (agent OTA) ─────────────────────────────────────────────── */

const AGENT_STATUS_BADGE = {
  queued: "off",
  installing: "off",
  success: "on",
  failed: "off",
};

function AppUpdate({ onErr }) {
  const [release, setRelease] = useState(null);
  const [updates, setUpdates] = useState([]);
  const [devices, setDevices] = useState([]);
  const [busy, setBusy] = useState(false);
  const [versionCode, setVersionCode] = useState("");
  const [versionName, setVersionName] = useState("");
  const [file, setFile] = useState(null);
  const [selected, setSelected] = useState({});

  const load = useCallback(async () => {
    try {
      const [u, d] = await Promise.all([api.listAgentUpdates(), api.listDevices()]);
      setUpdates(u); setDevices(d);
    } catch (e) { onErr(e.message); }
    // A missing release is the normal empty state, not an error worth surfacing.
    try { setRelease(await api.getAgentRelease()); } catch { setRelease(null); }
  }, [onErr]);
  // Poll while a rollout is in flight: a device that is mid-install reports
  // only on its next heartbeat, so the table would otherwise look frozen.
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);

  async function upload() {
    if (!file) { onErr("Choose an APK first"); return; }
    if (!/^\d+$/.test(versionCode.trim())) { onErr("Version code must be a whole number"); return; }
    setBusy(true);
    try {
      await api.uploadAgentRelease(file, versionCode.trim(), versionName.trim());
      toast("Agent build uploaded");
      setFile(null); setVersionCode(""); setVersionName("");
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function rollout(ids) {
    const label = ids.length ? `${ids.length} device(s)` : "all devices";
    if (!confirm(
      `Push Ali MDM ${release.version_name || ""} (versionCode ${release.version_code}) to ${label}?\n\n` +
      "Each tablet downloads the build, verifies it, then restarts into the new version. " +
      "The app is briefly unavailable while it installs.",
    )) return;
    setBusy(true);
    try {
      const r = await api.rolloutAgent(ids);
      toast(`Queued ${r.queued}/${r.targets} device(s) for versionCode ${r.version_code}`);
      setSelected({});
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const byDevice = useMemo(
    () => Object.fromEntries(updates.map((u) => [u.device_id, u])), [updates]);
  const selectedIds = Object.keys(selected);

  return (
    <div className="stack">
      <CardTable
        title="Current build"
        actions={<button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>}
      >
        <div style={{ padding: 16 }}>
          {release ? (
            <div className="flex" style={{ gap: 24, flexWrap: "wrap", marginBottom: 16 }}>
              <div><div className="small subtle">Version</div>
                <div className="mono strong">{release.version_name || "—"} ({release.version_code})</div></div>
              <div><div className="small subtle">File</div>
                <div className="mono small">{release.file_name}</div></div>
              <div><div className="small subtle">Size</div>
                <div>{(release.size / 1024 / 1024).toFixed(1)} MB</div></div>
              <div><div className="small subtle">SHA-256</div>
                <div className="mono small muted">{release.sha256.slice(0, 16)}…</div></div>
            </div>
          ) : (
            <p className="small subtle" style={{ marginTop: 0 }}>
              No agent build uploaded yet. Upload one below to enable over-the-air updates.
            </p>
          )}

          <div className="flex" style={{ gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
            <div>
              <label className="small subtle" htmlFor="agent-file">APK</label>
              <input id="agent-file" type="file" accept=".apk" disabled={busy}
                onChange={(e) => setFile(e.target.files[0] || null)} style={{ display: "block", width: 240 }} />
            </div>
            <div>
              <label className="small subtle" htmlFor="agent-vc">Version code</label>
              <input id="agent-vc" value={versionCode} placeholder="46" disabled={busy}
                onChange={(e) => setVersionCode(e.target.value)}
                style={{ display: "block", width: 110, height: 34 }} />
            </div>
            <div>
              <label className="small subtle" htmlFor="agent-vn">Version name</label>
              <input id="agent-vn" value={versionName} placeholder="1.2.21" disabled={busy}
                onChange={(e) => setVersionName(e.target.value)}
                style={{ display: "block", width: 130, height: 34 }} />
            </div>
            <button className="btn" onClick={upload} disabled={busy}>
              <IconUpload />{busy ? "Uploading…" : "Upload build"}
            </button>
          </div>
          <p className="small subtle">
            Version code must match the <span className="mono">versionCode</span> inside the APK — tablets
            refuse a build whose version does not match what the rollout promised.
          </p>
        </div>
      </CardTable>

      {release && (
        <CardTable
          title="Rollout"
          actions={<>
            <button className="btn outline sm" disabled={busy || selectedIds.length === 0}
              onClick={() => rollout(selectedIds)}>Update selected ({selectedIds.length})</button>
            <button className="btn sm" disabled={busy} onClick={() => rollout([])}>Update all</button>
          </>}
        >
          <table>
            <thead>
              <tr>
                <th style={{ width: 32 }} />
                <th>Device</th><th>Status</th><th>Attempts</th><th>Target</th><th>Last result</th>
              </tr>
            </thead>
            <tbody>
              {devices.map((d) => {
                const u = byDevice[d.id];
                const upToDate = u && u.status === "success";
                return (
                  <tr key={d.id}>
                    <td>
                      <input type="checkbox" checked={!!selected[d.id]}
                        onChange={() => setSelected((p) => {
                          const n = { ...p }; n[d.id] ? delete n[d.id] : (n[d.id] = true); return n;
                        })} />
                    </td>
                    <td className="nowrap">
                      <div className="strong">{d.name || d.id}</div>
                      {d.name && <div className="mono small subtle">{d.id}</div>}
                    </td>
                    <td>
                      {u ? (
                        <span className={"badge " + (AGENT_STATUS_BADGE[u.status] || "off")}>
                          <span className="dot" />{u.status}
                        </span>
                      ) : <span className="muted small">never updated</span>}
                    </td>
                    <td className="nowrap">{u ? u.attempts : <span className="muted">—</span>}</td>
                    <td className="nowrap mono small">
                      {u ? u.target_version_code : <span className="muted">—</span>}
                      {upToDate && release.version_code === u.target_version_code &&
                        <span className="small subtle"> (current)</span>}
                    </td>
                    <td className="small muted" title={u ? u.last_error : ""}>
                      {u && u.last_error ? u.last_error.slice(0, 60) : "—"}
                    </td>
                  </tr>
                );
              })}
              {devices.length === 0 && (
                <tr><td colSpan="6" style={{ padding: 0 }}>
                  <Empty icon={IconDevices} title="No devices enrolled"
                    desc="Enroll a tablet before pushing an app update." />
                </td></tr>
              )}
            </tbody>
          </table>
        </CardTable>
      )}
    </div>
  );
}

/* ── APKs ───────────────────────────────────────────────────────────────── */

function APKs({ onErr }) {
  const [apks, setApks] = useState(null);
  const [devices, setDevices] = useState([]);
  const [busy, setBusy] = useState(false);
  const [installName, setInstallName] = useState("");
  const [installPkg, setInstallPkg] = useState("");
  const [selected, setSelected] = useState({});
  const [selectAll, setSelectAll] = useState(false);

  const load = useCallback(async () => {
    try {
      const [a, d] = await Promise.all([api.listAPKs(), api.listDevices()]);
      setApks(a); setDevices(d);
    } catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); }, [load]);

  async function upload(e) {
    const file = e.target.files[0];
    if (!file) return;
    setBusy(true);
    try { await api.uploadAPK(file, file.name); toast("APK uploaded"); load(); }
    catch (err) { onErr(err.message); }
    setBusy(false); e.target.value = "";
  }

  function openPicker(apkName) {
    setInstallName(apkName); setInstallPkg(""); setSelected({}); setSelectAll(false);
  }
  async function removeAPK(name) {
    if (!confirm(`Delete ${name} from the server?\n\nTablets that already installed it keep it — this only removes the server copy and stops future installs.`)) return;
    setBusy(true);
    try {
      await api.deleteAPK(name);
      toast(`Deleted ${name}`);
      // Close the install picker if it was targeting the APK just removed.
      if (installName === name) setInstallName("");
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }
  function toggleDevice(id) {
    setSelected((prev) => { const n = { ...prev }; n[id] ? delete n[id] : (n[id] = true); return n; });
  }
  function toggleAll() {
    const next = !selectAll;
    setSelectAll(next);
    setSelected(next ? Object.fromEntries(devices.map((d) => [d.id, true])) : {});
  }
  async function doInstall() {
    if (!installName || !installPkg) { onErr("Enter the package name first"); return; }
    const ids = Object.keys(selected);
    if (ids.length === 0) { onErr("Select at least one device"); return; }
    setBusy(true);
    try {
      const r = await api.installAPK(installName, { package_name: installPkg, devices: ids });
      toast(`Queued ${r.queued}/${r.targets} device(s) to install ${installPkg}`);
      setInstallName(""); setInstallPkg(""); setSelected({}); setSelectAll(false);
    } catch (err) { onErr(err.message); }
    setBusy(false);
  }

  if (!apks) return <Loading label="Loading packages…" />;

  return (
    <div className="stack">
      <CardTable
        title="App packages"
        actions={
          <label className="btn sm" style={{ position: "relative", overflow: "hidden" }}>
            <IconUpload />{busy ? "Uploading…" : "Upload APK"}
            <input type="file" accept=".apk" onChange={upload} disabled={busy}
              style={{ position: "absolute", inset: 0, opacity: 0, cursor: "pointer", height: "100%" }} />
          </label>
        }
        note={
          <Alert>
            Upload an APK, then queue a silent install to the devices you pick. Devices download and
            install it on their next poll, and the whitelist locks it afterwards.
          </Alert>
        }
      >
        <table>
          <thead>
            <tr><th>File</th><th>Size</th><th>SHA-256</th><th style={{ textAlign: "right" }}>Actions</th></tr>
          </thead>
          <tbody>
            {apks.map((a) => (
              <tr key={a.name}>
                <td className="mono strong">{a.name}</td>
                <td className="nowrap">{(a.size / 1024 / 1024).toFixed(1)} MB</td>
                <td className="mono small muted">{a.sha256.slice(0, 16)}…</td>
                <td>
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    {installName === a.name ? (
                      <button className="btn outline sm" onClick={() => setInstallName("")}>Close</button>
                    ) : (
                      <button className="btn outline sm" onClick={() => openPicker(a.name)}>
                        <IconPackage />Install to devices
                      </button>
                    )}
                    <button className="btn danger-outline sm icon" title="Delete from server"
                      disabled={busy} onClick={() => removeAPK(a.name)}><IconTrash /></button>
                  </div>
                </td>
              </tr>
            ))}
            {apks.length === 0 && (
              <tr><td colSpan="4" style={{ padding: 0 }}>
                <Empty icon={IconPackage} title="No APKs uploaded"
                  desc="Upload an APK to push it out to your enrolled tablets." />
              </td></tr>
            )}
          </tbody>
        </table>
      </CardTable>

      {installName && (
        <Card
          title={<>Install <span className="mono">{installName}</span></>}
          actions={<button className="btn outline sm" onClick={() => setInstallName("")}>Cancel</button>}
          footer={<>
            <button className="btn" onClick={doInstall} disabled={busy}>
              {busy ? "Queuing…" : "Queue install"}
            </button>
            <span className="muted small">{Object.keys(selected).length} device(s) selected</span>
          </>}
        >
          <div className="field-grid">
            <div className="form-row">
              <label className="form-label" htmlFor="pkg">Package name</label>
              <div className="form-control">
                <input id="pkg" value={installPkg} onChange={(e) => setInstallPkg(e.target.value)}
                  placeholder="com.example.app" style={{ maxWidth: 360 }} />
              </div>
            </div>

            <div className="form-row" style={{ alignItems: "flex-start" }}>
              <label className="form-label">Target devices</label>
              <div className="form-control" style={{ flexDirection: "column", alignItems: "stretch" }}>
                {devices.length === 0 ? (
                  <Alert tone="warning" icon={IconWarning}>
                    No enrolled devices yet. Enroll a tablet first, then it will appear here.
                  </Alert>
                ) : (
                  <>
                    <label className="flex small subtle" style={{ cursor: "pointer", marginBottom: 6 }}>
                      <input type="checkbox" checked={selectAll} onChange={toggleAll} />
                      Select all ({devices.length})
                    </label>
                    <div className="scroll-box">
                      {devices.map((d) => (
                        <label key={d.id} className="flex" style={{ padding: "6px 6px", cursor: "pointer" }}>
                          <input type="checkbox" checked={!!selected[d.id]} onChange={() => toggleDevice(d.id)} />
                          <span className="mono small" style={{ minWidth: 160 }}>{d.name || d.id}</span>
                          <span className="small muted grow">{d.model || ""}</span>
                          <span className={"badge sm " + (d.online ? "on" : "off")}>
                            <span className="dot" />{d.online ? "Online" : "Offline"}
                          </span>
                        </label>
                      ))}
                    </div>
                  </>
                )}
              </div>
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}

/* ── Enroll ─────────────────────────────────────────────────────────────── */

function Enroll({ onErr }) {
  const cloud = window.location.origin;
  const [copied, setCopied] = useState("");

  function copy(text, key) {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(key);
      setTimeout(() => setCopied(""), 1500);
    }).catch(() => onErr("Copy failed — select the text manually"));
  }

  const steps = [
    { title: "Factory-reset the tablet",
      body: "Settings → System → Reset → Erase all data. Let it reboot into the Android setup wizard." },
    { title: "Complete basic setup",
      body: "Choose language, connect to Wi-Fi, and get to the home screen. Skip the Google account sign-in if you can." },
    { title: "Enable USB debugging",
      body: "Settings → About tablet → tap “Build number” 7 times to unlock Developer options. Then Settings → System → Developer options → turn on USB debugging." },
    { title: "Connect the tablet to the enrollment machine",
      body: "Plug in a USB cable (or use Wi-Fi debugging). Accept the “Allow USB debugging?” prompt on the tablet." },
    { title: "Run the enrollment command",
      body: "On the enrollment machine, run the command below. It installs Ali MDM, sets it as Device Owner, and enrolls the tablet to the cloud — which then auto-installs the managed apps.",
      code: "cd /path/to/ali-mdm && ./apps/enroll/enroll_tablet.sh <SERIAL>", copyKey: "cmd" },
    { title: "Reboot the tablet",
      body: "After enrollment completes, reboot the tablet. On boot it comes up in the kiosk with the managed apps installed and ready." },
  ];

  return (
    <div className="stack">
      <Card title="Cloud endpoint">
        <div className="field-grid">
          <div className="form-row">
            <label className="form-label" htmlFor="cloud-url">Cloud URL</label>
            <div className="form-control">
              <input id="cloud-url" readOnly value={cloud} style={{ maxWidth: 380 }} />
              <button className="btn outline" onClick={() => copy(cloud, "cloud")}>
                {copied === "cloud" ? <IconCheck /> : <IconCopy />}
                {copied === "cloud" ? "Copied" : "Copy"}
              </button>
            </div>
          </div>
        </div>
      </Card>

      <Card title="Enroll a tablet over ADB">
        <div className="stack">
          <Alert tone="warning" icon={IconWarning} title="QR enrollment is unavailable on Android 14+">
            Google Play Protect blocks sideloading a Device Owner app during the setup wizard. Enroll each
            tablet over ADB instead — a one-time, ~3-minute ritual per device. After that the cloud manages
            it fully: config, app installs, and remote commands.
          </Alert>

          <div className="stack tight">
            {steps.map((s, i) => (
              <div key={s.title} className="flex" style={{ alignItems: "flex-start", gap: 14, padding: "14px 0", borderTop: i === 0 ? "none" : "1px solid var(--border)" }}>
                <span className="avatar" style={{ width: 28, height: 28, fontSize: "0.75rem" }}>{i + 1}</span>
                <div className="grow" style={{ minWidth: 0 }}>
                  <div className="strong" style={{ fontSize: "0.875rem", marginBottom: 4 }}>{s.title}</div>
                  <div className="small subtle" style={{ lineHeight: 1.6 }}>{s.body}</div>
                  {s.code && (
                    <div className="flex" style={{ marginTop: 10, alignItems: "stretch" }}>
                      <code className="code grow">{s.code}</code>
                      <button className="btn outline" onClick={() => copy(s.code, s.copyKey)}>
                        {copied === s.copyKey ? <IconCheck /> : <IconCopy />}
                        {copied === s.copyKey ? "Copied" : "Copy"}
                      </button>
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      </Card>

      <Card title="Notes">
        <ul className="list-check">
          <li><span><b>USB is more reliable than Wi-Fi ADB</b> for the enrollment step — Wi-Fi ADB pairing does not survive a reboot.</span></li>
          <li><span>The <span className="mono">enroll_tablet.sh</span> script lives on the enrollment machine and reads the enroll token and cloud URL from its config — you only supply the tablet serial.</span></li>
          <li><span>Each tablet enrolls independently; the same cloud and group config applies to all of them.</span></li>
          <li><span><b>One-click helper (coming soon):</b> a Windows/macOS enrollment app will automate steps 4–6. Until then, the command above is all you need.</span></li>
        </ul>
      </Card>
    </div>
  );
}

/* ── Profile (own account) ──────────────────────────────────────────────── */

function RoleBadge({ role }) {
  return role === "admin"
    ? <span className="badge info">Administrator</span>
    : <span className="badge off">Operator</span>;
}

function Profile({ me, onErr, onMeChange }) {
  const [name, setName] = useState(me.name || "");
  const [email, setEmail] = useState(me.email || "");
  const [savingProfile, setSavingProfile] = useState(false);

  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [savingPw, setSavingPw] = useState(false);

  const profileDirty = name !== (me.name || "") || email !== (me.email || "");

  async function saveProfile(e) {
    e.preventDefault();
    setSavingProfile(true);
    try {
      const r = await api.updateMe({ name, email });
      // Changing your own email re-subjects the session token.
      if (r.token) setToken(r.token);
      onMeChange(r.user);
      toast("Profile updated");
    } catch (err) { onErr(err.message); }
    setSavingProfile(false);
  }

  async function savePassword(e) {
    e.preventDefault();
    if (next !== confirm) { onErr("The new passwords don't match"); return; }
    if (next.length < 8) { onErr("New password must be at least 8 characters"); return; }
    setSavingPw(true);
    try {
      await api.changePassword(current, next);
      setCurrent(""); setNext(""); setConfirm("");
      toast("Password changed");
    } catch (err) { onErr(err.message); }
    setSavingPw(false);
  }

  return (
    <div className="stack">
      <div className="card">
        <div className="card-header">
          <h3 className="card-title">Profile</h3>
          <div className="card-toolbar"><RoleBadge role={me.role} /></div>
        </div>
        <form onSubmit={saveProfile}>
          <div className="card-content">
            <div className="field-grid">
              <div className="form-row">
                <label className="form-label" htmlFor="p-name">Display name</label>
                <div className="form-control">
                  <input id="p-name" value={name} onChange={(e) => setName(e.target.value)}
                    placeholder="Your name" style={{ maxWidth: 320 }} />
                </div>
              </div>
              <div className="form-row">
                <label className="form-label" htmlFor="p-email">Email</label>
                <div className="form-control">
                  <input id="p-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)}
                    required style={{ maxWidth: 320 }} />
                  <div className="form-desc">You sign in with this address.</div>
                </div>
              </div>
              <div className="form-row">
                <label className="form-label">Role</label>
                <div className="form-control">
                  <RoleBadge role={me.role} />
                  <div className="form-desc">
                    {me.role === "admin"
                      ? "You can manage devices, policies, and user accounts."
                      : "You can manage devices, policies, and packages. Only administrators manage user accounts."}
                  </div>
                </div>
              </div>
            </div>
          </div>
          <div className="card-footer">
            <button className="btn" disabled={savingProfile || !profileDirty}>
              <IconSave />{savingProfile ? "Saving…" : "Save changes"}
            </button>
          </div>
        </form>
      </div>

      <div className="card">
        <div className="card-header"><h3 className="card-title">Password</h3></div>
        <form onSubmit={savePassword}>
          <div className="card-content">
            <div className="field-grid">
              <div className="form-row">
                <label className="form-label" htmlFor="p-cur">Current password</label>
                <div className="form-control">
                  <input id="p-cur" type="password" value={current} autoComplete="current-password"
                    onChange={(e) => setCurrent(e.target.value)} required style={{ maxWidth: 320 }} />
                </div>
              </div>
              <div className="form-row">
                <label className="form-label" htmlFor="p-new">New password</label>
                <div className="form-control">
                  <input id="p-new" type="password" value={next} autoComplete="new-password"
                    onChange={(e) => setNext(e.target.value)} required style={{ maxWidth: 320 }} />
                  <div className="form-desc">At least 8 characters.</div>
                </div>
              </div>
              <div className="form-row">
                <label className="form-label" htmlFor="p-conf">Confirm new password</label>
                <div className="form-control">
                  <input id="p-conf" type="password" value={confirm} autoComplete="new-password"
                    onChange={(e) => setConfirm(e.target.value)} required style={{ maxWidth: 320 }} />
                </div>
              </div>
            </div>
          </div>
          <div className="card-footer">
            <button className="btn" disabled={savingPw || !current || !next}>
              <IconKey />{savingPw ? "Changing…" : "Change password"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

/* ── Users (admin only) ─────────────────────────────────────────────────── */

const BLANK_USER = { email: "", name: "", role: "operator", password: "" };

function Users({ me, onErr, onMeChange }) {
  const [users, setUsers] = useState(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState(BLANK_USER);
  const [editing, setEditing] = useState(null);   // { email, name, role, original }
  const [resetting, setResetting] = useState(null); // { email, password }

  const load = useCallback(async () => {
    try { setUsers(await api.listUsers()); }
    catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); }, [load]);

  const isSelf = (email) => email.toLowerCase() === (me.email || "").toLowerCase();
  const adminCount = (users || []).filter((u) => u.role === "admin").length;

  async function create(e) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.createUser(draft);
      toast(`Created ${draft.email}`);
      setDraft(BLANK_USER); setAdding(false);
      load();
    } catch (err) { onErr(err.message); }
    setBusy(false);
  }

  async function saveEdit(e) {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api.updateUser(editing.original, {
        email: editing.email, name: editing.name, role: editing.role,
      });
      // Renaming your own account re-subjects the session token.
      if (r.token) { setToken(r.token); onMeChange(r.user); }
      else if (isSelf(editing.original)) onMeChange(r.user);
      toast(`Updated ${editing.email}`);
      setEditing(null);
      load();
    } catch (err) { onErr(err.message); }
    setBusy(false);
  }

  async function saveReset(e) {
    e.preventDefault();
    if (resetting.password.length < 8) { onErr("Password must be at least 8 characters"); return; }
    setBusy(true);
    try {
      await api.resetUserPassword(resetting.email, resetting.password);
      toast(`Password reset for ${resetting.email}`);
      setResetting(null);
    } catch (err) { onErr(err.message); }
    setBusy(false);
  }

  async function remove(u) {
    if (!confirm(`Delete ${u.email}? They will lose access immediately.`)) return;
    setBusy(true);
    try { await api.deleteUser(u.email); toast(`Deleted ${u.email}`); load(); }
    catch (err) { onErr(err.message); }
    setBusy(false);
  }

  if (!users) return <Loading label="Loading users…" />;

  return (
    <div className="stack">
      <CardTable
        title="User accounts"
        actions={<>
          <button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>
          <button className="btn sm" onClick={() => { setAdding((v) => !v); setEditing(null); setResetting(null); }}>
            <IconPlus />Add user
          </button>
        </>}
        note={
          <Alert>
            Administrators manage user accounts as well as devices. Operators can do
            everything except add, modify, or remove users.
          </Alert>
        }
      >
        <table>
          <thead>
            <tr>
              <th>User</th><th>Role</th><th>Added</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => {
              const self = isSelf(u.email);
              const lastAdmin = u.role === "admin" && adminCount <= 1;
              return (
                <tr key={u.email}>
                  <td>
                    <div className="flex" style={{ gap: 10 }}>
                      <span className="avatar">{(u.name || u.email).slice(0, 1)}</span>
                      <span>
                        <span className="strong" style={{ display: "block" }}>
                          {u.name || u.email}
                          {self && <span className="badge sm" style={{ marginLeft: 8 }}>You</span>}
                        </span>
                        {u.name && <span className="small muted">{u.email}</span>}
                      </span>
                    </div>
                  </td>
                  <td><RoleBadge role={u.role} /></td>
                  <td className="small subtle nowrap">
                    {u.created_at ? new Date(u.created_at).toLocaleDateString() : <span className="muted">—</span>}
                  </td>
                  <td>
                    <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                      <button className="btn outline sm" disabled={busy}
                        onClick={() => { setEditing({ ...u, original: u.email }); setAdding(false); setResetting(null); }}>
                        <IconEdit />Edit
                      </button>
                      <button className="btn outline sm" disabled={busy}
                        onClick={() => { setResetting({ email: u.email, password: "" }); setAdding(false); setEditing(null); }}>
                        <IconKey />Reset password
                      </button>
                      <button className="btn danger-outline sm icon" disabled={busy || self || lastAdmin}
                        title={self ? "You cannot delete your own account"
                          : lastAdmin ? "The last administrator cannot be deleted"
                          : "Delete user"}
                        onClick={() => remove(u)}>
                        <IconTrash />
                      </button>
                    </div>
                  </td>
                </tr>
              );
            })}
            {users.length === 0 && (
              <tr><td colSpan="4" style={{ padding: 0 }}>
                <Empty icon={IconUsers} title="No users yet" desc="Add an account to give someone access." />
              </td></tr>
            )}
          </tbody>
        </table>
      </CardTable>

      {adding && (
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">Add user</h3>
            <div className="card-toolbar">
              <button className="btn outline sm" onClick={() => setAdding(false)}>Cancel</button>
            </div>
          </div>
          <form onSubmit={create}>
            <div className="card-content">
              <div className="field-grid">
                <div className="form-row">
                  <label className="form-label" htmlFor="n-email">Email</label>
                  <div className="form-control">
                    <input id="n-email" type="email" required value={draft.email}
                      onChange={(e) => setDraft({ ...draft, email: e.target.value })}
                      placeholder="person@example.com" style={{ maxWidth: 320 }} />
                  </div>
                </div>
                <div className="form-row">
                  <label className="form-label" htmlFor="n-name">Display name</label>
                  <div className="form-control">
                    <input id="n-name" value={draft.name} placeholder="optional"
                      onChange={(e) => setDraft({ ...draft, name: e.target.value })} style={{ maxWidth: 320 }} />
                  </div>
                </div>
                <div className="form-row">
                  <label className="form-label">Role</label>
                  <div className="form-control">
                    <div className="pill-group">
                      {["operator", "admin"].map((r) => (
                        <button key={r} type="button"
                          className={"pill" + (draft.role === r ? " active" : "")}
                          onClick={() => setDraft({ ...draft, role: r })}>
                          {r === "admin" ? "Administrator" : "Operator"}
                        </button>
                      ))}
                    </div>
                    <div className="form-desc">
                      Administrators can also add, modify, and remove user accounts.
                    </div>
                  </div>
                </div>
                <div className="form-row">
                  <label className="form-label" htmlFor="n-pw">Password</label>
                  <div className="form-control">
                    <input id="n-pw" type="password" required value={draft.password} autoComplete="new-password"
                      onChange={(e) => setDraft({ ...draft, password: e.target.value })} style={{ maxWidth: 320 }} />
                    <div className="form-desc">At least 8 characters. Share it with them directly.</div>
                  </div>
                </div>
              </div>
            </div>
            <div className="card-footer">
              <button className="btn" disabled={busy}><IconPlus />{busy ? "Creating…" : "Create user"}</button>
            </div>
          </form>
        </div>
      )}

      {editing && (
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">Edit <span className="mono">{editing.original}</span></h3>
            <div className="card-toolbar">
              <button className="btn outline sm" onClick={() => setEditing(null)}>Cancel</button>
            </div>
          </div>
          <form onSubmit={saveEdit}>
            <div className="card-content">
              <div className="field-grid">
                <div className="form-row">
                  <label className="form-label" htmlFor="e-email">Email</label>
                  <div className="form-control">
                    <input id="e-email" type="email" required value={editing.email}
                      onChange={(e) => setEditing({ ...editing, email: e.target.value })} style={{ maxWidth: 320 }} />
                  </div>
                </div>
                <div className="form-row">
                  <label className="form-label" htmlFor="e-name">Display name</label>
                  <div className="form-control">
                    <input id="e-name" value={editing.name || ""}
                      onChange={(e) => setEditing({ ...editing, name: e.target.value })} style={{ maxWidth: 320 }} />
                  </div>
                </div>
                <div className="form-row">
                  <label className="form-label">Role</label>
                  <div className="form-control">
                    <div className="pill-group">
                      {["operator", "admin"].map((r) => (
                        <button key={r} type="button"
                          className={"pill" + (editing.role === r ? " active" : "")}
                          onClick={() => setEditing({ ...editing, role: r })}>
                          {r === "admin" ? "Administrator" : "Operator"}
                        </button>
                      ))}
                    </div>
                    {isSelf(editing.original) && editing.role !== "admin" && (
                      <div className="form-desc">
                        Removing your own administrator role will end your access to this page.
                      </div>
                    )}
                  </div>
                </div>
              </div>
            </div>
            <div className="card-footer">
              <button className="btn" disabled={busy}><IconSave />{busy ? "Saving…" : "Save changes"}</button>
            </div>
          </form>
        </div>
      )}

      {resetting && (
        <div className="card">
          <div className="card-header">
            <h3 className="card-title">Reset password for <span className="mono">{resetting.email}</span></h3>
            <div className="card-toolbar">
              <button className="btn outline sm" onClick={() => setResetting(null)}>Cancel</button>
            </div>
          </div>
          <form onSubmit={saveReset}>
            <div className="card-content">
              <div className="form-row">
                <label className="form-label" htmlFor="r-pw">New password</label>
                <div className="form-control">
                  <input id="r-pw" type="password" required value={resetting.password} autoComplete="new-password"
                    onChange={(e) => setResetting({ ...resetting, password: e.target.value })}
                    style={{ maxWidth: 320 }} />
                  <div className="form-desc">
                    At least 8 characters. They stay signed in until their session expires.
                  </div>
                </div>
              </div>
            </div>
            <div className="card-footer">
              <button className="btn" disabled={busy}><IconKey />{busy ? "Resetting…" : "Reset password"}</button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}

/* ── Shell ──────────────────────────────────────────────────────────────── */

const NAV = [
  { id: "devices", icon: IconDevices, txt: "Devices", title: "Devices", group: "Manage",
    desc: "Every enrolled tablet, its live status, and remote controls" },
  { id: "groups", icon: IconGroups, txt: "Groups", title: "Policy groups", group: "Manage",
    desc: "Define one policy and apply it to a whole set of devices" },
  { id: "enroll", icon: IconEnroll, txt: "Enroll", title: "Enroll a tablet", group: "Manage",
    desc: "Bring a new device under management over ADB" },
  { id: "apks", icon: IconPackage, txt: "Packages", title: "App packages", group: "Manage",
    desc: "Upload APKs and push silent installs to your fleet" },
  { id: "appupdate", icon: IconUpload, txt: "App update", title: "Ali MDM app update", group: "Manage",
    desc: "Push a new build of Ali MDM itself to your tablets over the air" },
  { id: "profile", icon: IconUser, txt: "Profile", title: "Your profile", group: "Account",
    desc: "Update your details and change your password" },
  { id: "users", icon: IconUsers, txt: "Users", title: "User accounts", group: "Account",
    adminOnly: true, desc: "Who can sign in to this console, and what they may do" },
];

function Shell({ onSignOut }) {
  const [view, setView] = useState("devices");
  const [collapsed, setCollapsed] = useState(false);
  const [me, setMe] = useState(null);
  const onErr = useCallback((m) => toast(m, false), []);

  // The signed-in account comes from the server rather than the token so the
  // role shown here is the live one.
  useEffect(() => {
    api.me().then(setMe).catch((e) => {
      if (e.status === 401) onSignOut();
      else onErr(e.message);
    });
  }, [onErr, onSignOut]);

  const nav = useMemo(
    () => NAV.filter((n) => !n.adminOnly || (me && me.role === "admin")),
    [me]);

  // An admin who demotes themselves loses the Users page under their feet.
  useEffect(() => {
    if (me && !nav.some((n) => n.id === view)) setView("devices");
  }, [nav, view, me]);

  useEffect(() => { window.scrollTo(0, 0); }, [view]);

  if (!me) {
    return (
      <div className="login-wrap">
        <span className="muted small">Loading console…</span>
      </div>
    );
  }

  const active = nav.find((n) => n.id === view) || nav[0];
  const groups = [...new Set(nav.map((n) => n.group))];

  return (
    <div className={"app" + (collapsed ? " collapsed" : "")}>
      <aside className="sidebar">
        <div className="sidebar-header">
          <div className="brand">
            <img className="brand-logo-full" src={logoLockup} alt="Ali MDM" width="150" height="50" />
            <img className="brand-logo-mini" src={logoMark} alt="Ali MDM" width="32" height="30" />
          </div>
          <button className="sidebar-toggle" onClick={() => setCollapsed(!collapsed)}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}>
            <IconChevronLeft />
          </button>
        </div>

        <nav className="sidebar-content">
          {groups.map((g) => (
            <React.Fragment key={g}>
              <div className="menu-heading">{g}</div>
              {nav.filter((n) => n.group === g).map((n) => {
                const Icon = n.icon;
                return (
                  <button key={n.id} className={"navbtn" + (view === n.id ? " active" : "")}
                    onClick={() => setView(n.id)} title={n.txt}>
                    <span className="navbtn-icon"><Icon /></span>
                    <span className="navbtn-title">{n.txt}</span>
                  </button>
                );
              })}
            </React.Fragment>
          ))}
        </nav>

        <div className="sidebar-footer">
          <div className="sidebar-user">
            <button className="avatar" title="Your profile" aria-label="Your profile"
              style={{ cursor: "pointer" }} onClick={() => setView("profile")}>
              {(me.name || me.email || "?").slice(0, 1)}
            </button>
            <span className="sidebar-user-meta">
              <span className="sidebar-user-name">{me.name || me.email}</span>
              <span className="sidebar-user-role">
                {me.role === "admin" ? "Administrator" : "Operator"}
              </span>
            </span>
            <button className="sidebar-signout" title="Sign out" aria-label="Sign out"
              onClick={onSignOut}>
              <IconSignOut />
            </button>
          </div>
        </div>
      </aside>

      <div className="wrapper">
        <header className="header">
          <div className="container-fixed header-inner">
            <div className="header-title">{active.title}</div>
            <div className="header-actions">
              <span className="badge off"><span className="dot" style={{ background: "var(--green-500)" }} />Connected</span>
            </div>
          </div>
        </header>

        <main className="content">
          <div className="container-fixed">
            <div className="toolbar">
              <div className="toolbar-heading">
                <h1 className="toolbar-title">{active.title}</h1>
                <div className="toolbar-desc">{active.desc}</div>
              </div>
            </div>

            {view === "devices" && <Devices onErr={onErr} />}
            {view === "groups" && <Groups onErr={onErr} />}
            {view === "enroll" && <Enroll onErr={onErr} />}
            {view === "apks" && <APKs onErr={onErr} />}
            {view === "appupdate" && <AppUpdate onErr={onErr} />}
            {view === "profile" && <Profile me={me} onErr={onErr} onMeChange={setMe} />}
            {view === "users" && me.role === "admin" && <Users me={me} onErr={onErr} onMeChange={setMe} />}
          </div>
        </main>
      </div>
    </div>
  );
}

function App() {
  const [authed, setAuthed] = useState(!!getToken());
  const signOut = useCallback(() => { setToken(""); setAuthed(false); }, []);
  return (
    <>
      {authed ? <Shell onSignOut={signOut} /> : <Login onLogin={() => setAuthed(true)} />}
      <div id="toast" className="toast" style={{ display: "none" }} />
    </>
  );
}

createRoot(document.getElementById("root")).render(<App />);
