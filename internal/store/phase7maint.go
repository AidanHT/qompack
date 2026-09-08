package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// This file is SP-16 §3's maintenance half for the artifacts SP-16 itself owns: the demand log and
// the per-segment filters.
//
// §3 asks for four things — "Maintenance has cancellation, quotas, crash recovery and explicit
// expiry; SessionEnd is not its only recovery path" — and one more from commit 6: retire arbitrary
// truncation in favour of explicit overflow. Each is a separate property below, and each has a
// failure mode that shows up only under interruption, which is why they are worth spelling out.
//
// NOTHING IS TRUNCATED. A record survives a compaction whole or is dropped whole, and every drop is
// accounted for in an explicit overflow record appended to the new log. Truncating an observation
// to fit a quota produces a record that parses, reads as data and is wrong; dropping it and saying
// how many were dropped produces a gap the reader can see. The overflow record is a DemandGap, so a
// compacted log's aggregate reports the missing observations as telemetry gaps — the same way it
// reports every other thing nobody could count.
//
// A CANCELLED PASS CHANGES NOTHING. Compaction writes a staging file and swaps it in only after the
// whole pass has completed and been re-read; a cancelled or quota-exhausted pass removes the
// staging file and leaves the original log exactly where it was. There is no state in which some
// records have been dropped and the pass has not finished.
//
// A CRASHED PASS RECOVERS ON THE NEXT ONE. A staging file left by a process that died mid-pass is
// removed at the start of the next pass rather than being resumed: it describes an unknown prefix
// of an unknown input, and the log it was built from is still intact, so rebuilding costs one pass
// and resuming costs correctness. This is what makes SessionEnd not the only recovery path — any
// later pass, from idle or from a fresh session, recovers.
//
// THE OLD LOG IS THE ROLLBACK. paths.WriteAtomic replaces the log in one rename, so a reader either
// sees the whole old log or the whole new one. A verification read of the staging file runs before
// the rename, so a staging file this build cannot read back is abandoned rather than sworn in.

// demandStagingSuffix is appended to the demand log's name for the staging file. It is a sibling
// rather than a file under tmp/ so that the rename which swaps it in stays within one directory,
// which is what makes the swap atomic on every filesystem this runs on.
const demandStagingSuffix = ".compacting"

// DemandCompactOptions bounds one compaction pass.
//
// Every bound is optional and a zero means "unbounded" for that dimension, except ExpireBefore
// where zero means "expire nothing". The asymmetry is deliberate: forgetting a quota costs a slow
// pass, and forgetting an expiry horizon would silently delete evidence.
type DemandCompactOptions struct {
	// ExpireBefore drops every observation older than it. Zero expires nothing, so a caller that
	// forgets it compacts without deleting.
	ExpireBefore core.UnixMilli
	// MaxRecords caps how many records the new log may hold. Records past the cap are dropped
	// whole and counted in an overflow record; zero means no cap.
	MaxRecords int
	// MaxReadRecords caps how many records the pass will READ. It bounds the pass's cost on a log
	// that has grown beyond what one maintenance window can process; reaching it abandons the pass
	// rather than writing a log built from a prefix of the input. Zero means no cap.
	MaxReadRecords int
}

// DemandCompactReport says what a pass did, including when it did nothing.
type DemandCompactReport struct {
	// Ran reports whether the log was actually replaced. False with a nil error means the pass
	// declined — nothing to do, or a bound reached — and the original log is untouched.
	Ran bool
	// Read, Kept and Expired count records. Dropped counts records lost to MaxRecords, which is
	// the only kind of loss that is not an expiry.
	Read, Kept, Expired, Dropped int
	// RecoveredStaging reports that a staging file left by an earlier, interrupted pass was
	// removed before this one started.
	RecoveredStaging bool
	// Declined names why a pass did nothing, for a maintenance manifest. It is empty when Ran.
	Declined string
}

// CompactDemandLog rewrites the demand log under opt, dropping expired observations and bounding
// the result.
//
// The pass is all-or-nothing. It reads the whole log into a staging file, verifies the staging file
// by reading it back, and only then replaces the original in one rename. A cancelled context, a
// read bound, or a verification failure all leave the original log exactly as it was and report
// what happened in Declined.
func CompactDemandLog(ctx context.Context, l *DemandLog, opt DemandCompactOptions) (DemandCompactReport, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var rep DemandCompactReport
	staging := l.path + demandStagingSuffix

	// Crash recovery, before anything else: a staging file here was left by a process that died
	// mid-pass. It describes an unknown prefix of an unknown input, and the log it was built from
	// is still intact, so it is removed rather than resumed.
	if _, err := os.Stat(paths.Long(staging)); err == nil {
		if rmErr := os.Remove(paths.Long(staging)); rmErr != nil {
			return rep, fmt.Errorf("store: removing a stale demand staging file %s: %w", staging, rmErr)
		}
		rep.RecoveredStaging = true
	}

	src, err := paths.OpenShared(l.path)
	if err != nil {
		rep.Declined = "no demand log to compact"
		return rep, nil
	}
	kept, rep2, err := readForCompaction(ctx, src, opt, rep)
	_ = src.Close()
	rep = rep2
	if err != nil || rep.Declined != "" {
		return rep, err
	}
	if rep.Expired == 0 && rep.Dropped == 0 {
		rep.Declined = "nothing to expire or drop"
		return rep, nil
	}

	if rep.Dropped > 0 {
		// The overflow record. It is a DemandGap so that a compacted log's aggregate reports the
		// loss as missing telemetry rather than as an absence of demand — the reader learns that
		// observations existed and were not kept, which is the whole difference between a bounded
		// log and a lying one.
		kept = append(kept, DemandObservation{
			V:    demandRecordVersion,
			Kind: DemandGap,
			Key:  "",
			TS:   opt.ExpireBefore,
		})
	}

	if err := writeDemandRecords(staging, kept); err != nil {
		_ = os.Remove(paths.Long(staging))
		return rep, err
	}
	if err := verifyDemandStaging(staging, len(kept)); err != nil {
		_ = os.Remove(paths.Long(staging))
		rep.Declined = "the staging file did not read back"
		return rep, err
	}
	if err := os.Rename(paths.Long(staging), paths.Long(l.path)); err != nil {
		_ = os.Remove(paths.Long(staging))
		return rep, fmt.Errorf("store: swapping in the compacted demand log: %w", err)
	}
	rep.Ran = true
	return rep, nil
}

// readForCompaction reads src and returns the records to keep, updating rep.
//
// A line that will not parse is DROPPED rather than kept: compaction is the one pass that may
// remove it, because the alternative is carrying an unreadable line forward forever. It is counted
// against Expired, which is where an aggregate already reports it as a gap.
func readForCompaction(ctx context.Context, src *os.File, opt DemandCompactOptions, rep DemandCompactReport) (
	[]DemandObservation, DemandCompactReport, error,
) {
	var kept []DemandObservation
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), bufio.MaxScanTokenSize)

	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			rep.Declined = "cancelled before the pass completed"
			return nil, rep, err
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		rep.Read++
		if opt.MaxReadRecords > 0 && rep.Read > opt.MaxReadRecords {
			rep.Declined = "the log is longer than this pass may read"
			return nil, rep, nil
		}
		var o DemandObservation
		if err := json.Unmarshal(line, &o); err != nil || o.Key == "" {
			rep.Expired++
			continue
		}
		if opt.ExpireBefore != 0 && o.TS < opt.ExpireBefore {
			rep.Expired++
			continue
		}
		if opt.MaxRecords > 0 && len(kept) >= opt.MaxRecords {
			// Dropped WHOLE, and counted. Nothing here shortens a record to make it fit.
			rep.Dropped++
			continue
		}
		kept = append(kept, o)
	}
	if err := sc.Err(); err != nil {
		rep.Declined = "the log could not be read to the end"
		return nil, rep, fmt.Errorf("store: reading the demand log for compaction: %w", err)
	}
	rep.Kept = len(kept)
	return kept, rep, nil
}

// writeDemandRecords writes recs to p as JSONL, replacing whatever is there.
func writeDemandRecords(p string, recs []DemandObservation) error {
	var buf []byte
	for _, o := range recs {
		b, err := json.Marshal(o)
		if err != nil {
			return fmt.Errorf("store: marshalling a demand record: %w", err)
		}
		buf = append(buf, b...)
		buf = append(buf, '\n')
	}
	if err := paths.WriteAtomic(p, buf, 0o600); err != nil {
		return fmt.Errorf("store: writing the compacted demand log: %w", err)
	}
	return nil
}

// verifyDemandStaging reads the staging file back and checks it holds want records.
//
// It exists because the rename is the point of no return: once the staging file is sworn in, the
// original is gone. Reading it back first is cheap next to a log that parses on write and not on
// read, which is exactly the failure a partial disk write produces.
func verifyDemandStaging(p string, want int) error {
	f, err := paths.OpenShared(p)
	if err != nil {
		return fmt.Errorf("store: re-reading the compacted demand log: %w", err)
	}
	defer func() { _ = f.Close() }()

	got := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), bufio.MaxScanTokenSize)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var o DemandObservation
		if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
			return fmt.Errorf("store: the compacted demand log does not parse: %w", err)
		}
		got++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("store: re-reading the compacted demand log: %w", err)
	}
	if got != want {
		return fmt.Errorf("store: the compacted demand log holds %d records, wrote %d", got, want)
	}
	return nil
}

// SweepSegmentFilters removes filter files that no live segment names, and reports what it found.
//
// It is the other half of §3's maintenance: a republished filter leaves its predecessor behind,
// and a segment that was never given one leaves a file from an abandoned build. Neither is read by
// anything — a reader reaches a filter only through Segment.BloomRef — so both are cost without
// benefit.
//
// The keep set is passed in rather than read from a SegmentLog, so that the sweep cannot delete a
// file on the strength of a log it failed to load. An empty keep set with no error would otherwise
// mean "delete every filter", and that is precisely the shape a transient read failure takes.
func SweepSegmentFilters(ctx context.Context, root string, keep map[string]bool) (SweepReport, error) {
	var rep SweepReport
	if keep == nil {
		return rep, errors.New("store: SweepSegmentFilters needs an explicit keep set, even an empty one")
	}

	dir := paths.Of(root).Sketches
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, fmt.Errorf("store: listing %s: %w", dir, err)
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			rep.Cancelled = true
			return rep, err
		}
		name := e.Name()
		if e.IsDir() || !isSegmentFilterName(name) {
			continue
		}
		rep.Found++
		ref := "sketches/" + name
		if keep[ref] {
			rep.Kept++
			continue
		}
		if err := os.Remove(paths.Long(filepath.Join(dir, name))); err != nil {
			rep.Failed = append(rep.Failed, name)
			continue
		}
		rep.Removed = append(rep.Removed, name)
	}
	sort.Strings(rep.Removed)
	sort.Strings(rep.Failed)
	return rep, nil
}

// SweepReport is one sweep's manifest.
type SweepReport struct {
	// Found is how many segment filter files were seen.
	Found int
	// Kept is how many a live segment still names.
	Kept int
	// Removed lists what was deleted, sorted.
	Removed []string
	// Failed lists files that could not be removed — a reader holding one open on Windows, a
	// permission change. They are reported rather than retried: a file that could not be deleted
	// costs disk, and the next sweep will try again.
	Failed []string
	// Cancelled reports that the sweep stopped early. Everything already removed stays removed;
	// nothing is half-removed, because a delete either happened or did not.
	Cancelled bool
}

// isSegmentFilterName reports whether name looks like a file SegmentFilterRef would produce. It
// matches on the shape rather than by parsing an id, so an unparseable name is left alone: a sweep
// deletes only what it recognises.
func isSegmentFilterName(name string) bool {
	const prefix, suffix = "seg-", ".bloom"
	if len(name) <= len(prefix)+len(suffix) {
		return false
	}
	if name[:len(prefix)] != prefix || name[len(name)-len(suffix):] != suffix {
		return false
	}
	for _, c := range name[len(prefix) : len(name)-len(suffix)] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
