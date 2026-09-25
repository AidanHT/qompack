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
		// (.../registry), which is not reached, stays disallowed.
		{"golang.org/x/sys/windows", true},
		{"golang.org/x/sys/windows/registry", false},
		// golang.org/x/sys/unix joined in V6 (C1.19, 00-ARCHITECTURE.md §2.5 amendment): the
		// linux and darwin RenameDirectoryNoReplace in internal/paths, which the maintenance
		// restore publishes through, needs renameat2(RENAME_NOREPLACE) and
		// renamex_np(RENAME_EXCL). The standard library wraps neither, and on darwin a libSystem
		// call needs a cgo_import_dynamic trampoline, the code x/sys/unix generates. It is the same
		// module, version and licence that already ships in the Windows binary. The entry is
		// the exact path, like the windows one: its subdirectories and every other x/sys
		// package stay out.
		{"golang.org/x/sys/unix", true},
		{"golang.org/x/sys/unix/linux", false},
		{"golang.org/x/sys/cpu", false},
		{"golang.org/x/sys/plan9", false},
		{"golang.org/x/sys", false},
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
