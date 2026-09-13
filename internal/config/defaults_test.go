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

	// Three defaults differ by platform, and all three live in internal/config/deadlines.go:
	//
	//   - connectDeadlineMs: on Windows it has to clear the named-pipe dial's 10 ms
	//     ERROR_PIPE_BUSY retry quantum, which the portable 5 ms does not (deadlines.go carries the
	//     arithmetic; internal/ipc's TestConnectDeadlineDefaultClearsTheBusyRetryQuantum guards it
	//     against the quantum itself).
	//   - l0IngestMs and ackDeadlineMs: SP20-D1's measured B-B re-budget, 2026-09-13. Windows is
	//     measured (L0IngestMs = roundup5(1.25 x 36.864) = 50, AckDeadlineMs = 50 + ceil(22.257) =
	//     73, over fifteen runs rather than the protocol's three -- see deadlines.go for why the
	//     three-run figure of 30 was withdrawn); linux and darwin are design §7.5's seeds and stay
	//     provisional until CI's bench-gate
	//     measures them on ubuntu-latest and macos-latest.
	//
	// Every expectation is spelled out here as a literal, not read back from the constants it pins,
	// so this stays the place the numbers are actually decided.
	wantConnectDeadlineMs, wantAckDeadlineMs, wantL0IngestMs := 5, 17, 15
	switch runtime.GOOS {
	case "windows":
		wantConnectDeadlineMs, wantAckDeadlineMs, wantL0IngestMs = 25, 73, 50
	case "darwin":
		wantAckDeadlineMs, wantL0IngestMs = 45, 40
	}

	require.Equal(t, config.DaemonCfg{
		Enabled: true, IdleExitSeconds: 1800, MaxSessions: 8, AckDeadlineMs: wantAckDeadlineMs,
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

	// The §11.5 cache-regime block SP-12 adds: neither key changes an Appendix C default, they
	// sit beside scheduler.cache and leave its values untouched (00-ARCHITECTURE.md §11.5).
	require.Equal(t, config.RSchedulerCfg{
		Cache: config.RSchedulerCacheCfg{ExpiringTriggerFraction: 0.8, AssumeMaxTTLSeconds: 3600},
	}, rt.Scheduler)

	// SP-19's migration block (Qompack.md v1.5 Appendix C): every gated switch off, the hardwired
	// manual-compact block off, the tested SessionStart adapter on. migration_test.go pins the
	// gate table; this pins the defaults.
	require.Equal(t, config.MigrationCfg{
		SettingsVersion: 1,
		Reinjection:     config.MigrationReinjectionCfg{SessionStartCompact: true},
	}, rt.Migration)

	// SP-01 additions beyond the §11.5 document reproduced in 00-ARCHITECTURE.md.
	require.Equal(t, config.BudgetsCfg{
		L0IngestMs: wantL0IngestMs, L0ProcessMs: 50, CheckpointFinalizeMs: 2000, MCPToolCallMs: 250,
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

// TestDefaults_AckDeadlineCoversTheIngestBudget is the anti-drift guard between the two constant
// sets SP20-D1's re-budget added (design §7.5's T30): the ACK deadline is derived as
// AckDeadlineMs = L0IngestMs + ceil(slack99) with slack99 > 0, so on every platform it must sit
// strictly above that platform's B-B budget. If it ever does not, a hook gives up and spools a
// duplicate of a delivery the daemon is still inside the budget for — SP05-D2's exact failure.
//
// It checks all three platforms' constants, not just the running host's: the constants are
// exported precisely so one host can see every platform's value (deadlines.go's
// l0IngestMsDefault comment), and a Windows-only CI would otherwise never exercise the linux and
// darwin rows at all.
func TestDefaults_AckDeadlineCoversTheIngestBudget(t *testing.T) {
	for _, c := range []struct {
		goos   string
		ingest int
		ack    int
	}{
		{"linux (portable)", config.L0IngestMsPortable, config.AckDeadlineMsPortable},
		{"windows", config.L0IngestMsWindows, config.AckDeadlineMsWindows},
		{"darwin", config.L0IngestMsDarwin, config.AckDeadlineMsDarwin},
	} {
		require.Greater(t, c.ack, c.ingest,
			"%s: ackDeadlineMs (%d) must exceed l0IngestMs (%d) — it is derived as "+
				"l0IngestMs + ceil(slack99) with slack99 > 0 (internal/config/deadlines.go)",
			c.goos, c.ack, c.ingest)
	}
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
