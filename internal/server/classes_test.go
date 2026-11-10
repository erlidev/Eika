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
	assign := func(pairs ...modelClass) map[modelClass]string {
		out := map[modelClass]string{}
		for i := 0; i+1 < len(pairs); i += 2 {
			out[pairs[i]] = string(pairs[i+1])
		}
		return out
	}
	for name, c := range map[string]struct {
		class    modelClass
		assigned map[modelClass]string
		def      string
		want     string
		via      modelClass
		viaDef   bool
	}{
		"a class with a model":                         {class: classFast, assigned: assign(classFast, "tiny", classMain, "mid"), def: "big", want: "tiny", via: classFast},
		"deep falls back to main":                      {class: classDeep, assigned: assign(classMain, "mid"), want: "mid", via: classMain},
		"fast falls back to main":                      {class: classFast, assigned: assign(classMain, "mid"), def: "big", want: "mid", via: classMain},
		"main does not fall back to deep":              {class: classMain, assigned: assign(classDeep, "big")},
		"fast does not fall back to deep":              {class: classFast, assigned: assign(classDeep, "big")},
		"deep is its own when assigned":                {class: classDeep, assigned: assign(classDeep, "big", classMain, "mid"), want: "big", via: classDeep},
		"a deleted model counts as none":               {class: classFast, assigned: assign(classFast, "gone", classMain, "mid"), want: "mid", via: classMain},
		"nothing assigned and no default":              {class: classFast},
		"main with no model is the default model":      {class: classMain, def: "big", want: "big", viaDef: true},
		"fast reaches the default through main":        {class: classFast, assigned: assign(classDeep, "mid"), def: "big", want: "big", viaDef: true},
		"deep reaches the default through main":        {class: classDeep, def: "big", want: "big", viaDef: true},
		"a default that does not exist counts as none": {class: classMain, def: "gone"},
		"a decision default counts as none":            {class: classMain, def: "judge"},
		"decision has a decision model":                {class: classDecision, assigned: assign(classDecision, "judge"), want: "judge", via: classDecision},
		"decision does not fall back to a chat one":    {class: classDecision, assigned: assign(classMain, "mid"), def: "big"},
		"a decision model never serves a chat class":   {class: classFast, assigned: assign(classFast, "judge", classMain, "mid"), want: "mid", via: classMain},
		"a chat model never serves decision":           {class: classDecision, assigned: assign(classDecision, "mid")},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := resolveClass(c.class, c.assigned, models, c.def)
			if c.want == "" {
				if ok {
					t.Fatalf("resolved to %q via %q, want no model", got.Model.Name, got.Via)
				}
				return
			}
			if !ok || got.Model.Name != c.want || got.Via != c.via || got.ViaDefault != c.viaDef {
				t.Errorf("resolved to %q via %q, default %v (found %v), want %q via %q, default %v",
					got.Model.Name, got.Via, got.ViaDefault, ok, c.want, c.via, c.viaDef)
			}
		})
	}
}
