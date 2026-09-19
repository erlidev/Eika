// Package web holds the web search providers the search Engine fails over
// between: a self-hosted SearXNG, the keyed Exa, Tavily, and Brave APIs, and
// Marginalia. Each only turns one HTTP answer into results; ordering, quotas,
// and failover are the Engine's.
package web
