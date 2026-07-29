package plugin_conform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
)

// matcherAlternativeRE matches one bare, optionally-anchored literal tool name -- the one shape
// this check recognizes as a plain, fully-bounded alternation. A matcher built from anything
// else (a character class, a wildcard, a group) still gets the overbroad check below, but is
// reported as only partially checked, since this check cannot enumerate its declared triggers.
var matcherAlternativeRE = regexp.MustCompile(`^\^?[A-Za-z_][A-Za-z0-9_]*\$?$`)

// matcherControlToken is a synthetic tool name no real matcher declares. A matcher that fires on
// it is not bounded to what it documents, however narrow its declared alternatives look -- the
// static, self-contained half of "fires on trigger, silent otherwise" this check can prove
// without a live probe.
const matcherControlToken = "PluginConformControlNoMatch"

// isPlainAlternation reports whether matcher is a "|"-joined alternation of bare or anchored
// literal tool names -- the one shape whose declared trigger set this check can fully account
// for. Firing on each such literal needs no separate assertion: Go's regexp alternation
// guarantees a compiled "a|b|c" matches each of a, b, c, so the only property left to check for
// this shape (or any other) is that it does NOT also fire beyond what it declares -- the
// overbroad check below.
func isPlainAlternation(matcher string) bool {
	for _, part := range strings.Split(matcher, "|") {
		if !matcherAlternativeRE.MatchString(part) {
			return false
		}
	}
	return true
}

// CheckMatcherFires runs the mechanism tier's static half of "documented matcher fires": for
// every hook binding with a real (non-empty, non-"*") matcher, the matcher must compile, and it
// must not fire on a synthetic control token no declared alternative names -- catching an
// accidentally universal pattern (".*", an unanchored substring, an empty string) that fires far
// beyond its own documented scope. A matcher that is a plain "|"-joined literal alternation is
// fully accounted for by those two checks; anything more complex (a character class, a wildcard,
// a group) still gets both, but is flagged with a caveat naming the limit, never folded into a
// false "fully checked" pass.
func CheckMatcherFires(hooks *HooksFile) (errs, caveats []clikit.Diagnostic, err error) {
	if hooks == nil {
		return nil, nil, nil
	}

	for _, event := range sortedEvents(hooks.Hooks) {
		for i, b := range hooks.Hooks[event] {
			matcher := b.Matcher
			if matcher == "" || matcher == "*" {
				continue // no matcher, or an explicit match-everything -- nothing to test
			}

			re, compileErr := regexp.Compile(matcher)
			if compileErr != nil {
				d, buildErr := clikit.NewError(
					"gate_negative.plugin_conform.hook_matcher_invalid_regex",
					oneLine(fmt.Sprintf("%s binding [%d] matcher %q does not compile: %v", event, i, matcher, compileErr)),
					clikit.Manual("fix the matcher regex in "+hooksJSONPath),
					map[string]any{"event": event, "matcher": matcher},
				)
				if buildErr != nil {
					return nil, nil, buildErr
				}
				errs = append(errs, d)
				continue
			}

			if re.MatchString(matcherControlToken) {
				d, buildErr := clikit.NewError(
					"gate_negative.plugin_conform.hook_matcher_overbroad",
					oneLine(fmt.Sprintf("%s binding [%d] matcher %q fires on %q, a tool name it never declares -- broader than its own documented scope", event, i, matcher, matcherControlToken)),
					clikit.Manual("narrow the matcher in "+hooksJSONPath+" so it fires only on its declared tool names"),
					map[string]any{"event": event, "matcher": matcher},
				)
				if buildErr != nil {
					return nil, nil, buildErr
				}
				errs = append(errs, d)
			}

			if !isPlainAlternation(matcher) {
				d, buildErr := clikit.NewCaveat(
					"caveats.plugin_conform.hook_matcher_complex_pattern",
					oneLine(fmt.Sprintf("%s binding [%d] matcher %q is not a plain literal alternation -- checked for compiling and for overbroad firing only, not fully accounted for", event, i, matcher)),
					clikit.Manual("review this matcher by hand; the static self-consistency check fully covers only plain \"tool|tool\" alternations"),
					map[string]any{"event": event, "matcher": matcher},
				)
				if buildErr != nil {
					return nil, nil, buildErr
				}
				caveats = append(caveats, d)
			}
		}
	}
	return errs, caveats, nil
}

// PathResolver resolves a launcher name to its location on PATH, or an error if it cannot be
// found -- exec.LookPath in production, an injectable seam in a test so this check never depends
// on the test host's real PATH contents.
type PathResolver func(name string) (string, error)

// CheckLauncherOnPath requires every name in required to resolve through lookPath: the mechanism
// half of "built to spec + wired" for a plugin that depends on an external CLI being reachable
// by its documented base name, not merely installed somewhere unreachable.
func CheckLauncherOnPath(required []string, lookPath PathResolver) ([]clikit.Diagnostic, error) {
	var diags []clikit.Diagnostic
	for _, name := range required {
		if _, err := lookPath(name); err != nil {
			d, buildErr := clikit.NewError(
				"gate_negative.plugin_conform.launcher_not_on_path",
				oneLine(fmt.Sprintf("required launcher %q does not resolve on PATH: %v", name, err)),
				clikit.Manual("install "+name+" or add its containing directory to PATH"),
				map[string]any{"launcher": name},
			)
			if buildErr != nil {
				return nil, buildErr
			}
			diags = append(diags, d)
		}
	}
	return diags, nil
}
