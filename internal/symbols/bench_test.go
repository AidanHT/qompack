package symbols_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/symbols"
)

// benchSourceBytes is the input size every benchmark in this file is stated against: 100 KB, the
// size plans/V2-SP-04 §"Performance budget" names for all three symbols benchmarks.
const benchSourceBytes = 100 * 1024

// benchNameCount is the name-set size BenchmarkReferences_100KB_50Names measures against. It is the
// budget's own number (50 names), and it is deliberately larger than any single retrieval query so
// the O(len(b))-regardless-of-len(names) claim is actually exercised.
const benchNameCount = 50

// tsBenchSource builds a ~100 KB TypeScript file with the structure a real front-end module has:
// exported classes with several methods each, an interface, and an arrow-function const. It is
// built rather than committed because the benchmark's contract is a *size*, and a committed file
// would silently drift away from 100 KB the first time someone edited it.
func tsBenchSource() []byte {
	var sb strings.Builder
	sb.Grow(benchSourceBytes + benchSourceBytes/8)
	for i := 0; sb.Len() < benchSourceBytes; i++ {
		fmt.Fprintf(&sb, `export interface Shape%d {
  readonly id: number;
  label: string;
}

export class Widget%d {
  private state = 0;

  constructor(private readonly shape: Shape%d) {
    this.state = shape.id;
  }

  render(depth: number): string {
    if (depth <= 0) {
      return this.shape.label;
    }
    for (let i = 0; i < depth; i += 1) {
      this.state += i;
    }
    return this.shape.label + ":" + String(this.state);
  }

  reset(): void {
    this.state = 0;
  }
}

export const make%d = (shape: Shape%d) => new Widget%d(shape);

`, i, i, i, i, i, i)
	}
	return []byte(sb.String())
}

// benchNames returns benchNameCount identifiers, half of which really occur in tsBenchSource and
// half of which never do, so the benchmark pays for both the hit and the miss path.
func benchNames() []string {
	names := make([]string, 0, benchNameCount)
	for i := 0; i < benchNameCount/2; i++ {
		names = append(names, fmt.Sprintf("Widget%d", i), fmt.Sprintf("absent%d", i))
	}
	return names
}

// BenchmarkExtract_100KB measures a full extraction pass over 100 KB of TypeScript. Budget: under
// 2 ms/op (plans/V2-SP-04 §"Performance budget"). SP-06 calls Extract once per stored chunk set and
// SP-08's observer calls it on the hot path, so this is a per-tool-use cost.
func BenchmarkExtract_100KB(b *testing.B) {
	src := tsBenchSource()
	ex := symbols.New()

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := ex.Extract("bench.ts", src); len(got) == 0 {
			b.Fatal("benchmark fixture produced no symbols")
		}
	}
}

// BenchmarkEnclosing_100KB measures the §8.7 minimal-sufficient-span resolution SP-13's mcp
// expand/re_read handlers call per request. Budget: under 2 ms/op. Enclosing re-extracts, so it is
// bounded below by BenchmarkExtract_100KB by construction.
func BenchmarkEnclosing_100KB(b *testing.B) {
	src := tsBenchSource()
	ex := symbols.New()
	off := len(src) / 2

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := ex.Enclosing("bench.ts", src, off); !ok {
			b.Fatal("benchmark offset resolved to no symbol")
		}
	}
}

// BenchmarkReferences_100KB_50Names measures the symbol-reference count SP-15's cheap scorer runs
// per candidate. Budget: under 1 ms/op, and the cost must track len(b), not len(names).
func BenchmarkReferences_100KB_50Names(b *testing.B) {
	src := tsBenchSource()
	names := benchNames()
	ex := symbols.New()

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := ex.References(src, names); len(got) != benchNameCount {
			b.Fatalf("References returned %d keys, want %d", len(got), benchNameCount)
		}
	}
}
