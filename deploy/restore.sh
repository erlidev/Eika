#!/bin/sh
# Restore a backup set (`make backup`) into a stopped, empty Eika stack
# (`make restore BACKUP=<set directory>`).
#
# The set is checked completely before anything is written: every file is
# there, every checksum in manifest.json matches, the migration is one this
# build knows, the dump and the archive can be read, and the memory computer's
# id is found in the dump. Then the target is checked: Eika must be stopped,
# and the database, the key file and the memory volume must all be empty. With
# any data in the target the restore refuses and writes nothing, unless FORCE=1
# (see below). Then it restores the database, the key (kept as it was, mode
# 0600, owned by the harness user) and the memory repository into the volume
# eika-cx-<id>, the id the restored database records, and starts the stack.
# Core's startup reconcile verifies and rebuilds the memory index from the
# restored files. If the set's volume exists and is not empty (the source stack
# still runs on this host) it is left untouched and a fresh id and volume are
# used and recorded. Nothing secret is ever printed.
#
# FORCE=1 is DESTRUCTIVE. It drops the target database and replaces it, and
# overwrites the key file and empties the memory volume before restoring.
# Whatever the target held is lost. Eika must still be stopped.
#
# Environment:
#   COMPOSE            compose command (default "docker compose")
#   BACKUP             the set directory (or the first argument)
#   FORCE              1 to overwrite a target that holds data
#   EIKA_MEMORY_VOLUME the memory volume (default eika-cx-<id>, from the dump)
#   EIKA_BACKUP_IMAGE  image whose tar, sh and pg_restore are used (default
#                      postgres:16-alpine, which the stack already uses)
set -eu
umask 077

compose="${COMPOSE:-docker compose}"
set_dir="${1:-${BACKUP:-}}"
force="${FORCE:-}"
image="${EIKA_BACKUP_IMAGE:-postgres:16-alpine}"
here="$(cd "$(dirname "$0")/.." && pwd)"

fail() { echo "restore: $*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || fail "docker is not installed"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is not installed"
[ -n "$set_dir" ] || fail "name the backup set: make restore BACKUP=backups/eika-backup-<time>"
[ -d "$set_dir" ] || fail "$set_dir is not a directory"
set_dir="$(cd "$set_dir" && pwd)"
case "$force" in "" | 0 | 1) ;; *) fail "FORCE must be 1 or unset" ;; esac

# ---- 1. The set, checked completely, before anything is touched. ----------
for f in manifest.json postgres.dump memory.tar secret.key; do
	[ -f "$set_dir/$f" ] && [ ! -L "$set_dir/$f" ] || fail "incomplete backup: $f is missing from $set_dir"
done
manifest="$set_dir/manifest.json"
format="$(sed -n 's/.*"format": \([12]\)[,[:space:]]*$/\1/p' "$manifest")"
[ -n "$format" ] || fail "manifest.json has an unsupported format"
files="postgres.dump memory.tar secret.key"
if [ "$format" = 2 ]; then files="$files skills.tar"; fi
for f in $files; do
 [ -f "$set_dir/$f" ] && [ ! -L "$set_dir/$f" ] || fail "incomplete backup: $f is missing from $set_dir"
	line="$(sed -n "s/.*\"$f\": {\"sha256\": \"\\([0-9a-f]\\{64\\}\\)\", \"bytes\": \\([0-9][0-9]*\\)}.*/\\1 \\2/p" "$manifest")"
	[ -n "$line" ] || fail "manifest.json has no checksum for $f"
	want_sum="${line% *}"
	want_size="${line#* }"
	[ "$(wc -c <"$set_dir/$f" | tr -d ' ')" = "$want_size" ] || fail "$f does not match its size in the manifest; the set is damaged"
	[ "$(sha256sum "$set_dir/$f" | cut -d' ' -f1)" = "$want_sum" ] || fail "$f does not match its checksum in the manifest; the set is damaged"
done

migration="$(sed -n 's/.*"migration": "\([^"]*\)".*/\1/p' "$manifest")"
case "$migration" in [0-9][0-9][0-9][0-9]_*) ;; *) fail "manifest.json records no valid migration" ;; esac
supported="$(ls "$here/internal/store/migrations" | sed -n 's/\.sql$//p' | LC_ALL=C sort | tail -1)"
[ -n "$supported" ] || fail "cannot find this build's migrations in $here/internal/store/migrations"
newest="$(printf '%s\n%s\n' "$migration" "$supported" | LC_ALL=C sort | tail -1)"
[ "$newest" = "$supported" ] || fail "the backup is at migration $migration, newer than this build supports ($supported); upgrade Eika first"

# The files must also be readable, not only intact.
docker run --rm -i --network none "$image" pg_restore -l <"$set_dir/postgres.dump" >/dev/null 2>&1 || fail "postgres.dump is not a readable pg_dump archive"
docker run --rm -i --network none "$image" tar -tf - <"$set_dir/memory.tar" 2>/dev/null | grep -qx '\./\.git/HEAD' || fail "memory.tar holds no git repository (.git/HEAD missing)"
if [ "$format" = 2 ]; then
 docker run --rm -i --network none "$image" tar -tf - <"$set_dir/skills.tar" >/dev/null 2>&1 || fail "skills.tar is not a readable archive"
fi
tr -d '[:space:]' <"$set_dir/secret.key" | grep -Eq '^[0-9a-fA-F]+$' || fail "secret.key is not a key file"

memvol="${EIKA_MEMORY_VOLUME:-}"
memid=""
if [ -z "$memvol" ]; then
	memid="$(docker run --rm -i --network none "$image" pg_restore -a -n core -t memory_computer -f - <"$set_dir/postgres.dump" 2>/dev/null |
		awk '/^COPY .*memory_computer/ {f=1; next} f && /^\\\.$/ {exit} f {print $2}')"
	printf '%s' "$memid" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$' || fail "the dump records no memory computer id; it cannot name the memory volume (set EIKA_MEMORY_VOLUME)"
	memvol="eika-cx-$memid"
fi
skillvol=""
if [ "$format" = 2 ]; then
 skillid="$(docker run --rm -i --network none "$image" pg_restore -a -n core -t skills_computer -f - <"$set_dir/postgres.dump" 2>/dev/null |
  awk '/^COPY .*skills_computer/ {f=1; next} f && /^\\\.$/ {exit} f {print $2}')"
 printf '%s' "$skillid" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$' || fail "the dump records no skills computer id"
 skillvol="eika-cx-$skillid"
fi
echo "restore: the set is complete and intact (migration $migration)"

# ---- 2. The target, checked before anything is written. --------------------
core="$($compose ps -aq eika 2>/dev/null || true)"
if [ -n "$core" ] && [ "$(docker inspect -f '{{.State.Running}}' "$core")" = true ]; then
	fail "eika is running; stop it first: $compose stop eika"
fi
proj="$($compose config 2>/dev/null | sed -n 's/^name: //p' | head -1)"
[ -n "$proj" ] || fail "cannot read the compose project name"
keyvol="${proj}_eika-data"

# nonempty VOLUME: the volume exists and holds anything. have VOLUME PATH: the
# path exists in it.
nonempty() { docker volume inspect "$1" >/dev/null 2>&1 && [ -n "$(docker run --rm --network none -v "$1:/v:ro" "$image" ls -A /v)" ]; }
have() { docker volume inspect "$1" >/dev/null 2>&1 && docker run --rm --network none -v "$1:/v:ro" "$image" test -e "/v/$2"; }

# Postgres is the only service started before the checks: the database is
# read through it. It writes nothing but its own empty cluster.
$compose up -d --wait postgres >/dev/null || fail "cannot start the postgres service"
tables="$($compose exec -T postgres sh -c 'psql -At -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -c "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('"'"'pg_catalog'"'"','"'"'information_schema'"'"')"' | tr -d '\r')"
case "$tables" in '' | *[!0-9]*) fail "cannot read the target database" ;; esac

# The volume eika-cx-<id> is named per Docker host, not per stack. If the set's
# volume is there and not empty it belongs to another stack on this host (the
# source of the backup, still running), and it is never written. The repository
# then goes to a volume of its own, named from a fresh id (or, when the target
# already records an id, from that one), and the restored database is changed
# to record it, before Core starts.
newid=""
rec=""
if [ "$tables" != 0 ]; then
	rec="$($compose exec -T postgres sh -c 'psql -At -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -c "SELECT id FROM core.memory_computer"' 2>/dev/null | tr -d '\r' || true)"
fi
if [ -z "${EIKA_MEMORY_VOLUME:-}" ] && [ "$memvol" != "eika-cx-$rec" ] && nonempty "$memvol"; then
	if [ -n "$rec" ]; then newid="$rec"; else newid="$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 20)"; fi
	[ -n "$newid" ] || fail "cannot generate a memory computer id"
	echo "restore: volume $memvol belongs to another stack on this host and is left alone; the memory repository goes to eika-cx-$newid"
	memvol="eika-cx-$newid"
fi

skillnewid=""
if [ "$format" = 2 ]; then
 skillrec=""
 if [ "$tables" != 0 ]; then
  skillrec="$($compose exec -T postgres sh -c 'psql -At -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}" -c "SELECT id FROM core.skills_computer"' 2>/dev/null | tr -d '\r' || true)"
 fi
 if [ "$skillvol" != "eika-cx-$skillrec" ] && nonempty "$skillvol"; then
  if [ -n "$skillrec" ]; then skillnewid="$skillrec"; else skillnewid="$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 20)"; fi
  printf '%s' "$skillnewid" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$' || fail "cannot generate a skills computer id"
  skillvol="eika-cx-$skillnewid"
 fi
fi
problems=""
[ "$tables" = 0 ] || problems="$problems
  - the database already holds data ($tables tables)"
if have "$keyvol" secret.key; then problems="$problems
  - the key file already exists in volume $keyvol"; fi
if nonempty "$memvol"; then problems="$problems
  - the memory volume $memvol is not empty"; fi
if [ -n "$skillvol" ] && nonempty "$skillvol"; then problems="$problems
  - the skills volume $skillvol is not empty"; fi
if [ -n "$problems" ]; then
	if [ "$force" != 1 ]; then
		echo "restore: refusing, the target is not empty:$problems" >&2
		echo "restore: nothing was written. Restore into a new stack (docker compose -p <name>), remove this one's data with '$compose down -v' (destroys it), or set FORCE=1, which overwrites all of it." >&2
		exit 1
	fi
	echo "restore: FORCE=1, overwriting the target:$problems"
fi

# ---- 3. Write. --------------------------------------------------------------
# The containers (and so the compose volumes) are created without starting Core.
$compose up --no-start eika >/dev/null || fail "cannot create the eika container"
core="$($compose ps -aq eika)"
[ -n "$core" ] || fail "the stack has no eika service"
keyvol="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/eika"}}{{.Name}}{{end}}{{end}}' "$core")"
[ -n "$keyvol" ] || fail "the eika service has no data volume"
uidgid="$(docker run --rm --network none --entrypoint sh "$(docker inspect -f '{{.Image}}' "$core")" -c 'echo "$(id -u eika):$(id -g eika)"')"
case "$uidgid" in [0-9]*:[0-9]*) ;; *) fail "cannot read the harness user's id from the eika image" ;; esac

echo "restore: restoring the database"
if [ "$force" = 1 ]; then
	$compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -q -U "${POSTGRES_USER:-eika}" -d postgres -c "DROP DATABASE IF EXISTS \"${POSTGRES_DB:-eika}\" WITH (FORCE)" -c "CREATE DATABASE \"${POSTGRES_DB:-eika}\""' >/dev/null ||
		fail "cannot recreate the database; the target is partly changed"
fi
$compose exec -T postgres sh -c 'pg_restore --no-owner --exit-on-error -U "${POSTGRES_USER:-eika}" -d "${POSTGRES_DB:-eika}"' <"$set_dir/postgres.dump" ||
	fail "the database restore failed; the target is partly restored, rerun with FORCE=1 after fixing the cause"

if [ -n "$newid" ]; then
	printf '%s' "$newid" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$' || fail "invalid memory computer id"
	$compose exec -T postgres sh -c "psql -v ON_ERROR_STOP=1 -q -U \"\${POSTGRES_USER:-eika}\" -d \"\${POSTGRES_DB:-eika}\" -c \"UPDATE core.memory_computer SET id = '$newid'\"" >/dev/null ||
		fail "cannot record the new memory computer id; the target is partly restored"
fi

if [ -n "$skillnewid" ]; then
 $compose exec -T postgres sh -c "psql -v ON_ERROR_STOP=1 -q -U \"\${POSTGRES_USER:-eika}\" -d \"\${POSTGRES_DB:-eika}\" -c \"UPDATE core.skills_computer SET id = '$skillnewid'\"" >/dev/null || fail "cannot record the skills computer id; the target is partly restored"
fi

echo "restore: restoring the key"
docker run --rm -i --network none -u 0 -v "$keyvol:/v" "$image" sh -c 'umask 077; cat >/v/secret.key; chown "$1" /v/secret.key; chmod 600 /v/secret.key' sh "$uidgid" <"$set_dir/secret.key" ||
	fail "cannot write the key; the target is partly restored"

echo "restore: restoring the memory repository into $memvol"
if ! docker volume inspect "$memvol" >/dev/null 2>&1; then
	# Labelled as the harness labels the volume it creates.
	docker volume create --label "eika.computer=${memvol#eika-cx-}" "$memvol" >/dev/null || fail "cannot create the volume $memvol"
fi
if [ "$force" = 1 ]; then
	docker run --rm --network none -v "$memvol:/v" "$image" find /v -mindepth 1 -delete || fail "cannot empty $memvol; the target is partly restored"
fi
docker run --rm -i --network none -u 0 -v "$memvol:/v" "$image" tar --numeric-owner -C /v -xpf - <"$set_dir/memory.tar" ||
	fail "the memory restore failed; the target is partly restored"

if [ "$format" = 2 ]; then
 echo "restore: restoring the skills volume"
 if ! docker volume inspect "$skillvol" >/dev/null 2>&1; then
  docker volume create --label "eika.computer=${skillvol#eika-cx-}" "$skillvol" >/dev/null || fail "cannot create the skills volume"
 fi
 if [ "$force" = 1 ]; then
  docker run --rm --network none -v "$skillvol:/v" "$image" find /v -mindepth 1 -delete || fail "cannot empty the skills volume"
 fi
 docker run --rm -i --network none -u 0 -v "$skillvol:/v" "$image" tar --numeric-owner -C /v -xpf - <"$set_dir/skills.tar" || fail "the skills restore failed; the target is partly restored"
fi

# What was written is what the set holds.
want_key="$(sed -n 's/.*"secret.key": {"sha256": "\([0-9a-f]*\)".*/\1/p' "$manifest")"
got_key="$(docker run --rm --network none -v "$keyvol:/v:ro" "$image" sha256sum /v/secret.key | cut -d' ' -f1)"
[ "$got_key" = "$want_key" ] || fail "the restored key does not match the set"
have "$memvol" .git/HEAD || fail "the memory volume holds no git repository after the restore"

echo "restore: starting the stack"
$compose up -d --wait --wait-timeout 300 >/dev/null || fail "the stack did not become healthy; see: $compose logs eika"
$compose logs eika 2>/dev/null | grep 'memory reconciled' | tail -1 || true
echo "restore: done. Sign in with the password of the backed-up stack."
