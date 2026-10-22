---
paths:
  - "**/*.md"
---

# Docs

What each document holds. How to write them is in [writing.md](writing.md).

- [README.md](../../README.md) is the overview, of 150 lines or fewer, as `make sizecheck` checks.
  It says what ikeward is for and whom it suits, what it needs, and how to install, set up and run
  it. Then come the routing, the limits, security, and how to uninstall it. Its `More` gives a line
  per topic, linking the topic's page in `docs/`, and links the files below.
- The README's install commands are those `tests/installer` runs, each block on its own, as its
  package comment says. They change together.
- `docs/` holds what the README leaves out, a page per topic, each linking back to the README:
  - [status.md](../../docs/status.md): status's checks, and the remote network's names while the
    tunnel is down. The server's networks inside the remote network's, strongSwan's own retries,
    and the watchdog's repairs and exit status.
  - [nat.md](../../docs/nat.md): the order of the rules, `updown`'s routes, the ranges it learns,
    and the grants it refuses. The floors, the NAT rule, forwarded networks and the firewall, and
    setting them again.
  - [profile.md](../../docs/profile.md): the profile's files and their flags, what ikeward reads of
    them, and what it refuses.
  - [development.md](../../docs/development.md): the commands, the installer's tests, the
    integration test, and the test that shows what ikeward reads of a profile.
  - [checks.md](../../docs/checks.md): each gate of `make lint`, Claude Code's hooks, how a
    finding is settled, an employer's terms and the history job. Then Vale on Go comments, the
    licenses, govulncheck, what gitleaks cannot read, the commit messages, and each file's gates.
  - [maintaining.md](../../docs/maintaining.md): landing pull requests on `main`, reviewing a
    fork's pull request, the list of terms, Dependabot, and publishing a release.
- [CONTRIBUTING.md](../../CONTRIBUTING.md) says what an issue and a pull request need, and links
  the code of conduct. [SECURITY.md](../../SECURITY.md) says how to report a vulnerability, and
  [LICENSE](../../LICENSE) is MIT's. The issue forms in `.github/ISSUE_TEMPLATE` ask for no
  profile, no secret and no employer's names or addresses.
- A new topic gets a page of its own and a line in the README's `More`. A page that would pass 300
  lines splits by topic.
- [CLAUDE.md](../../CLAUDE.md) is for Claude. It says what the repository is, its layout, the
  commands, how changes land, the hard rules and the commit style. `.claude/rules/` holds an
  area's rules, loaded with the files they cover.
- Each hard rule in CLAUDE.md links where its reason lives: a docs page, a comment or a CI job.
- A change of behaviour goes together across the code and its comments, the help in `cmd/ikeward`,
  the README, its page in `docs/` and the tests. A file that moves changes CLAUDE.md's layout.
- Links to files and headings resolve: `make lychee` checks them offline. A heading that another
  file links to keeps its name, or its links change with it.
