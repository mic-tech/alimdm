#!/usr/bin/env bash
# enroll-all.sh — enroll every connected, authorized tablet at once.
#
# Usage:
#   ./enroll-all.sh <cloud-url> <enroll-token> <path-to-apk>
#
# Connect ALL your tablets via USB (factory-reset, USB debugging on, each
# authorized), then run this. It enrolls each one in turn.
#
# Tip: factory-reset each tablet first and leave them on the setup screen;
# you don't need to finish setup on all of them before running this.
set -euo pipefail

CLOUD="${1:?Usage: enroll-all.sh <cloud-url> <enroll-token> <path-to-apk>}"
TOKEN="${2:?Missing enroll token (arg 2)}"
APK="${3:?Missing APK path (arg 3)}"

DEVS=$(adb devices | awk 'NR>1 && $2=="device"{print $1}')
if [ -z "$DEVS" ]; then
  echo "ERROR: no authorized ADB devices. Connect + allow the USB prompt on each tablet."
  exit 1
fi

COUNT=$(echo "$DEVS" | wc -l)
echo "Found $COUNT tablet(s):"
adb devices
echo

i=0
for s in $DEVS; do
  i=$((i+1))
  echo "=============================================================="
  echo "  Tablet $i / $COUNT  (serial: $s)"
  echo "=============================================================="
  if ./enroll-one.sh "$CLOUD" "$TOKEN" "$APK" "$s"; then
    echo "  ✅ $s enrolled"
  else
    echo "  ⚠️  $s FAILED — see above. Continuing with the rest."
  fi
  echo
done

echo "All done. Check the console (Devices) — online tablets should be appearing."
