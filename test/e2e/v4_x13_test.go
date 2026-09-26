// V4 §4.13 — the hot path is unchanged with the full wave-3 resident set.
//
// TIMING ARM DEFERRED, DELIBERATELY. The reconciliation map homes this row in test/bench/hotpath
// and grades B-A…B-F against budgets. This machine measured the SAME hot-path row at 90.1 ms,
// 81.9 ms, 30.7 ms and 6.1 ms across four runs: the variance exceeds the effect, so a wall-clock
// assertion here would be noise wearing a gate's name. ADR 0010's co-load policy applies, and the
// measured arm belongs on a quiet runner. This row asserts the STRUCTURAL claim instead — what is
// resident, and what work the hot path does — which is the half that can be established here and
// is the half a regression would break first. The one file a HOOK writes on a missed deadline, its
// client fallback spool, is therefore asserted per arm rather than compared between arms; see
// x13v4HookFallbackToken.
//
// It also lives in test/e2e rather than test/bench/hotpath: the bench package's budget machinery is
// owned elsewhere, and a structural A/B needs two DIFFERENT compositions of the same daemon, which
// only a composition root can build.
package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// The two session identities: the full wave-3 arm and the observer-only reference arm.
const (
	x13v4Session    = core.SessionID("sess-e2e-v4-x13")
	x13v4RefSession = core.SessionID("sess-e2e-v4-x13-ref")
)

// x13v4Turns is the hook burst each arm replays. Identical on both sides, so the write sets are
// comparable by construction.
const x13v4Turns = 8

// x13v4PendingDir is FSStore.beginPendingWrite's in-flight-Put registry, under .qompack/state
// (internal/store/lifecycle.go, pendingWriteDir). One marker per running Put, named from six random
// bytes, removed by pendingWrite.done once the root's index line has landed.
const x13v4PendingDir = "pending"

// x13v4QuiesceBound is how long a walk waits for the writes that are still running to finish.
//
// It is obsProcessAllowance — observer_e2e_test.go's PROCESSING half — because the writes this waits
// on are precisely that work: §2.4 ACKs after the WAL append, so the store Put and the capture
// sidecar of an accepted delivery are still running behind the hook that has already exited. Reusing
// the bound that already reasons about this latency keeps one number for one mechanism instead of a
// second hand-picked one racing it.
const x13v4QuiesceBound = obsProcessAllowance

// x13v4SettleBudgets is how many B-C budgets the settle window below spans. Four, so a co-loaded
// runner that misses its budget several times over still settles, without the window becoming a
// wall-clock assertion of its own — this row defers those to test/bench/hotpath on purpose.
const x13v4SettleBudgets = 4

// x13v4SettleWindow is how long .qompack/tmp and .qompack/state/pending must be seen empty
// CONTINUOUSLY before a walk may start.
//
// One empty sample is not quiescence, and this is the correction a probe of this row forced. One
// delivery's publication is a SEQUENCE of writes — the capture sidecar (ingest.go, publication
// order stage 1), the index record, the reference LinkCaptureReference stamps back onto the sidecar
// through paths.WriteAtomic (observer/tooluse.go step 6b), the object Put with its pending marker,
// then the committed-frontier line (stage 3) — and .qompack/tmp is empty in every gap BETWEEN two of
// them. WaitIndexed returns at stage 2, so the stages after it are still to come; a single-sample
// check landed in one of those gaps, called the tree quiet, and the walk that followed then found a
// tmp/wa-* staging file that had been created after the check. Requiring the directories to stay
// empty across a window longer than any such gap is what makes the answer mean what it says.
//
// The window is x13v4SettleBudgets times the B-C budget (config's l0ProcessMs, "WAL to fully
// chunked, stored, DAG/sketches updated") because that budget bounds the WHOLE of one delivery's
// processing and therefore bounds every gap inside it by construction. Taking it from
// config.Defaults keeps it moving with the budget rather than being a second number about the same
// mechanism.
var x13v4SettleWindow = x13v4SettleBudgets *
	time.Duration(config.Defaults().Runtime.Budgets.L0ProcessMs) * time.Millisecond

// x13v4QuarantineDir is the ONE subdirectory of .qompack/tmp that is not staging (internal/store's
// fsstore.go, quarantineDir). It is excluded from the in-flight listing below on a category
// argument, not on odds: a file in it is a corrupt object or an unparseable state file that the
// store (objects.go) or the MCP promoter (mcp/promote.go) moved aside DELIBERATELY to preserve it.
// Those bytes are finished output that is meant to stay, the exact opposite of a write in flight.
// store.Open pre-creates the directory, so it is empty in a healthy run and costs nothing today —
// but the first thing this test ever quarantines would otherwise make the wait below never settle
// and every walk hard-fail at its deadline, blaming "writes still in flight" for a finished file.
//
// It is excluded from the QUIESCENCE PREDICATE only. The write-set walk still sees anything in it,
// so a quarantined file remains a visible difference between the two arms.
const x13v4QuarantineDir = "quarantine"

// x13v4InFlight names every file under .qompack/tmp and .qompack/state/pending, as slash paths
// relative to .qompack/. Both directories hold a file ONLY between the start and the end of a write
// that is still running: .qompack/tmp is paths.WriteAtomic's staging area (create, sync, chmod,
// rename onto the destination), and .qompack/state/pending is the in-flight-Put registry above.
// An empty pair therefore means every write this daemon had begun has landed.
func x13v4InFlight(root string) []string {
	l := paths.Of(root)
	out := x13v4FilesUnder(l.Dot, l.Tmp, filepath.Join(l.Tmp, x13v4QuarantineDir))
	out = append(out, x13v4FilesUnder(l.Dot, filepath.Join(l.State, x13v4PendingDir))...)
	sort.Strings(out)
	return out
}

// x13v4FilesUnder lists every file under dir, recursively, as slash paths relative to dot, skipping
// any directory named in skip. A directory that does not exist holds nothing — .qompack/tmp is
// created by paths.EnsureLayout and .qompack/state/pending only by the first Put that registers a
// marker.
func x13v4FilesUnder(dot, dir string, skip ...string) []string {
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if !slices.Contains(skip, p) {
				out = append(out, x13v4FilesUnder(dot, p, skip...)...)
			}
			continue
		}
		if rel, relErr := filepath.Rel(dot, p); relErr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// x13v4InFlightDirs names the distinct directories the given in-flight paths came from, so a
// quiescence timeout says WHICH of the two watched directories was still non-empty instead of
// leaving that to be read off the file names.
func x13v4InFlightDirs(names []string) []string {
	seen := map[string]bool{}
	for _, n := range names {
		seen[path.Dir(n)] = true
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// x13v4Quiesce blocks until nothing is in flight under .qompack/, so every walk below — the before
// snapshots as much as the after deltas, and both arms — reads a tree in which every write that had
// started has finished.
//
// This is the second half of §4.13's fixture being made deterministic, and it deliberately WAITS
// rather than EXCLUDES. A staging file caught between create and rename, or a pending-write marker
// caught between Put and index line, is a coin flip on whether a walk sees it — but skipping those
// two directories would also hide a real finding, because a wave-3 resident that caused one extra
// Put would announce itself as exactly one extra marker and one extra staging file. So the fixture
// waits for the writes to land and then compares everything, instead of agreeing not to look.
//
// Applying it to the before snapshots matters as much as to the after walks: a before snapshot taken
// mid-write records a staging file that the burst then renames away, and the delta reports a
// "change" that belongs to the setup. That is the same asymmetry in a different place.
//
// A wait that expires FAILS, naming what was still in flight. A timeout is never close enough here:
// it means the walk that follows would have compared a tree mid-write, which is the whole defect
// this removes.
//
// A ticker paces the poll, never time.Sleep: §6.1 bans wall-clock sleeps outside test/bench,
// _test.go files included, and devtool lint's sleepcheck sub-check enforces it by AST scan. The bound
// stays a wall-clock comparison rather than becoming a second channel in a select, and that is the
// point of copying v3_x08_test.go's loop here rather than faultinject_test.go's
// e2eShutdownIfReachable: a select over a tick and a deadline picks between them at RANDOM when both
// are ready, so an expired wait could poll on past its bound. e2eShutdownIfReachable can afford that
// because its deadline is its only exit; this one's deadline is a hard failure, and a bound that
// slips is a bound that means less than it says. Nothing else about the wait changes — both
// directories must still read empty CONTINUOUSLY for x13v4SettleWindow, anything in flight still
// restarts that window, and expiry still fails while naming what was in flight.
func x13v4Quiesce(t *testing.T, root string) {
	t.Helper()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	deadline := time.Now().Add(x13v4QuiesceBound)
	var lastSeen []string
	var emptySince time.Time
	for {
		inflight := x13v4InFlight(root)
		now := time.Now()
		if len(inflight) > 0 {
			lastSeen, emptySince = inflight, time.Time{}
		} else {
			if emptySince.IsZero() {
				emptySince = now
			}
			if now.Sub(emptySince) >= x13v4SettleWindow {
				return
			}
		}
		if now.After(deadline) {
			require.FailNowf(t, "writes were still in flight when the tree was due to be walked",
				"in %s, .qompack/tmp and .qompack/state/%s were never both empty for %s within %s. What "+
					"was still in flight last sat under %v: %v. A walk now would compare a tree mid-write, "+
					"so this fails rather than walking anyway.",
				root, x13v4PendingDir, x13v4SettleWindow, x13v4QuiesceBound,
				x13v4InFlightDirs(lastSeen), lastSeen)
		}
		<-ticker.C
	}
}

// x13v4WriteSet returns every path under .qompack/ whose existence or size changed between before
// and after, as slash-relative names with volatile per-run components normalized away.
//
// It is a SET of names, never sizes: the two arms store different session ids and different tool
// use ids, so byte counts legitimately differ while the SHAPE of what the hot path touches must not.
//
// It quiesces first: what it must not report is a write that had merely not finished yet.
func x13v4WriteSet(t *testing.T, root string, before map[string]int64) []string {
	t.Helper()
	x13v4Quiesce(t, root)
	out := map[string]bool{}
	dot := paths.Of(root).Dot
	err := filepath.WalkDir(paths.Long(dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil //nolint:nilerr // a vanished temp file is not this walk's concern
		}
		rel, relErr := filepath.Rel(paths.Long(dot), p)
		if relErr != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		fi, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a vanished temp file is not this walk's concern
		}
		if was, ok := before[name]; ok && was == fi.Size() {
			return nil // present and unchanged: this burst did not touch it
		}
		out[x13v4Normalize(name)] = true
		return nil
	})
	require.NoError(t, err)

	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// x13v4CapturePrefix is the sidecar tree the SP-20 capture path writes one record into per leased
// delivery (internal/daemon/ingest.go publishCapture → store.WriteCaptureSidecar). The file name is
// the delivery's observation identity, a digest over session, nonce and arrival, so it differs
// between the two arms by construction and folds to a shape here. Its companion writes —
// state/retention-roots.jsonl and the delivery journal under state/ — carry fixed names already.
const x13v4CapturePrefix = "records/captures/"

// The three families that live side by side in .qompack/spool. They are DIFFERENT things and they
// fold to three different tokens:
//
//   - wal-<session>.ndjson, and wal-<session>.<seq>.ndjson once a segment has rotated
//     (internal/daemon/ingest.go, walPath) — the daemon's OWN durable log, appended by Accept
//     before it ACKs. Every accepted delivery leaves one; its presence is the hot path working.
//   - client-<pid>.ndjson (internal/ipc/spool.go, newSpool) — the HOOK's fallback spool, written
//     only when the client gave up waiting for the daemon's ACK. Its presence means that arm
//     DEGRADED.
//   - blob-<pid>-<n>.bin (internal/ipc/client.go, blobFilePrefix) — a client-externalized oversized
//     Event.ToolResponse, written when the request would not have fit in a frame.
//
// One token for all three is how this row used to fold them, and it is a hole in the claim rather
// than a tidy-up: an arm that fell back to the client spool and an arm that never did produced the
// SAME write set. That fallback is exactly what carried defect SP05-D2 is about and what this wave
// says it has bounded, so the comparison has to be able to see it. Splitting the token costs
// nothing that the fold was needed for — the per-run component (session, pid, rotation sequence) is
// still erased WITHIN each family. The row sees spool/<client> through its own per-arm assertion,
// not through the write-set equality, because whether a hook spools is decided by a wall clock; see
// x13v4HookFallbackToken.
const (
	x13v4WalPrefix    = "spool/wal-"
	x13v4ClientPrefix = "spool/client-"
	x13v4BlobPrefix   = "spool/blob-"
)

// The .qompack/logs names, of which exactly one is fixed.
//
// LOUD.log is opened once per log directory and NEVER rotated (internal/logging/logger.go,
// loudFileName), so it carries no per-run component and is not folded at all. It was being folded,
// and that made an arm that went Loud during its burst indistinguishable from an arm that did not —
// which is the single loudest signal the daemon has.
//
// The two dated names fold, and they fold APART. qompack-<YYYYMMDD>.log (and
// qompack-<YYYYMMDD>.<n>.log once the day's log has rotated; logger.go's currentPath/rotatedPath)
// is ordinary logging. hook-quiet-<YYYYMMDD>.jsonl (internal/cli/hookclient.go, logQuiet) is a
// record that a hook could NOT reach the daemon — a degradation marker of the same family as the
// client spool above, and worth just as little to collapse into its neighbour.
const (
	x13v4LoudLog         = "logs/LOUD.log"
	x13v4DayLogPrefix    = "logs/qompack-"
	x13v4HookQuietPrefix = "logs/hook-quiet-"
)

// The .qompack/state files whose names carry a session id, and the suffix that distinguishes a live
// draft from a discarded one.
//
// state/draft-<session>.json is the checkpoint writer's live draft (internal/checkpoint/writer.go,
// draftPathFor). state/draft-<session>.stale.json is a draft that was SET ASIDE because another
// writer claimed the sequence first (writer.go, setAsideStaleDraft, which renames the former onto
// the latter). Keying the fold on the draft- prefix alone swallowed the second into the first, so
// an arm that discarded a draft and an arm that wrote one looked the same. The fold keys on the
// whole shape instead: prefix AND suffix.
const (
	x13v4DraftPrefix     = "state/draft-"
	x13v4RehydratePrefix = "state/rehydrate-"
	x13v4StaleSuffix     = ".stale.json"
	x13v4JSONSuffix      = ".json"
)

// x13v4Normalize erases the per-run components of a path so two arms are comparable: the session id
// in a state file name, the pid in a spool file name, the content-addressed object shards and
// capture sidecars, and the date in a log name.
//
// It erases those and NOTHING ELSE. Every fold here is a claim that the two arms may legitimately
// differ in that component, and a fold that reaches wider than its claim does not make this row
// flaky — it makes it PASS when it should fail, because the write-set comparison below is the
// evidence that the wave-3 residents add no hot-path work. A name this function does not recognize
// is returned verbatim rather than folded into a neighbour's token.
func x13v4Normalize(name string) string {
	switch {
	case len(name) > 7 && name[:7] == "objects":
		return "objects/<shard>/<object>"
	case len(name) > len(x13v4CapturePrefix) && name[:len(x13v4CapturePrefix)] == x13v4CapturePrefix:
		return x13v4CapturePrefix + "<shard>/<observation>.json"

	case name == x13v4LoudLog:
		return name
	case strings.HasPrefix(name, x13v4DayLogPrefix):
		return x13v4DayLogPrefix + "<date>.log"
	case strings.HasPrefix(name, x13v4HookQuietPrefix):
		return x13v4HookQuietPrefix + "<date>.jsonl"

	case strings.HasPrefix(name, x13v4DraftPrefix):
		return x13v4NormalizeSessionFile(name, x13v4DraftPrefix)
	case strings.HasPrefix(name, x13v4RehydratePrefix):
		return x13v4NormalizeSessionFile(name, x13v4RehydratePrefix)

	case strings.HasPrefix(name, x13v4WalPrefix):
		return "spool/<wal>"
	case strings.HasPrefix(name, x13v4ClientPrefix):
		return "spool/<client>"
	case strings.HasPrefix(name, x13v4BlobPrefix):
		return "spool/<blob>"

	case len(name) > 4 && name[:4] == "tmp/":
		return "tmp/<staging>"
	default:
		return name
	}
}

// x13v4NormalizeSessionFile folds the session id out of a state file named <prefix><session><suffix>
// while KEEPING the suffix, so draft-<session>.json and the draft-<session>.stale.json it can be
// renamed onto stay two distinct tokens.
//
// A name that matches the prefix but neither suffix is returned verbatim. Guessing at it would be
// the same mistake one level down: this function's whole job is to stop erasing what it cannot
// account for.
func x13v4NormalizeSessionFile(name, prefix string) string {
	rest := strings.TrimPrefix(name, prefix)
	for _, suffix := range []string{x13v4StaleSuffix, x13v4JSONSuffix} {
		if len(rest) > len(suffix) && strings.HasSuffix(rest, suffix) {
			return prefix + "<session>" + suffix
		}
	}
	return name
}

// x13v4CaptureCount is how many capture sidecars the burst's own deliveries wrote that before did
// not hold: new sidecars whose op is observe.tool and whose session is the arm's. The write set is a
// SET, so the fold in x13v4Normalize would also hide an arm that wrote a different NUMBER of
// sidecars; this keeps that visible, and a hook that took a second identity (a second lease, so a
// second sidecar) or lost its capture still moves the count.
//
// It counts by op and session rather than every new file because capture is asynchronous: Accept
// only WALs and leases a delivery, and an ingest worker publishes its sidecar afterwards
// (internal/daemon/ingest.go, publishCapture). The session-start and observe.prompt deliveries each
// arm sends just before its burst can therefore land inside it: under co-load the prompt's did, and
// a count of every new file read nine for eight tool hooks. Those sidecars belong to the arm's setup,
// not its burst, and both arms send the same ones. Sidecars are written through paths.WriteAtomic
// (staged under .qompack/tmp, then renamed), so every file this walk sees is complete.
func x13v4CaptureCount(t *testing.T, root string, sess core.SessionID, before map[string]int64) int {
	t.Helper()
	dot := paths.Of(root).Dot
	n := 0
	setup := map[string]int{}
	err := filepath.WalkDir(paths.Long(filepath.Join(dot, filepath.FromSlash(x13v4CapturePrefix))),
		func(p string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() {
				return nil //nolint:nilerr // an absent tree is a count of zero
			}
			rel, relErr := filepath.Rel(paths.Long(dot), p)
			if relErr != nil {
				return nil
			}
			if _, was := before[filepath.ToSlash(rel)]; was {
				return nil
			}
			raw, readErr := os.ReadFile(p)
			require.NoError(t, readErr, "a new capture sidecar must be readable: %s", rel)
			var sc struct {
				Op      string         `json:"op"`
				Session core.SessionID `json:"session"`
			}
			require.NoError(t, json.Unmarshal(raw, &sc), "a new capture sidecar must decode: %s", rel)
			if sc.Op == string(ipc.OpObserveTool) && sc.Session == sess {
				n++
			} else {
				setup[sc.Op+" "+string(sc.Session)]++
			}
			return nil
		})
	if err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	t.Logf("new capture sidecars in %s: %d from the burst (%s, %s); setup deliveries that landed late: %v",
		root, n, ipc.OpObserveTool, sess, setup)
	return n
}

// x13v4Existing lists what is already under .qompack/ so the write set is a DELTA.
//
// It quiesces first, for the same reason x13v4WriteSet does: a baseline taken while a write is still
// running is a baseline of a tree that does not exist a millisecond later.
func x13v4Existing(t *testing.T, root string) map[string]int64 {
	t.Helper()
	x13v4Quiesce(t, root)
	out := map[string]int64{}
	dot := paths.Of(root).Dot
	_ = filepath.WalkDir(paths.Long(dot), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil //nolint:nilerr // the tree may not exist yet
		}
		fi, statErr := d.Info()
		if statErr != nil {
			return nil //nolint:nilerr // a vanished temp file is not this listing's concern
		}
		if rel, relErr := filepath.Rel(paths.Long(dot), p); relErr == nil {
			out[filepath.ToSlash(rel)] = fi.Size()
		}
		return nil
	})
	return out
}

// x13v4HookFallbackToken is the one write-set token that the HOOK writes rather than the daemon, and
// writes because of the clock rather than because of what the daemon does: the client's fallback
// spool (internal/ipc/client.go spoolAndReturn). A hook appends its delivery there when the dial
// does not connect within State.ConnectDeadlineMs, when the one-byte ACK does not arrive within
// State.AckDeadlineMs, or when the daemon NAKs. The first two are wall-clock outcomes, and
// config.AckDeadlineMsWindows's own derivation says the deadline "is sized from quiet runs and
// engages the degrade path under ordinary load".
//
// So under load either arm can spool, and a set equality over this token is a timing gate in
// disguise, one this row's header rules out. Measured under a CPU and fsync load generator
// (w4-e2eflakes runs/c-x13-diag-count15-load-windows.log): 5 of 15 runs failed the equality on
// this token alone, in both directions. The observer-only arm was the one that spooled in three of
// them, which no wave-3 resident can cause, and in each of those three the spooled delivery's nonce
// was already on a line of that arm's WAL: the daemon had made it durable and only acknowledged it
// late. (In the other two the wave-3 arm's spool had been drained away before it could be read.)
// With this assertion and the two settles below in place, two runs of 20 under the same load
// generator (the second beside TestV3_HotPathUnchangedWithLedgerResident's 2,250 spawns) passed 40
// of 40; 18 of their 80 arms spooled, 41 deliveries in all, every one a late-ACK duplicate
// (runs/c-x13-count20-final-load-windows.log, runs/c-x13-count20-final-heavy-windows.log).
//
// The token is not dropped. The equality compares x13v4DaemonWriteSet, and each arm's fallback is
// asserted on its own by x13v4RequireFallbackIsTimingOnly, which fails if the daemon refused any
// hot-path request it received: the one way a daemon-side change makes a hook spool without leaving
// a trace in the daemon's own write set. The two other daemon-side routes to a NAK both write
// logs/LOUD.log, which stays in the equality: a breach into spool submode (applyHotPathTransition)
// and a WAL'd delivery that could not be leased (makeDurable).
const x13v4HookFallbackToken = "spool/<client>"

// x13v4DaemonWriteSet is set without x13v4HookFallbackToken, and without nothing else.
func x13v4DaemonWriteSet(set []string) []string {
	out := make([]string, 0, len(set))
	for _, name := range set {
		if name != x13v4HookFallbackToken {
			out = append(out, name)
		}
	}
	return out
}

// x13v4Fallback is what one arm's client fallback spool held, classified.
type x13v4Fallback struct {
	// LateACK are burst deliveries the hook spooled although the daemon had already made them
	// durable live: their nonce is on a line of the daemon's WAL. Only the ACK was late.
	LateACK []core.ToolUseID
	// NeverLive are burst deliveries the hook spooled with no live copy in the WAL. The daemon
	// never received them (the dial or the write did not reach it), since a request it received
	// and refused before making it durable is what Received > Durable catches.
	NeverLive []core.ToolUseID
	// Received is how many hot-path requests the daemon received live, of every op: the samples
	// its dispatch recorded (the B-A histogram, plus the ones rejected for an implausible TS).
	// x13v4Received reads it.
	Received int64
	// Durable is how many hot-path lines the daemon's WAL holds, of every op and session.
	Durable int
}

// x13v4SampleInvalidCounter is internal/daemon/handlers.go's counterHotpathSampleInvalid, spelled
// here because it is unexported: a live hot-path request whose TS failed validHotPathTS is counted
// there instead of in the B-A histogram, and it was still received.
//
// A spelled name can go stale silently: obs.Registry creates an instrument on first use, so a name
// the daemon no longer records under reads as zero rather than as missing, and so does the B-A
// histogram if the daemon stopped recording into obs.Budgets()' name for it. Presence cannot be
// required outright either, since a healthy run records no invalid sample and so never creates the
// counter. What catches a stale name is x13v4ClassifyFallback's accounting: every hot-path WAL line
// has exactly one sample behind it, so a sample recorded under a name this row does not read leaves
// Received short of Durable, and that fails the row (errX13v4Unaccounted).
const x13v4SampleInvalidCounter = "hotpath_sample_invalid"

// errX13v4Unaccounted is x13v4ClassifyFallback's error when the daemon holds more hot-path WAL lines
// than the samples this row reads account for. A sample recorded under a name the row does not read
// is the cause it exists to catch; a sample still in flight is the one benign cause, and
// x13v4RequireFallbackIsTimingOnly re-reads the registry until that has had its bound.
var errX13v4Unaccounted = errors.New("the daemon's recorded hot-path samples do not account for every " +
	"hot-path WAL line")

// x13v4Received is how many hot-path requests the daemon received live, read from its registry:
// the samples its dispatch recorded in the B-A histogram (obs.Budgets()' name for it, baHist) plus
// the ones it counted under x13v4SampleInvalidCounter instead.
func x13v4Received(snap obs.Snapshot, baHist string) int64 {
	return snap.Hists[baHist].N + snap.Counters[x13v4SampleInvalidCounter]
}

// x13v4ClassifyFallback decides whether one arm's client fallback is a timing fallback and nothing
// else. spooled are the requests in the client spool files the burst created or grew, wal every line
// of the daemon's WAL, received the daemon's count of hot-path requests received live.
//
// It is an error when a spooled request is not one of this arm's own burst deliveries — the premise
// on which the token leaves the equality — or when the daemon received more hot-path requests than
// it made durable. Every live hot-path request that passes privacy admission is handled by a route
// that calls Accept, whose first act is the WAL append (ingest.makeDurable), and dispatchOp records
// its sample only after that handler returns, so at a quiescent point Received <= Durable. (One
// refused at admission is answered OK, so it never spools, records no sample, and is caught by the
// capture count instead.) A request the daemon received and refused before its WAL line — a failed
// append, a handler that returned early — is the one refusal that leaves no trace in the daemon's
// write set, and it is exactly Received > Durable.
//
// The other direction is an error too, errX13v4Unaccounted. Accept is reached only from dispatchOp's
// hot-path routes (a client spool line the drain replays goes to runIngested and appends nothing),
// and dispatchOp records exactly one sample for every hot-path request past admission, so every
// hot-path WAL line has one sample behind it and a quiescent daemon has Received == Durable.
// Received < Durable means a sample was recorded under a name x13v4Received does not read, which
// would make Received undercount and the refusal check above weaker without a sound; see
// x13v4SampleInvalidCounter.
func x13v4ClassifyFallback(sess core.SessionID, spooled, wal []ipc.Request, received int64) (x13v4Fallback, error) {
	burst := map[core.ToolUseID]bool{}
	for i := range x13v4Turns {
		burst[x13v4BurstID(i)] = true
	}
	live := map[string]bool{}
	f := x13v4Fallback{Received: received}
	for _, req := range wal {
		if req.Op.HotPath() {
			f.Durable++
		}
		if req.Nonce != "" {
			live[req.Nonce] = true
		}
	}
	for _, req := range spooled {
		if req.Op != ipc.OpObserveTool || req.Session != sess || req.Event == nil || !burst[req.Event.ToolUseID] {
			id := core.ToolUseID("")
			if req.Event != nil {
				id = req.Event.ToolUseID
			}
			return f, fmt.Errorf("the client fallback spool holds %s for session %q, tool use %q, which is "+
				"not one of this arm's burst deliveries; only those may leave the write-set equality",
				req.Op, req.Session, id)
		}
		if req.Nonce != "" && live[req.Nonce] {
			f.LateACK = append(f.LateACK, req.Event.ToolUseID)
		} else {
			f.NeverLive = append(f.NeverLive, req.Event.ToolUseID)
		}
	}
	if int64(f.Durable) < f.Received {
		return f, fmt.Errorf("the daemon received %d hot-path requests live but its WAL holds only %d "+
			"hot-path lines: it refused a delivery before making it durable, so this arm's client "+
			"fallback is not a timing fallback", f.Received, f.Durable)
	}
	if f.Received < int64(f.Durable) {
		return f, fmt.Errorf("%w: the WAL holds %d hot-path lines but only %d samples were read, from the "+
			"B-A histogram and the %q counter; if the daemon now records a hot-path sample under another "+
			"name, Received undercounts and the refusal check is weakened", errX13v4Unaccounted,
			f.Durable, f.Received, x13v4SampleInvalidCounter)
	}
	return f, nil
}

// x13v4ReadRequests decodes every complete line of the files under root's .qompack/spool whose name
// passes keep, skipping a file that is gone by the time it is read (the daemon's drain removes a
// client spool it has consumed). A final fragment with no terminator is an append still in progress
// and not a line yet. It reads with paths.ReadFileShared, never os.ReadFile: that drain deletes the
// file, and an ordinary Windows handle would make its delete fail.
func x13v4ReadRequests(t *testing.T, root string, keep func(name string) bool) (reqs []ipc.Request, read []string) {
	t.Helper()
	dir := paths.Of(root).Spool
	entries, err := os.ReadDir(paths.Long(dir))
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() || !keep(e.Name()) {
			continue
		}
		b, err := paths.ReadFileShared(filepath.Join(dir, e.Name()))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		require.NoError(t, err, "reading spool/%s", e.Name())
		read = append(read, e.Name())
		lines := strings.Split(string(b), "\n")
		for _, line := range lines[:len(lines)-1] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			req, err := ipc.DecodeRequest([]byte(line))
			require.NoError(t, err, "spool/%s holds a line that does not decode: %s", e.Name(), line)
			reqs = append(reqs, req)
		}
	}
	return reqs, read
}

// x13v4ClientFallback reads what the burst's hooks appended to their client fallback spools: the
// spool/client-*.ndjson files under root that before did not hold, or held at another size.
//
// It is called the moment the burst's last hook has returned and before anything drains, which is
// when those files are certain to be whole and most likely to still exist: every hook appends its
// fallback line before it exits (ipc.Client.Send), and the daemon's drain removes a client spool
// once it has consumed it. A spool consumed during the burst itself is gone before the walk too, so
// the write set never names it either. The files read are returned for the log.
func x13v4ClientFallback(t *testing.T, root string, before map[string]int64) (spooled []ipc.Request, files []string) {
	t.Helper()
	return x13v4ReadRequests(t, root, func(name string) bool {
		if !strings.HasPrefix("spool/"+name, x13v4ClientPrefix) {
			return false
		}
		info, err := os.Stat(paths.Long(filepath.Join(paths.Of(root).Spool, name)))
		was, ok := before["spool/"+name]
		return err == nil && (!ok || was != info.Size())
	})
}

// x13v4RequireFallbackIsTimingOnly asserts, for the arm r serves at root, what the write-set equality
// no longer asserts about x13v4HookFallbackToken: what the burst's hooks spooled (x13v4ClientFallback,
// read right after the burst) is a timing fallback of this arm's own burst and nothing else
// (x13v4ClassifyFallback). It runs after the arm's walk, when every live request has been handled;
// the WAL and the counters it reads are the daemon's and do not go away.
//
// A sample can still be in flight then: dispatchOp records it after its handler returns, and the
// worker can publish the delivery before that. So while the samples fall short of the WAL
// (errX13v4Unaccounted) the registry is read again on obsProcessTick, within obsProcessBound, the
// bound this package already gives the daemon to finish processing what it has accepted. A shortfall
// that outlasts it is not in flight, and fails the row.
func x13v4RequireFallbackIsTimingOnly(t *testing.T, r *v4Rig, root string, sess core.SessionID,
	spooled []ipc.Request, files []string,
) {
	t.Helper()
	wal, _ := x13v4ReadRequests(t, root, func(name string) bool {
		return strings.HasPrefix("spool/"+name, x13v4WalPrefix)
	})
	baHist := x1v5HistOf(t, obs.BA)
	f, err := x13v4ClassifyFallback(sess, spooled, wal, x13v4Received(r.Opts.Metrics.Snapshot(), baHist))
	if errors.Is(err, errX13v4Unaccounted) {
		ticker := time.NewTicker(obsProcessTick)
		defer ticker.Stop()
		deadline := time.NewTimer(obsProcessBound)
		defer deadline.Stop()
	await:
		for errors.Is(err, errX13v4Unaccounted) {
			select {
			case <-ticker.C:
				f, err = x13v4ClassifyFallback(sess, spooled, wal, x13v4Received(r.Opts.Metrics.Snapshot(), baHist))
			case <-deadline.C:
				break await
			}
		}
	}
	require.NoError(t, err, "arm %s (B-A histogram %q)", sess, baHist)
	t.Logf("arm %s: client fallback files read %v: %d late-ACK duplicates %v, %d never reached the daemon "+
		"live %v; the daemon received %d hot-path requests live and holds %d hot-path WAL lines",
		sess, files, len(f.LateACK), f.LateACK, len(f.NeverLive), f.NeverLive, f.Received, f.Durable)
}

// x13v4SettleSetup waits, before an arm's baseline is taken, until everything its setup hooks sent
// has reached the daemon and been leased: no client fallback spool is left under spool/ (the drain
// removes one once it has consumed it), and the setup prompt's capture sidecar exists (the ingest
// worker writes it for every leased hot-path delivery, whatever the observer then does with the
// prompt). It drives the rig's Drain on every tick, within v4Rig.WaitIndexed's bound.
//
// Without it the burst window could hold SETUP work, and only on one arm. Under heavy load a setup
// hook can miss its connect or ACK deadline, or its delivery can still be on its way through the
// daemon's durable path when x13v4Existing's settle window has passed, since nothing of it shows
// under tmp/ or state/pending yet. The wave-3 arm's residency steps then make further requests of
// its daemon before its baseline (a compact SessionStart through the real binary, an idle pass),
// and the observer-only arm makes none. With the CPU at 100 % the observer-only arm's delta alone
// carried the delivery journal's own files, which are created when the journal first opens
// (state/delivery-journal.json and state/delivery-generations/manifest.jsonl in five runs, the
// position seals and state/delivery-journal-log.jsonl too in two more; w4-e2eflakes runs/
// c-x13-count20-load-cpu100-windows.log). Settling both arms the same way puts setup where it
// belongs.
func x13v4SettleSetup(t *testing.T, r *v4Rig, root string, sess core.SessionID) {
	t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	deadline := time.Now().Add(obsProcessBound)
	for {
		_, _ = r.D.Drain(ctx)
		pending := x13v4FilesWithPrefix(root, x13v4ClientPrefix)
		captured := x13v4PromptCaptured(t, root, sess)
		if len(pending) == 0 && captured {
			return
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, "the setup never settled",
				"after %s, %s still held client fallback spools %v (setup prompt captured: %v); LOUD: %v",
				obsProcessBound, root, pending, captured, loudLines(t, root))
		}
		<-ticker.C
	}
}

// x13v4FilesWithPrefix names the files under root's .qompack/spool whose slash path relative to
// .qompack starts with prefix.
func x13v4FilesWithPrefix(root, prefix string) []string {
	entries, err := os.ReadDir(paths.Long(paths.Of(root).Spool))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix("spool/"+e.Name(), prefix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// x13v4PromptCaptured reports whether root holds a capture sidecar for sess's observe.prompt.
func x13v4PromptCaptured(t *testing.T, root string, sess core.SessionID) bool {
	t.Helper()
	dot := paths.Of(root).Dot
	found := false
	_ = filepath.WalkDir(paths.Long(filepath.Join(dot, filepath.FromSlash(x13v4CapturePrefix))),
		func(p string, d fs.DirEntry, werr error) error {
			if werr != nil || d.IsDir() || found {
				return nil //nolint:nilerr // an absent tree holds no capture yet
			}
			raw, err := paths.ReadFileShared(p)
			if err != nil {
				return nil //nolint:nilerr // a sidecar being replaced is read on the next tick
			}
			var sc struct {
				Op      string         `json:"op"`
				Session core.SessionID `json:"session"`
			}
			found = json.Unmarshal(raw, &sc) == nil && sc.Op == string(ipc.OpObservePrompt) && sc.Session == sess
			return nil
		})
	return found
}

// x13v4BurstID is the tool_use id of the burst's i-th hook, the same on both arms.
func x13v4BurstID(i int) core.ToolUseID { return core.ToolUseID(fmt.Sprintf("toolu_v4x13_%02d", i)) }

// x13v4WaitBurstIndexed waits until every one of the burst's own x13v4Turns tool uses is in root's
// index/tool_use.jsonl, driving the rig's Drain on every tick exactly as v4Rig.WaitIndexed does —
// once unconditionally first, so both arms take the same path (8658ccb) — and within the same
// bound.
//
// It waits for the burst's IDS, not for a line count, and that is the correction. index/tool_use.jsonl
// also holds the setup prompt's record (prompt_<session>_0 — the observer indexes a prompt there
// too), so WaitIndexed(x13v4Turns) returned once the prompt and SEVEN of the eight tool uses had
// landed. The eighth could still be queued behind its lease with nothing yet under .qompack/tmp or
// state/pending, so x13v4Quiesce's settle window (4 x the 50 ms B-C budget) could pass before its
// publication began, and the walk and the capture count then read a burst that was not finished.
// Under a CPU and fsync load generator that was a red of its own: "the observer-only arm must write
// one capture sidecar per hook in the burst", expected 8, actual 7 (w4-e2eflakes runs/
// c-x13-count10-load-before-windows.log). A tool use's index record lands after its capture
// sidecar (publication order), so once all eight ids are indexed all eight sidecars are durable.
//
// It also waits until no client fallback spool is left, i.e. until the drain has consumed and
// removed every copy a hook spooled. A spool left standing is work for the daemon's spool watcher,
// which passes over it on its own clock (spoolCheckInterval, then after 2, 4, 8 intervals) and
// rewrites state/drain.json through a tmp/ staging file on every pass (drainer.saveState): a
// writer that no settle window bounds, whose staging file a walk can catch. With the CPU at 100 %
// one arm's walk did catch a tmp/<staging> file that the other's did not (runs/
// c-x13-count20-load-cpu100-windows.log). What the hooks spooled has already been read by
// x13v4ClientFallback, so nothing the per-arm assertion needs is lost.
func x13v4WaitBurstIndexed(t *testing.T, r *v4Rig, root string) {
	t.Helper()
	ctx := context.Background()
	ticker := time.NewTicker(obsProcessTick)
	defer ticker.Stop()
	deadline := time.Now().Add(obsProcessBound)
	_, _ = r.D.Drain(ctx)
	for {
		missing := x13v4BurstNotIndexed(root)
		pending := x13v4FilesWithPrefix(root, x13v4ClientPrefix)
		if len(missing) == 0 && len(pending) == 0 {
			return
		}
		if time.Now().After(deadline) {
			require.FailNowf(t, "the burst never settled",
				"after %s, %d of the burst's %d tool uses were still not in %s's index/tool_use.jsonl %v, "+
					"and client fallback spools %v were still unconsumed; LOUD: %v",
				obsProcessBound, len(missing), x13v4Turns, root, missing, pending, loudLines(t, root))
		}
		<-ticker.C
		_, _ = r.D.Drain(ctx)
	}
}

// x13v4BurstNotIndexed lists the burst's tool use ids that root's index/tool_use.jsonl does not hold.
func x13v4BurstNotIndexed(root string) []core.ToolUseID {
	indexed := map[core.ToolUseID]bool{}
	for _, line := range obsToolUseLines(root) {
		var rec struct {
			ID core.ToolUseID `json:"id"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil {
			indexed[rec.ID] = true
		}
	}
	var missing []core.ToolUseID
	for i := range x13v4Turns {
		if id := x13v4BurstID(i); !indexed[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// TestV4_HotPathUnchangedWithTheFullWave3ResidentSet is V4-VERIFY §4.13, structural arm.
//
// The negative control is the last arm: one idle pass with the same resident set MUST change the
// write set. That is what proves the comparison can see added work at all — without it, "the write
// sets are identical" would also hold for a comparison that could not distinguish anything.
func TestV4_HotPathUnchangedWithTheFullWave3ResidentSet(t *testing.T) {
	// ── Arm A: the full wave-3 resident set ──────────────────────────────────────────────────────
	p := v4Project(t)
	r := v4StartRig(t, p)
	env := e2eEnv(p)

	obsRunHook(t, r.Bin, []string{"session-start"}, sessionStartFor(t, p.Root, x13v4Session), env)
	obsRunHook(t, r.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, p.Root, x13v4Session, "hold every wave-3 subsystem resident"), env)

	// The resident set this row names includes the negative-knowledge ledger, and that one is
	// opened lazily, on a compaction — so the row has to be a daemon that has compacted before it
	// can assert the ledger is resident. See OpenLedgerByCompacting.
	r.OpenLedgerByCompacting(t, x13v4Session)

	// Residency is ASSERTED, not assumed: without it the two arms could be the same daemon twice.
	require.NotNil(t, r.W, "the checkpoint writer must be resident")
	_, srcErr := r.Src()
	require.NoError(t, srcErr, "the full SourceSet must resolve: store, segments, ledger, pins, graph, grammar, tokens")
	require.NotNil(t, r.Opts.LedgerHandle(), "the negative-knowledge ledger must be open")
	require.NotNil(t, r.Opts.Store, "the store must be open")
	require.NotNil(t, r.Opts.Graph, "the dependence DAG must be open")
	registered := r.RunIdle(t)
	for _, want := range []string{"advance_frontier", "act.checkpoint_cadence", "materialize_pins"} {
		require.Contains(t, registered, want,
			"the wave-3 idle task %q must be registered, or 'the full resident set' is a claim about "+
				"nothing; ran=%v", want, registered)
	}

	// The hook burst, with NO idle pass inside it: this is the hot path and nothing else.
	x13v4SettleSetup(t, r, p.Root, x13v4Session)
	beforeA := x13v4Existing(t, p.Root)
	for i := range x13v4Turns {
		obsRunHook(t, r.Bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x13v4Session, string(x13v4BurstID(i)),
				fmt.Sprintf("src/x13_%02d.go", i),
				fmt.Sprintf("package x13\n\nfunc h%02d() error { return nil }\n", i)), env)
	}
	spooledA, spoolFilesA := x13v4ClientFallback(t, p.Root, beforeA)
	x13v4WaitBurstIndexed(t, r, p.Root)
	fullSet := x13v4WriteSet(t, p.Root, beforeA)
	require.NotEmpty(t, fullSet, "the hook burst must have written something")
	x13v4RequireFallbackIsTimingOnly(t, r, p.Root, x13v4Session, spooledA, spoolFilesA)

	// ── Arm B: the SAME burst against an observer-only daemon ────────────────────────────────────
	pr := v4Project(t)
	rr := v4StartObserverOnly(t, pr)
	envr := e2eEnv(pr)

	obsRunHook(t, rr.Bin, []string{"session-start"}, sessionStartFor(t, pr.Root, x13v4RefSession), envr)
	obsRunHook(t, rr.Bin, []string{"observe", "prompt"},
		obsPromptPayload(t, pr.Root, x13v4RefSession, "hold every wave-3 subsystem resident"), envr)

	x13v4SettleSetup(t, rr, pr.Root, x13v4RefSession)
	beforeB := x13v4Existing(t, pr.Root)
	for i := range x13v4Turns {
		obsRunHook(t, rr.Bin, []string{"observe", "tool"},
			obsToolPayload(t, pr.Root, x13v4RefSession, string(x13v4BurstID(i)),
				fmt.Sprintf("src/x13_%02d.go", i),
				fmt.Sprintf("package x13\n\nfunc h%02d() error { return nil }\n", i)), envr)
	}
	spooledB, spoolFilesB := x13v4ClientFallback(t, pr.Root, beforeB)
	x13v4WaitBurstIndexed(t, rr, pr.Root)
	refSet := x13v4WriteSet(t, pr.Root, beforeB)
	x13v4RequireFallbackIsTimingOnly(t, rr, pr.Root, x13v4RefSession, spooledB, spoolFilesB)

	// ── The claim: the wave-3 residents add NO work to the hot path ──────────────────────────────
	// Every file the DAEMON touched is compared. The hook's own fallback spool was asserted above,
	// per arm, rather than here: whether a hook spools is decided by its connect and ACK deadlines,
	// so it differs between arms under load (x13v4HookFallbackToken).
	require.Equal(t, x13v4DaemonWriteSet(refSet), x13v4DaemonWriteSet(fullSet),
		"a hook burst must make the daemon touch the same files whether or not the checkpoint writer, "+
			"the frontier advancer, the pin store, the ledger and the cadence task are resident. A "+
			"difference here is wave-3 work that migrated ONTO the hot path.\nwith wave 3: %v\nwithout:    %v"+
			"\nLOUD with wave 3: %v\nLOUD without:    %v",
		fullSet, refSet, loudLines(t, p.Root), loudLines(t, pr.Root))

	// The fold above compares KINDS. The capture path writes exactly one sidecar per leased
	// delivery, so the burst's eight tool hooks must have left eight on each arm — the count is
	// what the set cannot see, and a wave-3 resident that captured more (or fewer) would hide there.
	require.Equal(t, x13v4Turns, x13v4CaptureCount(t, pr.Root, x13v4RefSession, beforeB),
		"the observer-only arm must write one capture sidecar per hook in the burst")
	require.Equal(t, x13v4Turns, x13v4CaptureCount(t, p.Root, x13v4Session, beforeA),
		"the wave-3 arm must write one capture sidecar per hook in the burst, no more")

	// The scheduler and the checkpointer are off the hot path in the strongest observable sense:
	// no draft and no artifact exist after a burst with no idle pass.
	require.Empty(t, cpCheckpointArtifacts(t, p.Root),
		"no hook in the burst may seal a checkpoint — sealing is idle or PreCompact work")

	// ── NEGATIVE CONTROL: one idle pass MUST change the write set ────────────────────────────────
	cpCloseObserverSegment(t, r.Segs, x13v4Session)
	cpCloseSegment(t, r.Segs, x13v4Session, 0, 9)
	beforeIdle := x13v4Existing(t, p.Root)
	r.RunIdle(t)
	idleSet := x13v4WriteSet(t, p.Root, beforeIdle)
	require.NotEmpty(t, idleSet,
		"NEGATIVE CONTROL: an idle pass with the same resident set must write files the hook burst "+
			"did not. If it does not, the comparison above cannot see wave-3 work at all and its "+
			"passing means nothing")
	draft := filepath.Join(paths.Of(p.Root).State, "draft-"+string(x13v4Session)+".json")
	if _, err := os.Stat(paths.Long(draft)); err == nil {
		require.Contains(t, idleSet, "state/draft-<session>.json",
			"the idle pass's own draft write must show up in the delta; idle wrote %v", idleSet)
	}
}
