#!/usr/bin/env bash
# enroll-one.sh — enroll a single Ali MDM tablet over ADB.
#
# Usage:
#   ./enroll-one.sh <cloud-url> <enroll-token> <path-to-apk> [adb-serial]
#
# Example:
#   ./enroll-one.sh https://cloud.yourdomain.com "abc123..." ./alimdm-release.apk
#
# What it does:
#   1. Installs the Ali MDM APK
#   2. Sets Ali MDM as Device Owner
#   3. Grants required permissions
#   4. Pushes the cloud enrollment (token + cloud URL) so the app auto-enrolls
#   5. Restarts the app
#
# Prereqs: tablet factory-reset (no accounts/SIM), USB debugging on, connected
# and authorized (adb devices shows it as "device").
set -euo pipefail

CLOUD="${1:?Usage: enroll-one.sh <cloud-url> <enroll-token> [path-to-apk] [adb-serial]}"
TOKEN="${2:?Missing enroll token (arg 2)}"
APK="${3:-}"                       # optional — auto-detected below
SERIAL="${4:-}"
ORG="${FK_ORG_ID:-mic-tech}"       # org id baked into the enrollment (override via FK_ORG_ID)

PKG="com.alimdm"
ADMIN="$PKG/.DeviceAdminReceiver"
PREFS="AliMdmCloudEnrollment"

# --- pre-flight: adb present? ------------------------------------------
if ! command -v adb >/dev/null 2>&1; then
  echo "ERROR: 'adb' is not installed."
  echo "  Ubuntu/Debian:  sudo apt-get install adb"
  echo "  macOS:          brew install android-platform-tools"
  echo "  Windows:        scoop install adb   (or download platform-tools)"
  exit 1
fi

# --- pre-flight: locate the APK ----------------------------------------
if [ -z "$APK" ]; then
  # search common spots relative to this script
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  for cand in \
    "$SCRIPT_DIR/../../apk/alimdm-adb-debug.apk" \
    "$SCRIPT_DIR/../apk/"*.apk \
    "$HOME/ali-mdm/apk/"*.apk \
    "$PWD/"*.apk ; do
    if [ -f "$cand" ]; then APK="$cand"; break; fi
  done
fi
if [ -z "$APK" ] || [ ! -f "$APK" ]; then
  echo "ERROR: could not find the Ali MDM APK."
  echo "  Pass it as arg 3, e.g.:  $0 $CLOUD \"$TOKEN\" /path/to/alimdm.apk"
  echo "  Or drop it in:  $(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../apk/"
  exit 1
fi

# adb helper (optional serial)
adbx() {
  if [ -n "$SERIAL" ]; then adb -s "$SERIAL" "$@"; else adb "$@"; fi
}
shx() { adbx shell "$@"; }

echo "==> Target: ${SERIAL:-<only connected device>}  cloud=$CLOUD"

# --- sanity: a device must be connected -------------------------------
if [ -z "$SERIAL" ]; then
  DEVS=$(adb devices | awk 'NR>1 && $2=="device"{print $1}')
  if [ -z "$DEVS" ]; then
    echo "ERROR: no authorized ADB device. Connect + allow the USB prompt, then retry."
    exit 1
  fi
  if [ "$(echo "$DEVS" | wc -l)" -gt 1 ]; then
    echo "ERROR: multiple devices connected. Pass the serial as arg 4:"
    adb devices
    exit 1
  fi
  SERIAL="$DEVS"
  echo "==> Using device $SERIAL"
fi

# --- 1. install the APK ------------------------------------------------
echo "==> [1/5] Installing Ali MDM APK: $APK"
adbx install -r -t "$APK"

# --- 2. set Device Owner ----------------------------------------------
echo "==> [2/5] Setting Device Owner"
# If it's already our owner, skip; if it's someone else's, fail clearly.
OWNERS=$(shx dpm list-owners 2>/dev/null || true)
if echo "$OWNERS" | grep -q "DeviceOwner"; then
  if echo "$OWNERS" | grep -q "$PKG"; then
    echo "    (already Device Owner — skipping)"
  else
    echo "ERROR: a different app is Device Owner. Factory-reset the tablet and retry."
    exit 1
  fi
else
  shx dpm set-device-owner "$ADMIN"
fi

# --- 3. grant permissions ---------------------------------------------
echo "==> [3/5] Granting permissions"
shx appops set "$PKG" android:get_usage_stats allow 2>/dev/null || true
shx pm grant "$PKG" android.permission.WRITE_SECURE_SETTINGS 2>/dev/null || true

# --- 4. push the cloud enrollment -------------------------------------
echo "==> [4/5] Pushing cloud enrollment (auto-enroll on next launch)"
# Build the prefs XML on the host, push it into the app's shared_prefs, fix perms.
TMPXML="$(mktemp)"
cat > "$TMPXML" <<XML
<?xml version='1.0' encoding='utf-8' standalone='yes' ?>
<map>
    <boolean name='has_pending' value='true' />
    <string name='enroll_token'>$TOKEN</string>
    <string name='cloud_url'>${CLOUD%/}</string>
    <string name='org_id'>$ORG</string>
</map>
XML
adbx push "$TMPXML" "/data/data/$PKG/shared_prefs/$PREFS.xml"
rm -f "$TMPXML"
# The pushed file is owned by root; hand it to the app so it can read it.
shx chown "$PKG:$PKG" "/data/data/$PKG/shared_prefs/$PREFS.xml" 2>/dev/null || true
shx chmod 660 "/data/data/$PKG/shared_prefs/$PREFS.xml" 2>/dev/null || true

# --- 5. restart the app so it picks up the enrollment -----------------
echo "==> [5/5] Restarting Ali MDM to auto-enroll"
shx am force-stop "$PKG"
shx am start -n "$PKG/.MainActivity"

echo
echo "✅ Enrolled. Check the console (Devices) within ~30s — it should appear online."
echo "   The tablet is now locked to your app whitelist."
