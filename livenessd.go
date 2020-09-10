package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/syslog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

var replicaRegexp = regexp.MustCompile(`^.+-([0-9]+)`)
var membersTableRegexp = regexp.MustCompile(`(?m)^\|\s*(.+:\d+)\s*\|$`)
var l = log.New(os.Stderr, "", log.LstdFlags)

var (
	addr        = flag.String("listen", ":8080", "listen address")
	logToSyslog = flag.Bool("syslog", false, "write log messages to syslog")
)

var lxdClient = http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			t, ok := ctx.Deadline()
			if !ok {
				t = time.Now().Add(3 * time.Second)
			}

			return net.DialTimeout("unix", "/var/lib/lxd/unix.socket", t.Sub(time.Now()))
		},
	},
}
var replica = getReplica()

// ClusterMembers represents the response from LXD `GET /1.0/cluster/members`
type ClusterMembers struct {
	Type       string `json:"type"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`
	Operation  string `json:"operation"`
	ErrorCode  int    `json:"error_code"`
	Error      string `json:"error"`

	Members []string `json:"metadata"`
}

// ParseJSONBody attempts to parse the response body as JSON
func ParseJSONBody(v interface{}, r *http.Response) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}

	return nil
}

func getReplica() int {
	n, err := os.Hostname()
	if err != nil {
		l.Printf("Failed to get hostname: %v", err)
		return 0
	}

	m := replicaRegexp.FindStringSubmatch(n)
	if len(m) == 0 {
		return 0
	}

	r, err := strconv.ParseUint(m[1], 10, 8)
	if err != nil {
		l.Printf("Failed to parse replica index: %v", err)
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
			l.Printf("Failed to execute pgrep command: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	res, err := lxdClient.Get("http://lxd/1.0/cluster/members")
	if err != nil {
		l.Printf("Failed to query LXD API: %v", err)

		members, err := getLXDMembers()
		if err != nil {
			l.Printf("Failed to get LXD members: %v", err)
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

	var data ClusterMembers
	if err := ParseJSONBody(&data, res); err != nil {
		l.Printf("Failed to parse members from LXD API: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Make sure cluster is initialised
	if data.StatusCode != 200 || (len(data.Members) == 1 && data.Members[0] == "/1.0/cluster/members/none") ||
		len(data.Members) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func main() {
	flag.Parse()

	var err error
	if *logToSyslog {
		l, err = syslog.NewLogger(syslog.LOG_ERR|syslog.LOG_LOCAL7, log.Lshortfile)
		if err != nil {
			log.Fatalf("Failed to create syslog logger: %v", err)
		}
	}

	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	http.HandleFunc("/liveness", httpLiveness)
	server := http.Server{Addr: *addr}
	go func() {
		l.Fatal(server.ListenAndServe())
	}()

	<-sigs
	server.Close()
}
