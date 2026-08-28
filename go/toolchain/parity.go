package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// RegenerateMatrix rebuilds the pair set from MATRIX (matrixSpec, section
// 4.7's grid) alone: for each language it walks the checks the grid names,
// expanding CheckTest into the subcommands testKindsByLanguage lists, and
// returns every pair's canonical PairID sorted. It reads none of the
// committed table, so comparing its result to the committed pairs
// (VerifyMatrixParity) catches a pair dropped from, or invented in, that
// table. It has no side effect.
func RegenerateMatrix() []string {
	var ids []string
	for language, checks := range matrixSpec {
		for _, check := range checks {
			if check == CheckTest {
				for _, kind := range testKindsByLanguage[language] {
					ids = append(ids, MatrixEntry{Language: language, Check: check, Test: kind}.PairID())
				}
				continue
			}
			ids = append(ids, MatrixEntry{Language: language, Check: check}.PairID())
		}
	}
	sort.Strings(ids)
	return ids
}

// committedPairIDs returns every committed-table pair's PairID, sorted, so it
// compares against RegenerateMatrix on the same footing.
func committedPairIDs() []string {
	ids := make([]string, len(committedMatrix))
	for i, e := range committedMatrix {
		ids[i] = e.PairID()
	}
	sort.Strings(ids)
	return ids
}

// MatrixParity is the outcome of comparing the committed dispatch table
// against the pair set regenerated from MATRIX.
type MatrixParity struct {
	// Match is true when the two sets are byte-for-byte identical.
	Match bool
	// Regenerated is the newline-joined sorted PairIDs MATRIX produces.
	Regenerated string
	// Committed is the newline-joined sorted PairIDs the committed table
	// holds.
	Committed string
}

// VerifyMatrixParity reports whether the committed dispatch table and the
// pair set RegenerateMatrix produces are byte-for-byte identical. A mismatch
// means the committed table drifted from MATRIX in one direction or the
// other — a pair dropped from the table, or one invented in it — which is the
// drift the REPRO check exists to catch. It runs nothing and has no side
// effect.
func VerifyMatrixParity() MatrixParity {
	regenerated := strings.Join(RegenerateMatrix(), "\n")
	committed := strings.Join(committedPairIDs(), "\n")
	return MatrixParity{
		Match:       regenerated == committed,
		Regenerated: regenerated,
		Committed:   committed,
	}
}

// BinaryParity is the outcome of comparing a committed build artifact
// against a fresh build from its current sources.
type BinaryParity struct {
	// Match is true when Committed and Fresh are identical.
	Match bool
	// Committed is the sha256 of the artifact already on disk at binaryPath.
	Committed string
	// Fresh is the sha256 of the artifact a fresh build just produced.
	Fresh string
}

// VerifyBinaryParity hashes the committed binary at binaryPath, runs
// buildCmd in buildDir to produce a fresh one at outputPath, and reports
// whether the two are byte-identical. A mismatch means the committed
// artifact is stale against its current sources, or the build toolchain
// drifted — exactly the drift a mandatory post-merge compile check exists
// to catch before anything downstream trusts the committed binary.
func VerifyBinaryParity(ctx context.Context, binaryPath string, buildCmd []string, buildDir, outputPath string) (*BinaryParity, error) {
	if len(buildCmd) == 0 {
		return nil, fmt.Errorf("toolchain: buildCmd must not be empty")
	}
	committed, err := fileHash(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("toolchain: committed binary %s: %w", binaryPath, err)
	}
	res, err := sysops.Run(ctx, buildCmd[0], buildCmd[1:], sysops.Options{Dir: buildDir, Timeout: DefaultTimeout})
	if err != nil {
		return nil, fmt.Errorf("toolchain: fresh build %v: %w", buildCmd, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("toolchain: fresh build %v exited %d: %s", buildCmd, res.ExitCode, res.Stderr)
	}
	fresh, err := fileHash(outputPath)
	if err != nil {
		return nil, fmt.Errorf("toolchain: fresh build output %s: %w", outputPath, err)
	}
	return &BinaryParity{Match: committed == fresh, Committed: committed, Fresh: fresh}, nil
}

// fileHash returns the lowercase hex sha256 digest of path's bytes.
func fileHash(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
