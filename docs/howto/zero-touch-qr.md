# QR Enrollment (no cable, no ADB)

> **This is not Google "zero-touch enrollment".** Zero-touch is a separate
> programme where devices are registered to your organisation by an authorised
> reseller at purchase, and it does require a relationship with Google. The flow
> below is plain AOSP setup-wizard provisioning: it works with your own
> self-signed APK served from your own host, with no Google approval at all.
> The two are easy to confuse, and the confusion tends to end in "QR doesn't
> work for us" when in fact it was never tried.

The "scan a QR and it just works" flow. During the tablet's **first setup**,
Android's setup wizard reads a special QR code, **downloads + installs
Ali MDM automatically**, makes it the **Device Owner**, and enrolls it to
your cloud — all hands-free. This is the same mechanism Headwind MDM and other
enterprise MDMs use.

## How it works

The QR encodes an `androidenterprise://provisionDevice` URI with these fields:

| Field | Purpose |
|---|---|
| `PROVISIONING_DEVICE_ADMIN_COMPONENT_NAME` | Which app becomes Device Owner (`com.alimdm/.DeviceAdminReceiver`) |
| `PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION` | URL the wizard downloads the Ali MDM APK from |
| `PROVISIONING_DEVICE_ADMIN_SIGNATURE_CHECKSUM` | URL-safe base64 of the APK signing cert's SHA-256 — proves the download is the legit, unmodified Ali MDM |
| `PROVISIONING_ADMIN_EXTRAS_BUNDLE` | base64 JSON handed to Ali MDM: `{enroll_token, cloud_url, org_id}` |
| `PROVISIONING_WIFI_SSID` / `PASSWORD` *(optional)* | Wi-Fi creds so the tablet can download without you typing a password |

When the setup wizard scans it:
1. Downloads the Ali MDM APK from your server
2. Verifies the signature against the checksum
3. Installs it silently + sets it as Device Owner
4. Hands it the `admin_extras` → Ali MDM auto-enrolls to your cloud
5. Your 3 school apps get silently installed + locked on the next heartbeat

**No cable, no ADB, no computer. One QR works for every tablet.**

## Prerequisites

1. **Cloud server reachable from the tablet's network.**
   - Local test: `http://192.168.1.100:8090` (tablet on the same Wi-Fi/LAN)
   - Production: `https://cloud.yourdomain.com` (HTTPS required — see note below)
2. **A build staged on the App update page.** That upload is what a scanned
   tablet downloads and installs — the same APK the fleet is updated to, so a
   new tablet cannot arrive on an older build than everything else. There is no
   configured fallback: with nothing staged, the Enroll page refuses to produce
   a QR and says why.
3. **The signing-cert checksum** matching that exact APK. Derive it from the
   build you actually serve — never copy one from documentation:
   ```bash
   apksigner verify --print-certs apk/alimdm-release.apk   # take the SHA-256 digest
   python3 -c "import base64,sys; print(base64.urlsafe_b64encode(bytes.fromhex(sys.argv[1])).decode().rstrip('='))" <digest>
   ```
   Set it on the server as `ALIMDM_PROVISION_CHECKSUM`; the console reads it.
   Note this is the digest of the *signing certificate*, not of the APK file —
   confusing the two fails only after the tablet has downloaded everything,
   which looks like a network problem.
4. A **factory-fresh** tablet (no Google account, no SIM) on the network.

> **HTTPS note:** Android's setup wizard is strict about the download URL.
> For a **local LAN test**, `http://192.168.1.100:8090` generally works because
> the tablet is on a trusted private network. For **production**, use HTTPS
> (Let's Encrypt via Caddy) — some Android versions reject plain-HTTP downloads
> for device-owner provisioning. If a local test fails on the download step,
> that's the likely cause; the ADB path (`adb-enroll.md`) always works as a
> fallback.

## Step-by-step

### 1. Generate the QR in the console
1. Open the console → **Enroll** tab → *Enroll by QR (no cable)*
2. Fill in what the code should carry. The cloud URL, org and enrol token come
   from the server's own config — you never type them:
   - **Enroll into group** — the policy group the tablet lands in. Leave it on
     *(server default)* and it goes to `default`.
   - **Device label** *(optional)* — names the tablet in the console from the
     moment it enrols. One code carries one label, so a per-tablet name means a
     code per tablet; left blank the tablet shows as its id until renamed.
   - **Wi-Fi SSID / password** *(optional)* — lets the tablet reach the server
     before anyone has typed anything into it. Both are embedded in the QR.
3. Click **Generate QR**. The code is only drawn when it would actually work: if
   no build is staged or the checksum is unset, the page says so instead, rather
   than handing you a code that fails after the download.

### 2. Factory-reset the tablet
- Settings → System → Reset → **Erase all data (factory reset)**
- Make sure **no Google account** is added and **no SIM** is inserted

### 3. Scan the QR during first setup
- Power on the tablet → it starts the **Android setup wizard**
- When it asks for Wi-Fi, either:
  - connect manually, **or**
  - if you put Wi-Fi creds in the QR, it may connect automatically
- On the **very first setup screen, tap six times** — that opens the QR scanner
  (there is no visible button for it) — then **scan the QR**
- The wizard will:
  - download + install Ali MDM
  - set it as Device Owner
  - hand it the enrollment credentials
- Follow any on-screen prompts to finish setup

### 4. Verify enrollment
- Watch the **Devices** tab in the console — the tablet should appear **online**
  within ~30 seconds
- The tablet should be **locked to the apps in its group's whitelist**. A fresh
  install starts with an empty whitelist, so add the packages first if you want
  to see the lockdown on the first tablet.

## What to check if it doesn't work

| Symptom | Likely cause | Fix |
|---|---|---|
| Wizard won't download the APK | Plain-HTTP rejected by Android | Use HTTPS (Caddy + Let's Encrypt) |
| "Signature verification failed" | Checksum doesn't match the APK | Regenerate the checksum for the exact APK you're hosting (see below) |
| Tablet downloads but doesn't enroll | Wrong `enroll_token` or `cloud_url` | Check the QR's `admin_extras` match your server config |
| Tablet enrolls but apps don't install | APKs not uploaded to console | Upload the 3 school APKs in the **APKs** tab, add to whitelist, Save & push |
| QR won't scan | Low-res print / screen too small | Print larger, or use error-correction level H |

## The signing-cert checksum

The QR carries the SHA-256 of the **signing certificate** of the APK the wizard
downloads. It is not a hash of the APK file — that is a different provisioning
field — and a wrong value fails only *after* the tablet has pulled everything
down, which looks exactly like a network fault.

You do not set it per QR. It comes from `ALIMDM_PROVISION_CHECKSUM` on the
server, and the console refuses to draw a scannable code without it. Whoever
signs the APK you stage decides the value, so it changes only when you change
signing keys.

To work it out for a build:

```bash
# 1. The signing cert's SHA-256 digest, in hex
apksigner verify --print-certs your-alimdm.apk | grep -oP '(?<=SHA-256 digest: )\S+'

# 2. hex -> raw bytes -> url-safe base64, no padding
python3 -c 'import base64,sys; print(base64.urlsafe_b64encode(bytes.fromhex(sys.argv[1])).decode().rstrip("="))' <HEX-DIGEST>
```

Set that as `ALIMDM_PROVISION_CHECKSUM` and restart the API.

## ADB fallback

If zero-touch QR doesn't work on a particular tablet/Android version, use the
ADB path — it always works: see [`adb-enroll.md`](adb-enroll.md).
