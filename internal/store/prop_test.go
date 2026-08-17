package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// PropPutGetRoundtrip asserts the store never loses or alters a byte: whatever Put stored,
// Open returns exactly, for arbitrary payloads.
//
// The expected value is the canonicalized-and-redacted form rather than the raw input, because
// those two passes are part of what Put promises to store — the invariant is "Open returns what
// was STORED", not "Open returns what was handed in".
func TestPropPutGetRoundtrip(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()

	rapid.Check(t, func(rt *rapid.T) {
		payload := rapid.SliceOfN(rapid.Byte(), 0, 64*1024).Draw(rt, "payload")

		res, err := tp.Store.PutBytes(ctx, payload, PutOptions{Tool: "Bash", Path: "src/prop.txt"})
		if err != nil {
			rt.Fatalf("PutBytes: %v", err)
		}

		rc, err := tp.Store.Open(ctx, res.Root.Hash)
		if err != nil {
			rt.Fatalf("Open: %v", err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			rt.Fatalf("ReadAll: %v", err)
		}
		if string(got) != string(payload) {
			rt.Fatalf("round-trip lost bytes: stored %d, read back %d", len(payload), len(got))
		}
	})
}

// PropOpenSpanMatchesSlice asserts OpenSpan is exactly the clamped slice of the full content, for
// arbitrary offsets and lengths including out-of-range ones.
func TestPropOpenSpanMatchesSlice(t *testing.T) {
	tp := newTestStore(t)
	ctx := context.Background()
	payload := bigPayload(300 * 1024)

	res, err := tp.Store.PutBytes(ctx, payload, PutOptions{Path: "src/spanprop.txt"})
	require.NoError(t, err)
	total := int64(len(payload))

	rapid.Check(t, func(rt *rapid.T) {
		off := rapid.Int64Range(-1000, total+1000).Draw(rt, "off")
		n := rapid.Int64Range(-1000, total+1000).Draw(rt, "n")

		rc, err := tp.Store.OpenSpan(ctx, res.Root.Hash, off, n)
		if err != nil {
			rt.Fatalf("OpenSpan(%d,%d): %v", off, n, err)
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			rt.Fatalf("ReadAll: %v", err)
		}

		lo := off
		if lo < 0 {
			lo = 0
		}
		if lo > total {
			lo = total
		}
		length := n
		if length <= 0 || lo+length > total {
			length = total - lo
		}
		want := payload[lo : lo+length]
		if string(got) != string(want) {
			rt.Fatalf("OpenSpan(%d,%d): got %d bytes, want %d", off, n, len(got), len(want))
		}
	})
}

// PropDedupMonotone asserts storing two payloads together never costs more objects than storing
// each on its own — the defining property of content-addressed dedup.
//
// Each measurement opens and CLOSES its own store under a directory this test owns, rather than
// going through newTestStore: rapid runs the body a hundred times, and t.Cleanup-registered stores
// would all stay open until the test ended, exhausting file handles.
func TestPropDedupMonotone(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700))
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var seq int
	countObjects := func(rt *rapid.T, payloads ...[]byte) int {
		seq++
		root := filepath.Join(base, fmt.Sprintf("p%d", seq))
		if err := os.MkdirAll(paths.Long(root), 0o700); err != nil {
			rt.Fatalf("mkdir: %v", err)
		}
		s, err := openFS(root, config.Defaults(), Deps{
			Chunker: newFixedChunker(), Canon: canonIdentity(),
			Log: logging.Nop(), Clock: newFakeClock(),
		})
		if err != nil {
			rt.Fatalf("openFS: %v", err)
		}
		defer func() { _ = s.Close() }()

		for _, p := range payloads {
			if _, err := s.PutBytes(ctx, p, PutOptions{Path: "src/m.txt"}); err != nil {
				rt.Fatalf("PutBytes: %v", err)
			}
		}

		n := 0
		objects := paths.Long(paths.Of(root).Objects)
		if err := filepath.WalkDir(objects, func(_ string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				n++
			}
			return nil
		}); err != nil {
			rt.Fatalf("walk: %v", err)
		}
		return n
	}

	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.SliceOfN(rapid.Byte(), 0, 16*1024).Draw(rt, "a")
		b := rapid.SliceOfN(rapid.Byte(), 0, 16*1024).Draw(rt, "b")

		both := countObjects(rt, a, b)
		alone := countObjects(rt, a) + countObjects(rt, b)
		if both > alone {
			rt.Fatalf("storing both cost %d objects, more than %d for each stored alone", both, alone)
		}
	})
}
