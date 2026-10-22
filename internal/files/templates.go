package files

// connectionText is the swanctl connection: IKEv2, a login by certificate, and the server checked
// by the profile's CA. It takes an address from the server, and asks for every address, which the
// server narrows to the remote network's ranges, out Link. It comes up from boot, trying until it
// connects. Values from the profile go through value, so that none can end a line or a section.
const connectionText = `{{.Header}}
connections {
    {{.Connection}} {
        remote_addrs = {{value .Server}}
{{- if ne .Port 500}}
        remote_port  = {{.Port}}
{{- end}}
        version      = 2
        proposals    = {{value .IKEProposals}}
        # An address from the server, which strongSwan puts on {{.Link}}.
        vips         = 0.0.0.0
        # A reconnect replaces the old SA rather than leave it beside the new.
        unique       = replace
        dpd_delay    = 30s
        # charon tries again each time the server leaves an attempt unanswered,
        # until the tunnel connects or an error ends it for good, as a refused
        # login. swanctl's default, 1, gives up after one: 165 s of retransmits.
        keyingtries  = 0

        local-1 {
            auth  = pubkey
            certs = {{value (print .Name "-cert.pem")}}
{{- if .LocalID}}
            id    = {{value .LocalID}}
{{- end}}
        }
        remote-1 {
            auth    = pubkey
            id      = {{value .RemoteID}}
            # Only the profile's CA may sign the server's certificate.
            cacerts = {{.CACert}}
        }

        children {
            {{.Connection}} {
                # Every address, which the VPN server narrows to the remote
                # network's ranges. Those go through the tunnel, out
                # {{.Link}}, whose ID the SAs and their policies take: even a
                # grant of every address takes only what {{.Link}} routes,
                # never IKE's own.
                remote_ts     = 0.0.0.0/0
                if_id_in      = {{.InterfaceID}}
                if_id_out     = {{.InterfaceID}}
                esp_proposals = {{value .ESPProposals}}
                # Routes each range the server grants through {{.Link}} as the
                # tunnel goes up, with its floor, and records it for the floors
                # at boot. Removes the route as the tunnel goes down.
                updown        = {{.Binary}} updown
                # Up as strongSwan starts, at boot or a restart, and again in a
                # new IKE SA where the server stops answering DPD's checks, or
                # another exchange, once charon's retransmits run out.
                start_action  = start
                dpd_action    = restart
                # Nothing where the server closes the SA. A server that keeps
                # one SA per identity deletes this host's as a phone or a
                # laptop logs in with the profile's identity, which its .sswan
                # and .mobileconfig set up. With start, this host and that
                # device would replace each other's SA in a loop. The tunnel
                # stays down until the watchdog restarts strongSwan.
                close_action  = none
            }
        }
    }
}
`

// dropInText is the one strongSwan drop-in. strongswan.conf includes /etc/strongswan.d/*.conf
// after the plugins' files, so what it sets wins over theirs, and zz- puts it last of its own.
const dropInText = `{{.Header}}
# strongSwan reads it after the packaged files, which stay as shipped.
charon {
    # The address from the server goes on {{.Link}}, the tunnel's link, which
    # networkd keeps up and leaves alone, not on the uplink, whose DHCP renewals
    # and reconfigures dropped it. resolved sends the remote network's names
    # out {{.Link}}, which drops them while no SA takes them.
    install_virtual_ip_on = {{.Link}}
    # charon still puts the address from the server there, but neither tells
    # the server of the link's own address nor roams as networkd changes it.
    interfaces_ignore = {{.Link}}
    # No rule of charon's: its default, a rule that looks up table {{.Table}} before
    # main, would put the remote network before the server's own. ikeward
    # routing's rules look the table up after main's own networks, and ikeward
    # updown routes the remote network's ranges there, through {{.Link}}.
    routing_table = 0
    # Where the server's name does not resolve, charon tries to connect again
    # every 30 seconds rather than give up at once. The connection's
    # keyingtries tries again where the server does not answer.
    retry_initiate_interval = 30s

    plugins {
        resolve {
            # The server pushes its resolver for every name. {{.Link}}'s
            # network sends the remote network's names alone to the one
            # install was given.
            load = no
        }
    }
}
`

// netdevText is the tunnel's XFRM interface, which holds the address from the server.
const netdevText = `{{.Header}}
# The tunnel: what goes out here goes through the SAs whose ID is InterfaceId,
# or nowhere. It holds the address from the server: see
# /etc/strongswan.d/zz-ikeward.conf.
[NetDev]
Name={{.Link}}
Kind=xfrm

[Xfrm]
InterfaceId={{.InterfaceID}}
# On no underlying link: the SAs go out by whichever link reaches the server.
Independent=yes
`

// networkText sends lookups of the remote network's names to its resolver, through the tunnel.
// While the tunnel is down, LinkULA keeps them on Link, as docs/status.md says.
const networkText = `{{.Header}}
[Match]
Name={{.Link}}

[Network]
# The remote network's resolver, through the tunnel, for its names alone:
# they are no search domains, and no other lookup goes there.
DNS={{.Settings.DNS}}
Domains={{join .Settings.RoutingDomains " "}}
DNSDefaultRoute=no
# resolved sends lookups on a link only while it has a routable address.
# With this one, they stay here while the tunnel is down, where the link
# drops them, rather than go to the uplink's resolver in the clear.
# IPv6, so that their IPv4 source stays the address from the server.
Address={{.LinkULA}}
# strongSwan, not networkd, puts the address from the server on this link:
# a reconfigure keeps it.
KeepConfiguration=yes

[Link]
ActivationPolicy=always-up
`

// keepRoutingText keeps networkd off what strongSwan sets.
const keepRoutingText = `{{.Header}}
# networkd leaves alone the routes and rules it did not set: ikeward's rules,
# and its routes and floors in table {{.Table}}. Without this, a DHCP renewal or a
# reconfigure, which Docker's links set off, removed them: the tunnel stayed
# up, and nothing went through it.
[Network]
ManageForeignRoutes=no
ManageForeignRoutingPolicyRules=no
`

// serviceText runs the watchdog once; the timer starts it.
const serviceText = `{{.Header}}
[Unit]
Description=ikeward's watchdog of the VPN
Documentation=https://github.com/zadykian/ikeward
After=strongswan.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart={{.Binary}} watchdog
# The watchdog restarts strongSwan itself, and exits 1 while the VPN stays
# unhealthy, which is no failure of the unit; it exits 3 where it fails itself.
SuccessExitStatus=0 1
TimeoutStartSec=120
# journalctl -t {{.Watchdog}} shows its lines. It prints nothing while the
# VPN is up, so each line is a warning: what failed, or what it did.
SyslogIdentifier={{.Watchdog}}
SyslogLevel=warning
`

// timerText runs the watchdog every 5 minutes, from 3 minutes after boot. It sets no
// Persistent=true, which systemd applies only to OnCalendar= timers.
const timerText = `{{.Header}}
[Unit]
Description=Run ikeward's watchdog every 5 minutes

[Timer]
# The first run once the network has settled after boot.
OnBootSec=3min
OnUnitActiveSec=5min
RandomizedDelaySec=30
AccuracySec=10s
Unit={{.Watchdog}}.service

[Install]
WantedBy=timers.target
`

// routingText runs ikeward routing as the server boots, before network-pre.target, which networkd
// waits for, and with no default dependencies, as Debian's nftables.service has it. Those would
// order it after sysinit.target, and networkd's start with it. The unit runs before strongSwan
// too, so that no traffic to the remote network takes the default route while the tunnel comes up.
const routingText = `{{.Header}}
[Unit]
Description=ikeward's routing: the server's networks before the remote network
Documentation=https://github.com/zadykian/ikeward
# Before any link comes up, and before strongSwan. As nftables.service, it has
# no default dependencies, which would hold it, and networkd, back until
# sysinit.target.
DefaultDependencies=no
Wants=network-pre.target
Before=network-pre.target strongswan.service shutdown.target
Conflicts=shutdown.target
# After nftables.service, where it is enabled, whose flush ruleset would remove
# the NAT table; and once the file systems that hold ikeward and the ranges it
# learned are mounted.
After=nftables.service
RequiresMountsFor={{.Binary}} /var/lib/ikeward

[Service]
Type=oneshot
RemainAfterExit=yes
# Makes the rules, the floors in table {{.Table}} and the NAT table, which stay once
# the unit stops, until uninstall removes them. There is a floor for each
# range {{.SettingsFile}} gives, and one for each range
# /var/lib/ikeward/learned-ranges records. A restart adds what is missing, and
# makes the NAT table anew, in one transaction.
ExecStart={{.Binary}} routing

[Install]
WantedBy=multi-user.target
`

// settingsText is SettingsFile, as ParseSettings reads it.
const settingsText = `{{.Header}}
# The remote network's values, which install's flags gave: --dns, each --domain,
# --probe and each --floor. ikeward routing, status and the watchdog read them.
dns {{.Settings.DNS}}
{{- range .Settings.Domains}}
domain {{.}}
{{- end}}
probe {{.Settings.Probe}}
{{- range .Settings.Floors}}
floor {{.}}
{{- end}}
`
