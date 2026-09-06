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
                        one HTTP/2 connection, always opened by the tablet
┌────────────┐  ───────────────────────────────▶  ┌──────────────────────────┐
│  Tablet    │   enrol · heartbeat · fetch work    │  VPS                     │
│  Ali MDM   │   download apks and files           │  ┌─────────┐ ┌─────────┐ │
│  (Device   │   post results and frames           │  │ Go API  │ │ React   │ │
│   Owner)   │                                     │  │ :8080   │ │ console │ │
│            │  ◀ · · · · · · · · · · · · · · · ·  │  └────┬────┘ └─────────┘ │
│  behind    │   wake: "there is work" (SSE),      │       │ SQLite           │
│  NAT       │   and replies to the above          │  ┌────┴────────────────┐ │
└────────────┘                                     │  │ Caddy — TLS, HTTP/2 │ │
                                                   │  └─────────────────────┘ │
                                                   └──────────────────────────┘
```

Nothing can connect *to* a tablet, so the device opens everything. The wake
stream is a shortcut, not a channel: it says only "there is work", the device
answers by heartbeating, and if it never connects the 30-second poll does the
same job more slowly. See [docs/architecture.md](docs/architecture.md).

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
deploy/            # docker-compose.yml, Caddyfile (TLS), deploy.sh, backup.sh,
                   # systemd backup units, .env.example
docs/              # architecture.md (how devices and the server talk),
                   # SPEC.md, howto/
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
   # edit .env: CLOUD_DOMAIN, ALIMDM_BASE_URL, and generate:
   export ALIMDM_SECRET=*** rand -hex 32)
   export ALIMDM_ENROLL_TOKEN=*** rand -hex 24)
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
       --token "$ALIMDM_ENROLL_TOKEN" --org your-org --mode qr
   ```
   Scan the QR at each tablet's first setup. They appear online in the console
   within ~30s.

## Allowed apps

A new install starts with an empty whitelist — nothing is seeded, because what a
locked tablet may run is a decision for whoever is deploying it, not a default.

Add packages in the console → **Apps & Lockdown**, by package name (for example
`com.google.android.calculator`, found in the Play Store listing's URL).

## Building the Ali MDM APK (for the ADB `--apk` path)
```
cd apps/android/android
./gradlew assembleRelease
# -> android/app/build/outputs/apk/release/app-release.apk
```
(Requires the Android SDK. The QR enrollment path doesn't need this if the
tablet can install Ali MDM during setup.)

## Backups
The whole console — devices, groups, policies, operators, the file catalogue —
is one SQLite database.

```
./deploy/backup.sh          # verified snapshot to ~/backups, keeps the last 14
```

Do **not** back it up with `cp`. The database runs in WAL mode, so recent
transactions live in a separate `-wal` file until a checkpoint folds them in —
on a busy server that file is routinely larger than the database. A plain copy
takes the database without it: a valid file that restores cleanly and quietly
lacks the newest writes. `backup.sh` uses `VACUUM INTO`, which snapshots
everything consistently while the server keeps running, and verifies the result
before rotating anything.

Run it nightly with the units in `deploy/` (`alimdm-backup.service` and
`.timer` — copy them to `/etc/systemd/system/`, adjusting `User=` and the path,
then `systemctl enable --now alimdm-backup.timer`), or any scheduler you like.
Uploaded files and APKs sit beside the database under `data/`; the database
alone restores the console, `data/` as a whole restores the library with it.

## If you put your own proxy in front
The bundled Caddy config is already right (see `deploy/Caddyfile`). Swapping in
nginx needs three things Caddy does by default, and each one fails in a way that
looks like an application bug:

- **`http2`** on the `listen` line (nginx before 1.25.1) or `http2 on;`. Without
  it every device call is HTTP/1.1 and a large download blocks the calls behind
  it.
- **`client_max_body_size`** large enough for a split package — nginx's 1MB
  default rejects them with a bare `413` before the API ever sees the upload.
  2G matches the ceiling the API enforces for itself.
- **`proxy_request_buffering off`** so a large upload streams through instead of
  being written to the proxy's own disk first, and
  **`proxy_buffering off`** (or the app's `X-Accel-Buffering: no`) so streamed
  responses are not held back.

## Security notes
- **HTTPS is mandatory** — Ali MDM's cloud client requires it; Caddy provides
  automatic Let's Encrypt certs.
- **Keep `ALIMDM_ENROLL_TOKEN` secret** — anyone with it can enroll a device.
- **Keep `ALIMDM_SECRET` secret** — it signs operator JWTs.
- Device API keys are stored **SHA-256-hashed** in the DB (never plaintext).
- The API port (8080) is bound to `127.0.0.1` inside the compose network; only
  Caddy (443) is public.

## License
Ali MDM is **MIT**. This project's server/console/enroll code is yours to use
and modify. See `docs/SPEC.md` §9 for the full provenance and the Apache-2.0
(Headwind/Fleet) design influences.
