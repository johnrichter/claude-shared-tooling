package githooks

import (
	"os"
	"testing"
)

// defaultRealIncidentCorpusDir is the corpus directory (in a sibling repo,
// outside this module) whose chunks.jsonl caused the original
// private_key_block false-positive incident. Of its 5 lines naming "BEGIN
// PRIVATE KEY", 3 matched the old header-only pattern - one showing a
// truncated placeholder body, two an obvious filler body - and no line
// carries real key material; the other 2 are bare keyword-list mentions with
// no header delimiters, which never matched either pattern. This absolute
// path is specific to the machine the incident happened on, so
// realIncidentCorpusEnv overrides it and an absent corpus skips.
const defaultRealIncidentCorpusDir = "/home/bits/Development/workspaces/psa-platform/marketplace-datadog/plugins/knowledge-agents/datadog-docs-knowledge-agent/corpus"

// realIncidentCorpusEnv names the environment variable that points this test
// at the incident corpus on another checkout or machine.
const realIncidentCorpusEnv = "GITHOOKS_INCIDENT_CORPUS_DIR"

// realIncidentCorpusDir resolves the corpus directory to scan: the override
// if set, else the machine-specific default.
func realIncidentCorpusDir() string {
	if dir := os.Getenv(realIncidentCorpusEnv); dir != "" {
		return dir
	}
	return defaultRealIncidentCorpusDir
}

// TestScanSecretsRealIncidentCorpusNoLongerFlags is an independent,
// end-to-end re-run of the strengthened private_key_block pattern against
// the actual corpus file that caused the incident, not a synthetic stand-in
// for it. Skips if the sibling repo/file is not present in the current
// environment (e.g. CI checkout without the sibling repo), rather than
// failing the whole suite on an environment precondition.
func TestScanSecretsRealIncidentCorpusNoLongerFlags(t *testing.T) {
	dir := realIncidentCorpusDir()
	if _, err := os.Stat(dir + "/chunks.jsonl"); err != nil {
		t.Skipf("real incident corpus not present in this environment: %v", err)
	}

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	for _, f := range got {
		if f.Rule == "private_key_block" {
			t.Fatalf("got a private_key_block finding against the real incident corpus: %+v, want none", f)
		}
	}
}
