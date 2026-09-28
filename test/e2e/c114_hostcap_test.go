package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/rehydrate"
)

// c114LargeRules is how many oversized path-scoped rules the case writes. Each matches the seeded
// checkpoint's src/auth.ts pointer, so the rule scanner offers every one to item 6a.
const c114LargeRules = 6

// c114RuleLines is how many lines each oversized rule body carries: about 2,600 characters, so the
// six of them alone are half as much again as the host's 10,000-character cap, while one or two of
// them still fit beside everything the checkpoint carries.
const c114RuleLines = 25

// TestE2E_SessionStartCompactFitsTheHostCap is C1.14 (owner decision D5) end to end: a compact
// SessionStart whose restorable material is far over the host's 10,000-character additionalContext
// cap is answered, through the REAL binary, hook client, daemon and rehydrator, with a field the host
// delivers whole — the rehydration plus the daemon's contract probe inside the 9,500-character
// ceiling — and the hook client's over-cap Loud line never fires.
//
// Before D5 the same project produced a payload several times the cap, which the host replaces with a
// file path and a 2,000-character preview it does not ask the model to read
// (plans/sdd/V6-closeout/packaging/evidence/review/f2-live-host-cap-probe). Now the rules that do not
// fit are left out whole and named in section 7, each with the Read pointer that restores it.
func TestE2E_SessionStartCompactFitsTheHostCap(t *testing.T) {
	bin := Build(t)
	p := scProject(t)
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

	large := map[string]string{}
	var offered int
	for i := 0; i < c114LargeRules; i++ {
		body := strings.Repeat(fmt.Sprintf(
			"Rule %02d: keep the refresh-token rotation inside one short transaction; never hold a row lock across I/O.\n", i),
			c114RuleLines)
		offered += hookio.HostChars(body)
		large[fmt.Sprintf(".claude/rules/zz-large-%02d.md", i)] = "---\npaths: [\"src/**\"]\ndescription: oversized rule " + fmt.Sprint(i) + "\n---\n" + body
	}
	require.Greater(t, offered, hookio.HostFieldMaxChars+hookio.HostFieldMaxChars/2,
		"fixture sanity: the restorable rules alone must be well over the host cap")
	p.WithFiles(t, large)

	env := e2eEnv(p)
	scWarmDaemon(t, bin, p, env)
	out := scRunStart(t, bin, env, scStartPayload(t, p.Root, "compact", ""))
	ac := scAdditionalContext(t, out)
	t.Logf("compact additionalContext: %d host characters (ceiling %d, host cap %d); restorable rules offered: %d",
		hookio.HostChars(ac), rehydrate.HostContextCeilingChars, hookio.HostFieldMaxChars, offered)

	// The field the host receives is inside the D5 ceiling, and the hook client has no overrun to
	// report: out is exactly what the hook wrote to the host's stdin, after ConformOutput.
	require.LessOrEqual(t, hookio.HostChars(ac), rehydrate.HostContextCeilingChars,
		"the compact additionalContext is %d host characters, over the D5 ceiling", hookio.HostChars(ac))
	require.Empty(t, hookio.HostCapOverruns(out), "no hook field may reach the host's file-path fallback")
	if loud, err := os.ReadFile(filepath.Join(paths.Of(p.Root).Logs, "LOUD.log")); err == nil {
		require.NotContains(t, string(loud), "host's per-field cap",
			"the hook client's over-cap Loud path must be unreachable for a rehydration")
	}

	// The rehydration arrived: the checkpoint's own tier 1, the report on what was left out, and the
	// retrieval line that makes the report actionable.
	span := scInjectedSpan(t, ac)
	for _, heading := range []string{scHeadingInvariants, scHeadingNoLonger, scHeadingRetrieval} {
		require.Contains(t, span, heading, "§8.6 heading missing from the capped digest:\n%s", span)
	}
	// Every oversized rule is in the payload whole, or named with its restore pointer — here in the
	// rendered report or, past its counted tail, in the persisted state dropped() answers from.
	st := c114ReadState(t, p.Root)
	var named int
	for i := 0; i < c114LargeRules; i++ {
		rel := fmt.Sprintf(".claude/rules/zz-large-%02d.md", i)
		if strings.Contains(span, "### "+rel) {
			require.Contains(t, span, strings.TrimSuffix(strings.Repeat(fmt.Sprintf(
				"Rule %02d: keep the refresh-token rotation inside one short transaction; never hold a row lock across I/O.\n", i),
				c114RuleLines), "\n"), "a restored rule is whole or absent")
			continue
		}
		for _, e := range st.Dropped {
			if e.ID == rel {
				require.Contains(t, e.Detail, "restore: Read "+rel)
				named++
			}
		}
	}
	require.Positive(t, named, "the ceiling must have forced at least one oversized rule out, by name")
	require.Less(t, named, c114LargeRules, "and at least one rule must still have been restored whole")
}

// c114ReadState decodes the rehydration state file the daemon persisted for scSession. It waits
// for the file, which lands after the compact answer, and reads it with delete sharing; see
// scAwaitState for both.
func c114ReadState(t *testing.T, root string) rehydrate.State {
	t.Helper()
	return scAwaitState(t, scStatePath(root))
}
