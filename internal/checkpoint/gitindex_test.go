package checkpoint

// Tests for the pure-Go .git/index reader (SP-10 §11). The fixtures under
// testdata/fixtures/gitindex are real git output, generated once and committed (see the README
// beside them): v2/v3 parse to the same three entries, v4 and truncated must be refused with
// errIndexUnsupported, and FuzzParseGitIndex holds the "never panics" line on arbitrary bytes.

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureEntryShape is what real git recorded for the three files the fixture repository staged;
// sizes are fixed by the documented printf invocations, mtimes by the machine that generated the
// fixtures (tests read those from the parse result rather than pinning them).
var fixtureEntryShape = []struct {
	path string
	size uint32
}{
	{"a.txt", 6},
	{"b/c.txt", 13},
	{"d.txt", 22},
}

func TestGitIndexV2ParsesEntries(t *testing.T) {
	entries, err := parseIndex(readGitindexFixture(t, "v2.index"))
	require.NoError(t, err)
	require.Len(t, entries, 3)
	for i, want := range fixtureEntryShape {
		require.Equal(t, want.path, entries[i].Path)
		require.Equal(t, want.size, entries[i].Size)
		require.NotZero(t, entries[i].MTimeSec, "real git recorded a real mtime")
	}
}

func TestGitIndexV3ExtendedFlags(t *testing.T) {
	entries, err := parseIndex(readGitindexFixture(t, "v3.index"))
	require.NoError(t, err)
	require.Len(t, entries, 3, "the extended-flags entry must not desynchronize the walk")
	for i, want := range fixtureEntryShape {
		require.Equal(t, want.path, entries[i].Path)
		require.Equal(t, want.size, entries[i].Size)
	}
}

func TestGitIndexV4Unsupported(t *testing.T) {
	_, err := parseIndex(readGitindexFixture(t, "v4.index"))
	require.ErrorIs(t, err, errIndexUnsupported)

	// ValidatePointers still performs check 1 and reports the gap as one visible entry.
	root, _ := makeGitRoot(t, "v4.index")
	materialize(t, root, "a.txt", 6, 1767225480)
	drops, err := ValidatePointers(context.Background(), root, Pointers{
		Files: []FilePointer{
			{Path: "a.txt", Why: "present"},
			{Path: "gone.ts", Why: "absent"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pointer_missing", "pointer_git_unavailable"}, dropKinds(drops))
}

func TestGitIndexTruncated(t *testing.T) {
	_, err := parseIndex(readGitindexFixture(t, "truncated.index"))
	require.ErrorIs(t, err, errIndexUnsupported, "an entry count that overruns the buffer is refused, not chased")
}

func TestGitIndexUnknownMagic(t *testing.T) {
	b := readGitindexFixture(t, "v2.index")
	mangled := append([]byte("XXXX"), b[4:]...)
	_, err := parseIndex(mangled)
	require.ErrorIs(t, err, errIndexUnsupported)

	_, err = parseIndex([]byte("DI"))
	require.ErrorIs(t, err, errIndexUnsupported, "a short header is a short read, not a panic")
}

func TestGitIndexOver64MiBIsUnsupported(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	idx := filepath.Join(gitDir, "index")
	require.NoError(t, os.WriteFile(idx, readGitindexFixture(t, "v2.index"), 0o644))
	require.NoError(t, os.Truncate(idx, maxIndexBytes+1))

	_, _, err := readGitState(root)
	require.ErrorIs(t, err, errIndexUnsupported, "the 64 MiB cap refuses before reading")
}

func TestGitIndexNoGitDirSentinel(t *testing.T) {
	_, _, err := readGitState(t.TempDir())
	require.ErrorIs(t, err, errNoGitDir)
}

func TestGitHeadDetached(t *testing.T) {
	require.Equal(t, "main", parseHead([]byte("ref: refs/heads/main\n")))
	require.Equal(t, "feat/x9-gc", parseHead([]byte("ref: refs/heads/feat/x9-gc\n")))
	require.Equal(t, "detached@4d7a0c3f6b9e",
		parseHead([]byte("4d7a0c3f6b9e2158a4d7c0f3b6e9a2d5c8f1b4e7\n")),
		"a detached HEAD is named by its first 12 characters")
}

func FuzzParseGitIndex(f *testing.F) {
	for _, name := range []string{"v2.index", "v3.index", "v4.index", "truncated.index"} {
		f.Add(readGitindexFixture(f, name))
	}
	// Hand seeds: empty, header-only, and a header whose count promises entries it does not have.
	f.Add([]byte{})
	f.Add([]byte("DIRC\x00\x00\x00\x02\x00\x00\x00\x00"))
	f.Add([]byte("DIRC\x00\x00\x00\x02\xff\xff\xff\xff"))

	f.Fuzz(func(t *testing.T, b []byte) {
		entries, err := parseIndex(b) // must never panic
		if err != nil {
			return
		}
		for _, e := range entries {
			if bytes.IndexByte([]byte(e.Path), 0) >= 0 {
				t.Fatalf("well-formed entry list contains a NUL in a path: %q", e.Path)
			}
		}
	})
}

// buildIndex assembles a syntactically valid index (header + entries, no extensions) so the
// corrupt-framing tests can start from a good document and break exactly one thing.
func buildIndex(version uint32, names []string, extended bool) []byte {
	var b bytes.Buffer
	b.WriteString(indexMagic)
	_ = binary.Write(&b, binary.BigEndian, version)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(names)))
	for _, p := range names {
		start := b.Len()
		stat := make([]byte, 40)
		binary.BigEndian.PutUint32(stat[indexEntryMTimeOff:], 1767225480)
		binary.BigEndian.PutUint32(stat[indexEntrySizeOff:], 42)
		b.Write(stat)
		b.Write(make([]byte, 20)) // object id
		flags := uint16(len(p))
		if len(p) >= int(indexNameMask) {
			flags = indexNameMask
		}
		if extended {
			flags |= indexFlagExtended
		}
		_ = binary.Write(&b, binary.BigEndian, flags)
		if extended {
			_ = binary.Write(&b, binary.BigEndian, uint16(0))
		}
		b.WriteString(p)
		entryLen := b.Len() - start
		pad := ((entryLen + 8) &^ 7) - entryLen
		b.Write(make([]byte, pad))
	}
	return b.Bytes()
}

func TestGitIndexSaturatedPathLengthParses(t *testing.T) {
	long := strings.Repeat("a", 4200) // over the 0x0FFF cap: length saturates, path is read to its NUL
	entries, err := parseIndex(buildIndex(2, []string{long}, false))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, long, entries[0].Path)
	require.Equal(t, uint32(42), entries[0].Size)
}

func TestGitIndexCraftedCorruptions(t *testing.T) {
	good := buildIndex(2, []string{"q"}, false)

	t.Run("path_terminator_missing", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[indexHeaderLen+indexEntryFixedLen+1] = 'x' // the byte after the 1-byte path must be NUL
		_, err := parseIndex(bad)
		require.ErrorIs(t, err, errIndexUnsupported)
	})
	t.Run("nul_inside_claimed_path", func(t *testing.T) {
		withNul := buildIndex(2, []string{"q\x00"}, false)
		_, err := parseIndex(withNul)
		require.ErrorIs(t, err, errIndexUnsupported)
	})
	t.Run("saturated_path_unterminated", func(t *testing.T) {
		long := buildIndex(2, []string{strings.Repeat("a", 4200)}, false)
		cut := long[:indexHeaderLen+indexEntryFixedLen+100] // inside the path, before any NUL
		_, err := parseIndex(cut)
		require.ErrorIs(t, err, errIndexUnsupported)
	})
	t.Run("extended_flags_overrun", func(t *testing.T) {
		v3 := buildIndex(3, []string{"q"}, true)
		cut := v3[:indexHeaderLen+indexEntryFixedLen] // ends before the extended uint16
		_, err := parseIndex(cut)
		require.ErrorIs(t, err, errIndexUnsupported)
	})
}

func TestGitDirFileMalformed(t *testing.T) {
	for name, content := range map[string]string{
		"no_prefix":    "worktree = ../real\n",
		"empty_gitdir": "gitdir: \n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte(content), 0o644))
			_, err := resolveGitDir(root)
			require.ErrorIs(t, err, errIndexUnsupported,
				"an unusable .git file is visible degradation, not errNoGitDir")
		})
	}
}
