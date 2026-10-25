# The checks

The [README](../README.md) says what ikeward does, and [building and testing](development.md) has
the commands besides `make lint`.

`make lint` is the one gate. CI's `lint` job runs it, and so does the maintainer. Any finding fails
it, warnings included, but for one that govulncheck prints and passes ([govulncheck](#govulncheck)),
and go-licenses' warning on `golang.org/x/sys`'s assembly ([licenses](#licenses)).
The lint plugin's hooks run the gates as Claude works ([Claude Code's hooks](#claude-codes-hooks)).

## The gates

`make lint` runs these targets, each a gate of its own. CI runs `make -k lint`, which goes on past
a gate that fails, so its log shows each one that does.

- `vet`: ShellCheck and `shfmt -d -i 4` on the shell scripts, each read as POSIX sh, whatever its
  shebang: `shellcheck -s sh` and `shfmt -ln posix`. Then come gofmt on the Go files git lists and
  `go vet` on the Go module. The scripts are the files git lists that
  `go run ./tools/sizecheck -scripts` finds by their name or their shebang, as the size caps take
  them. It fails where git lists no script. gofmt reads no file git ignores, as one in a work tree
  of Claude Code's under `.claude/worktrees`.
- `golangci-lint`: the linters `.golangci.yml` sets, once `golangci-lint config verify` has
  checked that file.
- `sizecheck`: `tools/sizecheck`, the size caps. A Go, Markdown, YAML or shell file has 300 lines
  at most, a Go comment block 4, and a package comment 15.
  Directives, and the `//` line gofmt puts before a doc comment's directives, count for neither.
- `vale`: `tools/valecheck`, Vale on the Markdown files and on the Go comments, with `.vale.ini`
  alone. It sets three rules, each an error: sentences of 30 words at most, no wordy phrase, no
  needless variant. A comment that turns a gate off fails it too ([a finding](#a-finding)).
- `lychee`: the links to files and to their headings in the Markdown files, offline.
- `rumdl`: the form of the Markdown files, as `.rumdl.toml` sets it. It keeps rumdl's defaults,
  with lines of 100 columns at most, but for code blocks and tables.
- `govulncheck`: the known vulnerabilities in the Go module and its dependencies, and in the
  builds of ikeward for Linux ([govulncheck](#govulncheck)).
- `licenses`: the license of each module that a build of ikeward holds, by go-licenses
  ([licenses](#licenses)).
- `actionlint`: the workflows, and their shell scripts through the pinned ShellCheck. Its pyflakes
  is off: no step runs Python, and a pyflakes on the `PATH` would run unpinned.
- `zizmor`: the workflows' security, with the online audits where `GH_TOKEN` is set, as in CI. It
  runs zizmor's default persona, regular, which reports few false positives. The pedantic persona's
  code smells are left out, and zizmor prints how many it suppressed. An input it cannot parse
  fails it, with `--strict-collection`.
- `gitleaks`: secrets in each commit of `HEAD`'s history, by gitleaks' default rules and the two
  `.gitleaks.toml` adds. It heeds no `gitleaks:allow` comment, and its output redacts what it
  finds. `tools/profilecheck` runs first, for what gitleaks cannot read
  ([a client profile](#a-client-profile)).
- `denycheck`: an employer's terms in the files git lists and in their paths, by
  `tools/denycheck` ([an employer's terms](#an-employers-terms)). `make check` runs it too.
- `denycheck-commits`: `tools/denycheck -history` on what the commits from `origin/main` to
  `HEAD` add: their messages, authors and committers, paths and files.

`sizecheck`, `vale`, `lychee` and `rumdl` take `FILES`, so that they can check a few files alone.
make stops on a `FILES` entry that is not a file. With no `FILES`, each checks every file git lists,
new ones included, but those deleted from the work tree. `lychee`, `rumdl` and `vet`'s gofmt refuse
a name that the shell would split or git would quote, with a space, a tab, a newline, a quote or a
backslash. Each fails where git cannot list them:

```sh
make lint                         # every gate, as CI's lint job runs them
make vale FILES=README.md         # one gate on one file
make sizecheck FILES='a.go b.md'  # or on a few
```

`tools/run` pins each tool's version and four SHA-256 sums, for Linux and macOS on amd64 and
arm64. Its first run downloads the tool into `.cache/tools` and checks it against its sum.
govulncheck and go-licenses run through `go run`, at the version the Makefile names, which Go's
checksum database pins: go-licenses publishes no binaries for `tools/run` to fetch.

`make lint-local` runs the gates of `make lint` but govulncheck, and zizmor with `--offline`. What
it finds changes with the tree alone. govulncheck's findings change with the advisories published,
and zizmor's online audits with GitHub, so CI's `lint` job checks those.

## Claude Code's hooks

The lint plugin of [zadykian/skills](https://github.com/zadykian/skills), which
`.claude/settings.json` enables, runs two targets of the Makefile as Claude works.
[Its docs](https://github.com/zadykian/skills/blob/main/docs/lint.md) say when, and what it passes
over.

- After each Edit or Write, `make lint-file FILES=PATH` runs the gates of the file's type on that
  file, as `make lint-local` runs them, zizmor offline. Then a shell script's checks, the size
  caps, `gitleaks dir` and `tools/denycheck` read it, whatever its type. A change made through
  Bash, as `sed -i` or `gofmt -w`, waits for the end of the turn.
- As Claude ends its turn, `make -k lint-tree`, which is `lint-local`, runs in each work tree that
  Claude wrote, linked ones included. In the project's own, it runs where the tree changed since
  the session started and since the last run, as the plugin notes in its data directory.
- A failure goes back to Claude, which goes on once, then may stop.
- No gate runs in a tree where a commit since `origin/main` lacks its author's good signature,
  where a change is staged, or where the gate code differs from `HEAD`. The gate code is the
  plugin's, the Makefile and `tools/` among it, and what `.claude/lint-gates` adds. A Dependabot
  branch checked out here gets a notice instead, as does an edit of the Makefile until a commit
  takes it.

## A finding

A finding is fixed. Only golangci-lint's and ShellCheck's may be justified in place instead, with
the linter's own comment and a reason: `//nolint:LINTER // reason`, or
`# shellcheck disable=SCNNNN` below a comment that gives the reason. Any other gate's finding is
fixed, Vale's, rumdl's, lychee's, sizecheck's and gitleaks' among them. No severity is lowered, no
exclusion added, and no baseline kept. Every file passes every gate, but for the vendored Vale
styles, which the size caps pass over. sizecheck names `.vale/styles` alone, so a `LICENSE`
elsewhere sets nothing apart.

A comment that turns Vale, rumdl or markdownlint off is refused, as `<!-- vale off -->`,
`<!-- vale Style.Rule = NO -->` or `<!-- rumdl-disable -->`, on one line or across lines.
`tools/valecheck` fails on one in a Markdown file or a Go comment, but for one in a code span or a
fenced code block, as here. A finding of Vale or rumdl is fixed by rewriting the text.

A finding of gitleaks or denycheck is fixed by taking the secret or the term out, of the history
too where a commit holds it. No `gitleaks:allow` comment or `.gitleaksignore` passes one, and no
term leaves the list so that a file passes.

## An employer's terms

`tools/denycheck` keeps an employer's names, hosts, addresses and domains out of the repository.
Its list lives outside it: a committed list would publish the terms, and committed hashes would
confirm a guessed name. A gitleaks rule would read no commit message and no binary.

The list holds a term a line, and passes over blank lines and those that start with `#`.
denycheck reads it from `DENYLIST`, which CI's jobs set from the secret of that name, or else
from `ikeward/denylist` under `XDG_CONFIG_HOME`, `~/.config` by default.

A term matches as a word, in any case. A word ends at a sign that is no letter or digit, between
a letter and a digit, or where camel case starts a word. One starts after an escape too, as `\n`,
`\x0a`, `\u00e9`, `\012` or `%2F`, so a DNS name in wire format or a URL's escaped path is read.
The signs between a term's words match any run of space, `.`, `_`, `-`, `%20` and the term's own
signs, or none. A line break matches there too, with the next line's indent and its mark of a
comment or a quote, as `//`, `#`, `*` or `>`. So a term that prose wraps is found on its first line.
So `acme-widgets` finds `ACME_WIDGETS` and `getAcmeWidgets`, but not `acme-widgetsx`. Between two
numbers, one sign at least must stand: `198.51.100` finds `198.51.100.7`, not `19851100`. A `/24`
range is listed by its first three numbers, which find each of its addresses. A term's words match
as listed, with no sign inside them: `acme widgets` finds `acmewidgets`, but `acmewidgets` finds
no `acme-widgets`.

A finding says where the term is and the term's number, its line in the list, never the term.
A path prints each term it holds as `[term N]`:

- `go run ./tools/denycheck [FILE...]`: the files git lists, new ones included, or those of them
  a FILE names, and their paths. A finding is `PATH:LINE: term N`, or
  `PATH: the path holds term N`.
- `-history REVS`: each object that `git rev-list --objects REVS` lists. A commit or a tag is
  `commit ID:LINE` or `tag ID:LINE`, a line of `git cat-file -p ID`, author and committer
  included. A tree is `tree ID:LINE`, an entry's line in `git ls-tree ID`, and a file
  `PATH@ID:LINE`.
- `-files FILE...`: the FILEs as they are, binaries included, as `PATH:LINE`. Go packs a build's
  strings together, so a term the code holds as a string of its own may touch the next one and go
  unseen. The tree's check finds it in the source, and #47's sweep with `strings` stays.
- `-stdin NAME`: the input, as `NAME:LINE`.

`make check` and `make lint` run it on the tree, `make lint-file` on a file, and
`make denycheck-commits` on what the commits from `origin/main` add. Those targets run it as
`DENYCHECK` says. CI's workflows set `DENYCHECK=true`, a command that does nothing, and run
denycheck in steps of their own. So the secret `DENYLIST` reaches denycheck alone: no test, no tool
and no module that a Dependabot pull request bumps sees it. CI's `pr-text` job runs it on a pull
request's title, body and branch as they change. `release.yml` runs it on each file of the release
and on its notes, before it publishes them. `/flow:land` runs it on a pull request's commits, title,
body and branch before `/ff`, and `make history` on every ref's history.

With no list, denycheck prints a note and passes, so a contributor without the list can work. In
GitHub Actions it fails instead, so that a missing `DENYLIST` passes nothing. A fork's own runs
and a fork's pull request get no secrets, and pass with the note. With `-require`, as
`/flow:land` runs it, it fails without a list anywhere. Its exit status is 1 for a finding. A
mistake, a list it cannot read and a list of no term exit 2.

## The history job

`.github/workflows/history.yml` runs `make history` each Monday, and on demand:
`tools/profilecheck --all`, gitleaks over `--all`, and `tools/denycheck -history --all`. It reads
every branch and tag whole. So a rule or a term added later finds what landed before it, and what
lies on a branch that never landed.

## Vale on Go comments

Vale checks Go comments in Markdown mirrors that `tools/valemirror` writes, line for line. Their
alerts come back with the Go file's lines and columns. Vale 3.23.0 reads Go itself, through
tree-sitter. In a probe here, it flagged no sentence of 40 words in a Go comment, a doc comment's
first or any other.

A mirror holds the comments' prose alone, without their markers. Code, directives and the code
blocks of comments stay blank lines. A mirror masks the name a doc comment starts with, as Vale
masks code, and its sentence still counts the name.

A Go comment is plain text, and its mirror is Markdown, so a mirror writes a stand-in of one rune
for a sign that Markdown would read. Vale counts each stand-in as it counts the sign:

- `¤` for `$`. Vale 3.23.0 skips the text between two `$` of a paragraph, across lines too: a
  sentence of 40 words between `$A` and `($)` passed. A Markdown file goes to Vale with `¤` for `$`
  too, in a copy.
- `‹` for `<`, which would open a tag or an HTML comment. A `<pre>` would hide every later
  comment of the file from Vale, and a `<value>` ends a sentence for it.
- `'` for a backtick that pairs with none on its line, and for each of Go's two backticks that
  open a quote, which two apostrophes close. Either would pair with a later backtick, and hide the
  text between as code. A code span on one line stays one.

In plain text, a Go comment or Markdown outside a code span, Vale reads no sentence break after a
dotted name or address, such as `files.Link` or `10.88.0.0/16`. A sentence that ends on one runs
on into the next where that one starts with a lowercase word, a camel-case identifier or a code
span. One that starts with an ordinary word, as "The", is a sentence of its own. A code span in
Markdown, or a doc link in Go, as `[files.Link]`, ends a sentence as a word does.

## Licenses

`make licenses` runs go-licenses' `check` on `./cmd/ikeward`, as built for Linux. Each module that
a build holds must be under a license of the Makefile's `LICENSES`, all permissive. Its warning
that it cannot read `golang.org/x/sys`'s assembly is no finding, as assembly imports nothing.

`make notices` writes `dist/third-party-notices.txt`: each module's name, version, license and
the license's text, through `tools/notices.tmpl`, then Go's own, for its standard library. CI's
`check` job makes it, and each release publishes it beside the binaries.

## govulncheck

govulncheck runs three times. The first run, with `-show verbose`, prints every finding, and fails
on one whose trace reaches a function ikeward calls. The others, with `-mode binary -scan package`,
read a build of ikeward each, for linux/amd64 and linux/arm64, as `make build` makes them. Each
fails on a finding in a package its build holds, on that platform: an advisory for Windows alone
passes. A finding in a module ikeward requires alone is printed, and passes.

GO-2026-5932, in `golang.org/x/crypto/openpgp`, is such a finding. That package is unmaintained,
and will never be fixed. ikeward never imports it: `go-pkcs12` requires `golang.org/x/crypto` for
pbkdf2.

A finding in the standard library is settled by a bump of Go: the `go` line of `go.mod`, which
every job of CI and of a release builds with.

## A client profile

gitleaks reads text alone, so it never reads a client profile's `.p12`, or an archive. None of its
default rules reads the `p12` field of a `.sswan` either, which holds the client's key and
certificate. So `.gitleaks.toml` adds a rule for that field, its Base64 on one line or wrapped as
ikev2setup.sh writes it: at 52 characters, the lines joined by `\n` escapes. Its `pem-block` rule
finds a PEM block of any kind, as a certificate names its owner and a key is secret.

`tools/profilecheck` reads the files git lists and the paths of `HEAD`'s history, every ref's with
`--all`. It fails on a binary file, which gitleaks does not read, and on a path with one of these
extensions, in any case, which `.gitignore` ignores:

- keys: `.key`, `.pem`, `.ppk`, `.p8`, `.pk8`, `.jks`, `.keystore`, `.kdbx`, `.gpg`, `.pgp` and
  `.asc`;
- certificates: `.crt`, `.cer`, `.der`, `.csr`, `.p7b` and `.p7c`;
- archives: `.zip`, `.tar`, `.tgz`, `.gz`, `.bz2`, `.xz`, `.zst`, `.7z` and `.rar`;
- a client profile's files: `.p12`, `.pfx`, `.sswan` and `.mobileconfig`.

It fails on a `.gitleaksignore` at the root too, whose findings gitleaks would pass over. A file
that holds a bare password still passes, as no rule can tell one.

## The runner labels

`.github/actionlint.yaml` declares the runner label `ubuntu-26.04`, on which the integration test
runs. GitHub hosts it, but actionlint 1.7.12 doesn't know it yet.

## Every file

Each file git tracks is a source, a doc, a config with its own check, or data:

- Go files: vet, golangci-lint, the size caps, and Vale on their comments. `go.mod` and `go.sum`
  go through the Go tools and govulncheck.
- Markdown files: Vale, lychee, rumdl and the size caps.
- Shell scripts: ShellCheck, shfmt and the size caps.
- Workflows: actionlint, zizmor and the size caps. `.github/dependabot.yml`: zizmor and the size
  caps.
- `.golangci.yml`: golangci-lint's own check. `.github/actionlint.yaml` and `.rumdl.toml` are read
  by their tools, which fail on an error in them. Every YAML file has the size caps.
- `.gitleaks.toml`: gitleaks fails on a broken rule, but skips an unknown key or table without a
  word. A misspelled `[extend]` or `useDefault` drops its default rules. `tests/tools`'
  `TestGitleaks` catches that: it needs a finding of each of the file's rules, `sswan-p12` and
  `pem-block`, and of a default rule, `private-key`.
- `.vale.ini`: Vale fails on a broken one, but skips a rule it cannot find, misspelled or gone,
  without a word. `tools/valemirror`'s gate test catches that: each of the three rules must flag a
  sentence of a Markdown file and of a Go comment.
- The Vale styles in `.vale/styles` are vendored, each with its license, and the size caps pass
  over them. A bump replaces them whole.
- `tests/integration`'s `Dockerfile`, `compose.yaml` and the server's settings: the integration
  test builds and runs them.
- `internal/files/testdata`: the files `Generate` must write, which its tests compare byte for
  byte.
- `tools/notices.tmpl`: go-licenses reads it as `make notices` runs, in CI's `check` job.
- `Makefile`, `.gitignore`, `.editorconfig`, `.claude/settings.json`, `.claude/lint-gates`, the
  issue forms in `.github/ISSUE_TEMPLATE` and the licenses: data, or read by the tool they are for,
  GitHub for the forms. `tests/tools` runs the Makefile's recipes that list files, `lint-file`'s
  among them.
