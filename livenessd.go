package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
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

var (
	replicaRegexp      = regexp.MustCompile(`^.+-([0-9]+)`)
	membersTableRegexp = regexp.MustCompile(`(?m)^\|\s*(.+:\d+)\s*\|$`)
	memAvailableRegexp = regexp.MustCompile(`(?m)^MemAvailable:\s*(\d+)\s*kB$`)
)

var l = log.New(os.Stderr, "", log.LstdFlags)

var (
	addr        = flag.String("listen", ":8080", "listen address")
	logToSyslog = flag.Bool("syslog", false, "write log messages to syslog")
	oomInterval = flag.Duration("oom-interval", 0, "interval for OOM sweep, 0 to disable")
	minFree     = flag.Uint64("oom-min-free", 64*1024*1024, "minimum amount of memory before shutting down instances")
)

func newLXDClient(timeout time.Duration) http.Client {
	return http.Client{
		Timeout: timeout,
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
}

var (
	livenessClient = newLXDClient(3 * time.Second)
	lxdClient      = newLXDClient(60 * time.Second)
)

var replica = getReplica()

// LXDResponse represents a response from LXD
type LXDResponse struct {
	Type       string `json:"type"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`
	Operation  string `json:"operation"`
	ErrorCode  int    `json:"error_code"`
	Error      string `json:"error"`

	Metadata json.RawMessage `json:"metadata"`
}

// LXDInstance represents an LXD instance as returned by `GET /1.0/instances/<instance>` (not all fields)
type LXDInstance struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsed   time.Time `json:"last_used_at"`

	Config map[string]string `json:"config"`
}

// LXDStateRequest represents a request to change an LXD instance's state
type LXDStateRequest struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
	State   bool   `json:"stateful"`
}

// MemFree gets the amount of free memory in mebibytes
func MemFree() (uint64, error) {
	data, err := ioutil.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("failed to read /proc/meminfo: %w", err)
	}

	m := memAvailableRegexp.FindStringSubmatch(string(data))
	if len(m) == 0 {
		return 0, errors.New("failed to find MemAvailable in /proc/meminfo")
	}

	freeKiB, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse MemAvailable value: %w", err)
	}

	return freeKiB / 1024, nil
}

// ParseJSONBody attempts to parse the response body as JSON
func ParseJSONBody(v interface{}, r *http.Response) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}

	return nil
}

func lxdRequest(method, url string, body, meta interface{}, opTimeout int) (LXDResponse, error) {
	var res LXDResponse

	var bodyReader io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return res, fmt.Errorf("failed to encode body: %w", err)
		}

		bodyReader = &buf
	}

	req, err := http.NewRequest(method, "http://lxd"+url, bodyReader)
	if err != nil {
		return res, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	r, err := lxdClient.Do(req)
	if err != nil {
		return res, fmt.Errorf("failed to make HTTP request: %w", err)
	}

	if err := ParseJSONBody(&res, r); err != nil {
		return res, fmt.Errorf("failed to parse response: %w", err)
	}

	if res.StatusCode < 100 || res.StatusCode >= 400 {
		return res, fmt.Errorf("LXD returned non-OK status %v", res.StatusCode)
	}
	if res.StatusCode == 100 {
		// Wait for operation
		if _, err := lxdRequest(http.MethodGet, fmt.Sprintf("%v/wait?timeout=%v", res.Operation, opTimeout), nil, &res, -1); err != nil {
			return res, fmt.Errorf("failed to wait for response: %w", err)
		}
	}

	if meta != nil {
		if err := json.Unmarshal(res.Metadata, meta); err != nil {
			return res, fmt.Errorf("failed to parse response metadata: %w", err)
		}
	}

	return res, nil
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

	res, err := livenessClient.Get("http://lxd/1.0/cluster/members")
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

	var data LXDResponse
	if err := ParseJSONBody(&data, res); err != nil {
		l.Printf("Failed to parse response from LXD API: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var members []string
	if err := json.Unmarshal(data.Metadata, &members); err != nil {
		l.Printf("Failed to parse members from LXD API: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Make sure cluster is initialised
	if data.StatusCode != 200 || (len(members) == 1 && members[0] == "/1.0/cluster/members/none") ||
		len(members) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func oomSweep() error {
	free, err := MemFree()
	if err != nil {
		return fmt.Errorf("unable to determine free memory: %w", err)
	}

	if free >= *minFree {
		return nil
	}

	l.Printf("Only %vMiB RAM free (min %vMiB) - looking for OOM victim", free, *minFree)

	var instances []string
	_, err = lxdRequest(http.MethodGet, "/1.0/instances", nil, &instances, -1)
	if err != nil {
		return fmt.Errorf("failed to list LXD instances: %w", err)
	}

	var longest *LXDInstance
	for _, i := range instances {
		var instance LXDInstance
		_, err := lxdRequest(http.MethodGet, i, nil, &instance, -1)
		if err != nil {
			return fmt.Errorf("failed to retrieve LXD instance: %w", err)
		}

		shouldSkip := false
		skipStr, ok := instance.Config["user.oomSkip"]
		if ok {
			shouldSkip, _ = strconv.ParseBool(skipStr)
		}

		if instance.StatusCode == 103 && !shouldSkip && (longest == nil || instance.LastUsed.Before(longest.LastUsed)) {
			longest = &instance
		}
	}

	if longest == nil {
		return errors.New("failed to find OOM candidate")
	}

	l.Printf("Stopping %v...", longest.Name)
	url := fmt.Sprintf("/1.0/instances/%v/state", longest.Name)
	op, err := lxdRequest(http.MethodPut, url, LXDStateRequest{
		Action:  "stop",
		Timeout: 5,
	}, nil, 10)
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	if op.StatusCode != 200 {
		l.Printf("Failed to stop instance %v, killing...", longest.Name)
		res, err := lxdRequest(http.MethodPut, url, LXDStateRequest{
			Action:  "stop",
			Timeout: 5,
			Force:   true,
		}, nil, 10)
		if err != nil {
			return fmt.Errorf("failed to kill instance: %w", err)
		}
		if res.StatusCode != 200 {
			return fmt.Errorf("instance kill returned non-OK status: %v", res.StatusCode)
		}
	}

	return nil
}
func oomKiller(interval time.Duration, done chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			if err := oomSweep(); err != nil {
				l.Printf("OOM sweep failed: %v", err)
			}
		case <-done:
			return
		}
	}
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

	done := make(chan struct{})
	if *oomInterval != 0 {
		go func() {
			oomKiller(*oomInterval, done)
		}()
	}

	<-sigs
	close(done)
	server.Close()
}
