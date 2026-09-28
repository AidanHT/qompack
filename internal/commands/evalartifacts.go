package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/qompack/qompack/internal/eval"
)

// Where a completed evaluation's artifacts live, relative to the project root. They are the
// defaults of the two producers: `devtool live-eval` writes each real-host run to
// dist/live-eval/<run-id>/ (plan.json and summary.json beside the trial records), and
// `devtool replay` (test/replay) writes its report envelope to testdata/bench-replay.json. Both
// are build outputs of a Qompack source checkout, so outside one the command finds nothing unless
// --corpus names an artifact.
const (
	DefaultLiveEvalDir  = "dist/live-eval"
	DefaultReplayReport = "testdata/bench-replay.json"
)

// The file names inside one live-eval run directory.
const (
	livePlanFile    = "plan.json"
	liveSummaryFile = "summary.json"
)

// maxEvalArtifactBytes bounds one artifact read. A summary or a replay report is kilobytes; a file
// this large is not one, and reading it whole would be the command's only unbounded allocation.
const maxEvalArtifactBytes = 64 << 20

// replayEnvelope is the part of test/replay's DriverReport this command reads. The driver is a
// composition root nothing may import, so the fields are restated here by their JSON names; every
// other field of the envelope is ignored.
type replayEnvelope struct {
	Generator          string      `json:"generator"`
	Corpus             string      `json:"corpus"`
	CorpusTier         string      `json:"corpusTier"`
	CorpusSHA256       string      `json:"corpusSHA256"`
	Latency            string      `json:"latency"`
	PhaseChecksSkipped bool        `json:"phaseChecksSkipped"`
	BudgetViolations   []string    `json:"budgetViolations"`
	Report             eval.Report `json:"report"`
}

// FileEvalArtifacts is the EvalArtifacts provider the binary installs: it reads what the two
// evaluation producers left on disk and never runs either of them.
//
// With no corpus it reads, relative to root, the newest live-eval run under DefaultLiveEvalDir (by
// its plan's creation time) and the replay report at DefaultReplayReport, each when present. A
// corpus, resolved against root when relative, names exactly one of: a live-eval run directory, a
// directory of such runs (the newest is read), or a replay report file. Finding nothing is
// ErrUnavailable, naming where it looked; an artifact that is present but unreadable is a failure,
// and one written in a newer schema is ErrUnsupported — neither is ever read as an empty result.
func FileEvalArtifacts(root string) EvalArtifacts {
	return func(ctx context.Context, corpus string) (EvalInput, error) {
		if err := ctx.Err(); err != nil {
			return EvalInput{}, err
		}
		if corpus != "" {
			return readNamedEvalArtifact(resolveUnder(root, corpus))
		}
		var in EvalInput
		liveDir := resolveUnder(root, DefaultLiveEvalDir)
		live, err := newestLiveRun(liveDir)
		if err != nil {
			return EvalInput{}, err
		}
		in.Live = live
		replayPath := resolveUnder(root, DefaultReplayReport)
		found, err := readReplayInto(&in, replayPath)
		if err != nil {
			return EvalInput{}, err
		}
		if live == nil && !found {
			return EvalInput{}, fmt.Errorf("%w: no evaluation artifacts: no live-eval run under %s and no replay "+
				"report at %s. An evaluation is produced in a Qompack source checkout by `go run ./tools/devtool "+
				"live-eval` (real-host trials) or `go run ./tools/devtool replay` (deterministic replay); pass "+
				"--corpus <path> to read one from elsewhere", ErrUnavailable, liveDir, replayPath)
		}
		return in, nil
	}
}

// resolveUnder resolves p against root when it is relative.
func resolveUnder(root, p string) string {
	p = filepath.FromSlash(p)
	if filepath.IsAbs(p) || root == "" {
		return p
	}
	return filepath.Join(root, p)
}

// readNamedEvalArtifact reads the one artifact --corpus names.
func readNamedEvalArtifact(p string) (EvalInput, error) {
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return EvalInput{}, fmt.Errorf("%w: no evaluation artifact at %s", ErrUnavailable, p)
		}
		return EvalInput{}, fmt.Errorf("reading %s: %w", p, err)
	}
	var in EvalInput
	if !info.IsDir() && filepath.Base(p) == liveSummaryFile {
		// A run's summary.json names its run: read the directory it sits in, plan included.
		p = filepath.Dir(p)
	} else if !info.IsDir() {
		if _, err := readReplayInto(&in, p); err != nil {
			return EvalInput{}, err
		}
		return in, nil
	}
	if isLiveRunDir(p) {
		run, err := readLiveRun(p)
		if err != nil {
			return EvalInput{}, err
		}
		in.Live = run
		return in, nil
	}
	run, err := newestLiveRun(p)
	if err != nil {
		return EvalInput{}, err
	}
	if run == nil {
		return EvalInput{}, fmt.Errorf("%w: %s holds no live-eval run (a directory with %s and %s)",
			ErrUnavailable, p, livePlanFile, liveSummaryFile)
	}
	in.Live = run
	return in, nil
}

// isLiveRunDir reports whether dir holds a run's summary.
func isLiveRunDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, liveSummaryFile))
	return err == nil && info.Mode().IsRegular()
}

// newestLiveRun reads the newest run directly under dir, or returns nil when dir does not exist or
// holds none. Runs are ordered by their plan's creation time, then by directory name, because a run
// directory may be renamed (the committed pilots are) while its plan keeps the time it was made.
//
// Only the newest run's summary is read. An older run's summary is not what the command reports, so
// one that a crash left unreadable must not fail every later `qompack eval`; the newest run's own
// must be readable, and is an error when it is not. Every finished run's plan is read, because its
// creation time is what decides which run is the newest: a plan that cannot be read leaves that
// undecidable, and is an error too.
func newestLiveRun(dir string) (*LiveEvalInput, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing live-eval runs in %s: %w", dir, err)
	}
	type finishedRun struct {
		dir, createdAt string
	}
	var runs []finishedRun
	var unfinished []unfinishedRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if !isLiveRunDir(sub) {
			if u, ok := readUnfinishedRun(sub); ok {
				unfinished = append(unfinished, u)
			}
			continue
		}
		var plan eval.LivePlan
		if err := readEvalJSON(filepath.Join(sub, livePlanFile), &plan); err != nil {
			return nil, err
		}
		runs = append(runs, finishedRun{dir: sub, createdAt: plan.CreatedAt})
	}
	if len(runs) == 0 {
		return nil, nil
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].createdAt != runs[j].createdAt {
			return runs[i].createdAt < runs[j].createdAt
		}
		return runs[i].dir < runs[j].dir
	})
	newest, err := readLiveRun(runs[len(runs)-1].dir)
	if err != nil {
		return nil, err
	}
	for _, u := range unfinished {
		if u.createdAt == "" || u.createdAt > newest.Plan.CreatedAt {
			newest.Notes = append(newest.Notes, fmt.Sprintf("a newer live-eval run, %s (created %s), has a plan "+
				"and no summary: it is still running or it stopped without writing one, so the run reported here "+
				"is the newest finished one, not the newest", u.dir, orUnknown(u.createdAt)))
		}
	}
	return newest, nil
}

// unfinishedRun is a run directory with a plan and no summary.
type unfinishedRun struct {
	dir, createdAt string
}

// readUnfinishedRun reports a run directory that holds plan.json but no summary.json: a run that is
// still going, or one that stopped before writing its summary. An unreadable plan still names the
// directory, with its creation time unknown.
func readUnfinishedRun(dir string) (unfinishedRun, bool) {
	if info, err := os.Stat(filepath.Join(dir, livePlanFile)); err != nil || !info.Mode().IsRegular() {
		return unfinishedRun{}, false
	}
	u := unfinishedRun{dir: dir}
	var plan eval.LivePlan
	if readEvalJSON(filepath.Join(dir, livePlanFile), &plan) == nil {
		u.createdAt = plan.CreatedAt
	}
	return u, true
}

// readLiveRun reads one run directory's plan and summary. Both are required: a summary without its
// plan cannot say who ran it, on what, or whether it was the pre-registered design.
func readLiveRun(dir string) (*LiveEvalInput, error) {
	run := &LiveEvalInput{Source: dir}
	if err := readEvalJSON(filepath.Join(dir, livePlanFile), &run.Plan); err != nil {
		return nil, err
	}
	if err := readEvalJSON(filepath.Join(dir, liveSummaryFile), &run.Summary); err != nil {
		return nil, err
	}
	if run.Summary.Schema > eval.LiveSummarySchema {
		return nil, fmt.Errorf("%w: %s is summary schema %d, this build reads %d", ErrUnsupported,
			filepath.Join(dir, liveSummaryFile), run.Summary.Schema, eval.LiveSummarySchema)
	}
	return run, nil
}

// readReplayInto reads a replay report envelope into in, reporting whether one was there.
func readReplayInto(in *EvalInput, p string) (bool, error) {
	var env replayEnvelope
	if err := readEvalJSON(p, &env); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if len(env.Report.Policies) == 0 {
		return false, fmt.Errorf("%s is not a replay report: it carries no policy scores", p)
	}
	in.Report = env.Report
	// A deterministic replay runs every session it loaded, so there is nothing to skip or fail; a
	// session the driver could not replay stops the driver before it writes a report at all.
	in.Trials = TrialCounts{Planned: env.Report.Sessions, Ran: env.Report.Sessions}
	in.Notes = append(in.Notes, fmt.Sprintf("replay: %s from %s — corpus %s (tier %s, sha256 %s), latency %s; "+
		"deterministic and model-free: it estimates what a keep-set is worth, not what a model did",
		orUnknown(env.Generator), p, orUnknown(env.Corpus), orUnknown(env.CorpusTier), shortHash(env.CorpusSHA256),
		orUnknown(env.Latency)))
	if env.PhaseChecksSkipped {
		in.Notes = append(in.Notes, "replay: the phase-exit checks were skipped for this report")
	}
	if n := len(env.BudgetViolations); n > 0 {
		in.Notes = append(in.Notes, fmt.Sprintf("replay: %d keep-set budget violation(s) recorded", n))
	}
	return true, nil
}

// readEvalJSON decodes one bounded artifact file. A missing file keeps its fs.ErrNotExist.
func readEvalJSON(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return fmt.Errorf("reading %s: %w", p, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxEvalArtifactBytes+1))
	if err != nil {
		return fmt.Errorf("reading %s: %w", p, err)
	}
	if len(raw) > maxEvalArtifactBytes {
		return fmt.Errorf("%s exceeds %d bytes; it is not an evaluation artifact", p, maxEvalArtifactBytes)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decoding %s: %w", p, err)
	}
	return nil
}

// shortHash abbreviates a hex digest for a note line.
func shortHash(h string) string {
	const keep = 12
	if h == "" {
		return "unknown"
	}
	if len(h) <= keep {
		return h
	}
	return h[:keep] + "…"
}
