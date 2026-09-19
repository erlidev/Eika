// Package page is the Markdown page model fetch reads through: a page split
// into sections on its headings, a section picked out by a heading the model
// read, and the token budget that turns an oversized page into an outline or
// a truncated read with a note of what was left out.
//
// It is pure: no network, no clock. The sandboxed filter uses it too, so the
// sections a filter sees are the ones fetch selects from.
package page
