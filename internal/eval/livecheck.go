package eval

// ── Grading a live trial ─────────────────────────────────────────────────────────────────────────
//
// Every check here reads the trial's project through an os.Root, so a path in a task file can never
// reach outside the project even through a link the session created. Command checks are run by the
// driver (this package does not start processes) and handed back as CommandOutcome values.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// maxCheckedFileBytes bounds what a file check reads. A fixture file is small; a session that wrote
// something enormous has not produced the file the check is about.
const maxCheckedFileBytes = 8 << 20

// LiveCheckResult is one check's verdict.
type LiveCheckResult struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Kind    string `json:"kind"`
	Passed  bool   `json:"passed"`
	// Detail says what was observed, so a failure can be read without re-running anything.
	Detail string `json:"detail"`
}

// CommandOutcome is what the driver observed running one command check.
type CommandOutcome struct {
	ExitCode int
	// Output is the combined output, possibly truncated by the driver.
	Output string
	// Err is set when the command could not be run or timed out.
	Err error
}

// HashTree records the SHA-256 of every regular file under root, keyed by slash path. It is taken
// of the fixture before the session so file_unchanged can compare against what the task started
// from rather than against a second copy that might itself have drifted.
func HashTree(root string) (map[string]string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("eval: opening %s: %w", root, err)
	}
	defer func() { _ = r.Close() }()
	out := map[string]string{}
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.Type().IsRegular() {
			return nil
		}
		sum, herr := hashRootFile(r, p)
		if herr != nil {
			return herr
		}
		out[p] = sum
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("eval: hashing %s: %w", root, err)
	}
	return out, nil
}

func hashRootFile(r *os.Root, p string) (string, error) {
	f, err := r.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LiveEvidence is everything a trial left behind that grading reads, besides the project tree.
type LiveEvidence struct {
	// Answers maps a prompt step's id to the answer its turn ended with.
	Answers map[string]string
	// Fixture maps each fixture file's slash path to its pre-session SHA-256.
	Fixture map[string]string
	// Commands maps a command check's id to what the driver observed running it.
	Commands map[string]CommandOutcome
	// ToolUsesAfter maps a compaction step's id to every main-loop tool call made after it; a
	// step the session never reached is absent.
	ToolUsesAfter map[string][]HostToolUse
}

// GradeLiveTrial evaluates every check of task against the trial's project root and ev. A check
// the driver did not run, a step that produced no answer, or a compaction step the session never
// reached fails with a detail saying so: an ungraded check is never a pass.
func GradeLiveTrial(task LiveTask, root string, ev LiveEvidence) []LiveCheckResult {
	r, rootErr := os.OpenRoot(root)
	if rootErr == nil {
		defer func() { _ = r.Close() }()
	}
	out := make([]LiveCheckResult, 0, len(task.Checks))
	for _, c := range task.Checks {
		res := LiveCheckResult{ID: c.ID, Outcome: c.Outcome, Kind: c.Kind}
		switch c.Kind {
		case CheckAnswerMatches, CheckAnswerNotMatches:
			res.Passed, res.Detail = gradeAnswer(c, ev.Answers)
		case CheckCommand:
			res.Passed, res.Detail = gradeCommand(c, ev.Commands)
		case CheckToolNotUsedAfter:
			res.Passed, res.Detail = gradeToolNotUsed(c, ev.ToolUsesAfter)
		default:
			if rootErr != nil {
				res.Detail = "project root unreadable: " + rootErr.Error()
				break
			}
			res.Passed, res.Detail = gradeFile(c, r, ev.Fixture)
		}
		out = append(out, res)
	}
	return out
}

func gradeAnswer(c LiveCheck, answers map[string]string) (bool, string) {
	ans, ok := answers[c.Step]
	if !ok {
		return false, fmt.Sprintf("step %s produced no answer", c.Step)
	}
	re := regexp.MustCompile(c.Pattern) // validated at load
	hit := re.FindString(ans)
	matched := re.MatchString(ans)
	if c.Kind == CheckAnswerMatches {
		if matched {
			return true, fmt.Sprintf("answer matched %q", hit)
		}
		return false, fmt.Sprintf("answer did not match; answer began %q", clip(ans))
	}
	if matched {
		return false, fmt.Sprintf("answer matched the forbidden pattern at %q", hit)
	}
	return true, "answer did not match the forbidden pattern"
}

func gradeToolNotUsed(c LiveCheck, after map[string][]HostToolUse) (bool, string) {
	uses, ok := after[c.Step]
	if !ok {
		return false, fmt.Sprintf("the session never reached compaction step %s", c.Step)
	}
	re := regexp.MustCompile(c.Pattern) // validated at load
	for _, u := range uses {
		subject := u.Name + " " + string(u.Input)
		if hit := re.FindString(subject); hit != "" {
			return false, fmt.Sprintf("%s after %s matched %q", u.Name, c.Step, clip(hit))
		}
	}
	return true, fmt.Sprintf("none of %d tool call(s) after %s matched", len(uses), c.Step)
}

func gradeCommand(c LiveCheck, commands map[string]CommandOutcome) (bool, string) {
	co, ok := commands[c.ID]
	if !ok {
		return false, "the driver did not run this command"
	}
	if co.Err != nil {
		return false, fmt.Sprintf("%s: %v", strings.Join(c.Argv, " "), co.Err)
	}
	if co.ExitCode != 0 {
		return false, fmt.Sprintf("%s exited %d: %s", strings.Join(c.Argv, " "), co.ExitCode, clip(co.Output))
	}
	return true, fmt.Sprintf("%s exited 0", strings.Join(c.Argv, " "))
}

func gradeFile(c LiveCheck, r *os.Root, fixture map[string]string) (bool, string) {
	data, readErr := readRootFile(r, c.Path)
	missing := errors.Is(readErr, fs.ErrNotExist)
	switch c.Kind {
	case CheckFileExists:
		if readErr == nil {
			return true, "present"
		}
		return false, describeReadErr(c.Path, readErr)
	case CheckFileAbsent:
		if missing {
			return true, "absent"
		}
		if readErr != nil {
			return false, describeReadErr(c.Path, readErr)
		}
		return false, "present"
	}
	if readErr != nil {
		return false, describeReadErr(c.Path, readErr)
	}
	switch c.Kind {
	case CheckFileMatches, CheckFileNotMatches:
		re := regexp.MustCompile(c.Pattern)
		hit := re.Find(data)
		if c.Kind == CheckFileMatches {
			if hit != nil {
				return true, fmt.Sprintf("matched %q", clip(string(hit)))
			}
			return false, "no match"
		}
		if hit != nil {
			return false, fmt.Sprintf("forbidden pattern matched %q", clip(string(hit)))
		}
		return true, "forbidden pattern absent"
	case CheckFileUnchanged:
		want, ok := fixture[c.Path]
		if !ok {
			return false, "the fixture has no such file to compare against"
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			return false, "content differs from the fixture"
		}
		return true, "identical to the fixture"
	case CheckFileLines:
		n := nonEmptyLines(data)
		if n == *c.Lines {
			return true, fmt.Sprintf("%d non-empty line(s)", n)
		}
		return false, fmt.Sprintf("%d non-empty line(s), want %d", n, *c.Lines)
	}
	return false, fmt.Sprintf("unknown kind %q", c.Kind)
}

func readRootFile(r *os.Root, p string) ([]byte, error) {
	f, err := r.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCheckedFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCheckedFileBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", p, maxCheckedFileBytes)
	}
	return data, nil
}

func describeReadErr(p string, err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return p + " does not exist"
	}
	return fmt.Sprintf("%s unreadable: %v", p, err)
}

func nonEmptyLines(data []byte) int {
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n
}

// clipRunes bounds a detail string so one failed check cannot bloat a trial record.
const clipRunes = 160

func clip(s string) string { return boundRunes(s, clipRunes) }
