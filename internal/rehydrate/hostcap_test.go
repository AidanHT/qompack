package rehydrate

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/rules"
	"github.com/qompack/qompack/internal/skills"
)

// Owner decision D5 (2026-09-22, V6 close-out C1.14): the post-compaction rehydration must fit
// under the host's 10,000-character additionalContext cap. Over it, Claude Code hands the model a
// file path and a 2,000-character preview and does not ask it to read the file — a real 2.1.280
// session showed the model quoting only what the preview held
// (plans/sdd/V6-closeout/packaging/evidence/review/f2-live-host-cap-probe). These tests pin the
// ceiling on the field the host actually receives: Build's payload, plus the contract-probe line
// the daemon appends, run through the hook client's own ConformOutput and HostCapOverruns.

// probeEmitted is the instant the tests mint the daemon's contract probe at. The probe's length
// does not depend on it; it is fixed so the tests are deterministic.
const probeEmitted = core.UnixMilli(1767225600000)

// probeLine is exactly what the daemon's session.start route appends to a non-empty rehydration:
// a newline and contract.RenderSentinel (internal/daemon/handlers.go).
func probeLine(sess core.SessionID) string {
	return "\n" + contract.RenderSentinel(contract.MintSentinel(sess, probeEmitted))
}

// hostOutput is what the hook client writes to the host for a compact rehydration of res: the
// daemon's additionalContext (payload + probe line), reduced by ConformOutput to what the host
// accepts for SessionStart. An empty payload is answered with the probe alone, as the daemon does.
func hostOutput(res Result, sess core.SessionID) hookio.Output {
	field := strings.TrimPrefix(probeLine(sess), "\n")
	if res.Text != "" {
		field = res.Text + probeLine(sess)
	}
	return hookio.ConformOutput(hookio.EventSessionStart, hookio.SessionStartOutput(field))
}

// requireInsideTheHostCeiling is the D5 assertion on one Build result: the payload is inside
// PayloadCeilingChars, the whole host field is inside HostContextCeilingChars, and the hook
// client's Loud overrun path (hookio.HostCapOverruns, internal/cli hookclient) has nothing to say.
func requireInsideTheHostCeiling(t require.TestingT, res Result, sess core.SessionID) {
	require.LessOrEqual(t, hostChars(res.Text), PayloadCeilingChars,
		"the rendered payload is over the rehydration ceiling")
	out := hostOutput(res, sess)
	require.NotNil(t, out.HookSpecificOutput)
	require.LessOrEqual(t, hookio.HostChars(out.HookSpecificOutput.AdditionalContext), HostContextCeilingChars,
		"the additionalContext the host receives is over the D5 ceiling")
	require.Empty(t, hookio.HostCapOverruns(out),
		"a rehydration must never reach the host's file-path fallback, so the hook client's Loud "+
			"overrun path is unreachable for it")
}

// TestHostCeiling_SitsUnderTheHostCapWithHeadroom ties the ceiling to the host's own number: the
// whole field stays 500 characters under hookio.HostFieldMaxChars, and the payload ceiling is what
// is left once the probe line's reserve is taken out.
func TestHostCeiling_SitsUnderTheHostCapWithHeadroom(t *testing.T) {
	require.Equal(t, 500, hookio.HostFieldMaxChars-HostContextCeilingChars,
		"D5 fixes the ceiling at 9,500 host characters: 500 of headroom under the host's 10,000")
	require.Equal(t, HostContextCeilingChars-probeReserveChars, PayloadCeilingChars)
	require.Positive(t, PayloadCeilingChars)
}

// TestHostCeiling_ProbeReserveCoversTheProbeLine pins the one number rehydrate cannot compute for
// itself: the daemon's probe line is appended after Build returns, from a package rehydrate may not
// import, so its length is reserved here and checked against the real renderer for session ids of
// every shape — the token is a fixed prefix plus twelve hex digits whatever the id is.
func TestHostCeiling_ProbeReserveCoversTheProbeLine(t *testing.T) {
	for _, sess := range []core.SessionID{
		"", "e2e-1", goldenSession, core.SessionID(strings.Repeat("\U0001F600", 4000)),
	} {
		require.LessOrEqual(t, hookio.HostChars(probeLine(sess)), probeReserveChars, "session %q", sess)
	}
}

// TestHostChars_AgreesWithTheHookClient ties rehydrate's restated counter to the one the hook
// client measures with, over every shape the unit distinction matters for: ASCII, a two-byte and a
// three-byte BMP rune (one unit each), an astral rune (two units), invalid UTF-8 (one U+FFFD each),
// and newlines.
func TestHostChars_AgreesWithTheHookClient(t *testing.T) {
	for _, s := range []string{
		"", "abc", "é", "中文", "\U0001F600", "a\U0001F600b", string([]byte{0xff, 0xfe, 'x'}),
		"line one\nline two\r\n", strings.Repeat("\u2028", 3),
	} {
		require.Equal(t, hookio.HostChars(s), hostChars(s), "%q", s)
	}
	require.Equal(t, 2, hostChars("\U0001F600"), "an astral rune is two UTF-16 units")
	require.Equal(t, 1, hostChars("中"), "a BMP rune is one unit however many UTF-8 bytes it takes")
}

// TestBuild_HostCeiling_LargeSessionArrivesInline is D5's regression case. The golden "full"
// fixture offers far more than the host delivers whole — before D5 it rendered to a 32,053-byte
// payload at the default budget, three times the cap — and the rehydration must now arrive inline:
// inside the ceiling, tier 1 whole and first, every later item in §8.6 order, no record cut, and
// every record left out named in section 7 or in its counted tail with a pointer that restores it.
func TestBuild_HostCeiling_LargeSessionArrivesInline(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = fakeBodyTokens(goldenSkillBodies())
	t.Cleanup(func() { skillBodyTokens = restore })

	full := ckFull(t)
	d := goldenDeps(t, full)
	req := goldenRequest(full, maxBudget())

	// Fixture sanity: the material on offer is far over the host's cap, so the ceiling binds.
	var offered int
	for _, b := range buildAll(context.Background(), req, d, nil) {
		for _, u := range b.units {
			offered += hostChars(u.text)
		}
	}
	require.Greater(t, offered, 2*hookio.HostFieldMaxChars, "the fixture must offer well over the host cap")

	res, err := Build(context.Background(), req, goldenDeps(t, full))
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, req.Session)
	require.LessOrEqual(t, int(res.Tokens), int(req.Budget))
	require.Greater(t, hostChars(res.Text), PayloadCeilingChars*3/4,
		"the payload should be filled toward the ceiling, not stopped far short of it")

	// Tier 1 is whole and present: the pinned invariants, the verbatim original, the retrieval line.
	for _, inv := range full.Invariants {
		require.Contains(t, res.Text, "- ["+inv.ID+"] "+inv.Text)
	}
	require.Contains(t, res.Text, quoteLines(full.UserIntent.Original))
	require.Contains(t, res.Text, AffordanceNotice())
	// Current authority first: the newest restatement of the intent survives.
	require.Contains(t, res.Text, full.UserIntent.Evolution[len(full.UserIntent.Evolution)-1])
	// The small, high-priority items fit whole ahead of the bulky restored instructions.
	require.Contains(t, res.Text, decisionLines(full.Decisions[0]))
	require.Contains(t, res.Text, "goal: "+full.CurrentWork.Goal)
	for _, f := range full.Pointers.Files {
		require.Contains(t, res.Text, "- "+f.Path+" ")
	}

	// §8.6 order, 0-based contiguous ranks.
	for i, it := range res.Items {
		require.Equal(t, i, it.Rank)
		if i > 0 {
			require.Greater(t, it.Kind, res.Items[i-1].Kind, "items must be emitted in §8.6 order")
		}
	}

	// Whole records: every restored rule is in the payload whole, or absent and named with a pointer.
	var omittedRules int
	for _, rule := range append(goldenPathRules(t), goldenNestedRules(t)...) {
		if strings.Contains(res.Text, "### "+rule.Path) {
			require.Contains(t, res.Text, strings.TrimRight(rule.Body, "\n"), "a restored rule is whole or absent")
			continue
		}
		omittedRules++
		e, ok := dropFor(res.Dropped, rule.Path)
		require.True(t, ok, "omitted rule %s is not in the drop report", rule.Path)
		require.Contains(t, e.Detail, "Read "+rule.Path, "omitted rule %s carries no restore pointer", rule.Path)
	}
	require.Positive(t, omittedRules, "fixture sanity: the ceiling must force at least one rule out")

	requireSectionSevenAccountsForEveryDrop(t, res)
}

// dropFor finds the drop entry for id.
func dropFor(drops []checkpoint.DropEntry, id string) (checkpoint.DropEntry, bool) {
	for _, e := range drops {
		if e.ID == id {
			return e, true
		}
	}
	return checkpoint.DropEntry{}, false
}

// moreLineRE matches item 7's counted tail.
var moreLineRE = regexp.MustCompile(`^- … and (\d+) more; call dropped\(\)$`)

// requireSectionSevenAccountsForEveryDrop asserts the rendered overflow report covers the whole
// drop set: every report line is shown, or the section ends in a counted tail whose count is
// exactly the number of lines not shown.
func requireSectionSevenAccountsForEveryDrop(t require.TestingT, res Result) {
	want := len(buildDropReport(res.Dropped).units)
	if want == 0 {
		require.NotContains(t, res.Text, sectionHeading(ItemDropReport))
		return
	}
	body := sectionBody(res.Text, sectionHeading(ItemDropReport))
	require.NotEmpty(t, body, "%d drop entries but no section 7 in the payload", len(res.Dropped))
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	shown := len(lines)
	if m := moreLineRE.FindStringSubmatch(lines[len(lines)-1]); m != nil {
		n, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		shown--
		require.Equal(t, want, shown+n, "shown lines plus the counted tail must cover the whole report")
		return
	}
	require.Equal(t, want, shown, "an untruncated section 7 shows every report line")
}

// sectionBody returns the unit lines of the section headed by heading, or "" when it is absent.
func sectionBody(text, heading string) string {
	i := strings.Index(text, "\n"+heading+"\n")
	if i < 0 {
		return ""
	}
	rest := text[i+len(heading)+2:]
	if j := strings.Index(rest, "\n\n"); j >= 0 {
		return rest[:j+1]
	}
	if j := strings.Index(rest, checkpoint.InjectionCloseTag); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestBuild_HostCeiling_OversizedOriginalIsNamedWithItsPointer: a verbatim original prompt too
// large for the ceiling is never cut to fit. It is named as an explicit overflow carrying the
// expand call that retrieves it from L0, and everything that does fit still arrives inline.
func TestBuild_HostCeiling_OversizedOriginalIsNamedWithItsPointer(t *testing.T) {
	cp := ckFull(t)
	// The checkpoint copy is the source here (no store), so the L0 byte cap does not shorten it.
	cp.UserIntent.Original = strings.Repeat("the webhook double-charges when the retry fires twice; ", 400)
	d := fullDeps(t, cp)
	d.Store = nil

	res, err := Build(context.Background(), requestFor(t, cp, 0), d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	require.True(t, res.Degraded, "an essential record that cannot fit degrades the rehydration")
	require.True(t, Overflowed(res.Dropped))
	require.NotContains(t, res.Text, "retry fires twice", "the oversized original is whole or absent")

	e, ok := dropFor(res.Dropped, "tier1")
	require.True(t, ok, "the oversized original must be a named overflow: %v", res.Dropped)
	require.Equal(t, ItemUserIntent.String(), e.Kind)
	require.Contains(t, e.Detail, "Read .qompack/checkpoints/0001.json (user_intent.original)",
		"with L0 unavailable the checkpoint copy is the record, and the pointer says so")
	require.Contains(t, sectionBody(res.Text, sectionHeading(ItemDropReport)), "user_intent tier1",
		"an explicit overflow sorts first in section 7, where the counted tail cannot swallow it")
	for _, inv := range cp.Invariants {
		require.Contains(t, res.Text, inv.Text, "the rest of tier 1 still arrives inline")
	}
	require.Contains(t, res.Text, AffordanceNotice())
	requireSectionSevenAccountsForEveryDrop(t, res)
}

// TestBuild_HostCeiling_L0OriginalPointsAtExpand: when L0 answered, the overflow pointer is the
// expand call on the verbatim prompt's own tool_use id.
func TestBuild_HostCeiling_L0OriginalPointsAtExpand(t *testing.T) {
	cp := ckFull(t)
	// Many short lines: well under L0's byte cap, but the "> " quoting doubles it past the ceiling.
	cp.UserIntent.Original = strings.TrimSuffix(strings.Repeat("a\n", 5000), "\n")
	d := fullDeps(t, cp)

	res, err := Build(context.Background(), requestFor(t, cp, 0), d)
	require.NoError(t, err)
	requireInsideTheHostCeiling(t, res, cp.Session)
	e, ok := dropFor(res.Dropped, "tier1")
	require.True(t, ok, "%v", res.Dropped)
	require.Contains(t, e.Detail, "expand(tool_use_id="+string(firstPromptID(cp.Session))+")")
}

// ── the property: no input exceeds the ceiling ──

// PropBuild_NeverExceedsTheHostCeiling is D5's universal claim: whatever the checkpoint, the
// ledger, the rules and the skills hold — multibyte and astral text, one enormous record, thousands
// of small ones, a pathological session id or sequence in the wrapper — the field the host receives
// is inside the ceiling, no record is cut mid-record, and every record left out is reported.
func PropBuild_NeverExceedsTheHostCeiling(t *rapid.T) {
	cp, deps := drawRehydrationInput(t)
	budget := core.Tokens(rapid.SampledFrom([]int{0, 0, 1, 60, 400, 1500, 4000, 12000, 50000}).Draw(t, "budget"))
	if rapid.Bool().Draw(t, "bareEstimator") {
		deps.Tokens = nil
	}
	log := &spyLogger{}
	deps.Log = log

	var r Request
	r.Session = cp.Session
	r.Source = sourceCompact
	r.ProjectRoot = "/repo"
	r.Budget = budget
	r.Checkpoint = cp
	r.Ref.Seq = cp.Seq
	r.Ref.Path = "/repo/.qompack/checkpoints/0001.json"
	r.Cfg = testCfg()

	res, err := Build(context.Background(), r, deps)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	requireInsideTheHostCeiling(t, res, cp.Session)
	if res.Tokens > clampBudget(budget, r.Cfg.Runtime.Rehydrate) {
		t.Fatalf("tokens %d over the budget", res.Tokens)
	}
	if res.Text == "" {
		return
	}

	// Whole records or none: an invariant, a decision or a restored rule whose handle is in the
	// payload is there in full.
	for _, inv := range cp.Invariants {
		if strings.Contains(res.Text, "- ["+inv.ID+"] ") && !strings.Contains(res.Text, "- ["+inv.ID+"] "+inv.Text) {
			t.Fatalf("invariant %s was cut mid-record", inv.ID)
		}
		if !strings.Contains(res.Text, "- ["+inv.ID+"] ") {
			requireNamedOverflow(t, res.Dropped, ItemInvariants, inv.ID)
		}
	}
	for _, dec := range cp.Decisions {
		if strings.Contains(res.Text, "- ["+string(dec.ID)+"] ") && !strings.Contains(res.Text, decisionLines(dec)) {
			t.Fatalf("decision %s was cut mid-record", dec.ID)
		}
	}
	if fs, ok := deps.Rules.(*fakeScanner); ok {
		for _, rule := range append(append([]rules.Rule(nil), fs.pathScoped...), fs.nested...) {
			if strings.Contains(res.Text, "### "+rule.Path+"\n") || strings.Contains(res.Text, "### "+rule.Path+" — ") {
				if !strings.Contains(res.Text, strings.TrimRight(rule.Body, "\n")) {
					t.Fatalf("rule %s was cut mid-record", rule.Path)
				}
			}
		}
	}
	requireOriginalWholeOrNamed(t, res, cp.UserIntent.Original, deps.Store != nil)

	// When the token budget cannot bind — the default budget is several times what the ceiling
	// holds — every omission is accounted for in the rendered section 7, and the hard-cap eviction
	// loop never fires: the character accounting is exact, so only a token estimator can reach it.
	if budget == 0 || budget >= 12000 {
		requireSectionSevenAccountsForEveryDrop(t, res)
		for _, m := range log.msgs {
			if strings.Contains(m, "re-truncating") {
				t.Fatalf("the hard-cap eviction loop fired on the character ceiling: %v", log.msgs)
			}
		}
	}
}

// requireOriginalWholeOrNamed is the whole-record claim for item 2's verbatim original, the one
// tier-1 record the property above did not check — which is how an 8 KiB prefix of a long first
// prompt reached the model as "verbatim" (F-UAT04-1). The original is either the first thing in
// section 2, whole, or named as an explicit overflow; never a prefix of itself.
//
// fromL0 says whether the build read it from the (agreeing) L0 capture, whose text item 2
// normalizes as readL0First does; otherwise the checkpoint copy is the record.
func requireOriginalWholeOrNamed(t *rapid.T, res Result, original string, fromL0 bool) {
	want := strings.TrimSpace(original)
	if fromL0 {
		if int64(len(original)) > intentReadLimit {
			requireOriginalNamed(t, res.Dropped) // never read whole, so it can only be named
			return
		}
		want = strings.TrimSpace(checkpoint.StripInjections(string(trimToRuneBoundary([]byte(original)))))
	}
	if want == "" {
		return
	}
	body := sectionBody(res.Text, sectionHeading(ItemUserIntent))
	if strings.HasPrefix(body, "> ") {
		if !strings.HasPrefix(body, quoteLines(want)) {
			t.Fatalf("the verbatim original was cut mid-record: section 2 begins %q", firstLineOf(body))
		}
		return
	}
	requireOriginalNamed(t, res.Dropped)
}

// requireOriginalNamed asserts the verbatim original is named in the drop report as an explicit
// overflow: the tier-1 entry, or the hard cap's eviction of item 2.
func requireOriginalNamed(t *rapid.T, drops []checkpoint.DropEntry) {
	for _, e := range drops {
		if e.Kind == ItemUserIntent.String() && (e.ID == "tier1" || e.ID == dropIDEvicted) {
			return
		}
	}
	t.Fatalf("the verbatim original is neither in the payload nor named as an overflow: %v", drops)
}

// firstLineOf is s up to its first newline.
func firstLineOf(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// requireNamedOverflow asserts a tier-1 record absent from the payload is named as an explicit
// overflow that identifies it.
func requireNamedOverflow(t *rapid.T, drops []checkpoint.DropEntry, k ItemKind, id string) {
	for _, e := range drops {
		if e.Kind == k.String() && e.ID == "tier1" && strings.Contains(e.Detail, id) {
			return
		}
	}
	t.Fatalf("%s %s is neither in the payload nor named as an overflow: %v", k, id, drops)
}

func TestBuild_NeverExceedsTheHostCeiling(t *testing.T) {
	restore := skillBodyTokens
	skillBodyTokens = nil
	t.Cleanup(func() { skillBodyTokens = restore })
	rapid.Check(t, PropBuild_NeverExceedsTheHostCeiling)
}

// drawText draws one free-text field in one of the shapes the ceiling has to survive.
func drawText(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 5).Draw(t, label+"_shape") {
	case 0: // ordinary prose
		return rapid.StringMatching(`[A-Za-z][A-Za-z ,.;:()-]{0,80}`).Draw(t, label)
	case 1: // multibyte, astral and combining runes, including line and paragraph separators
		return rapid.StringOfN(rapid.SampledFrom([]rune{
			'a', 'é', '中', '文', 'ß', '\u0301', '\u2028', '\u2029', '\U0001F600', '\U0001D11E', ' ', '\n',
		}), 1, 400, -1).Draw(t, label)
	case 2: // one enormous record
		chunk := rapid.SampledFrom([]string{"x", "é", "中", "\U0001F600", "pool timeout ", "\n"}).Draw(t, label+"_chunk")
		return strings.Repeat(chunk, rapid.IntRange(2000, 30000).Draw(t, label+"_n"))
	case 3: // many short lines
		return strings.TrimSuffix(strings.Repeat("- a\n", rapid.IntRange(1, 3000).Draw(t, label+"_lines")), "\n")
	case 4: // fence-bearing, which items 4-6 reject and items 1-2 keep verbatim
		return "see:\n```\nstack trace\n```"
	default: // arbitrary bytes, invalid UTF-8 included
		return string(rapid.SliceOfN(rapid.Byte(), 0, 300).Draw(t, label))
	}
}

// drawRehydrationInput draws a checkpoint and the collaborators Build reads, sized and shaped to
// stress the ceiling from every direction.
func drawRehydrationInput(t *rapid.T) (checkpoint.Checkpoint, Deps) {
	var cp checkpoint.Checkpoint
	cp.Version = checkpoint.SchemaVersion
	switch rapid.IntRange(0, 2).Draw(t, "session_shape") {
	case 0:
		cp.Session = "sess_prop"
	case 1: // a pathological wrapper: an astral session id, only 8 runes of which reach the header
		cp.Session = core.SessionID(strings.Repeat("\U0001F600", rapid.IntRange(1, 5000).Draw(t, "sess_len")))
	default:
		cp.Session = core.SessionID(drawText(t, "session"))
	}
	cp.Seq = core.CheckpointSeq(rapid.SampledFrom([]int{0, 1, 7, 9999, 1 << 40}).Draw(t, "seq"))

	for i := range rapid.IntRange(0, 40).Draw(t, "invariants") {
		cp.Invariants = append(cp.Invariants, checkpoint.Invariant{
			ID: fmt.Sprintf("inv_%012d", i), Text: drawText(t, "inv"), Source: "user",
		})
	}
	cp.UserIntent.Original = drawText(t, "original")
	for range rapid.IntRange(0, 60).Draw(t, "evolution") {
		cp.UserIntent.Evolution = append(cp.UserIntent.Evolution, drawText(t, "evo"))
	}
	for i := range rapid.IntRange(0, 200).Draw(t, "decisions") {
		cp.Decisions = append(cp.Decisions, checkpoint.Decision{
			ID: core.DecisionID(fmt.Sprintf("dec_%012d", i)), What: drawText(t, "what"), Why: drawText(t, "why"),
			Turn: core.TurnIndex(i),
		})
	}
	cp.CurrentWork = checkpoint.CurrentWork{Goal: drawText(t, "goal"), NextStep: drawText(t, "next")}
	for i := range rapid.IntRange(0, 200).Draw(t, "files") {
		cp.Pointers.Files = append(cp.Pointers.Files, checkpoint.FilePointer{
			Path: fmt.Sprintf("src/f%04d.go", i), Why: drawText(t, "fwhy"),
		})
	}
	for i := range rapid.IntRange(0, 100).Draw(t, "tools") {
		cp.Pointers.Tools = append(cp.Pointers.Tools, checkpoint.ToolPointer{
			ToolUseID: core.ToolUseID(fmt.Sprintf("toolu_%020d", i)), Summary: drawText(t, "tsum"),
		})
	}
	var elims []negknow.Record
	for i := range rapid.IntRange(0, 50).Draw(t, "eliminations") {
		elims = append(elims, negknow.Record{
			ID: fmt.Sprintf("elim_%012d", i), Session: cp.Session, Target: drawText(t, "target"),
			Approach: drawText(t, "approach"), Reason: drawText(t, "reason"),
			Scope: negknow.ScopeProject, Status: negknow.StatusActive,
		})
	}
	scanner := &fakeScanner{}
	for i := range rapid.IntRange(0, 20).Draw(t, "rules") {
		scanner.pathScoped = append(scanner.pathScoped, rules.Rule{
			Path: fmt.Sprintf(".claude/rules/r%03d.md", i), Globs: []string{"src/**"}, Body: drawText(t, "rule"),
		})
	}
	for i := range rapid.IntRange(0, 5).Draw(t, "nested") {
		scanner.nested = append(scanner.nested, rules.Rule{
			Path: fmt.Sprintf("src/d%02d/CLAUDE.md", i), Body: drawText(t, "nested_body"), Nested: true,
		})
	}
	var skillSet []skills.Entry
	for i := range rapid.IntRange(0, 60).Draw(t, "skills") {
		skillSet = append(skillSet, skills.Entry{
			Name: fmt.Sprintf("skill-%02d", i), Description: drawText(t, "skill_desc"),
			Source: fmt.Sprintf(".claude/skills/skill-%02d/SKILL.md", i),
		})
	}

	d := Deps{
		Ledger: newFakeLedger().withActive(negknow.ScopeProject, elims...),
		Graph:  newFakeGraph(nil),
		Rules:  scanner,
		Skills: &fakeIndexer{all: skillSet, kept: skillSet},
		Tokens: fakeEstimator{},
	}
	if rapid.Bool().Draw(t, "l0") {
		d.Store = newFakeStore().withPrompt(firstPromptID(cp.Session), cp.Session, 0, cp.UserIntent.Original)
	}
	return cp, d
}

// FuzzBuild_HostCeiling is the same claim over arbitrary bytes in the fields a user or a host
// controls: the original prompt, a pinned invariant, a restored rule's body and the session id.
func FuzzBuild_HostCeiling(f *testing.F) {
	f.Add("fix the retries", "never bypass the pool", "rule body", "sess_a")
	f.Add(strings.Repeat("中", 9000), strings.Repeat("\U0001F600", 6000), strings.Repeat("x\n", 7000), "e2e-1")
	f.Add(strings.Repeat("a\n", 5000), "", strings.Repeat("é", 20000), strings.Repeat("\U0001F600", 300))
	f.Add(string([]byte{0xff, 0xfe}), "```\ncode\n```", "", "")
	f.Fuzz(func(t *testing.T, original, invariant, ruleBody, session string) {
		cp := ckMinimal()
		cp.Session = core.SessionID(session)
		cp.UserIntent.Original = original
		cp.Invariants = append(cp.Invariants, checkpoint.Invariant{ID: "inv_fuzz", Text: invariant})
		cp.Pointers.Files = []checkpoint.FilePointer{{Path: "src/a.go", Why: invariant}}
		d := fullDeps(t, cp)
		d.Rules = &fakeScanner{pathScoped: []rules.Rule{{Path: ".claude/rules/a.md", Globs: []string{"src/**"}, Body: ruleBody}}}
		res, err := Build(context.Background(), requestFor(t, cp, 0), d)
		require.NoError(t, err)
		requireInsideTheHostCeiling(t, res, cp.Session)
		if strings.Contains(res.Text, "- [inv_fuzz] ") {
			require.Contains(t, res.Text, "- [inv_fuzz] "+invariant, "an invariant is whole or absent")
		}
	})
}

// TestEvolutionCeiling_IsThePayloadCeiling: the checkpointer keeps no more restatement text than one
// rehydration can carry (checkpoint.EvolutionCeilingChars), and that bound is this package's payload
// ceiling. It is spelled there as a derived constant because checkpoint may not import rehydrate;
// this row is what keeps the two from drifting apart.
func TestEvolutionCeiling_IsThePayloadCeiling(t *testing.T) {
	require.Equal(t, PayloadCeilingChars, checkpoint.EvolutionCeilingChars)
}
