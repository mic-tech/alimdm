# First Tablet — End-to-End Test

Step-by-step for enrolling your first tablet and pushing the 3 school apps to it.
Everything runs locally on the workstation for now (no VPS needed).

**Test server:** `http://192.168.1.100:8090`
**Console login:** `admin@school.local` / `testpass123`

---

## 0. Prerequisites (one-time)

- [ ] Test server running: `docker ps | grep fk-cloud-test` (should be `Up`)
- [ ] Console reachable: open `http://192.168.1.100:8090/console/` in a browser
- [ ] The 3 school APKs uploaded (they already are — verify in the **APKs** tab)
- [ ] Tablet **charged**, and you have its **Wi-Fi password** (the tablet must join the
      same network as the workstation to reach `192.168.1.100`)

> If the server isn't running, start it:
> ```
> cd /path/to/ali-mdm
> docker run -d --name fk-cloud-test \
>   -e FK_ADDR=:8080 -e FK_DB=/data/alimdm.db -e FK_CONSOLE_DIR=/app/console-dist \
>   -e FK_APK_ROOT=/data/apks -e FK_PROVISION_APK=/data/provision/alimdm.apk \
>   -e FK_ENROLL_TOKEN=test-enroll-token-123 -e FK_BASE_URL=http://192.168.1.100:8090 \
>   -v /tmp/fktest-data:/data -p 8090:8080 ali-mdm:test
> docker exec -w /app fk-cloud-test ./bootstrap --db /data/alimdm.db \
>   --email admin@school.local --password testpass123
> ```

---

## 1. Factory-reset the tablet

1. **Settings → System → Reset → Erase all data (factory reset)**
2. Confirm. The tablet reboots into the **Android setup wizard**.
3. **Do NOT sign in to a Google account.** We want a clean, unmanaged device.

> If the tablet already has a Google account / work profile, the factory reset removes it.
> This is required — Device Owner enrollment only works on a fresh (or wiped) device.

---

## 2. Enroll the tablet (pick ONE method)

### Method A — Zero-touch QR (no cable)

1. On the workstation, open the console → **Enroll** tab.
2. It auto-fills:
   - **APK download URL:** `http://192.168.1.100:8090/api/v1/provision/apk`
   - **Signing-cert checksum:** `XrO7g750-vbu3SoiA97qM2HM6G0m24WfJI3EzAkDB2E`
   - **Enroll token:** `test-enroll-token-123`
   - **Wi-Fi SSID / password:** *(fill these in so the tablet can download the APK)*
3. Click **Generate QR**. A QR code appears.
4. On the tablet's setup wizard, choose **"Set up with a QR code"** / scan option
   (or just open the **Camera** app and point it at the QR).
5. The wizard **downloads + installs Ali MDM**, makes it the **Device Owner**, and
   **enrolls** the tablet to the cloud.
6. Wait ~30s → the tablet appears in the console **Devices** tab as **online**.

> **If the wizard refuses the plain-HTTP download** (some Android versions require
> HTTPS for the provisioning APK): use **Method B** instead, or deploy to the VPS with
> HTTPS (Caddy + Let's Encrypt) and regenerate the QR with the `https://` URL.

### Method B — ADB push (cable, most reliable)

1. On the tablet: **Settings → About tablet → tap "Build number" 7 times** → enables
   Developer Options. Then **Developer options → USB debugging = ON**.
2. Plug the tablet into the workstation via USB. Accept the **"Allow USB debugging?"** prompt.
3. On the workstation:
   ```
   cd /path/to/ali-mdm
   ./docs/howto/enroll-one.sh http://192.168.1.100:8090 test-enroll-token-123
   ```
4. The script installs Ali MDM, sets it as Device Owner, and enrolls it. ~2–3 min.
5. The tablet appears in the console **Devices** tab as **online**.

---

## 3. Verify enrollment

1. Console → **Devices** tab.
2. Your tablet should be listed with:
   - **Online** status (green dot)
   - Its model name
3. If it's not showing after ~1 min, check:
   - Is the tablet on the same Wi-Fi as the workstation?
   - Console → **Devices** → click the device → is `last_seen` recent?

---

## 4. Push the 3 school apps

For **each** of the 3 apps, do this:

1. Console → **APKs** tab.
2. Find the APK row and click **Install to devices**.
3. A picker opens. **Check your tablet** (or "Select all").
4. In the **package name** field, enter the app's package:
   - Noraneya → `com.gplanet_tech.noraneya`
   - Quran Majeed → `com.pakdata.QuranMajeed`
   - Adnan → `com.tagmedia.adnan`
5. Click **Queue install**. You'll see a toast: *"Queued 1/1 device(s) to install …"*.

> Repeat for all 3. (You can also do all 3 at once by queueing each — the tablet
> processes them on its next poll.)

---

## 5. Watch the apps install

1. The tablet polls the cloud every ~30s. On its next poll it picks up the 3 queued
   APKs and **silently downloads + installs** them (Device Owner privilege — no user
   taps needed).
2. This takes a few minutes (the APKs total ~185 MB).
3. Console → **Devices** → your tablet should now show the apps as installed/managed.

> **To confirm the install worked:** on the tablet, the 3 apps should appear and be
> launchable. Ali MDM's kiosk mode will lock the home screen to them.

---

## 6. Confirm lockdown

1. The tablet's home screen should now show **only** the 3 whitelisted apps.
2. Try to open **Settings**, a **browser**, or **install anything** → it should be
   blocked (kiosk mode).
3. **Escape hatch:** tap anywhere **5 times** → enter the PIN (if configured) to exit
   kiosk mode temporarily.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Tablet never shows in Devices | Not on the right Wi-Fi, or QR download blocked | Check Wi-Fi; use ADB (Method B) |
| QR scan does nothing | Android requires HTTPS for provisioning APK | Use ADB, or deploy VPS with HTTPS |
| Apps don't install after queuing | Tablet offline, or package name typo | Check Devices is online; re-check the exact package name |
| "apk not found" when queuing | APK not uploaded | Re-upload in the APKs tab |
| Console login fails | Operator account missing | Run the `bootstrap` command in step 0 |

---

## What to tell me after the test

- Did the tablet **enroll** (show online in Devices)?
- Did the **3 apps install** silently?
- Is the home screen **locked** to just those 3 apps?
- Any errors in the console or on the tablet?

Report back and I'll help debug anything that's off.
