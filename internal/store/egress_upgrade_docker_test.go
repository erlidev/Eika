//go:build docker

package store_test

import (
	"os"
	"testing"
)

func TestUpgradePublicEgressPreservesSandbox(t *testing.T) {
	st, conn := upgradeFrom(t, "0026_trusted_sources",
		`INSERT INTO computers (id, name, state, sandbox) VALUES ('old', 'Old', 'running', '{"egress":"open","allow":["example.com"],"pids":42}'), ('missing', 'Missing', 'stopped', '{}'), ('restricted', 'Restricted', 'stopped', '{"egress":"none"}')`,
		`INSERT INTO settings (key, value) VALUES ('sandbox_egress', '{"mode":"open","allow":["example.com"]}')`)
	for id, want := range map[string]string{"old": "public", "missing": "public", "restricted": "none"} {
		cx, err := st.Computer(t.Context(), id)
		if err != nil || cx.Sandbox.Egress != want {
			t.Fatalf("computer %s: %+v, %v", id, cx, err)
		}
		if id == "old" && (cx.Sandbox.PIDs != 42 || len(cx.Sandbox.Allow) != 1 || cx.Sandbox.Allow[0] != "example.com") {
			t.Fatalf("sandbox lost data: %+v", cx.Sandbox)
		}
	}
	var mode, host string
	if err := conn.QueryRow(t.Context(), `SELECT value->>'mode', value->'allow'->>0 FROM settings WHERE key = 'sandbox_egress'`).Scan(&mode, &host); err != nil || mode != "public" || host != "example.com" {
		t.Fatalf("setting: %s %s %v", mode, host, err)
	}
}

func TestUpgradePublicEgressHandlesNonObjectsAndRepeat(t *testing.T) {
	for _, value := range []string{`null`, `"scalar"`, `[]`, `{}`} {
		t.Run(value, func(t *testing.T) {
			_, conn := upgradeFrom(t, "0026_trusted_sources",
				`INSERT INTO computers (id, name, state, sandbox) VALUES ('bad', 'Bad', 'stopped', '`+value+`')`,
				`INSERT INTO settings (key, value) VALUES ('sandbox_egress', '`+value+`')`)
			migration, err := os.ReadFile("migrations/0027_public_egress.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(t.Context(), string(migration)); err != nil {
				t.Fatalf("repeat migration: %v", err)
			}
			var computerMode, settingMode string
			err = conn.QueryRow(t.Context(), `SELECT sandbox->>'egress', (SELECT value->>'mode' FROM settings WHERE key='sandbox_egress') FROM computers WHERE id='bad'`).Scan(&computerMode, &settingMode)
			if err != nil || computerMode != "public" || settingMode != "public" {
				t.Fatalf("modes: %q %q: %v", computerMode, settingMode, err)
			}
		})
	}
}
