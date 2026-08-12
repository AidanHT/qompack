package config

// RuntimeCfg is the §11.5 extension namespace: process-level concerns the Appendix C schema does
// not cover because they belong to the daemon/hook-client architecture rather than to the
// compaction algorithm. Budgets, Selection and Tokens are SP-01 additions beyond the §11.5
// document reproduced in 00-ARCHITECTURE.md: Budgets gives the §11.3/§2.4 latency budgets B-B
// through B-F config keys (B-A already has one at hotPath.budgetMs; B-D is reported, never
// gated, so it has no key), Selection carries the closing-note-3 ship-order gate that derives
// SelectionCfg.Submodular.Enabled, and Tokens carries the baseline token-estimator constants
// (G10.2 groundwork).
type RuntimeCfg struct {
	Mode      string        `json:"mode" doc:"overall operating mode" enum:"auto|full|passive|off" sec:"00-ARCH §12"`
	Daemon    DaemonCfg     `json:"daemon"`
	HotPath   HotPathCfg    `json:"hotPath"`
	Logging   LogCfg        `json:"logging"`
	Redact    RedactCfg     `json:"redact"`
	Telemetry TelemetryCfg  `json:"telemetry"`
	Rehydrate RehydrateCfg  `json:"rehydrate"`
	MCP       MCPCfg        `json:"mcp"`
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

// BudgetsCfg gives the §11.3/§2.4 latency budgets B-B through B-F config keys. B-A already has a
// key at hotPath.budgetMs; duplicating it here would create two sources of truth for the number
// §11.3 names. B-D is reported, never gated, so it has no key.
type BudgetsCfg struct {
	L0IngestMs           int `json:"l0IngestMs"           doc:"B-B latency budget: daemon read to WAL append returned"                      rng:"(0,∞)" sec:"00-ARCH §2.4"`
	L0ProcessMs          int `json:"l0ProcessMs"          doc:"B-C latency budget: WAL to fully chunked, stored, DAG/sketches updated"       rng:"(0,∞)" sec:"00-ARCH §2.4"`
	CheckpointFinalizeMs int `json:"checkpointFinalizeMs" doc:"B-E latency budget: PreCompact entry to exit"                                 rng:"(0,∞)" sec:"00-ARCH §2.4"`
	MCPToolCallMs        int `json:"mcpToolCallMs"        doc:"B-F latency budget: MCP request to response"                                  rng:"(0,∞)" sec:"00-ARCH §2.4"`
}

// RSelectionCfg carries the closing-note-3 ship-order gate: submodular selection must not ship
// before p-selection. Load derives config.SelectionCfg.Submodular.Enabled from this field.
type RSelectionCfg struct {
	SubmodularEnabled bool `json:"submodularEnabled" doc:"ship-order gate: enable submodular selection; refused without p-selection (closing-note-3)" sec:"Closing note"`
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
