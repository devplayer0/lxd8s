package lxd

import (
	"fmt"
	"os/exec"
	"regexp"
)

const (
	StatusCreated = 100
	StatusRunning = 103
	StatusSuccess = 200
	StatusFailure = 400
)

var membersTableRegexp = regexp.MustCompile(`(?m)^\|\s*(.+:\d+)\s*\|$`)

// GetClusterMembers gets members of the local cluster (importantly this reads
// the database directly and doesn't require the daemon to be running)
func GetClusterMembers() ([]string, error) {
	output, err := exec.Command("lxd", "cluster", "list-database").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run lxd command: %w", err)
	}

	matches := membersTableRegexp.FindAllStringSubmatch(string(output), -1)
	members := make([]string, len(matches))
	for i, m := range matches {
		members[i] = m[1]
	}

	return members, nil
}
