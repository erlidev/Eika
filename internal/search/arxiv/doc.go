// Package arxiv is the search source for research papers. It queries the
// arXiv API and reads its Atom feed with encoding/xml. Natural text searches
// every field; a query in arXiv's own field syntax (ti:, au:, cat:) passes
// through unchanged.
//
// arXiv asks for one connection at a time and three seconds between request
// starts. The Engine applies that pacing, from search.Registry.
package arxiv
