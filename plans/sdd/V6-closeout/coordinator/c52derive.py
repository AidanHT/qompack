"""c52derive.py <candidate-repo> <base-rev> <out-dir> [quiet.sh]
c52derive.py --selftest

D57(e) by construction: which C5.2 benchmarks lose their carry on the frozen candidate, so
overnight-c8.sh re-measures exactly those (c52-win and c52-linux) and nothing is assumed. The method
is w17-inventory/c52exec.py's, per benchmark instead of per package, against a base you name
(overnight-c8.sh passes candidate 7, d20309c0):

For every benchmark in quiet.sh's list that the candidate defines, run it once at its own listed
benchtime (quiet.sh's third column: 1s, 10x or 200x; an execution trace, not a measurement) with a
set-mode coverage profile over the whole module, and select it when
  (a) a product file it executes (a non-test .go file with an executed statement) changed in code
      between <base-rev> and the candidate. A change that touches only blank lines or // comments
      does not count, as in c52exec.py, except a //go: directive, which is code;
  (b) another non-test .go file of a package it executes changed in code. Coverage sees statements
      only: a file holding only declarations (a const, a type, a struct field, a var without an
      initializer call) has no coverage block and never reads as executed, though the benchmark
      may depend on it; and a file the trace's OS does not build (foo_linux.go, traced on Windows)
      is never executed here though c52-linux runs it. So a code change anywhere in a package
      directory holding one of its executed files selects it;
  (c) the _test.go file that defines it changed in code (the benchmark's own body);
  (d) a file under its package's testdata/ changed (its fixtures); or
  (e) a non-.go file beside one of the product files it executes changed (an embedded asset).
Tracing at the row's own benchtime matters: with b.N=1 the code a benchmark reaches only after its
first iterations (periodic rotation, compaction, eviction once a cache fills) never runs, so a
change there would read as a carry. At the listed benchtime the trace runs every b.N the measurement
runs.
Changed _test.go files of its package other than (c) are listed in the report but not selected:
they may hold its helpers, and coverage cannot see test files.
A benchmark whose trace fails (go test exits non-zero, writes no profile, or never runs it) is
selected, fail-closed, and the exit status is 1. When EVERY trace fails, the derivation itself is
broken (a flag, the toolchain), and selecting every row would hide that the derivation never ran:
it exits 2 instead, and overnight-c8.sh measures the full C5.2 list (D62(b)). go test
runs with -p 1 and -timeout 10m (its own default, made explicit) and without QOMPACK_UNDER_COLOAD.
An untracked or git-ignored .go file in the candidate tree (outside testdata/ and _ or . dirs)
refuses the run (exit 2): -coverpkg=<module>/... would compile it into every trace.

Line-level evidence, reported, never used to select: for each executed changed file, whether an
executed coverage block covers a changed line (candidate-side line numbers; a pure deletion counts
its two neighbouring lines), as c52exec.py reported it. D57(e) is file-level, so a row whose
executed changed files have no executed block on a changed line (a struct field, code reached only
through a package's init) is still selected; the report counts those rows, for the owner's ruling
on whether such a change keeps the carry.

Writes into <out-dir>: report.txt (per benchmark: the command, the executed changed files with
their line-level evidence and the verdict), selection.tsv (package, benchmark, reasons,
changed_line_executed: yes, no, or - when no executed changed file selected it), pkgs.txt
(QUIET_PKGS) and filter.txt (QUIET_BENCH_FILTER), both empty when nothing is selected. A listed
benchmark the candidate does not define is reported and skipped.
Exit: 0 every benchmark classified; 1 a trace failed (that row is still selected); 2 it could not
run at all (the base does not resolve, the tree has tracked changes or a stray .go file, no go.mod,
no list or a row without a valid benchtime, every trace failed), and then no selection file is
written.
"""
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile

# The go command; C52_GO replaces it only in nightharness.sh, whose go is a sh stub that a native
# Windows Python cannot start by name.
GO = shlex.split(os.environ.get("C52_GO", "go"))
QUIET_LIST_START = re.compile(r"^benches\(\) \{ cat <<'(\w+)'\s*$")
BENCHTIME = re.compile(r"^(\d+x|\d+(\.\d+)?(ns|us|ms|s|m|h))$")
PROFILE_LINE = re.compile(r"^(.+?):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$")
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", re.M)


def parse_bench_list(text):
    """quiet.sh's benches() heredoc: one "<pkg> <Name> <benchtime> <source...>" per line."""
    rows, delim = [], None
    for line in text.splitlines():
        if delim is None:
            m = QUIET_LIST_START.match(line)
            if m:
                delim = m.group(1)
            continue
        if line.strip() == delim:
            return rows
        f = line.split()
        if len(f) >= 2:
            if len(f) < 3 or not BENCHTIME.match(f[2]):
                raise ValueError("list row without a valid benchtime: %r" % line)
            rows.append((f[0], f[1], f[2]))
    raise ValueError("no complete benches() list")


def diff_is_code(diff_text):
    """True when an added or removed line is neither blank nor a // comment (a //go: line is code)."""
    for ln in diff_text.splitlines():
        if ln[:1] in "+-" and not ln.startswith(("+++", "---")):
            t = ln[1:].strip()
            if t and not (t.startswith("//") and not t.startswith("//go:")):
                return True
    return False


def changed_lines(diff_text):
    """Candidate-side line numbers a -U0 diff touches: its added lines, and for a pure deletion
    (+n,0) the two lines around it, so an executed block spanning the deleted code still counts."""
    out = set()
    for h in HUNK.finditer(diff_text):
        start, n = int(h.group(1)), int(h.group(2) if h.group(2) is not None else "1")
        out.update(range(start, start + n) if n else (start, start + 1))
    return out


def parse_profile(text, module):
    """{module-relative product file: [(first line, last line)] of its executed blocks} from a
    set-mode profile; the keys are the files with an executed statement."""
    out, pre = {}, module.rstrip("/") + "/"
    for line in text.splitlines():
        m = PROFILE_LINE.match(line.strip())
        if not m or m.group(4) == "0" or not m.group(1).startswith(pre):
            continue
        out.setdefault(m.group(1)[len(pre):], []).append((int(m.group(2)), int(m.group(3))))
    return out


def line_level(blocks, lines):
    """The changed lines an executed block covers."""
    return sorted({ln for a, b in blocks for ln in range(a, b + 1)} & lines)


def classify(pkg, name, executed, defining, changes):
    """The reasons a benchmark is selected (empty: its carry holds), and the informational list.
    executed: the set of executed product files. changes: {"code": set of non-test .go paths changed
    in code, "test": set of _test.go paths changed in code, "testdata": set of changed paths under
    some testdata/, "asset": set of other changed non-.go paths}."""
    reasons = []
    d = "internal/%s/" % pkg
    ex = sorted(executed & changes["code"])
    if ex:
        reasons.append("executes changed " + ",".join(ex))
    dirs = {os.path.dirname(p) for p in executed}
    decl = sorted(p for p in changes["code"] if os.path.dirname(p) in dirs and p not in executed)
    if decl:
        reasons.append("a changed file in a package it executes " + ",".join(decl) +
                       " (declarations and other-OS files have no executed block)")
    own = sorted(set(defining) & changes["test"])
    if own:
        reasons.append("its own benchmark file changed " + ",".join(own))
    td = sorted(p for p in changes["testdata"] if p.startswith(d + "testdata/"))
    if td:
        reasons.append("its fixtures changed " + ",".join(td))
    assets = sorted(p for p in changes["asset"] if os.path.dirname(p) in dirs)
    if assets:
        reasons.append("an asset beside its executed code changed " + ",".join(assets))
    helpers = sorted(p for p in changes["test"] if p.startswith(d) and "/" not in p[len(d):] and p not in own)
    return reasons, helpers


def bench_filter(names):
    return "^Benchmark(%s)$" % "|".join(names) if names else ""


def git(repo, *a):
    return subprocess.run(["git", "-C", repo, *a], capture_output=True, text=True, encoding="utf-8")


def changed_sets(repo, base, cand):
    r = git(repo, "diff", "--name-only", "--no-renames", base, cand)
    if r.returncode != 0:
        raise RuntimeError("git diff failed: " + r.stderr.strip())
    s = {"code": set(), "test": set(), "testdata": set(), "asset": set(), "comment_only": set(), "lines": {}}
    for p in r.stdout.split():
        if "/testdata/" in "/" + p:
            s["testdata"].add(p)
        elif p.endswith(".go"):
            d = git(repo, "diff", "-U0", "--no-renames", base, cand, "--", p).stdout
            if not diff_is_code(d):
                s["comment_only"].add(p)
            elif p.endswith("_test.go"):
                s["test"].add(p)
            else:
                s["code"].add(p)
                s["lines"][p] = changed_lines(d)
        else:
            s["asset"].add(p)
    return s


def trace(repo, module, pkg, name, benchtime, prof):
    cmd = GO + ["test", "-p", "1", "-count=1", "-timeout", "10m", "-run", "^$", "-bench", "^Benchmark%s$" % name,
           "-benchtime=" + benchtime, "-covermode=set", "-coverpkg=%s/..." % module, "-coverprofile=" + prof,
           "./internal/" + pkg]
    env = {k: v for k, v in os.environ.items() if k not in ("QOMPACK_UNDER_COLOAD", "QOMPACK_NONREFERENCE_DISK")}
    r = subprocess.run(cmd, cwd=repo, capture_output=True, text=True, encoding="utf-8", errors="replace", env=env)
    shown = " ".join(c if re.fullmatch(r"[\w./=:^$-]+", c) else "'%s'" % c for c in ["go"] + cmd[len(GO):]).replace(prof, "<tmp>")
    out = r.stdout + r.stderr
    ran = re.search(r"^Benchmark%s(/\S+)?(-\d+)?\s" % re.escape(name), out, re.M) is not None
    if r.returncode != 0:
        return shown, None, "go test exited %d: %s" % (r.returncode, (out.strip().splitlines() or ["no output"])[-1])
    if not ran:
        return shown, None, "go test exited 0 but never ran Benchmark%s" % name
    try:
        text = open(prof, encoding="utf-8").read()
    except OSError as e:
        return shown, None, "no coverage profile (%s)" % e
    return shown, parse_profile(text, module), ""


def main(argv):
    if len(argv) not in (4, 5):
        sys.exit(__doc__)
    repo, base, out = argv[1:4]
    quiet = argv[4] if len(argv) == 5 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "quiet.sh")

    def fatal(msg):
        print("c52derive: " + msg, file=sys.stderr)
        return 2

    cand = git(repo, "rev-parse", "--verify", "HEAD^{commit}")
    base_sha = git(repo, "rev-parse", "--verify", "%s^{commit}" % base)
    if cand.returncode != 0 or base_sha.returncode != 0:
        return fatal("cannot resolve the candidate (HEAD) or the base %r in %s" % (base, repo))
    cand, base_sha = cand.stdout.strip(), base_sha.stdout.strip()
    if git(repo, "status", "--porcelain", "--untracked-files=no").stdout.strip():
        return fatal("%s has tracked changes; the trace must run the candidate's own tree" % repo)
    strays = [p for p in (git(repo, "ls-files", "--others", "--exclude-standard", "--", "*.go").stdout.split() +
                          git(repo, "ls-files", "--others", "--ignored", "--exclude-standard", "--", "*.go").stdout.split())
              if not any(c == "testdata" or c[:1] in "_." for c in p.split("/")[:-1])]
    if strays:
        return fatal("untracked or ignored .go files would join -coverpkg's packages: %s" % ", ".join(sorted(strays)[:5]))
    try:
        module = next(l.split()[1] for l in open(os.path.join(repo, "go.mod"), encoding="utf-8") if l.startswith("module "))
        benches = parse_bench_list(open(quiet, encoding="utf-8").read())
        changes = changed_sets(repo, base_sha, cand)
    except (OSError, StopIteration, ValueError, RuntimeError) as e:
        return fatal(str(e))

    os.makedirs(out, exist_ok=True)
    rep = ["# c52derive: C5.2 rows whose carry breaks, candidate %s against base %s (%s)." % (cand, base_sha, base),
           "# Changed since the base: %d product .go file(s) in code, %d _test.go, %d under testdata/, %d other,"
           " %d .go comment-only (ignored)." % tuple(len(changes[k]) for k in ("code", "test", "testdata", "asset", "comment_only")),
           "# Product files changed in code: " + (", ".join(sorted(changes["code"])) or "none"), ""]
    sel, failed, traced, no_line = [], 0, 0, []
    tmp = tempfile.mkdtemp(prefix="c52derive-")
    try:
        for i, (pkg, name, benchtime) in enumerate(benches):
            g = git(repo, "grep", "-l", "-E", r"^func Benchmark%s\(" % name, cand, "--", "internal/%s/" % pkg)
            defining = [l.split(":", 1)[1] for l in g.stdout.split() if ":" in l]
            if not defining:
                rep.append("## internal/%s Benchmark%s: not defined on the candidate; skipped\n" % (pkg, name))
                continue
            traced += 1
            prof = os.path.join(tmp, "p%d.out" % i).replace("\\", "/")
            shown, blocks, err = trace(repo, module, pkg, name, benchtime, prof)
            rep.append("## internal/%s Benchmark%s\n$ %s" % (pkg, name, shown))
            if err:
                failed += 1
                sel.append((pkg, name, "trace failed (%s): selected, fail-closed" % err, "-"))
                rep.append("TRACE FAILED: %s\nverdict: SELECTED (fail-closed)\n" % err)
                continue
            executed = set(blocks)
            reasons, helpers = classify(pkg, name, executed, defining, changes)
            rep.append("product files executed: %d" % len(executed))
            hit = sorted(executed & changes["code"])
            any_line = False
            for f in hit:
                over, lines = line_level(blocks[f], changes["lines"].get(f, set())), changes["lines"].get(f, set())
                any_line = any_line or bool(over)
                rep.append("  %s: executed blocks over changed lines: %s (changed lines %s)" % (
                    f, ", ".join(map(str, over)) if over else "none", ", ".join(map(str, sorted(lines))) or "none"))
            if helpers:
                rep.append("changed test files of its package (helpers coverage cannot see; not selected): " + ", ".join(helpers))
            if reasons:
                file_level = [r for r in reasons if r.startswith(("executes changed ", "a changed file in a package"))]
                cle = "yes" if any_line else ("no" if file_level else "-")
                if cle == "no" and len(file_level) == len(reasons):
                    no_line.append(name)
                sel.append((pkg, name, "; ".join(reasons), cle))
                rep.append("verdict: SELECTED: %s\n" % "; ".join(reasons))
            else:
                rep.append("verdict: carry holds (no executed file, file of an executed package, own file, fixture or adjacent asset changed)\n")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    pkgs = []
    for p, _, _, _ in sel:
        if p not in pkgs:
            pkgs.append(p)
    rep.append("# selected: %d of %d listed; failed traces: %d" % (len(sel), len(benches), failed))
    rep.append("# selected with no executed block on a changed line and no other reason (D57(e) is file-level,"
               " so they stay selected; the owner's line-level question): %d%s" % (len(no_line), ": " + ", ".join(no_line) if no_line else ""))
    rep.append("# QUIET_PKGS=%s" % " ".join(pkgs))
    rep.append("# QUIET_BENCH_FILTER=%s" % bench_filter([n for _, n, _, _ in sel]))
    w = lambda f, s: open(os.path.join(out, f), "w", encoding="utf-8", newline="\n").write(s)
    if traced and failed == traced:
        rep.append("# every trace failed: the derivation itself is broken, so no selection is written")
        w("report.txt", "\n".join(rep) + "\n")
        return fatal("every one of the %d traces failed, so the derivation itself is broken (report.txt)" % traced)
    w("report.txt", "\n".join(rep) + "\n")
    w("selection.tsv", "package\tbenchmark\treasons\tchanged_line_executed\n" +
      "".join("internal/%s\tBenchmark%s\t%s\t%s\n" % r for r in sel))
    w("pkgs.txt", " ".join(pkgs) + "\n")
    w("filter.txt", bench_filter([n for _, n, _, _ in sel]) + "\n")
    print("c52derive: %d selected, %d failed trace(s); %s" % (len(sel), failed, os.path.join(out, "report.txt")))
    return 1 if failed else 0


def selftest():
    """Synthetic diffs, profiles and lists; no Go, no repository."""
    ok = True

    def check(what, cond):
        nonlocal ok
        if not cond:
            ok = False
            print("FAIL", what)

    lst = parse_bench_list("x\nbenches() { cat <<'EOF'\nstore GetChunk 1s src a\ncheckpoint Finalize 10x b\nobserver X 200x c\nEOF\n}\n")
    check("list with benchtimes", lst == [("store", "GetChunk", "1s"), ("checkpoint", "Finalize", "10x"), ("observer", "X", "200x")])
    for bad in ("benches() { cat <<'EOF'\nstore A 1s\n", "benches() { cat <<'EOF'\nstore A\nEOF\n", "benches() { cat <<'EOF'\nstore A fast src\nEOF\n"):
        try:
            parse_bench_list(bad)
            check("refused: %r" % bad, False)
        except ValueError:
            pass
    check("comment-only", not diff_is_code("--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-// old\n+// new\n+\n"))
    check("code", diff_is_code("@@ -1 +1 @@\n-return 1\n+return 2\n"))
    check("a //go: directive is code", diff_is_code("@@ -1 +1 @@\n+//go:build windows\n"))
    check("changed lines", changed_lines("@@ -3 +3 @@\n-a\n+b\n@@ -10,2 +11,3 @@\n") == {3, 11, 12, 13})
    check("a pure deletion counts its neighbours", changed_lines("@@ -7,2 +6,0 @@\n-x\n-y\n") == {6, 7})
    prof = ("mode: set\nm/q/internal/a/a.go:1.1,2.2 1 1\nm/q/internal/a/b.go:3.1,4.2 2 0\n"
            "m/q/internal/c/c.go:1.1,2.2 1 1\nm/q/internal/c/c.go:8.1,9.2 1 1\nother/x.go:1.1,2.2 1 1\n")
    blocks = parse_profile(prof, "m/q")
    check("profile", set(blocks) == {"internal/a/a.go", "internal/c/c.go"})
    check("profile blocks", blocks["internal/c/c.go"] == [(1, 2), (8, 9)])
    check("line-level hit", line_level(blocks["internal/c/c.go"], {9, 20}) == [9])
    check("line-level miss (a struct field)", line_level(blocks["internal/c/c.go"], {5}) == [])
    ch = {"code": {"internal/c/c.go", "internal/z/z.go"}, "test": {"internal/a/a_bench_test.go", "internal/a/helper_test.go"},
          "testdata": {"internal/a/testdata/in.json"}, "asset": {"internal/c/table.json", "internal/q/q.json"}}
    r, h = classify("a", "X", {"internal/a/a.go", "internal/c/c.go"}, ["internal/a/a_bench_test.go"], ch)
    check("executed changed file", any("executes changed internal/c/c.go" == x for x in r))
    check("own benchmark file", any(x.startswith("its own benchmark file changed") for x in r))
    check("fixtures", any(x.startswith("its fixtures changed internal/a/testdata/in.json") for x in r))
    check("asset beside executed code", any(x == "an asset beside its executed code changed internal/c/table.json" for x in r))
    check("helpers reported, not selected", h == ["internal/a/helper_test.go"])
    r, h = classify("b", "Y", {"internal/b/b.go"}, ["internal/b/b_test.go"], ch)
    check("nothing it executes changed: carry holds", r == [] and h == [])
    # A declaration-only file (types.go: a struct field) has no coverage block, so it is never in the
    # executed set; it changed in a package the benchmark executes, so the row is selected.
    ch2 = {"code": {"internal/d/types.go", "internal/e/e_linux.go"}, "test": set(), "testdata": set(), "asset": set()}
    r, _ = classify("d", "Z", {"internal/d/d.go"}, ["internal/d/d_test.go"], ch2)
    check("a declaration-only change in an executed package selects it",
          any(x.startswith("a changed file in a package it executes internal/d/types.go") for x in r))
    r, _ = classify("e", "W", {"internal/e/e.go"}, ["internal/e/e_test.go"], ch2)
    check("an other-OS file of an executed package selects it",
          any(x.startswith("a changed file in a package it executes internal/e/e_linux.go") for x in r))
    r, _ = classify("f", "V", {"internal/f/f.go"}, ["internal/f/f_test.go"], ch2)
    check("a change in a package it never executes does not", r == [])
    check("filter", bench_filter(["A", "B_1"]) == "^Benchmark(A|B_1)$" and bench_filter([]) == "")
    print("selftest:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(selftest() if sys.argv[1:] == ["--selftest"] else main(sys.argv))
