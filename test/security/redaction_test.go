package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// The privacy half of SP17-M7-03. §13 invariant 7 says no secret reaches a durable surface; the
// tests that existed before this package proved it of `objects/` and of nothing else.
//
// This file drives one session carrying every built-in credential family plus an operator's own
// pattern through the PACKAGED binary, triggers a checkpoint and a flush, takes a real backup and
// runs a real eval export, and then sweeps every file the product wrote — objects, indexes,
// records, checkpoints, pins, spool, state, metrics and logs INCLUDING LOUD.log, the backup tree
// and its manifest, the export, and the retrieval previews the MCP server returns — for each
// secret's literal value and for any fragment of one longer than secretRunLimit bytes.
//
// The fragment half is what makes the sweep more than a substring check: a surface that stored a
// secret split around a placeholder boundary, or reassembled one across a canonicalization, would
// pass a literal search and fail this.

const (
	// redactSession is the session this file's capture drives.
	redactSession = core.SessionID("sess-security-redaction-0001")
	// redactToolUseID addresses the tool result carrying the credential transcript.
	redactToolUseID = "toolu_security_redaction_01"
	// redactBashToolUseID addresses the Bash capture whose ARGUMENTS carry a credential, which is
	// what reaches index/tool_use.jsonl's args preview — a plaintext surface objects/ never sees.
	redactBashToolUseID = "toolu_security_redaction_02"
	// redactPath is the file the credential-bearing tool result was read from.
	redactPath = "logs/npm-install.txt"
	// backupID is the id the consistent backup is claimed under.
	backupID = "security-sweep-backup"
)

// TestSecurity_NoSecretReachesAnyDurableSurface is the sweep.
func TestSecurity_NoSecretReachesAnyDurableSurface(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	seeds := secretSeeds(t)
	require.Len(t, seeds, builtinSecretFamilies+1,
		"the sweep must plant every built-in credential family plus the operator's own pattern")
	for _, seed := range seeds {
		require.Greater(t, len(seed.Secret), secretRunLimit,
			"fixture sanity: the %s secret must be longer than %d bytes or the fragment scan is vacuous",
			seed.Rule, secretRunLimit)
	}
	// Fixture sanity, and it is not theoretical: the first run of this sweep planted three values
	// sharing the run "1234567890abcdef…", so ONE surviving token was reported as three separate
	// families' fragments. A shared run makes every hit ambiguous, which is worse than a missed one.
	require.Empty(t, overlappingSeeds(seeds),
		"two planted secrets share a run longer than %d bytes, so one survivor would be reported as "+
			"several: %v", secretRunLimit, overlappingSeeds(seeds))

	// The operator's own pattern, declared in the project's config so the capture path compiles it.
	//
	// Nothing INVALID may go in here, and the reason is finding S-7: config.LoadForCapture refuses
	// the whole delivery when Validate() reports anything at all, so one bad key would disable the
	// capture this sweep exists to examine. The first attempt at forcing a Loud for the LOUD.log
	// assertion did exactly that and produced a session with no daemon and no objects.
	writeProjectConfig(t, p, map[string]any{
		"runtime": map[string]any{
			"redact": map[string]any{"enabled": true, "patterns": []string{userPatternRegexp}},
		},
	})

	transcript := secretTranscript(seeds)
	writeProjectFile(t, p, redactPath, transcript)

	// A real session through the packaged binary: start, a credential-bearing tool result, a
	// credential-bearing tool ARGUMENT, a credential-bearing prompt, a checkpoint and a flush.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, redactSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")

	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, redactSession, redactToolUseID, redactPath, transcript))
	// The tool ARGUMENTS carry a credential too. That is a different surface from the tool result:
	// arguments become index/tool_use.jsonl's args preview, a plaintext line objects/ never sees and
	// that RecordToolUse redacts on its own. The key spelling is one the §5.22a table names
	// (`client_secret=`), so this measures the family rather than the shape — the shape question has
	// its own case below.
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		bashToolPayload(t, p.Root, redactSession, redactBashToolUseID,
			"deploy --"+seedByRule(t, seeds, "assignment_secret").Line,
			redactMarker+"\n"+seedByRule(t, seeds, "github_token").Line))
	runHook(t, b.Bin, p, []string{"observe", "prompt"},
		promptPayload(t, p.Root, redactSession,
			"the deploy failed after i pasted "+seedByRule(t, seeds, "anthropic_key").Line+
				" into the shell — what leaked?"))
	requireIndexed(t, p.Root, redactToolUseID)

	runHook(t, b.Bin, p, []string{"checkpoint"}, preCompactPayload(t, p.Root, redactSession))
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, redactSession))

	// The retrieval previews, taken while the daemon is still up: they are a surface too, and one
	// no on-disk walk can reach.
	previews := sweepRetrievalPreviews(t, b, p)

	// Everything that follows needs the writer quiesced.
	shutdownIfReachable(t, p.Root)

	backupTree, backupRec := takeSweepBackup(t, p)
	exportDir, exportRec := runSweepExport(t, base, seeds)

	// The backup is NOT a second root. It lives under .qompack/backup/, so the split walk below
	// already sweeps its tree and its manifest as the `backup` surface; adding it again would
	// double-count every file in it and report each hit twice.
	roots := []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}}
	if exportDir != "" {
		roots = append(roots, surfaceRoot{Label: "eval-export", Dir: exportDir})
	}
	files := walkSurfaces(t, roots)
	files = append(files, previews...)
	require.NotEmpty(t, files, "the sweep must have something to sweep")

	// Non-vacuity: the transcript really did reach the store, so an empty result below means
	// "redacted", never "never captured".
	requireSurfacePresent(t, files, "objects")
	requireSurfacePresent(t, files, "index")
	if backupTree != "" {
		requireSurfacePresent(t, files, "backup")
	}
	requireNothingSkipped(t, files)

	hits := scanForSecrets(files, seeds)
	counts := countPlaceholders(files)
	inventory := surfaceInventory(files)

	artifact := writeSweepArtifact(t, sweepReport{
		Surfaces:     inventory,
		Placeholders: counts,
		Hits:         hits,
		Seeds:        seedRules(seeds),
	})

	// One record per surface, so the evidence table names what was swept rather than one verdict
	// standing for eighteen different directories.
	for _, name := range sortedSurfaceNames(inventory) {
		rec := newRecord(t, "privacy_surface_"+strings.ReplaceAll(name, "-", "_"))
		rec.Capability = CapPrivacy
		rec.Artifact = artifact
		surfaceHits := hitsOn(hits, name)
		rec.Detail = fmt.Sprintf("%d files, %d bytes swept, %d redaction placeholders",
			inventory[name].Files, inventory[name].Bytes, counts[name])
		if len(surfaceHits) == 0 {
			rec.Outcome = OutcomeVerified
			rec.Reason = fmt.Sprintf("no literal and no >%d-byte fragment of any of the %d planted "+
				"credential families survives under %s.", secretRunLimit, len(seeds), name)
		} else {
			rec.Outcome = OutcomeFailed
			rec.Reason = fmt.Sprintf("%d credential hit(s) survive under %s; owner: %s. %s",
				len(surfaceHits), name, surfaceOwner(name), renderHits(surfaceHits))
		}
		writeRecord(t, rec)
	}
	writeRecord(t, backupRec)
	writeRecord(t, exportRec)

	// A hit on one of the product's OWN durable surfaces is invariant 7 broken and fails here. The
	// eval export is judged separately and deliberately: it is an opt-in developer export written
	// outside the working tree through internal/eval's own, admittedly weaker, rule set, and the
	// audit proposal states that limitation rather than this package pretending it does not exist.
	for _, h := range hits {
		if h.Surface == "eval-export" {
			t.Logf("RETURNED FINDING (owner internal/eval): the %s %s survives the eval export at %s",
				h.Rule, h.Kind, h.Rel)
			continue
		}
		t.Errorf("§13 invariant 7: the %s %s survives on the %s surface at %s (window %q)",
			h.Rule, h.Kind, h.Surface, h.Rel, h.Window)
	}
}

// sweepRetrievalPreviews drives every retrieval tool that can render archived text and returns each
// response as a surface file, so the on-disk sweep and the over-the-wire sweep share one scanner.
func sweepRetrievalPreviews(t *testing.T, b bundle, p project) []surfaceFile {
	t.Helper()

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{mcp.ToolRecall, map[string]any{"query": redactMarker, "k": 10}},
		{mcp.ToolExpand, map[string]any{"tool_use_id": redactToolUseID, "full": true}},
		{mcp.ToolExpand, map[string]any{"tool_use_id": redactBashToolUseID, "full": true}},
		{mcp.ToolReRead, map[string]any{"path": redactPath, "full": true}},
		{mcp.ToolTimeline, map[string]any{"session": string(redactSession)}},
		{mcp.ToolWhy, map[string]any{"question": redactMarker}},
		{mcp.ToolDropped, map[string]any{}},
	}

	out := make([]surfaceFile, 0, len(calls))
	for i, call := range calls {
		res := child.call(t, call.tool, call.args)
		out = append(out, surfaceFile{
			Surface: "mcp-retrieval",
			Rel:     fmt.Sprintf("%02d-%s.json", i, call.tool),
			Content: []byte(res.Text),
		})
	}
	child.finish(t)
	return out
}

// takeSweepBackup takes a real consistent backup through store.NewMigrator and returns the copied
// tree plus the record describing what happened.
//
// The migration gate ships CLOSED in this build, so production can never reach TakeBackup at all.
// This drives the SAME code with a gate value the test supplies rather than forking a copy of it:
// what is being measured is whether the backup COPY carries secrets, and a fork of the walk would
// measure the fork. The record says the gate was supplied, because a reader has to know that this
// surface does not exist in a shipped build today.
func takeSweepBackup(t *testing.T, p project) (tree string, rec Record) {
	t.Helper()

	rec = newRecord(t, "privacy_backup_take")
	rec.Capability = CapPrivacy

	s := openStoreAt(t, p.Root)
	m, err := store.NewMigrator(s, p.Root, store.MigrateOptions{
		Source: emptyLegacySource{},
		Gate:   config.MigrationGate{Key: config.LegacyImportGateKey, Passed: true},
		Cfg:    config.Defaults(),
	})
	if err != nil {
		rec.Outcome = OutcomeSkipped
		rec.Reason = "store.NewMigrator refused: " + err.Error()
		return "", rec
	}

	man, err := m.TakeBackup(context.Background(), backupID)
	if err != nil {
		rec.Outcome = OutcomeSkipped
		rec.Reason = "store.TakeBackup refused: " + err.Error()
		return "", rec
	}

	tree = filepath.Join(paths.Of(p.Root).Backup, backupID, "tree")
	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("a consistent backup of %d files was taken through the real "+
		"store.Migrator.TakeBackup with the legacy-import gate supplied by the test (it ships "+
		"closed, so no shipped build reaches this code); its tree is swept as its own surface.",
		len(man.Files))
	rec.Detail = "manifest consistent=" + fmt.Sprint(man.Consistent)
	return tree, rec
}

// runSweepExport runs a real eval import over a synthetic transcript carrying the same credential
// families, with the default redaction, and returns the destination for sweeping.
func runSweepExport(t *testing.T, base string, seeds []secretSeed) (dir string, rec Record) {
	t.Helper()

	rec = newRecord(t, "privacy_eval_export")
	rec.Capability = CapPrivacy

	from := filepath.Join(base, "transcripts")
	require.NoError(t, os.MkdirAll(paths.Long(from), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(from, "sess-security.jsonl")),
		[]byte(evalTranscript(t, seeds)), 0o600))

	dir = filepath.Join(base, "export")
	report, err := eval.Import(context.Background(), eval.ImportOptions{
		From: from, To: dir, Redact: true,
		Getenv: func(string) string { return "" },
	})
	if err != nil {
		rec.Outcome = OutcomeSkipped
		rec.Reason = "eval.Import refused: " + err.Error()
		return "", rec
	}

	rec.Outcome = OutcomeVerified
	rec.Reason = fmt.Sprintf("eval.Import wrote %d session(s) from %d transcript file(s) with the "+
		"default redaction, replacing %d spans; the destination is swept as its own surface.",
		report.Sessions, report.Files, report.RedactedSpans)
	require.Positive(t, report.Sessions, "the export must have written a session to sweep")
	return dir, rec
}

// emptyLegacySource is the LegacySource store.NewMigrator requires and TakeBackup never reads. It
// declares an empty import set, so nothing about a legacy store is asserted or implied here.
type emptyLegacySource struct{}

func (emptyLegacySource) Snapshot(context.Context) (store.LegacySnapshot, error) {
	return store.LegacySnapshot{ID: "security-sweep-empty"}, nil
}

func (emptyLegacySource) Read(context.Context, int64, int) ([]store.LegacyRecord, error) {
	return nil, nil
}

// evalTranscript renders a Claude Code transcript carrying every planted credential, in the record
// shape internal/eval's importer parses.
func evalTranscript(t *testing.T, seeds []secretSeed) string {
	t.Helper()

	line := func(v any) string {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		return string(b) + "\n"
	}

	var body strings.Builder
	for _, seed := range seeds {
		body.WriteString(seed.Line)
		body.WriteString("\n")
	}

	var out strings.Builder
	out.WriteString(line(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "why did this fail?\n" + body.String()}},
		},
	}))
	out.WriteString(line(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "tool_use", "id": "toolu_eval_01", "name": "Bash",
				"input": map[string]any{"command": "cat .npmrc", "description": redactMarker},
			}},
			"usage": map[string]any{"output_tokens": 32},
		},
	}))
	out.WriteString(line(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": "toolu_eval_01", "content": body.String(),
			}},
		},
	}))
	return out.String()
}

// seedByRule returns the planted seed for one rule, failing rather than returning a zero value: a
// silently missing family would make the case that used it assert nothing.
func seedByRule(t *testing.T, seeds []secretSeed, rule string) secretSeed {
	t.Helper()
	for _, s := range seeds {
		if s.Rule == rule {
			return s
		}
	}
	t.Fatalf("security: no planted seed for rule %q", rule)
	return secretSeed{}
}

// seedRules is the planted rule names, for the artifact.
func seedRules(seeds []secretSeed) []string {
	out := make([]string, 0, len(seeds))
	for _, s := range seeds {
		out = append(out, s.Rule)
	}
	sort.Strings(out)
	return out
}

// npmrcProbe is a credential-shaped run that matches NO built-in rule on its own, so whether it is
// redacted depends entirely on the KEY NAME beside it. It is written split across a `+` for the
// reason internal/testutil/secrettokens.go gives: the runtime value is exact while no contiguous
// credential-shaped run appears in this source for a scanner to match.
var npmrcProbe = "Kq4Rm8Tv2Wy6" + "Ze0AbCdEfGhIj"

// TestSecurity_AssignmentRuleCoverageForUnderscoredKeys measures one rule-coverage question the
// ten-family sweep above deliberately does not: whether `internal/redact`'s assignment_secret rule
// reaches the key spelling a real `.npmrc` leak uses.
//
// §5.22a's assignment family gates its whole key alternation behind one leading word boundary, and
// a word boundary cannot match between an underscore and a letter — so `_authToken=`, which is
// exactly how npm writes a registry credential into `.npmrc` and how it appears in every
// `npm config set` command line, is outside the rule, as is `_password=`.
//
// `_auth=` is outside it twice over, and the second reason is the more interesting one: the
// alternation carries `auth[_-]?token` and no bare `auth` branch at all, so plain `auth=` is
// unmatched too. A remedy that merely allowed a leading underscore would not reach either spelling.
//
// This is a COVERAGE measurement, not an invariant. The rule table is prose in §5.22a rather than a
// frozen fixture — internal/redact/redacttest pins behaviour, not this alternation — so widening it
// disturbs nothing, but it is still the owning package's call. The outcome is recorded with its
// owner and the Go test does not fail on it.
func TestSecurity_AssignmentRuleCoverageForUnderscoredKeys(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "privacy_assignment_rule_underscored_keys")
	rec.Capability = CapPrivacy

	const id = "toolu_security_npmrc_01"
	line := "//registry.npmjs.org/:_authToken=" + npmrcProbe

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, redactSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		bashToolPayload(t, p.Root, redactSession, id, "npm config set "+line,
			redactMarker+"\n"+line+"\n"))
	requireIndexed(t, p.Root, id)
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, redactSession))
	shutdownIfReachable(t, p.Root)

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	requireSurfacePresent(t, files, "objects")
	survived := surfacesContaining(files, npmrcProbe)

	rec.Detail = fmt.Sprintf("swept %d files under .qompack/; probe key spelling `_authToken=`", len(files))
	if len(survived) == 0 {
		rec.Outcome = OutcomeVerified
		rec.Reason = "the assignment rule reaches an underscore-prefixed key: an `_authToken=` " +
			"credential was redacted on every durable surface."
	} else {
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("an `_authToken=<value>` credential — the spelling npm writes into "+
			".npmrc and the one every `npm config set` command line carries — survives in clear on "+
			"%v. One leading word boundary gates the whole key alternation, and a word boundary "+
			"cannot match between `_` and a letter, so `_authToken=` and `_password=` are outside "+
			"the rule. `_auth=` is outside it for a SECOND and independent reason: the alternation "+
			"has no bare `auth` branch at all, only `auth[_-]?token`, so even plain `auth=` is "+
			"unmatched and an optional-leading-underscore remedy would not reach it. Owner: "+
			"internal/redact. RETURNED, not fixed here: the rule table is prose in §5.22a rather "+
			"than a frozen fixture — redacttest pins behaviour, not this alternation — so widening "+
			"it disturbs nothing, but it is the owning package's call.", survived)
		t.Logf("RETURNED FINDING (owner internal/redact): `_authToken=` is outside the assignment "+
			"rule; the credential survives on %v", survived)
	}
	writeRecord(t, rec)
}

// loudSession is the session the LOUD.log case drives.
const loudSession = core.SessionID("sess-security-loud-0001")

// TestSecurity_LoudDiagnosticsNeverCarryTheSecret makes the product write LOUD.log and then reads
// it.
//
// The independent review caught the evidence claiming "logs/ including LOUD.log swept and clean"
// on the strength of a file COUNT of one — and a day log alone accounts for one file. LOUD.log is
// written only when something Louds, so a sweep that finds it has to ARRANGE for it. Contriving one
// through a configuration violation turned out to disable the whole session (finding S-7), so the
// Loud here is produced the way §12.3 produces one, through the product's own degradation path:
// a credential-bearing capture whose object is then damaged, retrieved through the packaged MCP
// server, quarantined by the daemon, and Louded about.
//
// That makes this the strongest form of the assertion rather than merely a repaired one. The Loud
// is ABOUT the object that carried the credentials, which is exactly the case
// internal/mcp/handlers_common.go and internal/store/objects.go discipline themselves for: a
// diagnostic about a secret must never become one.
func TestSecurity_LoudDiagnosticsNeverCarryTheSecret(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "privacy_loud_log_carries_no_secret")
	rec.Capability = CapPrivacy

	seeds := secretSeeds(t)
	transcript := secretTranscript(seeds)
	writeProjectFile(t, p, redactPath, transcript)

	const id = "toolu_security_loud_01"
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, loudSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, loudSession, id, redactPath, transcript))
	requireIndexed(t, p.Root, id)
	shutdownIfReachable(t, p.Root)

	// Damage the object the capture produced, the same way the bounds cases do: same plaintext
	// length, one byte different, so the refusal is forced through the content address.
	damaged := damageCapturedObject(t, p.Root, id)

	// Ask for it through the packaged server. The daemon's own store quarantines it and Louds.
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, loudSession))
	require.True(t, waitDaemonUp(t, p.Root), "the second session-start must bring a daemon up")
	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)
	res := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true})
	child.finish(t)
	shutdownIfReachable(t, p.Root)

	require.True(t, quarantineHolds(t, p.Root, damaged),
		"the damaged object must have been quarantined, or nothing Louded and this case proves nothing")

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	requireNothingSkipped(t, files)
	requireSurfaceFile(t, files, "logs", "logs/LOUD.log")

	logsOnly := make([]surfaceFile, 0, 4)
	for _, f := range files {
		if f.Surface == "logs" {
			logsOnly = append(logsOnly, f)
		}
	}
	hits := scanForSecrets(logsOnly, seeds)
	inv := surfaceInventory(logsOnly)

	rec.Detail = fmt.Sprintf("expand envelope %s; logs/ held %v", describeEnvelope(res), inv["logs"].Names)
	if len(hits) == 0 {
		rec.Outcome = OutcomeVerified
		rec.Reason = "the daemon quarantined a credential-bearing object and Louded about it; " +
			"logs/LOUD.log was written, was swept by name, and carries no literal and no >8-byte " +
			"fragment of any of the 11 planted credentials. The Loud names a 12-character hash " +
			"prefix and a reason, never a span of the content."
	} else {
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("a Loud about a credential-bearing object carried the credential "+
			"into logs/: %s. Owner: internal/store + internal/logging.", renderHits(hits))
		t.Errorf("§13 invariant 7: a diagnostic became a secret: %s", renderHits(hits))
	}
	writeRecord(t, rec)
}

// damageCapturedObject flips one byte inside the object a tool_use record addresses, keeping the
// plaintext length identical so the read is refused by the content address rather than by the
// cheaper indexed-length check. It returns the chunk hash that will be quarantined.
func damageCapturedObject(t *testing.T, root, id string) core.Hash {
	t.Helper()

	s := openStoreAt(t, root)
	ctx := context.Background()
	recTU, err := s.ToolUse(ctx, core.ToolUseID(id))
	require.NoError(t, err, "the capture must be in the tool_use index")
	rt, err := s.GetRoot(ctx, recTU.Root)
	require.NoError(t, err, "the captured root must be readable")
	require.NotEmpty(t, rt.Chunks, "the captured root must name its chunks")
	require.NoError(t, s.Close())

	chunk := rt.Chunks[0].Hash
	path := objectFilePath(root, chunk)
	raw, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err)
	plain, err := store.Decode(raw)
	require.NoError(t, err)
	require.NotEmpty(t, plain)
	plain[len(plain)/2] ^= 0x01
	encoded, err := store.Encode(plain)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(path), encoded, 0o600))
	return chunk
}
