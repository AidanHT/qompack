// This is an INTERNAL test package (package observer, not observer_test) for two reasons:
// humanBytes is unexported, and its rounding is the part of Tombstone most likely to drift
// silently — a 1000-based divisor would render Qompack.md §8.1's own example as "2.5KB" and no
// external assertion on Tombstone alone would say why. Testing it directly makes the failure name
// itself. supersedableClass is unexported too, and it is the predicate the redundancy detection of
// §8.1 item 3 keys on, so it is asserted here rather than inferred from an observed supersession.
package observer

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
	"github.com/stretchr/testify/require"
)

// section81Hash is a hash whose Short() form is the "a3f2…" of Qompack.md §8.1's example
// tombstone, so the assertion below can be byte-for-byte against the design document.
const section81Hash = "sha256:a3f2c9e14b70d5183c6a94f27b0e5d8a1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e"

// hexHashLen is the number of hex characters core.ParseHash requires: a full sha256 digest.
const hexHashLen = 64

// hashFiller pads a 12-character short hash out to hexHashLen. Only the first 12 characters ever
// reach a marker, so the tail is arbitrary — but it is fixed, which is what makes every fixture
// below byte-reproducible and therefore safe to freeze in a golden file.
const hashFiller = "9e14b70d5183c6a94f27b0e5d8a1c4f7b0e3a6d9c2f5b8e1a4d7c0f3b6e"

// hashShort returns a core.Hash whose Short() is exactly short, which must be 12 hex characters.
// It takes a testing.TB rather than a *testing.T so BenchmarkTombstone can build its record the
// same way the tests do.
func hashShort(tb testing.TB, short string) core.Hash {
	tb.Helper()
	h, err := core.ParseHash((short + hashFiller)[:hexHashLen])
	require.NoError(tb, err, "short hash %q must be 12 hex characters", short)
	require.Equal(tb, short, h.Short())
	return h
}

// TestTombstone_RendersTheSection81Form asserts the exact marker Qompack.md §8.1 item 2 shows.
// Every character is load-bearing: the "sha256:" prefix and the 12-character short hash make the
// marker a real store address that `expand` can resolve, the ellipsis says the hash is an elision
// rather than the whole digest, and the U+00B7 separators are what the /qompack:status renderer
// and the docs both reproduce.
func TestTombstone_RendersTheSection81Form(t *testing.T) {
	root, err := core.ParseHash(section81Hash)
	require.NoError(t, err)

	rec := store.ToolUseRecord{
		Root:  root,
		Bytes: 2457,
		Tool:  "FileRead",
		Path:  "src/auth.ts",
	}

	require.Equal(t,
		"[cleared: sha256:a3f2c9e14b70… · 2.4KB · FileRead src/auth.ts · re-expandable]",
		Tombstone(rec))
}

// TestTombstone_IsAddressable asserts the property that makes a tombstone worth writing at all: a
// cleared result stays fetchable, because the marker carries the same 12 hex characters
// core.Hash.Short produces and can therefore be pasted straight back into a retrieval call.
func TestTombstone_IsAddressable(t *testing.T) {
	root, err := core.ParseHash(section81Hash)
	require.NoError(t, err)

	got := Tombstone(store.ToolUseRecord{Root: root, Bytes: 1, Tool: "Grep", Path: "src/pool.ts"})
	require.Contains(t, got, root.Short(), "the marker must carry a resolvable store address")
	require.Contains(t, got, "re-expandable", "the marker must say the content can be fetched back")
}

// TestTombstone_NormalizesTheHostToolName asserts the host's own tool name never reaches a marker:
// Claude Code says "Read", every Qompack surface says "FileRead" (§2.2), and the marker is one of
// those surfaces.
func TestTombstone_NormalizesTheHostToolName(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "b1c2d3e4f506"), Bytes: 973, Tool: "Read", Path: "src/index.ts",
	}

	require.Equal(t,
		"[cleared: sha256:b1c2d3e4f506… · 973B · FileRead src/index.ts · re-expandable]",
		Tombstone(rec))
}

// TestTombstone_NoPathUsesArgsPreview asserts the subject falls back to the argument preview when
// a tool has no path of its own. A Bash result is the common case: "[cleared: … · Bash]" tells a
// reader nothing, while the command line tells them whether they want it back.
func TestTombstone_NoPathUsesArgsPreview(t *testing.T) {
	rec := store.ToolUseRecord{
		Root:  hashShort(t, "c2d3e4f50617"),
		Bytes: 812,
		Tool:  "Bash",
		Path:  "",

		ArgsPreview: "go test ./internal/store/...",
	}

	require.Equal(t,
		"[cleared: sha256:c2d3e4f50617… · 812B · Bash go test ./internal/store/... · re-expandable]",
		Tombstone(rec))
}

// TestTombstone_LongPreviewTruncatedTo48Runes asserts the preview subject is bounded. A tombstone
// is a one-line marker injected into a context window that is already under pressure; an unbounded
// subject would let one pathological command line cost more than the result it replaced.
func TestTombstone_LongPreviewTruncatedTo48Runes(t *testing.T) {
	const previewLen = 200
	rec := store.ToolUseRecord{
		Root: hashShort(t, "516273849506"), Tool: "Bash",
		ArgsPreview: strings.Repeat("x", previewLen),
	}

	wantSubject := strings.Repeat("x", tombstonePreviewRunes-1) + tombstoneEllipsis
	require.Equal(t, tombstonePreviewRunes, utf8.RuneCountInString(wantSubject),
		"the truncated subject must be exactly %d runes wide", tombstonePreviewRunes)
	require.Equal(t,
		"[cleared: sha256:516273849506… · 0B · Bash "+wantSubject+" · re-expandable]",
		Tombstone(rec))
}

// TestTombstone_MegabyteSize asserts the MB tier reaches the marker.
func TestTombstone_MegabyteSize(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "f50617283940"), Bytes: 3_500_000, Tool: "WebFetch",
		Path: "https://pkg.go.dev/regexp",
	}
	require.Contains(t, Tombstone(rec), " · 3.3MB · ")
}

// TestTombstone_GigabyteSize asserts the shipped humanBytes GB tier survives SP-08's extension.
// The tier is not hypothetical: a single large build log or a repository-wide grep can exceed a
// gigabyte, and a marker that overflowed into "3072.0MB" would be the first sign of it.
func TestTombstone_GigabyteSize(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "061728394051"), Bytes: 3 << 30, Tool: "FileWrite",
		Path: "dist/bundle.js",
	}
	require.Contains(t, Tombstone(rec), " · 3.0GB · ")
}

// TestTombstone_SupersededMarker asserts a superseded record says so. §8.1 item 3 makes superseded
// reads the first candidates for eviction, so a reader who sees the marker should be able to tell
// that a newer answer for the same path already exists without resolving the hash.
func TestTombstone_SupersededMarker(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "172839405162"), Bytes: 2458, Tool: "FileRead",
		Path: "src/auth.ts", Status: store.StatusSuperseded,
	}

	got := Tombstone(rec)
	require.True(t, strings.HasSuffix(got, " · superseded · re-expandable]"),
		"superseded must be the segment immediately before re-expandable, got %q", got)
}

// TestTombstone_EphemeralMarker asserts a born-ephemeral record says so (§8.7). A retrieval result
// is the one kind of content Qompack itself put in the window, and it is first in the eviction
// order.
func TestTombstone_EphemeralMarker(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "283940516273"), Bytes: 1536, Tool: "mcp__qompack__recall",
		Path: "src/auth.ts", Ephemeral: true,
	}

	got := Tombstone(rec)
	require.True(t, strings.HasSuffix(got, " · ephemeral · re-expandable]"),
		"ephemeral must be the segment immediately before re-expandable, got %q", got)
}

// TestTombstone_BothMarkers pins the order of the two optional segments. Order is not cosmetic:
// the golden file freezes it, and a marker whose segments swapped between runs would make every
// diff of a rehydration block unreadable.
func TestTombstone_BothMarkers(t *testing.T) {
	rec := store.ToolUseRecord{
		Root: hashShort(t, "394051627384"), Bytes: 2048, Tool: "FileRead",
		Path: "src/auth.ts", Status: store.StatusSuperseded, Ephemeral: true,
	}

	got := Tombstone(rec)
	require.True(t, strings.HasSuffix(got, " · ephemeral · superseded · re-expandable]"),
		"ephemeral precedes superseded, got %q", got)
}

// TestTombstone_NoSubject asserts a record with neither a path nor a preview elides the subject
// group entirely, leading space included. It is the tightened successor to SP-01's
// TestTombstone_HandlesAnEmptyRecord: the renderer must still never panic on a zero-ish record,
// and it must no longer emit the double space the old Sprintf form left behind.
func TestTombstone_NoSubject(t *testing.T) {
	rec := store.ToolUseRecord{Root: hashShort(t, "405162738495"), Tool: "Bash"}

	got := Tombstone(rec)
	require.Equal(t, "[cleared: sha256:405162738495… · 0B · Bash · re-expandable]", got)
	require.NotContains(t, got, "  ", "an absent subject must not leave a double space")

	require.NotPanics(t, func() { _ = Tombstone(store.ToolUseRecord{}) },
		"a zero record must degrade to a useless-but-valid line, never take a hook down (§12.3)")
}

// goldenTombstoneRecords is the fixture set TestTombstoneGolden freezes: thirteen records chosen so
// that every branch of the renderer and every tier of humanBytes appears at least once — the §8.1
// design example, a host name that needs normalizing, a path subject, a preview subject, a
// truncated preview, no subject at all, each optional status segment alone and both together, a
// non-compactable tool, and the B/KB/MB/GB size tiers.
func goldenTombstoneRecords(tb testing.TB) []store.ToolUseRecord {
	tb.Helper()
	const longCommand = "go test -race -count=1 -timeout=30m ./internal/observer/... " +
		"./internal/store/... ./internal/dag/... ./internal/canon/... ./internal/sketch/..."

	return []store.ToolUseRecord{
		{Root: hashShort(tb, "a3f2c19d0b74"), Bytes: 2458, Tool: "FileRead", Path: "src/auth.ts"},
		{Root: hashShort(tb, "b1c2d3e4f506"), Bytes: 973, Tool: "Read", Path: "src/index.ts"},
		{
			Root: hashShort(tb, "c2d3e4f50617"), Bytes: 812, Tool: "Bash",
			ArgsPreview: "go test ./internal/store/...",
		},
		{Root: hashShort(tb, "d3e4f5061728"), Bytes: 1024, Tool: "Grep", Path: "internal/observer"},
		{Root: hashShort(tb, "e4f506172839"), Bytes: 4096, Tool: "Glob", ArgsPreview: "**/*.go"},
		{
			Root: hashShort(tb, "f50617283940"), Bytes: 3_500_000, Tool: "WebFetch",
			Path: "https://pkg.go.dev/regexp",
		},
		{
			Root: hashShort(tb, "061728394051"), Bytes: 3 << 30, Tool: "FileWrite",
			Path: "dist/bundle.js",
		},
		{
			Root: hashShort(tb, "172839405162"), Bytes: 2458, Tool: "FileRead",
			Path: "src/auth.ts", Status: store.StatusSuperseded,
		},
		{
			Root: hashShort(tb, "283940516273"), Bytes: 1536, Tool: "mcp__qompack__recall",
			Path: "src/auth.ts", Ephemeral: true,
		},
		{
			Root: hashShort(tb, "394051627384"), Bytes: 2048, Tool: "FileRead",
			Path: "src/auth.ts", Status: store.StatusSuperseded, Ephemeral: true,
		},
		{Root: hashShort(tb, "405162738495"), Tool: "Bash"},
		{Root: hashShort(tb, "516273849506"), Bytes: 65536, Tool: "Bash", ArgsPreview: longCommand},
		{
			Root: hashShort(tb, "627384950617"), Bytes: 65536, Tool: "Task",
			ArgsPreview: "review the diff for correctness",
		},
	}
}

// tombstoneGoldenPath is testdata/golden/observer/tombstones.txt, spelled relative to this package.
//
// testutil.Golden would resolve the same file and is what an external test would use, but this is
// an in-package test file: tools/devtool/importgraph.go forbids .TestImports from reaching the
// internal/testutil composition root, and its one carve-out is for x_test packages only. The
// relative spelling is the same one internal/dag/golden_test.go and internal/config already use.
const tombstoneGoldenPath = "../../testdata/golden/observer/tombstones.txt"

// tombstoneGoldenPerm matches testutil's: a golden is committed source, not a runtime store file.
const tombstoneGoldenPerm = 0o644

// updateGolden reports whether -update was passed.
//
// The flag is looked up before being registered because other packages in this repository declare
// an -update flag of their own; two registrations of one name panic a test binary during init.
// Looking it up first turns that collision into what both sides wanted: one flag driving every
// golden in the binary.
var updateGolden = registerGoldenUpdateFlag()

// registerGoldenUpdateFlag returns a reader for -update, registering the flag only if nothing else
// already has.
func registerGoldenUpdateFlag() func() bool {
	const name = "update"
	if f := flag.Lookup(name); f != nil {
		return func() bool { return f.Value.String() == "true" }
	}
	p := flag.Bool(name, false, "rewrite golden files under testdata/golden/ instead of comparing against them")
	return func() bool { return *p }
}

// TestTombstoneGolden freezes the rendered form of every branch of the renderer. Qompack.md §8.1
// item 2 specifies the marker down to the separator, and SP-11 and SP-13 both re-emit it verbatim,
// so a change to any part of the grammar has to be a deliberate golden update rather than a diff
// nobody notices until a rehydration block looks wrong in a live session.
func TestTombstoneGolden(t *testing.T) {
	recs := goldenTombstoneRecords(t)

	var sb strings.Builder
	for _, rec := range recs {
		sb.WriteString(Tombstone(rec))
		sb.WriteByte('\n')
	}
	got := sb.String()

	if updateGolden() {
		require.NoError(t, os.MkdirAll(filepath.Dir(tombstoneGoldenPath), 0o755))
		require.NoError(t, os.WriteFile(tombstoneGoldenPath, []byte(got), tombstoneGoldenPerm))
		t.Logf("updated %s (%d records, %d bytes)", tombstoneGoldenPath, len(recs), len(got))
		return
	}

	want, err := os.ReadFile(tombstoneGoldenPath)
	require.NoError(t, err, "golden missing: %s (regenerate with -update)", tombstoneGoldenPath)

	// Both sides are normalized to LF: .gitattributes leaves .txt subject to eol=lf, but a Windows
	// checkout can still hand a test CRLF bytes, and a golden that failed on the primary
	// development platform for a reason unrelated to the renderer would be worse than no golden.
	require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), got,
		"the rendered markers drifted; if the change is intended, regenerate with -update and say why in the commit")
}

// TestTombstoneNote_SingleLine asserts the expand affordance stays one line. It is injected into a
// rehydration block and into every `expand` response, where a multi-line note would break the
// block's own structure — and it has to name the tool a reader is meant to call, or the marker's
// promise that a cleared result is re-expandable has no instructions attached.
func TestTombstoneNote_SingleLine(t *testing.T) {
	got := TombstoneNote()
	require.Contains(t, got, "expand", "the note must name the tool that resolves a marker")
	require.NotContains(t, got, "\n", "the note is one line")
}

// TestNormalizeToolName covers every row of the host→display table, plus the two ways a name can
// miss it: an MCP name, which must survive untouched so §8.7's ephemeral rule can still recognize
// qompack's own retrieval results, and an unknown host tool, which must pass through rather than
// disappear when Claude Code adds one.
func TestNormalizeToolName(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "Read", want: "FileRead"},
		{in: "NotebookRead", want: "FileRead"},
		{in: "Edit", want: "FileEdit"},
		{in: "MultiEdit", want: "FileEdit"},
		{in: "NotebookEdit", want: "FileEdit"},
		{in: "Write", want: "FileWrite"},
		{in: "Bash", want: "Bash"},
		{in: "BashOutput", want: "Bash"},
		{in: "PowerShell", want: "PowerShell"},
		{in: "Grep", want: "Grep"},
		{in: "Glob", want: "Glob"},
		{in: "WebSearch", want: "WebSearch"},
		{in: "WebFetch", want: "WebFetch"},
		{in: "Task", want: "AgentTool"},
		{in: "TodoWrite", want: "TodoWrite"},
		{in: "mcp__qompack__recall", want: "mcp__qompack__recall"},
		{in: "SomeToolTheHostAddedLater", want: "SomeToolTheHostAddedLater"},
		{in: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, NormalizeToolName(tc.in))
		})
	}
}

// TestIsCompactable pins the §2.2 set: only high-volume, reproducible host results may be replaced
// by a marker. AgentTool and MCP results are preserved, in as many words, so a false positive here
// would let a caller clear the one kind of content that cannot be reproduced by re-running a tool.
func TestIsCompactable(t *testing.T) {
	compactable := []string{
		"FileRead", "Bash", "PowerShell", "Grep", "Glob",
		"WebSearch", "WebFetch", "FileEdit", "FileWrite",
	}
	for _, tool := range compactable {
		t.Run(tool, func(t *testing.T) {
			require.True(t, IsCompactable(tool), "%s is in the §2.2 set", tool)
		})
	}

	preserved := []string{"AgentTool", "TodoWrite", "mcp__qompack__expand", "mcp__qompack__recall", ""}
	for _, tool := range preserved {
		t.Run("preserved/"+tool, func(t *testing.T) {
			require.False(t, IsCompactable(tool), "%q is preserved, never replaced by a marker", tool)
		})
	}

	// A host name answers the same question its display name does, so a caller holding the raw
	// tool_name off a hook payload gets the right answer without normalizing first.
	require.True(t, IsCompactable("Read"))
	require.False(t, IsCompactable("Task"))
}

// TestSupersedableClass pins which results may supersede which. Supersession is keyed on the class
// rather than on the exact tool because a FileWrite genuinely replaces an earlier FileRead of the
// same path, while a Grep of that path replaces neither; a class that leaked across those
// boundaries would evict content that is still the only answer for its question (§8.1 item 3).
func TestSupersedableClass(t *testing.T) {
	cases := []struct{ tool, want string }{
		{tool: "FileRead", want: "filecontent"},
		{tool: "FileEdit", want: "filecontent"},
		{tool: "FileWrite", want: "filecontent"},
		{tool: "Grep", want: "search"},
		{tool: "Glob", want: "search"},
		{tool: "Bash", want: "exec"},
		{tool: "PowerShell", want: "exec"},
		{tool: "WebFetch", want: "web"},
		{tool: "WebSearch", want: "web"},
		{tool: "AgentTool", want: ""},
		{tool: "TodoWrite", want: ""},
		{tool: "mcp__qompack__recall", want: ""},
		{tool: "", want: ""},
		// Host names resolve through the same table, so the raw tool_name works too.
		{tool: "Read", want: "filecontent"},
		{tool: "Task", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			require.Equal(t, tc.want, supersedableClass(tc.tool))
		})
	}
}

// TestHumanBytes_UsesTheBinaryDivisor is the assertion the whole golden rests on: 1 KB is 1024
// bytes. Each case names why it is here rather than being an arbitrary number.
func TestHumanBytes_UsesTheBinaryDivisor(t *testing.T) {
	const kb = 1024
	cases := []struct {
		name string
		in   int64
		want string
	}{
		{name: "zero", in: 0, want: "0B"},
		{name: "one byte", in: 1, want: "1B"},
		{name: "one below the KB boundary stays in bytes", in: kb - 1, want: "1023B"},
		{name: "exactly one KB", in: kb, want: "1.0KB"},
		{name: "the §8.1 example rounds to 2.4, not 2.5", in: 2457, want: "2.4KB"},
		{name: "one below the MB boundary stays in KB", in: kb*kb - 1, want: "1024.0KB"},
		{name: "exactly one MB", in: kb * kb, want: "1.0MB"},
		{name: "exactly one GB", in: kb * kb * kb, want: "1.0GB"},
		{name: "beyond the largest unit stays in GB", in: 3 * kb * kb * kb, want: "3.0GB"},
		{name: "a negative size is not a panic", in: -5, want: "-5B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, humanBytes(tc.in))
		})
	}
}

// TestHumanBytes_NeverEmitsASpaceBeforeTheUnit pins the format detail that would otherwise be
// invisible until a golden diff: the unit is glued to the number.
func TestHumanBytes_NeverEmitsASpaceBeforeTheUnit(t *testing.T) {
	for _, n := range []int64{0, 1, 1023, 1024, 2457, 1 << 20, 1 << 30} {
		require.NotContains(t, humanBytes(n), " ", "humanBytes(%d) must not contain a space", n)
	}
}

// benchTombstoneSink keeps the compiler from eliminating the call under benchmark.
var benchTombstoneSink string

// BenchmarkTombstone measures the renderer on the hot path. Qompack.md §8.1 budgets the whole of
// PostToolUse at under 15 ms p99 and this repository's B-C budget wants the marker itself under
// 2 µs; the record below is the §8.1 design example, so the measurement covers the common case of
// a path subject and the KB size tier.
//
// There is deliberately NO allocation budget here. humanBytes is frozen by SP-08's plan and
// allocates before this function writes a byte (strconv.FormatInt plus a concatenation below the
// KB boundary, fmt.Sprintf plus a boxed float64 above it), so "zero allocations beyond the
// returned string" is not reachable without rewriting a function the plan says is untouched, and
// Qompack.md sets no allocation budget for the renderer. Tombstone still pre-sizes its builder so
// its own contribution is exactly one allocation; -benchmem records the rest.
func BenchmarkTombstone(b *testing.B) {
	rec := store.ToolUseRecord{
		Root: hashShort(b, "a3f2c19d0b74"), Bytes: 2458, Tool: "FileRead", Path: "src/auth.ts",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchTombstoneSink = Tombstone(rec)
	}
}
