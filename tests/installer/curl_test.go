package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// fakeCurl stands in for curl and GitHub. The release TAG's file NAME is RELEASES/TAG/NAME, and
// RELEASES/latest names the latest release's tag. Only zadykian/ikeward's releases are found, over
// HTTPS alone. It logs each address and where its reply goes. It writes nothing outside FAKE_ROOT,
// into its tools, which link to the host's, or through a link, whatever the installer asks.
const fakeCurl = `#!/bin/sh
refuse() { echo "fake curl: $*" >&2; exit 2; }
fail= follow= remote= out=- write= url= proto= tls=
while [ $# -gt 0 ]; do
    case $1 in
    --proto) proto=$2; shift 2 ;;
    --tlsv1.2) tls=1; shift ;;
    -[!-]*)
        flags=${1#-}; shift
        while [ -n "$flags" ]; do
            rest=${flags#?}; flag=${flags%"$rest"}; flags=$rest
            case $flag in
            f) fail=1 ;; s | S) ;; L) follow=1 ;; O) remote=1 ;;
            o) out=$1; shift ;;
            w) write=$1; shift ;;
            *) refuse "unknown option -$flag" ;;
            esac
        done ;;
    -*) refuse "unknown option $1" ;;
    *) url=$1; shift ;;
    esac
done
[ -n "$remote" ] && out=${url##*/}
printf '%s > %s\n' "$url" "$out" >>"$FAKE_CURL_LOG"
[ "$proto" = =https ] && [ -n "$tls" ] || refuse "not HTTPS alone, TLS 1.2"
path=$out
case $path in - | /*) ;; *) path=$(pwd)/$path ;; esac
case $path in
*/..* | "$FAKE_ROOT"/tools/*) refuse "$out is not the test's" ;;
- | /dev/null | "$FAKE_ROOT"/*) ;;
*) refuse "$out is not the test's" ;;
esac
[ ! -L "$path" ] || refuse "$out is a link"
# reply writes what the file $1 holds as the reply, or a 404 page where there is none, which
# fails only with -f.
reply() {
    if [ ! -f "$1" ]; then
        [ -z "$fail" ] || { echo "curl: (22) The requested URL returned error: 404" >&2; exit 22; }
        set -- "$FAKE_ROOT/not-found"
        echo "Not Found" >"$1"
    fi
    if [ "$out" = - ]; then cat "$1"; else cat "$1" >"$out"; fi
}
releases=https://github.com/zadykian/ikeward/releases
latest=$(cat "$FAKE_RELEASES/latest" 2>/dev/null)
case $url in
"$releases"/latest)
    # GitHub redirects to releases where there is no release.
    [ "$write" != '%{redirect_url}' ] || printf '%s' "$releases${latest:+/tag/$latest}"
    : >"$FAKE_ROOT/redirect" && reply "$FAKE_ROOT/redirect" ;;
"$releases"/latest/download/*)
    [ -n "$follow" ] || { : >"$FAKE_ROOT/redirect" && reply "$FAKE_ROOT/redirect"; exit; }
    reply "$FAKE_RELEASES/${latest:-none}/${url##*/}" ;;
"$releases"/download/*/*)
    [ -n "$follow" ] || { : >"$FAKE_ROOT/redirect" && reply "$FAKE_ROOT/redirect"; exit; }
    reply "$FAKE_RELEASES/${url#"$releases"/download/}" ;;
*) reply "$FAKE_RELEASES/none" ;;
esac
`

// The fake curl writes nothing outside the test's directory, into its tools or through a link,
// whatever the installer asks. It serves nothing but over HTTPS alone, TLS 1.2 or later.
func TestFakeCurl(t *testing.T) {
	t.Parallel()
	in := newInstaller(t)
	in.publish(t, "latest", "0.2.0")
	outside := t.TempDir()
	out, decoy := filepath.Join(outside, "ikeward"), filepath.Join(outside, "decoy")
	if err := os.WriteFile(decoy, []byte("decoy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(in.tools, "decoy")
	for _, link := range []string{tool, filepath.Join(in.work, "link")} {
		if err := os.Symlink(decoy, link); err != nil {
			t.Fatal(err)
		}
	}
	url := releases + "/download/v0.2.0/ikeward-linux-amd64"
	const https = "curl -fsSL --proto =https --tlsv1.2 "
	for script, refusal := range map[string]string{
		https + "-o " + out + " " + url:         out + " is not the test's",
		https + "-o ../../ikeward " + url:       "../../ikeward is not the test's",
		https + "-o " + tool + " " + url:        tool + " is not the test's",
		https + "-o link " + url:                "link is a link",
		"curl -fsSL --tlsv1.2 -o - " + url:      "not HTTPS alone, TLS 1.2",
		"curl -fsSL --proto =https -o - " + url: "not HTTPS alone, TLS 1.2",
	} {
		got := in.run(t, "sh", script+"\n", nil)
		if want := (result{2, "", "fake curl: " + refusal + "\n"}); got != want {
			t.Errorf("%s: got %+v, want %+v", script, got, want)
		}
	}
	for _, path := range []string{out, filepath.Join(in.root, "..", "ikeward")} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the fake curl wrote %s, or it cannot be read: %v", path, err)
		}
	}
	if data, err := os.ReadFile(decoy); err != nil || string(data) != "decoy\n" {
		t.Errorf("the fake curl wrote %s through a link: %q, %v", decoy, data, err)
	}
}
