# Ali MDM Cloud

A self-hosted cloud to **lock down and manage a fleet of Android tablets** —
built for a part-time school running ~12 tablets locked to a fixed app whitelist
(no Settings, no Play Store, no factory reset, no Google account fiddling).

It is **Ali MDM** (MIT, the per-device kiosk app) + a **custom Go cloud** + a
**React operator console**, all self-hosted on your own VPS. No per-seat fees.

## Why this design
Ali MDM already ships a **complete, enabled cloud client** (heartbeat, config
sync, command queue, silent APK install, enrollment). So instead of patching the
app, this project makes a lightweight Go server **speak Ali MDM's existing
protocol**. The app stays 100% stock — you can keep updating it upstream without
breaking your setup.

```
┌────────────┐   HTTPS (JWT / API key)   ┌─────────────────────────────┐
│  Tablet    │ ─────────────────────────▶│  VPS                        │
│ Ali MDM  │  heartbeat / commands /   │  ┌─────────┐  ┌──────────┐  │
│ (Device    │  config / APK updates     │  │ Go API  │  │ React    │  │
│  Owner)    │ ◀─────────────────────────│  │ :8080   │  │ console  │  │
└────────────┘   config / poke           │  └────┬────┘  │  at /   │  │
                                          │       │SQLite │  └──────────┘  │
                                          │  ┌────┴────┐ ┌──────────┐      │
                                          │  │  Caddy  │ │  (MQTT)  │      │
                                          │  │  TLS    │ │  optional│      │
                                          │  └─────────┘ └──────────┘      │
                                          └─────────────────────────────┘
```

## What it does
- **Lockdown** — each tablet is a Ali MDM **Device Owner**: kiosk/lock-task,
  factory reset blocked, Ali MDM pinned as the home launcher. Only the apps in
  the whitelist run.
- **Central management** — the operator console shows live device status
  (online, battery, Android version), lets you **change the app whitelist**
  (pushes to all devices within ~30s, no factory resets), upload APKs for
  silent install, and send commands (reboot / lock / unlock / unenroll).
- **Zero-touch enrollment** — a QR scanned at the tablet's first setup makes it
  a Device Owner and auto-enrolls it to the cloud. (Or ADB-push for tablets
  already past setup.)

## Repository layout

A monorepo holding every component of the Ali MDM system.

```
apps/
  server/          # Go cloud API (store, auth, config, apk, httpapi+MQTT, bootstrap)
  console/         # React operator console (Vite)
  android/         # Ali MDM device agent (React Native) — the app on the tablets
  enroll/          # ADB enrollment scripts (enroll_tablet.sh, enroll.py)
brand/             # logo kit: generated SVGs, Android vector icons, + their generator
deploy/            # docker-compose.yml, Caddyfile (TLS), deploy.sh, .env.example
docs/              # SPEC.md, build-android.md + howto/ guides
tools/             # misc helper scripts
```

Build and deploy are driven from the repo root:

```
docker build -f apps/server/Dockerfile -t ali-mdm-cloud .
```

## Quick start (on your VPS)
1. **DNS** — point `cloud.yourdomain.com` (A/AAAA) at the VPS IP.
2. **Install** Docker + the compose plugin.
3. **Clone** this repo on the VPS.
4. **Configure**
   ```
   cp .env.example .env
   # edit .env: CLOUD_DOMAIN, FK_BASE_URL, and generate:
   export FK_SECRET=*** rand -hex 32)
   export FK_ENROLL_TOKEN=*** rand -hex 24)
   ```
5. **Deploy**
   ```
   ./deploy/deploy.sh
   ```
   It builds the images, starts API + Caddy (automatic HTTPS), and creates your
   first operator + the default 3-app lockdown group.
6. **Open the console** — `https://cloud.yourdomain.com` and sign in.
7. **Enroll tablets** — see `enroll/README.md`:
   ```
   python3 enroll/enroll.py --cloud https://cloud.yourdomain.com \
       --token "$FK_ENROLL_TOKEN" --org your-org --mode qr
   ```
   Scan the QR at each tablet's first setup. They appear online in the console
   within ~30s.

## The three apps (default whitelist)
- `com.gplanet_tech.noraneya`
- `com.tagmedia.adnan`
- `com.pakdata.QuranMajeed`

Change these anytime in the console → **Apps & Lockdown**.

## Building the Ali MDM APK (for the ADB `--apk` path)
```
cd apps/android/android
./gradlew assembleRelease
# -> android/app/build/outputs/apk/release/app-release.apk
```
(Requires the Android SDK. The QR enrollment path doesn't need this if the
tablet can install Ali MDM during setup.)

## Security notes
- **HTTPS is mandatory** — Ali MDM's cloud client requires it; Caddy provides
  automatic Let's Encrypt certs.
- **Keep `FK_ENROLL_TOKEN` secret** — anyone with it can enroll a device.
- **Keep `FK_SECRET` secret** — it signs operator JWTs.
- Device API keys are stored **SHA-256-hashed** in the DB (never plaintext).
- The API port (8080) is bound to `127.0.0.1` inside the compose network; only
  Caddy (443) is public.

## License
Ali MDM is **MIT**. This project's server/console/enroll code is yours to use
and modify. See `docs/SPEC.md` §9 for the full provenance and the Apache-2.0
(Headwind/Fleet) design influences.
