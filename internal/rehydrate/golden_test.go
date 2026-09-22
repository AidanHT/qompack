package rehydrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
	"github.com/qompack/qompack/internal/tokens"
)

// The frozen §8.6 payload fixtures. They are the one place the RENDERED injection is pinned
// byte-for-byte: every other test in this package asserts on a builder, a unit, or a count, and
// none of them would notice a heading reworded, a blank line lost between sections, or an item
// emitted in the wrong place.
//
// That matters more here than in most golden sets, because these bytes are not an internal
// format. They are what the model reads after a compaction, in the order §8.6 fixes, and the
// order is what budget truncation drops from the tail of. A diff in these files is a change to
// what an agent knows when its context is rebuilt.
//
// Rule W-2 applies in full: the checkpoints these are built from are decoded from the frozen
// contract fixture or assigned field by field, never constructed through checkpoint.Writer, which
// belongs to a same-wave sibling.
const goldenRehydrateDir = "../../testdata/golden/rehydrate"

// goldenRuleDir holds the rule bodies item 6a restores verbatim. They live on disk rather than in
// a string literal for two reasons: a restored rule IS a file's contents, so the fixture should be
// a file; and it makes the bulk of the payload goldens reviewable as a diff against their source
// rather than as a wall of embedded text.
const goldenRuleDir = goldenRehydrateDir + "/input"

// goldenSession identifies the fixture session in every payload header. It is the frozen contract
// checkpoint's own value, so the goldens read as one coherent scenario.
const goldenSession = core.SessionID("sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A")

// goldenEmitted is the instant state.json is frozen at: just after the contract corpus's own
// window, so the fixture set reads as one session rather than as unrelated timestamps. Build
// itself reads no clock — this is the daemon-stamped field, supplied here explicitly.
const goldenEmitted = core.UnixMilli(1767225600000)

// stateGoldenCase names the case whose State is frozen as state.json. full-8k is chosen because it
// is the only case that carries all three drop sources at once — the checkpoint's own two entries,
// construction drops from items 6a and 6b, and budget-truncation drops — and because two of its
// items are Truncated, which is the flag nothing else in the fixture set would pin.
const stateGoldenCase = "full-8k"

// degradedBudget is the "degraded" case's budget: far below the §8.6 band, chosen so that the
// injection wrapper and tier 1 still fit and everything after them does not. It is a fixture
// parameter, not a configuration default — no config key names it.
const degradedBudget = core.Tokens(400)

// maxBudget and minBudget are the two ends of the §8.6 band, read from the shipped defaults rather
// than written as literals: 12000 and 8000 are both §11.6 forbidden integers, and a fixture that
// spelled them would pin the band against a copy of the config instead of against the config.
func maxBudget() core.Tokens { return core.Tokens(testCfg().Runtime.Rehydrate.MaxTokens) }
func minBudget() core.Tokens { return core.Tokens(testCfg().Runtime.Rehydrate.MinTokens) }

// goldenCase is one frozen payload.
type goldenCase struct {
	name string
	req  Request
	deps Deps
	// why states what this case exists to pin, and is printed on failure so that a reader who has
	// just broken one golden knows whether the break is the point.
	why string
}

// goldenCases is the frozen set. It is built fresh per test so that no case can observe another's
// mutations of a fake.
func goldenCases(t *testing.T) []goldenCase {
	t.Helper()
	full := ckFull(t)
	return []goldenCase{
		{
			name: "full-12k",
			req:  goldenRequest(full, maxBudget()),
			deps: goldenDeps(t, full),
			why: "the whole §8.6 injection at the top of the 8-12K band: all ten kinds present, " +
				"every restored rule whole, and only the checkpoint's own drops in section 7",
		},
		{
			name: "full-8k",
			req:  goldenRequest(full, minBudget()),
			deps: goldenDeps(t, full),
			why: "the same material at the bottom of the band: item 6a restores FEWER rules, each " +
				"still whole (G4.3), and section 7 names the ones that no longer fit",
		},
		{
			name: "minimal",
			req:  goldenRequest(ckMinimal(), maxBudget()),
			deps: fullDeps(t, ckMinimal()),
			why: "a tier-1-only checkpoint: no eliminations means no standing instruction, and an " +
				"absent item renders no heading at all rather than an empty section",
		},
		{
			name: "no-checkpoint",
			req:  goldenNoCheckpointRequest(),
			deps: goldenDeps(t, ckEmpty()),
			why: "the SessionStart-compact path with no checkpoint to read: items 2, 3, 6b, 7 and " +
				"8 still build from L0, the ledger and the skill index (§12.3)",
		},
		{
			name: "degraded",
			req:  goldenRequest(full, degradedBudget),
			deps: goldenDeps(t, full),
			why: "a budget far below the §8.6 band: tier 1 survives, everything else is dropped " +
				"and named, and section 7 itself truncates to a counted line",
		},
	}
}

// goldenRequest is the compact rehydration request every payload golden is built from. It fixes
// ProjectRoot to a POSIX literal so the goldens are byte-identical on Windows and Linux.
func goldenRequest(cp checkpoint.Checkpoint, budget core.Tokens) Request {
	var r Request
	r.Session = cp.Session
	r.Source = sourceCompact
	r.ProjectRoot = "/repo"
	r.Budget = budget
	r.Checkpoint = cp
	r.Ref.Seq = cp.Seq
	r.Ref.Path = "/repo/.qompack/checkpoints/0001.json"
	r.Cfg = testCfg()
	return r
}

// goldenNoCheckpointRequest is what the daemon builds when Reader.Latest reports ErrNotFound or
// ErrNotImplemented: a zero Checkpoint and Ref{Seq:0}, with the session id still known because it
// came from the hook event rather than from the checkpoint.
func goldenNoCheckpointRequest() Request {
	r := goldenRequest(ckEmpty(), maxBudget())
	r.Session = goldenSession
	r.Ref = checkpoint.Ref{}
	return r
}

// goldenDeps is the rich collaborator set: an L0 capture that agrees with the checkpoint, the
// checkpoint's own eliminations at project scope, seven restorable rules, and a skill set that
// overflows both the indexer's own reserve and the payload's.
func goldenDeps(t *testing.T, cp checkpoint.Checkpoint) Deps {
	t.Helper()
	d := fullDeps(t, cp)
	d.Skills = &fakeIndexer{all: goldenSkills(), kept: goldenKeptSkills()}

	// A checkpoint with no file pointers is a checkpoint the rule scanner has nothing to scan
	// FROM: both PathScoped and NestedClaudeMD are given the pointer set, and a real scanner
	// answers an empty one with an empty result. Scripting rules into that case anyway would
	// freeze a payload no scanner could produce, so the empty scanner stays.
	if len(cp.Pointers.Files) == 0 {
		// The no-checkpoint case has no UserIntent of its own either; L0 is the only source item 2
		// has, and pinning that it still fires is the point of that golden.
		d.Store = newFakeStore().withPrompt(firstPromptID(goldenSession), goldenSession, 0,
			"Fix the intermittent 500s on POST /api/session/refresh. They started after the "+
				"connection-pool change last Tuesday.")
		d.Ledger = newFakeLedger().withActive(negknow.ScopeProject, goldenEliminations()...)
		return d
	}
	d.Rules = &fakeScanner{pathScoped: goldenPathRules(t), nested: goldenNestedRules(t)}
	return d
}

// goldenPathRules are the six `paths:`-scoped rules whose globs match one of the frozen
// checkpoint's two file pointers, src/auth.ts and docker-compose.yml.
//
// EVERY rule here matches at least one pointer, and that is a correctness property of the fixture
// rather than a nicety: PathScoped is defined as "every rule whose globs match any path in
// pointers", so a scripted rule matching neither could not have come from a real scan, and its
// drop entry would lose the "matched <pointer>; " clause that tells the agent why a rule it no
// longer has was relevant in the first place.
//
// They are returned in arbitrary order on purpose: buildRestoredInstructions sorts by Path, and a
// golden built from a pre-sorted fixture could not tell whether it does.
func goldenPathRules(t *testing.T) []rules.Rule {
	t.Helper()
	return []rules.Rule{
		mustRule(t, "db-conventions.md", ".claude/rules/db-conventions.md",
			[]string{"src/db/**", "docker-compose.yml"}, false),
		mustRule(t, "api-conventions.md", ".claude/rules/api-conventions.md",
			[]string{"src/api/**", "src/auth.ts"}, false),
		mustRule(t, "testing-conventions.md", ".claude/rules/testing-conventions.md",
			[]string{"**/*.ts", "test/**"}, false),
		mustRule(t, "auth-conventions.md", ".claude/rules/auth-conventions.md",
			[]string{"src/auth.ts", "src/auth/**"}, false),
		mustRule(t, "infra-conventions.md", ".claude/rules/infra-conventions.md",
			[]string{"docker-compose.yml", "infra/**"}, false),
		mustRule(t, "security-conventions.md", ".claude/rules/security-conventions.md",
			[]string{"src/auth.ts", "src/api/**", "infra/**"}, false),
	}
}

// goldenNestedRules is the one nested CLAUDE.md the pointer set reaches: src/auth.ts lives in
// src/, so src/CLAUDE.md is in scope and the project root's own CLAUDE.md is not — the host
// re-injects that one itself (§2.7).
func goldenNestedRules(t *testing.T) []rules.Rule {
	t.Helper()
	return []rules.Rule{mustRule(t, "src-claude.md", "src/CLAUDE.md", nil, true)}
}

// mustRule loads one rule body from goldenRuleDir and prices it with the same baseline estimator
// the payload goldens are built under.
func mustRule(t *testing.T, file, rulePath string, globs []string, nested bool) rules.Rule {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenRuleDir, file))
	require.NoError(t, err, "rule body fixture missing")
	body := string(b)
	return rules.Rule{
		Path:   rulePath,
		Globs:  globs,
		Body:   body,
		Tokens: fakeEstimator{}.EstimateString(body, tokens.ClassProse),
		Nested: nested,
	}
}

// goldenEliminations is the ledger's answer on the no-checkpoint path, where there is no
// Checkpoint.Eliminated to serve. One active project-scoped record is enough: item 3's job on this
// branch is to prove the ledger is consulted at all when the checkpoint is absent.
func goldenEliminations() []negknow.Record {
	return []negknow.Record{{
		ID: "elim_3f9b2c7d1a48", Session: goldenSession, TS: core.UnixMilli(1767225480000),
		Target:   "src/auth.ts:refreshToken",
		Approach: "widen pool timeout",
		Reason: "pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the " +
			"widened timeout never takes effect",
		Scope: negknow.ScopeProject, Status: negknow.StatusActive,
	}}
}

// goldenSkills is the project's full skill set, as Index(root, 0) reports it.
//
// Twenty-four entries is not padding. Item 6b makes two DIFFERENT drops, and a smaller set would
// only ever produce one of them: a skill the INDEXER could not fit in its skillIndexTokens reserve
// is reported as "not in the compact skill index", while a skill the indexer kept and the PAYLOAD
// then could not afford is reported as a budget drop. The set is sized so both appear.
func goldenSkills() []skills.Entry {
	return []skills.Entry{
		{Name: "api-lint", Description: "Check a route against the API conventions: codes, pagination, idempotency.", Source: ".claude/skills/api-lint/SKILL.md"},
		{Name: "changelog-bump", Description: "Bump the version and update the changelog per the release protocol.", Source: ".claude/skills/changelog-bump/SKILL.md"},
		{Name: "code-review", Description: "Review a diff for correctness, security, and convention drift before merge.", Source: ".claude/skills/code-review/SKILL.md"},
		{Name: "compose-doctor", Description: "Diagnose a docker compose environment that no longer reproduces a bug.", Source: ".claude/skills/compose-doctor/SKILL.md"},
		{Name: "dep-audit", Description: "Audit dependencies for advisories and pin drift; propose the minimal upgrade.", Source: ".claude/skills/dep-audit/SKILL.md"},
		{Name: "flaky-triage", Description: "Quarantine a flaky test, assign an owner, and write the reproduction note.", Source: ".claude/skills/flaky-triage/SKILL.md"},
		{Name: "incident-writeup", Description: "Draft an incident report from a timeline, with contributing factors.", Source: ".claude/skills/incident-writeup/SKILL.md"},
		{Name: "load-test", Description: "Run and read the load suite under test/load, including refresh-storm.", Source: ".claude/skills/load-test/SKILL.md"},
		{Name: "log-grep", Description: "Find the structured log lines for one session across the retained window.", Source: ".claude/skills/log-grep/SKILL.md"},
		{Name: "migration-runner", Description: "Plan and run a forward-only migration, including the backfill split.", Source: ".claude/skills/migration-runner/SKILL.md"},
		{Name: "onboard", Description: "Walk a new contributor through the repository layout and review protocol.", Source: ".claude/skills/onboard/SKILL.md"},
		{Name: "perf-profile", Description: "Capture and read a CPU or allocation profile; name the top three costs.", Source: ".claude/skills/perf-profile/SKILL.md"},
		{Name: "pool-watch", Description: "Read the connection-pool metrics and say whether the cliff is near.", Source: ".claude/skills/pool-watch/SKILL.md"},
		{Name: "release-notes", Description: "Assemble release notes from merged pull requests by user-visible change.", Source: ".claude/skills/release-notes/SKILL.md"},
		{Name: "repro-protocol", Description: "Run the pool-exhaustion reproduction end to end and record the result.", Source: ".claude/skills/repro-protocol/SKILL.md"},
		{Name: "schema-diff", Description: "Diff two database schemas and explain the lock each change would take.", Source: ".claude/skills/schema-diff/SKILL.md"},
		{Name: "secret-scan", Description: "Scan a diff for credentials and explain why each hit is or is not a leak.", Source: ".claude/skills/secret-scan/SKILL.md"},
		{Name: "tf-plan-review", Description: "Read a terraform plan and flag destructive or lock-taking changes.", Source: ".claude/skills/tf-plan-review/SKILL.md"},
		{Name: "token-forensics", Description: "Trace one refresh-token family through mint, rotation, and revocation.", Source: ".claude/skills/token-forensics/SKILL.md"},
		{Name: "trace-read", Description: "Read a distributed trace and attribute latency to a span rather than a service.", Source: ".claude/skills/trace-read/SKILL.md"},
		{Name: "triage-500s", Description: "Attribute a burst of 500s to a dependency, a deploy, or a saturation cliff.", Source: ".claude/skills/triage-500s/SKILL.md"},
		{Name: "tx-audit", Description: "Find transactions that await something other than a database call.", Source: ".claude/skills/tx-audit/SKILL.md"},
		{Name: "webhook-replay", Description: "Replay a webhook delivery against a local build without touching production.", Source: ".claude/skills/webhook-replay/SKILL.md"},
		{Name: "why-flaky", Description: "Bisect a flaky test to the shared state or the wall-clock read that causes it.", Source: ".claude/skills/why-flaky/SKILL.md"},
	}
}

// goldenKeptSkills is what Index(root, skillIndexTokens) returns: the greedy prefix of
// goldenSkills that fits the 450-token reserve, priced exactly as item 6b renders it.
//
// It is COMPUTED rather than hand-listed so the fixture cannot drift out of agreement with itself
// — a hand-listed cut is a number that stops being true the moment a description is reworded, and
// the golden would then be pinning an indexer that keeps more than its budget allows.
func goldenKeptSkills() []skills.Entry {
	budget := core.Tokens(testCfg().Runtime.Rehydrate.SkillIndexTokens)
	var spent core.Tokens
	out := make([]skills.Entry, 0, len(goldenSkills()))
	for _, e := range goldenSkills() {
		cost := fakeEstimator{}.EstimateString("- "+e.Name+": "+e.Description+"\n", 0)
		if spent+cost > budget {
			break
		}
		spent += cost
		out = append(out, e)
	}
	return out
}

// goldenSkillBodies is the per-skill body size skillBodyTokens is scripted with, in tokens. Two
// entries exceed the host's 5K per-skill cap and the set exceeds its 25K total, which is what
// makes item 6b's §2.7 partial-restoration warnings appear.
//
// Scripting it is what keeps the goldens hermetic: the real skills.BodyTokens reads SKILL.md from
// disk, and the fixture project root is a POSIX literal that exists on no machine.
func goldenSkillBodies() map[string]core.Tokens {
	return map[string]core.Tokens{
		"api-lint": 900, "changelog-bump": 420, "code-review": 2100, "compose-doctor": 1350,
		"dep-audit": 780, "flaky-triage": 640, "incident-writeup": 1180, "load-test": 1500,
		"log-grep": 350, "migration-runner": 7400, "onboard": 6100, "perf-profile": 1900,
		"pool-watch": 720, "release-notes": 540, "repro-protocol": 1620, "schema-diff": 1240,
		"secret-scan": 860, "tf-plan-review": 1100, "token-forensics": 980, "trace-read": 1310,
		"triage-500s": 1040, "tx-audit": 760, "webhook-replay": 890, "why-flaky": 1120,
	}
}

// TestBuild_Golden_PayloadsMatchFrozenBytes is the byte-level assertion on the rendered injection.
//
// It is deliberately not table-driven over a shared Build result: each case rebuilds its own
// fakes, because a scanner or indexer fake records the calls made against it and a shared one
// would let case order change what a later case sees.
func TestBuild_Golden_PayloadsMatchFrozenBytes(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	for _, c := range goldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			res, err := Build(context.Background(), c.req, c.deps)
			require.NoError(t, err)

			want, err := os.ReadFile(filepath.Join(goldenRehydrateDir, c.name+".txt"))
			require.NoError(t, err, "frozen payload fixture missing")

			require.Equal(t, string(want), res.Text,
				"the rehydration payload drifted from its frozen fixture.\n"+
					"This golden pins: %s\n"+
					"These bytes are what the model reads after a compaction, in the §8.6 "+
					"importance order that budget truncation drops from the tail of. Read the "+
					"diff before re-recording it: a golden accepted without reading is a "+
					"placeholder.", c.why)

			// The payload never exceeds its budget, at either end of the band or below it.
			require.LessOrEqual(t, int(res.Tokens), int(c.req.Budget),
				"%s: Result.Tokens must never exceed Request.Budget", c.name)
		})
	}
}

// TestBuild_Golden_PayloadsRoundTripThroughUnwrap is the other half of the freeze: the frozen bytes must
// be consumable by the §8.5 stripper, not merely producible by the renderer. A payload the daemon
// can emit but the next transcript read cannot strip back out would be re-encoded into the very
// checkpoint it came from (§4.6).
func TestBuild_Golden_PayloadsRoundTripThroughUnwrap(t *testing.T) {
	for _, c := range goldenCases(t) {
		t.Run(c.name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(goldenRehydrateDir, c.name+".txt"))
			require.NoError(t, err)

			body, seq, ok := Unwrap(string(b))
			require.True(t, ok, "the frozen payload is not recognizable as an injection")
			require.Equal(t, c.req.Ref.Seq, seq, "the open tag must carry the checkpoint sequence")
			require.NotEmpty(t, body)
			require.Equal(t, string(b), Wrap(seq, body),
				"Wrap(Unwrap(golden)) must reproduce the golden exactly")
		})
	}
}

// TestState_MatchesFrozenGolden pins the on-disk shape of the drop-report state file.
//
// The file is not an internal detail: the `dropped` retrieval tool reads it, and it outlives the
// injection whose section 7 was budgeted and may have been truncated to a counted line. A field
// renamed here is a wire-format change to the one artifact that can still answer "what did I
// lose".
func TestState_MatchesFrozenGolden(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	var c goldenCase
	for _, gc := range goldenCases(t) {
		if gc.name == stateGoldenCase {
			c = gc
		}
	}
	require.Equal(t, stateGoldenCase, c.name, "the state golden's case is missing from the set")

	res, stats, err := BuildWithStats(context.Background(), c.req, c.deps)
	require.NoError(t, err)

	// Emitted is the daemon's stamp, not Build's: rehydrate.Build reads no clock at all, which is
	// what lets this golden be byte-stable in the first place.
	current := State{
		Session:  c.req.Session,
		Seq:      res.Seq,
		Emitted:  goldenEmitted,
		Tokens:   res.Tokens,
		Budget:   c.req.Budget,
		Items:    stats,
		Dropped:  res.Dropped,
		Degraded: res.Degraded,
	}

	want, err := os.ReadFile(filepath.Join(goldenRehydrateDir, "state.json"))
	require.NoError(t, err, "the frozen state fixture is missing")

	// Rule W-2's round trip: the frozen bytes must decode back into the declared type without
	// loss, or the first implementation to read them would silently drop a field.
	var back State
	require.NoError(t, json.Unmarshal(want, &back))
	again, err := json.MarshalIndent(back, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(want), string(again)+"\n",
		"State does not model every field its own frozen fixture carries")

	// V6 section 5 replaces additive fragment estimates with an estimate of the
	// complete payload. Keep the original golden bytes and its lossless reader
	// contract above; compare every current state field except the retired token
	// prices. Check the replacement criterion against the independently frozen
	// rendered payload, not a reconstruction through the implementation renderer.
	frozenText, err := os.ReadFile(filepath.Join(goldenRehydrateDir, c.name+".txt"))
	require.NoError(t, err)
	require.Equal(t, string(frozenText), res.Text, "the payload itself remains frozen")
	require.Equal(t, c.deps.Tokens.Estimate(frozenText, tokens.ClassProse), current.Tokens)
	var allocated core.Tokens
	for i := range current.Items {
		require.GreaterOrEqual(t, int(current.Items[i].Tokens), 0)
		allocated += current.Items[i].Tokens
		current.Items[i].Tokens = 0
	}
	require.Equal(t, current.Tokens, allocated, "item shares account for the assembled estimate")
	current.Tokens = 0
	back.Tokens = 0
	for i := range back.Items {
		back.Items[i].Tokens = 0
	}
	gotShape, err := json.MarshalIndent(current, "", "  ")
	require.NoError(t, err)
	wantShape, err := json.MarshalIndent(back, "", "  ")
	require.NoError(t, err)
	require.Equal(t, string(wantShape), string(gotShape),
		"all non-price state fields and the frozen wire shape remain compatible")
}

// TestState_GoldenAccountsForEveryToken is the arithmetic the state file must satisfy: the total
// is exactly the sum over items, because the injection wrapper is charged to the first emitted
// item rather than to a synthetic overhead row. An independent overhead row would make this
// identity false by construction, which is why there is not one.
func TestState_GoldenAccountsForEveryToken(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(goldenRehydrateDir, "state.json"))
	require.NoError(t, err)
	var st State
	require.NoError(t, json.Unmarshal(b, &st))

	var sum core.Tokens
	for i, it := range st.Items {
		sum += it.Tokens
		// Rank is the 0-based position among EMITTED items, so it is contiguous by construction —
		// an omitted kind consumes no rank, and the hard-cap eviction closes the hole it leaves.
		require.Equal(t, i, it.Rank, "%s: ranks must be contiguous from 0", it.Kind)
		require.GreaterOrEqual(t, it.Units, 1,
			"%s: an item with no admitted units renders no section and must not be recorded", it.Kind)
		// Units is NOT bounded by UnitsSeen in general, and the two exceptions are both real:
		// item 3 admits a fixed standing-instruction note that is not one of the ranked
		// candidates seen counts, and item 7 may append an "and N more" line of its own. Every
		// other kind is 1:1, which is what makes the pair readable at all.
		if it.Kind != ItemEliminations.String() && it.Kind != ItemDropReport.String() {
			require.LessOrEqual(t, it.Units, it.UnitsSeen,
				"%s: more units admitted than were ever built", it.Kind)
		}
		require.GreaterOrEqual(t, it.UnitsSeen, 1,
			"%s: an emitted item must record how many units it had to choose from", it.Kind)
	}
	require.Equal(t, int(st.Tokens), int(sum),
		"State.Tokens must be exactly the sum over State.Items")
	require.LessOrEqual(t, int(st.Tokens), int(st.Budget),
		"State.Tokens must not exceed the budget it was built under")
}
