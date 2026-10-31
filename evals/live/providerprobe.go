package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/erlidev/eika/internal/provider/probe"
)

// ProviderProbe is the scenario that runs the harness's provider probe
// against the bench and checks what it found against a route the probe does
// not read: llama.cpp's /slots lists every slot with its context, so the slot
// count and the per-request window have a second witness. It sends no prompt
// to the model.
//
// What it cannot check is the KV cache: nothing the server serves says whether
// it is unified (see probe's llamaCpp), so the probe must say unknown for a
// server with several slots, and the scenario fails if it guesses.
func ProviderProbe() Scenario {
	return Scenario{
		Name: "provider_probe",
		Run: func(ctx context.Context, t *Target) ([]Call, error) {
			res := probe.Run(ctx, probe.Target{BaseURL: t.BaseURL, APIKey: t.Key, Models: t.Models})
			if res.Backend != probe.LlamaCpp {
				return nil, fmt.Errorf("the probe took the bench for backend %q, want llamacpp", res.Backend)
			}
			slots, err := readSlots(ctx, t)
			if err != nil {
				return nil, err
			}
			if res.Slots != len(slots) {
				return nil, fmt.Errorf("the probe found %d slots, /slots lists %d", res.Slots, len(slots))
			}
			if res.Capabilities.Window <= 0 {
				return nil, fmt.Errorf("the probe found no per-request window")
			}
			for _, s := range slots {
				if s.Context != res.Capabilities.Window {
					return nil, fmt.Errorf("slot %d has a context of %d, the probe found a window of %d", s.ID, s.Context, res.Capabilities.Window)
				}
			}
			if res.Capabilities.Counting != probe.CountChat {
				return nil, fmt.Errorf("the probe found counting %q, want chat (/apply-template and /tokenize)", res.Capabilities.Counting)
			}
			if res.Capabilities.CacheMetrics != probe.CacheReported || !res.Capabilities.SlotHints {
				return nil, fmt.Errorf("the probe found cache metrics %q and slot hints %v, want reported and true", res.Capabilities.CacheMetrics, res.Capabilities.SlotHints)
			}
			switch {
			case res.Slots == 1 && (res.KV != probe.Shared || res.KVTokens != res.Capabilities.Window):
				return nil, fmt.Errorf("one slot owns the whole cache: kv = %q with %d tokens, want shared with %d", res.KV, res.KVTokens, res.Capabilities.Window)
			case res.Slots > 1 && res.KV != probe.Unknown:
				return nil, fmt.Errorf("kv = %q for %d slots: llama.cpp does not say whether its cache is unified, so the probe may only answer unknown", res.KV, res.Slots)
			}
			if !strings.HasPrefix(res.Capabilities.Build, "b") {
				return nil, fmt.Errorf("build = %q, want a llama.cpp build such as b11942-338fce651", res.Capabilities.Build)
			}
			return nil, nil
		},
	}
}

// slotWire is the part of a /slots entry the scenario compares.
type slotWire struct {
	ID      int `json:"id"`
	Context int `json:"n_ctx"`
}

// readSlots lists the server's slots from /slots.
func readSlots(ctx context.Context, t *Target) ([]slotWire, error) {
	u, err := url.Parse(t.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("read slots: %w", err)
	}
	u.Path = strings.TrimSuffix(u.Path, "/v1") + "/slots"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("read slots: %w", err)
	}
	if t.Key != "" {
		req.Header.Set("Authorization", "Bearer "+t.Key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read slots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read slots: status %d (is --slots on?)", resp.StatusCode)
	}
	var slots []slotWire
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&slots); err != nil {
		return nil, fmt.Errorf("read slots: decode: %w", err)
	}
	return slots, nil
}
