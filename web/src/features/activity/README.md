# activity

The Activity page: Eika's subagents by state, and a read-only live view of each.
Steering goes through Eika in Chat, so the view has no composer. A waiting
question shows its reason and a read-only ask_user card with the question and
choices. Stored arguments keep the card readable after reload; Eika supplies
the answer with subagent_message.

- `ActivityPage.tsx` is the page for `/activity` and `/activity/:sessionId`. From
  1024px the board and the open subagent sit side by side; under it the route
  picks one, so a phone goes from the board to a subagent and back.
- `AgentBoard.tsx` lists the subagents in groups, in this order: Waiting,
  Running, Idle, Failed and cancelled; empty groups are hidden. A row is the
  state dot, name, agent, project, when it last moved, and one line: the reason
  it waits (in `warning`), else the first line of its report, else of its brief.
  A project filter appears when the subagents belong to more than one project.
- `AgentDetail.tsx` is the header (name, state, project, the waiting reason,
  Cancel), a line saying to steer through Eika in Chat, the brief folded to its
  first line, `features/session`'s `SessionView` with `readOnly`, and the report
  as a card once there is one. Cancel is
  `POST /api/sessions/{id}/cancel`, which stops the subagent's assignment through
  the lifecycle service; it is offered whenever the state is running or waiting.
- `state.ts` is the pure part: grouping, the order inside a group, the line, the
  state colours. `queries.ts` reads `GET /api/sessions` and keeps the subagents,
  and `GET /api/projects` for their names.

The board is current without polling: `session.updated` on `global` (and a
reconnect, or a `bus.dropped`) invalidates the session lists in
`features/sessions`'s `useSessionUpdates`, which the shell mounts once. The last
tool call of each subagent is not on the row: the wire has no such field, and
the view shows the calls as they stream.

Test it: `npm test -- activity` covers `state.ts`; `web/e2e/specs/activity.spec.ts`
drives the page on the `activity` scenario (every state and waiting reason, two
projects, runs to cancel) with phone and dark baselines.
