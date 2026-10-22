#!/bin/sh
# Claude Code's SessionStart and Stop hooks (.claude/settings.json), in the work tree Claude is in
# where it is one of this repository's. As a session starts, it notes the work tree's fingerprint.
# As Claude ends its turn, it runs make -k lint-local where the fingerprint differs from that note
# and from the last one it ran on, and notes each fingerprint it runs on, pass or fail. So a turn
# that changed nothing, or the work the session started on, runs nothing. A failure prints the
# targets that failed and the end of the output to stderr with status 2, and Claude goes on; then
# stop_hook_active is true, and the hook lets Claude stop.

set -u

# common prints the git common directory of the directory $1, which every work tree of a
# repository shares.
common() {
    git -C "$1" rev-parse --path-format=absolute --git-common-dir 2>/dev/null
}

# sets says whether the hook's JSON, in $input, sets the key $1 to the JSON value $2. A quote in a
# JSON string is escaped, so only a key matches.
sets() {
    printf '%s\n' "$input" | tr '\n' ' ' |
        grep -q "\"$1\"[[:space:]]*:[[:space:]]*$2"
}

# fingerprint prints a sum of the work tree as git sees it: HEAD, the diff from it, and the names
# and contents of the files git neither tracks nor ignores.
fingerprint() {
    {
        git rev-parse HEAD
        git diff --binary --no-ext-diff HEAD --
        git ls-files -z --others --exclude-standard
        git ls-files -z --others --exclude-standard | xargs -0 git hash-object --
    } 2>/dev/null | git hash-object --stdin
}

main() {
    # All of stdin is read first.
    input=$(cat)
    # A work tree of another repository, or none, is passed over, and so is a branch from before
    # make lint-local.
    repo=$(common "$(dirname -- "$0")")
    if [ -z "$repo" ] || [ "$(common .)" != "$repo" ]; then
        return 0
    fi
    cd -- "$(git rev-parse --show-toplevel)" || return 0
    grep -q '^lint-local:' Makefile 2>/dev/null || return 0
    # The notes sit in the work tree's own git directory.
    notes=$(git rev-parse --absolute-git-dir) || return 0
    if sets hook_event_name '"SessionStart"'; then
        fingerprint >"$notes/claude-lint-start"
        return 0
    fi
    if ! sets hook_event_name '"Stop"' || sets stop_hook_active true; then
        return 0
    fi
    now=$(fingerprint)
    for note in "$notes/claude-lint-start" "$notes/claude-lint-checked"; do
        [ "$(cat -- "$note" 2>/dev/null)" != "$now" ] || return 0
    done

    log=$(mktemp) || return 0
    trap 'rm -f "$log"' EXIT
    make -k lint-local >"$log" 2>&1
    status=$?
    # A failure is reported once: a later turn that leaves the tree as it is runs nothing.
    printf '%s\n' "$now" >"$notes/claude-lint-checked"
    [ "$status" -ne 0 ] || return 0
    printf 'make -k lint-local fails at:\n' >&2
    grep 'make: \*\*\*' "$log" >&2
    printf 'Its output ends with:\n' >&2
    tail -n 50 "$log" >&2
    return 2
}

main "$@"
