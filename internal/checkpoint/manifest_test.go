package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
)

// frozenClock is a two-method core.Clock double, local to this in-package test file because
// in-package test imports get no carve-out for internal/testutil (00-ARCHITECTURE.md §3.2, as
// tools/devtool/importgraph.go's checkTestImportRoots spells it): an import there is an edge out
// of internal/checkpoint in everything but name. The x_test half of this package's tests
// (reader_test.go) uses the real testutil.FakeClock.
//
// fakeEpoch is testutil.Epoch's instant, spelled locally for the same reason. The goldens this
// package reproduces are the evidence the two match.
type frozenClock struct{ now time.Time }

var fakeEpoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func (c frozenClock) Now() time.Time                  { return c.now }
func (c frozenClock) Since(t time.Time) time.Duration { return c.now.Sub(t) }

// manifestLayout returns a real, ensured layout under t.TempDir(). There are no fake packages in
// this tree: every manifest test below writes real lines through paths.AppendManifest, the only
// legal writer of checkpoints/MANIFEST.jsonl.
func manifestLayout(t *testing.T) paths.Layout {
	t.Helper()
	l := paths.Of(t.TempDir())
	require.NoError(t, paths.EnsureLayout(l))
	return l
}

// appendEntries appends one manifest line per seq, timestamped from clk, exactly as Finalize
// does. The digests are not of any real file: maxSeq reads seq and nothing else.
func appendEntries(t *testing.T, l paths.Layout, clk core.Clock, seqs ...core.CheckpointSeq) {
	t.Helper()
	for _, s := range seqs {
		require.NoError(t, paths.AppendManifest(l, paths.ManifestEntry{
			Seq:     s,
			SHA256:  core.HashBytes(core.DomainChunk, []byte{byte(s)}).String(),
			Bytes:   int64(s),
			Created: core.NowMilli(clk),
		}))
	}
}

// captureLoud installs a process-wide Loud observer for the duration of one test and returns the
// messages it saw. logging.Nop() still fires the observer, so loudness is assertable without
// touching the filesystem.
func captureLoud(t *testing.T) *[]string {
	t.Helper()
	var msgs []string
	logging.AttachLoudObserver(func(msg string, _ ...any) { msgs = append(msgs, msg) })
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })
	return &msgs
}

// appendRawManifestLine appends text to the manifest without going through paths.AppendManifest,
// which is the only way to produce the malformed line paths.ReadManifest hard-errors on. It
// exists only in tests: production has exactly one writer.
func appendRawManifestLine(l paths.Layout, text string) error {
	f, err := os.OpenFile(paths.Long(paths.ManifestPath(l)), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(text)
	return err
}

// TestMaxSeqIsZeroWithoutManifest pins the absent-manifest answer. paths.ReadManifest returns
// (nil, nil) when no checkpoint has ever been written, and 0 is CheckpointSeq's documented
// "none", so the first checkpoint of a project is seq 1 without any special case.
func TestMaxSeqIsZeroWithoutManifest(t *testing.T) {
	require.Equal(t, core.CheckpointSeq(0), maxSeq(manifestLayout(t)))
}

// TestMaxSeqIsLargestSeqNotLastLine asserts maxSeq takes the largest Seq rather than the last
// line: the manifest is append-only but nothing in paths orders it, and a resumed session that
// re-appends an older seq must not make the next Finalize reuse a filename.
func TestMaxSeqIsLargestSeqNotLastLine(t *testing.T) {
	l := manifestLayout(t)
	clk := frozenClock{now: fakeEpoch}

	appendEntries(t, l, clk, 1, 2, 3)
	require.Equal(t, core.CheckpointSeq(3), maxSeq(l))

	appendEntries(t, l, clk, 7, 4)
	require.Equal(t, core.CheckpointSeq(7), maxSeq(l))
}

// TestMaxSeqSurfacesAMalformedManifestLoudly is the observability half of "a ReadManifest error is
// surfaced, never swallowed". maxSeq's signature (plans/V4-SP-10-checkpointer-l4.md line 972, and
// its call site `maxSeq(l) + 1` at line 810) has no error channel, so the surface is the counter
// and the Loud — see maxSeq's doc comment.
func TestMaxSeqSurfacesAMalformedManifestLoudly(t *testing.T) {
	l := manifestLayout(t)
	clk := frozenClock{now: fakeEpoch}
	appendEntries(t, l, clk, 1, 2)

	reg := obs.New(clk)
	SetObservers(logging.Nop(), reg)
	t.Cleanup(func() { SetObservers(nil, nil) })
	loud := captureLoud(t)

	require.NoError(t, appendRawManifestLine(l, "garbage\n"))

	require.Equal(t, core.CheckpointSeq(0), maxSeq(l), "an unreadable manifest yields no sequence")
	require.Equal(t, int64(1), reg.Counter("checkpoint.manifest_badline").Value())
	require.Len(t, *loud, 1)
}

// TestSeqFromFilename covers the parser Chain follows Checkpoint.Parent with. There is
// deliberately no checkpoint.Filename to pair with it: the %04d.json pattern has exactly one
// owner, internal/paths, and a checkpoint's own filename is filepath.Base(paths.CheckpointPath).
func TestSeqFromFilename(t *testing.T) {
	for _, tc := range []struct {
		name string
		want core.CheckpointSeq
	}{
		{"0001.json", 1},
		{"0006.json", 6},
		{"0012.json", 12},
		{"9999.json", 9999},
		{"10000.json", 10000},
	} {
		got, err := seqFromFilename(tc.name)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.want, got, tc.name)
	}

	for _, bad := range []string{
		"",
		"0006",
		"0006.txt",
		"MANIFEST.jsonl",
		"000x.json",
		"-001.json",
		"0000.json",
		"checkpoints/0006.json",
		`checkpoints\0006.json`,
		"../0006.json",
		"0006.json.bak",
		strings.Repeat("9", 40) + ".json",
	} {
		_, err := seqFromFilename(bad)
		require.ErrorIs(t, err, core.ErrContract, "%q must not parse", bad)
	}
}

// TestSeqFromFilenameRoundTripsCheckpointPath asserts the parser is the exact inverse of the one
// renderer, over the whole range a project can reach in practice.
func TestSeqFromFilenameRoundTripsCheckpointPath(t *testing.T) {
	l := manifestLayout(t)
	for _, seq := range []core.CheckpointSeq{1, 7, 42, 1000, 99999} {
		name := filepath.Base(paths.CheckpointPath(l, seq))
		got, err := seqFromFilename(name)
		require.NoError(t, err, name)
		require.Equal(t, seq, got, name)
	}
}
