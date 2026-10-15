package modwire

import (
	"fmt"
	"strconv"
	"strings"
)

// MeetsMinHostVersion reports whether a host version string, as `$.session.version()` reports it
// in its `version` field, is at least MinHostVersion. It is the only version gate: the Go side
// applies it when it serves a hello, and the mod obeys the answer's `enabled` field.
//
// The version must start with MAJOR.MINOR.PATCH in decimal. After that, a "+build" suffix does not
// affect ordering, and a "-prerelease" suffix (a development build such as
// 2.1.300-dev.20261014.t101500.sha1a2b3c4) counts as below the release it precedes, so a
// prerelease of X does not meet a minimum of X but a prerelease of a later release does. Anything
// else, including surrounding text or a leading "v", is unreadable and does not meet the minimum.
func MeetsMinHostVersion(version string) bool {
	got, gotPre, ok := parseHostVersion(version)
	if !ok {
		return false
	}
	want, _, ok := parseHostVersion(MinHostVersion)
	if !ok {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return got[i] > want[i]
		}
	}
	return !gotPre
}

// parseHostVersion reads MAJOR.MINOR.PATCH and whether a prerelease suffix follows it.
func parseHostVersion(v string) (nums [3]uint64, prerelease, ok bool) {
	release, build, _ := strings.Cut(v, "+")
	if strings.Contains(v, "+") && build == "" {
		return nums, false, false
	}
	release, pre, hasPre := strings.Cut(release, "-")
	if hasPre && pre == "" {
		return nums, false, false
	}
	parts := strings.Split(release, ".")
	if len(parts) != len(nums) {
		return nums, false, false
	}
	for i, p := range parts {
		if p == "" || strings.TrimLeft(p, "0123456789") != "" {
			return nums, false, false
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nums, false, false
		}
		nums[i] = n
	}
	return nums, hasPre, true
}

// Validate checks a hello response against the contract before the CLI writes it: the status
// invariants, that an unserved hello enables nothing, and that only an enabled hello steers.
func (r HelloResponse) Validate() error {
	if err := validStatus(r.Status); err != nil {
		return err
	}
	if !r.OK && (r.Enabled || r.Steering) {
		return fmt.Errorf("%w: an unserved hello enables nothing", ErrInvalid)
	}
	if r.Steering && !r.Enabled {
		return fmt.Errorf("%w: a hello that is not enabled does not steer", ErrInvalid)
	}
	return nil
}
