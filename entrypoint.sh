#!/bin/sh

random_mac() {
    printf '%02x' $((0x$(od /dev/urandom -N1 -t x1 -An | cut -c 2-) & 0xFE | 0x02)); od /dev/urandom -N5 -t x1 -An | sed 's/ /:/g'
}

mkdir -p /dev/net
[ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200

eth0="$(ip route list default | awk '{ print $5 }')"
gw="$(ip route list default | awk '{ print $3 }')"
ip="$(ip addr show $eth0 | grep 'scope global' | awk '{ print $2 }')"

ip link add vm-bridge type bridge
ip link set dev vm-bridge up

ip tuntap add vm mode tap
ip link set dev vm up
ip link set dev vm master vm-bridge

ip addr del "$ip" dev "$eth0"
ip link set dev "$eth0" master vm-bridge

[ -e "$LXD_DATA" ] || truncate -s 4G "$LXD_DATA"


rm -f /run/firecracker.sock
exec firectl \
    --socket-path /run/firecracker.sock \
    --ncpus $CPUS \
    --memory $MEM \
    --tap-device "vm/$(random_mac)" \
    --kernel ./vmlinux \
    --kernel-opts "console=ttyS0 noapic reboot=k panic=1 pci=off \
        hostname=$(hostname) k8s_ip=$ip k8s_gw=$gw \
        resolvconf=$(sed 's|^nameserver 127..*|nameserver 1.1.1.1|' < /etc/resolv.conf | base64 | tr -d '\n')" \
    --root-drive ./rootfs.img \
    --add-drive "$LXD_DATA:rw"
