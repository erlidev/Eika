//go:build docker

package store_test

import (
	"errors"
	"testing"

	"github.com/erlidev/eika/internal/agentdef"
	"github.com/erlidev/eika/internal/store"
	"github.com/erlidev/eika/internal/store/storetest"
)

func TestAgentOverrides(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if got, err := st.AgentOverrides(ctx); err != nil || len(got) != 0 {
		t.Fatalf("initial overrides: %v, %v", got, err)
	}
	for _, o := range []agentdef.Override{{Class: new("deep"), Prompt: new("")}, {Prompt: new("custom")}, {}} {
		if err := st.SetAgentOverride(ctx, "worker", o); err != nil {
			t.Fatal(err)
		}
		got, err := st.AgentOverrides(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if o.Class == nil && o.Prompt == nil {
			if len(got) != 0 {
				t.Fatal(got)
			}
			continue
		}
		stored := got["worker"]
		if (stored.Class == nil) != (o.Class == nil) || (stored.Prompt == nil) != (o.Prompt == nil) {
			t.Fatal(stored)
		}
		if o.Class != nil && *stored.Class != *o.Class {
			t.Fatal(stored)
		}
		if o.Prompt != nil && *stored.Prompt != *o.Prompt {
			t.Fatal(stored)
		}
	}
	if err := st.SetAgentOverride(ctx, "browser", agentdef.Override{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if err := st.SetAgentOverride(ctx, "eika", agentdef.Override{Class: new("decision")}); err == nil {
		t.Fatal("decision class accepted")
	}
}
