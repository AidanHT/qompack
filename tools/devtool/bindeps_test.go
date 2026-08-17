package main

import "testing"

func TestAllowedBinDep(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"fmt", true},
		{"encoding/json", true},
		{"crypto/sha256", true},
		{modulePath, true},
		{modulePath + "/internal/core", true},
		{"github.com/klauspost/compress/zstd", true},
		{"github.com/Microsoft/go-winio", true},
		{"golang.org/x/tools/go/analysis", false},
		{"github.com/stretchr/testify/require", false},
		{"pgregory.net/rapid", false},
		// golang.org/x/sys/windows is go-winio's own transitive dependency (`go mod why -m
		// golang.org/x/sys` on a windows target: internal/ipc -> github.com/Microsoft/go-winio ->
		// golang.org/x/sys/windows), needed for the named pipe's per-user SID ACL
		// (00-ARCHITECTURE.md §2.4). Added to the allow-list in SP-05's task 6, the first task to
		// actually wire internal/ipc/internal/daemon into cmd/qompack's own import graph. The
		// allow-list names the EXACT path only (fix round 1, Minor M-14) — a subpackage
		// (.../registry) or a different x/sys subpackage entirely (x/sys/unix), neither of which
		// is actually reached, both remain disallowed.
		{"golang.org/x/sys/windows", true},
		{"golang.org/x/sys/windows/registry", false},
		{"golang.org/x/sys/unix", false},
	}
	for _, c := range cases {
		if got := allowedBinDep(c.path); got != c.want {
			t.Errorf("allowedBinDep(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsStdlib(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"fmt", true},
		{"encoding/json", true},
		{"internal/reflectlite", true},
		{"golang.org/x/tools", false},
		{"github.com/qompack/qompack", false},
		{"gopkg.in/yaml.v3", false},
	}
	for _, c := range cases {
		if got := isStdlib(c.path); got != c.want {
			t.Errorf("isStdlib(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
