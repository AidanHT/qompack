package config

import (
	"fmt"
	"sort"
	"strings"
)

// Validate checks every rule below against c and returns one Violation per failing leaf. It
// never panics: a malformed Config (for example one built by hand in a test, or one that Load
// produced from adversarial input) can only ever produce a slice of Violations, never a crash.
//
// The rule list, transcribed from 00-ARCHITECTURE.md §11.3 — this comment block is the source of
// truth TestValidate_EveryRule enumerates one case per bound from:
//
//	store.chunk.min < store.chunk.target < store.chunk.max
//	store.compression ∈ {zstd, none}
//	store.retention.days ≥ 1 ; store.retention.sessions ≥ 1
//	store.canonicalize.strip ⊆ {timestamps, ansi, pids, addresses, tmpPaths, durations, crlf, paths}
//	store.canonicalize.minhash.permutations ∈ [16,512]
//	store.canonicalize.minhash.nearDupThreshold ∈ (0,1]
//	scheduler.softFloorPct ∈ (0,1)
//	scheduler.hardCeilingMargin > 0
//	scheduler.changepoint.hazardRate ∈ (0,1)
//	scheduler.cache.readMultiplier ∈ (0,1] ; scheduler.cache.writeMultiplier ≥ 1 ; scheduler.cache.ttlSeconds > 0
//	checkpoint.budgetTokens ∈ [1000,100000]
//	checkpoint.frontier.maxResidualTokens > 0
//	checkpoint.tiers.{never,late,first} ⊆ {invariants,user_intent,eliminated,decisions,open_questions,
//	    current_work,pointers,narrative}, pairwise disjoint, union == that whole set
//	sketches.bloom.capacity ≥ 100 ; sketches.bloom.fpRate ∈ (0,0.25)
//	sketches.cms.epsilon ∈ (0,1) ; sketches.cms.delta ∈ (0,1)
//	sketches.hll.registers is a power of two in [64,65536]
//	eliminations.defaultScope ∈ {session,project}
//	eliminations.rebuildOnStale ∈ {nextIdle,immediate,never}
//	eliminations.staleResponse ∈ {flag,drop}
//	retrieval.defaultSpan ∈ {minimal,full} ; retrieval.promoteAfterExpansions ≥ 1
//	selection.slicing ∈ {thin,full} ; selection.deltaScoring ∈ {cheap,medium,expensive}
//	selection.submodular.lambda ≥ 0
//	eval.minSessions ≥ 1
//	runtime.mode ∈ {auto,full,passive,off}
//	runtime.hotPath.budgetMs > 0 ; runtime.hotPath.breachWindows ≥ 1 ; runtime.hotPath.maxPayloadBytes ≥ 4096
//	runtime.logging.level ∈ {debug,info,warn,error}
//	runtime.rehydrate.minTokens ≥ 1 ; minTokens ≤ maxTokens ; skillIndexTokens ≥ 1 ; eliminationsTopN ≥ 1
//	runtime.mcp.spanWidenLines ≥ 0 ; runtime.mcp.maxResponseBytes ≥ 4096
//	runtime.tokens.* charsPerToken ∈ [1,20] ; imagePixelsPerToken ≥ 1 ; pdfTokensPerPage ≥ 1
//	runtime.tokens.calibrationMin ∈ (0,1] ; calibrationMax ≥ 1 ; calibrationAlpha ∈ (0,1]
//	runtime.budgets.* > 0
//	runtime.telemetry.enabled must be false            ← hardwired; a true value is a Violation
//
// Every leaf in Config not covered by a check below is intentionally unconstrained (booleans
// with no invalid state, and a handful of ints/strings §11.3 does not bound): see
// TestValidate_RuleTableIsComplete in the config_test package for the explicit allowlist.
func (c Config) Validate() []Violation {
	var out []Violation
	add := func(key, msg string, got, want any) {
		out = append(out, Violation{Key: key, Message: msg, Got: got, Want: want})
	}

	// store.chunk.min < store.chunk.target < store.chunk.max
	if !(c.Store.Chunk.Min < c.Store.Chunk.Target) {
		add("store.chunk.min", "must be less than store.chunk.target", c.Store.Chunk.Min,
			fmt.Sprintf("< %d", c.Store.Chunk.Target))
	}
	if !(c.Store.Chunk.Target < c.Store.Chunk.Max) {
		add("store.chunk.max", "must be greater than store.chunk.target", c.Store.Chunk.Max,
			fmt.Sprintf("> %d", c.Store.Chunk.Target))
	}

	// store.compression ∈ {zstd, none}
	if !isOneOf(c.Store.Compression, "zstd", "none") {
		add("store.compression", "must be one of the allowed values", c.Store.Compression, "zstd|none")
	}

	// store.retention.days ≥ 1 ; store.retention.sessions ≥ 1
	if c.Store.Retention.Days < 1 {
		add("store.retention.days", "must be at least 1", c.Store.Retention.Days, ">= 1")
	}
	if c.Store.Retention.Sessions < 1 {
		add("store.retention.sessions", "must be at least 1", c.Store.Retention.Sessions, ">= 1")
	}

	// store.canonicalize.strip ⊆ {timestamps, ansi, pids, addresses, tmpPaths, durations, crlf, paths}
	if !allKnown(c.Store.Canonicalize.Strip, knownCanonicalizeClasses) {
		add("store.canonicalize.strip", "contains an unknown canonicalizer class", c.Store.Canonicalize.Strip,
			"subset of "+strings.Join(sortedKeys(knownCanonicalizeClasses), "|"))
	}

	// store.canonicalize.minhash.permutations ∈ [16,512]
	if c.Store.Canonicalize.MinHash.Permutations < 16 || c.Store.Canonicalize.MinHash.Permutations > 512 {
		add("store.canonicalize.minhash.permutations", "out of range", c.Store.Canonicalize.MinHash.Permutations, "[16,512]")
	}

	// store.canonicalize.minhash.nearDupThreshold ∈ (0,1]
	if c.Store.Canonicalize.MinHash.NearDupThreshold <= 0 || c.Store.Canonicalize.MinHash.NearDupThreshold > 1 {
		add("store.canonicalize.minhash.nearDupThreshold", "out of range", c.Store.Canonicalize.MinHash.NearDupThreshold, "(0,1]")
	}

	// scheduler.softFloorPct ∈ (0,1)
	if c.Scheduler.SoftFloorPct <= 0 || c.Scheduler.SoftFloorPct >= 1 {
		add("scheduler.softFloorPct", "out of range", c.Scheduler.SoftFloorPct, "(0,1)")
	}

	// scheduler.hardCeilingMargin > 0
	if c.Scheduler.HardCeilingMargin <= 0 {
		add("scheduler.hardCeilingMargin", "must be greater than 0", c.Scheduler.HardCeilingMargin, "> 0")
	}

	// scheduler.changepoint.hazardRate ∈ (0,1)
	if c.Scheduler.Changepoint.HazardRate <= 0 || c.Scheduler.Changepoint.HazardRate >= 1 {
		add("scheduler.changepoint.hazardRate", "out of range", c.Scheduler.Changepoint.HazardRate, "(0,1)")
	}

	// scheduler.cache.readMultiplier ∈ (0,1] ; writeMultiplier ≥ 1 ; ttlSeconds > 0
	if c.Scheduler.Cache.ReadMultiplier <= 0 || c.Scheduler.Cache.ReadMultiplier > 1 {
		add("scheduler.cache.readMultiplier", "out of range", c.Scheduler.Cache.ReadMultiplier, "(0,1]")
	}
	if c.Scheduler.Cache.WriteMultiplier < 1 {
		add("scheduler.cache.writeMultiplier", "must be at least 1", c.Scheduler.Cache.WriteMultiplier, ">= 1")
	}
	if c.Scheduler.Cache.TTLSeconds <= 0 {
		add("scheduler.cache.ttlSeconds", "must be greater than 0", c.Scheduler.Cache.TTLSeconds, "> 0")
	}

	// checkpoint.budgetTokens ∈ [1000,100000]
	if c.Checkpoint.BudgetTokens < 1000 || c.Checkpoint.BudgetTokens > 100000 {
		add("checkpoint.budgetTokens", "out of range", c.Checkpoint.BudgetTokens, "[1000,100000]")
	}

	// checkpoint.frontier.maxResidualTokens > 0
	if c.Checkpoint.Frontier.MaxResidualTokens <= 0 {
		add("checkpoint.frontier.maxResidualTokens", "must be greater than 0", c.Checkpoint.Frontier.MaxResidualTokens, "> 0")
	}

	// checkpoint.tiers.{never,late,first} ⊆ known fields, pairwise disjoint, union covers the set
	if !allKnown(c.Checkpoint.Tiers.Never, checkpointFieldSet) {
		add("checkpoint.tiers.never", "contains a field outside the known checkpoint field set", c.Checkpoint.Tiers.Never, checkpointFieldEnum)
	}
	if !allKnown(c.Checkpoint.Tiers.Late, checkpointFieldSet) {
		add("checkpoint.tiers.late", "contains a field outside the known checkpoint field set", c.Checkpoint.Tiers.Late, checkpointFieldEnum)
	}
	if !allKnown(c.Checkpoint.Tiers.First, checkpointFieldSet) {
		add("checkpoint.tiers.first", "contains a field outside the known checkpoint field set", c.Checkpoint.Tiers.First, checkpointFieldEnum)
	}
	if dup := tierOverlap(c.Checkpoint.Tiers); len(dup) > 0 {
		add("checkpoint.tiers", "never/late/first must be pairwise disjoint", dup, "pairwise disjoint")
	}
	if missing := tierGaps(c.Checkpoint.Tiers, checkpointFieldSet); len(missing) > 0 {
		add("checkpoint.tiers", "never/late/first must together cover every checkpoint field", missing, checkpointFieldEnum)
	}

	// sketches.bloom.capacity ≥ 100 ; sketches.bloom.fpRate ∈ (0,0.25)
	if c.Sketches.Bloom.Capacity < 100 {
		add("sketches.bloom.capacity", "must be at least 100", c.Sketches.Bloom.Capacity, ">= 100")
	}
	if c.Sketches.Bloom.FPRate <= 0 || c.Sketches.Bloom.FPRate >= 0.25 {
		add("sketches.bloom.fpRate", "out of range", c.Sketches.Bloom.FPRate, "(0,0.25)")
	}

	// sketches.cms.epsilon ∈ (0,1) ; sketches.cms.delta ∈ (0,1)
	if c.Sketches.CMS.Epsilon <= 0 || c.Sketches.CMS.Epsilon >= 1 {
		add("sketches.cms.epsilon", "out of range", c.Sketches.CMS.Epsilon, "(0,1)")
	}
	if c.Sketches.CMS.Delta <= 0 || c.Sketches.CMS.Delta >= 1 {
		add("sketches.cms.delta", "out of range", c.Sketches.CMS.Delta, "(0,1)")
	}

	// sketches.hll.registers is a power of two in [64,65536]
	if !isPowerOfTwo(c.Sketches.HLL.Registers) || c.Sketches.HLL.Registers < 64 || c.Sketches.HLL.Registers > 65536 {
		add("sketches.hll.registers", "must be a power of two in [64,65536]", c.Sketches.HLL.Registers, "power of two in [64,65536]")
	}

	// eliminations.defaultScope / rebuildOnStale / staleResponse
	if !isOneOf(c.Eliminations.DefaultScope, "session", "project") {
		add("eliminations.defaultScope", "must be one of the allowed values", c.Eliminations.DefaultScope, "session|project")
	}
	if !isOneOf(c.Eliminations.RebuildOnStale, "nextIdle", "immediate", "never") {
		add("eliminations.rebuildOnStale", "must be one of the allowed values", c.Eliminations.RebuildOnStale, "nextIdle|immediate|never")
	}
	if !isOneOf(c.Eliminations.StaleResponse, "flag", "drop") {
		add("eliminations.staleResponse", "must be one of the allowed values", c.Eliminations.StaleResponse, "flag|drop")
	}

	// retrieval.defaultSpan ∈ {minimal,full} ; retrieval.promoteAfterExpansions ≥ 1
	if !isOneOf(c.Retrieval.DefaultSpan, "minimal", "full") {
		add("retrieval.defaultSpan", "must be one of the allowed values", c.Retrieval.DefaultSpan, "minimal|full")
	}
	if c.Retrieval.PromoteAfterExpansions < 1 {
		add("retrieval.promoteAfterExpansions", "must be at least 1", c.Retrieval.PromoteAfterExpansions, ">= 1")
	}

	// selection.slicing ∈ {thin,full} ; selection.deltaScoring ∈ {cheap,medium,expensive}
	if !isOneOf(c.Selection.Slicing, "thin", "full") {
		add("selection.slicing", "must be one of the allowed values", c.Selection.Slicing, "thin|full")
	}
	if !isOneOf(c.Selection.DeltaScoring, "cheap", "medium", "expensive") {
		add("selection.deltaScoring", "must be one of the allowed values", c.Selection.DeltaScoring, "cheap|medium|expensive")
	}

	// selection.submodular.lambda ≥ 0
	if c.Selection.Submodular.Lambda < 0 {
		add("selection.submodular.lambda", "must be at least 0", c.Selection.Submodular.Lambda, ">= 0")
	}

	// eval.minSessions ≥ 1
	if c.Eval.MinSessions < 1 {
		add("eval.minSessions", "must be at least 1", c.Eval.MinSessions, ">= 1")
	}

	// runtime.mode ∈ {auto,full,passive,off}
	if !isOneOf(c.Runtime.Mode, "auto", "full", "passive", "off") {
		add("runtime.mode", "must be one of the allowed values", c.Runtime.Mode, "auto|full|passive|off")
	}

	// runtime.hotPath.budgetMs > 0 ; breachWindows ≥ 1 ; maxPayloadBytes ≥ 4096
	if c.Runtime.HotPath.BudgetMs <= 0 {
		add("runtime.hotPath.budgetMs", "must be greater than 0", c.Runtime.HotPath.BudgetMs, "> 0")
	}
	if c.Runtime.HotPath.BreachWindows < 1 {
		add("runtime.hotPath.breachWindows", "must be at least 1", c.Runtime.HotPath.BreachWindows, ">= 1")
	}
	if c.Runtime.HotPath.MaxPayloadBytes < 4096 { //nomagic:allow validation bound, not a default
		add("runtime.hotPath.maxPayloadBytes", "must be at least 4096", c.Runtime.HotPath.MaxPayloadBytes, ">= 4096")
	}

	// runtime.logging.level ∈ {debug,info,warn,error}
	if !isOneOf(c.Runtime.Logging.Level, "debug", "info", "warn", "error") {
		add("runtime.logging.level", "must be one of the allowed values", c.Runtime.Logging.Level, "debug|info|warn|error")
	}

	// runtime.rehydrate.minTokens ≥ 1 ; minTokens ≤ maxTokens ; skillIndexTokens ≥ 1 ; eliminationsTopN ≥ 1
	if c.Runtime.Rehydrate.MinTokens < 1 {
		add("runtime.rehydrate.minTokens", "must be at least 1", c.Runtime.Rehydrate.MinTokens, ">= 1")
	} else if c.Runtime.Rehydrate.MinTokens > c.Runtime.Rehydrate.MaxTokens {
		add("runtime.rehydrate.minTokens", "must be less than or equal to runtime.rehydrate.maxTokens",
			c.Runtime.Rehydrate.MinTokens, fmt.Sprintf("<= %d", c.Runtime.Rehydrate.MaxTokens))
	}
	if c.Runtime.Rehydrate.SkillIndexTokens < 1 {
		add("runtime.rehydrate.skillIndexTokens", "must be at least 1", c.Runtime.Rehydrate.SkillIndexTokens, ">= 1")
	}
	if c.Runtime.Rehydrate.EliminationsTopN < 1 {
		add("runtime.rehydrate.eliminationsTopN", "must be at least 1", c.Runtime.Rehydrate.EliminationsTopN, ">= 1")
	}

	// runtime.mcp.spanWidenLines ≥ 0 ; maxResponseBytes ≥ 4096
	if c.Runtime.MCP.SpanWidenLines < 0 {
		add("runtime.mcp.spanWidenLines", "must be at least 0", c.Runtime.MCP.SpanWidenLines, ">= 0")
	}
	if c.Runtime.MCP.MaxResponseBytes < 4096 { //nomagic:allow validation bound, not a default
		add("runtime.mcp.maxResponseBytes", "must be at least 4096", c.Runtime.MCP.MaxResponseBytes, ">= 4096")
	}

	// runtime.tokens.* charsPerToken ∈ [1,20]
	checkCharsPerToken := func(key string, v float64) {
		if v < 1 || v > 20 {
			add(key, "out of range", v, "[1,20]")
		}
	}
	checkCharsPerToken("runtime.tokens.proseCharsPerToken", c.Runtime.Tokens.ProseCharsPerToken)
	checkCharsPerToken("runtime.tokens.codeCharsPerToken", c.Runtime.Tokens.CodeCharsPerToken)
	checkCharsPerToken("runtime.tokens.jsonCharsPerToken", c.Runtime.Tokens.JSONCharsPerToken)
	checkCharsPerToken("runtime.tokens.diffCharsPerToken", c.Runtime.Tokens.DiffCharsPerToken)
	checkCharsPerToken("runtime.tokens.binaryCharsPerToken", c.Runtime.Tokens.BinaryCharsPerToken)

	// runtime.tokens.imagePixelsPerToken ≥ 1 ; pdfTokensPerPage ≥ 1
	if c.Runtime.Tokens.ImagePixelsPerToken < 1 {
		add("runtime.tokens.imagePixelsPerToken", "must be at least 1", c.Runtime.Tokens.ImagePixelsPerToken, ">= 1")
	}
	if c.Runtime.Tokens.PDFTokensPerPage < 1 {
		add("runtime.tokens.pdfTokensPerPage", "must be at least 1", c.Runtime.Tokens.PDFTokensPerPage, ">= 1")
	}

	// runtime.tokens.calibrationMin ∈ (0,1] ; calibrationMax ≥ 1 ; calibrationAlpha ∈ (0,1]
	if c.Runtime.Tokens.CalibrationMin <= 0 || c.Runtime.Tokens.CalibrationMin > 1 {
		add("runtime.tokens.calibrationMin", "out of range", c.Runtime.Tokens.CalibrationMin, "(0,1]")
	}
	if c.Runtime.Tokens.CalibrationMax < 1 {
		add("runtime.tokens.calibrationMax", "must be at least 1", c.Runtime.Tokens.CalibrationMax, ">= 1")
	}
	if c.Runtime.Tokens.CalibrationAlpha <= 0 || c.Runtime.Tokens.CalibrationAlpha > 1 {
		add("runtime.tokens.calibrationAlpha", "out of range", c.Runtime.Tokens.CalibrationAlpha, "(0,1]")
	}

	// runtime.budgets.* > 0
	if c.Runtime.Budgets.L0IngestMs <= 0 {
		add("runtime.budgets.l0IngestMs", "must be greater than 0", c.Runtime.Budgets.L0IngestMs, "> 0")
	}
	if c.Runtime.Budgets.L0ProcessMs <= 0 {
		add("runtime.budgets.l0ProcessMs", "must be greater than 0", c.Runtime.Budgets.L0ProcessMs, "> 0")
	}
	if c.Runtime.Budgets.CheckpointFinalizeMs <= 0 {
		add("runtime.budgets.checkpointFinalizeMs", "must be greater than 0", c.Runtime.Budgets.CheckpointFinalizeMs, "> 0")
	}
	if c.Runtime.Budgets.MCPToolCallMs <= 0 {
		add("runtime.budgets.mcpToolCallMs", "must be greater than 0", c.Runtime.Budgets.MCPToolCallMs, "> 0")
	}
	if c.Runtime.Budgets.HookDegradedMs <= 0 {
		add("runtime.budgets.hookDegradedMs", "must be greater than 0", c.Runtime.Budgets.HookDegradedMs, "> 0")
	}

	// runtime.telemetry.enabled must be false — hardwired; a true value is a Violation
	if c.Runtime.Telemetry.Enabled {
		add("runtime.telemetry.enabled", "must be false: telemetry is hardwired off", c.Runtime.Telemetry.Enabled, false)
	}

	return out
}

// knownCanonicalizeClasses is the closed set store.canonicalize.strip may draw from (§8.1). It
// is a superset of the six classes Defaults() strips: crlf and paths are valid but off by
// default.
var knownCanonicalizeClasses = map[string]bool{
	"timestamps": true, "ansi": true, "pids": true, "addresses": true,
	"tmpPaths": true, "durations": true, "crlf": true, "paths": true,
}

// checkpointFieldSet is checkpointFieldEnum (config.go) as a lookup set.
var checkpointFieldSet = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Split(checkpointFieldEnum, "|") {
		m[f] = true
	}
	return m
}()

// isOneOf reports whether v is exactly one of allowed.
func isOneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// allKnown reports whether every element of vals is a key of allowed.
func allKnown(vals []string, allowed map[string]bool) bool {
	for _, v := range vals {
		if !allowed[v] {
			return false
		}
	}
	return true
}

// isPowerOfTwo reports whether n is a positive power of two.
func isPowerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }

// tierOverlap returns every checkpoint field that appears in more than one of
// never/late/first, in first-seen order across never, then late, then first.
func tierOverlap(t TiersCfg) []string {
	seen := map[string]bool{}
	var dup []string
	for _, list := range [][]string{t.Never, t.Late, t.First} {
		for _, v := range list {
			if seen[v] {
				dup = append(dup, v)
			}
			seen[v] = true
		}
	}
	return dup
}

// tierGaps returns every field in known that appears in none of never/late/first, sorted for
// deterministic output.
func tierGaps(t TiersCfg, known map[string]bool) []string {
	present := map[string]bool{}
	for _, list := range [][]string{t.Never, t.Late, t.First} {
		for _, v := range list {
			present[v] = true
		}
	}
	var missing []string
	for f := range known {
		if !present[f] {
			missing = append(missing, f)
		}
	}
	sort.Strings(missing)
	return missing
}

// sortedKeys returns the keys of m in sorted order, used only to render a stable message string.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ViolationsFromWarnings decodes the subset of ws produced by Load's per-leaf fallback (the
// "invalid value, using default: <got> not in <want>" messages) back into typed Violations, so a
// caller that already has Load's []Warning does not need a second Load-and-Validate pass to get
// the §11.3 list. Warnings from other sources (unknown key, unparseable file) do not match the
// fallback message shape and are silently skipped: they were never Violations to begin with.
//
// Got/Want on the decoded Violation are the %v-formatted strings from the original message, not
// the original typed values — Warning.Message is text, so that is the most a pure decoder over
// it can recover. That is sufficient for a caller (cli.LoadConfigAndReport) that only needs to
// log and persist the violation, not recompute it.
func ViolationsFromWarnings(ws []Warning) []Violation {
	const prefix = "invalid value, using default: "
	const sep = " not in "
	var out []Violation
	for _, w := range ws {
		if !strings.HasPrefix(w.Message, prefix) {
			continue
		}
		rest := strings.TrimPrefix(w.Message, prefix)
		got, want, ok := strings.Cut(rest, sep)
		if !ok {
			continue
		}
		out = append(out, Violation{Key: w.Key, Message: w.Message, Got: got, Want: want})
	}
	return out
}
