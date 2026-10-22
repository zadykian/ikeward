package watchdog

import (
	"errors"
	"slices"
	"strings"

	"github.com/zadykian/ikeward/internal/files"
	"github.com/zadykian/ikeward/internal/routing"
	"github.com/zadykian/ikeward/internal/status"
)

// unmendable says, by check, what a restart of strongSwan does not change, so that the check's
// failure stays. These are ikeward0's own address and resolved's settings of it, and ikeward's
// routing.
var unmendable = map[string]string{
	status.DNSCheck:   files.Link + "'s own address and resolved's settings, which networkd gives",
	status.RuleCheck:  "ikeward's rules, which the watchdog makes itself",
	status.FloorCheck: "the floors, which the watchdog makes itself",
	status.NATCheck:   "table ip " + routing.NATTable + ", which the watchdog makes itself",
}

// unchanged says what a restart of strongSwan does not change of r, a check that failed, or
// nothing where a restart may mend it. A resolver that answers, if with an error, does so through
// the tunnel, which then works. Where it does not answer, the tunnel may carry nothing. Settings
// that cannot be read fail the checks that need them, whatever strongSwan does.
func unchanged(r status.Result) string {
	switch {
	case errors.Is(r.Err, routing.ErrSettings):
		return files.SettingsFile + ", which install writes"
	case errors.Is(r.Err, status.ErrAnswer):
		return "the remote resolver's answer, which came through the tunnel"
	}
	return unmendable[r.Name]
}

// mendable is whether a check of results that a restart of strongSwan can mend failed: any that
// unchanged says nothing of. A restart would otherwise drop a working tunnel every MinInterval,
// and leave the fault in place.
func mendable(results []status.Result) bool {
	return slices.ContainsFunc(results, func(r status.Result) bool {
		return !r.OK && unchanged(r) == ""
	})
}

// unmended says what a restart would not change of the checks of results that failed, each once.
func unmended(results []status.Result) string {
	var what []string
	for _, r := range results {
		if u := unchanged(r); !r.OK && u != "" && !slices.Contains(what, u) {
			what = append(what, u)
		}
	}
	return strings.Join(what, ", nor ")
}
