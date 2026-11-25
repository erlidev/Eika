#!/bin/sh
# Take one consistent backup set of a running Eika stack (`make backup`).
#
# The set is a directory holding
#   postgres.dump   pg_dump custom format of the eika database
#   memory.tar      the whole memory repository (git history included), from
#                   the memory computer's volume
#   secret.key      the key that seals stored credentials
#   manifest.json   version, migration, time and the checksum of each file
#
# Core is the only writer of the database, the memory repository and the key,
# so it is stopped while the files are taken and started again afterwards,
# also when the backup fails. The stack is unavailable for that time. The
# set is built in a hidden directory and renamed when complete: a directory
# without a leading dot is a finished set. Nothing secret is ever printed.
#
# Environment:
#   COMPOSE            compose command (default "docker compose")
#   EIKA_BACKUP_DIR    where sets are written (default ./backups)
#   EIKA_MEMORY_VOLUME the memory computer's volume (default: eika-cx-<id>, the
#                      id this install recorded in its database)
#   EIKA_BACKUP_IMAGE  image whose tar and sh read the volumes (default
#                      postgres:16-alpine, which the stack already uses)
set -eu
umask 077

compose="${COMPOSE:-docker compose}"
root="${EIKA_BACKUP_DIR:-backups}"
memvol="${EIKA_MEMORY_VOLUME:-}"
image="${EIKA_BACKUP_IMAGE:-postgres:16-alpine}"
here="$(cd "$(dirname "$0")/.." && pwd)"

fail() { echo "backup: $*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || fail "docker is not installed"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is not installed"

# Everything is checked before anything is stopped.
pg="$($compose ps -q postgres 2>/dev/null || true)"
[ -n "$pg" ] || fail "the stack has no running postgres service; start it first"
[ "$(docker inspect -f '{{.State.Running}}' "$pg")" = true ] || fail "postgres is not running"
core="$($compose ps -aq eika 2>/dev/null || true)"
[ -n "$core" ] || fail "the stack has no eika service"
keyvol="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/eika"}}{{.Name}}{{end}}{{end}}' "$core")"
[ -n "$keyvol" ] || fail "the eika service has no data volume"
# The memory computer's id is per install and recorded in the database, so the
# volume is this stack's own, never another stack's on the same Docker host.
if [ -z "$memvol" ]; then
	memid="$($compose exec -T postgres sh -c 'psql -At -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -c "SELECT id FROM core.memory_computer"' 2>/dev/null | tr -d '\r' || true)"
	[ -n "$memid" ] || fail "no memory computer is recorded in the database yet; start eika once, or set EIKA_MEMORY_VOLUME"
	memvol="eika-cx-$memid"
fi
docker volume inspect "$memvol" >/dev/null 2>&1 || fail "memory volume $memvol does not exist (set EIKA_MEMORY_VOLUME if it is named otherwise)"

# have VOLUME PATH: the path exists inside the volume.
have() { docker run --rm --network none -v "$1:/v:ro" "$image" test -e "/v/$2"; }
have "$keyvol" secret.key || fail "secret.key is missing from the data volume; a backup without the key cannot unseal credentials"
have "$memvol" .git/HEAD || fail "the memory volume holds no git repository (.git/HEAD missing)"

ts="$(date -u +%Y%m%dT%H%M%SZ)"
name="eika-backup-$ts"
mkdir -p "$root"
chmod 700 "$root"
[ ! -e "$root/$name" ] || fail "$root/$name already exists"
tmp="$root/.$name.partial"
mkdir "$tmp"
chmod 700 "$tmp"

was_running=false
[ "$(docker inspect -f '{{.State.Running}}' "$core")" = true ] && was_running=true
done_ok=false
cleanup() {
	status=$?
	if [ "$done_ok" != true ]; then rm -rf "$tmp"; fi
	if [ "$was_running" = true ]; then
		echo "backup: starting eika again"
		$compose start eika >/dev/null || echo "backup: could not start eika; run: $compose start eika" >&2
	fi
	exit $status
}
trap cleanup EXIT INT TERM

if [ "$was_running" = true ]; then
	echo "backup: stopping eika (it is down until the set is written)"
	$compose stop eika >/dev/null
fi

echo "backup: dumping the database"
$compose exec -T postgres sh -c 'pg_dump -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -Fc' >"$tmp/postgres.dump"
[ -s "$tmp/postgres.dump" ] || fail "the database dump is empty"
migration="$($compose exec -T postgres sh -c 'psql -At -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -c "SELECT max(version) FROM core.schema_migrations"' | tr -d '\r')"
[ -n "$migration" ] || fail "could not read the migration number"

echo "backup: copying the memory repository"
docker run --rm --network none -v "$memvol:/v:ro" "$image" tar --numeric-owner -C /v -cf - . >"$tmp/memory.tar"
[ -s "$tmp/memory.tar" ] || fail "the memory archive is empty"

echo "backup: copying the seal key"
docker run --rm --network none -v "$keyvol:/v:ro" "$image" cat /v/secret.key >"$tmp/secret.key"
[ -s "$tmp/secret.key" ] || fail "the seal key is empty"

commit="$(git -C "$here" rev-parse HEAD 2>/dev/null || echo unknown)"
dirty=false
[ -z "$(git -C "$here" status --porcelain 2>/dev/null)" ] || dirty=true
image_id="$(docker inspect -f '{{.Image}}' "$core" 2>/dev/null || echo unknown)"

sum() { sha256sum "$tmp/$1" | cut -d' ' -f1; }
size() { wc -c <"$tmp/$1" | tr -d ' '; }
cat >"$tmp/manifest.json" <<JSON
{
  "format": 1,
  "created": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "eika": {
    "commit": "$commit",
    "dirty": $dirty,
    "image": "$image_id"
  },
  "migration": "$migration",
  "files": {
    "postgres.dump": {"sha256": "$(sum postgres.dump)", "bytes": $(size postgres.dump)},
    "memory.tar": {"sha256": "$(sum memory.tar)", "bytes": $(size memory.tar)},
    "secret.key": {"sha256": "$(sum secret.key)", "bytes": $(size secret.key)}
  }
}
JSON

chmod 600 "$tmp"/*
chmod 700 "$tmp"
mv "$tmp" "$root/$name"
done_ok=true
echo "backup: wrote $root/$name (migration $migration)"
