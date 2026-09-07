package checkpoint

import (
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file is deliberately two functions long.
//
// checkpoints/MANIFEST.jsonl is internal/paths' file, not this package's. SP-01 already ships the
// whole manifest layer — paths.ManifestEntry, paths.AppendManifest, paths.ReadManifest,
// paths.ManifestPath and paths.CheckpointPath — and paths.IsProtected's doc states that
// MANIFEST.jsonl lives under checkpoints/ deliberately, so that AppendManifest is the only
// function that can ever write it. This package therefore declares no manifest record type, no
// appendManifest and no readManifest: two writers with two `created` encodings on one append-only
// file is a wire-format fork that `qompack fsck` hits first (00-ARCHITECTURE.md §3.3).
//
// Note in particular that paths.ManifestEntry.Created is a core.UnixMilli NUMBER while
// Checkpoint.Created is an RFC 3339 string. They are different fields on different files and are
// populated independently; conflating them makes every manifest this package writes unreadable by
// the package that owns it.

// maxSeq returns the largest Seq recorded in checkpoints/MANIFEST.jsonl, and 0 when the manifest
// does not exist yet — core.CheckpointSeq's documented "none", so `maxSeq(l) + 1` yields 1 for the
// first checkpoint of a project without a special case. It takes the largest Seq rather than the
// last line's, because nothing in internal/paths orders an append-only file and a re-recorded
// older seq must never make the next Finalize reuse a filename.
//
// The two counters the manifest layer owns.
//
// checkpoint.manifest_badline counts manifests that could not be read at all — paths.ReadManifest
// hard-errors on a line it cannot decode rather than skipping it, and so does this package.
// checkpoint.manifest_mismatch counts artifacts whose bytes no longer hash to the digest
// checkpoints/MANIFEST.jsonl recorded, which is §12's "refuse to use the affected checkpoint".
const (
	metricManifestBadline  = "checkpoint.manifest_badline"
	metricManifestMismatch = "checkpoint.manifest_mismatch"
)

// An unreadable manifest is surfaced through the observers rather than through a return value:
// the signature this subplan fixes (plans/V4-SP-10-checkpointer-l4.md §12) has no error channel,
// and its one call site is spelled `seq = maxSeq(l) + 1` (§9 step 3). So a paths.ReadManifest
// failure increments checkpoint.manifest_badline and logs one Loud, and the 0 that comes back
// makes Finalize attempt seq 1, where paths.CreateNew's O_EXCL refuses to overwrite any artifact
// that already exists. The read path does have an error channel and uses it: fileReader.manifest
// returns the same condition wrapped in core.ErrContract, which is what List, Get, Latest, Chain
// and Verify surface.
func maxSeq(l paths.Layout) core.CheckpointSeq {
	entries, err := paths.ReadManifest(l)
	if err != nil {
		pkgMetrics().Counter(metricManifestBadline).Add(1)
		pkgLog().Loud("checkpoint manifest unreadable",
			"path", paths.ManifestPath(l), "detail", err.Error())
		return 0
	}

	var highest core.CheckpointSeq
	for _, e := range entries {
		if e.Seq > highest {
			highest = e.Seq
		}
	}
	return highest
}

// seqFromFilename parses a checkpoint artifact's own filename — "0006.json" → 6 — and is what
// Chain follows Checkpoint.Parent with.
//
// There is deliberately no matching checkpoint.Filename. The %04d.json pattern has exactly one
// owner, internal/paths, and a checkpoint's own filename is
// filepath.Base(paths.CheckpointPath(l, seq)); a second renderer here would be free to drift from
// the one the artifacts are actually written under.
//
// It is strict on purpose. A Parent naming a directory ("../0006.json", "sub/0006.json") would
// let a hand-edited or corrupted artifact walk Chain out of the checkpoints directory, and a
// Parent that is not a positive decimal seq cannot address an artifact at all, so both are
// reported as core.ErrContract rather than silently shortening the chain.
func seqFromFilename(name string) (core.CheckpointSeq, error) {
	bad := func() (core.CheckpointSeq, error) {
		return 0, fmt.Errorf("checkpoint: %q is not a checkpoint filename: %w", name, core.ErrContract)
	}

	// ".json" is spelled here rather than derived from paths.CheckpointPath because this is a
	// parser, not a renderer; the round-trip against the renderer is asserted in manifest_test.go.
	digits, ok := strings.CutSuffix(name, ".json")
	if !ok || digits == "" || strings.ContainsAny(name, `/\`) {
		return bad()
	}

	seq := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return bad()
		}
		seq = seq*10 + int(r-'0')
		if seq > maxParsableSeq {
			return bad()
		}
	}
	if seq < 1 {
		return bad()
	}
	return core.CheckpointSeq(seq), nil
}

// maxParsableSeq bounds seqFromFilename's accumulator so a filename of a million digits cannot
// overflow it into a plausible-looking small sequence number. It is far above any sequence a
// project can reach — one checkpoint per PreCompact, one PreCompact per compaction — and is a
// parser guard, not an Appendix-C-owned value.
const maxParsableSeq = 1 << 30
