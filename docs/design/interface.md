# Interface: remaining work

The current web app and notifications are in
[Architecture](../ARCHITECTURE.md#frontend). The pages, Inbox, approvals and
notifications added by later phases are described in [UI](ui.md) and their
feature designs. Remote channels remain deferred.

## Future pages and notifications

- Activity adds routine runs and wakeups, with read-only live session views.
- Inbox holds approvals, tripwire entries, elicitations and pending memories,
  with a badge for unanswered items. Elicitation forms move into Inbox cards.
- Memory has its list, filters, search, editor and Instructions editor. Skills
  installs from a zip and displays the skill and its files. Both editors use
  `MarkdownField` and a plain textarea for files without frontmatter.
- Plugins retain connection, OAuth, tools and log screens and add per-tool
  policies; Schedules shows routines and wakeups. Settings adds grants,
  blocklist and other security controls. See [UI](ui.md#7-inbox),
  [Memory](memory.md#8-ui), [Plugins](skills-and-plugins.md#plugins) and
  [Scheduler](scheduler.md#6-tools-and-ui).
- Approvals notify at once; background replies deliver routine results as well
  as subagent reports. Halt also notifies. ntfy payloads stay minimal and link
  to the app for details.

## 6. Channels

Deferred (`DEFERRED.md`), first in line to return.

A channel is an adapter that moves messages between an external chat app and the
home session ([Architecture](../ARCHITECTURE.md#session-trees)):

```
type Channel interface {
  Name() string
  Run(ctx, deliver func(inbound Message)) error  // receive
  Send(ctx, out Message) error                   // reply or notify
}
```

The interface waits for its second implementation, per the style guide's rule on
abstractions. Web is the first, built into Core. Discord or Matrix is the second;
the user's Matrix-Bot project is a head start.

- Every channel feeds the one home session, as talking to the same person by text
  and in person is one relationship. Each message's `<context>` names its channel,
  replies go back where the message came from, and the web Chat shows all of it.
  Proactive messages go to the `notify_channel` setting.
- Only allowlisted user ids are accepted. Anything else is ignored and logged.
- Approvals, secret requests and takeovers are never done in a channel. The
  channel gets a link to the web Inbox instead, following Duet's rule that a
  sign-in form never arrives as a link, text or email.
- Channel messages are user input and trusted at the user's level, since the
  sender is allowlisted. Attachments from a channel are untrusted content.
- Deferred: voice (the user's Parakeet ASR and Chatterbox TTS servers are natural
  backends), and email to Eika's own address.

## 7. Testing

The mock harness gains Inbox states for approvals, elicitations and pending
memories, plus memories, skills and routines. Add phone baselines for Inbox
and test those interactions in touch emulation.
