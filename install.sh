#!/bin/sh
# Installs ikeward, as root, from the releases of its GitHub repository, each of which publishes
# this script, through curl:
#
#   set -o pipefail
#   curl -fsSL --proto '=https' --tlsv1.2 \
#       https://github.com/zadykian/ikeward/releases/latest/download/install.sh | sh
#
# Without pipefail, a curl that fails pipes nothing into sh, which exits 0 having installed nothing.
#
# It downloads the binary for this machine, ikeward-linux-ARCH, from the latest release, or from
# the one IKEWARD_VERSION names (0.1.0 or later, the first release), checks it against the
# release's ikeward.sha256, runs it for its version, and only then moves it to
# /usr/local/sbin/ikeward, where its units and strongSwan run it, replacing any ikeward there: run
# again, it updates ikeward. It finds which release is the latest once, before either download, so
# that both come from one release. IKEWARD_INSTALL_DIR and IKEWARD_REPO stand in for
# /usr/local/sbin and zadykian/ikeward, for the tests. It runs in any POSIX sh, and needs curl and
# sha256sum.
#
# Everything is in functions that the last line calls, so that a download cut short runs nothing.

set -eu

# say prints a message of the installer's.
say() {
    printf 'install.sh: %s\n' "$*" >&2
}

# die prints a message of the installer's and exits with status 1.
die() {
    say "$*"
    exit 1
}

# platform sets binary to the name the releases give ikeward for this machine.
platform() {
    os=$(uname -s)
    arch=$(uname -m)
    [ "$os" = Linux ] || die "ikeward is released for Linux, not $os"
    case $arch in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "ikeward is released for x86_64 and aarch64, not $arch" ;;
    esac
    binary=ikeward-linux-$arch
}

# is_version succeeds where $1 is a version X.Y.Z, as a tag vX.Y.Z holds it: three numbers, none
# empty or with a leading zero.
is_version() {
    case $1 in
    *[!0-9.]* | .* | *. | *..* | *.*.*.* | 0[0-9]* | *.0[0-9]*) return 1 ;;
    *.*.*) return 0 ;;
    *) return 1 ;;
    esac
}

# release sets tag to the release to install, IKEWARD_VERSION's, or none for the latest until
# latest finds it, and where to name it in messages.
release() {
    repo=${IKEWARD_REPO:-zadykian/ikeward}
    version=${IKEWARD_VERSION:-}
    tag=
    if [ -n "$version" ]; then
        # A typo stops here, not at a download that is not found.
        is_version "${version#v}" || die "IKEWARD_VERSION is not a version, X.Y.Z: $version"
        tag=v${version#v}
        case $tag in
        v0.0.*) die "ikeward $version was never released: 0.1.0 is the first" ;;
        esac
    fi
    where="the ${tag:-latest} release of $repo"
}

# fetch runs curl over HTTPS alone, TLS 1.2 or later, with the options $@. A reply with an HTTP
# error fails it, rather than its page taking the place of a file.
fetch() {
    curl -fsS --proto '=https' --tlsv1.2 "$@"
}

# latest sets tag, where none is set, to the latest release's: GitHub redirects releases/latest to
# the latest release's page, releases/tag/vX.Y.Z, or to releases where there is none. The tag goes
# into the downloads' addresses, so anything but vX.Y.Z is refused.
latest() {
    [ -z "$tag" ] || return 0
    page=$(fetch -o /dev/null -w '%{redirect_url}' "https://github.com/$repo/releases/latest") ||
        die "cannot find $where"
    found=${page##*/releases/tag/v}
    if [ "$found" = "$page" ] || ! is_version "$found"; then
        die "cannot find $where: GitHub redirects to '$page'"
    fi
    tag=v$found
    where="$where, $tag"
}

# download writes the file $1 of the release tag to the file $2, or to stdout for -, following
# GitHub's redirect to where it keeps the file.
download() {
    fetch -L -o "$2" "https://github.com/$repo/releases/download/$tag/$1"
}

# sha256 prints the SHA-256 checksum of the file $1.
sha256() {
    sha256sum <"$1" | awk '{ print $1 }'
}

main() {
    platform
    release
    # ikeward goes where only root may write, and runs as root.
    [ "$(id -u)" = 0 ] || die "ikeward installs and runs as root: run install.sh as root"
    command -v curl >/dev/null || die "curl is needed to download ikeward's releases"
    command -v sha256sum >/dev/null || die "sha256sum is needed to check ikeward's checksum"

    latest
    sums=$(download ikeward.sha256 -) || die "cannot download ikeward.sha256 from $where"
    expected=$(printf '%s\n' "$sums" |
        awk -v name="$binary" '$2 == name || $2 == "*" name { print $1 }')
    [ -n "$expected" ] || die "ikeward.sha256 of $where has no checksum for $binary"

    dir=${IKEWARD_INSTALL_DIR:-/usr/local/sbin}
    mkdir -p "$dir" || die "cannot create $dir"
    dir=$(CDPATH='' cd -- "$dir" && pwd)
    # The download goes next to ikeward, so that moving it there replaces ikeward at once, even
    # while strongSwan or the watchdog runs it, and so that it runs from where ikeward will.
    new=
    trap '[ -z "$new" ] || rm -f "$new"' EXIT
    trap 'exit 1' HUP INT TERM
    new=$(mktemp "$dir/.ikeward.XXXXXX") || die "cannot write to $dir"
    download "$binary" "$new" || die "cannot download $binary from $where"
    [ "$(sha256 "$new")" = "$expected" ] ||
        die "$binary does not match its checksum in ikeward.sha256 of $where"
    chmod 755 "$new"
    installed=$("$new" --version </dev/null) || die "the $binary downloaded does not run"
    mv -f "$new" "$dir/ikeward" || die "cannot move ikeward to $dir"
    new=
    echo "installed $installed as $dir/ikeward"
}

main
