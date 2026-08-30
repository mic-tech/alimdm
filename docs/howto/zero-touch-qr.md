# Zero-Touch QR Enrollment (no cable, no ADB)

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
2. **The Ali MDM APK hosted on the server** via the `FK_PROVISION_APK` env var.
3. **The signing-cert checksum** matching that APK (pre-filled in the console for
   the official Rushb-signed Ali MDM).
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
1. Open the console → **Enroll (QR)** tab
2. Enter:
   - **Cloud URL** — `http://192.168.1.100:8090` (local) or your HTTPS domain
   - **Organization ID** — e.g. `mic-tech`
   - **Enroll token** — your server's `FK_ENROLL_TOKEN`
3. Leave **Zero-touch** checked (the APK download URL + checksum auto-fill)
4. *(Optional)* Enter your Wi-Fi SSID + password so the tablet connects on its own
5. Click **Generate QR** → **Download PNG**

### 2. Factory-reset the tablet
- Settings → System → Reset → **Erase all data (factory reset)**
- Make sure **no Google account** is added and **no SIM** is inserted

### 3. Scan the QR during first setup
- Power on the tablet → it starts the **Android setup wizard**
- When it asks for Wi-Fi, either:
  - connect manually, **or**
  - if you put Wi-Fi creds in the QR, it may connect automatically
- Open the **camera** (or the setup wizard's "scan QR" step) and **scan the QR**
- The wizard will:
  - download + install Ali MDM
  - set it as Device Owner
  - hand it the enrollment credentials
- Follow any on-screen prompts to finish setup

### 4. Verify enrollment
- Watch the **Devices** tab in the console — the tablet should appear **online**
  within ~30 seconds
- The tablet should be **locked to your 3 apps** (kiosk mode)

## What to check if it doesn't work

| Symptom | Likely cause | Fix |
|---|---|---|
| Wizard won't download the APK | Plain-HTTP rejected by Android | Use HTTPS (Caddy + Let's Encrypt) |
| "Signature verification failed" | Checksum doesn't match the APK | Regenerate the checksum for the exact APK you're hosting (see below) |
| Tablet downloads but doesn't enroll | Wrong `enroll_token` or `cloud_url` | Check the QR's `admin_extras` match your server config |
| Tablet enrolls but apps don't install | APKs not uploaded to console | Upload the 3 school APKs in the **APKs** tab, add to whitelist, Save & push |
| QR won't scan | Low-res print / screen too small | Print larger, or use error-correction level H |

## Regenerating the signature checksum for a different APK

If you build/sign your **own** Ali MDM APK (instead of the official Rushb
one), the checksum must match *your* signing cert:

```bash
# 1. Get the signing cert's SHA-256 digest (hex)
apksigner verify --print-certs your-alimdm.apk | grep -oP '(?<=SHA-256 digest: )\S+'

# 2. Convert hex -> raw bytes -> URL-safe base64
python3 - <<'PY'
import base64
hexdigest = "PASTE-HEX-DIGEST-HERE"
raw = bytes.fromhex(hexcipher if False else hexdigest)
b64 = base64.b64encode(raw).decode()
print(b64.replace("+","-").replace("/","_").rstrip("="))
PY
```

Paste that value into the console's **APK signing-cert checksum** field.

## The official Ali MDM signing cert (pre-filled)

The official Rushb-signed Ali MDM APK is signed by:
- **CN=Valentin GOMY, O=Rushb, OU=Dev, L=Mougins, ST=FRANCE, C=33**
- SHA-256 fingerprint: `5E:B3:BB:83:BE:74:FA:F6:EE:DD:2A:22:03:DE:EA:33:61:CC:E8:6D:26:DB:85:9F:24:8D:C4:CC:09:03:07:61`
- **URL-safe base64 checksum (what goes in the QR):** `XrO7g750-vbu3SoiA97qM2HM6G0m24WfJI3EzAkDB2E`

This is pre-filled in the console, so if you use the official APK you don't
need to change anything.

## ADB fallback

If zero-touch QR doesn't work on a particular tablet/Android version, use the
ADB path — it always works: see [`adb-enroll.md`](adb-enroll.md).
