---
paths:
  - "**/*.go"
  - "**/*.md"
---

# Writing

What ikeward adds to the `writing` skill, which holds the shared policy for comments and docs.
`make sizecheck`, `make vale` and `make rumdl` check the measurable part, and any alert fails
them. Review checks the rest.

## Comments

- A Go doc comment starts with the name it documents, as `// Check runs the checks, ...` does.
- A comment names the page that holds a fact, rather than repeat it: `files.LinkULA` names
  `docs/status.md`, and `tools/valemirror` names `docs/checks.md`.

## Docs

- How the code works lives in its comments, and what an operator runs and sees in the README and
  `docs/`. The gates live in `docs/checks.md`, and Claude's rules in `CLAUDE.md` and `.claude/`.
- A command's help is in `cmd/ikeward`. [docs.md](docs.md) says which document holds what.

## A public repository

- Nothing in it names an employer, or its hosts, internal addresses or domains. A doc, a comment,
  a test, a commit, an issue and a pull request make values up instead: documentation ranges,
  `example.test` and private ranges of their own, as the tests take.
- Nothing in it holds a secret: no profile, certificate, key or password.
- `tools/denycheck` checks for an employer's terms by a list kept outside the repository. Nothing
  quotes a term of it, to explain a finding either: a term goes by its number in the list.

## The gates

- Vale reads the Go comments too, through the Markdown mirrors that `tools/valemirror` writes.
- In plain text, a Go comment or Markdown outside a code span, Vale reads no sentence break after
  a dotted name or an address, such as `files.Link` or `10.88.0.0/16`. The sentence runs on
  where the next one starts with a lowercase word, a camel-case identifier or a code span. Start
  the next sentence with an ordinary word, or put the name earlier in its sentence. A code span,
  or a doc link in Go, ends a sentence as a word does
  ([Vale on Go comments](../../docs/checks.md#vale-on-go-comments)).
- golangci-lint's `lll` holds each Go line to 100 columns, a comment's too.
- Only golangci-lint's and ShellCheck's findings may be justified in place
  ([a finding](../../docs/checks.md#a-finding)).
