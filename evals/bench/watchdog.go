package bench

import (
	"fmt"
	"strings"
)

// The paths a run keeps on the server, all under /tmp. They are the only files
// the runner writes there: a lock that says who holds the bench, a heartbeat
// the watchdog watches, and the mark that ends the watchdog.
const (
	lockDir = "/tmp/eika-bench.lock"
)

func beatFile(run string) string { return "/tmp/eika-bench-" + run + ".beat" }
func doneFile(run string) string { return "/tmp/eika-bench-" + run + ".done" }

// variantName is the name of the container a run creates for a changed model.
func variantName(run string) string { return "eika-bench-" + run }

// watchdogPlan is what the server-side restore script needs: it is written at
// the start of the run from the snapshot, because it must work when the
// harness is gone.
type watchdogPlan struct {
	// Run identifies the run.
	Run string
	// Created is the container the run creates for a variant.
	Created string
	// Stop are the containers the run may start that were not running before:
	// the script stops them if they run.
	Stop []string
	// Start are the containers that ran before and that the run may stop, in
	// the order they are started again.
	Start []string
}

// script writes the restore script for sh. It exits at once when the done mark
// appears; otherwise it waits for the heartbeat to go stale (EIKA_BENCH_STALE
// seconds, 180 by default; EIKA_BENCH_POLL is how often it looks) and then does
// what a restore does: remove the variant, stop what the run started, wait for
// the GPU to empty, start what ran before. Every name in it passed checkName,
// so none needs quoting.
func (p watchdogPlan) script() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line(`beat=%s`, beatFile(p.Run))
	line(`done=%s`, doneFile(p.Run))
	line(`lock=%s`, lockDir)
	line(`poll=${EIKA_BENCH_POLL:-15}`)
	line(`stale=${EIKA_BENCH_STALE:-180}`)
	line(`while :; do`)
	line(`  if [ -e "$done" ]; then rm -f "$done" "$beat"; exit 0; fi`)
	line(`  mt=$(stat -c %%Y "$beat" 2>/dev/null || echo 0)`)
	line(`  if [ $(( $(date +%%s) - mt )) -gt "$stale" ]; then break; fi`)
	line(`  sleep "$poll"`)
	line(`done`)
	line(`echo "eika bench watchdog: run %s stopped answering, restoring the bench" >&2`, p.Run)
	line(`docker rm -f %s >/dev/null 2>&1`, p.Created)
	for _, c := range p.Stop {
		line(`if [ "$(docker inspect -f "{{.State.Running}}" %s 2>/dev/null)" = true ]; then docker stop -t 30 %s >/dev/null 2>&1; fi`, c, c)
	}
	line(`n=0`)
	line(`while [ -n "$(nvidia-smi --query-compute-apps=pid --format=csv,noheader 2>/dev/null)" ] && [ "$n" -lt 30 ]; do sleep 2; n=$((n+1)); done`)
	for _, c := range p.Start {
		line(`docker start %s >/dev/null 2>&1`, c)
	}
	line(`rm -rf "$lock"`)
	line(`rm -f "$beat"`)
	return b.String()
}
