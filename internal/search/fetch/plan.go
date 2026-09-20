package fetch

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// A plan is decided from the URL alone, before any request goes out,
// because the cheapest way to read a GitHub file reliably is never to ask for
// its HTML. A blob page is a script-driven shell whose content is not in the
// served HTML; the raw file and the API's Markdown are strictly better.

// planKind is how a URL is read.
type planKind int

const (
	// planHTML parses the answer as HTML and extracts its main content.
	planHTML planKind = iota
	// planText takes the answer verbatim, fenced when lang is set.
	planText
	// planGitHub assembles the answer from GitHub API calls.
	planGitHub
)

// githubOp names a GitHub read.
type githubOp int

const (
	opReadme githubOp = iota
	opContent
	opTree
	opIssue
	opDiff
	opRelease
	opGist
)

// plan is how to read one URL.
type plan struct {
	kind planKind
	// url is what to request, for HTML and text.
	url string
	// lang is the fence language of a source file, empty for prose.
	lang string

	// The GitHub read, for planGitHub.
	op          githubOp
	owner, repo string
	ref, path   string
	number      int
	tag, gist   string
	// apiPath is a Contents API path, with its query.
	apiPath string
	// label names the read in the page's title.
	label string
}

// rawHost serves GitHub files as they are.
const rawHost = "raw.githubusercontent.com"

// extensions are the file types worth serving verbatim, mapped to their
// fence language. An empty language is prose, which is not fenced.
var extensions = map[string]string{
	"md": "", "markdown": "", "mdx": "", "txt": "", "text": "", "rst": "", "adoc": "",
	"c": "c", "cc": "cpp", "cfg": "ini", "cpp": "cpp", "cs": "csharp", "css": "css", "diff": "diff",
	"go": "go", "h": "c", "hpp": "cpp", "ini": "ini", "java": "java", "js": "javascript",
	"json": "json", "jsonc": "json", "kt": "kotlin", "lua": "lua", "mjs": "javascript",
	"patch": "diff", "php": "php", "pl": "perl", "proto": "protobuf", "py": "python", "pyi": "python",
	"r": "r", "rb": "ruby", "rs": "rust", "scala": "scala", "sh": "bash", "sql": "sql",
	"svelte": "svelte", "swift": "swift", "tf": "hcl", "toml": "toml", "ts": "typescript",
	"tsx": "tsx", "vue": "vue", "xml": "xml", "yaml": "yaml", "yml": "yaml", "zsh": "bash",
}

// reserved are github.com paths whose first segment is not an owner.
var reserved = map[string]bool{
	"about": true, "apps": true, "blog": true, "collections": true, "customer-stories": true,
	"enterprise": true, "events": true, "explore": true, "features": true, "marketplace": true,
	"notifications": true, "orgs": true, "pricing": true, "pulls": true, "readme": true,
	"security": true, "settings": true, "site": true, "sponsors": true, "topics": true, "trending": true,
}

// hexID matches a gist's id.
var hexID = regexp.MustCompile(`(?i)^[0-9a-f]+$`)

// planFor decides how to read u.
func planFor(u *url.URL) plan {
	raw := u.String()
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := segments(u.Path)

	switch host {
	case "api.github.com":
		if p, ok := planContents(u, parts); ok {
			return p
		}
	case "github.com":
		if p, ok := planRepository(parts); ok {
			return p
		}
	case "gist.github.com":
		if len(parts) > 0 && hexID.MatchString(parts[len(parts)-1]) {
			id := parts[len(parts)-1]
			return plan{kind: planGitHub, op: opGist, gist: id, label: "gist " + id}
		}
	case rawHost, "gist.githubusercontent.com":
		// A raw host serves exactly what it says it does.
		return plan{kind: planText, url: raw, lang: fenceLanguage(u.Path)}
	}
	if lang, ok := extensions[extension(u.Path)]; ok {
		return plan{kind: planText, url: raw, lang: lang}
	}
	return plan{kind: planHTML, url: raw}
}

// planContents reads a Contents API file URL as the file, not as its base64
// JSON envelope.
func planContents(u *url.URL, parts []string) (plan, bool) {
	if len(parts) < 5 || parts[0] != "repos" || parts[3] != "contents" {
		return plan{}, false
	}
	file := strings.Join(parts[4:], "/")
	apiPath := u.EscapedPath()
	if u.RawQuery != "" {
		apiPath += "?" + u.RawQuery
	}
	return plan{kind: planGitHub, op: opContent, apiPath: apiPath, lang: fenceLanguage(file), label: file}, true
}

// planRepository reads a github.com repository URL through the API or the
// raw host.
func planRepository(parts []string) (plan, bool) {
	if len(parts) < 2 || reserved[strings.ToLower(parts[0])] {
		return plan{}, false
	}
	owner, repo := parts[0], parts[1]
	slug := owner + "/" + strings.TrimSuffix(repo, ".git")
	base := plan{kind: planGitHub, owner: owner, repo: repo}
	if len(parts) == 2 {
		base.op, base.label = opReadme, slug+" README"
		return base, true
	}
	rest := parts[3:]
	switch parts[2] {
	case "blob", "raw":
		// A branch name with a slash reads as a path segment; the common
		// single-segment ref is assumed rather than asking the API.
		if len(rest) < 2 {
			return plan{}, false
		}
		file := strings.Join(rest[1:], "/")
		return plan{
			kind: planText,
			url:  "https://" + rawHost + "/" + owner + "/" + repo + "/" + rest[0] + "/" + file,
			lang: fenceLanguage(file),
		}, true
	case "tree":
		if len(rest) < 1 {
			return plan{}, false
		}
		base.op, base.ref, base.path, base.label = opTree, rest[0], strings.Join(rest[1:], "/"), slug+" tree"
		return base, true
	case "issues", "pull":
		if len(rest) < 1 {
			return plan{}, false
		}
		n, err := strconv.Atoi(rest[0])
		if err != nil || n <= 0 {
			return plan{}, false
		}
		base.number = n
		// The files and commits views ask for the change, not the discussion.
		if parts[2] == "pull" && len(rest) > 1 && (rest[1] == "files" || rest[1] == "commits") {
			base.op, base.label = opDiff, slug+"#"+rest[0]+" diff"
			return base, true
		}
		base.op, base.label = opIssue, slug+"#"+rest[0]
		return base, true
	case "releases":
		if len(rest) < 2 || rest[0] != "tag" {
			return plan{}, false
		}
		base.op, base.tag, base.label = opRelease, strings.Join(rest[1:], "/"), slug+" "+rest[1]
		return base, true
	}
	return plan{}, false
}

// segments splits a path into its non-empty segments.
func segments(path string) []string {
	var out []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// extension returns a path's lowercased file extension, empty for none.
func extension(path string) string {
	name := path[strings.LastIndex(path, "/")+1:]
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return ""
	}
	return strings.ToLower(name[dot+1:])
}

// fenceLanguage returns the fence language for a path: empty for prose and
// unknown extensions.
func fenceLanguage(path string) string { return extensions[extension(path)] }
