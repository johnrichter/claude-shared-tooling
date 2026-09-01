package githooks

import "testing"

// fixturePrivateKeyHeader is the private_key_block pattern's own literal
// header text, used bare (with no qualifying body) in the false-positive
// fixtures below.
const fixturePrivateKeyHeader = "-----BEGIN " + "RSA PRIVATE " + "KEY-----"

// TestScanSecretsPrivateKeyBlockStillFlagsRealShapedBody confirms the
// body-requiring pattern still catches a fabricated, real-shaped fake key: a
// header immediately followed by a 40+ character base64-alphabet body line,
// the same shape a real PEM's own first wrapped line takes.
func TestScanSecretsPrivateKeyBlockStillFlagsRealShapedBody(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leak.pem", fixturePEMKey+"\n-----END RSA PRIVATE KEY-----\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Path != "leak.pem" || got[0].Rule != "private_key_block" {
		t.Fatalf("got %+v, want one private_key_block finding at leak.pem", got)
	}
}

// TestScanSecretsPrivateKeyBlockIgnoresBareKeywordMention confirms a
// document that mentions "BEGIN PRIVATE KEY" as a bare keyword - e.g. inside
// a redaction-keyword list - with no key body at all, no longer flags.
func TestScanSecretsPrivateKeyBlockIgnoresBareKeywordMention(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/keywords.md", "Redaction keywords: password, "+fixturePrivateKeyHeader+", api key\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for a bare keyword mention with no key body", got)
	}
}

// TestScanSecretsPrivateKeyBlockIgnoresFillerPlaceholder confirms a template
// that follows the header with obvious filler text (too short to be a real
// key body) no longer flags.
func TestScanSecretsPrivateKeyBlockIgnoresFillerPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/template.md", fixturePrivateKeyHeader+"\nXXXXXXXX\n-----END RSA PRIVATE KEY-----\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for an obvious filler placeholder body", got)
	}
}

// TestScanSecretsPrivateKeyBlockIgnoresTruncatedExample confirms a
// documentation example that truncates the body after only a few characters
// (ending in "...", not base64-alphabet) no longer flags.
func TestScanSecretsPrivateKeyBlockIgnoresTruncatedExample(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "corpus/example.md", fixturePrivateKeyHeader+"\nMIIE...\n-----END RSA PRIVATE KEY-----\n")

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no findings for a truncated \"MIIE...\" example", got)
	}
}

// TestScanSecretsPrivateKeyBlockMatchesJSONLEscapedNewline confirms the
// pattern still matches when the header/body line break is represented as
// the two-character `\n` escape sequence, as it appears literally inside a
// JSON string value in a .jsonl corpus - the shape that caused the original
// false-positive incident's real key (had there been one) to still need
// catching.
func TestScanSecretsPrivateKeyBlockMatchesJSONLEscapedNewline(t *testing.T) {
	dir := t.TempDir()
	line := `{"text": "` + fixturePrivateKeyHeader + `\n` + fixturePEMKeyBody + `"}` + "\n"
	writeFile(t, dir, "corpus/chunks.jsonl", line)

	got, err := ScanSecrets(dir, DefaultSkipRules, nil, nil)
	if err != nil {
		t.Fatalf("ScanSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "private_key_block" {
		t.Fatalf("got %+v, want one private_key_block finding for a JSONL-escaped-newline key body", got)
	}
}
