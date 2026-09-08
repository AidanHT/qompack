package config

// RuntimeCfg is the §11.5 extension namespace: process-level concerns the Appendix C schema does
// not cover because they belong to the daemon/hook-client architecture rather than to the
// compaction algorithm. Budgets, Selection and Tokens are SP-01 additions beyond the §11.5
// document reproduced in 00-ARCHITECTURE.md: Budgets gives the §11.3/§2.4 latency budgets B-B
// through B-F config keys (B-A already has one at hotPath.budgetMs; B-D is reported, never
// gated, so it has no key), Selection carries the closing-note-3 ship-order gate that derives
// SelectionCfg.Submodular.Enabled, and Tokens carries the baseline token-estimator constants
// (G10.2 groundwork). Scheduler is SP-12's §11.5 cache-regime block: it is documented in the
// §11.5 text 00-ARCHITECTURE.md reproduces, so it sits with the other §11.5 blocks above
// Budgets rather than with the SP-01 additions below it.
type RuntimeCfg struct {
	Mode      string        `json:"mode" doc:"overall operating mode" enum:"auto|full|passive|off" sec:"00-ARCH §12"`
	Daemon    DaemonCfg     `json:"daemon"`
	HotPath   HotPathCfg    `json:"hotPath"`
	Logging   LogCfg        `json:"logging"`
	Redact    RedactCfg     `json:"redact"`
	Telemetry TelemetryCfg  `json:"telemetry"`
	Rehydrate RehydrateCfg  `json:"rehydrate"`
	MCP       MCPCfg        `json:"mcp"`
	Scheduler RSchedulerCfg `json:"scheduler"`
	Migration MigrationCfg  `json:"migration"`
	Phase7    Phase7Cfg     `json:"phase7"`
	Budgets   BudgetsCfg    `json:"budgets"`
	Selection RSelectionCfg `json:"selection"`
	Tokens    RTokensCfg    `json:"tokens"`
}

// DaemonCfg controls the resident per-project daemon's lifecycle (00-ARCHITECTURE §2.4).
type DaemonCfg struct {
	Enabled           bool `json:"enabled"           doc:"run the resident per-project daemon" sec:"00-ARCH §2.4"`
	IdleExitSeconds   int  `json:"idleExitSeconds"   doc:"seconds with zero live sessions before the daemon exits" sec:"00-ARCH §2.4"`
	MaxSessions       int  `json:"maxSessions"       doc:"maximum concurrent sessions the daemon tracks" sec:"00-ARCH §2.4"`
	AckDeadlineMs     int  `json:"ackDeadlineMs"     doc:"deadline for the daemon's one-byte ACK on the hot path" sec:"00-ARCH §2.4"`
	ConnectDeadlineMs int  `json:"connectDeadlineMs" doc:"deadline for a hot-path client to connect to the daemon" sec:"00-ARCH §2.4"`
}

// HotPathCfg controls the B-A hook-latency budget and its overrun fallback (§8.1).
type HotPathCfg struct {
	BudgetMs        int  `json:"budgetMs"        doc:"B-A hot-path latency budget in milliseconds" rng:"(0,∞)" sec:"§8.1"`
	BreachWindows   int  `json:"breachWindows"   doc:"consecutive 512-sample windows over budget before the daemon spools instead of syncing" rng:"[1,∞)" sec:"§8.1"`
	SpoolOnBreach   bool `json:"spoolOnBreach"   doc:"degrade to spool-only mode when the hot-path budget is breached" sec:"§8.1"`
	MaxPayloadBytes int  `json:"maxPayloadBytes" doc:"maximum NDJSON request line size accepted by the daemon" rng:"[4096,∞)" sec:"00-ARCH §2.4"`
}

// LogCfg controls internal/logging's level and rotation (00-ARCHITECTURE §5.2).
type LogCfg struct {
	Level     string `json:"level"     doc:"minimum log level written to the day log" enum:"debug|info|warn|error" sec:"00-ARCH §5.2"`
	MaxFileMB int    `json:"maxFileMB" doc:"log file size in MB before rotation" sec:"00-ARCH §5.2"`
	MaxFiles  int    `json:"maxFiles"  doc:"number of rotated log files retained" sec:"00-ARCH §5.2"`
}

// RedactCfg controls secret scrubbing applied before content enters the store (00-ARCHITECTURE
// §5.23).
type RedactCfg struct {
	Enabled  bool     `json:"enabled"  doc:"scrub secrets before content enters the store" sec:"00-ARCH §5.23"`
	Patterns []string `json:"patterns" doc:"additional user-supplied secret-detection patterns" sec:"00-ARCH §5.23"`
}

// TelemetryCfg exists only to say telemetry is off. Enabled is hardwired false: a true value is
// a Validate() Violation, never a config the plugin will run with (§7.1: Qompack talks to
// nobody).
type TelemetryCfg struct {
	Enabled bool `json:"enabled" doc:"send telemetry; hardwired off, the key exists only to say so" sec:"§7.1"`
}

// RehydrateCfg controls the L5 rehydrator's context-injection budget (§8.6).
type RehydrateCfg struct {
	MinTokens        int `json:"minTokens"        doc:"lower bound of the rehydration budget" rng:"[1,maxTokens]" sec:"§8.6"`
	MaxTokens        int `json:"maxTokens"        doc:"upper bound of the rehydration budget" rng:"[minTokens,∞)" sec:"§8.6"`
	SkillIndexTokens int `json:"skillIndexTokens" doc:"token budget for the compact skill index" rng:"[1,∞)" sec:"§8.6"`
	EliminationsTopN int `json:"eliminationsTopN" doc:"number of eliminated approaches surfaced verbatim in the rehydrated digest" rng:"[1,∞)" sec:"§8.6"`
}

// MCPCfg controls the L6 retrieval tools' span-widening and response-size limits (§8.7).
type MCPCfg struct {
	SpanWidenLines   int `json:"spanWidenLines"   doc:"lines to widen a minimal span by when the caller requests more context" rng:"[0,∞)" sec:"§8.7"`
	MaxResponseBytes int `json:"maxResponseBytes" doc:"maximum bytes an MCP tool response may return" rng:"[4096,∞)" sec:"§8.7"`
}

// RSchedulerCfg is the §11.5 cache-regime namespace: scheduler knobs that describe the PROMPT
// CACHE the host happens to be running under, which is a property of the process the daemon
// observes rather than of the compaction algorithm Appendix C configures. None of it changes an
// Appendix C default — the keys sit beside `scheduler.cache` and leave its values untouched.
type RSchedulerCfg struct {
	Cache RSchedulerCacheCfg `json:"cache"`
}

// RSchedulerCacheCfg carries the two cache-regime keys 00-ARCHITECTURE.md §11.5 documents for
// SP-12's `cache_expiring` trigger: when to fire relative to a KNOWN TTL, and what upper bound to
// assume when the regime cannot be identified at all.
//
// assumeMaxTTLSeconds is bounded below by scheduler.cache.ttlSeconds rather than by a literal,
// because an assumed upper bound that sits under the TTL the scheduler already knows about would
// make the unknown-regime case fire sooner than the known one — the opposite of what an upper
// bound is for. The relation is cross-key, so Validate enforces it and the rng tag names the key
// it is measured against, the same way runtime.rehydrate's min/max pair does.
type RSchedulerCacheCfg struct {
	ExpiringTriggerFraction float64 `json:"expiringTriggerFraction" doc:"fraction of a KNOWN prompt-cache TTL past which the scheduler fires while the prefix is still readable (cache_expiring trigger)" rng:"(0,1)" sec:"00-ARCH §11.5 / Qompack.md §5.4"`
	AssumeMaxTTLSeconds     int     `json:"assumeMaxTTLSeconds"     doc:"upper TTL bound the scheduler assumes when the cache regime cannot be identified"                                             rng:"[scheduler.cache.ttlSeconds,∞)" sec:"00-ARCH §11.5 / Qompack.md §5.4"`
}

// MigrationCfg is SP-19's §11.5 block (Qompack.md v1.5 Appendix C, rows "Schema/config version"
// and "Recording/reinjection/replacement/experiments"): an explicit version for the block itself
// and one independent switch per capability the migration keeps off until its gate passes. None
// of it changes an Appendix C key. Recording has no switch here because runtime.mode already is
// one ("passive" records without acting; "off" does nothing).
//
// A switch whose gate has not passed in this build is refused by Validate when set true — Load
// then restores the default and warns — so the only way to enable a gated capability is the
// reviewed commit that flips its gate in MigrationGates (migration.go), never a config edit against
// a build that cannot honour it. The one switch that is on by default,
// reinjection.sessionStartCompact, is the tested SessionStart adapter SP-11 ships; turning it off
// is the independent kill switch v1.5 Appendix C asks for, consumed by internal/daemon's rehydrate
// service.
type MigrationCfg struct {
	SettingsVersion int                     `json:"settingsVersion" doc:"version of the runtime.migration block; a file written for a newer version has its whole block reset to defaults, so unknown future switches stay off" rng:"[1,1]" sec:"Qompack.md v1.5 Appendix C / SP-19 M0"`
	Capture         MigrationCaptureCfg     `json:"capture"`
	Publication     MigrationPublicationCfg `json:"publication"`
	Reinjection     MigrationReinjectionCfg `json:"reinjection"`
	Replacement     MigrationReplacementCfg `json:"replacement"`
	Compaction      MigrationCompactionCfg  `json:"compaction"`
	Experiments     MigrationExperimentsCfg `json:"experiments"`
}

// MigrationCaptureCfg is SP-20 M1's capture switch.
type MigrationCaptureCfg struct {
	RawEvidence bool `json:"rawEvidence" doc:"capture permitted raw host payload bytes before any transform (SP-20 M1); refused until the M1 gate passes" sec:"Qompack.md v1.5 §8.1 / SP-20 M1-01"`
}

// MigrationPublicationCfg is SP-20 M1's publication switch.
type MigrationPublicationCfg struct {
	DurableFrontier bool `json:"durableFrontier" doc:"publish references and the committed frontier only behind an acknowledged durable object write (SP-20 M1); refused until the M1 gate passes" sec:"Qompack.md v1.5 §8.2 / SP-20 M1-02"`
}

// MigrationReinjectionCfg is the injection kill switch.
type MigrationReinjectionCfg struct {
	SessionStartCompact bool `json:"sessionStartCompact" doc:"reinject the rehydration payload through SessionStart source=compact additionalContext, the one tested injection adapter; false disables injection without touching recording" sec:"Qompack.md v1.5 §8.6 / 00-ARCH §12.1"`
}

// MigrationReplacementCfg is SP-21 M4's admission switch.
type MigrationReplacementCfg struct {
	NewResult bool `json:"newResult" doc:"replace newly delivered tool results with Qompack handles (SP-21 M4); refused until the M4 gate passes" sec:"Qompack.md v1.5 §8.7 / SP-21"`
}

// MigrationCompactionCfg holds the two native-compaction controls, both off: one gated, one
// hardwired.
type MigrationCompactionCfg struct {
	AutomaticVeto      bool `json:"automaticVeto"      doc:"let the scheduler veto an automatic compaction for optimization; refused: the recovery/proactive distinction is unverified (SP-19 M0-03)" sec:"Qompack.md v1.5 §7.3 / 00-ARCH §12.1"`
	BlockManualCompact bool `json:"blockManualCompact" doc:"block a manual /compact for optimization; hardwired false, the key exists only to say so" sec:"Qompack.md v1.5 §12 / 00-ARCH §12.1"`
}

// MigrationExperimentsCfg gates the SP-15/SP-16 experimental policies.
type MigrationExperimentsCfg struct {
	Enabled bool `json:"enabled" doc:"enable experimental representation and optimizer policies (SP-15/SP-16); refused until their gates pass" sec:"Qompack.md v1.5 Appendix C / SP-15, SP-16"`
}

// Phase7Cfg is SP-16's §11.5 block: the phase-7 refinement experiments — scope-qualified reuse,
// bounded retrieval and demand-based promotion — plus the caps that bound them.
//
// It is a SIBLING of MigrationCfg, not a part of it, because the two answer different questions.
// runtime.migration says which parts of the shipped pipeline a build may run at all; this says
// which OPTIONAL refinements sit on top of it. Folding SP-16's switches into the migration block
// would make "reset the whole block so unknown future switches stay off" reset the pipeline's own
// controls whenever a refinement's schema moved, which is a much bigger hammer than the situation
// calls for. Each block therefore carries its own settingsVersion and is reset independently.
//
// Every switch here defaults FALSE and is gated in migration.go's table, and the block's presence
// is not a promise that any of it will ship: SP-16's own plan says its optional policies stay
// disabled where they cannot beat a simple baseline. What the block guarantees is only that a
// setting written for a refinement remains READABLE — SP-19's versioned validation and deprecation
// path applies to it unchanged — not that the refinement exists.
//
// The caps are separate from the switches on purpose. A cap has to be readable and enforceable
// even while its switch is off, because the code that would consult it must not carry a literal
// bound of its own (§11.6): a bound nobody can configure is a bound nobody can lower after it
// turns out to be wrong.
type Phase7Cfg struct {
	SettingsVersion int                `json:"settingsVersion" doc:"version of the runtime.phase7 block; a file written for a newer version has its whole block reset to defaults, so unknown future refinements stay off" rng:"[1,1]" sec:"Qompack.md v1.5 Appendix C / SP-16 M6"`
	Reuse           Phase7ReuseCfg     `json:"reuse"`
	Retrieval       Phase7RetrievalCfg `json:"retrieval"`
	Filters         Phase7FiltersCfg   `json:"filters"`
}

// Phase7ReuseCfg holds SP-16 §1's scope-qualified reuse switches.
type Phase7ReuseCfg struct {
	ScopedCandidates bool `json:"scopedCandidates" doc:"offer eliminations recorded outside this session as scope-qualified reusable candidates (SP-16 §1); refused until the M6-G16-A gate passes" sec:"Qompack.md v1.5 §8.3 / SP-16 M6"`
	WarmPrior        bool `json:"warmPrior"        doc:"seed a new session with a labeled statistical prior from earlier ones (SP-16 §1); refused until the M6-G16-D gate passes" sec:"Qompack.md v1.5 §5 / SP-16 M6"`
}

// Phase7RetrievalCfg holds SP-16 §2's bounded retrieval switches and the caps that bound them.
//
// The caps apply whether or not Reminders is on, and they are counted per session rather than per
// trigger for the ones a user actually experiences: a bound that reset on every changed reference
// would not bound anything a reader notices.
type Phase7RetrievalCfg struct {
	Reminders       bool `json:"reminders"       doc:"emit bounded retrieval reminders when references change or errors repeat (SP-16 §2); refused until the M6-G16-B gate passes" sec:"Qompack.md v1.5 §8.7 / SP-16 M6"`
	DemandPromotion bool `json:"demandPromotion" doc:"let observed demand promote a representation in the NEXT Qompack injection (SP-16 §2); refused until the M6-G16-C gate passes" sec:"Qompack.md v1.5 §8.6 / SP-16 M6"`

	MaxRemindersPerSession int `json:"maxRemindersPerSession" doc:"hard cap on retrieval reminders surfaced in one session; 0 emits none"                       rng:"[0,∞)" sec:"Qompack.md v1.5 §8.7 / SP-16 M6"`
	MaxAttemptsPerTrigger  int `json:"maxAttemptsPerTrigger"  doc:"hard cap on retrieval attempts one trigger may make before it stops and reports"              rng:"[1,∞)" sec:"Qompack.md v1.5 §8.7 / SP-16 M6"`
	MaxQueueDepth          int `json:"maxQueueDepth"          doc:"hard cap on queued retrieval work; a full queue drops new work rather than growing unbounded" rng:"[1,∞)" sec:"Qompack.md v1.5 §8.7 / SP-16 M6"`
}

// Phase7FiltersCfg holds SP-16 §3's optional per-segment filter acceleration.
//
// There is no false-positive-rate key here, and that absence is the point: §3 and ledger row A06
// both say a fixed filter cannot hold an unbounded insertion stream at a fixed error rate, so a
// key promising one would be a promise the mechanism cannot keep. Coverage and generation are
// published with the index instead, and an incomplete filter is bypassed rather than tuned.
type Phase7FiltersCfg struct {
	SegmentBloom bool `json:"segmentBloom" doc:"build a per-segment bloom filter alongside each segment index to skip segments that cannot match (SP-16 §3); refused until the M6-G16-E gate passes" sec:"Qompack.md v1.5 §6 / SP-16 M6"`
}

// BudgetsCfg gives the §11.3/§2.4 latency budgets B-B through B-F config keys, plus B-G's. B-A
// already has a key at hotPath.budgetMs; duplicating it here would create two sources of truth for
// the number §11.3 names. B-D is reported, never gated, and reports the host's process-creation
// cost, which no configuration can bound, so it has no key.
//
// hookDegradedMs is B-G's, and B-G is not one of §2.4's budgets: it bounds the synchronous spool
// append a hook pays when the daemon is unreachable (internal/obs/budgets.go's BG entry). It gets
// its own key rather than being derived from hotPath.budgetMs precisely because the two must move
// independently — a tightened hot-path budget is a statement about a daemon round trip and must
// not silently tighten a bound on the filesystem.
type BudgetsCfg struct {
	L0IngestMs           int `json:"l0IngestMs"           doc:"B-B latency budget: daemon read to WAL append returned"                      rng:"(0,∞)" sec:"00-ARCH §2.4"`
	L0ProcessMs          int `json:"l0ProcessMs"          doc:"B-C latency budget: WAL to fully chunked, stored, DAG/sketches updated"       rng:"(0,∞)" sec:"00-ARCH §2.4"`
	CheckpointFinalizeMs int `json:"checkpointFinalizeMs" doc:"B-E latency budget: PreCompact entry to exit"                                 rng:"(0,∞)" sec:"00-ARCH §2.4"`
	MCPToolCallMs        int `json:"mcpToolCallMs"        doc:"B-F latency budget: MCP request to response"                                  rng:"(0,∞)" sec:"00-ARCH §2.4"`
	HookDegradedMs       int `json:"hookDegradedMs"       doc:"B-G latency budget: the spool append a hook pays when the daemon is unreachable" rng:"(0,∞)" sec:"00-ARCH §12.3"`
}

// RSelectionCfg carries the closing-note-3 ship-order gate: submodular selection must not ship
// before p-selection. Load derives config.SelectionCfg.Submodular.Enabled from SubmodularEnabled.
//
// LoopWarningsEnabled is SP-15's second, INDEPENDENT switch (plans/sdd/V5-SP-15/contract.md §7).
// The two gate different things and share only this struct: selection decides what a rehydration
// carries, and a bad selection costs tokens; loop warnings decide whether the session is told it
// may be going in circles, and a bad warning costs the user's trust in every later warning. They
// have to be disableable one at a time, because the rollback for one is not the rollback for the
// other. Both default false, so the shipped default is exactly the pre-SP-15 behaviour.
type RSelectionCfg struct {
	SubmodularEnabled   bool `json:"submodularEnabled"   doc:"ship-order gate: enable submodular selection; refused without p-selection (closing-note-3)" sec:"Closing note"`
	LoopWarningsEnabled bool `json:"loopWarningsEnabled" doc:"enable state-aware loop warnings; warning-only, bounded and deduplicated when on"          sec:"00-ARCH §5.11"`
}

// RTokensCfg carries the baseline token-estimator constants (G10.2 groundwork; internal/tokens
// is a later commit in this same subplan, but the config keys live here so no estimator ever
// hardcodes them).
type RTokensCfg struct {
	ProseCharsPerToken  float64 `json:"proseCharsPerToken"  doc:"baseline characters-per-token estimate for prose"                 rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	CodeCharsPerToken   float64 `json:"codeCharsPerToken"   doc:"baseline characters-per-token estimate for source code"           rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	JSONCharsPerToken   float64 `json:"jsonCharsPerToken"   doc:"baseline characters-per-token estimate for JSON"                  rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	DiffCharsPerToken   float64 `json:"diffCharsPerToken"   doc:"baseline characters-per-token estimate for unified diffs"         rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`
	BinaryCharsPerToken float64 `json:"binaryCharsPerToken" doc:"baseline characters-per-token estimate for opaque binary content" rng:"[1,20]" sec:"00-ARCH §10 (G10.2)"`

	ImagePixelsPerToken int `json:"imagePixelsPerToken" doc:"pixels per token used to estimate image cost from dimensions" rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`
	ImageMaxTokens      int `json:"imageMaxTokens"      doc:"cap on estimated tokens for a single image"                   sec:"00-ARCH §10 (G10.2)"`
	PDFTokensPerPage    int `json:"pdfTokensPerPage"    doc:"tokens charged per PDF page"                                  rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`

	CalibrationMin   float64 `json:"calibrationMin"   doc:"lower clamp on the per-project calibration factor"                        rng:"(0,1]" sec:"00-ARCH §10 (G10.2)"`
	CalibrationMax   float64 `json:"calibrationMax"   doc:"upper clamp on the per-project calibration factor"                        rng:"[1,∞)" sec:"00-ARCH §10 (G10.2)"`
	CalibrationAlpha float64 `json:"calibrationAlpha" doc:"exponential-moving-average weight applied to each new calibration sample" rng:"(0,1]" sec:"00-ARCH §10 (G10.2)"`
}
