#!/usr/bin/env python3
"""
Ali MDM Cloud — ADB enrollment for the school tablets.

Makes each tablet a Ali MDM Device Owner and enrolls it to the cloud so it
pulls the lockdown config (3 apps, kiosk mode, factory-reset blocked).

Two enrollment paths, both supported:
  1. QR (zero-touch):  --mode qr      -> prints a setup-wizard QR the teacher scans
  2. Push (hands-on):  --mode push    -> ADB-pushes the enroll token + cloud URL
                                          so the app auto-enrolls on first launch

Usage:
  python3 enroll.py --cloud https://cloud.school.local --token <enroll-token> \
      --apps com.example.one,com.example.two \
      --mode qr|push [--serial <adb-serial>] [--apk alimdm-release.apk]

The enroll token must match ALIMDM_ENROLL_TOKEN on the cloud server.
"""
import argparse
import base64
import json
import os
import subprocess
import sys
import tempfile
import urllib.parse

PKG = "com.alimdm"
ADMIN = f"{PKG}/.DeviceAdminReceiver"


def run(cmd, check=True, quiet=False):
    if not quiet:
        print(f"  $ {cmd}")
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    if check and r.returncode != 0:
        print(f"  !! exit {r.returncode}: {r.stderr.strip() or r.stdout.strip()}")
        raise SystemExit(f"command failed: {cmd}")
    return r


def adb(serial, *args, check=True, quiet=False):
    base = ["adb"] + (["-s", serial] if serial else []) + list(args)
    return run(" ".join(base), check=check, quiet=quiet)


def adb_shell(serial, cmd, check=True, quiet=False):
    return adb(serial, "shell", cmd, check=check, quiet=quiet)


# ── QR generation (setup-wizard provisioning) ────────────────────────────────
def apk_signature_checksum(apk_path):
    """
    PROVISIONING_DEVICE_ADMIN_SIGNATURE_CHECKSUM: url-safe base64, unpadded, of
    the SHA-256 of the APK's *signing certificate*.

    Note this is the certificate digest, not a hash of the APK file — a common
    mix-up with PACKAGE_CHECKSUM, and one that fails at the point the wizard has
    already downloaded the APK, which makes it look like a network problem.
    Read via apksigner so it always matches whatever key actually signed the
    build being served.
    """
    apksigner = os.environ.get("APKSIGNER") or "apksigner"
    r = subprocess.run([apksigner, "verify", "--print-certs", apk_path],
                       capture_output=True, text=True)
    if r.returncode != 0:
        raise SystemExit(f"apksigner failed on {apk_path}: {r.stderr.strip()}\n"
                         "Set APKSIGNER=/path/to/build-tools/<ver>/apksigner")
    for line in r.stdout.splitlines():
        if "certificate SHA-256 digest" in line:
            hex_digest = line.split(":")[-1].strip()
            return base64.urlsafe_b64encode(bytes.fromhex(hex_digest)).decode().rstrip("=")
    raise SystemExit(f"could not read a signing certificate from {apk_path}")


def build_provisioning_payload(cloud_url, token, org_id, apk_url, checksum,
                               group_id="", wifi_ssid="", wifi_password=""):
    """
    Build the JSON the Android setup wizard expects from a provisioning QR.

    This must be a plain JSON object of android.app.extra.PROVISIONING_* keys.
    An `androidenterprise://provisionDevice?...` URI — which an earlier version
    of this script emitted — is a different mechanism entirely and is simply not
    recognised by the wizard, so the scan appears to do nothing.

    Requires no Google relationship: registering with Google is only needed for
    zero-touch enrolment, where devices are enrolled by the reseller at purchase.
    This flow works with a self-signed APK served from your own host.
    """
    extras = {
        "enroll_token": token,
        "cloud_url": cloud_url.rstrip("/"),
        "org_id": org_id,
    }
    if group_id:
        extras["group_id"] = group_id

    payload = {
        "android.app.extra.PROVISIONING_DEVICE_ADMIN_COMPONENT_NAME": ADMIN,
        "android.app.extra.PROVISIONING_DEVICE_ADMIN_PACKAGE_DOWNLOAD_LOCATION": apk_url,
        "android.app.extra.PROVISIONING_DEVICE_ADMIN_SIGNATURE_CHECKSUM": checksum,
        "android.app.extra.PROVISIONING_ADMIN_EXTRAS_BUNDLE": extras,
        # The tablets have no user data to protect at provisioning time, and
        # forcing encryption adds a reboot to every enrolment.
        "android.app.extra.PROVISIONING_SKIP_ENCRYPTION": True,
        "android.app.extra.PROVISIONING_LEAVE_ALL_SYSTEM_APPS_ENABLED": True,
    }
    if wifi_ssid:
        payload["android.app.extra.PROVISIONING_WIFI_SSID"] = wifi_ssid
        if wifi_password:
            payload["android.app.extra.PROVISIONING_WIFI_PASSWORD"] = wifi_password
            payload["android.app.extra.PROVISIONING_WIFI_SECURITY_TYPE"] = "WPA"
    return json.dumps(payload, separators=(",", ":"))


def make_qr(data, out_path):
    """Render a QR code PNG. Uses qrcode lib if present, else falls back to
    printing the payload so the teacher can encode it with any QR tool."""
    try:
        import qrcode
        img = qrcode.make(data)
        img.save(out_path)
        return out_path
    except ImportError:
        print("  (qrcode lib not installed — payload below; encode with any QR generator)")
        print(f"  {data}")
        return None


# ── Device Owner via ADB ─────────────────────────────────────────────────────
def set_device_owner(serial):
    """
    dpm set-device-owner only works from a fresh factory reset (no user accounts,
    no SIM). We verify preconditions and set the owner.
    """
    print("[1/4] Setting Ali MDM as Device Owner...")
    # Preconditions: device must be freshly reset. Check for existing owner.
    r = adb_shell(serial, f"dpm list-owners", check=False)
    out = r.stdout or ""
    if "DeviceOwner" in out and PKG not in out:
        print("  !! Another app is already Device Owner. Factory-reset the tablet first.")
        raise SystemExit("device already has a different owner")
    if PKG in out:
        print("  (already Device Owner — skipping)")
        return
    adb_shell(serial, f"dpm set-device-owner {ADMIN}")
    print("  ✓ Device Owner set")


def grant_permissions(serial):
    """Runs after install_apk: pm grant needs the package to exist."""
    print("[3/4] Granting required permissions...")
    # Usage stats (foreground monitoring), secure settings (auto-enable a11y),
    # and appear-over-other-apps (keep external app locked on top).
    adb_shell(serial, f"appops set {PKG} android:get_usage_stats allow", check=False)
    adb_shell(serial, f"pm grant {PKG} android.permission.WRITE_SECURE_SETTINGS", check=False)
    # SYSTEM_ALERT_WINDOW is granted automatically once Device Owner is set.
    print("  ✓ permissions granted")


def install_apk(serial, apk_path):
    if not apk_path:
        return
    print(f"[2/4] Installing Ali MDM APK ({os.path.basename(apk_path)})...")
    adb(serial, "install", "-r", "-t", apk_path)
    print("  ✓ installed")


def push_enrollment(serial, cloud_url, token, org_id):
    """
    Hands-on enrollment: push the enroll token + cloud URL into Ali MDM's
    enrollment prefs so it auto-enrolls on next launch (same prefs the
    setup-wizard path writes). Works when the device is already set up (not
    fresh), so you can enroll a tablet that's already past the wizard.
    """
    print("[4/4] Pushing cloud enrollment (auto-enroll on next launch)...")
    prefs = "AliMdmCloudEnrollment"
    xml = f"""<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <boolean name='has_pending' value='true' />
    <string name='enroll_token'>{token}</string>
    <string name='cloud_url'>{cloud_url.rstrip('/')}</string>
    <string name='org_id'>{org_id}</string>
</map>
"""
    with tempfile.NamedTemporaryFile("w", suffix=".xml", delete=False) as f:
        f.write(xml)
        tmp = f.name
    try:
        adb(serial, "push", tmp, f"/data/data/{PKG}/shared_prefs/{prefs}.xml")
        adb_shell(serial, f"chown {PKG}:{PKG} /data/data/{PKG}/shared_prefs/{prefs}.xml", check=False)
        adb_shell(serial, f"chmod 660 /data/data/{PKG}/shared_prefs/{prefs}.xml", check=False)
        # Restart the app so it picks up the pending enrollment and auto-enrolls.
        adb_shell(serial, f"am force-stop {PKG}", check=False)
        adb_shell(serial, f"am start -n {PKG}/.MainActivity", check=False)
        print("  ✓ enrollment pushed; app restarted to auto-enroll")
    finally:
        os.unlink(tmp)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--cloud", required=True, help="Cloud base URL, e.g. https://cloud.school.local")
    ap.add_argument("--token", required=True, help="Enroll token (must match ALIMDM_ENROLL_TOKEN on the server)")
    ap.add_argument("--org", default="mic-tech", help="Organization id (default: mic-tech)")
    ap.add_argument("--apps", default="", help="Comma-separated app package names (informational; the real list lives in the cloud group config)")
    ap.add_argument("--mode", choices=["qr", "push", "both"], default="both",
                    help="qr=print setup-wizard QR (zero-touch), push=ADB-push enrollment, both (default)")
    ap.add_argument("--serial", default=None, help="ADB device serial (omit for the only connected device)")
    ap.add_argument("--apk", default=None, help="Path to alimdm-release.apk to install first")
    ap.add_argument("--skip-owner", action="store_true", help="Skip dpm set-device-owner (device already owner)")
    ap.add_argument("--qr-out", default="enroll-qr.png", help="QR output path (qr mode)")
    ap.add_argument("--group", default="", help="Enrol into this policy group (default: the server's default group)")
    ap.add_argument("--apk-url", default=None,
                    help="URL the setup wizard downloads the APK from (default: <cloud>/api/v1/provision/apk)")
    ap.add_argument("--checksum", default=None,
                    help="Signing-cert checksum. Omit to derive it from --apk with apksigner.")
    ap.add_argument("--wifi-ssid", default="", help="Wi-Fi the tablet joins during provisioning (optional)")
    ap.add_argument("--wifi-password", default="", help="Wi-Fi password (optional)")
    args = ap.parse_args()

    print(f"=== Ali MDM enrollment: {args.mode} ===")
    print(f"    cloud={args.cloud}  org={args.org}")
    if args.apps:
        print(f"    apps={args.apps}")
    print()

    # QR mode works without a connected device (teacher scans it during setup).
    if args.mode in ("qr", "both"):
        print("[QR] Building setup-wizard provisioning QR...")
        apk_url = args.apk_url or (args.cloud.rstrip("/") + "/api/v1/provision/apk")
        checksum = args.checksum
        if not checksum:
            if not args.apk:
                raise SystemExit(
                    "QR mode needs the signing-cert checksum: pass --checksum, or --apk "
                    "<the APK the server serves> to derive it.")
            checksum = apk_signature_checksum(args.apk)
            print(f"  checksum (from {args.apk}): {checksum}")
        payload = build_provisioning_payload(
            args.cloud, args.token, args.org, apk_url, checksum,
            group_id=args.group, wifi_ssid=args.wifi_ssid, wifi_password=args.wifi_password)
        out = make_qr(payload, args.qr_out)
        if out:
            print(f"  ✓ QR written to {out} — scan it during the tablet's first setup")
            print(f"    APK download: {apk_url}")
            print("    The served APK must be signed with the key above, or the wizard")
            print("    rejects it after downloading.")
        print()

    # Push / owner modes need a connected device.
    if args.mode in ("push", "both"):
        r = adb(args.serial, "devices", quiet=True)
        lines = [l for l in (r.stdout or "").splitlines()[1:] if "device" in l]
        if not lines:
            print("  !! No ADB device connected. Plug in a tablet and enable USB debugging.")
            raise SystemExit("no device")
        if args.serial is None:
            args.serial = lines[0].split()[0]
            print(f"    using device {args.serial}")

        if not args.skip_owner:
            set_device_owner(args.serial)
        # Install first: "pm grant" fails on a package that is not there yet, and
        # it fails quietly (check=False). A fresh tablet therefore came up
        # without WRITE_SECURE_SETTINGS, which is what the app needs to turn its
        # own accessibility service on — and without that service it can only
        # ever screenshot its own window, never the app a pupil is using.
        install_apk(args.serial, args.apk)
        grant_permissions(args.serial)
        push_enrollment(args.serial, args.cloud, args.token, args.org)

    print()
    print("✅ Done. The tablet will pull the lockdown config from the cloud on next heartbeat.")
    print("   Verify: check the device appears online in the operator console.")


if __name__ == "__main__":
    main()
