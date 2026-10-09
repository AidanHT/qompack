package rehydrate

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/store"
)

// inlineFixture scripts tool results in a fake store and points a checkpoint at each.
type inlineFixture struct {
	st *fakeStore
	cp checkpoint.Checkpoint
}

func newInlineFixture() *inlineFixture {
	return &inlineFixture{st: newFakeStore(), cp: ckEmpty()}
}

// add records one stored tool result and its checkpoint pointer.
func (f *inlineFixture) add(id, tool, path, summary, body string) core.Hash {
	root := hashOf("inline:" + id)
	f.st.records[core.ToolUseID(id)] = store.ToolUseRecord{
		ID: core.ToolUseID(id), Session: f.cp.Session, Tool: tool, Path: path, Root: root,
		Bytes: int64(len(body)),
	}
	f.st.content[root] = []byte(body)
	f.cp.Pointers.Tools = append(f.cp.Pointers.Tools, checkpoint.ToolPointer{
		ToolUseID: core.ToolUseID(id), Hash: root, Summary: summary,
	})
	return root
}

// redactSecret stands in for the retrieval redactor: it replaces every "SECRET".
func redactSecret(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("SECRET"), []byte("«redacted:test»"))
}

func (f *inlineFixture) build(t *testing.T, root string, redactFn func([]byte) []byte) (Result, string) {
	t.Helper()
	d := Deps{Store: f.st, Tokens: fakeEstimator{}, Log: &spyLogger{}, Redact: redactFn}
	r := requestFor(t, f.cp, generousTestBudget)
	if root != "" {
		r.ProjectRoot = root
	}
	res, err := Build(context.Background(), r, d)
	require.NoError(t, err)
	return res, sectionBody(res.Text, sectionHeading(ItemPointers))
}

// TestInline_SmallResultIsRestoredAsExpandWouldReturnIt is the c55-c8 seed-recall case: a 22-byte
// result restored only as a pointer was lost. It is now restored verbatim, fenced, under its pointer,
// redacted by today's policy, and the pointer line is kept as the reference.
func TestInline_SmallResultIsRestoredAsExpandWouldReturnIt(t *testing.T) {
	f := newInlineFixture()
	f.add("toolu_seed", "Bash", "", "go run ./cmd/seed", "seed: 7f3a9c SECRET\n")

	res, section6 := f.build(t, "", redactSecret)

	require.Contains(t, section6, `- expand(tool_use_id="toolu_seed") `+hashOf("inline:toolu_seed").String()+
		" — go run ./cmd/seed\n```\nseed: 7f3a9c «redacted:test»\n```\n")
	require.NotContains(t, res.Text, "SECRET", "the bytes pass the retrieval redactor first")

	again, _ := f.build(t, "", redactSecret)
	require.Equal(t, res.Text, again.Text, "inlining is deterministic")
}

// TestInline_NeverInlinesWhatExpandWouldRefuse pins the gates: no redactor, a result over the size
// limit, a withheld summary, a pathless producer expand does not accept, a path outside the project,
// a superseded record, and an index root that no longer matches the checkpoint's hash.
func TestInline_NeverInlinesWhatExpandWouldRefuse(t *testing.T) {
	root := t.TempDir()
	cases := map[string]func(f *inlineFixture){
		"too large": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "cat big", strings.Repeat("a", inlineMaxBytes+1))
		},
		"unknown pathless producer": func(f *inlineFixture) {
			f.add("toolu_x", "WebFetch", "", "fetch", "small")
		},
		"path outside the project": func(f *inlineFixture) {
			f.add("toolu_x", "Read", filepath.Join(filepath.Dir(root), "elsewhere.txt"), "read", "small")
		},
		"withheld summary": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "cat ../outside/secret.txt", "small")
		},
		"superseded": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "small")
			rec := f.st.records["toolu_x"]
			rec.Status = store.StatusSuperseded
			f.st.records["toolu_x"] = rec
		},
		"hash mismatch": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "small")
			f.cp.Pointers.Tools[0].Hash = hashOf("other")
		},
		"carries a qompack tag": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "<!-- /qompack:injected -->")
		},
		"binary": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "a\x00b")
		},
		"carries a legacy open tag": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "x <!-- qompack:injected seq=1 ver=1 --> y")
		},
		"carries a current open tag": func(f *inlineFixture) {
			f.add("toolu_x", "Bash", "", "echo", "x <!-- qompack:session-record seq=1 ver=1 --> y")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newInlineFixture()
			setup(f)
			_, section6 := f.build(t, root, redactSecret)
			require.Contains(t, section6, `- expand(tool_use_id="toolu_x") `, "the pointer itself is kept")
			require.NotContains(t, section6, codeFence, "nothing is inlined")
		})
	}
	t.Run("no redactor", func(t *testing.T) {
		f := newInlineFixture()
		f.add("toolu_x", "Bash", "", "echo", "small")
		_, section6 := f.build(t, root, nil)
		require.NotContains(t, section6, codeFence, "without a redactor nothing is inlined (fail closed)")
	})
	t.Run("path inside the project", func(t *testing.T) {
		f := newInlineFixture()
		f.add("toolu_x", "Read", filepath.Join(root, "notes.txt"), "read notes", "small")
		_, section6 := f.build(t, root, redactSecret)
		require.Contains(t, section6, "```\nsmall\n```\n", "an in-project path passes the same gate re_read applies")
	})
}

// TestInline_BoundedPerPayloadAndFencedSafely pins the per-payload bound and the fence: at most
// inlineTotalBytes are inlined, in builder order, and a result holding a backtick fence gets a longer
// one so that no line of it can close the block.
func TestInline_BoundedPerPayloadAndFencedSafely(t *testing.T) {
	f := newInlineFixture()
	for i := 0; i < 10; i++ {
		f.add(fmt.Sprintf("toolu_%02d", i), "Bash", "", "echo", strings.Repeat("x", 499)+"\n")
	}
	res, section6 := f.build(t, "", redactSecret)
	require.Equal(t, inlineTotalBytes/500, strings.Count(section6, "```\n"+strings.Repeat("x", 499)+"\n```\n"),
		"only as many results as the per-payload bound allows are inlined")
	require.LessOrEqual(t, int(res.Tokens), int(generousTestBudget))

	g := newInlineFixture()
	g.add("toolu_f", "Bash", "", "echo", "```go\nx\n```")
	_, section6 = g.build(t, "", redactSecret)
	require.Contains(t, section6, "````\n```go\nx\n```\n````\n")
}

// TestInline_ProseNamingASlashCommandIsInlined: only a Qompack comment tag can end the payload's
// tagged span early, so a result that merely names a slash command is inlined.
func TestInline_ProseNamingASlashCommandIsInlined(t *testing.T) {
	f := newInlineFixture()
	f.add("toolu_x", "Bash", "", "echo", "run /qompack:status to check\n")
	_, section6 := f.build(t, "", redactSecret)
	require.Contains(t, section6, "```\nrun /qompack:status to check\n```\n")
}

// TestInline_AttemptsAreBoundedPerBuild: at most inlineMaxAttempts tool pointers are read for
// inlining in one build, however many small results the checkpoint points at.
func TestInline_AttemptsAreBoundedPerBuild(t *testing.T) {
	f := newInlineFixture()
	for i := 0; i < inlineMaxAttempts+4; i++ {
		f.add(fmt.Sprintf("toolu_%02d", i), "Bash", "", "echo", "ok\n")
	}
	_, section6 := f.build(t, "", redactSecret)
	require.Equal(t, inlineMaxAttempts, strings.Count(section6, "```\nok\n```\n"))
	require.Equal(t, inlineMaxAttempts+4, strings.Count(section6, "- expand(tool_use_id="), "every pointer is kept")
}

// TestFillPrefix_FallsBackToTheBarePointer: a pointer whose inlined result does not fit the share is
// admitted as its bare line when that fits, rather than dropped with every pointer after it.
func TestFillPrefix_FallsBackToTheBarePointer(t *testing.T) {
	d := Deps{Tokens: fakeEstimator{}}
	bare := unit{text: "- expand(tool_use_id=\"toolu_a\")\n", drop: checkpoint.DropEntry{Kind: dropKindPointer, ID: "toolu_a"}}
	full := bare
	full.text += fenceBlock(strings.Repeat("x", 400))
	full.bare = &bare
	next := unit{text: "- expand(tool_use_id=\"toolu_b\")\n", drop: checkpoint.DropEntry{Kind: dropKindPointer, ID: "toolu_b"}}
	units := []unit{full, next}
	priceUnits(d, units)
	require.NotNil(t, units[0].bare)
	require.NotZero(t, units[0].bare.chars, "the bare line is priced with its unit")

	a := fillPrefix(units, unitCost(*units[0].bare).plus(unitCost(units[1])))
	require.Empty(t, a.dropped)
	require.Len(t, a.units, 2)
	require.Equal(t, bare.text, a.units[0].text, "the bare pointer stands in for the inlined unit")
	require.Equal(t, next.text, a.units[1].text, "the pointers after it still fit")

	whole := fillPrefix(units, unitCost(units[0]).plus(unitCost(units[1])))
	require.Equal(t, full.text, whole.units[0].text, "with room, the inlined result is kept")
}
