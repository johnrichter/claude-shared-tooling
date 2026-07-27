package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

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
