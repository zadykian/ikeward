package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssuedProfile reads a real profile where IKEWARD_PROFILE names its zip, and logs its summary,
// which holds no secret. A .p12's password, where it has one, is read from the file that
// IKEWARD_PROFILE_PASSWORD_FILE names. Run with -v; CI sets neither, so the test skips.
func TestIssuedProfile(t *testing.T) {
	profile := os.Getenv("IKEWARD_PROFILE")
	if profile == "" {
		t.Skip("IKEWARD_PROFILE names no profile")
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(filepath.Base(profile), data, func() (string, error) {
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
