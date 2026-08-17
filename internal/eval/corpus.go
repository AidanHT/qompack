package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// corpusFilePerm is the mode every committed corpus file is written with.
const corpusFilePerm = 0o600

// CorpusManifest is testdata/sessions/synthetic/CORPUS.json: what the corpus is, and what it was
// generated from.
//
// RegeneratedAfterPhase is the §11.4 overfitting guard made mechanical. Logged sessions were
// produced by an agent operating under the CURRENT system, so a corpus that stays fixed while the
// system changes measures a world that no longer exists. The gate compares this number against
// the phase being asserted and fails when the corpus has fallen more than two phases behind.
type CorpusManifest struct {
	// Generator identifies the code that produced these sessions.
	Generator string `json:"generator"`
	// RegeneratedAfterPhase is the highest phase whose landing this corpus was regenerated after.
	RegeneratedAfterPhase int `json:"regeneratedAfterPhase"`
	// Sessions is one entry per committed file.
	Sessions []CorpusEntry `json:"sessions"`
}

// CorpusEntry describes one committed session.
type CorpusEntry struct {
	// File is the basename under testdata/sessions/synthetic/.
	File string `json:"file"`
	// Shape names the failure mode this session spans.
	Shape string `json:"shape"`
	// Seed is the generator seed that reproduces it.
	Seed int64 `json:"seed"`
	// SHA256 is the hex digest of the file's LF-normalized bytes.
	SHA256 string `json:"sha256"`
	// Turns is how many turns it holds.
	Turns int `json:"turns"`
	// CompactionAt lists its compaction turns.
	CompactionAt []core.TurnIndex `json:"compactionAt"`
}

// CorpusSpecs is the committed corpus: eight shapes spanning the failure space §6.3 names, three
// seeds each.
//
// 24 sessions is above the eval.minSessions default of 20, so the shipped configuration is
// satisfiable entirely offline — Phase 0 is reproducible on a machine that has never recorded a
// real session.
//
// Several rows below carry //nomagic:allow. Every number in this table is a synthetic-fixture
// shape parameter — a tool weight, a re-read rate, a noise level — and a few of them collide
// numerically with values that ARE Qompack tunables elsewhere. D11 exists so a cache multiplier is
// never written as a literal; it does not mean a corpus can never contain the number 0.4.
func CorpusSpecs() []NamedSpec {
	shapes := []struct {
		file    string
		shape   string
		seeds   [3]int64
		turns   int
		mix     map[string]float64
		reread  float64
		noise   float64
		cps     int
		elims   int
		subs    int
		depAt   []core.TurnIndex
		compact []core.TurnIndex
	}{
		{
			file: "read-heavy", shape: "read-heavy", seeds: [3]int64{1001, 1002, 1003}, turns: 180,
			mix:    map[string]float64{"FileRead": 0.60, "Grep": 0.20, "Edit": 0.12, "Bash": 0.08},
			reread: 0.45, noise: 0.0, cps: 3, elims: 2, subs: 0,
			compact: []core.TurnIndex{90},
		},
		{
			file: "test-output", shape: "test-output-heavy", seeds: [3]int64{1011, 1012, 1013}, turns: 200,
			mix:    map[string]float64{"Test": 0.45, "Bash": 0.25, "Edit": 0.18, "FileRead": 0.12},
			reread: 0.20, noise: 0.8, cps: 4, elims: 3, subs: 0,
			compact: []core.TurnIndex{100},
		},
		{
			file: "refactor", shape: "refactor-across-files", seeds: [3]int64{1021, 1022, 1023}, turns: 240,
			mix:    map[string]float64{"Edit": 0.40, "FileRead": 0.30, "Grep": 0.18, "Test": 0.12}, //nomagic:allow synthetic tool-mix weights
			reread: 0.35, noise: 0.2, cps: 5, elims: 3, subs: 0,
			compact: []core.TurnIndex{80, 160},
		},
		{
			file: "long-idle", shape: "long-idle-gap", seeds: [3]int64{1031, 1032, 1033}, turns: 160,
			mix:    map[string]float64{"FileRead": 0.35, "Bash": 0.30, "Edit": 0.20, "Grep": 0.15},
			reread: 0.30, noise: 0.1, cps: 8, elims: 2, subs: 0, //nomagic:allow synthetic test-output noise level
			compact: []core.TurnIndex{100},
		},
		{
			file: "dep-change", shape: "dependency-change", seeds: [3]int64{1041, 1042, 1043}, turns: 220,
			mix:    map[string]float64{"FileRead": 0.35, "Test": 0.25, "Edit": 0.25, "Bash": 0.15},
			reread: 0.30, noise: 0.4, cps: 4, elims: 5, subs: 0, //nomagic:allow synthetic test-output noise level
			depAt: []core.TurnIndex{60, 140}, compact: []core.TurnIndex{90, 170},
		},
		{
			file: "subagent", shape: "subagent-heavy", seeds: [3]int64{1051, 1052, 1053}, turns: 190,
			mix:    map[string]float64{"Task": 0.30, "FileRead": 0.30, "Edit": 0.22, "Bash": 0.18},
			reread: 0.25, noise: 0.1, cps: 3, elims: 2, subs: 8, //nomagic:allow synthetic test-output noise level
			compact: []core.TurnIndex{95},
		},
		{
			file: "thrash", shape: "thrash-loop", seeds: [3]int64{1061, 1062, 1063}, turns: 210,
			mix:    map[string]float64{"FileRead": 0.28, "Edit": 0.28, "Test": 0.28, "Bash": 0.16},
			reread: 0.65, noise: 0.5, cps: 2, elims: 4, subs: 0,
			compact: []core.TurnIndex{105},
		},
		{
			file: "multi-compact", shape: "multi-compaction", seeds: [3]int64{1071, 1072, 1073}, turns: 320,
			mix: map[string]float64{
				"FileRead": 0.32, "Edit": 0.26, "Test": 0.22, "Bash": 0.12, "Grep": 0.08,
			},
			reread: 0.40, noise: 0.3, cps: 6, elims: 6, subs: 3, //nomagic:allow synthetic re-read rate
			depAt: []core.TurnIndex{200}, compact: []core.TurnIndex{70, 140, 210, 280},
		},
	}

	out := make([]NamedSpec, 0, len(shapes)*3)
	for _, sh := range shapes {
		for i, seed := range sh.seeds {
			mix := make(map[string]float64, len(sh.mix))
			for k, v := range sh.mix {
				mix[k] = v
			}
			out = append(out, NamedSpec{
				File:  fmt.Sprintf("%s-%d.json", sh.file, i+1),
				Shape: sh.shape,
				Seed:  seed,
				Spec: SynthSpec{
					Turns:              sh.turns,
					ToolMix:            mix,
					FileRereadRate:     sh.reread,
					TestOutputNoise:    sh.noise,
					Changepoints:       sh.cps,
					Eliminations:       sh.elims,
					SubagentCalls:      sh.subs,
					DependencyChangeAt: append([]core.TurnIndex(nil), sh.depAt...),
					CompactionAt:       append([]core.TurnIndex(nil), sh.compact...),
				},
			})
		}
	}
	return out
}

// WriteCorpus regenerates every committed session plus CORPUS.json.
//
// It is the SINGLE writer of the committed corpus: the env-guarded writer test and the driver's
// --regen-corpus flag both call this, so the bytes on disk have exactly one producer and the
// golden stability test compares like with like.
func WriteCorpus(dir string) error {
	manifest := CorpusManifest{Generator: synthGeneratorID}

	for _, n := range CorpusSpecs() {
		s := SynthesizeNamed(n)
		body, err := EncodeSession(s)
		if err != nil {
			return err
		}
		if err := paths.WriteAtomic(filepath.Join(dir, n.File), body, corpusFilePerm); err != nil {
			return fmt.Errorf("eval: writing %s: %w", n.File, err)
		}
		sum := sha256.Sum256(body)
		manifest.Sessions = append(manifest.Sessions, CorpusEntry{
			File:         n.File,
			Shape:        n.Shape,
			Seed:         n.Seed,
			SHA256:       hex.EncodeToString(sum[:]),
			Turns:        len(s.Turns),
			CompactionAt: s.CompactionAt,
		})
	}

	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: encoding the corpus manifest: %w", err)
	}
	body = append(body, '\n')
	if err := paths.WriteAtomic(filepath.Join(dir, corpusManifestName), body, corpusFilePerm); err != nil {
		return fmt.Errorf("eval: writing %s: %w", corpusManifestName, err)
	}
	return nil
}
