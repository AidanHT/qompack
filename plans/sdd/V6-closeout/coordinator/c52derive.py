"""c52derive.py <candidate-repo> <base-rev> <out-dir> [quiet.sh]
c52derive.py --selftest

D57(e) by construction: which C5.2 benchmarks lose their carry on the frozen candidate, so
overnight-c8.sh re-measures exactly those (c52-win and c52-linux) and nothing is assumed. The method
is w17-inventory/c52exec.py's, per benchmark instead of per package, against a base you name
(overnight-c8.sh passes candidate 7, d20309c0):

For every benchmark in quiet.sh's list that the candidate defines, run it once (-benchtime=1x: an
execution trace, not a measurement) with a set-mode coverage profile over the whole module, and
select it when
  (a) a product file it executes (a non-test .go file with an executed statement) changed in code
      between <base-rev> and the candidate. A change that touches only blank lines or // comments
      does not count, as in c52exec.py, except a //go: directive, which is code;
  (b) the _test.go file that defines it changed in code (the benchmark's own body);
  (c) a file under its package's testdata/ changed (its fixtures); or
  (d) a non-.go file beside one of the product files it executes changed (an embedded asset).
Changed _test.go files of its package other than (b) are listed in the report but not selected:
they may hold its helpers, and coverage cannot see test files.
A benchmark whose trace fails (go test exits non-zero, writes no profile, or never runs it) is
selected, fail-closed, and the exit status is 1. When EVERY trace fails, the derivation itself is
broken (a flag, the toolchain), and selecting every row would put the whole C5.2 list (about 3.5 h
per OS) into the night: it exits 2 instead, and overnight-c8.sh measures its static floor. go test
runs with -p 1 and -timeout 10m (its own default, made explicit) and without QOMPACK_UNDER_COLOAD.
An untracked or git-ignored .go file in the candidate tree (outside testdata/ and _ or . dirs)
refuses the run (exit 2): -coverpkg=<module>/... would compile it into every trace.

Writes into <out-dir>: report.txt (per benchmark: the command, the executed changed files and the
verdict), selection.tsv (package, benchmark, reasons), pkgs.txt (QUIET_PKGS) and filter.txt
(QUIET_BENCH_FILTER), both empty when nothing is selected. A listed benchmark the candidate does not
define is reported and skipped.
Exit: 0 every benchmark classified; 1 a trace failed (that row is still selected); 2 it could not
run at all (the base does not resolve, the tree has tracked changes or a stray .go file, no go.mod,
no list, every trace failed), and then no selection file is written.
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
            rows.append((f[0], f[1]))
    raise ValueError("no complete benches() list")


def diff_is_code(diff_text):
    """True when an added or removed line is neither blank nor a // comment (a //go: line is code)."""
    for ln in diff_text.splitlines():
        if ln[:1] in "+-" and not ln.startswith(("+++", "---")):
            t = ln[1:].strip()
            if t and not (t.startswith("//") and not t.startswith("//go:")):
                return True
    return False


def parse_profile(text, module):
    """The module-relative product files with an executed statement in a set-mode profile."""
    out, pre = set(), module.rstrip("/") + "/"
    for line in text.splitlines():
        if line.startswith("mode:") or not line.strip():
            continue
        loc, _, rest = line.rpartition(" ")
        if not loc or rest.strip() in ("", "0"):
            continue
        path = loc.split(" ")[0].rsplit(":", 1)[0]
        if path.startswith(pre):
            out.add(path[len(pre):])
    return out


def classify(pkg, name, executed, defining, changes):
    """The reasons a benchmark is selected (empty: its carry holds), and the informational list.
    changes: {"code": set of non-test .go paths changed in code, "test": set of _test.go paths changed
    in code, "testdata": set of changed paths under some testdata/, "asset": set of other changed
    non-.go paths}."""
    reasons = []
    d = "internal/%s/" % pkg
    ex = sorted(executed & changes["code"])
    if ex:
        reasons.append("executes changed " + ",".join(ex))
    own = sorted(set(defining) & changes["test"])
    if own:
        reasons.append("its own benchmark file changed " + ",".join(own))
    td = sorted(p for p in changes["testdata"] if p.startswith(d + "testdata/"))
    if td:
        reasons.append("its fixtures changed " + ",".join(td))
    dirs = {os.path.dirname(p) for p in executed}
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
    s = {"code": set(), "test": set(), "testdata": set(), "asset": set(), "comment_only": set()}
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
        else:
            s["asset"].add(p)
    return s


def trace(repo, module, pkg, name, prof):
    cmd = GO + ["test", "-p", "1", "-count=1", "-timeout", "10m", "-run", "^$", "-bench", "^Benchmark%s$" % name,
           "-benchtime=1x", "-covermode=set", "-coverpkg=%s/..." % module, "-coverprofile=" + prof, "./internal/" + pkg]
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
    sel, failed, traced = [], 0, 0
    tmp = tempfile.mkdtemp(prefix="c52derive-")
    try:
        for i, (pkg, name) in enumerate(benches):
            g = git(repo, "grep", "-l", "-E", r"^func Benchmark%s\(" % name, cand, "--", "internal/%s/" % pkg)
            defining = [l.split(":", 1)[1] for l in g.stdout.split() if ":" in l]
            if not defining:
                rep.append("## internal/%s Benchmark%s: not defined on the candidate; skipped\n" % (pkg, name))
                continue
            traced += 1
            prof = os.path.join(tmp, "p%d.out" % i).replace("\\", "/")
            shown, executed, err = trace(repo, module, pkg, name, prof)
            rep.append("## internal/%s Benchmark%s\n$ %s" % (pkg, name, shown))
            if err:
                failed += 1
                sel.append((pkg, name, "trace failed (%s): selected, fail-closed" % err))
                rep.append("TRACE FAILED: %s\nverdict: SELECTED (fail-closed)\n" % err)
                continue
            reasons, helpers = classify(pkg, name, executed, defining, changes)
            rep.append("product files executed: %d" % len(executed))
            if helpers:
                rep.append("changed test files of its package (helpers coverage cannot see; not selected): " + ", ".join(helpers))
            if reasons:
                sel.append((pkg, name, "; ".join(reasons)))
                rep.append("verdict: SELECTED: %s\n" % "; ".join(reasons))
            else:
                rep.append("verdict: carry holds (no executed file, own file or fixture changed)\n")
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    pkgs = []
    for p, _, _ in sel:
        if p not in pkgs:
            pkgs.append(p)
    rep.append("# selected: %d of %d listed; failed traces: %d" % (len(sel), len(benches), failed))
    rep.append("# QUIET_PKGS=%s" % " ".join(pkgs))
    rep.append("# QUIET_BENCH_FILTER=%s" % bench_filter([n for _, n, _ in sel]))
    w = lambda f, s: open(os.path.join(out, f), "w", encoding="utf-8", newline="\n").write(s)
    if traced and failed == traced:
        rep.append("# every trace failed: the derivation itself is broken, so no selection is written")
        w("report.txt", "\n".join(rep) + "\n")
        return fatal("every one of the %d traces failed, so the derivation itself is broken (report.txt)" % traced)
    w("report.txt", "\n".join(rep) + "\n")
    w("selection.tsv", "package\tbenchmark\treasons\n" + "".join("internal/%s\tBenchmark%s\t%s\n" % r for r in sel))
    w("pkgs.txt", " ".join(pkgs) + "\n")
    w("filter.txt", bench_filter([n for _, n, _ in sel]) + "\n")
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

    lst = parse_bench_list("x\nbenches() { cat <<'EOF'\nstore GetChunk 1s src a\ncheckpoint Finalize 10x b\nEOF\n}\n")
    check("list", lst == [("store", "GetChunk"), ("checkpoint", "Finalize")])
    try:
        parse_bench_list("benches() { cat <<'EOF'\nstore A 1s\n")
        check("an unterminated list is refused", False)
    except ValueError:
        pass
    check("comment-only", not diff_is_code("--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-// old\n+// new\n+\n"))
    check("code", diff_is_code("@@ -1 +1 @@\n-return 1\n+return 2\n"))
    check("a //go: directive is code", diff_is_code("@@ -1 +1 @@\n+//go:build windows\n"))
    prof = "mode: set\nm/q/internal/a/a.go:1.1,2.2 1 1\nm/q/internal/a/b.go:3.1,4.2 2 0\nm/q/internal/c/c.go:1.1,2.2 1 1\nother/x.go:1.1,2.2 1 1\n"
    check("profile", parse_profile(prof, "m/q") == {"internal/a/a.go", "internal/c/c.go"})
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
    check("filter", bench_filter(["A", "B_1"]) == "^Benchmark(A|B_1)$" and bench_filter([]) == "")
    print("selftest:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(selftest() if sys.argv[1:] == ["--selftest"] else main(sys.argv))
