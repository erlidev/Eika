package agent

import "github.com/erlidev/eika/internal/provider"

// ShouldCompact exposes shouldCompact to the external tests, which judge it
// by the prediction a counter makes.
func (a *Agent) ShouldCompact(p provider.Prediction) bool { return a.shouldCompact(p) }

// WidenedReserve exposes widenedReserve to the external tests.
var WidenedReserve = widenedReserve

// PrefillAllowance exposes prefillAllowance to the external tests.
var PrefillAllowance = prefillAllowance

// PrefillRate exposes the rate the agent scales its stall allowance by.
func (a *Agent) PrefillRate() int { return a.prefillRate() }

// Transcript exposes the transcript form of a summary request to the external
// tests.
var Transcript = transcript
