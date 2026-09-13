package eikad

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
)

// maxWatchEntries bounds the snapshot the watcher keeps, so that a workspace
// with a huge tree cannot exhaust the sandbox's memory. Beyond the limit the
// walk stops and later files are not reported.
const maxWatchEntries = 100_000

// stamp is what the watcher remembers about a file between scans.
type stamp struct {
	modTime time.Time
	size    int64
	isDir   bool
}

// handleWatch upgrades to a WebSocket and reports file changes under the
// workspace root. It polls and compares modification times rather than taking
// a dependency on an inotify library; a workspace tree is small enough that a
// scan per second is cheap, and polling behaves the same on a bind mount.
func (d *Daemon) handleWatch(w http.ResponseWriter, r *http.Request) {
	root, err := d.requestPath(r)
	if err != nil {
		writeError(w, d.log, statusFor(err), err)
		return
	}

	// The first snapshot is taken before the handshake completes, so that a
	// change made as soon as the client is connected is never missed.
	previous := d.scan(root)

	// The origin check is skipped because the only client is the harness,
	// which authenticates with the bearer token rather than a browser origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		d.log.Error("accept watch websocket", "error", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(d.watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		current := d.scan(root)
		for _, ev := range changes(previous, current) {
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
		previous = current
	}
}

// scan snapshots the tree under root, keyed by root-relative path.
func (d *Daemon) scan(root string) map[string]stamp {
	out := make(map[string]stamp)
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk is reported as deleted by the
			// next comparison; an unreadable directory is skipped.
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if len(out) >= maxWatchEntries {
			return filepath.SkipAll
		}
		fi, err := entry.Info()
		if err != nil {
			return nil
		}
		out[d.rel(path)] = stamp{modTime: fi.ModTime(), size: fi.Size(), isDir: fi.IsDir()}
		return nil
	})
	return out
}

// changes compares two snapshots and returns one event per difference.
func changes(before, after map[string]stamp) []WatchEvent {
	now := time.Now().UTC()
	var out []WatchEvent
	for path, cur := range after {
		old, existed := before[path]
		switch {
		case !existed:
			out = append(out, WatchEvent{Path: path, Kind: ChangeCreated, IsDir: cur.isDir, Time: now})
		case !cur.isDir && (!old.modTime.Equal(cur.modTime) || old.size != cur.size):
			out = append(out, WatchEvent{Path: path, Kind: ChangeModified, Time: now})
		}
	}
	for path, old := range before {
		if _, ok := after[path]; !ok {
			out = append(out, WatchEvent{Path: path, Kind: ChangeDeleted, IsDir: old.isDir, Time: now})
		}
	}
	return out
}
