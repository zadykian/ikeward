package files

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"
)

var update = flag.Bool("update", false, "rewrite testdata from what Generate returns")

// Generate's files for each profile, byte for byte as testdata has them, which -update rewrites.
func TestGenerate(t *testing.T) {
	for name, p := range profiles {
		t.Run(name, func(t *testing.T) {
			files, err := Generate(p, testSettings)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join("testdata", name)
			if *update {
				writeGolden(t, root, files)
			}
			var paths []string
			for _, f := range files {
				paths = append(paths, f.Path)
				checkFile(t, root, f)
			}
			slices.Sort(paths)
			if golden := goldenPaths(t, root); !slices.Equal(golden, paths) {
				t.Errorf("testdata holds %q, and Generate returns %q", golden, paths)
			}
		})
	}
}

// checkFile checks f against its golden file under root, and its path and mode.
func checkFile(t *testing.T, root string, f File) {
	t.Helper()
	if !path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path {
		t.Errorf("%s: not a clean, absolute path", f.Path)
	}
	if f.Mode != 0o644 {
		t.Errorf("%s: mode %v, want -rw-r--r--", f.Path, f.Mode)
	}
	want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
	if err != nil {
		t.Errorf("%v (go test -update writes it)", err)
		return
	}
	if !bytes.Equal(f.Data, want) {
		t.Errorf("%s differs from testdata (go test -update rewrites it):\n%s", f.Path, f.Data)
	}
}

// writeGolden replaces root with files, each under its path.
func writeGolden(t *testing.T, root string, files []File) {
	t.Helper()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// goldenPaths are the files under root, each as the absolute path it stands for, sorted.
func goldenPaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, name)
		paths = append(paths, "/"+filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

// Only the connection reads the profile: every other file is the same for any profile.
func TestOnlyTheConnectionDiffers(t *testing.T) {
	issued, err := Generate(profiles["issued"], testSettings)
	if err != nil {
		t.Fatal(err)
	}
	options, err := Generate(profiles["options"], testSettings)
	if err != nil {
		t.Fatal(err)
	}
	for i := range issued {
		same := bytes.Equal(issued[i].Data, options[i].Data)
		if connection := issued[i].Path == templates[0].Name(); same == connection {
			t.Errorf("%s: the same for both profiles is %v", issued[i].Path, same)
		}
	}
}

// A profile that is not there is an error, not a panic.
func TestGenerateNil(t *testing.T) {
	files, err := Generate(nil, testSettings)
	if err == nil || files != nil {
		t.Fatalf("Generate(nil, testSettings) = %d files, %v; want an error", len(files), err)
	}
	if want := "cannot generate the files: no profile"; err.Error() != want {
		t.Errorf("the error is %q, want %q", err, want)
	}
}

// A template that reads what fields lacks, the profile's key above all, is Generate's error, so
// that no file can carry the key.
func TestGenerateUnknownField(t *testing.T) {
	saved := templates
	t.Cleanup(func() { templates = saved })
	for _, text := range []string{"{{.Key}}", "{{.Profile}}", "{{.Certificate}}", "{{.CA}}"} {
		templates = []*template.Template{template.Must(template.New("/etc/x").Parse(text))}
		files, err := Generate(profiles["issued"], testSettings)
		if err == nil || files != nil {
			t.Errorf("%s: Generate = %d files, %v; want an error", text, len(files), err)
			continue
		}
		if want := "cannot generate /etc/x: "; !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: the error is %q, want it to start with %q", text, err, want)
		}
	}
}

func TestValue(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{"vpn.example.org", "vpn.example.org"},
		{"2001:db8::1", "2001:db8::1"},
		{"client@example.org", "client@example.org"},
		{"aes256-sha256-modp2048", "aes256-sha256-modp2048"},
		{"aes256gcm16-ecp384,aes128gcm16", "aes256gcm16-ecp384,aes128gcm16"},
		{"CN=client,O=Test", "CN=client,O=Test"},
		{"a_client.2-cert.pem", "a_client.2-cert.pem"},
		{"", `""`},
		{"O=Test VPN, CN=client", `"O=Test VPN, CN=client"`},
		// What would end a value, or start a comment or a section, unquoted.
		{"a#b", `"a#b"`},
		{"a}b", `"a}b"`},
		{"a{b", `"a{b"`},
		// What a quoted value escapes: the parser drops a bare carriage return.
		{`a"b`, `"a\"b"`},
		{`a\b`, `"a\\b"`},
		{"a\nb\rc\td", `"a\nb\rc\td"`},
	} {
		if got := value(test.in); got != test.want {
			t.Errorf("value(%q) = %s, want %s", test.in, got, test.want)
		}
	}
}
