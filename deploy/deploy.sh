#!/usr/bin/env bash
# Deploy ali-mdm to a VPS. Run this ON the VPS after cloning the repo.
# Prereqs: docker + docker compose plugin, DNS A/AAAA record for CLOUD_DOMAIN
# pointing at this VPS's IP, ports 80/443 open.
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -f .env ]; then
  cp .env.example .env
  echo "Created .env from .env.example - EDIT IT (CLOUD_DOMAIN, ALIMDM_BASE_URL, ALIMDM_SECRET, ALIMDM_ENROLL_TOKEN)."
  echo "Generate secrets:"
  echo "  ALIMDM_SECRET=*** rand -hex 32)"
  echo "  ALIMDM_ENROLL_TOKEN=*** rand -hex 24)"
  exit 1
fi

for v in CLOUD_DOMAIN ALIMDM_BASE_URL ALIMDM_SECRET ALIMDM_ENROLL_TOKEN; do
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
  # -rsp with no variable name puts the answer in $REPLY and throws it away. This
  # used to read the password, discard it, and then pass "$OEMAIL" as -password,
  # so every install created an admin account whose password was its own email
  # address — on the public internet, from the very first boot.
  read -rsp "Operator password: " OPASS; echo
  if [ -z "$OPASS" ]; then
    echo "ERROR: the operator password cannot be empty."
    exit 1
  fi
  read -rp "Comma-separated app package names [default: the 3 school apps]: " APPS
  APPS="${APPS:-com.gplanet_tech.noraneya,com.tagmedia.adnan,com.pakdata.QuranMajeed}"
  docker compose run --rm api /app/bootstrap \
    -db /data/alimdm.db -email "$OEMAIL" -password "$OPASS" -apps "$APPS"
  unset OPASS
  echo "Operator created."
fi

echo
echo "Done. Console: https://${CLOUD_DOMAIN}"
echo "Verify health:  curl -s https://${CLOUD_DOMAIN}/healthz"
echo "Enroll a tablet: python3 enroll/enroll.py --cloud https://${CLOUD_DOMAIN} --token \"\$ALIMDM_ENROLL_TOKEN\" --mode qr"
