package provider_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// ptr returns a pointer to v, for the optional fields of Sampling.
func ptr[T any](v T) *T { return &v }

func TestSamplingOverLetsTheUpperLayerWin(t *testing.T) {
	model := provider.Sampling{MaxOutput: ptr(4096), ReasoningEffort: ptr("high"), Stop: []string{"END"}}
	profile := provider.Sampling{Temperature: ptr(0.2), TopK: ptr(20), ReasoningEffort: ptr("low")}
	cases := []struct {
		name  string
		upper provider.Sampling
		lower provider.Sampling
		want  provider.Sampling
	}{
		{"nothing over nothing", provider.Sampling{}, provider.Sampling{}, provider.Sampling{}},
		{"nothing over a layer keeps it", provider.Sampling{}, model, model},
		{"a layer over nothing keeps it", profile, provider.Sampling{}, profile},
		{
			"set fields win and the rest fall through",
			profile, model,
			provider.Sampling{Temperature: ptr(0.2), TopK: ptr(20), ReasoningEffort: ptr("low"), MaxOutput: ptr(4096), Stop: []string{"END"}},
		},
		{
			"a zero value is set",
			provider.Sampling{Temperature: ptr(0.0), Seed: ptr(int64(0))},
			provider.Sampling{Temperature: ptr(1.0), Seed: ptr(int64(9))},
			provider.Sampling{Temperature: ptr(0.0), Seed: ptr(int64(0))},
		},
		{
			"an empty stop list clears the lower one",
			provider.Sampling{Stop: []string{}}, model,
			provider.Sampling{MaxOutput: ptr(4096), ReasoningEffort: ptr("high"), Stop: []string{}},
		},
		{
			"an empty effort overrides the lower one",
			provider.Sampling{ReasoningEffort: ptr("")}, model,
			provider.Sampling{MaxOutput: ptr(4096), ReasoningEffort: ptr(""), Stop: []string{"END"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.upper.Over(c.lower); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Over = %s, want %s", encode(t, got), encode(t, c.want))
			}
		})
	}
}

func TestSamplingOverSharesNoMemoryWithItsLayers(t *testing.T) {
	upper := provider.Sampling{Temperature: ptr(0.5), Stop: []string{"a"}}
	lower := provider.Sampling{MaxOutput: ptr(10)}
	got := upper.Over(lower)
	*got.Temperature = 1
	*got.MaxOutput = 20
	got.Stop[0] = "b"
	if *upper.Temperature != 0.5 || *lower.MaxOutput != 10 || upper.Stop[0] != "a" {
		t.Errorf("layers changed through the result: upper %s, lower %s", encode(t, upper), encode(t, lower))
	}
}

func TestSamplingLeavesUnsetFieldsOutOfItsJSON(t *testing.T) {
	cases := []struct {
		name string
		s    provider.Sampling
		want string
	}{
		{"nothing set", provider.Sampling{}, `{}`},
		{"zero values", provider.Sampling{Temperature: ptr(0.0), Seed: ptr(int64(0))}, `{"temperature":0,"seed":0}`},
		{"an empty stop list", provider.Sampling{Stop: []string{}}, `{"stop":[]}`},
		{
			"every field",
			provider.Sampling{
				Temperature: ptr(0.7), TopP: ptr(0.9), TopK: ptr(40), MinP: ptr(0.05),
				FrequencyPenalty: ptr(0.1), PresencePenalty: ptr(0.2), Seed: ptr(int64(3)),
				Stop: []string{"x"}, MaxOutput: ptr(100), ReasoningEffort: ptr("high"),
			},
			`{"temperature":0.7,"top_p":0.9,"top_k":40,"min_p":0.05,"frequency_penalty":0.1,` +
				`"presence_penalty":0.2,"seed":3,"stop":["x"],"max_output":100,"reasoning_effort":"high"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encode(t, c.s); got != c.want {
				t.Errorf("JSON = %s, want %s", got, c.want)
			}
			var back provider.Sampling
			if err := json.Unmarshal([]byte(c.want), &back); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(back, c.s) {
				t.Errorf("round trip = %s, want %s", encode(t, back), c.want)
			}
		})
	}
}

func TestSamplingValidate(t *testing.T) {
	cases := []struct {
		name    string
		s       provider.Sampling
		wantErr string
	}{
		{"nothing set", provider.Sampling{}, ""},
		{
			"every field at its bounds",
			provider.Sampling{
				Temperature: ptr(2.0), TopP: ptr(1.0), TopK: ptr(1), MinP: ptr(0.0),
				FrequencyPenalty: ptr(-2.0), PresencePenalty: ptr(2.0), Seed: ptr(int64(-1)),
				Stop:      []string{strings.Repeat("s", provider.MaxStopSequenceLen)},
				MaxOutput: ptr(1), ReasoningEffort: ptr("think-harder"),
			},
			"",
		},
		{"an empty effort", provider.Sampling{ReasoningEffort: ptr("")}, ""},
		{"an empty stop list", provider.Sampling{Stop: []string{}}, ""},
		{"temperature below 0", provider.Sampling{Temperature: ptr(-0.1)}, "temperature"},
		{"temperature above 2", provider.Sampling{Temperature: ptr(2.1)}, "temperature"},
		{"temperature not a number", provider.Sampling{Temperature: ptr(math.NaN())}, "temperature"},
		{"top_p above 1", provider.Sampling{TopP: ptr(1.5)}, "top_p"},
		{"top_k of 0", provider.Sampling{TopK: ptr(0)}, "top_k"},
		{"min_p below 0", provider.Sampling{MinP: ptr(-0.5)}, "min_p"},
		{"frequency_penalty below -2", provider.Sampling{FrequencyPenalty: ptr(-3.0)}, "frequency_penalty"},
		{"presence_penalty above 2", provider.Sampling{PresencePenalty: ptr(2.5)}, "presence_penalty"},
		{"too many stop sequences", provider.Sampling{Stop: make([]string, provider.MaxStopSequences+1)}, "stop"},
		{"an empty stop sequence", provider.Sampling{Stop: []string{"ok", ""}}, "stop"},
		{"a long stop sequence", provider.Sampling{Stop: []string{strings.Repeat("s", provider.MaxStopSequenceLen+1)}}, "stop"},
		{"max_output of 0", provider.Sampling{MaxOutput: ptr(0)}, "max_output"},
		{"an effort with a space", provider.Sampling{ReasoningEffort: ptr("very high")}, "reasoning_effort"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.s.Validate()
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("Validate = %v, want nil", err)
			case c.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), c.wantErr)):
				t.Errorf("Validate = %v, want an error naming %s", err, c.wantErr)
			}
		})
	}
}

// encode returns the JSON of v, for comparisons and failure messages.
func encode(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	return string(data)
}
