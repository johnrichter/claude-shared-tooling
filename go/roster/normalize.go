package roster

import (
	"regexp"
	"strings"
)

// dateSnapshotSuffix matches a vendor dated full ID's trailing "-YYYYMMDD" (e.g.
// claude-haiku-4-5-20251001). Exactly 8 digits is unambiguous against a generation segment,
// which the roster schema caps at 3 digits, so stripping it can never eat part of the family
// or generation.
var dateSnapshotSuffix = regexp.MustCompile(`-[0-9]{8}$`)

// windowSelectorSuffix is the roster schema's one documented context-window opt-in marker on a
// full model ID (e.g. claude-sonnet-5[1m]).
const windowSelectorSuffix = "[1m]"

// windowVariant is the context_variants map key windowSelectorSuffix reaches. It doubles as
// normalize's variant return value, so a caller never invents its own literal for it.
const windowVariant = "1m"

// normalize reduces a transcript-supplied model ID to the bare pinned form used as a roster row
// key, and reports which context variant (if any) the id opted into: strip a trailing [1m]
// window selector first — recording it as variant — then a trailing -YYYYMMDD snapshot date.
// variant is "" for a bare id. This is the library's single normalization point — Lookup,
// EffortAvailable, and Price each route an id through here before touching roster data, so the
// reduction and the variant determination each happen exactly once.
func normalize(id string) (bareID, variant string) {
	if strings.HasSuffix(id, windowSelectorSuffix) {
		variant = windowVariant
		id = strings.TrimSuffix(id, windowSelectorSuffix)
	}
	id = dateSnapshotSuffix.ReplaceAllString(id, "")
	return id, variant
}
