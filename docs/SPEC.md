# Ali MDM Cloud — Build Spec

Lock down ~12 Android tablets for a part-time kids' school to a fixed app
whitelist, managed from a self-hosted cloud console on a VPS.

## 1. Goals
- OS-level lockdown (Android Enterprise Device Owner + lock-task/COSU).
  Kids cannot reach Settings, Play Store, or Google Account config.
- Only a fixed whitelist of 3-4 educational apps runs. No browser.
- Central dashboard on our VPS for enrollment + app management (no per-device
  ADB scripts for day-to-day).
- App list changeable later without factory resets.
- Free / open-source. Non-profit budget.

## 2. Architecture

> This section records what was planned. For how devices and the server
> actually talk today — the wake stream, HTTP/2, the full endpoint list — see
> [architecture.md](architecture.md), which is the authoritative description.
> Two things below were superseded and are corrected inline.
- DEVICE: fork of Ali MDM (MIT). Already has the primitives we need:
  - KioskModule.kt: Device Owner policies, startLockTask,
    setScreenCaptureDisabled(true), cloud wake-lock (PARTIAL_WAKE_LOCK + WifiLock).
  - ManagedAppInstallerModule.kt: silent OTA APK install w/ Bearer token auth,
    SHA-256 verify, package-name check, min-size (50KB) check. Requires DO.
  - CloudHeartbeatTaskService.kt: background "command poll" + "config-sync hash".
  - DeviceControlService.ts: DeviceStatus schema (battery, wifi, online, config ver).
- SERVER (VPS): lightweight API we build.
  - Data model borrowed from Headwind (Apache-2.0): Groups -> Config -> Devices.
  - Config format = standard Android Enterprise policy JSON (borrowed from Fleet).
  - Protocol: device heartbeats + a poke for instant push.
    SUPERSEDED: the poll is 30s, not 60s, and the push is a server-sent events
    stream the device holds open — not MQTT. The MQTT bridge that exists runs
    the other way: it lets an outside system (Home Assistant) flag work for a
    device, and the device never subscribes to it.
  - Auth: per-device JWT. Operator console: email+password JWT (SSO later).
    SUPERSEDED by §9: the device credential is an opaque per-device API key,
    not a JWT. Only the operator console uses JWTs.
  - APKs served over HTTPS, SHA-256 verified on device.
- CONSOLE: small React dashboard (enroll, groups, config, device status).

## 3. Why this base
- Ali MDM is MIT, lightweight, per-device, and ALREADY has heartbeat + OTA
  installer + DO lockdown. We add the missing cloud layer (its open issue #202
  is exactly "auto-fetch JSON config").
- We borrow IDEAS (data model, policy JSON, SSE) from Headwind + Fleet
  (both Apache-2.0) but rebuild in a modern stack — not their Java/AngularJS.
- License: keep MIT attribution for Ali MDM; add THIRD-PARTY.md crediting
  Apache-2.0 ideas borrowed from Headwind/Fleet.

## 4. Enrollment
- Factory reset each tablet.
- Enroll as Device Owner. Prefer QR / Android Enterprise enrollment; if Google's
  Dec-2025 restrictions block QR, fall back to ADB dpm set-device-owner one-time
  (we accept ADB for initial setup).
- Device fetches its JWT + initial config from the VPS on first heartbeat.

## 5. Corner cases
- Offline: device keeps last-known config; lock-task stays active.
- Config conflict: server config hash wins; device re-polls on mismatch.
- Escape hatch (staff-only): TBD — PIN screen vs 5-tap gesture (see §7 #6).
- App update: new APK uploaded to VPS, hash pushed, device silent-installs.

## 5b. Status (verified)
- [x] Go cloud API builds clean (`go build` + `go vet`), committed to git.
- [x] End-to-end verified (6/6 tests): bootstrap, operator login (401 on bad pw),
      group listing, device heartbeat (stale->config, synced->no-op), config-drift
      re-fetch after group change, device online status, 401 on bogus device token.
- [x] Bugs found & fixed during verification:
      * NULL last_seen/battery broke all device reads -> sql.Null* scans
      * requireDevice middleware never wired into routes (device id empty -> 404)
      * heartbeat stored reported hash instead of group hash (drift never cleared)
      * updateGroup set device hash to NEW hash (should invalidate to force re-sync)

## 6. Phases
- Phase 0: fork Ali MDM; stand up VPS repo + Docker Compose (API + console +
  Postgres). ADB enrollment script for the 12 tablets.
- Phase 1: API — device auth (JWT), heartbeat endpoint, config get/push,
  APK upload + hash. Console — enroll, groups, config editor, device list.
- Phase 2: MQTT poke for instant push; SSE live status; app-update flow E2E.
- Phase 3: hardening — rate limiting, audit log, backup, escape-hatch UX.

## 7. Decisions
- App whitelist: empty on a fresh install. The three this deployment happens to
  run (com.gplanet_tech.noraneya, com.tagmedia.adnan, com.pakdata.QuranMajeed)
  are configured in the console, not seeded by the installer.
- Escape hatch: BOTH — corner-tap gesture reveals a PIN keypad; correct PIN exits
  lock-task to Settings. (staff-only)
- API language: Go (single static binary; Fleet's Android policy patterns port over)
- MQTT: NOW (EMQX broker in compose; instant poke alongside 60s heartbeat)
- APK storage: plain disk on VPS, served over HTTPS by the API
- Operator auth: email+password JWT (SSO can be layered on later)

## 9. ARCHITECTURE PIVOT (major) — discovered during Ali MDM source analysis
Ali MDM (MIT) ALREADY ships a complete, enabled cloud client (`CLOUD_ENABLED=true`):
  - src/utils/CloudSyncService.ts   — 30s heartbeat, SHA-256 config-hash change detection,
                                      sync_action:apply config import, enroll/unenroll/wipe
  - src/utils/CloudCommandService.ts— command queue poll + APK-update channel + result reporting
  - src/utils/ManagedAppInstaller.ts— silent OTA APK install (SHA-256 verified)
  - android/.../CloudHeartbeatTaskService.kt — headless heartbeat while backgrounded
  - src/utils/storage.ts exportConfig/importConfig — structured {general,display,security,advanced}

=> We do NOT patch the app. We make OUR Go server speak Ali MDM's existing cloud protocol.
   App stays stock (easy to keep updating). All customization = server + a config template.

### Ali MDM cloud protocol (the contract our server must implement)
<!-- The list below is the original contract. The server has grown since:
     file delivery, app inventory, split packages, agent self-update and the
     wake stream are all missing from it. architecture.md has the current
     endpoint list. -->
Auth: `Authorization: Bearer <apiKey>` (opaque per-device key, NOT JWT).
- POST /api/v1/devices/enroll/        {token, device_info} -> {device_id, api_key, organization_name}
- POST /api/v1/devices/{id}/heartbeat/ {telemetry, config, config_version, config_updated_at, sensitive_config}
                                     -> {status, pending_commands, server_time, sync_action:none|apply,
                                         config, sensitive_config, config_version, force_unenroll}
- GET  /api/v1/devices/{id}/commands/ -> {commands:[{id,type,params,created_at,expires_at}]}
- GET  /api/v1/devices/{id}/updates/  -> [{command_id, package_name, version_name, download_url}]
- POST /api/v1/commands/{id}/result/  {status:success|error, result, error_message}
- POST /api/v1/devices/{id}/screenshot/ (multipart image)
- POST /api/v1/devices/{id}/unenroll/
Command types: screen_on, screen_off, reload, screensaver_on/off, wake, speak, play_sound,
  audio_stop, reboot, clear_cache, toast, execute_js, launch_app, screenshot, install_apk.

### Lockdown config template (Ali MDM structured format)
{
  "general":  { "displayMode":"external_app", "externalApp":{"package":"<primary>","mode":"single"},
                "managedApps":[ {"packageName":"...","displayName":"...","showOnHomeScreen":true,
                                 "launchOnBoot":true,"keepAlive":true,"allowAccessibility":false} x3 ] },
  "display":  { "keepScreenOn":true, "defaultBrightness":0.6 },
  "security": { "kioskEnabled":true, "blockFactoryReset":true, "defaultLauncher":true,
                "returnMode":"tap_anywhere", "returnTapCount":5, "pinMode":"numeric",
                "backButtonMode":"test", "allowPowerButton":false },
  "advanced": { "restApi":{"enabled":false}, "mqtt":{"enabled":false} }
}
Device Owner (via ADB enrollment) enforces lock-task + blocks Settings/Play/factory-reset.

### Consequences
- Server auth: per-device opaque API key (issued at enroll) + operator email/password JWT.
- Config stored per-group as Ali MDM structured JSON; hash = SHA-256 of that JSON.
- Heartbeat returns sync_action:apply + config when group hash != device's last-applied hash.
- Commands/updates: server-side queues per device; MQTT poke optional (client polls anyway).
