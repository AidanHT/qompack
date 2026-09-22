package main

// The side effects of `devtool live-eval`: the host process, the plugin's marketplace install and
// uninstall, the trial daemon's shutdown, the host transcript's location, the grading commands and
// the guard over the operator's real Claude Code configuration. Each is a field of liveEnv so the
// orchestration in liveeval.go can be driven end to end by a test with no host at all.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/ipc"
)

// liveProcSpec is one host session to run.
type liveProcSpec struct {
	Bin            string        `json:"bin"`
	Args           []string      `json:"args"`
	Dir            string        `json:"dir"`
	Env            []string      `json:"env"`
	Messages       []string      `json:"messages"`
	StepTimeout    time.Duration `json:"step_timeout"`
	SessionTimeout time.Duration `json:"session_timeout"`
}

// liveProcResult is what the host printed and how it ended.
type liveProcResult struct {
	Stream         []byte
	RecvMS         []int64
	Stderr         []byte
	ExitCode       int
	StepsCompleted int
	// Err is a harness failure: the process did not start, a step timed out, the stream closed
	// before every step had a result.
	Err       string
	StartedAt time.Time
	EndedAt   time.Time
}

// liveCLIResult is one `claude plugin …` invocation's outcome.
type liveCLIResult struct {
	Stdout, Stderr string
	Code           int
}

func (r liveCLIResult) combined() string {
	return strings.TrimSpace(strings.TrimSpace(r.Stdout) + "\n" + strings.TrimSpace(r.Stderr))
}

// liveEnv is every side effect the driver performs.
type liveEnv struct {
	claudeBin string
	// home is the Claude Code configuration directory: CLAUDE_CONFIG_DIR, else ~/.claude.
	home       string
	now        func() time.Time
	run        func(ctx context.Context, spec liveProcSpec) liveProcResult
	cli        func(ctx context.Context, dir string, args ...string) liveCLIResult
	stopDaemon func(project, spoolDir string) (string, error)
	check      func(dir string, argv []string) eval.CommandOutcome
	git        func(ctx context.Context, dir string, args ...string) error
}

func newLiveEnv(o liveOptions) (*liveEnv, error) {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		uh, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("live-eval: resolving the user home: %w", err)
		}
		home = filepath.Join(uh, ".claude")
	}
	bin, err := lookClaude(o.claude)
	if err != nil && !o.dryRun {
		return nil, err
	}
	e := &liveEnv{claudeBin: bin, home: home, now: time.Now}
	e.run = runLiveProcess
	e.cli = func(ctx context.Context, dir string, args ...string) liveCLIResult {
		return runLiveCLI(ctx, e.claudeBin, dir, args...)
	}
	e.stopDaemon = stopTrialDaemon
	e.check = runLiveCheckCommand
	e.git = runLiveGit
	return e, nil
}

// cliVersion is `claude --version`'s leading token, or "unknown".
func (e *liveEnv) cliVersion(ctx context.Context) string {
	r := e.cli(ctx, "", "--version")
	if f := strings.Fields(r.Stdout); r.Code == 0 && len(f) > 0 {
		return f[0]
	}
	return "unknown"
}

// ── the host process ─────────────────────────────────────────────────────────────────────────────

// runLiveProcess runs one streaming-input host session: it writes one user message, waits for that
// turn's result line, and only then writes the next, so a message never queues into a running
// turn. It kills only the process it started, and only on a timeout.
func runLiveProcess(ctx context.Context, spec liveProcSpec) liveProcResult {
	res := liveProcResult{StartedAt: time.Now()}
	sctx, cancel := context.WithTimeout(ctx, spec.SessionTimeout)
	defer cancel()
	cmd := exec.CommandContext(sctx, spec.Bin, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.WaitDelay = liveExitGrace
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		res.Err, res.EndedAt = "stdin: "+err.Error(), time.Now()
		return res
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Err, res.EndedAt = "stdout: "+err.Error(), time.Now()
		return res
	}
	if err := cmd.Start(); err != nil {
		res.Err, res.EndedAt = "starting the host: "+err.Error(), time.Now()
		return res
	}
	t0 := time.Now()

	var mu sync.Mutex
	var stream bytes.Buffer
	var recv []int64
	resultCount := 0
	// notify carries "a result line arrived" without ever blocking the reader: the count under mu
	// is the truth, and the channel only wakes the writer to re-read it.
	notify := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		br := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, rerr := br.ReadBytes('\n')
			if len(line) > 0 {
				trimmed := bytes.TrimSpace(line)
				mu.Lock()
				stream.Write(line)
				if len(trimmed) > 0 {
					recv = append(recv, time.Since(t0).Milliseconds())
				}
				mu.Unlock()
				var head struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(trimmed, &head) == nil && head.Type == "result" {
					mu.Lock()
					resultCount++
					mu.Unlock()
					select {
					case notify <- struct{}{}:
					default:
					}
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	results := func() int {
		mu.Lock()
		defer mu.Unlock()
		return resultCount
	}
	for i, msg := range spec.Messages {
		line, _ := json.Marshal(map[string]any{
			"type": "user", "message": map[string]any{"role": "user", "content": msg},
		})
		if _, werr := stdin.Write(append(line, '\n')); werr != nil {
			res.Err = "writing a message: " + werr.Error()
			break
		}
		if why := waitForResults(sctx, i+1, results, notify, done, spec.StepTimeout); why != "" {
			res.Err = fmt.Sprintf("step %d of %d: %s", i+1, len(spec.Messages), why)
			break
		}
		res.StepsCompleted++
	}
	_ = stdin.Close()
	grace := time.NewTimer(liveExitGrace)
	select {
	case <-done:
	case <-grace.C:
		cancel() // our own child: CommandContext kills it
		res.Err = joinErr(res.Err, fmt.Sprintf("the host did not exit within %s of its input closing", liveExitGrace))
	}
	grace.Stop()
	werr := cmd.Wait()
	var exitErr *exec.ExitError
	switch {
	case werr == nil:
	case errors.As(werr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	case errors.Is(werr, exec.ErrWaitDelay):
	default:
		res.Err = joinErr(res.Err, "waiting for the host: "+werr.Error())
	}
	mu.Lock()
	res.Stream, res.RecvMS = append([]byte(nil), stream.Bytes()...), append([]int64(nil), recv...)
	mu.Unlock()
	res.Stderr = stderr.Bytes()
	res.EndedAt = time.Now()
	return res
}

// waitForResults waits until at least want result lines have arrived. It returns "" when they
// have, and otherwise why they did not.
func waitForResults(ctx context.Context, want int, count func() int, notify, done <-chan struct{},
	bound time.Duration,
) string {
	step := time.NewTimer(bound)
	defer step.Stop()
	for {
		if count() >= want {
			return ""
		}
		select {
		case <-notify:
		case <-done:
			if count() >= want {
				return ""
			}
			return "the host closed its output before the step's result"
		case <-step.C:
			return fmt.Sprintf("no result within %s", bound)
		case <-ctx.Done():
			return "the session bound expired"
		}
	}
}

// runLiveCLI runs one short `claude` subcommand (plugin management, --version) with a closed stdin
// and a bound, in dir when given.
func runLiveCLI(ctx context.Context, bin, dir string, args ...string) liveCLIResult {
	cctx, cancel := context.WithTimeout(ctx, liveCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdin = bytes.NewReader(nil)
	cmd.WaitDelay = liveExitGrace
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := liveCLIResult{Stdout: out.String(), Stderr: errb.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
	case errors.As(err, &exitErr):
		r.Code = exitErr.ExitCode()
	default:
		r.Code, r.Stderr = -1, r.Stderr+"\n"+err.Error()
	}
	return r
}

// ── the project ──────────────────────────────────────────────────────────────────────────────────

// gitInit makes the fixture a repository with one commit, the same for both arms, so `git diff` is
// available to the session and Qompack's checkpoint has a HEAD to point at.
func (e *liveEnv) gitInit(ctx context.Context, project string) error {
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{
			"-c", "user.name=qompack-live-eval", "-c", "user.email=live-eval@invalid", "-c", "commit.gpgsign=false",
			"commit", "-q", "-m", "fixture",
		},
	} {
		if err := e.git(ctx, project, args...); err != nil {
			return err
		}
	}
	return nil
}

func runLiveGit(ctx context.Context, dir string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, liveCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runCheckCommand runs one command check in the project root.
func (e *liveEnv) runCheckCommand(project string, argv []string) eval.CommandOutcome {
	return e.check(project, argv)
}

func runLiveCheckCommand(dir string, argv []string) eval.CommandOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), liveCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil)
	cmd.WaitDelay = liveExitGrace
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if len(out) > liveCommandOutputKeep {
		out = out[len(out)-liveCommandOutputKeep:]
	}
	co := eval.CommandOutcome{Output: string(out)}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		co.Err = fmt.Errorf("timed out after %s", liveCommandTimeout)
		co.ExitCode = -1
	case err == nil:
	case errors.As(err, &exitErr):
		co.ExitCode = exitErr.ExitCode()
	default:
		co.Err = err
	}
	return co
}

// ── the plugin's marketplace flow ────────────────────────────────────────────────────────────────

// marketplaceInstall publishes the bundle through a disposable local marketplace and installs it at
// LOCAL scope for this project only, which is the real flow a user runs. The returned undo
// uninstalls the plugin and removes the marketplace, and names anything it could not remove.
func (e *liveEnv) marketplaceInstall(ctx context.Context, project, bundle, work string) (func() []string, error) {
	mkt := filepath.Join(work, "marketplace")
	base := filepath.Base(bundle)
	if err := copyTree(bundle, filepath.Join(mkt, base)); err != nil {
		return func() []string { return nil }, fmt.Errorf("copying the bundle into the marketplace: %w", err)
	}
	doc := map[string]any{
		"name":        liveMarketplaceName,
		"description": "Disposable local marketplace for the Qompack live evaluation",
		"owner":       map[string]any{"name": "Qompack"},
		"plugins": []any{map[string]any{
			"name": livePluginName, "source": "./" + base,
			"description": "Cache-aware, retrieval-backed context compaction",
		}},
	}
	if err := os.MkdirAll(filepath.Join(mkt, ".claude-plugin"), liveDirPerm); err != nil {
		return func() []string { return nil }, err
	}
	if err := writeJSONFile(filepath.Join(mkt, ".claude-plugin", "marketplace.json"), doc); err != nil {
		return func() []string { return nil }, err
	}
	id := livePluginName + "@" + liveMarketplaceName
	undo := func() []string {
		var left []string
		if r := e.cli(ctx, project, "plugin", "uninstall", id, "--scope", "local", "-y"); r.Code != 0 {
			left = append(left, "plugin uninstall "+id+": "+r.combined())
		}
		if r := e.cli(ctx, project, "plugin", "marketplace", "remove", liveMarketplaceName); r.Code != 0 {
			left = append(left, "marketplace remove "+liveMarketplaceName+": "+r.combined())
		}
		return left
	}
	if r := e.cli(ctx, project, "plugin", "marketplace", "add", mkt, "--scope", "local"); r.Code != 0 {
		return undo, fmt.Errorf("marketplace add: exit %d: %s", r.Code, r.combined())
	}
	if r := e.cli(ctx, project, "plugin", "install", id, "--scope", "local", "-y"); r.Code != 0 {
		return undo, fmt.Errorf("plugin install: exit %d: %s", r.Code, r.combined())
	}
	return undo, nil
}

// ── the trial daemon ─────────────────────────────────────────────────────────────────────────────

// stopTrialDaemon asks the trial project's daemon to shut down through the product's own
// admin.shutdown request and waits for it to release daemon.lock. It never signals a process: a
// daemon that does not stop is reported, not killed. The spool directory is the harness's own, so a
// request that could not be delivered is not written into the project the trial is graded on.
func stopTrialDaemon(project, spoolDir string) (string, error) {
	lockPath := daemon.LockPath(project)
	if _, err := os.Stat(lockPath); err != nil {
		return "no daemon lock after the session (the daemon had already exited or never started)", nil
	}
	info, _ := daemon.ReadLock(project)
	addr, err := ipc.Resolve(project)
	if err != nil {
		return fmt.Sprintf("daemon pid %d left running", info.PID), fmt.Errorf("resolving its address: %w", err)
	}
	if err := os.MkdirAll(spoolDir, liveDirPerm); err != nil {
		return fmt.Sprintf("daemon pid %d left running", info.PID), err
	}
	sp, err := ipc.NewSpool(spoolDir)
	if err != nil {
		return fmt.Sprintf("daemon pid %d left running", info.PID), err
	}
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot: project, ConnectDeadline: liveCLITimeout, AckDeadline: liveCLITimeout,
	})
	defer func() { _ = c.Close() }()
	deadline := time.NewTimer(liveDaemonStopBound)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, liveCLITimeout)
		if _, err := os.Stat(lockPath); err != nil {
			return fmt.Sprintf("daemon pid %d stopped by admin.shutdown", info.PID), nil
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return fmt.Sprintf("daemon pid %d left running", info.PID),
				fmt.Errorf("it still held %s after %s of admin.shutdown; it was not killed", lockPath, liveDaemonStopBound)
		}
	}
}

// ── the host transcript ──────────────────────────────────────────────────────────────────────────

// readTranscript finds the session's transcript under <home>/projects/*/<id>.jsonl — by the
// session ID the driver requested, then by the one the host reported — and parses it.
func (e *liveEnv) readTranscript(requested, reported string) (string, eval.HostTranscript, error) {
	for _, id := range []string{requested, reported} {
		if id == "" {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(e.home, "projects", "*", id+".jsonl"))
		if len(matches) == 0 {
			continue
		}
		f, err := os.Open(matches[0])
		if err != nil {
			return matches[0], eval.HostTranscript{}, err
		}
		tr, perr := eval.ParseHostTranscript(f)
		_ = f.Close()
		return matches[0], tr, perr
	}
	return "", eval.HostTranscript{}, fmt.Errorf("no transcript for session %s under %s", requested, filepath.Join(e.home, "projects"))
}

// ── the guard over the operator's configuration ──────────────────────────────────────────────────

// liveGuardSnap fingerprints the operator's Claude Code configuration files a trial could touch:
// user settings, the installed-plugin and known-marketplace registries (hashed), and the presence
// of every plugin data, cache or marketplace directory a Qompack trial could create. Host
// bookkeeping every session rewrites — ~/.claude.json, projects/, the plugin catalog cache and the
// .in_use markers — is deliberately not guarded: any Claude Code session changes those, including
// the other sessions sharing this machine, and guarding them would fail every run for a change
// nobody made.
type liveGuardSnap map[string]string

func (s liveGuardSnap) lines() []string {
	out := make([]string, 0, len(s))
	for k, v := range s {
		out = append(out, k+" "+v)
	}
	sort.Strings(out)
	return out
}

func (s liveGuardSnap) equal(o liveGuardSnap) bool {
	if len(s) != len(o) {
		return false
	}
	for k, v := range s {
		if o[k] != v {
			return false
		}
	}
	return true
}

// liveGuardedFiles are hashed; liveGuardedDirs are recorded present or absent.
var liveGuardedFiles = []string{
	"settings.json",
	"plugins/installed_plugins.json",
	"plugins/known_marketplaces.json",
}

func (e *liveEnv) liveGuardedDirs() []string {
	dirs := []string{
		"plugins/cache/" + liveMarketplaceName,
		"plugins/marketplaces/" + liveMarketplaceName,
	}
	entries, _ := os.ReadDir(filepath.Join(e.home, "plugins", "data"))
	for _, d := range entries {
		if strings.HasPrefix(d.Name(), livePluginName) {
			dirs = append(dirs, "plugins/data/"+d.Name())
		}
	}
	return dirs
}

func (e *liveEnv) guardSnapshot() (liveGuardSnap, error) {
	s := liveGuardSnap{}
	for _, rel := range liveGuardedFiles {
		raw, err := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel)))
		switch {
		case err == nil:
			s[rel] = sha256Hex(raw)
		case errors.Is(err, os.ErrNotExist):
			s[rel] = "absent"
		default:
			return nil, err
		}
	}
	for _, rel := range e.liveGuardedDirs() {
		if _, err := os.Stat(filepath.Join(e.home, filepath.FromSlash(rel))); err == nil {
			s[rel] = "present"
		}
	}
	return s, nil
}

// removeCreatedPluginData removes a plugin data directory the trial created, when it is empty —
// the host creates <home>/plugins/data/qompack-inline for a --plugin-dir session — and names one it
// could not remove because it holds something.
func (e *liveEnv) removeCreatedPluginData(before liveGuardSnap) []string {
	var left []string
	for _, rel := range e.liveGuardedDirs() {
		if !strings.HasPrefix(rel, "plugins/data/") || before[rel] == "present" {
			continue
		}
		p := filepath.Join(e.home, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			left = append(left, rel+" (created by the trial and not empty: "+err.Error()+")")
		}
	}
	return left
}
