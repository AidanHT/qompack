package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/tokens"
)

// This file tests one property, from every angle the five index logs offer: a damaged index must
// never make the store unopenable, and must never cost more than the damaged records themselves.
//
// This is not a defensive nicety. index/*.jsonl are appended to on the hot path and fsynced only on
// Flush, so a machine that loses power mid-append leaves a half-written final line as the NORMAL
// shape of a crash — not an exotic one. A loader that returned an error there would convert an
// interruption that cost the user one tool call into a store that will not open at all, and because
// objects/ is content-addressed and the index is the only thing that names those objects, "will not
// open" means every root, every file history and every segment is unreachable. §12.3's rule — count
// the bad line, log it once, keep the good ones — is what makes the difference, and each subtest
// below damages one log in one realistic way and asserts the surviving records are still there.

// writeIndexLines replaces one index file with exactly the given lines.
func writeIndexLines(t *testing.T, p *project, name string, lines ...string) {
	t.Helper()
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(p.Root).Index, name)), []byte(body), 0o600))
}

// appendIndexLines appends raw lines to an index file, which is how a corruption is injected into a
// log that already holds good records.
func appendIndexLines(t *testing.T, p *project, name string, lines ...string) {
	t.Helper()
	fp := paths.Long(filepath.Join(paths.Of(p.Root).Index, name))
	f, err := os.OpenFile(fp, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	for _, l := range lines {
		_, werr := f.WriteString(l + "\n")
		require.NoError(t, werr)
	}
}

// truncateIndexTail chops the last n bytes off an index file, reproducing a crash between the write
// syscall and its completion.
func truncateIndexTail(t *testing.T, p *project, name string, n int64) {
	t.Helper()
	fp := paths.Long(filepath.Join(paths.Of(p.Root).Index, name))
	fi, err := os.Stat(fp)
	require.NoError(t, err)
	require.Greater(t, fi.Size(), n, "fixture sanity: %s is too small to truncate by %d", name, n)
	require.NoError(t, os.Truncate(fp, fi.Size()-n))
}

// hashLiteral builds a syntactically valid hash string for a crafted index line.
func hashLiteral(seed byte) string {
	return "sha256:" + strings.Repeat(fmt.Sprintf("%02x", seed), 32)
}

// seedRoot puts one payload and returns the resulting root hash, so a corruption test has a real
// record to prove survived.
func seedRoot(t *testing.T, tp *testProject, path, body string) core.Hash {
	t.Helper()
	res, err := tp.Store.PutBytes(context.Background(), []byte(body), PutOptions{Tool: "FileRead", Path: path})
	require.NoError(t, err)
	require.NoError(t, tp.Store.Flush(context.Background()))
	return res.Root.Hash
}

func TestRecovery_RootsIndex(t *testing.T) {
	t.Run("malformed_lines_are_skipped_and_the_good_roots_survive", func(t *testing.T) {
		tp := newTestStore(t)
		good := seedRoot(t, tp, "src/survivor.ts", "the root that must survive a damaged index\n")
		require.NoError(t, tp.Store.Close())

		// Five different ways a line can be unusable, all appended after a good record.
		appendIndexLines(t, tp.project, rootsFile,
			`this is not JSON at all`,
			`{"v":1,"root":"not-a-hash","ts":1,"tool":"FileRead","path":"src/x.ts","chunks":[]}`,
			`{"v":1,"root":`+quoted(hashLiteral(0x11))+`,"ts":1,"tool":"FileRead","path":"src/y.ts","chunks":[{"h":"nope","n":4}]}`,
			`{"v":1,"op":"unheard-of","root":`+quoted(hashLiteral(0x22))+`}`,
			`{"v":99,"root":`+quoted(hashLiteral(0x88))+`,"ts":1,"tool":"FileRead","path":"src/z.ts","chunks":[]}`,
			`{"v":1,"root":`+quoted(hashLiteral(0x33))+`,"ts":1,"chunks":[`,
		)

		reopened := openOver(t, tp.project)

		require.Contains(t, reopened.Store.rootIndex, good,
			"a root recorded before the damage must still be indexed after it. If this fails the "+
				"loader aborted on the first bad line, and every root after it in the file is lost")
		require.Positive(t, reopened.counter("store.index.badline"),
			"malformed lines must be COUNTED, not silently swallowed: store.index.badline is the only "+
				"signal an operator gets that an index is decaying")

		// The unknown "op" and the unknown version are in the list above deliberately. Neither may be
		// treated as a tombstone (a later wave's new record type must not make this build retire
		// roots) and neither may be treated as a content record (which would publish a phantom root
		// with no chunks). The only correct handling of a record this build does not speak is to skip
		// it and count it.
		require.Len(t, reopened.Store.rootIndex, 1,
			"exactly the one good root should be indexed; a crafted line must never introduce a root")
	})

	t.Run("a_truncated_final_line_is_the_normal_crash_shape", func(t *testing.T) {
		tp := newTestStore(t)
		first := seedRoot(t, tp, "src/first.ts", "first payload, fully committed\n")
		second := seedRoot(t, tp, "src/second.ts", "second payload, cut off mid-append\n")
		require.NoError(t, tp.Store.Close())

		// Lose the tail of the last line, exactly as a power cut between write and fsync would.
		truncateIndexTail(t, tp.project, rootsFile, 40)

		reopened := openOver(t, tp.project)
		require.Contains(t, reopened.Store.rootIndex, first,
			"the completed record before the truncation must survive it")
		require.NotContains(t, reopened.Store.rootIndex, second,
			"fixture sanity: the truncated record should be the one that is lost")
	})

	t.Run("a_class_ordinal_this_build_does_not_know_degrades_to_prose", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		h := hashLiteral(0x44)
		writeIndexLines(t, tp.project, rootsFile,
			`{"v":1,"root":`+quoted(h)+`,"ts":5,"tool":"FileRead","path":"src/future.ts",`+
				`"raw":10,"canon":10,"tokens":3,"class":200,"chunks":[]}`)

		reopened := openOver(t, tp.project)

		parsed, err := core.ParseHash(h)
		require.NoError(t, err)
		e, ok := reopened.Store.rootIndex[parsed]
		require.True(t, ok, "a line with an unknown class must still load; only the class is unusable")
		require.Equal(t, uint8(tokens.ClassProse), e.Class,
			"an ordinal past this build's highest known class must degrade to prose rather than being "+
				"stored verbatim, or a later build's class 200 would be read back as a class this build "+
				"prices differently")
		require.Positive(t, reopened.counter("store.index.badclass"),
			"the degradation must be counted; a silent reclassification is a pricing bug nobody can see")
	})

	t.Run("an_impossible_chunk_length_degrades_to_unknown_not_to_negative", func(t *testing.T) {
		// chunkSet stores int32 to keep a million-chunk index affordable, but "n" on the read path
		// comes from a parsed line and can be any number JSON admits. A bare narrowing conversion
		// wraps 3e9 to a NEGATIVE length, which GetChunk hands to getObject as the expected size.
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		h := hashLiteral(0x99)
		writeIndexLines(t, tp.project, rootsFile,
			`{"v":1,"root":`+quoted(h)+`,"ts":9,"tool":"FileRead","path":"src/huge.ts","raw":10,`+
				`"canon":10,"tokens":3,"chunks":[{"h":`+quoted(hashLiteral(0xaa))+`,"n":3000000000},`+
				`{"h":`+quoted(hashLiteral(0xbb))+`,"n":-5}]}`)

		reopened := openOver(t, tp.project)

		for _, seed := range []byte{0xaa, 0xbb} {
			ch, err := core.ParseHash(hashLiteral(seed))
			require.NoError(t, err)
			require.Equal(t, int32(-1), reopened.Store.chunkSet[ch],
				"a length that cannot be an int32 must record as -1 (unknown), never as a wrapped "+
					"negative: GetChunk passes the recorded length to getObject as the expected size, so a "+
					"wrapped value turns every read of this chunk into a spurious integrity failure — or, "+
					"landing exactly on -1 by accident, into a silently disabled check")
		}

		for _, n := range []int{0, 1, MaxPutBytes} {
			require.Equal(t, int32(n), chunkLenOrUnknown(n), "a legitimate length must pass through exactly")
		}
		require.Equal(t, int32(-1), chunkLenOrUnknown(MaxPutBytes+1),
			"nothing above MaxPutBytes can be a real chunk: Put truncates before chunking")
	})

	t.Run("an_undecodable_signature_costs_the_signature_not_the_line", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		bad := hashLiteral(0x55)
		wellFormed := hashLiteral(0x66)
		writeIndexLines(t, tp.project, rootsFile,
			// Not valid base64 at all.
			`{"v":1,"root":`+quoted(bad)+`,"ts":6,"tool":"FileRead","path":"src/sig1.ts",`+
				`"chunks":[],"sig":{"p":4,"m":"!!!not-base64!!!"}}`,
			// Valid base64 whose payload sketch cannot decode.
			`{"v":1,"root":`+quoted(wellFormed)+`,"ts":7,"tool":"FileRead","path":"src/sig2.ts",`+
				`"chunks":[],"sig":{"p":4,"m":`+quoted(base64.StdEncoding.EncodeToString([]byte("garbage")))+`}}`,
			// A deltas reference that is not a hash: also recoverable, also must not drop the line.
			`{"v":1,"root":`+quoted(hashLiteral(0x77))+`,"ts":8,"tool":"FileRead","path":"src/sig3.ts",`+
				`"chunks":[],"deltas":"definitely-not-a-hash"}`)

		reopened := openOver(t, tp.project)
		require.Len(t, reopened.Store.rootIndex, 3,
			"a signature or delta reference that cannot be decoded must degrade that FIELD. Dropping the "+
				"whole line would orphan the root's chunks in objects/, where nothing else names them")

		badHash, err := core.ParseHash(bad)
		require.NoError(t, err)
		require.Equal(t, uint16(4), reopened.Store.rootIndex[badHash].Sig.Perms,
			"the permutation count survives even when the minhash payload does not, so a later build "+
				"can tell the signature was 4-permutation and recompute rather than guess")
	})
}

func TestRecovery_ToolUseIndex(t *testing.T) {
	t.Run("a_supersede_for_an_unknown_id_is_a_no_op", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		writeIndexLines(t, tp.project, toolUseFile,
			`{"v":1,"id":"toolu_01RECOVERYAAAAAAAAAAAAAA","s":"sess-recovery","turn":1,"ts":10,`+
				`"tool":"FileRead","path":"src/a.ts","bytes":5,"tokens":2}`,
			// Supersedes a record that is not in this file — a real shape when a GC has already
			// collected the superseded record's own line.
			`{"v":1,"op":"supersede","id":"toolu_01MISSINGBBBBBBBBBBBBBBB","by":"toolu_01RECOVERYAAAAAAAAAAAAAA","ts":11}`,
			// A version this build does not speak.
			`{"v":99,"id":"toolu_01FUTURECCCCCCCCCCCCCCCC","s":"sess-recovery","turn":2,"ts":12,"tool":"FileRead"}`,
			// An op this build does not speak.
			`{"v":1,"op":"from-a-later-wave","id":"toolu_01RECOVERYAAAAAAAAAAAAAA"}`,
			`{"v":1,"id":`)

		reopened := openOver(t, tp.project)

		rec, err := reopened.Store.ToolUse(context.Background(), "toolu_01RECOVERYAAAAAAAAAAAAAA")
		require.NoError(t, err)
		require.Equal(t, StatusOK, rec.Status,
			"a supersede naming an id this index does not hold must change nothing. If it were applied "+
				"by creating the record, a collected tool_use would resurrect as a statusless ghost")
		require.NotContains(t, reopened.Store.toolUse, core.ToolUseID("toolu_01FUTURECCCCCCCCCCCCCCCC"),
			"a record whose version this build does not speak must be skipped, not half-parsed into "+
				"whatever fields happen to overlap")
		require.Positive(t, reopened.counter("store.index.badline"))
	})

	t.Run("an_oversized_line_does_not_brick_the_store", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		// One line past the scanner's 4 MiB ceiling. bufio.Scanner reports this as an error for the
		// whole scan, and the loader must degrade it to "the rest of this file is unreadable" rather
		// than to "this store cannot be opened".
		writeIndexLines(t, tp.project, toolUseFile,
			`{"v":1,"id":"toolu_01BEFOREHUGEAAAAAAAAAAAA","s":"sess-recovery","turn":1,"ts":20,`+
				`"tool":"FileRead","path":"src/before.ts","bytes":5,"tokens":2}`,
			`{"v":1,"argp":"`+strings.Repeat("A", scannerMaxBuf+1024)+`"}`)

		reopened := openOver(t, tp.project)
		require.Contains(t, reopened.Store.toolUse, core.ToolUseID("toolu_01BEFOREHUGEAAAAAAAAAAAA"),
			"records read before the oversized line must remain usable")
		require.Positive(t, reopened.counter("store.index.badline"))
	})
}

func TestRecovery_FileHistoryIndex(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())

	h1, h2 := hashLiteral(0x01), hashLiteral(0x02)
	writeIndexLines(t, tp.project, filesLogFile,
		// Appended out of order, which a subagent recording a read after the parent already did can
		// genuinely produce.
		`{"v":1,"path":"src/hist.ts","ts":200,"turn":4,"root":`+quoted(h2)+`,"bytes":20}`,
		`{"v":1,"path":"src/hist.ts","ts":100,"turn":2,"root":`+quoted(h1)+`,"bytes":10}`,
		// No path: unusable, because path is the key the history is filed under.
		`{"v":1,"path":"","ts":300,"turn":5,"root":`+quoted(h1)+`,"bytes":30}`,
		`{"v":99,"path":"src/hist.ts","ts":400,"turn":6,"root":`+quoted(h1)+`,"bytes":40}`,
		`{ not json`)

	reopened := openOver(t, tp.project)

	hist, err := reopened.Store.FileHistory(context.Background(), "src/hist.ts")
	require.NoError(t, err)
	require.Len(t, hist, 2, "only the two usable versions should load")
	require.Equal(t, core.UnixMilli(100), hist[0].TS)
	require.Equal(t, core.UnixMilli(200), hist[1].TS,
		"history is read back ascending by timestamp no matter what order it was appended in, so "+
			"FileAt cannot answer with a version that did not exist yet")
	require.Positive(t, reopened.counter("store.index.badline"))
}

func TestRecovery_SessionIndex(t *testing.T) {
	t.Run("malformed_session_records_are_skipped", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		writeIndexLines(t, tp.project, sessionsFile,
			`{"v":1,"s":"sess-a","start":100,"end":200,"turns":2,"tooluses":3,"roots":3,"objects":5,"bytes":50,"rawbytes":80,"dedup":1.6}`,
			`{"v":1,"s":"","start":100,"end":200}`,
			`not json`)

		reopened := openOver(t, tp.project)
		require.Len(t, reopened.Store.sessions, 1,
			"a session record with no id names nothing and must be skipped")
		require.Equal(t, core.UnixMilli(200), reopened.Store.sessions["sess-a"].End)
	})

	t.Run("recent_sessions_orders_newest_first_and_clamps_n", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		// Two sessions share an End so the Start tiebreak is exercised, and two share both so the id
		// tiebreak is: without a total order, GC's retention set would differ run to run and the
		// "keep the last 10 sessions" axis would collect different roots on every invocation.
		writeIndexLines(t, tp.project, sessionsFile,
			`{"v":1,"s":"sess-oldest","start":100,"end":150}`,
			`{"v":1,"s":"sess-mid-a","start":200,"end":300}`,
			`{"v":1,"s":"sess-mid-b","start":250,"end":300}`,
			`{"v":1,"s":"sess-newest","start":400,"end":900}`)

		reopened := openOver(t, tp.project)

		require.Equal(t,
			[]core.SessionID{"sess-newest", "sess-mid-b", "sess-mid-a", "sess-oldest"},
			reopened.Store.RecentSessions(-1),
			"a negative n means every session, newest first; equal End must break on Start")
		require.Equal(t, []core.SessionID{"sess-newest", "sess-mid-b"}, reopened.Store.RecentSessions(2))
		require.Empty(t, reopened.Store.RecentSessions(0), "n=0 must select nothing, not everything")
		require.Len(t, reopened.Store.RecentSessions(99), 4, "n past the end must clamp, not panic")
	})
}

func TestRecovery_SegmentIndex(t *testing.T) {
	tp := newTestStore(t)
	require.NoError(t, tp.Store.Close())

	writeIndexLines(t, tp.project, segmentsFile,
		`{"v":1,"op":"open","id":1,"s":"sess-seg","st":0,"sts":1000}`,
		`{"v":1,"op":"close","id":1,"et":4,"ets":2000,"tok":900,"feat":{"gap_seconds":1.5}}`,
		`{"v":1,"op":"encode","id":1,"seq":3,"ts":3000}`,
		`{"v":1,"op":"bloom","id":1,"ref":"segments/1.bloom"}`,
		// Every one of these names a segment that was never opened — the shape a GC that collected an
		// old segment's open record leaves behind.
		`{"v":1,"op":"close","id":404,"et":9,"ets":4000,"tok":10}`,
		`{"v":1,"op":"encode","id":404,"seq":4,"ts":5000}`,
		`{"v":1,"op":"bloom","id":404,"ref":"segments/404.bloom"}`,
		`{"v":1,"op":"a-later-waves-op","id":1}`,
		`{"v":99,"op":"open","id":7,"s":"sess-seg","st":0,"sts":6000}`,
		// Records whose op is recognized but whose PAYLOAD does not typecheck. These get past the
		// discriminator probe and fail on the second unmarshal, which is a different branch from a
		// line that is not JSON at all — and the one a schema drift between waves would produce.
		`{"v":1,"op":"open","id":"not-a-number","s":"sess-seg"}`,
		`{"v":1,"op":"close","id":1,"et":"not-a-turn"}`,
		`{"v":1,"op":"encode","id":1,"seq":{"nested":true}}`,
		`{"v":1,"op":"bloom","id":1,"ref":[1,2,3]}`,
		`{"v":1,"op":"open",`)

	reopened := openOver(t, tp.project)

	segs, err := reopened.Store.Segments().Range(context.Background(), 0, 100)
	require.NoError(t, err)
	require.Len(t, segs, 1, "only the segment that was actually opened may exist")
	require.True(t, segs[0].Closed)
	require.True(t, segs[0].EncodedOnce,
		"the encode record must replay, or the DPI guard would let an already-encoded segment be "+
			"encoded a second time after any restart — which is the exact failure §4.6 exists to stop")
	require.Equal(t, core.CheckpointSeq(3), segs[0].CheckpointSeq)
	require.Equal(t, "segments/1.bloom", segs[0].BloomRef)
	require.InDelta(t, 1.5, segs[0].Features["gap_seconds"], 0)
	require.Equal(t, core.TurnIndex(4), segs[0].EndTurn,
		"the well-formed close record must win: a later record with a type-invalid payload must be "+
			"discarded whole, never applied field by field until it hits the bad one")

	// A mutation record for a segment that no longer exists must not conjure one.
	require.NotContains(t, reopened.Store.seg.byID, core.SegmentID(404))
	require.NotContains(t, reopened.Store.seg.byID, core.SegmentID(7))
}

func TestRecovery_StoreStateCounters(t *testing.T) {
	t.Run("a_corrupt_state_file_is_recomputed_rather_than_zeroed", func(t *testing.T) {
		tp := newTestStore(t)
		ctx := context.Background()
		_, err := tp.Store.PutBytes(ctx, []byte(strings.Repeat("recompute me\n", 64)),
			PutOptions{Tool: "FileRead", Path: "src/state.ts"})
		require.NoError(t, err)
		require.NoError(t, tp.Store.Flush(ctx))
		require.NoError(t, tp.Store.Close())

		statePath := filepath.Join(paths.Of(tp.Root).State, storeStateFile)
		require.NoError(t, os.WriteFile(paths.Long(statePath), []byte(`{"version":`), 0o600))

		reopened := openOver(t, tp.project)
		require.Positive(t, reopened.Store.bytesOnDisk,
			"with state/store.json unreadable, bytes on disk must be recomputed by walking objects/. "+
				"Starting from zero would make the store believe it is empty and let it grow past its "+
				"configured budget without ever triggering a GC")
		require.Positive(t, reopened.Store.rawBytes,
			"the pre-dedup total must be re-summed from the roots index for the same reason")
	})

	t.Run("an_absent_state_file_is_also_recomputed", func(t *testing.T) {
		tp := newTestStore(t)
		ctx := context.Background()
		_, err := tp.Store.PutBytes(ctx, []byte(strings.Repeat("no state file\n", 64)),
			PutOptions{Tool: "FileRead", Path: "src/nostate.ts"})
		require.NoError(t, err)
		require.NoError(t, tp.Store.Flush(ctx))
		require.NoError(t, tp.Store.Close())

		require.NoError(t, os.Remove(paths.Long(filepath.Join(paths.Of(tp.Root).State, storeStateFile))))

		reopened := openOver(t, tp.project)
		require.Positive(t, reopened.Store.bytesOnDisk)
	})

	t.Run("a_readable_state_file_is_trusted_verbatim", func(t *testing.T) {
		tp := newTestStore(t)
		require.NoError(t, tp.Store.Close())

		b, err := json.Marshal(storeState{Version: indexRecordVersion, Bytes: 4242, RawBytes: 8484})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			paths.Long(filepath.Join(paths.Of(tp.Root).State, storeStateFile)), b, 0o600))

		reopened := openOver(t, tp.project)
		require.Equal(t, int64(4242), reopened.Store.bytesOnDisk)
		require.Equal(t, int64(8484), reopened.Store.rawBytes,
			"the recorded counters win over any recomputation: they are the only record of bytes that "+
				"dedup means are no longer derivable from what is on disk")
	})
}

func TestRecovery_OpenFailsCleanlyWhenAnIndexIsUnusable(t *testing.T) {
	// Damage the loader can route around is skipped; damage it cannot is an error at Open. The
	// difference matters operationally: §12.3 has the daemon refuse to start on the second kind so
	// the client spools to its WAL, and a store that opened "successfully" but empty would instead
	// let the daemon accept writes against an index it had silently discarded.
	for _, name := range []string{rootsFile, toolUseFile, filesLogFile, sessionsFile, segmentsFile} {
		t.Run(name, func(t *testing.T) {
			p := newProject(t)
			require.NoError(t, os.MkdirAll(
				paths.Long(filepath.Join(paths.Of(p.Root).Index, name)), 0o700))

			s, err := openFS(p.Root, p.Cfg, Deps{
				Log: p.Log, Clock: p.Clock, Chunker: newFixedChunker(), Canon: canonIdentity(),
			})
			require.Error(t, err, "%s occupied by a directory must fail the open, not be ignored", name)
			require.Nil(t, s)
			require.Contains(t, err.Error(), "store: open",
				"the error must name the store and the file so an operator can act on it")
		})
	}
}

// quoted renders s as a JSON string literal, for building crafted index lines readably.
func quoted(s string) string {
	b, err := json.Marshal(s)
	if err != nil { // unreachable for a string
		panic(err)
	}
	return string(b)
}
