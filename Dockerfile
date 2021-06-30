FROM alpine:3.14 AS kernel_builder
ARG KERNEL_VERSION

RUN apk --no-cache add gcc make musl-dev bison flex linux-headers elfutils-dev \
    diffutils openssl openssl-dev perl patch findutils

WORKDIR /build
RUN wget "https://cdn.kernel.org/pub/linux/kernel/$(echo $KERNEL_VERSION | \
        sed -r 's|^([0-9])\.[0-9]+(\.[0-9]+)?|v\1.x|')/linux-$KERNEL_VERSION.tar.xz"

COPY patches/kernel-virtnet-no-lro-config.patch /build/
COPY lxd8s.config /build/
RUN tar Jxf "linux-$KERNEL_VERSION.tar.xz" && \
    for p in *.patch; do patch -d "linux-$KERNEL_VERSION/" -p1 < "$p"; done && \
    mv lxd8s.config "linux-$KERNEL_VERSION/arch/x86/configs/" && \
    make -C "linux-$KERNEL_VERSION/" defconfig lxd8s.config && \
    make -C "linux-$KERNEL_VERSION/" -j$(nproc) vmlinux && \
    mv "linux-$KERNEL_VERSION/vmlinux" ./ && \
    rm -r "linux-$KERNEL_VERSION/"


FROM alpine:3.14 AS lxd_builder

RUN apk add alpine-conf alpine-sdk sudo && \
    setup-apkcache /var/cache/apk
RUN adduser -D builder && \
    addgroup builder abuild && \
    echo 'builder ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers && \
    sed -i '1s|^|/home/builder/packages/testing\n|' /etc/apk/repositories

USER builder
WORKDIR /home/builder

# This commit is lxd 4.15
RUN git clone https://gitlab.alpinelinux.org/alpine/aports && \
    git -C aports checkout 402a656119b5b86edd9f01f3cb2b5b68d12d6396

RUN USER=builder abuild-keygen -na && \
    sudo cp -v "$HOME"/.abuild/builder-*.rsa.pub /etc/apk/keys/ && \
    printf "JOBS=\$(nproc)\nMAKEFLAGS=-j\$JOBS\n" >> "$HOME/.abuild/abuild.conf"

COPY patches/alpine-lxd-no-sql-replication.patch .
RUN git -C aports apply ../alpine-lxd-no-sql-replication.patch

RUN cd aports/testing/raft && \
    abuild -r
RUN cd aports/testing/dqlite && \
    abuild -r
RUN cd aports/testing/lxd && \
    abuild -r


FROM golang:1.16-alpine3.14 AS liveness_builder

WORKDIR /go/src/liveness
COPY livenessd.go ./

RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /go/bin/livenessd livenessd.go


FROM alpine:3.14 AS rootfs

RUN apk --no-cache add alpine-base udev iproute2 e2fsprogs curl jq

COPY --chown=root:root --from=lxd_builder /home/builder/.abuild/builder-*.rsa.pub /etc/apk/keys/
COPY --chown=root:root --from=lxd_builder /home/builder/packages /var/lib/apk
RUN sed -i '1s|^|/var/lib/apk/testing\n|' /etc/apk/repositories && \
    apk --no-cache add lxcfs lxd nftables btrfs-progs

RUN rm /sbin/modprobe && \
    > /etc/fstab && \
    sed -i 's|#unicode=".*"|unicode="YES"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_mode=".*"|rc_cgroup_mode="hybrid"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_memory_use_hierarchy=".*"|rc_cgroup_memory_use_hierarchy="YES"|' /etc/rc.conf && \
    echo 'cgroup_hierarchy_name="systemd"' > /etc/conf.d/cgroups && \
    echo 'opts="hostname inet_mtu inet_addr inet_gw lxd_addr lxd_mtu k8s_replica oom_interval oom_min_free"' > /etc/conf.d/cmdline && \
    echo 'LIVENESSD_OPTIONS="-syslog -listen :8080"' > /etc/conf.d/livenessd && \
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
    rc-update add udev-trigger sysinit && \
    rc-update add udev sysinit && \
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
    rc-update add udev-postmount default && \
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


FROM alpine:3.14 AS rootfs_img_builder

RUN echo "https://dl-cdn.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories && \
    apk --no-cache add e2fsprogs

WORKDIR /build

COPY --from=rootfs / root/
RUN mkfs.ext4 -L root -d root/ rootfs.img 512M && \
    rm -rf root/


FROM alpine:3.14
ARG FIRECRACKER_VERSION
ARG FIRECTL_VERSION

RUN apk --no-cache add iproute2 curl sed jq yq

RUN apk --no-cache add libc6-compat
RUN mkdir /tmp/firecracker && \
    wget -O /tmp/firecracker/release.tar.gz \
        "https://github.com/firecracker-microvm/firecracker/releases/download/v${FIRECRACKER_VERSION}/firecracker-v${FIRECRACKER_VERSION}-x86_64.tgz" && \
    tar -C /tmp/firecracker -zxf /tmp/firecracker/release.tar.gz && \
    mv "/tmp/firecracker/release-v${FIRECRACKER_VERSION}/firecracker-v${FIRECRACKER_VERSION}-x86_64" /usr/local/bin/firecracker && \
    chmod +x /usr/local/bin/firecracker && \
    rm -r /tmp/firecracker
RUN wget -O /usr/local/bin/firectl "https://github.com/devplayer0/firectl/releases/download/v${FIRECTL_VERSION}/firectl" && \
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

ENV OOM_INTERVAL= \
    OOM_MIN_FREE=64

COPY entrypoint.sh /
ENTRYPOINT ["/entrypoint.sh"]

LABEL org.opencontainers.image.source https://github.com/devplayer0/lxd8s
