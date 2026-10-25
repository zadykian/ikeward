# CLAUDE.md

`ikeward` keeps a Linux server on an IKEv2 VPN, from the client profile that hwdsl2's
`ikev2setup.sh` makes: strongSwan, the remote network's DNS, the routing and a watchdog. Every
packet goes to the server's own networks first, then to the remote network's through the tunnel,
then by the default route. A container's and a forwarded network's do too. The remote network's
ranges are learned from what the VPN server grants, and its resolver, domains and floors come from
`install`'s flags. While the tunnel is down, the floors keep the remote network's traffic off the
uplink.
The Go program, on cobra, is laid out like [cld](https://github.com/zadykian/cld). An operator
installs a release with `install.sh` and runs it as root. The repository is public: it names no
employer and holds no secret, as [writing.md](.claude/rules/writing.md) says.

## Layout

- `cmd/ikeward`: the command line, a file per command with its help text, `install`'s flags in
  `flags.go`, and `main`. `install` refuses a `--floor` that `routing.Refusal` refuses with the VPN
  server's address as `install` finds it: the profile's, or what the system's resolver gives for
  its name.
- `internal/...`: the rest, a package per concern, each reaching the system through small
  interfaces.
- `internal/profile`: reads the profile's files, as `docs/profile.md` says: the `.sswan`, the
  `.p12`, and the `.mobileconfig` for the CA where neither the `.p12` nor the `.sswan`'s
  `remote.cert` holds it. `install` takes each by its own flag, and no archive.
- `internal/files`: generates from the profile the files `install` writes. They are the swanctl
  connection, the strongSwan drop-in, networkd's `ikeward0`, the watchdog's units and
  `ikeward-routing.service`. `ikeward0` is the tunnel's XFRM interface, whose ID, `InterfaceID`, the
  connection's SAs take. The connection asks for `0.0.0.0/0`, which the VPN server narrows to the
  remote network's ranges. The drop-in sets `routing_table = 0`: charon makes no rule. The unit runs
  `ikeward routing` before `network-pre.target` and strongSwan, after `nftables.service`, with no
  default dependencies. `SettingsFile`, `/var/lib/ikeward/settings`, holds `Settings`: the remote
  network's resolver, domains, probe and floors, from `install`'s flags. The files in `testdata/`
  are those of two profiles. `Credentials` gives the certificate, the CA and the key, `0600`, in
  PEM.
- `internal/routing`: `routing`, which `ikeward-routing.service` runs as the server boots. Through
  netlink, it adds `Rules` where missing, `32000: lookup main suppress_prefixlength 0` and
  `32001: lookup 220 suppress_prefixlength 0`, then removes any other rule of table 220, as
  strongSwan's at 220. It makes the floors, unreachable routes in table 220 at metric 4000: one for
  each floor `ReadSettings` reads, and one for each range `Learned` records. `Learned` is
  `/var/lib/ikeward/learned-ranges`, which `updown` writes, with the VPN server's addresses.
  `Refusal` refuses a range wider than `/8`, or one that holds such an address, and the floors pass
  over it, a `--floor` among them where it holds an address `updown` recorded after `install`. It
  makes the table `ip ikeward` anew, in one transaction, with a postrouting NAT chain at priority
  99, before Docker's, and one rule: `oifname "ikeward0" counter masquerade`. Where a part fails, it
  makes the others, then fails. Tests fake `Kernel`, `NAT` and `Files`.
- `internal/install`: `install` and `uninstall`, with `--dry-run`. `install` takes each of its
  seven steps, from `systemctl daemon-reload` to the watchdog's timer, only where due. That's where
  it changes a file the step reads, the journal owes it, or the step's probe finds the system needs
  it. The probes read the units through `systemctl show`, and `ikeward0` and its address. Then
  `status`'s rule, floor, NAT and DNS checks, and the connection and certificates charon loaded.
  `/var/lib/ikeward/pending-steps`, the journal, names the steps due from before `install` changes
  a file until it ends, for a rerun where it stopped half way. v0.2.0's
  `/var/lib/ikeward/pending-restarts` owes every step. `install` restarts the routing's unit before
  strongSwan starts. `uninstall` stops strongSwan, then the routing's unit, then removes
  `ip ikeward`, the rules and table 220's routes. The files `/var/lib/ikeward/installed` lists go
  with `routing.Learned` and both journals, and `ikeward0` last. Tests fake `Files`, `Runner`,
  `Tables`, `Charon` and `Network`, which adds links, and the removal of rules and routes, to
  `status.Network`.
- `internal/status`: `status`, the watchdog's checks, read-only. They read the IKE SA and the CHILD
  SA through VICI, and through netlink, the address on `ikeward0`, the rules, and table 220's routes
  through `ikeward0`. The route check wants one for each range of the CHILD SA's remote traffic
  selectors but those `routing.Refusal` refuses, with the server's address VICI gives. The resolver
  check then asks the settings' resolver for their probe, out `ikeward0` alone, and again each
  second until it answers or 5 s pass. It, the DNS and the floor checks fail where
  `routing.ReadSettings` does, keeping its error. The check's `Result.Err` keeps `Lookup`'s error,
  which wraps `ErrAnswer` where the resolver answers with an error code or no address. The DNS check
  reads resolved's settings of `ikeward0` from `resolvectl`, and its own address through netlink.
  The floor check reads table 220 through netlink, wants a floor for each range `routing.Floors`
  names, and lists those `routing.Learned` records. Last, the NAT check reads `ip ikeward`, and its
  counter, and says what a forwarded network loses only on a `routing.Missing`. Tests fake `Tunnel`,
  `Network`, `Resolver`, `Runner`, `Files` and `NAT`.
- `internal/watchdog`: `watchdog`, status's checks. Where the rule, floor or NAT check fails, it
  makes that part again as `routing` does, logs it and checks again; where the kernel refuses, it
  goes on, then fails itself. Where a check fails that a restart can mend, it runs
  `systemctl restart --no-block strongswan.service`. A restart mends none of the DNS, rule, floor
  and NAT checks, nor the resolver check where the resolver answers with an error code or no
  address. Nor does it mend a check that cannot read the settings. It restarts at most once in 10
  minutes, as `/var/lib/ikeward/last-restart` records, then runs the checks again, every 2 s for up
  to 30 s. A failed check is `Unhealthy`. Tests fake `Checker`, `routing.Kernel`, `routing.NAT`,
  `Files`, `Runner` and `Clock`.
- `internal/updown`: `updown`, which strongSwan runs as the tunnel goes up or down, once for each
  range the VPN server granted, in `PLUTO_PEER_CLIENT`. On `PLUTO_VERB=up-client`, it routes
  the range through `ikeward0` in table 220, through netlink, as charon routes no SA with an
  interface ID. It makes the range's floor, and records it and `PLUTO_PEER` in `routing.Learned`. It
  refuses a range `routing.Refusal` refuses, says so and exits 0, leaving the SA up. On
  `down-client`, the route goes, unless VICI, asked with `noblock` for 3 s at most, lists another SA
  of the connection up that grants the range. The floor stays. As systemd stops or restarts
  `strongswan.service` (`ActiveState` `deactivating`), charon no longer answers VICI, and the route
  goes without asking. Tests fake `Route`, `Files`, `status.Tunnel` and `Runner`.
- `internal/host`: the live system behind those interfaces. That's files under a root directory,
  commands, the terminal, the clock, strongSwan's VICI socket, a DNS client bound to a link, the
  system's resolver, netlink and nftables. Its tests run a fake charon on a socket in a temporary
  directory. They answer nftables' batches and reads in the kernel's place, as for the NAT table.
  Its netlink calls, and nftables against a kernel, are checked in containers only.
- `internal/profile/profiletest`: makes a client profile from a test CA, and a certificate from
  it for a VPN server, for other packages' tests.
- `tests/integration`: `make integration`, ikeward end to end in Docker Compose (`compose.yaml`,
  `Dockerfile`): a strongSwan server, a DNS server on the remote network behind it, and a privileged
  Ubuntu 26.04 client. The server grants two ranges, and later its own range and every address,
  which `updown` refuses. The client has a LAN inside a floor `install` is given, with a host on
  it. It runs systemd, Docker and strongSwan as shipped, and `install` runs there. The tests' values
  are documentation ranges, `example.test` and private ranges of their own. The server stands in for
  the uplink's router too, so that what leaves the client in the clear gets an answer. The DNS
  server answers there too, at an address the server grants none of. It runs only where
  `IKEWARD_INTEGRATION` is set, so `make test` skips it.
- `tests/installer`: `install.sh` piped into `sh` and `bash`, and the README's install commands,
  with a fake `curl` serving test releases and fakes of `uname`, `id` and `less`. It installs into
  a temporary directory through `IKEWARD_INSTALL_DIR`. Part of `make test`.
- `install.sh`: POSIX sh, published with each release; the README's install commands pipe it
  from `curl` into `sh`. It downloads from the release `IKEWARD_VERSION` names, or the latest, found
  once. It installs the machine's binary as `/usr/local/sbin/ikeward`, checked against
  `ikeward.sha256` and run for its version first.
- `docs/`: what the README leaves out, a page per topic. The pages cover the checks and the
  watchdog, the routing and the NAT rule, the profile, building and testing, the gates, and
  maintaining: pull requests, a fork's, and releases.
- `README.md`, `CONTRIBUTING.md`, `SECURITY.md` and `LICENSE`, MIT's: the overview, what a
  contribution needs, how to report a vulnerability, and the terms. `.github/ISSUE_TEMPLATE` holds
  the issue forms, each with a required checkbox: no profile, no secret, no employer's names.
- `tools/run`: fetches the pinned tools of `make check`, `make lint` and `make test` into
  `.cache/tools`.
- `tools/sizecheck`, `tools/valecheck` with `tools/valemirror`, `tools/profilecheck`,
  `tools/denycheck` and `tools/commitcheck`: the size caps, Vale on the Markdown and the Go
  comments, the files gitleaks cannot read, an employer's terms, and the commit messages, as
  `docs/checks.md` says. `tests/tools` runs the scripts in scratch repositories. `.vale.ini`,
  `.vale/styles`, `.rumdl.toml`, `.gitleaks.toml` and `.github/actionlint.yaml` configure gates.
  `tools/notices.tmpl` is the form of the third-party notices that `make notices` writes.
- `.golangci.yml`: the linters, strict. `.github/workflows/ci.yml` runs `make check` with
  `make build` and `make notices`, `make lint`, and `make integration` on `ubuntu-26.04`.
  `.github/workflows/release.yml`, on a tag `vX.Y.Z`, runs `make check` and publishes what
  `make build` and `make notices` make, once denycheck reads it. `pr-text.yml` runs denycheck on
  a pull request's title, body and branch, and `history.yml` runs `make history` each week.
  `rules.yml` runs zadykian/actions' checks of a pull request's commits, description and terms,
  which warn and never block. `.github/dependabot.yml` asks for the modules' and the actions'
  updates each month.
- `.claude/`: Claude's rules and agent, as [Where else to look](#where-else-to-look) lists them.
  `settings.json` holds the permissions, and enables `flow`, `docs` and `lint`, the bundles of the
  marketplace [zadykian/skills](https://github.com/zadykian/skills). `lint-gates` adds to the lint
  plugin's gate code.

## Commands

```sh
make check                    # shellcheck, shfmt, gofmt, go vet, golangci-lint, denycheck, tests
make lint                     # every gate, as CI's lint job runs them (docs/checks.md)
make lint-local               # all but govulncheck, zizmor offline: the lint plugin's lint-tree
make lint-file FILES=a.go     # a file's gates, as the lint plugin runs them after an edit
make vale FILES=README.md     # one gate alone; sizecheck, vale, lychee and rumdl take FILES
make govulncheck              # any other gate alone, on the whole tree, as make lint lists them
make test                     # go test -count=1 ./..., with Vale, gitleaks and shfmt from tools/run
make history                  # gitleaks, profilecheck and denycheck over every ref's history
go run ./tools/denycheck -stdin NAME <FILE   # an employer's terms in a text, by the list
go test -count=1 -run 'TestVersion$' ./cmd/ikeward  # a single test
go test -count=1 ./internal/files -update           # rewrite the files in testdata from Generate
make build VERSION=X.Y.Z      # dist: ikeward-linux-{amd64,arm64}, ikeward.sha256, install.sh
make notices                  # dist/third-party-notices.txt, as each release publishes it
make integration              # end to end in Docker Compose; needs the modules ci.yml loads
# What ikeward reads of a real profile, no secret; .p12 password from IKEWARD_PROFILE_PASSWORD_FILE
IKEWARD_PROFILE=PATH/NAME go test -count=1 -v -run 'TestIssuedProfile$' ./internal/profile
```

A pull request is ready when `make check` and `make lint` pass and its tests cover what it adds.

## Hard rules

Each rule links where its reason lives: a docs page, a comment or a CI job.

- Tests never touch the host's VPN or network: no unit test runs `swanctl`, `ip`, `nft`,
  `resolvectl` or `systemctl`, or opens VICI, netlink or nftables on the host. `install.sh`'s
  tests always install into a temporary directory, never `/usr/local/sbin`
  ([internal/host](internal/host/host.go),
  [the installer's tests](docs/development.md#the-installers-tests)).
- What touches the system (files under a root directory, commands, VICI, netlink, nftables, the
  terminal) sits behind a small interface, and unit tests use fakes
  ([internal/host](internal/host/host.go)).
- Integration tests run only in containers, never on the host. The client is privileged. Its
  image masks every unit that would reach the host's kernel or devices, and has no
  `apparmor_parser`. Its `boot` keeps `/sys`, and `/proc/sys` but for `/proc/sys/net`, read-only,
  and makes `/dev/kmsg` `/dev/null`. Its networks give the host no address. Keep it so when it
  gains a package ([the integration test](docs/development.md#the-integration-test),
  [compose.yaml](tests/integration/compose.yaml), [Dockerfile](tests/integration/Dockerfile)).
- Only `main` exits: every error carries up to it ([main.go](cmd/ikeward/main.go),
  [the watchdog](docs/status.md#the-watchdog)). A mistake on the command line exits 2, a
  failure of the watchdog's own 3, which its service, unlike 1, counts as a failure; any other
  error 1. A change that `install --exit-code` reports exits 4, and `main` prints nothing of it.
- `CGO_ENABLED=0`, for linux/amd64 and linux/arm64 (the [Makefile](Makefile)'s `build`).
- `make build`'s names, `ikeward-linux-ARCH` and `ikeward.sha256`, change only together with
  `install.sh`, which relies on them; `install.sh` keeps its name, which the README's install
  commands download (the [Makefile](Makefile)'s `build`,
  [release.yml](.github/workflows/release.yml)).
- `make check` and `make lint` fail on any finding, warnings included, in any line of any file
  ([the checks](docs/checks.md), CI's `check` and `lint` jobs in
  [ci.yml](.github/workflows/ci.yml)). The exceptions are govulncheck's finding in a module
  ikeward requires alone, which it prints and passes ([govulncheck](docs/checks.md#govulncheck)),
  and go-licenses' warning that it cannot read `golang.org/x/sys`'s assembly
  ([licenses](docs/checks.md#licenses)).
- Fix a finding. Only golangci-lint's and ShellCheck's may be justified in place instead, with the
  linter's own comment and a reason: `//nolint:LINTER // reason`, or
  `# shellcheck disable=SCNNNN` below a comment giving the reason. `install.sh` holds one. Any
  other gate's finding is fixed, Vale's, rumdl's, lychee's, sizecheck's and gitleaks' among them,
  and no comment may turn Vale or rumdl off. Never lower a severity, add an exclusion or keep a
  baseline. Every file passes every gate outright, but the size caps pass over the vendored
  `.vale/styles` ([a finding](docs/checks.md#a-finding)).
- The caps: files of 300 lines or fewer, Go, Markdown, YAML and shell alike, and the README 150. A
  Go comment block holds 4 lines or fewer, and a package comment 15. Sentences run to 30 words at
  most, and prose wraps at 100 columns ([the gates](docs/checks.md#the-gates),
  [writing.md](.claude/rules/writing.md), [docs.md](.claude/rules/docs.md)).
- The shell scripts are POSIX sh, clean under `shellcheck -s sh` and `shfmt -ln posix -i 4` (the
  [Makefile](Makefile)'s `vet`).
- `tools/run` pins each tool's version with its four SHA-256 sums, and a bump changes them
  together, from the release's checksums file or GitHub's asset digests ([tools/run](tools/run)).
  govulncheck and go-licenses run through `go run`, at the version the [Makefile](Makefile)
  names, which Go's checksum database pins: go-licenses publishes no binaries.
- The Vale styles in `.vale/styles` are vendored with their licenses; a bump replaces them whole
  ([every file](docs/checks.md#every-file)).
- A workflow's job sets its own `permissions` and `timeout-minutes`, checks out with
  `persist-credentials: false`, and pins each action by its SHA, as
  [ci.yml](.github/workflows/ci.yml)'s jobs do. actionlint and zizmor check the workflows
  ([the gates](docs/checks.md#the-gates)).
- No secret in the repository: no profile, certificate, key or password, and no binary file or
  archive, which gitleaks cannot read. Tests make their own CA and `.p12`, at run time
  ([gitleaks and `tools/profilecheck`](docs/checks.md#a-client-profile),
  [a real profile](docs/development.md#what-ikeward-reads-of-a-profile)).
- No employer's names, hosts, internal addresses or domains anywhere: in files, commits, issues
  and pull requests. Values are made up, as the tests' are ([writing.md](.claude/rules/writing.md)).
  `tools/denycheck`'s list of terms stays outside the repository, in the secret `DENYLIST` and
  `~/.config/ikeward/denylist`. Nothing quotes a term: a finding gives its number alone, and a
  test makes its terms up ([an employer's terms](docs/checks.md#an-employers-terms)).

## How changes land

- Pull requests land on `main` by fast-forward only; never run `gh pr merge`.
- Rebase onto `main`, wait for the checks (`gh pr checks N --watch`), then comment `/ff`, alone,
  on the pull request (`.github/workflows/fast-forward.yml`). `/ff` pushes at once, and `main`'s
  ruleset refuses the push until CI's `check`, `integration` and `lint` pass and every
  conversation is resolved ([pull requests](docs/maintaining.md#pull-requests)). Read a fork's
  pull request by `gh pr diff N`, and never check its branch out where Claude Code runs, as
  [maintaining.md](docs/maintaining.md#a-forks-pull-request) says.
- The maintainer pushes a pull request that changes `.github/workflows` by hand,
  `git push origin SHA:main`, as the workflow's token may not push such a change.
- A tag `vX.Y.Z` on `main` publishes a release (`release.yml`), which fails for a commit not on
  `main`, or a number with a leading zero; the maintainer pushes it.
- Pull requests stack, each on the branch below it. Once the one below lands, retarget the next at
  `main` with `gh api -X PATCH 'repos/{owner}/{repo}/pulls/N' -f base=main`, and rebase it where
  `main` moved. Wait for its checks, then `/ff` it. `/flow:land` walks a stack, and runs
  denycheck on each pull request before its `/ff`.
- `.claude/settings.json` denies `gh pr merge`, the usual forms of a push to `main`, a bare
  `git push` or `git push origin`, a push of `HEAD`, and the usual forms of a tag's push. A rule
  matches the command as written, so it guards only the forms it names: never push from a local
  `main`, and leave the tags to the maintainer.

## Commits

The `atomic-commits` skill of `flow` cuts a change into commits, each of one logical change that
`make check` and `make lint` pass, and a change over 300 lines into stacked pull requests. What
follows wins where it differs.

Messages follow [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).
`make commits` (`tools/commitcheck`) checks each commit from `origin/main` to `HEAD` for these
([the commit messages](docs/checks.md#the-commit-messages)):

- The header is `type(scope): description`, 72 columns at most, with no period at its end. The
  type is `feat`, `fix`, `build`, `chore`, `ci`, `docs`, `perf`, `refactor`, `revert`, `style` or
  `test`. The scope is optional, of `a-z`, `0-9`, `_` and `-`; `git log` on `main` shows none so
  far. A breaking change takes `!` before the colon.
- A blank line follows the header. A line of the body is 72 columns at most, but for a
  `Co-authored-by` or `Signed-off-by` trailer and a line that holds a URL.

Review checks the rest:

- The description is short and lower case, a noun phrase or an imperative, as `main`'s history has
  both. The body is a list of bullets: what was missing, what changed, what was checked.
- A pull request's title is a header too, its commit's where it holds one. Its body starts
  `Part of #N.`, or `Closes #N.` on the one that completes the issue. Then come `## What`,
  `## Stack` in a stack of several, and what was checked.

## Where else to look

- `.claude/rules/`, each loaded with the files it covers. [writing.md](.claude/rules/writing.md)
  holds what ikeward adds to the `writing` skill's policy, and [docs.md](.claude/rules/docs.md)
  what each document holds. [tests.md](.claude/rules/tests.md) holds the tests' invariants and
  files.
- `.claude/agents/doc-editor.md`: rewrites prose to the writing policy and Vale's rules, keeping
  every fact and link.
- The marketplace's skills, which ikeward uses as they are: `/flow:issue` plans and files an issue.
  `/flow:land`, run by hand only, lands a stack by `/ff`. `/docs:trim` trims comments and docs to
  the policy. `/lint:split-file` splits a file over its cap.
- The lint plugin: after each Edit or Write, `make lint-file` runs the gates of the file's type,
  and gitleaks and denycheck on any file. As a turn ends, `make lint-tree`, which is `lint-local`,
  runs where the work tree changed. No gate runs in a tree with a commit that lacks its author's
  signature, or with gate code unlike `HEAD`'s, as `.claude/lint-gates` adds to it
  ([Claude Code's hooks](docs/checks.md#claude-codes-hooks)).
