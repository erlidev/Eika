// Package fetch reads one URL for the model as Markdown.
//
// A URL is planned before any request goes out: GitHub repository, blob,
// tree, issue, pull request, release, and gist URLs resolve through the API
// or the raw host, source files and prose files are read verbatim, and
// everything else is HTML. HTML is reduced to its content by the container a
// documentation generator marks, else a readability heuristic, else the
// body, and converted to Markdown with golang.org/x/net/html.
//
// Reader is the entry point. A read picks a section by heading, or runs the
// model's filter through a FilterFunc, which web_fetch runs inside the
// sandbox, and fits what is left to the content budget from search/page.
// Pages are cached, so a retried filter costs no download. Every request
// goes through NewClient, which refuses to connect to a non-public address.
package fetch
