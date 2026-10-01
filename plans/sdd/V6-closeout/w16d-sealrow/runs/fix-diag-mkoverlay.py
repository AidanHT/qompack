"""Temporary diagnostic (fix seat): a go -overlay that traces the PreCompact settle, the spool
index, the watcher's passes, every drain pass, the PreCompact route and every hook client's spool
fallback. The worktree is not modified. Argument: "before" for the pre-fix settle (git show of
ae601390..integration's precompact_settle.go is not needed: the anchors differ), "after" for the
fixed one."""
import json
import os
import sys

WT = "C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16d-sealrow"
MODE = sys.argv[1] if len(sys.argv) > 1 else "after"
OUT = os.path.dirname(os.path.abspath(__file__)).replace("\\", "/") + "/overlay-" + MODE
os.makedirs(OUT, exist_ok=True)

DIAG = '''package %s

import (
	"fmt"
	"os"
	"time"
)

var diagStart = time.Now()

func diagf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "DIAG %%8.3fs "+format+"\\n", append([]any{time.Since(diagStart).Seconds()}, args...)...)
}
'''


def patch(rel, edits):
    src = open(os.path.join(WT, rel), encoding="utf-8").read()
    for old, new in edits:
        if src.count(old) != 1:
            raise SystemExit(f"{rel}: anchor not unique/found: {old!r}")
        src = src.replace(old, new)
    dst = OUT + "/" + rel.replace("/", "_")
    open(dst, "w", encoding="utf-8", newline="\n").write(src)
    return dst


def add(rel, pkg):
    dst = OUT + "/" + rel.replace("/", "_")
    open(dst, "w", encoding="utf-8", newline="\n").write(DIAG % pkg)
    return dst


overlay = {}
overlay[WT + "/internal/daemon/zz_diag.go"] = add("internal/daemon/zz_diag.go", "daemon")
overlay[WT + "/internal/ipc/zz_diag.go"] = add("internal/ipc/zz_diag.go", "ipc")

settle_edits = [
    ("\tupTo := d.leasedUpTo(sess)\n\tfirst := d.scanClientSpools(sctx, sess, at, nil)\n",
     "\tupTo := d.leasedUpTo(sess)\n\tdiagf(\"settle start sess=%s at=%d bound=%v upTo=%d\", sess, at, bound, upTo)\n"
     "\tfirst := d.scanClientSpools(sctx, sess, at, nil)\n"
     "\tdiagf(\"settle first listed=%v unread=%v reads=%d caps=%d\", first.listed, first.unread, first.reads, len(first.caps))\n"
     "\tfor _, c := range first.caps { diagf(\"settle first-cap op=%s tool=%s file=%s ts=%d\", c.op, c.toolUseID, c.file, c.ts) }\n"),
    ("\tif len(own) == 0 && len(first.unread) == 0 && d.arrivalsSettled(sess, upTo) {\n",
     "\tdiagf(\"settle own=%v settled=%v\", own, d.arrivalsSettled(sess, upTo))\n"
     "\tif len(own) == 0 && len(first.unread) == 0 && d.arrivalsSettled(sess, upTo) {\n"),
    ("\tleft := d.unreplayedCaptures(sess, upTo, last.caps)\n",
     "\tleft := d.unreplayedCaptures(sess, upTo, last.caps)\n"
     "\tdiagf(\"settle end left=%d lastUnread=%v lastCaps=%d sctxErr=%v upToNow=%d\", len(left), last.unread, len(last.caps), sctx.Err(), d.leasedUpTo(sess))\n"),
]
if MODE == "after":
    settle_edits.append(
        ("\t\tn, err := dr.DrainClientSpoolsWithin(ctx, own)\n",
         "\t\tn, err := dr.DrainClientSpoolsWithin(ctx, own)\n"
         "\t\tdiagf(\"settle replay n=%d err=%v upToNow=%d\", n, err, d.leasedUpTo(sess))\n"))
rel = "internal/daemon/precompact_settle.go"
overlay[WT + "/" + rel] = patch(rel, settle_edits)

rel = "internal/daemon/spool_heads.go"
overlay[WT + "/" + rel] = patch(rel, [
    ("\tif hit && f.size == l.size && f.mod.Equal(l.mod) {\n\t\treturn f.lines, true, false\n\t}\n",
     "\tif hit && f.size == l.size && f.mod.Equal(l.mod) {\n"
     "\t\tdiagf(\"heads HIT %s size=%d lines=%d\", l.base, l.size, len(f.lines))\n"
     "\t\treturn f.lines, true, false\n\t}\n"),
    ("\tb, err := readFile(ctx, filepath.Join(paths.Of(root).Spool, l.base))\n",
     "\tb, err := readFile(ctx, filepath.Join(paths.Of(root).Spool, l.base))\n"
     "\tdiagf(\"heads READ %s listed size=%d got=%d err=%v\", l.base, l.size, len(b), err)\n"),
])

rel = "internal/daemon/spool_watch.go"
overlay[WT + "/" + rel] = patch(rel, [
    ("\t\tpass, budget := newPassBudget(ctx, idleRunBudget)\n",
     "\t\tdiagf(\"watch pass due=%d\", len(due))\n\t\tpass, budget := newPassBudget(ctx, idleRunBudget)\n"),
])

rel = "internal/daemon/drain.go"
overlay[WT + "/" + rel] = patch(rel, [
    ("\tspoolDir := paths.Of(dr.cfg.Root).Spool\n\tfiles, err := ipc.SpoolFiles(spoolDir)\n",
     "\tspoolDir := paths.Of(dr.cfg.Root).Spool\n\tfiles, err := ipc.SpoolFiles(spoolDir)\n"
     "\tdiagf(\"drain pass clientOnly=%v only=%v files=%d err=%v\", clientOnly, only, len(files), err)\n"),
    ("\t\tn, ferr := dr.drainFile(ctx, path, st, gaps)\n",
     "\t\tn, ferr := dr.drainFile(ctx, path, st, gaps)\n\t\tdiagf(\"drain file %s n=%d err=%v\", filepath.Base(path), n, ferr)\n"),
])

rel = "internal/daemon/handlers.go"
overlay[WT + "/" + rel] = patch(rel, [
    ("\t\t\tsealCtx := ctx\n\t\t\tif !spoolReplay(ctx) {\n",
     "\t\t\tsealCtx := ctx\n\t\t\tdiagf(\"precompact route replay=%v sess=%s\", spoolReplay(ctx), ev.SessionID)\n\t\t\tif !spoolReplay(ctx) {\n"),
])

rel = "internal/ipc/client.go"
overlay[WT + "/" + rel] = patch(rel, [
    ("func (c *client) spoolAndReturn(req Request) (Response, error) {\n",
     "func (c *client) spoolAndReturn(req Request) (Response, error) {\n"
     "\t_, diagFile, diagLine, _ := runtime.Caller(1)\n"
     "\tdiagf(\"client spool op=%s from %s:%d\", req.Op, filepath.Base(diagFile), diagLine)\n"),
    ('import (\n\t"context"', 'import (\n\t"runtime"\n\t"context"'),
])

json.dump({"Replace": overlay}, open(OUT + "/overlay.json", "w"), indent=1)
print(OUT + "/overlay.json")
