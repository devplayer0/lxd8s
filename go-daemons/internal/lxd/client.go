package lxd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/devplayer0/lxd8s/go-daemons/internal/util"
)

func NewHTTPClient(timeout time.Duration, socket string) http.Client {
	if socket == "" {
		socket = "/var/lib/lxd/unix.socket"
	}

	return http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				t, ok := ctx.Deadline()
				if !ok {
					t = time.Now().Add(3 * time.Second)
				}

				return net.DialTimeout("unix", socket, t.Sub(time.Now()))
			},
		},
	}
}

// Response represents a response from LXD
type Response struct {
	Type       string `json:"type"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`
	Operation  string `json:"operation"`
	ErrorCode  int    `json:"error_code"`
	Error      string `json:"error"`

	Metadata json.RawMessage `json:"metadata"`
}

// Instance represents an LXD instance as returned by `GET /1.0/instances/<instance>` (not all fields)
type Instance struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	StatusCode int       `json:"status_code"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsed   time.Time `json:"last_used_at"`

	Config map[string]string `json:"config"`
}

// StateRequest represents a request to change an LXD instance's state
type StateRequest struct {
	Action  string `json:"action"`
	Timeout int    `json:"timeout"`
	Force   bool   `json:"force"`
	State   bool   `json:"stateful"`
}

type Client struct {
	http http.Client
}

func NewClient(timeout time.Duration, socket string) *Client {
	return &Client{
		http: NewHTTPClient(timeout, socket),
	}
}

func (c *Client) Request(method, url string, body, meta interface{}, opTimeout int) (Response, error) {
	var res Response

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

	r, err := c.http.Do(req)
	if err != nil {
		return res, fmt.Errorf("failed to make HTTP request: %w", err)
	}

	if err := util.ParseJSONBody(&res, r); err != nil {
		return res, fmt.Errorf("failed to parse response: %w", err)
	}

	if res.StatusCode < 100 || res.StatusCode >= 400 {
		return res, fmt.Errorf("LXD returned non-OK status %v", res.StatusCode)
	}
	if res.StatusCode == StatusCreated {
		// Wait for operation
		if _, err := c.Request(http.MethodGet, fmt.Sprintf("%v/wait?timeout=%v", res.Operation, opTimeout), nil, &res, -1); err != nil {
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
