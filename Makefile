# The version make build stamps into ikeward.
VERSION ?= dev
# The platforms make build builds ikeward for, as dist/ikeward-linux-ARCH.
PLATFORMS = linux/amd64 linux/arm64
BUILD = CGO_ENABLED=0 go build -trimpath -ldflags '-X main.version=$(VERSION)'
# The files that sizecheck, vale, lychee and rumdl check, every one when empty:
# make vale FILES='README.md'. make stops on one that is not a file, which sizecheck would pass
# over as one git does not list.
FILES =
ifneq ($(strip $(FILES)),)
NOTFILES := $(shell for file in $(FILES); do [ -f "$$file" ] || printf '%s ' "$$file"; done)
ifneq ($(strip $(NOTFILES)),)
$(error FILES names what is not a file: $(strip $(NOTFILES)))
endif
endif
# Sets the shell's arguments to the files git lists that match the pattern $(1), new ones included,
# but those gone from disk. It fails where git cannot list them, or lists a name that the loop
# cannot pass on whole (WHOLE).
LISTED = files=$$(git -c core.quotePath=false ls-files --cached --others --exclude-standard \
	$(1)) && $(WHOLE) && set -- && \
	for file in $$files; do [ ! -e "$$file" ] || set -- "$$@" "$$file"; done
# Fails on a name in $$files, a line each, that the shell would split or git quotes: one with a
# space, or with a tab, a newline, a quote or a backslash. A non-ASCII name goes as it is.
WHOLE = { ! printf '%s\n' "$$files" | grep -e ' ' -e '^"' >&2 || \
	{ echo 'make: a name above holds a space, or a sign git quotes' >&2; false; }; }
# Sets the shell's arguments to the Markdown files lychee and rumdl check: those of FILES, or every
# one git lists.
MARKDOWN = $(if $(FILES),set -- $(filter %.md,$(FILES)),$(call LISTED,'*.md'))
GOVULNCHECK = go run golang.org/x/vuln/cmd/govulncheck@v1.8.0
# go-licenses publishes no binaries for tools/run to pin, so Go's checksum database pins it, as
# govulncheck. It runs on the host and reads the packages as built for Linux. LICENSES are those
# that a module a build holds may be under (docs/checks.md).
GO_LICENSES = go run -exec 'env GOOS=linux' github.com/google/go-licenses/v2@v2.0.1
LICENSES = Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT
# The command of the gates that run tools/denycheck. CI's workflows run make with DENYCHECK=true,
# a command that does nothing, and denycheck in steps of their own, which alone get the secret
# DENYLIST: no test or tool that make runs sees it (docs/checks.md).
DENYCHECK = go run ./tools/denycheck
# gitleaks' scan, with the rules of .gitleaks.toml, and no gitleaks:allow comment heeded.
GITLEAKS = tools/run gitleaks git --config=.gitleaks.toml --ignore-gitleaks-allow --no-banner \
	--redact --verbose

.PHONY: check lint lint-local lint-file lint-tree vet golangci-lint sizecheck vale lychee rumdl \
	govulncheck licenses actionlint zizmor gitleaks denycheck denycheck-commits history test \
	integration build notices

# The checks and the tests that CI's check job runs; each fails on any finding.
check: vet golangci-lint denycheck test

# Every gate, as CI's lint job runs them, each failing on any finding, warnings included.
# tools/run downloads the tools pinned there into .cache/tools.
lint: vet golangci-lint sizecheck vale lychee rumdl govulncheck licenses actionlint zizmor \
	gitleaks denycheck denycheck-commits

# The gates of lint whose findings change with the tree alone, not with the date or the network:
# all but govulncheck, with zizmor offline. The lint plugin's Stop hook runs it, through lint-tree
# (docs/checks.md).
lint-local: ZIZMOR_FLAGS = --offline
lint-local: vet golangci-lint sizecheck vale lychee rumdl licenses actionlint zizmor gitleaks \
	denycheck denycheck-commits

# The gates of each file of FILES, as the lint plugin runs them after Claude edits a file: those
# of its type, as lint-local runs them on every file, zizmor offline. Then, whatever its type, a
# shell script's checks, found by its name or its shebang as vet finds them, the size caps,
# gitleaks and denycheck. gitleaks heeds no gitleaks:allow comment and reads no .gitleaksignore.
lint-file:
	@status=0; for file in $(FILES); do \
		case $$file in \
		*.go) test -z "$$(gofmt -l "$$file" 2>&1)" || { gofmt -d "$$file"; status=1; }; \
			tools/run golangci-lint run --allow-serial-runners "./$$(dirname "$$file")/" || \
				status=1; \
			$(MAKE) --no-print-directory vale FILES="$$file" || status=1 ;; \
		*.md) $(MAKE) -k --no-print-directory vale lychee rumdl FILES="$$file" || status=1 ;; \
		.github/workflows/*.yml | .github/workflows/*.yaml) \
			{ shellcheck="$$(tools/run -path shellcheck)" && tools/run actionlint \
				-shellcheck="$$shellcheck" -pyflakes= "$$file"; } || status=1; \
			tools/run zizmor --strict-collection --offline "$$file" || status=1 ;; \
		.golangci.yml) tools/run golangci-lint config verify || status=1 ;; \
		esac; \
		if [ -n "$$(go run ./tools/sizecheck -scripts "$$file" 2>/dev/null)" ]; then \
			tools/run shellcheck -s sh "$$file" || status=1; \
			tools/run shfmt -ln posix -d -i 4 "$$file" || status=1; \
		fi; \
		$(MAKE) --no-print-directory sizecheck FILES="$$file" || status=1; \
		tools/run gitleaks dir --config=.gitleaks.toml --ignore-gitleaks-allow \
			--gitleaks-ignore-path=/dev/null --no-banner --redact --verbose "$$file" || status=1; \
		$(DENYCHECK) "$$file" || status=1; \
	done; exit $$status

# The gates of lint-local, as the lint plugin runs them, with -k, as Claude ends its turn.
# denycheck-commits reads origin/main itself, so BASE goes unread.
lint-tree: lint-local

# The shell scripts are those that sizecheck finds by their name or their shebang among the files
# git lists, new ones included, as it checks their size. ShellCheck and shfmt read each as POSIX
# sh, whatever its shebang. gofmt reads the Go files git lists, and so not Claude Code's work trees
# in .claude/worktrees, which git ignores. It fails where git cannot list the files, or lists no
# script.
vet:
	scripts=$$(go run ./tools/sizecheck -scripts) && \
		tools/run shellcheck -s sh $$scripts && tools/run shfmt -ln posix -d -i 4 $$scripts
	@$(call LISTED,'*.go') && { [ $$# -eq 0 ] || test -z "$$(gofmt -l "$$@")" || \
		{ gofmt -d "$$@"; exit 1; }; }
	go vet ./...

# The version tools/run pins, with the linters .golangci.yml sets, and the file checked against
# golangci-lint's schema.
golangci-lint:
	tools/run golangci-lint config verify
	tools/run golangci-lint run ./...

# The size caps (tools/sizecheck): 300 lines a file.
sizecheck:
	go run ./tools/sizecheck $(FILES)

# Vale on the Markdown files, and on the Go comments through their mirrors (tools/valecheck).
vale:
	tools/valecheck $(FILES)

# Links to files and their headings, offline.
lychee:
	$(MARKDOWN) && { [ $$# -eq 0 ] || \
		tools/run lychee --offline --include-fragments --no-progress "$$@"; }

# Markdown's form, as .rumdl.toml sets it.
rumdl:
	$(MARKDOWN) && { [ $$# -eq 0 ] || \
		tools/run rumdl check --no-cache --deny-config-warnings "$$@"; }

# A finding fails where ikeward calls the vulnerable function, or where a build of ikeward holds its
# package: one for each of PLATFORMS, as make build makes them. A finding in a module ikeward
# requires alone is printed without failing, as GO-2026-5932 in golang.org/x/crypto/openpgp is:
# go-pkcs12 requires x/crypto for pbkdf2.
govulncheck:
	$(GOVULNCHECK) -show verbose ./...
	tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		for platform in $(PLATFORMS); do \
			GOOS=$${platform%/*} GOARCH=$${platform#*/} \
				$(BUILD) -o "$$tmp/ikeward-$${platform%/*}-$${platform#*/}" ./cmd/ikeward && \
			$(GOVULNCHECK) -mode binary -scan package \
				"$$tmp/ikeward-$${platform%/*}-$${platform#*/}" || exit 1; \
		done

# The licenses of the modules that a build of ikeward holds: each is one of LICENSES. Its warning
# that it cannot read golang.org/x/sys's assembly is no finding, as assembly imports nothing.
licenses:
	$(GO_LICENSES) check ./cmd/ikeward --allowed_licenses=$(LICENSES)

# The workflows' shell scripts go through the pinned ShellCheck; .github/actionlint.yaml names the
# runner labels actionlint does not know. pyflakes is off: no step runs Python, and a pyflakes on
# the PATH would run unpinned.
actionlint:
	shellcheck="$$(tools/run -path shellcheck)" && \
		tools/run actionlint -shellcheck="$$shellcheck" -pyflakes=

# The workflows, with the online audits where GH_TOKEN is set, as in CI. zizmor runs its default
# persona, regular, which reports few false positives; the pedantic persona's code smells are left
# out. An input it cannot parse fails it. lint-local sets ZIZMOR_FLAGS to --offline.
zizmor:
	tools/run zizmor --strict-collection $(ZIZMOR_FLAGS) .

# Secrets in each commit of HEAD's history, by gitleaks' rules and those of .gitleaks.toml, with
# what it finds redacted. gitleaks reads no binary file, so tools/profilecheck first fails on one,
# and on the paths of keys, certificates, archives and a client profile's files, such as a .p12.
gitleaks:
	tools/profilecheck
	git rev-parse -q --verify HEAD >/dev/null || \
		{ echo 'make gitleaks: no HEAD to scan' >&2; exit 1; }
	$(GITLEAKS) --log-opts='--full-history HEAD' .

# An employer's terms in the files git lists and their paths, with the list outside the
# repository (tools/denycheck). Without a list, it passes with a note, and fails in CI alone
# (docs/checks.md).
denycheck:
	$(DENYCHECK)

# The employer's terms in what the commits from origin/main to HEAD add: their messages, authors
# and committers, paths and files.
denycheck-commits:
	$(DENYCHECK) -history origin/main..HEAD

# gitleaks, tools/profilecheck and tools/denycheck over every commit of every ref, as CI's weekly
# history job runs them.
history:
	tools/profilecheck --all
	$(GITLEAKS) --log-opts='--full-history --all' .
	$(DENYCHECK) -history --all

test:
	go test -count=1 ./...

# The VPN end to end in Docker Compose, with a privileged container: tests/integration. It needs
# the kernel's modules for the tunnel loaded, as CI's integration job loads them.
integration:
	IKEWARD_INTEGRATION=1 go test -count=1 -v -timeout 20m ./tests/integration

# With cgo off, any host cross-compiles a static binary for each platform. ikeward.sha256 lists
# them as `HASH  NAME` lines, which install.sh relies on. The installer goes with the binaries it
# downloads, outside ikeward.sha256.
build:
	rm -rf dist && mkdir dist
	set -e; for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
			$(BUILD) -o dist/ikeward-$${platform%/*}-$${platform#*/} ./cmd/ikeward; \
	done
	cd dist && { sha256sum ikeward-* 2>/dev/null || shasum -a 256 ikeward-*; } >ikeward.sha256
	cp install.sh dist/

# dist/third-party-notices.txt, which each release publishes: the license of each module that a
# build of ikeward holds, then Go's own, for its standard library.
notices:
	mkdir -p dist
	$(GO_LICENSES) report ./cmd/ikeward --ignore github.com/zadykian/ikeward \
		--template tools/notices.tmpl >dist/third-party-notices.txt.tmp
	{ printf '%080d\n' 0 | tr 0 = && \
		printf "Go's standard library %s, BSD-3-Clause\n\n" "$$(go env GOVERSION)" && \
		cat "$$(go env GOROOT)/LICENSE"; } >>dist/third-party-notices.txt.tmp
	mv dist/third-party-notices.txt.tmp dist/third-party-notices.txt
