---
paths:
  - "**/*.go"
  - "**/*.md"
---

# Writing

How comments and docs are written. `make sizecheck`, `make vale` and `make rumdl` check the
measurable part, and any alert fails them. Review checks the rest.

## What a comment says

- A comment gives a block's purpose in one or two sentences. Or it gives a reason the code can't
  show, with the finding behind it, such as what a probe or strongSwan's source showed. It never
  narrates the code.
- A doc comment starts with the name it documents, as `// Check runs the checks, ...` does.
- A detail that doesn't fit the 4 lines of a block goes in a comment beside the code it explains.
- A comment names the page that holds a fact, rather than repeat it: `files.LinkULA` names
  `docs/status.md`, and `tools/valemirror` names `docs/checks.md`.

## What a doc says

- Each fact lives in one place. How the code works lives in its comments, and what an operator runs
  and sees lives in the README and `docs/`. The gates live in `docs/checks.md`, and Claude's rules
  in `CLAUDE.md` and `.claude/`. Other places link to it.
- A doc doesn't repeat a command's help, in `cmd/ikeward`; it says what the help leaves out.
- [docs.md](docs.md) says which document holds what.
- The repository is public. Nothing in it names an employer, or its hosts, internal addresses or
  domains. A doc, a comment, a test, a commit, an issue and a pull request make values up instead:
  documentation ranges, `example.test` and private ranges of their own, as the tests take.
- Nothing in it holds a secret: no profile, certificate, key or password.
- `tools/denycheck` checks for an employer's terms by a list kept outside the repository. Nothing
  quotes a term of it, to explain a finding either: a term goes by its number in the list.

## The caps

- Sentences of 30 words or fewer. In Markdown, Vale counts the words outside code spans.
- Files of 300 lines or fewer, and the README 150. In Go, a comment block is 4 lines or fewer,
  and a package comment 15 or fewer.
- Prose is wrapped at 100 columns, in Markdown (`make rumdl`) and Go (golangci-lint's `lll`).
- Vale refuses wordy phrases (`write-good.TooWordy`), such as `however`, `minimum`, `multiple`,
  `in order to` or `it is`. It refuses needless variants too (`proselint.Needless`).
- In plain text, a Go comment or Markdown outside a code span, Vale reads no sentence break after
  a dotted name or an address, such as `files.Link` or `10.88.0.0/16`. The sentence runs on
  where the next one starts with a lowercase word, a camel-case identifier or a code span. Start
  the next sentence with an ordinary word, or put the name earlier in its sentence. A code span,
  or a doc link in Go, ends a sentence as a word does
  ([Vale on Go comments](../../docs/checks.md#vale-on-go-comments)).
- Fix an alert of Vale, rumdl or sizecheck by rewriting, keeping the facts. Never by an exception,
  a lower level or a baseline: only golangci-lint's and ShellCheck's findings may be justified in
  place ([a finding](../../docs/checks.md#a-finding)).
