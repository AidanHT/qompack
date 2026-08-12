//go:build tools

// Package tools pins the exact versions of the external command-line tools this repository
// invokes via `go run -modfile=tools/pinned/go.mod <import/path>`: gofumpt (format), golangci-lint
// (lint), govulncheck (vulnerability scan) and benchstat (benchmark diffing). It is never built by
// the root module — go.mod for this directory is a separate, nested module specifically so that
// none of these tool dependencies ever appear in `go list -deps ./cmd/qompack` (00-ARCHITECTURE.md
// §2.5, §2.6).
//
// The blank imports below are what make the pins real: `go mod tidy` only keeps a require as
// direct (and resolvable) while something imports it, and the "tools" build tag keeps this file,
// and therefore its imports, out of every ordinary build.
package tools

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "golang.org/x/perf/cmd/benchstat"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "mvdan.cc/gofumpt"
)
