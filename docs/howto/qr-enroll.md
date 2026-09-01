# Enrolling with a QR Code (zero-touch)

The QR path lets a tablet enroll by **scanning a QR code during its first
setup** — no computer, no ADB, no typing. It's the cleanest for a teacher to
run unattended. You generate the QR from the **console website**.

> **Read this first:** the QR sets the tablet up as a Device Owner and stores
> the enrollment. But the **Ali MDM app itself still has to be installed**
> for it to exist and read that enrollment. There are two ways to handle that —
> see "The APK question" below. Start with the ADB path (`adb-enroll.md`) until
> you've confirmed everything works on one tablet, then use QR for the rest.

---

## 1. Generate the QR from the console

1. Log in to `https://cloud.yourdomain.com`
2. Click **➕ Enroll (QR)** in the sidebar
3. Fill in:
   - **Cloud URL** — pre-filled with your console address
   - **Organization ID** — pre-filled (`mic-tech`)
   - **Enroll token** — paste your `FK_ENROLL_TOKEN` (from the server's `.env`)
4. Click **Generate QR**
5. **Download PNG** (to print) — or just leave it on screen to hold up

**The same QR works for every tablet.** Generate once, reuse for all 12.

---

## 2. The APK question (the important part)

The QR makes the tablet a Device Owner, but Ali MDM must be installed for the
lockdown to actually run. Two options:

### Option A — QR + ADB install (hybrid, most reliable)
Use the QR to set Device Owner + enrollment, and ADB just to drop the APK on:
1. Factory-reset the tablet.
2. During first setup, **scan the QR** (tablet becomes Device Owner, enrollment stored).
3. If the app isn't installed yet, connect via USB and run just:
   ```bash
   adb install -r alimdm-release.apk
   adb shell am start -n com.alimdm/.MainActivity
   ```
4. Ali MDM launches, reads the stored enrollment, and locks down.

### Option B — Full zero-touch (QR installs the app too)
The provisioning QR can carry an app-install instruction so the tablet
downloads and installs Ali MDM automatically during setup — truly hands-free.
This requires:
- The Ali MDM APK **hosted at a public HTTPS URL** (e.g. on your VPS).
- The provisioning payload extended with the app package + download URL.

This is the "fully unattended" end-state. It's more setup, and behavior varies
by Android version/brand, so **only pursue it after the ADB path is proven on
your specific tablets.** (Ask me and I'll extend the QR generator to include the
install.)

---

## 3. Scanning the QR — where exactly

- The QR must be scanned **during the Android setup wizard** (the "Choose
  language / Connect to Wi-Fi / Set up device" screens), **not** from the home
  screen.
- Use the tablet's **camera** on the setup screen, or a QR-scanner app if the
  camera doesn't offer it.
- Android will recognize it as a **device-management** code and prompt to
  confirm — **accept it**.

> If your tablet's setup wizard doesn't offer a QR/MDM scan (some OEM skins
> hide it), the ADB path is your fallback — it always works.

---

## 4. Verify

Same as the ADB path: open the console → **Devices** → the tablet appears
online within ~30s and is locked to your whitelist.

---

## 5. The one step QR cannot do for you

Ali MDM can capture its own kiosk screen on any tablet. Capturing **another
app** — Chrome, the Quran app, whatever a pupil is actually in — goes through
its accessibility service, and Android only lets the app switch that on by
itself while it holds `WRITE_SECURE_SETTINGS`. That permission is granted over
ADB, which the QR path never uses:

```
adb shell pm grant com.alimdm android.permission.WRITE_SECURE_SETTINGS
```

So on a QR-enrolled tablet, someone has to do one of these once:

- **On the tablet, no computer:** Ali MDM **Settings → Advanced → Open
  Accessibility Settings** → turn **Ali MDM** on. The permission wizard shown
  after enrolment lists this too and takes you straight there. If Android greys
  the toggle out as a *restricted setting*, allow it under Settings → Apps →
  Ali MDM → ⋮ → **Allow restricted settings**.
- **With a computer, once:** run the `pm grant` above, over USB or wireless
  debugging. This is the durable one — the app can then re-enable the service
  after a reboot or an update by itself, which the manual toggle does not
  guarantee on Android 13+.

Everything else about the tablet works without this. Only screenshots and live
view of other apps are affected: without it they show the Ali MDM kiosk and
nothing more.

---

## Which should you use?

| Situation | Use |
|---|---|
| You have a computer + USB, doing 12 tablets in a batch | **ADB** (`adb-enroll.md`) |
| A teacher needs to set up a tablet with no computer | **QR** (this guide) |
| You want fully unattended, hands-off enrollment | **QR + hosted APK** (Option B) |

For your school, I'd **start with ADB for all 12** (one session, reliable),
then use the QR for any future/replace tablets a teacher handles alone.
