package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Config is the root of the Appendix C configuration document, plus the runtime extension
// namespace of §11.5. Every leaf field carries up to five struct tags: json (the exact Appendix C
// or §11.5 key spelling — mandatory and explicit, never inferred from the Go field name), doc (a
// one-line description), rng (the valid range as text, where the leaf is bounded), enum
// (pipe-separated allowed values, where the leaf is an enumeration), and sec (the design-document
// section that motivates the key). schema.go reads all five; validate.go enforces rng/enum.
//
// TestDefaults_MatchesAppendixCVerbatim and TestDefaults_RuntimeNamespace exist precisely because
// a missing or wrong json tag would fall back to encoding/json's default (the Go field name) and
// silently break both the Appendix C golden and the §11.5 key contract.
type Config struct {
	Store        StoreCfg        `json:"store"`
	Scheduler    SchedulerCfg    `json:"scheduler"`
	Checkpoint   CheckpointCfg   `json:"checkpoint"`
	Sketches     SketchesCfg     `json:"sketches"`
	Eliminations EliminationsCfg `json:"eliminations"`
	Retrieval    RetrievalCfg    `json:"retrieval"`
	Selection    SelectionCfg    `json:"selection"`
	Eval         EvalCfg         `json:"eval"`
	// Runtime is the §11.5 extension namespace: process-level concerns Appendix C does not
	// cover (daemon, hot path, logging, redaction, telemetry, rehydration budgets, MCP limits,
	// plus the SP-01 additions Budgets/Selection/Tokens). No key here may change the meaning or
	// default of any Appendix C key.
	Runtime RuntimeCfg `json:"runtime"`
}

// StoreCfg is the L1 store's configuration (Appendix C "store").
type StoreCfg struct {
	Chunk        ChunkCfg        `json:"chunk"`
	Compression  string          `json:"compression" doc:"object compression codec" enum:"zstd|none" sec:"§6.1"`
	Retention    RetentionCfg    `json:"retention"`
	Canonicalize CanonicalizeCfg `json:"canonicalize"`
}

// ChunkCfg holds the FastCDC boundary parameters (§8.1).
type ChunkCfg struct {
	Min    int `json:"min"    doc:"FastCDC minimum chunk size in bytes" rng:"(0,target)"  sec:"§8.1"`
	Target int `json:"target" doc:"FastCDC target chunk size in bytes"  rng:"(min,max)"   sec:"§8.1"`
	Max    int `json:"max"    doc:"FastCDC maximum chunk size in bytes" rng:"(target,∞)"  sec:"§8.1"`
}

// RetentionCfg is the §8.2 garbage-collection retention window.
type RetentionCfg struct {
	Days     int `json:"days"     doc:"minimum object retention window in days before GC may collect"     rng:"[1,∞)" sec:"§8.2"`
	Sessions int `json:"sessions" doc:"minimum object retention window in sessions before GC may collect" rng:"[1,∞)" sec:"§8.2"`
}

// CanonicalizeCfg controls the §8.1 pre-chunking canonicalization pass.
type CanonicalizeCfg struct {
	Enabled bool       `json:"enabled" doc:"run per-tool canonicalizers before chunking" sec:"§8.1"`
	Strip   []string   `json:"strip"   doc:"canonicalizer classes to strip before chunking" enum:"timestamps|ansi|pids|addresses|tmpPaths|durations|crlf|paths" sec:"§8.1"`
	MinHash MinHashCfg `json:"minhash"`
}

// MinHashCfg controls near-duplicate detection over canonicalized content (§8.1).
type MinHashCfg struct {
	Enabled          bool    `json:"enabled"          doc:"compute a MinHash signature per stored result to detect near-duplicates" sec:"§8.1"`
	Permutations     int     `json:"permutations"     doc:"number of MinHash permutation functions" rng:"[16,512]" sec:"§8.1"`
	NearDupThreshold float64 `json:"nearDupThreshold" doc:"Jaccard similarity above which two results are treated as near-duplicates" rng:"(0,1]" sec:"§8.1"`
}

// SchedulerCfg is the L3 scheduler's configuration (Appendix C "scheduler").
type SchedulerCfg struct {
	SoftFloorPct      float64        `json:"softFloorPct"      doc:"fraction of the effective context window below which compaction never fires" rng:"(0,1)" sec:"§8.4"`
	HardCeilingMargin int            `json:"hardCeilingMargin" doc:"token headroom below Claude Code's own auto-compact threshold at which the plugin forces a checkpoint" rng:"(0,∞)" sec:"§2.5"`
	YoungDaly         YoungDalyCfg   `json:"youngDaly"`
	Changepoint       ChangepointCfg `json:"changepoint"`
	Cache             CacheCfg       `json:"cache"`
	Idle              IdleCfg        `json:"idle"`
}

// YoungDalyCfg controls the §6.7 optimal-checkpoint-interval pacing.
type YoungDalyCfg struct {
	Enabled bool `json:"enabled" doc:"use the Young-Daly optimal checkpoint interval to pace compaction" sec:"§6.7"`
	// MeasuredDeltaSeconds is nil when unset: "measure at runtime", never a pointer to zero.
	MeasuredDeltaSeconds *float64 `json:"measuredDeltaSeconds" doc:"measured compaction cost in seconds; null means measure at runtime" sec:"§6.7"`
}

// ChangepointCfg controls the §6.6 Bayesian online changepoint detector.
type ChangepointCfg struct {
	HazardRate float64  `json:"hazardRate" doc:"BOCD prior hazard rate for a changepoint at each step" rng:"(0,1)" sec:"§6.6"`
	Features   []string `json:"features"   doc:"feature streams BOCD conditions its run-length posterior on" sec:"§6.6"`
}

// CacheCfg holds the §5.1 prompt-cache multipliers and TTL. Every use site reads these from
// config — there is no package-level const r/w anywhere in the repository (D11, §11.6).
type CacheCfg struct {
	ReadMultiplier  float64 `json:"readMultiplier"  doc:"prompt-cache read multiplier r"  rng:"(0,1]"  sec:"§5.1"`
	WriteMultiplier float64 `json:"writeMultiplier" doc:"prompt-cache write multiplier w" rng:"[1,∞)"  sec:"§5.1"`
	TTLSeconds      int     `json:"ttlSeconds"      doc:"sliding cache TTL"               rng:"(0,∞)"  sec:"§5.4"`
}

// IdleCfg controls §8.4 idle-time detection and background work.
type IdleCfg struct {
	DetectAfterSeconds int  `json:"detectAfterSeconds" doc:"seconds of inactivity before the session is considered idle" sec:"§8.4"`
	BackgroundWork     bool `json:"backgroundWork"     doc:"run idle-time background work (shadow checkpoint, GC, slice/Δ-score refresh)" sec:"§8.4"`
	DeepCutWhenCold    bool `json:"deepCutWhenCold"    doc:"prefer a deep cut once the cache is provably cold during an idle gap" sec:"§8.4"`
}

// CheckpointCfg is the L4 checkpointer's configuration (Appendix C "checkpoint").
type CheckpointCfg struct {
	BudgetTokens               int         `json:"budgetTokens"               doc:"target token budget for a single checkpoint artifact" rng:"[1000,100000]" sec:"§8.5"`
	IncrementalSpanInstruction bool        `json:"incrementalSpanInstruction" doc:"emit the O1 focus instruction narrowing the summarizer to the span after the checkpoint frontier" sec:"§8.5"`
	Frontier                   FrontierCfg `json:"frontier"`
	Tiers                      TiersCfg    `json:"tiers"`
}

// FrontierCfg controls the §8.5 incrementally-advancing checkpoint frontier.
type FrontierCfg struct {
	AdvanceOnSegmentClose bool `json:"advanceOnSegmentClose" doc:"advance the checkpoint frontier incrementally whenever a segment closes" sec:"§8.5"`
	MaxResidualTokens     int  `json:"maxResidualTokens"     doc:"maximum tokens between the frontier and the compaction point before a full pass is forced" rng:"(0,∞)" sec:"§8.5"`
}

// checkpointFieldNames is the closed set of checkpoint field names TiersCfg.{Never,Late,First}
// partition (§6.9 embedded-coding order). validate.go and schema.go both reuse it.
const checkpointFieldEnum = "invariants|user_intent|eliminated|decisions|open_questions|current_work|pointers|narrative"

// TiersCfg assigns every checkpoint field to a truncation tier, in importance order (§6.9).
type TiersCfg struct {
	Never []string `json:"never" doc:"checkpoint fields that are never truncated"                          enum:"invariants|user_intent|eliminated|decisions|open_questions|current_work|pointers|narrative" sec:"§6.9"`
	Late  []string `json:"late"  doc:"checkpoint fields truncated only after the first tier is exhausted"   enum:"invariants|user_intent|eliminated|decisions|open_questions|current_work|pointers|narrative" sec:"§6.9"`
	First []string `json:"first" doc:"checkpoint fields truncated first under budget pressure"              enum:"invariants|user_intent|eliminated|decisions|open_questions|current_work|pointers|narrative" sec:"§6.9"`
}

// SketchesCfg sizes the permanent-memory sketches (Appendix C "sketches").
type SketchesCfg struct {
	Bloom BloomCfg `json:"bloom"`
	CMS   CMSCfg   `json:"cms"`
	HLL   HLLCfg   `json:"hll"`
}

// BloomCfg sizes the tried.bloom negative-knowledge filter (§6.2, Appendix A).
type BloomCfg struct {
	Capacity int     `json:"capacity" doc:"expected number of entries the tried.bloom filter is sized for" rng:"[100,∞)"  sec:"Appendix A"`
	FPRate   float64 `json:"fpRate"   doc:"target false-positive rate of the tried.bloom filter"            rng:"(0,0.25)" sec:"Appendix A"`
}

// CMSCfg sizes the touch.cms Count-Min sketch (§6.2, Appendix A).
type CMSCfg struct {
	Epsilon              float64 `json:"epsilon"               doc:"Count-Min sketch error factor ε" rng:"(0,1)" sec:"Appendix A"`
	Delta                float64 `json:"delta"                 doc:"Count-Min sketch failure probability δ" rng:"(0,1)" sec:"Appendix A"`
	WarmStartFromProject bool    `json:"warmStartFromProject"  doc:"seed the Count-Min sketch from project-scoped history at session start" sec:"§6.2"`
}

// HLLCfg sizes the explore.hll cardinality sketch (§6.2).
type HLLCfg struct {
	Registers int `json:"registers" doc:"HyperLogLog register count" rng:"power of two in [64,65536]" sec:"§6.2"`
}

// EliminationsCfg is the L2 negative-knowledge ledger's configuration (Appendix C "eliminations").
type EliminationsCfg struct {
	RequireEvidence bool   `json:"requireEvidence" doc:"require an evidence hash before an elimination is recorded" sec:"§8.3"`
	DefaultScope    string `json:"defaultScope"    doc:"default scope for a new elimination when the caller does not specify one" enum:"session|project" sec:"§8.3"`
	RebuildOnStale  string `json:"rebuildOnStale"  doc:"when to rebuild tried.bloom after a dependency hash changes" enum:"nextIdle|immediate|never" sec:"§8.3"`
	StaleResponse   string `json:"staleResponse"   doc:"how already_tried responds to a stale elimination" enum:"flag|drop" sec:"§8.3"`
}

// RetrievalCfg is the L6 retrieval layer's configuration (Appendix C "retrieval").
type RetrievalCfg struct {
	EphemeralResults       bool   `json:"ephemeralResults"       doc:"tag every retrieval result ephemeral at birth so it is the first eviction candidate" sec:"§8.7"`
	DefaultSpan            string `json:"defaultSpan"            doc:"default span returned by retrieval tools" enum:"minimal|full" sec:"§8.7"`
	PromoteAfterExpansions int    `json:"promoteAfterExpansions" doc:"repeated expansions of the same hash before it is promoted into the next checkpoint's pointer tier" rng:"[1,∞)" sec:"§8.7"`
}

// SelectionCfg is the L2 analyzer's budget-allocation configuration (Appendix C "selection").
type SelectionCfg struct {
	Slicing      string        `json:"slicing"      doc:"dependence-DAG slicing variant used to compute the backward slice" enum:"thin|full" sec:"§8.3"`
	DeltaScoring string        `json:"deltaScoring" doc:"cost tier of the Δ-scoring implementation" enum:"cheap|medium|expensive" sec:"§8.3"`
	Submodular   SubmodularCfg `json:"submodular"`
}

// SubmodularCfg controls the §8.3/§6.5 lazy-greedy submodular selector.
type SubmodularCfg struct {
	Lambda     float64 `json:"lambda"     doc:"redundancy penalty weight in coverage(S) minus lambda*redundancy(S)" rng:"[0,∞)" sec:"§8.3"`
	LazyGreedy bool    `json:"lazyGreedy" doc:"use lazy greedy evaluation for submodular maximization" sec:"§6.5"`
	// Enabled is derived, not read from any file: Load sets it from
	// Runtime.Selection.SubmodularEnabled (default false) so the Appendix C golden stays exact
	// while §5.12's Go-level name (config.SelectionCfg.Submodular.Enabled) is honoured.
	Enabled bool `json:"-" doc:"derived at Load time from runtime.selection.submodularEnabled (closing-note-3 ship-order gate); never read from Appendix C" sec:"Closing note"`
}

// EvalCfg is the L7 evaluation harness's configuration (Appendix C "eval").
type EvalCfg struct {
	ReplayOnPhaseGate bool `json:"replayOnPhaseGate" doc:"run the full replay suite at every phase gate" sec:"§11.3"`
	MinSessions       int  `json:"minSessions"       doc:"minimum logged sessions required for a phase-gate replay to be credible" rng:"[1,∞)" sec:"§11.4"`
}

// Origin identifies which configuration layer produced a value, lowest precedence first:
// OriginDefault < OriginUserFile < OriginProjectFile < OriginEnv < OriginFlag.
type Origin uint8

const (
	// OriginDefault marks a value that came from Defaults() and was never overridden.
	OriginDefault Origin = iota
	// OriginUserFile marks a value set by ~/.qompack/config.json.
	OriginUserFile
	// OriginProjectFile marks a value set by <project>/.qompack/config.json.
	OriginProjectFile
	// OriginEnv marks a value set by a QOMPACK_* environment variable.
	OriginEnv
	// OriginFlag marks a value set by a --set dotted.key=value flag.
	OriginFlag
)

// String renders the origin the way Provenance.Render and `qompack config print --provenance`
// display it.
func (o Origin) String() string {
	switch o {
	case OriginDefault:
		return "default"
	case OriginUserFile:
		return "user"
	case OriginProjectFile:
		return "project"
	case OriginEnv:
		return "env"
	case OriginFlag:
		return "flag"
	default:
		return "unknown"
	}
}

// Source records where a single leaf's effective value came from.
type Source struct {
	Origin   Origin
	Location string // e.g. "C:/proj/.qompack/config.json:12"
}

// Provenance maps a dotted leaf path (e.g. "scheduler.cache.readMultiplier") to the Source that
// produced its effective value.
type Provenance map[string]Source

// Env is everything Load needs from its caller to resolve the five-layer precedence. It carries
// no defaults of its own: ProjectRoot must be supplied by the caller, and Getenv/Flags may be
// nil/empty for a caller that only wants defaults-plus-files.
//
// Getenv is a single-name lookup (matching os.Getenv's shape), not an enumerator: Load never
// scans the process environment. Instead, for every known leaf it synthesizes the one exact
// QOMPACK_<SEC>__<KEY>__<SUB> name that leaf would use and asks Getenv for that name. This keeps
// the env layer fully deterministic and testable without depending on real process environment
// enumeration.
type Env struct {
	ProjectRoot string
	HomeDir     string
	Getenv      func(string) string
	Flags       map[string]string // from the invoking subcommand's --set k=v
}

// Warning is a non-fatal problem discovered while loading configuration: an unknown key, an
// unparseable file, or a leaf that fell back to its default. Load never returns an error for any
// of these; it returns Warnings instead, so a hook that reads bad config never crashes (§11.3).
type Warning struct {
	Key      string
	Message  string
	Location string
}

// Violation is one rule failure from Validate(): the leaf that failed, why, what was found, and
// what was required.
type Violation struct {
	Key     string
	Message string
	Got     any
	Want    any
}

// Get looks up the effective value at a dotted path such as "scheduler.cache.writeMultiplier".
// It reports false for an unknown path. A nil *float64 leaf (youngDaly.measuredDeltaSeconds when
// unset) is reported as (nil, true): the path exists, its value is simply unset.
func (c Config) Get(dotted string) (any, bool) {
	idx, ok := globalSchema.fieldIndex[dotted]
	if !ok {
		return nil, false
	}
	v := reflect.ValueOf(c).FieldByIndex(idx)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, true
		}
		return v.Elem().Interface(), true
	}
	return v.Interface(), true
}

// --- shared reflection schema index -----------------------------------------------------------
//
// Load, Validate's leaf enumeration (exercised by the config_test package), JSONSchema, and Get
// all need the same fact: for every leaf in Config, its dotted JSON path, its Go kind, and its
// struct tags. Building that by reflection once at package init — rather than on every Load call
// — is what keeps BenchmarkConfigLoad_ColdNoFiles well under its 2ms budget.

// leafKind is the reflected Go kind of one config leaf, narrowed to the six shapes Config
// actually uses. It drives env/flag value parsing (load.go) and JSON Schema type emission
// (schema.go).
type leafKind uint8

const (
	kindBool leafKind = iota
	kindInt
	kindFloat
	kindString
	kindStringSlice
	kindFloatPtr
)

// leafInfo describes one leaf field: its kind and its doc/rng/enum/sec struct tags.
type leafInfo struct {
	kind    leafKind
	jsonKey string
	doc     string
	rng     string
	sec     string
	enum    []string
}

// schemaIndex is the fully-walked shape of Config, built once by buildSchemaIndex.
type schemaIndex struct {
	leaves     map[string]leafInfo // dotted path -> leaf descriptor
	sections   map[string]bool     // dotted path -> true, including "" for the root
	fieldIndex map[string][]int    // dotted path (leaf or section) -> reflect.Value.FieldByIndex chain
	leafPaths  []string            // sorted dotted paths of every leaf; iteration order for env/schema
}

// globalSchema is computed once at package init from the Config type's own struct tags. It never
// changes at runtime, so every caller shares it without repeating the reflection walk.
var globalSchema = buildSchemaIndex()

func buildSchemaIndex() *schemaIndex {
	idx := &schemaIndex{
		leaves:     map[string]leafInfo{},
		sections:   map[string]bool{"": true},
		fieldIndex: map[string][]int{},
	}
	walkSchema(reflect.TypeOf(Config{}), "", nil, idx)
	idx.leafPaths = make([]string, 0, len(idx.leaves))
	for p := range idx.leaves {
		idx.leafPaths = append(idx.leafPaths, p)
	}
	sort.Strings(idx.leafPaths)
	return idx
}

func walkSchema(t reflect.Type, prefix string, index []int, idx *schemaIndex) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, skip := jsonLeafName(f)
		if skip {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fieldPath := make([]int, len(index), len(index)+1)
		copy(fieldPath, index)
		fieldPath = append(fieldPath, i)
		idx.fieldIndex[path] = fieldPath

		if f.Type.Kind() == reflect.Struct {
			idx.sections[path] = true
			walkSchema(f.Type, path, fieldPath, idx)
			continue
		}

		li := leafInfo{
			jsonKey: name,
			doc:     f.Tag.Get("doc"),
			rng:     f.Tag.Get("rng"),
			sec:     f.Tag.Get("sec"),
		}
		if e := f.Tag.Get("enum"); e != "" {
			li.enum = strings.Split(e, "|")
		}
		switch {
		case f.Type.Kind() == reflect.Bool:
			li.kind = kindBool
		case f.Type.Kind() == reflect.Int:
			li.kind = kindInt
		case f.Type.Kind() == reflect.Float64:
			li.kind = kindFloat
		case f.Type.Kind() == reflect.String:
			li.kind = kindString
		case f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.String:
			li.kind = kindStringSlice
		case f.Type.Kind() == reflect.Ptr && f.Type.Elem().Kind() == reflect.Float64:
			li.kind = kindFloatPtr
		default:
			panic(fmt.Sprintf("config: unhandled leaf kind %s for %s", f.Type.Kind(), path))
		}
		idx.leaves[path] = li
	}
}

// jsonLeafName extracts the JSON key a struct field serializes under, honouring "-" (skip
// entirely — SubmodularCfg.Enabled is the one field in Config that uses this).
func jsonLeafName(f reflect.StructField) (name string, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", true
	}
	name = tag
	if i := strings.IndexByte(tag, ','); i >= 0 {
		name = tag[:i]
	}
	if name == "" {
		name = f.Name
	}
	return name, false
}
