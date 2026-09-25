package eikad_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/erlidev/eika/internal/eikad"
)

// processConn is a /process socket under test.
type processConn struct {
	t    *testing.T
	ctx  context.Context
	conn *websocket.Conn
}

func openProcess(t *testing.T, base string, start eikad.ProcessMessage) *processConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	p := &processConn{t: t, ctx: ctx, conn: dial(t, base+"/process")}
	start.Type = eikad.ProcessStart
	p.send(start)
	return p
}

func (p *processConn) send(msg eikad.ProcessMessage) {
	p.t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		p.t.Fatalf("encode process message: %v", err)
	}
	if err := p.conn.Write(p.ctx, websocket.MessageText, data); err != nil {
		p.t.Fatalf("write process message: %v", err)
	}
}

// until reads messages until one of type typ, collecting the output.
func (p *processConn) until(typ string) (eikad.ProcessMessage, string, string) {
	p.t.Helper()
	var stdout, stderr strings.Builder
	for {
		_, data, err := p.conn.Read(p.ctx)
		if err != nil {
			p.t.Fatalf("read process message: %v (stdout %q, stderr %q)", err, stdout.String(), stderr.String())
		}
		var msg eikad.ProcessMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			p.t.Fatalf("decode process message: %v", err)
		}
		switch msg.Type {
		case eikad.ProcessStdout:
			stdout.Write(msg.Data)
		case eikad.ProcessStderr:
			stderr.Write(msg.Data)
		}
		if msg.Type == typ {
			return msg, stdout.String(), stderr.String()
		}
		if msg.Type == eikad.ProcessExit || msg.Type == eikad.ProcessError {
			p.t.Fatalf("got %+v while waiting for %s", msg, typ)
		}
	}
}

func TestProcessCarriesTheStandardStreams(t *testing.T) {
	base, root := newDaemon(t, eikad.Options{})
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `echo "in $(basename "$PWD")"; while read line; do echo "got $line"; done`
	p := openProcess(t, base, eikad.ProcessMessage{Command: "sh", Args: []string{"-c", script}, Dir: "sub"})
	p.send(eikad.ProcessMessage{Type: eikad.ProcessStdin, Data: []byte("one\ntwo\n")})
	var stdout string
	for !strings.Contains(stdout, "got two") {
		_, more, _ := p.until(eikad.ProcessStdout)
		stdout += more
	}
	if stdout != "in sub\ngot one\ngot two\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestProcessReportsHowItEnded(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	t.Setenv(eikad.TokenEnv, "daemon-secret")
	p := openProcess(t, base, eikad.ProcessMessage{Command: "sh", Args: []string{"-c", `echo "token=$` + eikad.TokenEnv + ` $GREETING" >&2; exit 4`}, Env: []string{"GREETING=hi"}})
	exit, _, stderr := p.until(eikad.ProcessExit)
	if exit.ExitCode != 4 {
		t.Errorf("exit code = %d, want 4", exit.ExitCode)
	}
	if stderr != "token= hi\n" {
		t.Errorf("stderr = %q, want the environment without the daemon's token", stderr)
	}
}

func TestProcessThatCannotStart(t *testing.T) {
	base, _ := newDaemon(t, eikad.Options{})
	for name, start := range map[string]eikad.ProcessMessage{
		"missing command": {Command: "no-such-command-anywhere"},
		"escaping dir":    {Command: "true", Dir: "../.."},
		"empty command":   {Command: " "},
	} {
		t.Run(name, func(t *testing.T) {
			p := openProcess(t, base, start)
			msg, _, _ := p.until(eikad.ProcessError)
			if msg.Error == "" {
				t.Error("error message is empty")
			}
		})
	}
}

func TestProcessStopsWhenTheClientLeaves(t *testing.T) {
	base, root := newDaemon(t, eikad.Options{})
	marker := filepath.Join(root, "stopped")
	// The process ignores its stdin closing and waits for SIGTERM, which
	// it records before exiting.
	script := `trap 'touch ` + marker + `; exit 0' TERM; echo ready; while :; do sleep 0.05; done`
	p := openProcess(t, base, eikad.ProcessMessage{Command: "sh", Args: []string{"-c", script}})
	p.until(eikad.ProcessStdout)
	p.conn.CloseNow()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the process was not sent SIGTERM after the client left")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
