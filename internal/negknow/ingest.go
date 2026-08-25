package negknow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// This file is §8.3's four elimination sources, minus the DAG heuristic (detector.go): source #1
// the record_eliminated MCP tool, source #2 `/qompack:pin --eliminated`, source #4 an explicit
// user statement — plus the observation ring source #3 scans.
//
// Two rules govern every ingest path here, and both come out of the ledger rather than out of this
// file.
//
// Redaction is NOT re-applied here (ruling R19). Record and Query already redact their free-text
// fields before anything is derived from them, and Deps.Redact must be idempotent on its own
// output, so a second pass in an ingest helper would at best be wasted work and at worst — for a
// redactor that is only idempotent by luck — a key Record and Query can never agree on. The reason
// text handed to store.PutBytes is likewise the caller's own: SP-06's Put path is the redaction
// choke point, and routing agent-authored text through it is precisely what minting evidence from
// the reason buys.
//
// No ingest path ever passes a pre-set ID into Record. Record re-derives the id whenever the
// derived one collides with a stale record's (its TS-advance loop), so a caller-supplied ID is
// silently discarded there; the id that matters is the one Record RETURNS, which is what every
// helper below reads the stored record back by.

// autoDepCandidates is the closed, ordered set of project files an elimination's validity most
// often rests on: the lockfiles that pin the dependency versions a reason cites, and the container
// definitions that pin the runtime it cites. It matches §8.3's own example set — a compose file
// and a lockfile.
//
// It is CLOSED and ORDERED on purpose. A list that grew over time would give the same elimination
// a different staleness baseline depending on when it was recorded, and the order is what makes
// the maxAutoDeps cap deterministic rather than map-iteration-dependent.
var autoDepCandidates = []string{
	"package-lock.json",
	"yarn.lock",
	"pnpm-lock.yaml",
	"go.sum",
	"Cargo.lock",
	"poetry.lock",
	"requirements.txt",
	"Gemfile.lock",
	"composer.lock",
	"docker-compose.yml",
	"docker-compose.yaml",
	"Dockerfile",
}

// maxAutoDeps caps how many dependencies one auto-derived record carries. Each one costs a hash
// comparison on every RefreshStaleness, and a record guarded by every lockfile in a polyglot
// repository would flip stale on any change anywhere — which is the same as having no guard at
// all, because an always-stale elimination is never answered as a block.
const maxAutoDeps = 8

// evidenceTool is the store.PutOptions.Tool every minted-evidence object is attributed to. Every
// source that mints one shares it: it names the OPERATION that produced the text, and §8.3 calls
// that operation record_eliminated whichever surface invoked it.
const evidenceTool = "record_eliminated"

// counterUserStatementUnresolved counts source #4 phrase matches that could not be placed against
// a target or an approach. It is a string, so nothing catches a typo at compile time and a second
// spelling produces a second, silently empty instrument; this is the package's single
// transcription of the subplan's name.
const counterUserStatementUnresolved = "negknow.user_statement.unresolved"

// signalsFileName is records/signals.jsonl's basename: the observer's signal log.
const signalsFileName = "signals.jsonl"

// The compile-time assertions that keep the extended surface and this implementation in sync.
// Open returns the Ledger interface and the concrete type is unexported, so these two are what
// turn "SP-13 cannot reach IngestMCP" into a build error here rather than into a missing feature
// three subplans later.
var (
	_ Maintainer        = (*ledger)(nil)
	_ ObservationSource = (*ledger)(nil)
)

// signalsPath is <root>/.qompack/records/signals.jsonl.
func signalsPath(root string) string {
	return filepath.Join(paths.Of(root).Records, signalsFileName)
}

// resolveDeps turns caller-supplied paths into evidence-bearing core.Dep entries.
//
// A path with no known version in the store cannot serve as a staleness baseline — there is
// nothing for store.ChangedSince to compare the record against — so it is SKIPPED and named in
// warnings rather than recorded with a zero hash that would compare equal to nothing.
//
// The returned order is CANDIDATE order (ruling R13): the target's own path first, then
// autoDepCandidates in the order that list declares them, deduplicated on the paths.Key form with
// the first occurrence winning, capped at maxAutoDeps. It is deliberately NOT sorted here — the
// ledger's normalizeRecord sorts a record's dependencies ascending by path on the way to the log,
// which is what makes a JSONL line byte-stable, and sorting twice would only hide which of the two
// orders a caller is actually looking at.
//
// It takes no lock: deps.Store is written once, in Open, before the ledger escapes to any other
// goroutine, and this must not hold mu because its caller goes on to call Record, which takes it.
func (l *ledger) resolveDeps(ctx context.Context, explicit []string, target string) ([]core.Dep, []string) {
	candidates, warnOnMissing := l.depCandidates(explicit, target)

	var (
		out      []core.Dep
		warnings []string
	)
	seen := make(map[string]struct{}, len(candidates))
	for _, p := range candidates {
		key := paths.Key(p)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}

		h, ok := l.latestRoot(ctx, key)
		if !ok {
			if warnOnMissing {
				warnings = append(warnings,
					fmt.Sprintf("no stored version for %s; not used as a staleness dependency", p))
			}
			continue
		}
		out = append(out, core.Dep{Path: key, Hash: h})
		if len(out) == maxAutoDeps {
			break
		}
	}
	return out, warnings
}

// depCandidates is resolveDeps' step 1: the caller's own list when it supplied one, or the
// derived default otherwise.
//
// It also reports whether a candidate that resolves to nothing is worth warning about. An
// EXPLICIT path always is — the caller named a file and expected it to guard the record. A
// DERIVED one is not: §8.3's default set is "every entry of autoDepCandidates that has a known
// version", so filtering the ones without a version IS the derivation, and a repository with no
// yarn.lock has nothing wrong with it. Warning once per absent lockfile would bury the one warning
// that matters under eleven that never do. The target's own path rides in the derived list under
// the same rule, so a project whose store has not yet seen the target stays quiet rather than
// warning on every single ingest.
func (l *ledger) depCandidates(explicit []string, target string) ([]string, bool) {
	if len(explicit) > 0 {
		return explicit, true
	}

	out := make([]string, 0, 1+len(autoDepCandidates))
	if p, _ := SplitTarget(target); p != "" {
		out = append(out, p)
	}
	return append(out, autoDepCandidates...), false
}

// latestRoot returns the root of key's newest recorded version.
//
// "Newest" is the greatest TS rather than the last entry: store orders index/files.json by TS, but
// this reads the answer out of the values rather than out of the ordering, so a history that
// arrives out of order still yields the version a staleness comparison would be made against.
func (l *ledger) latestRoot(ctx context.Context, key string) (core.Hash, bool) {
	if l.deps.Store == nil {
		return core.Hash{}, false
	}
	vs, err := l.deps.Store.FileHistory(ctx, key)
	if err != nil {
		l.log.Debug("negknow: could not read the file history for a dependency", "path", key, "err", err)
		return core.Hash{}, false
	}

	best := -1
	for i := range vs {
		if vs[i].Root.IsZero() {
			continue
		}
		if best < 0 || vs[i].TS > vs[best].TS {
			best = i
		}
	}
	if best < 0 {
		return core.Hash{}, false
	}
	return vs[best].Root, true
}

// resolveScope reads the caller's scope string, falling back to the configured default.
//
// An unparseable value is a WARNING and not an error: the caller of an MCP tool or a slash command
// has already done the work of eliminating something, and refusing to record it over a typo in an
// enum would lose the elimination entirely. l.elim was normalized at Open, so the fallback is
// always one of the two real scopes.
func (l *ledger) resolveScope(s string) (Scope, []string) {
	switch Scope(s) {
	case ScopeSession, ScopeProject:
		return Scope(s), nil
	case "":
		return Scope(l.elim.DefaultScope), nil
	default:
		return Scope(l.elim.DefaultScope), []string{fmt.Sprintf(
			"scope %q is neither %q nor %q; using %q",
			s, ScopeSession, ScopeProject, l.elim.DefaultScope)}
	}
}

// mintEvidence stores text and returns its root, which is what gives an elimination a real,
// retrievable object behind its claim rather than a hash of something nobody kept.
//
// It is also the redaction choke point for agent-authored text: SP-06's Put path scrubs on the way
// in (store.PutResult.Redacted counts what it replaced), so routing the reason through it is a
// security property and not only a convenience.
//
// Under requireEvidence a failure is ErrNoEvidence and nothing is recorded — the counter the error
// table names is incremented here rather than in Record, because this path returns before Record
// is ever reached. Without requireEvidence a failure degrades to a zero evidence hash and a Warn:
// an elimination with no evidence is still better negative knowledge than no elimination at all,
// and whether that trade is acceptable is exactly what the configuration key decides.
func (l *ledger) mintEvidence(ctx context.Context, text string) (core.Hash, error) {
	if l.deps.Store == nil {
		if l.elim.RequireEvidence {
			l.m.Counter(counterRejectedNoEvidence).Add(1)
			return core.Hash{}, fmt.Errorf("%w: no store to mint one from", ErrNoEvidence)
		}
		return core.Hash{}, nil
	}

	res, err := l.deps.Store.PutBytes(ctx, []byte(text), store.PutOptions{
		Tool:      evidenceTool,
		Ephemeral: false,
	})
	if err != nil {
		if l.elim.RequireEvidence {
			l.m.Counter(counterRejectedNoEvidence).Add(1)
			return core.Hash{}, fmt.Errorf("%w: %w", ErrNoEvidence, err)
		}
		l.log.Warn("negknow: could not store the elimination's evidence; recording without one", "err", err)
		return core.Hash{}, nil
	}
	return res.Root.Hash, nil
}

// ingest appends r and reads the STORED record back.
//
// Reading it back is not a convenience: Record fills in the id, the session, the timestamp, the
// scope and the status, recomputes the descriptor, truncates the free text and sorts the
// dependencies, and a dedup hit returns an id that names an ENTIRELY different record line. What
// SP-13 and SP-14 render is the record the log actually holds, so that is what is returned.
//
// r.ID is cleared first. Record re-derives the id when the derived one collides with a stale
// record's, so a pre-set id is discarded there anyway; clearing it here makes that explicit rather
// than accidental.
func (l *ledger) ingest(ctx context.Context, r Record) (Record, error) {
	r.ID = ""
	id, err := l.Record(ctx, r)
	if err != nil {
		return Record{}, err
	}
	return l.Get(ctx, id)
}

// IngestMCP records one record_eliminated MCP tool call — §8.3's source #1, the most reliable of
// the four because the agent is stating the elimination outright (SP-13).
//
// The returned warnings are advisory and are meant to be rendered back to the agent: a dependency
// that could not be resolved, or a scope that did not parse, changes what was recorded and the
// caller should be able to say so.
func (l *ledger) IngestMCP(ctx context.Context, a MCPArgs) (Record, []string, error) {
	scope, warnings := l.resolveScope(a.Scope)

	deps, depWarnings := l.resolveDeps(ctx, a.DependsOn, a.Target)
	warnings = append(warnings, depWarnings...)

	evidence := a.Evidence
	if evidence.IsZero() {
		h, err := l.mintEvidence(ctx, a.Reason)
		if err != nil {
			return Record{}, warnings, err
		}
		evidence = h
	}

	rec, err := l.ingest(ctx, Record{
		Target:    a.Target,
		Approach:  a.Approach,
		Reason:    a.Reason,
		Evidence:  evidence,
		DependsOn: deps,
		Scope:     scope,
		Source:    SourceMCP,
	})
	if err != nil {
		return Record{}, warnings, err
	}
	return rec, warnings, nil
}

// IngestPin records one `/qompack:pin --eliminated` invocation — §8.3's source #2 (SP-14, which
// owns the flag parsing and the output rendering).
//
// It differs from IngestMCP in exactly one way: a slash command carries evidence as TEXT, so it is
// parsed rather than taken as a hash. A value that does not parse is a warning and then a fall
// through to the mint-from-reason path, never a refusal — a user who typed the hash by hand should
// still get their elimination recorded.
func (l *ledger) IngestPin(ctx context.Context, a PinArgs) (Record, []string, error) {
	scope, warnings := l.resolveScope(a.Scope)

	deps, depWarnings := l.resolveDeps(ctx, a.DependsOn, a.Target)
	warnings = append(warnings, depWarnings...)

	var evidence core.Hash
	if a.Evidence != "" {
		h, err := core.ParseHash(a.Evidence)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"evidence %q is not a \"sha256:<64 hex>\" hash; storing the reason text instead", a.Evidence))
		} else {
			evidence = h
		}
	}
	if evidence.IsZero() {
		h, err := l.mintEvidence(ctx, a.Reason)
		if err != nil {
			return Record{}, warnings, err
		}
		evidence = h
	}

	rec, err := l.ingest(ctx, Record{
		Target:    a.Target,
		Approach:  a.Approach,
		Reason:    a.Reason,
		Evidence:  evidence,
		DependsOn: deps,
		Scope:     scope,
		Source:    SourceSlashCommand,
	})
	if err != nil {
		return Record{}, warnings, err
	}
	return rec, warnings, nil
}

// userStatementPhrases is the CLOSED, ordered list of phrases that count as an explicit user
// elimination — §8.3's source #4 ("that didn't work," "we tried that").
//
// Every entry is written in normalizePrompt's output alphabet: lowercase, apostrophe-free,
// single-spaced. That is what collapses "didn't", "didn’t" and "didnt" onto one entry instead of
// three, and it is why the list is matched against the NORMALIZED prompt and never against the raw
// one.
//
// It is closed for the same reason the stopword list is: a phrase list that grew over time would
// make the same prompt an elimination in one version and not in another, with no signal anywhere
// that the behaviour had changed. The order is the order of the first hit, which only matters for
// the warning text, since every hit produces the same single record.
var userStatementPhrases = []string{
	"that didnt work",
	"that did not work",
	"that doesnt work",
	"that does not work",
	"we tried that",
	"we already tried",
	"i tried that",
	"already tried",
	"that didnt help",
	"didnt fix it",
	"thats not it",
	"that was a dead end",
	"no luck with",
	"we ruled that out",
}

// apostropheStripper removes the two apostrophes a human keyboard produces. Stripping rather than
// replacing is deliberate: "didn't" must become "didnt" and not "didn t", because the phrase list
// is written the first way.
var apostropheStripper = strings.NewReplacer("'", "", "’", "")

// normalizePrompt folds a user prompt into the alphabet userStatementPhrases is written in:
// lowercased, apostrophes removed, every whitespace run collapsed to one space and the ends
// trimmed.
func normalizePrompt(prompt string) string {
	return collapseWS(apostropheStripper.Replace(strings.ToLower(prompt)))
}

// matchUserStatement returns the first phrase of userStatementPhrases that appears in the
// normalized prompt, or "" when none does.
func matchUserStatement(prompt string) string {
	normalized := normalizePrompt(prompt)
	for _, phrase := range userStatementPhrases {
		if strings.Contains(normalized, phrase) {
			return phrase
		}
	}
	return ""
}

// IngestUserStatement records an elimination a user stated outright — §8.3's source #4 (SP-08
// feeds this from UserPromptSubmit).
//
// It returns a slice because a prompt is a natural place for more than one elimination to appear;
// this version recognizes at most one, and the shape is what lets that change without changing
// SP-08's call site.
//
// The refusal in step 4 is the load-bearing part. A phrase matched with no resolvable target, or
// with no approach text from the preceding assistant turn, produces NOTHING: guessing which file
// the user meant would manufacture a wrong elimination, and a wrong elimination that blocks a
// viable approach is the High-severity failure §12 rates this whole feature against. A counter is
// the right response to "we heard it and could not place it"; a guess is not.
func (l *ledger) IngestUserStatement(ctx context.Context, p UserStatement) ([]Record, error) {
	if matchUserStatement(p.Prompt) == "" {
		return nil, nil
	}
	if (p.Path == "" && p.Symbol == "") || p.Approach == "" {
		l.m.Counter(counterUserStatementUnresolved).Add(1)
		l.log.Debug("negknow: a user statement matched but named no target or approach",
			"turn", int(p.Turn), "path", p.Path, "symbol", p.Symbol)
		return nil, nil
	}

	target := p.Path
	if p.Symbol != "" {
		target = p.Path + ":" + p.Symbol
	}

	reason, _ := truncateUTF8("user stated: "+strings.TrimSpace(p.Prompt), maxReasonBytes)

	evidence := p.PromptRoot
	if evidence.IsZero() {
		h, err := l.mintEvidence(ctx, p.Prompt)
		if err != nil {
			return nil, err
		}
		evidence = h
	}

	deps, warnings := l.resolveDeps(ctx, nil, target)
	for _, w := range warnings {
		l.log.Debug("negknow: user-statement dependency resolution", "warning", w)
	}

	rec, err := l.ingest(ctx, Record{
		Target:    target,
		Approach:  p.Approach,
		Reason:    reason,
		Evidence:  evidence,
		DependsOn: deps,
		Source:    SourceUserStatement,
	})
	if err != nil {
		return nil, err
	}
	return []Record{rec}, nil
}

// Observe records one observer signal — SP-08 calls it from PostToolUse — in both places the
// heuristic detector's inputs live.
//
// The durable half is records/signals.jsonl, appended to through paths.AppendOnly exactly as the
// elimination log is. It is NOT a source of truth for anything: it is regenerable from the
// transcript and GC-eligible, and the detector reads the in-memory ring rather than the file. The
// handle is opened and closed per call rather than held for the ledger's lifetime, because Close
// is the elimination log's and a second long-lived handle on Windows would block the very
// t.TempDir cleanup that catches leaked handles.
//
// The bounded half is the ring: signalRing entries, oldest dropped first. A long session must not
// grow an unbounded signal history for a detector whose window is detectWindowTurns turns wide.
//
// When nobody calls this, the detector simply finds nothing and source #3 is inert — which is a
// missing feature, never a correctness risk.
func (l *ledger) Observe(ctx context.Context, o Observation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return os.ErrClosed
	}

	if err := l.appendSignal(o); err != nil {
		// The ring is still updated: the detector reads the ring, so a signals log that could not
		// be written costs the regenerable half and not the working half.
		l.log.Warn("negknow: could not append to the signals log", "path", signalsPath(l.root), "err", err)
	}

	l.ring = append(l.ring, o)
	if len(l.ring) > signalRing {
		// Drop the oldest in place. Re-slicing alone would keep the whole prefix alive behind the
		// slice header, so the "bounded" ring would grow without bound anyway.
		copy(l.ring, l.ring[len(l.ring)-signalRing:])
		l.ring = l.ring[:signalRing]
	}
	return nil
}

// signalWire is one records/signals.jsonl line.
//
// Observation itself carries no json tags — it is an in-memory value type SP-08 hands across a
// function call, not a wire shape — so the tags live here rather than on it. They are lowercase
// snake_case like every other JSONL line in this tree, and this file is deliberately NOT a
// contract: nothing reads it back, no golden fixture pins it, and it is regenerable from the
// transcript and GC-eligible. The reason to spell the keys out anyway is that a signals log dumped
// during a support session should read like the other logs beside it rather than like Go field
// names that leaked.
type signalWire struct {
	Turn    core.TurnIndex `json:"turn"`
	Kind    ObsKind        `json:"kind"`
	Path    string         `json:"path"`
	Symbol  string         `json:"symbol,omitempty"`
	Detail  string         `json:"detail,omitempty"`
	ToolUse core.ToolUseID `json:"tool_use,omitempty"`
	Root    string         `json:"root,omitempty"`
	TS      core.UnixMilli `json:"ts,omitempty"`
}

// wire projects o into its signals.jsonl form. A zero Root is omitted rather than written as the
// all-zero digest, which is this package's "unset" sentinel everywhere else.
func (o Observation) wire() signalWire {
	w := signalWire{
		Turn:    o.Turn,
		Kind:    o.Kind,
		Path:    o.Path,
		Symbol:  o.Symbol,
		Detail:  o.Detail,
		ToolUse: o.ToolUse,
		TS:      o.TS,
	}
	if !o.Root.IsZero() {
		w.Root = o.Root.String()
	}
	return w
}

// appendSignal writes one observation to records/signals.jsonl. The caller holds mu.
func (l *ledger) appendSignal(o Observation) error {
	p := signalsPath(l.root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), logDirPerm); err != nil {
		return fmt.Errorf("negknow: create %s: %w", filepath.Dir(p), err)
	}
	w, err := paths.AppendOnly(p)
	if err != nil {
		return fmt.Errorf("negknow: open %s: %w", p, err)
	}
	defer func() { _ = w.Close() }()
	return appendLine(w, o.wire())
}

// Since returns a copy of every ring entry at or after turn, in ascending (Turn, Kind, Path)
// order: the ObservationSource contract NewDetector's caller reaches through
// `led.(negknow.ObservationSource)`.
//
// The entries are COPIES. Observation is a flat value type, so copying the slice is enough — a
// caller that mutates what it was handed cannot reach back into the ledger's own history through
// the alias, which matters because the detector rewrites nothing but a future one might.
//
// A fresh ring answers with an empty, non-nil slice rather than nil: the detector ranges over the
// result, and an explicit empty answer is what distinguishes "nothing observed" from "the source
// declined to answer".
func (l *ledger) Since(turn core.TurnIndex) []Observation {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]Observation, 0, len(l.ring))
	for _, o := range l.ring {
		if o.Turn >= turn {
			out = append(out, o)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Turn != out[j].Turn {
			return out[i].Turn < out[j].Turn
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out
}
