package main

import (
	"errors"
	"flag"
	"log"
	"log/syslog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devplayer0/lxd8s/go-daemons/internal/livenessd"
)

var (
	addr                    = flag.String("listen", ":8080", "listen address")
	logToSyslog             = flag.Bool("syslog", false, "write log messages to syslog")
	livenessClusterLenience = flag.Duration("liveness-cluster-lenience", 5*time.Minute, "lenience perioid for initial cluster member readiness")
	oomInterval             = flag.Duration("oom-interval", 0, "interval for OOM sweep, 0 to disable")
	minFree                 = flag.Uint64("oom-min-free", 64*1024*1024, "minimum amount of memory before shutting down instances")
)

func main() {
	flag.Parse()

	logger := log.New(os.Stderr, "", log.LstdFlags)
	if *logToSyslog {
		var err error
		logger, err = syslog.NewLogger(syslog.LOG_ERR|syslog.LOG_LOCAL7, log.Lshortfile)
		if err != nil {
			log.Fatalf("Failed to create syslog logger: %v", err)
		}
	}

	s := livenessd.NewServer(livenessd.Config{
		HTTPAddress: *addr,

		LivenessClusterLenience: *livenessClusterLenience,

		OOMInterval: *oomInterval,
		OOMMinFree:  *minFree,
	}, logger)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Printf("Starting server")

		if err := s.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("Failed to start server: %v", err)
		}
	}()

	<-sigs
	if err := s.Stop(); err != nil {
		logger.Fatalf("Failed to stop server: %v", err)
	}
}
