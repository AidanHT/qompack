package hostperm

import "strings"

// ReadRulePatterns returns the path specifier of every Read deny and ask rule in force, as its
// settings source spells it inside `Read(…)` (`./private/deny.txt`, `./secrets/**`, `**/*.env`),
// each once, deny lists first and in source order, and "" for a tool-level rule (`Read`, or a tool
// glob such as `*`) that refuses every read. A carve-out (`!…`) is left out: it only reopens what an
// earlier rule refused. The aliases a rule set adds (a drive path's POSIX spelling, a long name, a
// link's target) share their rule's specifier and add nothing here. It reads no file.
//
// The rehydration block screens free text by these (rehydrate.HostRules): a summary that holds a
// rule's literal part may name a path the rule refuses. Like Decision.Rule, a specifier spells the
// very path it protects, so it is for the caller's own screening and never for a response.
func (rs *RuleSet) ReadRulePatterns() []string {
	if rs.Empty() {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	add := func(spec string) {
		if !seen[spec] {
			seen[spec] = true
			out = append(out, spec)
		}
	}
	for _, lists := range [][]ruleList{rs.deny, rs.ask} {
		for _, l := range lists {
			if l.toolRule != "" {
				add("")
			}
			for _, p := range l.patterns {
				if !p.neg {
					add(ruleSpecifier(p.raw))
				}
			}
		}
	}
	return out
}

// ruleSpecifier is the path specifier of raw, a path rule's entry as written (`Read(./.env)`):
// what lies between its first `(` and its closing `)`, trimmed, as parseRule reads it.
func ruleSpecifier(raw string) string {
	s := strings.TrimSpace(raw)
	i := strings.IndexByte(s, '(')
	if i < 0 || !strings.HasSuffix(s, ")") {
		return s
	}
	return strings.TrimSpace(s[i+1 : len(s)-1])
}
