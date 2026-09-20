// Package filter runs a fetch filter: a JavaScript expression the model
// writes to pick what it needs out of a page before any of it enters the
// context window. It embeds the goja interpreter and is imported only by
// eikad, so a filter always runs inside the workspace sandbox, never in the
// harness.
//
// Run takes a page.FilterRequest and returns a page.FilterOutcome. The
// bindings are text, lines, sections, grep(re, ctx?), and code(lang?); a
// filter may be an expression or a function body, and its answer is cut to
// the content budget before it leaves the sandbox.
package filter
