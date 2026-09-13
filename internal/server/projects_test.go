package server

import (
	"errors"
	"strings"
	"testing"
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
			for _, secret := range []string{"ghp_secret", "pw@"} {
				if strings.Contains(remote, secret) || strings.Contains(reason, secret) {
					t.Errorf("%q leaked through %q / %q", secret, remote, reason)
				}
			}
		})
	}
}
