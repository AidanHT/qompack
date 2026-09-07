package skills_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/skills"
	"github.com/stretchr/testify/require"
)

// fixtureRoot returns the absolute path of the committed proj-a fixture project, whose
// .claude/skills/ tree carries the canonical discovery shapes: two SKILL.md directories and one
// flat <name>.md.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "rules", "proj-a"))
	require.NoError(t, err)
	require.DirExists(t, filepath.Join(root, ".claude", "skills"))
	return root
}

// lineCost recomputes an Entry's rendered index-line cost from the contract documented on
// skills.Indexer. It is deliberately duplicated here: the test pins the formula rather than
// trusting whatever the implementation happens to compute.
func lineCost(e skills.Entry) core.Tokens {
	line := "- " + e.Name + ": " + e.Description + "\n"
	return core.Tokens((len(line) + 3) / 4)
}

// totalCost sums lineCost over entries.
func totalCost(entries []skills.Entry) core.Tokens {
	var sum core.Tokens
	for _, e := range entries {
		sum += lineCost(e)
	}
	return sum
}

// writeDirSkill writes root/.claude/skills/<name>/SKILL.md with the given content.
func writeDirSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills", name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600))
}

// writeFlatSkill writes root/.claude/skills/<name>.md with the given content.
func writeFlatSkill(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600))
}

// indexAll indexes root at budget 0, which the Indexer contract defines as "return every entry".
func indexAll(t *testing.T, root string) ([]skills.Entry, core.Tokens) {
	t.Helper()
	entries, used, err := skills.New(skills.WithLogger(logging.Nop())).Index(context.Background(), root, 0)
	require.NoError(t, err)
	return entries, used
}

func TestIndex_DirAndFlatForms(t *testing.T) {
	entries, used, err := skills.New().Index(context.Background(), fixtureRoot(t), 0)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	names := make([]string, 0, len(entries))
	sources := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
		sources = append(sources, e.Source)
	}
	require.Equal(t, []string{"code-review", "migration-runner", "quick-fmt"}, names)
	require.Equal(t, []string{
		".claude/skills/code-review/SKILL.md",
		".claude/skills/migration-runner/SKILL.md",
		".claude/skills/quick-fmt.md",
	}, sources)
	require.Equal(t, totalCost(entries), used)

	require.Equal(t, "Review the current diff for correctness bugs.", entries[0].Description)
	for _, e := range entries {
		require.NotEmpty(t, e.Description)
	}
}

func TestIndex_NameFromFrontmatterElseBasename(t *testing.T) {
	t.Run("falls back to the directory name", func(t *testing.T) {
		root := fixtureRoot(t)
		entries, _ := indexAll(t, root)
		var found bool
		for _, e := range entries {
			if e.Source == ".claude/skills/migration-runner/SKILL.md" {
				found = true
				require.Equal(t, "migration-runner", e.Name)
			}
		}
		require.True(t, found, "migration-runner fixture missing")

		raw, err := os.ReadFile(filepath.Join(root, ".claude", "skills", "migration-runner", "SKILL.md"))
		require.NoError(t, err)
		require.NotContains(t, string(raw), "\nname:",
			"the fixture must carry no name: key, or the fallback is not exercised")
	})

	t.Run("frontmatter name wins over the basename", func(t *testing.T) {
		root := t.TempDir()
		writeDirSkill(t, root, "on-disk-dir",
			"---\nname: from-frontmatter\ndescription: Named by frontmatter.\n---\nBody.\n")
		writeFlatSkill(t, root, "on-disk-flat",
			"---\nname: flat-frontmatter\ndescription: Also named by frontmatter.\n---\nBody.\n")
		entries, _ := indexAll(t, root)
		require.Len(t, entries, 2)
		require.Equal(t, "flat-frontmatter", entries[0].Name)
		require.Equal(t, ".claude/skills/on-disk-flat.md", entries[0].Source)
		require.Equal(t, "from-frontmatter", entries[1].Name)
		require.Equal(t, ".claude/skills/on-disk-dir/SKILL.md", entries[1].Source)
	})

	t.Run("basename strips .md on the flat form", func(t *testing.T) {
		root := t.TempDir()
		writeFlatSkill(t, root, "bare-flat", "No frontmatter at all, just a body line.\n")
		entries, _ := indexAll(t, root)
		require.Len(t, entries, 1)
		require.Equal(t, "bare-flat", entries[0].Name)
	})
}

func TestIndex_DescriptionFallsBackToFirstBodyLine(t *testing.T) {
	const longRunes = 150
	long := strings.Repeat("abcdefghij", longRunes/10)
	require.Len(t, long, longRunes)

	root := t.TempDir()
	writeDirSkill(t, root, "no-desc",
		"---\nname: no-desc\n---\n\n# Heading that must be skipped\n\n"+long+"\ntrailing line\n")

	entries, _ := indexAll(t, root)
	require.Len(t, entries, 1)
	require.Equal(t, string([]rune(long)[:100])+"…", entries[0].Description)
	require.Equal(t, 101, utf8.RuneCountInString(entries[0].Description))

	t.Run("truncation counts runes, not bytes", func(t *testing.T) {
		multi := strings.Repeat("éüöåø", longRunes/5)
		require.Equal(t, longRunes, utf8.RuneCountInString(multi))
		sub := t.TempDir()
		writeDirSkill(t, sub, "wide", "---\nname: wide\ndescription: "+multi+"\n---\nBody.\n")
		got, _ := indexAll(t, sub)
		require.Len(t, got, 1)
		require.Equal(t, string([]rune(multi)[:100])+"…", got[0].Description)
		require.Equal(t, 101, utf8.RuneCountInString(got[0].Description))
	})
}

func TestIndex_DescriptionEmpty(t *testing.T) {
	root := t.TempDir()
	writeDirSkill(t, root, "empty-body", "---\nname: empty-body\n---\n")
	writeDirSkill(t, root, "headings-only", "---\nname: headings-only\n---\n# Only\n\n## Headings\n")
	writeFlatSkill(t, root, "totally-empty", "")

	entries, _ := indexAll(t, root)
	require.Len(t, entries, 3)
	for _, e := range entries {
		require.Equal(t, "(no description)", e.Description, "entry %q", e.Name)
	}
}

func TestIndex_BudgetPrefixTruncation(t *testing.T) {
	root := fixtureRoot(t)
	all, _ := indexAll(t, root)
	require.Len(t, all, 3)

	first, second, third := all[0], all[1], all[2]
	require.Equal(t, "code-review", first.Name)
	require.Greater(t, lineCost(first), lineCost(third),
		"the fixture must make the first entry costlier than the cheapest, or the test proves nothing")

	ix := skills.New(skills.WithLogger(logging.Nop()))

	t.Run("budget of exactly the first entry keeps only it", func(t *testing.T) {
		entries, used, err := ix.Index(context.Background(), root, lineCost(first))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		require.Equal(t, first, entries[0])
		require.Equal(t, lineCost(first), used)
		require.NotEqual(t, third.Name, entries[0].Name,
			"a cheapest-first filler would have returned the cheapest entry instead")
	})

	t.Run("stops at the first entry that does not fit", func(t *testing.T) {
		budget := lineCost(first) + lineCost(third)
		require.Less(t, budget, lineCost(first)+lineCost(second),
			"the budget must fit the cheapest entry but not the second sorted one")

		entries, used, err := ix.Index(context.Background(), root, budget)
		require.NoError(t, err)
		require.Len(t, entries, 1, "prefix truncation must not skip ahead to a smaller entry")
		require.Equal(t, first, entries[0])
		require.Equal(t, lineCost(first), used)
	})

	t.Run("a budget below the first entry keeps nothing", func(t *testing.T) {
		entries, used, err := ix.Index(context.Background(), root, lineCost(first)-1)
		require.NoError(t, err)
		require.Empty(t, entries)
		require.Equal(t, core.Tokens(0), used)
	})
}

func TestIndex_ZeroBudgetReturnsAll(t *testing.T) {
	root := fixtureRoot(t)
	entries, used := indexAll(t, root)
	require.Len(t, entries, 3)
	require.Equal(t, totalCost(entries), used)
	require.Greater(t, used, core.Tokens(0))

	negative, negUsed, err := skills.New().Index(context.Background(), root, -1)
	require.NoError(t, err)
	require.Equal(t, entries, negative, "a non-positive budget means 'no budget', not 'zero budget'")
	require.Equal(t, used, negUsed)
}

func TestIndex_Deterministic(t *testing.T) {
	root := fixtureRoot(t)
	ix := skills.New(skills.WithLogger(logging.Nop()))

	want, wantUsed, err := ix.Index(context.Background(), root, 0)
	require.NoError(t, err)
	for i := range 10 {
		got, used, err := ix.Index(context.Background(), root, 0)
		require.NoError(t, err)
		require.Equal(t, wantUsed, used, "run %d", i)
		if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("run %d: Index is not deterministic (-first +run):\n%s", i, diff)
		}
	}
}

func TestIndex_SkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeDirSkill(t, root, "real", "---\nname: real\ndescription: A real skill.\n---\nBody.\n")

	// A skip here must carry devtool lint's mandatory "platform: " prefix followed by a reason
	// (tools/devtool/stubskips.go classifySkips): an unprivileged Windows process cannot create a
	// symlink at all, and a skip reason matching none of the three permitted messages is a hard
	// lint failure rather than a tolerated gap.
	skillsDir := filepath.Join(root, ".claude", "skills")
	if err := os.Symlink(filepath.Join(skillsDir, "real"), filepath.Join(skillsDir, "linked")); err != nil {
		t.Skipf("platform: this process may not create symlinks: %v", err)
	}
	outside := filepath.Join(root, "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("---\nname: outside\n---\nBody.\n"), 0o600))
	if err := os.Symlink(outside, filepath.Join(skillsDir, "linked-flat.md")); err != nil {
		t.Skipf("platform: this process may not create symlinks: %v", err)
	}

	entries, _ := indexAll(t, root)
	require.Len(t, entries, 1)
	require.Equal(t, "real", entries[0].Name)
	require.Equal(t, ".claude/skills/real/SKILL.md", entries[0].Source)
}

func TestIndex_SkipsOversize(t *testing.T) {
	root := t.TempDir()
	writeDirSkill(t, root, "small", "---\nname: small\ndescription: Fits fine.\n---\nBody.\n")

	const twoMiB = 2 * 1024 * 1024
	huge := "---\nname: huge\ndescription: Far too large to index.\n---\n" + strings.Repeat("x", twoMiB)
	writeDirSkill(t, root, "huge", huge)
	writeFlatSkill(t, root, "huge-flat", huge)

	entries, _ := indexAll(t, root)
	require.Len(t, entries, 1)
	require.Equal(t, "small", entries[0].Name)
}

func TestBodyTokens(t *testing.T) {
	root := fixtureRoot(t)
	entries, _ := indexAll(t, root)

	var migration skills.Entry
	for _, e := range entries {
		if e.Name == "migration-runner" {
			migration = e
		}
	}
	require.NotEmpty(t, migration.Source)

	got, err := skills.BodyTokens(root, migration)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got, core.Tokens(7000))
	require.LessOrEqual(t, got, core.Tokens(8000))
	t.Logf("BodyTokens(migration-runner) = %d", got)
}

func TestBodyTokens_StripsFrontmatter(t *testing.T) {
	root := fixtureRoot(t)
	entries, _ := indexAll(t, root)
	require.NotEmpty(t, entries)

	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Source)))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(raw), "---\n"), "%s: fixture must open with frontmatter", e.Source)

		_, body, ok := strings.Cut(string(raw)[len("---\n"):], "\n---\n")
		require.True(t, ok, "%s: fixture frontmatter must be closed", e.Source)

		got, err := skills.BodyTokens(root, e)
		require.NoError(t, err)
		require.Equal(t, core.Tokens((len(body)+3)/4), got, "%s", e.Source)
		require.Less(t, got, core.Tokens((len(raw)+3)/4), "%s: frontmatter must not be counted", e.Source)
	}
}

func TestBodyTokens_MissingFile(t *testing.T) {
	_, err := skills.BodyTokens(t.TempDir(), skills.Entry{
		Name:   "gone",
		Source: ".claude/skills/gone/SKILL.md",
	})
	require.Error(t, err)
}

func BenchmarkIndex(b *testing.B) {
	const synthetic = 60
	root := b.TempDir()
	dir := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	for i := range synthetic {
		name := fmt.Sprintf("skill-%02d", i)
		content := fmt.Sprintf(
			"---\nname: %s\ndescription: Synthetic skill %d used only by BenchmarkIndex.\n---\n%s",
			name, i, strings.Repeat("Body line for the synthetic benchmark skill.\n", 20))
		if i%2 == 0 {
			sub := filepath.Join(dir, name)
			if err := os.MkdirAll(sub, 0o700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(content), 0o600); err != nil {
				b.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600); err != nil {
			b.Fatal(err)
		}
	}

	ix := skills.New()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		entries, _, err := ix.Index(ctx, root, 0)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != synthetic {
			b.Fatalf("got %d entries, want %d", len(entries), synthetic)
		}
	}
}
