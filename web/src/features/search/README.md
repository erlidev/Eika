# search

What the web UI knows about web search: the status of every search backend,
the stored API keys, and the rules behind the search settings. The Search
tab that uses them is `features/settings/SearchSettings.tsx`, because the
settings dialog owns its tabs.

- `queries.ts` wraps `/api/search/status`, `/api/search/keys/{name}`, and
  `POST /api/search`, the settings page's "try a search".
- `search.ts` holds the pure rules: reading the provider order and quotas
  from the settings table with the harness's defaults, reordering and
  toggling providers, rendering usage against a quota, and checking a quota
  field (`limitProblem`): a whole number, or empty for unlimited, never
  anything read as unlimited by accident.

How a search runs, the failover chain and its cooldowns, is the harness's:
see `internal/search`. The session renderers for `web_search` and
`web_fetch` are in `features/session/renderers`.

Test it: `search.test.ts` covers the rules. The tab is a form over three
queries; check it with `make dev`.
