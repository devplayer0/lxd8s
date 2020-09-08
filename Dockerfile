FROM alpine:3.12 AS kernel_builder
ARG KERNEL_VERSION

RUN apk --no-cache add gcc make musl-dev bison flex linux-headers elfutils-dev \
    diffutils openssl openssl-dev perl patch

WORKDIR /build
RUN wget "https://cdn.kernel.org/pub/linux/kernel/$(echo $KERNEL_VERSION | \
        sed -r 's|^([0-9])\.[0-9]+(\.[0-9]+)?|v\1.x|')/linux-$KERNEL_VERSION.tar.xz"

COPY patches/kernel-virtnet-no-lro-config.patch /build/
COPY kernel_config /build/.config
RUN tar Jxf "linux-$KERNEL_VERSION.tar.xz" && \
    for p in *.patch; do patch -d "linux-$KERNEL_VERSION/" -p1 < "$p"; done && \
    mv .config "linux-$KERNEL_VERSION/" && \
    make -C "linux-$KERNEL_VERSION/" -j$(nproc) vmlinux && \
    mv "linux-$KERNEL_VERSION/vmlinux" ./ && \
    rm -r "linux-$KERNEL_VERSION/"

### UNTIL LXCFS IS RELEASED UPSTREAM AND UPDATED IN ALPINE

FROM alpinelinux/docker-abuild AS lxcfs_builder

RUN git clone https://gitlab.alpinelinux.org/alpine/aports

COPY patches/alpine-lxcfs-dirname.patch lxcfs.patch
RUN cd aports/community/lxcfs && \
    git checkout 90fdadb3c5e71d749ff24454a6bebb322c851968 && \
    git apply ~/lxcfs.patch && \
    /home/builder/entrypoint.sh -r

###

FROM golang:1.15-alpine AS liveness_builder

WORKDIR /go/src/liveness
COPY livenessd.go ./

RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /go/bin/livenessd livenessd.go


FROM alpine:3.12 AS rootfs

RUN apk --no-cache add alpine-base iproute2 e2fsprogs curl jq

COPY --from=lxcfs_builder /home/builder/.abuild/*.rsa.pub /etc/apk/keys/
COPY --from=lxcfs_builder /home/builder/packages /packages
RUN echo "https://dl-cdn.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories && \
    sed -i '1s|^|/packages/community\n|' /etc/apk/repositories && \
    apk --no-cache add lxcfs lxd nftables btrfs-progs && \
    rm /packages/*/x86_64/*

RUN rm /sbin/modprobe && \
    > /etc/fstab && \
    sed -i 's|#unicode=".*"|unicode="YES"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_mode=".*"|rc_cgroup_mode="hybrid"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_memory_use_hierarchy=".*"|rc_cgroup_memory_use_hierarchy="YES"|' /etc/rc.conf && \
    echo 'cgroup_hierarchy_name="systemd"' > /etc/conf.d/cgroups && \
    echo 'opts="hostname inet_mtu inet_addr inet_gw lxd_addr lxd_mtu k8s_replica"' > /etc/conf.d/cmdline && \
    echo 'LIVENESSD_OPTIONS="-listen :8080"' > /etc/conf.d/livenessd && \
    #
    echo ttyS0 >> /etc/securetty && \
    sed -ri 's|^#ttyS0(.+)ttyS0|ttyS0\1-l /bin/autologin ttyS0|' /etc/inittab

COPY scripts/modprobe /sbin/modprobe
COPY scripts/autologin /bin/autologin
COPY --from=liveness_builder /go/bin/livenessd /usr/local/bin/livenessd

COPY openrc/* /etc/init.d/

RUN rc-update add devfs sysinit && \
    rc-update add sysfs sysinit && \
    rc-update add procfs sysinit && \
    rc-update add cgroups sysinit && \
    rc-update add cmdline sysinit && \
    rc-update add lxd-data sysinit && \
    rc-update add overlay sysinit && \
    #
    rc-update add sysctl boot && \
    rc-update add hostname boot && \
    rc-update add syslog boot && \
    rc-update add lxd8snet boot && \
    #
    rc-update add killprocs shutdown && \
    rc-update add mount-ro shutdown && \
    #
    rc-update add lxcfs default && \
    rc-update add lxd default && \
    rc-update add lxd-init default && \
    rc-update add livenessd default

RUN ln -sf /etc/init.d/noop /etc/init.d/modules && \
    ln -sf /etc/init.d/noop /etc/init.d/clock && \
    rm /etc/init.d/osclock /etc/init.d/hwclock /etc/init.d/swclock

COPY conf/sysctl.conf /etc/sysctl.d/lxd.conf
COPY conf/limits.conf /etc/security/limits.d/lxd.conf

RUN echo 'LXD_OPTIONS="--logfile /var/lib/lxd/lxd.log"' >> /etc/conf.d/lxd

FROM alpine:edge AS rootfs_img_builder

RUN echo "https://dl-cdn.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories && \
    apk --no-cache add tar libguestfs

WORKDIR /build

RUN wget https://download.libguestfs.org/binaries/appliance/appliance-1.40.1.tar.xz && \
    tar -Jxf appliance-1.40.1.tar.xz
COPY --from=rootfs / root/
RUN LIBGUESTFS_PATH=appliance/ guestfish \
        sparse rootfs.img 256M : \
        launch : \
        mkfs ext4 /dev/sda label:root : \
        mount /dev/sda / : \
        lcd root/ : \
        copy-in . / && \
    rm -rf root/


FROM alpine:3.12
ARG FIRECRACKER_VERSION

RUN echo "@edge https://dl-cdn.alpinelinux.org/alpine/edge/main" >> /etc/apk/repositories && \
    echo "@edge https://dl-cdn.alpinelinux.org/alpine/edge/community" >> /etc/apk/repositories
RUN apk --no-cache add iproute2 curl sed jq
RUN apk --no-cache add yq@edge

RUN apk --no-cache add libc6-compat
RUN wget -O /usr/local/bin/firecracker \
        "https://github.com/firecracker-microvm/firecracker/releases/download/v${FIRECRACKER_VERSION}/firecracker-v${FIRECRACKER_VERSION}-x86_64" && \
    chmod +x /usr/local/bin/firecracker
RUN wget -O /usr/local/bin/firectl "https://github.com/netsoc/firectl/releases/latest/download/firectl" && \
    chmod +x /usr/local/bin/firectl

WORKDIR /opt/lxd8s
COPY --from=kernel_builder /build/vmlinux ./vmlinux
COPY --from=rootfs_img_builder /build/rootfs.img ./rootfs.img

COPY scripts/k8s.sh /usr/local/bin/k8s.sh
RUN mkdir -p /run/config && echo '{}' > /run/config/preseed.yaml

ENV FIRECRACKER_GO_SDK_REQUEST_TIMEOUT_MILLISECONDS=10000

ENV CPUS=1 \
    MEM=512 \
    LXD_DATA=./lxd.img \
    LXD_STORAGE=./storage.img

ENV INET_HOST=192.168.69.1/30 \
    INET_VM=192.168.69.2/30 \
    LXD_NET=172.20.0.0/16

ENV KUBELAN=no \
    CERT_SECRET_BASE=

COPY entrypoint.sh /
ENTRYPOINT ["/entrypoint.sh"]
