package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Probe: sub-second boundary precision — one second before/after the 90-day
// line must land on opposite sides.
func TestProbeCurrencyBoundarySubSecond(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	justInside := now.Add(-currencyWindow + time.Second)  // 1s inside window -> warn
	justOutside := now.Add(-currencyWindow - time.Second) // 1s past window -> error
	cat := mustCatalog(t,
		relEntry{name: "v3.1.0", published: now.Format(time.RFC3339)},
		relEntry{name: "v3.0.1", published: justInside.Format(time.RFC3339)},
		relEntry{name: "v3.0.2", published: justOutside.Format(time.RFC3339)},
	)
	if v := evaluateCurrency(templateRef{version: "v3.0.1"}, cat, "", now); v.kind != verdictWarn {
		t.Errorf("1s inside window: kind=%d, want warn", v.kind)
	}
	if v := evaluateCurrency(templateRef{version: "v3.0.2"}, cat, "", now); v.kind != verdictError {
		t.Errorf("1s past window: kind=%d, want error", v.kind)
	}
}

// Probe: no bare-version tag at all in the catalog -> silent no_tag even for a
// version-named ref, never a crash or a false current.
func TestProbeCurrencyNoBareTagsAtAll(t *testing.T) {
	cat := mustCatalog(t, relEntry{name: "go/toolchain/v0.3.0", published: time.Now().Format(time.RFC3339)})
	v := evaluateCurrency(templateRef{version: "v3.0.0"}, cat, "", time.Now())
	if v.kind != verdictSilent || v.reason != reasonNoTag {
		t.Errorf("catalog with no bare tags: verdict=%+v, want silent no_tag", v)
	}
}

// Probe: a run: step body containing a banned command only as a substring of a
// longer, unrelated token must not false-positive (word-boundary check).
func TestProbeBannedCommandWordBoundary(t *testing.T) {
	if m := bannedCommandMatch("echo gofmt-checker-tool-name"); m != "gofmt" {
		// bannedCommandSet uses \b...\b so "gofmt" inside "gofmt-checker" still
		// matches at a word boundary (- is a non-word char). Document actual
		// behavior rather than assume.
		t.Logf("bannedCommandMatch on hyphenated token = %q", m)
	}
	if m := bannedCommandMatch("echo mygofmtrunner"); m != "" {
		t.Errorf("bannedCommandMatch matched %q inside a non-boundary token %q", m, "mygofmtrunner")
	}
}

// Probe: enumeration must never pick up files under .git even when a file
// inside .git happens to be named like a shipped body. git ls-files never lists
// its own metadata directory, so reading the tracked tree gives this for free.
func TestProbeEnumerationSkipsDotGit(t *testing.T) {
	root := t.TempDir()
	gitInitAdd(t, root) // an empty repository; .git now exists
	p := filepath.Join(root, ".git", "sneaky.github-workflow.yml")
	if err := os.WriteFile(p, []byte("on: push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateWorkflowBodies(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("enumeration picked up a body inside .git: %v", got)
	}
}

// Probe: a malformed LANGUAGE_TOOLS_RELEASES_FILE (invalid JSON) must be an
// infrastructure error, not a silent verdict swallowed by the run.
func TestProbeReleasesFileMalformedIsError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(file, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(releasesFileEnv, file)
	_, _, err := resolveReleaseSource().catalog(context.Background())
	if err == nil {
		t.Error("malformed releases file produced no error; WFRULES clause (e) requires infra failure, not silence")
	}
}

// Probe: a caller uses: line for an official (non-template) action must never
// be classified as a template ref, regardless of a version-tag-looking @ref.
func TestProbeOfficialActionNotTemplateRef(t *testing.T) {
	if _, ok := parseTemplateRef("actions/checkout@v4.0.0"); ok {
		t.Error("actions/checkout@v4.0.0 classified as a template ref; official actions must stay outside fleet rule two")
	}
}
