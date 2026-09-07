# How-To: Enrolling & Managing Your Tablets

Practical, copy-paste guides for getting your Android tablets locked down and
managed by Ali MDM Cloud. Read this top-to-bottom once; then use the specific
guide that fits your situation.

## How do you enroll?

- **QR at first setup** — [`zero-touch-qr.md`](zero-touch-qr.md). The tablet's
  setup wizard downloads Ali MDM from your own server, makes it Device Owner and
  enrols it, carrying the group and label you picked. No cable, no ADB, and a
  teacher can run it unattended. **Start here.**
- **ADB over USB** — [`adb-enroll.md`](adb-enroll.md). One USB session per
  tablet. What you need for a tablet already past its setup wizard, or when the
  QR path is blocked on a particular Android build.

## Where does the APK come from?

There is no public release to download — this is a private fork, and the app is
built from this repo. Two ways it reaches a tablet:

- **The QR path fetches it for you.** Whatever build is staged on the console's
  **App update** page is what a scanned tablet installs, so a new tablet arrives
  on the same version as the rest of the fleet. Nothing to download by hand.
- **The ADB path needs a file.** Build one — see
  [`build-apk.md`](build-apk.md) — and pass it to the enrol script.

## The 5 things every tablet needs (in order)

No matter which method, a fully enrolled tablet has:

1. **Factory reset** — no accounts, no SIM (required for Device Owner).
2. **Ali MDM installed** — the APK is on the device.
3. **Device Owner** — Ali MDM is the device's owner (enforces the lockdown).
4. **Enrolled to the cloud** — it has your enroll token + cloud URL, so it pulls
   the lockdown config.
5. **Locked** — showing your whitelisted app(s), can't escape.

The guides below are just different ways to accomplish steps 1–4.

## Files in this directory

| File | What it's for |
|---|---|
| `README.md` | This overview — read first |
| `zero-touch-qr.md` | **Main path**: QR enrollment at first setup, no cable |
| `adb-enroll.md` | Enroll one tablet over USB, step by step |
| `enroll-one.sh` | The ADB steps as a copy-paste script (one tablet) |
| `enroll-all.sh` | Loop to enroll many tablets at once |
| `first-tablet-test.md` | Click-by-click first enrollment + app push, end to end |
| `build-apk.md` | Build an Ali MDM release APK |
| `publish-image.md` | Build and publish the server image |
| `troubleshooting.md` | Fix the common problems |
| `day-to-day.md` | Managing tablets after enrollment (change apps, unenroll, etc.) |
| `qr-enroll.md` | Superseded — points at `zero-touch-qr.md` |

## Prerequisites (do these once)

- **ADB installed** on your computer — see `adb-enroll.md` §1.
- **A build staged** on the console's App update page (for the QR path), or an
  APK built locally (for ADB) — see `build-apk.md`.
- **Your cloud is live** — you can log in to `https://cloud.yourdomain.com`.
- **Your enroll token** — the `ALIMDM_ENROLL_TOKEN` value from your server's `.env`.

## Quick start (the fast version)

```
1. Factory-reset the tablet (no accounts, no SIM)
2. Console -> Enroll -> pick the group and label -> Generate QR
3. Power on the tablet, tap the first setup screen six times, scan the QR
4. Watch it appear online in the console within ~30s
```

That's it for one tablet, and the same code works for the next one if you leave
the label blank. For the ADB route instead:

```
./docs/howto/enroll-one.sh <cloud-url> <enroll-token> <path-to-apk>
```
