package negknow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The acknowledged-record barriers (C1.6/D24, w6-ckptsync). record_eliminated and
// `/qompack:pin --eliminated` answer the agent or the user with the record they made, and
// record_eliminated's answer is documented as "durable — this tool writes a persistent record". No
// daemon spool line stands behind either call, so the elimination log line is the only record of
// it: the tests below pin that the minted evidence and then the line (and, the first time, the
// line's file name) are durable before either answers, and that the other sources — whose
// durability boundary is the daemon spool, and whose lost tail costs one elimination by design
// (log.go) — still pay nothing.

// durableProbe records every barrier an ingest issues, in order, with what the log held at each.
type durableProbe struct {
	t       *testing.T
	logPath string
	steps   []string
	// logAt maps a step index to the elimination log's size when that barrier ran.
	logAt map[int]int64
	// durable is the log's size at its last successful file barrier: what a power cut keeps.
	durable int64
	// failFile, when set, is returned by the file barrier instead of syncing.
	failFile error
}

func newDurableProbe(t *testing.T, root string) *durableProbe {
	return &durableProbe{t: t, logPath: logPath(root), logAt: map[int]int64{}}
}

func (p *durableProbe) note(step string) {
	fi, err := os.Stat(paths.Long(p.logPath))
	size := int64(0)
	if err == nil {
		size = fi.Size()
	}
	p.logAt[len(p.steps)] = size
	p.steps = append(p.steps, step)
}

func (p *durableProbe) barriers() paths.Barriers {
	return paths.Barriers{
		SyncFile: func(f *os.File) error {
			p.note("file:" + filepath.Base(f.Name()))
			if p.failFile != nil {
				return p.failFile
			}
			if err := f.Sync(); err != nil {
				return err
			}
			fi, err := f.Stat()
			require.NoError(p.t, err)
			p.durable = fi.Size()
			return nil
		},
		SyncDir: func(dir string) error {
			p.note("dir:" + filepath.Base(dir))
			return paths.SyncDir(dir)
		},
	}
}

// publishingStore is fakeStore with the store's durability half: SyncPublication records the root
// it was asked to make durable, as an evidence step in the probe's sequence.
type publishingStore struct {
	*fakeStore
	probe *durableProbe
}

func (s publishingStore) SyncPublication(_ context.Context, h core.Hash) error {
	s.probe.note("evidence:" + h.String())
	return nil
}

// ackSource is one acknowledged ingest path, driven through the same probe.
type ackSource struct {
	name   string
	ingest func(l *ledger, target, reason string) (Record, error)
}

func ackSources() []ackSource {
	return []ackSource{
		{"record_eliminated", func(l *ledger, target, reason string) (Record, error) {
			rec, _, err := l.IngestMCP(context.Background(), MCPArgs{
				Target: target, Approach: "widen pool timeout", Reason: reason, Scope: "project",
			})
			return rec, err
		}},
		{"pin --eliminated", func(l *ledger, target, reason string) (Record, error) {
			rec, _, err := l.IngestPin(context.Background(), PinArgs{
				Target: target, Approach: "widen pool timeout", Reason: reason, Scope: "project",
			})
			return rec, err
		}},
	}
}

// TestIngest_AnAcknowledgedEliminationIsDurableBeforeItIsAnswered pins, for both acknowledged
// sources, the order the answer depends on: the minted evidence first (its publication pass, before
// the log names it), then the log line's file sync (the line is in the file by then), then — only
// the first time in the ledger's lifetime — the records directory, for the log's own name. A power
// cut after the answer keeps the record: the log cut back to its durable size still holds it on the
// next Open.
func TestIngest_AnAcknowledgedEliminationIsDurableBeforeItIsAnswered(t *testing.T) {
	for _, src := range ackSources() {
		t.Run(src.name, func(t *testing.T) {
			root, cfg := newProject(t)
			probe := newDurableProbe(t, root)
			st := publishingStore{fakeStore: newFakeStore(), probe: probe}
			l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), st))
			l.barriers = probe.barriers()

			const reasonA = "pgbouncer 1.18 ignores it in transaction mode"
			recA, err := src.ingest(l, "src/auth.ts:refreshToken", reasonA)
			require.NoError(t, err)
			evA := fakeStoreRoot([]byte(reasonA))
			require.Equal(t, evA, recA.Evidence, "fixture: the evidence was minted from the reason")
			want := []string{"evidence:" + evA.String(), "file:" + logFileName, "dir:" + filepath.Base(l.lay.Records)}
			require.Equal(t, want, probe.steps,
				"evidence, then the line, then the log's name — the first acknowledged record")
			require.Equal(t, int64(0), probe.logAt[0], "the evidence is durable before the log names it")
			require.Positive(t, probe.logAt[1], "the line is in the file when the file barrier runs")

			probe.steps = nil
			const reasonB = "the pool is not the bottleneck; the upstream DNS lookup is"
			recB, err := src.ingest(l, "src/db.ts:connect", reasonB)
			require.NoError(t, err)
			evB := fakeStoreRoot([]byte(reasonB))
			require.Equal(t, []string{"evidence:" + evB.String(), "file:" + logFileName}, probe.steps,
				"once the log's name is durable, a record pays its evidence and its line only")

			require.NoError(t, l.Close())
			raw, err := os.ReadFile(paths.Long(probe.logPath))
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(paths.Long(probe.logPath), raw[:probe.durable], 0o600),
				"the power cut keeps what the barriers made durable")
			re := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), newFakeStore()))
			for _, id := range []string{recA.ID, recB.ID} {
				_, err := re.Get(context.Background(), id)
				require.NoError(t, err, "acknowledged record %s survives the cut", id)
			}
		})
	}
}

// TestIngest_AnEliminationWhoseSyncFailsIsNotAcknowledged: a failed file barrier fails the call, so
// neither source answers "recorded" for a line it could not make durable.
func TestIngest_AnEliminationWhoseSyncFailsIsNotAcknowledged(t *testing.T) {
	for _, src := range ackSources() {
		t.Run(src.name, func(t *testing.T) {
			root, cfg := newProject(t)
			probe := newDurableProbe(t, root)
			probe.failFile = errInjectedLogSync
			l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), newFakeStore()))
			l.barriers = probe.barriers()

			_, err := src.ingest(l, "src/auth.ts:refreshToken", "pgbouncer 1.18 ignores it")
			require.ErrorIs(t, err, errInjectedLogSync, "a line that is not durable is not acknowledged")
			require.Equal(t, []string{"file:" + logFileName}, probe.steps,
				"the store has no publication pass to call, and the failed file barrier stops the rest")
		})
	}
}

var errInjectedLogSync = errors.New("injected elimination-log sync failure")

// TestRecord_AnUnacknowledgedSourcePaysNoBarrier pins the other half: a record that answers no one
// — Record as the heuristic detector calls it, and a user statement recognized in a prompt, both of
// which arrive through the daemon spool — issues no barrier at all, and its line is written.
func TestRecord_AnUnacknowledgedSourcePaysNoBarrier(t *testing.T) {
	root, cfg := newProject(t)
	probe := newDurableProbe(t, root)
	st := publishingStore{fakeStore: newFakeStore(), probe: probe}
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), st))
	l.barriers = probe.barriers()

	mustRecord(t, l, newRecord("detector", "src/auth.ts:refreshToken", "widen pool timeout", "timed out again"))
	recs, err := l.IngestUserStatement(context.Background(), UserStatement{
		Prompt: "that didn't work", Turn: 3, Path: "src/db.ts", Approach: "raise the retry count",
		PromptRoot: testEvidence("prompt"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, recs, "fixture: the statement is recognized")
	require.Empty(t, probe.steps, "no barrier for a record nothing acknowledges")

	raw, err := os.ReadFile(paths.Long(probe.logPath))
	require.NoError(t, err)
	require.Equal(t, 1+len(recs), bytes.Count(raw, []byte("\n")), "every line is written, without a sync")
}

// TestIngestMCP_PublishesItsEvidenceThroughTheRealStore runs the acknowledged path over the real
// store, the one the daemon hands the ledger: the minted evidence gets exactly one publication pass
// (store.publication.sync), the pass succeeds on a root PutBytes just wrote, and the evidence reads
// back as the reason text.
func TestIngestMCP_PublishesItsEvidenceThroughTheRealStore(t *testing.T) {
	root, cfg := newProject(t)
	reg := newMetrics()
	st, err := store.Open(root, cfg, store.Deps{Log: logging.Nop(), Clock: core.SystemClock(), Metrics: reg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	l := openLedger(t, root, cfg, nil, ingestDeps("s1", newMetrics(), st))

	const reason = "pgbouncer 1.18 ignores it in transaction mode"
	rec, _, err := l.IngestMCP(context.Background(), MCPArgs{
		Target: "src/auth.ts:refreshToken", Approach: "widen pool timeout", Reason: reason, Scope: "project",
	})
	require.NoError(t, err)
	require.False(t, rec.Evidence.IsZero(), "fixture: the reason was stored as evidence")
	require.Equal(t, int64(1), reg.Counter(store.CounterPublicationSync).Value(),
		"one publication pass makes the evidence durable before the record names it")

	rc, err := st.Open(context.Background(), rec.Evidence)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)
	require.Equal(t, reason, string(got))
}
