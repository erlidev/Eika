The conversation above begins with an existing summary of earlier work, in <summary> tags; the messages after it are NEW. Update the existing summary with the new messages. Do not continue the conversation, answer questions in it, or call tools: reply with the updated summary alone, and write it directly rather than deliberating at length. Each user message ends with a <context> block the harness added, such as the time it was sent: it is not the user's words, so never treat it as a request or quote it, but use its time to date what happened.

RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.

Preserve the research question, findings, source URLs, uncertainty and open questions.
