"""mkrerun6.py <live-rerun-c5.js> <rerun-parts-c6.js> <out.js> <candidate-sha> <bundle-dir>

Builds the candidate 6 live re-run from the candidate 5 one (D52, D53(f)): swaps RERUN and PARTS for
the c6 parts, which add an install part (UAT-01/C4.1 through a release-named marketplace entry, the
C4.8/UAT-12 upgrade leg from the candidate 5 bundle, a C1.7 restore smoke); pins the candidate and
bundle; moves the branch to closeout/live6 and the evidence to rerun-c6; allows the release entry's
uninstall in the guard rule; and widens the audit to the rows the c6 lane runs.
"""
import sys

src, parts_p, out, cand, bundle = sys.argv[1:6]
s = open(src, encoding="utf-8").read()
parts = open(parts_p, encoding="utf-8").read().strip() + "\n"
a = s.index("const RERUN = `")
b = s.index("\n]\n", s.index("const PARTS = [")) + 3
s = s[:a] + parts + s[b:]


def swap(old, new, count=1):
    global s
    assert s.count(old) >= 1, old
    s = s.replace(old, new) if count == 0 else s.replace(old, new, count)


swap("name: 'v6-closeout-live-rerun-c5'", "name: 'v6-closeout-live-rerun-c6'")
swap("const CAND = '0d06ab1233efb0fd5be8c99dc453e1aa2ee071af'", f"const CAND = '{cand}'")
swap("qompack-bundles/c5/qompack-plugin-0.3.0-windows-amd64'\nif", bundle.split("Projects/", 1)[1] + "'\nif")
swap("closeout/live5", "closeout/live6", 0)
swap(r"Restoring means ONLY \`claude plugin uninstall qompack@qompack-live -s local\`,",
     r"Restoring means ONLY \`claude plugin uninstall qompack@qompack-live -s local\` (or, for the install part's "
     r"release-named entry, \`claude plugin uninstall qompack-windows-amd64@qompack-live -s local\`),")
swap("THIS IS A RE-RUN of the rows candidate 4 failed or left open (decision D52); evidence is under "
     "${LIVE}/rerun-c5/. Coverage: for each of C4.3, C4.4, C4.5, C4.6, C4.9",
     "THIS IS A RE-RUN of the rows candidate 4 failed or left open plus D53(f)'s install, upgrade and restore "
     "rows (decisions D52, D53); evidence is under ${LIVE}/rerun-c6/. Coverage: for each of C4.1, C4.3, C4.4, "
     "C4.5, C4.6, C4.8, C4.9, C1.7 and D53(i) (host-reported hook failures and timeouts = 0 apart from "
     "documented host behaviour)")
swap("For every re-run UAT Result block (UAT-03, 04, 05, 06, 09, 10, 12)",
     "For every re-run UAT Result block (UAT-01, 03, 04, 05, 06, 09, 10, 12), and the release entry's observed "
     "slash-command and MCP-tool namespace against docs/install.md and docs/commands.md")
assert "rerun-c5" not in s and "live5" not in s, "stale c5 reference"
assert "phase: 'Resilience'" not in s, "Resilience is not a meta phase"
open(out, "w", encoding="utf-8", newline="\n").write(s)
print(out)
