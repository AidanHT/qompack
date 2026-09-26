"""Render a workstream report from a workflow journal.

usage: python wfreport.py <journal.jsonl> <ws> <run-id> <title> <out.md>
"""
import json
import os
import sys

journal, ws, run, title, out = sys.argv[1:6]
branch = sys.argv[6] if len(sys.argv) > 6 else f"closeout/{ws}"
labels, results = {}, {}
for line in open(journal, encoding="utf-8"):
    d = json.loads(line)
    if d.get("type") == "started":
        labels[d["agentId"]] = d.get("label", "")
    elif d.get("type") == "result":
        results[d["agentId"]] = d.get("result") or d.get("value")

by = {}
for aid, lab in labels.items():
    if aid in results and (lab == f"impl:{ws}" or lab == f"fix:{ws}" or lab.startswith(f"review:{ws}:") or lab == f"review:{ws}"):
        by.setdefault(lab.split(":")[0], []).append((lab, results[aid]))

L = [f"# {title}", "",
     f"Branch `{branch}`. Workflow `{run}`. Subagents cannot write report files in this harness, "
     "so the coordinator committed this verbatim from their returned results.", ""]


def result_block(name, r):
    if not isinstance(r, dict):
        L.extend([f"## {name}", "", str(r), ""])
        return
    L.extend([f"## {name} — status `{r.get('status')}`, head `{r.get('head')}`", ""])
    if r.get("root_cause"):
        L.extend(["### Root cause", "", r["root_cause"], ""])
    L.extend(["### Summary", "", r.get("summary", ""), ""])
    if r.get("commits"):
        L.append("### Commits\n")
        L.extend(f"- {c}" for c in r["commits"])
        L.append("")
    if r.get("tests"):
        L.append("### Tests\n")
        L.extend(f"- `{t.get('command')}` — {t.get('result')}" for t in r["tests"])
        L.append("")
    for key, head in (("criterion_changes", "Criterion changes"), ("open_issues", "Open issues"),
                      ("needs_owner", "Needs the owner")):
        if r.get(key):
            L.append(f"### {head}\n")
            L.extend(f"- {x}" for x in r[key])
            L.append("")


for lab, r in by.get("impl", []):
    result_block("Implementer", r)
if by.get("review"):
    L.extend(["## Independent review", ""])
    for lab, r in sorted(by["review"]):
        if not isinstance(r, dict):
            continue
        L.extend([f"### {lab}: {r.get('verdict')}", ""])
        for f in r.get("findings", []):
            L.append(f"- **{f['severity']}** `{f['location']}` — {f['issue']}")
            L.append(f"  - Evidence: {f['evidence']}")
            L.append(f"  - Fix: {f['fix']}")
        L.append("")
for lab, r in by.get("fix", []):
    result_block("Fix seat (review resolution)", r)
if not by.get("fix"):
    L.extend(["## Fix seat", "", "Not run: the reviews returned no actionable (non-nit) findings.", ""])

os.makedirs(os.path.dirname(out), exist_ok=True)
with open(out, "w", encoding="utf-8", newline="\n") as fh:
    fh.write("\n".join(L) + "\n")
print(out, sum(len(x) for x in L))
