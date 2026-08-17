// Package dedup measures the store's chunk-level deduplication ratio over a corpus of real tool
// output, with and without the §8.1 canonicalization pass in front of the chunker.
//
// It lives under test/ rather than inside internal/ deliberately. Measuring dedup requires
// importing both chunk and canon into one package, and 00-ARCHITECTURE.md §3.2's import table
// forbids canon from importing chunk; a measurement harness that had to live inside one of them
// would have forced exactly the dependency the table exists to prevent. Nothing imports this
// package.
//
// What it produces is the half of Qompack.md §10 Phase 1's exit criterion that SP-04 owns:
// "measure with and without canonicalization — the gap on test-output-heavy sessions justifies O2
// on its own". The ≥ 4:1 overall ratio and the < 15 ms hook p99 are SP-08's and SP-05's to
// achieve; this package only supplies the comparison they cite.
package dedup

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
)

// corpusDir is the tool-output corpus, relative to the repository root.
const corpusDir = "testdata/corpora/toolout"

// reportPath is the committed measurement, relative to the repository root. SP-08 reads it when it
// argues that O2 pays for itself on test-output-heavy sessions.
const reportPath = "testdata/canon-dedup-report.json"

// metaSuffix marks the sidecar that carries the (tool, path) pair a corpus file was captured as.
const metaSuffix = ".meta.json"

// ratioDecimals is how many decimal places the report rounds its ratios to. The report is
// committed and compared byte-for-byte, so the numbers are rounded rather than written at full
// float64 precision: four places is far more resolution than any threshold here needs, and it
// keeps a diff readable.
const ratioDecimals = 4

// Meta is one corpus file's sidecar: the (tool, path) pair to pass to canon.Registry.Run.
//
// It is data rather than a convention because it selects which per-tool canonicalizers apply. A
// file of `go test` output measured as tool "Grep" would exercise none of the rules that matter
// for it, and the gap this package reports would silently understate O2.
type Meta struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
}

// GroupStat is one corpus group's measurement.
type GroupStat struct {
	Group string `json:"group"`
	Files int    `json:"files"`

	RawBytes   int64 `json:"raw_bytes"`
	CanonBytes int64 `json:"canon_bytes"`

	ChunksWithout       int   `json:"chunks_without"`
	UniqueChunksWithout int   `json:"unique_chunks_without"`
	UniqueBytesWithout  int64 `json:"unique_bytes_without"`

	ChunksWith       int   `json:"chunks_with"`
	UniqueChunksWith int   `json:"unique_chunks_with"`
	UniqueBytesWith  int64 `json:"unique_bytes_with"`

	RatioWithout float64 `json:"ratio_without"`
	RatioWith    float64 `json:"ratio_with"`
	Gain         float64 `json:"gain"`
}

// Overall is the whole-corpus roll-up.
type Overall struct {
	RatioWithout float64 `json:"ratio_without"`
	RatioWith    float64 `json:"ratio_with"`
	Gain         float64 `json:"gain"`
}

// ChunkParams is chunk.Params with the report's own JSON spelling. chunk.Params carries no tags —
// it is an in-process configuration value, not a wire type — and the report is a committed
// document SP-08 reads, so the key names are pinned here rather than inherited from a struct whose
// field names are free to change.
type ChunkParams struct {
	Min    int `json:"min"`
	Target int `json:"target"`
	Max    int `json:"max"`
}

// Report is the committed measurement document.
type Report struct {
	GeneratedBy string      `json:"generated_by"`
	ChunkParams ChunkParams `json:"chunk_params"`
	Groups      []GroupStat `json:"groups"`
	Overall     Overall     `json:"overall"`
}

// accumulator collects distinct chunk hashes and their byte totals across a whole group, which is
// where dedup actually shows up: two reads of the same file, or a test suite rerun with one new
// failure, only collapse when they are measured together.
type accumulator struct {
	seen   map[core.Hash]bool
	chunks int
	unique int
	bytes  int64
}

func newAccumulator() *accumulator { return &accumulator{seen: map[core.Hash]bool{}} }

// add folds one file's chunk list into the accumulator.
func (a *accumulator) add(chunks []chunk.Chunk) {
	for _, c := range chunks {
		a.chunks++
		if a.seen[c.Hash] {
			continue
		}
		a.seen[c.Hash] = true
		a.unique++
		a.bytes += int64(c.Len)
	}
}

// Measure walks the corpus under root and returns the with-versus-without comparison.
func Measure(root string) (Report, error) {
	cfg := config.Defaults()
	params := chunk.FromConfig(cfg)
	chunker := chunk.New(params)
	registry := canon.Default(cfg.Store.Canonicalize)
	opts := canon.OptionsFrom(cfg.Store.Canonicalize, false)

	groups, err := readGroups(filepath.Join(root, filepath.FromSlash(corpusDir)))
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		GeneratedBy: "SP-04",
		ChunkParams: ChunkParams{Min: params.Min, Target: params.Target, Max: params.Max},
	}
	var totalRaw, totalCanon, totalUniqRaw, totalUniqCanon int64

	for _, g := range groups {
		stat := GroupStat{Group: g.name, Files: len(g.files)}
		without, with := newAccumulator(), newAccumulator()

		for _, f := range g.files {
			raw, meta, rerr := readCorpusFile(f)
			if rerr != nil {
				return Report{}, rerr
			}
			res, cerr := registry.Run(meta.Tool, meta.Path, raw, opts)
			if cerr != nil {
				return Report{}, fmt.Errorf("dedup: canonicalizing %s: %w", f, cerr)
			}

			stat.RawBytes += int64(len(raw))
			stat.CanonBytes += int64(len(res.Canonical))
			without.add(chunker.Split(raw))
			with.add(chunker.Split(res.Canonical))
		}

		stat.ChunksWithout, stat.UniqueChunksWithout, stat.UniqueBytesWithout = without.chunks, without.unique, without.bytes
		stat.ChunksWith, stat.UniqueChunksWith, stat.UniqueBytesWith = with.chunks, with.unique, with.bytes
		stat.RatioWithout = ratio(stat.RawBytes, without.bytes)
		stat.RatioWith = ratio(stat.CanonBytes, with.bytes)
		stat.Gain = gain(stat.RatioWithout, stat.RatioWith)
		rep.Groups = append(rep.Groups, stat)

		totalRaw += stat.RawBytes
		totalCanon += stat.CanonBytes
		totalUniqRaw += without.bytes
		totalUniqCanon += with.bytes
	}

	rep.Overall = Overall{
		RatioWithout: ratio(totalRaw, totalUniqRaw),
		RatioWith:    ratio(totalCanon, totalUniqCanon),
	}
	rep.Overall.Gain = gain(rep.Overall.RatioWithout, rep.Overall.RatioWith)
	return rep, nil
}

// ratio is stored-bytes-in over distinct-bytes-kept, rounded for a stable committed report.
func ratio(in, unique int64) float64 {
	if unique == 0 {
		return 0
	}
	return round(float64(in) / float64(unique))
}

// gain is how much better the with-canonicalization ratio is than the without.
//
// It is computed from the ROUNDED ratios, not from the raw ones, so the committed report is
// internally consistent: a reader who divides the two published numbers gets the published gain.
func gain(without, with float64) float64 {
	if without == 0 {
		return 0
	}
	return round(with / without)
}

// round trims a ratio to ratioDecimals places.
func round(v float64) float64 {
	scale := math.Pow(10, ratioDecimals)
	return math.Round(v*scale) / scale
}

// group is one corpus subdirectory and its (sorted) corpus files.
type group struct {
	name  string
	files []string
}

// readGroups lists the corpus subdirectories and their files, sorted, so the report is
// reproducible on any filesystem regardless of directory-iteration order.
func readGroups(dir string) ([]group, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dedup: reading corpus %s: %w", dir, err)
	}

	var groups []group
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, ferr := readGroupFiles(filepath.Join(dir, e.Name()))
		if ferr != nil {
			return nil, ferr
		}
		if len(files) == 0 {
			continue
		}
		groups = append(groups, group{name: e.Name(), files: files})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups, nil
}

// readGroupFiles returns one group's corpus files — everything that is not a .meta.json sidecar —
// in sorted order.
func readGroupFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("dedup: reading corpus group %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), metaSuffix) {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// readCorpusFile reads one corpus file and its sidecar.
func readCorpusFile(path string) ([]byte, Meta, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a path assembled from this repo's own testdata tree
	if err != nil {
		return nil, Meta{}, fmt.Errorf("dedup: reading %s: %w", path, err)
	}
	metaBytes, err := os.ReadFile(path + metaSuffix) //nolint:gosec // same
	if err != nil {
		return nil, Meta{}, fmt.Errorf("dedup: reading sidecar for %s: %w", path, err)
	}
	var m Meta
	if err := json.Unmarshal(metaBytes, &m); err != nil {
		return nil, Meta{}, fmt.Errorf("dedup: parsing sidecar for %s: %w", path, err)
	}
	if m.Tool == "" {
		return nil, Meta{}, fmt.Errorf("dedup: sidecar for %s names no tool", path)
	}
	return raw, m, nil
}

// Encode renders a Report the way the committed file stores it: indented JSON, HTML escaping off
// (so a "<" in a canonicalizer token appears literally), one trailing newline.
func Encode(rep Report) ([]byte, error) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rep); err != nil {
		return nil, fmt.Errorf("dedup: encoding report: %w", err)
	}
	return []byte(buf.String()), nil
}
