package characterize

import (
	"fmt"
	"strings"
)

// idMinter mints this manifest build's ids, one counter per entry kind (surface, weak-spot, gap)
// so every id is unique within the manifest without depending on the characterizing agent to
// invent one itself.
type idMinter struct {
	pluginSlug string
	counts     map[string]int
}

func newIDMinter(pluginSlug string) *idMinter {
	return &idMinter{pluginSlug: pluginSlug, counts: map[string]int{}}
}

func (m *idMinter) next(kind string) string {
	m.counts[kind]++
	return mintID(m.pluginSlug, kind, m.counts[kind])
}

// buildManifest turns one probe's raw candidate claims into a manifest's surfaces and
// could_not_determine gaps. Every surface and weak-spot citation is checked with resolveCitation
// against the plugin's real files at pluginDir; a candidate claim that fails that check, fails the
// schema's own minimum lengths, or names an unrecognized surface type is redirected into a gap
// instead of being dropped or emitted as a surface anyway -- this is where the package's
// never-fabricate-a-surface invariant is enforced. The returned slices are never nil: an empty
// manifest reports zero surfaces and zero gaps, not the absence of either field.
func buildManifest(ids *idMinter, pluginRepoPath, pluginDir string, cand candidateManifest) ([]Surface, []Gap) {
	surfaces := []Surface{}
	gaps := []Gap{}

	for _, cs := range cand.Surfaces {
		surface, weakSpotGaps, reason, ok := resolveSurface(ids, pluginRepoPath, pluginDir, cs)
		if !ok {
			gaps = append(gaps, Gap{
				ID:                ids.next("gap"),
				Area:              candidateSurfaceArea(cs),
				Reason:            reason,
				AttemptedCitation: attemptedCitation(cs.Citation),
			})
			continue
		}
		surfaces = append(surfaces, surface)
		gaps = append(gaps, weakSpotGaps...)
	}

	dropped := 0
	for _, cg := range cand.CouldNotDetermine {
		area := strings.TrimSpace(cg.Area)
		reason := strings.TrimSpace(cg.Reason)
		if len(area) < minGapAreaLen || len(reason) < minGapReasonLen {
			dropped++
			continue
		}
		var cite *Citation
		if cg.AttemptedCitation != nil {
			cite = attemptedCitation(*cg.AttemptedCitation)
		}
		gaps = append(gaps, Gap{ID: ids.next("gap"), Area: cg.Area, Reason: cg.Reason, AttemptedCitation: cite})
	}
	if dropped > 0 {
		gaps = append(gaps, Gap{
			ID:     ids.next("gap"),
			Area:   "characterizing reply quality",
			Reason: fmt.Sprintf("%d could-not-determine entr%s from the probe's reply did not meet the manifest's minimum area/reason length and were omitted rather than emitted underfilled", dropped, plural(dropped, "y", "ies")),
		})
	}

	return surfaces, gaps
}

// resolveSurface validates and resolves one candidate surface. ok is false when the candidate
// fails a check this package enforces before anything is allowed into Surfaces; reason then names
// why, for the gap the caller folds it into. When ok is true, the returned gaps are the surface's
// own weak spots that failed to resolve -- folded in as gaps naming the surface they were claimed
// against, rather than silently dropped.
func resolveSurface(ids *idMinter, pluginRepoPath, pluginDir string, cs candidateSurface) (Surface, []Gap, string, bool) {
	if !validSurfaceType(cs.Type) {
		return Surface{}, nil, fmt.Sprintf("candidate names surface type %q, not one of this manifest's recognized surface kinds", cs.Type), false
	}
	trigger := strings.TrimSpace(cs.Trigger)
	if len(trigger) < minTriggerLen {
		return Surface{}, nil, fmt.Sprintf("candidate's trigger description is %d characters, below the manifest's %d-character minimum", len(trigger), minTriggerLen), false
	}
	if err := resolveCitation(pluginRepoPath, pluginDir, cs.Citation); err != nil {
		return Surface{}, nil, err.Error(), false
	}

	label := candidateSurfaceArea(cs)
	weakSpots := []WeakSpot{}
	var weakSpotGaps []Gap
	for _, cw := range cs.WeakSpots {
		ws, reason, ok := resolveWeakSpot(ids, pluginRepoPath, pluginDir, cw)
		if !ok {
			weakSpotGaps = append(weakSpotGaps, Gap{
				ID:                ids.next("gap"),
				Area:              fmt.Sprintf("weak spot claimed on %s", label),
				Reason:            reason,
				AttemptedCitation: attemptedCitation(cw.Citation),
			})
			continue
		}
		weakSpots = append(weakSpots, ws)
	}

	return Surface{
		ID:        ids.next("surface"),
		Type:      cs.Type,
		Name:      cs.Name,
		Trigger:   cs.Trigger,
		Citation:  cs.Citation,
		WeakSpots: weakSpots,
		Notes:     cs.Notes,
	}, weakSpotGaps, "", true
}

// resolveWeakSpot validates and resolves one candidate weak spot the same way resolveSurface
// resolves a surface: a citation that does not check out against a real file names a claimed
// problem this package cannot verify, so it is never emitted as a weak spot.
func resolveWeakSpot(ids *idMinter, pluginRepoPath, pluginDir string, cw candidateWeakSpot) (WeakSpot, string, bool) {
	desc := strings.TrimSpace(cw.Description)
	basis := strings.TrimSpace(cw.Basis)
	if len(desc) < minWeakSpotProseLen {
		return WeakSpot{}, fmt.Sprintf("weak-spot description is %d characters, below the manifest's %d-character minimum", len(desc), minWeakSpotProseLen), false
	}
	if len(basis) < minWeakSpotProseLen {
		return WeakSpot{}, fmt.Sprintf("weak-spot basis is %d characters, below the manifest's %d-character minimum", len(basis), minWeakSpotProseLen), false
	}
	if err := resolveCitation(pluginRepoPath, pluginDir, cw.Citation); err != nil {
		return WeakSpot{}, err.Error(), false
	}
	return WeakSpot{
		ID:          ids.next("weak-spot"),
		Description: cw.Description,
		Basis:       cw.Basis,
		Citation:    cw.Citation,
		Severity:    cw.Severity,
	}, "", true
}

// candidateSurfaceArea renders a rejected candidate surface's gap.area: identifying enough for a
// later tier to know what was claimed, without repeating the full rejection reason (that is the
// gap's Reason).
func candidateSurfaceArea(cs candidateSurface) string {
	name := strings.TrimSpace(cs.Name)
	if name == "" {
		name = strings.TrimSpace(string(cs.Type))
	}
	if name == "" {
		name = "unnamed candidate surface"
	}
	return fmt.Sprintf("candidate surface: %s", name)
}

func plural(n int, singular, pluralSuffix string) string {
	if n == 1 {
		return singular
	}
	return pluralSuffix
}
