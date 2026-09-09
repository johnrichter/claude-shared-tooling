package githooks

import "testing"

// Trigger literals are assembled from fragments so this file's own source
// does not trip the repo's own secret guardrail(s), matching this package's
// existing convention (see sanity_test.go).
var (
	// fixtureDemoJWT is jwt.io's own canonical homepage demo token: decodes
	// to sub:"1234567890", name:"John Doe", and its signature verifies
	// against jwt.io's own published demo HMAC secret - it matches all
	// three of isKnownDemoJWT's markers at once.
	fixtureDemoJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ." +
		"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

	// fixtureJaneDoeJWT decodes to sub:"0000000001" (not the demo sub),
	// name:"Jane Doe" (the demo's one documented sibling variant) - a
	// header+payload pair with no signature segment at all, pinning that
	// the sub/name check runs independent of signature verification.
	fixtureJaneDoeJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIwMDAwMDAwMDAxIiwibmFtZSI6IkphbmUgRG9lIn0"

	// fixtureDemoSecretSignedJWT carries arbitrary claims (neither the demo
	// sub nor a demo name) but is signed (HS256) with jwt.io's own published
	// demo secret - pinning that the signature check catches a token the
	// claim check alone would miss.
	fixtureDemoSecretSignedJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiI5OTk5OTk5OTk5IiwibmFtZSI6IlNvbWVvbmUgRWxzZSIsImlhdCI6MTUxNjIzOTAyMn0." +
		"DuV2icWWAH9Mutro1ZoGEQwkMpPorLJ4hbXgbKYwGhs"

	// fixtureArbitraryJWT has well-formed base64 header/payload, arbitrary
	// claims, and a signature that is neither valid nor demo-secret-signed -
	// it must be treated as a real, reportable finding.
	fixtureArbitraryJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiI1NTU1NTUiLCJuYW1lIjoiTm90IEEgRGVtbyIsInJvbGUiOiJhZG1pbiJ9." +
		"somesignaturegoeshereXX"
)

// TestIsKnownDemoJWTDetectsCanonicalDemoToken confirms jwt.io's own
// homepage demo token - matching sub, name, AND signature all at once - is
// recognized as a known fake.
func TestIsKnownDemoJWTDetectsCanonicalDemoToken(t *testing.T) {
	if !isKnownDemoJWT(fixtureDemoJWT) {
		t.Error("isKnownDemoJWT(canonical demo token) = false, want true")
	}
}

// TestIsKnownDemoJWTDetectsNameOnlyMatchWithNoSignature confirms the
// sub/name claim check alone is sufficient - it does not require a
// signature segment to be present at all.
func TestIsKnownDemoJWTDetectsNameOnlyMatchWithNoSignature(t *testing.T) {
	if !isKnownDemoJWT(fixtureJaneDoeJWT) {
		t.Error("isKnownDemoJWT(Jane Doe, 2-segment token) = false, want true")
	}
}

// TestIsKnownDemoJWTDetectsDemoSecretSignedToken confirms a token with
// wholly arbitrary claims is still recognized, purely because its HS256
// signature verifies against jwt.io's own published demo secret - the
// marker this session's own research specifically called out as catching
// more than just the exact homepage default claims.
func TestIsKnownDemoJWTDetectsDemoSecretSignedToken(t *testing.T) {
	if !isKnownDemoJWT(fixtureDemoSecretSignedJWT) {
		t.Error("isKnownDemoJWT(arbitrary claims, demo-secret-signed) = false, want true")
	}
}

// TestIsKnownDemoJWTStillFlagsArbitraryToken confirms a well-formed token
// matching none of the three markers is never exempted - the exemption
// narrows what's flagged, it never widens it.
func TestIsKnownDemoJWTStillFlagsArbitraryToken(t *testing.T) {
	if isKnownDemoJWT(fixtureArbitraryJWT) {
		t.Error("isKnownDemoJWT(arbitrary, non-demo token) = true, want false")
	}
}

// TestIsKnownDemoJWTNeverExemptsMalformedInput confirms a decode failure -
// invalid base64, too few segments, or empty input - is never itself
// grounds for exemption: it must fall through to "still flagged", the same
// as any other unrecognized token.
func TestIsKnownDemoJWTNeverExemptsMalformedInput(t *testing.T) {
	for _, s := range []string{
		"",
		"not-a-jwt",
		"not-base64!!!.also-not-base64!!!.sig",
		"eyJhbGciOiJIUzI1" + "NiJ9", // single segment, no payload at all
	} {
		if isKnownDemoJWT(s) {
			t.Errorf("isKnownDemoJWT(%q) = true, want false (malformed input is never exempted)", s)
		}
	}
}
