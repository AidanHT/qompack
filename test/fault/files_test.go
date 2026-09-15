package fault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The file mechanics every cut shares. They are deliberately blunt — overwrite, truncate, list —
// because a cut's value is that it leaves the product's files in a state a crash could have left
// them in, and anything cleverer would be simulating recovery rather than testing it.

// longPath is paths.Long under a shorter name, since every line here uses it. Windows refuses a
// path past 260 characters through the ordinary API, and a temp project inside a temp bundle inside
// a temp directory reaches that sooner than it looks.
func longPath(p string) string { return paths.Long(p) }

// writeOver replaces a file's contents, restoring a read-only mode first if one is set. Every write
// here is to a file under a fixture's own `.qompack/`, never to the repository's.
func writeOver(t *testing.T, path string, body []byte) {
	t.Helper()
	_ = os.Chmod(longPath(path), 0o600)
	if err := os.WriteFile(longPath(path), body, 0o600); err != nil {
		t.Fatalf("fault: overwriting %s: %v", path, err)
	}
}

// sidecarPaths lists every capture sidecar under records/captures/, in a stable order.
func sidecarPaths(root string) []string {
	dir := filepath.Join(paths.Of(root).Records, "captures")
	var out []string
	_ = filepath.WalkDir(longPath(dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out
}

// checkpointArtifacts lists the sealed checkpoint artifacts, ascending. MANIFEST.jsonl is not one:
// it ends in .jsonl and it is the index rather than an artifact.
func checkpointArtifacts(root string) []string {
	var out []string
	for _, name := range relativeFiles(paths.Of(root).Checkpoints) {
		if strings.HasSuffix(name, ".json") && !strings.EqualFold(name, "MANIFEST.jsonl") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// firstCheckpointArtifact returns the full path of the lowest-numbered artifact.
func firstCheckpointArtifact(root string) (string, bool) {
	names := checkpointArtifacts(root)
	if len(names) == 0 {
		return "", false
	}
	return filepath.Join(paths.Of(root).Checkpoints, names[0]), true
}

// highestCheckpointSeq returns the largest sequence number an artifact on disk carries, or zero.
func highestCheckpointSeq(root string) core.CheckpointSeq {
	var high core.CheckpointSeq
	for _, name := range checkpointArtifacts(root) {
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		if seq := core.CheckpointSeq(n); seq > high {
			high = seq
		}
	}
	return high
}

// syntheticCheckpointCreated is the Created stamp a synthesized artifact carries. It is a fixed
// RFC 3339 string rather than time.Now so that two runs' artifacts differ only where the case made
// them differ — and so that nothing here reads as a measurement of when anything happened.
const syntheticCheckpointCreated = "2026-01-01T00:00:00.000Z"

// writeSyntheticCheckpoint seals one checkpoint through the product's own writers, for the rows
// whose boundary is the checkpoint tier on a host where one seeded session does not reach §8.5's
// cadence thresholds.
//
// It goes through checkpoint.Marshal (the canonical bytes the manifest digest is taken over),
// paths.CreateNew (which chmods the artifact 0o444, the §7.4 immutable tier) and
// paths.AppendManifest (which IsProtected makes the only legal writer of MANIFEST.jsonl). Writing
// the three by hand would produce an artifact the product would reject for reasons that have
// nothing to do with the boundary under test.
func writeSyntheticCheckpoint(t *testing.T, root string, sess core.SessionID) {
	t.Helper()
	l := paths.Of(root)
	seq := highestCheckpointSeq(root) + 1

	body, err := checkpoint.Marshal(checkpoint.Checkpoint{
		Session: sess, Seq: seq, Created: syntheticCheckpointCreated,
		Narrative: "a checkpoint sealed by test/fault so a publication-boundary row has an artifact " +
			"and a MANIFEST line to damage",
	})
	if err != nil {
		t.Fatalf("fault: marshalling a checkpoint: %v", err)
	}
	if err := paths.CreateNew(paths.CheckpointPath(l, seq), body); err != nil {
		t.Fatalf("fault: writing checkpoints/%04d.json: %v", int(seq), err)
	}
	sum := sha256.Sum256(body)
	if err := paths.AppendManifest(l, paths.ManifestEntry{
		Seq: seq, SHA256: core.Hash(sum).String(), Bytes: int64(len(body)),
		Created: core.NowMilli(core.SystemClock()),
	}); err != nil {
		t.Fatalf("fault: appending the MANIFEST.jsonl line for %04d: %v", int(seq), err)
	}
}

// objectFingerprints hashes every file under objects/, so a case can prove that a refused or killed
// reader changed nothing.
func objectFingerprints(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	base := paths.Long(paths.Of(root).Objects)
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out
}

// changedObjects names every object present in before that is now missing or holds different bytes.
//
// It is deliberately one-directional. A daemon coming up under a retrieval may legitimately publish
// something NEW, and failing on that would be failing on the wrong thing; what may never happen is
// an object that was there changing or disappearing.
func changedObjects(after, before map[string]string) []string {
	var out []string
	for rel, sum := range before {
		got, ok := after[rel]
		switch {
		case !ok:
			out = append(out, rel+" (gone)")
		case got != sum:
			out = append(out, rel+" (changed)")
		}
	}
	sort.Strings(out)
	return out
}

// seedUndrainedSpool lives in a test file rather than beside the other session seeds because it
// returns a degradationSnapshot, and that type is declared in evidence_test.go for the guard reason
// stated there: a non-test file may not name a type a test file declares.

// spoolBurstEvents is how many deliveries the undrained-spool seed fires before the cut. It is the
// smallest burst that reliably leaves a WAL segment with more than one record in it, which is what
// a mid-record truncation needs.
const spoolBurstEvents = 6

// seedUndrainedSpool leaves spool bytes on disk by KILLING the daemon rather than shutting it down.
//
// It exists because a graceful shutdown is thorough: the drainer consumes every WAL segment the
// ingest releases and removes it, so a cleanly closed session leaves spool/ empty and the two rows
// that need a spool file to damage would skip on a healthy product. A kill is the state a crash
// actually leaves — a WAL segment the ingest held open, with records below the drain's offset and
// records above it — and it is the only daemon-side cut this package has (the injection switch
// never reaches a daemon).
//
// It returns prose for the record naming what survived, so a row that goes on to skip says which of
// the two it could not find rather than "something was missing".
// It returns the pre-cut degradation baseline as well, taken while the burst daemon is still ALIVE.
// Taking it afterwards would be self-defeating: `status --json` lazily spawns a daemon, and that
// daemon drains and removes the very spool file the row exists to damage — which is exactly what
// happened the first time, and the WAL row skipped itself for want of a file it had just had.
func seedUndrainedSpool(t *testing.T, b bundle, p project, sess core.SessionID) (string, degradationSnapshot) {
	t.Helper()
	name := strings.TrimPrefix(string(sess), "sess-fault-")

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	if !waitDaemonUp(t, p.Root) {
		return "session-start did not bring a daemon up, so nothing was spooled", degradationSnapshot{}
	}
	for i := range spoolBurstEvents {
		writeProjectFile(t, p, fmt.Sprintf("src/burst%02d.ts", i), seedContent("burst", 24))
		runHook(t, b.Bin, p, []string{"observe", "tool"},
			readToolPayload(t, p.Root, sess, toolUseID(name, i+1),
				fmt.Sprintf("src/burst%02d.ts", i), seedContent("burst", 24)))
	}
	// The baseline, while the burst daemon can still answer and before the kill takes the spool's
	// owner away.
	baseline := snapshotDegradation(t, b, p)

	killed := killDaemon(t, p.Root)
	// The killed daemon's lock is still honoured until its heartbeat goes stale; see ageHeartbeat
	// for why that window is advanced rather than waited out.
	ageHeartbeat(t, p.Root)

	wal, client := spoolInventory(p.Root)
	return fmt.Sprintf("a %d-delivery burst was cut by killing the daemon (killed=%v) and the "+
		"heartbeat aged past daemon.staleAfter so the next daemon may reclaim the lock, leaving "+
		"%d wal-* and %d client-* spool file(s)", spoolBurstEvents, killed, wal, client), baseline
}
