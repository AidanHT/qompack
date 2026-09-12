package observer

import (
	"context"
	"fmt"
	"strings"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/grammar"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/tokens"
)

// mcpToolPrefix marks Qompack's own retrieval results (§8.7, resolved decision 6). A tool whose
// name starts with it is recorded Ephemeral, excluded from supersession in both directions, and
// not fed to the exploration sketches — retrieval is not exploration.
const mcpToolPrefix = "mcp__qompack__"

// The two display names whose results are WRITES rather than reads. dag.ObservedTool.Writes is
// what orients every shared-state edge (SP-07 D-1), so this is the only place the distinction is
// made on the observer's side.
const (
	toolFileEdit  = "FileEdit"
	toolFileWrite = "FileWrite"
)

// The action-grammar symbols a test verdict contributes alongside the tool symbol (§8.1 item 6).
const (
	grammarTestPass grammar.Symbol = "test:pass"
	grammarTestFail grammar.Symbol = "test:fail"
)

// crlfClass is stripped unconditionally, even when store.canonicalize.enabled is false: §4 makes
// CRLF→LF normalization a property of content entering the store rather than a configured option
// (resolved decision 2).
const crlfClass = canon.Class("crlf")

// minHashShingleSize is the shingle width the near-duplicate signature is computed over. It is a
// property of this call site's content class — tool output — rather than a configuration default,
// which is why it is named here and not in internal/config.
const minHashShingleSize = 5

// OnToolUse is the PostToolUse pipeline of §8.1 items 1–6, in the order resolved decision 1 fixes:
// index → file version → sketches → DAG → grammar → signals/features → tombstone.
//
// Required content/reference failures return ErrUnpublished so the daemon retains the delivery
// for retry. Secondary derived stages remain soft failures. Output is always empty because
// PostToolUse emits nothing; the host's original result is preserved.
func (o *observer) OnToolUse(ctx context.Context, e Event) (Output, error) {
	var out Output
	err := o.timed(histToolUse, func() error {
		var err error
		out, err = o.onToolUse(ctx, e)
		return err
	})
	return out, err
}

func (o *observer) onToolUse(ctx context.Context, e Event) (Output, error) {
	// 1. Nothing before the ctx check; nothing after it reads the clock twice (decision 11).
	if err := ctx.Err(); err != nil {
		return hookio.Empty(), err
	}
	now := o.now()
	st := o.session(e.SessionID)
	st.mu.Lock()
	defer st.mu.Unlock()

	// 2. The display name is what selects the canonicalizer, the supersession class and the
	//    grammar symbol; the RAW name is what identifies a retrieval result.
	display := NormalizeToolName(e.ToolName)
	ephemeral := strings.HasPrefix(e.ToolName, mcpToolPrefix)

	// 3. The configured hot-path cap binds here, after signals.go's own allocation bound.
	body := responseText(e)
	if len(body) > o.maxResultBytes {
		body = body[:o.maxResultBytes]
	}
	empty := len(body) == 0

	// 4. The path this call touched, normalized once, in paths.Key form for everything downstream.
	pathKey := ""
	if raw := PathsFromInput(display, e.ToolInput); len(raw) > 0 && raw[0] != "" {
		if n, err := paths.Norm(o.opt.ProjectRoot, raw[0]); err == nil {
			pathKey = paths.Key(n)
		}
	}

	// 5. §8.1 item 1. The observer never chunks, redacts or canonicalizes by hand: the store does
	//    redact → canonicalize → chunk internally, and this call's contribution to O2 is the
	//    per-tool canonicalizer SELECTION carried by Tool, Path and Canon.Strip.
	var res store.PutResult
	if !empty {
		var err error
		res, err = o.opt.Store.PutBytes(ctx, body, store.PutOptions{
			Tool: display, Path: pathKey, Canon: o.canonOptions(),
			KeepRaw: true, Ephemeral: ephemeral,
		})
		if err != nil {
			return hookio.Empty(), o.unpublished(stagePut) // never a dangling index record
		}
	}

	// 5a. §8.1 item 1's near-duplicate signal, counted at the PUT and not inside the
	//     supersession scan. The store detects it for PATHLESS content too — Bash and
	//     test-runner output, the noisiest class in a session and the one item 1 says the dedup
	//     ratio is won or lost on — and supersession returns early on an empty Path, so
	//     counting it there would read ~0 for exactly the content it exists to measure. A
	//     retrieval result is excluded because it is not exploration (resolved decision 6).
	if res.NearDup != nil && !ephemeral {
		o.count(counterNearDup)
	}

	// 6. The index entry. store.ArgsDigest is §5.8's, and nothing else may re-derive it.
	tok := res.Root.Tokens
	if tok == 0 && o.opt.Tokens != nil {
		tok = o.opt.Tokens.EstimateRoot(ctx, res.Root.Chunks, tokens.Classify(display, pathKey, body))
	}
	digest, preview := store.ArgsDigest(e.ToolInput)
	rec := store.ToolUseRecord{
		ID: e.ToolUseID, Session: e.SessionID, Turn: st.Turn,
		TS: now, Tool: display, ArgsDigest: digest, ArgsPreview: preview,
		Root: res.Root.Hash, Path: pathKey, Bytes: res.Root.RawBytes, Tokens: tok,
		Signature: res.Signature, Status: store.StatusOK, Ephemeral: ephemeral,
	}
	obs := ObservationFrom(ctx)
	rec.Observation = obs
	if e.ToolUseID == "" {
		// A payload with no tool_use_id still gets a stable, session-local identity, because a
		// record the graph cannot name is a record nothing can retrieve.
		//
		// SP08-D2: that identity is DERIVED from this process's state, so a redelivery re-derives a
		// different one and records twice for one host event. Two guards, in this order.
		rec.ID = derivedToolUseID(e.SessionID, st.Turn, len(st.ToolUses))
		switch prior, ok := o.observationRecord(ctx, obs, e.SessionID, opObserveTool); {
		case ok && prior.Root == res.Root.Hash:
			// Recognized: reuse the id this observation already published, and let the store's own
			// dedup absorb the rest.
			rec.ID = prior.ID
		case obs != "":
			// Either nobody has published this delivery, or one did and its root DIFFERS from this
			// run's — a changed canonicalization config, a different payload cap, a moved
			// truncation boundary. The root check is what stops the second case from adopting an id
			// it then cannot record: same id with a different root is ErrAppendOnly, which becomes
			// ErrUnpublished, which makes the drain break its read loop WITHOUT advancing the
			// offset, so every later drain retries that line forever with the rest of the spool
			// file stuck behind it. Falling through to a freshly minted id keeps the base's
			// self-healing duplicate instead of converting it into a permanent stall.
			//
			// The mint is probed rather than taken on faith, for the reason stop.go step 0(b)
			// gives: a restarted process starts this window at 0 again, so the id it derives may
			// already be held — by a record with a different root, which is the same stall.
			if n, found := o.freeDerivedTurn(ctx, len(st.ToolUses), func(i int) core.ToolUseID {
				return derivedToolUseID(e.SessionID, st.Turn, i)
			}); found {
				rec.ID = derivedToolUseID(e.SessionID, st.Turn, n)
			} else {
				o.count(counterTurnExhausted)
			}
		}
	}
	// 6a. The write. The record and every supersede mark it authors land in ONE index write
	//     (publishRecord). Splitting them is SP08-D2's second mechanism: a handler cancelled between
	//     the record and its marks leaves a record whose marks never landed, and the at-least-once
	//     redelivery that follows appends those marks alone, behind a record line that predates the
	//     flush — a supersede mark with no new record behind it, which x09's flush arm rejects.
	superseded, recorded, recErr := o.publishRecord(ctx, rec, res, empty)
	if recErr != nil {
		return hookio.Empty(), o.unpublished(stageIndex)
	}
	// 6b. Publication order's second stage completes here: the reference is joined to the durable
	//     capture the daemon already made, keyed by this delivery's observation identity. Until this
	//     link exists there is a capture with no reference and a reference with no capture, and a
	//     crash between them must resolve to "not published" rather than to a handle that names a
	//     dependency nothing can produce. A delivery with no identity (an in-process caller, or one
	//     the daemon could not lease) has no link to make and skips the stage.
	//
	//     It runs BEFORE the replay check below on purpose: a first run killed between its record
	//     and this link left a durable record with no join, and the redelivery completing that join
	//     is the only thing that can repair it.
	if err := o.linkObservation(obs, rec); err != nil {
		return hookio.Empty(), o.unpublished(stageLink)
	}
	// 6c. A redelivery the store recognized: the index already holds this record with this root, so
	//     the marks it authored landed with it and there is nothing left to publish. Everything
	//     below is DERIVED state of the first run, and recomputing it from the redelivering
	//     process's view produces wrong data rather than the same data twice — a file version
	//     appended out of order, sketch and tombstone counts doubled, a DAG edge built from this
	//     process's predecessor, LastToolUseID moved back to the replayed record. A first run cut
	//     before one of those soft stages loses it exactly as any soft failure does.
	//
	//     Window membership is the one exception, because its correct value after a replay is
	//     process-independent: "contains this tool use", in any process. Restoring it by id repairs
	//     a restarted daemon whose next subagent capture would otherwise omit the tool result from
	//     its hash list, and is a no-op in the process that already has it.
	if !recorded {
		o.rememberToolUseOnce(st, rec)
		o.count(counterRedelivery)
		return hookio.Empty(), nil
	}
	o.rememberToolUse(st, rec)

	// 7. §8.2 file version history. Only a result that IS the content of a path is a version of it.
	if !empty && pathKey != "" && supersedableClass(display) == classFileContent {
		o.soft(stageFileVer, o.opt.Store.AppendFileVersion(ctx, pathKey, store.FileVersion{
			TS: now, Root: res.Root.Hash, Turn: st.Turn, Bytes: res.Root.RawBytes,
		}))
	}

	// 8. §8.1 item 3. Every earlier read of this path that this one makes redundant is marked
	//    SUPERSEDED in the index; graph.go turns marked[0] into ObservedTool.Supersedes and emits
	//    the tail itself.
	//
	//    On a store with the atomic capability the marks already landed, in step 6's single write,
	//    and superseded already names them. This is the LEGACY path only: a separate MarkSuperseded
	//    per mark, after the record, which is the interleaving SP08-D2 closes. The EMPTY skip lives
	//    in publishRecord for the other path and is repeated here for this one.
	if o.idx == nil && !empty {
		superseded = o.detectSupersession(ctx, st, rec, res)
	}

	// 9. §8.1 item 5.
	o.feedSketches(rec)

	// 10. §8.1 item 4, expressed entirely as one dag.BuildToolUse call.
	o.emitToolGraph(ctx, st, rec, body, superseded)

	// 11. §8.1 item 6, producer side. The warnings are only COLLECTED here; OnUserPrompt drains
	//     them, because PostToolUse has no channel to say anything through.
	if o.opt.Grammar != nil {
		o.opt.Grammar.Append(grammar.Symbol(display))
		switch ExtractTestOutcome(e) {
		case TestPass:
			o.opt.Grammar.Append(grammarTestPass)
		case TestFail:
			o.opt.Grammar.Append(grammarTestFail)
		case TestUnknown:
		}
		o.collectThrash(st)
	}

	// 12. Task-boundary evidence. ExtractSignals stays pure, so the transition detector and the
	//     path normalization are applied here rather than inside it.
	sig := ExtractSignals(e)
	sig.TodoCompleted = o.newlyCompletedTodos(st, e)
	sig.Paths = normalizedPaths(o.opt.ProjectRoot, sig.Paths)
	if sig.TodoCompleted {
		o.count(counterSignalTodo)
	}
	if sig.TestPassed {
		o.count(counterSignalTest)
	}
	if sig.GitCommit {
		o.count(counterSignalGit)
	}
	if o.opt.OnSignals != nil {
		o.opt.OnSignals(e.SessionID, sig)
	}

	// 13. The §6.6 features. LastTS is assigned AFTER features() runs, because GapSeconds is
	//     measured against the PREVIOUS event's timestamp.
	o.recordRecent(st, display, sig.Paths, body, now)
	if fs, ok := o.features(st, now); ok && o.opt.OnFeatures != nil {
		o.opt.OnFeatures(e.SessionID, fs)
	}
	st.LastTS = now

	// 14. §8.1 item 2: this result may later be cleared and replaced by an addressable tombstone.
	if IsCompactable(display) {
		o.count(counterTombstone)
	}
	return hookio.Empty(), nil
}

// canonOptions is the observer's whole contribution to O2 beyond tool/path selection: which
// canonicalizer classes to strip, and whether a near-duplicate signature is worth computing.
//
// A NON-NIL Strip means "this entire canon.Options is mine, MinHash included" on the store's side,
// which is exactly what makes the crlf-only case below turn near-duplicate detection off rather
// than inheriting the store's configured default.
func (o *observer) canonOptions() canon.Options {
	cc := o.opt.Cfg.Store.Canonicalize

	cls := []canon.Class{crlfClass}
	if cc.Enabled {
		for _, s := range cc.Strip {
			if canon.Class(s) != crlfClass {
				cls = append(cls, canon.Class(s))
			}
		}
	}

	return canon.Options{
		Strip:      cls,
		KeepDeltas: true,
		MinHash: sketch.MinHashOptions{
			Enabled:          cc.Enabled && cc.MinHash.Enabled,
			Permutations:     cc.MinHash.Permutations,
			ShingleSize:      minHashShingleSize,
			NearDupThreshold: cc.MinHash.NearDupThreshold,
		},
	}
}

// derivedToolUseID is the identity a tool payload with no tool_use_id is recorded under:
// "tu_<session>_<turn>_<position in the subagent window>".
//
// It is a function rather than an inline format string for the reason SubagentCaptureID is exported
// as one — the free-id probe in identity.go has to spell exactly what this call site spells, and two
// copies of a format string are two chances to disagree about what a record is called.
func derivedToolUseID(s core.SessionID, t core.TurnIndex, n int) core.ToolUseID {
	return core.ToolUseID(fmt.Sprintf("tu_%s_%d_%d", s, t, n))
}

// rememberToolUse appends rec to the SubagentStop window, front-evicting at subagentWindowCap and
// clamping SubagentSince in the SAME operation — otherwise a long subagent run would slice past
// the end of the ring.
//
// It is called for every tool use, ephemeral or not, because a subagent's retrieval calls are part
// of what the parent never held.
func (o *observer) rememberToolUse(st *sessionState, rec store.ToolUseRecord) {
	st.ToolUses = append(st.ToolUses, toolUseLite{
		ID: rec.ID, Root: rec.Root, Tool: rec.Tool, Path: rec.Path, Bytes: rec.Bytes,
	})
	if k := len(st.ToolUses) - subagentWindowCap; k > 0 {
		st.ToolUses = st.ToolUses[k:]
		st.SubagentSince = max(0, st.SubagentSince-k)
	}
}

// normalizedPaths maps each raw path through paths.Norm then paths.Key, dropping entries whose
// Norm errors — an escape above the project root — and preserving order and first-appearance
// dedup. It is the ONLY place Signals.Paths is normalized: ExtractSignals stays pure and returns
// the host's own spelling (see signals.go).
func normalizedPaths(projectRoot string, raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool, len(raw))
	for _, p := range raw {
		n, err := paths.Norm(projectRoot, p)
		if err != nil {
			continue
		}
		key := paths.Key(n)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}
