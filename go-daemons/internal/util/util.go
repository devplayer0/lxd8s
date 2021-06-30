package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
)

var memAvailableRegexp = regexp.MustCompile(`(?m)^MemAvailable:\s*(\d+)\s*kB$`)

// ParseJSONBody attempts to parse the response body as JSON
func ParseJSONBody(v interface{}, r *http.Response) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}

	return nil
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
