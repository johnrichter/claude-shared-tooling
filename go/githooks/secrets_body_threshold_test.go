package githooks

import (
	"strings"
	"testing"
)

// thresholdBodyChar is a base64-alphabet filler character used to build
// bodies of an exact length around the private_key_block pattern's {40,}
// threshold. Reusing a single repeated character keeps each test's intent
// (length, not content) unambiguous.
const thresholdBodyChar = "A"

// TestScanSecretsPrivateKeyBlockThresholdBoundary confirms the {40,}
// base64-alphabet-run threshold in the private_key_block pattern
// (go/githooks/secrets.go) is exact: a 39-character body does not flag, a
// 40-character body does, and a 41-character body does too. This is an
// independent, adversarial proof of the exact boundary the implementer
// chose - not inferred from reading the regex.
func TestScanSecretsPrivateKeyBlockThresholdBoundary(t *testing.T) {
	cases := []struct {
		name    string
		bodyLen int
		want    int
	}{
		{"39_chars_below_threshold_no_match", 39, 0},
		{"40_chars_at_threshold_matches", 40, 1},
		{"41_chars_above_threshold_matches", 41, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body := strings.Repeat(thresholdBodyChar, tc.bodyLen)
			writeFile(t, dir, "leak.pem", fixturePrivateKeyHeader+"\n"+body+"\n")

			got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
			if err != nil {
				t.Fatalf("ScanSecrets: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("body length %d: got %d findings, want %d (got=%+v)", tc.bodyLen, len(got), tc.want, got)
			}
		})
	}
}
