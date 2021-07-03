package livenessd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"

	"github.com/devplayer0/lxd8s/go-daemons/internal/lxd"
)

var replicaRegexp = regexp.MustCompile(`^.+-([0-9]+)`)

type Config struct {
	Replica uint

	LXDSocket   string
	HTTPAddress string

	LivenessClusterLenience time.Duration

	OOMInterval time.Duration
	OOMMinFree  uint64
}

type Server struct {
	config Config
	logger *log.Logger

	// This represents the time at which an exception for cluster readiness was made
	livenessClusterLenienceStart time.Time

	lxd            *lxd.Client
	livenessClient http.Client

	http    http.Server
	oomDone chan struct{}
}

func NewServer(config Config, logger *log.Logger) *Server {
	mux := http.NewServeMux()
	s := &Server{
		config: config,
		logger: logger,

		lxd:            lxd.NewClient(60*time.Second, config.LXDSocket),
		livenessClient: lxd.NewHTTPClient(3*time.Second, config.LXDSocket),

		http: http.Server{
			Addr:    config.HTTPAddress,
			Handler: mux,
		},
		oomDone: make(chan struct{}),
	}

	mux.HandleFunc("/liveness", s.httpLiveness)

	return s
}

func (s *Server) Start() error {
	if s.config.OOMInterval != 0 {
		go func() {
			t := time.NewTicker(s.config.OOMInterval)
			defer t.Stop()

			for {
				select {
				case <-t.C:
					if err := s.oomSweep(); err != nil {
						s.logger.Printf("OOM sweep failed: %v", err)
					}
				case <-s.oomDone:
					return
				}
			}
		}()
	}

	return s.http.ListenAndServe()
}

func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := s.http.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shut down HTTP server: %w", err)
	}

	close(s.oomDone)

	return nil
}
