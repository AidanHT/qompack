"""For each C5.2 benchmark package: the internal import closure (non-test files, any GOOS) and which
files in it changed between candidate 5 (0d06ab12) and candidate 6 (99d0b18)."""
import os, re, subprocess, sys

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "..", ".."))
C5, C6 = "0d06ab1233efb0fd5be8c99dc453e1aa2ee071af", "99d0b18cf7d5991aa733e70ce52a542b80aa7710"
MOD = "github.com/qompack/qompack/"

def git(*a):
    return subprocess.check_output(["git", "-C", ROOT, *a], text=True, encoding="utf-8")

gofiles = [f for f in git("ls-tree", "-r", "--name-only", C6).split() if f.endswith(".go")]
pkgfiles = {}
for f in gofiles:
    pkgfiles.setdefault(os.path.dirname(f), []).append(f)

def imports(pkg, with_tests):
    out = set()
    for f in pkgfiles.get(pkg, []):
        if f.endswith("_test.go") and not with_tests:
            continue
        src = open(os.path.join(ROOT, f), encoding="utf-8").read()
        for m in re.finditer(r'"' + re.escape(MOD) + r'([\w/]+)"', src):
            out.add(m.group(1))
    return out

def closure(pkg):
    seen, todo = set(), [pkg]
    first = True
    while todo:
        p = todo.pop()
        if p in seen:
            continue
        seen.add(p)
        # the benchmark package's own test files count (they hold the benchmark); deps: product only
        todo.extend(imports(p, with_tests=False))
        first = False
    return seen

changed = [l for l in git("diff", "--name-only", C5, C6, "--", "internal", "cmd", "tools", "test").split()]
pkgs = sys.argv[1:]
for p in pkgs:
    cl = closure(p)
    hits = [c for c in changed if os.path.dirname(c) in cl and (not c.endswith("_test.go") or os.path.dirname(c) == p)]
    print(f"== {p}: closure {len(cl)} pkgs; changed files in closure: {len(hits)}")
    for h in hits:
        print("   ", h, git("diff", "--shortstat", C5, C6, "--", h).strip())
