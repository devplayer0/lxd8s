package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	flag "github.com/spf13/pflag"

	"github.com/devplayer0/lxd8s/go-daemons/internal/util"
	"github.com/devplayer0/lxd8s/go-daemons/internal/vmmd"
)

var (
	firecrackerSocket = flag.String("firecracker-socket", "/run/firecracker.sock", "firecracker unix socket path")

	cpus           = flag.String("cpus", "1", "number of CPU's to allocate, or percentage")
	hyperthreading = flag.Bool("hyperthreading", true, "enable hyperthreading")
	memory         = flag.String("mem", "512", "amount of memory (mebibytes), or percentage")

	commandLine = flag.StringP("command-line", "c", "", "kernel command line")

	disks = flag.StringArrayP("disk", "d", []string{}, "disks to attach to the VM")
	nics  = flag.StringArrayP("nic", "n", []string{}, "network interfaces")

	metaFile = flag.String("meta-file", "", "MMDS JSON file path")
)

func init() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %v [flags] <kernel>\n", os.Args[0])
		flag.PrintDefaults()
	}
}

func run(ctx context.Context) error {
	cpuCount, err := util.AbsoluteOrPercentage(*cpus, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("failed to parse CPUs: %w", err)
	}

	totalMem, err := util.MemTotal()
	if err != nil {
		return fmt.Errorf("failed to get total system memory: %w", err)
	}
	memValue, err := util.AbsoluteOrPercentage(*memory, int(totalMem))
	if err != nil {
		return fmt.Errorf("failed to parse memory: %w", err)
	}

	pDisks := make([]vmmd.Disk, len(*disks))
	for i, d := range *disks {
		pDisks[i] = vmmd.ParseDisk(d)
	}
	pNICs := make([]vmmd.NIC, len(*nics))
	for i, n := range *nics {
		pNICs[i] = vmmd.ParseNIC(n)
	}

	config := vmmd.Config{
		FirecrackerSocket: *firecrackerSocket,

		CPUs:           uint(cpuCount),
		Hyperthreading: *hyperthreading,
		Memory:         uint64(memValue),

		Kernel:      flag.Arg(0),
		CommandLine: *commandLine,

		Disks: pDisks,
		NICs:  pNICs,
	}

	if *metaFile != "" {
		f, err := os.Open(*metaFile)
		if err != nil {
			return fmt.Errorf("failed to open metadata file: %w", err)
		}

		if err := json.NewDecoder(f).Decode(&config.Metadata); err != nil {
			f.Close()
			return fmt.Errorf("failed to decode JSON metadata: %w", err)
		}

		f.Close()
	}

	vmCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	vm, err := vmmd.NewVM(vmCtx, config)
	if err != nil {
		return fmt.Errorf("failed to create VM: %w", err)
	}

	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

		for {
			switch s := <-sigs; {
			case s == syscall.SIGTERM || s == os.Interrupt:
				log.Printf("Caught signal: %s, requesting clean shutdown", s.String())
				vm.Shutdown(vmCtx)
			case s == syscall.SIGQUIT:
				log.Printf("Caught signal: %s, forcing shutdown", s.String())
				vm.StopVMM()
			}
		}
	}()

	if err := vm.Start(vmCtx); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}
	defer vm.StopVMM()

	if err := vm.Wait(vmCtx); err != nil {
		return fmt.Errorf("failed wait for firecracker to exit: %w", err)
	}

	return nil
}

func main() {
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
		return
	}

	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
