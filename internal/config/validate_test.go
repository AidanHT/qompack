package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// ruleCase is one table row for TestValidate_EveryRule: mutate applies exactly one out-of-range
// change to an otherwise-default Config, and wantKey is the single Violation.Key Validate() must
// report for it.
type ruleCase struct {
	name    string
	mutate  func(c *config.Config)
	wantKey string
}

// ruleCases enumerates one case per bound in validate.go's rule list (00-ARCHITECTURE.md §11.3),
// in the same top-to-bottom order as that list; a rule with two bounds (an open or closed range)
// gets two cases, one per bound. TestValidate_RuleTableIsComplete cross-checks the set of
// wantKeys here, plus an explicit unconstrained allowlist, against every leaf in Config.
var ruleCases = []ruleCase{
	// store.chunk.min < store.chunk.target < store.chunk.max
	{"chunk.min not less than target", func(c *config.Config) { c.Store.Chunk.Min = 4096 }, "store.chunk.min"},
	{"chunk.max not greater than target", func(c *config.Config) { c.Store.Chunk.Max = 4096 }, "store.chunk.max"},

	// store.compression ∈ {zstd, none}
	{"compression unknown value", func(c *config.Config) { c.Store.Compression = "gzip" }, "store.compression"},

	// store.retention.days ≥ 1 ; store.retention.sessions ≥ 1
	{"retention.days below 1", func(c *config.Config) { c.Store.Retention.Days = 0 }, "store.retention.days"},
	{"retention.sessions below 1", func(c *config.Config) { c.Store.Retention.Sessions = 0 }, "store.retention.sessions"},

	// store.canonicalize.strip ⊆ known classes
	{"canonicalize.strip unknown class", func(c *config.Config) { c.Store.Canonicalize.Strip = []string{"bogus"} }, "store.canonicalize.strip"},

	// store.canonicalize.minhash.permutations ∈ [16,512]
	{"minhash.permutations below 16", func(c *config.Config) { c.Store.Canonicalize.MinHash.Permutations = 15 }, "store.canonicalize.minhash.permutations"},
	{"minhash.permutations above 512", func(c *config.Config) { c.Store.Canonicalize.MinHash.Permutations = 513 }, "store.canonicalize.minhash.permutations"},

	// store.canonicalize.minhash.nearDupThreshold ∈ (0,1]
	{"nearDupThreshold at 0", func(c *config.Config) { c.Store.Canonicalize.MinHash.NearDupThreshold = 0 }, "store.canonicalize.minhash.nearDupThreshold"},
	{"nearDupThreshold above 1", func(c *config.Config) { c.Store.Canonicalize.MinHash.NearDupThreshold = 1.1 }, "store.canonicalize.minhash.nearDupThreshold"},

	// scheduler.softFloorPct ∈ (0,1)
	{"softFloorPct at 0", func(c *config.Config) { c.Scheduler.SoftFloorPct = 0 }, "scheduler.softFloorPct"},
	{"softFloorPct at 1", func(c *config.Config) { c.Scheduler.SoftFloorPct = 1 }, "scheduler.softFloorPct"},

	// scheduler.hardCeilingMargin > 0
	{"hardCeilingMargin at 0", func(c *config.Config) { c.Scheduler.HardCeilingMargin = 0 }, "scheduler.hardCeilingMargin"},

	// scheduler.changepoint.hazardRate ∈ (0,1)
	{"hazardRate at 0", func(c *config.Config) { c.Scheduler.Changepoint.HazardRate = 0 }, "scheduler.changepoint.hazardRate"},
	{"hazardRate at 1", func(c *config.Config) { c.Scheduler.Changepoint.HazardRate = 1 }, "scheduler.changepoint.hazardRate"},

	// scheduler.cache.readMultiplier ∈ (0,1] ; writeMultiplier ≥ 1 ; ttlSeconds > 0
	{"readMultiplier at 0", func(c *config.Config) { c.Scheduler.Cache.ReadMultiplier = 0 }, "scheduler.cache.readMultiplier"},
	{"readMultiplier above 1", func(c *config.Config) { c.Scheduler.Cache.ReadMultiplier = 1.5 }, "scheduler.cache.readMultiplier"},
	{"writeMultiplier below 1", func(c *config.Config) { c.Scheduler.Cache.WriteMultiplier = 0.5 }, "scheduler.cache.writeMultiplier"},
	{"ttlSeconds at 0", func(c *config.Config) { c.Scheduler.Cache.TTLSeconds = 0 }, "scheduler.cache.ttlSeconds"},

	// checkpoint.budgetTokens ∈ [1000,100000]
	{"budgetTokens below 1000", func(c *config.Config) { c.Checkpoint.BudgetTokens = 999 }, "checkpoint.budgetTokens"},
	{"budgetTokens above 100000", func(c *config.Config) { c.Checkpoint.BudgetTokens = 100001 }, "checkpoint.budgetTokens"},

	// checkpoint.frontier.maxResidualTokens > 0
	{"maxResidualTokens at 0", func(c *config.Config) { c.Checkpoint.Frontier.MaxResidualTokens = 0 }, "checkpoint.frontier.maxResidualTokens"},

	// checkpoint.tiers.{never,late,first} ⊆ known fields, pairwise disjoint, union covers the set
	{"tiers.never unknown field", func(c *config.Config) {
		c.Checkpoint.Tiers.Never = []string{"invariants", "user_intent", "eliminated", "bogus"}
	}, "checkpoint.tiers.never"},
	{"tiers.late unknown field", func(c *config.Config) {
		c.Checkpoint.Tiers.Late = []string{"decisions", "open_questions", "current_work", "bogus"}
	}, "checkpoint.tiers.late"},
	{"tiers.first unknown field", func(c *config.Config) {
		c.Checkpoint.Tiers.First = []string{"pointers", "narrative", "bogus"}
	}, "checkpoint.tiers.first"},
	{"tiers not disjoint", func(c *config.Config) {
		c.Checkpoint.Tiers.Late = []string{"decisions", "open_questions", "current_work", "pointers"}
	}, "checkpoint.tiers"},
	{"tiers does not cover the whole set", func(c *config.Config) {
		c.Checkpoint.Tiers.First = []string{"pointers"}
	}, "checkpoint.tiers"},

	// sketches.bloom.capacity ≥ 100 ; sketches.bloom.fpRate ∈ (0,0.25)
	{"bloom.capacity below 100", func(c *config.Config) { c.Sketches.Bloom.Capacity = 99 }, "sketches.bloom.capacity"},
	{"bloom.fpRate at 0", func(c *config.Config) { c.Sketches.Bloom.FPRate = 0 }, "sketches.bloom.fpRate"},
	{"bloom.fpRate at 0.25", func(c *config.Config) { c.Sketches.Bloom.FPRate = 0.25 }, "sketches.bloom.fpRate"},

	// sketches.cms.epsilon ∈ (0,1) ; sketches.cms.delta ∈ (0,1)
	{"cms.epsilon at 0", func(c *config.Config) { c.Sketches.CMS.Epsilon = 0 }, "sketches.cms.epsilon"},
	{"cms.epsilon at 1", func(c *config.Config) { c.Sketches.CMS.Epsilon = 1 }, "sketches.cms.epsilon"},
	{"cms.delta at 0", func(c *config.Config) { c.Sketches.CMS.Delta = 0 }, "sketches.cms.delta"},
	{"cms.delta at 1", func(c *config.Config) { c.Sketches.CMS.Delta = 1 }, "sketches.cms.delta"},

	// sketches.hll.registers is a power of two in [64,65536]
	{"hll.registers below 64", func(c *config.Config) { c.Sketches.HLL.Registers = 32 }, "sketches.hll.registers"},
	{"hll.registers above 65536", func(c *config.Config) { c.Sketches.HLL.Registers = 131072 }, "sketches.hll.registers"},
	{"hll.registers not a power of two", func(c *config.Config) { c.Sketches.HLL.Registers = 1000 }, "sketches.hll.registers"},

	// eliminations.defaultScope / rebuildOnStale / staleResponse
	{"eliminations.defaultScope unknown", func(c *config.Config) { c.Eliminations.DefaultScope = "bogus" }, "eliminations.defaultScope"},
	{"eliminations.rebuildOnStale unknown", func(c *config.Config) { c.Eliminations.RebuildOnStale = "bogus" }, "eliminations.rebuildOnStale"},
	{"eliminations.staleResponse unknown", func(c *config.Config) { c.Eliminations.StaleResponse = "bogus" }, "eliminations.staleResponse"},

	// retrieval.defaultSpan ∈ {minimal,full} ; retrieval.promoteAfterExpansions ≥ 1
	{"retrieval.defaultSpan unknown", func(c *config.Config) { c.Retrieval.DefaultSpan = "bogus" }, "retrieval.defaultSpan"},
	{"promoteAfterExpansions below 1", func(c *config.Config) { c.Retrieval.PromoteAfterExpansions = 0 }, "retrieval.promoteAfterExpansions"},

	// selection.slicing ∈ {thin,full} ; selection.deltaScoring ∈ {cheap,medium,expensive}
	{"selection.slicing unknown", func(c *config.Config) { c.Selection.Slicing = "bogus" }, "selection.slicing"},
	{"selection.deltaScoring unknown", func(c *config.Config) { c.Selection.DeltaScoring = "bogus" }, "selection.deltaScoring"},

	// selection.submodular.lambda ≥ 0
	{"submodular.lambda below 0", func(c *config.Config) { c.Selection.Submodular.Lambda = -0.1 }, "selection.submodular.lambda"},

	// eval.minSessions ≥ 1
	{"eval.minSessions below 1", func(c *config.Config) { c.Eval.MinSessions = 0 }, "eval.minSessions"},

	// runtime.mode ∈ {auto,full,passive,off}
	{"runtime.mode unknown", func(c *config.Config) { c.Runtime.Mode = "bogus" }, "runtime.mode"},

	// runtime.hotPath.budgetMs > 0 ; breachWindows ≥ 1 ; maxPayloadBytes ≥ 4096
	{"hotPath.budgetMs at 0", func(c *config.Config) { c.Runtime.HotPath.BudgetMs = 0 }, "runtime.hotPath.budgetMs"},
	{"hotPath.breachWindows below 1", func(c *config.Config) { c.Runtime.HotPath.BreachWindows = 0 }, "runtime.hotPath.breachWindows"},
	{"hotPath.maxPayloadBytes below 4096", func(c *config.Config) { c.Runtime.HotPath.MaxPayloadBytes = 4095 }, "runtime.hotPath.maxPayloadBytes"},

	// runtime.logging.level ∈ {debug,info,warn,error}
	{"logging.level unknown", func(c *config.Config) { c.Runtime.Logging.Level = "bogus" }, "runtime.logging.level"},

	// runtime.rehydrate.minTokens ≥ 1 ; minTokens ≤ maxTokens ; skillIndexTokens ≥ 1 ; eliminationsTopN ≥ 1
	{"rehydrate.minTokens below 1", func(c *config.Config) { c.Runtime.Rehydrate.MinTokens = 0 }, "runtime.rehydrate.minTokens"},
	{"rehydrate.minTokens above maxTokens", func(c *config.Config) { c.Runtime.Rehydrate.MinTokens = 20000 }, "runtime.rehydrate.minTokens"},
	{"rehydrate.skillIndexTokens below 1", func(c *config.Config) { c.Runtime.Rehydrate.SkillIndexTokens = 0 }, "runtime.rehydrate.skillIndexTokens"},
	{"rehydrate.eliminationsTopN below 1", func(c *config.Config) { c.Runtime.Rehydrate.EliminationsTopN = 0 }, "runtime.rehydrate.eliminationsTopN"},

	// runtime.mcp.spanWidenLines ≥ 0 ; maxResponseBytes ≥ 4096
	{"mcp.spanWidenLines below 0", func(c *config.Config) { c.Runtime.MCP.SpanWidenLines = -1 }, "runtime.mcp.spanWidenLines"},
	{"mcp.maxResponseBytes below 4096", func(c *config.Config) { c.Runtime.MCP.MaxResponseBytes = 4095 }, "runtime.mcp.maxResponseBytes"},

	// runtime.tokens.* charsPerToken ∈ [1,20]
	{"proseCharsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.ProseCharsPerToken = 0.5 }, "runtime.tokens.proseCharsPerToken"},
	{"proseCharsPerToken above 20", func(c *config.Config) { c.Runtime.Tokens.ProseCharsPerToken = 21 }, "runtime.tokens.proseCharsPerToken"},
	{"codeCharsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.CodeCharsPerToken = 0.5 }, "runtime.tokens.codeCharsPerToken"},
	{"codeCharsPerToken above 20", func(c *config.Config) { c.Runtime.Tokens.CodeCharsPerToken = 21 }, "runtime.tokens.codeCharsPerToken"},
	{"jsonCharsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.JSONCharsPerToken = 0.5 }, "runtime.tokens.jsonCharsPerToken"},
	{"jsonCharsPerToken above 20", func(c *config.Config) { c.Runtime.Tokens.JSONCharsPerToken = 21 }, "runtime.tokens.jsonCharsPerToken"},
	{"diffCharsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.DiffCharsPerToken = 0.5 }, "runtime.tokens.diffCharsPerToken"},
	{"diffCharsPerToken above 20", func(c *config.Config) { c.Runtime.Tokens.DiffCharsPerToken = 21 }, "runtime.tokens.diffCharsPerToken"},
	{"binaryCharsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.BinaryCharsPerToken = 0.5 }, "runtime.tokens.binaryCharsPerToken"},
	{"binaryCharsPerToken above 20", func(c *config.Config) { c.Runtime.Tokens.BinaryCharsPerToken = 21 }, "runtime.tokens.binaryCharsPerToken"},

	// runtime.tokens.imagePixelsPerToken ≥ 1 ; pdfTokensPerPage ≥ 1
	{"imagePixelsPerToken below 1", func(c *config.Config) { c.Runtime.Tokens.ImagePixelsPerToken = 0 }, "runtime.tokens.imagePixelsPerToken"},
	{"pdfTokensPerPage below 1", func(c *config.Config) { c.Runtime.Tokens.PDFTokensPerPage = 0 }, "runtime.tokens.pdfTokensPerPage"},

	// runtime.tokens.calibrationMin ∈ (0,1] ; calibrationMax ≥ 1 ; calibrationAlpha ∈ (0,1]
	{"calibrationMin at 0", func(c *config.Config) { c.Runtime.Tokens.CalibrationMin = 0 }, "runtime.tokens.calibrationMin"},
	{"calibrationMin above 1", func(c *config.Config) { c.Runtime.Tokens.CalibrationMin = 1.5 }, "runtime.tokens.calibrationMin"},
	{"calibrationMax below 1", func(c *config.Config) { c.Runtime.Tokens.CalibrationMax = 0.5 }, "runtime.tokens.calibrationMax"},
	{"calibrationAlpha at 0", func(c *config.Config) { c.Runtime.Tokens.CalibrationAlpha = 0 }, "runtime.tokens.calibrationAlpha"},
	{"calibrationAlpha above 1", func(c *config.Config) { c.Runtime.Tokens.CalibrationAlpha = 1.5 }, "runtime.tokens.calibrationAlpha"},

	// runtime.budgets.* > 0
	{"budgets.l0IngestMs at 0", func(c *config.Config) { c.Runtime.Budgets.L0IngestMs = 0 }, "runtime.budgets.l0IngestMs"},
	{"budgets.l0ProcessMs at 0", func(c *config.Config) { c.Runtime.Budgets.L0ProcessMs = 0 }, "runtime.budgets.l0ProcessMs"},
	{"budgets.checkpointFinalizeMs at 0", func(c *config.Config) { c.Runtime.Budgets.CheckpointFinalizeMs = 0 }, "runtime.budgets.checkpointFinalizeMs"},
	{"budgets.mcpToolCallMs at 0", func(c *config.Config) { c.Runtime.Budgets.MCPToolCallMs = 0 }, "runtime.budgets.mcpToolCallMs"},

	// runtime.telemetry.enabled must be false
	{"telemetry.enabled true", func(c *config.Config) { c.Runtime.Telemetry.Enabled = true }, "runtime.telemetry.enabled"},
}

func TestValidate_EveryRule(t *testing.T) {
	for _, tc := range ruleCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			tc.mutate(&cfg)
			violations := cfg.Validate()
			require.Len(t, violations, 1, "violations: %+v", violations)
			require.Equal(t, tc.wantKey, violations[0].Key)
		})
	}
}

// TestValidate_DefaultsAreValid guards the rule table itself: if Defaults() ever failed its own
// Validate(), every ruleCase above (which mutates a single leaf away from the default baseline)
// would be built on a already-invalid baseline and could spuriously report more than one
// violation.
func TestValidate_DefaultsAreValid(t *testing.T) {
	require.Empty(t, config.Defaults().Validate())
}

// unconstrainedLeaves are the Config leaves 00-ARCHITECTURE.md §11.3 places no rule on: booleans
// with no invalid state, and a handful of ints/strings/slices the rule list does not bound.
// TestValidate_RuleTableIsComplete asserts every leaf in Config is in exactly one of this set or
// the wantKeys of ruleCases above — so a newly added key can never silently skip a validation
// decision.
var unconstrainedLeaves = map[string]bool{
	"store.canonicalize.enabled":                true,
	"store.canonicalize.minhash.enabled":        true,
	"scheduler.youngDaly.enabled":               true,
	"scheduler.youngDaly.measuredDeltaSeconds":  true,
	"scheduler.changepoint.features":            true,
	"scheduler.idle.detectAfterSeconds":         true,
	"scheduler.idle.backgroundWork":             true,
	"scheduler.idle.deepCutWhenCold":            true,
	"checkpoint.incrementalSpanInstruction":     true,
	"checkpoint.frontier.advanceOnSegmentClose": true,
	"sketches.cms.warmStartFromProject":         true,
	"eliminations.requireEvidence":              true,
	"retrieval.ephemeralResults":                true,
	"selection.submodular.lazyGreedy":           true,
	"eval.replayOnPhaseGate":                    true,
	"runtime.daemon.enabled":                    true,
	"runtime.daemon.idleExitSeconds":            true,
	"runtime.daemon.maxSessions":                true,
	"runtime.daemon.ackDeadlineMs":              true,
	"runtime.daemon.connectDeadlineMs":          true,
	"runtime.hotPath.spoolOnBreach":             true,
	"runtime.logging.maxFileMB":                 true,
	"runtime.logging.maxFiles":                  true,
	"runtime.redact.enabled":                    true,
	"runtime.redact.patterns":                   true,
	"runtime.selection.submodularEnabled":       true,
	"runtime.tokens.imageMaxTokens":             true,
}

func TestValidate_RuleTableIsComplete(t *testing.T) {
	leaves := allLeafPaths(t)

	covered := map[string]bool{}
	for _, tc := range ruleCases {
		covered[tc.wantKey] = true
	}
	// checkpoint.tiers is a cross-field key the disjoint/cover checks report on; it is not a
	// Config leaf at all (never/late/first are), so it must not be checked against leaves below.
	delete(covered, "checkpoint.tiers")

	// Two leaves are genuinely validated but only ever surface a Violation keyed to a sibling:
	// store.chunk.target is the middle term of min<target<max (out-of-range target always
	// violates the min or max comparison, never a comparison keyed to target itself), and
	// runtime.rehydrate.maxTokens only ever fails through the minTokens<=maxTokens check, which
	// validate.go reports under "runtime.rehydrate.minTokens". Mark both covered explicitly
	// rather than adding a wantKey that could never actually be produced.
	covered["store.chunk.target"] = true
	covered["runtime.rehydrate.maxTokens"] = true

	for leaf := range leaves {
		if covered[leaf] || unconstrainedLeaves[leaf] {
			continue
		}
		t.Errorf("leaf %q is neither covered by a ruleCases entry nor in unconstrainedLeaves", leaf)
	}
	for k := range unconstrainedLeaves {
		if !leaves[k] {
			t.Errorf("unconstrainedLeaves has stale entry %q: not a Config leaf", k)
		}
		require.False(t, covered[k], "leaf %q is both validated and unconstrained", k)
	}
	for k := range covered {
		if !leaves[k] {
			t.Errorf("a ruleCases wantKey %q is not a Config leaf", k)
		}
	}
}

func TestValidate_TiersPartition(t *testing.T) {
	t.Run("overlap", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Checkpoint.Tiers.Late = []string{"decisions", "open_questions", "current_work", "pointers"}
		require.True(t, hasViolation(cfg.Validate(), "checkpoint.tiers", "never/late/first must be pairwise disjoint"))
	})
	t.Run("missing narrative", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Checkpoint.Tiers.First = []string{"pointers"}
		require.True(t, hasViolation(cfg.Validate(), "checkpoint.tiers", "never/late/first must together cover every checkpoint field"))
	})
}

func TestValidate_TelemetryMustBeFalse(t *testing.T) {
	cfg := config.Defaults()
	cfg.Runtime.Telemetry.Enabled = true
	violations := cfg.Validate()
	require.Len(t, violations, 1)
	require.Equal(t, "runtime.telemetry.enabled", violations[0].Key)
}

func hasViolation(vs []config.Violation, key, message string) bool {
	for _, v := range vs {
		if v.Key == key && v.Message == message {
			return true
		}
	}
	return false
}

// allLeafPaths independently reflects over config.Config's own exported json tags to enumerate
// every leaf's dotted path. It deliberately duplicates production's schema walk rather than
// reaching into it, because these are black-box (config_test) tests exercising only the public
// API — and because a second, independently-written walk is a better check that config.go's
// struct tags actually say what this file assumes they say.
func allLeafPaths(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	var walk func(rt reflect.Type, prefix string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := tag
			if idx := strings.Index(name, ","); idx >= 0 {
				name = name[:idx]
			}
			if name == "" {
				name = f.Name
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, path)
				continue
			}
			out[path] = true
		}
	}
	walk(reflect.TypeOf(config.Config{}), "")
	return out
}
