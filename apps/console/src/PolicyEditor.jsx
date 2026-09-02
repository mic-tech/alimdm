import { useState, useEffect } from "react";
import { api } from "./api";
import { POLICY_TABS, POLICY_FIELDS } from "./policySchema";
import {
  IconHome, IconGrid, IconMonitor, IconShield, IconKey, IconSliders,
  IconPlus, IconTrash, IconSave, IconWarning, IconInfo,
} from "./icons.jsx";

// The schema names icons after the app's icon font; map them onto our set.
const ICONS = {
  home: IconHome,
  "view-dashboard": IconGrid,
  monitor: IconMonitor,
  "shield-lock": IconShield,
  key: IconKey,
  cog: IconSliders,
};

// Read a dot-path from an object (returns undefined if any segment is missing).
function getPath(obj, path) {
  return path.split(".").reduce((o, k) => (o == null ? undefined : o[k]), obj);
}
// Set a dot-path in a (deep-cloned) object, creating intermediate objects.
function setPath(obj, path, value) {
  const keys = path.split(".");
  let cur = obj;
  for (let i = 0; i < keys.length - 1; i++) {
    if (typeof cur[keys[i]] !== "object" || cur[keys[i]] === null) cur[keys[i]] = {};
    cur = cur[keys[i]];
  }
  cur[keys[keys.length - 1]] = value;
}
// Remove a dot-path.
function delPath(obj, path) {
  const keys = path.split(".");
  let cur = obj;
  for (let i = 0; i < keys.length - 1; i++) {
    if (cur[keys[i]] == null) return;
    cur = cur[keys[i]];
  }
  delete cur[keys[keys.length - 1]];
}

/* Managed apps: a compact table of packages with per-app flags. */
function ManagedAppsControl({ value, onChange }) {
  const apps = Array.isArray(value) ? value : [];
  const [newPkg, setNewPkg] = useState("");
  const [newName, setNewName] = useState("");

  function addApp() {
    if (!newPkg.trim()) return;
    onChange([...apps, {
      packageName: newPkg.trim(),
      displayName: newName.trim() || newPkg.trim(),
      showOnHomeScreen: true,
      launchOnBoot: false,
      keepAlive: false,
      autoInstall: false,
    }]);
    setNewPkg(""); setNewName("");
  }
  function removeApp(i) { onChange(apps.filter((_, j) => j !== i)); }
  function setFlag(i, key, val) {
    const next = [...apps];
    next[i] = { ...next[i], [key]: val };
    onChange(next);
  }

  return (
    <div style={{ width: "100%" }}>
      {apps.length > 0 && (
        <div style={{ border: "1px solid var(--border)", borderRadius: "calc(var(--radius) - 2px)", overflow: "hidden", marginBottom: 10 }}>
          <div className="table-scroll">
            <table className="stacked">
              <thead>
                <tr>
                  <th>Package</th>
                  <th style={{ textAlign: "center" }}>Home screen</th>
                  <th style={{ textAlign: "center" }}>Auto-install</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {apps.map((a, i) => (
                  <tr key={a.packageName}>
                    <td data-label="Package">
                      <div className="mono strong">{a.packageName}</div>
                      {a.displayName && a.displayName !== a.packageName && (
                        <div className="small muted">{a.displayName}</div>
                      )}
                    </td>
                    <td style={{ textAlign: "center" }} data-label="Home screen">
                      <label className="toggle sm">
                        <input type="checkbox" checked={!!a.showOnHomeScreen}
                          onChange={(e) => setFlag(i, "showOnHomeScreen", e.target.checked)} />
                        <span className="track" />
                      </label>
                    </td>
                    <td style={{ textAlign: "center" }} data-label="Auto-install">
                      <label className="toggle sm">
                        <input type="checkbox" checked={!!a.autoInstall}
                          onChange={(e) => setFlag(i, "autoInstall", e.target.checked)} />
                        <span className="track" />
                      </label>
                    </td>
                    <td style={{ textAlign: "right" }} className="cell-actions">
                      <button className="btn danger-outline sm icon" title="Remove app"
                        onClick={() => removeApp(i)}><IconTrash /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      <div className="flex">
        <input placeholder="com.example.app" value={newPkg}
          onChange={(e) => setNewPkg(e.target.value)} style={{ flex: 2, minWidth: 180 }} />
        <input placeholder="Display name (optional)" value={newName}
          onChange={(e) => setNewName(e.target.value)} style={{ flex: 1, minWidth: 140 }} />
        <button className="btn outline" onClick={addApp}><IconPlus />Add app</button>
      </div>
    </div>
  );
}

/**
 * Unit conversion between what an administrator types and what the app stores.
 *
 * `scale` is the multiplier from displayed to stored, so brightness can be a
 * percentage here while the device keeps a 0..1 float, and a delay can be
 * minutes here while the device keeps milliseconds. Both directions are
 * rounded: 35 * 0.01 is 0.35000000000000003 in binary floating point, and that
 * is not a number to write into a device policy.
 */
const round = (n, places) => Number(n.toFixed(places));
const toStored = (v, field) => (field.scale ? round(v * field.scale, 6) : v);
const toDisplay = (v, field) => (field.scale ? round(v / field.scale, 3) : v);

/**
 * How the field behaves when it is absent from the policy.
 *
 * Absent is not the same as off: importConfig skips undefined values, so a
 * field left alone here keeps whatever the device already has, and a device
 * that has never been told uses this. Worth stating plainly — otherwise the
 * only way to know what an untouched setting does is to read the app.
 */
function defaultLabel(field) {
  const d = field.default;
  if (d === undefined) return null;
  if (field.type === "bool") return d ? "On" : "Off";
  // A default of "" or [] adds nothing: "not set" already says the field is
  // empty, and "devices use empty" is a sentence that wastes a line.
  if (d === "" || (Array.isArray(d) && d.length === 0)) return null;
  if (Array.isArray(d)) return JSON.stringify(d);
  if (field.type === "select") {
    const opt = (field.options || []).find((o) => o.value === d);
    return opt ? opt.label : String(d);
  }
  const shown = toDisplay(d, field);
  // "50%" reads as one value; "50 %" reads as two. Percent sits tight against
  // the number, every other unit is a word and takes a space.
  if (!field.unit) return String(shown);
  return field.unit === "%" ? shown + "%" : shown + " " + field.unit;
}

/**
 * A stored value that is not one of the offered options.
 *
 * Older versions of this editor wrote option values the app has never heard
 * of, and nothing anywhere rejects them: the device stores the string, it
 * matches no branch, and the setting quietly does something other than what
 * the console said. Left alone, such a field renders as a pill group with
 * nothing selected, which reads as "unset" and hides the problem. Say it
 * instead — the value is on screen and the fix is to pick a real one.
 */
function UnknownValueNote({ field, value }) {
  if (field.type !== "select" || value === undefined || value === null || value === "") return null;
  if ((field.options || []).some((o) => o.value === value)) return null;
  return (
    <div className="form-desc unknown-note">
      This policy holds <span className="mono">{String(value)}</span>, which is not one of the
      values the app understands — it is ignoring the setting. Choose one above to replace it.
    </div>
  );
}

/* Shown only while a field is unset — once it has a value, the note is noise. */
function DefaultNote({ field, value }) {
  if (value !== undefined && value !== null && value !== "") return null;
  const label = defaultLabel(field);
  if (label === null) return null;
  return <div className="form-desc default-note">Not set — devices use <strong>{label}</strong></div>;
}

/* One field, rendered as a Metronic settings row: label column + control column. */
function FieldControl({ field, value, onChange }) {
  const label = <label className="form-label">{field.label}</label>;
  const help = field.help ? <div className="form-desc">{field.help}</div> : null;
  const unset = value === undefined || value === null || value === "";

  // Booleans read best as a full-width switch row with the help text inline.
  if (field.type === "bool") {
    return (
      <div className="toggle-row">
        <div className="toggle-text">
          <span className="toggle-label">{field.label}</span>
          {field.help && <span className="toggle-help">{field.help}</span>}
          {unset && defaultLabel(field) && (
            <span className="toggle-help default-note">
              Not set — devices use <strong>{defaultLabel(field)}</strong>
            </span>
          )}
        </div>
        <label className="toggle">
          <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} />
          <span className="track" />
        </label>
      </div>
    );
  }

  let control;
  switch (field.type) {
    case "slider": {
      const shown = unset
        ? toDisplay(field.default ?? field.min ?? 0, field)
        : toDisplay(value, field);
      control = (
        <>
          <input type="range" min={field.min ?? 0} max={field.max ?? 100} step={field.step ?? 1}
            value={shown} onChange={(e) => onChange(toStored(Number(e.target.value), field))}
            style={{ flex: 1, minWidth: 160, maxWidth: 320 }} />
          <span className={"badge mono " + (unset ? "off" : "")}>
            {shown}{field.unit || ""}
          </span>
        </>
      );
      break;
    }

    case "number":
      control = (
        <>
          <input type="number" min={field.min} max={field.max} step={field.step}
            value={unset ? "" : toDisplay(value, field)}
            placeholder={field.placeholder || (field.default !== undefined ? String(toDisplay(field.default, field)) : "")}
            onChange={(e) => onChange(e.target.value === "" ? undefined : toStored(Number(e.target.value), field))}
            style={{ width: 140 }} />
          {field.unit && <span className="unit-suffix">{field.unit}</span>}
        </>
      );
      break;

    case "select": {
      const opts = field.options || [];
      control = opts.length <= 5 ? (
        <div className="pill-group">
          {opts.map((o) => (
            <button key={o.value} type="button"
              className={"pill" + (value === o.value ? " active" : "")
                + (unset && o.value === field.default ? " is-default" : "")}
              title={o.value === field.default ? "Device default" : undefined}
              onClick={() => onChange(o.value)}>{o.label}</button>
          ))}
        </div>
      ) : (
        <select value={value ?? ""} onChange={(e) => onChange(e.target.value || undefined)}
          style={{ maxWidth: 320 }}>
          <option value="" disabled>— Select —</option>
          {opts.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}{o.value === field.default ? " (default)" : ""}
            </option>
          ))}
        </select>
      );
      break;
    }

    case "textarea":
      control = (
        <textarea value={value ?? ""} rows={3}
          placeholder={field.placeholder || (typeof field.default === "string" ? field.default : "")}
          onChange={(e) => onChange(e.target.value)} style={{ width: "100%", maxWidth: 480 }} />
      );
      break;

    case "list":
      control = (
        <div style={{ width: "100%", maxWidth: 480 }}>
          {(Array.isArray(value) ? value : []).map((item, i) => (
            <div className="flex" key={i} style={{ marginBottom: 6 }}>
              <input value={item} onChange={(e) => {
                const next = [...(Array.isArray(value) ? value : [])];
                next[i] = e.target.value; onChange(next);
              }} className="grow" />
              <button className="btn danger-outline sm icon" title="Remove row" onClick={() => {
                onChange((Array.isArray(value) ? value : []).filter((_, j) => j !== i));
              }}><IconTrash /></button>
            </div>
          ))}
          <button className="btn outline sm"
            onClick={() => onChange([...(Array.isArray(value) ? value : []), ""])}>
            <IconPlus />Add row
          </button>
        </div>
      );
      break;

    case "managedApps":
      control = <ManagedAppsControl value={value} onChange={onChange} />;
      break;

    default:
      control = (
        <input type={field.danger ? "password" : "text"} value={value ?? ""}
          placeholder={field.placeholder || (typeof field.default === "string" ? field.default : "")}
          onChange={(e) => onChange(e.target.value || undefined)}
          style={{ flex: 1, maxWidth: 380 }} />
      );
  }

  // Wide controls get the label stacked above; the rest use the label column.
  const stacked = field.type === "managedApps" || field.type === "list" || field.type === "textarea";
  if (stacked) {
    return (
      <div className="field">
        {label}
        {control}
        {help}
        <DefaultNote field={field} value={value} />
        <UnknownValueNote field={field} value={value} />
        {field.danger && <DangerNote />}
      </div>
    );
  }
  return (
    <div className="form-row">
      {label}
      <div className="form-control">
        <div className="control-line">{control}</div>
        {help}
        <DefaultNote field={field} value={value} />
        <UnknownValueNote field={field} value={value} />
        {field.danger && <DangerNote />}
      </div>
    </div>
  );
}

function DangerNote() {
  return (
    <span className="badge warning" title="Powerful setting — use with care">
      <IconWarning style={{ width: 12, height: 12 }} />Powerful
    </span>
  );
}

// Conditional visibility: field path → { path, value } that must match.
const SHOW_IF = {
  // General tab — media player fields
  "general.mediaPlayer.items":          { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.autoPlay":       { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.loop":           { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.shuffle":        { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.imageDuration":  { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.showControls":   { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.mute":           { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.fitMode":        { path: "general.displayMode", value: "media_player" },
  "general.mediaPlayer.bgColor":        { path: "general.displayMode", value: "media_player" },
  // General tab — webview fields
  "general.url":                        { path: "general.displayMode", value: "webview" },
  "general.httpBasicAuth.username":     { path: "general.displayMode", value: "webview" },
  "general.urlRotation.enabled":        { path: "general.displayMode", value: "webview" },
  "general.urlRotation.list":            [{ path: "general.displayMode", value: "webview" }, { path: "general.urlRotation.enabled", value: true }],
  "general.urlRotation.interval":        [{ path: "general.displayMode", value: "webview" }, { path: "general.urlRotation.enabled", value: true }],
  "general.urlPlanner.enabled":         { path: "general.displayMode", value: "webview" },
  "general.inactivityReturn.enabled":   { path: "general.displayMode", value: "webview" },
  "general.inactivityReturn.delay":      [{ path: "general.displayMode", value: "webview" }, { path: "general.inactivityReturn.enabled", value: true }],
  "general.inactivityReturn.resetOnNav": [{ path: "general.displayMode", value: "webview" }, { path: "general.inactivityReturn.enabled", value: true }],
  "general.inactivityReturn.clearCache": [{ path: "general.displayMode", value: "webview" }, { path: "general.inactivityReturn.enabled", value: true }],
  "general.autoReload":                 { path: "general.displayMode", value: "webview" },
  "general.pdfViewerEnabled":           { path: "general.displayMode", value: "webview" },
  "general.printEnabled":               { path: "general.displayMode", value: "webview" },
  "general.printPaperSize":             { path: "general.displayMode", value: "webview" },
  "general.webviewBackButton.enabled":  { path: "general.displayMode", value: "webview" },
  "general.webviewBackButton.xPercent":  [{ path: "general.displayMode", value: "webview" }, { path: "general.webviewBackButton.enabled", value: true }],
  "general.webviewBackButton.yPercent":  [{ path: "general.displayMode", value: "webview" }, { path: "general.webviewBackButton.enabled", value: true }],
  // General tab — external app fields
  "general.externalApp.mode":           { path: "general.displayMode", value: "external_app" },
  "general.externalApp.package":        { path: "general.externalApp.mode", value: "single" },
  "general.managedApps":                { path: "general.externalApp.mode", value: "multi" },
  // Display tab — webview fields
  "display.zoom.level":                 { path: "general.displayMode", value: "webview" },
  "display.zoom.disableUserZoom":       { path: "general.displayMode", value: "webview" },
  "display.customUserAgent":            { path: "general.displayMode", value: "webview" },
  "display.keyboardMode":               { path: "general.displayMode", value: "webview" },
  // Security tab — webview fields
  "security.urlFilter.enabled":         { path: "general.displayMode", value: "webview" },
  "security.urlFilter.mode":             [{ path: "general.displayMode", value: "webview" }, { path: "security.urlFilter.enabled", value: true }],
  // Security tab — external app fields
  "security.autoRelaunchApp":           { path: "general.displayMode", value: "external_app" },
  "security.backButtonMode":            { path: "general.displayMode", value: "external_app" },
  // Security tab — button position (already has showIf in schema)
  "display.autoBrightness.min":          [{ path: "display.autoBrightness.enabled", value: true }],
  "display.autoBrightness.max":          [{ path: "display.autoBrightness.enabled", value: true }],
  "display.autoBrightness.offset":       [{ path: "display.autoBrightness.enabled", value: true }],
  "display.screensaver.type":            [{ path: "display.screensaver.enabled", value: true }],
  "display.screensaver.inactivityEnabled": [{ path: "display.screensaver.enabled", value: true }],
  "display.screensaver.brightness":      [{ path: "display.screensaver.enabled", value: true }],
  "display.screenScheduler.wakeOnTouch": [{ path: "display.screenScheduler.enabled", value: true }],
  "display.statusBar.showBattery":       [{ path: "display.statusBar.enabled", value: true }],
  "display.statusBar.showWifi":          [{ path: "display.statusBar.enabled", value: true }],
  "display.statusBar.showTime":          [{ path: "display.statusBar.enabled", value: true }],
  "display.statusBar.theme":             [{ path: "display.statusBar.enabled", value: true }],
  "security.urlFilter.list":             [{ path: "security.urlFilter.enabled", value: true }],
  "security.lockscreen.wifi":            [{ path: "security.lockscreen.enabled", value: true }],
  "security.lockscreen.brightness":      [{ path: "security.lockscreen.enabled", value: true }],
  "security.lockscreen.emergencyCall":   [{ path: "security.lockscreen.enabled", value: true }],
  "advanced.restApi.port":               [{ path: "advanced.restApi.enabled", value: true }],
  "advanced.restApi.allowControl":       [{ path: "advanced.restApi.enabled", value: true }],
  "advanced.mqtt.brokerUrl":             [{ path: "advanced.mqtt.enabled", value: true }],
  "advanced.mqtt.port":                  [{ path: "advanced.mqtt.enabled", value: true }],
  "advanced.mqtt.username":              [{ path: "advanced.mqtt.enabled", value: true }],
  "advanced.mqtt.clientId":              [{ path: "advanced.mqtt.enabled", value: true }],
  "advanced.mqtt.baseTopic":             [{ path: "advanced.mqtt.enabled", value: true }],
  // Newly exposed settings, gated on what they depend on.
  "general.externalApp.testMode":        { path: "general.displayMode", value: "external_app" },
  "general.inactivityReturn.scrollTop":  [{ path: "general.displayMode", value: "webview" }, { path: "general.inactivityReturn.enabled", value: true }],
  "general.mediaPlayer.transition":         { path: "general.displayMode", value: "media_player" },
  "general.urlPlanner.events":           [{ path: "general.displayMode", value: "webview" }, { path: "general.urlPlanner.enabled", value: true }],
  "display.zoom.mode":                   { path: "general.displayMode", value: "webview" },
  "display.autoBrightness.updateInterval": { path: "display.autoBrightness.enabled", value: true },
  "display.screensaver.videoItems":      [{ path: "display.screensaver.enabled", value: true }, { path: "display.screensaver.type", value: "video" }],
  "display.screensaver.videoLoop":       [{ path: "display.screensaver.enabled", value: true }, { path: "display.screensaver.type", value: "video" }],
  "display.screensaver.url":             [{ path: "display.screensaver.enabled", value: true }, { path: "display.screensaver.type", value: "url" }],
  "display.screensaver.inactivityDelay": [{ path: "display.screensaver.enabled", value: true }, { path: "display.screensaver.inactivityEnabled", value: true }],
  "display.screenScheduler.rules":       { path: "display.screenScheduler.enabled", value: true },
  "display.statusBar.onOverlay":         { path: "display.statusBar.enabled", value: true },
  "display.statusBar.onReturn":          { path: "display.statusBar.enabled", value: true },
  "display.statusBar.showBluetooth":     { path: "display.statusBar.enabled", value: true },
  "display.statusBar.showVolume":        { path: "display.statusBar.enabled", value: true },
  "display.motionDetection.sensitivity":      { path: "display.motionDetection.enabled", value: true },
  "display.motionDetection.cameraPosition":   { path: "display.motionDetection.enabled", value: true },
  "display.motionDetection.proximityEnabled": { path: "display.motionDetection.enabled", value: true },
  "security.backButtonTimerDelay":       [{ path: "general.displayMode", value: "external_app" }, { path: "security.backButtonMode", value: "timer" }],
  "security.blockingOverlays.regions":   { path: "security.blockingOverlays.enabled", value: true },
  "security.urlFilter.showFeedback":     [{ path: "general.displayMode", value: "webview" }, { path: "security.urlFilter.enabled", value: true }],
  "security.lockscreen.audio":           { path: "security.lockscreen.enabled", value: true },
  "security.lockscreen.bluetooth":       { path: "security.lockscreen.enabled", value: true },
  "security.lockscreen.flashlight":      { path: "security.lockscreen.enabled", value: true },
  "security.lockscreen.rotationLock":    { path: "security.lockscreen.enabled", value: true },
  "advanced.mqtt.discoveryPrefix":       { path: "advanced.mqtt.enabled", value: true },
  "advanced.mqtt.statusInterval":        { path: "advanced.mqtt.enabled", value: true },
  "advanced.mqtt.deviceName":            { path: "advanced.mqtt.enabled", value: true },
  "advanced.mqtt.allowControl":          { path: "advanced.mqtt.enabled", value: true },
  "advanced.mqtt.motionAlwaysOn":        { path: "advanced.mqtt.enabled", value: true },
  // Matches the app: the Show Button switch lives inside {returnMode === 'button'}.
  "security.overlayButtonVisible":       { path: "security.returnMode", value: "button" },
  // The app renders motion detection inside the Screensaver section, whose body
  // is gated on the screensaver being enabled.
  "display.motionDetection.enabled":     { path: "display.screensaver.enabled", value: true },
  "security.returnButtonPosition":      { path: "security.returnMode", value: "button" },
};

// Index every field by path so a condition can ask whether the field it depends
// on is itself visible.
const FIELDS_BY_PATH = new Map(POLICY_FIELDS.map((f) => [f.path, f]));

function conditionsFor(field) {
  const raw = field.showIf ?? SHOW_IF[field.path];
  if (!raw) return [];
  return Array.isArray(raw) ? raw : [raw];
}

/**
 * Whether a field should be shown.
 *
 * Conditions are ANDed, a condition's `value` may be an array meaning "any of",
 * and visibility is **transitive**: a field is hidden when the field it depends
 * on is itself hidden. Without that, a setting could surface under a mode it
 * has nothing to do with — the package picker, for instance, was gated only on
 * externalApp.mode, so a leftover value from a previous mode kept it on screen
 * in Web view.
 *
 * `seen` guards against a mis-specified cycle in the schema turning into
 * infinite recursion.
 */
function isFieldVisible(field, config, seen = new Set()) {
  if (seen.has(field.path)) return true;
  seen.add(field.path);

  for (const cond of conditionsFor(field)) {
    const actual = getPath(config, cond.path);
    const want = Array.isArray(cond.value) ? cond.value : [cond.value];
    if (!want.includes(actual)) return false;

    const parent = FIELDS_BY_PATH.get(cond.path);
    if (parent && !isFieldVisible(parent, config, seen)) return false;
  }
  return true;
}

// The schema-driven policy editor. Mirrors the app's own settings tabs/sections.
export default function PolicyEditor({ groupId, onErr, onClose }) {
  const [config, setConfig] = useState(null);
  const [tab, setTab] = useState(POLICY_TABS[0].id);
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [version, setVersion] = useState(null);

  useEffect(() => {
    if (!groupId) return;
    setConfig(null); setDirty(false);
    api.getGroup(groupId).then((g) => {
      let c = {};
      try { c = JSON.parse(g.config || "{}"); } catch { /* a group whose config will not parse starts empty */ }
      setConfig(c);
      setVersion(g.config_version);
    }).catch(onErr);
  }, [groupId, onErr]);

  if (!config) {
    return (
      <div className="card">
        <div className="card-content"><span className="muted small">Loading policy…</span></div>
      </div>
    );
  }

  function update(field, value) {
    setDirty(true);
    setConfig((prev) => {
      const next = JSON.parse(JSON.stringify(prev));
      if (value === undefined || value === "") delPath(next, field.path);
      else setPath(next, field.path, value);
      return next;
    });
  }

  async function save() {
    setBusy(true);
    try {
      await api.updateGroup(groupId, { config });
      const g = await api.getGroup(groupId);
      setConfig(JSON.parse(g.config || "{}"));
      setVersion(g.config_version);
      setDirty(false);
      const el = document.getElementById("toast");
      if (el) {
        el.textContent = `Policy saved (v${g.config_version}). Devices re-sync within ~30s.`;
        el.className = "toast ok"; el.style.display = "flex";
        setTimeout(() => (el.style.display = "none"), 3600);
      }
    } catch (e) { onErr(e.message); }
    setBusy(false);
  }

  const fieldsInTab = POLICY_FIELDS.filter((f) => f.tab === tab);
  const activeTab = POLICY_TABS.find((t) => t.id === tab);

  return (
    <div className="card">
      <div className="card-header">
        <div className="flex" style={{ gap: 10 }}>
          <h3 className="card-title">Policy — {groupId}</h3>
          {version != null && <span className="badge off">v{version}</span>}
          {dirty && <span className="badge warning">Unsaved changes</span>}
        </div>
        <div className="card-toolbar">
          {onClose && <button className="btn outline sm" onClick={onClose}>Close</button>}
          <button className="btn sm" disabled={busy || !dirty} onClick={save}>
            <IconSave />{busy ? "Saving…" : "Save policy"}
          </button>
        </div>
      </div>

      <div style={{ paddingInline: 20 }}>
        <div className="tabs">
          {POLICY_TABS.map((t) => {
            const Icon = ICONS[t.icon] || IconSliders;
            return (
              <button key={t.id} onClick={() => setTab(t.id)}
                className={"tab" + (tab === t.id ? " active" : "")}>
                <Icon /><span>{t.label}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="card-content">
        {activeTab.sections.map((sectionTitle) => {
          const sectionFields = fieldsInTab.filter((f) => f.section === sectionTitle);
          if (sectionFields.length === 0) return null;

          const visibleFields = sectionFields.filter((f) => isFieldVisible(f, config));
          if (visibleFields.length === 0) return null;

          return (
            <section key={sectionTitle} style={{ marginBottom: 28 }}>
              <h4 style={{
                fontSize: "0.6875rem", fontWeight: 600, textTransform: "uppercase",
                letterSpacing: "0.05em", color: "var(--muted-foreground)",
                paddingBottom: 10, marginBottom: 16, borderBottom: "1px solid var(--border)",
              }}>{sectionTitle}</h4>
              <div className="field-grid">
                {visibleFields.map((f) => (
                  <FieldControl key={f.path} field={f} value={getPath(config, f.path)}
                    onChange={(v) => update(f, v)} />
                ))}
              </div>
            </section>
          );
        })}

        {fieldsInTab.length === 0 && (
          <div className="empty">
            <div className="empty-icon"><IconInfo /></div>
            <div className="empty-title">Nothing to configure here</div>
            <div className="empty-desc">This tab has no settings for the current display mode.</div>
          </div>
        )}
      </div>

      <div className="card-footer" style={{ justifyContent: "space-between" }}>
        <span className="small muted">
          Changes apply to every device in <span className="mono">{groupId}</span> on its next sync.
        </span>
        <button className="btn" disabled={busy || !dirty} onClick={save}>
          <IconSave />{busy ? "Saving…" : "Save policy"}
        </button>
      </div>
    </div>
  );
}
