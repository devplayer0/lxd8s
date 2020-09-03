FROM alpine:3.12 AS kernel_builder
ARG KERNEL_VERSION

RUN apk --no-cache add gcc make musl-dev bison flex linux-headers elfutils-dev \
    diffutils openssl openssl-dev perl

WORKDIR /build
RUN wget "https://cdn.kernel.org/pub/linux/kernel/$(echo $KERNEL_VERSION | \
        sed -r 's|^([0-9])\.[0-9]+(\.[0-9]+)?|v\1.x|')/linux-$KERNEL_VERSION.tar.xz"

COPY kernel_config /build/.config
RUN tar Jxf "linux-$KERNEL_VERSION.tar.xz" && \
    mv .config "linux-$KERNEL_VERSION/" && \
    make -C "linux-$KERNEL_VERSION/" -j$(nproc) vmlinux && \
    mv "linux-$KERNEL_VERSION/vmlinux" ./ && \
    rm -r "linux-$KERNEL_VERSION/"

### UNTIL LXCFS IS RELEASED UPSTREAM AND UPDATED IN ALPINE

FROM alpinelinux/docker-abuild AS lxcfs_builder

RUN git clone https://gitlab.alpinelinux.org/alpine/aports

COPY alpine-lxcfs-dirname.patch lxcfs.patch
RUN cd aports/community/lxcfs && \
    git checkout 90fdadb3c5e71d749ff24454a6bebb322c851968 && \
    git apply ~/lxcfs.patch && \
    /home/builder/entrypoint.sh -r

###

FROM alpine:3.12 AS rootfs

RUN apk --no-cache add alpine-base e2fsprogs

COPY --from=lxcfs_builder /home/builder/.abuild/*.rsa.pub /etc/apk/keys/
COPY --from=lxcfs_builder /home/builder/packages /packages
RUN echo "https://dl-cdn.alpinelinux.org/alpine/edge/testing" >> /etc/apk/repositories && \
    sed -i '1s|^|/packages/community\n|' /etc/apk/repositories && \
    apk --no-cache add lxcfs lxd nftables && \
    rm /packages/*/x86_64/*

RUN rm /sbin/modprobe && \
    > /etc/fstab && \
    sed -i 's|#unicode=".*"|unicode="YES"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_mode=".*"|rc_cgroup_mode="hybrid"|' /etc/rc.conf && \
    sed -i 's|#rc_cgroup_memory_use_hierarchy=".*"|rc_cgroup_memory_use_hierarchy="YES"|' /etc/rc.conf && \
    echo 'cgroup_hierarchy_name="systemd"' > /etc/conf.d/cgroups && \
    #
    echo ttyS0 >> /etc/securetty && \
    sed -ri 's|^#ttyS0(.+)ttyS0|ttyS0\1-l /bin/autologin ttyS0|' /etc/inittab
COPY scripts/modprobe /sbin/modprobe
COPY scripts/autologin /bin/autologin
COPY openrc/cgroups /etc/init.d/cgroups
COPY openrc/noop /etc/init.d/noop
COPY openrc/k8snet /etc/init.d/k8snet
COPY openrc/lxd-data /etc/init.d/lxd-data

RUN rc-update add devfs sysinit && \
    rc-update add sysfs sysinit && \
    rc-update add procfs sysinit && \
    rc-update add cgroups sysinit && \
    #
    rc-update add k8snet boot && \
    rc-update add sysctl boot && \
    rc-update add hostname boot && \
    rc-update add syslog boot && \
    #
    rc-update add killprocs shutdown && \
    rc-update add mount-ro shutdown && \
    #
    rc-update add lxd-data default && \
    rc-update add lxcfs default && \
    rc-update add lxd default

RUN ln -sf /etc/init.d/noop /etc/init.d/modules && \
    ln -sf /etc/init.d/noop /etc/init.d/clock && \
    rm /etc/init.d/osclock /etc/init.d/hwclock /etc/init.d/swclock

RUN echo 'LXD_OPTIONS="--debug --logfile /var/log/lxd.log"' >> /etc/conf.d/lxd

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
ARG FIRECTL_VERSION

RUN apk --no-cache add tini iproute2

RUN apk --no-cache add libc6-compat
RUN wget -O /usr/local/bin/firecracker \
        "https://github.com/firecracker-microvm/firecracker/releases/download/v${FIRECRACKER_VERSION}/firecracker-v${FIRECRACKER_VERSION}-x86_64" && \
    chmod +x /usr/local/bin/firecracker
RUN wget -O /usr/local/bin/firectl "https://firectl-release.s3.amazonaws.com/firectl-v${FIRECTL_VERSION}" && \
    chmod +x /usr/local/bin/firectl

WORKDIR /opt/lxd8s
COPY --from=kernel_builder /build/vmlinux ./vmlinux
COPY --from=rootfs_img_builder /build/rootfs.img ./rootfs.img

ENV CPUS=1
ENV MEM=512
ENV LXD_DATA=./lxd.img
COPY entrypoint.sh /
ENTRYPOINT ["/entrypoint.sh"]
