package installer

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// latest names the latest release in the installer's messages, once it has found it.
const latest = "the latest release of zadykian/ikeward, v0.2.0"

// refusal is a run of the installer that fails: prepare changes the world, env the installer's
// environment, and the installer's last line is "install.sh: " and the refusal.
type refusal struct {
	name    string
	prepare func(t *testing.T, in *installer)
	env     map[string]string
	refusal string
}

// refusals are the runs that TestInstallRefuses checks.
func refusals() []refusal {
	const binary = "ikeward-linux-amd64"
	runs := []refusal{
		{"no curl", func(t *testing.T, in *installer) {
			t.Helper()
			in.remove(t, "curl")
		}, nil, "curl is needed to download ikeward's releases"},
		{"no sha256sum", func(t *testing.T, in *installer) {
			t.Helper()
			in.remove(t, "sha256sum")
		}, nil, "sha256sum is needed to check ikeward's checksum"},
		{"another repository", nil, map[string]string{"IKEWARD_REPO": "someone/else"},
			"cannot find the latest release of someone/else"},
		{"no latest release", func(t *testing.T, in *installer) {
			t.Helper()
			if err := os.Remove(filepath.Join(in.releases, "latest")); err != nil {
				t.Fatal(err)
			}
		}, nil, "cannot find the latest release of zadykian/ikeward: GitHub redirects to '" +
			releases + "'"},
		{"release not found", nil, map[string]string{"IKEWARD_VERSION": "9.9.9"},
			"cannot download ikeward.sha256 from the v9.9.9 release of zadykian/ikeward"},
		{"no checksum", func(t *testing.T, in *installer) {
			t.Helper()
			in.write(t, "latest", "ikeward.sha256", "0123  ikeward-linux-arm64\n")
		}, nil, "ikeward.sha256 of " + latest + " has no checksum for " + binary},
		{"no binary", func(t *testing.T, in *installer) {
			t.Helper()
			in.unpublish(t, "latest", binary)
		}, nil, "cannot download " + binary + " from " + latest},
		{"checksum mismatch", func(t *testing.T, in *installer) {
			t.Helper()
			in.write(t, "latest", binary, fakeBinary("0.2.0", "amd64")+"# changed\n")
		}, nil, binary + " does not match its checksum in ikeward.sha256 of " + latest},
		{"does not run", func(t *testing.T, in *installer) {
			t.Helper()
			content := "#!/bin/sh\nexit 3\n"
			in.write(t, "latest", binary, content)
			in.write(t, "latest", "ikeward.sha256",
				fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte(content)), binary))
		}, nil, "the " + binary + " downloaded does not run"},
	}
	// The latest tag goes into the downloads' addresses: anything but vX.Y.Z is refused.
	for _, tag := range []string{"nightly", "1.2.3", "v1.2", "v1.2.3-rc.1", "v01.2.3", "v1x.2.3",
		"v1.2.3 x", "v1.2.3/../../x"} {
		runs = append(runs, refusal{"latest " + tag, func(t *testing.T, in *installer) {
			t.Helper()
			in.makeLatest(t, tag)
		}, nil, "cannot find the latest release of zadykian/ikeward: GitHub redirects to '" +
			releases + "/tag/" + tag + "'"})
	}
	return runs
}

// The installer refuses, leaving the ikeward installed unchanged and no temporary file. It refuses
// without curl or sha256sum, without a release or a latest tag vX.Y.Z, and without the binary's
// checksum or the binary. It refuses a binary that does not match its checksum, and one that does
// not run.
func TestInstallRefuses(t *testing.T) {
	t.Parallel()
	for _, test := range refusals() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t)
			in.publish(t, "latest", "0.2.0")
			old := fakeBinary("0.1.0", "amd64")
			in.publish(t, "v0.1.0", "0.1.0")
			if got := in.run(t, "sh", installScript(t),
				map[string]string{"IKEWARD_VERSION": "0.1.0"}); got.code != 0 {
				t.Fatalf("installing 0.1.0: %+v", got)
			}
			if test.prepare != nil {
				test.prepare(t, in)
			}
			got := in.run(t, "sh", installScript(t), test.env)
			if got.code != 1 || got.stdout != "" ||
				!strings.HasSuffix(got.stderr, "install.sh: "+test.refusal+"\n") {
				t.Errorf("got %+v, want status 1 and %q", got, test.refusal)
			}
			checkInstalled(t, in.dir, old)
		})
	}
}

// A download of the script cut short runs nothing: everything is in functions that its last
// line calls. Wherever the script ends, curl never runs and nothing is installed.
func TestInstallCutShort(t *testing.T) {
	t.Parallel()
	lines := strings.SplitAfter(installScript(t), "\n")
	last := slices.Index(lines, "main\n")
	if last < 0 || last != len(lines)-2 {
		t.Fatal("install.sh's last line is not main")
	}
	// The script cut after each line before main, and main itself cut short.
	var cuts []string
	for i := 0; i <= last; i++ {
		cuts = append(cuts, strings.Join(lines[:i], ""))
	}
	cuts = append(cuts, strings.Join(lines[:last], "")+"mai")
	in := newInstaller(t)
	in.publish(t, "latest", "0.2.0")
	for i, cut := range cuts {
		in.run(t, "sh", cut, nil)
		if checkNothing(t, in); t.Failed() {
			t.Fatalf("the script cut after line %d ran", i)
		}
	}
}
