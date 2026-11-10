package openai

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/erlidev/eika/internal/provider"
)

// slotField is the request field llama.cpp reads the slot to run on from. Both
// of its routes read it: /v1/chat/completions and /v1/responses.
const slotField = "id_slot"

// attempt is one attempt at a request, naming the request's slot when withSlot
// is true.
type attempt func(ctx context.Context, req provider.Request, withSlot bool) (<-chan provider.Event, error)

// streamWithSlot makes the request with its slot hint when the endpoint takes
// one, and again without it if the server refuses the field, so that a hint is
// never the reason a call fails. A server that ignores the hint needs nothing:
// the request simply runs where the server puts it (llama.cpp reads a number
// past its slot count modulo that count). With no hint, or an endpoint that
// takes none, it is a plain attempt.
func streamWithSlot(ctx context.Context, req provider.Request, takesHint bool, try attempt) (<-chan provider.Event, error) {
	if req.SlotHint == nil || !takesHint {
		return try(ctx, req, false)
	}
	events, err := try(ctx, req, true)
	if err != nil {
		if slotRefused(err) {
			return try(ctx, req, false)
		}
		return nil, err
	}
	// A refusal arrives as the first event of a stream: the request fails
	// before any token. Everything after it is passed on as it comes.
	out := make(chan provider.Event)
	go func() {
		defer close(out)
		first, ok := <-events
		if !ok {
			return
		}
		if first.Kind == provider.KindError && slotRefused(first.Err) {
			retried, err := try(ctx, req, false)
			if err != nil {
				sendEvent(ctx, out, provider.Errorf(err))
				return
			}
			events = retried
			first, ok = <-events
			if !ok {
				return
			}
		}
		if !sendEvent(ctx, out, first) {
			return
		}
		for e := range events {
			if !sendEvent(ctx, out, e) {
				return
			}
		}
	}()
	return out, nil
}

// sendEvent delivers e unless ctx ends first, and reports whether it did.
func sendEvent(ctx context.Context, out chan<- provider.Event, e provider.Event) bool {
	select {
	case out <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// slotRefused reports whether err is a server refusing the request for the
// slot field it carried: a 400 or 422 whose message names id_slot. Any other
// failure, a 400 for another reason included, is not the hint's.
func slotRefused(err error) bool {
	var perr *provider.Error
	if !errors.As(err, &perr) {
		return false
	}
	if perr.StatusCode != http.StatusBadRequest && perr.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	return strings.Contains(err.Error(), slotField)
}
