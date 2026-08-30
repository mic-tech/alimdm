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

1. Log in to `https://cloud.yourdomain.com/console/`
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

## Which should you use?

| Situation | Use |
|---|---|
| You have a computer + USB, doing 12 tablets in a batch | **ADB** (`adb-enroll.md`) |
| A teacher needs to set up a tablet with no computer | **QR** (this guide) |
| You want fully unattended, hands-off enrollment | **QR + hosted APK** (Option B) |

For your school, I'd **start with ADB for all 12** (one session, reliable),
then use the QR for any future/replace tablets a teacher handles alone.
