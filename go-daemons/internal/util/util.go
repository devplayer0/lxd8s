package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	memAvailableRegexp = regexp.MustCompile(`(?m)^MemAvailable:\s*(\d+)\s*kB$`)
	memTotalRegexp     = regexp.MustCompile(`(?m)^MemTotal:\s*(\d+)\s*kB$`)
)

// AbsoluteOrPercentage parses a string as either an int or as a percentage (float) of a provided value
func AbsoluteOrPercentage(s string, max int) (int, error) {
	var err error
	var value int
	if strings.HasSuffix(s, "%") {
		percentage, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse percentage: %w", err)
		}

		value = int(float64(max) * (percentage / 100))
	} else {
		value, err = strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("failed to parse value: %w", err)
		}
	}

	return value, nil
}

// ParseJSONBody attempts to parse the response body as JSON
func ParseJSONBody(v interface{}, r *http.Response) error {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return err
	}

	return nil
}

// MemInfoMiB gets a value in MiB from /proc/meminfo
func MemInfoMiB(exp *regexp.Regexp) (uint64, error) {
	data, err := ioutil.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("failed to read /proc/meminfo: %w", err)
	}

	m := exp.FindStringSubmatch(string(data))
	if len(m) == 0 {
		return 0, errors.New("failed to find value in /proc/meminfo")
	}

	kib, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse /proc/meminfo value: %w", err)
	}

	return kib / 1024, nil
}

// MemTotal gets the total amount of memory in mebibytes
func MemTotal() (uint64, error) {
	return MemInfoMiB(memTotalRegexp)
}

// MemFree gets the amount of free memory in mebibytes
func MemFree() (uint64, error) {
	return MemInfoMiB(memAvailableRegexp)
}
