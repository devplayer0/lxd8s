# lxd8s

lxd8s allows LXD to be run inside Kubernetes. This is achieved by using
[Firecracker](https://github.com/firecracker-microvm/firecracker) to create a minimal, Alpine-based OS to host LXD.
LXD clustering is supported (compatible with a Kubernetes `StatefulSet`).

## Deployment

A Helm chart is provided. See [my charts repo](https://github.com/devplayer0/charts). Check the default `values.yaml`,
there are quite a number of adjustable options. It's recommended that you install
[smarter-device-manager](https://gitlab.com/arm-research/smarter/smarter-device-manager) in your cluster so that the
container has access to `/dev/kvm`.

## Development

As well as deploying in Kubernetes, running in plain Docker is supported for testing. The Docker Compose file provided
supports this.

# Firecracker

Since LXD needs fairly extensive control over cgroups and other kernel-provided isolation tools, it's impractical to run
host LXD directly inside a container, without a significant number of hacks and compromises. Since Firecracker provides
a lightweight KVM-based VM option, LXD can have complete access to the required features. The Kubernetes container also
only needs access to `/dev/kvm` (along with `CAP_NET_ADMIN`), no `privileged: true` required!

## Sticking with v0.24.x

In Firecracker 0.25.0 (specifically
[this commit](https://github.com/firecracker-microvm/firecracker/commit/96b7fff9e9d46c9170a858443edece38713c5f4b)) a
check for `KVM_CAP_XCRS` was added, even though this call doesn't seem to be used. Since some older hardware doesn't
seem to support this (Intel Nehalem?), lxd8s will stay on v0.24.x.

# Kernel

A custom kernel is built to optimise for Firecracker and LXD. Currently the 5.10.x LTS branch is used.

## LRO patch

The kernel will sometimes disable LRO
([Large Receive Offload](https://en.wikipedia.org/wiki/Large_receive_offload)) on network interfaces. Due to what seems
to be an ambiguous spec, this causes a panic in `virtio_net` (used for networking between host and guest). A simple
patch in `patches/kernel-virtnet-no-lro-config.patch` makes this operation a no-op to prevent the panic.

## Editing config

Below assumes a checkout of a kernel tree for the desired version.

1. Copy `lxd8s.config` to `arch/x86/configs`
2. Run `make defconfig lxd8s.config`
3. Make changes (e.g. with `make xconfig`)
4. Copy `.config` to `.config.lxd8s`
5. Run `make defconfig` to overwrite `.config` with the default config
5. Run `scripts/diffconfig -m .config .config.lxd8s > lxd8s.config` to generate the updated config fragment

# Environment

## Kubernetes container

The main Kubernetes container's purpose is simply to configure some networking, pass configuration to the VM and start
Firecracker. See `entrypoint.sh` for all of these details. In summary:

1. The TUN device is created (`/dev/net/tun`) - this is required for the host side of networking with the VM
2. Networking for the container is configured, including forwarding traffic destined for the pod to the VM's internet
   interface and making use of a separate LXD-private interface utilising
   [kubelan](https://github.com/devplayer0/kubelan)
3. LXD configuration is prepared - this includes the cluster cert and preseed files for cluster initialisation
4. Config for the VM is added to a tar archive which will overlayed atop the root filesystem on init
5. If LXD data and storage volumes have not been provided, temporary 4GiB and 16GiB sparse images will be created
6. The VM is started with vmmd, attaching:
    - Drives for the read-only rootfs, LXD data, overlay tar and LXD storage
    - Internet and LXD private network interfaces

## VM userspace

The userspace for the LXD VM is Alpine-based (currently 3.14), although the boot process is custom. In the
`Dockerfile`, one of the
builder's is named `rootfs`, and the contents of this image is later compressed into a SquashFS image. The Docker
Alpine images are missing many packages needed for a booting Linux system (e.g. an init), so these are all installed.
A number of custom OpenRC scripts are included to perform tasks such as applying the overlay tar, initialising LXD with
a preseed, formatting the LXD data volume etc.

Since the VM is ephemeral (its lifecycle being tied to the host Kubernetes pod), there's no need to have a writable
rootfs. A custom initramfs (embedded into the kernel) uses OverlayFS to add a `tmpfs` over `/` for scratch files.

### LXD

Currently, LXD is in Alpine's `testing` repo, which is the most bleeding edge. In order to keep the system stable, LXD
is built from source in a separate Alpine 3.14 builder.

## Daemons

A number of custom daemons were created in the `go-daemons/` directory.

### vmmd

vmmd is a small program to manage Firecracker. On its own, Firecracker has no real CLI, only a REST API. Using
[firecracker-go-sdk](https://github.com/firecracker-microvm/firecracker-go-sdk), a simple daemon configures and
starts Firecracker, attaching the serial port to the console. It also supports specifying VM vCPU and memory allocation
as a percentage of the host's, which is handy when Kubernetes nodes don't have the same hardware configuration.

### livenessd

livenessd is responsible for reporting health status of each LXD node. It exposes a health check endpoint that
Kubernetes can use to check if a node is online. This endpoint attempts to list members of the LXD cluster, which will
only succeed if LXD is running and cluster consensus has been reached. It has some special cases, such as allowing
health checks to pass if the current node is in the lower half of replicas to be started, since otherwise startup will
deadlock (Kubernetes waits for health check to pass but it will not until a majority of cluster nodes are online).

Additionally, livenessd accounts for overprovisioning of memory by shutting down the longest running containers when
free memory drops below a certain threshold.
