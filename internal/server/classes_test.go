package server

import (
	"testing"

	"github.com/erlidev/eika/internal/store"
)

func TestResolveClass(t *testing.T) {
	models := []store.Model{
		{Name: "big", Kind: store.ModelKindChat},
		{Name: "mid", Kind: store.ModelKindChat},
		{Name: "tiny", Kind: store.ModelKindChat},
		{Name: "judge", Kind: store.ModelKindDecision},
	}
	for name, c := range map[string]struct {
		class    modelClass
		assigned map[modelClass]string
		want     string
		via      modelClass
	}{
		"a class with a model":                      {classFast, map[modelClass]string{classFast: "tiny", classMain: "mid"}, "tiny", classFast},
		"deep falls back to main":                   {classDeep, map[modelClass]string{classMain: "mid"}, "mid", classMain},
		"fast falls back to main":                   {classFast, map[modelClass]string{classMain: "mid"}, "mid", classMain},
		"main does not fall back to deep":           {classMain, map[modelClass]string{classDeep: "big"}, "", ""},
		"fast does not fall back to deep":           {classFast, map[modelClass]string{classDeep: "big"}, "", ""},
		"deep is its own when assigned":             {classDeep, map[modelClass]string{classDeep: "big", classMain: "mid"}, "big", classDeep},
		"a deleted model counts as none":            {classFast, map[modelClass]string{classFast: "gone", classMain: "mid"}, "mid", classMain},
		"nothing assigned":                          {classFast, nil, "", ""},
		"decision has a decision model":             {classDecision, map[modelClass]string{classDecision: "judge"}, "judge", classDecision},
		"decision does not fall back to a chat one": {classDecision, map[modelClass]string{classMain: "mid"}, "", ""},
		"a decision model never serves a chat class": {
			classFast, map[modelClass]string{classFast: "judge", classMain: "mid"}, "mid", classMain,
		},
		"a chat model never serves decision": {classDecision, map[modelClass]string{classDecision: "mid"}, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := resolveClass(c.class, c.assigned, models)
			if c.want == "" {
				if ok {
					t.Fatalf("resolved to %q via %s, want no model", got.Model.Name, got.Via)
				}
				return
			}
			if !ok || got.Model.Name != c.want || got.Via != c.via {
				t.Errorf("resolved to %q via %q (found %v), want %q via %q", got.Model.Name, got.Via, ok, c.want, c.via)
			}
		})
	}
}
