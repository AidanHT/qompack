package fault

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// Unavailable historical objects (deliverable 6): bytes that WERE captured and are not reachable
// now.
//
// The distinction the whole row turns on is §7.1's: `unavailable` says the address was real and the
// bytes are gone, `miss` and `absent` say nothing was ever there. Collapsing the two is how a
// retrieval layer comes to promise exactness it cannot deliver, which is what §12 forbids in so
// many words. Task 3 already measured this seam and returned S-3 — quarantined objects surface as
// tool errors or `miss` through MCP rather than `unavailable`, owner internal/mcp — so this row
// reproduces that finding on damaged (rather than quarantined) bytes rather than discovering it.

// TestFault_UnavailableHistoricalObject damages an object after it was captured and after a
// checkpoint referenced it, then asks four questions: what `expand` answers, what a checkpoint
// finalize does with the pointer, whether a forced GC survives it, and whether the audit can name
// the reference as reported.
func TestFault_UnavailableHistoricalObject(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "historical_object_unavailable")
	rec.Phase = PhaseHistorical
	rec.Boundary = "an object present at capture, damaged afterwards, asked for through the " +
		"installed MCP server"
	rec.SeedMethod = "a real session captures a file through the installed binary; one of the " +
		"stored object files is then truncated in half — §12.3's \"store corrupt (bad CRC, " +
		"truncated object)\" — and the retrieval is driven through `<bundle>/bin/qompack mcp`"

	sess := sessionID("historical")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	// The address to ask about, and the object behind it.
	target, ok := firstIndexedToolUse(p.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded session left no tool_use record to ask about")
		return
	}
	chunk, ok := firstChunkOf(t, p.Root, target.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded root "+shortHash(target.Root)+" names no chunk on disk")
		return
	}

	before := auditProject(t, p.Root)
	damaged := damageObject(t, p.Root, chunk)
	if damaged == "" {
		skipRecorded(t, rec, "the object file for chunk "+shortHash(chunk.String())+" is not on disk "+
			"in a form this row can damage")
		return
	}

	// (1) What `expand` answers for an address that was real.
	c := startMCP(t, b.Bin, p)
	t.Cleanup(func() { c.stop(t) })
	c.handshake(t)
	byHash, hashErr := c.call(t, "expand", map[string]any{"hash": target.Root})
	byID, idErr := c.call(t, "expand", map[string]any{"tool_use_id": target.ID})
	c.stop(t)
	shutdownIfReachable(t, p.Root)

	// (2) Whether the damaged object was quarantined rather than silently dropped, which is §12.3's
	// own recovery: "quarantine the object to .qompack/tmp/quarantine/, Loud, continue".
	quarantined := quarantineHolds(p.Root, chunk)

	// (3) A forced GC pass — negative retention on both axes, the form `fsck --gc-all` uses — must
	// not crash and must not collect the quarantine evidence.
	gcDetail, quarantineKept := forcedGCKeepsEvidence(t, p.Root)

	after := auditProject(t, p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	rec.Detail = fmt.Sprintf(
		"damaged: %s\nexpand(hash=%s): %s\nexpand(tool_use_id=%s): %s\nquarantined: %v\n%s\n"+
			"audit before %s; after %s\nnamed as reported: %s",
		damaged, shortHash(target.Root), describeEnvelope(byHash, hashErr),
		target.ID, describeEnvelope(byID, idErr), quarantined, gcDetail,
		before, after, describeRefs(after.Reported))

	if !quarantineKept {
		recordOutcome(t, rec, OutcomeFailed, "a forced GC pass removed the quarantine evidence a "+
			"damaged object left behind, which plan §3 forbids in so many words. Owner: internal/store.")
		t.Errorf("fault %s: forced GC collected diagnostic evidence\n%s", rec.Name, rec.Detail)
		return
	}

	// The envelope question. `unavailable` anywhere in either answer is the right shape; a tool
	// error or a `miss` is S-3, already returned.
	saysUnavailable := mentionsUnavailable(byHash, hashErr) || mentionsUnavailable(byID, idErr)
	switch {
	case saysUnavailable:
		recordOutcome(t, rec, OutcomeExplicitIncomplete, "the retrieval layer answered `unavailable` "+
			"for an address whose bytes are gone rather than reporting a miss, the damaged object was "+
			"quarantined as evidence, and a forced GC pass left that evidence in place")
	default:
		recordOutcome(t, rec, OutcomeFailed, "a damaged historical object reaches the host as a tool "+
			"error or a miss rather than as `unavailable`, so a real address that lost its bytes is "+
			"indistinguishable from one that never existed (§7.1). Owner: internal/mcp. Returned as "+
			"S-3 (Task 3); this row reproduces it on damaged rather than quarantined bytes.")
		t.Logf("fault %s: reproduced the returned finding S-3 (owner internal/mcp); the record is "+
			"the deliverable and nothing here fixes it", rec.Name)
	}
}

// TestFault_CheckpointDropsAnUnresolvablePointer asks whether a checkpoint written AFTER an object
// went away carries an explicit DropEntry rather than a pointer to nothing.
//
// finalize.go's keepResolvableTools is the code under test, and it is driven here through the
// installed binary's PreCompact hook rather than in process.
//
// The answer to the checkpoint's own question turned out to be "it holds no pointer into gone
// bytes", but the row does NOT stop there and it does not decide its own outcome any more. It used
// to record `recovered` with two dangling references and nothing reporting them, which is what M16
// exists to forbid — so the last branch hands the standard judge the same before/after audit, the
// same measured `recording` and the same named evidence every publication row gets.
func TestFault_CheckpointDropsAnUnresolvablePointer(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "historical_checkpoint_drops_pointer")
	rec.Phase = PhaseHistorical
	rec.Boundary = "a checkpoint written after the object one of its pointers names went away"
	rec.SeedMethod = "a real session, one stored object removed outright, then a real PreCompact " +
		"through the installed binary, then one more observation so the judge has a measured " +
		"\"did recording go on\" rather than an assumed one"

	sess := sessionID("historical-drop")
	seedSession(t, b, p, sess)
	baseline := snapshotDegradation(t, b, p)
	shutdownIfReachable(t, p.Root)

	target, ok := firstIndexedToolUse(p.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded session left no tool_use record to point at")
		return
	}
	chunk, ok := firstChunkOf(t, p.Root, target.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded root names no chunk on disk")
		return
	}

	before := auditProject(t, p.Root)
	artifactsBefore := len(checkpointArtifacts(p.Root))
	removed := removeObject(t, p.Root, chunk)
	if removed == "" {
		skipRecorded(t, rec, "the object file could not be removed")
		return
	}

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "resume"))
	up := waitDaemonUp(t, p.Root)
	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, "manual"))

	// One observation AFTER the seal, so the standard judge below has a MEASURED "did the product go
	// on recording" rather than an assumed one. It lands after the checkpoint on purpose: the
	// artifact's pointer set is what this row is about and must not change underneath it.
	recoveryID := toolUseID("historical-drop", 3)
	writeProjectFile(t, p, "src/gamma.ts", seedContent("gamma", 40))
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, recoveryID, "src/gamma.ts", seedContent("gamma", 40)))
	recording := up && waitIndexed(t, p.Root, recoveryID, indexBound)

	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, sess))
	all := degradationSince(baseline, snapshotDegradation(t, b, p))
	shutdownIfReachable(t, p.Root)

	after := auditProject(t, p.Root)
	pointers := checkpointPointerCount(t, p.Root)
	sealed := len(checkpointArtifacts(p.Root)) - artifactsBefore
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)

	// Only the drops this PreCompact ADDED count, and only if one of them names the pointer the cut
	// took away. Round 1 read the absolute drop set and passed on `pointer_git_unavailable()` — a
	// git-provenance drop the seeded checkpoint already carried, which names nothing about the
	// removed object. A drop that does not name the cut is not this row's evidence.
	cutNames := append(append([]string{}, checkpointDropNames...),
		target.ID, shortHash(chunk.String()))
	ev := namingEvidence(t, all, cutNames)
	naming := dropsNaming(all.Drops, target.ID, shortHash(chunk.String()))
	rec.Detail = fmt.Sprintf("removed: %s\nartifacts %d -> %d (sealed %d); pointers across them: %d"+
		"\nnew drop entries: %v, of which naming the cut: %v"+
		"\nrecovery observation %s indexed: %v"+
		"\neverything the surfaces gained: %s\nof which naming this cut (%s): %s",
		removed, artifactsBefore, artifactsBefore+sealed, sealed, pointers, all.Drops, naming,
		recoveryID, recording, all, strings.Join(cutNames, ", "), ev)
	auditLine := fmt.Sprintf("\naudit before %s; after %s", before, after)

	danglingPointers := refsOfKind(after.Dangling, refCheckpointPointer)
	switch {
	case sealed == 0:
		rec.Detail += auditLine
		skipRecorded(t, rec, "the PreCompact sealed no new checkpoint artifact on this host (the "+
			"§8.5 cadence thresholds are not reached by one seeded session), so no finalize pass "+
			"ran over the removed object and there was nothing for this row to observe")
	case len(danglingPointers) > 0:
		rec.Detail += auditLine
		recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf("a checkpoint sealed after the object went "+
			"away carries %d pointer(s) into bytes that are gone, with no DropEntry naming them: %s. "+
			"Owner: internal/checkpoint.", len(danglingPointers), describeRefs(danglingPointers)))
		t.Errorf("fault %s: a checkpoint points at nothing and does not say so\n%s", rec.Name, rec.Detail)
	case len(naming) > 0:
		rec.Detail += auditLine
		recordOutcome(t, rec, OutcomeExplicitIncomplete, "the sealed checkpoint carries no pointer "+
			"into bytes that are gone, and it names what it left out in a drop entry that identifies "+
			"the cut: "+strings.Join(naming, ", "))
	case pointers == 0:
		rec.Detail += auditLine
		skipRecorded(t, rec, "the sealed checkpoint carries no file or tool pointer at all, so "+
			"\"no pointer into bytes that are gone\" is true of a document that points at nothing; "+
			"this host gave the row nothing to measure")
	default:
		// The standard judge, not a verdict of this row's own. The previous round recorded `recovered`
		// here with `dangling_after: 2` and `reported: 0`, which is exactly what M16 exists to forbid:
		// the sealed checkpoint holds no pointer into gone bytes, but the index that outlived the
		// removed object still names it and no surface says so.
		//
		// The pin is F4-1, mirror shape, not F4-8. F4-8 was the checkpoint predicate's blindness
		// and it is FIXED — finalize decides with store.ObjectPresence.ObjectOnDisk now, and
		// TestFault_CheckpointResolvabilityIsBlindToADeletedObject is its regression assertion.
		// F4-1 as recorded is index line gone, object present; this row's residue is the mirror
		// (object gone, index line present). Both are index/objects disagreements whose surface
		// is `qompack fsck`'s objects/roots walk. What this branch is left holding is the OTHER
		// half the row always measured and the one that is ruled out of Task 6's scope:
		// index/roots.jsonl goes on naming bytes that are gone, and no surface says so. That is
		// F4-1, owner internal/store.
		//
		// Which references those are is left to the "newly dangling" line judgeRecovery writes,
		// rather than asserted here: naming them in advance is how the subagent row came to claim a
		// tool_use record that did exist.
		rec.Detail += "\nnote: the sealed checkpoint's OWN pointers all resolve on disk, so the " +
			"references listed as newly dangling below are not the checkpoint's — they are what the " +
			"removed object left behind in the index that outlived it. This row cannot distinguish " +
			"finalize having DROPPED an unresolvable pointer from the pointer never having been a " +
			"candidate: no drop entry names the removed object either way."
		judgeRecovery(t, rec, before, after, recording, ev,
			"internal/store (the index keeps naming bytes that are gone)", "F4-1")
	}
}

// checkpointDropNames are the tokens, other than the cut's own identifiers, that would constitute
// the product reporting an object that went away under a live index: §12.3's quarantine answer and
// the two ways a reference into absent bytes gets named.
//
// `unavailable` is deliberately NOT here, and the reason is the trap C2 fixed one level up: the
// seeded checkpoint emits `pointer_git_unavailable()` whenever it seals, a git-provenance drop that
// names nothing about a removed object, and a token of "unavailable" would take it as this cut being
// reported. The identifiers this row appends — the tool_use id and the chunk's short hash — are what
// a report of THIS cut would have to carry.
var checkpointDropNames = registerNames("historical_checkpoint_drops_pointer",
	[]string{"quarantin", "materialize", "dangling", "missing object"})

// dropsNaming filters drop entries to those that identify one of the given tokens. A drop that names
// nothing about the cut is not evidence that the cut was noticed.
func dropsNaming(drops []string, tokens ...string) []string {
	var out []string
	for _, d := range drops {
		for _, tok := range tokens {
			if tok != "" && strings.Contains(d, tok) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// firstIndexedToolUse returns the first tool_use record with a non-zero root.
func firstIndexedToolUse(root string) (toolUseLine, bool) {
	lines, err := readJSONLines(filepath.Join(paths.Of(root).Index, "tool_use.jsonl"))
	if err != nil {
		return toolUseLine{}, false
	}
	for _, raw := range lines {
		var tu toolUseLine
		if json.Unmarshal(raw, &tu) != nil {
			continue
		}
		if tu.Root != "" && !isZeroHash(tu.Root) {
			return tu, true
		}
	}
	return toolUseLine{}, false
}

// firstChunkOf returns the first chunk hash the given root names, read through the product's own
// store rather than out of the index file, so the row damages a chunk the reader would actually go
// looking for.
func firstChunkOf(t *testing.T, root, rootHash string) (core.Hash, bool) {
	t.Helper()
	h, err := core.ParseHash(rootHash)
	if err != nil {
		return core.Hash{}, false
	}
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	if err != nil {
		return core.Hash{}, false
	}
	defer func() { _ = s.Close() }()
	r, err := s.GetRoot(context.Background(), h)
	if err != nil || len(r.Chunks) == 0 {
		return core.Hash{}, false
	}
	return r.Chunks[0].Hash, true
}

// damageObject truncates a stored object in half, the shape §12.3 calls "store corrupt (bad CRC,
// truncated object)" and answers with a quarantine.
func damageObject(t *testing.T, root string, h core.Hash) string {
	t.Helper()
	p, ok := objectFilePath(root, h)
	if !ok {
		return ""
	}
	raw, err := os.ReadFile(paths.Long(p))
	if err != nil || len(raw) < 2 {
		return ""
	}
	writeOver(t, p, raw[:len(raw)/2])
	return fmt.Sprintf("truncated the object file for chunk %s from %d to %d bytes",
		shortHash(h.String()), len(raw), len(raw)/2)
}

// removeObject deletes a stored object outright: the state a collection, a partial restore or a
// hand-edited tree leaves, and the one keepResolvableTools has to notice.
func removeObject(t *testing.T, root string, h core.Hash) string {
	t.Helper()
	p, ok := objectFilePath(root, h)
	if !ok {
		return ""
	}
	_ = os.Chmod(paths.Long(p), 0o600)
	if err := os.Remove(paths.Long(p)); err != nil {
		t.Logf("fault: could not remove %s: %v", p, err)
		return ""
	}
	return "removed the object file for chunk " + shortHash(h.String())
}

// quarantineHolds reports whether .qompack/tmp/quarantine/ holds a file named for h.
func quarantineHolds(root string, h core.Hash) bool {
	hx := strings.TrimPrefix(h.String(), "sha256:")
	found := false
	_ = filepath.WalkDir(paths.Long(filepath.Join(paths.Of(root).Tmp, "quarantine")),
		func(p string, _ os.DirEntry, err error) error {
			if err == nil && strings.Contains(filepath.Base(p), hx) {
				found = true
			}
			return nil
		})
	return found
}

// forcedGCKeepsEvidence runs the most destructive pass the GC API can express — negative retention
// on both axes, which is the forcing form `qompack fsck --gc-all` uses — and reports whether the
// quarantine and the capture sidecars are still there afterwards.
//
// Anything weaker would leave "GC did not collect it" and "GC never considered it"
// indistinguishable, which is the point test/security's own retention row makes.
func forcedGCKeepsEvidence(t *testing.T, root string) (string, bool) {
	t.Helper()
	quarantineBefore := relativeFiles(filepath.Join(paths.Of(root).Tmp, "quarantine"))
	sidecarsBefore := len(sidecarPaths(root))

	s, err := store.Open(root, config.Defaults(), store.Deps{})
	if err != nil {
		return "a forced GC pass could not run: store.Open refused: " + err.Error(), true
	}
	report, gcErr := s.GC(context.Background(), store.GCPolicy{RetainDays: -1, RetainSessions: -1})
	_ = s.Close()
	if gcErr != nil {
		return "a forced GC pass returned an error rather than crashing: " + gcErr.Error(), true
	}

	quarantineAfter := relativeFiles(filepath.Join(paths.Of(root).Tmp, "quarantine"))
	sidecarsAfter := len(sidecarPaths(root))
	kept := len(quarantineAfter) >= len(quarantineBefore) && sidecarsAfter >= sidecarsBefore
	return fmt.Sprintf("forced GC (RetainDays -1, RetainSessions -1) deleted %d object(s); "+
		"quarantine %d→%d, capture sidecars %d→%d", report.DeletedObjects,
		len(quarantineBefore), len(quarantineAfter), sidecarsBefore, sidecarsAfter), kept
}

// mentionsUnavailable reports whether a retrieval answer used the `unavailable` vocabulary at all.
// It reads the envelope first and falls back to the raw body, because the eight tools render several
// shapes and this row is asking which WORD came back, not which struct.
func mentionsUnavailable(res callResult, err error) bool {
	if err != nil {
		return false
	}
	var env retrievalEnvelope
	if json.Unmarshal([]byte(res.Text), &env) == nil {
		// `available: false`, present, is the wire spelling of the `unavailable` outcome. An ABSENT
		// key is a miss or a hit, not this.
		if env.Available != nil && !*env.Available {
			return true
		}
	}
	return false
}

// checkpointPointerCount is how many file and tool pointers the artifacts on disk carry between
// them. A row that found no dangling pointer over zero pointers proved nothing, and this is what
// makes that visible in the record.
func checkpointPointerCount(t *testing.T, root string) int {
	t.Helper()
	total := 0
	l := paths.Of(root)
	for _, name := range checkpointArtifacts(root) {
		raw, err := paths.ReadFileShared(filepath.Join(l.Checkpoints, name))
		if err != nil {
			continue
		}
		var doc struct {
			Pointers struct {
				Files []json.RawMessage `json:"files"`
				Tools []json.RawMessage `json:"tools"`
			} `json:"pointers"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		total += len(doc.Pointers.Files) + len(doc.Pointers.Tools)
	}
	return total
}

// refsOfKind filters an audit's references to one class.
func refsOfKind(refs []danglingRef, kind refKind) []danglingRef {
	var out []danglingRef
	for _, r := range refs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// TestFault_CheckpointResolvabilityIsBlindToADeletedObject returned F4-8, and is now its regression
// assertion. The row's NAME is historical and describes the defect, not the product.
//
// The finding: internal/checkpoint/finalize.go's toolResultResolvable decides whether a checkpoint
// KEEPS a tool pointer or drops it, and its comment said the pointer is kept when the root
// "resolves and every chunk the root names is still held". It asked store.Has, which answers from
// an in-memory chunkSet built from index/roots.jsonl and does not stat when the index says yes. So
// an object deleted from objects/ while its index line survived was "still held" as far as that
// function could tell, and the checkpoint kept a pointer into bytes that were gone.
//
// This package found it by having the same defect: the round-1 audit asked Has and reported a clean
// store for a project it had just deleted an object from.
//
// What is asserted now is the divergence that MATTERS, and the two answers are kept apart because
// only one of them moved. store.Has still answers from the index — that is its documented contract,
// it is the cheap question, and its comment now says so — while store.ObjectPresence.ObjectOnDisk
// is the filesystem question the checkpoint decides with. The row measures the product's own
// predicate through the exported capability rather than re-implementing it, and fails if it drifts
// from the filesystem in either direction.
func TestFault_CheckpointResolvabilityIsBlindToADeletedObject(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "historical_resolvability_blind_to_deleted_object")
	rec.Phase = PhaseHistorical
	rec.Boundary = "store.Has answers `held` for an object whose bytes are gone but whose index " +
		"line survives, and checkpoint finalize decides pointer resolvability with it"
	rec.SeedMethod = "a real session through the installed binary, then one object file deleted " +
		"from objects/ with index/roots.jsonl untouched; store.Has and an on-disk Lstat are then " +
		"asked the same question"

	sess := sessionID("historical-blind")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	target, ok := firstIndexedToolUse(p.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded session left no tool_use record to ask about")
		return
	}
	chunk, ok := firstChunkOf(t, p.Root, target.Root)
	if !ok {
		skipRecorded(t, rec, "the seeded root names no chunk on disk")
		return
	}
	if removeObject(t, p.Root, chunk) == "" {
		skipRecorded(t, rec, "the object file could not be removed")
		return
	}

	r := resolvabilityDisagreement(t, p.Root, target.Root, chunk)
	rec.Detail = fmt.Sprintf("after deleting the object backing chunk %s with its index line intact: "+
		"store.Has(chunk)=%v (the index's belief), store.ObjectOnDisk(chunk)=%v (offered=%v, the "+
		"predicate the checkpoint decides with), the bytes are on disk=%v, and the whole root %s "+
		"answers resolvable=%s", shortHash(chunk.String()), r.ChunkHeld, r.ChunkPresent,
		r.PresenceOffered, r.OnDisk, shortHash(target.Root), describeRootHeld(r))

	switch {
	case !r.PresenceOffered:
		recordOutcome(t, rec, OutcomeFailed, "the store no longer offers store.ObjectPresence, so "+
			"internal/checkpoint has nothing to decide pointer resolvability with except store.Has "+
			"— which answers from the index and not from the filesystem. That is F4-8 returning. "+
			"Owner: internal/store.")
		t.Errorf("fault %s: %s\n%s", rec.Name, rec.Reason, rec.Detail)
	case r.ChunkPresent != r.OnDisk:
		recordOutcome(t, rec, OutcomeFailed, "store.ObjectOnDisk and the filesystem disagree about a "+
			"deleted object, so the predicate internal/checkpoint keeps or drops a pointer with is "+
			"blind again. Owner: internal/checkpoint (the predicate) + internal/store. F4-8.")
		t.Errorf("fault %s: %s\n%s", rec.Name, rec.Reason, rec.Detail)
	case r.RootAsked && r.RootHeld:
		recordOutcome(t, rec, OutcomeFailed, "the root-level predicate — the one finalize.go asks — "+
			"still reports every chunk resolvable after one of them was deleted. Owner: "+
			"internal/checkpoint. F4-8.")
		t.Errorf("fault %s: %s\n%s", rec.Name, rec.Reason, rec.Detail)
	default:
		recordOutcome(t, rec, OutcomeRecovered, "the predicate internal/checkpoint decides pointer "+
			"resolvability with agrees with the filesystem about a deleted object, and so does the "+
			"root-level form of it. store.Has still answers from the index, which is its documented "+
			"contract and no longer what a durable promise is made on.")
	}
}

// describeRootHeld renders the root-level answer, keeping "the predicate said no" apart from "the
// root could not be read, so the predicate was never asked".
func describeRootHeld(r resolvability) string {
	if !r.RootAsked {
		return "not asked (the root could not be read)"
	}
	return fmt.Sprintf("%v", r.RootHeld)
}

// resolvability is the three answers the F4-8 row compares, kept APART.
//
// This used to fold the root-level answer back into the chunk-level variable (`saysHeld = saysHeld
// || all`) and then print that one variable for both questions, so a record claiming
// "store.Has(chunk)=true and the whole root answers resolvable-by-Has=true" was printing the same
// boolean twice and could not have said otherwise. They are different questions, and the finding
// turns on both: Has is what the store believes, the root form is what finalize.go actually asks.
type resolvability struct {
	// ChunkHeld is store.Has for the deleted chunk itself: the store's BELIEF, answered from the
	// index. Since the F4-8 fix that is documented behaviour rather than the defect, and the row
	// keeps measuring it so a future reader can see the two answers side by side.
	ChunkHeld bool
	// ChunkPresent is store.ObjectPresence.ObjectOnDisk for the same chunk: the predicate
	// internal/checkpoint now decides pointer resolvability with. It is the one that must agree
	// with the filesystem.
	ChunkPresent bool
	// PresenceOffered says the store offered the ObjectPresence capability at all, so a `false`
	// ChunkPresent is distinguishable from "never asked".
	PresenceOffered bool
	// RootHeld is finalize.go's actual predicate: the root resolves and every chunk it names is
	// present. Meaningless unless RootAsked.
	RootHeld bool
	// RootAsked says the root could be read at all, so "false" is distinguishable from "not asked".
	RootAsked bool
	// OnDisk is whether the chunk's bytes are there, by Lstat rather than by the store's belief.
	OnDisk bool
}

// resolvabilityDisagreement opens the store the way the product does and asks all three questions
// about one chunk: what store.Has says, what the root-level predicate says, and what is on disk.
//
// The store is opened AFTER the deletion, which is the honest ordering: Open builds its chunkSet
// from the index at that moment, so a Has that still answers "held" is answering from an index line
// that describes a file nobody has.
func resolvabilityDisagreement(t *testing.T, root, rootHash string, chunk core.Hash) resolvability {
	t.Helper()
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	if err != nil {
		t.Fatalf("fault: store.Open(%s): %v", root, err)
	}
	defer func() { _ = s.Close() }()

	r := resolvability{ChunkHeld: s.Has(chunk), OnDisk: objectOnDisk(root, chunk)}

	// The predicate internal/checkpoint decides with since the F4-8 fix. It is asked through the
	// exported capability rather than re-implemented here, so this row measures the product's own
	// answer and not a second opinion that could agree by accident.
	present, offered := s.(store.ObjectPresence)
	r.PresenceOffered = offered
	if offered {
		r.ChunkPresent = present.ObjectOnDisk(chunk)
	}

	// The same question at the level the checkpoint actually asks it: the root resolves and every
	// chunk it names is present.
	h, parseErr := core.ParseHash(rootHash)
	if parseErr != nil {
		return r
	}
	rec, getErr := s.GetRoot(context.Background(), h)
	if getErr != nil {
		return r
	}
	r.RootAsked = true
	r.RootHeld = true
	for _, c := range rec.Chunks {
		held := s.Has(c.Hash)
		if offered {
			held = present.ObjectOnDisk(c.Hash)
		}
		if !held {
			r.RootHeld = false
			break
		}
	}
	return r
}
