package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssuedProfile reads a real profile where IKEWARD_PROFILE names its files' path without their
// extension, as /root/vpnclient, and logs its summary, which holds no secret. It reads a
// .mobileconfig where there is one, and the .p12's password from IKEWARD_PROFILE_PASSWORD_FILE's
// file. Run with -v; CI sets neither, so the test skips.
func TestIssuedProfile(t *testing.T) {
	profile := os.Getenv("IKEWARD_PROFILE")
	if profile == "" {
		t.Skip("IKEWARD_PROFILE names no profile")
	}
	read := func(ext string) File {
		data, err := os.ReadFile(profile + ext)
		if err != nil {
			t.Fatal(err)
		}
		return File{filepath.Base(profile + ext), data}
	}
	files := Files{P12: read(".p12"), SSwan: read(".sswan")}
	if _, err := os.Stat(profile + ".mobileconfig"); err == nil {
		files.Mobileconfig = read(".mobileconfig")
	}
	p, err := Parse(files, func() (string, error) {
		passwordFile := os.Getenv("IKEWARD_PROFILE_PASSWORD_FILE")
		if passwordFile == "" {
			return "", errors.New("IKEWARD_PROFILE_PASSWORD_FILE names no file")
		}
		password, err := os.ReadFile(passwordFile)
		return strings.TrimSuffix(string(password), "\n"), err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range p.Summary() {
		t.Log(line)
	}
}
