package store

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// TestOpenReadOnly_CreatesNothing is the whole reason readonly.go exists (SP-17 ruling R5-A).
//
// `Open` on a bare directory manufactures the .qompack tree and five empty index files, so a
// diagnostic that used it would answer a question by changing the thing it asked about. This asserts
// the read-only opener answers the same question and leaves the tree byte-identical — including on a
// directory that has never been used with Qompack at all, where the correct outcome is an empty
// store and NOT a new project.
func TestOpenReadOnly_CreatesNothing(t *testing.T) {
	t.Parallel()

	t.Run("a directory that has never been used with Qompack", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		before := treeSnapshot(t, dir)

		s, err := OpenReadOnly(dir, config.Defaults(), Deps{})
		require.NoError(t, err)
		st, err := s.Stats(context.Background())
		require.NoError(t, err)
		require.Zero(t, st.Objects)
		require.NoError(t, s.Close())

		require.Equal(t, before, treeSnapshot(t, dir),
			"OpenReadOnly must not bring a project into existence")
		require.Empty(t, treeSnapshot(t, dir), "the directory is still empty")
	})

	t.Run("a bare .qompack directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".qompack"), 0o700))
		before := treeSnapshot(t, dir)

		s, err := OpenReadOnly(dir, config.Defaults(), Deps{})
		require.NoError(t, err)
		_, err = s.Stats(context.Background())
		require.NoError(t, err)
		require.NoError(t, s.Close())

		require.Equal(t, before, treeSnapshot(t, dir),
			"a bare .qompack must not gain index files, a .gitignore or tmp/quarantine")
	})
}

// TestOpenReadOnly_RejectsAnObjectWithoutMovingIt is the other half of R5-A: the READ path's own
// write.
//
// getObject quarantines what it rejects — that is §12.3's behaviour and it is right for the product
// — but it makes verifying an object relocate it, so an integrity scan would change the evidence it
// was reporting on. A read-only store must refuse the bytes and leave the file exactly where it is.
func TestOpenReadOnly_RejectsAnObjectWithoutMovingIt(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, []byte("package main\n"), PutOptions{Tool: "Read", Path: "a.go"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	chunk := res.Root.Chunks[0].Hash
	p := readOnlyObjectPath(t, tp.Root, chunk)

	// A VALID zstd frame under the wrong name: it decodes, and only the content address catches it.
	encoded, err := Encode([]byte("bytes that are not what this address names\n"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(p), encoded, 0o600))

	before := treeSnapshot(t, tp.Root)
	s, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	_, err = s.GetChunk(ctx, chunk)
	require.Error(t, err, "a rejected object is still refused")
	require.ErrorIs(t, err, core.ErrNotFound)
	require.NoError(t, s.Close())

	require.FileExists(t, paths.Long(p), "the rejected object stays where it was")
	require.Equal(t, before, treeSnapshot(t, tp.Root),
		"nothing was moved into tmp/quarantine by a read")
}

// readOnlyObjectPath returns the on-disk path backing h, trying both spellings objectCandidates does.
func readOnlyObjectPath(t *testing.T, root string, h core.Hash) string {
	t.Helper()

	hx := h.String()[len("sha256:"):]
	dir := filepath.Join(paths.Of(root).Objects, hx[0:2], hx[2:4])
	for _, name := range []string{hx + objectSuffix, hx} {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(paths.Long(p)); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	t.Fatalf("no object file for %s under %s", h.Short(), dir)
	return ""
}

// treeSnapshot lists every path under dir with its size, so a "created nothing" claim is a
// comparison rather than an assertion about the files somebody happened to think of.
func treeSnapshot(t *testing.T, dir string) []string {
	t.Helper()

	var out []string
	require.NoError(t, filepath.WalkDir(paths.Long(dir), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(paths.Long(dir), p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		entry := filepath.ToSlash(rel)
		if !d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			entry += "|" + strconv.FormatInt(info.Size(), 10)
		}
		out = append(out, entry)
		return nil
	}))
	sort.Strings(out)
	return out
}

// TestOpenReadOnly_LoadsARealStoreUnchanged reads a populated store through the read-only opener.
//
// It is a separate, NON-parallel test rather than a subtest of the one above: newTestStore calls
// t.Setenv, which Go forbids under a parallel parent.
func TestOpenReadOnly_LoadsARealStoreUnchanged(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, []byte("package main\n"), PutOptions{Tool: "Read", Path: "a.go"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	before := treeSnapshot(t, tp.Root)
	s, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	got, err := s.GetRoot(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.Equal(t, res.Root.Hash, got.Hash)
	_, fidelity, err := s.RestoreOriginal(ctx, res.Root.Hash)
	require.NoError(t, err)
	require.NotEqual(t, FidelityCorrupt, fidelity)
	require.NoError(t, s.Close())

	require.Equal(t, before, treeSnapshot(t, tp.Root),
		"reading a real store through the read-only opener changes nothing")
}

// TestOpenReadOnly_TheValueIsNotAStore is the compile-and-run half of the read-only refusal
// (SP-17 fix round 2, finding N-1).
//
// The first version of readOnlyStore EMBEDDED *FSStore. An embedded pointer promotes every method,
// so the returned value still satisfied store.Store, and one type assertion handed a caller the
// whole mutating half — measured through it, GC deleted an object and PutBytes created five
// directories and two files before panicking on the nil roots writer. The value now holds its store
// in an unexported field and delegates the read half explicitly, so the assertion must FAIL.
func TestOpenReadOnly_TheValueIsNotAStore(t *testing.T) {
	t.Parallel()

	s, err := OpenReadOnly(t.TempDir(), config.Defaults(), Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	_, isStore := s.(Store)
	require.False(t, isStore,
		"a read-only value must not satisfy store.Store: the assertion is the whole attack")
	_, isRefCounter := s.(RefCounter)
	require.False(t, isRefCounter, "nor any other seam whose methods it does not honour")
	_, isCloser := s.(interface {
		PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
	})
	require.False(t, isCloser, "and no single mutating method is reachable on its own either")
}

// TestOpenReadOnly_EveryMutatingMethodRefuses is the second half of N-1: the store BEHIND the
// value refuses too.
//
// The narrow interface is what a caller sees; mutate() is what the store enforces. This reaches
// past the interface to the *FSStore the value holds — the position anything inside this package
// is already in — calls every mutating method on it, and requires ErrReadOnly from each plus a
// byte-identical tree afterwards. A guard that returned an error while the method had already
// created a directory would pass the first assertion and fail the second.
func TestOpenReadOnly_EveryMutatingMethodRefuses(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	res, err := tp.Store.PutBytes(ctx, []byte("package main\n"), PutOptions{Tool: "Read", Path: "a.go"})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	before := treeSnapshot(t, tp.Root)
	opened, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	ro, ok := opened.(readOnlyStore)
	require.True(t, ok, "the returned value is this package's readOnlyStore")
	s := ro.fs

	for _, op := range readOnlyRefusals(res.Root.Hash) {
		t.Run(op.name, func(t *testing.T) {
			require.ErrorIs(t, op.call(ctx, s), ErrReadOnly,
				"%s must refuse a read-only store with the sentinel", op.name)
			require.Equal(t, before, treeSnapshot(t, tp.Root),
				"%s must leave the tree byte-identical", op.name)
		})
	}

	require.NoError(t, opened.Close())
	require.Equal(t, before, treeSnapshot(t, tp.Root), "and Close writes nothing either")
}

// readOnlyRefusals is every mutating method on *FSStore, in the shape the sweep above calls.
//
// It is deliberately the same enumeration mutatingOps (degraded_test.go) makes for the CLOSED-store
// contract, minus the pure reads: a method that appears there and writes must appear here, and the
// way this list goes stale is a new writer that nobody adds to it.
// TestReadOnly_EveryExportedMethodIsClassified below pins it to the source: it requires this list
// to be exactly the set of methods calling mutate(), AND requires every exported *FSStore method to
// be delegated, swept here, or justified as a read — so a writer that never takes the guard has
// nowhere to hide either.
func readOnlyRefusals(seeded core.Hash) []storeOp {
	return []storeOp{
		{"Put", func(ctx context.Context, s *FSStore) error {
			_, err := s.Put(ctx, bytes.NewReader([]byte("payload")), PutOptions{Tool: "Bash"})
			return err
		}},
		{"PutBytes", func(ctx context.Context, s *FSStore) error {
			_, err := s.PutBytes(ctx, []byte("payload"), PutOptions{Tool: "Bash", Path: "b.go"})
			return err
		}},
		{"Flush", func(ctx context.Context, s *FSStore) error { return s.Flush(ctx) }},
		{"GC", func(ctx context.Context, s *FSStore) error {
			_, err := s.GC(ctx, GCPolicy{})
			return err
		}},
		{"RecordToolUse", func(ctx context.Context, s *FSStore) error {
			return s.RecordToolUse(ctx, ToolUseRecord{
				ID: "toolu_01READONLYAAAAAAAAAAAAA", Session: "sess-readonly", Turn: 1,
				TS: 1, Tool: "Bash", Root: seeded,
			})
		}},
		{"RecordToolUseSuperseding", func(ctx context.Context, s *FSStore) error {
			_, _, err := s.RecordToolUseSuperseding(ctx, ToolUseRecord{
				ID: "toolu_01READONLYBBBBBBBBBBBBB", Session: "sess-readonly", Turn: 2,
				TS: 2, Tool: "Bash", Root: seeded,
			}, nil)
			return err
		}},
		{"MarkSuperseded", func(ctx context.Context, s *FSStore) error {
			return s.MarkSuperseded(ctx, "toolu_01READONLYAAAAAAAAAAAAA", "toolu_01READONLYBBBBBBBBBBBBB")
		}},
		{"AppendFileVersion", func(ctx context.Context, s *FSStore) error {
			return s.AppendFileVersion(ctx, "src/readonly.ts", FileVersion{
				TS: 1, Root: seeded, Turn: 1, Bytes: 4,
			})
		}},
		// R5-2's exported quarantine. It MOVES a file, so a read-only store must refuse it like any
		// other writer — which is the property `qompack fsck`'s fidelity pass depends on: it opens
		// read-only precisely so that asking about a damaged root cannot relocate it.
		{"Quarantine", func(_ context.Context, s *FSStore) error {
			return s.Quarantine(seeded, "readonly refusal probe")
		}},
	}
}

// TestReadOnly_EveryExportedMethodIsClassified pins the enumeration above to the source, and pins
// it by EXPORTED SURFACE rather than by the guard.
//
// The first version compared {methods calling mutate()} with {methods readOnlyRefusals sweeps},
// which is a tautology for the one mistake that matters: a new writer that never calls mutate() —
// a Prune that opens with s.use() and calls os.Remove — is absent from BOTH sets, so the sweep
// stays green. This parses the package's non-test files and partitions every exported *FSStore
// method into exactly three buckets: delegated by readOnlyStore (reachable through the value),
// swept by readOnlyRefusals (refuses with ErrReadOnly), or named in readOnlyReadsAllowlist below.
// A method in none of them fails, which is the only arrangement a new writer cannot slip past.
//
// The delegated set is itself parsed off readOnlyStore's own methods, so adding a delegation does
// not need this test edited. The allowlist is deliberately hand-written, because "this one is a
// read" is a judgement that should cost a reviewer's attention rather than being inferred.
func TestReadOnly_EveryExportedMethodIsClassified(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	exported, guarded, delegated := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, isFunc := decl.(*ast.FuncDecl)
				if !isFunc || fn.Recv == nil {
					continue
				}
				switch {
				case receiverIs(fn, "FSStore"):
					if fn.Name.IsExported() {
						exported[fn.Name.Name] = true
					}
					if fn.Body != nil && bodyCallsMutate(fn.Body) {
						guarded[fn.Name.Name] = true
					}
				case receiverIs(fn, "readOnlyStore"):
					delegated[fn.Name.Name] = true
				}
			}
		}
	}
	require.NotEmpty(t, exported, "the parse found no exported method at all, so it is not working")
	require.NotEmpty(t, delegated, "nor any delegating method")

	swept := map[string]bool{}
	for _, op := range readOnlyRefusals(core.Hash{}) {
		swept[op.name] = true
	}
	require.Equal(t, swept, guarded,
		"every method calling mutate() must be swept by readOnlyRefusals, and vice versa")

	var unclassified []string
	for name := range exported {
		if delegated[name] || swept[name] || readOnlyReadsAllowlist[name] != "" {
			continue
		}
		unclassified = append(unclassified, name)
	}
	sort.Strings(unclassified)
	require.Empty(t, unclassified,
		"every exported *FSStore method must be delegated, swept as a writer, or justified in "+
			"readOnlyReadsAllowlist; these are in none of the three: %v", unclassified)

	// The allowlist may not rot in the other direction either: a name it justifies that no longer
	// exists, or that has since become delegated or guarded, is a stale justification.
	for name := range readOnlyReadsAllowlist {
		require.True(t, exported[name], "readOnlyReadsAllowlist names %s, which is not an exported "+
			"*FSStore method any more", name)
		require.False(t, swept[name], "%s is swept as a writer; remove its reads justification", name)
		require.False(t, delegated[name], "%s is delegated; remove its reads justification", name)
	}
}

// readOnlyReadsAllowlist is every exported *FSStore method that is neither delegated by
// readOnlyStore nor a writer — with the reason it is safe on a read-only store, one line each.
//
// It is a reviewed list, not a generated one. Adding a name here is an explicit claim that the
// method writes nothing, and the test above makes that claim mandatory: a new method that is not
// delegated, not swept and not listed fails, whether or not it took the mutate() guard.
var readOnlyReadsAllowlist = map[string]string{
	"Has": "a map lookup under an RLock, with a stat only when the index says no; it opens " +
		"and writes nothing",
	"ObjectOnDisk":   "one stat per candidate object spelling; it opens, decodes and writes nothing",
	"Open":           "returns a reader over already-loaded chunk refs; objects are read, never written",
	"OpenSpan":       "Open's bounded form, over the same read path",
	"Search":         "materializes candidates through the same object reads Open uses",
	"FileHistory":    "reads the in-memory per-path version list",
	"FileAt":         "the same list, resolved at a timestamp",
	"ChangedSince":   "compares caller-supplied deps against the loaded file index",
	"ToolUse":        "an in-memory tool_use lookup",
	"ToolUsesByPath": "the same index, by path",
	"RecentSessions": "reads the loaded session index",
	"ApproxRefs":     "reads the in-memory refcount map",
	"Segments": "returns the segment log, which carries its own refusal in segLog.append " +
		"(TestOpenReadOnly_TheSegmentLogRefusesWrites)",
}

// receiverIs reports whether fn is a method on want, by pointer or by value.
func receiverIs(fn *ast.FuncDecl, want string) bool {
	if len(fn.Recv.List) != 1 {
		return false
	}
	expr := fn.Recv.List[0].Type
	if star, isStar := expr.(*ast.StarExpr); isStar {
		expr = star.X
	}
	name, isIdent := expr.(*ast.Ident)
	return isIdent && name.Name == want
}

// bodyCallsMutate reports whether body contains a call to the receiver's mutate method.
func bodyCallsMutate(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if isSel && sel.Sel.Name == "mutate" {
			found = true
		}
		return !found
	})
	return found
}

// TestOpenReadOnly_LoadsTheSegmentLog is fix round 3's finding 1.
//
// The read-only store did not load index/segments.jsonl, because openSegLog opens the file O_CREATE
// and asking a project a question may not create one. The first version left s.seg nil and the
// second installed a degraded log — and BOTH reported a confident zero, because segmentCount reads
// len(byID) and never consults degraded. `qompack doctor` printed "0 segment(s)" with status ok for
// a project whose log held three open records.
//
// The log is now replayed by an opener that creates nothing: reads answer, writes refuse.
func TestOpenReadOnly_LoadsTheSegmentLog(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	for turn := 0; turn < 3; turn++ {
		id, err := tp.Store.Segments().Open(ctx, Segment{Session: "sess-ro", StartTurn: core.TurnIndex(turn)})
		require.NoError(t, err)
		require.NoError(t, tp.Store.Segments().Close(ctx, id, core.TurnIndex(turn), map[string]float64{"tokens": 1}))
	}
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	before := treeSnapshot(t, tp.Root)
	s, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })

	st, err := s.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, st.Segments,
		"the read-only store reports the segments the project actually holds, never a confident zero")
	require.Equal(t, before, treeSnapshot(t, tp.Root), "and loading the log created nothing")
}

// TestOpenReadOnly_TheSegmentLogRefusesWrites is the other half of finding 1: the log a diagnostic
// can READ must still be one it cannot write.
//
// The refusal lives in segLog.writable, consulted by every public writer BEFORE it validates
// anything, and again in segLog.append, the single choke point every record passes through — so
// Open, Close, MarkEncoded and PublishFilter all refuse and a writer added later cannot miss the
// guard. The empty-ref case pins the ORDER: a read-only log refuses the call rather than reporting
// what would have been wrong with its argument. Each writer appends before it touches memory, so a
// refusal leaves the in-memory log unchanged too — which is what the re-read at the end checks.
//
// The tail is the post-Close contract of fsstore.go's Segments: after Close every one of the log's
// methods reports core.ErrDegraded, so Range may not keep answering from memory.
func TestOpenReadOnly_TheSegmentLogRefusesWrites(t *testing.T) {
	// Not parallel: newTestStore calls t.Setenv.
	tp := newTestStore(t)
	ctx := context.Background()
	id, err := tp.Store.Segments().Open(ctx, Segment{Session: "sess-ro-w", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))
	require.NoError(t, tp.Store.Close())

	before := treeSnapshot(t, tp.Root)
	s, err := OpenReadOnly(tp.Root, config.Defaults(), Deps{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	ros, ok := s.(readOnlyStore)
	require.True(t, ok)
	log := ros.fs.seg // the concrete log: PublishFilter is not on the §5.8 seam

	_, openErr := log.Open(ctx, Segment{Session: "sess-ro-w", StartTurn: 9})
	require.ErrorIs(t, openErr, ErrReadOnly, "Segments().Open is a write")
	require.ErrorIs(t, log.Close(ctx, id, 1, map[string]float64{"tokens": 1}), ErrReadOnly)
	require.ErrorIs(t, log.MarkEncoded(ctx, []core.SegmentID{id}, 1), ErrReadOnly)
	require.ErrorIs(t, log.PublishFilter(ctx, id, "segments/filters/0001.bin"), ErrReadOnly)
	require.ErrorIs(t, log.PublishFilter(ctx, id, ""), ErrReadOnly,
		"a read-only log refuses the call before it judges the reference")

	// The reads still answer — a refusing log is not a degraded one.
	seg, err := log.Get(ctx, id)
	require.NoError(t, err)
	require.False(t, seg.Closed, "the refused Close left the in-memory segment open")
	require.Equal(t, before, treeSnapshot(t, tp.Root), "and nothing reached the disk")

	// ... until Close, which degrades it: fsstore.go's Segments contract holds here too.
	require.NoError(t, s.Close())
	_, rangeErr := log.Range(ctx, 0, 9)
	require.ErrorIs(t, rangeErr, core.ErrDegraded,
		"Segments().Range after Close reports the degradation instead of answering from memory")
	require.Equal(t, before, treeSnapshot(t, tp.Root), "and closing wrote nothing either")
}
