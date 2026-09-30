"""mkrerun.py <live-uat.js> <parts.js> <out.js> <candidate-sha> <bundle-dir> <name>

Builds a live re-run script from live-uat.js: keeps COMMON, the schemas and the audit, swaps PARTS for
the re-run parts, pins the candidate and bundle, and narrows the audit to the re-run rows.
"""
import sys

src, parts_p, out, cand, bundle, name = sys.argv[1:7]
s = open(src, encoding="utf-8").read()
parts = open(parts_p, encoding="utf-8").read()
a = s.index("const PARTS = [")
b = s.index("\n]\n", a) + 3
s = s[:a] + parts.strip() + "\n" + s[b:]
s = s.replace("'__CANDIDATE__'", repr(cand), 1).replace("'__BUNDLE__'", repr(bundle), 1)
s = s.replace("name: 'v6-closeout-live-uat'", f"name: '{name}'", 1)
old = "Coverage: for each of C4.1, C4.2, C4.3, C4.4, C4.5, C4.6, C4.7, C4.8, C4.9, C4.10, C4.11, C1.6, C1.7, C5.6"
assert s.count(old) == 1, "audit coverage anchor"
s = s.replace(old, "THIS IS A RE-RUN of the rows candidate 3 failed (decision D47); evidence is under ${LIVE}/rerun-c4/. "
              "Coverage: for each of C4.2, C4.3, C4.4, C4.5, C4.8, C4.9, C1.7", 1)
s = s.replace("For every UAT-01..12 Result block", "For every re-run UAT Result block (UAT-01..09, 11, 12)", 1)
open(out, "w", encoding="utf-8", newline="\n").write(s)
print(out)
