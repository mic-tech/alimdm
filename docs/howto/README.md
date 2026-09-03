# How-To: Enrolling & Managing Your Tablets

Practical, copy-paste guides for getting your Android tablets locked down and
managed by Ali MDM Cloud. Read this top-to-bottom once; then use the specific
guide that fits your situation.

## The two big decisions

**1. How do you enroll?**
- **ADB (recommended for you)** — `adb-enroll.md`. One USB session per tablet.
- **zero-touch-qr.md** — zero-touch QR (scan during setup, auto-download + install, no ADB)
  Most reliable. Best when you have a computer + USB cable and ~12 tablets.
- **QR / zero-touch** — `qr-enroll.md`. Scan a QR at first setup. No ADB.
  Cleanest for a teacher to do unattended, but the app must be installable.

**2. Where does the Ali MDM APK come from?**
- **Official release** — download from Ali MDM's GitHub Releases. Easiest.
- **Your own build** — build from source if you want to customize. See
  `build-apk.md`.

> You almost always want **ADB + official release APK** to start. Use QR once
> you've confirmed the ADB path works on one tablet.

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
| `adb-enroll.md` | **Main path**: enroll one tablet over USB (step by step) |
| `enroll-one.sh` | The ADB steps as a copy-paste script (one tablet) |
| `enroll-all.sh` | Loop to enroll many tablets at once |
| `qr-enroll.md` | Zero-touch QR enrollment (from the website) |
| `build-apk.md` | Build your own Ali MDM release APK |
| `troubleshooting.md` | Fix the common problems |
| `day-to-day.md` | Managing tablets after enrollment (change apps, unenroll, etc.) |

## Prerequisites (do these once)

- **ADB installed** on your computer — see `adb-enroll.md` §1.
- **The Ali MDM APK** downloaded — see `build-apk.md` (or grab the official
  release).
- **Your cloud is live** — you can log in to `https://cloud.yourdomain.com`.
- **Your enroll token** — the `ALIMDM_ENROLL_TOKEN` value from your server's `.env`.

## Quick start (the fast version)

```
1. Factory-reset the tablet (no accounts, no SIM)
2. Enable USB debugging, connect to your computer, allow the prompt
3. Run:  ./docs/howto/enroll-one.sh <cloud-url> <enroll-token> <path-to-apk>
4. Watch it appear online in the console within ~30s
```

That's it for one tablet. Repeat per tablet (or use `enroll-all.sh`).

- [first-tablet-test.md](first-tablet-test.md) — click-by-click first-tablet enrollment + app push test
