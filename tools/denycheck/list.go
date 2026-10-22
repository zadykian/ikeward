package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// A term is one of the list's: its line in the list, which findings print, and how it matches.
type term struct {
	n  int
	re *regexp.Regexp
}

// listPath returns the list's file, under XDG_CONFIG_HOME or else ~/.config, and "" where neither
// is set.
func listPath(getenv func(string) string) string {
	if dir := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "ikeward", "denylist")
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		return filepath.Join(home, ".config", "ikeward", "denylist")
	}
	return ""
}

// load returns the terms of DENYLIST, or else of the list's file. Where there is neither, it
// returns no terms and a note. It fails instead where a list is required: by -require, or by CI
// outside a fork, which gets no secrets.
func load(getenv func(string) string, require bool) ([]term, string, error) {
	if list := getenv("DENYLIST"); strings.TrimSpace(list) != "" {
		terms, err := parseList(list)
		if err != nil {
			return nil, "", fmt.Errorf("DENYLIST: %w", err)
		}
		return terms, "", nil
	}
	path := listPath(getenv)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		terms, err := parseList(string(data))
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", path, err)
		}
		return terms, "", nil
	case path != "" && !errors.Is(err, os.ErrNotExist):
		return nil, "", err
	}
	where := "no list in DENYLIST or " + path
	if path == "" {
		where = "no list in DENYLIST, and no HOME for ~/.config/ikeward/denylist"
	}
	if require || ci(getenv) {
		return nil, "", errors.New(where + ": this run requires one, as the secret DENYLIST")
	}
	return nil, where + ": nothing checked; CI and the maintainer check with the list", nil
}

// ci returns whether GitHub Actions runs denycheck outside a fork: neither in a fork's own
// repository nor for a fork's pull request. An event it cannot read counts as no fork's.
func ci(getenv func(string) string) bool {
	if getenv("GITHUB_ACTIONS") != "true" {
		return false
	}
	data, err := os.ReadFile(getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		return true
	}
	var event struct {
		Repository struct {
			Fork bool `json:"fork"`
		} `json:"repository"`
		PullRequest struct {
			Head struct {
				Repo struct {
					Fork bool `json:"fork"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if json.Unmarshal(data, &event) != nil {
		return true
	}
	return !event.Repository.Fork && !event.PullRequest.Head.Repo.Fork
}

// parseList returns the terms of a list, a term a line. Blank lines, and those that start with #,
// are passed over. An error names a term by its line alone.
func parseList(list string) ([]term, error) {
	var terms []term
	for i, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, err := compile(line)
		if err != nil {
			return nil, fmt.Errorf("term %d: %w", i+1, err)
		}
		terms = append(terms, term{n: i + 1, re: re})
	}
	if len(terms) == 0 {
		return nil, errors.New("the list holds no term")
	}
	return terms, nil
}

// separators are the signs that may stand between two words of a term in a text, besides the
// term's own, as %20 may too.
const separators = " \t._-"

// lineBreak may stand between two words of a term too, where prose wraps. A line break comes
// first, then the next line's indent and its mark of a comment or a quote, if any.
const lineBreak = `\r?\n[ \t]*(?:(?://+|#+|\*+|>+|--|;+)[ \t]*)?`

// compile returns the expression of a term. Its words, the runs of letters and digits, match in
// any case, and the signs between them as any run of separators or none. Between two numbers, a
// run of one at least keeps them apart.
func compile(s string) (*regexp.Regexp, error) {
	s = strings.ReplaceAll(s, "%20", " ")
	var words, between []string
	word, signs := "", ""
	for _, r := range s + " " {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if word == "" && signs != "" && len(words) > 0 {
				between = append(between, signs)
			}
			word, signs = word+string(r), ""
			continue
		}
		if word != "" {
			words, word = append(words, word), ""
		}
		signs += string(r)
	}
	if len(words) == 0 {
		return nil, errors.New("it holds no letter or digit")
	}
	var b strings.Builder
	b.WriteString("(?i)")
	for i, w := range words {
		if i > 0 {
			b.WriteString(separator(between[i-1], digit(words[i-1], true) && digit(w, false)))
		}
		b.WriteString(regexp.QuoteMeta(w))
	}
	return regexp.Compile(b.String())
}

// separator returns the expression between two words, whose own signs are signs: a run of
// separators, of one at least where apart is true.
func separator(signs string, apart bool) string {
	alternatives := []string{"[" + regexp.QuoteMeta(separators) + "]", "%20", lineBreak}
	for _, r := range signs {
		if !strings.ContainsRune(separators, r) {
			alternatives = append(alternatives, regexp.QuoteMeta(string(r)))
		}
	}
	if apart {
		return "(?:" + strings.Join(alternatives, "|") + ")+"
	}
	return "(?:" + strings.Join(alternatives, "|") + ")*"
}

// digit returns whether the word w ends in a digit, or starts with one where last is false.
func digit(w string, last bool) bool {
	r := []rune(w)
	if last {
		return unicode.IsDigit(r[len(r)-1])
	}
	return unicode.IsDigit(r[0])
}
