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
    #ip link show vm-bridge && ip link del vm-bridge
    #ip link add vm-bridge type bridge
    #ip link set dev vm-bridge up

    ip link show vm 2>&1 > /dev/null && ip link del vm
    ip tuntap add vm mode tap
    ip link set dev vm up
    #ip link set dev vm master vm-bridge

    #ip addr del "$ip" dev "$eth0"
    #ip link set dev "$eth0" master vm-bridge
}

mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200

eth0="$(ip route list default | awk '{ print $5 }')"
gw="$(ip route list default | awk '{ print $3 }')"
ip="$(ip addr show $eth0 | grep 'scope global' | awk '{ print $2 }')"

setup_network

CMDLINE="console=ttyS0 noapic reboot=k panic=1 pci=off"
CMDLINE="$CMDLINE hostname=$(hostname)"
#CMDLINE="$CMDLINE k8s_ip=$ip k8s_gw=$gw"
CMDLINE="$CMDLINE resolvconf=$(sed 's|^nameserver 127..*|nameserver 1.1.1.1|' < /etc/resolv.conf | b64)"

mkdir /tmp/lxd_overlay

if [ -n "$CERT_SECRET_BASE" ]; then
    source k8s.sh
    INDEX="$(hostname | sed -r 's|^.*-([0-9]+)|\1|')"
    data="$(k8s_get "api/v1/namespaces/$K8S_NAMESPACE/secrets/${CERT_SECRET_BASE}${INDEX}")"

    extract_secret "$data" "ca.crt"  > /tmp/lxd_overlay/ca.crt
    extract_secret "$data" "tls.crt"  > /tmp/lxd_overlay/server.crt
    extract_secret "$data" "tls.key"  > /tmp/lxd_overlay/server.key
fi

tar -C /tmp/lxd_overlay -cf lxd_overlay.tar .
rm -r /tmp/lxd_overlay

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
