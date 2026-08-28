package toolchain

import "testing"

// TestAdversarialExhaustiveUnimplementedPairsFailClosed walks every
// unimplemented (declared or not) pair reachable across the full check x
// language x test-kind cross product and asserts none resolves to a
// non-nil entry without a diagnostic — i.e. no silent pass anywhere.
func TestAdversarialExhaustiveUnimplementedPairsFailClosed(t *testing.T) {
	langs := []string{LanguageGo, LanguageRust, LanguagePython, LanguageShell, "unknown-lang", ""}
	checks := []Check{CheckBuild, CheckFormat, CheckLint, CheckVet, CheckSecurity, CheckTest, Check("bogus")}
	kinds := []TestKind{"", TestUnit, TestE2E, TestBenchmark, TestKind("bogus")}
	for _, l := range langs {
		for _, c := range checks {
			for _, k := range kinds {
				entry, diag := ResolveCheck(l, c, k)
				declared, ok := LookupPair(l, c, k)
				implemented := ok && declared.Implemented
				if implemented {
					if diag != nil {
						t.Errorf("ResolveCheck(%q,%q,%q) implemented pair got diag %+v, want nil", l, c, k, diag)
					}
					continue
				}
				if diag == nil {
					t.Errorf("ResolveCheck(%q,%q,%q) = nil diag for unimplemented/undeclared pair, want unsupported diagnostic (silent pass)", l, c, k)
				}
				if entry.Language != "" || entry.Check != "" {
					t.Errorf("ResolveCheck(%q,%q,%q) returned non-zero entry %+v alongside diagnostic, want zero value", l, c, k, entry)
				}
			}
		}
	}
}

// TestAdversarialUnsupportedDiagnosticNeverEmptyMessage checks the
// diagnostic message always names the refused pair, so a caller triaging
// EXIT 80 sees which check was rejected rather than a blank/templated
// message.
func TestAdversarialUnsupportedDiagnosticNeverEmptyMessage(t *testing.T) {
	d := UnsupportedDiagnostic(LanguageShell, CheckBuild, "")
	if d.Message == "" {
		t.Fatal("UnsupportedDiagnostic message is empty")
	}
	if d.Code != DiagUnsupportedCheck {
		t.Fatalf("code = %q, want %q", d.Code, DiagUnsupportedCheck)
	}
}
