#!/bin/bash
# ============================================================
# Ali MDM ADB enrollment - one tablet at a time
# Usage: ./enroll_tablet.sh <adb-serial> [--group <group-id>]
#   e.g. ./enroll_tablet.sh HA273Z07
#   e.g. ./enroll_tablet.sh HA273Z07 --group classroom-a
#
#   --group <id>  Enroll the tablet into a specific policy group instead of
#                 "default". The group must already exist (create it in the
#                 console Groups tab). Omit to use "default".
#
# Prereqs (done on the tablet BEFORE running this):
#   1. Factory reset + complete basic setup (language, Wi-Fi) to home screen
#   2. Enable USB debugging: Settings > About tablet > tap "Build number" 7x,
#      then Settings > System > Developer options > USB debugging ON
#   3. Connect tablet to this machine via USB (or: adb connect <tablet-ip>:5555)
#   4. Accept the "Allow USB debugging?" prompt on the tablet
#
# This script:
#   1. Installs the Ali MDM APK (release builds work; no debug build needed)
#   2. Hands the cloud enrollment (token + cloud URL) over as intent extras
#   3. Sets Ali MDM as Device Owner
#   4. Launches it -> it auto-enrolls with the cloud + auto-installs the 3 apps
# ============================================================
set -e

SERIAL="${1:?Usage: $0 <adb-serial> [--group <id>] [--label \"Library tablet\"]   (run 'adb devices' to see serials)}"
GROUP_ID=""
DEVICE_LABEL=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --group) GROUP_ID="${2:?--group needs a value}"; shift 2 ;;
    # Names the tablet in the console from the moment it enrols. Without it the
    # device shows as its id, which is hard to match to a tablet in your hand
    # when a trolley of them all look identical.
    --label) DEVICE_LABEL="${2:?--label needs a value}"; shift 2 ;;
    *) shift ;;
  esac
done
# Deployment settings live outside the repo so the enrolment token is not
# committed. Copy enroll.env.example to enroll.env and fill it in, or export
# these in your shell.
ENV_FILE="$(dirname "$0")/enroll.env"
[ -f "$ENV_FILE" ] && . "$ENV_FILE"

APK="${APK:?set APK in apps/enroll/enroll.env (path to the Ali MDM APK)}"
CLOUD_URL="${CLOUD_URL:?set CLOUD_URL in apps/enroll/enroll.env}"
ENROLL_TOKEN="${ENROLL_TOKEN:?set ENROLL_TOKEN in apps/enroll/enroll.env}"
ORG_ID="${ORG_ID:-your-org}"
# The app requires a settings PIN on first ADB configuration, and re-uses it to
# authorise any later ADB config on an already-configured device.
KIOSK_PIN="${KIOSK_PIN:?set KIOSK_PIN in apps/enroll/enroll.env (kiosk settings PIN)}"
# Optional: ssh target for the cloud host, used only in the closing hint below.
SERVER_SSH="${SERVER_SSH:-<user>@<your-cloud-host>}"
PKG="com.alimdm"
ADMIN="com.alimdm/.DeviceAdminReceiver"

ADB="adb -s $SERIAL"

echo "==> Target tablet: $SERIAL${DEVICE_LABEL:+  (label: $DEVICE_LABEL)}"
$ADB wait-for-device
echo "    device online"

echo "==> 1. Install the Ali MDM APK"
$ADB install -r -d "$APK"

echo "==> 2. Launch once so the app data dir exists"
$ADB shell am start -n "$PKG/.MainActivity" >/dev/null 2>&1 || true
sleep 3

echo "==> 3. Hand over the cloud enrolment as intent extras"
# Passed as extras rather than written into the app's shared_prefs with `run-as`:
# run-as only works on a debuggable build, so the old approach could not enrol a
# signed release APK. MainActivity.applyAdbCloudEnrollment() stages these.
$ADB shell am start -n "$PKG/.MainActivity" \
  --es pin "$KIOSK_PIN" \
  --es cloud_url "$CLOUD_URL" \
  --es enroll_token "$ENROLL_TOKEN" \
  --es org_id "$ORG_ID" \
  ${GROUP_ID:+--es group_id "$GROUP_ID"} \
  ${DEVICE_LABEL:+--es device_label "$DEVICE_LABEL"} >/dev/null
sleep 2
echo "    enrolment staged"

echo "==> 4. Set Ali MDM as Device Owner"
$ADB shell dpm set-device-owner "$ADMIN"

echo "==> 5. Restart Ali MDM (auto-enroll + auto-install kicks in)"
$ADB shell am force-stop "$PKG"
sleep 1
$ADB shell am start -n "$PKG/.MainActivity"

echo
echo "==> DONE. The tablet should now:"
echo "    - Enroll with $CLOUD_URL${GROUP_ID:+ into group '$GROUP_ID'}"
echo "    - Auto-install Noraneya, Adnan, Quran Majeed (watch the grid show 'Downloading...')"
echo "    - Settle into the multi-app kiosk"
echo
echo "==> Verify device owner:"
$ADB shell dumpsys device_policy 2>/dev/null | grep -iE "owner|Ali MDM" | head -5 || echo "    (check manually)"
echo
echo "==> Watch enrollment on the server:"
echo "    ssh ${SERVER_SSH} 'docker logs --since 2m fk-cloud'"
