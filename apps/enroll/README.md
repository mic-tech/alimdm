# ADB Enrollment — locking the tablets down

One-time setup per tablet. After this, the tablet is a **Ali MDM Device Owner**
enrolled to the cloud: it pulls the lockdown config (the 3 apps, kiosk mode,
factory-reset blocked) and stays locked. Changing the app list later is a cloud
config change — no re-enrollment.

## Prerequisites (on the machine running ADB)
- `adb` on PATH (Android platform-tools), USB debugging enabled on the tablet
- Python 3.8+
- Optional, for QR mode: `pip install -r requirements.txt`
  (on PEP-668 systems: `pip install --break-system-packages -r requirements.txt`,
   or use a venv). Without it, `--mode qr` prints the URL to encode with any QR tool.

## The enroll token
Must match `FK_ENROLL_TOKEN` on the cloud server. Keep it secret — anyone with it
can enroll a device. Generate one and set it on the server:
```
export FK_ENROLL_TOKEN="$(openssl rand -hex 24)"
```

## Two ways to enroll

### A. Zero-touch (QR) — best for a fresh tablet
Factory-reset the tablet, then during its **first setup** (the "Set up device"
wizard) scan the QR. The tablet becomes a Device Owner and auto-enrolls.
```
python3 enroll.py --cloud https://cloud.school.local \
    --token "$FK_ENROLL_TOKEN" --org mic-tech --mode qr --qr-out qr.png
```
Print/display `qr.png`. The teacher scans it at the setup wizard. Done.

### B. Hands-on (ADB push) — for a tablet already past setup
If the tablet is already set up (has a user), use ADB to set Device Owner and
push the enrollment so it auto-enrolls on next launch:
```
python3 enroll.py --cloud https://cloud.school.local \
    --token "$FK_ENROLL_TOKEN" --org mic-tech --mode push \
    --apk alimdm-release.apk
```
Notes:
- `dpm set-device-owner` only works from a **fresh factory reset** (no user, no
  SIM). If the tablet already has a user/owner, factory-reset first, then use
  either mode. `--skip-owner` skips the owner step if it's already set.
- `--apk` installs/updates the Ali MDM APK first (build with
  `cd alimdm/android && ./gradlew assembleRelease`).

## Enrolling all 12
Loop over connected devices (each must be freshly reset first):
```
for s in $(adb devices | awk 'NR>1 && /device/{print $1}'); do
  python3 enroll.py --cloud https://cloud.school.local \
    --token "$FK_ENROLL_TOKEN" --org mic-tech --mode push \
    --serial "$s" --apk alimdm-release.apk
done
```

## Verify
After enrollment, the tablet appears **online** in the operator console within
~30s (one heartbeat). It should be showing the first app in the list, locked.

## Troubleshooting
- **"device already has a different owner"** → factory-reset, retry.
- **App doesn't auto-enroll** → confirm `FK_ENROLL_TOKEN` matches; check the
  tablet has Wi-Fi; watch the console for the device appearing.
- **QR won't scan** → make sure the tablet is on the *setup wizard* screen (not
  the home screen); regenerate with a higher-contrast QR.
