"""Temporary diagnostic: build a go -overlay that times the PreCompact settle's stages and each
replayed line. The worktree is not modified."""
import json
import os
import sys

WT = "C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w16d-sealrow"
OUT = os.path.dirname(os.path.abspath(__file__)).replace("\\", "/") + "/overlay"
os.makedirs(OUT, exist_ok=True)
EXTRA_SLEEP = sys.argv[1] if len(sys.argv) > 1 else ""
WHICH = sys.argv[2] if len(sys.argv) > 2 else "stop"


def patch(rel, edits):
    src = open(os.path.join(WT, rel), encoding="utf-8").read()
    for old, new in edits:
        if src.count(old) != 1:
            raise SystemExit(f"{rel}: anchor not unique/found: {old!r}")
        src = src.replace(old, new)
    dst = OUT + "/" + rel.replace("/", "_")
    open(dst, "w", encoding="utf-8", newline="\n").write(src)
    return dst


overlay = {}

settle = "internal/daemon/precompact_settle.go"
overlay[WT + "/" + settle] = patch(settle, [
    ('import (\n\t"cmp"', 'import (\n\t"os"\n\t"cmp"'),
    ("\tupTo := d.leasedUpTo(sess)\n\tfirst := d.scanClientSpools(sctx, sess, at, nil)\n",
     "\tdiagT0 := time.Now()\n\tdiag := func(stage string, kv ...any) { fmt.Fprintf(os.Stderr, \"DIAG settle %s +%v %v\\n\", stage, time.Since(diagT0), kv) }\n"
     "\tupTo := d.leasedUpTo(sess)\n\tfirst := d.scanClientSpools(sctx, sess, at, nil)\n"
     "\tfor _, c := range first.caps { diag(\"first-cap\", c.op, c.toolUseID, c.file) }\n"),
    ("\td.awaitArrivals(sctx, sess, upTo)\n\tif len(own) > 0",
     "\tdiag(\"before-await\", \"own\", len(own), \"upTo\", upTo)\n\td.awaitArrivals(sctx, sess, upTo)\n\tdiag(\"after-await\")\n\tif len(own) > 0"),
    ("\t\t\t_, err := dr.DrainClientSpoolsWithin(sctx, own)\n",
     "\t\t\tdiagN, err := dr.DrainClientSpoolsWithin(sctx, own)\n\t\t\tdiag(\"after-drain\", \"n\", diagN, \"err\", err)\n"),
    ("\tleft := d.unreplayedCaptures(sess, upTo, last.caps)\n",
     "\tleft := d.unreplayedCaptures(sess, upTo, last.caps)\n"
     "\tfor _, c := range left { diag(\"left\", c.op, c.toolUseID, c.file, c.nonce) }\n"
     "\tdiag(\"end\", \"left\", len(left), \"unread\", len(last.unread))\n"),
])

drain = "internal/daemon/drain.go"
sleep = ""
if EXTRA_SLEEP:
    cond = "resolved.Op == ipc.OpObserveStop" if WHICH == "stop" else "resolved.Op.HotPath()"
    sleep = ("\tif " + cond + " { select { case <-time.After(" + EXTRA_SLEEP + "): case <-ctx.Done(): } }\n")
overlay[WT + "/" + drain] = patch(drain, [
    ("\t\tblob, dispatchErr := dr.dispatchPending(ctx, dl.req, dl.lease, dl.leased, dl.key)\n",
     "\t\tdiagT := time.Now()\n\t\tblob, dispatchErr := dr.dispatchPending(ctx, dl.req, dl.lease, dl.leased, dl.key)\n"
     "\t\tfmt.Fprintf(os.Stderr, \"DIAG drain line op=%s took=%v err=%v ctxErr=%v\\n\", dl.req.Op, time.Since(diagT), dispatchErr, ctx.Err())\n"),
    ("\tresp := dr.cfg.Dispatch(observer.WithObservation(dctx, lease.ObservationID), resolved)\n",
     sleep + "\tdiagD := time.Now()\n\tresp := dr.cfg.Dispatch(observer.WithObservation(dctx, lease.ObservationID), resolved)\n"
     "\tfmt.Fprintf(os.Stderr, \"DIAG drain handler op=%s took=%v\\n\", resolved.Op, time.Since(diagD))\n"),
])

json.dump({"Replace": overlay}, open(OUT + "/overlay.json", "w"), indent=1)
print(OUT + "/overlay.json")
