package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// matchTests are texts, and whether a term of the list matches in each, as a word.
var matchTests = []struct {
	term  string
	match []string
	miss  []string
}{
	{
		term: "acme-widgets",
		match: []string{"acme-widgets", "ACME_WIDGETS", "acme widgets", "acme.widgets",
			"acme%20widgets", "acme -_ widgets", "acmewidgets", "AcmeWidgets", "getAcmeWidgets()",
			"ACMEWidgetsList", "acmeWIDGETSRules", "x/acme-widgets/y", "acme-widgets2", "MAKE_ACME_WIDGETS",
			"acme\nwidgets", "made for Acme\r\n  Widgets", "// for acme\n\t// widgets", "# acme\n# widgets",
			"> acme\n> widgets", " * acme\n * widgets", "a\nacme-widgets", "\\nacme-widgets",
			`\x0aacme widgets\x02ru`, "https%3A%2F%2Facme-widgets", `\u00e9acme_widgets`,
			`\101acmewidgets`, `"\tacme widgets`},
		miss: []string{"acme/widgets", "acme, widgets", "xacme-widgets", "acme-widgetsx",
			"ACMEWIDGETSX", "acme-widget", "acme\n// x widgets", "\\qacme-widgets", "%2Gacme-widgets"},
	},
	{
		term:  "198.51.100",
		match: []string{"198.51.100.7", "198.51.100.0/24", "198-51-100", "v198.51.100", "198 51 100"},
		miss:  []string{"19851100", "198.51.1000", "1198.51.100", "198.511.00"},
	},
	{
		term: "Contoso",
		match: []string{"contoso", "CONTOSO", "contoso-api", "myContoso", "@contoso.example",
			`a\ncontoso`, `\x0acontoso\x02ru`, "https%3A%2F%2Fcontoso", "\x7fELF\x00contoso\x00"},
		miss: []string{"contosos", "mycontoso", "\x7fELF\x00no such processcontosomething\x00"},
	},
	{
		term:  "fabrikam%20labs",
		match: []string{"fabrikam labs", "fabrikam%20labs", "FabrikamLabs", "Fabrikam-Labs"},
	},
	{
		term:  "~host.example/7",
		match: []string{"host.example/7", "host-example_7", "hostexample7", "a~host.example/7"},
		miss:  []string{"host.example:7"},
	},
	{term: "Контосо", match: []string{"контосо", "КОНТОСО-1"}, miss: []string{"неконтосо"}},
}

func TestMatch(t *testing.T) {
	t.Parallel()
	for _, tt := range matchTests {
		terms, err := parseList(tt.term)
		if err != nil {
			t.Fatalf("%s: %v", tt.term, err)
		}
		for _, text := range tt.match {
			if len(places(terms, []byte(text))) == 0 {
				t.Errorf("%q does not match %q, want it to", tt.term, text)
			}
		}
		for _, text := range tt.miss {
			if hits := places(terms, []byte(text)); len(hits) != 0 {
				t.Errorf("%q matches %q at %v, want no match", tt.term, text, hits)
			}
		}
	}
}

// listErrorTests are lists that parseList refuses, and its error, which never names a term.
var listErrorTests = map[string]string{
	"":                      "the list holds no term",
	"# acme-widgets\n\n \n": "the list holds no term",
	"Contoso\n --- \n":      "term 2: it holds no letter or digit",
}

func TestParseListErrors(t *testing.T) {
	t.Parallel()
	for list, want := range listErrorTests {
		if _, err := parseList(list); err == nil || err.Error() != want {
			t.Errorf("parseList(%q) = %v, want %q", list, err, want)
		}
	}
}

// loadTests are environments, and the list that load finds in each: the numbers of its terms,
// or else its note or its error. $DIR stands for a directory that holds ikeward/denylist.
var loadTests = []struct {
	name                 string
	env                  map[string]string
	require              bool
	terms                int
	note, err, eventJSON string
}{
	{name: "DENYLIST", env: map[string]string{"DENYLIST": list, "XDG_CONFIG_HOME": "$DIR"}, terms: 4},
	{name: "XDG_CONFIG_HOME's file", env: map[string]string{"XDG_CONFIG_HOME": "$DIR"}, terms: 1},
	{name: "HOME's file", env: map[string]string{"DENYLIST": " \n", "HOME": "$HOME"}, terms: 1},
	{
		name: "no list", env: map[string]string{"HOME": "/nonexistent"},
		note: "no list in DENYLIST or /nonexistent/.config/ikeward/denylist: nothing checked",
	},
	{name: "no HOME", env: map[string]string{}, note: "no list in DENYLIST, and no HOME"},
	{
		name: "no list, required", env: map[string]string{"HOME": "/nonexistent"}, require: true,
		err: "no list in DENYLIST or /nonexistent/.config/ikeward/denylist: this run requires one",
	},
	{
		name: "no list in CI", env: map[string]string{"GITHUB_ACTIONS": "true"},
		err: "no list in DENYLIST, and no HOME for ~/.config/ikeward/denylist: this run requires",
	},
	{
		name: "no list in CI, for a pull request", env: map[string]string{"GITHUB_ACTIONS": "true"},
		eventJSON: `{"pull_request": {"head": {"repo": {"fork": false}}}}`, err: "requires one",
	},
	{
		name:      "no list in CI, for a fork's pull request",
		env:       map[string]string{"GITHUB_ACTIONS": "true"},
		eventJSON: `{"pull_request": {"head": {"repo": {"fork": true}}}}`, note: "nothing checked",
	},
	{
		name: "no list in a fork's CI", env: map[string]string{"GITHUB_ACTIONS": "true"},
		eventJSON: `{"repository": {"fork": true}}`, note: "nothing checked",
	},
	{
		name: "no list in CI, an event it cannot read", env: map[string]string{"GITHUB_ACTIONS": "true"},
		eventJSON: `{"repository": `, err: "requires one",
	},
	{name: "a list of no term", env: map[string]string{"DENYLIST": "# a\n"}, err: "DENYLIST: the"},
}

func TestLoad(t *testing.T) {
	t.Parallel()
	for _, tt := range loadTests {
		dir, home := t.TempDir(), t.TempDir()
		for _, root := range []string{filepath.Join(dir, "ikeward"),
			filepath.Join(home, ".config", "ikeward")} {
			write(t, root, map[string]string{"denylist": "Contoso\n"})
		}
		env := map[string]string{}
		for name, value := range tt.env {
			env[name] = strings.NewReplacer("$DIR", dir, "$HOME", home).Replace(value)
		}
		if tt.eventJSON != "" {
			env["GITHUB_EVENT_PATH"] = filepath.Join(dir, "event.json")
			write(t, dir, map[string]string{"event.json": tt.eventJSON})
		}
		terms, note, err := load(environ(env), tt.require)
		switch {
		case len(terms) != tt.terms:
			t.Errorf("%s: %d terms, note %q, error %v; want %d", tt.name, len(terms), note, err,
				tt.terms)
		case tt.err == "" && err != nil, tt.err != "" && (err == nil || !strings.Contains(err.Error(),
			tt.err)):
			t.Errorf("%s: error %v, want %q in it", tt.name, err, tt.err)
		case !strings.Contains(note, tt.note) || (tt.note == "") != (note == ""):
			t.Errorf("%s: note %q, want %q in it", tt.name, note, tt.note)
		}
	}
}

// TestLoadUnreadable checks a list's file that cannot be read: here, a directory.
func TestLoadUnreadable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ikeward", "denylist"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := load(environ(map[string]string{"XDG_CONFIG_HOME": dir}), false)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error %v, want one of reading a directory", err)
	}
}
