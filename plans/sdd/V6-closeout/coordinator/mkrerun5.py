"""mkrerun5.py <live-rerun-c4.js> <rerun-parts-c5.js> <out.js> <candidate-sha> <bundle-dir>

Builds the candidate 5 live re-run from the candidate 4 one: swaps RERUN and PARTS for the c5 parts, pins the
candidate and bundle, moves the branch to closeout/live5 and the evidence to rerun-c5, and narrows the audit.
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


swap("name: 'v6-closeout-live-rerun-c4'", "name: 'v6-closeout-live-rerun-c5'")
swap("const CAND = '9f6a2fadf086eba8080af589a35dd9554ae6cab4'", f"const CAND = '{cand}'")
swap("qompack-bundles/c4/qompack-plugin-0.3.0-windows-amd64'", bundle.split("Projects/", 1)[1] + "'")
swap("closeout/live4", "closeout/live5", 0)
swap("THIS IS A RE-RUN of the rows candidate 3 failed (decision D47); evidence is under ${LIVE}/rerun-c4/. "
     "Coverage: for each of C4.2, C4.3, C4.4, C4.5, C4.8, C4.9, C1.7",
     "THIS IS A RE-RUN of the rows candidate 4 failed or left open (decision D52); evidence is under "
     "${LIVE}/rerun-c5/. Coverage: for each of C4.3, C4.4, C4.5, C4.6, C4.9")
swap("For every re-run UAT Result block (UAT-01..09, 11, 12)",
     "For every re-run UAT Result block (UAT-03, 04, 05, 06, 09, 10, 12)")
assert "rerun-c4/C1.7" not in s
open(out, "w", encoding="utf-8", newline="\n").write(s)
print(out)
