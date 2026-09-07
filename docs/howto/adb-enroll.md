# Enrolling a Tablet with ADB

The cable route. Use it for a tablet already past its setup wizard, or when QR
provisioning is blocked on a particular Android build — for a factory-fresh
tablet, [`zero-touch-qr.md`](zero-touch-qr.md) needs no cable and is the path
the fleet is enrolled with.
## TL;DR — one command

Once `adb` is installed and the tablet is connected + authorized:

```bash
cd ali-mdm
./docs/howto/enroll-one.sh https://cloud.yourdomain.com "YOUR_ENROLL_TOKEN"
```

That's it. The script auto-finds the APK in `apk/`, installs it, sets Device
Owner, grants permissions, pushes the enrollment, and restarts the app. The
tablet appears in your console within ~30 seconds.

(For the full step-by-step, keep reading.)
 You do it over USB, one
tablet at a time. Each tablet takes about 2–3 minutes once you're set up.

You'll need:
- A computer (Windows, Mac, or Linux)
- A USB cable
- The Ali MDM APK file (see `build-apk.md`)
- Your cloud URL (e.g. `https://cloud.yourdomain.com`)
- Your enroll token (the `ALIMDM_ENROLL_TOKEN` from your server's `.env`)

---

## 1. Install ADB (once, on your computer)

**Windows**
1. Download [SDK Platform Tools](https://dl.google.com/android/repository/platform-tools-latest-windows.zip) (~15 MB).
2. Extract to `C:\platform-tools\`.
3. Open **Command Prompt** in that folder (`cd C:\platform-tools`).

**Mac**
```bash
brew install android-platform-tools
```

**Linux (Ubuntu/Debian)**
```bash
sudo -S -p '' apt install adb
```

Check it works:
```bash
adb version
```

---

## 2. Prepare the tablet

### a. Factory reset (required)
Device Owner can only be set on a tablet with **no user accounts and no SIM**.
The cleanest way is a factory reset:

- **Settings → System → Reset → Erase all data (factory reset)**
- Let it finish and reboot into the setup wizard.

> **Do NOT sign in to any Google/Samsung/Microsoft account and do NOT insert a
> SIM** before you set Device Owner. If you must skip the full reset, you can
> instead remove every account (Settings → Accounts) and the SIM — but a fresh
> reset is less error-prone.

### b. Get past the setup wizard (just enough to use ADB)
You need the tablet far enough along to enable USB debugging:
1. Choose a language.
2. Connect to **Wi-Fi** (the tablet needs internet later to reach your cloud).
3. Skip / dismiss the Google account sign-in if it appears (you can do this —
   you're not adding an account, just getting to the home screen).

> If your tablet **forces** Google sign-in and you can't get to the home screen
> without an account, do the ADB enrollment *during* the wizard instead — see
> the note in `troubleshooting.md` ("Google sign-in is forced").

### c. Enable USB debugging
1. **Settings → About tablet → tap "Build number" 7 times** → "You are now a developer!"
2. **Settings → System → Developer options → turn on "USB debugging"**

---

## 3. Connect and authorize

1. Plug the tablet into your computer with the USB cable.
2. The tablet shows **"Allow USB debugging?"** → tap **Allow** (tick "Always allow from this computer" to save time on future tablets).
3. On your computer, confirm it's visible:
   ```bash
   adb devices
   ```
   You should see:
   ```
   List of devices attached
   ABC123XYZ    device
   ```
   - If it says `unauthorized` → check the tablet screen for the Allow prompt.
   - If it says `offline` or nothing → try a different cable/port; on Windows
     install the manufacturer's USB driver (see `troubleshooting.md`).

---

## 4. Enroll (the actual commands)

### The easy way — one script
```bash
./docs/howto/enroll-one.sh https://cloud.yourdomain.com "PASTE_ENROLL_TOKEN" /path/to/alimdm-release.apk
```
This does everything: installs the APK, sets Device Owner, grants permissions,
and pushes the enrollment. See the script for what each step does.

### Or, step by step (so you understand each part)

**1. Install the Ali MDM APK**
```bash
adb install -r /path/to/alimdm-release.apk
```
`-r` = replace if already installed.

**2. Set Ali MDM as Device Owner**
```bash
adb shell dpm set-device-owner com.alimdm/.DeviceAdminReceiver
```
Expected output:
```
Success: Device owner set to package com.alimdm
Active admin set to component {com.alimdm/com.alimdm.DeviceAdminReceiver}
```
> If this fails with "not allowed", the tablet has an account or SIM — go back
> to step 2a and factory reset. See `troubleshooting.md`.

**3. Grant the permissions Ali MDM needs**
```bash
adb shell appops set com.alimdm android:get_usage_stats allow
adb shell pm grant com.alimdm android.permission.WRITE_SECURE_SETTINGS
```
(With Device Owner set, most of these are granted automatically — these are
belt-and-braces.)

**4. Push the cloud enrollment**
This writes the enroll token + cloud URL into Ali MDM so it auto-enrolls on
next launch:
```bash
# (the enroll-one.sh script does this; here's the manual form)
adb shell run-as com.alimdm ... # see enroll-one.sh for the exact command
```
The script handles the file-push + permissions correctly — prefer it over doing
this by hand.

**5. Restart the app so it enrolls**
```bash
adb shell am force-stop com.alimdm
adb shell am start -n com.alimdm/.MainActivity
```

---

## 5. Verify

1. Open your console: `https://cloud.yourdomain.com` → **Devices**.
2. Within ~30 seconds the tablet appears **online** (battery, Android version, model).
3. On the tablet, it should be showing your first whitelisted app, locked.
4. Try the Home button / recents / Settings — none should work. That's the lockdown.

**Done.** Unplug the tablet. It's enrolled and locked.

---

## What just happened (the mental model)

- **Device Owner** = Ali MDM now has the highest level of control Android
  allows a normal app. It can enforce lock-task, block factory reset, hide the
  status bar, and pin itself as the home launcher. A user (or kid) cannot
  remove it without a factory reset — which is itself blocked.
- **Enrolled** = Ali MDM has your enroll token + cloud URL. On launch it
  calls your cloud, gets a device ID + API key, and starts heartbeating. Your
  cloud then pushes the lockdown config (your app whitelist, kiosk on, factory
  reset blocked).
- **Locked** = the config is applied. Only your apps run.

## Next
- Enroll the rest: `enroll-all.sh` (loop) or repeat this per tablet.
- Manage them day-to-day: `day-to-day.md`.
- Something went wrong: `troubleshooting.md`.
