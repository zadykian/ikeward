package installer

import (
	"slices"
	"strings"
	"testing"
)

// The installer finds the latest release, then downloads its ikeward.sha256 and the binary for
// this machine by its tag. It checks the binary, runs it for its version and installs it, in sh and
// in bash.
func TestInstall(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"sh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t)
			in.publish(t, "latest", "0.2.0")
			got := in.run(t, shell, installScript(t), nil)
			want := result{stdout: "installed ikeward version 0.2.0 as " + in.dir + "/ikeward\n"}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			checkInstalled(t, in.dir, fakeBinary("0.2.0", "amd64"))
			wantAsked := append([]string{askedLatest}, downloads("v0.2.0", in.dir)...)
			if asked := in.asked(t); !slices.Equal(asked, wantAsked) {
				t.Errorf("curl was asked for\n%s\nwant\n%s", strings.Join(asked, "\n"),
					strings.Join(wantAsked, "\n"))
			}
		})
	}
}

// Run again, the installer replaces the ikeward it installed with the latest release's.
func TestInstallAgain(t *testing.T) {
	t.Parallel()
	in := newInstaller(t)
	in.publish(t, "latest", "0.2.0")
	if got := in.run(t, "sh", installScript(t), nil); got.code != 0 {
		t.Fatalf("the first install: %+v", got)
	}
	in.publish(t, "latest", "0.3.0")
	got := in.run(t, "sh", installScript(t), nil)
	want := result{stdout: "installed ikeward version 0.3.0 as " + in.dir + "/ikeward\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	checkInstalled(t, in.dir, fakeBinary("0.3.0", "amd64"))
}

// The binary is the one for the machine's processor. Another system, or another processor, is
// refused before curl runs.
func TestInstallPlatforms(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		system, machine string
		arch, refusal   string
	}{
		{"Linux", "x86_64", "amd64", ""},
		{"Linux", "amd64", "amd64", ""},
		{"Linux", "aarch64", "arm64", ""},
		{"Linux", "arm64", "arm64", ""},
		{"Darwin", "arm64", "", "ikeward is released for Linux, not Darwin"},
		{"Linux", "riscv64", "", "ikeward is released for x86_64 and aarch64, not riscv64"},
	} {
		t.Run(test.system+" "+test.machine, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t)
			in.publish(t, "latest", "0.2.0")
			got := in.run(t, "sh", installScript(t), map[string]string{
				"FAKE_UNAME_S": test.system, "FAKE_UNAME_M": test.machine,
			})
			if test.refusal != "" {
				if want := (result{1, "", "install.sh: " + test.refusal + "\n"}); got != want {
					t.Errorf("got %+v, want %+v", got, want)
				}
				checkNothing(t, in)
				return
			}
			if got.code != 0 {
				t.Fatalf("got %+v", got)
			}
			checkInstalled(t, in.dir, fakeBinary("0.2.0", test.arch))
		})
	}
}

// Only root may install ikeward, which runs as root: any other user is refused before curl runs.
func TestInstallRoot(t *testing.T) {
	t.Parallel()
	in := newInstaller(t)
	in.publish(t, "latest", "0.2.0")
	got := in.run(t, "sh", installScript(t), map[string]string{"FAKE_UID": "1000"})
	refusal := "install.sh: ikeward installs and runs as root: run install.sh as root\n"
	if want := (result{1, "", refusal}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	checkNothing(t, in)
}

// Without IKEWARD_INSTALL_DIR, ikeward goes in /usr/local/sbin. A mkdir, mktemp and mv that fail,
// and a curl that writes only into the test's directory, keep the installer out of the host's.
func TestInstallDefaultDir(t *testing.T) {
	t.Parallel()
	in := newInstaller(t)
	in.publish(t, "latest", "0.2.0")
	for _, name := range hostWriters {
		in.fake(t, name, fakeRefuse)
	}
	got := in.run(t, "sh", installScript(t), map[string]string{"IKEWARD_INSTALL_DIR": ""})
	if want := (result{1, "", "install.sh: cannot create /usr/local/sbin\n"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if refused, want := in.refused(t), []string{"mkdir -p /usr/local/sbin"}; !slices.Equal(
		refused, want) {
		t.Errorf("the installer ran %q, want %q", refused, want)
	}
	want := []string{askedLatest, downloads("v0.2.0", in.dir)[0]}
	if asked := in.asked(t); !slices.Equal(asked, want) {
		t.Errorf("curl was asked for %q, want %q", asked, want)
	}
}
