package store

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/symbols"
)

// These tests live in package store, not store_test, because most of them assert on the on-disk
// layout and on the in-memory index — both unexported. That also means they cannot use
// internal/testutil: testutil imports internal/store, so importing it back would be an import
// cycle. The project helper below is the minimum replacement.

// The test chunker's boundary parameters.
//
// They are deliberately an order of magnitude smaller than config's production defaults
// (1 KiB/4 KiB/16 KiB). A 23 KB fixture chunked at a 4 KiB average is only ~5 chunks, and a
// content-defined splitter needs MANY chunks before an edit's boundary shift reliably
// resynchronizes — with five chunks it usually does not, so the dedup these tests exist to
// measure would be invisible. Smaller chunks make resynchronization observable on a fixture small
// enough to keep in the repository.
const (
	testChunkMin = 64
	// mixConstant/mixShift define the cut probability: the gear value is multiplied by a
	// 64-bit odd constant (so the high bits mix every input bit) and the top 12 are tested,
	// giving ~1-in-256 and therefore a ~256 B average chunk. Testing the RAW low bits instead
	// does not work on regular source text: they cycle without ever reaching zero.
	mixConstant  = 0x9E3779B97F4A7C15
	mixShift     = 56
	testChunkMax = 2048
)

// gearTable is the content-defined chunker's substitution table, filled deterministically so a
// given input always chunks identically across runs and platforms.
var gearTable = func() [256]uint64 {
	var t [256]uint64
	x := uint64(0x2545F4914F6CDD1D)
	for i := range t {
		// xorshift64*, purely to spread the table; any deterministic spread would do.
		x ^= x >> 12
		x ^= x << 25
		x ^= x >> 27
		t[i] = x * 0x2545F4914F6CDD1D
	}
	return t
}()

// cdcChunker is a small CONTENT-DEFINED chunker: a gear hash over a sliding window, bounded by min
// and max.
//
// The hash MUST use a left SHIFT rather than a rotate. A shift pushes each byte's contribution out
// of the 64-bit register after 64 steps, so the value depends only on the last ~64 bytes — which is
// exactly what lets two versions of one file resynchronize on the same boundaries after an edit.
// Rotate-and-XOR looks equivalent but never forgets: XOR does not decay, so every byte since the
// last cut keeps contributing and two streams knocked out of phase by an insertion never realign.
//
// It has to be content-defined rather than fixed-size, and that is the whole point. internal/chunk
// is still an SP-01 stub whose Split returns nil (Rule W-2), so these tests must inject their own
// chunker — but a FIXED-size splitter shifts every subsequent boundary when a single byte is
// inserted, so it cannot dedup two versions of one file at all. Testing the store's dedup
// bookkeeping against such a chunker would assert the opposite of the behaviour §6.1 promises.
// This is not a reimplementation of SP-04's FastCDC and makes no claim to match its boundaries; it
// only supplies the boundary-stability property the store's own logic is being tested against.
type cdcChunker struct {
	min, max int
	shift    uint
}

// Split cuts data at content-defined boundaries.
func (c cdcChunker) Split(data []byte) []chunk.Chunk {
	minSize, maxSize, shift := c.min, c.max, c.shift
	if minSize == 0 {
		minSize, maxSize, shift = testChunkMin, testChunkMax, mixShift
	}
	var out []chunk.Chunk
	start, h := 0, uint64(0)
	for i := 0; i < len(data); i++ {
		h = (h << 1) + gearTable[data[i]]
		size := i - start + 1
		if (size >= minSize && (h*mixConstant)>>shift == 0) || size >= maxSize {
			out = append(out, chunk.Chunk{
				Offset: int64(start),
				Len:    size,
				Hash:   core.HashBytes(core.DomainChunk, data[start:i+1]),
			})
			start, h = i+1, 0
		}
	}
	if start < len(data) {
		out = append(out, chunk.Chunk{
			Offset: int64(start),
			Len:    len(data) - start,
			Hash:   core.HashBytes(core.DomainChunk, data[start:]),
		})
	}
	return out
}

// SplitStream is unused by this package: the store only ever calls Split.
func (c cdcChunker) SplitStream(r io.Reader, fn func(chunk.Chunk, []byte) error) error {
	return core.ErrNotImplemented
}

// newFixedChunker returns the default content-defined test chunker, tuned fine so dedup is
// observable on a repository-sized fixture.
func newFixedChunker() chunk.Chunker { return cdcChunker{} }

// newProdChunker returns a chunker at config's PRODUCTION boundary parameters (1 KiB / ~4 KiB /
// 16 KiB). The benchmarks use it because the latency budgets are stated against production chunk
// sizes: the fine-grained default splits 100 KB into ~390 objects rather than ~25, and on Windows
// the per-object create+rename syscalls dominate, so benchmarking with it would measure the test
// double's granularity rather than the store.
func newProdChunker() chunk.Chunker {
	return cdcChunker{min: 1024, max: 16384, shift: 52}
}

// fixedChunker splits data into equal-size chunks. Only the MaxPutBytes truncation test uses it,
// where the point is to move 64 MiB in a few dozen hashes rather than to dedup anything.
type fixedChunker struct{ size int }

// Split tiles data into fixed-size chunks.
func (f fixedChunker) Split(data []byte) []chunk.Chunk {
	var out []chunk.Chunk
	for off := 0; off < len(data); off += f.size {
		end := off + f.size
		if end > len(data) {
			end = len(data)
		}
		out = append(out, chunk.Chunk{
			Offset: int64(off),
			Len:    end - off,
			Hash:   core.HashBytes(core.DomainChunk, data[off:end]),
		})
	}
	return out
}

// SplitStream is unused by this package: the store only ever calls Split.
func (f fixedChunker) SplitStream(r io.Reader, fn func(chunk.Chunk, []byte) error) error {
	return core.ErrNotImplemented
}

// ── canon doubles ────────────────────────────────────────────────────────────────────────────

// canonFunc adapts a plain function into a canon.Registry, so each test can state exactly the
// canonicalization behaviour it needs in one line.
type canonFunc func(tool, path string, in []byte, o canon.Options) (canon.Result, error)

// Register is not exercised by these tests.
func (canonFunc) Register(c canon.Canonicalizer) error { return core.ErrNotImplemented }

// For returns nothing: these doubles are a single fused pass, not a registry of passes.
func (canonFunc) For(tool, path string) []canon.Canonicalizer { return nil }

// Names identifies the double in any Applied list a test inspects.
func (canonFunc) Names() []string { return []string{"testdouble"} }

// Run applies the wrapped function.
func (f canonFunc) Run(tool, path string, in []byte, o canon.Options) (canon.Result, error) {
	return f(tool, path, in, o)
}

// canonIdentity passes content through unchanged — the "canonicalization ran and did nothing"
// baseline, as distinct from the SP-01 stub, which fails.
func canonIdentity() canon.Registry {
	return canonFunc(func(_, _ string, in []byte, _ canon.Options) (canon.Result, error) {
		return canon.Result{Canonical: in}, nil
	})
}

// canonUpper uppercases content, so a test can prove the bytes that were CHUNKED are the
// canonicalized ones and not the input.
func canonUpper() canon.Registry {
	return canonFunc(func(_, _ string, in []byte, _ canon.Options) (canon.Result, error) {
		return canon.Result{Canonical: bytes.ToUpper(in)}, nil
	})
}

// canonFailing always fails, so a test can prove Put falls back to the redacted bytes rather than
// failing the whole ingest.
func canonFailing() canon.Registry {
	return canonFunc(func(_, _ string, _ []byte, _ canon.Options) (canon.Result, error) {
		return canon.Result{}, errors.New("testdouble: canonicalization unavailable")
	})
}

// timestampPrefix is the volatile line prefix canonStripTimestamp removes.
const timestampPrefix = "TIMESTAMP: "

// canonStripTimestamp drops any line beginning with timestampPrefix and, when KeepDeltas is set,
// records each removal as a Delta. It is the double behind the tests that need two inputs which
// differ ONLY in a volatile substring and must therefore canonicalize to a single root.
func canonStripTimestamp() canon.Registry {
	return canonFunc(func(_, _ string, in []byte, o canon.Options) (canon.Result, error) {
		var out bytes.Buffer
		var deltas []canon.Delta
		for _, line := range strings.SplitAfter(string(in), "\n") {
			if strings.HasPrefix(line, timestampPrefix) {
				if o.KeepDeltas {
					deltas = append(deltas, canon.Delta{
						Offset:   out.Len(),
						Original: strings.TrimRight(line, "\n"),
						Class:    canon.ClassTimestamps,
					})
				}
				continue
			}
			out.WriteString(line)
		}
		return canon.Result{Canonical: out.Bytes(), Deltas: deltas}, nil
	})
}

// canonWithSignature returns content unchanged but attaches sig, so near-duplicate detection can
// be exercised while internal/sketch is still a stub.
func canonWithSignature(sig sketch.Signature) canon.Registry {
	return canonFunc(func(_, _ string, in []byte, _ canon.Options) (canon.Result, error) {
		return canon.Result{Canonical: in, Signature: sig}, nil
	})
}

// ── redact double ────────────────────────────────────────────────────────────────────────────

// fixedRedactor replaces every occurrence of one literal, so the ingest-order tests do not depend
// on internal/redact having landed yet.
type fixedRedactor struct{ secret string }

// Redact replaces every occurrence of the configured literal with a placeholder.
func (r fixedRedactor) Redact(in []byte) ([]byte, []redact.Match) {
	if r.secret == "" {
		return in, nil
	}
	var matches []redact.Match
	for off := 0; ; {
		i := bytes.Index(in[off:], []byte(r.secret))
		if i < 0 {
			break
		}
		matches = append(matches, redact.Match{Offset: off + i, Len: len(r.secret), Rule: "testdouble"})
		off += i + len(r.secret)
	}
	if len(matches) == 0 {
		return in, nil
	}
	return bytes.ReplaceAll(in, []byte(r.secret), []byte("«redacted:testdouble»")), matches
}

// Rules names the single active rule.
func (r fixedRedactor) Rules() []string { return []string{"testdouble"} }

// ── symbols double ───────────────────────────────────────────────────────────────────────────

// fakeSymbols reports a fixed symbol table, so the symbol-aware paths can be exercised while
// internal/symbols is still an SP-01 stub.
type fakeSymbols struct {
	syms []symbols.Symbol
	refs map[string]int
}

// Extract returns the fixed table.
func (f fakeSymbols) Extract(path string, b []byte) []symbols.Symbol { return f.syms }

// Enclosing returns the first symbol whose span contains off.
func (f fakeSymbols) Enclosing(path string, b []byte, off int) (symbols.Symbol, bool) {
	for _, s := range f.syms {
		if off >= s.Offset && off < s.Offset+s.Len {
			return s, true
		}
	}
	return symbols.Symbol{}, false
}

// References returns the fixed reference counts.
func (f fakeSymbols) References(b []byte, names []string) map[string]int { return f.refs }

// ── project and store construction ───────────────────────────────────────────────────────────

// fakeClock is a core.Clock that only moves when a test moves it, so every timestamp this package
// writes into an index file is deterministic.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// testEpoch is the fixed instant every fakeClock starts at.
var testEpoch = time.Date(2024, 6, 13, 9, 11, 4, 0, time.UTC)

// newFakeClock returns a clock frozen at testEpoch.
func newFakeClock() *fakeClock { return &fakeClock{now: testEpoch} }

// Now returns the current fake instant.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Since returns the fake elapsed time since t.
func (c *fakeClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }

// Advance moves the clock forward.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// project is a disposable Qompack project on disk.
type project struct {
	Root  string
	Cfg   config.Config
	Clock *fakeClock
	Log   logging.Logger
}

// newProject creates a project root with a real .qompack layout under t.TempDir().
//
// HOME, USERPROFILE and QOMPACK_HOME are redirected into the temp tree because store.Open's
// default token estimator resolves tokens.DefaultCalibPath(), which reads the user's home
// directory: without this, a test run would write to the developer's own ~/.qompack, which §13
// invariant 7 forbids.
func newProject(t *testing.T) *project {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		require.NoError(t, os.MkdirAll(paths.Long(d), 0o700))
	}
	t.Setenv("QOMPACK_PROJECT_ROOT", root)
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	return &project{Root: root, Cfg: config.Defaults(), Clock: newFakeClock(), Log: logging.Nop()}
}

// storeOpt customizes newTestStore's configuration and dependencies.
type storeOpt func(*config.Config, *Deps)

// withCanon injects a canon.Registry double.
func withCanon(r canon.Registry) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Canon = r }
}

// withRedactor injects a redact.Redactor double.
func withRedactor(r redact.Redactor) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Redact = r }
}

// withNilRedactor clears Deps.Redact, so a test can prove Open's defaulting never leaves a store
// without redaction.
func withNilRedactor() storeOpt {
	return func(_ *config.Config, d *Deps) { d.Redact = nil }
}

// withSymbols injects a symbols.Extractor double.
func withSymbols(e symbols.Extractor) storeOpt {
	return func(_ *config.Config, d *Deps) { d.Symbols = e }
}

// withStubChunker restores the SP-01 stub chunker, so a test can prove splitChecked's data-loss
// guard actually fires.
func withStubChunker() storeOpt {
	return func(_ *config.Config, d *Deps) { d.Chunker = chunk.New(chunk.DefaultParams()) }
}

// withCompressionNone turns object compression off.
func withCompressionNone() storeOpt {
	return func(c *config.Config, _ *Deps) { c.Store.Compression = compressionNone }
}

// withMinHash configures near-duplicate detection.
func withMinHash(enabled bool, threshold float64) storeOpt {
	return func(c *config.Config, _ *Deps) {
		c.Store.Canonicalize.MinHash.Enabled = enabled
		c.Store.Canonicalize.MinHash.NearDupThreshold = threshold
	}
}

// testProject is one disposable project plus the store opened over it.
type testProject struct {
	*project
	Store   *FSStore
	Metrics obs.Registry
}

// newTestStore builds a project and opens a real FSStore over it, with a working chunker injected.
func newTestStore(t *testing.T, opts ...storeOpt) *testProject {
	t.Helper()
	return openOver(t, newProject(t), opts...)
}

// openOver opens a store over an existing project, which is what the reopen tests need. The store
// is closed by t.Cleanup; Close is idempotent, so a test that closes it early is fine.
func openOver(t *testing.T, p *project, opts ...storeOpt) *testProject {
	t.Helper()

	cfg := p.Cfg
	m := obs.New(p.Clock)
	deps := Deps{
		Chunker: newFixedChunker(),
		Canon:   canonIdentity(),
		Log:     p.Log,
		Metrics: m,
		Clock:   p.Clock,
	}
	for _, o := range opts {
		o(&cfg, &deps)
	}

	// openFS, not the exported Open: these are in-package tests of the concrete store, and going
	// through the interface would couple this whole file to whichever sibling method happens to be
	// unimplemented at the moment (Store is only satisfied once every subplan slice has landed).
	fs, err := openFS(p.Root, cfg, deps)
	require.NoError(t, err)
	t.Cleanup(func() { _ = fs.Close() })
	return &testProject{project: p, Store: fs, Metrics: m}
}

// counter reads a named counter's current value, or 0 when it was never touched.
func (tp *testProject) counter(name string) int64 {
	return tp.Metrics.Counter(name).Value()
}

// objectPaths lists every object file under objects/, relative to the objects root, sorted.
func (tp *testProject) objectPaths(t *testing.T) []string {
	t.Helper()
	base := paths.Long(paths.Of(tp.Root).Objects)
	var out []string
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(base, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}

// eachObjectPlaintext calls fn with every stored object's DECOMPRESSED bytes. It is what the
// redaction tests use to prove a secret never reached objects/ in any form.
func (tp *testProject) eachObjectPlaintext(t *testing.T, fn func(rel string, plain []byte)) {
	t.Helper()
	objects := paths.Of(tp.Root).Objects
	for _, rel := range tp.objectPaths(t) {
		raw, err := os.ReadFile(paths.Long(filepath.Join(objects, filepath.FromSlash(rel))))
		require.NoError(t, err)
		plain := raw
		if strings.HasSuffix(rel, objectSuffix) {
			plain, err = Decode(raw)
			require.NoError(t, err)
		}
		fn(rel, plain)
	}
}

// indexLines splits one index/ file into its non-empty lines.
func (tp *testProject) indexLines(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(tp.Root).Index, name)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// fixture reads one of the sp06 tool-output corpus files.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpora", "toolout", "sp06", name))
	require.NoError(t, err, "SP-06 fixture missing: %s", name)
	return b
}

// osRemove deletes p through the long-path form, so a >260-character path works on Windows.
func osRemove(p string) error { return os.Remove(paths.Long(p)) }

// appendRawIndexLine appends a verbatim line (newline-terminated) to one index/ file, which is how
// the loader-robustness tests plant a deliberately malformed record.
func appendRawIndexLine(t *testing.T, tp *testProject, name, line string) {
	t.Helper()
	p := filepath.Join(paths.Of(tp.Root).Index, name)
	f, err := os.OpenFile(paths.Long(p), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}
