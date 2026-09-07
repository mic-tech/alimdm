import React, { useEffect, useState, useCallback, useMemo, useRef } from "react";
import { createRoot } from "react-dom/client";
import { api, getToken, setToken } from "./api.js";
import PolicyEditor from "./PolicyEditor";
import { defaultPolicyConfig } from "./policySchema";
import QRCode from "qrcode";
import logoLockup from "./assets/ali-mdm-lockup-h.svg";
import logoMark from "./assets/ali-mdm-logo.svg";
import {
  IconDevices, IconGroups, IconEnroll, IconPackage, IconSignOut, IconChevronLeft, IconBell,
  IconRefresh, IconPlus, IconTrash, IconEdit, IconPower, IconLock, IconUnlock,
  IconEject, IconCopy, IconCheck, IconUpload, IconInfo, IconWarning,
  IconUser, IconUsers, IconKey, IconSave, IconShield, IconFile, IconEye, IconRows, IconGrid,
  IconSliders, IconMonitor, IconAndroid, IconFolder, IconChevronRight,
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
          {title && <h3 className="card-title">{title}</h3>}
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
        {title && <h3 className="card-title">{title}</h3>}
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
            <div className="login-sub">Operator console for your device fleet</div>
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

  // Re-deliver an unchanged policy. Saving only reaches devices the console
  // thinks are behind, which after the first delivery is none of them, so this
  // is the only way to correct a tablet whose settings have drifted. Confirmed
  // because it touches every device in the group at once.
  async function resend(g) {
    const n = countFor(g.id);
    if (!confirm(
      `Re-apply the "${g.name}" policy to ${n === 1 ? "1 device" : `all ${n} devices`}?\n\n` +
      "Each one is sent the whole policy again on its next check-in, within about 30 seconds, " +
      "and any setting changed on the device itself goes back to what the policy says.",
    )) return;
    setBusy(true);
    try {
      const r = await api.resendGroupConfig(g.id);
      const sent = r?.resent ?? 0;
      toast(sent === 1
        ? "The policy will be sent again to 1 device"
        : `The policy will be sent again to ${sent} devices`);
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function create() {
    if (!newId.trim()) { onErr("Enter a group id"); return; }
    setBusy(true);
    try {
      const body = { id: newId.trim(), name: newName.trim() || newId.trim() };
      // "Start from" is a deliberate choice to inherit another group's policy;
      // otherwise the group starts at the app's own defaults rather than at
      // nothing, so every device moved into it lands in the same known state.
      if (copyFrom) body.copy_from = copyFrom;
      else body.config = JSON.stringify(defaultPolicyConfig());
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
        <table className="stacked">
          <thead>
            <tr>
              <th>Group</th><th>ID</th><th>Devices</th><th>Config version</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((g) => (
              <tr key={g.id}>
                <td data-label="Group"><span className="strong">{g.name}</span></td>
                <td className="mono muted" data-label="ID">{g.id}</td>
                <td data-label="Devices"><span className="badge off">{countFor(g.id)}</span></td>
                <td className="subtle" data-label="Config version">v{g.config_version}</td>
                <td className="cell-actions">
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    <button className="btn outline sm" disabled={busy}
                      onClick={() => setEditing(editing === g.id ? "" : g.id)}>
                      <IconEdit />{editing === g.id ? "Close editor" : "Edit policy"}
                    </button>
                    <button className="btn outline sm" disabled={busy || countFor(g.id) === 0}
                      onClick={() => resend(g)}
                      title={countFor(g.id) === 0
                        ? "No devices in this group"
                        : "Send this policy again to every device in the group, on each one's next check-in"}>
                      <IconRefresh />Re-apply to all
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
            <span className="muted small">
              “Start from” copies an existing group’s policy so you can tweak it. Without it the
              group starts locked down — kiosk on, Back ignored, PIN <span className="mono">1234</span>
              — so check the policy before moving devices in.
            </span>
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

/* The tablet is behind NAT, so this cannot query it: the console asks by
   command and shows the last answer with its age. A stale listing labelled
   "checked 4 minutes ago" is more use than a spinner that never resolves
   because the tablet is in a cupboard. */
/* Live view. Frames travel tablet → server → here, because nothing can open a
   connection to a tablet behind school NAT. The tablet only learns it is being
   watched on its next heartbeat, so the first frame can be up to 30 seconds
   away; the panel says so rather than showing a spinner that looks broken. */
function LiveView({ device, onErr }) {
  const imgRef = useRef(null);
  const [state, setState] = useState("starting");
  const [frames, setFrames] = useState(0);
  const lastUrl = useRef(null);
  // Control is opt-in per session: watching is the common case, and a stray
  // click on a picture of a classroom should not press anything.
  const [control, setControl] = useState(false);
  const [typing, setTyping] = useState("");

  const send = useCallback(async (event) => {
    try { await api.sendInput(device.id, event); }
    catch (e) { onErr(e.message); }
  }, [device.id, onErr]);

  // A click on the picture becomes a tap on the device. The frame is letterboxed
  // inside the img, so the fraction is measured against the rendered image
  // rather than the element — otherwise every tap lands short of where it looked.
  const tap = (e) => {
    if (!control) return;
    const img = e.currentTarget;
    const box = img.getBoundingClientRect();
    const nat = img.naturalWidth / img.naturalHeight;
    const shown = box.width / box.height;
    let w = box.width, h = box.height, left = 0, top = 0;
    if (nat > shown) { h = box.width / nat; top = (box.height - h) / 2; }
    else { w = box.height * nat; left = (box.width - w) / 2; }
    const x = (e.clientX - box.left - left) / w;
    const y = (e.clientY - box.top - top) / h;
    if (x < 0 || x > 1 || y < 0 || y > 1) return;
    send({ type: "tap", x: Number(x.toFixed(4)), y: Number(y.toFixed(4)) });
  };

  useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    let framesSeen = 0;

    api.startStream(device.id).catch((e) => onErr(e.message));

    api.openStream(
      device.id,
      (blob) => {
        if (cancelled) return;
        framesSeen++;
        setFrames(framesSeen);
        setState("live");
        const url = URL.createObjectURL(blob);
        if (imgRef.current) imgRef.current.src = url;
        // Revoking the previous URL only after the next one is set avoids a
        // flash of nothing between frames.
        if (lastUrl.current) URL.revokeObjectURL(lastUrl.current);
        lastUrl.current = url;
      },
      (e) => { if (!cancelled) { setState("error"); onErr(e.message); } },
      controller.signal,
    );

    return () => {
      cancelled = true;
      controller.abort();
      if (lastUrl.current) URL.revokeObjectURL(lastUrl.current);
      // Tell the tablet to stop now rather than waiting for the lease to lapse.
      api.stopStream(device.id).catch(() => {/* the lease expires anyway */});
    };
  }, [device.id, onErr]);

  return (
    <>
      <div className="flex" style={{ marginBottom: 10 }}>
        <span className="view-switch" role="group" aria-label="Watch or control">
          <button className={!control ? "on" : ""} aria-pressed={!control}
            onClick={() => setControl(false)}><IconEye />Watch</button>
          <button className={control ? "on" : ""} aria-pressed={control}
            onClick={() => setControl(true)}
            title="Click the screen to tap it. The device shows “Remotely controlled” while you do.">
            <IconSliders />Control
          </button>
        </span>
        {control && (
          <>
            <button className="btn outline sm" onClick={() => send({ type: "key", key: "back" })}>Back</button>
            <button className="btn outline sm" onClick={() => send({ type: "key", key: "home" })}>Home</button>
            {/* Arrows and Enter move around a screen and commit what is on it —
                between them and a tap, most of a kiosk can be driven. Grouped
                like keys on a keyboard rather than spread along the toolbar. */}
            <span className="keypad" role="group" aria-label="Arrow keys">
              {[["left", "←"], ["up", "↑"], ["down", "↓"], ["right", "→"]].map(([key, glyph]) => (
                <button key={key} className="btn outline sm icon" aria-label={key}
                  title={key} onClick={() => send({ type: "key", key })}>{glyph}</button>
              ))}
            </span>
            {/* Scrolling on a tablet is a finger, not a wheel: these send a drag
                across the middle of the screen. Named for what you want to see,
                so Scroll down shows what is further down the page. */}
            <span className="keypad" role="group" aria-label="Scroll">
              <button className="btn outline sm" title="Scroll up — swipes down on the device"
                onClick={() => send({ type: "scroll", dir: "up" })}>Scroll ↑</button>
              <button className="btn outline sm" title="Scroll down — swipes up on the device"
                onClick={() => send({ type: "scroll", dir: "down" })}>Scroll ↓</button>
            </span>
            <button className="btn outline sm" title="Send the Enter key on its own"
              onClick={() => send({ type: "key", key: "enter" })}>Enter</button>
            <input className="grow" placeholder="Type into the device…" value={typing}
              style={{ minWidth: 120 }}
              onChange={(e) => setTyping(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== "Enter" || !typing) return;
                send({ type: "text", text: typing });
                setTyping("");
              }} />
            <button className="btn outline sm" disabled={!typing}
              onClick={() => { send({ type: "text", text: typing }); setTyping(""); }}>Send</button>
          </>
        )}
      </div>
      <div className="screen-frame">
        <img ref={imgRef} alt="Live view of the device's screen" onClick={tap}
          style={{ maxWidth: "100%", maxHeight: 460, display: state === "live" ? "block" : "none",
            cursor: control ? "crosshair" : "default" }} />
        {state !== "live" && (
          <span className="muted small" style={{ padding: 40, textAlign: "center" }}>
            {state === "error" ? "The stream stopped."
              : "Waiting for the first frame — the device starts on its next check-in, up to 30s."}
          </span>
        )}
      </div>
      <div className="muted small" style={{ marginTop: 10 }}>
        {state === "live" && <>{frames} frame{frames === 1 ? "" : "s"}. </>}
        {control
          ? "Click the screen to tap it. Taps reach the device on its next frame, and it shows “Remotely controlled” while you are driving it. "
          : "Smooth while the kiosk is on screen. Inside another app Android limits capture to about one frame a second, so control there feels slow. "}
        Screenshot protection is lifted for the length of the session — so stop this when you
        are done.
      </div>
    </>
  );
}

/* The card layout is the phone layout, offered at any width. A class on <html>
   drives it from one place so the CSS has a single copy of the rules. */
function useNarrowFlag() {
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 48rem)");
    const apply = () => document.documentElement.classList.toggle("is-narrow", mq.matches);
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, []);
}

/* A still of the tablet's screen, refreshed on a timer.
   Asking and fetching are separate steps because they happen on different
   clocks: the request reaches the tablet on its next check-in, so the image
   that answers it arrives later. The previous still stays on screen until the
   new one lands, and carries its age, rather than blanking between refreshes. */
function DeviceSnapshot({ deviceId, online, intervalMs, big, nonce = 0 }) {
  const imgRef = useRef(null);
  const urlRef = useRef(null);
  const [state, setState] = useState("waiting");
  const [takenAt, setTakenAt] = useState(null);
  // Read inside the polling loop, where the state value would be the one
  // captured when the effect ran rather than the current one.
  const takenAtRef = useRef(null);
  // Bumped to ask for a fresh still outside the timer.
  const [manual, setManual] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    const show = async () => {
      try {
        const got = await api.fetchSnapshot(deviceId, controller.signal);
        if (cancelled || !got) return;
        const url = URL.createObjectURL(got.blob);
        if (imgRef.current) imgRef.current.src = url;
        if (urlRef.current) URL.revokeObjectURL(urlRef.current);
        urlRef.current = url;
        takenAtRef.current = got.at;
        setTakenAt(got.at);
        setState("shown");
      } catch (e) {
        if (!cancelled && e.name !== "AbortError") setState("error");
      }
    };

    /* Wait for a still newer than the one already on screen.

       This used to sleep twelve seconds, which was right when a queued command
       took up to a heartbeat to reach the device. A tablet holding the wake
       stream open now acts within a second, so a fixed wait spends most of its
       time doing nothing. Poll for the answer instead: fast at first, easing
       off, and giving up well before the next cycle so the two never overlap. */
    const awaitFresh = async (previousAt) => {
      const deadline = Date.now() + 20000;
      let wait = 300;
      while (!cancelled && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, wait));
        wait = Math.min(wait * 1.5, 2000);
        if (cancelled) return;
        try {
          const meta = await api.snapshotMeta(deviceId, controller.signal);
          if (meta?.has_snapshot && meta.at !== previousAt) {
            await show();
            return;
          }
        } catch (e) {
          if (e.name === "AbortError") return;
          // A failed check is not a failed screenshot; keep waiting.
        }
      }
      // Nothing arrived. The still already on screen stays, with its own
      // timestamp, rather than being replaced by an error.
    };

    const cycle = async () => {
      // Offline tablets cannot answer; keep showing the last still rather than
      // queuing commands that will pile up until they return.
      if (!online) return;
      const before = takenAtRef.current;
      try {
        await api.requestSnapshot(deviceId);
      } catch {
        return; // retried on the next cycle
      }
      awaitFresh(before);
    };

    show();          // whatever is already there, immediately
    cycle();
    // 0 means the operator turned refreshing off: the last still stays, and the
    // card can still be asked for a new one by hand.
    const t = intervalMs > 0 ? setInterval(cycle, intervalMs) : null;
    return () => {
      cancelled = true;
      controller.abort();
      if (t) clearInterval(t);
      if (urlRef.current) URL.revokeObjectURL(urlRef.current);
    };
  }, [deviceId, online, intervalMs, manual, nonce]);

  return (
    <button className={"snap" + (big ? " big" : "")} type="button"
      title={online ? "Ask this device for a fresh screen" : "The device is offline"}
      disabled={!online} onClick={() => setManual((n) => n + 1)}>
      <img ref={imgRef} alt="The device's screen"
        style={{ display: state === "shown" ? "block" : "none" }} />
      {state !== "shown" && (
        <div className="snap-note">
          {online ? "Waiting for the device to send a screen…" : "Offline — no screen to show"}
        </div>
      )}
      {state === "shown" && takenAt && (
        <span className="snap-age">{timeAgo(takenAt) || "just now"}</span>
      )}
    </button>
  );
}

/* How an event's severity reads in the feeds, here and on the Activity page. */
const SEVERITY_LABEL = { info: "Info", warn: "Attention", error: "Failure" };

/* What a command is about to do, for the dialog that asks first. Lock and
   unlock change the device's Lock Mode setting and not just the lock task, so
   they say so — and say how it comes back. */
function commandPrompt(type, who) {
  if (type === "unlock") {
    return `Unlock ${who}?\n\nIt leaves kiosk mode and stays out of it — a pupil can reach other apps and the home screen — until the group's policy is applied to it again.`;
  }
  if (type === "lock") {
    return `Lock ${who}?\n\nIt returns to kiosk mode with the policy's app whitelist.`;
  }
  if (type === "reboot") {
    return `Reboot ${who}?\n\nWhoever is using it loses what is on screen.`;
  }
  if (type === "restart_app") {
    return `Restart Ali MDM on ${who}?\n\nOnly the app closes and reopens — about ten seconds, and the tablet stays on. Whatever is on screen is lost. Worth trying before a reboot.`;
  }
  return `Send "${type}" to ${who}?`;
}

/* What to call a command in something a person reads. "Sent restart_app to
   IQRA Tab 1" is the wire talking; a name is what the operator just clicked. */
const COMMAND_NAMES = {
  screen_on: "wake",
  restart_app: "restart app",
  reboot: "reboot",
  lock: "lock",
  unlock: "unlock",
};
function commandName(type) {
  return COMMAND_NAMES[type] || type;
}

/* What to call a device in something a person reads. The label if it has one,
   the id if nobody has named it — the same rule the device list, the kiosk
   screen and the server's own notifications follow. */
// numeric so track2 sorts before track10, which is the whole point of naming
// them that way.
const byName = (a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" });

function deviceLabel(d) {
  return d.label || d.name || d.id;
}

/* Asked before re-applying a policy, from the row or from the device's page.
   It is worth confirming: any setting someone changed on the tablet itself is
   about to go back to what the policy says. */
function resendPrompt(label) {
  return `Re-apply the policy to ${label}?\n\n` +
    "It is sent the whole policy again on its next check-in, within about 30 seconds, " +
    "and any setting changed on the device itself goes back to what the policy says.";
}

/* ── One device ─────────────────────────────────────────────────────────────

   Everything about a single device in one place: what it is, what its screen
   is showing, what is on it, and what has happened to it.

   The split with the Devices list is the point. The list answers "is the fleet
   all right?" at a glance and stays a status board; anything that needs room —
   a live stream, a file listing, a history that pages, an action you should
   not take by accident — belongs here. */

function DeviceFiles({ device, onErr }) {
  const [data, setData] = useState(null);
  const [busy, setBusy] = useState(false);
  // Closed folders, not open ones. This tab exists to confirm that what was
  // sent actually arrived and kept its shape, so folders start open — and
  // tracking what has been shut keeps a folder that arrives on the next refresh
  // visible instead of hidden.
  const [collapsed, setCollapsed] = useState(() => new Set());

  const load = useCallback(async () => {
    try { setData(await api.deviceInbox(device.id)); }
    catch (e) { onErr(e.message); }
  }, [device.id, onErr]);
  useEffect(() => { load(); }, [load]);
  // The device answers on its own schedule, so keep looking while this is open.
  useEffect(() => { const t = setInterval(load, 5000); return () => clearInterval(t); }, [load]);

  async function refresh() {
    setBusy(true);
    try { await api.refreshDeviceInbox(device.id); toast("Asked the device for its file list"); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function remove(name, relPath) {
    const where = relPath ? `${relPath}/${name}` : name;
    if (!confirm(`Delete ${where} from ${device.label || device.id}?\n\nThis removes it from the device itself.`)) return;
    setBusy(true);
    try { await api.deleteDeviceFile(device.id, name, relPath); toast(`Asked the device to delete ${where}`); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  function toggle(path) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      next.has(path) ? next.delete(path) : next.add(path);
      return next;
    });
  }

  // Memoised so the tree below is rebuilt when the listing changes, not on
  // every render of a tab that polls every five seconds.
  const entries = useMemo(() => data?.entries ?? [], [data]);
  // The device reports each file with the folder it sits in; rebuild the tree so
  // this reads like the tablet's own Files app rather than a flat dump.
  const rows = useMemo(
    () => treeRows(buildFileTree(entries, (f) => f.name), (p) => !collapsed.has(p)),
    [entries, collapsed]);

  return (
    <CardTable
      title="Files"
      actions={<>
        <span className="muted small">
          {!data ? "Loading…"
            : data.fetched_at ? `Checked ${timeAgo(data.fetched_at) || "just now"}`
            : "Never checked"}
        </span>
        <button className="btn outline sm" disabled={busy || !device.online} onClick={refresh}
          title={device.online
            ? "The device answers on its next check-in"
            : "The device is offline; it will answer when it checks in"}>
          <IconRefresh />Ask the device
        </button>
      </>}
    >
      <table className="stacked">
        <thead>
          <tr><th>File</th><th>Size</th><th>Modified</th><th style={{ textAlign: "right" }}>Actions</th></tr>
        </thead>
        <tbody>
          {entries.length === 0 && (
            <tr><td colSpan="4" style={{ padding: 0 }}>
              <Empty icon={IconFile}
                title={!data ? "Loading…" : data.fetched_at ? "Nothing in the inbox folder" : "No listing yet"}
                desc={data && !data.fetched_at
                  ? "Ask the device for its file list and it will answer on its next check-in."
                  : "Send a document from the File library and it lands here, in Download/Ali MDM."} />
            </td></tr>
          )}
          {rows.map((row) => {
            if (row.kind === "folder") {
              const st = folderStats(row.node);
              const open = !collapsed.has(row.node.path);
              return (
                <tr key={row.key}>
                  <td className="small strong" data-label="Folder">
                    <TreeCell depth={row.depth}>
                      <button className="tree-toggle" aria-expanded={open}
                        aria-label={`${open ? "Collapse" : "Expand"} ${row.node.path}`}
                        onClick={() => toggle(row.node.path)}>
                        <IconChevronRight width="16" height="16"
                          className={"tree-caret" + (open ? " is-open" : "")} />
                        <IconFolder width="18" height="18" className="tree-icon" />
                        <span className="tree-name">{row.node.name}</span>
                      </button>
                    </TreeCell>
                  </td>
                  <td className="small nowrap" data-label="Size">
                    {fmtSize(st.size)} <span className="muted">· {st.files} file{st.files === 1 ? "" : "s"}</span>
                  </td>
                  <td className="small muted nowrap" data-label="Modified">—</td>
                  <td className="cell-actions" />
                </tr>
              );
            }
            const f = row.file;
            return (
              <tr key={row.key}>
                <td className="small strong" data-label="File">
                  <TreeCell depth={row.depth}>
                    <span className="tree-caret-gap" />
                    <IconFile width="17" height="17" className="tree-icon" />
                    <span className="tree-name">{f.label}</span>
                  </TreeCell>
                </td>
                <td className="small nowrap" data-label="Size">{fmtSize(f.size)}</td>
                <td className="small muted nowrap" data-label="Modified">
                  {f.modified_at ? timeAgo(new Date(f.modified_at).toISOString()) : "—"}
                </td>
                <td className="cell-actions">
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    <button className="btn danger-outline sm icon" title="Delete from the device"
                      disabled={busy} onClick={() => remove(f.name, f.rel_path)}><IconTrash /></button>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </CardTable>
  );
}

/* This device's own history: the same feed the Activity page shows, narrowed
   to the one device. Every entry the fleet feed holds for it is here — what was
   sent to it, when it went quiet, when it came back — without the noise of the
   other eleven. */
/* What is actually installed on one tablet.

   The policy says what a group should run; this says what one device really
   has. The gap between them is where the awkward problems live — an OEM package
   whose name is not the one the policy guessed, an app left behind after being
   dropped from the whitelist and still taking a gigabyte. Like the Files tab,
   the tablet answers on its own schedule, so the listing is cached and stamped
   with when it was taken. */
function DeviceApps({ device, onErr }) {
  const [data, setData] = useState(null);
  const [busy, setBusy] = useState(false);
  const [q, setQ] = useState("");
  const [showSystem, setShowSystem] = useState(false);

  const load = useCallback(async () => {
    try { setData(await api.deviceApps(device.id)); }
    catch (e) { onErr(e.message); }
  }, [device.id, onErr]);
  useEffect(() => { load(); }, [load]);
  // The device answers on its next check-in, so keep looking while this is
  // open — but slowly. An inventory only changes when someone asks for it or
  // uninstalls something, and the answer is 70KB.
  useEffect(() => { const t = setInterval(load, 20000); return () => clearInterval(t); }, [load]);

  async function refresh() {
    setBusy(true);
    try { await api.refreshDeviceApps(device.id); toast("Asked the device what it has installed"); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function uninstall(app) {
    const label = app.label && app.label !== app.package_name
      ? `${app.label} (${app.package_name})` : app.package_name;
    if (!confirm(`Uninstall ${label} from ${deviceLabel(device)}?\n\n` +
      "The app and its data are removed from the tablet. Anything the policy still " +
      "lists is installed again on a later check-in.")) return;
    setBusy(true);
    try {
      await api.uninstallDeviceApp(device.id, app.package_name);
      toast(`Asked the device to uninstall ${app.package_name}`);
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const entries = useMemo(() => data?.entries ?? [], [data]);
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return entries
      .filter((a) => showSystem || !a.system || a.updated_system_app)
      .filter((a) => !needle ||
        (a.package_name || "").toLowerCase().includes(needle) ||
        (a.label || "").toLowerCase().includes(needle))
      .sort((a, b) => byName(a.label || a.package_name, b.label || b.package_name));
  }, [entries, q, showSystem]);
  const systemCount = entries.filter((a) => a.system && !a.updated_system_app).length;

  return (
    <CardTable
      title="Apps"
      actions={<>
        <input className="search-input" placeholder="Search apps…" value={q}
          onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 200 }} />
        <span className="muted small">
          {!data ? "Loading…"
            : data.fetched_at ? `Checked ${timeAgo(data.fetched_at) || "just now"}`
            : "Never checked"}
        </span>
        <button className="btn outline sm" disabled={busy} onClick={refresh}
          title={device.online
            ? "The device answers on its next check-in"
            : "The device is offline; it will answer when it checks in"}>
          <IconRefresh />Ask the device
        </button>
      </>}
      note={
        <Alert>
          What this tablet reports it has installed. System packages are hidden by
          default and cannot be uninstalled — Device Owner can only roll back an
          update to one, not remove it. Uninstalling an app the policy still lists
          only frees the space until the next check-in reinstalls it.
        </Alert>
      }
    >
      <table className="stacked">
        <thead>
          <tr><th>App</th><th>Version</th><th>Kind</th><th style={{ textAlign: "right" }}>Actions</th></tr>
        </thead>
        <tbody>
          {shown.length === 0 && (
            <tr><td colSpan="4" style={{ padding: 0 }}>
              <Empty icon={IconPackage}
                title={!data ? "Loading…"
                  : !data.fetched_at ? "No inventory yet"
                  : q ? "Nothing matches that"
                  : "Nothing to show"}
                desc={data && !data.fetched_at
                  ? "Ask the device what it has installed and it will answer on its next check-in."
                  : systemCount > 0 && !showSystem
                  ? `${systemCount} system packages are hidden.`
                  : "This device reported no apps."} />
            </td></tr>
          )}
          {shown.map((a) => (
            <tr key={a.package_name}>
              <td className="small strong" data-label="App">
                {a.label || a.package_name}
                <div className="muted small mono">{a.package_name}</div>
              </td>
              <td className="small nowrap" data-label="Version">{a.version_name || "—"}</td>
              <td className="small nowrap" data-label="Kind">
                {a.system && !a.updated_system_app
                  ? <span className="badge off">System</span>
                  : a.updated_system_app
                  ? <span className="badge info">Updated system</span>
                  : <span className="badge on">Installed</span>}
                {a.enabled === false && <> <span className="badge warning">Disabled</span></>}
              </td>
              <td className="cell-actions">
                <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                  {/* Not offered where it cannot work: Device Owner cannot remove
                      a package that shipped with the device, and a button that
                      always fails is worse than no button. */}
                  {a.system && !a.updated_system_app ? (
                    <span className="muted small">Cannot be removed</span>
                  ) : (
                    <button className="btn danger-outline sm icon" title={`Uninstall ${a.package_name}`}
                      disabled={busy} onClick={() => uninstall(a)}><IconTrash /></button>
                  )}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {systemCount > 0 && (
        <div style={{ padding: "10px 20px" }}>
          <button className="linkish small" onClick={() => setShowSystem((v) => !v)}>
            {showSystem ? "Hide" : "Show"} {systemCount} system package{systemCount === 1 ? "" : "s"}
          </button>
        </div>
      )}
    </CardTable>
  );
}

function DeviceHistory({ deviceId, onErr }) {
  const [events, setEvents] = useState(null);
  const [hasMore, setHasMore] = useState(false);
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState(false);
  const PAGE = 25;

  const load = useCallback(async () => {
    try {
      const r = await api.listEventsPage({ limit: PAGE, device: deviceId });
      setEvents(r.events || []);
      setHasMore(!!r.has_more);
      setTotal(r.total || 0);
    } catch (e) { onErr(e.message); }
  }, [deviceId, onErr]);
  useEffect(() => { load(); }, [load]);

  async function loadOlder() {
    if (!events || events.length === 0) return;
    setBusy(true);
    try {
      const r = await api.listEventsPage({
        limit: PAGE, before: events[events.length - 1].id, device: deviceId,
      });
      setEvents((prev) => [...prev, ...(r.events || [])]);
      setHasMore(!!r.has_more);
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  return (
    <div className="stack">
      <CardTable title="History"
        actions={<button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>}>
        <table className="stacked">
          <thead><tr><th>When</th><th>Level</th><th>Who</th><th>What happened</th></tr></thead>
          <tbody>
            {events && events.length === 0 && (
              <tr><td colSpan="4" style={{ padding: 0 }}>
                <Empty icon={IconBell} title="Nothing recorded yet"
                  desc="Commands, renames, and going quiet or coming back are all logged here." />
              </td></tr>
            )}
            {(events || []).map((ev) => (
              <tr key={ev.id}>
                <td className="small subtle nowrap" data-label="When"
                  title={new Date(ev.at).toLocaleString()}>{timeAgo(ev.at) || "just now"}</td>
                <td className="nowrap" data-label="Level">
                  <span className={"badge " + (ev.severity === "error" ? "off"
                    : ev.severity === "warn" ? "warning" : "")}>
                    {SEVERITY_LABEL[ev.severity] || ev.severity}
                  </span>
                </td>
                <td className="small nowrap" data-label="Who">{ev.actor || "—"}</td>
                <td className="small" data-label="What happened">{ev.summary}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </CardTable>
      {events && events.length > 0 && (
        <div className="flex" style={{ justifyContent: "center", gap: 12 }}>
          <span className="small muted">Showing {events.length} of {total}</span>
          {hasMore && (
            <button className="btn outline sm" disabled={busy} onClick={loadOlder}>
              {busy ? "Loading…" : "Load older"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/* What has been queued for this device, and how it went.
   The history says what was asked for; this says what came of it — a command
   still waiting for a device that is not checking in looks exactly like one
   that succeeded, until you can see its status. */
const CMD_BADGE = { pending: "warning", sent: "warning", success: "on", error: "off", failed: "off" };
const CMD_LABEL = { pending: "Waiting", sent: "Sent", success: "Done", error: "Failed", failed: "Failed" };

function DeviceCommands({ deviceId, onErr }) {
  const [cmds, setCmds] = useState(null);
  const load = useCallback(async () => {
    try { setCmds(await api.deviceCommands(deviceId) || []); }
    catch (e) { onErr(e.message); }
  }, [deviceId, onErr]);
  // A queued command is settled by the device on its own schedule, so this
  // would otherwise sit on "Waiting" long after the device had answered.
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);

  return (
    <CardTable title="Commands"
      actions={<button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>}>
      <table className="stacked">
        <thead><tr><th>When</th><th>Command</th><th>Status</th><th>Detail</th></tr></thead>
        <tbody>
          {cmds && cmds.length === 0 && (
            <tr><td colSpan="4" style={{ padding: 0 }}>
              <Empty icon={IconSliders} title="Nothing has been sent to this device"
                desc="Reboot, lock and unlock are on this page; app installs and updates come from Packages and App update." />
            </td></tr>
          )}
          {(cmds || []).map((c) => (
            <tr key={c.id}>
              <td className="small subtle nowrap" data-label="When"
                title={c.created_at ? new Date(c.created_at).toLocaleString() : ""}>
                {c.created_at ? timeAgo(c.created_at) || "just now" : "—"}
              </td>
              <td className="small strong nowrap" data-label="Command">{c.type}</td>
              <td className="nowrap" data-label="Status">
                <span className={"badge " + (CMD_BADGE[c.status] || "")}>
                  {CMD_LABEL[c.status] || c.status}
                </span>
              </td>
              <td className="small muted" data-label="Detail">{c.err_msg || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </CardTable>
  );
}

/* What the tablet says it has been doing.
   The console could see that a device was online and what it had been asked to
   do, and nothing about why any of it failed — every diagnosis so far has meant
   having the tablet in hand. This asks; the device answers on its next
   check-in, so the page waits rather than pretending to be live.

   Administrators only, and the server enforces it: a log is a minute-by-minute
   account of a classroom's tablet, which is more detail about a room than
   everyone who can sign in needs. */
function DeviceLogs({ deviceId, onErr }) {
  const [data, setData] = useState(null);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState("app_log");

  const load = useCallback(async () => {
    try { setData(await api.deviceLogs(deviceId)); }
    catch (e) { onErr(e.message); }
  }, [deviceId, onErr]);
  useEffect(() => { load(); }, [load]);
  // A queued capture is answered on the device's own schedule.
  useEffect(() => {
    if (data?.status !== "pending") return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [data?.status, load]);

  async function fetchLogs() {
    setBusy(true);
    try {
      await api.requestDeviceLogs(deviceId);
      toast("Asked the device for its log");
      await load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const state = data?.state || null;
  const text = tab === "logcat" ? data?.logcat : data?.app_log;

  return (
    <Card title="Diagnostics" actions={<>
      {data?.status === "pending" && <span className="muted small">Waiting for the device…</span>}
      <button className="btn outline sm" disabled={busy} onClick={fetchLogs}>
        <IconRefresh />{data?.status && data.status !== "none" ? "Fetch again" : "Fetch logs"}
      </button>
    </>}>
      {/* How the last screen capture went. Shown before anything is asked of the
          device, because the command history already knows and because a
          capture that failed is usually why this tab is open at all. */}
      {data?.last_capture && (
        data.last_capture.error ? (
          <Alert tone="warning" icon={IconWarning} title="The last screen capture failed">
            {data.last_capture.error}
            <div className="muted small" style={{ marginTop: 4 }}>
              {timeAgo(data.last_capture.at) || "just now"}. Screenshots and live view show
              the Ali MDM kiosk and nothing else until this is resolved.
            </div>
          </Alert>
        ) : (
          <div className="muted small" style={{ marginBottom: 12 }}>
            Last screen capture{" "}
            {data.last_capture.status === "success"
              ? `succeeded ${timeAgo(data.last_capture.at) || "just now"}`
              : `was asked for ${timeAgo(data.last_capture.at) || "just now"} and the device has not answered yet`}.
          </div>
        )
      )}

      {!data || data.status === "none" ? (
        <Empty icon={IconSliders} title="No log captured yet"
          desc="Ask the device for its recent log and the state of the permissions that decide how it behaves. It answers on its next check-in." />
      ) : (
        <div className="stack">
          {data.status === "error" && (
            <Alert tone="warning" icon={IconWarning} title="The device could not collect a log">
              {data.error || "No reason given."}
            </Alert>
          )}
          {state && (
            <dl className="facts">
              <Fact label="Android">{state.android || "—"} on {state.model || "—"}</Fact>
              <Fact label="Storage"><Storage state={state} /></Fact>
              <Fact label="Device Owner"><YesNo ok={state.device_owner} /></Fact>
              {/* Only meaningful while the screen is on — the device says so
                  itself rather than leaving the reader to wonder. */}
              <Fact label="Lock task">
                <YesNo ok={state.lock_task}
                  why="The screen was off when this was captured, and a sleeping tablet cannot report its lock state" />
                {state.lock_task == null && state.screen_on === false &&
                  <span className="muted small"> screen was off</span>}
              </Fact>
              <Fact label="Accessibility service"><YesNo ok={state.accessibility_running} /></Fact>
              <Fact label="Can tap the screen"><YesNo ok={state.can_perform_gestures} /></Fact>
              <Fact label="Secure settings"><YesNo ok={state.can_write_secure_settings} /></Fact>
              <Fact label="Draw over apps"><YesNo ok={state.can_draw_overlays} /></Fact>
              <Fact label="Usage access"><YesNo ok={state.usage_access} /></Fact>
              <Fact label="Captured">
                {data.collected_at ? timeAgo(data.collected_at) || "just now" : "—"}
              </Fact>
            </dl>
          )}
          {(data.app_log || data.logcat) && (
            <>
              <span className="view-switch" role="group" aria-label="Which log">
                <button className={tab === "app_log" ? "on" : ""} onClick={() => setTab("app_log")}>
                  Ali MDM
                </button>
                <button className={tab === "logcat" ? "on" : ""} onClick={() => setTab("logcat")}>
                  System
                </button>
              </span>
              <pre className="logbox">{text || "Nothing recorded."}</pre>
              <div className="muted small">
                Newest last, and stamped with the device’s own clock — which is not
                necessarily this one. The Ali MDM log is what the app records about itself
                and survives only until it restarts; System is the last few hundred lines
                Android holds for this process.
              </div>
            </>
          )}
        </div>
      )}
    </Card>
  );
}

/* A permission or capability, said plainly. Missing ones are what an operator
   is looking for, so they are the ones that stand out. */
/* A fact the device reported, or did not.
   
   null and undefined are their own answer and must not collapse into "No": a
   device that cannot currently tell, or a build too old to report a field at
   all, would otherwise look exactly like a permission that has been refused. */
function YesNo({ ok, why }) {
  if (ok === null || ok === undefined) {
    return <span className="badge off" title={why || "The device did not report this"}>Unknown</span>;
  }
  return <span className={"badge " + (ok ? "on" : "off")}>{ok ? "Yes" : "No"}</span>;
}

/* Free space, and how tight it is. An install that fails for want of storage
   says nothing an operator can see, so the number is worth showing before
   anyone starts guessing. */
function Storage({ state }) {
  const free = state.storage_free_bytes;
  const total = state.storage_total_bytes;
  if (!free && free !== 0) return <span className="muted">Unknown</span>;
  const gb = (n) => (n / 1024 / 1024 / 1024).toFixed(1) + " GB";
  const pct = total ? Math.round((free / total) * 100) : null;
  // Below a couple of gigabytes an app of any size starts failing to install,
  // and that is worth colouring rather than leaving to arithmetic.
  const tone = free < 2 * 1024 ** 3 ? "off" : "on";
  return (
    <>
      <span className={"badge " + tone}>{gb(free)} free</span>
      {total ? <span className="muted small"> of {gb(total)}{pct !== null ? ` · ${pct}%` : ""}</span> : null}
    </>
  );
}

function Fact({ label, children }) {
  return <><dt>{label}</dt><dd>{children}</dd></>;
}

function DeviceDetail({ deviceId, me, onErr, onTitle, navigate }) {
  // History, files and commands are all tables; on a phone they stack into
  // cards like every other table in the console.
  useNarrowFlag();
  const [d, setD] = useState(null);
  const [groups, setGroups] = useState([]);
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState("history");
  const [live, setLive] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [snapNonce, setSnapNonce] = useState(0);

  const load = useCallback(async () => {
    try {
      const got = await api.getDevice(deviceId);
      setD(got);
      // The page is named after its device, which only this component knows.
      // No description line: the bar names the device, and every fact one could
      // carry is in the Details card a few lines below it.
      onTitle({ title: got.label, desc: "" });
    } catch (e) {
      if (e.status === 404) {
        setMissing(true);
        onTitle({ title: "Device not found", desc: "" });
      } else onErr(e.message);
    }
  }, [deviceId, onErr, onTitle]);

  // Head the page with the id until the record lands, rather than leaving the
  // previous page's title in the bar for the length of a request. It is also
  // what an unnamed device is called everywhere else.
  useEffect(() => { onTitle({ title: deviceId, desc: "" }); }, [deviceId, onTitle]);
  useEffect(() => { load(); const t = setInterval(load, 10000); return () => clearInterval(t); }, [load]);
  useEffect(() => { api.listGroups().then(setGroups).catch(() => {/* the group row just shows its id */}); }, []);

  async function cmd(type) {
    // Waking a screen is not worth a dialog; the rest interrupt whoever is
    // holding the tablet.
    if (type !== "screen_on" && !confirm(commandPrompt(type, d.label))) return;
    setBusy(true);
    try { await api.sendCommand(d.id, type); toast(`Sent ${commandName(type)} to ${d.label}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function rename(next) {
    const name = next.trim().slice(0, 64);
    setRenaming(false);
    if (name === (d.name || "")) return;
    setBusy(true);
    try { await api.renameDevice(d.id, name); toast(name ? `Renamed to "${name}"` : "Cleared the label"); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function moveGroup(groupId) {
    setBusy(true);
    try { await api.moveDeviceGroup(d.id, groupId); toast(`Moved to ${groupId}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // For a device whose settings have drifted from the policy. The server only
  // sends config when it thinks the device is behind, so a tablet that was
  // changed locally sits there looking in sync and is never corrected.
  async function resendConfig() {
    if (!confirm(resendPrompt(d.label || d.id))) return;
    setBusy(true);
    try {
      await api.resendDeviceConfig(d.id);
      toast("The policy will be sent again on the next check-in");
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // Only the app can surrender Device Owner, so this is a queued command
  // rather than something the server can do — unlike removing the device.
  async function releaseOwner() {
    if (!d.online && !confirm(
      `${d.label} is offline.\n\n` +
      "Only the app itself can surrender Device Owner, so this command sits queued " +
      "until the device checks in. If it never does, nothing happens.\n\nQueue it anyway?",
    )) return;
    if (!confirm(
      `Release Device Owner on ${d.label}?\n\n` +
      "The device leaves kiosk mode and loses lockdown: no app whitelist, no " +
      "navigation blocking, no factory-reset protection.\n\n" +
      "This is one-way. Nothing on the device can grant it back — restoring " +
      "management needs physical ADB access to the device.",
    )) return;
    setBusy(true);
    try { await api.sendCommand(d.id, "release_device_owner"); toast(`Queued Device Owner release for ${d.label}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function unenroll() {
    if (!confirm(
      `Remove ${d.label} from the console?\n\n` +
      "This removes the device here and revokes its key. It does not need the " +
      "device to be reachable, so it works for one that is lost or broken.\n\n" +
      "If the device ever checks in again it will be rejected and will wipe its " +
      "own cloud settings — but it stays locked down locally until Device Owner " +
      "is removed on the device itself.",
    )) return;
    setBusy(true);
    try {
      await api.unenroll(d.id);
      toast(`Removed ${d.label}`);
      navigate("/devices");
    } catch (e) { onErr(e.message); setBusy(false); }
  }

  if (missing) {
    return (
      <div className="stack">
        <Empty icon={IconDevices} title="No such device"
          desc="It may have been removed from the console, or this link may be out of date." />
      </div>
    );
  }
  if (!d) return <Loading label="Loading device…" />;

  return (
    <div className="stack">
      {!d.online && (
        <Alert tone="warning" icon={IconWarning} title="This device is not checking in">
          It keeps showing its kiosk while offline, so nothing looks wrong in the room.
          Commands and policy changes wait here and reach it when it returns
          {d.last_seen ? ` — last seen ${timeAgo(d.last_seen)}` : ""}.
        </Alert>
      )}

      <div className="detail-grid">
        <Card title="Screen" actions={live ? (
          <button className="btn outline sm" onClick={() => setLive(false)}>Stop live view</button>
        ) : (<>
          <button className="btn outline sm" disabled={!d.online}
            title={d.online ? "Watch this screen as it changes" : "The device is offline"}
            onClick={() => setLive(true)}><IconEye />Live view</button>
          <button className="btn outline sm" disabled={!d.online}
            title={d.online ? "Ask for a fresh screen now" : "The device is offline"}
            onClick={() => setSnapNonce((n) => n + 1)}><IconRefresh />Refresh</button>
        </>)}>
          {live
            ? <LiveView device={d} onErr={onErr} />
            : <DeviceSnapshot deviceId={d.id} online={d.online} intervalMs={30000}
                nonce={snapNonce} big />}
        </Card>

        <Card title="Details" footer={
          <div className="btn-group">
            {/* screen_on, not wake: wake only clears the screensaver overlay and
                resets the inactivity timer, while screen_on is what actually
                turns the display on. */}
            <button className="btn outline sm" disabled={busy} onClick={() => cmd("screen_on")}>
              <IconMonitor />Wake
            </button>
            <button className="btn outline sm" disabled={busy} onClick={() => cmd("restart_app")}
              title="Close and reopen Ali MDM. The tablet stays on — try this before a reboot.">
              <IconAndroid />Restart app
            </button>
            <button className="btn outline sm" disabled={busy} onClick={() => cmd("reboot")}>
              <IconPower />Reboot
            </button>
            <button className="btn outline sm" disabled={busy} onClick={() => cmd("lock")}>
              <IconLock />Lock
            </button>
            <button className="btn outline sm" disabled={busy} onClick={() => cmd("unlock")}>
              <IconUnlock />Unlock
            </button>
            <button className="btn outline sm" disabled={busy} onClick={resendConfig}
              title="Send the whole policy again on this device's next check-in. For a tablet whose settings have drifted — the policy is only sent when the console thinks the device is behind.">
              <IconRefresh />Re-apply policy
            </button>
          </div>
        }>
          <dl className="facts">
            <Fact label="Label">
              {renaming ? (
                <input autoFocus defaultValue={d.name || ""} placeholder={d.id} maxLength={64}
                  disabled={busy} style={{ height: 28, width: "100%" }}
                  title="Shown in the corner of this device's kiosk screen"
                  onBlur={(e) => rename(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") e.target.blur();
                    if (e.key === "Escape") { e.target.value = d.name || ""; e.target.blur(); }
                  }} />
              ) : (
                <span className="device-label" style={{ justifyContent: "flex-end" }}>
                  <span className="strong">{d.name || <span className="muted">Not set</span>}</span>
                  <button className="label-edit" title="Rename this device" disabled={busy}
                    aria-label="Rename this device" onClick={() => setRenaming(true)}>
                    <IconEdit />
                  </button>
                </span>
              )}
            </Fact>
            <Fact label="Status">
              <span className={"badge " + (d.online ? "on" : "off")}>
                <span className="dot" />{d.online ? "Online" : "Offline"}
              </span>
            </Fact>
            <Fact label="Last seen">
              <span title={d.last_seen ? new Date(d.last_seen).toLocaleString() : ""}>
                {d.last_seen ? timeAgo(d.last_seen) : "Never"}
              </span>
            </Fact>
            {/* Whether a command lands in a second or waits for the next
                check-in. Worth stating: the difference is otherwise only
                visible by queuing something and watching a clock. */}
            <Fact label="Commands">
              {d.wake_stream
                ? <span className="badge on" title="Holding the wake stream open">Land at once</span>
                : <span className="badge off" title="No wake stream; the device collects work on its next heartbeat">
                    Next check-in
                  </span>}
            </Fact>
            <Fact label="Battery">{d.battery != null && d.battery > 0 ? d.battery + "%" : "—"}</Fact>
            <Fact label="Android">{d.android_ver || "—"}</Fact>
            <Fact label="Model">{d.model || "—"}</Fact>
            <Fact label="Ali MDM">
              {!d.app_version_code ? "—" : d.stale ? (
                <span className="badge warning"
                  title={d.staged_version ? `Behind ${d.staged_version}, staged for the fleet` : "Behind the staged build"}>
                  {d.app_version_name || d.app_version_code}
                </span>
              ) : (d.app_version_name || d.app_version_code)}
            </Fact>
            <Fact label="Policy group">
              <select value={d.group_id || ""} disabled={busy}
                onChange={(e) => moveGroup(e.target.value)} style={{ height: 30, maxWidth: 170 }}>
                {groups.length === 0 && <option value={d.group_id}>{d.group_name}</option>}
                {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
              </select>
            </Fact>
            <Fact label="Policy version">
              v{d.config_version}
              {d.config_pending && (
                <span className="badge warning" style={{ marginInlineStart: 6 }}
                  title="Saved in the console, but this device has not checked in since. It will pick it up on its next check-in.">
                  not yet delivered
                </span>
              )}
            </Fact>
            <Fact label="Enrolled">
              {d.enrolled_at ? new Date(d.enrolled_at).toLocaleDateString() : "—"}
            </Fact>
            <Fact label="Device id"><span className="mono small">{d.id}</span></Fact>
            {d.pending_commands > 0 && (
              <Fact label="Queued">
                <button className="linkish small" onClick={() => setTab("commands")}>
                  {d.pending_commands} waiting
                </button>
              </Fact>
            )}
          </dl>
        </Card>
      </div>

      <span className="view-switch" role="group" aria-label="What to show about this device">
        <button className={tab === "history" ? "on" : ""} aria-pressed={tab === "history"}
          onClick={() => setTab("history")}><IconBell />History</button>
        <button className={tab === "files" ? "on" : ""} aria-pressed={tab === "files"}
          onClick={() => setTab("files")}><IconFile />Files</button>
        <button className={tab === "apps" ? "on" : ""} aria-pressed={tab === "apps"}
          onClick={() => setTab("apps")}><IconPackage />Apps</button>
        <button className={tab === "commands" ? "on" : ""} aria-pressed={tab === "commands"}
          onClick={() => setTab("commands")}><IconSliders />Commands</button>
        {/* Administrators only, matching what the server will allow. */}
        {me?.role === "admin" && (
          <button className={tab === "logs" ? "on" : ""} aria-pressed={tab === "logs"}
            onClick={() => setTab("logs")}><IconKey />Diagnostics</button>
        )}
      </span>

      {tab === "history" && <DeviceHistory deviceId={d.id} onErr={onErr} />}
      {tab === "files" && <DeviceFiles device={d} onErr={onErr} />}
      {tab === "apps" && <DeviceApps device={d} onErr={onErr} />}
      {tab === "commands" && <DeviceCommands deviceId={d.id} onErr={onErr} />}
      {tab === "logs" && me?.role === "admin" && <DeviceLogs deviceId={d.id} onErr={onErr} />}

      <Card title="Retire this device">
        <div className="stack" style={{ gap: 16 }}>
          <div className="flex" style={{ justifyContent: "space-between", gap: 16 }}>
            <div className="grow">
              <div className="strong small">Release Device Owner</div>
              <div className="muted small">
                Hands the device back: no kiosk, no lockdown, no factory-reset protection.
                Only the app can do this, so it needs the device to check in, and nothing
                here can grant it back afterwards.
              </div>
            </div>
            <button className="btn danger-outline sm" disabled={busy} onClick={releaseOwner}>
              <IconShield />Release
            </button>
          </div>
          <div className="flex" style={{ justifyContent: "space-between", gap: 16 }}>
            <div className="grow">
              <div className="strong small">Remove from the console</div>
              <div className="muted small">
                Revokes its key and drops it from the fleet. Works for a device that is lost
                or broken, but it stays locked down until Device Owner is released on the
                device itself.
              </div>
            </div>
            <button className="btn danger-outline sm" disabled={busy} onClick={unenroll}>
              <IconEject />Remove
            </button>
          </div>
        </div>
      </Card>
    </div>
  );
}

/* The full notification history.
   The bell is a peek at what just happened; this is the record. It pages by
   cursor rather than offset so a page cannot skip or repeat an entry when new
   events arrive while it is being read, and it says how much history exists so
   a gap reads as retention rather than as something lost. */

function Notifications({ me, onErr }) {
  useNarrowFlag();
  const [events, setEvents] = useState(null);
  const [hasMore, setHasMore] = useState(false);
  const [total, setTotal] = useState(0);
  const [kept, setKept] = useState(0);
  const [severity, setSeverity] = useState("");
  const [busy, setBusy] = useState(false);

  const PAGE = 50;

  const load = useCallback(async () => {
    try {
      const r = await api.listEventsPage({ limit: PAGE, severity });
      setEvents(r.events || []);
      setHasMore(!!r.has_more);
      setTotal(r.total || 0);
      setKept(r.kept || 0);
    } catch (e) { onErr(e.message); }
  }, [onErr, severity]);
  useEffect(() => { load(); }, [load]);

  async function loadOlder() {
    if (!events || events.length === 0) return;
    setBusy(true);
    try {
      const r = await api.listEventsPage({
        limit: PAGE, before: events[events.length - 1].id, severity,
      });
      setEvents((prev) => [...prev, ...(r.events || [])]);
      setHasMore(!!r.has_more);
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function markAllRead() {
    setBusy(true);
    try { await api.markEventsRead(0); toast("Marked everything read"); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  /* Admin-only, and deliberately blunt about it: this is the fleet's only
     record of who did what, and it does not come back. */
  async function clearAll() {
    if (!confirm(
      `Clear the activity feed?\n\n` +
      `All ${total} entr${total === 1 ? "y" : "ies"} are removed, for every operator, ` +
      `and cannot be recovered. A single entry recording that you cleared it stays behind.`
    )) return;
    setBusy(true);
    try {
      const r = await api.clearEvents();
      toast(`Cleared ${r.removed} entr${r.removed === 1 ? "y" : "ies"}`);
      await load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  if (!events) return <Loading label="Loading notifications…" />;

  return (
    <div className="stack">
      {/* Admin-only, as it was when this lived on its own page: it changes
          behaviour for everyone, not just the operator looking at it. */}
      {me?.role === "admin" && <OfflineAlertSettings onErr={onErr} />}

      <CardTable
        title="Notifications"
        actions={<>
          <select value={severity} onChange={(e) => setSeverity(e.target.value)}
            title="Narrow to the entries worth chasing" style={{ height: 32 }}>
            <option value="">Everything</option>
            <option value="warn">Needs attention</option>
            <option value="error">Failures only</option>
          </select>
          <button className="btn outline sm" disabled={busy} onClick={markAllRead}>Mark all read</button>
          {me?.role === "admin" && (
            <button className="btn danger-outline sm" disabled={busy || total === 0}
              title="Remove every entry, for every operator" onClick={clearAll}>
              Clear activity
            </button>
          )}
          <button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>
        </>}
        note={
          <Alert>
            Everything the fleet and its operators have done, newest first. The most recent{" "}
            {kept || 500} entries are kept — anything older is discarded, so a gap here is
            retention rather than something gone missing.
          </Alert>
        }
      >
        <table className="stacked">
          <thead>
            <tr><th>When</th><th>Level</th><th>Who</th><th>What happened</th></tr>
          </thead>
          <tbody>
            {events.length === 0 && (
              <tr><td colSpan={4} className="muted" style={{ textAlign: "center", padding: "24px 0" }}>
                {severity ? "Nothing at this level." : "Nothing has happened yet."}
              </td></tr>
            )}
            {events.map((ev) => (
              <tr key={ev.id}>
                <td className="small subtle nowrap" data-label="When"
                  title={new Date(ev.at).toLocaleString()}>
                  {timeAgo(ev.at) || "just now"}
                </td>
                <td className="nowrap" data-label="Level">
                  <span className={"badge " + (ev.severity === "error" ? "off"
                    : ev.severity === "warn" ? "warning" : "")}>
                    {SEVERITY_LABEL[ev.severity] || ev.severity}
                  </span>
                </td>
                <td className="small nowrap" data-label="Who">{ev.actor || "\u2014"}</td>
                <td className="small" data-label="What happened">{ev.summary}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </CardTable>

      <div className="flex" style={{ justifyContent: "center", gap: 12, alignItems: "center" }}>
        <span className="small muted">
          Showing {events.length} of {total}{severity ? " matching" : ""}
        </span>
        {hasMore && (
          <button className="btn outline sm" disabled={busy} onClick={loadOlder}>
            {busy ? "Loading\u2026" : "Load older"}
          </button>
        )}
      </div>
    </div>
  );
}

function Devices({ onErr, navigate }) {
  useNarrowFlag();
  const [view, setView] = useState(() => {
    try { return localStorage.getItem("devicesView") === "cards" ? "cards" : "table"; }
    catch { return "table"; }
  });
  const setViewMode = (v) => {
    setView(v);
    try { localStorage.setItem("devicesView", v); } catch { /* private mode */ }
  };
  // How often each card asks its tablet for a fresh screen. Per browser, like
  // the view: it decides how much work this operator's page makes for the
  // fleet, so it belongs with them rather than in the shared policy.
  const [snapEvery, setSnapEvery] = useState(() => {
    // Read null explicitly: Number(null) is 0, which is a valid stored value
    // meaning "manual", so a fresh browser silently started with refreshing off.
    const raw = localStorage.getItem("snapEvery");
    if (raw === null || raw === "") return 30000;
    const n = Number(raw);
    return Number.isFinite(n) && n >= 0 ? n : 30000;
  });
  const setSnapInterval = (ms) => {
    setSnapEvery(ms);
    try { localStorage.setItem("snapEvery", String(ms)); } catch { /* private mode */ }
  };
  const [renaming, setRenaming] = useState(null);
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

  // Every message here names the device the way the page does: its label, and
  // the id only for one nobody has named. An operator reads "IQRA Tab 2", not
  // android-6cabbb95036a8830.
  async function cmd(d, type) {
    const who = deviceLabel(d);
    if (type !== "screen_on" && !confirm(commandPrompt(type, who))) return;
    setBusy(true);
    try { await api.sendCommand(d.id, type); toast(`Sent ${type} to ${who}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // Re-deliver an unchanged policy to one device. The console only sends config
  // when it thinks a device is behind, so a tablet changed locally is never
  // corrected without this.
  async function resendConfig(d) {
    if (!confirm(resendPrompt(deviceLabel(d)))) return;
    setBusy(true);
    try { await api.resendDeviceConfig(d.id); toast(`The policy will be sent again to ${deviceLabel(d)}`); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  async function moveGroup(d, newGroup) {
    setBusy(true);
    try { await api.moveDeviceGroup(d.id, newGroup); toast(`Moved ${deviceLabel(d)} to ${newGroup}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }
  // Commit a label edit. Skips the round-trip when nothing changed so that
  // simply tabbing through the field does not spam the API on every 10s reload.
  async function renameDevice(id, name, previous) {
    const next = name.trim().slice(0, 64);
    if (next === (previous || "")) return;
    const was = previous || id;
    setBusy(true);
    try { await api.renameDevice(id, next); toast(next ? `Renamed ${was} to "${next}"` : `Cleared the label on ${was}`); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const filtered = useMemo(() => {
    if (!devices) return [];
    const s = q.trim().toLowerCase();
    const matched = !s ? devices : devices.filter((d) =>
      [d.id, d.name, d.model, d.android_ver, d.group_id].some((v) => (v || "").toLowerCase().includes(s)));
    // Offline first: in a fleet of twelve, the one that stopped reporting is
    // the only row anyone urgently needs, and alphabetical order buries it.
    return [...matched].sort((a, b) => (a.online === b.online ? 0 : a.online ? 1 : -1));
  }, [devices, q]);

  const offline = useMemo(() => (devices || []).filter((d) => !d.online), [devices]);

  if (!devices) return <Loading label="Loading devices…" />;
  const online = devices.filter((d) => d.online).length;

  return (
    <div className="stack">
      {offline.length > 0 && (
        <Alert tone="warning" icon={IconWarning}
          title={offline.length === 1 ? "1 device is not checking in" : `${offline.length} devices are not checking in`}>
          A device that stops checking in keeps working on screen, so this is usually the only
          sign. Commands and policy changes will not reach it until it returns.
          <div style={{ marginTop: 8 }}>
            {offline.map((d) => (
              <div key={d.id} className="small">
                <span className="strong">{d.name || d.id}</span>
                <span className="muted">
                  {" — "}{d.last_seen ? `silent ${timeAgo(d.last_seen)?.replace(" ago", "")}` : "never checked in"}
                </span>
              </div>
            ))}
          </div>
        </Alert>
      )}
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
        actions={<>
          <input className="search-input" placeholder="Search devices…" value={q}
            onChange={(e) => setQ(e.target.value)} />
          <span className="view-switch" role="group" aria-label="Device layout">
            <button className={view === "table" ? "on" : ""} aria-pressed={view === "table"}
              onClick={() => setViewMode("table")} title="Table"><IconRows />Table</button>
            <button className={view === "cards" ? "on" : ""} aria-pressed={view === "cards"}
              onClick={() => setViewMode("cards")} title="Cards, with a screen snapshot"><IconGrid />Cards</button>
          </span>
          {view === "cards" && (
            <select className="snap-every" value={snapEvery}
              onChange={(e) => setSnapInterval(Number(e.target.value))}
              aria-label="Screen refresh"
              title="How often each card asks its device for a fresh screen. Every refresh wakes the device, so slower is kinder to a fleet you are not actively watching. Click a screen to refresh it on demand.">
              <option value={0}>Manual</option>
              <option value={15000}>Every 15s</option>
              <option value={30000}>Every 30s</option>
              <option value={60000}>Every 1 min</option>
              <option value={300000}>Every 5 min</option>
            </select>
          )}
          <button className="btn outline sm" onClick={load}><IconRefresh />Refresh</button>
        </>}
      >
        <table className={"stacked" + (view === "cards" ? " cards" : "")}>
          <thead>
            <tr>
              <th>Device</th><th>Status</th><th>Battery</th><th>Android</th>
              <th>Build</th><th>Last seen</th><th>Group</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((d) => (
              <React.Fragment key={d.id}>
              <tr>
                <td className="nowrap" data-label="Device">
                  {renaming === d.id ? (
                    <input
                      autoFocus
                      defaultValue={d.name || ""}
                      // Empty shows the id, so the placeholder is what clearing
                      // the field will leave behind.
                      placeholder={d.id}
                      maxLength={64}
                      disabled={busy}
                      title="Shown in the corner of this device's kiosk screen"
                      style={{ width: 200, height: 28 }}
                      onBlur={(e) => { renameDevice(d.id, e.target.value, d.name); setRenaming(null); }}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") e.target.blur();
                        if (e.key === "Escape") { e.target.value = d.name || ""; e.target.blur(); }
                      }}
                    />
                  ) : (
                    <span className="device-label">
                      {/* The name opens the device's own page. It falls back to
                          the id, so a tablet nobody has named is still
                          identifiable, with the id in the tooltip. */}
                      <a className="device-link strong" title={d.id}
                        {...linkTo(pathForDevice(d.id), navigate)}>{d.name || d.id}</a>
                      <button className="label-edit" title="Rename this device"
                        aria-label={`Rename ${d.name || d.id}`}
                        disabled={busy} onClick={() => setRenaming(d.id)}>
                        <IconEdit />
                      </button>
                    </span>
                  )}
                  {d.model && <div className="small muted">{d.model}</div>}
                  {view === "cards" && (
                    <DeviceSnapshot deviceId={d.id} online={d.online} intervalMs={snapEvery} />
                  )}
                </td>
                <td data-label="Status">
                  <span className={"badge " + (d.online ? "on" : "off")}>
                    <span className="dot" />{d.online ? "Online" : "Offline"}
                  </span>
                  {/* Only worth marking when it is true. A fleet where most
                      tablets are on the fast path should draw the eye to the
                      one that is not, and an online device without the stream
                      is simply the ordinary case working. */}
                  {d.online && d.wake_stream && (
                    <div className="muted small" title="Holding the wake stream open — commands land at once">
                      instant
                    </div>
                  )}
                </td>
                <td className="nowrap" data-label="Battery">{d.battery != null ? d.battery + "%" : <span className="muted">—</span>}</td>
                <td className="nowrap" data-label="Android">{d.android_ver || <span className="muted">—</span>}</td>
                <td className="nowrap small" data-label="Build">
                  {!d.app_version_code ? (
                    <span className="muted" title="This build is too old to report which version it is">—</span>
                  ) : d.stale ? (
                    <span className="badge warning" title="Behind the build staged for the fleet">
                      {d.app_version_name || d.app_version_code}
                    </span>
                  ) : (
                    <span className="muted">{d.app_version_name || d.app_version_code}</span>
                  )}
                </td>
                <td className="small subtle nowrap" data-label="Last seen"
                  title={d.last_seen ? new Date(d.last_seen).toLocaleString() : ""}>
                  {d.last_seen ? timeAgo(d.last_seen) : <span className="muted">Never</span>}
                </td>
                <td data-label="Group">
                  <select value={d.group_id || ""} onChange={(e) => moveGroup(d, e.target.value)}
                    style={{ minWidth: 140, maxWidth: 180 }}>
                    {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                  </select>
                </td>
                <td className="cell-actions">
                  {/* The quick ones only. Live view, files, and anything that
                      cannot be undone are on the device's own page, where there
                      is room to explain them and no chance of hitting one while
                      aiming at the row above. The name opens that page; a
                      second control for it was a button saying what the link
                      next to it already did. */}
                  <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                    <button className="btn outline sm icon" title="Wake the screen" disabled={busy}
                      onClick={() => cmd(d, "screen_on")}><IconMonitor /></button>
                    <button className="btn outline sm icon" title="Reboot" disabled={busy}
                      onClick={() => cmd(d, "reboot")}><IconPower /></button>
                    <button className="btn outline sm icon" title="Lock" disabled={busy}
                      onClick={() => cmd(d, "lock")}><IconLock /></button>
                    <button className="btn outline sm icon" title="Unlock" disabled={busy}
                      onClick={() => cmd(d, "unlock")}><IconUnlock /></button>
                    <button className="btn outline sm icon" title="Re-apply policy" disabled={busy}
                      aria-label={`Re-apply the policy to ${d.name || d.id}`}
                      onClick={() => resendConfig(d)}><IconRefresh /></button>
                  </div>
                </td>
              </tr>
              </React.Fragment>
            ))}
            {filtered.length === 0 && (
              <tr><td colSpan="8" style={{ padding: 0 }}>
                <Empty
                  icon={IconDevices}
                  title={devices.length === 0 ? "No devices enrolled yet" : "No devices match that search"}
                  desc={devices.length === 0
                    ? "Enroll a device over ADB and it will appear here within a few seconds."
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
    setBusy(true);
    try {
      const r = await api.uploadAgentRelease(file);
      toast(`Uploaded ${r.version_name || ""} (versionCode ${r.version_code})`);
      setFile(null);
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function rollout(ids) {
    const label = ids.length ? `${ids.length} device(s)` : "all devices";
    if (!confirm(
      `Push Ali MDM ${release.version_name || ""} (versionCode ${release.version_code}) to ${label}?\n\n` +
      "Each device downloads the build, verifies it, then restarts into the new version. " +
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
            <div className="flex" style={{ gap: 24, marginBottom: 16 }}>
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

          <div className="flex" style={{ gap: 8, alignItems: "flex-end" }}>
            <div>
              <label className="small subtle" htmlFor="agent-file">APK</label>
              <input id="agent-file" type="file" accept=".apk" disabled={busy}
                onChange={(e) => setFile(e.target.files[0] || null)} style={{ display: "block", width: 240 }} />
            </div>
            <button className="btn" onClick={upload} disabled={busy}>
              <IconUpload />{busy ? "Uploading…" : "Upload build"}
            </button>
          </div>
          <p className="small subtle">
            The version is read from the APK itself. Uploading anything other than an Ali MDM
            build is rejected, since it could not replace the app the devices are running.
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
          <table className="stacked">
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
                    <td className="nowrap" data-label="Device">
                      <div className="strong">{d.name || d.id}</div>
                      {d.name && <div className="mono small subtle">{d.id}</div>}
                    </td>
                    <td data-label="Status">
                      {u ? (
                        <span className={"badge " + (AGENT_STATUS_BADGE[u.status] || "off")}>
                          <span className="dot" />{u.status}
                        </span>
                      ) : <span className="muted small">never updated</span>}
                    </td>
                    <td className="nowrap" data-label="Attempts">{u ? u.attempts : <span className="muted">—</span>}</td>
                    <td className="nowrap mono small" data-label="Target">
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
                    desc="Enroll a device before pushing an app update." />
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
    // The server reads the package name out of the manifest at upload, for a
    // plain APK as well as a split one — so there is nothing here for the
    // operator to type, and nothing to mistype. It stays editable for the case
    // where the parser could not read a manifest and left it empty.
    const known = apks?.find((a) => a.name === apkName)?.package_name || "";
    setInstallName(apkName); setInstallPkg(known); setSelected({}); setSelectAll(false);
  }
  async function removeAPK(name) {
    if (!confirm(`Delete ${name} from the server?\n\nDevices that already installed it keep it — this only removes the server copy and stops future installs.`)) return;
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
        actions={
          <label className="btn sm" style={{ position: "relative", overflow: "hidden" }}>
            <IconUpload />{busy ? "Uploading…" : "Upload APK"}
            <input type="file" accept=".apk,.xapk,.apks,.apkm" onChange={upload} disabled={busy}
              style={{ position: "absolute", inset: 0, opacity: 0, cursor: "pointer", height: "100%" }} />
          </label>
        }
        note={
          <Alert>
            Upload an APK, then queue a silent install to the devices you pick. Devices download and
            install it on their next poll, and the whitelist locks it afterwards.
            A <span className="mono">.xapk</span> or <span className="mono">.apks</span> is unpacked
            here and its APKs are installed together, so an app split across a base and several
            config APKs arrives in one piece. It is listed under its package name.
          </Alert>
        }
      >
        <table className="stacked">
          <thead>
            <tr><th>File</th><th>Size</th><th>SHA-256</th><th style={{ textAlign: "right" }}>Actions</th></tr>
          </thead>
          <tbody>
            {apks.map((a) => (
              <tr key={a.name}>
                <td className="mono strong" data-label="File">
                  {a.name}
                  {/* What an install actually targets, which the file name only
                      sometimes resembles. */}
                  {a.package_name && (
                    <div className="muted small mono">
                      {a.package_name}{a.version_name ? ` · ${a.version_name}` : ""}
                    </div>
                  )}
                </td>
                <td className="nowrap" data-label="Size">{(a.size / 1024 / 1024).toFixed(1)} MB</td>
                <td className="mono small muted" data-label="SHA-256">
                  {/* A split package is a set of APKs rather than a file, so it
                      has no single hash. Say what it is instead of showing an
                      empty hash where every other row has one. */}
                  {a.parts
                    ? <span className="badge off">{a.parts.length} APKs</span>
                    : <>{(a.sha256 || "").slice(0, 16)}…</>}
                </td>
                <td className="cell-actions">
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
                  desc="Upload an APK to push it out to your enrolled devices." />
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
                    No enrolled devices yet. Enroll a device first, then it will appear here.
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

/**
 * Setup-wizard QR provisioning.
 *
 * The QR embeds the enrolment token, so it is generated on demand rather than
 * whenever this page is opened, and is hidden again as soon as the inputs
 * change — a stale QR on a shared screen is a live credential.
 */
function QrEnrollCard({ onErr }) {
  const [groups, setGroups] = useState([]);
  const [group, setGroup] = useState("");
  const [label, setLabel] = useState("");
  const [ssid, setSsid] = useState("");
  const [wifiPassword, setWifiPassword] = useState("");
  const [info, setInfo] = useState(null);
  const [png, setPng] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => { api.listGroups().then(setGroups).catch(() => {/* optional */}); }, []);
  // Any change to what the QR would encode invalidates the one on screen.
  useEffect(() => { setInfo(null); setPng(""); }, [group, label, ssid, wifiPassword]);

  async function generate() {
    setBusy(true);
    try {
      const q = new URLSearchParams();
      if (group) q.set("group", group);
      if (label.trim()) q.set("label", label.trim());
      if (ssid) {
        q.set("ssid", ssid);
        if (wifiPassword) q.set("wifi_password", wifiPassword);
      }
      const d = await api.provisionQR(q.toString());
      setInfo(d);
      // Only render a scannable code when the payload would actually work;
      // a QR missing its checksum fails after the tablet has downloaded the
      // APK, which looks like a network fault and wastes a factory reset.
      setPng(d.ready
        ? await QRCode.toDataURL(d.payload, { errorCorrectionLevel: "M", margin: 2, width: 320 })
        : "");
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  return (
    <Card title="Enroll by QR (no cable)">
      <div className="stack">
        <Alert>
          On a <b>factory-fresh</b> device, tap the first setup screen six times to open the QR
          scanner, then scan this. The wizard downloads Ali MDM from this server, verifies its
          signature, makes it Device Owner and enrolls it — no cable, no ADB. The same QR works
          for every device.
        </Alert>

        <div className="flex" style={{ gap: 12, alignItems: "flex-end" }}>
          <div>
            <label className="small subtle" htmlFor="qr-group">Enroll into group</label>
            <select id="qr-group" value={group} onChange={(e) => setGroup(e.target.value)}
              style={{ display: "block", minWidth: 160, height: 34 }}>
              <option value="">(server default)</option>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </div>
          <div>
            <label className="small subtle" htmlFor="qr-label">Device label (optional)</label>
            <input id="qr-label" value={label} onChange={(e) => setLabel(e.target.value)}
              maxLength={64} placeholder="e.g. Library device"
              title="One code carries one label, so name a code per device. Left blank, the device shows as its id until renamed."
              style={{ display: "block", minWidth: 180, height: 34 }} />
          </div>
          <div>
            <label className="small subtle" htmlFor="qr-ssid">Wi-Fi SSID (optional)</label>
            <input id="qr-ssid" value={ssid} onChange={(e) => setSsid(e.target.value)}
              placeholder="School-WiFi" style={{ display: "block", width: 170, height: 34 }} />
          </div>
          <div>
            <label className="small subtle" htmlFor="qr-wifi-pw">Wi-Fi password</label>
            <input id="qr-wifi-pw" type="password" value={wifiPassword}
              onChange={(e) => setWifiPassword(e.target.value)} placeholder="(WPA)"
              style={{ display: "block", width: 150, height: 34 }} />
          </div>
          <button className="btn" onClick={generate} disabled={busy}>
            {busy ? "Generating…" : "Generate QR"}
          </button>
        </div>
        <p className="small subtle" style={{ marginTop: 0 }}>
          Supplying Wi-Fi lets the device reach this server before anyone has typed a password
          into it. Both the SSID and password are embedded in the QR.
        </p>

        {info && !info.ready && (
          <Alert tone="warning" icon={IconWarning} title="Not ready yet — fix these first">
            <ul style={{ margin: "6px 0 0", paddingLeft: 18 }}>
              {info.problems.map((p) => <li key={p} className="small">{p}</li>)}
            </ul>
          </Alert>
        )}

        {png && (
          <div className="stack tight" style={{ alignItems: "flex-start" }}>
            <Alert tone="warning" icon={IconWarning} title="This QR contains your enrollment token">
              Anyone who photographs it can enroll a device into your fleet. Show it only while
              provisioning, and rotate the token if it leaks.
            </Alert>
            <img src={png} width="320" height="320" alt="Provisioning QR code"
              style={{ background: "#fff", padding: 8, borderRadius: 8 }} />
            <div className="small subtle">
              {/* Which build a tablet scanning this will end up on. It is the
                  release staged on the App update page, so it cannot drift
                  behind the fleet the way a hand-copied file did. */}
              Installs: <span className="mono">{info.build || "nothing staged"}</span><br />
              APK: <span className="mono">{info.apk_url}</span><br />
              Signing checksum: <span className="mono">{info.checksum}</span>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

/* ── File library ─────────────────────────────────────────────────────────────
   Upload once, push to a group, and every tablet fetches it into its "Ali MDM"
   folder under Downloads, where the pupil's own Files app can open it. The list
   shows where each push got to, because a push that quietly missed half a class
   is the failure worth catching. */
function fmtSize(bytes) {
  if (!bytes && bytes !== 0) return "—";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(0) + " KB";
  return (bytes / 1024 / 1024).toFixed(1) + " MB";
}

/* The catalogue key is the file's path in the library, so the row should show
   the last part of it — which is also the name the tablet saves it under. The
   server sends that name; the fallback is the same thing read off the key. */
function fileLabel(f) {
  return f.file_name || f.name.split("/").pop();
}

/* The library is stored flat — each file carries the folder it belongs to as
   rel_path — so the shape the operator uploaded exists only here and on the
   tablet. buildFileTree puts it back together for display. */
function buildFileTree(files, labelOf) {
  const root = { name: "", path: "", folders: new Map(), files: [] };
  for (const f of files) {
    let node = root;
    for (const part of (f.rel_path || "").split("/").filter(Boolean)) {
      let child = node.folders.get(part);
      if (!child) {
        child = {
          name: part, path: node.path ? `${node.path}/${part}` : part,
          folders: new Map(), files: [],
        };
        node.folders.set(part, child);
      }
      node = child;
    }
    node.files.push({ ...f, label: labelOf(f) });
  }
  return root;
}

/* What a folder is worth saying without opening it: how much is inside, and
   whether it reached the tablets. Everything counts the whole subtree, so a
   collapsed folder still tells the truth about what is under it.

   Delivery is counted in files, not in devices: adding up the device counts of
   thirty tracks gives "90 delivered", which is a true number and a useless one.
   A file counts as delivered only once every device it was sent to has it. */
function folderStats(node) {
  const s = { files: 0, size: 0, sent: 0, complete: 0, pending: 0, failed: 0 };
  (function walk(n) {
    for (const f of n.files) {
      s.files += 1;
      s.size += f.size || 0;
      if (!f.targets) continue;
      s.sent += 1;
      if (f.failed) s.failed += 1;
      else if (f.pending) s.pending += 1;
      else s.complete += 1;
    }
    for (const d of n.folders.values()) walk(d);
  })(node);
  return s;
}


/* Flatten the tree into table rows, honouring which folders are open. Rows
   carry their depth so the File column can indent them, and folders come before
   files at each level, the way a file manager orders them. */
function treeRows(node, isOpen, depth = 0, out = []) {
  const folders = [...node.folders.values()].sort((a, b) => byName(a.name, b.name));
  for (const d of folders) {
    out.push({ kind: "folder", key: "d:" + d.path, node: d, depth });
    if (isOpen(d.path)) treeRows(d, isOpen, depth + 1, out);
  }
  for (const f of [...node.files].sort((a, b) => byName(a.label, b.label))) {
    out.push({ kind: "file", key: "f:" + f.name, file: f, depth });
  }
  return out;
}

/* One shared indent for folder and file rows. A file row keeps the caret's
   width as a spacer, or names would jitter sideways as folders open. */
function TreeCell({ depth, children }) {
  return <div className="tree-cell" style={{ paddingLeft: depth * 20 }}>{children}</div>;
}

/* Delivery for one file, counted in devices. */
function DeliveryCell({ targets, delivered, pending, failed }) {
  if (!targets) return <span className="muted small">Not sent yet</span>;
  return (
    <span className="small">
      <span className="badge success">{delivered} delivered</span>
      {pending > 0 && <> <span className="badge warning">{pending} pending</span></>}
      {failed > 0 && <> <span className="badge off">{failed} failed</span></>}
    </span>
  );
}

/* Delivery for a folder, counted in files — see folderStats for why. */
function FolderDeliveryCell({ files, sent, complete, pending, failed }) {
  if (!sent) return <span className="muted small">Not sent yet</span>;
  return (
    <span className="small">
      <span className="badge success">{complete} of {files} delivered</span>
      {pending > 0 && <> <span className="badge warning">{pending} in progress</span></>}
      {failed > 0 && <> <span className="badge off">{failed} failed</span></>}
    </span>
  );
}

function Files({ onErr }) {
  const [files, setFiles] = useState(null);
  const [groups, setGroups] = useState([]);
  const [devices, setDevices] = useState([]);
  const [busy, setBusy] = useState(false);
  // Which row has its send panel open — a file or a folder, so one bit of state
  // cannot leave both open at once.
  const [push, setPush] = useState(null);
  // The audience for the open send panel: everyone, one group, or a chosen few.
  const [pushScope, setPushScope] = useState("all");
  const [pushGroup, setPushGroup] = useState("");
  const [pushDevices, setPushDevices] = useState(() => new Set());
  const [detail, setDetail] = useState(null);
  const [progress, setProgress] = useState("");
  const [expanded, setExpanded] = useState(() => new Set());

  const load = useCallback(async () => {
    try {
      const [f, g, d] = await Promise.all([api.listLibraryFiles(), api.listGroups(), api.listDevices()]);
      setFiles(f); setGroups(g); setDevices(d);
    } catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); }, [load]);
  // Deliveries land on the tablets' own schedule, so the counts move on their own.
  useEffect(() => { const t = setInterval(load, 15000); return () => clearInterval(t); }, [load]);

  // Rebuilt whenever the list refreshes; open folders live in their own state so
  // a background refresh never collapses what the operator was looking at.
  const rows = useMemo(
    () => treeRows(buildFileTree(files ?? [], fileLabel), (p) => expanded.has(p)),
    [files, expanded]);

  function toggle(path) {
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(path) ? next.delete(path) : next.add(path);
      return next;
    });
  }

  async function upload(e) {
    const file = e.target.files[0];
    if (!file) return;
    setBusy(true);
    try { await api.uploadLibraryFile(file, file.name); toast(`Uploaded ${file.name}`); load(); }
    catch (err) { onErr(err.message); }
    setBusy(false); e.target.value = "";
  }

  /**
   * Upload a folder, one file at a time.
   *
   * Sequential rather than parallel on purpose: thirty at once would open
   * thirty uploads against a server sized for a school, and a failure part-way
   * through would leave no way to say which ones landed. One at a time reports
   * progress honestly and stops where it broke.
   */
  async function uploadFolder(e) {
    const picked = Array.from(e.target.files || []);
    e.target.value = "";
    if (picked.length === 0) return;

    setBusy(true);
    let done = 0;
    const failed = [];
    const roots = new Set();
    for (const file of picked) {
      // webkitRelativePath is "RootFolder/Sub/track.mp3"; the folder part is
      // what the tablet recreates, so drop the file name from the end.
      const parts = (file.webkitRelativePath || "").split("/").slice(0, -1);
      const rel = parts.join("/");
      if (parts[0]) roots.add(parts[0]);
      setProgress(`Uploading ${done + 1} of ${picked.length}…`);
      try {
        await api.uploadLibraryFile(file, file.name, rel);
        done += 1;
      } catch (err) {
        failed.push(`${file.name}: ${err.message}`);
      }
    }
    setProgress("");
    setBusy(false);
    // Open what was just uploaded. Landing on a collapsed folder after watching
    // thirty files go up reads as if nothing happened.
    setExpanded((prev) => new Set([...prev, ...roots]));
    load();

    if (failed.length === 0) {
      toast(`Uploaded ${done} file${done === 1 ? "" : "s"}`);
    } else {
      // Name the first failure rather than only a count: "3 failed" sends an
      // operator back to the folder with nothing to go on.
      onErr(`Uploaded ${done} of ${picked.length}. Failed: ${failed[0]}` +
        (failed.length > 1 ? ` (and ${failed.length - 1} more)` : ""));
    }
  }

  /* Opening a panel starts from "everyone" every time. Carrying the last
     choice over would mean a folder quietly going to whoever the previous send
     happened to name. */
  function openPush(target) {
    setPush(target);
    setPushScope("all");
    setPushGroup("");
    setPushDevices(new Set());
  }

  function toggleDevice(id) {
    setPushDevices((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  // What the server is told, and what the button says it will do.
  const audience =
    pushScope === "group" ? { group_id: pushGroup }
    : pushScope === "devices" ? { devices: [...pushDevices] }
    : {};
  const audienceCount =
    pushScope === "group" ? devices.filter((d) => d.group_id === pushGroup).length
    : pushScope === "devices" ? pushDevices.size
    : devices.length;
  // A group with nothing in it, or no group picked, is not a send worth making.
  const canSend = audienceCount > 0 && (pushScope !== "group" || pushGroup !== "");

  async function doPush(target) {
    const where = audience;
    setBusy(true);
    try {
      if (target.kind === "folder") {
        const r = await api.pushLibraryFolder(target.path, where);
        toast(`Sending ${r.files} file${r.files === 1 ? "" : "s"} from ${target.path} ` +
          `to ${r.targets} device${r.targets === 1 ? "" : "s"}`);
      } else {
        const r = await api.pushLibraryFile(target.name, where);
        toast(`Sending ${target.name} to ${r.queued} device${r.queued === 1 ? "" : "s"}`);
      }
      setPush(null);
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function removeFile(name) {
    if (!confirm(`Delete ${name} from the library?\n\nCopies already on devices stay where they are — this only removes the server copy and stops future sends.`)) return;
    setBusy(true);
    try { await api.deleteLibraryFile(name); toast(`Deleted ${name}`); if (detail === name) setDetail(null); load(); }
    catch (e) { onErr(e.message); }
    setBusy(false);
  }

  async function removeFolder(path, count) {
    if (!confirm(`Delete the folder ${path} and the ${count} file${count === 1 ? "" : "s"} in it from the library?\n\nCopies already on devices stay where they are — this only removes the server copies and stops future sends.`)) return;
    setBusy(true);
    try {
      const r = await api.deleteLibraryFolder(path);
      toast(`Deleted ${r.deleted} file${r.deleted === 1 ? "" : "s"} from ${path}`);
      setDetail(null);
      load();
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const deviceName = (id) => {
    const d = devices.find((x) => x.id === id);
    return d ? deviceLabel(d) : id;
  };

  /* The send panel, shared by both row kinds: pick a group, confirm. It sits in
     a row of its own directly under whatever it belongs to. */
  const sendPanel = (target, label) => (
    <tr><td colSpan={4} style={{ background: "var(--muted-bg, transparent)" }}>
      <div className="send-panel">
        <div className="send-row">
          <span className="small muted">Send to</span>
          <select value={pushScope} onChange={(e) => setPushScope(e.target.value)}>
            <option value="all">Every enrolled device</option>
            <option value="group">A group</option>
            <option value="devices">Chosen devices</option>
          </select>
          {pushScope === "group" && (
            <select value={pushGroup} onChange={(e) => setPushGroup(e.target.value)}>
              <option value="">Pick a group…</option>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name || g.id}</option>)}
            </select>
          )}
          <button className="btn sm" disabled={busy || !canSend} onClick={() => doPush(target)}>
            Send {label} to {audienceCount} device{audienceCount === 1 ? "" : "s"}
          </button>
          {!canSend && (
            <span className="muted small">
              {pushScope === "group" && pushGroup === "" ? "Pick a group first."
                : pushScope === "devices" ? "Choose at least one device."
                : "No devices are enrolled yet."}
            </span>
          )}
        </div>
        {pushScope === "devices" && (
          <div className="device-picker">
            {devices.length > 1 && (
              <button className="linkish small" onClick={() => setPushDevices(
                pushDevices.size === devices.length ? new Set() : new Set(devices.map((d) => d.id)))}>
                {pushDevices.size === devices.length ? "Clear all" : "Select all"}
              </button>
            )}
            {devices.map((d) => (
              <label key={d.id} className={"device-pick" + (pushDevices.has(d.id) ? " is-on" : "")}>
                <input type="checkbox" checked={pushDevices.has(d.id)}
                  onChange={() => toggleDevice(d.id)} />
                <span className={"dot " + (d.online ? "on" : "off")} />
                <span className="tree-name">{deviceLabel(d)}</span>
              </label>
            ))}
            {devices.length === 0 && <span className="muted small">No devices are enrolled yet.</span>}
          </div>
        )}
      </div>
    </td></tr>
  );

  if (!files) return <Loading label="Loading files…" />;

  return (
    <div className="stack">
      <CardTable
        actions={<>
          {progress && <span className="muted small" style={{ marginRight: 10 }}>{progress}</span>}
          <label className="btn outline sm" style={{ position: "relative", overflow: "hidden" }}>
            <IconUpload />{busy ? "Working…" : "Upload file"}
            <input type="file" onChange={upload} disabled={busy}
              style={{ position: "absolute", inset: 0, opacity: 0, cursor: "pointer", height: "100%" }} />
          </label>
          {/* webkitdirectory is what makes the picker offer a folder. It is
              non-standard but supported everywhere the console runs, and the
              plain file button above stays for anyone whose browser is not. */}
          <label className="btn sm" style={{ position: "relative", overflow: "hidden" }}>
            <IconUpload />{busy ? "Working…" : "Upload folder"}
            <input type="file" webkitdirectory="" multiple onChange={uploadFolder} disabled={busy}
              style={{ position: "absolute", inset: 0, opacity: 0, cursor: "pointer", height: "100%" }} />
          </label>
        </>}
        note={
          <Alert>
            Upload a file or a whole folder, then send it to a group — sending a folder sends
            everything inside it, including its sub-folders. Each device downloads on its next
            check-in into <span className="mono">Download/Ali MDM</span>, keeping the same folder
            structure, where the device’s own Files app can open it. Deleting here does not remove
            copies already on devices.
          </Alert>
        }
      >
        <table className="stacked">
          <thead>
            <tr>
              <th>File</th><th>Size</th><th>Delivery</th>
              <th style={{ textAlign: "right" }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr><td colSpan={4} className="muted" style={{ textAlign: "center", padding: "24px 0" }}>
                No files yet. Upload one to send it to a class.
              </td></tr>
            )}
            {rows.map((row) => {
              if (row.kind === "folder") {
                const st = folderStats(row.node);
                const open = expanded.has(row.node.path);
                return (
                  <React.Fragment key={row.key}>
                    <tr>
                      <td className="strong" data-label="Folder">
                        <TreeCell depth={row.depth}>
                          {/* The whole name is the toggle, so opening a folder
                              does not mean hitting a 16px caret. */}
                          <button className="tree-toggle" aria-expanded={open}
                            aria-label={`${open ? "Collapse" : "Expand"} ${row.node.path}`}
                            onClick={() => toggle(row.node.path)}>
                            <IconChevronRight width="16" height="16"
                              className={"tree-caret" + (open ? " is-open" : "")} />
                            <IconFolder width="18" height="18" className="tree-icon" />
                            <span className="tree-name">{row.node.name}</span>
                          </button>
                        </TreeCell>
                      </td>
                      <td className="nowrap" data-label="Size">
                        {fmtSize(st.size)}
                        <div className="muted small">{st.files} file{st.files === 1 ? "" : "s"}</div>
                      </td>
                      <td className="nowrap" data-label="Delivery"><FolderDeliveryCell {...st} /></td>
                      <td className="cell-actions">
                        <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                          {push?.key === row.key ? (
                            <button className="btn outline sm" onClick={() => setPush(null)}>Cancel</button>
                          ) : (
                            <button className="btn outline sm" title={`Send ${row.node.path} to devices`}
                              onClick={() => openPush({ kind: "folder", key: row.key, path: row.node.path })}>
                              <IconUpload />Send folder
                            </button>
                          )}
                          <button className="btn danger-outline sm icon"
                            title={`Delete ${row.node.path} from the library`}
                            disabled={busy} onClick={() => removeFolder(row.node.path, st.files)}><IconTrash /></button>
                        </div>
                      </td>
                    </tr>
                    {push?.key === row.key && sendPanel(push, `${st.files} file${st.files === 1 ? "" : "s"}`)}
                  </React.Fragment>
                );
              }

              const f = row.file;
              return (
                <React.Fragment key={row.key}>
                  <tr>
                    <td className="strong" data-label="File">
                      <TreeCell depth={row.depth}>
                        <span className="tree-caret-gap" />
                        <IconFile width="17" height="17" className="tree-icon" />
                        <span className="tree-name">{f.label}</span>
                      </TreeCell>
                    </td>
                    <td className="nowrap" data-label="Size">{fmtSize(f.size)}</td>
                    <td className="nowrap" data-label="Delivery"><DeliveryCell {...f} /></td>
                    <td className="cell-actions">
                      <div className="btn-group" style={{ justifyContent: "flex-end", width: "100%" }}>
                        {f.targets > 0 && (
                          <button className="btn outline sm" onClick={() => setDetail(detail === f.name ? null : f.name)}>
                            {detail === f.name ? "Hide" : "Details"}
                          </button>
                        )}
                        {push?.key === row.key ? (
                          <button className="btn outline sm" onClick={() => setPush(null)}>Cancel</button>
                        ) : (
                          <button className="btn outline sm" title={`Send ${f.label} to devices`}
                            onClick={() => openPush({ kind: "file", key: row.key, name: f.name })}>
                            <IconUpload />Send to devices
                          </button>
                        )}
                        <button className="btn danger-outline sm icon" title={`Delete ${f.label} from the library`}
                          disabled={busy} onClick={() => removeFile(f.name)}><IconTrash /></button>
                      </div>
                    </td>
                  </tr>
                  {push?.key === row.key && sendPanel(push, f.label)}
                  {detail === f.name && <FileDeliveries name={f.name} deviceName={deviceName} onErr={onErr} />}
                </React.Fragment>
              );
            })}
          </tbody>
        </table>
      </CardTable>
    </div>
  );
}

/** Per-device delivery state for one file. */
function FileDeliveries({ name, deviceName, onErr }) {
  const [rows, setRows] = useState(null);
  useEffect(() => {
    let cancelled = false;
    const load = () => api.fileDeliveries(name)
      .then((r) => { if (!cancelled) setRows(r); })
      .catch((e) => onErr(e.message));
    load();
    const t = setInterval(load, 10000);
    return () => { cancelled = true; clearInterval(t); };
  }, [name, onErr]);

  return (
    <tr><td colSpan={4}>
      {!rows ? <span className="muted small">Loading…</span> : (
        <table style={{ margin: 0 }}>
          <thead><tr><th>Device</th><th>Status</th><th>Detail</th></tr></thead>
          <tbody>
            {rows.map((d) => (
              <tr key={d.id}>
                <td className="small">{deviceName(d.device_id)}</td>
                <td className="nowrap small">
                  {d.status === "done" ? <span className="badge success">Delivered</span>
                    : d.status === "failed" ? <span className="badge off">Failed</span>
                    : <span className="badge warning">{d.status === "sent" ? "Downloading" : "Waiting"}</span>}
                </td>
                <td className="small muted">{d.last_error || (d.status === "done" ? "In the device's Ali MDM folder" : "—")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </td></tr>
  );
}

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
    { title: "Factory-reset the device",
      body: "Settings → System → Reset → Erase all data. Let it reboot into the Android setup wizard." },
    { title: "Complete basic setup",
      body: "Choose language, connect to Wi-Fi, and get to the home screen. Skip the Google account sign-in if you can." },
    { title: "Enable USB debugging",
      body: "Settings → About tablet → tap “Build number” 7 times to unlock Developer options. Then Settings → System → Developer options → turn on USB debugging." },
    { title: "Connect the device to the enrollment machine",
      body: "Plug in a USB cable (or use Wi-Fi debugging). Accept the “Allow USB debugging?” prompt on the device." },
    { title: "Run the enrollment command",
      body: "On the enrollment machine, run the command below. It installs Ali MDM, sets it as Device Owner, and enrolls the device to the cloud — which then auto-installs the managed apps. --label names the device straight away (drop it and the device shows as its id until you rename it); add --group <id> to put it in a policy group other than the default.",
      code: "cd /path/to/ali-mdm && ./apps/enroll/enroll_tablet.sh <SERIAL> --label \"Library device\"", copyKey: "cmd" },
    { title: "Reboot the device",
      body: "After enrollment completes, reboot the device. On boot it comes up in the kiosk with the managed apps installed and ready." },
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

      <QrEnrollCard onErr={onErr} />

      <Card title="Enroll a device over ADB">
        <div className="stack">
          <Alert>
            Use this when a device is already past its setup wizard, or when the QR path is not
            an option. Both routes end in the same place; QR avoids the cable and the reset.
          </Alert>

          <div className="stack tight">
            {steps.map((s, i) => (
              <div key={s.title} className="flex oneline" style={{ alignItems: "flex-start", gap: 14, padding: "14px 0", borderTop: i === 0 ? "none" : "1px solid var(--border)" }}>
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
          <li><span>The <span className="mono">enroll_tablet.sh</span> script lives on the enrollment machine and reads the enroll token and cloud URL from its config — you only supply the device serial.</span></li>
          <li><span>Each device enrolls independently; the same cloud and group config applies to all of them.</span></li>
          <li><span><b>One-click helper (coming soon):</b> a Windows/macOS enrollment app will automate steps 4–6. Until then, the command above is all you need.</span></li>
        </ul>
      </Card>
    </div>
  );
}

/* ── Alerts (server-wide) ───────────────────────────────────────────────── */

function OfflineAlertSettings({ onErr }) {
  const [settings, setSettings] = useState(null);
  const [url, setUrl] = useState("");
  const [minutes, setMinutes] = useState("15");
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await api.getAlertSettings();
      setSettings(s);
      setUrl(s.webhook_url || "");
      setMinutes(String(s.offline_minutes || "15"));
    } catch (e) { onErr(e.message); }
  }, [onErr]);
  useEffect(() => { load(); }, [load]);

  async function save() {
    setBusy(true);
    try {
      const s = await api.updateAlertSettings({ webhook_url: url.trim(), offline_minutes: minutes.trim() });
      setSettings(s);
      toast(s.enabled ? "Alerting on" : "Alerting off — no webhook set");
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  // Sends a real request to the configured endpoint, so a wrong URL or a
  // rejecting receiver shows up now rather than the first time a tablet
  // actually goes quiet.
  async function sendTest() {
    setTesting(true);
    try { await api.testAlertWebhook(); toast("Test alert sent"); }
    catch (e) { onErr(e.message); }
    setTesting(false);
  }

  if (!settings) return <Loading label="Loading alert settings…" />;

  return (
    <Card title="Offline alerts">
        <div className="stack">
          <Alert>
            A device that stops checking in keeps showing its kiosk, so nobody in the room can
            tell. This posts to a webhook when a device goes quiet, and again when it comes back.
            Leave the URL empty to turn alerting off.
          </Alert>

          <div className="field">
            <label className="form-label" htmlFor="hook-url">Webhook URL</label>
            <input id="hook-url" value={url} onChange={(e) => setUrl(e.target.value)}
              placeholder="https://hooks.slack.com/services/…" />
            <div className="small subtle" style={{ marginTop: 4 }}>
              The payload includes a plain <span className="mono">text</span> field, so Slack,
              Discord and Mattermost render it without any extra setup.
            </div>
          </div>

          <div className="field">
            <label className="form-label" htmlFor="hook-mins">Alert after (minutes of silence)</label>
            <input id="hook-mins" value={minutes} onChange={(e) => setMinutes(e.target.value)}
              style={{ width: 120 }} />
            <div className="small subtle" style={{ marginTop: 4 }}>
              Deliberately longer than the Online badge, which flips after 3 minutes: a reboot or
              a brief Wi-Fi drop should not page anyone. 15 is a reasonable default.
            </div>
          </div>

          {/* wrap: on a phone the two buttons and the status badge do not fit on
              one line, and without it the badge was squeezed to 24px with its
              text running under the button beside it. */}
          <div className="flex" style={{ gap: 8 }}>
            <button className="btn" onClick={save} disabled={busy}>
              <IconSave />{busy ? "Saving…" : "Save"}
            </button>
            <button className="btn outline" onClick={sendTest} disabled={testing || !settings.enabled}
              title={settings.enabled ? "Post a sample alert now" : "Save a webhook URL first"}>
              {testing ? "Sending…" : "Send test alert"}
            </button>
            <span className={"badge " + (settings.enabled ? "on" : "off")} style={{ alignSelf: "center" }}>
              <span className="dot" />{settings.enabled ? "Alerting on" : "Alerting off"}
            </span>
          </div>
        </div>
    </Card>
  );
}

/* ── Offline alerts end ─────────────────────────────────────────────────── */

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
        <table className="stacked">
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
                  <td data-label="User">
                    <div className="flex oneline" style={{ gap: 10 }}>
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
                  <td data-label="Role"><RoleBadge role={u.role} /></td>
                  <td className="small subtle nowrap" data-label="Added">
                    {u.created_at ? new Date(u.created_at).toLocaleDateString() : <span className="muted">—</span>}
                  </td>
                  <td className="cell-actions">
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
  { id: "devices", path: "devices", icon: IconDevices, txt: "Devices", title: "Devices",
    desc: "Every enrolled device, its live status, and remote controls" },
  { id: "groups", path: "groups", icon: IconGroups, txt: "Groups", title: "Policy groups",
    desc: "Define one policy and apply it to a whole set of devices" },
  { id: "notifications", path: "activity", icon: IconBell, txt: "Activity", title: "Activity",
    desc: "What the fleet has done, and who to tell when a device goes quiet" },
  { id: "enroll", path: "enroll", icon: IconEnroll, txt: "Enroll", title: "Enroll a device",
    desc: "Bring a new device under management over ADB" },
  { id: "apks", path: "packages", icon: IconPackage, txt: "Packages", title: "App packages",
    desc: "Upload APKs and push silent installs to your fleet" },
  { id: "files", path: "files", icon: IconFile, txt: "Files", title: "File library",
    desc: "Send documents to every device's inbox folder" },
  { id: "appupdate", path: "app-update", icon: IconUpload, txt: "App update", title: "Ali MDM app update",
    desc: "Push a new build of Ali MDM itself to your devices over the air" },
  // menu: reached from the account dropdown in the header rather than the
  // sidebar. They stay in NAV so the header title and page description still
  // resolve by view id.
  { id: "profile", path: "profile", icon: IconUser, txt: "Profile", title: "Your profile", menu: true,
    desc: "Update your details and change your password" },
  { id: "users", path: "users", icon: IconUsers, txt: "Users", title: "User accounts", menu: true,
    adminOnly: true, desc: "Who can sign in to this console, and what they may do" },
];

/* ── Routing ─────────────────────────────────────────────────────────────────
   Each page has its own URL, so a page can be bookmarked, sent to a colleague,
   and reloaded where it was rather than always landing on Devices.

   The History API directly, rather than a router: there are nine flat pages
   and no nested or parameterised routes, so a dependency would only wrap what
   is written here in three lines. */

const HOME = NAV[0];

/** The page a URL names — and the device, where it names one. */
function parseRoute(pathname) {
  const parts = pathname.replace(/^\/+|\/+$/g, "").split("/");
  const page = NAV.find((n) => n.path === (parts[0] || "").toLowerCase()) || HOME;
  // /devices/<id> is the only route with anything after the page name. The id
  // is opaque, so it is taken as given rather than checked here; the page says
  // so plainly if the server does not know it.
  const deviceId = page.id === "devices" && parts[1] ? decodeURIComponent(parts[1]) : "";
  return { view: page.id, deviceId };
}

function pathForView(id) {
  return "/" + ((NAV.find((n) => n.id === id) || HOME).path);
}
function pathForDevice(deviceId) {
  return "/devices/" + encodeURIComponent(deviceId);
}
function pathForRoute({ view, deviceId }) {
  return deviceId ? pathForDevice(deviceId) : pathForView(view);
}

/* A real link that navigates in place: href so the browser's own affordances
   work — ctrl-click, middle-click, "copy link address" — with only a plain
   left click taken over. */
function linkTo(path, navigate) {
  return {
    href: path,
    onClick: (e) => {
      if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
      e.preventDefault();
      navigate(path);
    },
  };
}

function Shell({ onSignOut }) {
  const [route, setRoute] = useState(() => parseRoute(window.location.pathname));
  const { view, deviceId } = route;
  const [collapsed, setCollapsed] = useState(false);
  const [me, setMe] = useState(null);
  const [menuOpen, setMenuOpen] = useState(false);
  const accountRef = useRef(null);
  const [bellOpen, setBellOpen] = useState(false);
  const bellRef = useRef(null);
  const [events, setEvents] = useState([]);
  const [unread, setUnread] = useState(0);
  const onErr = useCallback((m) => toast(m, false), []);

  // Navigate, and put it in the address bar. replace is for corrections the
  // operator did not ask for — landing on "/" or on a page they may not open —
  // which should not leave a Back step pointing at a URL that redirects again.
  const navigate = useCallback((to, replace) => {
    setRoute(parseRoute(to));
    if (to !== window.location.pathname) {
      window.history[replace ? "replaceState" : "pushState"]({}, "", to);
    }
  }, []);
  const go = useCallback((id, replace) => navigate(pathForView(id), replace), [navigate]);

  // Back and forward move between pages, as anyone would expect of real URLs.
  useEffect(() => {
    const onPop = () => setRoute(parseRoute(window.location.pathname));
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  // "/" and any unknown path show Devices; say so in the bar rather than
  // leaving a URL that does not match the page.
  useEffect(() => {
    navigate(pathForRoute(parseRoute(window.location.pathname)), true);
  }, [navigate]);

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

  // An admin who demotes themselves loses the Users page under their feet, as
  // does anyone who types /users without the role for it.
  useEffect(() => {
    if (me && !nav.some((n) => n.id === view)) go("devices", true);
  }, [nav, view, me, go]);

  // A device page is named after its device, which only the page itself knows.
  // Cleared on every move so a stale name can never head the wrong page.
  const [pageTitle, setPageTitle] = useState(null);
  useEffect(() => { setPageTitle(null); }, [view, deviceId]);

  // Declared here, above the effects that depend on them: a dependency array is
  // evaluated during render, so a const declared further down would still be in
  // its temporal dead zone when the effect below reads it.
  const active = nav.find((n) => n.id === view) || nav[0];
  const title = pageTitle?.title || active.title;
  const desc = pageTitle?.desc ?? active.desc;

  // Offline count, polled here rather than on the Devices page so a silent
  // tablet is visible from wherever the operator happens to be. This is the
  // whole point: a device can stop being managed while looking perfectly fine
  // in the room, so the console has to be the thing that notices.
  const [offlineCount, setOfflineCount] = useState(0);
  useEffect(() => {
    let cancelled = false;
    const poll = () => api.listDevices()
      .then((d) => { if (!cancelled) setOfflineCount(d.filter((x) => !x.online).length); })
      .catch(() => {/* the page itself reports API errors */});
    poll();
    const t = setInterval(poll, 30000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  useEffect(() => { window.scrollTo(0, 0); }, [view, deviceId]);

  // Name the tab after the page. Bookmarks and history entries are per page
  // now, and "Ali MDM Console" nine times over tells nobody which is which.
  useEffect(() => {
    document.title = title ? title + " · Ali MDM" : "Ali MDM Console";
  }, [title]);

  // A dropdown that can only be dismissed by its own button is a trap, so close
  // on any click outside it and on Escape. Listeners exist only while one is open.
  useEffect(() => {
    if (!menuOpen && !bellOpen) return undefined;
    const onDown = (e) => {
      if (menuOpen && accountRef.current && !accountRef.current.contains(e.target)) setMenuOpen(false);
      if (bellOpen && bellRef.current && !bellRef.current.contains(e.target)) setBellOpen(false);
    };
    const onKey = (e) => { if (e.key === "Escape") { setMenuOpen(false); setBellOpen(false); } };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [menuOpen, bellOpen]);

  // The feed is polled rather than pushed: at this fleet's volume a 30s poll is
  // indistinguishable from live, and it needs no second transport to keep alive.
  useEffect(() => {
    let cancelled = false;
    const poll = () => api.listEvents(50)
      .then((r) => {
        if (cancelled) return;
        setEvents(r.events || []);
        setUnread(r.unread || 0);
      })
      .catch(() => {/* the feed is not worth interrupting anyone over */});
    poll();
    const t = setInterval(poll, 30000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  // Opening the bell clears the badge, marking read only as far as the newest
  // entry actually rendered — anything arriving mid-request stays unread.
  const openBell = () => {
    const next = !bellOpen;
    setBellOpen(next);
    setMenuOpen(false);
    if (next && events.length > 0 && unread > 0) {
      const newest = events[0].id;
      setUnread(0);
      api.markEventsRead(newest).catch(() => setUnread(unread));
    }
  };

  if (!me) {
    return (
      <div className="login-wrap">
        <span className="muted small">Loading console…</span>
      </div>
    );
  }

  const sidebarNav = nav.filter((n) => !n.menu);
  const menuNav = nav.filter((n) => n.menu);

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
          {sidebarNav.map((n) => {
            const Icon = n.icon;
            return (
              <a key={n.id} className={"navbtn" + (view === n.id ? " active" : "")}
                {...linkTo(pathForView(n.id), navigate)} title={n.txt}
                aria-current={view === n.id ? "page" : undefined}>
                <span className="navbtn-icon"><Icon /></span>
                <span className="navbtn-title">{n.txt}</span>
                {n.id === "devices" && offlineCount > 0 && (
                  <span className="badge off" title={`${offlineCount} device(s) not checking in`}
                    style={{ marginLeft: "auto", fontSize: "0.6875rem", padding: "1px 7px" }}>
                    {offlineCount}
                  </span>
                )}
              </a>
            );
          })}
        </nav>

      </aside>

      <div className="wrapper">
        <header className="header">
          <div className="container-fixed header-inner">
            <h1 className="header-title">{title}</h1>
            <div className="header-tools">
            <div className="header-bell" ref={bellRef}>
              <button className="bell-btn" aria-haspopup="menu" aria-expanded={bellOpen}
                aria-label={unread > 0 ? `Notifications (${unread} unread)` : "Notifications"}
                title="Notifications" onClick={openBell}>
                <IconBell />
                {unread > 0 && <span className="bell-dot">{unread > 99 ? "99+" : unread}</span>}
              </button>
              {bellOpen && (
                <div className="bell-panel" role="menu">
                  <div className="bell-panel-head">Notifications</div>
                  <div className="bell-list">
                    {events.length === 0 ? (
                      <div className="bell-empty">Nothing has happened yet.</div>
                    ) : events.map((ev) => (
                      <div key={ev.id} className={"bell-item sev-" + (ev.severity || "info")}>
                        <span className="bell-item-dot" />
                        <span className="bell-item-body">
                          <span className="bell-item-summary">{ev.summary}</span>
                          <span className="bell-item-meta">
                            {ev.actor} · {timeAgo(ev.at) || "just now"}
                          </span>
                        </span>
                      </div>
                    ))}
                  </div>
                  <button className="bell-all" role="menuitem"
                    onClick={() => { setBellOpen(false); go("notifications"); }}>
                    See all activity
                  </button>
                </div>
              )}
            </div>

            <div className="header-account" ref={accountRef}>
              <button className="avatar header-avatar" aria-haspopup="menu"
                aria-expanded={menuOpen} aria-label="Account menu"
                title={me.name || me.email}
                onClick={() => setMenuOpen((o) => !o)}>
                {(me.name || me.email || "?").slice(0, 1)}
              </button>
              {menuOpen && (
                <div className="account-menu" role="menu">
                  <div className="account-menu-head">
                    <span className="account-menu-name">{me.name || me.email}</span>
                    {me.name && <span className="account-menu-sub">{me.email}</span>}
                    <span className="account-menu-sub">
                      {me.role === "admin" ? "Administrator" : "Operator"}
                    </span>
                  </div>
                  <div className="account-menu-sep" />
                  {menuNav.map((n) => {
                    const Icon = n.icon;
                    return (
                      <a key={n.id} role="menuitem"
                        className={"account-menu-item" + (view === n.id ? " active" : "")}
                        {...linkTo(pathForView(n.id), navigate)}
                        onClickCapture={() => setMenuOpen(false)}>
                        <Icon />{n.txt}
                      </a>
                    );
                  })}
                  <div className="account-menu-sep" />
                  <button role="menuitem" className="account-menu-item danger"
                    onClick={() => { setMenuOpen(false); onSignOut(); }}>
                    <IconSignOut />Sign out
                  </button>
                </div>
              )}
            </div>
            </div>
          </div>
        </header>

        <main className="content">
          <div className="container-fixed">
            <div className="toolbar">
              <div className="toolbar-heading">
                {deviceId && (
                  <a className="back-link" {...linkTo(pathForView("devices"), navigate)}>
                    <IconChevronLeft />All devices
                  </a>
                )}
                {desc && <div className="toolbar-desc">{desc}</div>}
              </div>
            </div>

            {view === "devices" && (deviceId
              ? <DeviceDetail key={deviceId} deviceId={deviceId} me={me} onErr={onErr}
                  onTitle={setPageTitle} navigate={navigate} />
              : <Devices onErr={onErr} navigate={navigate} />)}
            {view === "groups" && <Groups onErr={onErr} />}
            {view === "enroll" && <Enroll onErr={onErr} />}
            {view === "apks" && <APKs onErr={onErr} />}
            {view === "files" && <Files onErr={onErr} />}
            {view === "appupdate" && <AppUpdate onErr={onErr} />}
            {view === "profile" && <Profile me={me} onErr={onErr} onMeChange={setMe} />}
            {view === "notifications" && <Notifications me={me} onErr={onErr} />}
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
