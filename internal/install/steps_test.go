package install

import (
	"bytes"
	"testing"

	"github.com/zadykian/ikeward/internal/files"
)

// enable is the step that enables ikeward's units and strongSwan.
const enable = "systemctl enable ikeward-routing.service strongswan.service ikeward-watchdog.timer"

// A file that install changes makes the steps that read it due, and a change to networkd's or
// strongSwan's own settings restarts that daemon. On a system as install left it, nothing else is.
func TestStepsReadFiles(t *testing.T) {
	p := newProfile(t, "client")
	f, r := newFakeFiles(), newFakeRunner(Packages...)
	installOn(t, f, r, p)
	s := onHome(f, r, &bytes.Buffer{})
	for _, test := range []struct {
		changed string
		want    []string
	}{
		{"", nil},
		{"/etc/systemd/network/25-ikeward0.network", []string{"networkctl reload"}},
		{"/etc/systemd/networkd.conf.d/10-keep-vpn-routing.conf",
			[]string{"systemctl restart systemd-networkd.service"}},
		{"/etc/strongswan.d/zz-ikeward.conf", []string{"systemctl restart strongswan.service"}},
		{connection, []string{"swanctl --load-all --noprompt"}},
		{"/etc/swanctl/private/client-key.pem", []string{"swanctl --load-all --noprompt"}},
		{"/etc/systemd/system/ikeward-watchdog.service", []string{"systemctl daemon-reload"}},
		{timer, []string{"systemctl daemon-reload", enable, "systemctl start ikeward-watchdog.timer"}},
		{"/etc/systemd/system/ikeward-routing.service", []string{"systemctl daemon-reload", enable,
			"systemctl restart ikeward-routing.service"}},
		{files.SettingsFile, []string{"systemctl restart ikeward-routing.service"}},
	} {
		var changed []string
		if test.changed != "" {
			changed = []string{test.changed}
		}
		got := s.plan(newProbes(s), changed, owed{}).lines()
		checkLines(t, "the steps due as "+test.changed+" changes", got, test.want)
	}
}
