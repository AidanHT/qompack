"""Index Test/Benchmark/Fuzz funcs in the worktree and check every symbol the inventory map names."""
import csv, json, os, re, subprocess, sys, tempfile

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..", ".."))
OUT = os.environ.get("W17_OUT", tempfile.gettempdir())

files = subprocess.check_output(["git", "-C", ROOT, "ls-files", "*_test.go"], text=True).split()
FUNC = re.compile(r"^func ((?:Test|Benchmark|Fuzz|Prop|Example)\w*)\(", re.M)
idx = {}  # name -> list of dict(pkg, file, tags, skip)
for f in files:
    src = open(os.path.join(ROOT, f), encoding="utf-8").read()
    pkg = os.path.dirname(f)
    m = re.search(r"^//go:build (.+)$", src, re.M)
    tags = m.group(1).strip() if m else ""
    base = os.path.basename(f)[:-len("_test.go")]
    for suf in ("_windows", "_linux", "_darwin", "_unix", "_other"):
        if base.endswith(suf):
            tags = (tags + " file" + suf).strip()
    starts = [(mm.start(), mm.group(1)) for mm in FUNC.finditer(src)]
    for i, (s, name) in enumerate(starts):
        e = starts[i + 1][0] if i + 1 < len(starts) else len(src)
        body = src[s:e]
        skips = re.findall(r"t\.Skip(?:f|Now)?\((.{0,120})", body)
        idx.setdefault(name, []).append({"pkg": pkg, "file": f, "tags": tags, "skips": skips})

json.dump(idx, open(os.path.join(OUT, "symidx.json"), "w"), indent=0)

TOK = re.compile(r"([\w/.\-]+(?:,[\w/.\-]+)*):((?:Test|Benchmark|Fuzz|Prop)\w*\*?)")
rows = list(csv.reader(open(os.path.join(ROOT, "plans/sdd/V6-closeout/inventory-map.tsv"), encoding="utf-8"), delimiter="\t"))
report = {}
for r in rows[1:]:
    rid, found = r[0], r[1]
    res = []
    for pkgs, name in TOK.findall(found):
        pk = pkgs.split(",")
        if name.endswith("*"):
            pre = name[:-1]
            hits = [n for n in idx if n.startswith(pre) and any(d["pkg"] in pk for d in idx[n])]
            res.append({"sym": f"{pkgs}:{name}", "exists": bool(hits), "n": len(hits)})
        else:
            hits = [d for d in idx.get(name, []) if d["pkg"] in pk]
            res.append({"sym": f"{pkgs}:{name}", "exists": bool(hits),
                        "tags": sorted({d["tags"] for d in hits}), "skips": sum((d["skips"] for d in hits), [])})
    report[rid] = res
json.dump(report, open(os.path.join(OUT, "rowsyms.json"), "w"), indent=1)
missing = {k: [x["sym"] for x in v if not x["exists"]] for k, v in report.items()}
missing = {k: v for k, v in missing.items() if v}
print("rows with a now-missing symbol:", json.dumps(missing, indent=1))
skipped = {k: [(x["sym"], x.get("tags"), x.get("skips")) for x in v if x.get("skips") or x.get("tags") and any(x["tags"])] for k, v in report.items()}
skipped = {k: v for k, v in skipped.items() if v}
print("rows naming a test with a build constraint or t.Skip:")
for k, v in skipped.items():
    for s in v:
        print(k, s[0], s[1], [z[:80] for z in (s[2] or [])])
