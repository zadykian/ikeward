# Maintaining

How the maintainer lands pull requests, reviews a fork's, and publishes a release. The
[README](../README.md) says how to install ikeward and run its commands, and
[CONTRIBUTING](../CONTRIBUTING.md) what a pull request needs.

## Pull requests

Pull requests land on `main` by fast-forward only, as the commits CI checked. GitHub's merge methods
are all refused. A comment `/ff`, from someone who can push here, pushes the pull request's head to
`main` and closes its linked issues (`.github/workflows/fast-forward.yml`). A pull request that
changes `.github/workflows` is pushed by hand, `git push origin SHA:main`, as the workflow's token
may not push such a change.

`main`'s ruleset requires CI's `check`, `integration` and `lint` jobs to have passed on a commit
before it lands there, by `/ff` or by hand. It requires signed commits, and every conversation
resolved, too.

`/flow:land` lands a stack of pull requests, the bottom first, by the steps of the
[land](https://github.com/zadykian/skills/blob/main/skills/land/SKILL.md) skill.

## A fork's pull request

A fork's branch may change `.claude/`, the Makefile, `tools/` and the tests, and Claude Code runs
some of them unasked. `.claude/settings.json` lets Claude run `git`, `make`, `go` and `docker run`
without asking, and a fork's may wire hooks or enable plugins of its own. The lint plugin's hooks
come from the plugin, not the checkout. They run the checkout's `make lint-file` and
`make lint-tree`, but only in a tree the maintainer vouches for, so a fork's commits run no gate
([Claude Code's hooks](checks.md#claude-codes-hooks)). A clone with the fork's branch checked out
loads the fork's settings, so another clone alone keeps none of that from running.

So read the diff first, `gh pr diff N`, `.claude/` and the Makefile above all. Check the fork's
branch out only where Claude Code isn't running, or in a container. CI runs it on GitHub's runners,
apart from your machine.

GitHub gives a fork's pull request no secrets, so CI checks it with no list of terms. Before `/ff`,
check its commits, title, body and branch with the list, by your own clone's `tools/denycheck`, as
`/flow:land` does. Each command runs where the one before it succeeded, so a failed `gh` or
`git` fails the check rather than pass an empty one:

```sh
head=$(gh pr view N --json headRefOid -q .headRefOid) &&
    git fetch origin main "pull/N/head" &&
    go run ./tools/denycheck -require -history "origin/main..$head"
text=$(gh pr view N --json title,body,headRefName -q '.title, .body, .headRefName') &&
    [ -n "$text" ] &&
    printf '%s\n' "$text" | go run ./tools/denycheck -require -stdin "pull request N"
```

## The list of terms

In CI, `tools/denycheck` reads the list of an employer's terms from the secret `DENYLIST`.
Dependabot's runs read secrets of their own, so the list goes there too. Keep it in
`~/.config/ikeward/denylist`, mode `0600`, outside every repository, and set both from a clone:

```sh
gh secret set DENYLIST <~/.config/ikeward/denylist
gh secret set DENYLIST --app dependabot <~/.config/ikeward/denylist
```

Without it, CI's `check` and `lint` jobs fail, as do `pr-text`, `history` and a release, but for
a fork's pull request. [The checks](checks.md#an-employers-terms) says how a term matches.

The weekly `history` job reads every branch and tag whole
([the history job](checks.md#the-history-job)). Where it fails, a ref holds a secret or a term
that a gate added since finds: take it out of that ref's history.

## Dependabot

Each month, Dependabot opens a pull request for the Go modules, headed `build:`, and one for the
actions, headed `ci:`, each of versions out a week at least (`.github/dependabot.yml`). The
actions' pull request changes `.github/workflows`, so the maintainer pushes it by hand.

It leaves the actions of zadykian/toolkit alone: it would move their hashes, but not the
marketplace's `ref` in `.claude/settings.json`. The toolkit's `tools/pin` moves both to one
release, as its [Pin a release](https://github.com/zadykian/toolkit#pin-a-release) says.

## Releases

A tag `vX.Y.Z` on `main` publishes a release (`.github/workflows/release.yml`). A tag with a
leading zero in a number, as `v01.2.3`, which `install.sh` would refuse, fails it before anything
runs. So does a tag on a commit that is not on `main`.

`make check` runs on the tagged commit, then `make build VERSION=X.Y.Z` and `make notices`. The
release gets `ikeward-linux-amd64`, `ikeward-linux-arm64`, `ikeward.sha256`, `install.sh` and
`third-party-notices.txt`, with notes GitHub writes from the pull requests. denycheck reads each,
and the notes, before anything is published. GitHub marks it the latest release, which
`install.sh` installs by default. The integration test ran in the tagged commit's pull request,
and does not run again.

```sh
git tag -a vX.Y.Z -m vX.Y.Z COMMIT
git push origin vX.Y.Z
```
