package fault

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The cuts. Each one is a forced failure at exactly one publication boundary, applied to a project
// a real seeded session already populated.
//
// Two mechanisms only, and the constraint is structural rather than stylistic: QOMPACK_FAULT is a
// HOOK-side seam that internal/daemon/spawn.go strips from every daemon it spawns, and
// test/guards/faultenv_test.go forbids adding a second one. So a cut is either that seam (the two
// rows that use it say so) or a mutation of the files the product already wrote — the same
// mutations the unit tests named in each row's Seed make in process.

// midRecordBackoff is how many bytes a truncation removes from the end of a file to land INSIDE the
// final record rather than between two of them. A JSONL record's tail is its closing brace and
// newline; removing more than that but less than a whole record is what makes the line unparseable
// instead of merely absent.
const midRecordBackoff = 24

// cutDropLastRootLine removes index/roots.jsonl's last complete line, leaving every object on disk.
// The tool_use record that names the removed root then points at something store.GetRoot cannot
// resolve, which is the publication-order state between "the object is durable" and "the reference
// exists".
func cutDropLastRootLine(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	path := filepath.Join(paths.Of(p.Root).Index, "roots.jsonl")
	lines, err := readJSONLines(path)
	if err != nil || len(lines) < 2 {
		return "", fmt.Sprintf("index/roots.jsonl held %d line(s) after the seeded session; "+
			"this cut needs at least two (err %v)", len(lines), err)
	}
	kept := lines[:len(lines)-1]
	var buf strings.Builder
	for _, l := range kept {
		buf.Write(l)
		buf.WriteString("\n")
	}
	writeOver(t, path, []byte(buf.String()))
	return fmt.Sprintf("removed the last of %d index/roots.jsonl lines; objects untouched", len(lines)), ""
}

// cutTruncateRootsMidRecord truncates index/roots.jsonl inside its final record.
func cutTruncateRootsMidRecord(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	return truncateMidRecord(t, filepath.Join(paths.Of(p.Root).Index, "roots.jsonl"))
}

// cutTruncateToolUseMidRecord truncates index/tool_use.jsonl inside its final record.
func cutTruncateToolUseMidRecord(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	return truncateMidRecord(t, filepath.Join(paths.Of(p.Root).Index, "tool_use.jsonl"))
}

// truncateMidRecord cuts path short by midRecordBackoff bytes past its last newline.
func truncateMidRecord(t *testing.T, path string) (string, string) {
	t.Helper()
	raw, err := paths.ReadFileShared(path)
	if err != nil {
		return "", fmt.Sprintf("%s is not readable after the seeded session: %v", filepath.Base(path), err)
	}
	trimmed := strings.TrimRight(string(raw), "\n")
	if len(trimmed) <= midRecordBackoff {
		return "", fmt.Sprintf("%s held only %d bytes after the seeded session; too small to truncate "+
			"inside a record", filepath.Base(path), len(trimmed))
	}
	cut := trimmed[:len(trimmed)-midRecordBackoff]
	writeOver(t, path, []byte(cut))
	return fmt.Sprintf("truncated %s from %d to %d bytes, inside its final record",
		filepath.Base(path), len(raw), len(cut)), ""
}

// cutUnpublishCaptureSidecar rewrites one capture sidecar back to the state stage 1 leaves: bytes
// retained, no reference joined. store/capture_sidecar.go names this exactly — "a sidecar with
// Published false is a durable capture with no reference yet, which is the exact state a crash
// between publication order's first two stages leaves behind" — and calls it a reportable gap
// rather than a repair.
func cutUnpublishCaptureSidecar(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	found := sidecarPaths(p.Root)
	if len(found) == 0 {
		return "", "the seeded session left no capture sidecar under records/captures/"
	}
	target := found[len(found)-1]
	raw, err := os.ReadFile(longPath(target))
	if err != nil {
		return "", "the capture sidecar could not be read: " + err.Error()
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", "the capture sidecar does not parse: " + err.Error()
	}
	// The REAL stage-1 shape, not a plausible-looking one: published false, tool_use_id absent and
	// the root back to zero. LinkCaptureReference is what writes `root`, and it sets `published`
	// true in the same assignment, so a sidecar carrying a root and published:false is a state the
	// product cannot produce. `bytes_hash` stays — those bytes are durable, and they are what makes
	// this a capture with no reference rather than a capture that never happened.
	doc["published"] = json.RawMessage("false")
	doc["root"] = json.RawMessage(`"` + core.Hash{}.String() + `"`)
	delete(doc, "tool_use_id")
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("fault: remarshalling a capture sidecar: %v", err)
	}
	writeOver(t, target, append(out, '\n'))
	return "rewrote " + filepath.Base(target) + " to the real stage-1 shape: published:false, " +
		"zero root, no tool_use_id, bytes_hash retained", ""
}

// cutOrphanCheckpointArtifact writes a second artifact beside the sealed one with no MANIFEST line.
// finalize.go logs this state Loud when paths.CreateNew succeeded and paths.AppendManifest did not,
// and says "qompack fsck reconciles an orphan by re-hashing it".
func cutOrphanCheckpointArtifact(t *testing.T, _ bundle, p project, sess core.SessionID) (string, string) {
	t.Helper()
	l := paths.Of(p.Root)
	next := highestCheckpointSeq(p.Root) + 1
	body, err := checkpoint.Marshal(checkpoint.Checkpoint{
		Session: sess, Seq: next, Created: syntheticCheckpointCreated,
		Narrative: "an orphan artifact: written to disk, never named in checkpoints/MANIFEST.jsonl",
	})
	if err != nil {
		t.Fatalf("fault: marshalling the orphan checkpoint: %v", err)
	}
	if err := paths.CreateNew(paths.CheckpointPath(l, next), body); err != nil {
		return "", "the orphan artifact could not be created: " + err.Error()
	}
	return fmt.Sprintf("created checkpoints/%04d.json with no MANIFEST.jsonl line", int(next)), ""
}

// cutRemoveCheckpointArtifact deletes a sealed artifact and leaves its manifest line in place: the
// ErrContract case reader.go documents, where "the manifest is the thing that promised the file
// exists and verifies".
func cutRemoveCheckpointArtifact(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	target, ok := firstCheckpointArtifact(p.Root)
	if !ok {
		return "", "the project holds no checkpoint artifact to remove"
	}
	// A sealed checkpoint carries no write bit (paths.CreateNew chmods it 0o444), so the mode is
	// restored before the removal rather than after.
	_ = os.Chmod(longPath(target), 0o600)
	if err := os.Remove(longPath(target)); err != nil {
		return "", "the checkpoint artifact could not be removed: " + err.Error()
	}
	return "removed " + filepath.Base(target) + ", leaving its MANIFEST.jsonl line", ""
}

// cutFlipCheckpointBit flips one bit of a sealed artifact so it no longer re-hashes to its manifest
// line — §12.3's "checkpoint MANIFEST mismatch | Loud, refuse to use the affected checkpoint, fall
// back to its parent, degrade to passive".
func cutFlipCheckpointBit(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	target, ok := firstCheckpointArtifact(p.Root)
	if !ok {
		return "", "the project holds no checkpoint artifact to damage"
	}
	raw, err := os.ReadFile(longPath(target))
	if err != nil || len(raw) == 0 {
		return "", fmt.Sprintf("the checkpoint artifact could not be read (%d bytes, err %v)", len(raw), err)
	}
	// The flipped byte is chosen inside the narrative rather than in the JSON structure, so the
	// artifact still PARSES and the only thing that changed is its digest: that keeps the row about
	// the manifest mismatch instead of about a malformed document.
	idx := len(raw) / 2
	raw[idx] ^= 0x20
	_ = os.Chmod(longPath(target), 0o600)
	writeOver(t, target, raw)
	_ = os.Chmod(longPath(target), 0o444)
	return fmt.Sprintf("flipped one bit at offset %d of %s", idx, filepath.Base(target)), ""
}

// cutTruncateWAL truncates a spool WAL segment inside a record.
func cutTruncateWAL(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	return truncateSpoolFile(t, p.Root, "wal-")
}

// cutTruncateClientSpool truncates a client spool file inside a record.
func cutTruncateClientSpool(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	return truncateSpoolFile(t, p.Root, "client-")
}

// truncateSpoolFile truncates the largest spool file with the given prefix.
func truncateSpoolFile(t *testing.T, root, prefix string) (string, string) {
	t.Helper()
	spool := paths.Of(root).Spool
	var target string
	var size int64
	for _, name := range relativeFiles(spool) {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		fi, err := os.Stat(longPath(filepath.Join(spool, name)))
		if err != nil || fi.Size() <= midRecordBackoff {
			continue
		}
		if fi.Size() > size {
			target, size = filepath.Join(spool, name), fi.Size()
		}
	}
	if target == "" {
		return "", fmt.Sprintf("the seeded session left no %s* spool file large enough to truncate "+
			"inside a record (the drain may have consumed and removed them all)", prefix)
	}
	if err := os.Truncate(longPath(target), size-midRecordBackoff); err != nil {
		return "", "the spool file could not be truncated: " + err.Error()
	}
	return fmt.Sprintf("truncated %s from %d to %d bytes", filepath.Base(target), size, size-midRecordBackoff), ""
}

// cutStaleDrainProgress rewrites state/drain.json to claim a durable bound the spool no longer
// carries: every record's size and offset are doubled, which is the wedge
// daemon/drain_stale_progress_test.go cuts at ten points in process. validateProgress refuses a
// spool whose file is shorter than a bound some pass recorded.
func cutStaleDrainProgress(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	path := filepath.Join(paths.Of(p.Root).State, "drain.json")
	raw, err := paths.ReadFileShared(path)
	if err != nil {
		return "", "state/drain.json does not exist after the seeded session: " + err.Error()
	}
	var state map[string]drainRecord
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", "state/drain.json does not parse: " + err.Error()
	}
	if len(state) == 0 {
		// The drainer persists progress only for files it has actually read, and a daemon killed
		// mid-burst can leave the spool full and the progress document empty. A stale record is
		// then MINTED for each spool file on disk, in the drainer's own on-disk shape — the same
		// shape drain_stale_progress_test.go writes in process — because the boundary under test is
		// "drain.json disagrees with the spool", not "the drainer happened to have written first".
		state = mintDrainRecords(p.Root)
		if len(state) == 0 {
			return "", "state/drain.json records no spool file and no spool file survives on disk, " +
				"so there is no progress document to make stale"
		}
	}
	names := make([]string, 0, len(state))
	for name, rec := range state {
		rec.Size *= 2
		rec.Offset *= 2
		rec.Done = false
		state[name] = rec
		names = append(names, name)
	}
	sort.Strings(names)
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("fault: remarshalling drain.json: %v", err)
	}
	writeOver(t, path, append(out, '\n'))
	return fmt.Sprintf("doubled the recorded size and offset of %d drain record(s): %s",
		len(names), strings.Join(names, ",")), ""
}

// cutTearDeliverySeal damages both halves of the delivery position pair: one file is torn inside a
// slot and the other removed outright, which leaves the half-present pair delivery_seal_tool.go
// refuses to repair because it is "a recovery decision, not a repair".
func cutTearDeliverySeal(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	state := paths.Of(p.Root).State
	lease := filepath.Join(state, "delivery-lease-position.json")
	ack := filepath.Join(state, "delivery-ack-position.json")

	torn, removed := "", ""
	if fi, err := os.Stat(longPath(lease)); err == nil && fi.Size() > midRecordBackoff {
		if err := os.Truncate(longPath(lease), fi.Size()-midRecordBackoff); err == nil {
			torn = fmt.Sprintf("tore %s from %d to %d bytes", filepath.Base(lease), fi.Size(), fi.Size()-midRecordBackoff)
		}
	}
	if _, err := os.Stat(longPath(ack)); err == nil {
		if err := os.Remove(longPath(ack)); err == nil {
			removed = "removed " + filepath.Base(ack) + ", leaving a half-present pair"
		}
	}
	if torn == "" && removed == "" {
		return "", "the seeded session wrote neither delivery position file; there was no seal to tear"
	}
	return strings.TrimPrefix(torn+"; "+removed, "; "), ""
}

// cutTruncateRetentionRoots truncates state/retention-roots.jsonl inside a record. gcrun.go's
// RetentionRootsError makes an unreadable set collect NOTHING at all, which is the safe direction
// and also the one that goes unnoticed.
func cutTruncateRetentionRoots(t *testing.T, _ bundle, p project, _ core.SessionID) (string, string) {
	t.Helper()
	path := store.RetentionRootsPath(p.Root)
	if fi, err := os.Stat(longPath(path)); err != nil || fi.Size() <= midRecordBackoff {
		// The file is optional — "a missing file means the producer has not shipped, never that
		// collection is unsafe" — so one is planted through the product's own AppendRetentionRoot
		// before it is torn, rather than skipping a row the brief names.
		if err := seedRetentionRoot(p.Root); err != nil {
			return "", "no retention-roots.jsonl existed and one could not be appended: " + err.Error()
		}
	}
	return truncateMidRecord(t, path)
}

// seedRetentionRoot appends one retention root through store's own exported writer, so the file the
// cut tears is in the format the product reads.
func seedRetentionRoot(root string) error {
	h := core.HashBytes("qompack.chunk", []byte("a retention root that names bytes nobody stored"))
	return store.AppendRetentionRoot(root, store.RetentionRoot{
		Hash: h, Class: store.RetentionEvidence, Reason: "test/fault seeded this root to tear the file",
	})
}

// cutCorruptStateBin drives a hook with the `state-corrupt` QOMPACK_FAULT site, which writes 32
// random bytes into run/state.bin from inside a real hook process (internal/cli/fault.go:256-278).
// This is one of the two rows whose cut is the seam rather than a file mutation, and the seam is
// hook-side by construction: the daemon never sees the variable.
func cutCorruptStateBin(t *testing.T, b bundle, p project, sess core.SessionID) (string, string) {
	t.Helper()
	before, _ := os.Stat(longPath(ipc.StatePath(p.Root)))
	runHookWithEnv(t, b.Bin, p, []string{"observe", "prompt"},
		promptPayload(t, p.Root, sess, "a prompt delivered under the state-corrupt fault site"),
		map[string]string{"QOMPACK_FAULT": "state-corrupt"})
	shutdownIfReachable(t, p.Root)
	after, err := os.Stat(longPath(ipc.StatePath(p.Root)))
	if err != nil {
		return "the state-corrupt site removed run/state.bin entirely", ""
	}
	if before != nil && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
		return "", "the state-corrupt fault site did not change run/state.bin on this host " +
			"(the site no-ops when the resolved root is absent or not a directory)"
	}
	return fmt.Sprintf("the state-corrupt site rewrote run/state.bin (%d bytes)", after.Size()), ""
}

// cutCorruptConfig drives a hook with the `config-corrupt` site, which writes truncated JSON into
// .qompack/config.json (internal/cli/fault.go:286-296).
func cutCorruptConfig(t *testing.T, b bundle, p project, sess core.SessionID) (string, string) {
	t.Helper()
	cfgPath := filepath.Join(paths.Of(p.Root).Dot, "config.json")
	runHookWithEnv(t, b.Bin, p, []string{"observe", "prompt"},
		promptPayload(t, p.Root, sess, "a prompt delivered under the config-corrupt fault site"),
		map[string]string{"QOMPACK_FAULT": "config-corrupt"})
	shutdownIfReachable(t, p.Root)
	raw, err := os.ReadFile(longPath(cfgPath))
	if err != nil {
		return "", "the config-corrupt fault site wrote no .qompack/config.json on this host: " + err.Error()
	}
	if json.Valid(raw) {
		return "", "the config-corrupt fault site left .qompack/config.json parseable"
	}
	return fmt.Sprintf(".qompack/config.json holds %d bytes of unparseable JSON", len(raw)), ""
}

// mintDrainRecords builds one drain-progress record per spool file on disk, each claiming to have
// consumed the whole file. Doubling those numbers afterwards is what makes the document name bytes
// the spool does not have — the state validateProgress refuses the spool over.
func mintDrainRecords(root string) map[string]drainRecord {
	out := map[string]drainRecord{}
	spool := paths.Of(root).Spool
	for _, name := range relativeFiles(spool) {
		if !strings.HasPrefix(name, "wal-") && !strings.HasPrefix(name, "client-") {
			continue
		}
		fi, err := os.Stat(longPath(filepath.Join(spool, name)))
		if err != nil || fi.Size() == 0 {
			continue
		}
		out[name] = drainRecord{Size: fi.Size(), Offset: fi.Size(), DurableSize: true}
	}
	return out
}

// measureForcedGC runs the most destructive pass the GC API can express over a project whose
// retention-roots file is torn, and reports what it actually did.
//
// It exists because round 1 asserted the wrong consequence. The claim was that a torn
// retention-roots.jsonl makes GC "collect nothing at all" through gcrun.go's RetentionRootsError;
// the code says otherwise. `declaredRetentionLine` (internal/store/gcrun.go:764-780) treats a line
// it cannot parse as a claim it may not ignore and retains EVERYTHING on that line under the
// blanket RetentionRollback class, and `errRetentionRootsUnavailable` is raised only by in-process
// sources (:784-794), never by a bad file line. So the real consequence is OVER-retention — objects
// held live by a line nobody can read — and it is unreported either way. This measures it instead
// of asserting it: the flag, the deletions, and how many roots were retained under the blanket
// class the unparseable line produces.
func measureForcedGC(t *testing.T, p project) string {
	t.Helper()
	s, err := store.Open(p.Root, config.Defaults(), store.Deps{})
	if err != nil {
		return "a forced GC pass could not run: store.Open refused: " + err.Error()
	}
	defer func() { _ = s.Close() }()

	report, gcErr := s.GC(context.Background(), store.GCPolicy{RetainDays: -1, RetainSessions: -1})
	if gcErr != nil {
		return "a forced GC pass returned an error: " + gcErr.Error()
	}
	blanket := 0
	for _, o := range report.Outcomes {
		if o.Result == store.RootRetained && o.Class == store.RetentionRollback {
			blanket++
		}
	}
	return fmt.Sprintf("a forced GC pass (RetainDays -1, RetainSessions -1) over the torn "+
		"retention-roots.jsonl completed: RetentionRootsError=%v, scanned %d object(s), deleted %d, "+
		"retained %d root(s) of which %d under the blanket `rollback` class that gcrun.go's "+
		"declaredRetentionLine gives an unparseable line. Collection was NOT stopped, so round 1's "+
		"claim that a torn file makes GC collect nothing is refuted by measurement; whether this "+
		"particular torn line also OVER-retained depends on whether its surviving hash-shaped "+
		"tokens name anything live, and the blanket count above is what says so for this fixture",
		report.RetentionRootsError, report.ScannedObjects, report.DeletedObjects,
		report.Retained, blanket)
}
