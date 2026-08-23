package githooks

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// writeFileMode is writeFile with a caller-chosen permission, for the
// executable-bit cases raw_binary_executable cares about.
func writeFileMode(t *testing.T, dir, rel, content string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	return path
}

// findingKey reduces a Finding to its (path, rule) pair - the part every
// case below asserts on; Detail's exact wording is not load-bearing.
func findingKey(f Finding) [2]string { return [2]string{f.Path, f.Rule} }

// TestScanRawBinaryExecutableRule table-drives every case the
// raw_binary_executable rule (SC12) must and must not fire on, plus the two
// invariants the rule must not disturb: the oversize raw_binary rule still
// fires on its own candidates, and a candidate that qualifies for both rules
// yields both findings.
func TestScanRawBinaryExecutableRule(t *testing.T) {
	nulPrefix := append([]byte{0x00}, bytes.Repeat([]byte{0xff}, 20)...)

	cases := []struct {
		name    string
		setup   func(t *testing.T, dir string) (candidate string, skipRules []fsx.Rule, maxBytes int64, lfsRouted LFSRouteChecker)
		want    [][2]string // (path, rule) pairs, in emission order
		wantErr bool
	}{
		{
			name: "small executable with a NUL byte is reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "bin/tool", string(nulPrefix), 0o755)
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: [][2]string{{"bin/tool", "raw_binary_executable"}},
		},
		{
			name: "small executable with no NUL byte is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "bin/tool", "#!/bin/sh\necho hi\n", 0o755)
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "non-executable small file with a NUL byte is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "data/blob", string(nulPrefix), 0o644)
				return "data/blob", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "mode-100755 candidate routed to LFS is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "bin/tool", string(nulPrefix), 0o755)
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, func(rel string) (bool, error) {
					return rel == "bin/tool", nil
				}
			},
			want: nil,
		},
		{
			name: "skip-ruled executable candidate is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "vendor/tool", string(nulPrefix), 0o755)
				return "vendor/tool", []fsx.Rule{{Pattern: "vendor/**", Class: SkipClass}}, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "NUL only after the 8000-byte sniff window is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				content := append(bytes.Repeat([]byte("a"), sniffBytes+10), 0x00)
				writeFileMode(t, dir, "bin/tool", string(content), 0o755)
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "empty executable file is not reported",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "bin/tool", "", 0o755)
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "an unreadable executable candidate errors rather than panicking",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				path := writeFileMode(t, dir, "bin/tool", string(nulPrefix), 0o111) // exec-only, no read bit
				t.Cleanup(func() { _ = os.Chmod(path, 0o755) })                     // let t.TempDir clean up
				return "bin/tool", DefaultSkipRules, DefaultMaxBytes, nil
			},
			wantErr: true,
		},
		{
			name: "a missing candidate is skipped, not an error",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				return "bin/gone", DefaultSkipRules, DefaultMaxBytes, nil
			},
			want: nil,
		},
		{
			name: "an oversize non-executable candidate still trips only raw_binary",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "assets/blob.bin", string(nulPrefix), 0o644)
				return "assets/blob.bin", DefaultSkipRules, 5, nil
			},
			want: [][2]string{{"assets/blob.bin", "raw_binary"}},
		},
		{
			name: "a candidate that is both oversize and a binary executable trips both rules",
			setup: func(t *testing.T, dir string) (string, []fsx.Rule, int64, LFSRouteChecker) {
				writeFileMode(t, dir, "bin/big-tool", string(nulPrefix), 0o755)
				return "bin/big-tool", DefaultSkipRules, 5, nil
			},
			want: [][2]string{{"bin/big-tool", "raw_binary"}, {"bin/big-tool", "raw_binary_executable"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			candidate, skipRules, maxBytes, lfsRouted := tc.setup(t, dir)

			got, err := ScanRawBinary(dir, []string{candidate}, skipRules, maxBytes, lfsRouted)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got nil error, want an error for %q", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScanRawBinary: %v", err)
			}
			gotKeys := make([][2]string, len(got))
			for i, f := range got {
				gotKeys[i] = findingKey(f)
			}
			if len(gotKeys) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if gotKeys[i] != tc.want[i] {
					t.Fatalf("got %+v, want %+v", got, tc.want)
				}
			}
		})
	}
}
