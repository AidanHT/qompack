package config_test

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
)

// TestDefaults_MatchesAppendixCVerbatim is the §11.1 golden test: json.Marshal(Defaults()) with
// the "runtime" key removed must deep-equal the Appendix C document byte-for-byte in structure
// and value. It is the single most important requirement of this package — a missing or
// misspelled json tag anywhere in config.go would break it silently otherwise.
func TestDefaults_MatchesAppendixCVerbatim(t *testing.T) {
	golden, err := os.ReadFile("../../testdata/golden/config/appendix-c.jsonc")
	require.NoError(t, err)

	var want map[string]any
	require.NoError(t, json.Unmarshal(config.StripJSONC(golden), &want), "golden fixture itself must be valid JSONC")

	b, err := json.Marshal(config.Defaults())
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))

	_, hasRuntime := got["runtime"]
	require.True(t, hasRuntime, "Defaults() must serialize a runtime key even though Appendix C does not have one")
	delete(got, "runtime")

	require.Equal(t, want, got)
}

// TestDefaults_RuntimeNamespace pins Defaults().Runtime field by field against the §11.5
// document plus the three SP-01 additions (Budgets, Selection, Tokens), the same way
// TestDefaults_MatchesAppendixCVerbatim pins the Appendix C portion.
func TestDefaults_RuntimeNamespace(t *testing.T) {
	rt := config.Defaults().Runtime

	require.Equal(t, "auto", rt.Mode)

	// connectDeadlineMs is the one default that differs by platform: on Windows it has to clear
	// the named-pipe dial's 10 ms ERROR_PIPE_BUSY retry quantum, which the portable 5 ms does not
	// (internal/config/deadlines.go carries the arithmetic; internal/ipc's
	// TestConnectDeadlineDefaultClearsTheBusyRetryQuantum guards it against the quantum itself).
	// Both expectations are spelled out here as literals, not read back from the constants they
	// pin, so this stays the place the numbers are actually decided.
	wantConnectDeadlineMs := 5
	if runtime.GOOS == "windows" {
		wantConnectDeadlineMs = 25
	}

	require.Equal(t, config.DaemonCfg{
		Enabled: true, IdleExitSeconds: 1800, MaxSessions: 8, AckDeadlineMs: 8,
		ConnectDeadlineMs: wantConnectDeadlineMs,
	}, rt.Daemon)

	require.Equal(t, config.HotPathCfg{
		BudgetMs: 15, BreachWindows: 3, SpoolOnBreach: true, MaxPayloadBytes: 1048576,
	}, rt.HotPath)

	require.Equal(t, config.LogCfg{Level: "info", MaxFileMB: 10, MaxFiles: 5}, rt.Logging)

	require.Equal(t, config.RedactCfg{Enabled: true, Patterns: []string{}}, rt.Redact)

	require.Equal(t, config.TelemetryCfg{Enabled: false}, rt.Telemetry)

	require.Equal(t, config.RehydrateCfg{
		MinTokens: 8000, MaxTokens: 12000, SkillIndexTokens: 450, EliminationsTopN: 8,
	}, rt.Rehydrate)

	require.Equal(t, config.MCPCfg{SpanWidenLines: 40, MaxResponseBytes: 262144}, rt.MCP)

	// SP-01 additions beyond the §11.5 document reproduced in 00-ARCHITECTURE.md.
	require.Equal(t, config.BudgetsCfg{
		L0IngestMs: 2, L0ProcessMs: 50, CheckpointFinalizeMs: 2000, MCPToolCallMs: 250,
		HookDegradedMs: 1000,
	}, rt.Budgets)
	require.Equal(t, config.RSelectionCfg{SubmodularEnabled: false}, rt.Selection)
	require.Equal(t, config.RTokensCfg{
		ProseCharsPerToken: 4.0, CodeCharsPerToken: 3.6, JSONCharsPerToken: 3.2,
		DiffCharsPerToken: 3.4, BinaryCharsPerToken: 3.0,
		ImagePixelsPerToken: 750, ImageMaxTokens: 1600, PDFTokensPerPage: 1800,
		CalibrationMin: 0.6, CalibrationMax: 1.6, CalibrationAlpha: 0.2,
	}, rt.Tokens)
}

// TestDefaults_SubmodularEnabledDefaultsFalseAndHidden checks the §5.12 derived-field decision
// directly on Defaults(), independently of Load: the Go-level field defaults to false and never
// appears in JSON (json:"-").
func TestDefaults_SubmodularEnabledDefaultsFalseAndHidden(t *testing.T) {
	cfg := config.Defaults()
	require.False(t, cfg.Selection.Submodular.Enabled)

	b, err := json.Marshal(cfg.Selection.Submodular)
	require.NoError(t, err)
	require.NotContains(t, string(b), "enabled", "SubmodularCfg.Enabled must never serialize")
}
