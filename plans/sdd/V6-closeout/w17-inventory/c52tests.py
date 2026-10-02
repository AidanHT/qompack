"""D57(e) proof, test-file half: which _test.go files each C5.2 benchmark executes, and whether they changed.

c52exec.py measures the product files a benchmark executes with a coverage profile, but Go never
instruments _test.go files, so its intersection covers non-test files only. A benchmark also runs
code in its package's _test.go files: its own body, the helpers it calls, TestMain, and every
package-level variable initializer and init() of the test binary. This script finds those
statically, at candidate 6 (99d0b18c), and intersects them with the _test.go files that differ
between candidate 5 (0d06ab12) and candidate 6.

Method, conservative (it can only widen the executed set):
- every top-level func and method of the package directory's _test.go files (internal and external
  test packages alike, on every GOOS) is indexed by name;
- starting from each benchmark of the set and from TestMain, a function is reached when its name
  appears as an identifier in a reached body, string literals and // comments removed (so a common
  method name reaches every test method of that name), transitively;
- a changed file is executed when it holds a reached function, a package-level var with an
  initializer, or an init();
- for a reached function in a changed file, it says whether any changed line (candidate 6 numbering)
  lies inside that function.

Run from the repository root (it reads git objects only, it runs nothing):
    python plans/sdd/V6-closeout/w17-inventory/c52tests.py > plans/sdd/V6-closeout/w17-inventory/runs/c52-test-files.txt
"""
import os, re, subprocess

C5, C6 = "0d06ab12", "99d0b18c"
HERE = os.path.dirname(os.path.abspath(__file__))
src = open(os.path.join(HERE, "c52exec.py"), encoding="utf-8").read()
# Reuse c52exec.py's BENCH table without running its measurement loop.
BENCH = eval(re.search(r"^BENCH = (\[.*?^\])", src, re.M | re.S).group(1))


def git(*a):
    return subprocess.run(["git", *a], capture_output=True, text=True, encoding="utf-8",
                          errors="replace", check=True).stdout


def changed_lines(f):
    d = git("diff", "-U0", "-M", C5, C6, "--", f)
    lines = set()
    for h in re.finditer(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", d, re.M):
        start, n = int(h.group(1)), int(h.group(2) or "1")
        lines.update(range(start, start + n))
    return lines


def code_only(body):
    """The body without string literals and // comments, so a name in prose reaches nothing."""
    body = re.sub(r"`[^`]*`", '""', body)
    body = re.sub(r'"(?:\\.|[^"\\\n])*"', '""', body)
    body = re.sub(r"'(?:\\.|[^'\\\n])*'", "''", body)
    return re.sub(r"//[^\n]*", "", body)


FUNC = re.compile(r"^func (?:\(\s*\w*\s*\*?\s*(\w+)(?:\[[^\]]*\])?\s*\) )?(\w+)\s*[\[(]")


def index(pkg):
    """name -> list of (file, first, last, body) for the top-level funcs of pkg's _test.go files at C6,
    plus the files with a package-level initializer or init()."""
    funcs, initers = {}, {}
    names = [n for n in git("ls-tree", "--name-only", C6 + ":" + pkg).split() if n.endswith("_test.go")]
    for n in names:
        f = pkg + "/" + n
        text = git("show", f"{C6}:{f}").splitlines()
        i = 0
        while i < len(text):
            ln = text[i]
            m = FUNC.match(ln)
            if m:
                j = i
                if not ln.rstrip().endswith("}") or ln.count("{") != ln.count("}"):
                    j = i + 1
                    while j < len(text) and text[j] != "}":
                        j += 1
                body = code_only("\n".join(text[i:j + 1]))
                funcs.setdefault(m.group(2), []).append((f, i + 1, j + 1, body))
                if m.group(2) == "init" and not m.group(1):
                    initers.setdefault(f, []).append(("init()", i + 1, j + 1))
                i = j + 1
                continue
            v = re.match(r"^var (\w+)[^=]*=", ln)
            if v:
                # The initializer ends where its brackets balance, literals and comments removed.
                j, depth = i, 0
                while True:
                    c = code_only(text[j])
                    depth += c.count("{") + c.count("(") + c.count("[")
                    depth -= c.count("}") + c.count(")") + c.count("]")
                    if depth <= 0 or j + 1 >= len(text):
                        break
                    j += 1
                initers.setdefault(f, []).append(("var " + v.group(1), i + 1, j + 1))
                i = j
            elif ln.startswith("var ("):
                j = i + 1
                while j < len(text) and text[j] != ")":
                    w = re.match(r"^\t(\w+)[^=]*=", text[j])
                    if w:
                        initers.setdefault(f, []).append(("var " + w.group(1), j + 1, j + 1))
                    j += 1
                i = j
            i += 1
    return funcs, initers


IDENT = re.compile(r"\b([A-Za-z_]\w*)\b")
changed = set(git("diff", "--name-only", "-M", C5, C6, "--", "*_test.go").split())
print(f"# D57(e) test-file proof (c52tests.py), read from git objects at candidate 6 {C6}.")
print(f"# _test.go files changed {C5} -> {C6}: {len(changed)}.")
print("# For each benchmark set: the changed _test.go files of its package that the test binary executes")
print("# for that set (a reached function, a package-level initializer or init()), and whether a changed")
print("# line lies inside a reached function. 'none' means D57(e) holds for the set's test files.\n")
summary = []
for pkg, rx, rows in BENCH:
    funcs, initers = index(pkg)
    brx = re.compile(rx)
    roots = [n for n in funcs if brx.match(n)] + (["TestMain"] if "TestMain" in funcs else [])
    seen, todo = set(), list(roots)
    while todo:
        n = todo.pop()
        if n in seen:
            continue
        seen.add(n)
        for (_, _, _, body) in funcs[n]:
            for idn in set(IDENT.findall(body)):
                if idn in funcs and idn not in seen:
                    todo.append(idn)
    reached = {}  # file -> [(name, first, last)]
    for n in seen:
        for (f, a, b, _) in funcs[n]:
            reached.setdefault(f, []).append((n, a, b))
    pkg_changed = sorted(f for f in changed if os.path.dirname(f) == pkg)
    print(f"## rows {rows}: {pkg}")
    print(f"benchmarks: {', '.join(sorted(n for n in roots if n != 'TestMain'))}"
          + ("; TestMain present" if "TestMain" in roots else ""))
    print(f"changed _test.go files in {pkg}: {len(pkg_changed)}")
    hit = []
    for f in pkg_changed:
        why = []
        cl = changed_lines(f)
        r = sorted(reached.get(f, []), key=lambda t: t[1])
        if r:
            inside = sorted({n for (n, a, b) in r if any(a <= l <= b for l in cl)})
            why.append("reached " + ", ".join(n for (n, _, _) in r)
                       + ("; changed lines inside " + ", ".join(inside) if inside
                          else "; no changed line inside a reached function"))
        if f in initers:
            why.append("package initialization: " + ", ".join(
                f"{lab} at line {a}" + (" (changed)" if any(a <= l <= b for l in cl) else " (unchanged)")
                for (lab, a, b) in initers[f]))
        if why:
            hit.append(f)
            print(f"  {f}: " + " | ".join(why))
    if not hit:
        print("  executed changed _test.go files: none")
    else:
        # Per benchmark (with TestMain): the changed files its own reach enters, and where a changed
        # line lies inside a reached function. Package initialization is shared by every benchmark.
        print("  per benchmark (package initialization above applies to all):")
        for bn in sorted(n for n in roots if n != "TestMain"):
            own, todo = set(), [bn] + (["TestMain"] if "TestMain" in funcs else [])
            while todo:
                n = todo.pop()
                if n in own:
                    continue
                own.add(n)
                for (_, _, _, body) in funcs[n]:
                    todo.extend(i for i in set(IDENT.findall(body)) if i in funcs and i not in own)
            parts = []
            for f in pkg_changed:
                fr = [(n, a, b) for n in own for (ff, a, b, _) in funcs[n] if ff == f]
                if fr:
                    cl = changed_lines(f)
                    ins = sorted({n for (n, a, b) in fr if any(a <= l <= b for l in cl)})
                    parts.append(os.path.basename(f) + (" (changed lines in " + ", ".join(ins) + ")"
                                                        if ins else " (no changed line reached)"))
            print(f"    {bn}: " + ("; ".join(parts) if parts else "no changed _test.go function reached"))
    summary.append((rows, pkg, hit))
    print()
print("# sets with an executed changed _test.go file:")
for rows, pkg, hit in summary:
    if hit:
        print(f"#   {pkg} (rows {rows}): {', '.join(os.path.basename(h) for h in hit)}")
