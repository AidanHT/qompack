"""Real-home guard for the Phase 4 live lane (live-uat.js).

usage:
  homeguard.py snap  <snap.json> [--home DIR]   record the fingerprint; refuses to overwrite
  homeguard.py check <snap.json> [--home DIR]   read-only; exit 1 on any difference
  homeguard.py clean <snap.json> [--home DIR]   rmdir run-created, EMPTY plugins/data/qompack* dirs

The fingerprint is the SHA-256 of ~/.claude/settings.json, plugins/installed_plugins.json and
plugins/known_marketplaces.json (byte-identical, or "absent"), plus the entry NAMES under
plugins/cache (to marketplace/plugin/version depth), plugins/marketplaces and plugins/data. It opens
no other file: credentials (.credentials.json or anything else) are never read, and only directory
listings are taken. CLAUDE_CONFIG_DIR is deliberately ignored: the real profile is <home>/.claude.

~/.qompack is listed by name only (top level and bin/). Entries a run ADDS there (the D10 staged
daemon copies, logs/) are reported as INFO, not failures; an entry that existed at snap time and is
gone is a failure, because the run must never delete the user's global Qompack state.

check prints hashes (12 hex chars) and counts, and names entries only when they differ, so its
output can be committed as evidence. The snap file itself lists the operator's installed plugins:
keep it in scratch, never commit it.
"""
import hashlib
import json
import os
import sys

FILES = ("settings.json", "plugins/installed_plugins.json", "plugins/known_marketplaces.json")
TREES = (("plugins/cache", 3), ("plugins/marketplaces", 1), ("plugins/data", 1))


def _sha(path):
    try:
        with open(path, "rb") as f:
            return hashlib.sha256(f.read()).hexdigest()
    except FileNotFoundError:
        return "absent"


def _names(root, depth):
    out = []
    if not os.path.isdir(root):
        return ["<absent>"]
    stack = [("", 1)]
    while stack:
        rel, d = stack.pop()
        try:
            entries = sorted(os.scandir(os.path.join(root, rel)), key=lambda e: e.name)
        except OSError as e:
            out.append(f"{rel}<unlistable {e.errno}>")
            continue
        for e in entries:
            name = f"{rel}{e.name}"
            if e.is_dir(follow_symlinks=False):
                out.append(name + "/")
                if d < depth:
                    stack.append((name + "/", d + 1))
            else:
                out.append(name)
    return sorted(out)


def fingerprint(home):
    dot = os.path.join(home, ".claude")
    qh = os.path.join(home, ".qompack")
    return {
        "files": {f: _sha(os.path.join(dot, *f.split("/"))) for f in FILES},
        "trees": {t: _names(os.path.join(dot, *t.split("/")), d) for t, d in TREES},
        "qompack": _names(qh, 1) + ["bin/" + n for n in _names(os.path.join(qh, "bin"), 1)
                                     if os.path.isdir(os.path.join(qh, "bin"))],
    }


def snap(path, home):
    if os.path.exists(path):
        print("REFUSED: snap file exists; a guard is taken once, before the first step:", path)
        return 2
    with open(path, "w", encoding="utf-8") as f:
        json.dump({"home": home, "fp": fingerprint(home)}, f, indent=1)
    print("snap ok")
    return 0


def check(path, home):
    with open(path, encoding="utf-8") as f:
        before = json.load(f)["fp"]
    now = fingerprint(home)
    bad = False
    for name in FILES:
        a, b = before["files"][name], now["files"][name]
        if a == b:
            print(f"same  {name} {a[:12]}")
        else:
            bad = True
            print(f"DIFF  {name} {a[:12]} -> {b[:12]}")
    for tree, _ in TREES:
        a, b = set(before["trees"][tree]), set(now["trees"][tree])
        if a == b:
            print(f"same  {tree}/ ({len(a)} entries)")
        else:
            bad = True
            print(f"DIFF  {tree}/ lost {sorted(a - b)} gained {sorted(b - a)}")
    a, b = set(before["qompack"]), set(now["qompack"])
    if a - b:
        bad = True
        print(f"DIFF  .qompack/ lost pre-existing {sorted(a - b)}")
    if b - a:
        print(f"INFO  .qompack/ gained {sorted(b - a)} (run-created; record, remove only if yours)")
    if a == b:
        print(f"same  .qompack/ ({len(a)} entries)")
    print("REAL HOME CHANGED" if bad else "real home fingerprint unchanged")
    return 1 if bad else 0


def clean(path, home):
    with open(path, encoding="utf-8") as f:
        before = set(json.load(f)["fp"]["trees"]["plugins/data"])
    data = os.path.join(home, ".claude", "plugins", "data")
    for entry in _names(data, 1):
        if not entry.endswith("/") or entry in before or not entry.startswith("qompack"):
            continue
        p = os.path.join(data, entry.rstrip("/"))
        try:
            os.rmdir(p)
            print("removed run-created empty", "plugins/data/" + entry)
        except OSError:
            print("LEFT non-empty run-created", "plugins/data/" + entry, "(check will fail)")
    return 0


def main(argv):
    if len(argv) < 2 or argv[0] not in ("snap", "check", "clean"):
        print(__doc__)
        return 2
    home = os.path.expanduser("~")
    if "--home" in argv:
        home = argv[argv.index("--home") + 1]
    return {"snap": snap, "check": check, "clean": clean}[argv[0]](argv[1], home)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
