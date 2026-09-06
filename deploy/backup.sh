#!/usr/bin/env bash
#
# Back up the Ali MDM database, safely, while the server is running.
#
# `cp alimdm.db` is the obvious thing and it is wrong. SQLite in WAL mode keeps
# recent transactions in a separate -wal file until a checkpoint folds them in,
# so a plain copy takes the main file without them: on this server the WAL has
# been larger than the database itself. Worse, copying a file that is being
# written to can tear a page and produce a database that opens and then fails
# later, on a page nobody read during the restore test.
#
# `VACUUM INTO` takes a consistent snapshot of the whole database — WAL included
# — while writers carry on, and the result is a compacted single file with no
# sidecars to remember. It is the one-line answer, and the only one worth using.
#
# Usage:  ./backup.sh [destination-directory]
set -euo pipefail

DB="${ALIMDM_DB:-$HOME/ali-mdm/data/alimdm.db}"
DEST="${1:-$HOME/backups}"
KEEP="${ALIMDM_BACKUP_KEEP:-14}"

if [ ! -f "$DB" ]; then
  echo "No database at $DB. Set ALIMDM_DB if it lives somewhere else." >&2
  exit 1
fi

mkdir -p "$DEST"
OUT="$DEST/alimdm-$(date +%Y%m%d-%H%M%S).db"

# Prefer the sqlite3 CLI; fall back to the one inside the running container,
# which is the case on a host that only ever installed Docker.
if command -v sqlite3 >/dev/null 2>&1; then
  sqlite3 "$DB" "VACUUM INTO '$OUT'"
elif docker ps --format '{{.Names}}' | grep -qx alimdm-cloud; then
  echo "sqlite3 not installed on the host; using the container's copy." >&2
  docker exec alimdm-cloud sh -c "command -v sqlite3 >/dev/null 2>&1" ||
    { echo "The container has no sqlite3 either. Install sqlite3 on the host." >&2; exit 1; }
  docker exec alimdm-cloud sqlite3 /data/alimdm.db "VACUUM INTO '/data/backup-tmp.db'"
  docker cp alimdm-cloud:/data/backup-tmp.db "$OUT"
  docker exec alimdm-cloud rm -f /data/backup-tmp.db
else
  echo "Install sqlite3, or start the alimdm-cloud container, and try again." >&2
  exit 1
fi

# A backup that cannot be opened is not a backup. Check before reporting success
# and before the rotation below deletes an older one that might have been fine.
if command -v sqlite3 >/dev/null 2>&1; then
  if ! sqlite3 "$OUT" "PRAGMA integrity_check" | grep -qx ok; then
    echo "The backup at $OUT did not pass integrity_check — keeping it, not rotating." >&2
    exit 1
  fi
fi

echo "Backed up to $OUT ($(du -h "$OUT" | cut -f1))"

# Keep the most recent KEEP, oldest first out of the door.
ls -1t "$DEST"/alimdm-*.db 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do
  echo "Removing old backup $(basename "$old")"
  rm -f "$old"
done
