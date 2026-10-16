package main

// The Stage A arms of `devtool live-eval` (V7 W6 items 1, 4, 7 and 12): the trial order with
// per-arm counts, each arm's session environment and project, the slot naming for records that are
// not scored, and the steer arm's hello file. The arm states themselves are eval.LiveArmSpec's.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/modwire"
)

// The session environment the harness sets. Every arm's session has the auto-updater off (the host
// is pinned, W6 item 12), the operator's claude.ai MCP servers off and the daemon idle exit; every
// Qompack arm sets both mod switches, on for steer and off otherwise.
const (
	liveEnvMCPServers  = "ENABLE_CLAUDEAI_MCP_SERVERS"
	liveEnvAutoUpdater = "DISABLE_AUTOUPDATER"
	liveEnvIdleExit    = "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS"
	liveEnvModEnabled  = "QOMPACK_RUNTIME__MOD__ENABLED"
	liveEnvModSteering = "QOMPACK_RUNTIME__MOD__STEERING"
)

// liveStoreDir is the project-relative store a Qompack arm's project gets before its session.
const liveStoreDir = ".qompack"

// liveCompactHeading is the CLAUDE.md heading the host reads compaction instructions from; the
// generic arm's text G goes under it.
const liveCompactHeading = "# Compact instructions"

// liveConfigErrorLimit is how many configuration errors one run tolerates: the third stops it for
// investigation (W6 item 4).
const liveConfigErrorLimit = 3

// The kinds of record a slot holds besides its one scored trial.json (coordinator decision D-b): a
// configuration-discarded attempt and a limit-cut one, each numbered per slot from 1.
const (
	liveSlotDiscarded = "discarded"
	liveSlotLimited   = "limited"
	liveScoredRecord  = "trial.json"
)

// liveAttemptFiles are the files a trial attempt writes into its slot beside its record. An attempt
// that is set aside takes them with it under its numbered name, so the re-run cannot overwrite them.
var liveAttemptFiles = []string{
	"stream.jsonl", "stderr.txt", "invocation.json", "ledger.json", "estimate.json", "transcript-facts.json",
	"transcript.raw.jsonl",
}

// planLiveTrials orders the trials in balanced blocks of rounds (W6 item 1). The run has as many
// rounds as the largest per-arm count; an arm with n of R trials runs its k-th trial in round
// ceil((2k-1)·R/(2n)), the middle of its k-th share of the schedule, so a short arm's trials spread
// over the whole schedule with their mean near the schedule's middle, neither bunched at its start
// nor pushed late, and arm is not confounded with week. Each round is one block, and the order inside a block
// is shuffled from seed, so no arm or task always goes first.
func planLiveTrials(tasks []eval.LiveTask, arms []string, counts map[string]int, seed int64) []eval.LivePlannedTrial {
	rounds := 0
	for _, a := range arms {
		rounds = max(rounds, counts[a])
	}
	blocks := make([][]eval.LivePlannedTrial, rounds)
	for _, a := range arms {
		n := counts[a]
		for k := 1; k <= n; k++ {
			r := ((2*k-1)*rounds + 2*n - 1) / (2 * n) // ceil((2k-1)·R/(2n)), in 1..R
			for _, t := range tasks {
				blocks[r-1] = append(blocks[r-1], eval.LivePlannedTrial{Task: t.ID, Arm: a, Trial: k})
			}
		}
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
	var out []eval.LivePlannedTrial
	for _, b := range blocks {
		rng.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
		out = append(out, b...)
	}
	return out
}

// liveOrderSeed is the seed a run's order is shuffled from: the operator's --order-seed, or one
// derived from the run id. plan.json records it either way.
func liveOrderSeed(explicit int64, runID string) int64 {
	if explicit != 0 {
		return explicit
	}
	h := sha256.Sum256([]byte(runID))
	s := int64(binary.BigEndian.Uint64(h[:8]) >> 1) // the top bit is dropped, so it fits
	if s == 0 {
		s = 1
	}
	return s
}

// liveSessionEnv is the environment the harness sets for one trial of an arm.
func liveSessionEnv(arm eval.LiveArmState, idleExit int) []string {
	env := []string{
		liveEnvMCPServers + "=false",
		liveEnvAutoUpdater + "=1",
		fmt.Sprintf("%s=%d", liveEnvIdleExit, idleExit),
	}
	if arm.Plugin {
		env = append(env,
			liveEnvModEnabled+"="+strconv.FormatBool(arm.ModEnabled),
			liveEnvModSteering+"="+strconv.FormatBool(arm.ModSteering))
	}
	return env
}

// checkLiveArmEnv verifies that env is the environment arm declares: each harness-set variable once
// with the arm's value, and no mod switch on an arm without the plugin. A mismatch is a
// configuration error.
func checkLiveArmEnv(arm eval.LiveArmState, idleExit int, env []string) error {
	got := map[string][]string{}
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		got[strings.ToUpper(name)] = append(got[strings.ToUpper(name)], val)
	}
	want := map[string]string{
		liveEnvMCPServers:  "false",
		liveEnvAutoUpdater: "1",
		liveEnvIdleExit:    strconv.Itoa(idleExit),
	}
	if arm.Plugin {
		want[liveEnvModEnabled] = strconv.FormatBool(arm.ModEnabled)
		want[liveEnvModSteering] = strconv.FormatBool(arm.ModSteering)
	}
	for _, name := range []string{liveEnvMCPServers, liveEnvAutoUpdater, liveEnvIdleExit, liveEnvModEnabled, liveEnvModSteering} {
		w, set := want[name]
		vals := got[name]
		switch {
		case !set && len(vals) > 0:
			return fmt.Errorf("the %s arm's environment sets %s, which only a Qompack arm sets", arm.Arm, name)
		case set && (len(vals) != 1 || vals[0] != w):
			return fmt.Errorf("the %s arm's environment has %s=%v, not %s", arm.Arm, name, vals, w)
		}
	}
	return nil
}

// liveCLIEnv is the environment a `claude` subcommand (plugin management, --version) runs with: the
// driver's own, with the auto-updater off, so no command of the run updates the pinned host.
func liveCLIEnv(base []string) []string {
	return liveProcessEnv(base, liveProcSpec{Env: []string{liveEnvAutoUpdater + "=1"}})
}

// applyLiveArmProject makes a freshly copied fixture the arm's project before its session: generic's
// root CLAUDE.md gains the `# Compact instructions` section with text G (created when absent,
// appended otherwise), and every Qompack arm gets an empty store, so the mod's hello finds one
// whichever hook runs first (R25). Other arms keep the fixture as it is.
func applyLiveArmProject(project string, arm eval.LiveArmState, textG string) error {
	if arm.ClaudeMDCompactSection {
		if strings.TrimSpace(textG) == "" {
			return errors.New("the generic arm needs the task set's text G (analysis.generic_compact_instructions)")
		}
		path := filepath.Join(project, "CLAUDE.md")
		old, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading CLAUDE.md: %w", err)
		}
		if err := os.WriteFile(path, liveGenericClaudeMD(old, textG), liveFilePerm); err != nil {
			return fmt.Errorf("writing CLAUDE.md: %w", err)
		}
	}
	if arm.StorePrecreated {
		if err := os.Mkdir(filepath.Join(project, liveStoreDir), liveDirPerm); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("pre-creating the store: %w", err)
		}
	}
	return nil
}

// checkLiveArmProject verifies the project is what the arm declares just before its session: the
// store present exactly on a Qompack arm, and the root CLAUDE.md the fixture's own bytes, plus the
// section with text G on generic.
func checkLiveArmProject(project, fixture string, arm eval.LiveArmState, textG string) error {
	info, err := os.Stat(filepath.Join(project, liveStoreDir))
	has := err == nil && info.IsDir()
	if has != arm.StorePrecreated {
		return fmt.Errorf("the %s arm's project has a store before the session: %t, want %t", arm.Arm, has, arm.StorePrecreated)
	}
	orig, err := os.ReadFile(filepath.Join(fixture, "CLAUDE.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading the fixture's CLAUDE.md: %w", err)
	}
	want := string(orig)
	if arm.ClaudeMDCompactSection {
		want = string(liveGenericClaudeMD(orig, textG))
	}
	got, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading the project's CLAUDE.md: %w", err)
	}
	if string(got) != want {
		return fmt.Errorf("the %s arm's CLAUDE.md is not what the arm declares (%d bytes, want %d)", arm.Arm, len(got), len(want))
	}
	return nil
}

// liveGenericClaudeMD is the generic arm's root CLAUDE.md made from the fixture's (orig, empty when
// it has none): orig, a blank line, then the `# Compact instructions` heading and text G.
func liveGenericClaudeMD(orig []byte, textG string) []byte {
	var b strings.Builder
	if len(orig) > 0 {
		b.Write(orig)
		if !strings.HasSuffix(string(orig), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(liveCompactHeading + "\n\n" + strings.TrimRight(textG, "\n") + "\n")
	return []byte(b.String())
}

// liveSetAside writes rec into slotDir as trial.<kind>-<n>.json, n the next free number for kind in
// that slot, and renames the attempt's own files beside it the same way (stream.jsonl becomes
// stream.<kind>-<n>.jsonl). It never writes trial.json, the only name a scored trial has, so a
// set-aside attempt never fills its slot (D-b). It returns the record's path.
func liveSetAside(slotDir, kind string, rec eval.LiveTrial) (string, error) {
	n := 1
	for {
		if _, err := os.Stat(filepath.Join(slotDir, liveSetAsideName("trial.json", kind, n))); errors.Is(err, os.ErrNotExist) {
			break
		}
		n++
	}
	for _, f := range liveAttemptFiles {
		src := filepath.Join(slotDir, f)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(slotDir, liveSetAsideName(f, kind, n))); err != nil {
			return "", fmt.Errorf("live-eval: setting %s aside: %w", f, err)
		}
	}
	p := filepath.Join(slotDir, liveSetAsideName("trial.json", kind, n))
	return p, writeJSONFile(p, rec)
}

// liveSetAsideName is name with ".<kind>-<n>" before its extension.
func liveSetAsideName(name, kind string, n int) string {
	ext := filepath.Ext(name)
	return fmt.Sprintf("%s.%s-%d%s", strings.TrimSuffix(name, ext), kind, n, ext)
}

// liveModFiles is the bundle paths of the mod sources a --mods bundle ships for goos and a classic
// bundle never does: the paths the mods plugin tree has beyond the classic one. BUNDLE.json carries
// no mods flag, so a bundle's file list is how its shape is told.
func liveModFiles(goos string) ([]string, error) {
	plain, err := pluginTreeFiles("0.0.0", goos, pluginTreeOptions{})
	if err != nil {
		return nil, err
	}
	mods, err := pluginTreeFiles("0.0.0", goos, pluginTreeOptions{mods: true})
	if err != nil {
		return nil, err
	}
	var out []string
	for p := range mods {
		if _, ok := plain[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	if len(out) == 0 {
		return nil, errors.New("live-eval: the mods plugin tree adds no file to the classic one")
	}
	return out, nil
}

// judgeLiveHello reads the steer arm's hello file from project after its session (W6 item 4): there
// must be exactly one state/mod-hello-*.json, it must parse under the wire contract, its session must
// be sessionID (the stream's), its host must meet modwire.MinHostVersion, and its served hello must
// have said steering true. Whatever it finds is a treatment outcome, never a reason to drop the trial.
func judgeLiveHello(project, sessionID string) *eval.LiveHelloOutcome {
	dir := filepath.Join(project, liveStoreDir, modwire.StateDir)
	matches, _ := filepath.Glob(filepath.Join(dir, "mod-hello-*.json"))
	out := &eval.LiveHelloOutcome{Files: len(matches)}
	switch len(matches) {
	case 0:
		out.Problem = "no state/mod-hello-*.json after the session"
		return out
	case 1:
	default:
		out.Problem = fmt.Sprintf("%d state/mod-hello-*.json files after the session, not one", len(matches))
		return out
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		out.Problem = "reading " + filepath.Base(matches[0]) + ": " + err.Error()
		return out
	}
	h, err := modwire.ParseHelloFile(raw)
	if err != nil {
		out.Problem = filepath.Base(matches[0]) + " does not parse: " + err.Error()
		return out
	}
	out.SessionID, out.HostVersion, out.Enabled, out.Steering = h.SessionID, h.HostVersion, h.Enabled, h.Steering
	switch {
	case h.SessionID != sessionID:
		out.Problem = fmt.Sprintf("the hello file is for session %s, not the stream's %s", h.SessionID, sessionID)
	case !modwire.MeetsMinHostVersion(h.HostVersion):
		out.Problem = fmt.Sprintf("host %s is below the mod's minimum %s", h.HostVersion, modwire.MinHostVersion)
	case !h.Steering:
		out.Problem = "the served hello said steering false"
	default:
		out.OK = true
	}
	return out
}
