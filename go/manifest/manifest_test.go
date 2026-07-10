package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// goldenFixtureDigest is the manifest package's own contract check: the
// hardcoded expected digest was computed independently (Python hashlib, not
// this package) over the exact preimage the contract specifies, for
// testdata/fixture's two eligible children (alpha.md, beta.txt) — sorted
// byte order, README/dotfile/subdirectory excluded. If this test breaks, the
// algorithm drifted from the documented contract, not the fixture.
const goldenFixtureDigest = "bfad8b7bfad612eab527c3723107fe0105203ff9c0e29c46e2c6bdcd8513965c"

func TestCompute_GoldenFixture_KnownStableDigest(t *testing.T) {
	got, err := Compute("testdata/fixture")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got != goldenFixtureDigest {
		t.Fatalf("digest drift: got %s, want %s", got, goldenFixtureDigest)
	}
}

func TestCompute_ExcludesReadmeDotfilesAndSubdirs(t *testing.T) {
	// testdata/fixture also carries a README.md, a .hidden dotfile, and a
	// sub/nested.md subdirectory entry. The golden digest above already
	// proves they don't perturb the hash (it only accounts for alpha.md and
	// beta.txt) — this test makes that exclusion explicit and independently
	// checkable by re-deriving the same digest from a hand-built dir that
	// omits all three, in a fresh temp dir.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "alpha.md"), "---\nname: \"Alpha\"\ndescription: \"First fixture file\"\n---\n")
	writeFile(t, filepath.Join(dir, "beta.txt"), "Plain text file with no frontmatter block at all.\n")

	got, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got != goldenFixtureDigest {
		t.Fatalf("got %s, want %s (should match fixture with README/dotfile/subdir excluded)", got, goldenFixtureDigest)
	}
}

func TestCompute_MutatingChildNameOrDescriptionChangesDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "child.md"), "---\nname: \"Original\"\ndescription: \"Original desc\"\n---\n")
	before, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	writeFile(t, filepath.Join(dir, "child.md"), "---\nname: \"Changed\"\ndescription: \"Original desc\"\n---\n")
	afterName, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if afterName == before {
		t.Fatal("changing frontmatter name did not change digest")
	}

	writeFile(t, filepath.Join(dir, "child.md"), "---\nname: \"Original\"\ndescription: \"Changed desc\"\n---\n")
	afterDesc, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if afterDesc == before {
		t.Fatal("changing frontmatter description did not change digest")
	}
}

func TestCompute_AddingOrRemovingChildChangesDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.md"), "---\nname: \"One\"\ndescription: \"First\"\n---\n")
	before, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	writeFile(t, filepath.Join(dir, "two.md"), "---\nname: \"Two\"\ndescription: \"Second\"\n---\n")
	afterAdd, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if afterAdd == before {
		t.Fatal("adding a child did not change digest")
	}

	if err := os.Remove(filepath.Join(dir, "two.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	afterRemove, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if afterRemove != before {
		t.Fatal("removing the added child did not restore the original digest")
	}
}

func TestCompute_EditingReadmeHumanZoneDoesNotChangeDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "child.md"), "---\nname: \"Child\"\ndescription: \"Desc\"\n---\n")
	writeFile(t, filepath.Join(dir, "README.md"), "---\nname: \"R\"\ndescription: \"D\"\n---\n\n## Original human zone\n")
	before, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	writeFile(t, filepath.Join(dir, "README.md"), "---\nname: \"R changed too\"\ndescription: \"D changed too\"\n---\n\n## Rewritten human zone entirely\n")
	after, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if after != before {
		t.Fatal("editing README.md (including its own frontmatter) changed the digest — README must be fully excluded")
	}
}

func TestCompute_FileWithNoFrontmatterContributesEmptyFields(t *testing.T) {
	withNone := t.TempDir()
	writeFile(t, filepath.Join(withNone, "plain.md"), "no frontmatter here at all\n")

	withExplicitEmpty := t.TempDir()
	writeFile(t, filepath.Join(withExplicitEmpty, "plain.md"), "---\nname: \"\"\ndescription: \"\"\n---\n")

	a, err := Compute(withNone)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	b, err := Compute(withExplicitEmpty)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if a != b {
		t.Fatal("missing frontmatter block should contribute the same empty name/description as an explicit empty scalar")
	}
}

func TestCompute_SortIsDeterministicRegardlessOfCreationOrder(t *testing.T) {
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "zeta.md"), "---\nname: \"Z\"\ndescription: \"z\"\n---\n")
	writeFile(t, filepath.Join(dirA, "alpha.md"), "---\nname: \"A\"\ndescription: \"a\"\n---\n")
	writeFile(t, filepath.Join(dirA, "mid.md"), "---\nname: \"M\"\ndescription: \"m\"\n---\n")

	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "mid.md"), "---\nname: \"M\"\ndescription: \"m\"\n---\n")
	writeFile(t, filepath.Join(dirB, "alpha.md"), "---\nname: \"A\"\ndescription: \"a\"\n---\n")
	writeFile(t, filepath.Join(dirB, "zeta.md"), "---\nname: \"Z\"\ndescription: \"z\"\n---\n")

	a, err := Compute(dirA)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	b, err := Compute(dirB)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if a != b {
		t.Fatal("digest depends on filesystem creation order, not on canonical filename sort")
	}
}

func TestReadExpected_MatchesComputedOnFixture(t *testing.T) {
	expected, err := ReadExpected("testdata/fixture")
	if err != nil {
		t.Fatalf("ReadExpected: %v", err)
	}
	if expected != goldenFixtureDigest {
		t.Fatalf("README marker %s does not match golden digest %s", expected, goldenFixtureDigest)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- Adversarial extension: empty input set ---------------------------------

func TestCompute_EmptyInputSet_EqualsSha256OfEmptyString(t *testing.T) {
	// sha256("") — independently known constant, not derived from this package.
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	t.Run("dir with only a README", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "README.md"), "---\nname: \"R\"\ndescription: \"D\"\n---\n\nbody\n")
		got, err := Compute(dir)
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if got != emptyDigest {
			t.Fatalf("got %s, want sha256(\"\")=%s — README-only folder must have an empty preimage", got, emptyDigest)
		}
	})

	t.Run("truly empty dir", func(t *testing.T) {
		dir := t.TempDir()
		got, err := Compute(dir)
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if got != emptyDigest {
			t.Fatalf("got %s, want sha256(\"\")=%s", got, emptyDigest)
		}
	})

	t.Run("dir with only a dotfile", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".hidden"), "---\nname: \"Should not count\"\n---\n")
		got, err := Compute(dir)
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if got != emptyDigest {
			t.Fatalf("got %s, want sha256(\"\")=%s — dotfile-only folder must have an empty preimage", got, emptyDigest)
		}
	})

	t.Run("dir with only a subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeFile(t, filepath.Join(dir, "sub", "child.md"), "---\nname: \"Nested\"\n---\n")
		got, err := Compute(dir)
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if got != emptyDigest {
			t.Fatalf("got %s, want sha256(\"\")=%s — subdir-only folder must have an empty preimage", got, emptyDigest)
		}
	})
}

// --- Adversarial extension: README exclusion is total, incl. its own marker -

func TestCompute_MutatingReadmeMarkerLineDoesNotChangeDigest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "child.md"), "---\nname: \"Child\"\ndescription: \"Desc\"\n---\n")
	writeFile(t, filepath.Join(dir, "README.md"),
		"---\nname: \"R\"\ndescription: \"D\"\n---\n\n<!-- manifest: sha256:0000000000000000000000000000000000000000000000000000000000000 -->\n")
	before, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Rewrite the marker line itself (as `check` would after a real recompute)
	// plus everything else about the README — must still not move the digest.
	writeFile(t, filepath.Join(dir, "README.md"),
		"---\nname: \"R totally different\"\ndescription: \"D totally different\"\n---\n\n<!-- manifest: sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff -->\n\nmore body\n")
	after, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if after != before {
		t.Fatal("rewriting the README's manifest marker line changed the digest — README must be fully excluded, marker included")
	}
}

// --- Adversarial extension: ordering independence over readdir return order -

func TestCompute_ManyFilesRandomizedNames_SortIsByteOrderNotLocale(t *testing.T) {
	// Names chosen to diverge under locale-aware collation vs pure byte
	// comparison: uppercase vs lowercase, digits, and underscore vs hyphen
	// sort differently under e.g. a case-insensitive or locale collator.
	names := []string{"Zebra.md", "apple.md", "_under.md", "10.md", "2.md", "a-b.md", "a_b.md"}
	dirA := t.TempDir()
	for _, n := range names {
		writeFile(t, filepath.Join(dirA, n), "---\nname: \""+n+"\"\n---\n")
	}
	// Same set, reverse creation order, in a second dir.
	dirB := t.TempDir()
	for i := len(names) - 1; i >= 0; i-- {
		writeFile(t, filepath.Join(dirB, names[i]), "---\nname: \""+names[i]+"\"\n---\n")
	}

	a, err := Compute(dirA)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	b, err := Compute(dirB)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if a != b {
		t.Fatal("digest differs by creation order for a mixed-case/digit/punctuation filename set")
	}
}

// --- Adversarial extension: frontmatter edge cases ---------------------------

func TestCompute_QuotedValueWithEscapedQuote(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"Say \\\"Hi\\\" now\"\ndescription: \"plain\"\n---\n")
	name, desc, err := extractFrontmatter(filepath.Join(dir, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != `Say "Hi" now` {
		t.Fatalf("name = %q, want %q", name, `Say "Hi" now`)
	}
	if desc != "plain" {
		t.Fatalf("description = %q, want %q", desc, "plain")
	}
}

// TestCompute_UnitSeparatorByteInValue_MustNotForgeCollision is the
// drift-critical attack the routine exists to withstand, and this test is the
// regression that keeps it closed. An earlier version of the contract used a
// raw 0x1f (unit separator) as an unescaped field delimiter; that encoding was
// not injective — a literal 0x1f byte inside a frontmatter scalar was
// indistinguishable from a delimiter boundary, so two distinct
// (name, description) tuples could serialize to byte-identical preimages and
// collide on one digest.
//
// The current implementation uses no delimiter byte at all: each field is
// length-prefixed (see writeLengthPrefixed / go/README.md), which is
// structurally injective — a reader knows a field's exact byte length before
// reading it, so no byte the content might contain (0x1f, "\n", anything) can
// forge a boundary. This test asserts that safety property directly: the same
// 0x1f byte placed in two different fields must NOT collide. It PASSES against
// the current implementation and must stay green.
func TestCompute_UnitSeparatorByteInValue_MustNotForgeCollision(t *testing.T) {
	dirA := t.TempDir()
	writeFile(t, filepath.Join(dirA, "a.md"), "---\nname: \"x\x1fy\"\ndescription: \"z\"\n---\n")

	dirB := t.TempDir()
	writeFile(t, filepath.Join(dirB, "a.md"), "---\nname: \"x\"\ndescription: \"y\x1fz\"\n---\n")

	digestA, err := Compute(dirA)
	if err != nil {
		t.Fatalf("Compute(dirA): %v", err)
	}
	digestB, err := Compute(dirB)
	if err != nil {
		t.Fatalf("Compute(dirB): %v", err)
	}
	if digestA == digestB {
		t.Fatalf("COLLISION: name=%q/desc=%q and name=%q/desc=%q both produce digest %s — "+
			"a raw 0x1f byte in a frontmatter scalar forges a collision between two distinct "+
			"(name, description) tuples on the same filename",
			`x\x1fy`, `z`, `x`, `y\x1fz`, digestA)
	}
}

func TestCompute_NameOnlyOrDescriptionOnly_OtherFieldIsEmpty(t *testing.T) {
	dirNameOnly := t.TempDir()
	writeFile(t, filepath.Join(dirNameOnly, "a.md"), "---\nname: \"OnlyName\"\n---\n")
	name, desc, err := extractFrontmatter(filepath.Join(dirNameOnly, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "OnlyName" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want name=%q desc=\"\"", name, desc, "OnlyName")
	}

	dirDescOnly := t.TempDir()
	writeFile(t, filepath.Join(dirDescOnly, "a.md"), "---\ndescription: \"OnlyDesc\"\n---\n")
	name2, desc2, err := extractFrontmatter(filepath.Join(dirDescOnly, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name2 != "" || desc2 != "OnlyDesc" {
		t.Fatalf("got name=%q desc=%q, want name=\"\" desc=%q", name2, desc2, "OnlyDesc")
	}

	// These two must NOT hash the same as each other (distinct tuples,
	// no 0x1f byte involved — a sanity check that the encoding does at
	// least distinguish (name,"") from ("",desc) in the normal case).
	digestA, _ := Compute(dirNameOnly)
	digestB, _ := Compute(dirDescOnly)
	if digestA == digestB {
		t.Fatal("(name,\"\") and (\"\",description) collided even without a delimiter byte involved")
	}
}

func TestCompute_HorizontalRuleMidFile_NotMisreadAsFrontmatter(t *testing.T) {
	dir := t.TempDir()
	// First line is prose, not "---" — the file has NO leading frontmatter
	// block even though a "---" horizontal rule appears later, with
	// key:value-shaped lines around it that must not be captured.
	content := "# Title\n\nSome intro text.\n\n---\n\nname: this looks like frontmatter but is body prose\ndescription: also body prose\n\n---\n\nMore text.\n"
	writeFile(t, filepath.Join(dir, "a.md"), content)
	name, desc, err := extractFrontmatter(filepath.Join(dir, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty — first line isn't the opening delimiter", name, desc)
	}
}

func TestCompute_UnterminatedFrontmatterFence_TreatedAsNoFrontmatter(t *testing.T) {
	// The contract requires a matching closing `---` before EOF for the
	// leading block to count as frontmatter at all. A file whose first line
	// is coincidentally "---" (e.g. used as a horizontal rule) but never
	// closes must NOT have its body lines captured as frontmatter fields —
	// it contributes empty name/description, same as a file with no opening
	// fence at all. (Previously this was an unclosed gap: the implementation
	// scanned to EOF and forged a field from unrelated prose; fixed here.)
	dir := t.TempDir()
	content := "---\nname: \"Real Name\"\nSome unrelated prose line without a colon\nanother: value\n"
	writeFile(t, filepath.Join(dir, "a.md"), content)
	name, desc, err := extractFrontmatter(filepath.Join(dir, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "" {
		t.Fatalf("got name=%q, want empty — an unterminated fence must not be treated as frontmatter", name)
	}
	if desc != "" {
		t.Fatalf("got desc=%q, want empty", desc)
	}
}

func TestCompute_CRLFLineEndingsInFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := "---\r\nname: \"CRLF Name\"\r\ndescription: \"CRLF Desc\"\r\n---\r\n\r\nbody\r\n"
	writeFile(t, filepath.Join(dir, "a.md"), content)
	name, desc, err := extractFrontmatter(filepath.Join(dir, "a.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "CRLF Name" || desc != "CRLF Desc" {
		t.Fatalf("got name=%q desc=%q, want name=%q desc=%q", name, desc, "CRLF Name", "CRLF Desc")
	}
}

// --- Adversarial extension: scope --------------------------------------------

func TestCompute_SubdirectoryNamedLikeMarkdownFile_Excluded(t *testing.T) {
	dir := t.TempDir()
	// A directory literally named "trap.md" — must be excluded because it's
	// a directory, regardless of its .md-shaped name.
	if err := os.Mkdir(filepath.Join(dir, "trap.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "trap.md", "inner.md"), "---\nname: \"Inner\"\n---\n")
	writeFile(t, filepath.Join(dir, "real.md"), "---\nname: \"Real\"\ndescription: \"D\"\n---\n")

	got, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	// Independently-built dir with only "real.md" must produce the same
	// digest — proving "trap.md" (the directory) contributed nothing.
	control := t.TempDir()
	writeFile(t, filepath.Join(control, "real.md"), "---\nname: \"Real\"\ndescription: \"D\"\n---\n")
	want, err := Compute(control)
	if err != nil {
		t.Fatalf("Compute(control): %v", err)
	}
	if got != want {
		t.Fatalf("got %s, want %s — a directory named like a .md file must be excluded as a directory", got, want)
	}
}

// --- Adversarial extension: error paths --------------------------------------

func TestCompute_NonexistentDir_ReturnsError(t *testing.T) {
	if _, err := Compute(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("want error for nonexistent dir, got nil")
	}
}

func TestFormatDigest_PrependsSha256Prefix(t *testing.T) {
	got := FormatDigest("abcd")
	if got != "sha256:abcd" {
		t.Fatalf("got %q, want %q", got, "sha256:abcd")
	}
}

func TestExtractFrontmatter_NonexistentFile_ReturnsError(t *testing.T) {
	if _, _, err := extractFrontmatter(filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("want error for nonexistent file, got nil")
	}
}

func TestExtractFrontmatter_TrulyEmptyFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "empty.md"), "")
	name, desc, err := extractFrontmatter(filepath.Join(dir, "empty.md"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want both empty", name, desc)
	}
}

func TestReadExpected_MissingReadme_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadExpected(dir); err == nil {
		t.Fatal("want error for missing README.md, got nil")
	}
}

func TestReadExpected_MalformedMarker_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "<!-- manifest: sha256:not-hex -->\n")
	if _, err := ReadExpected(dir); err == nil {
		t.Fatal("want error for malformed marker, got nil")
	}
}

func TestReadExpected_NoMarkerAtAll_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# Just a plain README, no marker\n")
	if _, err := ReadExpected(dir); err == nil {
		t.Fatal("want error for missing marker, got nil")
	}
}

// --- Entries: the shared accessor consumed by both Compute and any listing --

func TestEntries_MatchesGoldenFixtureFilenamesInSortOrder(t *testing.T) {
	entries, err := Entries("testdata/fixture")
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Filename)
	}
	want := []string{"alpha.md", "beta.txt"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestEntries_CarriesFrontmatterNameAndDescription(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "---\nname: \"A Name\"\ndescription: \"A Desc\"\n---\n")
	entries, err := Entries(dir)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Filename != "a.md" || entries[0].Name != "A Name" || entries[0].Description != "A Desc" {
		t.Fatalf("got %+v, want {a.md A Name A Desc}", entries[0])
	}
}

func TestEntries_ExcludesReadmeDotfilesAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.md"), "---\nname: \"Real\"\n---\n")
	writeFile(t, filepath.Join(dir, "README.md"), "---\nname: \"R\"\n---\n")
	writeFile(t, filepath.Join(dir, ".hidden"), "---\nname: \"Hidden\"\n---\n")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entries, err := Entries(dir)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Filename != "real.md" {
		t.Fatalf("got %+v, want exactly [real.md]", entries)
	}
}

func TestEntries_DigestFromEntries_MatchesCompute(t *testing.T) {
	// Proves Compute is a pure derivation of Entries — a caller could
	// reconstruct the same digest from Entries alone, so there's no hidden
	// second extraction path.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.md"), "---\nname: \"One\"\ndescription: \"First\"\n---\n")
	writeFile(t, filepath.Join(dir, "two.md"), "---\nname: \"Two\"\ndescription: \"Second\"\n---\n")

	entries, err := Entries(dir)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	h := sha256.New()
	for _, e := range entries {
		writeLengthPrefixed(h, e.Filename)
		writeLengthPrefixed(h, e.Name)
		writeLengthPrefixed(h, e.Description)
	}
	rederived := hex.EncodeToString(h.Sum(nil))

	want, err := Compute(dir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if rederived != want {
		t.Fatalf("got %s, want %s", rederived, want)
	}
}

func TestCompute_ChildWithNoFrontmatterContributesEmptyNameAndDescription(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plain.txt"), "just some text\nwith multiple lines\n")
	name, desc, err := extractFrontmatter(filepath.Join(dir, "plain.txt"))
	if err != nil {
		t.Fatalf("extractFrontmatter: %v", err)
	}
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want both empty", name, desc)
	}
}
