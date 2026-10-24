package install

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zadykian/ikeward/internal/routing"
)

// fakes are the fakes of a system that install set up, for a probe to find otherwise.
type fakes struct {
	runner *fakeRunner
}

// probeCase is a probe that finds a system that install set up otherwise, as setUp leaves it.
// want are the steps due, each its command, then why.
type probeCase struct {
	name  string
	setUp func(fakes)
	want  []string
}

// checkProbes runs a dry run of install for each of cases, which must take the steps want names
// alone, but for those with no probe, which run all the same.
func checkProbes(t *testing.T, cases []probeCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			p := newProfile(t, "client")
			f, r := newFakeFiles(), newFakeRunner(Packages...)
			installOn(t, f, r, p)
			var out bytes.Buffer
			s := dryRunOn(f, r, &out)
			fs := fakes{r}
			test.setUp(fs)
			if err := s.Install(p, settings); err != nil {
				t.Fatal(err)
			}
			wantOut := []string{"Left 12 of the 12 files as they were"}
			for _, line := range test.want {
				wantOut = append(wantOut, "Would run "+line)
			}
			checkLines(t, "the output", probed(outputLines(t, out.String())), wantOut)
		})
	}
}

// probed are lines but those of the steps with no probe, which run on every install.
func probed(lines []string) []string {
	return slices.DeleteFunc(lines, func(line string) bool {
		return slices.ContainsFunc(steps, func(st step) bool {
			return st.probe == nil && line == "Would run "+strings.Join(st.command, " ")
		})
	})
}

// What systemctl show says of a unit makes systemd's reload due: a unit changed on disk or not
// loaded. systemctl failing does too.
func TestUnitProbes(t *testing.T) {
	failed := errors.New("exit status 1")
	checkProbes(t, []probeCase{
		{"changed on disk", func(f fakes) { f.runner.states[Service+" NeedDaemonReload"] = "yes" },
			[]string{"systemctl daemon-reload, as ikeward-watchdog.service changed on disk"}},
		{"not loaded", func(f fakes) { f.runner.states[routing.Unit+" LoadState"] = "not-found" },
			[]string{"systemctl daemon-reload, as ikeward-routing.service is not-found"}},
		{"systemctl fails", func(f fakes) {
			f.runner.fail["systemctl show --property=LoadState,ActiveState,UnitFileState,"+
				"NeedDaemonReload "+Timer] = failed
		}, []string{
			"systemctl daemon-reload, as systemctl show ikeward-watchdog.timer fails: exit status 1",
		}},
	})
}
