package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pipefail is the line each block of the README's Install section that pipes curl into sh starts
// with.
const pipefail = "set -o pipefail\n"

// fakeLess stands in for less, with which an operator reads install.sh before it runs: it copies
// what it shows to FAKE_ROOT/shown.
const fakeLess = `#!/bin/sh
cat "$@" >"$FAKE_ROOT/shown"
`

// readmeBlocks are the sh blocks of the README's Install section, each as an operator pastes it.
func readmeBlocks(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(data), "\n## Install\n")
	if !found {
		t.Fatal("the README has no Install section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var blocks []string
	for {
		_, rest, found := strings.Cut(section, "\n```sh\n")
		if !found {
			return blocks
		}
		var block string
		if block, section, found = strings.Cut(rest, "\n```\n"); !found {
			t.Fatal("a block of the README's Install section has no end")
		}
		blocks = append(blocks, block+"\n")
	}
}

// readmeBlock is a block of the README's Install section, 0.1.0 in place of X.Y.Z, and the
// version it installs. script is curl's log line for install.sh, and latest whether install.sh
// then asks which release is the latest.
type readmeBlock struct {
	name, block, version, script string
	latest                       bool
}

// installBlocks are the README's three blocks: one for the latest release and one for a given
// release, through IKEWARD_VERSION, each setting pipefail and piping curl into sh once. The third
// downloads install.sh, then shows it with less, then runs it, each only where the one before
// succeeds.
func installBlocks(t *testing.T) []readmeBlock {
	t.Helper()
	blocks := readmeBlocks(t)
	const (
		given     = "| IKEWARD_VERSION=X.Y.Z sh\n"
		readFirst = " &&\n    less install.sh && sh install.sh\n"
	)
	if len(blocks) != 3 || !strings.HasPrefix(blocks[0], pipefail) ||
		strings.Count(blocks[0], "| sh\n") != 1 || strings.Contains(blocks[0], "X.Y.Z") ||
		!strings.HasPrefix(blocks[1], pipefail) || strings.Count(blocks[1], given) != 1 ||
		strings.Count(blocks[1], "X.Y.Z") != 2 || strings.Contains(blocks[2], pipefail) ||
		!strings.HasSuffix(blocks[2], readFirst) {
		t.Fatalf("the README's Install section holds %q, want a block for the latest release, one "+
			"with %q, each starting with %q, then one that ends with %q", blocks, given, pipefail,
			readFirst)
	}
	script := releases + "/latest/download/install.sh"
	return []readmeBlock{
		{"latest", blocks[0], "0.2.0", script + " > -", true},
		{"given", strings.ReplaceAll(blocks[1], "X.Y.Z", "0.1.0"), "0.1.0",
			releases + "/download/v0.1.0/install.sh > -", false},
		{"read first", blocks[2], "0.2.0", script + " > install.sh", true},
	}
}

// readmeInstaller is a world for the README's blocks. The latest release, v0.2.0, and an older
// one, v0.1.0, publish install.sh. The PATH holds the sh the blocks run, and a fake less.
func readmeInstaller(t *testing.T) *installer {
	t.Helper()
	in := newInstaller(t)
	in.link(t, "sh")
	in.fake(t, "less", fakeLess)
	in.publish(t, "latest", "0.2.0")
	in.publish(t, "v0.1.0", "0.1.0")
	for _, tag := range []string{"latest", "v0.1.0"} {
		in.write(t, tag, "install.sh", installScript(t))
	}
	return in
}

// runBlock runs script in shell as in.run does, but skips the test where the script sets pipefail
// and shell has none. Debian's dash has it from 0.5.12-7 on, so Ubuntu 24.04's sh, which CI's
// check job runs on ubuntu-latest, lacks it.
func runBlock(t *testing.T, in *installer, shell, script string) result {
	t.Helper()
	if strings.HasPrefix(script, pipefail) &&
		exec.Command(shellPath(t, shell), "-c", pipefail).Run() != nil {
		t.Skipf("%s has no pipefail, as Ubuntu 24.04's dash 0.5.12-6", shell)
	}
	return in.run(t, shell, script, nil)
}

// Each block of the README's Install section, pasted whole, installs one release: the latest, or
// the one IKEWARD_VERSION gives. The release's install.sh asks which is the latest where none is
// given, then downloads the checksums and the binary by its tag.
func TestReadmeInstall(t *testing.T) {
	t.Parallel()
	blocks := installBlocks(t)
	for _, shell := range []string{"sh", "bash"} {
		for _, test := range blocks {
			t.Run(shell+" "+test.name, func(t *testing.T) {
				t.Parallel()
				in := readmeInstaller(t)
				got := runBlock(t, in, shell, test.block)
				want := result{stdout: "installed ikeward version " + test.version + " as " +
					in.dir + "/ikeward\n"}
				if got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
				checkInstalled(t, in.dir, fakeBinary(test.version, "amd64"))
				wantAsked := []string{test.script}
				if test.latest {
					wantAsked = append(wantAsked, askedLatest)
				}
				wantAsked = append(wantAsked, downloads("v"+test.version, in.dir)...)
				if asked := in.asked(t); !slices.Equal(asked, wantAsked) {
					t.Errorf("curl was asked for\n%s\nwant\n%s", strings.Join(asked, "\n"),
						strings.Join(wantAsked, "\n"))
				}
			})
		}
	}
}

// The third block downloads install.sh into the directory it runs in, and less shows the operator
// the script that sh then runs, as the latest release publishes it.
func TestReadmeInstallReadFirst(t *testing.T) {
	t.Parallel()
	in := readmeInstaller(t)
	if got := runBlock(t, in, "sh", installBlocks(t)[2].block); got.code != 0 {
		t.Fatalf("got %+v, want status 0", got)
	}
	for _, path := range []string{filepath.Join(in.work, "install.sh"),
		filepath.Join(in.root, "shown")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != installScript(t) {
			t.Errorf("%s holds\n%s\nwant install.sh", path, data)
		}
	}
}

// A curl that cannot download install.sh fails each block, which runs nothing and installs
// nothing. The first two exit with curl's status, 22, through their pipefail, and without it exit
// 0, as the README warns. The third exits 22 as its commands are joined with &&.
func TestReadmeInstallFails(t *testing.T) {
	t.Parallel()
	for _, test := range installBlocks(t) {
		scripts := map[string]int{test.block: 22}
		if without, found := strings.CutPrefix(test.block, pipefail); found {
			scripts[without] = 0
		}
		for _, shell := range []string{"sh", "bash"} {
			for script, code := range scripts {
				t.Run(fmt.Sprintf("%s %s status %d", shell, test.name, code), func(t *testing.T) {
					t.Parallel()
					checkCurlFails(t, shell, script, test.script, code)
				})
			}
		}
	}
}

// checkCurlFails runs script in shell where no release has install.sh, and an older download of it
// waits in the directory it runs in. It checks that the script exits with code, having asked curl
// for install.sh alone, as the log line asked says. Nothing ran the older one or showed it, and
// nothing was installed.
func checkCurlFails(t *testing.T, shell, script, asked string, code int) {
	t.Helper()
	in := readmeInstaller(t)
	for _, tag := range []string{"latest", "v0.1.0"} {
		in.unpublish(t, tag, "install.sh")
	}
	older := "#!/bin/sh\n: >\"$FAKE_ROOT/ran\"\n"
	if err := os.WriteFile(filepath.Join(in.work, "install.sh"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runBlock(t, in, shell, script)
	stderr := "curl: (22) The requested URL returned error: 404\n"
	if want := (result{code, "", stderr}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got, want := in.asked(t), []string{asked}; !slices.Equal(got, want) {
		t.Errorf("curl was asked for %q, want %q", got, want)
	}
	for _, path := range []string{filepath.Join(in.root, "ran"), filepath.Join(in.root, "shown"),
		in.dir} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists, or cannot be read: %v", path, err)
		}
	}
}
