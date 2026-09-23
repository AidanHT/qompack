// Package list parses comma-separated lists.
package list

import "strings"

// ParseList splits s on commas, trims spaces around each item and drops empty items. It returns
// nil when no item remains.
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
