# context

The request inspector shows the next request or a recorded call by part.
`ContextInspector` opens the full request; `useRequestSelection` loads it.
`segments.ts` calculates token sizes and drift. `Inspection` and `Pane` show
sections, tools, messages and parameters. The request can be copied as JSON.

Test the accounting with `npm test -- segments` and inspect the Developer tab
with the `profiles` and `context-estimated` shot scenarios.
