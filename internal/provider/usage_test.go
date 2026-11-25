package provider_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/erlidev/eika/internal/provider"
)

// fixture reads a file of testdata/usage. See the README there for which are
// recorded from a live server and which are written from documentation.
func fixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "usage", path))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// streamData returns the data lines of a recorded server-sent events stream,
// each with the event name that preceded it ("" for a stream without names).
func streamData(t *testing.T, path string) (events, data []string) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(fixture(t, path)))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && line != "data: [DONE]":
			events = append(events, name)
			data = append(data, strings.TrimPrefix(line, "data: "))
			name = ""
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read stream %s: %v", path, err)
	}
	return events, data
}

// chatStreamUsage folds the usage a recorded Chat Completions stream states,
// the way the provider does.
func chatStreamUsage(t *testing.T, path string) provider.Usage {
	t.Helper()
	var usage provider.Usage
	_, data := streamData(t, path)
	for _, chunk := range data {
		if u, ok := provider.ChatUsage([]byte(chunk)); ok {
			usage = usage.Merge(u)
		}
	}
	return usage
}

// responsesStreamUsage reads the usage of a recorded Responses stream from its
// terminal event.
func responsesStreamUsage(t *testing.T, path string) provider.Usage {
	t.Helper()
	events, data := streamData(t, path)
	for i, name := range events {
		if name != "response.completed" && name != "response.incomplete" {
			continue
		}
		u, ok := provider.ResponsesUsage([]byte(data[i]))
		if !ok {
			t.Fatalf("%s: the %s event states no usage", path, name)
		}
		return u
	}
	t.Fatalf("%s has no terminal event", path)
	return provider.Usage{}
}

func rep(n int) provider.Count { return provider.Reported(n) }

// TestLlamaCppUsageAsRecorded pins what llama.cpp b11942 states on each route,
// cold and warm, with reasoning on. The numbers are the recording's; what they
// show is the point: the prompt count includes the cached tokens
// (input = cached + prefilled), the completion count includes the reasoning,
// and no reasoning count is stated at all.
func TestLlamaCppUsageAsRecorded(t *testing.T) {
	cases := []struct {
		name string
		got  provider.Usage
		want provider.Usage
	}{
		{"chat cold", chatStreamUsage(t, "llamacpp/chat_cold.sse"), provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(100), TotalTokens: rep(190), CachedTokens: rep(0), PrefilledTokens: rep(90)}},
		{"chat warm", chatStreamUsage(t, "llamacpp/chat_warm.sse"), provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(100), TotalTokens: rep(190), CachedTokens: rep(86), PrefilledTokens: rep(4)}},
		{"responses warm", responsesStreamUsage(t, "llamacpp/responses_warm.sse"), provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(107), TotalTokens: rep(197), CachedTokens: rep(86), PrefilledTokens: rep(4)}},
		{"responses cut off inside the reasoning", responsesStreamUsage(t, "llamacpp/responses_cut_in_reasoning.sse"), provider.Usage{
			InputTokens: rep(90), OutputTokens: rep(20), TotalTokens: rep(110), CachedTokens: rep(86), PrefilledTokens: rep(4)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("usage = %+v, want %+v", c.got, c.want)
			}
			in, _ := c.got.InputTokens.Value()
			cached, _ := c.got.CachedTokens.Value()
			prefilled, _ := c.got.PrefilledTokens.Value()
			if in != cached+prefilled {
				t.Errorf("input %d != cached %d + prefilled %d: the prompt count must include the cached tokens", in, cached, prefilled)
			}
			if c.got.ReasoningTokens.Reported() {
				t.Errorf("llama.cpp states no reasoning count, got %+v", c.got.ReasoningTokens)
			}
		})
	}
}

// TestCachedCountPreference pins which field answers when two state the cached
// count: the usage field, with llama.cpp's cache_n as the fallback.
func TestCachedCountPreference(t *testing.T) {
	cases := []struct {
		name  string
		chunk string
		want  provider.Count
	}{
		{"both, usage wins", `{"usage":{"prompt_tokens":90,"completion_tokens":1,"total_tokens":91,"prompt_tokens_details":{"cached_tokens":5}},"timings":{"cache_n":7,"prompt_n":83}}`, rep(5)},
		{"only cache_n, an older build", `{"usage":{"prompt_tokens":90,"completion_tokens":1,"total_tokens":91},"timings":{"cache_n":7,"prompt_n":83}}`, rep(7)},
		{"a stated zero beats a stated cache_n", `{"usage":{"prompt_tokens":90,"completion_tokens":1,"total_tokens":91,"prompt_tokens_details":{"cached_tokens":0}},"timings":{"cache_n":7,"prompt_n":83}}`, rep(0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, ok := provider.ChatUsage([]byte(c.chunk))
			if !ok {
				t.Fatal("no usage read")
			}
			if u.CachedTokens != c.want {
				t.Errorf("cached = %+v, want %+v", u.CachedTokens, c.want)
			}
		})
	}
}

// TestUsageFromEachBackend reads the fixtures written from each backend's
// documentation. They are NOT recorded from live servers (see the README next
// to them): they pin how this code reads the shape the project documents, and
// that a count the backend does not state is not reported.
func TestUsageFromEachBackend(t *testing.T) {
	chat := func(path string) provider.Usage {
		t.Helper()
		u, ok := provider.ChatUsage(fixture(t, path))
		if !ok {
			t.Fatalf("%s: no usage read", path)
		}
		return u
	}
	responses := func(path string) provider.Usage {
		t.Helper()
		u, ok := provider.ResponsesUsage(fixture(t, path))
		if !ok {
			t.Fatalf("%s: no usage read", path)
		}
		return u
	}
	cases := []struct {
		name string
		got  provider.Usage
		want provider.Usage
	}{
		{"openai chat (unverified)", chat("openai/chat_final.json"), provider.Usage{
			InputTokens: rep(2006), OutputTokens: rep(300), TotalTokens: rep(2306), CachedTokens: rep(1920), ReasoningTokens: rep(192)}},
		{"openai responses (unverified)", responses("openai/responses_completed.json"), provider.Usage{
			InputTokens: rep(1000), OutputTokens: rep(300), TotalTokens: rep(1300), CachedTokens: rep(800), ReasoningTokens: rep(200)}},
		{"vllm chat without --enable-prompt-tokens-details (unverified)", chat("vllm/chat_final_without_details.json"), provider.Usage{
			InputTokens: rep(512), OutputTokens: rep(48), TotalTokens: rep(560)}},
		// SGLang's top-level reasoning_tokens is 0 whether or not reasoning
		// was counted, so it is not read, and the count stays unreported.
		{"sglang chat without cache report (unverified)", chat("sglang/chat_final_without_cache_report.json"), provider.Usage{
			InputTokens: rep(512), OutputTokens: rep(48), TotalTokens: rep(560)}},
		{"tabby chat (unverified)", chat("tabby/chat_final.json"), provider.Usage{
			InputTokens: rep(512), OutputTokens: rep(48), TotalTokens: rep(560)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("usage = %+v, want %+v", c.got, c.want)
			}
		})
	}

	t.Run("a chunk before the last states nothing (unverified)", func(t *testing.T) {
		if u, ok := provider.ChatUsage(fixture(t, "openai/chat_early_chunk.json")); ok {
			t.Errorf("read usage %+v from a chunk with usage null", u)
		}
	})
}

// TestUsageStatedZeroStaysZero checks the line between a count a server
// stated as 0 and one it did not state.
func TestUsageStatedZeroStaysZero(t *testing.T) {
	t.Run("zero beside real counts is a statement", func(t *testing.T) {
		u, ok := provider.ChatUsage([]byte(`{"usage":{"prompt_tokens":12,"completion_tokens":0,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`))
		if !ok {
			t.Fatal("no usage read")
		}
		want := provider.Usage{InputTokens: rep(12), OutputTokens: rep(0), TotalTokens: rep(12), CachedTokens: rep(0), ReasoningTokens: rep(0)}
		if u != want {
			t.Errorf("usage = %+v, want %+v", u, want)
		}
	})

	t.Run("a block of placeholder zeros states nothing", func(t *testing.T) {
		for _, chunk := range []string{
			`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			`{"usage":null}`,
		} {
			if u, ok := provider.ChatUsage([]byte(chunk)); ok {
				t.Errorf("ChatUsage(%s) = %+v, want nothing", chunk, u)
			}
		}
	})

	t.Run("a count that is no token count is not reported", func(t *testing.T) {
		u, ok := provider.ChatUsage([]byte(`{"usage":{"prompt_tokens":12,"completion_tokens":-3,"total_tokens":12.5}}`))
		if !ok {
			t.Fatal("no usage read")
		}
		want := provider.Usage{InputTokens: rep(12)}
		if u != want {
			t.Errorf("usage = %+v, want %+v", u, want)
		}
	})

	t.Run("a whole number written with a decimal point is read", func(t *testing.T) {
		u, _ := provider.ChatUsage([]byte(`{"usage":{"prompt_tokens":12.0,"completion_tokens":2.0,"total_tokens":14.0}}`))
		if u.InputTokens != rep(12) || u.OutputTokens != rep(2) || u.TotalTokens != rep(14) {
			t.Errorf("usage = %+v", u)
		}
	})

	t.Run("text that is not an object states nothing", func(t *testing.T) {
		for _, raw := range []string{`[DONE]`, `{"usage":"many"}`, `{`} {
			if u, ok := provider.ChatUsage([]byte(raw)); ok {
				t.Errorf("ChatUsage(%q) = %+v, want nothing", raw, u)
			}
			if u, ok := provider.ResponsesUsage([]byte(raw)); ok {
				t.Errorf("ResponsesUsage(%q) = %+v, want nothing", raw, u)
			}
		}
	})

}

func TestCountEncodesNullWhenNotReported(t *testing.T) {
	type wrap struct {
		A provider.Count `json:"a"`
		B provider.Count `json:"b"`
		C provider.Count `json:"c"`
	}
	data, err := json.Marshal(wrap{B: rep(0), C: rep(7)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"a":null,"b":0,"c":7}` {
		t.Fatalf("encoded as %s", data)
	}
	var back wrap
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != (wrap{B: rep(0), C: rep(7)}) {
		t.Errorf("decoded %+v", back)
	}
	for _, bad := range []string{`{"a":-1}`, `{"a":"7"}`} {
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want an error", bad)
		}
	}
}

func TestTallyReportsOnlyWhatEveryCallReported(t *testing.T) {
	var tally provider.Tally
	if tally.Total().Reported() {
		t.Errorf("an empty tally reports %+v", tally.Total())
	}
	tally.Add(provider.Usage{InputTokens: rep(10), OutputTokens: rep(2), TotalTokens: rep(12), CachedTokens: rep(8)})
	tally.Add(provider.Usage{InputTokens: rep(20), OutputTokens: rep(3), TotalTokens: rep(23)})
	want := provider.Usage{InputTokens: rep(30), OutputTokens: rep(5), TotalTokens: rep(35)}
	if got := tally.Total(); got != want {
		t.Errorf("total = %+v, want %+v: the cached count of the second call was not reported, so the total's is not", got, want)
	}

	var silent provider.Tally
	silent.Add(provider.Usage{InputTokens: rep(10), OutputTokens: rep(2), TotalTokens: rep(12)})
	silent.Add(provider.Usage{})
	if got := silent.Total(); got.Reported() {
		t.Errorf("a call that reported nothing left %+v in the total", got)
	}
}

func TestPrefilledIsStatedOrThePromptLessItsCachedPart(t *testing.T) {
	cases := []struct {
		name  string
		usage provider.Usage
		want  provider.Count
	}{
		{"stated by the server", provider.Usage{InputTokens: rep(90), CachedTokens: rep(86), PrefilledTokens: rep(4)}, rep(4)},
		{"prompt less cached", provider.Usage{InputTokens: rep(90), CachedTokens: rep(86)}, rep(4)},
		{"cached count unknown", provider.Usage{InputTokens: rep(90)}, provider.Count{}},
		{"cached beyond the prompt is not believed", provider.Usage{InputTokens: rep(10), CachedTokens: rep(86)}, provider.Count{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.usage.Prefilled(); got != c.want {
				t.Errorf("Prefilled = %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestUsageReadsEntriesStoredBeforeCountsCouldBeUnreported keeps old session
// entries readable: they have no cached_tokens key and wrote 0 for anything a
// server did not state.
func TestUsageReadsEntriesStoredBeforeCountsCouldBeUnreported(t *testing.T) {
	cases := []struct {
		name string
		json string
		want provider.Usage
	}{
		{"old entry with counts", `{"input_tokens":18,"output_tokens":5,"total_tokens":23}`,
			provider.Usage{InputTokens: rep(18), OutputTokens: rep(5), TotalTokens: rep(23)}},
		{"old entry that stated nothing", `{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, provider.Usage{}},
		{"new entry that states zeros", `{"input_tokens":0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"prefilled_tokens":null,"reasoning_tokens":null}`,
			provider.Usage{InputTokens: rep(0), OutputTokens: rep(0), TotalTokens: rep(0), CachedTokens: rep(0)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var u provider.Usage
			if err := json.Unmarshal([]byte(c.json), &u); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if u != c.want {
				t.Errorf("usage = %+v, want %+v", u, c.want)
			}
			// Whatever was read encodes and reads back as itself.
			data, err := json.Marshal(u)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var again provider.Usage
			if err := json.Unmarshal(data, &again); err != nil || again != u {
				t.Errorf("round trip = %+v (%v), want %+v", again, err, u)
			}
		})
	}
}
