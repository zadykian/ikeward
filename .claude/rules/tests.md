---
paths:
  - "**/*_test.go"
  - "tests/**"
  - "internal/files/testdata/**"
  - "internal/profile/profiletest/**"
---

# Tests

The invariants a change to the tests keeps, from CLAUDE.md's hard rules. Each fake's doc comment
says what it stands for, and [development.md](../../docs/development.md) says how the tests run.

## What runs

- Unit tests never touch the host's VPN or network. None runs `swanctl`, `ip`, `nft`, `resolvectl`
  or `systemctl`, or opens VICI, netlink or nftables on the host.
- A package's tests reach the system through its small interfaces, with fakes: `Files`, `Runner`,
  `Route`, `NAT`, `Kernel`, `Tables`, `Tunnel`, `Charon`, `Network`, `Resolver`, `Checker` and
  `Clock`, as CLAUDE.md's layout lists them.
- `internal/host`'s tests touch a temporary directory, in-memory pipes and fakes. A fake charon
  listens on a socket in a temporary directory, a fake kernel answers nftables' batches and reads,
  and a fake DNS server answers on a pipe. They run sh alone, through `host.Commands`, and no
  command that reaches the network or the VPN. Its netlink calls, and nftables against a kernel,
  are checked in containers only.
- `tests/installer` installs into a directory of the test's, through `IKEWARD_INSTALL_DIR`, never
  `/usr/local/sbin`. A fake `curl`, `uname`, `id` and `less` stand in, and the `PATH` holds nothing
  else but the tools the script runs. The fake `curl` serves the test's releases and writes only
  into the test's directory, so no test reaches the network.
- `tests/integration` runs only in containers, and only where `IKEWARD_INTEGRATION` is set, as
  `make integration` sets it. `make test` skips it.
- The integration test's client is privileged. Its image masks every unit that would reach the
  host's kernel or devices, and has no `apparmor_parser`. Its `boot` keeps `/sys`, and `/proc/sys`
  but for `/proc/sys/net`, read-only, and makes `/dev/kmsg` `/dev/null`. Its networks give the
  host no address. Keep it so when it gains a package.
- Each step of `TestIntegration` starts from where the one before left the client, so the first
  that fails ends the test. A new step goes into its chain, in order.
- Tests make their own CA and `.p12`: `internal/profile/profiletest` for other packages, and
  `fixture_test.go` in `internal/profile`. `TestIssuedProfile` reads a real profile only where
  `IKEWARD_PROFILE` names it, and logs no secret.
- `tools/denycheck`'s tests make their terms up, as `acme-widgets`, `Contoso` and `198.51.100`,
  and never take the list's. They pass `run` an environment of their own, so a `DENYLIST` or a
  list of the user's never reaches them.
- `internal/files/testdata` holds the files `Generate` must write, compared byte for byte.
  `go test ./internal/files -update` rewrites them.
- The command line's tests check each exit status. A mistake on the command line exits 2, a
  failure of the watchdog's own 3, and any other error 1.

## Where a test goes

- A test goes in the file named for what it covers. A file that would pass 300 lines splits by
  topic. A helper of one file's tests stays in that file. The fakes and helpers that several files
  share go in `fake_test.go` or `helpers_test.go`, with no test in them. Some packages name that
  file for what it builds: `fixture_test.go` in `internal/profile`, `installer_test.go` in
  `tests/installer`, and `stack_test.go` in `tests/integration`. `tests/tools` keeps them in
  `tools_test.go`. `tools/sizecheck` and `tools/denycheck` keep them in `main_test.go`, beside
  their `TestMain` and `TestRunErrors`, a test of `run`.
- `cmd/ikeward`: `main_test.go`, the version, the help, mistakes and exit statuses.
  `install_test.go`, install, its dry run, its errors, and its refusal to run as another program.
  `flags_test.go`, install's flags, their mistakes, and the floors that hold the VPN server's
  address. `password_test.go`, the `.p12`'s password. `status_test.go`, `watchdog_test.go`,
  `updown_test.go` and `routing_test.go` cover their commands, and `fake_test.go` the fake system.
  `helpers_test.go` writes the profile's files, gives install's arguments, and reads what it wrote.
- `internal/install`: `install_test.go`, a run, again and with changes; `half_way_test.go`, a stop
  half way, the journal and the rerun. `steps_test.go`, the steps due by the files they read, and
  `AllSteps`; `probe_test.go`, each probe making its step due. `dry_run_test.go`, `--dry-run`;
  `failures_test.go`, failed commands, the journal's errors and output; `uninstall_test.go`,
  `uninstall`; `diff_test.go`, the diffs.
- `internal/updown`: `updown_test.go`, `Run` as the tunnel goes up, the ranges it learns or
  refuses, and its errors. `down_test.go`, the tunnel going down, as strongSwan stops or another
  SA grants the range.
- `internal/status`: `status_test.go`, a healthy run, the resolver left unasked and the error its
  check keeps; `tunnel_test.go`, the SAs and their ranges. `dns_test.go`, the DNS check;
  `nat_test.go`, the NAT check; `routing_test.go`, the route and floor checks of learned ranges;
  `unhealthy_test.go`, each check failing. `internal/watchdog`: `watchdog_test.go`, the restarts,
  their limit and errors; `unmendable_test.go`, the failures a restart doesn't mend;
  `routing_test.go`, the routing's repairs. `internal/routing`: `routing_test.go`;
  `learned_test.go`, the record of learned ranges and their floors; `refusal_test.go`, the ranges
  refused; `settings_test.go`, `ReadSettings`.
- `internal/host`, `internal/files`, `tools/sizecheck`, `tools/denycheck` and `tools/valemirror`:
  `NAME_test.go` beside `NAME.go`, and `run_test.go` for a tool's `run`, in `main.go`.
  `tools/valemirror/gate_test.go` runs `tools/valecheck` and Vale on a Markdown file and a Go
  file: each rule of `.vale.ini` must flag both.
- `internal/profile`: `profile_test.go`, `Parse` and the summary; `sswan_test.go` and
  `mobileconfig_test.go`, their files; `errors_test.go`, what `Parse` refuses; `issued_test.go`, a
  real profile. `fixture_test.go` makes the test CA and the profile's files.
- `tests/installer`: `install_test.go`, installs and updates; `refuses_test.go`, what `install.sh`
  refuses; `version_test.go`, `IKEWARD_VERSION`; `readme_test.go`, the README's install commands.
  `curl_test.go` holds the fake curl and its test, `installer_test.go` the other fakes, and
  `helpers_test.go` the checks the files share.
- `tests/tools`: a file per script of `tools/` but `run`, as `commitcheck_test.go`, and
  `TestGitleaks`, which runs `make gitleaks`, in `profilecheck_test.go`. `makefile_test.go` covers
  the Makefile's recipes and `tools/run -path`, and `lint_file_test.go` `make lint-file`.
- `tests/integration`: `integration_test.go`, the steps' order and the install, tunnel, resolver,
  resolver's probe and uninstall steps. `names_test.go`, the names with strongSwan stopped and
  without `ikeward0`'s own address; `nat_test.go`, the NAT steps and their probe;
  `watchdog_test.go`, the watchdog's.
  `ranges_test.go`, the ranges learned; `refused_test.go`, the grants refused; `floor_test.go`,
  the LAN's and the floors' steps, and their probe; `reboot_test.go`, the reboot.
  `stack_test.go`, the Compose stack; `helpers_test.go`, the install, waits and reads the steps
  share.
