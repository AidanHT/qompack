package fault

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/core"
)

// Child failure (deliverable 4): a child process that died, and a hook fed nothing it can use.
//
// Every kill here is of a process THIS package forked — the MCP launcher through the *exec.Cmd it
// holds, the daemon through a lock file under one of its own `qompack-fault-` fixtures. Nothing
// here ever looks up a pid it did not put there, because this machine runs other sessions' suites.

// TestFault_MCPChildKilledMidRequest kills the packaged MCP server while a request is in flight.
//
// What it establishes: the client sees an ERROR — the session ending — rather than hanging, and the
// store the server was reading is untouched afterwards. A retrieval server is a reader; a killed
// reader must not be able to leave the archive in a different state than it found it.
func TestFault_MCPChildKilledMidRequest(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "child_mcp_killed_mid_request")
	rec.Phase = PhaseChild
	rec.Boundary = "the MCP server child is killed mid-request"
	rec.SeedMethod = "a real session through the installed binary, then `<bundle>/bin/qompack mcp` " +
		"launched the way plugin/.mcp.json instructs (exec form, no shell), killed between a " +
		"request and its answer"

	sess := sessionID("child-mcp")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)

	before := auditProject(t, p.Root)
	// The property the reason CLAIMS, hashed rather than asserted from the audit counts. The audit
	// answers "does anything dangle", which is a different question from "did the bytes change":
	// round 1 said "the store the server was reading is unchanged" over a detail line that showed
	// tool_uses 3 -> 4. A retrieval server may legitimately bring a daemon up that publishes
	// something new, so this is CONTAINMENT — every object present before is still present with
	// the same bytes — rather than equality.
	objectsBefore := objectFingerprints(t, p.Root)

	c := startMCP(t, b.Bin, p)
	t.Cleanup(func() { c.stop(t) })
	c.handshake(t)

	// One successful call first, so the row is about the kill rather than about a server that never
	// worked. `recall` is the retrieval entry point a host reaches for first.
	warm, warmErr := c.call(t, "recall", map[string]any{"query": "alpha"})
	if warmErr != nil {
		t.Fatalf("fault: the mcp launcher could not answer a first recall: %v", warmErr)
	}

	// The kill: the request goes out, the child dies before it can answer.
	c.kill(t)
	res, err := c.call(t, "recall", map[string]any{"query": "beta"})

	shutdownIfReachable(t, p.Root)
	after := auditProject(t, p.Root)
	changed := changedObjects(objectFingerprints(t, p.Root), objectsBefore)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	newRefs := newlyDangling(before, after)
	rec.Detail = fmt.Sprintf("first call: %s\nafter the kill: %s\naudit before %s; after %s"+
		"\nobjects present before the session: %d, of which changed or gone afterwards: %v",
		describeEnvelope(warm, nil), describeEnvelope(res, err), before, after,
		len(objectsBefore), changed)

	requireAuditClean(t, rec.Name, after)
	if len(changed) > 0 {
		recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf("a killed MCP reader left %d stored "+
			"object(s) changed or missing: %v. A retrieval server is a reader. Owner: internal/mcp.",
			len(changed), changed))
		t.Errorf("fault %s: a killed reader changed the archive\n%s", rec.Name, rec.Detail)
		return
	}

	switch {
	case err == nil:
		// Not a failure by itself: the child may have answered out of a buffer before dying. It is
		// recorded, because "the client got an answer from a process that is now dead" is exactly
		// the kind of thing a matrix exists to notice.
		recordOutcome(t, rec, OutcomeRecovered, "the killed launcher had already written its answer; "+
			"the client saw a complete response, every object present before the session is still "+
			"present with the same bytes, and nothing dangles")
	case len(newRefs) == 0:
		recordOutcome(t, rec, OutcomeRecovered, "the client saw the session end as an error rather "+
			"than hanging ("+err.Error()+"); every object present before the MCP session is still "+
			"present with the same bytes, and nothing dangles")
	default:
		recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf("a killed MCP reader left %d dangling "+
			"reference(s) behind: %s. Owner: internal/mcp.", len(newRefs), describeRefs(newRefs)))
		t.Errorf("fault %s: a killed reader changed the archive\n%s", rec.Name, rec.Detail)
	}
}

// daemonKillNames are the tokens that would constitute the product reporting THIS cut: the spool the
// killed daemon left, the drain that has to replay it, and the gap vocabulary drain.go uses. Passing
// none — which this row did until now — makes `explicit_incomplete` mean "something new was
// logged", and the universal segment-tokens warn is new on every single run.
var daemonKillNames = registerNames("child_daemon_killed_mid_ingest",
	[]string{"drain", "spool", "replay", "wal-", "client-", "unreplayed"})

// TestFault_DaemonKilledMidIngest kills the daemon during a burst of deliveries, restarts it, and
// asks whether the drain replays what was in flight.
//
// This is the one daemon-side forced failure this package has: QOMPACK_FAULT never reaches a
// daemon (internal/daemon/spawn.go strips it; test/guards/faultenv_test.go forbids a second seam),
// so the cut is a real kill of a real process in one of this package's own fixtures.
func TestFault_DaemonKilledMidIngest(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "child_daemon_killed_mid_ingest")
	rec.Phase = PhaseChild
	rec.Boundary = "the daemon is killed during a burst of `observe tool` deliveries"

	sess := sessionID("child-daemon")
	seedSession(t, b, p, sess)
	shutdownIfReachable(t, p.Root)
	// The baseline comes back from the burst, taken while its daemon is still alive: asking
	// `status --json` after the kill would spawn a daemon that drains the spool this row is about.
	burstProse, baseline := seedUndrainedSpool(t, b, p, sessionID("child-daemon-burst"))
	rec.SeedMethod = burstProse

	before := auditProject(t, p.Root)
	walBefore, clientBefore := spoolInventory(p.Root)

	// The restart: a new session-start brings a fresh daemon up over the spool the killed one left,
	// and it is LEFT UP so the degradation reading below has something to ask.
	recording := recoverSession(t, b, p, sessionID("child-daemon-r"))
	ev := namingEvidence(t, degradationSince(baseline, snapshotDegradation(t, b, p)), daemonKillNames)
	shutdownIfReachable(t, p.Root)

	after := auditProject(t, p.Root)
	walAfter, clientAfter := spoolInventory(p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	rec.Detail = fmt.Sprintf("spool wal/client %d/%d → %d/%d; recording resumed: %v\n"+
		"audit before %s; after %s; reported gaps: %s\ndegradation (new since the kill): %s",
		walBefore, clientBefore, walAfter, clientAfter, recording, before, after,
		describeRefs(after.Reported), ev)

	newRefs := newlyDangling(before, after)
	switch {
	case len(newRefs) == 0 && recording:
		recordOutcome(t, rec, OutcomeRecovered, "the restarted daemon replayed the spool the killed "+
			"one left, went on recording, and introduced no dangling reference")
	case len(newRefs) == 0 && ev.Explicit():
		// An outcome of `explicit_incomplete` has to name the surface that carried the gap. Round 1
		// recorded this branch without consulting one at all, which made "explicit" mean nothing.
		recordOutcome(t, rec, OutcomeExplicitIncomplete, "the restarted daemon introduced no dangling "+
			"reference; the recovery session's own observation did not reach the index within the "+
			"bound and the product says so on "+strings.Join(ev.Surfaces(), "+")+": "+ev.String())
	case len(newRefs) == 0:
		recordOutcome(t, rec, OutcomeFailed, "the restarted daemon introduced no dangling reference, "+
			"but the recovery session's own observation never reached the index and no surface "+
			"gained anything about it. Owner: internal/daemon.")
		t.Errorf("fault %s: recording stopped silently after a kill mid-ingest\n%s", rec.Name, rec.Detail)
	default:
		recordOutcome(t, rec, OutcomeFailed, fmt.Sprintf("a killed daemon left %d dangling "+
			"reference(s) a restart did not resolve: %s. Owner: internal/daemon.",
			len(newRefs), describeRefs(newRefs)))
		t.Errorf("fault %s: a kill mid-ingest left a dangling reference\n%s", rec.Name, rec.Detail)
	}
}

// TestFault_SubagentStopWithoutResult delivers `observe stop --subagent` for a subagent that never
// produced a result: the SubagentStop a host sends when a sub-task ended with nothing to record.
func TestFault_SubagentStopWithoutResult(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		requireNoOrphan(t, p.Root)
	})

	rec := newRecord(t, "child_subagent_stop_without_result")
	rec.Phase = PhaseChild
	rec.Boundary = "`observe stop --subagent` for a subagent that never produced a result"
	rec.SeedMethod = "a live session, then a SubagentStop with no preceding tool observation for " +
		"that subagent at all"

	sess := sessionID("child-subagent")
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	if !waitDaemonUp(t, p.Root) {
		t.Fatalf("fault: session-start did not bring a daemon up")
	}
	before := auditProject(t, p.Root)

	runHook(t, b.Bin, p, []string{"observe", "stop", "--subagent"}, subagentStopPayload(t, p.Root, sess))
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, sess))
	shutdownIfReachable(t, p.Root)

	after := auditProject(t, p.Root)
	rec.DanglingBefore = len(before.Dangling)
	rec.DanglingAfter = len(after.Dangling)
	gained := indexGrowth(before, after)
	rec.Detail = fmt.Sprintf("audit before %s; after %s\nwhat the stop caused to be written: %s",
		before, after, firstNonEmpty(gained, "nothing"))

	requireAuditClean(t, rec.Name, after)
	if len(newlyDangling(before, after)) > 0 {
		recordOutcome(t, rec, OutcomeFailed, "a subagent stop with no result left a dangling "+
			"reference. Owner: internal/daemon.")
		t.Errorf("fault %s: %s", rec.Name, rec.Detail)
		return
	}

	// The reason is DERIVED from the counts rather than written ahead of them, which is this round's
	// correction. The text here hard-coded "produced no tool_use record … the sidecar count rises by one"
	// and the record beside it read {tool_uses:0 roots:0 sidecars:0} → {tool_uses:1 roots:1
	// sidecars:1}: all three rose, so half the sentence was false and the other half incomplete. The
	// property asserted below is the one stated: whatever the stop caused to be written resolves,
	// which requireAuditClean and newlyDangling have just established over exactly those records.
	if gained == "" {
		recordOutcome(t, rec, OutcomeRecovered, "the hook exited 0 with parseable stdout and left no "+
			"dangling reference; the stop was INERT — none of the four populations this audit scans "+
			"(tool_use records, roots, capture sidecars, checkpoint artifacts) grew")
		return
	}
	recordOutcome(t, rec, OutcomeRecovered, "the hook exited 0 with parseable stdout and left no "+
		"dangling reference. The stop is NOT inert, and what it wrote is stated rather than denied: "+
		gained+". Every one of those records resolves — that is what the clean audit above means")
}

// TestFault_HookStdinUnusable drives the two fault sites that take a hook's input away underneath it
// — `stdin-eof` and `stdin-garbage` — during a LIVE session, and asserts the half of the contract
// that holds no matter what arrived: exit 0, parseable stdout, nothing broken behind it.
//
// Both sites are hook-side by construction, which is the only place QOMPACK_FAULT reaches.
func TestFault_HookStdinUnusable(t *testing.T) {
	b := assembledBundle(t)

	for _, site := range []string{"stdin-eof", "stdin-garbage"} {
		t.Run(strings.ReplaceAll(site, "-", "_"), func(t *testing.T) {
			p := newProject(t, "proj")
			t.Cleanup(func() {
				shutdownIfReachable(t, p.Root)
				requireNoOrphan(t, p.Root)
			})

			rec := newRecord(t, "child_hook_"+strings.ReplaceAll(site, "-", "_"))
			rec.Phase = PhaseChild
			rec.Boundary = "a hook whose stdin is " + site + ", during a live session"
			rec.SeedMethod = "the `" + site + "` QOMPACK_FAULT site (internal/cli/fault.go), " +
				"delivered to every hook subcommand of a live session"

			sess := sessionID("child-" + site)
			runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
			if !waitDaemonUp(t, p.Root) {
				t.Fatalf("fault: session-start did not bring a daemon up")
			}
			before := auditProject(t, p.Root)

			env := map[string]string{"QOMPACK_FAULT": site}
			faulted := [][]string{
				{"observe", "prompt"},
				{"observe", "tool"},
				{"observe", "stop"},
				{"checkpoint"},
				{"flush"},
				{"session-start"},
			}
			for _, argv := range faulted {
				// runHookWithEnv is where §13 invariant 6 is enforced: a non-zero exit or
				// unparseable stdout fails the case right here, which is the point of the row.
				runHookWithEnv(t, b.Bin, p, argv, hookPayloadFor(t, p.Root, sess, argv), env)
			}
			shutdownIfReachable(t, p.Root)

			after := auditProject(t, p.Root)
			rec.DanglingBefore = len(before.Dangling)
			rec.DanglingAfter = len(after.Dangling)
			rec.Detail = fmt.Sprintf("six hook subcommands driven under %s\naudit before %s; after %s",
				site, before, after)

			requireAuditClean(t, rec.Name, after)
			if len(newlyDangling(before, after)) == 0 {
				recordOutcome(t, rec, OutcomeRecovered, "every hook exited 0 with parseable stdout "+
					"under "+site+", and the store the live session was writing gained no dangling reference")
				return
			}
			recordOutcome(t, rec, OutcomeFailed, "a hook whose stdin was unusable left a dangling "+
				"reference. Owner: internal/cli.")
			t.Errorf("fault %s: %s", rec.Name, rec.Detail)
		})
	}
}

// hookPayloadFor builds the payload each hook subcommand expects. The fault sites replace stdin
// anyway, but a hook driven with the WRONG payload would be measuring the mismatch rather than the
// site.
func hookPayloadFor(t *testing.T, root string, sess core.SessionID, argv []string) []byte {
	t.Helper()
	switch strings.Join(argv, " ") {
	case "observe prompt":
		return promptPayload(t, root, sess, "a prompt whose stdin is about to be taken away")
	case "observe tool":
		return readToolPayload(t, root, sess, toolUseID("stdin", 1), "src/alpha.ts", seedContent("alpha", 48))
	case "observe stop":
		return stopPayload(t, root, sess)
	case "checkpoint":
		return preCompactPayload(t, root, sess, "auto")
	case "flush":
		return sessionEndPayload(t, root, sess)
	case "session-start":
		return sessionStartPayload(t, root, sess, "startup")
	default:
		t.Fatalf("fault: no payload for %v", argv)
		return nil
	}
}
