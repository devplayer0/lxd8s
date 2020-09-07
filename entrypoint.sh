#!/bin/sh

random_mac() {
    printf '%02x' $((0x$(od /dev/urandom -N1 -t x1 -An | cut -c 2-) & 0xFE | 0x02)); od /dev/urandom -N5 -t x1 -An | sed 's/ /:/g'
}

b64() {
    base64 - | tr -d '\n'
}

extract_secret() {
    printf '%s' $1 | jq -r .data[\"$2\"] | base64 -d
}

setup_network() {
    until curl -f -s localhost:8181/health > /dev/null; do sleep 0.5; done
    overlay_iface="$(curl -s localhost:8181/config | jq -r .VXLAN.Interface)"
    mtu="$(ip link show dev $overlay_iface | grep mtu | awk '{ print $5 }')"

    ip link show lxd-bridge > /dev/null 2>&1 && ip link del lxd-bridge
    ip link add lxd-bridge type bridge
    ip link set dev lxd-bridge up
    ip link set dev lxd-bridge mtu "$mtu"
    ip addr add 192.168.69.1/30 dev lxd-bridge

    ip link set dev "$overlay_iface" master lxd-bridge

    ip link show vm > /dev/null 2>&1 && ip link del vm
    ip tuntap add vm mode tap
    ip link set dev vm up
    ip link set dev vm master lxd-bridge

    iptables -t nat -F
    iptables -t nat -A POSTROUTING -s 192.168.69.2 -j SNAT --to-source "$(hostname -i)"
    iptables -t nat -A PREROUTING -d "$(hostname)" -p tcp --dport 443 -j DNAT --to-destination 192.168.69.2

    CMDLINE="$CMDLINE k8s_mtu=$mtu"
}

make_overlay() {
    mkdir /tmp/lxd_overlay

    if [ -n "$CERT_SECRET_BASE" ]; then
        source k8s.sh
        INDEX="$(hostname | sed -r 's|^.*-([0-9]+)|\1|')"
        data="$(k8s_get "api/v1/namespaces/$K8S_NAMESPACE/secrets/${CERT_SECRET_BASE}${INDEX}")"

        extract_secret "$data" "ca.crt" > /tmp/lxd_overlay/ca.crt
        extract_secret "$data" "tls.crt" > /tmp/lxd_overlay/server.crt
        extract_secret "$data" "tls.key" > /tmp/lxd_overlay/server.key
    fi

    tar -C /tmp/lxd_overlay -cf lxd_overlay.tar .
    rm -r /tmp/lxd_overlay
}

mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200

CMDLINE="console=ttyS0 noapic reboot=k panic=1 pci=off"
CMDLINE="$CMDLINE hostname=$(hostname)"
CMDLINE="$CMDLINE resolvconf=$(sed 's|^nameserver 127..*|nameserver 1.1.1.1|' < /etc/resolv.conf | b64)"

[ -n "$KUBELAN" ] && [ "$KUBELAN" != "no" ] && setup_network
make_overlay

[ -e "$LXD_DATA" ] || truncate -s 4G "$LXD_DATA"
[ -e "$LXD_STORAGE" ] || truncate -s 16G "$LXD_STORAGE"


rm -f /run/firecracker.sock
exec firectl \
    --socket-path /run/firecracker.sock \
    --ncpus $CPUS \
    --memory $MEM \
    --tap-device "vm/$(random_mac)" \
    --kernel ./vmlinux \
    --kernel-opts "$CMDLINE" \
    --root-drive ./rootfs.img \
    --add-drive "$LXD_DATA:rw" \
    --add-drive "./lxd_overlay.tar:ro" \
    --add-drive "$LXD_STORAGE:rw"
