package githooks

import (
	"os"
	"testing"
)

// realIncidentCorpusDir is the exact corpus directory (in the sibling
// marketplace-datadog repo, outside this worktree) whose chunks.jsonl caused
// the original private_key_block false-positive incident: 5 lines naming
// "BEGIN PRIVATE KEY" as a bare keyword or showing a filler/truncated
// placeholder, no real key material, all flagged under the old header-only
// pattern. Read-only: this test never writes into that path.
const realIncidentCorpusDir = "/home/bits/Development/workspaces/psa-platform/marketplace-datadog/plugins/knowledge-agents/datadog-docs-knowledge-agent/corpus"

// TestScanSecretsRealIncidentCorpusNoLongerFlags is an independent,
// end-to-end re-run of the strengthened private_key_block pattern against
// the actual corpus file that caused the incident, not a synthetic stand-in
// for it. Skips if the sibling repo/file is not present in the current
// environment (e.g. CI checkout without the sibling repo), rather than
// failing the whole suite on an environment precondition.
func TestScanSecretsRealIncidentCorpusNoLongerFlags(t *testing.T) {
	if _, err := os.Stat(realIncidentCorpusDir + "/chunks.jsonl"); err != nil {
		t.Skipf("real incident corpus not present in this environment: %v", err)
	}

	got, err := ScanSecrets(realIncidentCorpusDir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	for _, f := range got {
		if f.Rule == "private_key_block" {
			t.Fatalf("got a private_key_block finding against the real incident corpus: %+v, want none", f)
		}
	}
}
