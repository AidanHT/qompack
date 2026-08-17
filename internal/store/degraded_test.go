package store

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
)

// This file pins two contracts that every entry point shares, and that nothing else tests as a
// whole: what the store does after Close, and what it does with a context that is already done.
//
// Both matter to the daemon rather than to a library caller. §5.8 has Segments() keep returning a
// non-nil log after Close precisely so shutdown does not have to be ordered — callers race with it
// and must get a clean core.ErrDegraded instead of a nil dereference. And SP-05's hot path runs
// every store call under a deadline: a method that ignores its context turns a 200 ms budget into
// however long a 64 MiB Put takes, and the budget stops meaning anything.
//
// Testing these one method at a time is how the coverage ends up uneven, so they are swept.

// storeOp is one entry point, reduced to something a sweep can call.
type storeOp struct {
	name string
	call func(ctx context.Context, s *FSStore) error
}

// mutatingOps are the entry points that write, walk the filesystem, or decompress — everything that
// can block long enough for a cancelled context to be the caller's answer.
func mutatingOps(seeded core.Hash, chunk core.Hash) []storeOp {
	return []storeOp{
		{"Put", func(ctx context.Context, s *FSStore) error {
			_, err := s.Put(ctx, bytes.NewReader([]byte("payload")), PutOptions{Tool: "Bash"})
			return err
		}},
		{"PutBytes", func(ctx context.Context, s *FSStore) error {
			_, err := s.PutBytes(ctx, []byte("payload"), PutOptions{Tool: "Bash"})
			return err
		}},
		{"GetChunk", func(ctx context.Context, s *FSStore) error {
			_, err := s.GetChunk(ctx, chunk)
			return err
		}},
		{"Open", func(ctx context.Context, s *FSStore) error {
			rc, err := s.Open(ctx, seeded)
			if rc != nil {
				_ = rc.Close()
			}
			return err
		}},
		{"OpenSpan", func(ctx context.Context, s *FSStore) error {
			rc, err := s.OpenSpan(ctx, seeded, 0, 4)
			if rc != nil {
				_ = rc.Close()
			}
			return err
		}},
		{"RecordToolUse", func(ctx context.Context, s *FSStore) error {
			return s.RecordToolUse(ctx, ToolUseRecord{
				ID: "toolu_01DEGRADEDAAAAAAAAAAAAAA", Session: "sess-degraded", Turn: 1,
				TS: 1, Tool: "Bash", Root: seeded,
			})
		}},
		{"MarkSuperseded", func(ctx context.Context, s *FSStore) error {
			return s.MarkSuperseded(ctx, "toolu_01DEGRADEDAAAAAAAAAAAAAA", "toolu_01DEGRADEDBBBBBBBBBBBBBB")
		}},
		{"AppendFileVersion", func(ctx context.Context, s *FSStore) error {
			return s.AppendFileVersion(ctx, "src/degraded.ts", FileVersion{TS: 1, Root: seeded, Turn: 1, Bytes: 4})
		}},
		{"Search", func(ctx context.Context, s *FSStore) error {
			_, err := s.Search(ctx, Query{Text: "payload", K: 5})
			return err
		}},
		{"Stats", func(ctx context.Context, s *FSStore) error {
			_, err := s.Stats(ctx)
			return err
		}},
		{"GC", func(ctx context.Context, s *FSStore) error {
			_, err := s.GC(ctx, GCPolicy{RetainDays: 30, RetainSessions: 10})
			return err
		}},
		{"Flush", func(ctx context.Context, s *FSStore) error { return s.Flush(ctx) }},
		{"Segments.Open", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Open(ctx, Segment{Session: "sess-degraded", StartTurn: 0})
			return err
		}},
		{"Segments.Close", func(ctx context.Context, s *FSStore) error {
			return s.Segments().Close(ctx, 1, 4, map[string]float64{"tokens": 1})
		}},
		{"Segments.MarkEncoded", func(ctx context.Context, s *FSStore) error {
			return s.Segments().MarkEncoded(ctx, []core.SegmentID{1}, 1)
		}},
	}
}

// lookupOps are the pure in-memory reads. They are swept for the closed-store contract but NOT for
// cancellation: they are a map lookup under an RLock, so a context check would cost more than the
// work it guards and would let a caller's cancelled context turn an answer the store already holds
// into an error. The split is deliberate and is asserted, not assumed — see
// TestContract_InMemoryLookupsIgnoreCancellation.
func lookupOps(seeded core.Hash) []storeOp {
	return []storeOp{
		{"GetRoot", func(ctx context.Context, s *FSStore) error {
			_, err := s.GetRoot(ctx, seeded)
			return err
		}},
		{"ToolUse", func(ctx context.Context, s *FSStore) error {
			_, err := s.ToolUse(ctx, "toolu_01DEGRADEDAAAAAAAAAAAAAA")
			return err
		}},
		{"ToolUsesByPath", func(ctx context.Context, s *FSStore) error {
			_, err := s.ToolUsesByPath(ctx, "src/degraded.ts", 10)
			return err
		}},
		{"FileHistory", func(ctx context.Context, s *FSStore) error {
			_, err := s.FileHistory(ctx, "src/degraded.ts")
			return err
		}},
		{"FileAt", func(ctx context.Context, s *FSStore) error {
			_, err := s.FileAt(ctx, "src/degraded.ts", time.Time{})
			return err
		}},
		// ChangedSince is here rather than above because it is a bounded run of map lookups over the
		// deps the caller already handed it — no I/O, no decompression. §8.3 staleness is checked on
		// the way OUT of a budget overrun, so making it refuse a cancelled context would deny the one
		// answer a caller needs precisely when it is shutting down.
		{"ChangedSince", func(ctx context.Context, s *FSStore) error {
			_, err := s.ChangedSince(ctx, []core.Dep{{Path: "src/degraded.ts", Hash: seeded}})
			return err
		}},
		{"Segments.Get", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Get(ctx, 1)
			return err
		}},
		{"Segments.Range", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Range(ctx, 0, 100)
			return err
		}},
		{"Segments.Current", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Current(ctx, "sess-degraded")
			return err
		}},
		{"Segments.Frontier", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Frontier(ctx, "sess-degraded")
			return err
		}},
		{"Segments.Unencoded", func(ctx context.Context, s *FSStore) error {
			_, err := s.Segments().Unencoded(ctx, "sess-degraded")
			return err
		}},
	}
}

// seedForContract fills a store with one of everything the sweeps reference, so a failure is
// always the guard under test and never a missing fixture.
func seedForContract(t *testing.T) (*testProject, core.Hash, core.Hash) {
	t.Helper()
	tp := newTestStore(t)
	ctx := context.Background()

	res, err := tp.Store.PutBytes(ctx, []byte("contract sweep payload\n"),
		PutOptions{Tool: "FileRead", Path: "src/degraded.ts"})
	require.NoError(t, err)

	argsDigest, preview := ArgsDigest(json.RawMessage(`{"file_path":"src/degraded.ts"}`))
	require.NoError(t, tp.Store.RecordToolUse(ctx, ToolUseRecord{
		ID: "toolu_01DEGRADEDAAAAAAAAAAAAAA", Session: "sess-degraded", Turn: 1, TS: 1,
		Tool: "FileRead", ArgsDigest: argsDigest, ArgsPreview: preview,
		Root: res.Root.Hash, Path: "src/degraded.ts", Bytes: res.Root.RawBytes,
	}))
	require.NoError(t, tp.Store.AppendFileVersion(ctx, "src/degraded.ts", FileVersion{
		TS: 1, Root: res.Root.Hash, Turn: 1, Bytes: res.Root.RawBytes,
	}))
	_, err = tp.Store.Segments().Open(ctx, Segment{Session: "sess-degraded", StartTurn: 0})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(ctx))

	return tp, res.Root.Hash, res.Root.Chunks[0].Hash
}

// TestContract_EveryEntryPointRefusesAClosedStore is §5.8's shutdown contract.
func TestContract_EveryEntryPointRefusesAClosedStore(t *testing.T) {
	tp, root, chunk := seedForContract(t)
	require.NoError(t, tp.Store.Close())

	require.NotNil(t, tp.Store.Segments(),
		"Segments() must stay non-nil after Close — callers do not nil-check it, and returning nil "+
			"would turn a shutdown race into a panic in the daemon rather than an error it can log")

	ops := append(mutatingOps(root, chunk), lookupOps(root)...)
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			err := op.call(context.Background(), tp.Store)
			require.ErrorIs(t, err, core.ErrDegraded,
				"%s must report core.ErrDegraded once the store is closed. Any other error — or worse, "+
					"success against a closed handle — leaves a caller unable to tell 'the store is shutting "+
					"down, retry later' apart from 'your request was wrong'", op.name)
		})
	}

	require.NoError(t, tp.Store.Close(), "Close is idempotent: a second Close reports nil")
}

// TestContract_BlockingEntryPointsHonourCancellation is the deadline half of the contract.
func TestContract_BlockingEntryPointsHonourCancellation(t *testing.T) {
	tp, root, chunk := seedForContract(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, op := range mutatingOps(root, chunk) {
		t.Run(op.name, func(t *testing.T) {
			err := op.call(ctx, tp.Store)
			require.ErrorIs(t, err, context.Canceled,
				"%s must check its context before doing work. SP-05 runs every store call under a "+
					"deadline (budget B-C), and a method that ignores cancellation makes that deadline "+
					"advisory — the call still runs to completion and the budget is already blown by the "+
					"time it returns", op.name)
		})
	}
}

// TestContract_InMemoryLookupsIgnoreCancellation asserts the other side of the split above.
//
// This is the deliberate exemption, pinned so it stays deliberate: these calls are a map read under
// an RLock, already complete before any cancellation could matter. Refusing to answer from data the
// store is already holding would mean a checkpoint that ran out of budget could not even read back
// what it had already written, which is worse than useless.
func TestContract_InMemoryLookupsIgnoreCancellation(t *testing.T) {
	tp, root, _ := seedForContract(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, op := range lookupOps(root) {
		t.Run(op.name, func(t *testing.T) {
			err := op.call(ctx, tp.Store)
			require.NotErrorIs(t, err, context.Canceled,
				"%s answers from memory and must not start refusing on a cancelled context: that would "+
					"be a behaviour change for every caller that reads back its own writes while shutting "+
					"down", op.name)
		})
	}
}
