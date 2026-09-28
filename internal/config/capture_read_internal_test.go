package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// captureFind is what one scripted Lstat or open of config.json finds.
type captureFind int

const (
	findAbsent captureFind = iota // the name does not exist
	findFile                      // a bounded regular file
	findDir                       // a directory
	findDenied                    // the call fails for a reason other than absence
)

// captureTick is how far the scripted clock moves on each yield: the reader's re-check loop yields
// once per attempt, so the budget is spent after captureConfigGoneBudget/captureTick attempts.
const captureTick = time.Millisecond

// scriptedCaptureFS answers readCaptureConfig's Lstats and opens from two scripts, one entry per
// call, repeating each script's last entry once it runs out, and keeps a clock that only yields
// move. The FileInfo and handles it returns are real ones, of a file and a directory in a temp dir.
type scriptedCaptureFS struct {
	t             *testing.T
	file, dir     string
	lstats, opens []captureFind
	nLstat, nOpen int
	yields        int
	start, now    time.Time
}

// capturedBody is the scripted file's content.
const capturedBody = `{"checkpoint":{"budgetTokens":9001}}`

func newScriptedCaptureFS(t *testing.T, lstats, opens []captureFind) *scriptedCaptureFS {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(file, []byte(capturedBody), 0o600))
	start := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	return &scriptedCaptureFS{t: t, file: file, dir: dir, lstats: lstats, opens: opens, start: start, now: start}
}

func (s *scriptedCaptureFS) Now() time.Time                  { return s.now }
func (s *scriptedCaptureFS) Since(t time.Time) time.Duration { return s.now.Sub(t) }

func scripted(script []captureFind, n int) captureFind {
	if n < len(script) {
		return script[n]
	}
	return script[len(script)-1]
}

func (s *scriptedCaptureFS) reader() captureConfigReader {
	return captureConfigReader{
		lstat: func(p string) (os.FileInfo, error) {
			find := scripted(s.lstats, s.nLstat)
			s.nLstat++
			switch find {
			case findFile:
				return os.Lstat(s.file)
			case findDir:
				return os.Lstat(s.dir)
			case findDenied:
				return nil, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrPermission}
			default:
				return nil, &fs.PathError{Op: "lstat", Path: p, Err: fs.ErrNotExist}
			}
		},
		open: func(p string) (*os.File, error) {
			require.NotEmpty(s.t, s.opens, "the reader opened config.json, which this case never expects")
			find := scripted(s.opens, s.nOpen)
			s.nOpen++
			switch find {
			case findFile:
				return os.Open(s.file)
			case findDir:
				return os.Open(s.dir)
			case findDenied:
				return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission}
			default:
				return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
			}
		},
		clock: s,
		yield: func() { s.yields++; s.now = s.now.Add(captureTick) },
	}
}

// TestReadCaptureConfig_RechecksAFileGoneBetweenLstatAndOpen pins what the hook path does when its
// Lstat found config.json and its open then did not. That is positive evidence the file exists: on
// Windows it is the moment a rename-replace leaves the name missing (18.8-115.4 ms measured,
// captureConfigGoneBudget), after which the new file is there. Taking it for a missing layer would
// capture that delivery without the file's runtime.mode and runtime.redact, the fail-open the
// privacy policy never allows. So the reader looks again, for at most captureConfigGoneBudget, and
// takes the layer for missing only if the file stayed absent throughout; a file that comes back and
// still cannot be read is refused, as any existing file that cannot be read is.
func TestReadCaptureConfig_RechecksAFileGoneBetweenLstatAndOpen(t *testing.T) {
	budgetYields := int(captureConfigGoneBudget / captureTick)
	for _, tc := range []struct {
		name          string
		lstats, opens []captureFind
		wantBytes     bool
		wantMissing   bool
		wantOK        bool
		// wantFullBudget says the reader must have spent the whole budget before answering.
		wantFullBudget bool
	}{
		{
			name:   "the file is back at the next look and is read",
			lstats: []captureFind{findFile, findFile}, opens: []captureFind{findAbsent, findFile},
			wantBytes: true, wantOK: true,
		},
		{
			name:      "the file is back after several absent looks and is read",
			lstats:    []captureFind{findFile, findAbsent, findAbsent, findAbsent, findFile},
			opens:     []captureFind{findAbsent, findFile},
			wantBytes: true, wantOK: true,
		},
		{
			name:   "the file stays absent through the whole budget: missing",
			lstats: []captureFind{findFile, findAbsent}, opens: []captureFind{findAbsent},
			wantMissing: true, wantOK: true, wantFullBudget: true,
		},
		{
			name:   "a directory is back in its place: refused",
			lstats: []captureFind{findFile, findDir}, opens: []captureFind{findAbsent},
		},
		{
			name:   "the name keeps coming back and every open misses: refused at the budget",
			lstats: []captureFind{findFile}, opens: []captureFind{findAbsent},
			wantFullBudget: true,
		},
		{
			name:   "the file is back and its open fails for another reason: refused",
			lstats: []captureFind{findFile, findFile}, opens: []captureFind{findAbsent, findDenied},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScriptedCaptureFS(t, tc.lstats, tc.opens)
			b, missing, ok := s.reader().read(s.file)
			if tc.wantBytes {
				require.Equal(t, capturedBody, string(b))
			} else {
				require.Nil(t, b)
			}
			require.Equal(t, tc.wantMissing, missing, "missing")
			require.Equal(t, tc.wantOK, ok, "ok")
			if tc.wantFullBudget {
				require.GreaterOrEqual(t, s.now.Sub(s.start), captureConfigGoneBudget,
					"the reader answered before its re-check budget was spent")
				require.LessOrEqual(t, s.yields, budgetYields+1, "the re-check must stop at its budget")
			} else {
				require.Less(t, s.now.Sub(s.start), captureConfigGoneBudget,
					"the reader must answer as soon as it has its answer, not wait out the budget")
			}
		})
	}
}

// TestReadCaptureConfig_AnswersAtOnceWithoutARecheck pins the cases that never enter the re-check,
// so its cost stays on the one path that needs it: a config.json the Lstat does not find is missing
// at once, with no open and no wait — the common case of a project with no config file, and the
// residual docs/cannot-do.md records for a rename-replace whose absent moment the Lstat itself
// falls into — and an existing file whose Lstat or open fails for any reason but absence is refused
// at once, never taken for missing (a permission refusal, a sharing violation).
func TestReadCaptureConfig_AnswersAtOnceWithoutARecheck(t *testing.T) {
	for _, tc := range []struct {
		name          string
		lstats, opens []captureFind
		wantBytes     bool
		wantMissing   bool
		wantOK        bool
	}{
		{name: "found and read", lstats: []captureFind{findFile}, opens: []captureFind{findFile}, wantBytes: true, wantOK: true},
		{name: "the Lstat finds no file: missing", lstats: []captureFind{findAbsent}, wantMissing: true, wantOK: true},
		{name: "the Lstat fails for another reason: refused", lstats: []captureFind{findDenied}},
		{name: "the Lstat finds a directory: refused", lstats: []captureFind{findDir}},
		{name: "the open fails for another reason: refused", lstats: []captureFind{findFile}, opens: []captureFind{findDenied}},
		{name: "the open finds a directory: refused", lstats: []captureFind{findFile}, opens: []captureFind{findDir}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScriptedCaptureFS(t, tc.lstats, tc.opens)
			b, missing, ok := s.reader().read(s.file)
			if tc.wantBytes {
				require.Equal(t, capturedBody, string(b))
			} else {
				require.Nil(t, b)
			}
			require.Equal(t, tc.wantMissing, missing, "missing")
			require.Equal(t, tc.wantOK, ok, "ok")
			require.Zero(t, s.yields, "no re-check may run here")
			require.Equal(t, 1, s.nLstat, "exactly one Lstat")
			require.LessOrEqual(t, s.nOpen, 1, "at most one open")
		})
	}
}
