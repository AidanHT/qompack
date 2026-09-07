package mcp

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The one large corpus this package's budget test and benchmarks share.
//
// It exists separately from corpus_test.go's seeded fixture because the two answer different
// questions. That one is EXACT — 200 000 bytes, a symbol at a known offset — so a span assertion
// is a statement about the resolver. This one is BIG — two thousand tool uses over roughly forty
// megabytes — so a latency measurement is a statement about a session that has actually been
// worked in, rather than about a store with six files in it.
//
// Three properties of its construction are load-bearing:
//
//   - It is built ONCE per test binary, behind a sync.Once. At the store's real write throughput
//     on this host (~3 MB/s) the corpus costs about thirteen seconds, which is affordable once and
//     is not affordable per test or per benchmark iteration.
//   - It lives under os.MkdirTemp, not t.TempDir. A t.TempDir is removed when the test that
//     created it ends, which for a fixture shared by a test and three benchmarks means the second
//     consumer finds an empty directory. TestMain takes it away instead.
//   - It does NOT go through newFixture. newFixture is correct and is reused everywhere else in
//     this package, but it is built on t.TempDir and t.Cleanup — including the t.Cleanup that
//     closes the store — so a fixture built through it cannot outlive its first consumer by
//     construction. What is reused instead is everything that does not carry a *testing.T: the
//     fixture type itself, buildFiller, and the same real store the rest of the package uses.

// The corpus's shape. These are the one place to tune it: bigFixtureUses times bigFixtureUseBytes
// is the total, and everything else is derived.
const (
	// bigFixtureUses is how many tool uses the corpus records.
	bigFixtureUses = 2000
	// bigFixtureUseBytes is the exact canonical size of each one; 2000 x 20 KiB is ~39 MiB.
	bigFixtureUseBytes = 20 * 1024
	// bigFixturePathEvery says how often a tool use is a pathless Bash capture rather than a file
	// read: one in four, which is roughly the mix a real session produces and is what gives
	// re_read fifteen hundred addressable paths to choose from.
	bigFixturePathEvery = 4
)

// bigFixtureSkip is the message every consumer skips with under -short. The corpus is by far the
// slowest thing in this package, and a short run is exactly the run that must not pay for it.
//
// The "platform: " prefix is not decoration: tools/devtool's stubskips check permits exactly three
// skip reasons tree-wide, and it is the only one of the three that fits a skip hiding no missing
// work. internal/store/put_test.go's own -short skip uses it for the same reason.
const bigFixtureSkip = "platform: -short skips the ~40 MB retrieval corpus and its multi-minute build"

// The shared corpus and its guard. bigFixtureDir is kept separately from the fixture so TestMain
// can remove it even if construction failed halfway through.
var (
	bigFixtureOnce sync.Once
	bigFixtureVal  *fixture
	bigFixtureDir  string
	bigFixtureErr  error
)

// TestMain exists solely to take the shared corpus away again.
//
// internal/mcp had no TestMain before this file, which is what makes adding one safe: nothing else
// in the package depends on the default behaviour, and golden_test.go's -update flag registration
// is defensive about a TestMain already owning the name either way. The cleanup runs BEFORE
// os.Exit because os.Exit runs no deferred function.
func TestMain(m *testing.M) {
	code := m.Run()
	closeBigFixture()
	os.Exit(code)
}

// closeBigFixture releases the corpus's store handles and removes its directory. It is a no-op
// when the corpus was never built, which is every -short run and every run whose -run pattern
// selected none of its consumers.
func closeBigFixture() {
	if bigFixtureVal != nil && bigFixtureVal.Store != nil {
		_ = bigFixtureVal.Store.Close()
	}
	if bigFixtureDir != "" {
		_ = os.RemoveAll(paths.Long(bigFixtureDir))
	}
}

// newBigFixture returns the package's single large corpus, building it on first use.
//
// It takes a testing.TB rather than a *testing.T because a benchmark is one of its consumers and
// cannot produce one. That is also why the build itself reports through bigFixtureErr instead of
// through require: sync.Once takes a bare func(), so the failure has to be carried out rather than
// asserted in place.
func newBigFixture(tb testing.TB) *fixture {
	tb.Helper()
	bigFixtureOnce.Do(buildBigFixture)
	if bigFixtureErr != nil {
		tb.Fatalf("building the shared %d-use corpus: %v", bigFixtureUses, bigFixtureErr)
	}
	return bigFixtureVal
}

// buildBigFixture is the Once's body: a real store over a real directory, the eight tools
// registered against it, and bigFixtureUses tool uses seeded in.
//
// The collaborators a large-corpus latency measurement does not exercise are left nil rather than
// faked — there is no ledger and no checkpoint reader here, because `already_tried` and `why` read
// neither the store nor a span and would measure nothing about this corpus. The promoter IS real,
// because every expand notes an expansion and persists it, and leaving that out would measure a
// retrieval path production does not have.
func buildBigFixture() {
	dir, err := os.MkdirTemp("", "qompack-mcp-big-")
	if err != nil {
		bigFixtureErr = fmt.Errorf("mkdir temp: %w", err)
		return
	}
	bigFixtureDir = dir

	if err := paths.EnsureLayout(paths.Of(dir)); err != nil {
		bigFixtureErr = fmt.Errorf("EnsureLayout(%s): %w", dir, err)
		return
	}

	cfg := config.Defaults()
	clk := newFakeClock(epoch)

	st, err := store.Open(dir, cfg, store.Deps{Log: logging.Nop(), Clock: clk})
	if err != nil {
		bigFixtureErr = fmt.Errorf("store.Open(%s): %w", dir, err)
		return
	}

	f := &fixture{
		Root:    dir,
		Cfg:     cfg,
		Store:   st,
		Checks:  &fakeCheckpoints{},
		Drops:   &fakeDrops{},
		Widen:   &fakeWidener{},
		Metrics: obs.New(clk),
		Clock:   clk,
	}

	prom, err := NewPromoter(PromotionsPath(dir), cfg.Retrieval.PromoteAfterExpansions, clk)
	if err != nil {
		bigFixtureErr = fmt.Errorf("NewPromoter: %w", err)
		return
	}
	f.Prom = prom

	f.Deps = ToolDeps{
		Cfg:         cfg,
		ProjectRoot: dir,
		Clock:       clk,
		Log:         logging.Nop(),
		Metrics:     f.Metrics,
		Store:       st,
		Promoter:    prom,
		Widener:     f.Widen,
	}
	f.Server = NewServerWithOptions(ServerOptions{Name: ServerName, Version: core.Version, Log: logging.Nop()})
	if err := RegisterAll(f.Server, f.Deps); err != nil {
		bigFixtureErr = fmt.Errorf("RegisterAll: %w", err)
		return
	}

	if err := seedBigFixture(f); err != nil {
		bigFixtureErr = err
		return
	}
	bigFixtureVal = f
}

// bigFixturePath is the project-relative path of the i-th file-backed tool use.
func bigFixturePath(i int) string { return fmt.Sprintf("src/gen/unit%04d.ts", i) }

// bigFixtureToolUseID is the i-th tool use's id.
func bigFixtureToolUseID(i int) core.ToolUseID { return core.ToolUseID(fmt.Sprintf("tu-big-%04d", i)) }

// bigFixtureBody generates the i-th body: exactly bigFixtureUseBytes bytes, opening with text a
// recall query can match and closing with per-index filler.
//
// The filler's label carries i, which matters more than it looks: identical bytes would deduplicate
// down to a handful of chunks and the "40 MB corpus" would be a 20 KB corpus wearing a hat, with
// every measurement below reading one warm chunk instead of two thousand cold ones.
func bigFixtureBody(i int) string {
	head := fmt.Sprintf("// %s\n// a pool timeout here means pgbouncer is in transaction mode (%d ms)\n"+
		"export function connect%04d(): void {\n  return;\n}\n", bigFixturePath(i), i*10, i)
	return head + buildFiller(fmt.Sprintf("unit%04d", i), bigFixtureUseBytes-len(head))
}

// seedBigFixture writes the corpus: one PutBytes per tool use, a file version for the three in
// four that carry a path, and a ToolUseRecord for every one.
//
// It repeats fixture.store's body rather than calling putAndRecord because putAndRecord takes a
// *testing.T and this runs inside a sync.Once. The two must stay in step; if fixture.store grows a
// step, this needs the same one, which is why the field lists below are written in the same order.
func seedBigFixture(f *fixture) error {
	ctx := context.Background()
	for i := 0; i < bigFixtureUses; i++ {
		body := bigFixtureBody(i)

		tool, path := "Read", bigFixturePath(i)
		if i%bigFixturePathEvery == 0 {
			tool, path = "Bash", ""
		}

		res, err := f.Store.PutBytes(ctx, []byte(body), store.PutOptions{Tool: tool, Path: path})
		if err != nil {
			return fmt.Errorf("PutBytes(%d): %w", i, err)
		}

		turn := core.TurnIndex(i + 1)
		if path != "" {
			if err := f.Store.AppendFileVersion(ctx, path, store.FileVersion{
				TS: core.NowMilli(f.Clock), Root: res.Root.Hash, Turn: turn, Bytes: int64(len(body)),
			}); err != nil {
				return fmt.Errorf("AppendFileVersion(%s): %w", path, err)
			}
		}

		if err := f.Store.RecordToolUse(ctx, store.ToolUseRecord{
			ID:          bigFixtureToolUseID(i),
			Session:     testSession,
			Turn:        turn,
			TS:          core.NowMilli(f.Clock),
			Tool:        tool,
			ArgsPreview: path,
			Root:        res.Root.Hash,
			Path:        path,
			Bytes:       int64(len(body)),
		}); err != nil {
			return fmt.Errorf("RecordToolUse(%d): %w", i, err)
		}
	}
	return nil
}
