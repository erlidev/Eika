package agent

import "github.com/erlidev/eika/internal/provider"

// ShouldCompact exposes shouldCompact to the external tests, which judge it
// by the prediction a counter makes.
func (a *Agent) ShouldCompact(p provider.Prediction) bool { return a.shouldCompact(p) }

// WidenedReserve exposes widenedReserve to the external tests.
var WidenedReserve = widenedReserve

// Transcript exposes the transcript form of a summary request to the external
// tests.
var Transcript = transcript
