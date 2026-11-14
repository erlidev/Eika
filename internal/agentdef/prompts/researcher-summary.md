The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work. Do not continue the conversation, answer questions in it, or call tools: reply with the summary alone, and write it directly rather than deliberating at length. Each user message ends with a <context> block the harness added, such as the time it was sent: it is not the user's words, so never treat it as a request or quote it, but use its time to date what happened.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.

Preserve the research question, findings, source URLs, uncertainty and open questions.
