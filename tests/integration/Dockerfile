# The images of the integration test's three services, a stage each, which compose.yaml builds:
# Ubuntu 26.04 with its packages, strongSwan 6.0.4 among them, as on a live server.
FROM ubuntu:26.04 AS base
ENV DEBIAN_FRONTEND=noninteractive

# The VPN server: charon without systemd, in the foreground, and ip, which routes the remote
# network's second range.
FROM base AS server
RUN apt-get update \
    && apt-get install -y --no-install-recommends charon-systemd iproute2 strongswan-swanctl \
    && rm -rf /var/lib/apt/lists/*
COPY server/strongswan.conf /etc/strongswan.d/zz-server.conf
COPY server/swanctl.conf /etc/swanctl/conf.d/server.conf
CMD ["/usr/sbin/charon-systemd"]

# A host on the remote network, or on the client's LAN: its DNS server, ping, and peer, which
# socat runs to answer a connection with the address it came from.
FROM base AS far
RUN apt-get update \
    && apt-get install -y --no-install-recommends dnsmasq-base iproute2 iputils-ping socat \
    && rm -rf /var/lib/apt/lists/*
COPY --chmod=755 <<'EOF' /usr/local/bin/peer
#!/bin/sh
echo "$SOCAT_PEERADDR"
EOF

# The server that ikeward installs on: systemd with networkd and resolved, and Docker, whose
# containers reach the remote network through ikeward's NAT rule. strongSwan's packages are
# those install adds where they are missing: it finds them, and needs no network. probe.tar is an
# image of busybox alone, for Docker to import and run with no registry.
FROM base AS client
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        dbus iproute2 nftables systemd systemd-resolved \
        strongswan strongswan-swanctl charon-systemd libcharon-extra-plugins \
        docker.io busybox-static \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir /tmp/probe && cp /usr/bin/busybox /tmp/probe/ \
    && tar -C /tmp/probe -cf /opt/probe.tar . && rm -r /tmp/probe
# The container is privileged, so most of these units would reach the host's kernel and devices
# rather than the container's: its sysctls, modules, binfmt_misc, TPM, pstore, entropy, EFI
# variables, clock, consoles and disks. apt's timers and motd-news have no network to reach.
# networkd manages no link at boot, Docker having set its links up, so wait-online would hold back
# Docker's start for its 2 minutes. dockerd loads its AppArmor profile into the host's kernel
# where apparmor_parser is installed, so it must not be.
RUN systemctl enable systemd-networkd.service \
    && systemctl mask \
        systemd-sysctl.service systemd-modules-load.service systemd-binfmt.service \
        proc-sys-fs-binfmt_misc.automount proc-sys-fs-binfmt_misc.mount \
        systemd-pcrextend.socket systemd-pcrlock.socket systemd-pcrmachine.service \
        systemd-pcrnvdone.service systemd-pcrphase.service systemd-pcrphase-sysinit.service \
        systemd-pcrproduct.service systemd-tpm2-setup.service systemd-tpm2-setup-early.service \
        systemd-pstore.service systemd-random-seed.service systemd-hibernate-clear.service \
        systemd-timedated.service systemd-logind.service systemd-mute-console.socket \
        getty.target getty@.service getty-static.service console-getty.service \
        serial-getty@.service container-getty@.service \
        systemd-ask-password-console.path systemd-ask-password-wall.path \
        sys-kernel-config.mount sys-kernel-debug.mount sys-kernel-tracing.mount \
        sys-fs-fuse-connections.mount \
        fstrim.timer e2scrub_all.timer e2scrub_reap.service \
        apt-daily.timer apt-daily-upgrade.timer dpkg-db-backup.timer motd-news.timer \
        systemd-networkd-wait-online.service \
    && ! command -v apparmor_parser
# boot keeps the host's kernel out of the client's reach before systemd starts, as systemd's
# interface for containers has it. /sys is read-only: where it is writable, networkd waits for
# udev to set links up, and udev must not run here, where it would act on the host's devices.
# /proc/sys is read-only but for /proc/sys/net, the client's own network namespace's, so that
# systemd and dockerd set no global sysctl. /dev/kmsg, the host's in a privileged container, is
# /dev/null, so that journald and systemd log nothing into the host's kernel log; journald, which
# cannot poll /dev/null, no longer reads it into the client's journal. Each remount changes a
# mount's flag alone, the cgroups stay writable, and the /dev it changes is the container's own.
COPY <<'EOF' /etc/systemd/journald.conf.d/no-kmsg.conf
[Journal]
ReadKMsg=no
EOF
COPY --chmod=755 <<'EOF' /usr/local/sbin/boot
#!/bin/sh
set -e
mount -o remount,bind,ro /sys
mount --bind /proc/sys /proc/sys
mount --bind /proc/sys/net /proc/sys/net
mount -o remount,bind,ro /proc/sys
rm -f /dev/kmsg
ln -s /dev/null /dev/kmsg
exec /usr/lib/systemd/systemd
EOF
STOPSIGNAL SIGRTMIN+3
CMD ["/usr/local/sbin/boot"]
