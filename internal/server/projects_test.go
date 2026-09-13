package server

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/erlidev/eika/internal/store"
)

// A remote URL is the one place a user can hand the harness a credential, so
// nothing that quotes it may pass the credential on.

func TestRemoteURLsAreReportedWithoutTheirCredentials(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		err    error
		remote string
		reason string
	}{
		{
			name:   "a plain url is reported as it is",
			raw:    "https://example.test/team/repo.git",
			err:    errors.New("fetch: not found"),
			remote: "https://example.test/team/repo.git",
			reason: "fetch: not found",
		},
		{
			name:   "a token in the userinfo is dropped",
			raw:    "https://ghp_secret@example.test/team/repo.git",
			err:    errors.New("fetch: not found"),
			remote: "https://example.test/team/repo.git",
			reason: "fetch: not found",
		},
		{
			name:   "a user and password are dropped",
			raw:    "https://user:ghp_secret@example.test/team/repo.git",
			err:    errors.New("fetch: denied"),
			remote: "https://example.test/team/repo.git",
			reason: "fetch: denied",
		},
		{
			name:   "git quoting the url back does not leak it either",
			raw:    "https://user:ghp_secret@example.test/team/repo.git",
			err:    errors.New("could not read from 'https://user:ghp_secret@example.test/team/repo.git'"),
			remote: "https://example.test/team/repo.git",
			reason: "could not read from 'https://example.test/team/repo.git'",
		},
		{
			name:   "a query and fragment are dropped",
			raw:    "https://example.test/team/repo.git?access_token=ghp_query#ghp_fragment",
			err:    errors.New("remote rejected token ghp_query from fragment ghp_fragment"),
			remote: "https://example.test/team/repo.git",
			reason: "remote rejected token [REDACTED] from fragment [REDACTED]",
		},
		{
			name:   "a url that does not parse is described, not printed",
			raw:    "https://user:pw@exa mple.test/\x7f",
			err:    errors.New("could not read from 'https://user:pw@exa mple.test/'"),
			remote: "[UNPARSEABLE]",
			reason: "the remote url could not be parsed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			remote, reason := withoutCredentials(c.raw, c.err)
			if remote != c.remote {
				t.Errorf("remote = %q, want %q", remote, c.remote)
			}
			if reason != c.reason {
				t.Errorf("reason = %q, want %q", reason, c.reason)
			}
			for _, secret := range []string{"ghp_secret", "ghp_query", "ghp_fragment", "pw@"} {
				if strings.Contains(remote, secret) || strings.Contains(reason, secret) {
					t.Errorf("%q leaked through %q / %q", secret, remote, reason)
				}
			}
		})
	}
}

func TestRemoteURLCredentialsDoNotReachLogs(t *testing.T) {
	raw := "https://user:ghp_userinfo@example.test/repo.git?token=ghp_query#ghp_fragment"
	remote, reason := withoutCredentials(raw, errors.New(
		"git rejected https://user:ghp_userinfo@example.test/repo.git?token=ghp_query#ghp_fragment: ghp_userinfo ghp_query ghp_fragment",
	))
	var output strings.Builder
	log := slog.New(slog.NewTextHandler(&output, nil))
	log.Error("mirror remote", "remote", remote, "error", reason)

	for _, secret := range []string{"ghp_userinfo", "ghp_query", "ghp_fragment"} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("log exposed %q: %s", secret, output.String())
		}
	}
}

func TestProjectResponsesRemoveLegacyURLCredentials(t *testing.T) {
	p := asProject(store.Project{
		ID:                "legacy",
		Name:              "legacy",
		Kind:              store.ProjectRemote,
		RemoteURL:         "https://user:secret@example.test/repo.git?token=query-secret#fragment-secret",
		RemoteUsernameEnv: "EIKA_GIT_USERNAME",
		RemotePasswordEnv: "EIKA_GIT_PASSWORD",
		DefaultBranch:     "main",
		CreatedAt:         time.Unix(1, 0),
	})
	if p.RemoteURL != "https://example.test/repo.git" {
		t.Errorf("remote_url = %q, want a URL without credential data", p.RemoteURL)
	}
}
