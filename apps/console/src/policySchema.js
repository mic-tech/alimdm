// Policy schema for the Ali MDM Console.
//
// Each field describes ONE setting in a group's policy (the config JSON the
// app applies via applyCloudConfig). The editor auto-renders the right control
// from `type` and writes the value at `path` (dot-separated, e.g.
// "display.screensaver.type"). The layout mirrors the Ali MDM app's own
// settings tabs/sections so a setting means the same thing in both places.
//
// Adding a new setting = add one entry here (no component changes).
//
// THREE RULES, learned the hard way:
//
//  1. `options[].value` and the units of every number MUST match what the app
//     actually stores (StorageService.exportConfig / importConfig and the
//     screens under src/screens/settings/tabs). A value the app doesn't
//     recognise is not rejected anywhere — it is written to the device and
//     silently falls through every comparison, so the setting just stops
//     working. Check the app before inventing an option value.
//
//  2. `default` is what a device does when this field is ABSENT from the
//     policy. importConfig skips undefined/null, so leaving a field unset in
//     the console does not reset the device — it leaves whatever the device
//     already has, and a fresh device uses the default recorded here. The
//     editor shows it so an administrator can see the consequence of not
//     touching a setting. Keep it in sync with exportConfig's fallbacks.
//
//     Three settings the app round-trips but never reads are deliberately NOT
//     here: display.screensaver.delay, display.motionDetection.delay and
//     security.overlayButtonPosition. They survive an export/import cycle but
//     nothing in the app or the native code consults them (the return button is
//     positioned by security.returnButtonPosition instead). A control that
//     silently does nothing is worse than a missing one — it costs an
//     administrator the time to set it and then to work out why it had no
//     effect. Add them back if the app ever grows a reader.
//
//  3. `help` is written for whoever is configuring a group of school devices,
//     not for whoever wrote the code. Where the app's own settings screen has
//     a hint, say the same thing here so the two do not drift apart.

/**
 * @typedef {"bool"|"number"|"slider"|"text"|"textarea"|"select"|"list"|"managedApps"} FieldType
 * @typedef {Object} PolicyField
 * @property {string} path          dot path into the config object
 * @property {string} label
 * @property {FieldType} type
 * @property {string} section       section title (mirrors the app)
 * @property {string} tab           tab id: general|dashboard|display|security|advanced
 * @property {{value:string,label:string}[]} [options]  for select
 * @property {number} [min]         in DISPLAYED units
 * @property {number} [max]         in DISPLAYED units
 * @property {number} [step]        in DISPLAYED units
 * @property {number} [scale]       displayed x scale = stored. Use when the app
 *                                  stores a different unit than the one an
 *                                  administrator should be typing: brightness
 *                                  is a 0..1 float on the device but a
 *                                  percentage here (scale 0.01), delays are
 *                                  milliseconds on the device but seconds or
 *                                  minutes here (scale 1000 / 60000).
 * @property {string} [unit]        suffix shown next to the value
 * @property {string} [placeholder]
 * @property {string} [help]        helper text under the field
 * @property {boolean} [danger]     render a warning (powerful/sensitive setting)
 * @property {*} [default]          what the device does when the field is absent
 * @property {{path:string,value:*}|{path:string,value:*}[]} [showIf]
 * @typedef {Object} PolicyTab
 * @property {string} id
 * @property {string} label
 * @property {string} icon
 * @property {string[]} sections    ordered section titles shown in this tab
 */

// ── Tabs (mirror the app's settings tabs, same order) ───────────────────────
// A section that is not listed here is not rendered at all, so every `section`
// used below must appear in exactly one tab.
export const POLICY_TABS = [
  { id: "general",   label: "General",   icon: "home",
    sections: ["Display Mode", "URL to Display", "Website Authentication", "URL Rotation", "URL Planner",
               "Media Playlist", "Playback", "Display Options", "App Mode", "Application", "Applications",
               "Password", "Inactivity Return", "Auto Reload", "PDF Viewer", "Printing",
               "Web Navigation Button"] },
  { id: "dashboard", label: "Dashboard", icon: "view-dashboard",
    sections: ["Dashboard Tiles"] },
  { id: "display",   label: "Display",   icon: "monitor",
    sections: ["Brightness Control", "Manual Brightness", "Auto-Brightness", "Screen Always On",
               "Screensaver", "Screen Sleep Schedule", "System Status Bar", "Web Page Zoom",
               "User Agent", "Web Media", "Keyboard Mode"] },
  { id: "security",  label: "Security",  icon: "shield-lock",
    sections: ["Lock Mode", "Auto Launch", "Return to Settings", "Touch Blocking", "URL Filtering",
               "Back Button Behavior", "Lock Screen Controls"] },
  { id: "advanced",  label: "Advanced",  icon: "cog",
    sections: ["REST API", "MQTT"] },
];

// ── Fields ───────────────────────────────────────────────────────────────────
export const POLICY_FIELDS = [
  // ══ GENERAL ══════════════════════════════════════════════════════════════
  { path: "general.displayMode", label: "Display mode", type: "select", tab: "general", section: "Display Mode",
    options: [
      { value: "webview", label: "Website" },
      { value: "media_player", label: "Media" },
      { value: "external_app", label: "App" },
    ],
    default: "webview",
    help: "What the device shows all day: a website, a media playlist (video/images), or an installed Android app. Most of the settings below only apply to one of the three." },

  // URL to Display
  { path: "general.url", label: "URL to display", type: "text", tab: "general", section: "URL to Display",
    placeholder: "https://example.com", default: "",
    help: "The page the kiosk opens and returns to. This URL is always allowed, even when URL filtering would block it." },

  // Website Authentication
  { path: "general.httpBasicAuth.username", label: "Username", type: "text", tab: "general", section: "Website Authentication",
    placeholder: "Leave empty to disable", default: "",
    help: "Username for sites that answer with an HTTP Basic Auth (401) challenge. The password is set on the device — it is kept in the Android Keychain and never travels in a policy." },

  // URL Rotation
  { path: "general.urlRotation.enabled", label: "Enable rotation", type: "bool", tab: "general", section: "URL Rotation",
    default: false, help: "Cycle through several URLs instead of showing one page." },
  { path: "general.urlRotation.list", label: "URLs", type: "list", tab: "general", section: "URL Rotation",
    default: [], help: "One URL per row, shown in this order." },
  { path: "general.urlRotation.interval", label: "Rotation interval", type: "number", tab: "general", section: "URL Rotation",
    min: 5, max: 86400, step: 5, unit: "s", default: 30,
    help: "Seconds each URL stays on screen before the next one. Minimum 5 seconds." },

  // URL Planner
  { path: "general.urlPlanner.enabled", label: "Enable scheduled URLs", type: "bool", tab: "general", section: "URL Planner",
    default: false, help: "Show a different URL at scheduled times — a timetable page during class hours and a notice board after, for example." },
  { path: "general.urlPlanner.events", label: "Planner events", type: "textarea", tab: "general", section: "URL Planner",
    default: [], placeholder: '[{"url":"https://…","days":[1,2,3,4,5],"start":"08:00","end":"15:00"}]',
    help: "A JSON array of scheduled URL changes, in the shape the app stores them. Easiest to build one on a device, then copy it here." },

  // Media Playlist / Playback / Display Options
  { path: "general.mediaPlayer.items", label: "Playlist items", type: "list", tab: "general", section: "Media Playlist",
    default: [], help: "One media URL per row — image or video. Played in order unless Shuffle is on." },
  { path: "general.mediaPlayer.autoPlay", label: "Auto play", type: "bool", tab: "general", section: "Playback",
    default: true, help: "Start playing as soon as the screen loads." },
  { path: "general.mediaPlayer.loop", label: "Loop playlist", type: "bool", tab: "general", section: "Playback",
    default: true, help: "Restart the playlist from the beginning when it ends." },
  { path: "general.mediaPlayer.shuffle", label: "Shuffle", type: "bool", tab: "general", section: "Playback",
    default: false, help: "Play items in random order." },
  { path: "general.mediaPlayer.mute", label: "Mute videos", type: "bool", tab: "general", section: "Playback",
    default: false, help: "Play every video without audio." },
  { path: "general.mediaPlayer.imageDuration", label: "Image duration", type: "number", tab: "general", section: "Playback",
    min: 1, max: 3600, step: 1, unit: "s", default: 10,
    help: "How long each image stays on screen. Individual items can override this on the device." },
  { path: "general.mediaPlayer.showControls", label: "Show playback controls", type: "bool", tab: "general", section: "Playback",
    default: false, help: "Show play/pause and next/previous controls — tapping the screen toggles them." },
  { path: "general.mediaPlayer.transition", label: "Crossfade transition", type: "bool", tab: "general", section: "Playback",
    default: true, help: "Fade smoothly between items instead of cutting." },
  { path: "general.mediaPlayer.transitionDuration", label: "Transition duration", type: "number", tab: "general", section: "Playback",
    min: 0, max: 3000, step: 100, unit: "ms", default: 500,
    help: "Length of the crossfade, in milliseconds.", showIf: { path: "general.mediaPlayer.transition", value: true } },
  { path: "general.mediaPlayer.fitMode", label: "Content fit mode", type: "select", tab: "general", section: "Display Options",
    options: [
      { value: "contain", label: "Contain" },
      { value: "cover", label: "Cover" },
      { value: "fill", label: "Fill" },
    ],
    default: "contain",
    help: "Contain fits the whole item on screen and may leave bars; Cover fills the screen and may crop; Fill stretches and may distort." },
  { path: "general.mediaPlayer.bgColor", label: "Background color", type: "text", tab: "general", section: "Display Options",
    placeholder: "#000000", default: "#000000",
    help: "Hex colour behind the media, seen wherever Contain leaves the screen uncovered." },

  // App Mode / Application / Applications
  { path: "general.externalApp.mode", label: "App layout", type: "select", tab: "general", section: "App Mode",
    options: [
      { value: "single", label: "Single app" },
      { value: "multi", label: "Multi-app grid" },
    ],
    default: "single",
    help: "Single locks the device into one app. Multi shows a home-screen grid of the managed apps below — the app marks this one BETA." },
  { path: "general.externalApp.testMode", label: "Test mode", type: "bool", tab: "general", section: "App Mode",
    default: true,
    help: "Leaves the Back button working normally so you can get out of the app while setting a device up. Turn this OFF before handing devices to pupils, or Back is an escape hatch. The Back button behaviour on the Security tab takes over once it is off." },
  { path: "general.externalApp.package", label: "Primary app package", type: "text", tab: "general", section: "Application",
    placeholder: "com.example.app", default: "",
    help: "Package name of the app the kiosk launches, e.g. com.google.android.calculator." },
  { path: "general.managedApps", label: "Apps in grid", type: "managedApps", tab: "general", section: "Applications",
    default: [],
    help: "Apps managed on the device. Those with Kiosk on appear in the home-screen grid; the rest are still installed and kept alive in the background — the app splits these into Applications and Additional Managed Apps." },

  // Password
  { path: "sensitive.pin", label: "Kiosk PIN", type: "text", tab: "general", section: "Password",
    placeholder: "e.g. 1234", danger: true,
    help: "PIN required to leave the kiosk and open the device's own settings. Stored hashed on the device. Leave blank to keep the PIN each device already has — sending a blank value does not clear it." },
  { path: "security.pinMode", label: "Password mode", type: "select", tab: "general", section: "Password",
    options: [
      { value: "numeric", label: "Numeric" },
      { value: "alphanumeric", label: "Alphanumeric" },
    ],
    default: "numeric",
    help: "Numeric is a 4-6 digit PIN. Alphanumeric allows letters and symbols. Changing the mode forces a new password to be set on the device." },
  { path: "security.pinMaxAttempts", label: "Max attempts before lockout", type: "number", tab: "general", section: "Password",
    min: 1, max: 100, step: 1, default: 5,
    help: "Wrong entries allowed before the device locks the PIN screen for 15 minutes." },

  // Inactivity Return
  { path: "general.inactivityReturn.enabled", label: "Return to start page when idle", type: "bool", tab: "general", section: "Inactivity Return",
    default: false,
    help: "Go back to the start URL when nobody has touched the screen for a while, so the next pupil finds the device where it should be." },
  { path: "general.inactivityReturn.delay", label: "Idle timeout", type: "number", tab: "general", section: "Inactivity Return",
    min: 5, max: 3600, step: 5, unit: "s", default: 60,
    help: "Seconds of no touches before returning to the start page." },
  { path: "general.inactivityReturn.resetOnNav", label: "Reset timer on page load", type: "bool", tab: "general", section: "Inactivity Return",
    default: true, help: "Restart the countdown whenever a new page loads, so reading a long page does not count as idle." },
  { path: "general.inactivityReturn.clearCache", label: "Clear cache on return", type: "bool", tab: "general", section: "Inactivity Return",
    default: false, help: "Empty the web view cache on the way back — a full reload, and it drops anything the previous user left cached." },
  { path: "general.inactivityReturn.scrollTop", label: "Scroll to top on return", type: "bool", tab: "general", section: "Inactivity Return",
    default: true, help: "Scroll back to the top when the device is already on the start page." },

  // Auto Reload / PDF / Printing
  { path: "general.autoReload", label: "Reload on error", type: "bool", tab: "general", section: "Auto Reload",
    default: false, help: "Reload the page automatically after a network error, so a brief Wi-Fi drop does not leave an error page up all day." },
  { path: "general.pdfViewerEnabled", label: "Inline PDF viewer", type: "bool", tab: "general", section: "PDF Viewer",
    default: false, help: "Open PDF links in the built-in viewer instead of downloading them." },
  { path: "general.printEnabled", label: "Allow printing", type: "bool", tab: "general", section: "Printing",
    default: false, help: "Let web pages call window.print() — label printers, receipts, worksheets." },
  { path: "general.printPaperSize", label: "Default paper size", type: "select", tab: "general", section: "Printing",
    options: [
      { value: "A4", label: "A4" },
      { value: "A5", label: "A5" },
      { value: "A3", label: "A3" },
      { value: "LETTER", label: "Letter" },
      { value: "LEGAL", label: "Legal" },
    ],
    default: "A4", help: "Paper size used when a page prints without asking." },

  // Web Navigation Button
  { path: "general.webviewBackButton.enabled", label: "Enable back button", type: "bool", tab: "general", section: "Web Navigation Button",
    default: false,
    help: "Float a Back button over the page that goes back in web history. It navigates the website only — it never leaves the kiosk." },
  { path: "general.webviewBackButton.xPercent", label: "Position X", type: "number", tab: "general", section: "Web Navigation Button",
    min: 0, max: 100, step: 1, unit: "%", default: 2, help: "0% is the left edge, 100% the right." },
  { path: "general.webviewBackButton.yPercent", label: "Position Y", type: "number", tab: "general", section: "Web Navigation Button",
    min: 0, max: 100, step: 1, unit: "%", default: 10, help: "0% is the top edge, 100% the bottom. Keep it clear of the status bar." },

  // ══ DASHBOARD ════════════════════════════════════════════════════════════
  { path: "general.dashboardMode", label: "Enable dashboard mode", type: "bool", tab: "dashboard", section: "Dashboard Tiles",
    default: false,
    help: "Show a grid of tiles the pupil picks from, instead of going straight to one URL. Applies in Website display mode." },
  { path: "general.dashboardTiles", label: "Dashboard tiles", type: "textarea", tab: "dashboard", section: "Dashboard Tiles",
    default: [],
    placeholder: '[{"id":"1","label":"Library","url":"https://…","iconMode":"favicon","order":0}]',
    help: "A JSON array of tiles. Each needs id, label, url, order and an iconMode of favicon, image or letter (iconValue holds the image URL or the letter). Building the tiles on one device and copying the array here is usually quicker than writing it by hand." },

  // ══ DISPLAY ══════════════════════════════════════════════════════════════
  { path: "display.brightnessManagement", label: "App brightness control", type: "bool", tab: "display", section: "Brightness Control",
    default: true,
    help: "Let Ali MDM set the screen brightness. Turn this off to leave brightness entirely to Android and the pupil." },
  { path: "display.defaultBrightness", label: "Brightness", type: "slider", tab: "display", section: "Manual Brightness",
    min: 0, max: 100, step: 1, scale: 0.01, unit: "%", default: 0.5,
    help: "Fixed screen brightness. Ignored while auto-brightness is on." },
  { path: "display.autoBrightness.enabled", label: "Enable auto-brightness", type: "bool", tab: "display", section: "Auto-Brightness",
    default: false,
    help: "Adjust brightness to the light in the room. Devices without a light sensor ignore this and keep the fixed brightness above." },
  { path: "display.autoBrightness.min", label: "Minimum brightness", type: "slider", tab: "display", section: "Auto-Brightness",
    min: 0, max: 100, step: 5, scale: 0.01, unit: "%", default: 0.1,
    help: "The dimmest the screen may go in a dark room." },
  { path: "display.autoBrightness.max", label: "Maximum brightness", type: "slider", tab: "display", section: "Auto-Brightness",
    min: 0, max: 100, step: 5, scale: 0.01, unit: "%", default: 1,
    help: "The brightest the screen may go in a bright room." },
  { path: "display.autoBrightness.offset", label: "Brightness offset", type: "slider", tab: "display", section: "Auto-Brightness",
    min: 0, max: 50, step: 5, scale: 0.01, unit: "%", default: 0,
    help: "Added on top of the measured level — raise it if the devices read as too dim in your rooms." },
  { path: "display.autoBrightness.updateInterval", label: "Update interval", type: "number", tab: "display", section: "Auto-Brightness",
    min: 1, max: 600, step: 1, scale: 1000, unit: "s", default: 1000,
    help: "How often the light sensor is read. Longer intervals react more slowly but use less battery." },

  // Screen Always On
  { path: "display.keepScreenOn", label: "Keep screen on", type: "bool", tab: "display", section: "Screen Always On",
    default: true,
    help: "Never let the screen sleep while the kiosk is in front. Devices left on mains power all day should keep this on; consider the sleep schedule below instead if they run on battery." },
  { path: "display.autoWakeOnScreenOff", label: "Auto-wake on screen off", type: "bool", tab: "display", section: "Screen Always On",
    default: false,
    help: "Turn the screen back on if something else switches it off. A last resort — it fights the power button, and it will drain a device that is not plugged in." },

  // Screensaver
  { path: "display.screensaver.enabled", label: "Enable screensaver", type: "bool", tab: "display", section: "Screensaver",
    default: false, help: "Take over the screen after a period with no touches." },
  { path: "display.screensaver.type", label: "Type", type: "select", tab: "display", section: "Screensaver",
    options: [
      { value: "dim", label: "Dim only" },
      { value: "url", label: "Web page" },
      { value: "video", label: "Video / images" },
    ],
    default: "dim",
    help: "Dim only lowers the brightness and leaves the page up. Web page shows another URL (a clock or notice board). Video / images plays the list below." },
  { path: "display.screensaver.url", label: "Screensaver URL", type: "text", tab: "display", section: "Screensaver",
    placeholder: "https://example.com/clock", default: "",
    help: "Shown read-only — a touch anywhere wakes the device rather than interacting with the page.",
    showIf: { path: "display.screensaver.type", value: "url" } },
  { path: "display.screensaver.videoItems", label: "Screensaver media", type: "list", tab: "display", section: "Screensaver",
    default: [], help: "One video or image URL per row.",
    showIf: { path: "display.screensaver.type", value: "video" } },
  { path: "display.screensaver.videoLoop", label: "Loop screensaver media", type: "bool", tab: "display", section: "Screensaver",
    default: true, help: "Restart the list when it reaches the end.",
    showIf: { path: "display.screensaver.type", value: "video" } },
  { path: "display.screensaver.inactivityEnabled", label: "Trigger on inactivity", type: "bool", tab: "display", section: "Screensaver",
    default: true, help: "Start the screensaver after the idle time below." },
  { path: "display.screensaver.inactivityDelay", label: "Inactivity delay", type: "number", tab: "display", section: "Screensaver",
    min: 1, max: 999, step: 1, scale: 60000, unit: "min", default: 600000,
    help: "Minutes without a touch before the screensaver starts." },
  { path: "display.screensaver.brightness", label: "Screensaver brightness", type: "slider", tab: "display", section: "Screensaver",
    min: 0, max: 100, step: 1, scale: 0.01, unit: "%", default: 0,
    help: "Brightness while the screensaver is showing. 0% is as dark as the device allows." },
  { path: "display.motionDetection.enabled", label: "Wake on motion", type: "bool", tab: "display", section: "Screensaver",
    default: false,
    help: "Use the camera to end the screensaver when somebody approaches. The camera is only read while the screensaver is up, unless always-on motion is enabled on the Advanced tab." },
  { path: "display.motionDetection.sensitivity", label: "Motion sensitivity", type: "select", tab: "display", section: "Screensaver",
    options: [
      { value: "low", label: "Low" },
      { value: "medium", label: "Medium" },
      { value: "high", label: "High" },
    ],
    default: "medium", help: "Higher sensitivity triggers on smaller movements — and on more false ones." },
  { path: "display.motionDetection.cameraPosition", label: "Camera", type: "select", tab: "display", section: "Screensaver",
    options: [
      { value: "front", label: "Front" },
      { value: "back", label: "Back" },
    ],
    default: "front", help: "Which camera watches for motion. Front is the one facing the room." },
  { path: "display.motionDetection.proximityEnabled", label: "Use proximity sensor", type: "bool", tab: "display", section: "Screensaver",
    default: false, help: "Also wake when a hand or body comes close to the front sensor. Works without the camera." },

  // Screen Sleep Schedule
  { path: "display.screenScheduler.enabled", label: "Enable sleep schedule", type: "bool", tab: "display", section: "Screen Sleep Schedule",
    default: false,
    help: "Turn the screen off and on at set times — overnight and at weekends, for instance. The device stays enrolled and keeps checking in while the screen is off." },
  { path: "display.screenScheduler.rules", label: "Schedule rules", type: "textarea", tab: "display", section: "Screen Sleep Schedule",
    default: [], placeholder: '[{"days":[1,2,3,4,5],"off":"17:00","on":"07:30"}]',
    help: "A JSON array of off/on windows, in the shape the app stores them." },
  { path: "display.screenScheduler.wakeOnTouch", label: "Wake on touch", type: "bool", tab: "display", section: "Screen Sleep Schedule",
    default: true, help: "Let a touch wake the screen temporarily during a scheduled sleep. It goes back to sleep afterwards." },

  // System Status Bar
  { path: "display.statusBar.enabled", label: "Show status bar", type: "bool", tab: "display", section: "System Status Bar",
    default: false,
    help: "Ali MDM's own bar across the top with battery, Wi-Fi, Bluetooth, volume and time. This is not the Android status bar — see Show system info bar on the Security tab for that." },
  { path: "display.statusBar.theme", label: "Theme", type: "select", tab: "display", section: "System Status Bar",
    options: [
      { value: "dark", label: "Dark" },
      { value: "light", label: "Light" },
    ],
    default: "dark", help: "Dark icons and text suit a light page; light suits a dark one." },
  { path: "display.statusBar.showBattery", label: "Show battery", type: "bool", tab: "display", section: "System Status Bar", default: true },
  { path: "display.statusBar.showWifi", label: "Show Wi-Fi", type: "bool", tab: "display", section: "System Status Bar", default: true },
  { path: "display.statusBar.showBluetooth", label: "Show Bluetooth", type: "bool", tab: "display", section: "System Status Bar", default: true },
  { path: "display.statusBar.showVolume", label: "Show volume", type: "bool", tab: "display", section: "System Status Bar", default: true },
  { path: "display.statusBar.showTime", label: "Show time", type: "bool", tab: "display", section: "System Status Bar", default: true },
  { path: "display.statusBar.onOverlay", label: "Show over external apps", type: "bool", tab: "display", section: "System Status Bar",
    default: true, help: "Keep the bar on top while another app is running." },
  { path: "display.statusBar.onReturn", label: "Show on return screen", type: "bool", tab: "display", section: "System Status Bar",
    default: true, help: "Show the bar on the 'External app running' screen." },

  // Web Page Zoom
  { path: "display.zoom.mode", label: "Zoom mode", type: "select", tab: "display", section: "Web Page Zoom",
    options: [
      { value: "standard", label: "Standard" },
      { value: "fit", label: "Home Assistant" },
    ],
    default: "standard",
    help: "Standard zooms the whole document and suits most sites. Home Assistant zooms the page body instead, so HA-style dashboards re-flow their cards and fill the screen." },
  { path: "display.zoom.level", label: "Zoom level", type: "number", tab: "display", section: "Web Page Zoom",
    min: 50, max: 300, step: 5, unit: "%", default: 100,
    help: "100% matches what Chrome shows. Raise it for wall-mounted devices read from a distance." },
  { path: "display.zoom.disableUserZoom", label: "Disable user zoom", type: "bool", tab: "display", section: "Web Page Zoom",
    default: false, help: "Block pinch-to-zoom and double-tap zoom. The zoom level above still applies." },

  // User Agent
  { path: "display.customUserAgent", label: "Custom user agent", type: "text", tab: "display", section: "User Agent",
    placeholder: "Mozilla/5.0 (Linux; Android 13; …) Chrome/131…", default: "",
    help: "Leave empty to use the built-in modern Chrome user agent. Only worth setting for a site that misreads the default — some hosts block user agents they do not recognise." },

  // Web Media
  { path: "general.pauseWebMediaWhenHidden", label: "Pause web media when hidden", type: "bool", tab: "display", section: "Web Media",
    default: true,
    help: "Pause page audio and video when the screensaver appears, the screen turns off, or the app goes to the background. Turn it off only if a device is meant to keep playing audio you cannot see — a web radio, say." },
  { path: "general.intercomMode", label: "2-way audio (intercom) mode", type: "bool", tab: "display", section: "Web Media",
    default: false,
    help: "For WebRTC talk-back pages such as a door intercom. While the page is using the microphone the device switches to communication audio mode so the microphone back-channel transmits, then restores normal audio. Leave off for ordinary browsing." },

  // Keyboard Mode
  { path: "display.keyboardMode", label: "Keyboard mode", type: "select", tab: "display", section: "Keyboard Mode",
    options: [
      { value: "default", label: "Default" },
      { value: "force_numeric", label: "Force numeric" },
      { value: "smart", label: "Smart detection" },
    ],
    default: "default",
    help: "Default respects what the website asks for and is the right choice for nearly every site. Force numeric puts a number pad on every field. Smart detection converts only the fields that look numeric." },

  // ══ SECURITY ═════════════════════════════════════════════════════════════
  { path: "security.kioskEnabled", label: "Lock mode", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Pin the device to Ali MDM so a pupil cannot leave it — exiting needs the kiosk PIN. This is the setting the device's own Security tab shows, and the one the console's Lock and Unlock buttons change." },
  { path: "security.allowPowerButton", label: "Allow power menu", type: "bool", tab: "security", section: "Lock Mode",
    default: true,
    help: "On, a long press shows the Restart/Shutdown menu. Off (the app calls this Block power menu), a long press does nothing and only a short press turns the screen on and off. Blocking it mutes audio on some Samsung/OneUI devices." },
  { path: "security.allowNotifications", label: "Allow notifications", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Needed for NFC tag reading inside external apps. It also makes Android show a (non-functional) Home button and lets the notification panel be pulled down — worth knowing before enabling it on pupil devices." },
  { path: "security.allowSystemInfo", label: "Show system info bar", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Show Android's own status bar — time, battery, signal — while locked. This also fixes muted audio on some Samsung/OneUI devices in lock mode." },
  { path: "security.blockFactoryReset", label: "Block factory reset", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Remove Factory reset from the Android Settings app. Worth having whenever Settings is one of the allowed apps, so nobody can wipe a device. Survives reboots and applies even outside lock mode.", danger: true },
  { path: "security.defaultLauncher", label: "Set as default launcher", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Make Ali MDM the home screen, so the launcher chooser never appears and Home returns to the kiosk. Requires Device Owner." },
  { path: "security.allowRemoteScreenshot", label: "Allow remote screenshots", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Lock mode blocks screen capture device-wide, which also blocks the console's screenshot and live view from capturing anything other than Ali MDM itself. Enable this to let the device lift the block for the fraction of a second a capture takes — which also re-enables the pupil's own Power+Volume Down screenshot for that moment. Needs the accessibility service and Android 11+." },
  { path: "security.screenLockCompat", label: "System screen-lock compatibility", type: "bool", tab: "security", section: "Lock Mode",
    default: false,
    help: "Only for devices that also have a native Android PIN or password. It stops the kiosk fighting the secure lock screen at boot and keeps the keyguard working while pinned. It also means somebody must type that password on the device after every reboot and every wake — unsuitable for unattended devices. The kiosk PIN plus Device Owner is usually the better choice.", danger: true },

  // Auto Launch
  { path: "security.autoLaunch", label: "Launch on boot", type: "bool", tab: "security", section: "Auto Launch",
    default: false, help: "Start Ali MDM automatically when the device powers on. Leave this on for devices that get unplugged." },
  { path: "security.autoRelaunchApp", label: "Auto-relaunch if closed", type: "bool", tab: "security", section: "Auto Launch",
    default: true, help: "Bring the app back if it closes or crashes." },

  // Return to Settings
  { path: "security.returnMode", label: "Return gesture", type: "select", tab: "security", section: "Return to Settings",
    options: [
      { value: "tap_anywhere", label: "Tap anywhere" },
      { value: "button", label: "On-screen button" },
    ],
    default: "tap_anywhere",
    help: "How a teacher gets from the kiosk to the settings screen. Either way the kiosk PIN is still required." },
  { path: "security.returnTapCount", label: "Taps required", type: "number", tab: "security", section: "Return to Settings",
    min: 2, max: 20, step: 1, default: 5,
    help: "How many quick taps open the PIN prompt. Higher is harder to trigger by accident." },
  { path: "security.returnTapTimeout", label: "Detection window", type: "number", tab: "security", section: "Return to Settings",
    min: 500, max: 5000, step: 100, unit: "ms", default: 1500,
    help: "Time allowed to complete all the taps. Longer is easier to perform but easier to trigger accidentally." },
  { path: "security.returnButtonPosition", label: "Button position", type: "select", tab: "security", section: "Return to Settings",
    options: [
      { value: "top-left", label: "Top left" },
      { value: "top-right", label: "Top right" },
      { value: "bottom-left", label: "Bottom left" },
      { value: "bottom-right", label: "Bottom right" },
    ],
    default: "bottom-right", help: "Which corner the return button sits in.",
    showIf: { path: "security.returnMode", value: "button" } },
  { path: "security.overlayButtonVisible", label: "Show the button", type: "bool", tab: "security", section: "Return to Settings",
    default: false,
    help: "Draw the return button visibly instead of leaving it invisible. Useful while setting devices up; usually off in a classroom." },
  { path: "security.volumeUp5Tap", label: "Volume-up 5-tap escape", type: "bool", tab: "security", section: "Return to Settings",
    default: true,
    help: "Five presses of volume-up opens the PIN prompt. Keep it as a way back in when the screen gesture is unusable — but anyone holding the device can find it, so turn it off where that matters.", danger: true },

  // Touch Blocking
  { path: "security.blockingOverlays.enabled", label: "Enable touch blocking", type: "bool", tab: "security", section: "Touch Blocking",
    default: false, help: "Swallow touches inside defined rectangles — used to cover a control on a page that pupils should not reach." },
  { path: "security.blockingOverlays.regions", label: "Blocked regions", type: "textarea", tab: "security", section: "Touch Blocking",
    default: [], placeholder: '[{"x":0,"y":0,"width":20,"height":10}]',
    help: "A JSON array of screen regions, in the shape the app stores them. Drawing them on a device and copying the array here is the practical route." },

  // URL Filtering
  { path: "security.urlFilter.enabled", label: "Enable URL filtering", type: "bool", tab: "security", section: "URL Filtering",
    default: false, help: "Control which addresses the kiosk browser may open." },
  { path: "security.urlFilter.mode", label: "Filter mode", type: "select", tab: "security", section: "URL Filtering",
    options: [
      { value: "blacklist", label: "Blacklist" },
      { value: "whitelist", label: "Whitelist" },
    ],
    default: "blacklist",
    help: "Blacklist blocks the patterns listed and allows everything else. Whitelist allows only the patterns listed — with an empty list that means the start URL and nothing more. The start URL is always allowed either way." },
  { path: "security.urlFilter.list", label: "URL patterns", type: "list", tab: "security", section: "URL Filtering",
    default: [], help: "One pattern per row, with * as a wildcard: *facebook.com* to block a site, *mysite.com/* to allow one." },
  { path: "security.urlFilter.showFeedback", label: "Show blocked message", type: "bool", tab: "security", section: "URL Filtering",
    default: false, help: "Briefly show a message when a URL is blocked, instead of appearing to do nothing." },

  // Back Button Behavior
  { path: "security.backButtonMode", label: "Back button behaviour", type: "select", tab: "security", section: "Back Button Behavior",
    options: [
      { value: "test", label: "Test mode" },
      { value: "immediate", label: "Immediate return" },
      { value: "timer", label: "Delayed return" },
    ],
    default: "test",
    help: "What happens when Back is pressed inside an external app. Test mode leaves it working normally and is for setup only. Immediate relaunches the kiosk at once. Delayed waits the time below, so a pupil can still use Back inside the app." },
  { path: "security.backButtonTimerDelay", label: "Return delay", type: "number", tab: "security", section: "Back Button Behavior",
    min: 1, max: 3600, step: 1, unit: "s", default: 10,
    help: "Seconds to wait before relaunching the kiosk.", showIf: { path: "security.backButtonMode", value: "timer" } },

  // Lock Screen Controls
  { path: "security.lockscreen.enabled", label: "Enable lock-screen controls", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false,
    help: "Put quick controls on the PIN entry screen. They work without unlocking, so a pupil can fix Wi-Fi or brightness without reaching Settings or any other app." },
  { path: "security.lockscreen.wifi", label: "Wi-Fi control", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "Turn Wi-Fi on and off and join networks from the PIN screen." },
  { path: "security.lockscreen.bluetooth", label: "Bluetooth control", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "Toggle Bluetooth and pair devices from the PIN screen." },
  { path: "security.lockscreen.brightness", label: "Brightness control", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "Open a brightness slider from the PIN screen." },
  { path: "security.lockscreen.audio", label: "Audio controls", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "Mute and audio output controls on the PIN screen." },
  { path: "security.lockscreen.flashlight", label: "Flashlight", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "A flashlight toggle on the PIN screen." },
  { path: "security.lockscreen.rotationLock", label: "Rotation lock", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "A screen-rotation lock on the PIN screen." },
  { path: "security.lockscreen.emergencyCall", label: "Emergency call", type: "bool", tab: "security", section: "Lock Screen Controls",
    default: false, help: "An emergency call button on the PIN screen — it opens Android's emergency dialer." },

  // ══ ADVANCED ═════════════════════════════════════════════════════════════
  { path: "advanced.restApi.enabled", label: "Enable local REST API", type: "bool", tab: "advanced", section: "REST API",
    default: false,
    help: "Serve an HTTP API from the device itself, for something else on the same network to drive it. The console does not need this — it reaches devices over the cloud connection.", danger: true },
  { path: "advanced.restApi.port", label: "Port", type: "number", tab: "advanced", section: "REST API",
    min: 1024, max: 65535, step: 1, default: 8080, help: "Port the device listens on." },
  { path: "advanced.restApi.allowControl", label: "Allow control commands", type: "bool", tab: "advanced", section: "REST API",
    default: true,
    help: "Accept POSTs that change the device — brightness, reload, relaunch — not just status reads. Set an API key on each device before enabling this on a shared network.", danger: true },

  { path: "advanced.mqtt.enabled", label: "Enable MQTT", type: "bool", tab: "advanced", section: "MQTT",
    default: false, help: "Publish device status to an MQTT broker and accept commands from it.", danger: true },
  { path: "advanced.mqtt.brokerUrl", label: "Broker URL", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "e.g. 192.168.1.100", default: "", help: "Broker hostname or IP address. Required when MQTT is on." },
  { path: "advanced.mqtt.port", label: "Port", type: "number", tab: "advanced", section: "MQTT",
    min: 1, max: 65535, step: 1, default: 1883, help: "Broker port." },
  { path: "advanced.mqtt.username", label: "Username", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "Leave empty if not required", default: "",
    help: "The password is set on each device — it lives in the Android Keychain and never travels in a policy." },
  { path: "advanced.mqtt.clientId", label: "Client ID", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "Auto-generated if empty", default: "",
    help: "Leave empty and each device generates its own. Setting one here would give every device in the group the same ID, and they would knock each other off the broker." },
  { path: "advanced.mqtt.deviceName", label: "Device name", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "e.g. lobby, entrance", default: "",
    help: "Friendly name used in MQTT topics. Empty means each device uses its Android ID — usually what you want for a group, since a fixed name here would be shared by every device in it." },
  { path: "advanced.mqtt.baseTopic", label: "Base topic", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "alimdm", default: "alimdm", help: "Root topic this device publishes under." },
  { path: "advanced.mqtt.discoveryPrefix", label: "Discovery prefix", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "homeassistant", default: "homeassistant", help: "Home Assistant MQTT discovery prefix." },
  { path: "advanced.mqtt.statusInterval", label: "Status interval", type: "number", tab: "advanced", section: "MQTT",
    min: 5, max: 3600, step: 5, unit: "s", default: 30, help: "How often status is published." },
  { path: "advanced.mqtt.allowControl", label: "Allow control over MQTT", type: "bool", tab: "advanced", section: "MQTT",
    default: true, help: "Accept commands from the broker — brightness, reload and the rest.", danger: true },
  { path: "advanced.mqtt.motionAlwaysOn", label: "Always-on motion detection", type: "bool", tab: "advanced", section: "MQTT",
    default: false,
    help: "Run camera motion detection continuously rather than only during the screensaver. It costs battery, and it means the camera is watching the room all day." },
];

/**
 * The policy a brand-new group starts with.
 *
 * An empty policy and a policy full of defaults are not the same thing. An
 * empty one says nothing, so every device in the group keeps whatever it
 * happens to have — which for a device enrolled by hand, or one a teacher has
 * been into the settings of, is not necessarily what anyone chose. Writing the
 * defaults in makes a new group mean something: every device in it is put into
 * the same known state, and the editor shows the administrator what that state
 * is instead of a page of blanks.
 *
 * Empty strings and empty lists are left out. They would apply as "clear this"
 * on the device while looking exactly like an unset field in the editor, which
 * is the worst of both — the URL, the app package and the various lists are
 * things an administrator fills in, not things a new group should blank.
 *
 * Not used when a group is created with "Start from": copying an existing
 * policy is a deliberate choice to inherit it, defaults included.
 */
export function defaultPolicyConfig() {
  const cfg = {};
  for (const f of POLICY_FIELDS) {
    const d = f.default;
    if (d === undefined || d === "" || (Array.isArray(d) && d.length === 0)) continue;
    const parts = f.path.split(".");
    let node = cfg;
    for (const p of parts.slice(0, -1)) node = node[p] ??= {};
    node[parts.at(-1)] = d;
  }
  return cfg;
}
