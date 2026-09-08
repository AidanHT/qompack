package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// The shared fixture set every test file in this package builds on.
//
// It is one file rather than one per test file so that the collaborators are constructed exactly
// once, the same way: a `recall` test and an `expand` test that each rolled their own store would
// eventually disagree about the chunk size, and a disagreement about the chunk size is invisible
// until a span assertion fails for reasons that have nothing to do with spans.
//
// Everything here uses a FakeClock. §6.1 bans wall-clock sleeps and this package stamps a time
// into every response, every ephemeral record and every promotion, so a real clock would make a
// fixture's own output unstable.

// testSession is the session almost every fixture belongs to. It is a literal rather than a
// generated ID so a failure message names something a reader can grep for.
const testSession core.SessionID = "sess-mcp-test"

// newFixtureRoot returns a project root under t.TempDir(), with .qompack/ laid out and any
// requested worktree files written.
//
// This is testutil.NewProject's job everywhere else in the tree, and it is open-coded here for the
// import-cycle reason fakeclock_test.go documents: mcp cannot import testutil once cli imports
// mcp. Only the three things this package's tests actually need are reproduced — the layout, the
// files and the frozen clock — rather than the whole Project type.
func newFixtureRoot(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)), "EnsureLayout(%s)", root)

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// Sorted so a failure names the same file on every run.
	sort.Strings(names)
	for _, name := range names {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(full)), 0o755), "mkdir for %s", name)
		require.NoError(t, os.WriteFile(paths.Long(full), []byte(files[name]), 0o600), "writing %s", name)
	}
	return root
}

// newFixtureStore opens a real store at root. It is a REAL store, not a fake, because the whole
// point of the span resolver is that it agrees with FastCDC's chunk boundaries — and a fake store
// would let the resolver be wrong in exactly the way that matters.
func newFixtureStore(t *testing.T, root string, cfg config.Config, clk core.Clock) store.Store {
	t.Helper()
	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: clk})
	require.NoError(t, err, "store.Open(%s)", root)
	// Registered here rather than left to callers: the store holds open append-only handles on
	// index/*.jsonl, and on Windows an unreleased handle fails the t.TempDir cleanup as a
	// confusing post-test error in whichever test happened to open one.
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newFixtureLedger opens a real elimination ledger at root, bound to st.
func newFixtureLedger(t *testing.T, root string, cfg config.Config, st store.Store, clk core.Clock) negknow.Ledger {
	t.Helper()
	bloom := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)
	led, err := negknow.Open(root, cfg, bloom, negknow.Deps{
		Store:   st,
		Session: testSession,
		Log:     logging.Nop(),
		Clock:   clk,
	})
	require.NoError(t, err, "negknow.Open(%s)", root)
	t.Cleanup(func() { _ = led.Close() })
	return led
}

// fixture is one fully wired tool set: the collaborators, the deps they are bound to, and a server
// with the eight tools registered.
type fixture struct {
	Root    string
	Cfg     config.Config
	Store   store.Store
	Ledger  negknow.Ledger
	Checks  *fakeCheckpoints
	Drops   *fakeDrops
	Prom    Promoter
	Widen   *fakeWidener
	Metrics obs.Registry
	Clock   *fakeClock
	Deps    ToolDeps
	Server  Server

	// huge records whether seed should also store the 400 000-byte object the response-cap tests
	// page through. It is off by default because chunking it costs a second per fixture, and only
	// a handful of tests need it.
	huge bool
}

// fixtureOpt customises a fixture before its deps are assembled.
type fixtureOpt func(*fixtureCfg)

// fixtureCfg is what the options mutate.
type fixtureCfg struct {
	files     map[string]string
	noStore   bool
	noLedger  bool
	noChecks  bool
	noDrops   bool
	noProm    bool
	noWidener bool
	huge      bool
	tweak     func(*config.Config)
	// captureRedactOff disables redaction on the STORE only, leaving the handlers' own effective
	// config (and therefore their retrieval-side Redactor) at its normal, enabled setting. It
	// exists to reproduce, deterministically, exactly what "a record captured before a redaction
	// rule existed" means: content that entered the store with a WEAKER policy than the one
	// retrieval checks against today.
	captureRedactOff bool
	// disableCkptTools sets ToolDeps.DisableWhy and DisableDropped.
	disableCkptTools bool
	// noRedactor leaves ToolDeps.Redactor nil, which is the fail-closed case: a build with no
	// retrieval-side redactor must serve no archive content at all.
	noRedactor bool
}

// testRedactor is this package's stand-in for internal/cli's retrievalRedactor: the same adapter
// over the same redact.New(cfg), rebuilt here because an in-package test may not import a
// composition root (§3.2). It is deliberately not a hand-written double — the redaction tests below
// pin PRODUCTION rules firing on production content, which only the real redactor can show.
type testRedactor struct{ r redact.Redactor }

// Redact applies the real policy and reports the rule behind each match, one entry per match.
func (tr testRedactor) Redact(in []byte) ([]byte, []string) {
	out, matches := tr.r.Redact(in)
	if len(matches) == 0 {
		return out, nil
	}
	rules := make([]string, len(matches))
	for i, m := range matches {
		rules[i] = m.Rule
	}
	return out, rules
}

// withFiles seeds the project worktree.
func withFiles(files map[string]string) fixtureOpt {
	return func(c *fixtureCfg) { c.files = files }
}

// withoutStore leaves ToolDeps.Store nil, which is how the "store unavailable" degradation of
// every content tool is exercised.
func withoutStore() fixtureOpt { return func(c *fixtureCfg) { c.noStore = true } }

// withoutLedger leaves ToolDeps.Ledger nil: available:false, never evidence of absence.
func withoutLedger() fixtureOpt { return func(c *fixtureCfg) { c.noLedger = true } }

// withoutCheckpoints leaves ToolDeps.Checkpoints nil, which is SP-10's pre-merge state.
func withoutCheckpoints() fixtureOpt { return func(c *fixtureCfg) { c.noChecks = true } }

// withoutDrops leaves ToolDeps.Rehydrator nil, which is SP-11's pre-merge state.
func withoutDrops() fixtureOpt { return func(c *fixtureCfg) { c.noDrops = true } }

// withoutPromoter leaves ToolDeps.Promoter nil.
func withoutPromoter() fixtureOpt { return func(c *fixtureCfg) { c.noProm = true } }

// withoutWidener leaves ToolDeps.Widener nil, so spans stay chunk-aligned without symbol widening.
func withoutWidener() fixtureOpt { return func(c *fixtureCfg) { c.noWidener = true } }

// withHugeObject makes seed store the 400 000-byte object the response-cap and paging tests need.
func withHugeObject() fixtureOpt { return func(c *fixtureCfg) { c.huge = true } }

// withConfig mutates the effective configuration after it is loaded.
func withConfig(fn func(*config.Config)) fixtureOpt {
	return func(c *fixtureCfg) { c.tweak = fn }
}

// withCheckpointToolsDisabled sets ToolDeps.DisableWhy and DisableDropped, exercising the explicit
// operator/build gate that lets core archive retrieval (recall, expand, re_read, already_tried,
// record_eliminated, timeline) be verified independently of checkpoint/rehydration work landing in
// the same build (SP-13 interface contract).
func withCheckpointToolsDisabled() fixtureOpt {
	return func(c *fixtureCfg) { c.disableCkptTools = true }
}

// withoutRedactor leaves ToolDeps.Redactor nil, reproducing a composition root that forgot to
// supply one. It is the ONE missing collaborator that must not degrade gracefully.
func withoutRedactor() fixtureOpt { return func(c *fixtureCfg) { c.noRedactor = true } }

// withCaptureRedactionDisabled opens the store with runtime.redact disabled while every other
// collaborator, including the handlers' own retrieval-side Redactor, keeps the fixture's normal
// (enabled) configuration. Content put through f.Store or f.put/f.record after this option is
// therefore stored exactly as an older build, or a capture predating a redaction rule, would have
// left it: in the clear, or scrubbed only by whatever rules existed then.
func withCaptureRedactionDisabled() fixtureOpt {
	return func(c *fixtureCfg) { c.captureRedactOff = true }
}

// newFixture assembles a fixture. Every collaborator is real except the three a wave-3 sibling
// owns — the checkpoint reader, the drop reporter and the symbol widener — which are fakes here
// because SP-10 and SP-11 have not merged and because a fake is the only way to drive `why` and
// `dropped` through their interesting cases deterministically.
func newFixture(t *testing.T, opts ...fixtureOpt) *fixture {
	t.Helper()

	var c fixtureCfg
	for _, o := range opts {
		o(&c)
	}

	clk := newFakeClock(epoch)
	root := newFixtureRoot(t, c.files)
	cfg := config.Defaults()
	if c.tweak != nil {
		c.tweak(&cfg)
	}

	f := &fixture{
		Root:    root,
		Cfg:     cfg,
		Checks:  &fakeCheckpoints{},
		Drops:   &fakeDrops{},
		Widen:   &fakeWidener{},
		Metrics: obs.New(clk),
		Clock:   clk,
		huge:    c.huge,
	}
	if !c.noStore {
		storeCfg := cfg
		if c.captureRedactOff {
			storeCfg.Runtime.Redact.Enabled = false
		}
		f.Store = newFixtureStore(t, root, storeCfg, clk)
	}
	if !c.noLedger {
		f.Ledger = newFixtureLedger(t, root, cfg, f.Store, clk)
	}
	if !c.noProm {
		prom, err := NewPromoter(PromotionsPath(root), cfg.Retrieval.PromoteAfterExpansions, clk)
		require.NoError(t, err, "NewPromoter")
		f.Prom = prom
	}

	f.Deps = ToolDeps{
		Cfg:         cfg,
		ProjectRoot: root,
		Clock:       clk,
		Log:         logging.Nop(),
		Metrics:     f.Metrics,
		Store:       f.Store,
		Ledger:      f.Ledger,
		Promoter:    f.Prom,
	}
	if !c.noChecks {
		f.Deps.Checkpoints = f.Checks
	}
	if !c.noDrops {
		f.Deps.Rehydrator = f.Drops
	}
	if !c.noWidener {
		f.Deps.Widener = f.Widen
	}
	if c.disableCkptTools {
		f.Deps.DisableWhy = true
		f.Deps.DisableDropped = true
	}
	if !c.noRedactor {
		f.Deps.Redactor = testRedactor{r: redact.New(cfg)}
	}

	f.Server = NewServerWithOptions(ServerOptions{
		Name: ServerName, Version: core.Version, Log: logging.Nop(),
	})
	require.NoError(t, RegisterAll(f.Server, f.Deps), "RegisterAll")
	return f
}

// call dispatches one tool call and returns the raw Response.
//
// It goes through Dispatch rather than calling a handler directly, so every test exercises the
// same path a real `tools/call` takes — including the schema validation, the B-F timing and the
// ephemeral tagging that live in the run() preamble rather than in the handler bodies.
func (f *fixture) call(t *testing.T, name string, args map[string]any) Response {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err, "marshalling arguments for %s", name)

	resp, err := Dispatch(context.Background(), f.Server, Request{
		Session:  testSession,
		Name:     name,
		Args:     raw,
		Turn:     1,
		Deadline: f.Clock.Now().Add(time.Minute),
	})
	require.NoError(t, err, "Dispatch(%s)", name)
	return resp
}

// callOK dispatches one tool call, asserts it is not a tool error, and decodes the single text
// content block as JSON into v.
func (f *fixture) callOK(t *testing.T, name string, args map[string]any, v any) Response {
	t.Helper()
	resp := f.call(t, name, args)
	require.False(t, resp.IsError, "%s reported a tool error: %s", name, responseText(resp))
	if v != nil {
		require.NoError(t, json.Unmarshal([]byte(responseText(resp)), v),
			"%s returned a body that is not JSON: %s", name, responseText(resp))
	}
	return resp
}

// callErr dispatches one tool call and asserts it IS a tool error, returning the message.
//
// A tool error is a RESULT, never a JSON-RPC error: the transport worked and the tool answered,
// and the model reads the answer. Asserting that distinction is worth a helper because getting it
// backwards is the single most likely way to break the §5.16 contract without noticing.
func (f *fixture) callErr(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	resp := f.call(t, name, args)
	require.True(t, resp.IsError, "%s was expected to report a tool error, got: %s", name, responseText(resp))
	return responseText(resp)
}

// responseText concatenates a Response's text content blocks.
func responseText(r Response) string {
	var out string
	for _, c := range r.Content {
		out += c.Text
	}
	return out
}

// put stores b under path and returns the resulting root hash, as a §8.2 file version.
func (f *fixture) put(t *testing.T, path, body string) core.Hash {
	t.Helper()
	res, err := f.Store.PutBytes(context.Background(), []byte(body), store.PutOptions{Path: path})
	require.NoError(t, err, "PutBytes(%s)", path)
	require.NoError(t, f.Store.AppendFileVersion(context.Background(), path, store.FileVersion{
		TS:    core.NowMilli(f.Clock),
		Root:  res.Root.Hash,
		Turn:  1,
		Bytes: int64(len(body)),
	}), "AppendFileVersion(%s)", path)
	return res.Root.Hash
}

// record stores b under path AND indexes it as one tool use, returning the record's id.
//
// The two halves are separate on purpose, because the store treats them separately: put alone
// makes content addressable and gives `re_read` a version to resolve, while it is the TOOL-USE
// index that `recall` searches (store.FSStore.candidates walks s.toolUse, not the root index). A
// fixture that only called put would make every `recall` assertion vacuously pass on an empty
// result set — which is exactly the shape of test that looks green and proves nothing.
func (f *fixture) record(t *testing.T, tool, path, body string, turn core.TurnIndex) core.ToolUseID {
	t.Helper()
	_, id := f.store(t, tool, path, body, turn, false)
	return id
}

// putAndRecord does both halves from ONE PutBytes.
//
// Calling put and then record would chunk, canonicalize and MinHash the same bytes twice, which on
// the seeded corpus's 200 KB source is most of a second of pure waste per file. The content is
// identical either way — the store deduplicates it — but the CPU is not, and a fixture that costs
// a second is a fixture tests avoid using.
func (f *fixture) putAndRecord(t *testing.T, tool, path, body string, turn core.TurnIndex) (core.Hash, core.ToolUseID) {
	t.Helper()
	return f.store(t, tool, path, body, turn, true)
}

// store is the shared body: one PutBytes, then a tool-use record and optionally a file version.
func (f *fixture) store(t *testing.T, tool, path, body string, turn core.TurnIndex, version bool,
) (core.Hash, core.ToolUseID) {
	t.Helper()

	res, err := f.Store.PutBytes(context.Background(), []byte(body), store.PutOptions{Tool: tool, Path: path})
	require.NoError(t, err, "PutBytes(%s)", path)

	// A pathless capture (a Bash output) has no file version to append: §8.2's history is keyed by
	// path, and the store refuses an empty one.
	if version && path != "" {
		require.NoError(t, f.Store.AppendFileVersion(context.Background(), path, store.FileVersion{
			TS:    core.NowMilli(f.Clock),
			Root:  res.Root.Hash,
			Turn:  turn,
			Bytes: int64(len(body)),
		}), "AppendFileVersion(%s)", path)
	}

	id := core.ToolUseID(fmt.Sprintf("tu-%s-%d", strings.ToLower(tool), turn))
	require.NoError(t, f.Store.RecordToolUse(context.Background(), store.ToolUseRecord{
		ID:          id,
		Session:     testSession,
		Turn:        turn,
		TS:          core.NowMilli(f.Clock),
		Tool:        tool,
		ArgsPreview: path,
		Root:        res.Root.Hash,
		Path:        path,
		Bytes:       int64(len(body)),
	}), "RecordToolUse(%s)", id)
	return res.Root.Hash, id
}

// fakeCheckpoints is a checkpoint.Reader a test can load with exactly the chain it wants.
//
// It is a fake rather than the real reader because SP-10 has not merged: checkpoint.OpenReader
// still reports core.ErrNotImplemented, and `why` has to be tested against real chain shapes
// before it can be tested against a real chain.
type fakeCheckpoints struct {
	// Chained is the chain Chain returns, newest first.
	Chained []checkpoint.Checkpoint
	// Refs is what List returns.
	Refs []checkpoint.Ref
	// Err, when non-nil, is returned from every method: the "checkpoints unreadable" path.
	Err error
}

// Latest returns the newest checkpoint in the loaded chain.
func (f *fakeCheckpoints) Latest(_ context.Context, _ core.SessionID) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	if f.Err != nil {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, f.Err
	}
	if len(f.Chained) == 0 {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotFound
	}
	return f.Chained[0], checkpoint.Ref{Seq: f.Chained[0].Seq}, nil
}

// Get returns the checkpoint with the given sequence number.
func (f *fakeCheckpoints) Get(_ context.Context, seq core.CheckpointSeq) (checkpoint.Checkpoint, checkpoint.Ref, error) {
	if f.Err != nil {
		return checkpoint.Checkpoint{}, checkpoint.Ref{}, f.Err
	}
	for _, c := range f.Chained {
		if c.Seq == seq {
			return c, checkpoint.Ref{Seq: seq}, nil
		}
	}
	return checkpoint.Checkpoint{}, checkpoint.Ref{}, core.ErrNotFound
}

// List returns the loaded refs.
func (f *fakeCheckpoints) List(_ context.Context) ([]checkpoint.Ref, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Refs, nil
}

// Chain returns seq and every ancestor, which for this fake is the whole loaded chain from seq
// downwards.
func (f *fakeCheckpoints) Chain(_ context.Context, seq core.CheckpointSeq) ([]checkpoint.Checkpoint, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	var out []checkpoint.Checkpoint
	for _, c := range f.Chained {
		if c.Seq <= seq {
			out = append(out, c)
		}
	}
	return out, nil
}

// Verify reports no mismatches.
func (f *fakeCheckpoints) Verify(_ context.Context) ([]core.CheckpointSeq, error) {
	return nil, f.Err
}

// fakeCheckpoints must satisfy the interface ToolDeps declares.
var _ checkpoint.Reader = (*fakeCheckpoints)(nil)

// fakeDrops is a DropReporter a test loads with the drop report it wants `dropped` to render.
type fakeDrops struct {
	// Entries is what CurrentDrops returns.
	Entries []checkpoint.DropEntry
	// Err, when non-nil, is returned instead: the "rehydrator unavailable" path.
	Err error
	// Calls counts invocations, so a test can assert the tool consulted the reporter at all.
	Calls int
}

// CurrentDrops returns the loaded entries.
func (f *fakeDrops) CurrentDrops(_ context.Context, _ core.SessionID) ([]checkpoint.DropEntry, error) {
	f.Calls++
	if f.Err != nil {
		return nil, f.Err
	}
	return f.Entries, nil
}

// fakeDrops must satisfy the interface ToolDeps declares.
var _ DropReporter = (*fakeDrops)(nil)

// fakeWidener is a Widener that finds brace-delimited symbols by scanning, with per-test overrides.
//
// It is a fake rather than a real symbols.Extractor because §3.2 forbids this package importing
// symbols at all — the production adapter lives in internal/cli. It is a SCANNING fake rather than
// a canned-answer one because the span tests are about what the resolver does with a widener's
// answer, and a widener that always says "no" would leave every widening path untested. The scan
// understands exactly one thing: `func name(` / `function name(` followed by a brace-balanced body,
// which is all the seeded corpus contains.
type fakeWidener struct {
	// WidenTo, when non-nil, overrides the scan: it is the end offset Widen reports.
	WidenTo *int64
	// FindSpan, when non-nil, overrides the scan: it is the [start,end) Find reports.
	FindSpan *[2]int64
	// Refuse makes both methods report "nothing found", which is the nil-symbol-table path.
	Refuse bool
	// WidenCalls and FindCalls count invocations.
	WidenCalls, FindCalls int
}

// Widen grows end to the end of the symbol enclosing end-1.
func (f *fakeWidener) Widen(_ string, b []byte, off, end int64) (int64, int64, bool) {
	f.WidenCalls++
	if f.Refuse {
		return off, end, false
	}
	if f.WidenTo != nil {
		if *f.WidenTo <= end {
			return off, end, false
		}
		return off, *f.WidenTo, true
	}
	if end <= 0 || end > int64(len(b)) {
		return off, end, false
	}
	_, symEnd, ok := enclosingSymbol(b, end-1)
	if !ok || symEnd <= end {
		return off, end, false
	}
	return off, symEnd, true
}

// Find reports the [start,end) of the named symbol.
func (f *fakeWidener) Find(_ string, b []byte, name string) (int64, int64, bool) {
	f.FindCalls++
	if f.Refuse || name == "" {
		return 0, 0, false
	}
	if f.FindSpan != nil {
		return f.FindSpan[0], f.FindSpan[1], true
	}
	return findSymbol(b, name)
}

// fakeWidener must satisfy the interface ToolDeps declares.
var _ Widener = (*fakeWidener)(nil)

// findSymbol locates `func <name>(` or `function <name>(` and returns its brace-balanced extent.
func findSymbol(b []byte, name string) (int64, int64, bool) {
	for _, kw := range []string{"function " + name + "(", "func " + name + "("} {
		i := bytes.Index(b, []byte(kw))
		if i < 0 {
			continue
		}
		start := int64(i)
		// A leading `export ` belongs to the declaration, so include it.
		if e := int64(len("export ")); start >= e && bytes.Equal(b[start-e:start], []byte("export ")) {
			start -= e
		}
		end, ok := braceEnd(b, int64(i))
		if !ok {
			return 0, 0, false
		}
		return start, end, true
	}
	return 0, 0, false
}

// enclosingSymbol returns the extent of the symbol containing at, if any.
func enclosingSymbol(b []byte, at int64) (int64, int64, bool) {
	best := int64(-1)
	var bestEnd int64
	for _, kw := range [][]byte{[]byte("function "), []byte("func ")} {
		for i := 0; ; {
			j := bytes.Index(b[i:], kw)
			if j < 0 {
				break
			}
			start := int64(i + j)
			i = int(start) + len(kw)
			end, ok := braceEnd(b, start)
			if !ok || start > at || end <= at {
				continue
			}
			if start > best {
				best, bestEnd = start, end
			}
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestEnd, true
}

// braceEnd returns the offset just past the closing brace of the block opened after from.
func braceEnd(b []byte, from int64) (int64, bool) {
	open := int64(-1)
	depth := 0
	for i := from; i < int64(len(b)); i++ {
		switch b[i] {
		case '{':
			if open < 0 {
				open = i
			}
			depth++
		case '}':
			if open < 0 {
				continue
			}
			depth--
			if depth == 0 {
				// Include the newline that terminates the closing brace's line, so a widened span
				// ends on a line boundary the way a reader expects.
				if i+1 < int64(len(b)) && b[i+1] == '\n' {
					return i + 2, true
				}
				return i + 1, true
			}
		}
	}
	return 0, false
}

// int64p is a one-line helper for the pointer fields above.
func int64p(v int64) *int64 { return &v }
