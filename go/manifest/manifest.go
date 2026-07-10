// Package manifest computes and verifies the README-freshness manifest for a
// folder: a deterministic sha256 over the folder's direct-child source files'
// frontmatter name/description. This is the single source of the algorithm —
// every consumer (navigator README generator, per-repo freshness checks) must
// call this package rather than reimplement the hash. See MANIFEST-CONTRACT.md
// for the full contract this file implements.
package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// writeLengthPrefixed writes s to h as an 8-byte big-endian length prefix
// followed by s's raw bytes. Length-prefixing (not a delimiter byte) is what
// makes the per-entry preimage injective: given the length up front, a
// reader never has to scan the payload for a boundary marker, so no byte
// value the payload might contain — including 0x1f, "\n", or anything else —
// can be mistaken for one. Two distinct strings can never serialize to the
// same bytes this way, regardless of content.
func writeLengthPrefixed(h hash.Hash, s string) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
	h.Write(lenBuf[:])
	h.Write([]byte(s))
}

// Compute returns the lowercase-hex sha256 manifest digest for dir's
// canonically-sorted source-input set (see package doc / contract). The
// returned string is the bare hex digest — callers that need the
// "sha256:<hex>" presentation format it themselves (see FormatDigest).
func Compute(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("manifest: read dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "README.md" {
			continue // the folder's own README is never part of its own preimage
		}
		if strings.HasPrefix(name, ".") {
			continue // dotfiles excluded
		}
		names = append(names, name)
	}
	sort.Strings(names) // byte-order sort — Go string comparison is byte-wise

	h := sha256.New()
	for _, name := range names {
		fmName, fmDescription, err := extractFrontmatter(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("manifest: %s: %w", name, err)
		}
		// Each entry is three length-prefixed fields back-to-back: filename,
		// then frontmatter name, then frontmatter description. No delimiter
		// byte between fields or between entries — the lengths alone make
		// the whole preimage unambiguously decodable, so it's injective over
		// arbitrary byte content in any field (see writeLengthPrefixed).
		writeLengthPrefixed(h, name)
		writeLengthPrefixed(h, fmName)
		writeLengthPrefixed(h, fmDescription)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FormatDigest renders a bare hex digest in the "sha256:<hex>" presentation
// form used by the CLI and by the README `<!-- manifest: ... -->` marker.
func FormatDigest(hexDigest string) string {
	return "sha256:" + hexDigest
}

var frontmatterFieldRe = regexp.MustCompile(`^([A-Za-z0-9_]+):\s*(.*)$`)

// extractFrontmatter line-scans the leading `---` ... `---` block of a file
// (matching the convention scripts/check_privacy.py uses elsewhere in this
// repo) and returns its `name` and `description` scalar values. A file with
// no frontmatter block, or a field absent from the block, yields "" for that
// field — this function never errors on a missing/absent field.
//
// The block counts as frontmatter ONLY when the opening `---` has a matching
// closing `---` before EOF. An opening `---` with no closer is NOT a
// frontmatter block — the rest of the file is body text, so a body line
// shaped like `key: value` must never be captured as a real field. Without
// this check, an unterminated fence would line-scan the whole file and could
// forge a name/description from unrelated prose.
func extractFrontmatter(path string) (name string, description string, err error) {
	data, ferr := os.ReadFile(path)
	if ferr != nil {
		return "", "", fmt.Errorf("read: %w", ferr)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", "", nil // empty file, or first line isn't the opening delimiter — no frontmatter
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(strings.TrimRight(lines[i], "\r")) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return "", "", nil // opening fence never closes before EOF — not a frontmatter block
	}

	for i := 1; i < closeIdx; i++ {
		line := strings.TrimRight(lines[i], "\r")
		m := frontmatterFieldRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key, rawValue := m[1], m[2]
		switch key {
		case "name":
			name = parseScalar(rawValue)
		case "description":
			description = parseScalar(rawValue)
		}
	}
	return name, description, nil
}

// parseScalar strips a frontmatter value's surrounding whitespace and, when
// present, its surrounding double-quotes, unescaping any `\"` inside.
func parseScalar(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
		v = strings.ReplaceAll(v, `\"`, `"`)
	}
	return v
}

var readmeMarkerRe = regexp.MustCompile(`<!--\s*manifest:\s*sha256:([0-9a-f]{64})\s*-->`)

// ReadExpected reads dir's README.md and returns the bare hex digest recorded
// in its `<!-- manifest: sha256:<hex> -->` marker line. Returns an error if
// the README or the marker line is missing/malformed.
func ReadExpected(dir string) (string, error) {
	path := filepath.Join(dir, "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("manifest: read %s: %w", path, err)
	}
	m := readmeMarkerRe.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("manifest: %s: no <!-- manifest: sha256:... --> marker found", path)
	}
	return string(m[1]), nil
}
