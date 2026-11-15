# agents

The Agents tab of the settings, and the prompt editor it is built from. Eika,
the researcher and the worker are built in; the user changes each one's model
class and prompt, nothing else (the tools and limits are the definition's).
The harness holds an override per agent, and `GET /api/agents` returns the
built-in values beside the effective ones, so nothing here re-derives the rule.

- `AgentsSettings` is the tab: the list, and one agent's editor with a class
  choice, the prompt, Save, and Reset to built-in. An unset field runs as built.
- `PromptEditor` is a replaceable prompt: unset it shows the built-in text,
  Override starts from it, and a set prompt can be compared with the built-in
  one line by line (`DiffRows`).
- `fields.tsx` is what the fields are made of: the header whose chip says
  where the value comes from, the reset, and the segmented choice that can be
  left unset. The Context inspector uses the chip too. `form.ts` has the pure
  rules (`promptChange`, `layerName`).
- `queries.ts` holds the agent queries; saving refreshes every session's
  configuration and context preview, which resolve the agent at each run.

Test: `npm test -- src/features/agents`, and `e2e/specs/agents.spec.ts`
against the mock harness. The global instructions are `features/settings`'s
Instructions tab.
