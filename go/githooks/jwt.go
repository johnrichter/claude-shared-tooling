package githooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// jwtDemoSecret is jwt.io's own published demo HMAC secret, shown on its
// homepage debugger for every HS256 example token. It is fragment-assembled,
// matching this package's existing convention (see secrets.go's
// awsExampleAccessKeyIDs), so this source file's own text never contains an
// unbroken, secret-shaped literal.
var jwtDemoSecret = "a-string-secret-at-" + "least-256-bits-long"

// jwtDemoSub and jwtDemoNames are the exact claim values jwt.io's own
// canonical homepage demo token carries (decoded: sub:"1234567890",
// name:"John Doe") plus its one documented sibling variant ("Jane Doe"),
// per this session's research (see the "secret-scanner rework" plan, §5).
const jwtDemoSub = "1234567890"

var jwtDemoNames = map[string]bool{"John Doe": true, "Jane Doe": true}

// jwtClaims is the subset of a JWT payload's fields isKnownDemoJWT inspects.
// Every other claim is ignored - this is a narrow, known-fake-payload check,
// not a general JWT validator.
type jwtClaims struct {
	Sub  string `json:"sub"`
	Name string `json:"name"`
}

// jwtHeader is the subset of a JWT header isKnownDemoJWT inspects.
type jwtHeader struct {
	Alg string `json:"alg"`
}

// decodeJWTSegment base64url-decodes one dot-separated JWT segment, trying
// the unpadded form first (the form every real JWT library emits) and
// falling back to padded standard base64url for a segment some encoder
// added padding to. Neither JWT's actual spec, nor betterleaks' own "jwt"
// rule regex, requires unpadded encoding, so both are accepted here; a
// segment that decodes as neither is reported as an error.
func decodeJWTSegment(seg string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(seg); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(seg)
}

// isKnownDemoJWT reports whether secret - the full JWT-shaped string
// betterleaks' "jwt" rule flagged (finding["secret"], its own header.payload.
// signature triple) - is jwt.io's own canonical debugger demo token, or an
// arbitrary token signed with jwt.io's published demo HMAC secret. See the
// "secret-scanner rework" plan, §5, for why this check exists and what it
// intentionally does NOT cover: this is one small, engine-agnostic post-
// filter, run on betterleaks' JSON output after the subprocess returns, not
// a general JWT validator.
//
// A candidate matching any of the following is a known fake, never a real,
// reportable finding:
//   - its payload's "sub" claim is exactly "1234567890" (jwt.io's own demo
//     subject), or
//   - its payload's "name" claim is "John Doe" or "Jane Doe" (jwt.io's own
//     demo name, plus its one documented sibling variant), or
//   - its header names "HS256" and its signature verifies against jwt.io's
//     own published demo secret - catching any OTHER payload someone signed
//     with that same well-known, publicly documented secret, not only the
//     homepage's exact default claims.
//
// A token this package cannot decode (malformed base64, non-JSON header/
// payload, fewer than two dot-separated segments) matches none of the above
// and is therefore treated as an ordinary, reportable finding - a decode
// failure is never itself grounds for exemption. Likewise, a well-formed
// token whose claims and signature match none of these markers passes
// through unexempted: this check only ever narrows what's flagged, it never
// widens it.
func isKnownDemoJWT(secret string) bool {
	parts := strings.Split(secret, ".")
	if len(parts) < 2 {
		return false
	}

	payloadJSON, err := decodeJWTSegment(parts[1])
	if err != nil {
		return false
	}
	var claims jwtClaims
	if err := json.Unmarshal(payloadJSON, &claims); err == nil {
		if claims.Sub == jwtDemoSub || jwtDemoNames[claims.Name] {
			return true
		}
	}

	if len(parts) != 3 {
		return false
	}
	headerJSON, err := decodeJWTSegment(parts[0])
	if err != nil {
		return false
	}
	var header jwtHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil || !strings.EqualFold(header.Alg, "HS256") {
		return false
	}
	sig, err := decodeJWTSegment(parts[2])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(jwtDemoSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	return hmac.Equal(sig, mac.Sum(nil))
}
