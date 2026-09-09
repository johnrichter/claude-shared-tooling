package githooks

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/fsx"
)

// betterleaksBaseConfig is a curated, vendored copy of betterleaks' own
// default configuration (data/betterleaks-base.toml - see that file's own
// header comment for exact provenance, version, and the hand-reviewed
// additions on top of the pristine upstream file). This is this package's
// compiled-in, immutable base config: never read from, or overridable by, a
// scanned repo's own config, exactly like secrets.go's own hardcoded
// secretPatterns/exactExemptions - just now sourced from a real upstream
// project instead of authored from scratch. See buildBetterleaksConfig for
// how a caller's own additive rules/allowlist entries are layered on top of
// this without ever weakening it.
//
//go:embed data/betterleaks-base.toml
var betterleaksBaseConfig []byte

// BetterleaksRule is a caller-supplied additional secret-detection rule
// (the "secret-scanner rework" plan's §3 user-additive layer), expressed
// directly in betterleaks' own rule shape: a stable rule id and the regex
// that flags it. A caller passing this can only ever ADD a new detection
// rule on top of the compiled-in base config - there is no way, through this
// parameter, to remove or weaken a base-config rule.
type BetterleaksRule struct {
	ID    string
	Regex string
}

// BetterleaksAllowlistEntry is a caller-supplied additional exemption (the
// same §3 user-additive layer): RuleID names which base or extra rule this
// narrows ("" or "*" applies across every rule, matching betterleaks' own
// allowlist semantics for an entry with no targetRules). Exactly one of
// Value (an exact secret value, matched literally - never compiled as a
// pattern) or Regex (a secret-value regex, used verbatim) must be set. Like
// BetterleaksRule, this can only ever ADD an exemption on top of the
// compiled-in base config - never remove or weaken one.
type BetterleaksAllowlistEntry struct {
	RuleID string
	Value  string
	Regex  string
}

// BetterleaksOptions parameterizes ScanCredentials beyond the target root
// and the resolved betterleaks binary path.
type BetterleaksOptions struct {
	// SkipRules is the injected fsx.ClassifyPath ruleset for directories/
	// paths never scanned at all - the same convention every other scan in
	// this package uses (see ScanSecrets, ScanPrivacy).
	SkipRules []fsx.Rule
	// ExtraRules are a caller's own additional detection rules (§3),
	// appended to the compiled-in base config's rule set. Nil means no
	// additions: every base-config rule still applies.
	ExtraRules []BetterleaksRule
	// ExtraAllowlist are a caller's own additional exemptions (§3), appended
	// to the compiled-in base config's exemptions. Nil means no additions:
	// every base-config allowlist entry still applies, and nothing else is
	// exempted.
	ExtraAllowlist []BetterleaksAllowlistEntry
	// CacheDir, if set, turns on the content-addressed scan cache (see
	// BetterleaksCache): a file whose path, content, effective merged
	// config, and scanning binary all match a previous scan reuses that
	// scan's verdict instead of re-invoking betterleaks. Empty (the default)
	// means no caching at all - every existing caller that never sets this
	// field sees byte-for-byte the same behavior as before this option
	// existed.
	//
	// A cache entry is trusted on read, so write access to CacheDir is write
	// access to this scanner's verdicts: anyone who can plant an entry can
	// suppress a finding. Point this only at a directory under the same
	// trust boundary as the scan itself.
	CacheDir string
}

// betterleaksBatchSize caps how many file paths ScanCredentials passes to a
// single betterleaks invocation (see runBetterleaksBatch's doc comment for
// why individual file paths, never a directory, are what gets passed at
// all). This bounds the subprocess's argv length well under any OS's
// ARG_MAX for a large tree, at the cost of more than one invocation for a
// tree with more files than this. A var, not a const, solely so a test can
// lower it to exercise the multi-batch merge path without creating hundreds
// of fixture files.
var betterleaksBatchSize = 500

// betterleaksFinding is the subset of betterleaks' own JSON report-format
// fields ScanCredentials needs, confirmed directly against a live v1.8.1
// run's actual output shape this session (RuleID, Description, Secret, File
// are exactly the field names and casing betterleaks emits).
type betterleaksFinding struct {
	RuleID      string `json:"RuleID"`
	Description string `json:"Description"`
	Secret      string `json:"Secret"`
	File        string `json:"File"`
}

// tomlRawStringSafe reports whether s can safely be embedded inside a TOML
// literal multi-line string (delimited by three single quotes on each side):
// such a string is never escape-processed, so its only forbidden content is
// the delimiter sequence itself.
func tomlRawStringSafe(s string) bool {
	return !strings.Contains(s, "'''")
}

// buildBetterleaksConfig appends extraRules and extraAllowlist onto the
// compiled-in base config's text and returns the merged TOML, ready to write
// to a temp file and pass via --config. This is pure text concatenation, not
// a structural parse-and-reserialize of the 461-rule base config: betterleaks'
// own TOML schema is additive by construction for both a new [[rules]] block
// and a new [[allowlists]] block (confirmed via live testing this session -
// an [[allowlists]] entry with targetRules scopes an exemption to one named
// rule; omitting targetRules scopes it globally, exactly matching this
// function's RuleID == "" / "*" case), so appending well-formed new blocks at
// the end of the file is sufficient and carries none of the round-trip
// data-loss risk a full parse-modify-reserialize of an unfamiliar 461-rule
// file would risk (rules carry embedded validate scripts, raw regex
// delimiters, and other fields this package has no need to understand).
//
// Every extraRules/extraAllowlist entry is appended, never used to modify an
// existing block - this is what makes the merge purely additive: a caller
// can only ever add a new rule or a new exemption, never remove or weaken a
// compiled-in one (the "secret-scanner rework" plan's §3).
func buildBetterleaksConfig(extraRules []BetterleaksRule, extraAllowlist []BetterleaksAllowlistEntry) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(betterleaksBaseConfig)

	for _, r := range extraRules {
		if r.ID == "" || r.Regex == "" {
			return nil, fmt.Errorf("githooks: BetterleaksRule requires both ID and Regex")
		}
		if !tomlRawStringSafe(r.Regex) {
			return nil, fmt.Errorf("githooks: BetterleaksRule %q: Regex must not contain %s", r.ID, "'''")
		}
		fmt.Fprintf(&buf, "\n[[rules]]\nid = %s\nregex = '''%s'''\n", strconv.Quote(r.ID), r.Regex)
	}

	for _, a := range extraAllowlist {
		if (a.Value == "") == (a.Regex == "") {
			return nil, fmt.Errorf("githooks: BetterleaksAllowlistEntry (RuleID %q) must set exactly one of Value or Regex", a.RuleID)
		}
		regex := a.Regex
		if a.Value != "" {
			regex = "^" + regexp.QuoteMeta(a.Value) + "$"
		}
		if !tomlRawStringSafe(regex) {
			return nil, fmt.Errorf("githooks: BetterleaksAllowlistEntry (RuleID %q): exemption must not contain %s", a.RuleID, "'''")
		}
		buf.WriteString("\n[[allowlists]]\nregexTarget = \"secret\"\n")
		if a.RuleID != "" && a.RuleID != "*" {
			fmt.Fprintf(&buf, "targetRules = [%s]\n", strconv.Quote(a.RuleID))
		}
		fmt.Fprintf(&buf, "regexes = ['''%s''']\n", regex)
	}

	return buf.Bytes(), nil
}

// betterleaksEnvPrefixes are the environment-variable name prefixes
// filteredEnviron strips before every betterleaks invocation - defense in
// depth for §1b item 2, applied even though --config already makes every
// variable under these prefixes unreachable via betterleaks' own documented
// precedence order.
var betterleaksEnvPrefixes = []string{"BETTERLEAKS_", "GITLEAKS_"}

// filteredEnviron returns the current process's environment with every
// variable named under betterleaksEnvPrefixes removed.
func filteredEnviron() []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		var drop bool
		for _, prefix := range betterleaksEnvPrefixes {
			if strings.HasPrefix(kv, prefix) {
				drop = true
				break
			}
		}
		if !drop {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

// runBetterleaksBatch invokes betterleaksPath once, scanning exactly the
// files named in batch (paths relative to root, root itself set as the
// subprocess's working directory), and returns betterleaks' own parsed JSON
// findings for that invocation.
//
// # Closing betterleaks' implicit-config bypass surfaces
//
// Betterleaks (like gitleaks, its predecessor) reads several implicit
// config sources of its own. Left open, any one of these would let anyone
// with write access to a scanned repo - a human, or an agent looking for a
// way around a blocked commit - weaken or disable the compiled-in base
// ruleset entirely, defeating this whole package's "no way for end users to
// change our default rules" design goal. This list, verified directly
// against betterleaks' actual v1.8.1 source AND, for two items, against its
// actual live runtime behavior (this session ran the real v1.8.1 binary
// against planted fixtures for every vector below - static source reading
// alone missed one of these, see item 3), is deliberately recorded here so
// a future betterleaks upgrade that changes this precedence is a reviewed
// decision made once, in the one place every caller of this function
// shares, not a silent regression a second consumer could reintroduce
// independently:
//
//  1. ".betterleaks.toml"/".gitleaks.toml" auto-discovered in the scanned
//     directory - a full config REPLACEMENT, not merely additive, if
//     --config is not explicitly passed (confirmed via source and via a
//     live run: an empty planted .betterleaks.toml, scanned with no
//     --config flag, suppressed a real, planted finding entirely). Closed
//     by always passing --config <our merged temp file>, never omitted.
//  2. BETTERLEAKS_CONFIG/GITLEAKS_CONFIG (a config file path) and
//     BETTERLEAKS_CONFIG_TOML/GITLEAKS_CONFIG_TOML (an inline TOML config
//     string) environment variables - confirmed via a live run
//     (BETTERLEAKS_CONFIG_TOML set to an empty ruleset, with no --config
//     flag, suppressed a real finding the same way) that these are
//     unreachable once --config is explicitly set. Closed the same way,
//     PLUS filteredEnviron strips all "BETTERLEAKS_"/"GITLEAKS_"-prefixed
//     variables from this subprocess's environment as defense in depth -
//     never relying on upstream's current precedence order alone.
//  3. ".betterleaksignore"/".gitleaksignore" auto-discovery. The plan this
//     package implements assumed (from static source reading) that this is
//     controlled entirely by --gitleaks-ignore-path (default "."), closable
//     by always pointing that flag at a fresh, empty temp directory. LIVE
//     TESTING THIS SESSION DISPROVED THAT: betterleaks v1.8.1 ADDITIONALLY,
//     unconditionally checks the immediate top level of EVERY directory
//     passed as a positional scan-target argument for one of these files -
//     regardless of what --gitleaks-ignore-path names, and regardless of
//     the process's own working directory. Confirmed by direct experiment:
//     a .betterleaksignore file placed at the top of a scanned directory
//     argument suppressed a real, planted finding even with
//     --gitleaks-ignore-path pointed at a verified-empty, unrelated
//     directory; the same file one level deeper (a subdirectory discovered
//     by betterleaks' own recursion, never itself passed as a positional
//     argument) was NOT honored; passing the leaf FILE itself as the
//     positional argument (instead of its containing directory) also
//     closed it. Because any directory ever handed to betterleaks as a
//     positional argument gets this treatment - including the scanned
//     root - the only fully verified-safe mitigation is this function's own
//     departure from a naive reading of the flag docs: it NEVER passes a
//     directory to betterleaks, only individual file paths (see
//     ScanCredentials, which walks root itself and hands this function one
//     batch of plain file paths at a time). --gitleaks-ignore-path is still
//     always pointed at a fresh, empty temp directory too, closing the
//     documented (cwd-default) half of this surface as well.
//  4. Inline "// gitleaks:allow" / "// betterleaks:allow" source comments -
//     honored by default. Confirmed via a live run (a real, planted secret
//     with a trailing "// gitleaks:allow" comment was suppressed with no
//     flag set, and still flagged with the flag below set). Closed by
//     always passing --ignore-gitleaks-allow=true.
//  5. --baseline-path (a file of previously-accepted findings to suppress) -
//     confirmed opt-in only (empty default, never auto-discovered). Closed
//     simply by never passing it.
//
// # Exit-code and stdout handling
//
// Betterleaks exits 1 both when leaks are found (valid JSON on stdout) AND
// on a fatal startup error such as an unreadable --config path (nothing on
// stdout) - confirmed via a live run. So this function never inspects the
// process exit code to decide success; it decides purely on whether stdout
// parses as valid JSON (including the literal "null" betterleaks emits for
// zero findings).
func runBetterleaksBatch(betterleaksPath, root, configPath, ignoreDir string, batch []string) ([]betterleaksFinding, error) {
	args := []string{
		"dir",
		"--config", configPath,
		"--gitleaks-ignore-path", ignoreDir,
		"--ignore-gitleaks-allow=true",
		"--no-banner",
		"--report-format", "json",
		"--report-path", "-",
	}
	args = append(args, batch...)

	cmd := exec.Command(betterleaksPath, args...)
	cmd.Dir = root
	cmd.Env = filteredEnviron()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var findings []betterleaksFinding
	if jsonErr := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &findings); jsonErr != nil {
		return nil, fmt.Errorf("githooks: betterleaks invocation failed (run error: %v): %s", runErr, strings.TrimSpace(stderr.String()))
	}
	return findings, nil
}

// categoryForRuleID derives a Finding's Category from a betterleaks rule id:
// every rule this package itself added to the compiled-in base config for
// PII/financial detection carries a "pii-"/"financial-" prefix (see the
// "Locally authored additions" block at the end of data/betterleaks-base.toml)
// specifically so this function can recover the right category from the rule
// id alone, with no separate id-to-category table to keep in sync. Every
// other rule id - the full pristine upstream betterleaks catalog - has
// neither prefix and categorizes as "credentials".
//
// That prefix convention is the entire coupling between this function and
// that data file, and getting it wrong on either side mis-buckets a finding
// silently rather than failing: the finding is still reported, just under the
// wrong category, so nothing errors. The two sides are pinned against each
// other by
// TestCategoryForRuleIDMatchesBaseConfigPIIFinancialRules,
// so that stays impossible to do by accident.
func categoryForRuleID(ruleID string) string {
	switch {
	case strings.HasPrefix(ruleID, "pii-"):
		return "pii"
	case strings.HasPrefix(ruleID, "financial-"):
		return "financial"
	default:
		return "credentials"
	}
}

// betterleaksFindingsToFindings converts betterleaks' own raw finding shape
// into this package's Finding, dropping a JWT-shaped finding that matches a
// known jwt.io demo payload/secret (see isKnownDemoJWT) before it is ever
// constructed.
func betterleaksFindingsToFindings(raw []betterleaksFinding) []Finding {
	var findings []Finding
	for _, f := range raw {
		if f.RuleID == "jwt" && isKnownDemoJWT(f.Secret) {
			continue
		}
		findings = append(findings, Finding{
			Path:     filepath.ToSlash(f.File),
			Rule:     f.RuleID,
			Detail:   f.Description,
			Category: categoryForRuleID(f.RuleID),
		})
	}
	return findings
}

// ScanCredentials runs betterleaksPath - the caller's own resolved, already-
// verified absolute path to a betterleaks binary; this function never
// discovers or provisions that path itself, so any caller (git-tools, a CI
// script, another repo entirely) can resolve it however fits its own
// environment - over every file under root not excluded by opts.SkipRules,
// returning one Finding (Category "credentials", "pii", or "financial" - see
// categoryForRuleID) per non-exempt secret betterleaks reports.
//
// The compiled-in base config (see betterleaksBaseConfig) plus opts.
// ExtraRules/opts.ExtraAllowlist (see buildBetterleaksConfig) are merged
// into one temp config file, passed to betterleaks via --config, and never
// otherwise reachable by the scanned repo's own config (see
// runBetterleaksBatch's doc comment for the full list of implicit-config
// surfaces this closes). A JWT-shaped finding (betterleaks' own "jwt" rule)
// matching jwt.io's own canonical demo payload, or signed with jwt.io's own
// published demo HMAC secret, is dropped before it ever becomes a Finding
// (see isKnownDemoJWT) - every other finding, JWT-shaped or not, passes
// through unexempted.
//
// A tree with more scannable files than betterleaksBatchSize is scanned in
// more than one betterleaks invocation, sharing the same merged config and
// ignore directory; results are merged. A root with zero scannable files
// returns (nil, nil) without invoking betterleaks at all.
//
// opts.CacheDir, if set, turns on BetterleaksCache: each candidate file whose
// path, content, merged config, and betterleaksPath binary all match a prior
// scan's cache entry reuses that verdict instead of a real invocation, and
// every batch-scanned file (including one with zero findings) gets a fresh
// cache entry written afterward. A candidate that is not a plain regular
// file is scanned but never cached (see hashScannableFile), so turning the
// cache on never fails a scan that would otherwise have succeeded. Findings
// are returned in the same, candidate-order-derived arrangement regardless
// of how many were served from cache versus freshly scanned.
func ScanCredentials(root, betterleaksPath string, opts BetterleaksOptions) ([]Finding, error) {
	if betterleaksPath == "" {
		return nil, fmt.Errorf("githooks: ScanCredentials: betterleaksPath must not be empty")
	}

	var files []string
	if err := walkScannable(root, opts.SkipRules, func(rel, abs string) error {
		files = append(files, rel)
		return nil
	}); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	mergedConfig, err := buildBetterleaksConfig(opts.ExtraRules, opts.ExtraAllowlist)
	if err != nil {
		return nil, err
	}

	configFile, err := os.CreateTemp("", "githooks-betterleaks-config-*.toml")
	if err != nil {
		return nil, fmt.Errorf("githooks: creating betterleaks config temp file: %w", err)
	}
	defer os.Remove(configFile.Name())
	if _, err := configFile.Write(mergedConfig); err != nil {
		configFile.Close()
		return nil, fmt.Errorf("githooks: writing betterleaks config temp file: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return nil, fmt.Errorf("githooks: closing betterleaks config temp file: %w", err)
	}

	ignoreDir, err := os.MkdirTemp("", "githooks-betterleaks-ignore-*")
	if err != nil {
		return nil, fmt.Errorf("githooks: creating betterleaks ignore-path temp dir: %w", err)
	}
	defer os.RemoveAll(ignoreDir)

	cache := BetterleaksCache{Dir: opts.CacheDir}
	if cache.Dir == "" {
		var raw []betterleaksFinding
		for i := 0; i < len(files); i += betterleaksBatchSize {
			end := min(i+betterleaksBatchSize, len(files))
			batchFindings, err := runBetterleaksBatch(betterleaksPath, root, configFile.Name(), ignoreDir, files[i:end])
			if err != nil {
				return nil, err
			}
			raw = append(raw, batchFindings...)
		}
		return betterleaksFindingsToFindings(raw), nil
	}

	// Caching is on: the merged config and the betterleaks binary are each
	// hashed once here, not per file - see BetterleaksCache's own doc
	// comment for why a composite key over these two plus each file's own
	// content is exactly what a scan verdict depends on.
	configHash := sha256.Sum256(mergedConfig)
	binaryHash, err := hashFile(betterleaksPath)
	if err != nil {
		return nil, fmt.Errorf("githooks: hashing betterleaks binary %s: %w", betterleaksPath, err)
	}

	keyForFile := make(map[string]string, len(files))
	cachedForFile := make(map[string][]cachedFinding, len(files))
	var missFiles []string
	for _, rel := range files {
		fileHash, ok := hashScannableFile(filepath.Join(root, rel))
		if !ok {
			// Not hashable as a plain regular file, so it has no cache key
			// (see hashScannableFile): scan it, and write nothing back.
			missFiles = append(missFiles, rel)
			continue
		}
		key := cacheKey(sha256.Sum256([]byte(rel)), fileHash, configHash, binaryHash)
		keyForFile[rel] = key

		if hit, ok, _ := cache.Get(key); ok {
			cachedForFile[rel] = hit
			continue
		}
		missFiles = append(missFiles, rel)
	}

	var raw []betterleaksFinding
	for i := 0; i < len(missFiles); i += betterleaksBatchSize {
		end := min(i+betterleaksBatchSize, len(missFiles))
		batchFindings, err := runBetterleaksBatch(betterleaksPath, root, configFile.Name(), ignoreDir, missFiles[i:end])
		if err != nil {
			return nil, err
		}
		raw = append(raw, batchFindings...)
	}

	// Group the batch's findings (post JWT-demo filtering, so a cache hit
	// never needs to re-apply that filter) by file, seeding every scanned
	// file - including one with zero findings - with an entry, so a clean
	// file's cache write is an explicit empty array, not a silent omission.
	missFindings := make(map[string][]cachedFinding, len(missFiles))
	for _, rel := range missFiles {
		missFindings[rel] = []cachedFinding{}
	}
	for _, f := range betterleaksFindingsToFindings(raw) {
		missFindings[f.Path] = append(missFindings[f.Path], cachedFinding{RuleID: f.Rule, Description: f.Detail})
	}
	for _, rel := range missFiles {
		key, cacheable := keyForFile[rel]
		if !cacheable {
			continue
		}
		if err := cache.Put(key, missFindings[rel]); err != nil {
			return nil, fmt.Errorf("githooks: writing betterleaks cache entry for %s: %w", rel, err)
		}
	}

	var findings []Finding
	for _, rel := range files {
		cfs := cachedForFile[rel]
		if cfs == nil {
			cfs = missFindings[rel]
		}
		for _, cf := range cfs {
			findings = append(findings, Finding{
				Path:     rel,
				Rule:     cf.RuleID,
				Detail:   cf.Description,
				Category: categoryForRuleID(cf.RuleID),
			})
		}
	}
	return findings, nil
}
