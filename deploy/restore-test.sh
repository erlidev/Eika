#!/bin/bash
# End-to-end test of backup and restore (`make restore-test`), on throwaway
# compose projects of their own, never the stack `make local` runs and never
# the smoke project. It
#   1. starts a source stack, populates it (password, a provider with a sealed
#      API key, a model, sessions with real entries, memories active, pending
#      and archived, one edited and one archived through the API), and records
#      what the API shows;
#   2. takes a backup with deploy/backup.sh;
#   3. checks that an incomplete set, a damaged set and a set from a newer
#      migration are rejected before anything is written, and that a running
#      or non-empty target is refused with nothing changed;
#   4. keeps the source stack running, restores into a new empty stack on the
#      same Docker host (which gets a memory computer id and volume of its own),
#      and checks everything came back: sign-in, sessions, entries, memories
#      and their states, the sealed key opening, the memory git history, the
#      key's mode and an edit through Core (so ownership is right);
#   5. restores again with FORCE=1 over the filled target and checks again;
#      then checks the source's memories, git history and key are unchanged.
# Both stacks, their computers' containers and volumes and the temporary files
# are removed at the end, pass or fail. The images eika:restoretest and
# eika-sandbox:restoretest stay, as a build cache for the next run.
# Needs Docker, curl-in-image (the eika image has it), python3, about 3 GB free.
set -eu
cd "$(dirname "$0")/.."
root="$(pwd)"
tmp="$(mktemp -d)"
id="$$"
pa="eikartsrc$id"
pb="eikartdst$id"
token=restore-test-token
image=postgres:16-alpine
stackc() { echo "env RT_NET=$1 docker compose -p $1 -f compose.yaml -f deploy/restore-test.compose.yaml"; }
ca="$(stackc "$pa")"
cb="$(stackc "$pb")"

step() { echo "== restore-test: $*"; }
die() { echo "restore-test FAILED: $*" >&2; exit 1; }

cleanup() {
	status=$?
	set +e
	for p in "$pa" "$pb"; do
		left="$(docker ps -aq --filter label=eika.computer --filter "network=${p}_sandbox"; docker ps -aq --filter label=eika.computer --filter "network=${p}_sandbox_internal")"
		if [ -n "$left" ]; then
			vols="$(docker inspect -f '{{range .Mounts}}{{.Name}} {{end}}' $left)"
			docker rm -fv $left >/dev/null 2>&1
			[ -z "$(echo $vols)" ] || docker volume rm -f $vols >/dev/null 2>&1
		fi
		$(stackc "$p") down -v >/dev/null 2>&1
	done
	for v in "${memvol:-}" "${bvol:-}" "${skillvol:-}" "${bskillvol:-}"; do [ -z "$v" ] || docker volume rm -f "$v" >/dev/null 2>&1; done
	rm -rf "$tmp"
	[ "$status" = 0 ] && echo "restore-test: passed" || echo "restore-test: failed (stacks removed)" >&2
	exit $status
}
trap cleanup EXIT INT TERM

[ "$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 9999)" -ge 2500 ] || die "less than 2.5 GB of memory is available"

api() { # api "<compose>" METHOD PATH [curl args]
	c="$1"; m="$2"; p="$3"; shift 3
	$c exec -T eika curl -sS -X "$m" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' "http://localhost:8080$p" "$@"
}
code() { $1 exec -T eika curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" "http://localhost:8080$2" 2>/dev/null || true; }
wait_memory() {
	for i in $(seq 1 90); do [ "$(code "$1" /api/memories)" = 200 ] && return 0; sleep 2; done
	die "memory did not become available"
}
# snapshot "<compose>" FILE: what the API shows, in a stable text form.
snapshot() {
	c="$1"; out="$2"
	{
		api "$c" GET /api/memories?limit=50 | python3 -I -c '
import sys, json
names = sorted(m["name"] for m in json.load(sys.stdin)["memories"])
print("\n".join(names))' | while read -r n; do
			echo "memory $n"
			api "$c" GET "/api/memories/$n" | python3 -I -c '
import sys, json
m = json.load(sys.stdin)
for k in sorted(m): print(" ", k, json.dumps(m[k]))'
		done
		api "$c" GET /api/sessions | python3 -I -c '
import sys, json
for s in sorted(json.load(sys.stdin)["sessions"], key=lambda s: s["id"]):
    print("session", s["id"], s.get("kind"), s.get("name"))'
		home="$(api "$c" GET /api/home | python3 -I -c 'import sys,json;print(json.load(sys.stdin)["session"]["id"])')"
		api "$c" GET "/api/sessions/$home/entries" | python3 -I -c '
import sys, json
for e in json.load(sys.stdin)["entries"]:
    print("entry", e["id"], e["seq"], e["kind"], json.dumps(e.get("message", {}).get("content")))'
		api "$c" GET /api/providers | python3 -I -c '
import sys, json
d = json.load(sys.stdin)
for p in (d["providers"] if isinstance(d, dict) else d): print("provider", p["id"], p["name"], p["api_key_set"], p["api_key_hint"])'
	} >"$out"
}
skill_volume() { $1 exec -T postgres sh -c 'psql -At -U eika -d eika -c "SELECT id FROM core.skills_computer"' | tr -d '\r'; }
mem_volume() { $1 exec -T postgres sh -c 'psql -At -U eika -d eika -c "SELECT id FROM core.memory_computer"' | tr -d '\r'; }
git_log() { docker run --rm --network none -v "$1:/v:ro" -e GIT_CONFIG_GLOBAL=/dev/null --entrypoint git "${sandbox:-eika-sandbox:restoretest}" -c safe.directory=/v -C /v log --format='%H %s'; }
keyvol_of() { docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/eika"}}{{.Name}}{{end}}{{end}}' "$($1 ps -aq eika)"; }

# Verification of a restored stack against the source's recording.
verify() { # verify "<compose>" LABEL
	c="$1"; label="$2"
	step "$label: checking the restored stack"
	for i in $(seq 1 60); do [ "$(code "$c" /healthz)" = 200 ] && break; sleep 2; done
	wait_memory "$c"
 api "$c" GET /api/skills | python3 -I -c 'import json,sys; s={x["name"]: x for x in json.load(sys.stdin)["skills"]}; assert s["invoice-totals"]["enabled"] and not s["notes-formatter"]["enabled"]; assert not s["invoice-totals"]["broken"] and not s["notes-formatter"]["broken"]' || die "$label: skills did not reconcile"
 login="$($c exec -T eika curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"password":"restore-test-password-1"}' http://localhost:8080/api/auth/login)"
	[ "$login" = 200 ] || die "$label: the sign-in password did not come back (HTTP $login)"
	snapshot "$c" "$tmp/after-$label.txt"
	diff -u "$tmp/before.txt" "$tmp/after-$label.txt" >"$tmp/diff-$label.txt" || { cat "$tmp/diff-$label.txt" >&2; die "$label: sessions, memories or providers differ from the source"; }
	pid="$(sed -n 's/^provider \([^ ]*\) rt .*/\1/p' "$tmp/after-$label.txt")"
	[ "$(api "$c" POST /api/providers/probe -d "{\"provider_id\":\"$pid\"}" -o /dev/null -w '%{http_code}')" = 200 ] || die "$label: the sealed API key does not open with the restored key"
	kv="$(keyvol_of "$c")"
	[ "$(docker run --rm --network none -v "$kv:/v:ro" "$image" stat -c '%a %u' /v/secret.key)" = "600 1000" ] || die "$label: the key file is not mode 0600 owned by the harness user"
	[ "$(docker run --rm --network none -v "$kv:/v:ro" "$image" sha256sum /v/secret.key | cut -d' ' -f1)" = "$keysum" ] || die "$label: the key differs from the source"
	[ "$(git_log "$bvol")" = "$(cat "$tmp/gitlog.txt")" ] || die "$label: the memory git history differs from the source"
	st="$(api "$c" GET /api/memories/prefers-metric-units)"
	sha="$(printf '%s' "$st" | python3 -I -c 'import sys,json;print(json.load(sys.stdin)["content_sha"])')"
	[ "$(api "$c" PATCH /api/memories/prefers-metric-units -d "{\"content_sha\":\"$sha\",\"body\":\"Edited after $label.\"}" -o /dev/null -w '%{http_code}')" = 200 ] || die "$label: Core cannot edit a restored memory"
	$c logs eika 2>/dev/null | grep -q 'memory reconciled' || die "$label: Core did not reconcile the memory index at startup"
}

step "starting the source stack ($pa)"
$ca up -d --build --wait >/dev/null 2>"$tmp/up.log" || { cat "$tmp/up.log" >&2; die "the source stack did not start"; }
wait_memory "$ca"
memid="$(mem_volume "$ca")"
[ -n "$memid" ] || die "no memory computer id recorded"
memvol="eika-cx-$memid"
skillvol="eika-cx-$(skill_volume "$ca")"

step "populating it"
api "$ca" POST /api/auth/setup -d '{"password":"restore-test-password-1"}' >/dev/null
pid="$(api "$ca" POST /api/providers -d '{"name":"rt","base_url":"http://model:8000/v1","api_key":"rt-sealed-key-0123456789"}' | python3 -I -c 'import sys,json;print(json.load(sys.stdin)["id"])')"
api "$ca" POST /api/models -d "{\"provider_id\":\"$pid\",\"model\":\"smoke\",\"context_window\":32768,\"max_output\":4096}" >/dev/null
home="$(api "$ca" GET /api/home | python3 -I -c 'import sys,json;print(json.load(sys.stdin)["session"]["id"])')"
for text in "Say hello" "Say hello again"; do
	api "$ca" POST "/api/sessions/$home/messages" -d "{\"text\":\"$text\"}" >/dev/null
	for i in $(seq 1 30); do
		api "$ca" GET "/api/sessions/$home/run" | grep -q '"active":false' && break
		sleep 1
	done
done
api "$ca" POST /api/projects -d '{"name":"restore-test-project"}' >/dev/null || true

# Memories are seeded as committed files in the memory computer's volume, in
# all three states, and indexed by Core's startup reconcile. The test stack has no guard, so the user's edit of one memory leaves it pending (screening unavailable).
sandbox=eika-sandbox:restoretest
docker run --rm -u 1000 -v "$memvol:/v" -e GIT_CONFIG_GLOBAL=/dev/null -e GIT_AUTHOR_NAME=Eika -e GIT_AUTHOR_EMAIL=e@localhost -e GIT_COMMITTER_NAME=Eika -e GIT_COMMITTER_EMAIL=e@localhost --entrypoint sh "$sandbox" -c '
set -e; cd /v
mk() { printf -- "---\nname: %s\ndescription: %s\ntype: %s\nsource: restore-test\ncreated: 2026-11-01T10:00:00Z\nupdated: 2026-11-01T10:00:00Z\nexpires:\npinned: false\nstate: %s\n---\n%s\n" "$1" "$2" "$3" "$4" "$5" >"$1.md"; }
mk prefers-metric-units "Use when giving measurements, recipes or weather; the user wants metric." preference active "The user wants metric units."
mk partner-name-sam "Use when the user mentions their partner or plans for two people." person active "The partner is called Sam."
mk uses-24-hour-clock "Use when giving times or scheduling; the user reads the 24-hour clock." preference active "Write times as 14:30."
mk held-for-review "Use when travel plans come up; this one was held for the user to review." fact pending "Held: the user may be travelling in March."
mk old-coffee-order "Use when ordering coffee; this was the old order and is archived." preference archived "Flat white, no sugar."
git add -A; git commit -q -m "seed memories"' || die "cannot seed the memory repository"
$ca restart eika >/dev/null
for i in $(seq 1 60); do [ "$(code "$ca" /healthz)" = 200 ] && break; sleep 2; done
wait_memory "$ca"
sha="$(api "$ca" GET /api/memories/prefers-metric-units | python3 -I -c 'import sys,json;print(json.load(sys.stdin)["content_sha"])')"
api "$ca" PATCH /api/memories/prefers-metric-units -d "{\"content_sha\":\"$sha\",\"body\":\"The user wants metric units, also for recipes.\"}" >/dev/null
api "$ca" POST /api/memories/partner-name-sam/archive -d '{"reason":"restore test"}' >/dev/null
states="$(api "$ca" GET '/api/memories?limit=50' | python3 -I -c 'import sys,json;print(sorted(m["state"] for m in json.load(sys.stdin)["memories"]))')"
[ "$states" = "['active', 'archived', 'archived', 'pending', 'pending']" ] || die "the source memories are not in the expected states: $states"
# Install both fixtures through the API. Set one enabled in the database because
# this test stack has no exact model token counter for the enable endpoint.
for name in invoice-totals notes-formatter; do
 python3 -I -c 'import pathlib,sys,zipfile; p=pathlib.Path(sys.argv[1]); z=zipfile.ZipFile(sys.argv[2], "w"); [z.write(f, f.relative_to(p.parent)) for f in p.rglob("*") if f.is_file()]; z.close()' "internal/skill/testdata/$name" "$tmp/$name.zip"
 status="$($ca exec -T eika curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" -F 'file=@-;filename=skill.zip' http://localhost:8080/api/skills <"$tmp/$name.zip")"
 [ "$status" = 200 ] || [ "$status" = 201 ] || die "skill installation failed (HTTP $status)"
done
$ca exec -T postgres psql -q -v ON_ERROR_STOP=1 -U eika -d eika -c "UPDATE core.skills SET enabled=true WHERE name = 'invoice-totals'" >/dev/null
snapshot "$ca" "$tmp/before.txt"
git_log "$memvol" >"$tmp/gitlog.txt"
keysum="$(docker run --rm --network none -v "$(keyvol_of "$ca"):/v:ro" "$image" sha256sum /v/secret.key | cut -d' ' -f1)"

step "taking the backup"
COMPOSE="$ca" EIKA_BACKUP_DIR="$tmp/backups" deploy/backup.sh >/dev/null || die "the backup failed"
set_dir="$(ls -d "$tmp"/backups/eika-backup-* | head -1)"
[ -f "$set_dir/manifest.json" ] || die "no backup set was written"

step "a running or non-empty target is refused and nothing changes"
out="$(COMPOSE="$ca" BACKUP="$set_dir" deploy/restore.sh 2>&1 || true)"
echo "$out" | grep -q 'eika is running' || die "a running stack was not refused: $out"
$ca stop eika >/dev/null
out="$(COMPOSE="$ca" BACKUP="$set_dir" deploy/restore.sh 2>&1 || true)"
echo "$out" | grep -q 'database already holds data' || die "a non-empty database was not refused: $out"
echo "$out" | grep -q 'key file already exists' || die "an existing key was not refused: $out"
echo "$out" | grep -q 'memory volume .* is not empty' || die "a non-empty memory volume was not refused: $out"
echo "$out" | grep -q 'nothing was written' || die "the refusal does not say nothing was written"
$ca start eika >/dev/null
for i in $(seq 1 60); do [ "$(code "$ca" /healthz)" = 200 ] && break; sleep 2; done
wait_memory "$ca"
snapshot "$ca" "$tmp/refused.txt"
diff -q "$tmp/before.txt" "$tmp/refused.txt" >/dev/null || die "a refused restore changed the target"

step "incomplete, damaged and too-new sets are rejected before anything is written"
bad="$tmp/bad"
for case in missing-tar missing-skills checksum skills-checksum newer missing-manifest missing-key; do
	rm -rf "$bad"; cp -r "$set_dir" "$bad"; chmod -R u+rw "$bad"
	case "$case" in
	missing-tar) rm "$bad/memory.tar"; want='incomplete backup' ;;
	missing-skills) rm "$bad/skills.tar"; want='incomplete backup' ;;
	skills-checksum) printf 'x' >>"$bad/skills.tar"; want='damaged' ;;
	missing-key) rm "$bad/secret.key"; want='incomplete backup' ;;
	missing-manifest) rm "$bad/manifest.json"; want='incomplete backup' ;;
	checksum) printf 'x' >>"$bad/postgres.dump"; want='damaged' ;;
	newer) sed -i 's/"migration": "[^"]*"/"migration": "9999_future"/' "$bad/manifest.json"; want='newer than this build' ;;
	esac
	out="$(COMPOSE="$cb" BACKUP="$bad" deploy/restore.sh 2>&1 || true)"
	echo "$out" | grep -q "$want" || die "the $case set was not rejected as '$want': $out"
done
[ -z "$($cb ps -aq 2>/dev/null)" ] || die "a rejected set started something in the target stack"
[ -z "$(docker volume ls -q --filter "name=${pb}_")" ] || die "a rejected set created volumes"

step "restoring into a new empty stack ($pb) while the source keeps running"
COMPOSE="$cb" BACKUP="$set_dir" deploy/restore.sh >"$tmp/restore.log" 2>&1 || { sed 's/^/  /' "$tmp/restore.log" >&2; die "the restore failed"; }
bvol="eika-cx-$(mem_volume "$cb")"
[ "$bvol" != "eika-cx-" ] && [ "$bvol" != "$memvol" ] || die "the restored stack did not get a memory computer id of its own ($bvol)"
bskillvol="eika-cx-$(skill_volume "$cb")"
[ "$bskillvol" != "$skillvol" ] && [ "$bskillvol" != "eika-cx-" ] || die "the restored stack did not get its own skills volume"
verify "$cb" restore

step "FORCE=1 over the filled target"
$cb stop eika >/dev/null
out="$(COMPOSE="$cb" BACKUP="$set_dir" deploy/restore.sh 2>&1 || true)"
echo "$out" | grep -q 'refusing' || die "the filled target was not refused without FORCE: $out"
FORCE=1 COMPOSE="$cb" BACKUP="$set_dir" deploy/restore.sh >"$tmp/force.log" 2>&1 || { sed 's/^/  /' "$tmp/force.log" >&2; die "the FORCE restore failed"; }
verify "$cb" force
[ "$bskillvol" = "eika-cx-$(skill_volume "$cb")" ] || die "FORCE changed the target skills id"
[ "$bvol" = "eika-cx-$(mem_volume "$cb")" ] || die "FORCE changed the target's memory computer id"

step "the source stack is untouched"
[ "$(code "$ca" /healthz)" = 200 ] || die "the source stack stopped answering"
snapshot "$ca" "$tmp/source-after.txt"
diff -u "$tmp/before.txt" "$tmp/source-after.txt" >&2 || die "the source's sessions, memories or providers changed"
[ "$(git_log "$memvol")" = "$(cat "$tmp/gitlog.txt")" ] || die "the source's memory git history changed"
[ "$(docker run --rm --network none -v "$(keyvol_of "$ca"):/v:ro" "$image" sha256sum /v/secret.key | cut -d' ' -f1)" = "$keysum" ] || die "the source's key changed"

if grep -rq 'rt-sealed-key-0123456789' "$tmp/restore.log" "$tmp/force.log" "$tmp/up.log"; then die "a secret was printed"; fi
