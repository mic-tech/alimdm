// Policy schema for the Ali MDM Console.
//
// Each field describes ONE setting in a group's policy (the config JSON the
// app applies via applyCloudConfig). The editor auto-renders the right control
// from `type` and writes the value at `path` (dot-separated, e.g.
// "display.screensaver.type"). The layout mirrors the Ali MDM app's own
// settings tabs/sections so a setting means the same thing in both places.
//
// Adding a new setting = add one entry here (no component changes).

/**
 * @typedef {"bool"|"number"|"slider"|"text"|"textarea"|"select"|"list"} FieldType
 * @typedef {Object} PolicyField
 * @property {string} path          dot path into the config object
 * @property {string} label
 * @property {FieldType} type
 * @property {string} section       section title (mirrors the app)
 * @property {string} tab           tab id: general|dashboard|display|security|sensitive|advanced
 * @property {{value:string,label:string}[]} [options]  for select
 * @property {number} [min]
 * @property {number} [max]
 * @property {number} [step]
 * @property {string} [placeholder]
 * @property {string} [help]        small helper text under the field
 * @property {boolean} [danger]     render a warning (powerful/sensitive setting)
 * @property {*} [default]          value used when the field is absent
 * @typedef {Object} PolicyTab
 * @property {string} id
 * @property {string} label
 * @property {string} icon
 * @property {string[]} sections    ordered section titles shown in this tab
 */

// ── Tabs (mirror the app's settings tabs, same order) ───────────────────────
export const POLICY_TABS = [
  // Section order mirrors the app's own General tab, so a setting sits in the
  // same place whether an operator is looking at the console or a teacher is
  // looking at the tablet.
  { id: "general",   label: "General",   icon: "home",
    sections: ["Display Mode", "Media Playlist", "Playback", "Display Options", "URL to Display", "Website Authentication", "URL Rotation", "URL Planner", "App Mode", "Application", "Applications", "Password", "Inactivity Return", "Auto Reload", "PDF Viewer", "Printing", "Web Navigation Button", "Background Apps"] },
  { id: "dashboard", label: "Dashboard", icon: "view-dashboard",
    sections: ["Dashboard Tiles"] },
  { id: "display",   label: "Display",   icon: "monitor",
    sections: ["Brightness Control", "Manual Brightness", "Auto-Brightness", "Screen Always On", "Screensaver", "Screen Sleep Schedule", "System Status Bar", "Web Page Zoom", "User Agent", "Web Media", "Keyboard Mode"] },
  { id: "security",  label: "Security",  icon: "shield-lock",
    sections: ["Lock Mode", "Auto Launch", "Return to Settings", "Touch Blocking", "URL Filtering", "External App Behavior", "Back Button Behavior", "Lock Screen Controls"] },
  { id: "advanced",  label: "Advanced",  icon: "cog",
    sections: ["Cloud Management", "Updates", "REST API", "MQTT"] },
];

// ── Fields ───────────────────────────────────────────────────────────────────
export const POLICY_FIELDS = [
  // ── GENERAL ──────────────────────────────────────────────────────────────
  { path: "general.displayMode", label: "Display mode", type: "select", tab: "general", section: "Display Mode",
    options: [
      { value: "webview", label: "Website" },
      { value: "media_player", label: "Media" },
      { value: "external_app", label: "App" },
    ], help: "What the kiosk shows: a web page, an installed app, or a media playlist." },
  { path: "general.url", label: "URL to display", type: "text", tab: "general", section: "URL to Display",
    placeholder: "https://…", help: "Used when display mode is Web view." },
  { path: "general.autoReload", label: "Reload on Error", type: "bool", tab: "general", section: "Auto Reload",
    help: "Reload the web view periodically to pick up changes." },

  // App Mode / Application / Managed Apps
  { path: "general.externalApp.package", label: "Primary app package", type: "text", tab: "general", section: "Application",
    placeholder: "com.example.app", help: "The app the kiosk launches (display mode = External app)." },
  { path: "general.managedApps", label: "Apps in grid", type: "managedApps", tab: "general", section: "Applications",
    help: "Apps managed on the device. Those with Kiosk on appear in the home-screen grid; the rest are still installed and kept alive in the background — the app splits these into Applications and Additional Managed Apps." },
  { path: "general.externalApp.mode", label: "App layout", type: "select", tab: "general", section: "App Mode",
    options: [ { value: "single", label: "Single app" }, { value: "multi", label: "Multi-app grid" } ],
    help: "Single launches one app; multi shows a grid of the managed apps." },
  // managedApps is edited in the dedicated "Allowed apps" UI, not here.

  // Media Playlist / Playback / Display Options
  { path: "general.mediaPlayer.items", label: "Playlist items", type: "list", tab: "general", section: "Media Playlist",
    help: "One URL per line (image or video). Shown when display mode = Media player." },
  { path: "general.mediaPlayer.autoPlay", label: "Auto-play", type: "bool", tab: "general", section: "Playback" },
  { path: "general.mediaPlayer.loop", label: "Loop playlist", type: "bool", tab: "general", section: "Playback" },
  { path: "general.mediaPlayer.shuffle", label: "Shuffle", type: "bool", tab: "general", section: "Playback" },
  { path: "general.mediaPlayer.imageDuration", label: "Image duration (s)", type: "number", tab: "general", section: "Playback", min: 1, max: 3600, step: 1 },
  { path: "general.mediaPlayer.showControls", label: "Show controls", type: "bool", tab: "general", section: "Playback" },
  { path: "general.mediaPlayer.mute", label: "Mute audio", type: "bool", tab: "general", section: "Playback" },
  { path: "general.mediaPlayer.fitMode", label: "Fit mode", type: "select", tab: "general", section: "Display Options",
    options: [ { value: "contain", label: "Fit (contain)" }, { value: "cover", label: "Fill (cover)" }, { value: "original", label: "Original size" } ] },
  { path: "general.mediaPlayer.bgColor", label: "Background color", type: "text", tab: "general", section: "Display Options", placeholder: "#000000" },

  // Website Authentication
  { path: "general.httpBasicAuth.username", label: "Username", type: "text", tab: "general", section: "Website Authentication",
    help: "For sites that require HTTP basic auth. Password is set on the device." },

  // URL Rotation
  { path: "general.urlRotation.enabled", label: "Enable Rotation", type: "bool", tab: "general", section: "URL Rotation" },
  { path: "general.urlRotation.list", label: "URLs (one per line)", type: "list", tab: "general", section: "URL Rotation" },
  { path: "general.urlRotation.interval", label: "Rotate every (s)", type: "number", tab: "general", section: "URL Rotation", min: 5, max: 86400, step: 5 },

  // URL Planner
  { path: "general.urlPlanner.enabled", label: "Enable Scheduled URLs", type: "bool", tab: "general", section: "URL Planner",
    help: "Schedule different URLs at different times." },

  // Kiosk PIN — a secret. Stored under sensitive.pin; the server ships it via
  // sensitive_config and the app writes it to secure (hashed) storage.
  { path: "sensitive.pin", label: "Kiosk PIN", type: "text", tab: "general", section: "Password",
    placeholder: "e.g. 1234", help: "PIN required to leave the kiosk / open settings. Stored hashed on the device. Leave blank for no PIN.", danger: true },
  { path: "security.pinMode", label: "Advanced Password Mode", type: "select", tab: "general", section: "Password",
    options: [ { value: "numeric", label: "Numeric" }, { value: "alphanumeric", label: "Alphanumeric" } ] },
  { path: "security.pinMaxAttempts", label: "Max attempts before lockout", type: "number", tab: "general", section: "Password", min: 1, max: 10, step: 1 },

  // Inactivity Return
  { path: "general.inactivityReturn.enabled", label: "Return to Start Page on Inactivity", type: "bool", tab: "general", section: "Inactivity Return",
    help: "Return to the kiosk home after the user is idle." },
  { path: "general.inactivityReturn.delay", label: "Idle delay (s)", type: "number", tab: "general", section: "Inactivity Return", min: 10, max: 86400, step: 10 },
  { path: "general.inactivityReturn.resetOnNav", label: "Reset timer on navigation", type: "bool", tab: "general", section: "Inactivity Return" },
  { path: "general.inactivityReturn.clearCache", label: "Clear cache on return", type: "bool", tab: "general", section: "Inactivity Return" },

  // PDF / Printing
  { path: "general.pdfViewerEnabled", label: "Inline PDF Viewer", type: "bool", tab: "general", section: "PDF Viewer",
    help: "Open PDF links in the built-in viewer instead of downloading." },
  { path: "general.printEnabled", label: "Allow Printing", type: "bool", tab: "general", section: "Printing" },
  { path: "general.printPaperSize", label: "Paper size", type: "select", tab: "general", section: "Printing",
    options: [ { value: "a4", label: "A4" }, { value: "letter", label: "US Letter" }, { value: "a5", label: "A5" } ] },

  // Web Navigation Button
  { path: "general.webviewBackButton.enabled", label: "Enable Back Button", type: "bool", tab: "general", section: "Web Navigation Button" },
  { path: "general.webviewBackButton.xPercent", label: "Position X (%)", type: "number", tab: "general", section: "Web Navigation Button", min: 0, max: 100, step: 1 },
  { path: "general.webviewBackButton.yPercent", label: "Position Y (%)", type: "number", tab: "general", section: "Web Navigation Button", min: 0, max: 100, step: 1 },

  // ── DASHBOARD ────────────────────────────────────────────────────────────
  { path: "general.dashboardMode", label: "Enable dashboard mode", type: "bool", tab: "dashboard", section: "Dashboard Tiles",
    help: "Show a tile dashboard instead of a single app/URL." },
  { path: "general.dashboardTiles", label: "Dashboard tiles", type: "list", tab: "dashboard", section: "Dashboard Tiles",
    help: "One tile label per line." },

  // ── DISPLAY ──────────────────────────────────────────────────────────────
  { path: "display.brightnessManagement", label: "App brightness control", type: "bool", tab: "display", section: "Brightness Control",
    help: "Let the app manage screen brightness." },
  { path: "display.defaultBrightness", label: "Brightness", type: "slider", tab: "display", section: "Manual Brightness", min: 0, max: 100, step: 1 },
  { path: "display.autoBrightness.enabled", label: "Enable auto-brightness", type: "bool", tab: "display", section: "Auto-Brightness" },
  { path: "display.autoBrightness.min", label: "Minimum", type: "slider", tab: "display", section: "Auto-Brightness", min: 0, max: 100, step: 1 },
  { path: "display.autoBrightness.max", label: "Maximum", type: "slider", tab: "display", section: "Auto-Brightness", min: 0, max: 100, step: 1 },
  { path: "display.autoBrightness.offset", label: "Offset", type: "slider", tab: "display", section: "Auto-Brightness", min: -50, max: 50, step: 1 },
  { path: "display.keepScreenOn", label: "Keep screen on", type: "bool", tab: "display", section: "Screen Always On",
    help: "Never let the screen sleep while the kiosk is active." },
  { path: "display.autoWakeOnScreenOff", label: "Auto-wake on screen off", type: "bool", tab: "display", section: "Screen Always On" },

  // Screensaver
  { path: "display.screensaver.enabled", label: "Enable screensaver", type: "bool", tab: "display", section: "Screensaver" },
  { path: "display.screensaver.type", label: "Type", type: "select", tab: "display", section: "Screensaver",
    options: [ { value: "none", label: "None (black)" }, { value: "url", label: "Web page" }, { value: "video", label: "Video" } ] },
  { path: "display.screensaver.url", label: "Screensaver URL", type: "text", tab: "display", section: "Screensaver", placeholder: "https://…" },
  { path: "display.screensaver.inactivityEnabled", label: "Trigger on inactivity", type: "bool", tab: "display", section: "Screensaver" },
  { path: "display.screensaver.inactivityDelay", label: "Inactivity delay (s)", type: "number", tab: "display", section: "Screensaver", min: 10, max: 86400, step: 10 },
  { path: "display.screensaver.brightness", label: "Screensaver brightness", type: "slider", tab: "display", section: "Screensaver", min: 0, max: 100, step: 1 },

  // Screen Sleep Schedule
  { path: "display.screenScheduler.enabled", label: "Enable sleep schedule", type: "bool", tab: "display", section: "Screen Sleep Schedule",
    help: "Turn the screen off/on on a schedule (e.g. nights)." },
  { path: "display.screenScheduler.wakeOnTouch", label: "Wake on touch", type: "bool", tab: "display", section: "Screen Sleep Schedule" },

  // System Status Bar
  { path: "display.statusBar.enabled", label: "Show status bar", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.showBattery", label: "Show battery", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.showWifi", label: "Show Wi-Fi", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.showTime", label: "Show time", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.theme", label: "Theme", type: "select", tab: "display", section: "System Status Bar",
    options: [ { value: "light", label: "Light" }, { value: "dark", label: "Dark" } ] },

  // Web Page Zoom / User Agent / Web Media / Keyboard
  { path: "display.zoom.level", label: "Zoom level (%)", type: "number", tab: "display", section: "Web Page Zoom", min: 50, max: 300, step: 5 },
  { path: "display.zoom.disableUserZoom", label: "Disable user zoom", type: "bool", tab: "display", section: "Web Page Zoom" },
  { path: "display.customUserAgent", label: "Custom user agent", type: "text", tab: "display", section: "User Agent",
    help: "Override the browser user-agent string. Leave blank for default." },
  { path: "general.pauseWebMediaWhenHidden", label: "Pause web media when hidden", type: "bool", tab: "display", section: "Web Media" },
  { path: "display.keyboardMode", label: "Keyboard mode", type: "select", tab: "display", section: "Keyboard Mode",
    options: [ { value: "hidden", label: "Hidden" }, { value: "on-tap", label: "Show on tap" }, { value: "always", label: "Always visible" } ] },

  // ── SECURITY ─────────────────────────────────────────────────────────────
  { path: "security.kioskEnabled", label: "Kiosk mode (lock task)", type: "bool", tab: "security", section: "Lock Mode",
    help: "Trap the user in the app — they can't leave without the PIN." },
  { path: "security.allowPowerButton", label: "Allow power button", type: "bool", tab: "security", section: "Lock Mode",
    help: "When off, the power menu is blocked in kiosk mode." },
  { path: "security.allowNotifications", label: "Allow notifications", type: "bool", tab: "security", section: "Lock Mode" },
  { path: "security.allowSystemInfo", label: "Allow system info", type: "bool", tab: "security", section: "Lock Mode" },
  { path: "security.blockFactoryReset", label: "Block factory reset", type: "bool", tab: "security", section: "Lock Mode",
    help: "Prevent the user from wiping the device via Settings." },
  { path: "security.defaultLauncher", label: "Set as default launcher", type: "bool", tab: "security", section: "Lock Mode",
    help: "Make Ali MDM the home screen so the launcher chooser never appears." },

  // Auto Launch
  { path: "security.autoLaunch", label: "Auto-launch on boot", type: "bool", tab: "security", section: "Auto Launch",
    help: "Start the kiosk automatically when the device boots." },
  { path: "security.autoRelaunchApp", label: "Auto-relaunch if closed", type: "bool", tab: "security", section: "Auto Launch",
    help: "Restart the app if the user manages to close it." },

  // Return to Settings
  { path: "security.returnMode", label: "Return gesture", type: "select", tab: "security", section: "Return to Settings",
    options: [ { value: "tap_anywhere", label: "Tap anywhere (N taps)" }, { value: "button", label: "On-screen button" } ] },
  { path: "security.returnTapCount", label: "Taps required", type: "number", tab: "security", section: "Return to Settings", min: 1, max: 10, step: 1 },
  { path: "security.returnTapTimeout", label: "Tap window (s)", type: "number", tab: "security", section: "Return to Settings", min: 1, max: 30, step: 1 },
  { path: "security.returnButtonPosition", label: "Button position", type: "select", tab: "security", section: "Return to Settings",
    options: [
      { value: "top-left", label: "Top Left" },
      { value: "top-right", label: "Top Right" },
      { value: "bottom-left", label: "Bottom Left" },
      { value: "bottom-right", label: "Bottom Right" },
    ],
    help: "Where the invisible return button appears on screen.",
    showIf: { path: "security.returnMode", value: "button" } },
  { path: "security.overlayButtonVisible", label: "Show overlay button", type: "bool", tab: "security", section: "Return to Settings" },

  // Touch Blocking
  { path: "security.blockingOverlays.enabled", label: "Enable touch blocking", type: "bool", tab: "security", section: "Touch Blocking",
    help: "Block touches in defined screen regions." },

  // URL Filtering
  { path: "security.urlFilter.enabled", label: "Enable URL filtering", type: "bool", tab: "security", section: "URL Filtering" },
  { path: "security.urlFilter.mode", label: "Mode", type: "select", tab: "security", section: "URL Filtering",
    options: [ { value: "allowlist", label: "Allow list" }, { value: "blocklist", label: "Block list" } ] },
  { path: "security.urlFilter.list", label: "URLs (one per line)", type: "list", tab: "security", section: "URL Filtering" },

  // External App Behavior / Back Button
  { path: "security.backButtonMode", label: "Back button behavior", type: "select", tab: "security", section: "Back Button Behavior",
    options: [ { value: "exit", label: "Exit kiosk" }, { value: "navigate", label: "Navigate back" }, { value: "ignore", label: "Ignore" } ] },

  // Lock Screen Controls
  { path: "security.lockscreen.enabled", label: "Enable lock-screen controls", type: "bool", tab: "security", section: "Lock Screen Controls",
    help: "Quick controls on the lock screen (Wi-Fi, brightness, etc.)." },
  { path: "security.lockscreen.wifi", label: "Wi-Fi toggle", type: "bool", tab: "security", section: "Lock Screen Controls" },
  { path: "security.lockscreen.brightness", label: "Brightness slider", type: "bool", tab: "security", section: "Lock Screen Controls" },
  { path: "security.lockscreen.emergencyCall", label: "Emergency call", type: "bool", tab: "security", section: "Lock Screen Controls" },

  // ── ADVANCED ─────────────────────────────────────────────────────────────
  { path: "advanced.restApi.enabled", label: "Enable local REST API", type: "bool", tab: "advanced", section: "REST API",
    help: "Expose a local HTTP API on the device for integrations.", danger: true },
  { path: "advanced.restApi.port", label: "Port", type: "number", tab: "advanced", section: "REST API", min: 1024, max: 65535, step: 1 },
  { path: "advanced.restApi.allowControl", label: "Allow control commands", type: "bool", tab: "advanced", section: "REST API", danger: true },

  { path: "advanced.mqtt.enabled", label: "Enable MQTT", type: "bool", tab: "advanced", section: "MQTT",
    help: "Publish/subscribe device events over an MQTT broker.", danger: true },
  { path: "advanced.mqtt.brokerUrl", label: "Broker URL", type: "text", tab: "advanced", section: "MQTT", placeholder: "mqtt://…" },
  { path: "advanced.mqtt.port", label: "Port", type: "number", tab: "advanced", section: "MQTT", min: 1, max: 65535, step: 1 },
  { path: "advanced.mqtt.username", label: "Username", type: "text", tab: "advanced", section: "MQTT" },
  { path: "advanced.mqtt.clientId", label: "Client ID", type: "text", tab: "advanced", section: "MQTT" },
  { path: "advanced.mqtt.baseTopic", label: "Base topic", type: "text", tab: "advanced", section: "MQTT" },
  // ── Settings the app accepts that were previously unreachable from here ────
  // Audited against StorageService.importConfig: anything the device applies
  // should be configurable centrally, or a fleet cannot be managed from one
  // place. Types and defaults mirror exportConfig so a round-trip is lossless.

  // General
  { path: "general.externalApp.testMode", label: "Test mode", type: "bool", tab: "general", section: "App Mode",
    help: "Back button returns to settings instead of being swallowed. For setup only." },
  { path: "general.inactivityReturn.scrollTop", label: "Scroll to top on return", type: "bool", tab: "general", section: "Inactivity Return" },
  { path: "general.intercomMode", label: "Intercom mode", type: "bool", tab: "display", section: "Web Media" },
  { path: "general.mediaPlayer.transition", label: "Transition", type: "select", tab: "general", section: "Playback",
    options: [
      { value: "none", label: "None" },
      { value: "fade", label: "Fade" },
      { value: "slide", label: "Slide" },
    ] },
  { path: "general.mediaPlayer.transitionDuration", label: "Transition duration (ms)", type: "number", tab: "general", section: "Playback", min: 0, max: 5000, step: 100 },
  { path: "general.urlPlanner.events", label: "Planner events", type: "textarea", tab: "general", section: "URL Planner",
    help: "JSON array of scheduled URL changes, as the app stores them." },

  // Display
  { path: "display.zoom.mode", label: "Zoom mode", type: "select", tab: "display", section: "Web Page Zoom",
    options: [
      { value: "auto", label: "Auto" },
      { value: "manual", label: "Manual" },
    ] },
  { path: "display.autoBrightness.updateInterval", label: "Update interval (ms)", type: "number", tab: "display", section: "Auto-Brightness", min: 1000, max: 600000, step: 1000 },
  { path: "display.screensaver.delay", label: "Screensaver delay (ms)", type: "number", tab: "display", section: "Screensaver", min: 1000, max: 3600000, step: 1000 },
  { path: "display.screensaver.videoItems", label: "Screensaver videos", type: "list", tab: "display", section: "Screensaver",
    help: "Media URLs shown when the screensaver type is video." },
  { path: "display.screensaver.videoLoop", label: "Loop screensaver video", type: "bool", tab: "display", section: "Screensaver" },
  { path: "display.screenScheduler.rules", label: "Schedule rules", type: "textarea", tab: "display", section: "Screen Sleep Schedule",
    help: "JSON array of on/off windows, as the app stores them." },
  { path: "display.statusBar.onOverlay", label: "Show over external apps", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.onReturn", label: "Show on return to kiosk", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.showBluetooth", label: "Show Bluetooth", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.statusBar.showVolume", label: "Show volume", type: "bool", tab: "display", section: "System Status Bar" },
  { path: "display.motionDetection.enabled", label: "Wake on motion", type: "bool", tab: "display", section: "Screensaver",
    help: "Uses the camera to wake the screen when someone approaches." },
  { path: "display.motionDetection.sensitivity", label: "Motion sensitivity", type: "select", tab: "display", section: "Screensaver",
    options: [
      { value: "low", label: "Low" },
      { value: "medium", label: "Medium" },
      { value: "high", label: "High" },
    ] },
  { path: "display.motionDetection.cameraPosition", label: "Camera", type: "select", tab: "display", section: "Screensaver",
    options: [
      { value: "front", label: "Front" },
      { value: "back", label: "Back" },
    ] },
  { path: "display.motionDetection.delay", label: "Motion delay (ms)", type: "number", tab: "display", section: "Screensaver", min: 0, max: 600000, step: 1000 },
  { path: "display.motionDetection.proximityEnabled", label: "Use proximity sensor", type: "bool", tab: "display", section: "Screensaver" },

  // Security
  { path: "security.backButtonTimerDelay", label: "Back button timer (ms)", type: "number", tab: "security", section: "Back Button Behavior", min: 0, max: 60000, step: 500 },
  { path: "security.blockingOverlays.regions", label: "Blocked regions", type: "textarea", tab: "security", section: "Touch Blocking",
    help: "JSON array of screen regions to swallow touches in, as the app stores them." },
  { path: "security.overlayButtonPosition", label: "Overlay button position", type: "select", tab: "security", section: "Return to Settings",
    options: [
      { value: "top-left", label: "Top left" },
      { value: "top-right", label: "Top right" },
      { value: "bottom-left", label: "Bottom left" },
      { value: "bottom-right", label: "Bottom right" },
    ] },
  { path: "security.screenLockCompat", label: "Screen-lock compatibility mode", type: "bool", tab: "security", section: "Lock Mode",
    help: "Workaround for devices that fight lock task on screen off." },
  { path: "security.volumeUp5Tap", label: "Volume-up 5-tap escape", type: "bool", tab: "security", section: "Return to Settings",
    help: "Five presses of volume-up returns to settings. A fallback when the screen gesture is unusable.", danger: true },
  { path: "security.urlFilter.showFeedback", label: "Show blocked-URL message", type: "bool", tab: "security", section: "URL Filtering" },
  { path: "security.lockscreen.audio", label: "Audio controls", type: "bool", tab: "security", section: "Lock Screen Controls" },
  { path: "security.lockscreen.bluetooth", label: "Bluetooth toggle", type: "bool", tab: "security", section: "Lock Screen Controls" },
  { path: "security.lockscreen.flashlight", label: "Flashlight", type: "bool", tab: "security", section: "Lock Screen Controls" },
  { path: "security.lockscreen.rotationLock", label: "Rotation lock", type: "bool", tab: "security", section: "Lock Screen Controls" },

  // Advanced
  { path: "advanced.mqtt.discoveryPrefix", label: "Discovery prefix", type: "text", tab: "advanced", section: "MQTT",
    placeholder: "homeassistant" },
  { path: "advanced.mqtt.statusInterval", label: "Status interval (s)", type: "number", tab: "advanced", section: "MQTT", min: 5, max: 3600, step: 5 },
  { path: "advanced.mqtt.deviceName", label: "Device name", type: "text", tab: "advanced", section: "MQTT" },
  { path: "advanced.mqtt.allowControl", label: "Allow control over MQTT", type: "bool", tab: "advanced", section: "MQTT", danger: true },
  { path: "advanced.mqtt.motionAlwaysOn", label: "Publish motion always", type: "bool", tab: "advanced", section: "MQTT" },
];