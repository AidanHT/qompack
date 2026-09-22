package hostperm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/paths"
)

// The per-request cost of honouring host rules, measured rather than asserted: C1.9 asks for a
// bounded reload cost, and these are the numbers the close-out report quotes. No budget is derived
// from them here.

// benchPolicy builds a policy over a project with project, local and user settings present, using
// this platform's REAL managed locations (and registry values on Windows), because their stat and
// registry queries are part of what a production request pays.
func benchPolicy(b *testing.B, rules string) (*Policy, string) {
	b.Helper()
	root, home := b.TempDir(), b.TempDir()
	for _, f := range []string{
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if err := os.MkdirAll(paths.Long(filepath.Dir(f)), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(paths.Long(f), []byte(rules), 0o600); err != nil {
			b.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(paths.Long(f), old, old); err != nil {
			b.Fatal(err)
		}
	}
	p := New(Options{ProjectRoot: root, Home: home, Getenv: func(string) string { return "" }})
	return p, root
}

// tenRules is a realistic deny/ask list: the documentation's own examples.
const tenRules = `{"permissions":{"deny":["Read(./.env)","Read(./.env.*)","Read(./secrets/**)",` +
	`"Read(~/.ssh/**)","Read(//etc/**)","Read(*.pem)","Read(config/credentials.json)","Read(build)"],` +
	`"ask":["Read(./notes/**)","Read(!./notes/public/**)"]}}`

// BenchmarkSnapshotUnchanged is the steady state: every source stat'ed, nothing re-read.
func BenchmarkSnapshotUnchanged(b *testing.B) {
	p, _ := benchPolicy(b, tenRules)
	if _, err := p.Snapshot(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Snapshot(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSnapshotChanged forces a full re-read and re-parse on every iteration.
func BenchmarkSnapshotChanged(b *testing.B) {
	p, root := benchPolicy(b, tenRules)
	f := filepath.Join(root, ".claude", "settings.json")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		stamp := time.Now().Add(-time.Hour).Add(time.Duration(i) * time.Millisecond)
		if err := os.Chtimes(paths.Long(f), stamp, stamp); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := p.Snapshot(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvaluate is one path checked against the ten rules, links resolved.
func BenchmarkEvaluate(b *testing.B) {
	p, root := benchPolicy(b, tenRules)
	rs, err := p.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(root, "internal", "service", "handler_"+strconv.Itoa(7)+".go")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if rs.Evaluate(path).Effect != Allow {
			b.Fatal("an ordinary path must be allowed")
		}
	}
}

// BenchmarkEvaluateNoRules is the common case: no Read rule anywhere, so nothing is resolved.
func BenchmarkEvaluateNoRules(b *testing.B) {
	p, root := benchPolicy(b, `{"permissions":{"allow":["Bash(ls *)"]}}`)
	rs, err := p.Snapshot()
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(root, "src", "a.go")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rs.Evaluate(path)
	}
}
