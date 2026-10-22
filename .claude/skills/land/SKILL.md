---
name: land
description: >-
  Land a stack of approved ikeward pull requests on main, bottom first, by fast-forward through a
  /ff comment, and tag a release if asked.
disable-model-invocation: true
argument-hint: "PR... [vX.Y.Z]"
---

# Land a stack

Land the pull requests `$ARGUMENTS` on main, the bottom of the stack first. main takes
fast-forwards only, and GitHub's merge methods are refused. A comment `/ff` lands a pull request
(`.github/workflows/fast-forward.yml`). main's ruleset refuses its push until CI's `check`,
`integration` and `lint` pass and every conversation is resolved.

`.claude/settings.json` denies `gh pr merge`, the pushes that name main or `HEAD`, a bare
`git push` or `git push origin`, and the usual forms of a tag's push. It guards only the forms it
names, so never push from a local main.

## Each pull request, in order

Run each command from a clone of this repository, whose remote names it to `gh`.

1. **Base.** `gh pr view N --json baseRefName,headRefOid,state`. `/ff` lands only a pull request
   into main. Where the base is the branch that just landed, retarget the pull request at main:

   ```sh
   gh api -X PATCH 'repos/{owner}/{repo}/pulls/N' -f base=main
   ```

   `gh pr edit --base` fails on the Projects (classic) deprecation, so use the API.
2. **On main.** `git fetch origin main "pull/N/head"`, which fetches a fork's head too, then
   `git merge-base --is-ancestor origin/main HEAD_SHA`. Where main is not an ancestor of the
   head, stop: the stack needs a rebase (below). Then `git rev-list --count origin/main..HEAD_SHA`
   must print 1, as a pull request is one commit. Stop otherwise, and report the count.
3. **Checks.** `gh pr checks N --watch`, until none is pending. Each job that the head's ci.yml
   defines must have passed: `check` and `integration`, and `lint` where the head has it
   (`git show HEAD_SHA:.github/workflows/ci.yml`). Stop on a check that failed or is missing, and
   report it.
4. **Conversations.** Every review conversation resolved; `/ff` pushes nothing otherwise.
5. **Terms.** CI checks a fork's pull request with no list, as it gets no secrets, so check each
   pull request with the maintainer's, in `~/.config/ikeward/denylist`. Run this clone's
   `tools/denycheck`, never the pull request's, on its commits, its title, its body and its branch:

   ```sh
   go run ./tools/denycheck -require -history origin/main..HEAD_SHA
   text=$(gh pr view N --json title,body,headRefName -q '.title, .body, .headRefName') &&
       [ -n "$text" ] &&
       printf '%s\n' "$text" | go run ./tools/denycheck -require -stdin "pull request N"
   ```

   The texts pass through a variable, so that a `gh` that fails stops the check rather than give
   it nothing to read. Stop on a finding, on no list or on a failed command. Report where each
   finding is and its term's number, never the term.
6. **Land.**
   - A pull request that changes `.github/workflows`: the workflow's token may not push it. Give
     the maintainer `git push origin SHA:main` to run, with the head's SHA, and wait for them.
     Its branch stays on GitHub. Once the next pull request is retargeted at main (step 1), give
     the maintainer `git push origin --delete BRANCH` too.
   - Any other: `gh pr comment N --body /ff`, the comment alone, as the workflow takes no other.
7. **Wait** until `gh pr view N --json state -q .state` says `MERGED`. Where the workflow
   comments that it did not move main, report its reason and stop.

Then go on with the next pull request.

## A rebased stack

Rebase only when the maintainer asks. Then push each branch with its old head as the lease:

```sh
git push origin --force-with-lease=BRANCH:OLD_SHA BRANCH
```

The checks run again. Wait for them, `gh pr checks N --watch`, before `/ff`:
main's ruleset refuses the push until they pass.

## Release

Only when the maintainer gives a version, once the stack has landed. The maintainer pushes the
tag, as [CLAUDE.md](../../../CLAUDE.md#how-changes-land) says, and
[maintaining.md](../../../docs/maintaining.md#releases) says what the release holds.

1. `git fetch origin`, and check the version against the last tag,
   `git describe --tags --abbrev=0 origin/main`. A tag other than `vX.Y.Z` starts no run of
   `release.yml`. One with a leading zero in a number, or on a commit off main, fails it.
2. Give the maintainer the commands, with main's head:

   ```sh
   git tag vX.Y.Z COMMIT
   git push origin vX.Y.Z
   ```

3. `release.yml` runs `make check` on the tagged commit and publishes. Report its run, once its
   `headSha` is the tagged commit:
   `gh run list --workflow release.yml --limit 1 --json headSha,status,url`.
   Then report the release once published: `gh release view vX.Y.Z`.
