//go:build docker

package server_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
	"github.com/erlidev/eika/internal/tool"
	"github.com/erlidev/eika/internal/tool/builtin"
)

func TestHistoryToolsWithStoredSessions(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	home, err := st.CreateSession(ctx, store.Session{Kind: store.SessionHome, Name: "home", Agent: "eika"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		payload, err := json.Marshal(map[string]string{"content": fmt.Sprintf("orchard %d %s", i, strings.Repeat("x", 2500))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendEntry(ctx, home.ID, store.Entry{Kind: store.KindUser, Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AppendEntry(ctx, home.ID, store.Entry{Kind: store.KindCompaction, Payload: json.RawMessage(`{"summary":"old work","kept":1}`)}); err != nil {
		t.Fatal(err)
	}
	reg, err := builtin.Registry(builtin.Deps{History: st})
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, input string) tool.Result {
		t.Helper()
		tl, ok := reg.Get(name)
		if !ok {
			t.Fatal(name)
		}
		result, err := tl.Call(ctx, tool.CallContext{SessionID: home.ID}, json.RawMessage(input))
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
		return result
	}
	var first, second []struct {
		Hit string `json:"hit"`
	}
	if err := json.Unmarshal([]byte(call("history_search", `{"query":"orchard"}`).Content), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(call("history_search", `{"query":"orchard","page":2}`).Content), &second); err != nil {
		t.Fatal(err)
	}
	if len(first) != 5 || len(second) != 2 || first[0].Hit == second[0].Hit {
		t.Fatalf("pages = %+v, %+v", first, second)
	}
	var browsed []struct {
		Hit string `json:"hit"`
	}
	if err := json.Unmarshal([]byte(call("history_search", `{}`).Content), &browsed); err != nil || len(browsed) != 5 {
		t.Fatalf("browse = %+v, %v", browsed, err)
	}
	var read []struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(call("history_read", fmt.Sprintf(`{"entry":%q,"full":true}`, first[0].Hit)).Content), &read); err != nil || len(read) != 1 || read[0].Truncated || !strings.Contains(read[0].Content, strings.Repeat("x", 2500)) {
		t.Fatalf("read = %+v, %v", read, err)
	}
}
