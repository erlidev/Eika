// Package search finds pages on the web and in a few sources that rank their
// own content well: Wikipedia, arXiv, and GitHub.
//
// A web search goes to exactly one provider, the first in the configured
// order that has its key, quota to spare, and no cooldown: a self-hosted
// SearXNG by default, then the keyed Exa, Tavily, and Brave APIs, then
// Marginalia. A provider that fails is cooled down, doubling per consecutive
// failure, and the chain moves on. It is deliberately not a fan-out: every
// extra provider costs quota and buys little. A source search goes to its one
// backend. Results are cached, so a repeated query is free.
//
// The Engine is the entry point. Backends implement Searcher and register
// through Registry; the Tracker counts each bucket's use against its quota
// and persists the counts through a UsageStore. Everything the model reads
// is rendered in format.go, which is where a search's token cost is decided.
//
// Subpackages hold the backends (web, wikipedia, arxiv, github) and fetch,
// which reads one page as Markdown.
package search
