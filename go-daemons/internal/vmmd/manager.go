package vmmd

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

type Disk struct {
	Source   string
	ReadOnly bool
}

type NIC struct {
	Source     string
	MACAddress string
	AllowMMDS  bool
}

type Config struct {
	FirecrackerSocket string

	CPUs           uint
	Hyperthreading bool
	Memory         uint64

	Kernel      string
	CommandLine string

	Disks []Disk
	NICs  []NIC

	Metadata interface{}
}

func NewVM(ctx context.Context, config Config) (*firecracker.Machine, error) {
	drives := make([]models.Drive, len(config.Disks))
	for i, d := range config.Disks {
		drives[i] = models.Drive{
			DriveID:      firecracker.String(strconv.Itoa(i)),
			PathOnHost:   firecracker.String(d.Source),
			IsReadOnly:   firecracker.Bool(d.ReadOnly),
			IsRootDevice: firecracker.Bool(false),
		}
	}

	nics := make(firecracker.NetworkInterfaces, len(config.NICs))
	for i, n := range config.NICs {
		nics[i] = firecracker.NetworkInterface{
			StaticConfiguration: &firecracker.StaticNetworkConfiguration{
				HostDevName: n.Source,
				MacAddress:  n.MACAddress,
			},
			AllowMMDS: n.AllowMMDS,
		}
	}

	cfg := firecracker.Config{
		SocketPath:     config.FirecrackerSocket,
		ForwardSignals: []os.Signal{},

		MachineCfg: models.MachineConfiguration{
			VcpuCount:  firecracker.Int64(int64(config.CPUs)),
			HtEnabled:  firecracker.Bool(config.Hyperthreading),
			MemSizeMib: firecracker.Int64(int64(config.Memory)),
		},

		KernelImagePath: config.Kernel,
		KernelArgs:      config.CommandLine,

		Drives:            drives,
		NetworkInterfaces: nics,
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate machine config: %w", err)
	}
	if err := cfg.ValidateNetwork(); err != nil {
		return nil, fmt.Errorf("failed to validate machine network config: %w", err)
	}

	vm, err := firecracker.NewMachine(ctx, cfg)
	if err != nil {
		return nil, err
	}

	if config.Metadata != nil {
		vm.Handlers.FcInit = vm.Handlers.FcInit.Append(firecracker.Handler{
			Name: "fcinit.SetMetadata",
			Fn: func(c context.Context, m *firecracker.Machine) error {
				return m.SetMetadata(c, config.Metadata)
			},
		})
	}

	return vm, nil
}
