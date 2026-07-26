package agentcontract

// Lint discovers every roster under root and returns the combined, deterministic Report: the
// discriminator matrix over each roster, plus the instruction properties on every brief found.
// A brief that fails to parse is a hard error (see DiscoverRosters), never a skipped roster
// member.
func Lint(root string, opts Options) (*Report, error) {
	rosters, err := DiscoverRosters(root)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	briefsChecked := 0
	for _, r := range rosters {
		findings = append(findings, CheckMatrix(r)...)
		for _, b := range r.Briefs {
			briefsChecked++
			findings = append(findings, CheckProperties(b, opts)...)
		}
	}

	return NewReport(len(rosters), briefsChecked, findings), nil
}
