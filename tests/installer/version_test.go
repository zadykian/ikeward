package installer

import (
	"slices"
	"testing"
)

// IKEWARD_VERSION installs that release, given as X.Y.Z or vX.Y.Z, and empty the latest; anything
// else is refused before curl runs. A release given needs no question of which is the latest.
func TestInstallVersion(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"0.1.0", "v0.1.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t)
			in.publish(t, "latest", "0.2.0")
			in.publish(t, "v0.1.0", "0.1.0")
			got := in.run(t, "sh", installScript(t), map[string]string{"IKEWARD_VERSION": version})
			if want := (result{stdout: "installed ikeward version 0.1.0 as " + in.dir +
				"/ikeward\n"}); got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
			checkInstalled(t, in.dir, fakeBinary("0.1.0", "amd64"))
			if asked, want := in.asked(t), downloads("v0.1.0", in.dir); !slices.Equal(asked,
				want) {
				t.Errorf("curl was asked for %q, want %q", asked, want)
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		in := newInstaller(t)
		in.publish(t, "latest", "0.2.0")
		got := in.run(t, "sh", installScript(t), map[string]string{"IKEWARD_VERSION": ""})
		if want := (result{stdout: "installed ikeward version 0.2.0 as " + in.dir +
			"/ikeward\n"}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
	for _, test := range []struct{ version, refusal string }{
		{"1.x", "IKEWARD_VERSION is not a version, X.Y.Z: 1.x"},
		{"1.2", "IKEWARD_VERSION is not a version, X.Y.Z: 1.2"},
		{"v", "IKEWARD_VERSION is not a version, X.Y.Z: v"},
		{"1.2.3.4", "IKEWARD_VERSION is not a version, X.Y.Z: 1.2.3.4"},
		{"1..2.3", "IKEWARD_VERSION is not a version, X.Y.Z: 1..2.3"},
		{"1.2.3.", "IKEWARD_VERSION is not a version, X.Y.Z: 1.2.3."},
		{".1.2.3", "IKEWARD_VERSION is not a version, X.Y.Z: .1.2.3"},
		{"01.2.3", "IKEWARD_VERSION is not a version, X.Y.Z: 01.2.3"},
		{"1.2.03", "IKEWARD_VERSION is not a version, X.Y.Z: 1.2.03"},
		{"latest", "IKEWARD_VERSION is not a version, X.Y.Z: latest"},
		{"0.0.9", "ikeward 0.0.9 was never released: 0.1.0 is the first"},
	} {
		t.Run(test.version, func(t *testing.T) {
			t.Parallel()
			in := newInstaller(t)
			in.publish(t, "latest", "0.2.0")
			env := map[string]string{"IKEWARD_VERSION": test.version}
			got := in.run(t, "sh", installScript(t), env)
			if want := (result{1, "", "install.sh: " + test.refusal + "\n"}); got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
			checkNothing(t, in)
		})
	}
}
