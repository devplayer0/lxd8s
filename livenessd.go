package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
)

var replicaRegexp = regexp.MustCompile(`^.+-([0-9]+)`)
var membersTableRegexp = regexp.MustCompile(`(?m)^\|\s*(.+:\d+)\s*\|$`)

var addr = flag.String("listen", ":8080", "listen address")

var replica = getReplica()

func getReplica() int {
	n, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return 0
	}

	m := replicaRegexp.FindStringSubmatch(n)
	if len(m) == 0 {
		return 0
	}

	r, err := strconv.ParseUint(m[1], 10, 8)
	if err != nil {
		log.Printf("Failed to parse replica index: %v", err)
		return 0
	}

	return int(r)
}

func getLXDMembers() ([]string, error) {
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

func httpLiveness(w http.ResponseWriter, r *http.Request) {
	if err := exec.Command("pgrep", "lxd").Run(); err != nil {
		var perr *exec.ExitError
		if !errors.As(err, &perr) {
			log.Printf("Failed to execute pgrep command: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := exec.Command("lxd", "waitready", "--timeout", "1").Run(); err != nil {
		var perr *exec.ExitError
		if !errors.As(err, &perr) {
			log.Printf("Failed to execute waitready command: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		members, err := getLXDMembers()
		if err != nil {
			log.Printf("Failed to get LXD members: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if replica < len(members)/2 {
			// Special case: the cluster is initialised already and we're within the minority of members.
			// LXD will hang waiting for quorum until there's a a majority node, so let this one slip :)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func main() {
	flag.Parse()

	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	http.HandleFunc("/liveness", httpLiveness)
	server := http.Server{Addr: *addr}
	go func() {
		log.Fatal(server.ListenAndServe())
	}()

	<-sigs
	server.Close()
}
