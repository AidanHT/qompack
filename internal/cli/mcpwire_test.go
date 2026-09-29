package cli

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
)

// mcpwire.go's ledger seam. cmd_mcp_test.go covers the widener branch; this file covers the one
// collaborator NewToolDeps must NOT capture by value.

// callWiredTool drives one registered tool through the handler mcp.ToolDefs bound, which is the
// same handler `tools/call` reaches, and returns its first text block.
func callWiredTool(t *testing.T, d mcp.ToolDeps, name string, args string) string {
	t.Helper()
	for _, tool := range mcp.ToolDefs(d) {
		if tool.Name != name {
			continue
		}
		resp, err := tool.Handler(t.Context(), mcp.Request{
			Session: "sess_wire", Name: name, Args: json.RawMessage(args),
		})
		require.NoError(t, err, "%s handler", name)
		require.NotEmpty(t, resp.Content, "%s produced no content", name)
		return resp.Content[0].Text
	}
	t.Fatalf("tool %q is not registered", name)
	return ""
}

// TestNewToolDepsResolvesTheLedgerLive is the shipped-daemon proof for the two negative-knowledge
// tools.
//
// negknow.Open is deliberately lazy — its one production call site is the rehydrate service, on
// the first compaction, because an eager open would create sketches/tried.bloom in every daemon
// that never compacts. So opts.Ledger is nil when installMCPTools runs, and a ToolDeps that
// captured that VALUE froze the nil for the life of the process: `already_tried` and
// `record_eliminated` answered available:false for ever in the shipped binary while the ledger sat
// open beside them. The accessor reads the FIELD on every call, exactly as SchedulerRuntimeOptions
// .LedgerFn and wireCheckpointSources' supplier already do.
func TestNewToolDepsResolvesTheLedgerLive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults()
	// No store is wired here — this row is about the ledger seam and nothing else — so the write
	// side has nothing to mint an evidence hash from. Turning the requirement off keeps the row
	// pointed at what it tests instead of at eliminations.requireEvidence.
	cfg.Eliminations.RequireEvidence = false
	opts := &daemon.Options{ProjectRoot: root, Cfg: cfg, Log: logging.Nop(), Clock: testClock()}

	deps := NewToolDeps(root, cfg, nil, liveLedger(opts), nil, nil, nil, nil, logging.Nop(), nil, testClock())

	// 1. Wiring is inert: it opens nothing and creates nothing.
	require.Nil(t, opts.Ledger, "wiring must not open a ledger")
	require.NoFileExists(t, filepath.Join(root, ".qompack", "records", "eliminations.jsonl"))
	require.NoFileExists(t, filepath.Join(root, ".qompack", "sketches", "tried.bloom"))
	require.NoDirExists(t, filepath.Join(root, ".qompack", "sketches"))

	// Criterion change (V6 close-out wave 13, retrieval D1): "say so" used to be the body
	// {"available":false,...}, outside already_tried's documented states; it is now the tool's own
	// unavailable state, degraded. The row still asserts the tool invents no answer.
	args := `{"target":"src/auth.ts:refreshToken","approach":"widen pool timeout"}`
	noLedger := callWiredTool(t, deps, mcp.ToolAlreadyTried, args)
	require.Contains(t, noLedger, `"state":"unavailable"`,
		"with no ledger open yet the tool must say so, not invent an answer")
	require.Contains(t, noLedger, `"degraded":true`)

	// 2. A ledger opened AFTER wiring — exactly as the first compaction opens it, onto the same
	//    Options — is visible to the tools.
	led, err := negknow.Open(root, cfg, nil, negknow.Deps{
		Store: opts.Store, Graph: opts.Graph, Log: opts.Log, Clock: opts.Clock,
	})
	require.NoError(t, err, "negknow.Open")
	t.Cleanup(func() { _ = led.Close() })
	opts.Ledger = led

	body := callWiredTool(t, deps, mcp.ToolAlreadyTried, args)
	require.NotContains(t, body, `"state":"unavailable"`,
		"the accessor reads the FIELD; a captured value would still be nil")
	require.Contains(t, body, `"state":"absent"`, "an open, empty ledger answers absent")

	written := callWiredTool(t, deps, mcp.ToolRecordEliminated,
		`{"target":"src/auth.ts:refreshToken","approach":"widen pool timeout","reason":"the pool is not the bottleneck"}`)
	require.NotContains(t, written, `"available":false`, "the write side must reach the same live ledger")
	require.Contains(t, written, `"status":"active"`)

	recs, err := led.All(t.Context())
	require.NoError(t, err, "Ledger.All")
	require.Len(t, recs, 1, "record_eliminated must have written through to the ledger the daemon holds")
}

// TestNewToolDepsWithoutAnAccessorStaysNilTolerant keeps the zero value honest: a caller that
// supplies no ledger accessor at all (mcptest's shape block, the proxy's tools/list) must still
// register every tool and answer already_tried's unavailable state rather than panic.
//
// Criterion change (V6 close-out wave 13, retrieval D1): the answer was {"available":false,...},
// a body outside the tool's documented states; it is now state "unavailable", degraded.
func TestNewToolDepsWithoutAnAccessorStaysNilTolerant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults()
	deps := NewToolDeps(root, cfg, nil, nil, nil, nil, nil, nil, logging.Nop(), nil, testClock())

	body := callWiredTool(t, deps, mcp.ToolAlreadyTried, `{"target":"a","approach":"b"}`)
	require.Contains(t, body, `"state":"unavailable"`)
	require.Contains(t, body, `"degraded":true`)
}

// TestNewToolDepsAlwaysSuppliesARedactor pins the composition root's half of T20-M2-04.
//
// internal/mcp declares mcp.Redactor and cannot build one — 00-ARCHITECTURE.md §3.2 keeps
// internal/redact out of its allow-set — so it fails CLOSED when none arrives: `expand` and
// `re_read` report themselves unavailable and `recall` withholds every summary. That makes
// supplying it non-optional HERE, on every path, including the ones where every other collaborator
// is nil. A NewToolDeps that quietly left the field nil would ship a daemon whose retrieval tools
// all answer available:false, and this is the assertion that would catch it.
func TestNewToolDepsAlwaysSuppliesARedactor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults()

	bare := NewToolDeps(root, cfg, nil, nil, nil, nil, nil, nil, logging.Nop(), nil, testClock())
	require.NotNil(t, bare.Redactor, "a ToolDeps with no store still carries a redactor")

	// And it is the REAL one: today's built-in rules fire on a credential shape, and the rule name
	// comes back so a diagnostic can say what was caught without quoting it.
	const key = "AKIA" + "IOSFODNN7EXAMPLE"
	out, rules := bare.Redactor.Redact([]byte("aws_access_key_id = " + key))
	require.NotContains(t, string(out), key, "the production rule set must actually fire")
	require.Contains(t, string(out), "«redacted:", "a placeholder must stand in the secret's place")
	require.Len(t, rules, 1, "one match, one rule name")
	require.NotEmpty(t, rules[0], "the rule behind a match must be named")
}

// TestNewRetrievalRedactorIsIdempotent pins the property retrieval-side redaction rests on: it runs
// over bytes capture-time redaction may already have scrubbed, so a second pass must change
// nothing. Without it, re-reading a stored record would rewrite its own placeholders and the span
// offsets around them would drift on every read.
func TestNewRetrievalRedactorIsIdempotent(t *testing.T) {
	t.Parallel()

	r := NewRetrievalRedactor(config.Defaults())
	const key = "AKIA" + "IOSFODNN7EXAMPLE"
	once, rules := r.Redact([]byte("aws_access_key_id = " + key + "\n"))
	require.NotEmpty(t, rules)

	twice, again := r.Redact(once)
	require.Equal(t, string(once), string(twice), "a second pass over redacted bytes must change nothing")
	require.Empty(t, again, "and must report no new matches")
}

// TestLedgerAccessorsAreSafeAgainstTheLazyOpen is the concurrency half of the seam above, and it
// is written to be run under -race: it is the reviewer's failing input, executed.
//
// The daemon serves one goroutine per connection (ipc.Server), so goroutine A can be handling a
// PreCompact — which reaches the lazy opener and PUBLISHES the ledger handle — while goroutine B
// is handling an `mcp` op and READING that handle through liveLedger, and a third is running the
// scheduler's rebuild_bloom idle task through the same shape. The publication used to be a plain
// `o.Ledger = l` under a sync.Once, and sync.Once establishes happens-before only for goroutines
// that call Do: a plain field read elsewhere has no edge to it. That is a data race on a two-word
// interface value — a CI failure under the detector, and without it a non-nil interface over a nil
// data pointer that faults inside the ledger call.
//
// The three reader shapes here are the production ones: liveLedger (every MCP tool call),
// wireScheduler's LedgerFn, and wireCheckpointSources' SourceSet supplier.
func TestLedgerAccessorsAreSafeAgainstTheLazyOpen(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Eliminations.RequireEvidence = false
	opts := daemon.NewOptions(root, cfg)
	opts.Log = logging.Nop()
	opts.Clock = testClock()

	_ = daemon.WireRehydrator(&opts) // installs the one-shot opener, opens nothing yet
	require.NotNil(t, opts.OpenLedger, "the lazy opener is the seam under test")
	require.Nil(t, opts.LedgerHandle(), "wiring must not open a ledger")
	t.Cleanup(func() {
		if l := opts.LedgerHandle(); l != nil {
			_ = l.Close()
		}
	})

	live := liveLedger(&opts)
	readers := []func() negknow.Ledger{
		live,
		opts.LedgerHandle,
		func() negknow.Ledger { return checkpoint.SourceSet{LedgerFn: live}.LedgerFn() },
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, read := range readers {
		for range 4 {
			wg.Add(1)
			go func(read func() negknow.Ledger) {
				defer wg.Done()
				<-start
				for range 200 {
					_ = read()
				}
			}(read)
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		opts.OpenLedger()
	}()
	close(start)
	wg.Wait()

	require.NotNil(t, opts.LedgerHandle(),
		"the open must actually have happened, or the readers raced against nothing")
	require.NotNil(t, live(), "and every accessor must see the published handle")
}

