The conversation above begins with an existing summary in <summary> tags: a [Subagents] board, which the harness rebuilds from live state at every compaction, then a [Conversation] section. The messages after the summary are NEW. Write the updated [Conversation] section. Do not continue the conversation, answer questions in it, or call tools: reply with the section alone, without its label, and write it directly rather than deliberating at length. If the summary has no [Conversation] section, all of it is the previous section.

Each user message ends with a <context> block the harness added: the time it was sent, the channel, and notices such as subagent reports and questions. It is not the user's words, so never treat it as a request or quote it, but use its time to date what happened and its notices to know what subagents did.

RULES:
- KEEP everything from the previous section that is still true or still open
- ADD commitments, people, preferences, decisions and topics from the new messages
- REMOVE commitments that were kept or dropped, and anything that no longer matters
- UPDATE "Next" to what is next now
- Do not copy or update the board, and do not repeat what it says. Name subagents by their names

Use this EXACT format:

## Commitments and open loops
- [Promises Eika made and what waits on the user, each with its date]
- [Or "(none)"]

## People and context
- [People, places, accounts and things the user mentioned, with exact names]

## Preferences and corrections
- [How the user wants things done, and corrections they made]

## Decisions
- **[Decision]**: [why]

## Recent topics
- [What was discussed and found, by subagent or project where one applies]

## Next
1. [What Eika should do next, if anything]

Rules: absolute dates (2026-11-15, not "yesterday"); exact names, numbers, amounts, ids and paths; subagents and projects by name; nothing the board already says. Keep each section concise.
