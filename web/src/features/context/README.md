# context

The Context panel of the context pane: what a model request sends, part by
part. The harness assembles the request (`GET /api/sessions/{id}/context`
for the next one, `GET /api/sessions/{id}/requests/{request_id}` for a
recorded one) with the same code a run uses; this feature only draws it.

- `ContextPanel` has the request select (the next request, live, or any
  recorded call), the stacked token bar whose segments open their part,
  the system prompt's sections, the tool schemas raw or pretty-printed, the
  messages, and the parameters with the layer each came from.
- `segments.ts` splits a request into the bar's parts and scales the
  estimate to a call the endpoint measured.
- `queries.ts` holds the three queries; the session stream invalidates them
  when a turn ends.

Test: `npm test -- src/features/context`, and `e2e/specs/context.spec.ts`
against the mock harness.
