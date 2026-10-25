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

- Commits and pull requests as the [atomic-commits][atomic] skill cuts and writes them. Each
  commit passes `make check` and `make lint` with no finding, warnings included, and its tests
  cover what it adds. [The checks](docs/checks.md) says what each gate checks, and how a finding
  is settled: fixed, never turned off. Without the maintainer's list of employers' terms,
  `tools/denycheck` passes with a note, and the maintainer checks with the list before landing.
- Signed, by a GPG or SSH key that GitHub verifies, as `main` takes signed commits alone. GitHub's
  docs say [how to sign commits][sig].
- Based on `main`'s head. Rebase it where `main` moves, rather than merge `main` into it.
- A change of behaviour goes together across the code and its comments, the help, the README, its
  page in `docs/` and the tests.
- Prose follows [the writing policy][writing], with ikeward's part in
  [writing.md](.claude/rules/writing.md).

With Claude Code, `CLAUDE.md`, `.claude/` and the skills of [zadykian/skills][skills] give Claude
the same rules. That marketplace's lint plugin runs the gates as Claude works.

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
[atomic]: https://github.com/zadykian/skills/blob/main/skills/atomic-commits/SKILL.md
[skills]: https://github.com/zadykian/skills
[writing]: https://github.com/zadykian/skills/blob/main/skills/writing/SKILL.md
[covenant]: https://www.contributor-covenant.org/version/2/1/code_of_conduct/
