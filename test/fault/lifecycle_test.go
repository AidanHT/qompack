package fault

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The lifecycle matrix (deliverable 3): startup, resume, fork and clear; manual, automatic and
// FAILED compaction; a duplicate event, a missing one, and two out of order.
//
// Every row drives the installed binary against a LIVE daemon, and the whole matrix shares one
// project and one daemon. That is deliberate rather than economical: a host's session lifecycle
// happens inside one project, and a matrix that restarted everything between rows would never see
// the state a previous row left — which is where a duplicate or an out-of-order event actually goes
// wrong. Each row still gets its own session ids and its own audit delta.
//
// What every row asserts, no matter what it sent: the hook exited 0 with parseable stdout (§13
// invariant 6, enforced inside runHook), the audit gained no dangling reference, and no tool_use id
// appears twice in the index.

// lifecycleRow is one lifecycle case.
type lifecycleRow struct {
	Name string
	// What is the lifecycle event sequence, in the matrix's words.
	What string
	// Drive sends the row's events and returns prose describing what it sent.
	Drive func(t *testing.T, b bundle, p project, name string) string
}

// TestFault_Lifecycle runs the lifecycle matrix serially against one daemon.
func TestFault_Lifecycle(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	// One daemon for the whole matrix, brought up the way a host brings it up.
	boot := sessionID("lifecycle-boot")
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, boot, "startup"))
	if !waitDaemonUp(t, p.Root) {
		t.Fatalf("fault: session-start did not bring a daemon up for the lifecycle matrix")
	}

	for _, row := range lifecycleRows() {
		t.Run(row.Name, func(t *testing.T) {
			rec := newRecord(t, "lifecycle_"+row.Name)
			rec.Phase = PhaseLifecycle
			rec.Boundary = row.What
			loudBefore := len(loudLines(p.Root))
			before := auditProject(t, p.Root)

			rec.SeedMethod = row.Drive(t, b, p, row.Name)

			after := auditProject(t, p.Root)
			rec.DanglingBefore = len(before.Dangling)
			rec.DanglingAfter = len(after.Dangling)
			newRefs := newlyDangling(before, after)
			dupes := duplicateToolUseIDs(p.Root)
			dupeRoots := duplicateRootRecords(p.Root)

			rec.Detail = fmt.Sprintf("audit before %s; after %s; newly dangling: %s; duplicate "+
				"tool_use ids: %v; duplicate root records: %v; loud lines gained: %d",
				before, after, describeRefs(newRefs), dupes, dupeRoots, len(loudLines(p.Root))-loudBefore)

			switch {
			case len(newRefs) == 0 && len(dupes) == 0 && len(dupeRoots) == 0:
				recordOutcome(t, rec, OutcomeRecovered,
					"every hook exited 0 with parseable stdout, the index gained no duplicate "+
						"tool_use record, and the event sequence left no dangling reference")
			case len(dupes) > 0 || len(dupeRoots) > 0:
				recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf(
					"the index holds a record more than once: tool_use ids %v, root content records "+
						"%v. Owner: internal/daemon (the ingest's dedup) + internal/store.", dupes, dupeRoots))
				t.Errorf("fault %s: duplicate index records: tool_use %v, roots %v\n%s",
					rec.Name, dupes, dupeRoots, rec.Detail)
			default:
				recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf(
					"the event sequence left %d dangling reference(s): %s. Owner: internal/daemon.",
					len(newRefs), describeRefs(newRefs)))
				t.Errorf("fault %s: a lifecycle event sequence left a dangling reference\n%s",
					rec.Name, rec.Detail)
			}
		})
	}

	// The frontier state after the whole matrix, ASSERTED rather than assumed.
	//
	// Round 1's version of this row could not record anything but `explicit_incomplete`: its LOUD
	// baseline was a hard-coded zero so every pre-existing line counted as gained, and its condition
	// was OR'd with "a checkpoint artifact exists", which the matrix's own compaction rows had
	// already guaranteed. It was also using `explicit_incomplete` to mean "the product stated where
	// the session stands", which is not what that outcome means anywhere else in this package.
	//
	// What it asks now: with the daemon up, does `status --json` answer at all, does the checkpoint
	// reader agree with what is on disk, and is the store consistent? Those are answerable, and the
	// answer is `recovered` or `failed`.
	t.Run("frontier_state_is_explicit", func(t *testing.T) {
		rec := newRecord(t, "lifecycle_frontier_state_is_explicit")
		rec.Phase = PhaseLifecycle
		rec.Boundary = "the frontier and checkpoint state after the whole lifecycle matrix"
		rec.SeedMethod = "status --json and self-test --json taken with the matrix's daemon still " +
			"up, checkpoint List against the artifacts on disk, and a full store audit"

		shot := snapshotDegradation(t, b, p)
		shutdownIfReachable(t, p.Root)
		audit := auditProject(t, p.Root)
		listed, listErr := checkpointSeqs(t, p.Root)
		onDisk := checkpointArtifacts(p.Root)

		rec.DanglingBefore = len(audit.Dangling)
		rec.DanglingAfter = len(audit.Dangling)
		rec.Detail = fmt.Sprintf(
			"status gaps: %v\nself-test failures: %v\nloud lines: %d; day-log warnings: %d"+
				"\ncheckpoint artifacts on disk: %v; manifest lists: %v (err %v)\naudit: %s",
			shot.StatusGaps, shot.SelfTestFailures, len(shot.LoudLines), len(shot.DayLogWarnings),
			onDisk, listed, listErr, audit)

		var problems []string
		if listErr != nil {
			problems = append(problems, "checkpoint List refused: "+listErr.Error())
		}
		if len(listed) != len(onDisk) {
			problems = append(problems, fmt.Sprintf("the manifest lists %d checkpoint(s) and %d "+
				"artifact(s) are on disk", len(listed), len(onDisk)))
		}
		if len(audit.Dangling) > 0 {
			problems = append(problems, "the store audit found "+describeRefs(audit.Dangling))
		}
		if len(problems) > 0 {
			recordOutcome(t, rec, OutcomeFailed, "after thirteen lifecycle sequences the recorded "+
				"state does not agree with itself: "+strings.Join(problems, "; ")+
				". Owner: internal/daemon + internal/checkpoint.")
			t.Errorf("fault %s: %s\n%s", rec.Name, strings.Join(problems, "; "), rec.Detail)
			return
		}
		recordOutcome(t, rec, OutcomeRecovered, fmt.Sprintf("the frontier state is explicit and "+
			"self-consistent after thirteen lifecycle sequences: %d checkpoint artifact(s), the "+
			"same %d recorded in MANIFEST.jsonl, and no dangling reference anywhere under .qompack/",
			len(onDisk), len(listed)))
	})
}

// checkpointSeqs asks the product's own reader what the manifest records, so the row compares the
// index against the artifacts rather than counting files twice.
func checkpointSeqs(t *testing.T, root string) ([]core.CheckpointSeq, error) {
	t.Helper()
	reader, err := checkpoint.OpenReader(root, logging.Nop(), nil)
	if err != nil {
		return nil, err
	}
	refs, err := reader.List(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]core.CheckpointSeq, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Seq)
	}
	return out, nil
}

// lifecycleRows is the matrix the brief enumerates, in its order.
func lifecycleRows() []lifecycleRow {
	return []lifecycleRow{
		{"startup", "SessionStart source: startup, then a turn", driveStartup},
		{"resume", "SessionStart source: resume, then a turn", driveResume},
		{"fork", "SessionStart source: fork (a source the observer's own set does not name)", driveFork},
		{"clear", "SessionStart source: clear", driveClear},
		{"compaction_manual", "PreCompact trigger: manual, then SessionStart source: compact", driveManualCompaction},
		{"compaction_auto", "PreCompact trigger: auto, then SessionStart source: compact", driveAutoCompaction},
		{"compaction_failed", "PreCompact with no compact SessionStart at all", driveFailedCompaction},
		{"compaction_crossed", "PreCompact whose compact SessionStart arrives for ANOTHER session id", driveCrossedCompaction},
		{"duplicate_tool_use", "the same tool_use_id delivered twice", driveDuplicateToolUse},
		{"duplicate_precompact", "the same PreCompact delivered twice", driveDuplicatePreCompact},
		{"missing_turn", "a turn the host never delivered, followed by the next one", driveMissingTurn},
		{"out_of_order_precompact", "PreCompact before the tool events it would summarise", drivePreCompactFirst},
		{"out_of_order_sessionend", "SessionEnd before Stop", driveSessionEndBeforeStop},
	}
}

// oneTurn delivers a prompt, a tool observation and a stop for sess, which is the shape of one
// ordinary turn as a host produces it.
func oneTurn(t *testing.T, b bundle, p project, sess core.SessionID, name string, n int) string {
	t.Helper()
	rel := fmt.Sprintf("src/%s%02d.ts", strings.ReplaceAll(name, "_", ""), n)
	body := seedContent(name, 32)
	writeProjectFile(t, p, rel, body)
	id := toolUseID(name, n)
	runHook(t, b.Bin, p, []string{"observe", "prompt"}, promptPayload(t, p.Root, sess, "turn "+rel))
	runHook(t, b.Bin, p, []string{"observe", "tool"}, readToolPayload(t, p.Root, sess, id, rel, body))
	runHook(t, b.Bin, p, []string{"observe", "stop"}, stopPayload(t, p.Root, sess))
	// The turn must ACTUALLY be recorded. Logging and moving on was round 1's mistake: a daemon
	// that died in the middle of the matrix would have produced thirteen `recovered` rows, each one
	// reporting truthfully that a store nobody was writing to gained no dangling reference.
	if !waitIndexed(t, p.Root, id, indexBound) {
		t.Fatalf("fault: the lifecycle matrix's daemon did not index %s within %s; every row after "+
			"this one would be asserting over a store nothing is writing to", id, indexBound)
	}
	return id
}

// TestFault_FlushWaitsForASpooledPromptsFinalDrain: a prompt whose live send failed reaches only the
// hook's client spool, and the daemon publishes it in the final drain that ends the session, which
// runs AFTER the terminal-hook marker is written (internal/daemon/handlers.go, endSession). Every
// row audits the store the moment runFlush returns, so runFlush must wait for that drain, not only
// for the marker; otherwise the audit catches the prompt's root half-published as a dangling
// reference (lifecycle_compaction_failed on ubuntu, ci.yml run 38062397496).
func TestFault_FlushWaitsForASpooledPromptsFinalDrain(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	const name = "flush_spooled_prompt"
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	rel := "src/spooled.ts"
	body := seedContent(name, 32)
	writeProjectFile(t, p, rel, body)
	id := toolUseID(name, 1)
	runHookWithEnv(t, b.Bin, p, []string{"observe", "prompt"}, promptPayload(t, p.Root, sess, "turn "+rel),
		map[string]string{"QOMPACK_FAULT": "daemon-down"})
	runHook(t, b.Bin, p, []string{"observe", "tool"}, readToolPayload(t, p.Root, sess, id, rel, body))
	runHook(t, b.Bin, p, []string{"observe", "stop"}, stopPayload(t, p.Root, sess))
	if !waitIndexed(t, p.Root, id, indexBound) {
		t.Fatalf("fault: the daemon did not index %s within %s", id, indexBound)
	}

	runFlush(t, b, p, sess)

	// Read once, with no further wait: this is the instant every row's audit runs.
	prompt := "prompt_" + string(sess) + "_"
	if !waitIndexed(t, p.Root, prompt, 0) {
		t.Errorf("fault: runFlush returned before the session's spooled prompt (%s*) was indexed", prompt)
	}
	if a := auditProject(t, p.Root); len(a.Dangling) > 0 {
		t.Errorf("fault: runFlush returned with %d dangling reference(s): %s", len(a.Dangling), a)
	}
}

func driveStartup(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	id := oneTurn(t, b, p, sess, name, 1)
	runFlush(t, b, p, sess)
	return "SessionStart(startup) + one turn (" + id + ") + SessionEnd"
}

func driveResume(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	// The resume: the SAME session id arrives again, which is what a host sends when a user picks a
	// conversation back up. observer/session.go names `resume` in its own source set.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "resume"))
	id := oneTurn(t, b, p, sess, name, 2)
	runFlush(t, b, p, sess)
	return "SessionStart(startup) + turn, SessionStart(resume) on the same id + turn (" + id + ")"
}

// driveFork sends `source: fork`, which is NOT one of the four sources observer/session.go names
// (startup, resume, compact, clear). The brief asks for it "if the hookio parser accepts it, else
// record unsupported" — and the answer this row records is which of the two happened, because
// hookio.Event carries `source` as a plain string and the parser accepting the FIELD is a different
// question from the observer knowing the VALUE.
func driveFork(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "fork"))
	id := oneTurn(t, b, p, sess, name, 1)
	runFlush(t, b, p, sess)
	indexed := indexHolds(p.Root, id)
	return fmt.Sprintf("SessionStart(source:fork) + one turn; the hook exited 0 and the turn was "+
		"%s — `fork` is not one of the four sources internal/observer names, so the source is "+
		"carried but not branched on", map[bool]string{true: "indexed", false: "NOT indexed"}[indexed])
}

func driveClear(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "clear"))
	id := oneTurn(t, b, p, sess, name, 2)
	runFlush(t, b, p, sess)
	return "SessionStart(startup) + turn, SessionStart(clear) + turn (" + id + ")"
}

func driveManualCompaction(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	return driveCompaction(t, b, p, name, "manual")
}

func driveAutoCompaction(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	return driveCompaction(t, b, p, name, "auto")
}

// driveCompaction is the complete, successful compaction shape: turns, PreCompact, and the
// SessionStart(compact) the host sends once the summary exists.
func driveCompaction(t *testing.T, b bundle, p project, name, trigger string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, trigger))
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "compact"))
	id := oneTurn(t, b, p, sess, name, 2)
	runFlush(t, b, p, sess)
	return fmt.Sprintf("turn, PreCompact(trigger:%s), SessionStart(compact), turn (%s)", trigger, id)
}

// driveFailedCompaction is the compaction that never completed: PreCompact fired and the host's
// summary never arrived, so no SessionStart(compact) follows. e2e/sessionstart_compact_test.go's
// ...AfterFailedSummary owns the in-process form of this.
func driveFailedCompaction(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, "auto"))
	// No SessionStart(compact). The session simply goes on.
	id := oneTurn(t, b, p, sess, name, 2)
	runFlush(t, b, p, sess)
	return "turn, PreCompact(auto), NO SessionStart(compact), turn (" + id + ")"
}

// driveCrossedCompaction fires PreCompact for one session and delivers the compact SessionStart for
// a DIFFERENT one — the shape a host produces when two conversations compact at once and the
// correlation is lost.
func driveCrossedCompaction(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	other := sessionID(name + "-other")
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, "auto"))
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, other, "compact"))
	id := oneTurn(t, b, p, other, name, 2)
	runFlush(t, b, p, sess)
	runFlush(t, b, p, other)
	return "PreCompact on " + string(sess) + ", SessionStart(compact) on " + string(other) + ", turn (" + id + ")"
}

// driveDuplicateToolUse delivers the same tool_use_id twice, byte for byte. daemon_test.go's
// TestNAKDuplicateIsDedupedOnDrain owns the in-process form; this asks whether the INSTALLED
// binary's index ends up with one record or two.
func driveDuplicateToolUse(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	rel := "src/duplicate.ts"
	body := seedContent(name, 32)
	writeProjectFile(t, p, rel, body)
	id := toolUseID(name, 1)
	payload := readToolPayload(t, p.Root, sess, id, rel, body)
	runHook(t, b.Bin, p, []string{"observe", "tool"}, payload)
	if !waitIndexed(t, p.Root, id, indexBound) {
		t.Logf("fault: %s was not indexed before the duplicate was delivered", id)
	}
	runHook(t, b.Bin, p, []string{"observe", "tool"}, payload)
	runFlush(t, b, p, sess)
	return "the identical PostToolUse payload for " + id + " delivered twice"
}

func driveDuplicatePreCompact(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	oneTurn(t, b, p, sess, name, 1)
	payload := preCompactPayload(t, p.Root, sess, "auto")
	runHook(t, b.Bin, p, []string{"checkpoint"}, payload)
	runHook(t, b.Bin, p, []string{"checkpoint"}, payload)
	runFlush(t, b, p, sess)
	return "the identical PreCompact payload delivered twice"
}

// driveMissingTurn skips a turn entirely: turn 1 and turn 3 are delivered and turn 2 never is, which
// is what a dropped hook looks like from the daemon's side.
//
// What this row CAN observe is bounded, and saying so is the point. A turn the host never delivered
// leaves no trace anywhere — there is no sequence number in a hook payload and no gap for the daemon
// to notice — so nothing here can assert that the product detected the absence, because it cannot.
// What it does assert is that the absence breaks nothing: the turns either side of the hole are
// recorded, no reference dangles, and the index gains no duplicate.
func driveMissingTurn(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	first := oneTurn(t, b, p, sess, name, 1)
	// Turn 2 is the one the host never delivered.
	third := oneTurn(t, b, p, sess, name, 3)
	runFlush(t, b, p, sess)
	return "turn " + first + " and turn " + third + " delivered; the turn between them never was. " +
		"An undelivered turn leaves no trace for the product to detect, so the row measures that " +
		"the turns either side of the hole are intact rather than that the hole was noticed"
}

// drivePreCompactFirst sends PreCompact before any tool event of the session — the summariser asked
// to summarise a session it has not seen.
func drivePreCompactFirst(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, sess, "auto"))
	id := oneTurn(t, b, p, sess, name, 1)
	runFlush(t, b, p, sess)
	return "PreCompact before any tool event, then the turn (" + id + ") it would have summarised"
}

// driveSessionEndBeforeStop ends the session and then sends the Stop that should have preceded it.
func driveSessionEndBeforeStop(t *testing.T, b bundle, p project, name string) string {
	t.Helper()
	sess := sessionID(name)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	rel := "src/outoforder.ts"
	body := seedContent(name, 32)
	writeProjectFile(t, p, rel, body)
	id := toolUseID(name, 1)
	runHook(t, b.Bin, p, []string{"observe", "tool"}, readToolPayload(t, p.Root, sess, id, rel, body))
	runFlush(t, b, p, sess)
	runHook(t, b.Bin, p, []string{"observe", "stop"}, stopPayload(t, p.Root, sess))
	return "tool event " + id + ", SessionEnd, then the Stop that should have come first"
}

// duplicateToolUseIDs returns every tool_use id index/tool_use.jsonl records more than once.
//
// A second record for one id is not a cosmetic duplicate: the id is the address `expand` and the
// checkpoint tier resolve against, so two records for one address are two different answers to one
// question. Supersession is a different thing and is not counted here — it rewrites the FIRST
// record's Status rather than appending a second line under the same id.
func duplicateToolUseIDs(root string) []string {
	lines, err := readJSONLines(filepath.Join(paths.Of(root).Index, "tool_use.jsonl"))
	if err != nil {
		return nil
	}
	seen := map[string]int{}
	for _, raw := range lines {
		var tu toolUseLine
		if json.Unmarshal(raw, &tu) != nil || tu.ID == "" {
			continue
		}
		seen[tu.ID]++
	}
	var out []string
	for id, n := range seen {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s x%d", id, n))
		}
	}
	return out
}

// indexHolds reports whether index/tool_use.jsonl names id at all.
func indexHolds(root, id string) bool {
	b, err := paths.ReadFileShared(filepath.Join(paths.Of(root).Index, "tool_use.jsonl"))
	return err == nil && strings.Contains(string(b), id)
}

// duplicateRootRecords returns every root hash index/roots.jsonl records as a CONTENT record more
// than once.
//
// Deliverable 3 asks for no duplicate roots and no duplicate tool_use records; round 1 checked only
// the second. A root is content-addressed, so a second content record for one hash is the store
// having failed to dedup — two index lines claiming to describe the same bytes, with whatever
// disagreement about tool, path or class the two writers happened to carry.
//
// Tombstones are not counted: a `gc` record names a root deliberately and legitimately follows the
// content record it retires. Neither are the synthetic side records the delta path writes, whose
// tool names are the reserved forms and whose address is derived rather than the content's own.
func duplicateRootRecords(root string) []string {
	lines, err := readJSONLines(filepath.Join(paths.Of(root).Index, "roots.jsonl"))
	if err != nil {
		return nil
	}
	seen := map[string]int{}
	for _, raw := range lines {
		var rl rootLine
		if json.Unmarshal(raw, &rl) != nil || rl.Root == "" {
			continue
		}
		if rl.Op != "" || rl.Tool == deltaToolName || rl.Tool == rawToolName {
			continue
		}
		seen[rl.Root]++
	}
	var out []string
	for h, n := range seen {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s x%d", shortHash(h), n))
		}
	}
	sort.Strings(out)
	return out
}

// The reserved synthetic tool names the delta path writes its side records under
// (internal/store/roots.go:29-33). They are GC-visible roots at a derived address, not a second
// content record for one hash.
const (
	deltaToolName = "«deltas»"
	rawToolName   = "«raw»"
)
