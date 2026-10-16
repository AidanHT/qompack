package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// briefArgs is `mod brief` with both mod switches on for the invocation, plus extra flags.
func briefArgs(extra ...string) []string {
	args := append(append([]string{"brief"}, enableMod...), enableSteering...)
	return append(args, extra...)
}

// briefRequestFor is a brief request for cwd, as the mod writes it.
func briefRequestFor(t *testing.T, cwd, hostVersion, trigger string) string {
	t.Helper()
	b, err := modwire.Encode(modwire.BriefRequest{
		V: modwire.Version, SessionID: "sess-1", CWD: cwd, HostVersion: hostVersion, Trigger: trigger,
	})
	require.NoError(t, err)
	return string(b)
}

// decodeBriefOut reads stdout as exactly one brief response line.
func decodeBriefOut(t *testing.T, stdout string) modwire.BriefResponse {
	t.Helper()
	require.True(t, strings.HasSuffix(stdout, "\n"), "one line, newline-terminated: %q", stdout)
	require.Equal(t, 1, strings.Count(stdout, "\n"), "exactly one JSON object: %q", stdout)
	var r modwire.BriefResponse
	require.NoError(t, json.Unmarshal([]byte(stdout), &r), "stdout: %q", stdout)
	require.NoError(t, r.Validate(modwire.BriefMaxChars))
	return r
}

// briefRecordPath is where sess-1's brief-outcome log is written.
func (p modProject) briefRecordPath() string {
	return filepath.Join(paths.Of(p.root).State, "mod-brief-sess-1.jsonl")
}

// briefRecords reads every line of sess-1's brief-outcome log; none when it does not exist.
func (p modProject) briefRecords(t *testing.T) []modwire.BriefRecord {
	t.Helper()
	b, err := os.ReadFile(p.briefRecordPath())
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []modwire.BriefRecord
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		r, err := modwire.ParseBriefRecord(sc.Bytes())
		require.NoError(t, err, "line %q", sc.Text())
		out = append(out, r)
	}
	require.NoError(t, sc.Err())
	return out
}

// briefDaemon is a fake daemon for the brief rows: it records every mod call it receives and
// answers with answer(call).
type briefDaemon struct {
	mu    sync.Mutex
	reqs  []ipc.Request
	calls []ipc.ModCall
}

func (d *briefDaemon) serve(t *testing.T, root string, answer func(ipc.ModCall) ipc.Response) {
	t.Helper()
	fakeModDaemon(t, root, func(ctx context.Context, req ipc.Request) ipc.Response {
		call, err := ipc.DecodeModCall(req.Raw)
		d.mu.Lock()
		d.reqs = append(d.reqs, req)
		d.calls = append(d.calls, call)
		d.mu.Unlock()
		if err != nil || req.Op != ipc.OpMod {
			return ipc.Response{OK: false, Err: "not a mod call"}
		}
		return answer(call)
	})
}

func (d *briefDaemon) received() ([]ipc.Request, []ipc.ModCall) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ipc.Request(nil), d.reqs...), append([]ipc.ModCall(nil), d.calls...)
}

// briefData encodes a brief response as a daemon's answer carries it.
func briefData(t *testing.T, r modwire.BriefResponse) ipc.Response {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return ipc.Response{OK: true, Data: b}
}

// servedBrief is a served brief with two items.
func servedBrief() modwire.BriefResponse {
	return modwire.BriefResponse{
		Status:       modwire.Served(),
		Instructions: modwire.BriefMarker + "\n- [inv_1] Never log access tokens.\n- [intent:original] Fix the 500s.",
		FactIDs:      []string{"inv_1", "intent:original"},
	}
}

// briefEnv is p's Env with an executable path, so a cold exchange would spawn, and Started set
// elapsed before the frozen now.
func (p modProject) briefEnv(stdin string, elapsed time.Duration) Env {
	env := p.env(stdin, nil)
	env.Self = "qompack-under-test"
	env.Started = modTestNow.Add(-elapsed)
	return env
}

// TestModBrief_RefusedWritesNothing covers every refusal of the brief: the project's (as for hello),
// steering off, and a host below the minimum or with an unreadable version. Each answers refused,
// exits 0, spawns nothing and leaves the project and the home as they were, record included.
func TestModBrief_RefusedWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name          string
		withStore     bool
		homeRoot      bool
		stateOffAlive bool
		version       string
		args          []string
	}{
		{name: "both switches off by default", withStore: true, args: []string{"brief"}},
		{name: "steering off", withStore: true, args: append([]string{"brief"}, enableMod...)},
		{name: "enabled off", withStore: true, args: append([]string{"brief"}, enableSteering...)},
		{name: "no store", args: briefArgs()},
		{name: "home root", withStore: true, homeRoot: true, args: briefArgs()},
		{name: "mode off in the configuration", withStore: true, args: briefArgs("--set", "runtime.mode=off")},
		{name: "mode off in a live daemon's state.bin", withStore: true, stateOffAlive: true, args: briefArgs()},
		{name: "host below the minimum", withStore: true, version: "2.1.299", args: briefArgs()},
		{name: "host prerelease of the minimum", withStore: true, version: "2.1.300-dev.1", args: briefArgs()},
		{name: "host version unreadable", withStore: true, version: "dev", args: briefArgs()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newModProject(t, tc.withStore)
			if tc.homeRoot {
				p.home = p.root
			}
			if tc.stateOffAlive {
				require.NoError(t, paths.EnsureLayout(paths.Of(p.root)))
				staleModeOffState(t, p.root)
				useStateDaemonAlive(t, true)
			}
			version := tc.version
			if version == "" {
				version = modwire.MinHostVersion
			}
			spawns := &modSpawns{}
			spawns.install(t)
			before := treeOf(t, p.root, p.home)

			code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, version, "auto"), 0), tc.args...)
			require.Equal(t, ExitOK, code, stderr)
			r := decodeBriefOut(t, stdout)
			require.False(t, r.OK)
			require.Equal(t, modwire.ReasonRefused, r.Reason)
			require.NotEmpty(t, r.Detail)
			require.NotContains(t, r.Detail, p.root, "a detail never names a path")

			require.Zero(t, spawns.count(), "a refused brief starts no daemon")
			require.Equal(t, before, treeOf(t, p.root, p.home), "a refused brief creates and changes nothing")
		})
	}
}

// TestModBrief_ColdStartsTheDaemonAndRecordsCold: with no daemon listening the brief starts the
// detached daemon, answers cold at once, and records the call; nothing is spooled.
func TestModBrief_ColdStartsTheDaemonAndRecordsCold(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 40*time.Millisecond),
		briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	r := decodeBriefOut(t, stdout)
	require.False(t, r.OK)
	require.Equal(t, modwire.ReasonCold, r.Reason)
	require.Equal(t, 1, spawns.count(), "one detached spawn")

	require.Equal(t, []modwire.BriefRecord{{
		V: modwire.Version, TS: core.UnixMilli(modTestNow.UnixMilli()), Trigger: "auto",
		Outcome: modwire.OutcomeCold, Chars: 0, MS: 40,
	}}, p.briefRecords(t))
	files, err := ipc.SpoolFiles(paths.Of(p.root).Spool)
	require.NoError(t, err)
	require.Empty(t, files, "a brief is never spooled")
}

// TestModBrief_ServedForwardsTheDaemonsAnswerAndRecordsIt: the daemon's answer is the brief's,
// unchanged, and the record logs it served with its character count.
func TestModBrief_ServedForwardsTheDaemonsAnswerAndRecordsIt(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	d := &briefDaemon{}
	d.serve(t, p.root, func(ipc.ModCall) ipc.Response { return briefData(t, servedBrief()) })

	stdin := briefRequestFor(t, p.root, "2.1.300", "manual")
	code, stdout, stderr := runMod(t, p.briefEnv(stdin, 300*time.Millisecond), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, servedBrief(), decodeBriefOut(t, stdout), "the daemon's brief, unchanged")
	require.Zero(t, spawns.count())

	reqs, calls := d.received()
	require.Len(t, calls, 1)
	require.Equal(t, ipc.OpMod, reqs[0].Op)
	require.True(t, reqs[0].Reply)
	require.Equal(t, core.SessionID("sess-1"), reqs[0].Session)
	require.Equal(t, core.UnixMilli(modTestNow.Add(-300*time.Millisecond).UnixMilli()), reqs[0].TS,
		"the request is stamped with when the brief was asked for: the process start")
	require.Equal(t, string(modwire.VerbBrief), calls[0].Verb)
	require.JSONEq(t, stdin, string(calls[0].Req), "the mod's request reaches the daemon as sent")

	require.Equal(t, []modwire.BriefRecord{{
		V: modwire.Version, TS: core.UnixMilli(modTestNow.UnixMilli()), Trigger: "manual",
		Outcome: modwire.OutcomeServed, Chars: modwire.CharCount(servedBrief().Instructions), MS: 300,
	}}, p.briefRecords(t))
}

// TestModBrief_TheDaemonsBudgetIsTheReplyWaitLessItsMargin: the reply wait is the mod's timeout less
// the time since the process started less the record margin, and the daemon is sent that less its
// reply margin.
func TestModBrief_TheDaemonsBudgetIsTheReplyWaitLessItsMargin(t *testing.T) {
	for _, elapsed := range []time.Duration{0, 300 * time.Millisecond, 1500 * time.Millisecond} {
		p := newModProject(t, true)
		d := &briefDaemon{}
		d.serve(t, p.root, func(ipc.ModCall) ipc.Response { return briefData(t, servedBrief()) })

		code, _, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), elapsed), briefArgs()...)
		require.Equal(t, ExitOK, code, stderr)

		_, calls := d.received()
		require.Len(t, calls, 1)
		want := int64(modwire.TimeoutMs-modwire.RecordMarginMs-modwire.DaemonReplyMarginMs) - elapsed.Milliseconds()
		require.Equal(t, want, calls[0].BudgetMs, "elapsed %v", elapsed)
	}
}

// TestModBrief_NoTimeLeftRecordsATimeoutWithoutAsking: when the time since the process started
// leaves the daemon no budget, the brief is a timeout, recorded, and the daemon is never asked.
func TestModBrief_NoTimeLeftRecordsATimeoutWithoutAsking(t *testing.T) {
	for _, elapsed := range []time.Duration{
		(modwire.TimeoutMs - modwire.RecordMarginMs - modwire.DaemonReplyMarginMs) * time.Millisecond,
		1750 * time.Millisecond,
		modwire.TimeoutMs * time.Millisecond,
		5 * time.Second,
	} {
		p := newModProject(t, true)
		spawns := &modSpawns{}
		spawns.install(t)
		d := &briefDaemon{}
		d.serve(t, p.root, func(ipc.ModCall) ipc.Response { return briefData(t, servedBrief()) })

		code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), elapsed), briefArgs()...)
		require.Equal(t, ExitOK, code, stderr)
		r := decodeBriefOut(t, stdout)
		require.False(t, r.OK, "elapsed %v", elapsed)
		require.Equal(t, modwire.ReasonTimeout, r.Reason)

		_, calls := d.received()
		require.Empty(t, calls, "the daemon is not asked")
		require.Zero(t, spawns.count())
		recs := p.briefRecords(t)
		require.Len(t, recs, 1)
		require.Equal(t, modwire.OutcomeTimeout, recs[0].Outcome)
		require.Equal(t, elapsed.Milliseconds(), recs[0].MS)
	}
}

// TestModBrief_TheDaemonsTimeoutIsForwardedAndRecorded: a timeout the route answered itself is the
// brief's answer, unchanged.
func TestModBrief_TheDaemonsTimeoutIsForwardedAndRecorded(t *testing.T) {
	p := newModProject(t, true)
	d := &briefDaemon{}
	timeout := modwire.BriefResponse{Status: modwire.Failure(modwire.ReasonTimeout, "the daemon's deadline passed")}
	d.serve(t, p.root, func(ipc.ModCall) ipc.Response { return briefData(t, timeout) })

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, timeout, decodeBriefOut(t, stdout))
	recs := p.briefRecords(t)
	require.Len(t, recs, 1)
	require.Equal(t, modwire.OutcomeTimeout, recs[0].Outcome)
	require.Zero(t, recs[0].Chars)
}

// TestModBrief_AnUnansweredDaemonIsATimeoutWithinTheReplyWait: a daemon that accepts and never
// answers is a timeout once the reply wait ends, and it is recorded.
func TestModBrief_AnUnansweredDaemonIsATimeoutWithinTheReplyWait(t *testing.T) {
	p := newModProject(t, true)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	d := &briefDaemon{}
	d.serve(t, p.root, func(ipc.ModCall) ipc.Response {
		<-release
		return ipc.Response{OK: true}
	})
	// 1,600 ms already spent leaves a 300 ms reply wait and a 100 ms budget.
	env := p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 1600*time.Millisecond)

	began := time.Now()
	code, stdout, stderr := runMod(t, env, briefArgs()...)
	require.Less(t, time.Since(began), modAnswerWait, "the reply wait, not a hang, ended the call")
	require.Equal(t, ExitOK, code, stderr)
	r := decodeBriefOut(t, stdout)
	require.Equal(t, modwire.ReasonTimeout, r.Reason)
	_, calls := d.received()
	require.Len(t, calls, 1)
	require.Equal(t, int64(100), calls[0].BudgetMs)
	recs := p.briefRecords(t)
	require.Len(t, recs, 1)
	require.Equal(t, modwire.OutcomeTimeout, recs[0].Outcome)
}

// TestModBrief_ADaemonThatRefusesIsAnErrorWithNoPath: a daemon that refuses the call (one built
// before the mod op answers "unknown op") is an error, recorded, whose detail names no path.
func TestModBrief_ADaemonThatRefusesIsAnErrorWithNoPath(t *testing.T) {
	p := newModProject(t, true)
	fakeModDaemon(t, p.root, func(context.Context, ipc.Request) ipc.Response {
		return ipc.Response{OK: false, Err: "unknown op: mod in " + p.root}
	})

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	r := decodeBriefOut(t, stdout)
	require.Equal(t, modwire.ReasonError, r.Reason)
	require.Contains(t, r.Detail, "unknown op: mod")
	require.NotContains(t, r.Detail, p.root)
	recs := p.briefRecords(t)
	require.Len(t, recs, 1)
	require.Equal(t, modwire.OutcomeError, recs[0].Outcome)
}

// TestModBrief_AnAnswerThatBreaksTheContractIsAnError: an answer that is not a valid brief, or is
// over the configured cap, is never passed on.
func TestModBrief_AnAnswerThatBreaksTheContractIsAnError(t *testing.T) {
	overCap := servedBrief()
	overCap.Instructions = modwire.BriefMarker + "\n- [inv_1] " + strings.Repeat("z", 60)
	for name, tc := range map[string]struct {
		answer ipc.Response
		extra  []string
	}{
		"no data":          {answer: ipc.Response{OK: true}},
		"not json":         {answer: ipc.Response{OK: true, Data: json.RawMessage(`"x"`)}},
		"no marker":        {answer: briefData(t, modwire.BriefResponse{Status: modwire.Served(), Instructions: "hi"})},
		"over the cap":     {answer: briefData(t, overCap), extra: []string{"--set", "runtime.mod.briefMaxChars=50"}},
		"unserved content": {answer: briefData(t, modwire.BriefResponse{Status: modwire.Failure(modwire.ReasonBusy, ""), FactIDs: []string{"x"}})},
	} {
		t.Run(name, func(t *testing.T) {
			p := newModProject(t, true)
			answer := tc.answer
			fakeModDaemon(t, p.root, func(context.Context, ipc.Request) ipc.Response { return answer })

			code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 0),
				briefArgs(tc.extra...)...)
			require.Equal(t, ExitOK, code, stderr)
			r := decodeBriefOut(t, stdout)
			require.False(t, r.OK)
			require.Equal(t, modwire.ReasonError, r.Reason)
			require.Empty(t, r.Instructions)
			recs := p.briefRecords(t)
			require.Len(t, recs, 1)
			require.Equal(t, modwire.OutcomeError, recs[0].Outcome)
		})
	}
}

// TestModBrief_ARequestsOwnCapIsHonoured: the request's max_chars bounds what the CLI passes on.
func TestModBrief_ARequestsOwnCapIsHonoured(t *testing.T) {
	p := newModProject(t, true)
	fakeModDaemon(t, p.root, func(context.Context, ipc.Request) ipc.Response { return briefData(t, servedBrief()) })
	b, err := modwire.Encode(modwire.BriefRequest{
		V: modwire.Version, SessionID: "sess-1", CWD: p.root, HostVersion: "2.1.300", Trigger: "auto", MaxChars: 50,
	})
	require.NoError(t, err)

	code, stdout, stderr := runMod(t, p.briefEnv(string(b), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, modwire.ReasonError, decodeBriefOut(t, stdout).Reason, "an answer over the request's cap")
}

// TestModBrief_AnUnreadableRequestIsAnErrorWithNoRecord: a request that cannot be read cannot name
// its record file or its trigger, so it gets the bare error object and nothing is written.
func TestModBrief_AnUnreadableRequestIsAnErrorWithNoRecord(t *testing.T) {
	for name, stdin := range map[string]string{
		"empty":          "",
		"not json":       "brief",
		"no version":     `{"session_id":"s","cwd":"/p","host_version":"2.1.300","trigger":"auto"}`,
		"unsafe session": `{"v":1,"session_id":"../x","cwd":"/p","host_version":"2.1.300","trigger":"auto"}`,
		"bad trigger":    `{"v":1,"session_id":"s","cwd":"/p","host_version":"2.1.300","trigger":"a b"}`,
		"no trigger":     `{"v":1,"session_id":"s","cwd":"/p","host_version":"2.1.300"}`,
		"negative cap":   `{"v":1,"session_id":"s","cwd":"/p","host_version":"2.1.300","trigger":"auto","max_chars":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newModProject(t, true)
			spawns := &modSpawns{}
			spawns.install(t)
			before := treeOf(t, p.root, p.home)
			code, stdout, stderr := runMod(t, p.briefEnv(stdin, 0), briefArgs()...)
			require.Equal(t, ExitOK, code, stderr)

			var st modwire.Status
			require.NoError(t, json.Unmarshal([]byte(stdout), &st), "stdout: %q", stdout)
			require.False(t, st.OK)
			require.Equal(t, modwire.ReasonError, st.Reason)
			require.Zero(t, spawns.count())
			require.Equal(t, before, treeOf(t, p.root, p.home))
		})
	}
}

// TestModBrief_AFailedRecordIsAnError: a call that cannot be recorded is not passed on, so a brief
// never steers a compaction its record does not show.
func TestModBrief_AFailedRecordIsAnError(t *testing.T) {
	p := newModProject(t, true)
	fakeModDaemon(t, p.root, func(context.Context, ipc.Request) ipc.Response { return briefData(t, servedBrief()) })
	require.NoError(t, os.WriteFile(paths.Of(p.root).State, []byte("not a directory"), 0o600))

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", "auto"), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	r := decodeBriefOut(t, stdout)
	require.Equal(t, modwire.ReasonError, r.Reason)
	require.Empty(t, r.Instructions)
	require.NotContains(t, r.Detail, p.root)
}

// TestModBrief_EveryCallAppendsOneLine: the log grows by one line per call.
func TestModBrief_EveryCallAppendsOneLine(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	for _, trigger := range []string{"auto", "precompute"} {
		code, _, stderr := runMod(t, p.briefEnv(briefRequestFor(t, p.root, "2.1.300", trigger), 0), briefArgs()...)
		require.Equal(t, ExitOK, code, stderr)
	}
	recs := p.briefRecords(t)
	require.Len(t, recs, 2)
	require.Equal(t, "auto", recs[0].Trigger)
	require.Equal(t, "precompute", recs[1].Trigger)
}

// TestModBrief_ResolvesTheRootLikeTheHooks: a session root in a subdirectory resolves to the
// repository root, and the record lands in that root's store.
func TestModBrief_ResolvesTheRootLikeTheHooks(t *testing.T) {
	p := newModProject(t, true)
	spawns := &modSpawns{}
	spawns.install(t)
	sub := filepath.Join(p.root, "pkg", "deep")
	require.NoError(t, os.MkdirAll(sub, 0o700))

	code, stdout, stderr := runMod(t, p.briefEnv(briefRequestFor(t, sub, "2.1.300", "auto"), 0), briefArgs()...)
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, modwire.ReasonCold, decodeBriefOut(t, stdout).Reason)
	require.Equal(t, []string{p.root}, spawns.roots, "the daemon started is the root's")
	require.Len(t, p.briefRecords(t), 1)
	require.NoDirExists(t, filepath.Join(sub, ".qompack"))
}

// TestModBrief_IsNotAHookEntryPoint: like hello, brief answers the mod, not the host.
func TestModBrief_IsNotAHookEntryPoint(t *testing.T) {
	var found bool
	for _, c := range All() {
		if c.Name == "mod brief" {
			found = true
			require.False(t, c.Hook)
			require.False(t, c.SilentStdout)
		}
	}
	require.True(t, found, "`qompack mod brief` is registered")
}
