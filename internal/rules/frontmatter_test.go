package rules_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/rules"
	"github.com/stretchr/testify/require"
)

// requireFront compares a parsed Front against want and returns nothing; EquateEmpty is applied
// because a rule with no `paths:` key and one with `paths: []` are the same thing to every caller
// downstream — both are unscoped and both are skipped.
func requireFront(t *testing.T, want, got rules.Front) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("Front mismatch (-want +got):\n%s", diff)
	}
}

// TestParseFront_ListForm pins the block-sequence form, which is what Claude Code's own rule
// files and the rulestest fixture both use.
func TestParseFront_ListForm(t *testing.T) {
	t.Parallel()
	src := "---\npaths:\n  - \"src/**/*.ts\"\n  - api/**\ndescription: API conventions for the REST layer\n---\nbody text\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{
		Paths:       []string{"src/**/*.ts", "api/**"},
		Description: "API conventions for the REST layer",
		Present:     true,
	}, got)
	require.Equal(t, "body text\n", src[off:], "the offset must point just past the closing ---")
}

// TestParseFront_InlineArray pins the flow-sequence form.
func TestParseFront_InlineArray(t *testing.T) {
	t.Parallel()
	src := "---\npaths: [\"src/**/*.ts\", 'api/**', \"\"]\n---\nbody\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{
		Paths:   []string{"src/**/*.ts", "api/**"},
		Present: true,
	}, got)
	require.Equal(t, "body\n", src[off:])
}

// TestParseFront_ScalarForm pins the single-value form: an unquoted scalar is one glob, not a
// character-by-character list.
func TestParseFront_ScalarForm(t *testing.T) {
	t.Parallel()
	src := "---\npaths: src/**/*.ts\n---\nbody\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{Paths: []string{"src/**/*.ts"}, Present: true}, got)
	require.Equal(t, "body\n", src[off:])
}

// TestParseFront_CRLF pins that a Windows-authored rule file parses identically: the \r must not
// end up glued to the last glob, where it would silently never match anything.
func TestParseFront_CRLF(t *testing.T) {
	t.Parallel()
	src := "---\r\npaths:\r\n  - \"src/**/*.ts\"\r\n  - api/**\r\ndescription: API conventions\r\n---\r\nbody\r\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{
		Paths:       []string{"src/**/*.ts", "api/**"},
		Description: "API conventions",
		Present:     true,
	}, got)
	require.Equal(t, "body\r\n", src[off:])
	for _, p := range got.Paths {
		require.NotContains(t, p, "\r", "a carriage return survived into a glob")
	}
}

// TestParseFront_Unclosed pins the malformed case: an opened-but-never-closed block is not
// frontmatter at all, so the file is an unscoped rule the host re-injects itself and Parse must
// report Present=false with offset 0 rather than guessing where the body starts.
func TestParseFront_Unclosed(t *testing.T) {
	t.Parallel()
	src := "---\npaths:\n  - src/**\nstill inside, no terminator ever arrives\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{}, got)
	require.False(t, got.Present)
	require.Equal(t, 0, off)
}

// TestParseFront_NoFrontmatter pins the ordinary-markdown case, including the near-misses that
// must not be mistaken for an opener.
func TestParseFront_NoFrontmatter(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"# Just a heading\n\nSome prose.\n",
		"",
		"---",
		"----\npaths: src/**\n---\n",
		" ---\npaths: src/**\n---\n",
	} {
		got, off := rules.Parse([]byte(src))
		require.False(t, got.Present, "unexpectedly parsed frontmatter out of %q", src)
		require.Equal(t, 0, off)
		require.Empty(t, got.Paths)
	}
}

// TestParseFront_UnknownKeysIgnored pins forward compatibility: a key Qompack does not model is
// dropped, not treated as an error, so a rule file written for a newer Claude Code still scopes
// correctly today.
func TestParseFront_UnknownKeysIgnored(t *testing.T) {
	t.Parallel()
	src := "---\nname: api\nallowed-tools: [Read, Grep]\npaths:\n  - src/api/**\nmodel: opus\n---\nbody\n"

	got, off := rules.Parse([]byte(src))

	requireFront(t, rules.Front{Paths: []string{"src/api/**"}, Present: true}, got)
	require.Equal(t, "body\n", src[off:])
}

// TestParseFront_OversizeFrontmatter pins the 8 KiB scan ceiling: a closing --- past it is not
// found, so the file degrades to unscoped rather than making the scanner read an unbounded
// prefix looking for a terminator that may never come.
func TestParseFront_OversizeFrontmatter(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("---\npaths:\n  - src/api/**\n")
	b.WriteString(strings.Repeat("filler: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n", 400))
	b.WriteString("---\nbody\n")
	require.Greater(t, b.Len(), 8192, "the fixture must actually exceed the ceiling")

	got, off := rules.Parse([]byte(b.String()))

	require.False(t, got.Present)
	require.Equal(t, 0, off)
	require.Empty(t, got.Paths)
}
