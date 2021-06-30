# Editing config

Below assumes a checkout of a kernel tree of the desired version.

1. Copy `lxd8s.config` to `arch/x86/configs`
2. Run `make defconfig lxd8s.config`
3. Make changes (e.g. with `make xconfig`)
4. Copy `.config` to `.config.lxd8s`
5. Run `make defconfig` to overwrite `.config` with the default config
5. Run `scripts/diffconfig -m .config .config.lxd8s > lxd8s.config` to generate the updated config fragment
