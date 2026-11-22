package scan

import (
	"strings"
	"testing"
)

func TestFindUsesAllCredentialPatterns(t *testing.T) {
	if len(secretPrefixes) != len(secrets) {
		t.Fatalf("secretPrefixes has %d entries, secrets has %d", len(secretPrefixes), len(secrets))
	}
	samples := []string{
		"-----BEGIN PRIVATE KEY-----x-----END PRIVATE KEY-----",
		"sk-ant-" + strings.Repeat("a", 20),
		"sk-proj-" + strings.Repeat("a", 32),
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("a", 50),
		"AKIA" + strings.Repeat("A", 16),
		"AIza" + strings.Repeat("a", 35),
		"xoxp-" + strings.Repeat("a", 10),
		"rk_live_" + strings.Repeat("a", 20),
		"npm_" + strings.Repeat("a", 36),
		"eyJ" + strings.Repeat("a", 10) + ".eyJ" + strings.Repeat("a", 10) + "." + strings.Repeat("a", 10),
	}
	for _, sample := range samples {
		if Find(sample) == "" || Redact(sample) == sample {
			t.Errorf("credential was not found and redacted: %s", sample)
		}
	}
}
