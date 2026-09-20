package fetch

import (
	"net/url"
	"testing"
)

func planOf(t *testing.T, raw string) plan {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return planFor(u)
}

func TestPlan(t *testing.T) {
	cases := []struct {
		url  string
		want plan
	}{
		{"https://github.com/tokio-rs/tokio",
			plan{kind: planGitHub, op: opReadme, owner: "tokio-rs", repo: "tokio", label: "tokio-rs/tokio README"}},
		{"https://github.com/tokio-rs/tokio/blob/master/README.md",
			plan{kind: planText, url: "https://raw.githubusercontent.com/tokio-rs/tokio/master/README.md"}},
		{"https://github.com/a/b/blob/main/src/lib.rs",
			plan{kind: planText, url: "https://raw.githubusercontent.com/a/b/main/src/lib.rs", lang: "rust"}},
		{"https://github.com/a/b/blob/main/x.py?plain=1#L10-L20",
			plan{kind: planText, url: "https://raw.githubusercontent.com/a/b/main/x.py", lang: "python"}},
		{"https://github.com/cli/cli/issues/42",
			plan{kind: planGitHub, op: opIssue, owner: "cli", repo: "cli", number: 42, label: "cli/cli#42"}},
		{"https://github.com/cli/cli/pull/42",
			plan{kind: planGitHub, op: opIssue, owner: "cli", repo: "cli", number: 42, label: "cli/cli#42"}},
		{"https://github.com/cli/cli/pull/42/files",
			plan{kind: planGitHub, op: opDiff, owner: "cli", repo: "cli", number: 42, label: "cli/cli#42 diff"}},
		{"https://github.com/a/b/tree/main/src/util",
			plan{kind: planGitHub, op: opTree, owner: "a", repo: "b", ref: "main", path: "src/util", label: "a/b tree"}},
		{"https://github.com/a/b/releases/tag/v1.2.0",
			plan{kind: planGitHub, op: opRelease, owner: "a", repo: "b", tag: "v1.2.0", label: "a/b v1.2.0"}},
		{"https://gist.github.com/someone/abc123def",
			plan{kind: planGitHub, op: opGist, gist: "abc123def", label: "gist abc123def"}},
		{"https://raw.githubusercontent.com/a/b/main/setup.py",
			plan{kind: planText, url: "https://raw.githubusercontent.com/a/b/main/setup.py", lang: "python"}},
		{"https://api.github.com/repos/a/b/contents/src/lib.rs?ref=next",
			plan{kind: planGitHub, op: opContent, apiPath: "/repos/a/b/contents/src/lib.rs?ref=next", lang: "rust", label: "src/lib.rs"}},
		{"https://example.com/spec.yaml", plan{kind: planText, url: "https://example.com/spec.yaml", lang: "yaml"}},
		{"https://example.com/CHANGELOG.md", plan{kind: planText, url: "https://example.com/CHANGELOG.md"}},
		{"https://github.com/features/copilot", plan{kind: planHTML, url: "https://github.com/features/copilot"}},
		{"https://github.com/pricing", plan{kind: planHTML, url: "https://github.com/pricing"}},
		{"https://github.com/orgs/nodejs/discussions", plan{kind: planHTML, url: "https://github.com/orgs/nodejs/discussions"}},
		{"https://github.com/cli/cli/issues/x", plan{kind: planHTML, url: "https://github.com/cli/cli/issues/x"}},
		{"https://docs.python.org/3/library/asyncio.html", plan{kind: planHTML, url: "https://docs.python.org/3/library/asyncio.html"}},
		{"https://example.com/guide/", plan{kind: planHTML, url: "https://example.com/guide/"}},
	}
	for _, c := range cases {
		if got := planOf(t, c.url); got != c.want {
			t.Errorf("planFor(%s)\n got %+v\nwant %+v", c.url, got, c.want)
		}
	}
}
