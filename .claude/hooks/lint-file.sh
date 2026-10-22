#!/bin/sh
# Claude Code's PostToolUse hook for Edit and Write (.claude/settings.json): runs make lint-local's
# gates on the file just written, those of its type, and gitleaks and denycheck on any. A finding
# goes to stderr with status 2, which Claude sees. A file outside this repository, or one git
# ignores, such as a plan, is passed over.

set -u

# file_path prints tool_input.file_path of the hook's JSON on stdin. sed reads it, as jq may be
# missing; a quote in a JSON string is escaped, so only a key matches.
file_path() {
    tr '\n' ' ' | sed -E -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"(([^"\\]|\\.)*)".*/\1/p'
}

# common prints the git common directory of the directory $1, which every work tree of a
# repository shares.
common() {
    git -C "$1" rev-parse --path-format=absolute --git-common-dir 2>/dev/null
}

# gate runs a command from the repository's root. Where it fails, gate adds its output to $report,
# and fails too.
gate() {
    "$@" >"$tmp/out" 2>&1 && return 0
    printf '%s fails:\n' "$*" >>"$report"
    cat "$tmp/out" >>"$report"
    return 1
}

# gates runs the gates of the file at $path by its type, as make lint-local runs them on every file.
gates() {
    case $path in
    *.go)
        if [ -n "$(gofmt -l "$path" 2>&1)" ]; then
            printf 'gofmt -l %s lists it:\n' "$path" >>"$report"
            gofmt -d "$path" >>"$report" 2>&1
        fi
        gate tools/run golangci-lint run --allow-serial-runners "./$(dirname -- "$path")/"
        gate tools/valecheck "$path"
        ;;
    *.md)
        gate tools/valecheck "$path"
        gate tools/run lychee --offline --include-fragments --no-progress "$path"
        gate tools/run rumdl check --no-cache --deny-config-warnings "$path"
        ;;
    .github/workflows/*.yml | .github/workflows/*.yaml)
        # A ShellCheck that tools/run cannot fetch would turn actionlint's off.
        if gate tools/run -path shellcheck; then
            gate tools/run actionlint -shellcheck="$(tools/run -path shellcheck)" -pyflakes= "$path"
        fi
        gate tools/run zizmor --strict-collection --offline "$path"
        ;;
    .golangci.yml)
        gate tools/run golangci-lint config verify
        ;;
    esac
    # A shell script, by its name or its shebang, as make vet finds them, read as POSIX sh. Where
    # sizecheck fails, its own gate below says so, and where tools/run cannot fetch a tool, the
    # tool's gate does.
    if [ -n "$(go run ./tools/sizecheck -scripts "$path" 2>/dev/null)" ]; then
        gate tools/run shellcheck -s sh "$path"
        gate tools/run shfmt -ln posix -d -i 4 "$path"
    fi
    gate go run ./tools/sizecheck "$path"
    # Every file: secrets, and an employer's terms, as make lint-local reads them in the whole
    # tree. gitleaks reads no .gitleaksignore. A branch from before denycheck has no
    # tools/denycheck.
    gate tools/run gitleaks dir --config=.gitleaks.toml --ignore-gitleaks-allow \
        --gitleaks-ignore-path=/dev/null --no-banner --redact --verbose "$path"
    if [ -d tools/denycheck ]; then
        gate go run ./tools/denycheck "$path"
    fi
}

main() {
    file=$(file_path)
    # A path with an escape in its JSON, a backslash or a quote, is none of the repository's.
    case $file in
    '' | *\\*) return 0 ;;
    esac
    [ -f "$file" ] || return 0
    dir=$(dirname -- "$file")
    repo=$(common "$(dirname -- "$0")")
    if [ -z "$repo" ] || [ "$(common "$dir")" != "$repo" ]; then
        return 0
    fi
    # The file's own work tree, which may be a linked one, and the file's path in it. A branch
    # from before the gates has no .vale.ini.
    cd -- "$dir" || return 0
    path=$(git rev-parse --show-prefix)$(basename -- "$file")
    cd -- "$(git rev-parse --show-toplevel)" || return 0
    if git check-ignore -q -- "$path" || [ ! -f .vale.ini ]; then
        return 0
    fi

    tmp=$(mktemp -d) || return 0
    trap 'rm -rf "$tmp"' EXIT
    report=$tmp/report
    : >"$report"
    gates
    if [ -s "$report" ]; then
        cat "$report" >&2
        return 2
    fi
}

main "$@"
