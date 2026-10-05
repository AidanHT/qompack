package rehydrate

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// BenchmarkBuild measures rehydrate.Build's own cost (audit 2's finding 35: internal/rehydrate had
// no benchmark, so the D63 screen's 2.6-2.9x CPU regression reached the freeze unseen). The fixture
// is a long session's checkpoint: 200 tool pointers in eight store-preview shapes (a Read's and an
// Edit's file_path, a Bash command, a Grep and a Glob preview, a Task's canonical JSON, a command run
// from the project root, a git command naming a file), 50 file pointers, and 0, 600 (one in five a
// path-keyed file_pointer drop, the rest tool_pointer drops) or 1000 (every one a file_pointer drop)
// checkpoint drop entries. "norules" is a project with no Read deny or ask rule (the host's rules
// are established and empty); "uat12" is one with UAT-12's three Read deny rules, judged by an
// in-memory stand-in for the host (the daemon's cost rows measure the real adapter). Beside ns/op it
// reports each build's p50 and p99 wall time, measured per iteration.
func BenchmarkBuild(b *testing.B) {
	root := previewRoot("proj")
	for _, rules := range []struct {
		name string
		host func() HostPaths
	}{
		{"norules", func() HostPaths {
			return func() HostRules { return HostRules{Refuses: func(string) bool { return false }} }
		}},
		{"uat12", func() HostPaths { return hostRules(root, uat12Rules...) }},
	} {
		for _, drops := range []struct {
			name           string
			n, filePerDrop int
		}{{"drops0", 0, 1}, {"drops600", 600, 5}, {"drops1000", 1000, 1}} {
			b.Run(rules.name+"/mix200_files50_"+drops.name, func(b *testing.B) {
				cp := benchCheckpoint(b, root, drops.n, drops.filePerDrop)
				r := Request{
					Session: cp.Session, Source: "compact", ProjectRoot: root, Budget: 0, Checkpoint: cp,
					Ref: checkpoint.Ref{Seq: cp.Seq, Path: filepath.Join(root, ".qompack", "checkpoints", "0001.json")},
					Cfg: testCfg(),
				}
				d := Deps{HostPaths: rules.host()}
				if _, err := Build(context.Background(), r, d); err != nil {
					b.Fatal(err)
				}
				took := make([]time.Duration, 0, b.N)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					start := time.Now()
					if _, err := Build(context.Background(), r, d); err != nil {
						b.Fatal(err)
					}
					took = append(took, time.Since(start))
				}
				b.StopTimer()
				sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
				b.ReportMetric(float64(took[len(took)/2].Nanoseconds()), "p50-ns")
				b.ReportMetric(float64(took[(len(took)*99)/100].Nanoseconds()), "p99-ns")
			})
		}
	}
}

// benchCheckpoint is BenchmarkBuild's checkpoint for root: ckUAT05's records, 50 file pointers, 200
// tool pointers whose summaries are the store's own previews, and n drop entries of which one in
// filePerDrop is a path-keyed file_pointer drop.
func benchCheckpoint(b *testing.B, root string, n, filePerDrop int) checkpoint.Checkpoint {
	b.Helper()
	cp := ckUAT05()
	cp.Pointers.Files = nil
	for i := 0; i < 50; i++ {
		p := fmt.Sprintf("pkg/mod%d/file%d.go", i%7, i)
		cp.Pointers.Files = append(cp.Pointers.Files, checkpoint.FilePointer{Path: p, Hash: hashOf(p), Why: "referenced"})
	}
	cp.Pointers.Tools = nil
	for i := 0; i < 200; i++ {
		var args map[string]any
		switch i % 8 {
		case 0:
			args = map[string]any{"file_path": filepath.Join(root, "src", fmt.Sprintf("mod%d", i%9), fmt.Sprintf("a%d.go", i))}
		case 1:
			args = map[string]any{
				"file_path":  filepath.Join(root, "src", fmt.Sprintf("mod%d", i%9), fmt.Sprintf("b%d.go", i)),
				"old_string": "x", "new_string": "y",
			}
		case 2:
			args = map[string]any{"command": fmt.Sprintf("go test ./internal/mod%d/... -run TestRetry%d", i%9, i), "description": "run"}
		case 3:
			args = map[string]any{"pattern": fmt.Sprintf("retryBackoff%d", i), "path": filepath.Join(root, "internal", fmt.Sprintf("mod%d", i%9))}
		case 4:
			args = map[string]any{"pattern": fmt.Sprintf("**/*_%d.go", i)}
		case 5:
			args = map[string]any{
				"description": fmt.Sprintf("explore %d", i), "subagent_type": "Explore",
				"prompt": fmt.Sprintf("find where mod%d handles retries and report the file", i%9),
			}
		case 6:
			args = map[string]any{"command": fmt.Sprintf("cd %s && rg -n retry%d internal/", root, i)}
		default:
			args = map[string]any{"command": fmt.Sprintf("git diff HEAD~%d -- src/mod%d/file%d.go", i%5+1, i%9, i)}
		}
		raw, err := json.Marshal(args)
		if err != nil {
			b.Fatal(err)
		}
		_, preview := store.ArgsDigest(raw)
		id := fmt.Sprintf("toolu_bench_%03d", i)
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(id), Hash: hashOf(id), Summary: preview,
		})
	}
	for i := 0; i < n; i++ {
		e := checkpoint.DropEntry{
			Kind: "tool_pointer", ID: fmt.Sprintf("toolu_drop_%04d", i), Detail: "truncated at budget; expand(hash) still resolves",
		}
		if i%filePerDrop == 0 {
			e = checkpoint.DropEntry{
				Kind: "file_pointer", ID: fmt.Sprintf("vendor/m%d/z%d.go", i%50, i),
				Detail: "truncated at budget; re_read(path) still resolves",
			}
		}
		cp.Dropped = append(cp.Dropped, e)
	}
	return cp
}
