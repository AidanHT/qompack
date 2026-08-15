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
		{"golang.org/x/sys/windows", false},
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
