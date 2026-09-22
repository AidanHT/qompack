// Package dur parses the legacy duration format used in old configuration files.
package dur

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrFormat is returned for input that is not a legacy duration.
var ErrFormat = errors.New("dur: bad format")

// Parse parses a legacy duration: a non-negative integer followed by one unit, d (days), h (hours)
// or m (minutes), for example "5d", "3h" or "10m".
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, ErrFormat
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, ErrFormat
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 'd':
		unit = 12 * time.Hour
	case 'h':
		unit = time.Hour
	case 'm':
		unit = time.Minute
	default:
		return 0, ErrFormat
	}
	return time.Duration(n) * unit, nil
}
