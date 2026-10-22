// Package installer checks install.sh, the installer each release publishes, piped into a shell
// as the README has it. It runs each block of the README's Install section on its own too, in
// bash, and in sh where sh has pipefail or the block sets none. A fake curl stands in for GitHub:
// it serves the files of test releases from a directory of the test's, and logs each address it
// is asked for. The binaries it serves are scripts that print their version as ikeward --version
// does. A fake uname and id say the machine and the user, and a fake less keeps what it shows. The
// installer's PATH holds them and the real tools it runs, with the sh that the README's blocks run,
// nothing else. IKEWARD_INSTALL_DIR names a directory of the test's to install into, never the
// host's.
package installer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// arches are those the releases publish ikeward for, as they name the binaries:
// ikeward-linux-ARCH.
var arches = []string{"amd64", "arm64"}

// fakeUname and fakeID say what FAKE_UNAME_S, FAKE_UNAME_M and FAKE_UID say: the system, the
// machine and the user's ID. fakeRefuse logs each run to FAKE_TOOL_LOG and fails. As mkdir,
// mktemp and mv, it keeps the installer out of the host's directories where it runs with its
// default one, whatever their order.
const (
	fakeUname = `#!/bin/sh
case $1 in -s) echo "$FAKE_UNAME_S" ;; -m) echo "$FAKE_UNAME_M" ;; *) exit 1 ;; esac
`
	fakeID = `#!/bin/sh
[ "$1" = -u ] || exit 1
echo "$FAKE_UID"
`
	fakeRefuse = `#!/bin/sh
printf '%s %s\n' "${0##*/}" "$*" >>"$FAKE_TOOL_LOG"
exit 1
`
)

// fakeBinary is the script a release of version serves as ikeward for arch. It prints
// "ikeward version VERSION" for --version, as ikeward does, and fails for anything else.
func fakeBinary(version, arch string) string {
	return "#!/bin/sh\n# ikeward-linux-" + arch + "\n[ \"$*\" = --version ] || exit 2\n" +
		"echo 'ikeward version " + version + "'\n"
}

// installer is a world for install.sh: the releases its curl serves, the tools on its PATH, and
// the directory it installs into, which it makes itself. It runs in work, an empty directory.
type installer struct {
	root, releases, tools, work, dir, log, toolLog string
	// faked are the tools replaced by fakes.
	faked map[string]bool
}

// hostWriters are the tools through which the installer writes to its directory: with them all
// faked, it may run with its default one, the host's /usr/local/sbin.
var hostWriters = []string{"mkdir", "mktemp", "mv"}

// result is how a run of the installer ended.
type result struct {
	code           int
	stdout, stderr string
}

func newInstaller(t *testing.T) *installer {
	t.Helper()
	root := t.TempDir()
	in := &installer{
		root:     root,
		releases: filepath.Join(root, "releases"),
		tools:    filepath.Join(root, "tools"),
		work:     filepath.Join(root, "work"),
		dir:      filepath.Join(root, "sbin"),
		log:      filepath.Join(root, "curl.log"),
		toolLog:  filepath.Join(root, "tools.log"),
		faked:    map[string]bool{},
	}
	for _, dir := range []string{in.tools, in.work} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, script := range map[string]string{"curl": fakeCurl, "uname": fakeUname,
		"id": fakeID} {
		in.fake(t, name, script)
	}
	// The real tools the installer and the fake curl run.
	for _, name := range []string{"awk", "cat", "chmod", "mkdir", "mktemp", "mv", "rm",
		"sha256sum"} {
		in.link(t, name)
	}
	return in
}

// link puts the host's tool name on the installer's PATH.
func (in *installer) link(t *testing.T, name string) {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is not installed", name)
	}
	if err := os.Symlink(path, filepath.Join(in.tools, name)); err != nil {
		t.Fatal(err)
	}
}

// fake puts the script on the installer's PATH as the tool name, in place of any there. It
// removes a link to a real tool first, so as never to write through it.
func (in *installer) fake(t *testing.T, name, script string) {
	t.Helper()
	path := filepath.Join(in.tools, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	// A child that a parallel test forks holds the open files until it execs, and the script
	// fails with ETXTBSY while one holds it for writing. A fork waits for ForkLock's readers.
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(script); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	in.faked[name] = true
}

// remove takes the tool name off the installer's PATH.
func (in *installer) remove(t *testing.T, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(in.tools, name)); err != nil {
		t.Fatal(err)
	}
}

// publish makes a release of version at tag: a binary for each architecture, and ikeward.sha256
// listing them as make build does. At latest, it makes the release at vVERSION the latest one.
func (in *installer) publish(t *testing.T, tag, version string) {
	t.Helper()
	if tag == "latest" {
		tag = "v" + version
		in.makeLatest(t, tag)
	}
	var sums strings.Builder
	for _, arch := range arches {
		name, content := "ikeward-linux-"+arch, fakeBinary(version, arch)
		in.write(t, tag, name, content)
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(content)), name)
	}
	in.write(t, tag, "ikeward.sha256", sums.String())
}

// makeLatest makes tag, whatever it holds, the latest release's, as GitHub's redirect names it.
func (in *installer) makeLatest(t *testing.T, tag string) {
	t.Helper()
	if err := os.MkdirAll(in.releases, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.releases, "latest"), []byte(tag), 0o644); err != nil {
		t.Fatal(err)
	}
}

// write writes the file name of the release at tag, or of the latest one at latest.
func (in *installer) write(t *testing.T, tag, name, content string) {
	t.Helper()
	dir := filepath.Join(in.releases, in.tag(t, tag))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unpublish removes the file name from the release at tag, or from the latest one at latest.
func (in *installer) unpublish(t *testing.T, tag, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(in.releases, in.tag(t, tag), name)); err != nil {
		t.Fatal(err)
	}
}

// tag is tag, or the latest release's at latest.
func (in *installer) tag(t *testing.T, tag string) string {
	t.Helper()
	if tag != "latest" {
		return tag
	}
	data, err := os.ReadFile(filepath.Join(in.releases, "latest"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// run pipes script into shell, as root on an x86_64 Linux and into in.dir, but for what env
// says.
func (in *installer) run(t *testing.T, shell, script string, env map[string]string) result {
	t.Helper()
	vars := map[string]string{
		"PATH": in.tools, "FAKE_ROOT": in.root, "FAKE_RELEASES": in.releases,
		"FAKE_CURL_LOG": in.log, "FAKE_TOOL_LOG": in.toolLog,
		"FAKE_UNAME_S": "Linux", "FAKE_UNAME_M": "x86_64", "FAKE_UID": "0",
		"IKEWARD_INSTALL_DIR": in.dir,
	}
	maps.Copy(vars, env)
	if vars["IKEWARD_INSTALL_DIR"] == "" {
		for _, name := range hostWriters {
			if !in.faked[name] {
				t.Fatalf("the installer would install into the host's /usr/local/sbin "+
					"through the real %s", name)
			}
		}
	}
	command := exec.Command(shellPath(t, shell))
	command.Dir = in.work
	command.Stdin = strings.NewReader(script)
	command.Env = []string{}
	for name, value := range vars {
		command.Env = append(command.Env, name+"="+value)
	}
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	var exit *exec.ExitError
	if err := command.Run(); err != nil && !errors.As(err, &exit) {
		t.Fatalf("%s: %v", shell, err)
	}
	return result{command.ProcessState.ExitCode(), stdout.String(), stderr.String()}
}

// refused are the commands the installer ran through fakeRefuse.
func (in *installer) refused(t *testing.T) []string {
	t.Helper()
	return readLog(t, in.toolLog)
}

// asked are the addresses curl was asked for, each with where its reply went, the temporary
// file's random part as XXXXXX.
func (in *installer) asked(t *testing.T) []string {
	t.Helper()
	lines := readLog(t, in.log)
	const temporary = "/.ikeward.XXXXXX"
	for i, line := range lines {
		if at := strings.Index(line, "/.ikeward."); at >= 0 {
			lines[i] = line[:at] + temporary + line[min(at+len(temporary), len(line)):]
		}
	}
	return lines
}

// readLog is the lines of the log at path, none where it does not exist.
func readLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// shellPath is the shell the installer is piped into.
func shellPath(t *testing.T, shell string) string {
	t.Helper()
	path, err := exec.LookPath(shell)
	if err != nil {
		t.Skipf("%s is not installed", shell)
	}
	return path
}
