# context

The Context panel and the Context inspector: what a model request sends,
part by part. The harness assembles the request (`GET
/api/sessions/{id}/context` for the next one, `GET
/api/sessions/{id}/requests/{request_id}` for a recorded one) with the same
code a run uses; this feature only draws it.

- `ContextPanel` is the summary in the context pane: the request select (the
  next request, live, or any recorded call), the context window's fill, the
  stacked token bar and its parts, and the key parameters. A part, or
  Inspect, opens the inspector on it.
- `ContextInspector` is the large dialog: the parts in a column, and the
  chosen one whole beside it: the system prompt section by section, the
  context files, the tools with their parameter tables or raw schemas, every
  message with its size, and the parameters with the layer each came from.
  A part of the next request has an Edit link to the setting behind it, and
  the request copies as JSON.
- `parts.tsx` holds what both draw: the window meter, the parts bar, the
  colour dots, and the Edit link, which opens the profile's editor when the
  profile sets the value and the session's otherwise.
- `segments.ts` is the pure side: the parts, the scale to a measured call,
  the tools by where they come from, a schema's parameters, the window's
  fill, the request as JSON, and the setting behind each part.
- `queries.ts` holds the queries and the request selection both share; the
  session stream invalidates them when a turn ends.

Test: `npm test -- src/features/context`, and `e2e/specs/context.spec.ts`
against the mock harness.
