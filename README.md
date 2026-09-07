# Ali MDM Cloud

A self-hosted cloud to **lock down and manage a fleet of Android tablets** —
built for a part-time school running ~12 tablets locked to a fixed app whitelist
(no Settings, no Play Store, no factory reset, no Google account fiddling).

It is the **Ali MDM app** (the per-device kiosk, a fork of
[FreeKiosk](https://github.com/rushb-fr/freekiosk) — MIT, © 2025 Rushb) + a
**custom Go cloud** + a **React operator console**, all self-hosted on your own
VPS. No per-seat fees.

## Why this design
FreeKiosk already ships a **complete, enabled cloud client** (heartbeat, config
sync, command queue, silent APK install, enrollment). So instead of patching the
app, this project makes a lightweight Go server **speak that existing
protocol**. The app stays close to stock — you can keep pulling upstream changes
without breaking your setup.

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
- **Lockdown** — each tablet runs Ali MDM as **Device Owner**: kiosk/lock-task,
  factory reset blocked, Ali MDM pinned as the home launcher. Only the apps in
  the whitelist run.
- **Central management** — the console shows live device status (online,
  battery, Android version, free storage), and a policy group defines the
  whitelist and kiosk behaviour for a whole set of tablets at once. Changes
  reach a connected device in under a second, and within 30s otherwise — no
  factory resets.
- **Enrollment** — a QR scanned at the tablet's first setup downloads the app,
  makes it Device Owner, and enrols it into the group and under the label you
  chose when generating the code. ADB enrolment covers tablets already past
  setup.
- **Apps** — upload an APK or a split bundle (`.xapk`/`.apks`, base + config
  splits committed as one install session) and push a silent install. The
  console can also list what a tablet actually has installed and uninstall
  remotely.
- **Files** — a document library that keeps its folder structure, sent to the
  tablets' inbox folder and readable there in the pupil's own Files app.
- **Ali MDM updates itself** — stage a build and roll it out over the air; the
  same build is what a newly scanned QR installs, so a new tablet never arrives
  older than the fleet.
- **Seeing what happened** — screenshots and a live view with remote input, and
  an activity feed recording every operator action and everything the server
  noticed on its own.
- **Commands** — reboot, lock/unlock, screen on/off, request logs, unenroll.

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
                   # SPEC.md, build-android.md, howto/ guides
tools/             # misc helper scripts
```

Build and deploy are driven from the repo root:

```
docker build -f apps/server/Dockerfile -t alimdm-cloud .
```

## Quick start (on your VPS)
1. **DNS** — point `cloud.yourdomain.com` (A/AAAA) at the VPS IP.
2. **Install** Docker + the compose plugin.
3. **Clone** this repo on the VPS.
4. **Configure** — the env file lives in `deploy/`, and `deploy.sh` creates it
   from the example on first run, then stops so you can fill it in.
   ```
   cp deploy/.env.example deploy/.env
   # edit it: CLOUD_DOMAIN, ALIMDM_BASE_URL, and generate:
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
7. **Enroll tablets** — see [`apps/enroll/README.md`](apps/enroll/README.md):
   ```
   python3 apps/enroll/enroll.py --cloud https://cloud.yourdomain.com \
       --token "$ALIMDM_ENROLL_TOKEN" --org your-org --mode qr
   ```
   Scan the QR at each tablet's first setup. They appear online in the console
   within ~30s.

## Allowed apps

A new install starts with an empty whitelist — nothing is seeded, because what a
locked tablet may run is a decision for whoever is deploying it, not a default.

The whitelist belongs to a policy group rather than to the fleet, so each group
can allow a different set. Add packages in the console under **Groups → Edit
policy → General → Applications → Apps in grid**, by package name (for example
`com.google.android.calculator`, found in the Play Store listing's URL).

Apps with **Kiosk** on appear in the tablet's home-screen grid; the rest are
still installed and kept running in the background. Uploading the APK is a
separate step on the **Packages** page — the whitelist decides what may run,
not what gets installed.

## Building the Ali MDM APK (for the ADB `--apk` path)
```
cd apps/android/android
./gradlew assembleRelease
# -> app/build/outputs/apk/release/app-release.apk
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

One thing worth adding rather than merely not breaking: a per-address cap on
sign-in attempts. The API locks a single account after a few failures, but that
lock is keyed by email and cannot see one guess each across many accounts. Only
the proxy knows the real client address — the API sees `127.0.0.1` — so the
per-address half belongs there.

```nginx
# http level, outside the server block
limit_req_zone $binary_remote_addr zone=alimdm_login:10m rate=10r/m;

# inside the server block, before `location /`
location = /api/v1/operator/login {
    limit_req zone=alimdm_login burst=5 nodelay;
    limit_req_status 429;

    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_pass http://127.0.0.1:8080;
}
```

Signing in is one request, so 10 a minute is far above any person and far below
anything worth an attacker's time. Caddy has no equivalent in a stock build —
see the note in `deploy/Caddyfile`.

The security headers are set by the application, not by the proxy, so they
survive a proxy swap: `X-Frame-Options: DENY` and `frame-ancestors 'none'`
(an operator console must never be framable), `nosniff`, a referrer policy, and
HSTS on requests the proxy marks `X-Forwarded-Proto: https`.

## Security notes
- **HTTPS is mandatory** — Ali MDM's cloud client requires it; Caddy provides
  automatic Let's Encrypt certs.
- **Keep `ALIMDM_ENROLL_TOKEN` secret** — anyone with it can enroll a device.
- **Keep `ALIMDM_SECRET` secret** — it signs operator JWTs.
- Device API keys are stored **SHA-256-hashed** in the DB (never plaintext).
- The API port (8080) is bound to `127.0.0.1` inside the compose network; only
  Caddy (443) is public.
- **Operator passwords** are PBKDF2-HMAC-SHA256, 100k iterations, per-account
  salt. Repeated failures lock an account for a growing interval, and an
  unknown email is hashed against a throwaway so it cannot be told apart from a
  real one by how long the answer takes.
- **Sessions** are 12-hour HS256 tokens. The role is read live from the
  database rather than trusted from the token, so a demotion or deletion takes
  effect on the next request; changing a password ends every session it opened.
- **Devices** authenticate with a 192-bit random key, stored SHA-256-hashed. A
  device may only act for itself: reporting a command or install result is
  scoped to the device the key belongs to.
- The only unauthenticated endpoints are `/healthz` and the two that serve the
  Ali MDM build itself (`/api/v1/provision/apk`, `/api/v1/agent/apk`) — a
  factory-fresh tablet has to fetch it before it has any credential to present.

## License and attribution
This project is **MIT** (© 2026 mic-tech) — the server, console and enroll code
are yours to use and modify. See [LICENSE](LICENSE).

The Android app in `apps/android/` is a fork of
**[FreeKiosk](https://github.com/rushb-fr/freekiosk)**, MIT, © 2025 Rushb. Its
copyright notice and licence are kept verbatim at
[`apps/android/LICENSE`](apps/android/LICENSE) and apply to that code; the
cloud client this whole design is built around is upstream's work, not ours.

The Outfit typeface under `brand/build/fonts/` is © 2021 The Outfit Project
Authors, SIL Open Font License 1.1, with its licence alongside it.

`docs/SPEC.md` §9 records the fuller provenance, including the Apache-2.0
(Headwind/Fleet) design influences.
