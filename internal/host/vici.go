package host

import (
	"context"
	"fmt"
	"iter"

	"github.com/strongswan/govici/vici"

	"github.com/zadykian/ikeward/internal/status"
)

// VICISocket is where charon, strongSwan's daemon, takes VICI's commands: the socket the packaged
// vici.conf gives, which only root may open.
const VICISocket = "/run/charon.vici"

// VICI is charon, which takes VICI's commands on the socket Socket.
type VICI struct {
	Socket string
	// NoBlock has charon list only the IKE SAs no thread of its own holds, rather than wait for
	// them. updown needs it: charon holds the IKE SA whose CHILD SA it runs updown for until
	// updown ends, so a list that waits for it never ends.
	NoBlock bool
}

// IKESAs returns the IKE SAs of the connection named conn, as charon lists them.
func (v VICI) IKESAs(ctx context.Context, conn string) ([]status.IKESA, error) {
	session, err := vici.NewSession(vici.WithSocketPath(v.Socket))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", v.Socket, reason(err))
	}
	defer session.Close() //nolint:errcheck // read only: closing loses nothing
	in := vici.NewMessage()
	if err := in.Set("ike", conn); err != nil {
		return nil, err
	}
	if v.NoBlock {
		if err := in.Set("noblock", "yes"); err != nil {
			return nil, err
		}
	}
	return ikeSAs(session.CallStreaming(ctx, "list-sas", "list-sa", in))
}

// viciIKESA is what ikeSAs reads of an IKE SA in a list-sa event.
type viciIKESA struct {
	State  string   `vici:"state"`
	Remote string   `vici:"remote-host"`
	VIPs   []string `vici:"local-vips"`
	// Children are keyed by the child's name and a unique ID, as ikeward-61.
	Children *vici.Message `vici:"child-sas"`
}

// viciChildSA is what ikeSAs reads of a CHILD SA.
type viciChildSA struct {
	Name     string   `vici:"name"`
	State    string   `vici:"state"`
	LocalTS  []string `vici:"local-ts"`
	RemoteTS []string `vici:"remote-ts"`
}

// ikeSAs reads the IKE SAs of list-sas' events, which hold one each, keyed by its connection's
// name. CHILD SAs keep charon's order.
func ikeSAs(events iter.Seq2[*vici.Message, error]) ([]status.IKESA, error) {
	var sas []status.IKESA
	for event, err := range events {
		if err != nil {
			return nil, fmt.Errorf("list-sas: %w", err)
		}
		for _, name := range event.Keys() {
			var in viciIKESA
			if err := unmarshal(event, name, &in); err != nil {
				return nil, err
			}
			sa := status.IKESA{State: in.State, Remote: in.Remote, VIPs: in.VIPs}
			for _, id := range keys(in.Children) {
				var c viciChildSA
				if err := unmarshal(in.Children, id, &c); err != nil {
					return nil, err
				}
				sa.Children = append(sa.Children, status.ChildSA{Name: c.Name, State: c.State,
					LocalTS: c.LocalTS, RemoteTS: c.RemoteTS})
			}
			sas = append(sas, sa)
		}
	}
	return sas, nil
}

// unmarshal reads m's section key into v.
func unmarshal(m *vici.Message, key string, v any) error {
	section, ok := m.Get(key).(*vici.Message)
	if !ok {
		return fmt.Errorf("list-sas: %s is no section", key)
	}
	if err := vici.UnmarshalMessage(section, v); err != nil {
		return fmt.Errorf("list-sas: %s: %w", key, err)
	}
	return nil
}

// keys are m's keys, none where m is nil.
func keys(m *vici.Message) []string {
	if m == nil {
		return nil
	}
	return m.Keys()
}
