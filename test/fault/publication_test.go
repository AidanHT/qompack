package fault

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
)

// The publication-boundary matrix (acceptance row SP17-M7-04, verbatim: "Forced failure at each
// publication boundary yields recoverable or explicit incomplete state; no newly dangling pointer").
//
// Every row here seeds real state through the INSTALLED binary, cuts it at one boundary, runs a
// recovery session through the same binary with the daemon restarted, and then walks `.qompack/`
// with the consistency audit. The row passes when the post-state is `recovered` (the cut introduced
// no dangling reference and recording resumed) or `explicit_incomplete` (something still does not
// resolve AND the product says so in `status --json`, `self-test --json`, LOUD.log or a DropEntry).
//
// What this file deliberately does NOT do is re-unit-test the seams. Each row's Seed names the unit
// test whose recipe it borrows (survey-security-fault.md §2); those tests own the cut point in
// process, this one owns the question of what a user's installed copy looks like afterwards.

// boundaryRow is one row of the matrix.
type boundaryRow struct {
	// Name is the subtest name and the evidence record's file name.
	Name string
	// Boundary is what was cut, in the matrix's own words.
	Boundary string
	// Seed says how the state reached the condition the cut needed, and cites the unit test whose
	// recipe it borrows.
	Seed string
	// Owner is the package a finding from this row belongs to. Task 6 collects by owner.
	Owner string
	// WantsCheckpoint asks the seed to produce a checkpoint artifact before the cut.
	WantsCheckpoint bool
	// WantsUndrainedSpool asks the seed to leave spool bytes on disk by killing a daemon mid-burst,
	// for the rows whose boundary IS a spool file.
	WantsUndrainedSpool bool
	// Known is the returned finding this row reproduces on today's tree, or "" when the row is
	// expected to recover. See judgeRecovery: a pinned failure is evidence, an unpinned one is a
	// test failure, and a pin over a row that recovered is a false claim.
	Known string
	// Cut performs the forced failure. It returns a description of what it did, or a non-empty
	// skip reason when this host cannot express the cut.
	Cut func(t *testing.T, b bundle, p project, sess core.SessionID) (desc, skip string)
	// Measure is an optional extra observation taken after the recovery, for a row whose boundary
	// has a consequence the reference audit cannot see. Its prose joins the record's Detail.
	Measure func(t *testing.T, p project) string
	// Note is a caveat the record must carry that the measurement itself cannot state — an
	// inference the row makes, or a reading of the evidence a later reader should not take as
	// measured. It joins the Detail prefixed as a note, so it can never be mistaken for a reading.
	Note string
	// Names are the tokens a surface line must contain to count as the product reporting THIS cut.
	// It is REQUIRED: namingEvidence fails a row that declares none, because a row with no tokens
	// accepts anything new. See namingEvidence for why new is not the same as relevant.
	//
	// A token must be one the product cannot emit incidentally. Three of them were: `ack` matched
	// every day-log line in the run, because dayLogWarnings prefixes each line with the log's file
	// name and "qompack" ends in "ack"; `lease` is a substring of "release"; `seq` is one of
	// "sequence" and "consequence". TestFault_LinesNamingIgnoresTheUniversalWarn audits this list.
	Names []string
}

// TestFault_PublicationBoundaries runs the whole matrix serially against one assembled bundle.
// Serial is a requirement, not a convenience: every row starts a real detached daemon, and one
// daemon at a time is this task's process-safety rule.
func TestFault_PublicationBoundaries(t *testing.T) {
	b := assembledBundle(t)
	for _, row := range publicationBoundaries() {
		t.Run(row.Name, func(t *testing.T) { runBoundary(t, b, row) })
	}
}

// runBoundary is one row: seed, baseline, cut, recover, read the surfaces, audit, judge.
//
// The order of the last three steps is the fix for round 1's worst defect. Every "nothing reported a
// gap" verdict there was taken AFTER the recovery daemon had been shut down, so
// `StatusReport.Snapshot` was always nil and the whole snapshot branch of statusGaps — the spool
// count, the LOUD tail and every degradation counter — was dead code in all forty rows. The
// degradation reading is now taken while the recovery daemon is up, and it is DELTAED against a
// baseline taken the same way before the cut.
func runBoundary(t *testing.T, b bundle, row boundaryRow) {
	t.Helper()

	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, row.Name)
	rec.Phase = PhasePublication
	rec.Boundary = row.Boundary
	rec.SeedMethod = row.Seed

	// Seed: a real session through the installed binary, so the artifact the cut damages was
	// written by the product rather than by this test. seedSession leaves its daemon UP.
	sess := sessionID(row.Name)
	seedSession(t, b, p, sess)
	if row.WantsCheckpoint {
		rec.SeedMethod += "; " + ensureCheckpoint(t, b, p, sess)
	}
	// The baseline, taken with a daemon UP so `status --json` carries its snapshot. An
	// undrained-spool row takes it inside its own burst instead, before the kill, because asking
	// `status` afterwards would spawn a daemon that drains away the spool file the row cuts.
	var baseline degradationSnapshot
	if row.WantsUndrainedSpool {
		shutdownIfReachable(t, p.Root)
		var prose string
		prose, baseline = seedUndrainedSpool(t, b, p, sessionID(row.Name+"-burst"))
		rec.SeedMethod += "; " + prose
	} else {
		baseline = snapshotDegradation(t, b, p)
	}
	shutdownIfReachable(t, p.Root)
	before := auditProject(t, p.Root)

	desc, skip := row.Cut(t, b, p, sess)
	if skip != "" {
		skipRecorded(t, rec, skip)
		return
	}
	rec.Detail = "cut: " + desc

	// Recover: session-start + tool events + flush, with the daemon restarted and LEFT UP.
	recording := recoverSession(t, b, p, sessionID(row.Name+"-r"))
	all := degradationSince(baseline, snapshotDegradation(t, b, p))
	ev := namingEvidence(t, all, row.Names)
	shutdownIfReachable(t, p.Root)
	rec.Detail += "\neverything the surfaces gained: " + all.String() +
		"\nof which naming this cut (" + strings.Join(row.Names, ", ") + "): " + ev.String()

	after := auditProject(t, p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	if row.Measure != nil {
		rec.Detail += "\nmeasured: " + row.Measure(t, p)
	}
	if row.Note != "" {
		rec.Detail += "\nnote (INFERENCE, not a measurement): " + row.Note
	}

	judgeRecovery(t, rec, before, after, recording, ev, row.Owner, row.Known)
}

// publicationBoundaries is the matrix the brief enumerates, in its order. Every row's Cut is
// declared in cuts_test.go beside the recipe it borrows.
func publicationBoundaries() []boundaryRow {
	rows := publicationRows()
	for _, row := range rows {
		registerNames(row.Name, row.Names)
	}
	return rows
}

// publicationRows is the table itself. It is separate from publicationBoundaries only so that the
// registration above cannot be forgotten when a row is added: every caller goes through the wrapper.
func publicationRows() []boundaryRow {
	return []boundaryRow{
		{
			Name:     "object_written_index_line_absent",
			Boundary: "object written but index line absent",
			Seed: "a real session's last index/roots.jsonl line is removed while its objects stay on " +
				"disk — the state daemon/delivery_crash_test.go's cut between the durable object and " +
				"the reference produces in process",
			Owner: "internal/store + internal/daemon",
			// F4-1's pin is gone: the daemon's startup publication accounting (V6-RECOVERY-1) now
			// walks objects/ after the drain and Louds the object no live index chunk references, so
			// the recovery daemon reports the gap on LOUD.log and in `status --json` — the loud tail
			// and the daemon.publication.unindexed_object_candidates counter. The row is now
			// `explicit_incomplete`: the object is still unindexed and NOTHING here repairs it, but it
			// is named. Detection is not recovery — reapplying an index line without the current
			// authority is unsafe (main's ruling), so recovery stays the operator's verified
			// backup/restore path. `unindexed` is the token the startup LOUD line carries for this cut.
			Names: []string{"roots.jsonl", "badline", "index", "root", "resolve", "unindexed"},
			Cut:   cutDropLastRootLine,
		},
		{
			Name:     "roots_last_line_truncated",
			Boundary: "roots.jsonl last line truncated mid-record",
			Seed: "index/roots.jsonl is truncated inside its final record, the torn-write shape " +
				"store/fsstore's loadRoots counts as store.index.badline",
			Owner: "internal/store",
			Names: []string{"roots.jsonl", "badline", "malformed index"},
			Cut:   cutTruncateRootsMidRecord,
		},
		{
			Name:     "tool_use_truncated",
			Boundary: "tool_use.jsonl truncated",
			Seed: "index/tool_use.jsonl is truncated inside its final record (the frozen wire shape " +
				"testdata/golden/contracts/store/want/tool_use_line.jsonl pins)",
			Owner: "internal/store",
			Names: []string{"tool_use", "badline", "malformed"},
			Cut:   cutTruncateToolUseMidRecord,
		},
		{
			Name:     "capture_sidecar_stage_one_only",
			Boundary: "capture sidecar at stage 1 only (published:false)",
			Seed: "a real sidecar is rewritten to the state WriteCaptureSidecar leaves and " +
				"LinkCaptureReference has not yet joined — store/capture_sidecar.go's own " +
				"\"crash between publication order's first two stages\"",
			Owner: "internal/store + internal/daemon",
			// F4-4's pin is gone: the daemon's startup publication accounting (V6-RECOVERY-1) now
			// classifies capture sidecars after the drain and Louds an observe.tool delivery whose
			// outcome is ok and whose bytes are durable with no reference joined — fsck's own
			// calibration rule 2 — so the recovery daemon reports the gap on LOUD.log and in
			// `status --json` (the loud tail and the daemon.publication.unpublished_captures counter).
			// The row is now `explicit_incomplete`: the capture is still at stage one and the sidecar
			// is preserved as evidence, but it is named. Detection is not recovery — reapplying a
			// sidecar without the current authority is unsafe (main's ruling), so recovery stays the
			// operator's verified backup/restore path. `unpublished` is the token the startup LOUD
			// line carries for this cut.
			Names: []string{"capture", "sidecar", "published", "observation", "unpublished"},
			Cut:   cutUnpublishCaptureSidecar,
		},
		{
			Name:            "checkpoint_orphan_artifact",
			Boundary:        "checkpoint artifact present without its MANIFEST line (orphan)",
			Seed:            "a second artifact is written beside the sealed one with no manifest line, the state finalize.go logs when CreateNew succeeded and AppendManifest did not",
			Owner:           "internal/checkpoint",
			WantsCheckpoint: true,
			// F4-9's pin is gone: checkpoint.Reader.List now sweeps the directory and Louds an
			// artifact no MANIFEST line claims. The row is `explicit_incomplete` — the orphan is
			// still an orphan, and it is now named.
			Names: []string{"checkpoint", "manifest", "artifact", "orphan"},
			Cut:   cutOrphanCheckpointArtifact,
		},
		{
			Name:            "checkpoint_manifest_artifact_missing",
			Boundary:        "MANIFEST line whose artifact is missing",
			Seed:            "the sealed artifact is removed and its manifest line left in place (checkpoint/reader_test.go's ErrContract case)",
			Owner:           "internal/checkpoint",
			WantsCheckpoint: true,
			// F4-9's pin is gone: List stats every artifact the manifest claims and Louds the
			// missing one, and Verify Louds it too on the pass that reads it.
			Names: []string{"checkpoint", "manifest", "artifact"},
			Cut:   cutRemoveCheckpointArtifact,
		},
		{
			Name:            "checkpoint_manifest_digest_mismatch",
			Boundary:        "MANIFEST digest mismatch (flip one bit)",
			Seed:            "one byte of the sealed artifact is flipped so it no longer re-hashes to its manifest line (§12.3 \"checkpoint MANIFEST mismatch\")",
			Owner:           "internal/checkpoint",
			WantsCheckpoint: true,
			// F4-9's pin is gone: Verify is no longer silent about a digest that does not match.
			Names: []string{"checkpoint", "manifest", "mismatch", "digest"},
			Cut:   cutFlipCheckpointBit,
		},
		{
			Name:     "wal_segment_truncated",
			Boundary: "WAL segment truncated mid-record",
			Seed: "a spool/wal-*.ndjson segment is truncated inside a record, the shape " +
				"daemon/drain.go answers with DrainGapCorruptLine",
			Owner:               "internal/daemon",
			WantsUndrainedSpool: true,
			Names:               []string{"drain", "spool", "wal"},
			Cut:                 cutTruncateWAL,
		},
		{
			Name:                "client_spool_truncated",
			Boundary:            "client spool truncated",
			Seed:                "a spool/client-*.ndjson file is truncated inside a record",
			Owner:               "internal/daemon + internal/ipc",
			WantsUndrainedSpool: true,
			Names:               []string{"drain", "spool", "client"},
			Cut:                 cutTruncateClientSpool,
		},
		{
			Name:     "stale_drain_progress",
			Boundary: "stale drain.json",
			Seed: "state/drain.json is rewritten to claim durable bytes the spool no longer has, " +
				"the wedge daemon/drain_stale_progress_test.go cuts at ten points in process",
			Owner:               "internal/daemon",
			WantsUndrainedSpool: true,
			Names:               []string{"drain", "spool", "progress"},
			Cut:                 cutStaleDrainProgress,
		},
		{
			Name:     "delivery_seal_torn_slot",
			Boundary: "delivery seal with one torn slot, and a half-present seal pair",
			Seed: "the delivery position files are torn the way daemon/delivery_seal_test.go's " +
				"crash-reachable images are, then one side of the pair is removed entirely " +
				"(delivery_seal_tool.go calls a half-present pair \"a recovery decision, not a repair\")",
			Owner: "internal/daemon",
			// Not `ack` and not `lease`: the first is a substring of "qompack" and matched every
			// day-log line in the run, the second of "release". The tokens here are the two seal
			// files by name and the two counters whose spelling only a seal failure produces.
			Names: []string{
				"delivery", "seal", "delivery-lease-position", "delivery-ack-position",
				"unleased", "unacknowledged",
			},
			Cut: cutTearDeliverySeal,
		},
		{
			Name:     "retention_roots_truncated",
			Boundary: "retention-roots file truncated",
			Seed: "state/retention-roots.jsonl is truncated mid-record, then a forced GC pass is run " +
				"to measure what that actually costs (declaredRetentionLine retains everything on a " +
				"line it cannot parse; only an in-process source failure stops a pass)",
			Owner: "internal/store",
			// F4-5's pin is gone: an unreadable retention line Louds once per GC pass and says that
			// everything it names is retained under the blanket rollback class. The over-retention
			// itself is unchanged and is still what the row measures.
			Names:   []string{"retention", "retention-roots.jsonl", "collect"},
			Cut:     cutTruncateRetentionRoots,
			Measure: measureForcedGC,
		},
		{
			Name:     "state_bin_corrupt",
			Boundary: "state.bin corrupt",
			Seed: "the `state-corrupt` QOMPACK_FAULT site, which writes 32 random bytes into " +
				"run/state.bin from a real hook process (internal/cli/fault.go)",
			Owner: "internal/cli + internal/ipc",
			// NOT `contract`, `degrad` or `passive`. The recovery session emits `contract:
			// degrading to passive recording … sentinel not found after two chances` whether or
			// not anything was cut — lifecycle_frontier_state_is_explicit, which cuts nothing,
			// carries the identical line five times. Crediting it here credits the run, not the
			// cut; the Note below says what the row can and cannot claim about it.
			Names: []string{"state", "state.bin"},
			Cut:   cutCorruptStateBin,
			Note: "run/state.bin holds the host-contract observations, so the `contract: " +
				"degrading to passive recording` LOUD line this session emits is a PLAUSIBLE " +
				"consequence of the corruption — and this row did not establish that it is one. " +
				"The identical line appears in lifecycle_frontier_state_is_explicit, which cuts " +
				"nothing at all. The chain (state.bin corrupt → the contract observation is lost " +
				"→ the self-check fails → passive) is read off the message text; what was " +
				"MEASURED is that the cut left no dangling reference and recording resumed.",
		},
		{
			Name:     "config_json_corrupt",
			Boundary: "config.json corrupt",
			Seed: "the `config-corrupt` QOMPACK_FAULT site, which writes truncated JSON into " +
				".qompack/config.json from a real hook process (internal/cli/fault.go)",
			Owner: "internal/cli + internal/config",
			Names: []string{"config"},
			Cut:   cutCorruptConfig,
		},
	}
}

// ensureCheckpoint makes sure the project holds at least one sealed checkpoint artifact with its
// MANIFEST line, and returns prose for the record saying how it got there.
//
// It asks the product first: a real PreCompact through the installed binary. Whether that seals an
// artifact depends on the daemon's mode and on the cadence thresholds §8.5 sets, neither of which a
// single seeded session controls — so when the hook sealed nothing, the artifact is written through
// the product's OWN writers (checkpoint.Marshal, paths.CreateNew, paths.AppendManifest, which
// IsProtected makes the only legal manifest writer) and the record says so. A synthesized artifact
// is honest evidence of the checkpoint boundary; a synthesized artifact presented as a sealed one
// would not be.
func ensureCheckpoint(t *testing.T, b bundle, p project, sess core.SessionID) string {
	t.Helper()

	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, "auto"))
	shutdownIfReachable(t, p.Root)

	if n := len(checkpointArtifacts(p.Root)); n > 0 {
		return fmt.Sprintf("a real PreCompact through the installed binary sealed %d artifact(s)", n)
	}
	writeSyntheticCheckpoint(t, p.Root, sess)
	return "the PreCompact hook sealed nothing on this host (the cadence thresholds of §8.5 are not " +
		"reached by one seeded session), so the artifact was written through the product's own " +
		"writers: checkpoint.Marshal + paths.CreateNew + paths.AppendManifest"
}
