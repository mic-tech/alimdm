# Day-to-Day Management

Once your tablets are enrolled, this is how you manage them from the console
(`https://cloud.yourdomain.com`). No ADB needed for any of this.

## The console at a glance

| Tab | What you do there |
|---|---|
| **📱 Devices** | See all tablets live (online, battery, Android version). Send commands. |
| **➕ Enroll (QR)** | Generate a QR to enroll new/replace tablets. |
| **🔒 Apps & Lockdown** | Change the app whitelist, toggle kiosk mode. |
| **📦 APKs** | Upload APKs to push-install to devices. |

---

## Change the app list (most common task)

Say you want to add a 4th app or remove one:

1. Go to **🔒 Apps & Lockdown**.
2. Edit the **App whitelist** — add a package name (e.g. `com.example.newapp`) or
   remove one.
3. (Optional) toggle **Kiosk mode** on/off.
4. Click **Save & push to devices**.

**What happens:** the config version bumps. Every enrolled tablet picks up the
change on its next heartbeat (within ~30 seconds) — **no factory reset, no ADB,
no touching the tablets.** They just start running the new app list.

> **Finding package names:** the package name is the app's internal ID (like
> `com.gplanet_tech.noraneya`). You can find it in the Play Store listing's
> "App info", or on a device via `adb shell pm list packages | grep <name>`.

---

## Send a command to a device

From **📱 Devices**, each tablet has action buttons:

- **Reboot** — restart the tablet.
- **Lock** — immediately lock it into kiosk (e.g. if a kid got it into a weird state).
- **Unlock** — release the lock (to make a change, then re-lock).
- **Unenroll** — remove it from management (see `troubleshooting.md`).

Commands are delivered on the device's next heartbeat (≤30s) or immediately if
you have MQTT enabled.

---

## Push a new APK to all devices

To update an app (or add a new one that isn't on the tablets yet):

1. Go to **📦 APKs**.
2. **Upload** the `.apk` file.
3. The APK is now available for devices to install. (Pair this with adding the
   app to the whitelist in **Apps & Lockdown** so devices know to install it.)

Devices install new APKs silently in the background.

---

## Check on a tablet

**📱 Devices** shows, live:
- **Online / offline** (last heartbeat)
- **Battery** %
- **Android version**
- **Model**
- **Group** (which config it's following)

If a tablet shows **offline**, it's either off, out of Wi-Fi range, or lost
connection. Check the tablet's power + Wi-Fi first.

---

## The escape hatch (for you, the teacher)

Ali MDM has a hidden way for *you* to get back in without a factory reset:

1. On the tablet, **tap the bottom-right corner 5 times** quickly.
2. A **PIN keypad** appears.
3. Enter your **PIN** (the one configured in the app's settings).
4. You're now in Ali MDM's admin view — you can change settings, exit kiosk,
   etc.

> **Set this PIN during initial setup** and write it down somewhere safe. Kids
> can't use it without the PIN. If you forget it, the only reset is a factory
> reset + re-enroll.

> Where the PIN lives: it's stored in Ali MDM's settings. When you configure
> a tablet (or via the cloud config's `security` section), set it there. Tell me
> if you want the console to manage the PIN centrally.

---

## Replacing a broken/lost tablet

1. Get a replacement tablet.
2. Factory reset it.
3. Enroll it (ADB or QR — see the other guides).
4. In the console, the old tablet will show **offline** after a while — you can
   **unenroll**/remove it from your records.

The new tablet inherits the same group config automatically.

---

## Seeing a device's screen

The Devices page shows a still of each screen, and a device's own page can hold
a live view. Both need to be able to capture whatever is on screen.

Ali MDM can always capture **its own kiosk**. To capture **another app** — which
is what you want when a pupil is in Chrome or the Quran app — it needs its
accessibility service, and Android only lets the app switch that on if it holds
`WRITE_SECURE_SETTINGS`. That is granted once, over ADB, at enrolment:

```
adb shell pm grant com.alimdm android.permission.WRITE_SECURE_SETTINGS
```

`enroll.py` does this for you. On a tablet enrolled before September 2026 the
grant ran before the app was installed and quietly did nothing, so it has to be
run by hand once — plug the tablet in, run the command, and that is the end of
it. The app enables the service by itself from then on, including after a
reboot.

Two ways to tell whether a tablet is in this state:

- Its **Commands** tab shows `screenshot` rows that failed, and the reason.
- Its screen still shows the Ali MDM kiosk but never anything else.

Capture of another app also requires **Allow remote screenshots** in the policy
(Security → Lock Mode). Lock Mode blacks out screen capture; that setting lets
the device lift the block for the fraction of a second the picture takes, which
also re-enables the pupil's own Power+Volume Down screenshot for that moment.

---

## Security reminders

- **Keep your enroll token secret** — anyone with it can enroll a device.
- **Keep your console password strong** — it's your admin access.
- **HTTPS is mandatory** — your cloud must be on HTTPS (Caddy handles this).
- The device API keys are stored hashed on the server; the enroll token is the
  only secret that grants new enrollments.
