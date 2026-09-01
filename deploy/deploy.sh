#!/usr/bin/env bash
# Deploy ali-mdm to a VPS. Run this ON the VPS after cloning the repo.
# Prereqs: docker + docker compose plugin, DNS A/AAAA record for CLOUD_DOMAIN
# pointing at this VPS's IP, ports 80/443 open.
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -f .env ]; then
  cp .env.example .env
  echo "Created .env from .env.example - EDIT IT (CLOUD_DOMAIN, FK_BASE_URL, FK_SECRET, FK_ENROLL_TOKEN)."
  echo "Generate secrets:"
  echo "  FK_SECRET=*** rand -hex 32)"
  echo "  FK_ENROLL_TOKEN=*** rand -hex 24)"
  exit 1
fi

for v in CLOUD_DOMAIN FK_BASE_URL FK_SECRET FK_ENROLL_TOKEN; do
  val=$(grep -E "^${v}=" .env | cut -d= -f2-)
  if [ -z "$val" ] || echo "$val" | grep -qE "CHANGE_ME|yourdomain"; then
    echo "ERROR: .env variable $v is empty or a placeholder. Fix it and re-run."
    exit 1
  fi
done

echo "Building images..."
docker compose build
echo "Starting services..."
docker compose up -d

echo
read -rp "Create the first operator? [Y/n] " ans
if [ "${ans:-Y}" != "n" ]; then
  read -rp "Operator email: " OEMAIL
  read -rsp "Operator password: "; echo
  read -rp "Comma-separated app package names [default: the 3 school apps]: " APPS
  APPS="${APPS:-com.gplanet_tech.noraneya,com.tagmedia.adnan,com.pakdata.QuranMajeed}"
  docker compose run --rm api /app/bootstrap \
    -db /data/alimdm.db -email "$OEMAIL" -password "$OEMAIL" -apps "$APPS"
  echo "Operator created."
fi

echo
echo "Done. Console: https://${CLOUD_DOMAIN}"
echo "Verify health:  curl -s https://${CLOUD_DOMAIN}/healthz"
echo "Enroll a tablet: python3 enroll/enroll.py --cloud https://${CLOUD_DOMAIN} --token \"\$FK_ENROLL_TOKEN\" --mode qr"
