package plugin_behavioral

import (
	"fmt"
	"os"
	"strings"
)

// LiveOptInEnv is the ambient opt-in a driver honors in addition to Options.Live -- either one
// being set is sufficient. Scoped to this package the same way ModelPinEnv is, so a caller
// forcing every behavioral matrix in a batch live has one place to do it without editing a call
// site.
const LiveOptInEnv = "PLUGIN_BEHAVIORAL_LIVE"

// LiveOptInRequiredError reports a matrix run that selected at least one live (KindProbe) case
// without an explicit opt-in. It always propagates out of Run -- a caller must never turn this
// into a per-case failure and continue, since the run itself must not spend or provision
// anything.
type LiveOptInRequiredError struct {
	// LiveCaseCount is how many selected cases required the opt-in this run did not have.
	LiveCaseCount int
}

func (e *LiveOptInRequiredError) Error() string {
	return fmt.Sprintf(
		"plugin-behavioral: %d live case(s) selected without an explicit --live opt-in (or a truthy %s) -- refusing to run any of them",
		e.LiveCaseCount, LiveOptInEnv,
	)
}

// LiveOptedIn reports whether a live run is authorized: live, or a truthy LiveOptInEnv -- either
// is sufficient.
func LiveOptedIn(live bool) bool {
	if live {
		return true
	}
	v := strings.TrimSpace(os.Getenv(LiveOptInEnv))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// RequireLiveOptIn is the safety interlock every matrix run passes through before provisioning or
// spending anything on its live (KindProbe) cases. liveCaseCount is how many selected cases need
// a live run; zero always passes regardless of live or the ambient env, since a selection with no
// live case has nothing to gate. A KindAgentic case is never counted here -- it spends nothing and
// launches no probe, so it needs no opt-in.
func RequireLiveOptIn(liveCaseCount int, live bool) error {
	if liveCaseCount == 0 {
		return nil
	}
	if LiveOptedIn(live) {
		return nil
	}
	return &LiveOptInRequiredError{LiveCaseCount: liveCaseCount}
}
