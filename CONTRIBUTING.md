# Contributing

Thanks for helping ikeward. The [README](README.md) says what it does, and
[building and testing](docs/development.md) how to build it and run its tests.

## Issues

Open an issue with its form: a bug, or a change. Talk a change of behaviour over in an issue before
its pull request. Report a vulnerability privately instead, as [SECURITY.md](SECURITY.md) says.

Put no profile, key, certificate or password in an issue, a pull request or a commit. Put no
employer's names, hosts, addresses or domains there either. Make values up, as the tests do, from
documentation ranges and `example.test`. Replace the real ones in a log before you paste it.

## A pull request

- Small commits, each of one logical change, which `make check` and `make lint` pass with no
  finding, warnings included. Their tests cover what they add. A change of more than 300 lines goes
  as a stack of pull requests, each on the one below, as the [atomic-commits][atomic] skill cuts
  it. [The checks](docs/checks.md) says what each gate checks, and how a finding is settled:
  fixed, never turned off. Without the maintainer's list of employers' terms, `tools/denycheck`
  passes with a note, and the maintainer checks with the list before landing.
- Signed, by a GPG or SSH key that GitHub verifies, as `main` takes signed commits alone. GitHub's
  docs say [how to sign commits][sig].
- A message in [Conventional Commits 1.0.0][commits], as `make commits` checks it. The header and
  each line of the body hold 72 columns at most, with a blank line between, and the body is a list
  of bullets. [The commit messages](docs/checks.md#the-commit-messages) gives the types. The pull
  request's title is a header too, its commit's where it holds one.
- Based on `main`'s head. Rebase it where `main` moves, rather than merge `main` into it.
- A change of behaviour goes together across the code and its comments, the help, the README, its
  page in `docs/` and the tests.
- Prose follows [the writing policy](.claude/rules/writing.md): sentences of 30 words at most, lines
  of 100 columns, and files of 300 lines.

With Claude Code, `CLAUDE.md` and `.claude/` give Claude the same rules, and its hooks run the gates
as it works.

## Landing

CI runs `check`, `integration` and `lint` on the pull request. Once they pass and each conversation
is resolved, the maintainer lands it by a comment `/ff`. That pushes the very commit CI checked to
`main`, by fast-forward: GitHub's merge methods are off. [Maintaining](docs/maintaining.md) says
more.

## Conduct

ikeward follows the [Contributor Covenant][covenant], version 2.1. Report a breach privately to the
maintainer, [@zadykian](https://github.com/zadykian).

## License

ikeward is under the [MIT license](LICENSE). A contribution is under it too.

[sig]: https://docs.github.com/authentication/managing-commit-signature-verification/signing-commits
[commits]: https://www.conventionalcommits.org/en/v1.0.0/
[atomic]: https://github.com/zadykian/skills/blob/main/skills/atomic-commits/SKILL.md
[covenant]: https://www.contributor-covenant.org/version/2/1/code_of_conduct/
