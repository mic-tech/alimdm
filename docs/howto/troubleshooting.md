# Troubleshooting

Fixes for the problems you'll actually hit when enrolling tablets.

## ADB problems

### `adb: command not found`
You're not in the platform-tools folder (Windows) or ADB isn't installed.
- Windows: `cd C:\platform-tools` then retry.
- Otherwise: install ADB (see `adb-enroll.md` §1).

### `adb devices` shows nothing
- Try a different **USB cable** (many cables are charge-only, no data).
- Try a different **USB port** (prefer a port directly on the computer, not a hub).
- On **Windows**, install the manufacturer's USB driver:
  - Samsung: https://developer.samsung.com/android-usb-driver
  - Other: search "[brand] Android USB driver Windows".
- Re-plug the cable; wait a few seconds.

### `adb devices` shows `unauthorized`
The tablet is waiting for you to accept the **"Allow USB debugging?"** prompt.
- Look at the **tablet screen** (not the computer) and tap **Allow**.
- If no prompt appears: unplug, re-plug, or toggle USB debugging off/on.

### `adb devices` shows `offline`
- Toggle USB debugging off, wait, on again.
- `adb kill-server && adb start-server`, then re-check.
- Re-plug the cable.

### Linux: `adb: error: insufficient permissions`
```bash
sudo adb kill-server
sudo adb start-server
adb devices
```
Permanent fix: add a udev rule (see Ali MDM's `docs/adb-configuration.md`).

---

## Device Owner problems

### `dpm set-device-owner` → "not allowed" / "Device owner already set"
Causes, in order of likelihood:
1. **A user account exists** (Google, Samsung, Microsoft) → remove all of them
   (Settings → Accounts), or just factory reset.
2. **A SIM is inserted** → remove it.
3. **Another app is already Device Owner** → factory reset (you can't have two).
4. **The tablet isn't freshly reset** → factory reset and retry *before* adding
   any account.

> The golden rule: **factory reset → no accounts, no SIM → set-device-owner.**
> Do it in that order.

### Device Owner set, but the tablet isn't locked
- Reboot: `adb reboot`, then re-open Ali MDM.
- Confirm the app actually launched and enrolled (check the console — is it
  online?). If it's offline, it never got the config — re-push the enrollment
  (`enroll-one.sh` step 4) and restart the app.
- Some OEM skins need the app to be the **home launcher** — confirm Ali MDM is
  set as default home (it should be, as Device Owner).

---

## Enrollment / cloud problems

### Tablet doesn't appear in the console
Work through this list:
1. **Does the tablet have Wi-Fi / internet?** It must reach your cloud URL.
2. **Is the cloud URL correct and HTTPS?** (Ali MDM requires HTTPS.)
3. **Is the enroll token exactly right?** A single wrong character = 401.
4. **Wait up to 30s** — the app heartbeats every 30s; it may just not have
   ticked yet. Refresh the console.
5. Check the app is actually running (it should be the home screen).

### Console shows the device but it's not locked
- The device enrolled but the **config didn't apply**. In the console, go to
  **Apps & Lockdown** and re-save the config (bumps the version → device
  re-syncs on next heartbeat).
- Confirm **Kiosk mode** is toggled on in the config.

### "401 unauthorized" when enrolling
- Wrong enroll token, or the token in the server's `.env` doesn't match what
  you're passing. They must be identical.

### An app's file picker, camera or share sheet closes after a few seconds

The app opens, you tap "choose a file", the picker appears — and a few seconds
later you are back in the app with no picker. Nothing crashed.

Two things bring a kiosk back to where it thinks it should be: the watchdog and
the overlay service. Both used to relaunch whenever the package in front was not
one they recognised, and a file picker is a different package from the app that
opened it. From **1.2.47** neither does that while lock task is on, because
Android is already deciding what may come to the front — so on 1.2.47 or later
this should not happen at all, and if it does the cause is something else.

On an older build, or with **Kiosk mode (lock task)** switched off, the fix is to
name the helper explicitly:

1. Console → **Devices** → the tablet → **Capture Logs**.
2. Look for a line naming the package that was rejected:
   `Foreground package 'com.google.android.documentsui' is not on the
   managed-apps whitelist`.
3. Console → **Groups** → the group → **Managed Apps** → add that package with
   **Show on Home screen** off. It becomes allowed without appearing to pupils.

> **The package name is not the one you expect.** The document picker is
> `com.android.documentsui` on some tablets and `com.google.android.documentsui`
> on others — the Lenovo TB330FU uses the Google one. Read the name out of the
> log rather than guessing; a policy that names the wrong one looks exactly like
> a policy that names none.

### The app keeps crashing / won't stay open
- Check the Android version is supported (Ali MDM needs Android 8.0+).
- Some very old or heavily-modified tablets have issues — test on a known-good
  tablet first.

---

## "Google sign-in is forced" (can't reach home screen)

Some tablets (especially certain OEM skins) **require** a Google account during
setup before you can get to the home screen / Developer options. You have two
ways around it:

1. **Enroll during the wizard (preferred):** you can run the ADB enrollment
   *while the tablet is still in the setup wizard*, before the Google sign-in
   screen. The tablet just needs USB debugging reachable — on many devices you
   can enable it via ADB directly:
   ```bash
   adb shell settings put global development_settings_enabled 1
   adb shell settings put global adb_enabled 1
   ```
   Then proceed with `enroll-one.sh`. Once it's a Device Owner, the Google
   sign-in becomes skippable.

2. **Sign in, then reset the account:** complete setup with a throwaway Google
   account, then **remove that account** (Settings → Accounts) and re-run
   `set-device-owner`. (This works because removing the account clears the
   blocker — but a fresh factory reset is cleaner if you can do it.)

> If your specific tablets force Google sign-in and neither workaround is
> comfortable, tell me the **brand + Android version** and I'll pin down the
> exact sequence for that model.

---

## Unenrolling / removing a tablet

To take a tablet out of management (e.g. it's being retired):
- **From the console:** Devices → select → **Unenroll** (sends the unenroll
  command; the device removes its admin and resets).
- **Via ADB:**
  ```bash
  adb shell dpm remove-active-admin com.alimdm/.DeviceAdminReceiver
  ```
- **From the app:** tap the secret button 5× (bottom-right) → enter your PIN →
  "Remove Device Owner".

After unenrolling, the tablet is a normal tablet again (factory reset it before
re-enrolling or reusing).
