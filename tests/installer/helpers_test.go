package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installScript is install.sh, as a release publishes it.
func installScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// checkInstalled checks that dir holds ikeward with content, 0755, and no temporary file of the
// installer's.
func checkInstalled(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "ikeward")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ikeward is not installed: %v", err)
	}
	if string(data) != content {
		t.Errorf("ikeward holds\n%s\nwant\n%s", data, content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode(); mode != 0o755 {
		t.Errorf("ikeward has mode %v, want -rwxr-xr-x", mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ikeward.") {
			t.Errorf("%s holds the temporary %s", dir, entry.Name())
		}
	}
}

// releases is where GitHub serves zadykian/ikeward's releases, and askedLatest the installer's
// question of which is the latest, as the fake curl logs it.
const (
	releases    = "https://github.com/zadykian/ikeward/releases"
	askedLatest = releases + "/latest > /dev/null"
)

// downloads are what the installer downloads from the release at tag, as the fake curl logs it:
// ikeward.sha256, then the amd64 binary into a temporary file in dir.
func downloads(tag, dir string) []string {
	return []string{releases + "/download/" + tag + "/ikeward.sha256 > -",
		releases + "/download/" + tag + "/ikeward-linux-amd64 > " + dir + "/.ikeward.XXXXXX"}
}

// checkNothing checks that the installer asked curl for nothing and made no directory to install
// into.
func checkNothing(t *testing.T, in *installer) {
	t.Helper()
	if asked := in.asked(t); len(asked) != 0 {
		t.Errorf("curl was asked for %q, want nothing", asked)
	}
	if _, err := os.Stat(in.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists, or cannot be read: %v", in.dir, err)
	}
}
