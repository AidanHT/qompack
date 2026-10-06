package hostperm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRuleSet_ReadRulePatternsListsEveryReadRule pins what the rehydration block's free-text screen
// is handed (coordinator decision D61, ADR 0011 §23.5): the specifier of every Read deny and ask
// rule as written, each once, deny first; "" for a tool-level rule; nothing for a carve-out, a rule
// for another tool or a parameter rule; and none of the aliases the rule set adds on Windows.
func TestRuleSet_ReadRulePatternsListsEveryReadRule(t *testing.T) {
	for _, e := range []pureEnv{posixEnv, winEnv} {
		t.Run(e.goos, func(t *testing.T) {
			rs := e.build(t,
				deny("Read(./private/deny.txt)", "Read( ./.env )", "Edit(./src/**)", "Read(file_path:x)",
					"Read(./secrets/**)", "Read(!./secrets/public.txt)", "Read(./.env)"),
				ask(`Read(C:\Users\U\notes\secret.txt)`, "Read(~/.ssh/**)"),
				layer{json: `{"permissions":{"deny":["Read(**/*.pem)","Read"]}}`},
			)
			require.Equal(t, []string{
				"./private/deny.txt", "./.env", "./secrets/**", "", "**/*.pem",
				`C:\Users\U\notes\secret.txt`, "~/.ssh/**",
			}, rs.ReadRulePatterns())
		})
	}
	var empty *RuleSet
	require.Nil(t, empty.ReadRulePatterns(), "no rules in force: nothing to screen by")
}
