package config

// Defaults returns the built-in configuration: Qompack.md Appendix C reproduced exactly, plus
// the §11.5 runtime extension namespace. This is the only file in the repository exempt from the
// nomagic literal lint (D11, §11.6) — every number below is a config default, not a magic
// constant duplicating one, so this is precisely the one place they are allowed to appear as Go
// literals. TestDefaults_MatchesAppendixCVerbatim asserts json.Marshal of this value, with the
// "runtime" key removed, deep-equals testdata/golden/config/appendix-c.jsonc byte-for-byte.
func Defaults() Config {
	return Config{
		Store: StoreCfg{
			Chunk: ChunkCfg{
				Min:    1024,
				Target: 4096,
				Max:    16384,
			},
			Compression: "zstd",
			Retention: RetentionCfg{
				Days:     30,
				Sessions: 10,
			},
			Canonicalize: CanonicalizeCfg{
				Enabled: true,
				Strip:   []string{"timestamps", "ansi", "pids", "addresses", "tmpPaths", "durations"},
				MinHash: MinHashCfg{
					Enabled:          true,
					Permutations:     128,
					NearDupThreshold: 0.9,
				},
			},
		},
		Scheduler: SchedulerCfg{
			SoftFloorPct:      0.55,
			HardCeilingMargin: 20000,
			YoungDaly: YoungDalyCfg{
				Enabled:              true,
				MeasuredDeltaSeconds: nil,
			},
			Changepoint: ChangepointCfg{
				HazardRate: 0.004,
				Features:   []string{"paths", "tools", "time", "todos"},
			},
			Cache: CacheCfg{
				ReadMultiplier:  0.1,
				WriteMultiplier: 1.25,
				TTLSeconds:      300,
			},
			Idle: IdleCfg{
				DetectAfterSeconds: 120,
				BackgroundWork:     true,
				DeepCutWhenCold:    true,
			},
		},
		Checkpoint: CheckpointCfg{
			BudgetTokens:               12000,
			IncrementalSpanInstruction: true,
			Frontier: FrontierCfg{
				AdvanceOnSegmentClose: true,
				MaxResidualTokens:     20000,
			},
			Tiers: TiersCfg{
				Never: []string{"invariants", "user_intent", "eliminated"},
				Late:  []string{"decisions", "open_questions", "current_work"},
				First: []string{"pointers", "narrative"},
			},
		},
		Sketches: SketchesCfg{
			Bloom: BloomCfg{
				Capacity: 10000,
				FPRate:   0.01,
			},
			CMS: CMSCfg{
				Epsilon:              0.001,
				Delta:                0.01,
				WarmStartFromProject: true,
			},
			HLL: HLLCfg{
				Registers: 2048,
			},
		},
		Eliminations: EliminationsCfg{
			RequireEvidence: true,
			DefaultScope:    "session",
			RebuildOnStale:  "nextIdle",
			StaleResponse:   "flag",
		},
		Retrieval: RetrievalCfg{
			EphemeralResults:       true,
			DefaultSpan:            "minimal",
			PromoteAfterExpansions: 2,
		},
		Selection: SelectionCfg{
			Slicing:      "thin",
			DeltaScoring: "cheap",
			Submodular: SubmodularCfg{
				Lambda:     0.4,
				LazyGreedy: true,
				// Enabled is derived by Load from Runtime.Selection.SubmodularEnabled; Defaults()
				// leaves it false, matching that field's own default below.
				Enabled: false,
			},
		},
		Eval: EvalCfg{
			ReplayOnPhaseGate: true,
			MinSessions:       20,
		},
		Runtime: RuntimeCfg{
			Mode: "auto",
			Daemon: DaemonCfg{
				Enabled:           true,
				IdleExitSeconds:   1800,
				MaxSessions:       8,
				AckDeadlineMs:     8,
				ConnectDeadlineMs: 5,
			},
			HotPath: HotPathCfg{
				BudgetMs:        15,
				BreachWindows:   3,
				SpoolOnBreach:   true,
				MaxPayloadBytes: 1048576,
			},
			Logging: LogCfg{
				Level:     "info",
				MaxFileMB: 10,
				MaxFiles:  5,
			},
			Redact: RedactCfg{
				Enabled:  true,
				Patterns: []string{},
			},
			Telemetry: TelemetryCfg{
				Enabled: false,
			},
			Rehydrate: RehydrateCfg{
				MinTokens:        8000,
				MaxTokens:        12000,
				SkillIndexTokens: 450,
				EliminationsTopN: 8,
			},
			MCP: MCPCfg{
				SpanWidenLines:   40,
				MaxResponseBytes: 262144,
			},
			Budgets: BudgetsCfg{
				L0IngestMs:           2,
				L0ProcessMs:          50,
				CheckpointFinalizeMs: 2000,
				MCPToolCallMs:        250,
			},
			Selection: RSelectionCfg{
				SubmodularEnabled: false,
			},
			Tokens: RTokensCfg{
				ProseCharsPerToken:  4.0,
				CodeCharsPerToken:   3.6,
				JSONCharsPerToken:   3.2,
				DiffCharsPerToken:   3.4,
				BinaryCharsPerToken: 3.0,
				ImagePixelsPerToken: 750,
				ImageMaxTokens:      1600,
				PDFTokensPerPage:    1800,
				CalibrationMin:      0.6,
				CalibrationMax:      1.6,
				CalibrationAlpha:    0.2,
			},
		},
	}
}
