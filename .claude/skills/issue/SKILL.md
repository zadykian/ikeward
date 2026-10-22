---
name: issue
description: >-
  Plan, then file a GitHub issue for ikeward in the house style once the maintainer approves the
  plan. Use when asked to file, open or write up an issue, or to plan a change.
argument-hint: "[topic]"
---

# File an issue

Plan, approval, then the issue. Never branch or write code unless the maintainer asks for it.

## 1. Plan

Reply with a plan and ask for approval:

- the goal, in a sentence or two;
- the changes, with `file:line` references;
- what to check, and the tests;
- open decisions, each with a recommendation;
- the pull requests and their order, where the change takes several, and what each waits for.

"Approved, create the issue" means `gh issue create`, not code.

## 2. Read the latest main

```sh
git fetch origin
git rev-parse --short origin/main      # the SHA the issue cites
git show origin/main:PATH              # read files at that SHA
```

The local checkout may lag main by several commits: cite only lines read at `origin/main`. Cite a
pull request in flight by its number, as an issue that builds on another's head does.

## 3. Write it

- **Title:** a Conventional Commits header, lower case, a noun phrase or an imperative, as the
  commits have them. A title may pass a header's 72 columns, as one that names two changes:
  `feat: NAT for more networks than Docker's, and a check for a LAN inside a floor`. A scope is
  optional: `chore(claude): the writing rules, lint hooks and doc-editor`.
- **Summary:** a paragraph. Say what is missing or wrong today, and what this adds. End with "Line
  references are to main SHA." Name the versions of any other source cited, as strongSwan 6.0.4 or
  Linux 7.0.
- **`## Today`:** bullets with `file:line` references, the crucial facts only. Group them under a
  short label where they cover several subjects, as "The NAT rule:" and "The LAN:".
- **`## Proposal`:** bullets saying what changes. A decision leads with its name in bold, and a
  rejected option says why: "**Not `bypass-lan`:** ...". The last bullet says what the change lands
  on, as "Lands on top of #N's gate."
- **`## Pull requests`:** where the change takes several, a checkbox per pull request with its
  title, in landing order.
- **`## Waits`:** only where something outside the issue must happen first.
- **`## Open decisions`:** only when something is undecided, each with options and a
  recommendation.
- 200 to 350 words. A design that rests on probes runs longer.
- The repository is public: no employer's names, hosts, internal addresses or domains, and no
  secret, as [writing.md](../../rules/writing.md) says. Another repository's issue is
  `OWNER/REPO#N`.

```markdown
SUMMARY. Line references are to main SHA.

## Today

- `path/file.go:12-30` does X.

## Proposal

- Do Y.
- Lands on top of #N.
```

## 4. File it

Write the body to `BODY.md` in the scratchpad, never in the repository. Name it by one absolute
path in each command. From the repository's root, check it with Vale, and the title and the body
with `tools/denycheck` and the maintainer's list, as nothing else reads an issue. Then file it:

```sh
B=/ABSOLUTE/PATH/TO/SCRATCHPAD/BODY.md
tools/valecheck "$B"
printf '%s\n' 'TITLE' | go run ./tools/denycheck -require -stdin title
go run ./tools/denycheck -require -stdin body <"$B"
gh issue create --title 'TITLE' --body-file "$B" [--label LABEL]
```

`tools/valecheck` fails on an alert, and on a path that names no file. Fix each alert by
rewriting the sentence, then check again. denycheck fails on an employer's term, and where it
finds no list: take the term out, or stop and ask for the list. Never quote a term it finds.

Labels: `feat` gets `enhancement`, `fix` gets `bug` and `docs` gets
`documentation`. The other types get none. Reply with the issue's URL.
