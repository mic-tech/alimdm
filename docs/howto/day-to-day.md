# Day-to-Day Management

Once your tablets are enrolled, this is how you manage them from the console
(`https://cloud.yourdomain.com`). No ADB needed for any of this.

## The console at a glance

| Tab | What you do there |
|---|---|
| **Devices** | Every tablet live — online, battery, Android version, free storage. Open one for its screen, its files, its installed apps and its history. Send commands. |
| **Groups** | Edit a policy: managed apps, kiosk mode, return gesture, everything a group's tablets follow. |
| **Activity** | What the fleet and its operators have done, newest first. An admin can clear it; the clear itself stays on the record. |
| **Enroll** | Generate a provisioning QR (group + label baked in), or the ADB command for a tablet on a cable. |
| **Packages** | Upload APKs and split bundles (`.xapk`/`.apks`), then push silent installs. |
| **Files** | The document library. Upload a folder and it keeps its shape all the way to the tablet's inbox. |
| **App update** | Stage a build of Ali MDM itself and roll it out over the air. The staged build is also what a scanned QR installs. |
| **Users** *(admin)* | Who can sign in, and what they may do. |
| **Enroll** | Generate a QR to enroll new or replacement tablets. |
| **Packages** | Upload APKs to install on devices. |
| **Files** | Upload documents and audio, and send them to tablets. |
| **App update** | Publish a new Ali MDM build and roll it out. |

---

## Change the app list (most common task)

Say you want to add a 4th app or remove one:

1. Go to **Groups** and open the group the tablets are in.
2. Edit **Managed Apps** — add a package name (e.g. `com.example.newapp`) or
   remove one. **Show on Home screen** off keeps an app allowed without putting
   it on the grid, which is what a file picker or a camera app wants.
3. (Optional) toggle **Kiosk mode** on/off.
4. Click **Save**.

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

## Send files to tablets

**Files** is for documents, worksheets and audio — anything that is not an app.

1. **Upload file** for one, or **Upload folder** for a whole tree. A folder keeps
   its shape: sub-folders and all.
2. The library shows folders you can open and close. Sizes and delivery counts
   on a folder row cover everything inside it, so a closed folder still tells you
   where things got to.
3. **Send folder** sends everything beneath it in one go, at any depth. **Send to
   devices** on a single file sends just that one.
4. Choose who gets it: **every enrolled device**, **a group**, or **chosen
   devices** — tick the tablets you want. The button says what it is about to do
   ("Send 3 files to 2 devices") before you press it.

Files land in **`Download/Ali MDM`** on the tablet, with the same folder
structure they had here, where the tablet's own Files app can open them. Offline
tablets collect on their next check-in, so sending to one that is switched off is
fine. Deleting from the library does **not** remove copies already on tablets;
the device's own **Files** tab does that.

> A tablet takes files in batches of 25 per check-in rather than all at once, so
> a folder of 200 tracks arrives over a few minutes. That is deliberate: if a
> tablet reboots mid-download, only that batch is affected and the rest are still
> queued.

---

## Push a new APK to all devices

To update an app (or add a new one that isn't on the tablets yet):

1. Go to **📦 APKs**.
2. **Upload** the `.apk` file — or a `.xapk` / `.apks`, for an app that ships as
   a base plus config splits. The archive is unpacked here and its APKs are
   installed together in one go; it is listed under its package name, and the
   install dialog fills that name in for you.
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
a live view. Ali MDM can always capture **its own kiosk**. Capturing **another
app** — Chrome, the Quran app, whatever a pupil is actually in — goes through
its accessibility service.

Two things have to be true for that, and until 1 September 2026 neither was:

1. **The build must ship the service.** Every release before build 63 disabled
   the component in the manifest, so it did not exist as far as Android was
   concerned: absent from Settings → Accessibility, impossible to enable by any
   means. Fixed in build 63.
2. **The app must be able to switch it on**, which needs `WRITE_SECURE_SETTINGS`.
   `enroll.py` grants it over ADB during enrolment. With it, the app enables the
   service itself the first time capture needs it — no reboot, nobody at the
   device.

A tablet enrolled by QR never sees ADB, so it does not have the permission.
Someone has to enable the service once, on the device: Ali MDM **Settings →
Advanced → Open Accessibility Settings** → turn **Ali MDM** on. If Android greys
the toggle out as a *restricted setting*, allow it first under Settings → Apps →
Ali MDM → ⋮ → **Allow restricted settings**. Or run the grant by hand and let the
app do the rest:

```
adb shell pm grant com.alimdm android.permission.WRITE_SECURE_SETTINGS
```

When capture of another app is not working, the device's **Commands** tab says
which of the two is missing, in the `screenshot` rows.

Capture of another app also requires **Allow remote screenshots** in the policy
(Security → Lock Mode). Lock Mode blacks out screen capture; that setting lets
the device lift the block for the fraction of a second the picture takes, which
also re-enables the pupil's own Power+Volume Down screenshot for that moment.

A capture that comes back black is usually a device whose screen is off — send
**Wake** first.

---

## Backing up

Everything the console knows — devices, groups, policies, operators, the file
catalogue — is one SQLite database. Back it up with:

```bash
./deploy/backup.sh
```

It writes a timestamped, verified snapshot to `~/backups` and keeps the last 14.
Run it nightly with a systemd timer (a minimal cloud image often has systemd but
no `crontab`):

```bash
sudo systemctl enable --now alimdm-backup.timer
```

The unit files are `alimdm-backup.service` and `alimdm-backup.timer` in
`/etc/systemd/system/`. Check it with `systemctl list-timers alimdm-backup.timer`
and force a run with `sudo systemctl start alimdm-backup.service`.

> **Do not back it up with `cp`.** The database runs in WAL mode, which keeps
> recent transactions in a separate `-wal` file until a checkpoint folds them
> in. On a live server that file is routinely larger than the database itself,
> and a plain copy takes the database *without* it — a valid file, restoring
> cleanly, silently missing the most recent writes. Copy it mid-checkpoint and
> it can be inconsistent outright. `backup.sh` uses `VACUUM INTO`, which takes a
> consistent snapshot including the WAL while the server keeps running.

Uploaded files and APKs live on disk next to the database, under `data/`. The
database alone restores the console; `data/` as a whole restores the library
with it.

---

## Security reminders

- **Keep your enroll token secret** — anyone with it can enroll a device.
- **Keep your console password strong** — it's your admin access.
- **HTTPS is mandatory** — your cloud must be on HTTPS (Caddy handles this).
- The device API keys are stored hashed on the server; the enroll token is the
  only secret that grants new enrollments.
