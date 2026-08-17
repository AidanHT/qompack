package eval_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/stretchr/testify/require"
)

// corpusDir is where the committed synthetic corpus lives.
func corpusDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "testdata", "sessions", "synthetic")
}

// specByShape finds one corpus row by shape name.
func specByShape(t *testing.T, shape string) eval.NamedSpec {
	t.Helper()
	for _, n := range eval.CorpusSpecs() {
		if n.Shape == shape {
			return n
		}
	}
	t.Fatalf("no corpus row with shape %q", shape)
	return eval.NamedSpec{}
}

// toolCounts tallies tool-call names across a session.
func toolCounts(s eval.Session) (map[string]int, int) {
	counts := map[string]int{}
	total := 0
	for _, turn := range s.Turns {
		for _, tc := range turn.ToolCalls {
			counts[tc.Name]++
			total++
		}
	}
	return counts, total
}

// TestSynthesize_ByteIdenticalForSeed is the property the whole corpus rests on: without it,
// replay numbers are not comparable across commits.
func TestSynthesize_ByteIdenticalForSeed(t *testing.T) {
	spec := specByShape(t, "read-heavy")

	first, err := json.Marshal(eval.Synthesize(spec.Seed, spec.Spec))
	require.NoError(t, err)
	second, err := json.Marshal(eval.Synthesize(spec.Seed, spec.Spec))
	require.NoError(t, err)

	require.Equal(t, string(first), string(second))
}

// TestSynthesize_DifferentSeedsDifferentSessions guards against a generator that ignores its seed,
// which would make a 24-session corpus three sessions wearing eight hats.
func TestSynthesize_DifferentSeedsDifferentSessions(t *testing.T) {
	spec := specByShape(t, "read-heavy")

	a, err := json.Marshal(eval.Synthesize(spec.Seed, spec.Spec))
	require.NoError(t, err)
	b, err := json.Marshal(eval.Synthesize(spec.Seed+1, spec.Spec))
	require.NoError(t, err)

	require.NotEqual(t, string(a), string(b))
}

// TestSynthesize_ToolMixOrderIndependent: Go randomizes map iteration, so a generator that walked
// ToolMix directly would produce a different session on every process run.
func TestSynthesize_ToolMixOrderIndependent(t *testing.T) {
	spec := specByShape(t, "refactor-across-files").Spec

	reordered := spec
	reordered.ToolMix = map[string]float64{}
	keys := []string{"Test", "Grep", "FileRead", "Edit"}
	for _, k := range keys {
		if v, ok := spec.ToolMix[k]; ok {
			reordered.ToolMix[k] = v
		}
	}
	require.Len(t, reordered.ToolMix, len(spec.ToolMix))

	want, err := json.Marshal(eval.Synthesize(1021, spec))
	require.NoError(t, err)
	got, err := json.Marshal(eval.Synthesize(1021, reordered))
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))
}

// TestSynthesize_MatchesCommittedCorpus is 00-ARCHITECTURE.md §6.3's golden stability test: the
// committed bytes must be exactly what the generator produces today.
func TestSynthesize_MatchesCommittedCorpus(t *testing.T) {
	dir := corpusDir(t)
	for _, n := range eval.CorpusSpecs() {
		t.Run(n.File, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, n.File))
			require.NoError(t, err, "run devtool replay --regen-corpus to create it")

			got, err := eval.EncodeSession(eval.SynthesizeNamed(n))
			require.NoError(t, err)

			require.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), string(got))
		})
	}
}

// TestCorpus_CountAtLeastMinSessions: the corpus has to satisfy the shipped config default with no
// recorded sessions on the machine, or Phase 0 is not reproducible offline.
func TestCorpus_CountAtLeastMinSessions(t *testing.T) {
	require.GreaterOrEqual(t, len(eval.CorpusSpecs()), config.Defaults().Eval.MinSessions)
	require.Len(t, eval.CorpusSpecs(), 24)
}

// TestCorpus_SeedsAndFilesAreUnique: a duplicated seed would silently make two corpus rows the
// same session.
func TestCorpus_SeedsAndFilesAreUnique(t *testing.T) {
	seeds, files := map[int64]bool{}, map[string]bool{}
	for _, n := range eval.CorpusSpecs() {
		require.False(t, seeds[n.Seed], "duplicate seed %d", n.Seed)
		require.False(t, files[n.File], "duplicate file %s", n.File)
		seeds[n.Seed], files[n.File] = true, true
	}
	require.Len(t, seeds, 24)
}

// TestCorpus_ManifestHashesMatch: the manifest is what the gate hashes to detect a corpus that
// drifted from the generator.
func TestCorpus_ManifestHashesMatch(t *testing.T) {
	dir := corpusDir(t)
	raw, err := os.ReadFile(filepath.Join(dir, "CORPUS.json"))
	require.NoError(t, err)

	var m eval.CorpusManifest
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m.Sessions, 24)
	require.Equal(t, 0, m.RegeneratedAfterPhase)

	for _, entry := range m.Sessions {
		body, err := os.ReadFile(filepath.Join(dir, entry.File))
		require.NoError(t, err)
		sum := sha256.Sum256([]byte(strings.ReplaceAll(string(body), "\r\n", "\n")))
		require.Equal(t, entry.SHA256, hex.EncodeToString(sum[:]), entry.File)
	}
}

// TestSynthesize_ShapeInvariants: the corpus exists to span the failure space, so each shape has
// to actually exhibit the failure it is named for.
func TestSynthesize_ShapeInvariants(t *testing.T) {
	t.Run("read-heavy is dominated by reads", func(t *testing.T) {
		s := eval.SynthesizeNamed(specByShape(t, "read-heavy"))
		counts, total := toolCounts(s)
		require.Greater(t, float64(counts["FileRead"])/float64(total), 0.55)
	})

	t.Run("test-output-heavy is dominated by tests", func(t *testing.T) {
		s := eval.SynthesizeNamed(specByShape(t, "test-output-heavy"))
		counts, total := toolCounts(s)
		require.Greater(t, float64(counts["Test"])/float64(total), 0.40)
	})

	t.Run("thrash-loop repeats the FileRead,Edit,Test trigram", func(t *testing.T) {
		s := eval.SynthesizeNamed(specByShape(t, "thrash-loop"))
		var seq []string
		for _, turn := range s.Turns {
			for _, tc := range turn.ToolCalls {
				seq = append(seq, tc.Name)
			}
		}
		n := 0
		for i := 0; i+2 < len(seq); i++ {
			if seq[i] == "FileRead" && seq[i+1] == "Edit" && seq[i+2] == "Test" {
				n++
			}
		}
		require.GreaterOrEqual(t, n, 20, "SP-15's Sequitur detector needs a loop to find")
	})

	t.Run("subagent-heavy reports every subagent result back", func(t *testing.T) {
		spec := specByShape(t, "subagent-heavy")
		s := eval.SynthesizeNamed(spec)

		taskIDs := map[string]bool{}
		for _, turn := range s.Turns {
			for _, tc := range turn.ToolCalls {
				if tc.Name == "Task" {
					taskIDs[string(tc.ID)] = true
				}
			}
		}
		// The invariant that matters is not how many Task calls the mix happened to draw, but how
		// many of them a later turn quotes by tool_use_id — that quoting is what makes
		// DemandToolResult fire, and it is what SubagentCalls actually controls.
		quoted := 0
		for _, turn := range s.Turns {
			for id := range taskIDs {
				if strings.Contains(turn.Text, id) {
					quoted++
				}
			}
		}
		require.Equal(t, spec.Spec.SubagentCalls, quoted)
	})

	t.Run("dependency-change writes at every named turn", func(t *testing.T) {
		spec := specByShape(t, "dependency-change")
		s := eval.SynthesizeNamed(spec)
		for _, at := range spec.Spec.DependencyChangeAt {
			found := false
			for _, tc := range s.Turns[at].ToolCalls {
				if tc.Name == "Write" && len(tc.Paths) > 0 {
					found = true
				}
			}
			require.True(t, found, "no dependency write at turn %d", at)
		}
	})

	t.Run("multi-compaction compacts four times", func(t *testing.T) {
		s := eval.SynthesizeNamed(specByShape(t, "multi-compaction"))
		require.Len(t, s.CompactionAt, 4)
	})

	t.Run("long-idle-gap has one long gap per changepoint and none up front", func(t *testing.T) {
		spec := specByShape(t, "long-idle-gap")
		s := eval.SynthesizeNamed(spec)

		long := 0
		for i := 1; i < len(s.Turns); i++ {
			if s.Turns[i].TS-s.Turns[i-1].TS >= 3_600_000 {
				long++
				require.Greater(t, i, 3, "§5.4's cold-cache window belongs at a task boundary, not at startup")
			}
		}
		require.Equal(t, spec.Spec.Changepoints, long)
	})
}

// TestSynthesize_EveryCompactionHasDemands is what makes the corpus measure anything at all.
//
// Demands that no affordable block can satisfy give Σo = 0, which scores EVERY policy 1.0 and
// hides all signal — so both halves are asserted: that something was demanded, and that OPT could
// actually satisfy some of it under the real budget.
func TestSynthesize_EveryCompactionHasDemands(t *testing.T) {
	for _, n := range eval.CorpusSpecs() {
		t.Run(n.File, func(t *testing.T) {
			s := eval.SynthesizeNamed(n)
			require.NotEmpty(t, s.CompactionAt)
			for _, at := range s.CompactionAt {
				to := min(at+core.TurnIndex(eval.DefaultHorizonK), core.TurnIndex(len(s.Turns)))
				require.NotEmpty(t, eval.Demands(s, at, to), "no demands after the compaction at %d", at)

				_, detail, err := eval.BeladyDetail(context.Background(), s, at,
					eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
				require.NoError(t, err)
				require.Positive(t, detail.Value, "OPT satisfies nothing at turn %d", at)
				require.True(t, detail.Exact, "the corpus must stay inside the exact solver")
			}
		})
	}
}

// TestSynthesize_StockBeatsNullOnEverySession is the Definition-of-Done shape check: if stock ever
// ties the floor, the corpus is wrong and the corpus gets fixed, never the assertion.
func TestSynthesize_StockBeatsNullOnEverySession(t *testing.T) {
	cfg := config.Defaults()
	ctx := context.Background()

	for _, n := range eval.CorpusSpecs() {
		t.Run(n.File, func(t *testing.T) {
			s := eval.SynthesizeNamed(n)
			h := eval.New(eval.Options{Cfg: cfg})

			opt := map[core.TurnIndex]eval.KeepSet{}
			for _, at := range s.CompactionAt {
				ks, _, err := eval.BeladyDetail(ctx, s, at, eval.DefaultKeepBudget, eval.DefaultBeladyOptions())
				require.NoError(t, err)
				opt[at] = ks
			}

			score := func(name string) float64 {
				p, ok := eval.PolicyByName(name, cfg)
				require.True(t, ok)
				run, err := h.Replay(ctx, s, p, deterministicOptions())
				require.NoError(t, err)
				return h.ScoreRun(run, opt).FractionOfOPT
			}

			require.Equal(t, 1.0, score("oracle"), "the ceiling must be exactly 1.0")
			require.Equal(t, 0.0, score("null"), "the floor must be exactly 0.0")
			require.Greater(t, score("stock"), 0.0, "stock must beat the floor")
		})
	}
}

// TestSynthesize_MetaCarriesProvenance: the changepoint turns have to be recoverable from the
// committed file without re-running the generator.
func TestSynthesize_MetaCarriesProvenance(t *testing.T) {
	n := specByShape(t, "long-idle-gap")
	s := eval.SynthesizeNamed(n)

	require.True(t, s.Synthetic)
	require.Equal(t, "eval.Synthesize/1", s.Meta["generator"])
	require.Equal(t, "1031", s.Meta["seed"])
	require.Equal(t, n.Shape, s.Meta["shape"])
	require.Len(t, strings.Split(s.Meta["changepoints"], ","), n.Spec.Changepoints)
	require.NotEmpty(t, s.Meta["spec"])
}

// TestSynthesize_IDIsShapeQualified: Synthesize alone cannot know the shape, because SynthSpec has
// no field for it, so the naming lives on SP-02's NamedSpec and SynthesizeNamed is the single
// path by which a corpus session is produced.
func TestSynthesize_IDIsShapeQualified(t *testing.T) {
	n := specByShape(t, "refactor-across-files")

	require.Equal(t, "synth-1021", eval.Synthesize(n.Seed, n.Spec).ID)
	require.Equal(t, "refactor-across-files-1021", eval.SynthesizeNamed(n).ID)
	require.NotContains(t, eval.Synthesize(n.Seed, n.Spec).Meta, "shape")
}

// TestSynthesize_WriteCorpus is the single writer of the committed corpus, and the same function
// --regen-corpus calls. It no-ops without the environment variable so CI can never rewrite the
// corpus it is measuring against.
func TestSynthesize_WriteCorpus(t *testing.T) {
	if os.Getenv("QOMPACK_EVAL_WRITE_CORPUS") != "1" {
		t.Skip("platform: set QOMPACK_EVAL_WRITE_CORPUS=1 to regenerate the committed corpus")
	}
	require.NoError(t, eval.WriteCorpus(corpusDir(t)))
}

// BenchmarkSynthesize_320Turns is budget E-3: <= 50 ms/op.
func BenchmarkSynthesize_320Turns(b *testing.B) {
	var spec eval.NamedSpec
	for _, n := range eval.CorpusSpecs() {
		if n.Shape == "multi-compaction" {
			spec = n
			break
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = eval.Synthesize(spec.Seed, spec.Spec)
	}
}
