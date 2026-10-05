"""Build pre-fix overlay files for the w15-ledger and w15-services fixes on today's merged tree.

Each overlay replaces exactly the fixed code with the behaviour it had before the fix, ported to
where the code lives now, and nothing else. Run from the worktree root.
"""
import json
import os
import re
import sys

WT = os.getcwd()
OUT = sys.argv[1]
os.makedirs(OUT, exist_ok=True)


def read(rel):
    with open(os.path.join(WT, rel), encoding="utf-8", newline="") as f:
        return f.read()


def write(name, text):
    p = os.path.join(OUT, name)
    with open(p, "w", encoding="utf-8", newline="") as f:
        f.write(text)
    return p


def replace_once(text, old, new, what):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{what}: expected one match, found {n}")
    return text.replace(old, new)


overlays = {}

# 1a. ca0b7caa, Query half: no filter-miss return before viewerFor.
ledger = read("internal/negknow/ledger.go")
m = re.search(
    r"\n\t// A filter miss answers absent for every caller.*?\n\tif miss \{\n.*?\n\t\}\n(?=\tv := l\.viewerFor\(ctx\))",
    ledger,
    re.S,
)
if not m:
    raise SystemExit("ledger.go: fast path not found")
ledger_pre = ledger[: m.start()] + "\n" + ledger[m.end():]
overlays["negknow-ledger-prefix.go"] = ("internal/negknow/ledger.go", ledger_pre)

# 1b. ca0b7caa, LedgerAncestry half: no memo, every call walks the lineage records.
lineage = read("internal/checkpoint/lineage.go")
m = re.search(r"func LedgerAncestry\(root string\) func\(core\.SessionID\) \[\]negknow\.Inherited \{\n.*?\n\}\n", lineage, re.S)
if not m:
    raise SystemExit("lineage.go: LedgerAncestry not found")
lineage_pre = (
    lineage[: m.start()]
    + "func LedgerAncestry(root string) func(core.SessionID) []negknow.Inherited {\n"
    + "\tl := paths.Of(root)\n"
    + "\treturn func(s core.SessionID) []negknow.Inherited {\n"
    + "\t\tif checkSessionComponent(s) != nil {\n"
    + "\t\t\treturn nil\n"
    + "\t\t}\n"
    + "\t\treturn Ancestry(l, s)\n"
    + "\t}\n"
    + "}\n"
    + lineage[m.end():]
)
# sync is used only by the memo.
lineage_pre = replace_once(lineage_pre, '\t"sync"\n', "", "lineage.go sync import")
overlays["checkpoint-lineage-prefix.go"] = ("internal/checkpoint/lineage.go", lineage_pre)

# 2. 2348f796: the carry reads the sealed decisions by shape (the pre-fix body), behind the
# signature the fork-point carry calls today.
decisions = read("internal/checkpoint/decisions.go")
m = re.search(
    r"func \(d \*Draft\) carryDecisionsLocked\(ctx context\.Context, from \*Checkpoint, invs \[\]pins\.Invariant\) \{\n.*?\n\}\n",
    decisions,
    re.S,
)
if not m:
    raise SystemExit("decisions.go: carryDecisionsLocked not found")
pre_body = (
    "func (d *Draft) carryDecisionsLocked(ctx context.Context, from *Checkpoint, invs []pins.Invariant) {\n"
    "\t_ = ctx\n"
    "\tif from == nil {\n"
    "\t\treturn\n"
    "\t}\n"
    "\tprev := from.Decisions\n"
    "\tif len(prev) == 0 {\n"
    "\t\treturn\n"
    "\t}\n"
    "\tholds := make(map[core.DecisionID]bool, len(d.cp.Eliminated)+len(invs))\n"
    "\tfor _, r := range d.cp.Eliminated {\n"
    "\t\tif dec, ok := eliminationDecision(r, 0); ok {\n"
    "\t\t\tholds[dec.ID] = true\n"
    "\t\t}\n"
    "\t}\n"
    "\tfor _, inv := range invs {\n"
    "\t\tif dec, ok := pinDecision(inv, 0); ok {\n"
    "\t\t\tholds[dec.ID] = true\n"
    "\t\t}\n"
    "\t}\n"
    "\tcands := make([]decisionCandidate, 0, len(prev))\n"
    "\tfor _, dec := range prev {\n"
    "\t\texplains := len(dec.AlternativesRejected) == 0 && dec.Evidence != (core.Hash{})\n"
    "\t\tif !explains && !holds[dec.ID] {\n"
    "\t\t\tcontinue\n"
    "\t\t}\n"
    "\t\tcands = append(cands, decisionCandidate{d: dec})\n"
    "\t}\n"
    "\td.mergeDecisionsLocked(cands)\n"
    "}\n"
)
decisions_pre = decisions[: m.start()] + pre_body + decisions[m.end():]
overlays["checkpoint-decisions-prefix.go"] = ("internal/checkpoint/decisions.go", decisions_pre)

# 3. 503375d2: no fork-point carry and no inherited-decision ranking.
writer = read("internal/checkpoint/writer.go")
writer_pre = replace_once(
    writer,
    "\tif fp, at, ok := w.forkPoint(ctx, d.session, d.inherit); ok {\n"
    "\t\td.inheritedDec = inheritedDecisions(src.Graph, fp, at)\n"
    "\t\tif carryFrom == nil {\n"
    "\t\t\tcarryFrom = &fp\n"
    "\t\t}\n"
    "\t}\n",
    "",
    "writer.go seedTierOne fork point",
)
writer_pre = replace_once(
    writer_pre,
    "\t\tvar inheritedDec map[core.DecisionID]core.UnixMilli\n"
    "\t\tif fp, at, ok := w.forkPoint(ctx, s, inherit); ok {\n"
    "\t\t\tinheritedDec = inheritedDecisions(src.Graph, fp, at)\n"
    "\t\t}\n",
    "\t\tvar inheritedDec map[core.DecisionID]core.UnixMilli\n",
    "writer.go Begin resume fork point",
)
overlays["checkpoint-writer-prefix.go"] = ("internal/checkpoint/writer.go", writer_pre)

# 3b. 503375d2's ranking half alone: the fork point is still carried, but inheritedDec stays nil.
writer_norank = replace_once(
    writer,
    "\t\t\tinheritedDec = inheritedDecisions(src.Graph, fp, at)\n",
    "\t\t\t_, _ = fp, at\n",
    "writer.go Begin resume ranking",
)
writer_norank = replace_once(
    writer_norank,
    "\t\td.inheritedDec = inheritedDecisions(src.Graph, fp, at)\n",
    "\t\t_ = at\n",
    "writer.go seedTierOne ranking",
)
overlays["checkpoint-writer-norank.go"] = ("internal/checkpoint/writer.go", writer_norank)

# 4. a1ccd1ff: a pass with a snapshot notes a removed file as unreadable (the hook call stays, as in
# the branch's own red run).
audit = read("internal/store/publication_audit.go")
audit_pre, n = re.subn(
    r"if !bud\.vanished\(err, a\) \{\n(\t+)(a\.note\(\"[^\"]+\"\))\n\t+\}",
    lambda mm: mm.group(2),
    audit,
)
if n != 5:
    raise SystemExit(f"publication_audit.go: expected five vanished sites, found {n}")
overlays["store-publication-audit-prefix.go"] = ("internal/store/publication_audit.go", audit_pre)

# 5. 765a9bcc: the matcher accepts any publication counter above zero, as the row did before.
fsck = read("test/fault/v6_fsck_test.go")
fsck_pre = replace_once(
    fsck,
    "\tcounterNamed = env.Data.Snapshot.Counters[counterUnpublishedCaptures] > 0\n",
    "\tfor name, v := range env.Data.Snapshot.Counters {\n"
    "\t\tif v > 0 && strings.Contains(name, \"publication\") {\n"
    "\t\t\tcounterNamed = true\n"
    "\t\t\tbreak\n"
    "\t\t}\n"
    "\t}\n",
    "v6_fsck_test.go matcher",
)
overlays["fault-v6-fsck-prefix.go"] = ("test/fault/v6_fsck_test.go", fsck_pre)

for name, (rel, text) in overlays.items():
    p = write(name, text)
    single = {"Replace": {os.path.normpath(os.path.join(WT, rel)): os.path.normpath(p)}}
    with open(os.path.join(OUT, name[:-3] + ".json"), "w", encoding="utf-8") as f:
        json.dump(single, f, indent=1)
    print(name, "->", rel)

# Both halves of ca0b7caa together.
both = {
    "Replace": {
        os.path.normpath(os.path.join(WT, "internal/negknow/ledger.go")): os.path.normpath(os.path.join(OUT, "negknow-ledger-prefix.go")),
        os.path.normpath(os.path.join(WT, "internal/checkpoint/lineage.go")): os.path.normpath(os.path.join(OUT, "checkpoint-lineage-prefix.go")),
    }
}
with open(os.path.join(OUT, "negknow-both-prefix.json"), "w", encoding="utf-8") as f:
    json.dump(both, f, indent=1)
print("negknow-both-prefix.json")
