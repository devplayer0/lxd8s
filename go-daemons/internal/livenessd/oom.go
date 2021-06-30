package livenessd

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/devplayer0/lxd8s/go-daemons/internal/lxd"
	"github.com/devplayer0/lxd8s/go-daemons/internal/util"
)

func (s *Server) oomSweep() error {
	free, err := util.MemFree()
	if err != nil {
		return fmt.Errorf("unable to determine free memory: %w", err)
	}

	if free >= s.config.OOMMinFree {
		return nil
	}

	s.logger.Printf("Only %vMiB RAM free (min %vMiB) - looking for OOM victim", free, s.config.OOMMinFree)

	var instances []string
	_, err = s.lxd.Request(http.MethodGet, "/1.0/instances", nil, &instances, -1)
	if err != nil {
		return fmt.Errorf("failed to list LXD instances: %w", err)
	}

	var longest *lxd.Instance
	for _, i := range instances {
		var instance lxd.Instance
		_, err := s.lxd.Request(http.MethodGet, i, nil, &instance, -1)
		if err != nil {
			return fmt.Errorf("failed to retrieve LXD instance: %w", err)
		}

		shouldSkip := false
		skipStr, ok := instance.Config["user.oomSkip"]
		if ok {
			shouldSkip, _ = strconv.ParseBool(skipStr)
		}

		if instance.StatusCode == lxd.StatusRunning && !shouldSkip && (longest == nil || instance.LastUsed.Before(longest.LastUsed)) {
			longest = &instance
		}
	}

	if longest == nil {
		return errors.New("failed to find OOM candidate")
	}

	s.logger.Printf("Stopping %v...", longest.Name)
	url := fmt.Sprintf("/1.0/instances/%v/state", longest.Name)
	op, err := s.lxd.Request(http.MethodPut, url, lxd.StateRequest{
		Action:  "stop",
		Timeout: 5,
	}, nil, 10)
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	if op.StatusCode != lxd.StatusSuccess {
		s.logger.Printf("Failed to stop instance %v, killing...", longest.Name)
		res, err := s.lxd.Request(http.MethodPut, url, lxd.StateRequest{
			Action:  "stop",
			Timeout: 5,
			Force:   true,
		}, nil, 10)
		if err != nil {
			return fmt.Errorf("failed to kill instance: %w", err)
		}
		if res.StatusCode != lxd.StatusSuccess {
			return fmt.Errorf("instance kill returned non-OK status: %v", res.StatusCode)
		}
	}

	return nil
}
