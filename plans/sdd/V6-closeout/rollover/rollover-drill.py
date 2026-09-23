#!/usr/bin/env python3
"""Rotated-store drill with real binaries: old-reader safety and backup/restore (V6 close-out C1.10).

Gates 1 and 2 of the SP20-D4 rollover close-out, on the Linux verification container, with the REAL
binaries and the shipped CLI, as the unprivileged container user:

  cur       the candidate built from <commit> with production defaults (rollover on, rotation at the
            65,536-entry / 64 MiB caps)
  producer  the SAME commit with ONE test-only patch, the rotation threshold lowered to 3 entries, so a
            real daemon rotates a store many times in a handful of hook deliveries. It only PRODUCES the
            rotated fixture; every verdict below is taken with `cur` or the old binary. The patch is
            recorded in the evidence.
  old       /work/compat-old-linux-301a8e9, the reference build of 301a8e9 (v0.2.0-640-g301a8e9): it has
            the delivery journal and the dual v1/v2 seal reader but predates segments — the build class
            the old-reader barrier exists for. Its hash, version and source archive are verified.

Flow (every command's argv/exit/stdout/stderr and every file fingerprint is saved):

  A. producer daemon: hook deliveries into projectA until the journal has rotated several times.
  B. cur, read-only: fsck and fsck --seal-check certify the rotated store; recall finds the content.
  C. cur backup create/verify, restore into a fresh destination; on the restored copy: fsck --seal-check
     certifies, the archived history is byte-identical, a restarted cur daemon captures new deliveries
     whose arrivals continue densely (a new session at 1, the history's session past its last), and the
     restored store still certifies afterwards.
  D. old binary on a disposable COPY of projectA: read-only fsck, admin delivery-seal --check, then a
     hook session (spawns the OLD daemon). Every delivery-state file (legacy journals and their frozen
     seals, every segment, the authority, the generation store) must be byte-identical afterwards: no
     identity re-minted, no arrival reassigned, no evidence truncated. Then cur reopens the copy and
     continues the history's arrivals exactly where the producer left them.

Usage:  python plans/sdd/V6-closeout/rollover/rollover-drill.py --commit <ref> [--run-id ID]
        (<ref> is a ref git can bundle, such as HEAD; the commit it names is recorded)
Safety: touches only /work/cx-rollover-drill-<run-id>; signals only its own daemons (pid read from its own
project roots); never reuses an existing /work directory.
"""
from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import pathlib
import subprocess
import sys
import time

CONTAINER = "qompack-v6-linux-verification"
USER = "qompack-test"
OLD_BIN = "/work/compat-old-linux-301a8e9"
OLD_TAR = "/work/compat-old-301a8e9.tar"
EXPECT_OLD_SHA = "8cb4c0bcc3b98626a625cf0cf53898bab4316effd3d41820f85c3f38fa096bd0"
EXPECT_OLD_VER = "v0.2.0-640-g301a8e9"
PRODUCER_THRESHOLD = 3
UP_TIMEOUT = 90.0
INDEX_TIMEOUT = 120.0
STOP_TIMEOUT = 60.0
POLL = 0.5

REPO = pathlib.Path(__file__).resolve().parents[4]
EVID = pathlib.Path(__file__).resolve().parent / "drill"


def now_iso():
    return datetime.datetime.now().astimezone().isoformat()


def sha_bytes(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def dexec(argv, *, stdin: bytes | None = None, env: dict | None = None, user: str | None = USER,
          timeout: float = 600.0):
    """Run one command in the container (as the unprivileged user unless user is None)."""
    cmd = ["docker", "exec"]
    if stdin is not None:
        cmd.append("-i")
    if user:
        cmd += ["-u", user]
    for k, v in (env or {}).items():
        cmd += ["-e", f"{k}={v}"]
    cmd += [CONTAINER, *argv]
    p = subprocess.run(cmd, input=stdin, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    return p.returncode, p.stdout, p.stderr


def csh(script: str, *, user: str | None = USER, timeout: float = 600.0):
    return dexec(["sh", "-c", script], user=user, timeout=timeout)


class Journal:
    def __init__(self, run_id: str):
        self.run_id = run_id
        self.dir = EVID / "runs" / run_id
        if self.dir.exists():
            raise SystemExit(f"refusing to overwrite prior evidence: {self.dir}")
        (self.dir / "steps").mkdir(parents=True)
        self.steps: list[dict] = []
        self.verdicts: dict[str, dict] = {}
        self.meta: dict = {"run_id": run_id, "started_at": now_iso()}

    def step(self, name, argv, code, out: bytes, err: bytes, note="", saved=None):
        rec = {"seq": len(self.steps) + 1, "name": name, "argv": argv, "exit": code,
               "stdout_bytes": len(out), "stderr_bytes": len(err), "note": note, "at": now_iso()}
        base = self.dir / "steps" / f"{rec['seq']:02d}-{name}"
        base.with_suffix(".stdout").write_bytes(out)
        if err:
            base.with_suffix(".stderr").write_bytes(err)
        if saved:
            (self.dir / saved).write_bytes(out)
            rec["saved"] = saved
        self.steps.append(rec)
        self.flush()
        return rec

    def save_json(self, name, obj):
        (self.dir / name).write_text(json.dumps(obj, indent=2, sort_keys=True), encoding="utf-8")

    def verdict(self, key, status, detail, evidence):
        self.verdicts[key] = {"status": status, "detail": detail, "evidence": evidence}
        self.flush()

    def flush(self):
        (self.dir / "journal.json").write_text(json.dumps(
            {"meta": self.meta, "steps": self.steps, "verdicts": self.verdicts}, indent=2), encoding="utf-8")


# ---- container helpers ------------------------------------------------------------------------------
def tree_files(root: str, sub: str = ".qompack") -> dict:
    code, out, _ = csh(f"cd {root}/{sub} 2>/dev/null && find . -type f -exec sha256sum {{}} + 2>/dev/null || true")
    fps = {}
    for line in out.decode("utf-8", "replace").splitlines():
        digest, _, rel = line.strip().partition("  ")
        if rel:
            fps[rel[2:] if rel.startswith("./") else rel] = digest
    return fps


def delivery_state(fps: dict) -> dict:
    """The delivery-identity files: journals, seals, segments, authority, generation store."""
    return {k: v for k, v in fps.items()
            if k.startswith("state/delivery-") or k.startswith("state/delivery_")}


def delta(after: dict, before: dict) -> dict:
    return {"added": sorted(k for k in after if k not in before),
            "removed": sorted(k for k in before if k not in after),
            "changed": sorted(k for k in after if k in before and after[k] != before[k])}


def lease_lines(root: str) -> list[dict]:
    """Every lease line in every segment, with the segment it is in."""
    code, out, _ = csh(
        f"cd {root}/.qompack/state && for f in delivery-leases.jsonl delivery-segments/*/delivery-leases.jsonl; do "
        f"[ -f \"$f\" ] && sed \"s|^|$f\\t|\" \"$f\"; done; true")
    rows = []
    for line in out.decode("utf-8", "replace").splitlines():
        path, _, js = line.partition("\t")
        try:
            rec = json.loads(js)
        except Exception:
            continue
        rec["_file"] = path
        rows.append(rec)
    return rows


def last_arrival(rows: list[dict], session: str) -> int:
    return max((r["arrival"] for r in rows if r.get("session") == session), default=0)


def daemon_pid(root: str):
    code, out, _ = csh(f"cat {root}/.qompack/run/daemon.lock 2>/dev/null || true")
    try:
        return int(json.loads(out.decode().strip())["pid"])
    except Exception:
        return None


def is_alive(pid: int) -> bool:
    code, out, _ = csh(f"kill -0 {pid} 2>/dev/null && echo A || echo D")
    return out.decode().strip().endswith("A")


def wait_lock(root: str, timeout: float):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        pid = daemon_pid(root)
        if pid and is_alive(pid):
            return pid
        time.sleep(POLL)
    return daemon_pid(root)


def wait_indexed(root: str, ids: list[str], timeout: float) -> bool:
    tu = f"{root}/.qompack/index/tool_use.jsonl"
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        code, out, _ = csh(f"cat {tu} 2>/dev/null || true")
        text = out.decode("utf-8", "replace")
        if all(i in text for i in ids):
            return True
        time.sleep(POLL)
    return False


def stop_daemon(drill: str, root: str, jr: Journal, tag: str):
    assert root.startswith(drill + "/"), f"refusing to stop a daemon outside the drill root: {root}"
    pid = daemon_pid(root)
    if pid is None:
        jr.step(f"stop-{tag}", ["(no daemon lock)"], 0, b"", b"", note="nothing to stop")
        return
    csh(f"kill -TERM {pid} 2>/dev/null || true")
    deadline = time.monotonic() + STOP_TIMEOUT
    gone = False
    while time.monotonic() < deadline:
        if daemon_pid(root) is None or not is_alive(pid):
            gone = True
            break
        time.sleep(POLL)
    note = "SIGTERM; lock cleared" if gone else "SIGTERM did not clear the lock; SIGKILL (own daemon)"
    if not gone:
        csh(f"kill -KILL {pid} 2>/dev/null || true")
        time.sleep(1.0)
    jr.step(f"stop-{tag}", ["kill", "-TERM", str(pid)], 0, note.encode(), b"", note=note)


def env_for(root: str, home: str) -> dict:
    return {"QOMPACK_PROJECT_ROOT": root, "HOME": home, "USERPROFILE": home}


def session_start(root, sess):
    return json.dumps({"hook_event_name": "SessionStart", "session_id": sess, "cwd": root, "source": "startup"}).encode()


def session_end(root, sess):
    return json.dumps({"hook_event_name": "SessionEnd", "session_id": sess, "cwd": root}).encode()


def tool_event(root, sess, use_id, path, content):
    return json.dumps({"hook_event_name": "PostToolUse", "session_id": sess, "cwd": root, "tool_name": "Read",
                       "tool_use_id": use_id, "tool_input": {"file_path": path},
                       "tool_response": {"content": content}}).encode()


def content_for(marker: str) -> str:
    return "\n".join([f"// {marker}"] + [f"export const line_{i:03d} = \"{marker} {i}\";" for i in range(60)]) + "\n"


def hook_session(jr, drill, binary, tag, root, home, sess, events):
    """One hook session: session-start (spawns the daemon), PostToolUse deliveries, SessionEnd, stop."""
    env = env_for(root, home)
    c, o, e = dexec([binary, "session-start"], stdin=session_start(root, sess), env=env)
    jr.step(f"{tag}-session-start", [binary, "session-start"], c, o, e)
    pid = wait_lock(root, UP_TIMEOUT)
    jr.step(f"{tag}-daemon-up", ["(wait lock)"], 0 if pid else 1, str(pid).encode(), b"", note=f"pid={pid}")
    for use_id, marker in events:
        c, o, e = dexec([binary, "observe", "tool"],
                        stdin=tool_event(root, sess, use_id, f"src/{use_id}.ts", content_for(marker)), env=env)
        jr.step(f"{tag}-observe-{use_id}", [binary, "observe", "tool"], c, o, e)
    indexed = wait_indexed(root, [u for u, _ in events], INDEX_TIMEOUT)
    jr.step(f"{tag}-indexed", ["(wait index)"], 0 if indexed else 1, b"indexed" if indexed else b"NOT INDEXED", b"")
    c, o, e = dexec([binary, "flush"], stdin=session_end(root, sess), env=env)
    jr.step(f"{tag}-flush", [binary, "flush"], c, o, e)
    # The daemon is stopped only once it has worked through what the flush released (or the wait
    # times out): a SIGTERM in the middle of that backlog cuts in-flight captures short, and they are
    # then captured by the next daemon's drain rather than live.
    after = wait_indexed(root, [u for u, _ in events], INDEX_TIMEOUT)
    jr.step(f"{tag}-indexed-after-flush", ["(wait index)"], 0 if after else 1,
            b"indexed" if after else b"NOT INDEXED", b"")
    stop_daemon(drill, root, jr, tag)
    return indexed or after


def fsck(jr, binary, tag, root, home, seal_check):
    argv = [binary, "fsck", "--project", root, "--json"] + (["--seal-check"] if seal_check else [])
    c, o, e = dexec(argv, env=env_for(root, home))
    jr.step(f"{tag}-fsck{'-seal' if seal_check else ''}", argv, c, o, e, saved=f"{tag}-fsck{'-seal' if seal_check else ''}.json")
    try:
        doc = json.loads(o.decode("utf-8", "replace"))
    except Exception:
        doc = {}
    rows = {r.get("id"): r for r in doc.get("checks", [])} if isinstance(doc, dict) else {}
    return c, rows.get("delivery", {})


def failing_checks(doc: dict) -> dict:
    """id -> detail of every check an fsck report marks not ok."""
    return {r.get("id"): r.get("detail") for r in (doc or {}).get("checks", []) if not r.get("ok")}


def fsck_failures(jr, binary, tag, root, home) -> tuple[int, dict]:
    """A read-only fsck of root whose failing checks are returned for comparison."""
    argv = [binary, "fsck", "--project", root, "--json"]
    c, o, e = dexec(argv, env=env_for(root, home))
    jr.step(f"{tag}-fsck", argv, c, o, e, saved=f"{tag}-fsck.json")
    try:
        doc = json.loads(o.decode("utf-8", "replace"))
    except Exception:
        doc = {}
    return c, failing_checks(doc)


def recall(jr, drill, binary, tag, root, home, marker):
    """recall spawns a daemon to answer; it is stopped again before the drill goes on."""
    argv = [binary, "recall", marker, "--json"]
    c, o, e = dexec(argv, env=env_for(root, home))
    jr.step(f"{tag}-recall", argv, c, o, e)
    stop_daemon(drill, root, jr, f"{tag}-recall")
    return c == 0 and marker.encode() in o


def dense(rows: list[dict], session: str) -> bool:
    """A session's arrivals are exactly 1..n, each once, and every delivery nonce is leased once."""
    arrivals = sorted(r["arrival"] for r in rows if r.get("session") == session)
    nonces = [r["delivery"] for r in rows]
    return arrivals == list(range(1, len(arrivals) + 1)) and len(nonces) == len(set(nonces))


def indexed_ids(root: str, ids: list[str]) -> list[str]:
    code, out, _ = csh(f"cat {root}/.qompack/index/tool_use.jsonl 2>/dev/null || true")
    text = out.decode("utf-8", "replace")
    return [i for i in ids if i in text]


# ---- the drill --------------------------------------------------------------------------------------
def build(jr, drill, commit):
    """Bundle <commit>, build cur and the threshold-patched producer in the drill workspace."""
    bundle = REPO / "plans" / "sdd" / "V6-closeout" / "rollover" / "drill" / f"{jr.run_id}.bundle"
    subprocess.run(["git", "-C", str(REPO), "bundle", "create", str(bundle), commit], check=True,
                   stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    sha = subprocess.run(["git", "-C", str(REPO), "rev-parse", commit], check=True, stdout=subprocess.PIPE,
                         text=True).stdout.strip()
    subprocess.run(["docker", "exec", CONTAINER, "mkdir", drill], check=True)
    subprocess.run(["docker", "cp", str(bundle), f"{CONTAINER}:{drill}/src.bundle"], check=True)
    bundle.unlink()
    script = f"""set -eu
cd {drill}
git clone -q src.bundle src-cur
git -C src-cur checkout -q {sha}
git clone -q src.bundle src-producer
git -C src-producer checkout -q {sha}
cd {drill}/src-producer
grep -q 'deliveryRolloverEntries = deliveryLeaseMaxEntries' internal/daemon/delivery_lease.go
sed -i 's/deliveryRolloverEntries = deliveryLeaseMaxEntries/deliveryRolloverEntries = {PRODUCER_THRESHOLD} \\/\\/ DRILL PRODUCER ONLY/' internal/daemon/delivery_lease.go
git diff > {drill}/producer.patch
export GOFLAGS=-mod=readonly GOPROXY=off GOTOOLCHAIN=local GOMAXPROCS=4 CGO_ENABLED=0
cd {drill}/src-cur && go build -trimpath -buildvcs=false -ldflags "-X github.com/qompack/qompack/internal/core.Version=drill-cur-{sha[:12]}" -o {drill}/cur ./cmd/qompack
cd {drill}/src-producer && go build -trimpath -buildvcs=false -ldflags "-X github.com/qompack/qompack/internal/core.Version=drill-producer-{sha[:12]}" -o {drill}/producer ./cmd/qompack
sha256sum {drill}/cur {drill}/producer {OLD_BIN}
"""
    c, o, e = csh(script, user=None, timeout=1800)
    jr.step("build", ["(build cur + producer)"], c, o, e)
    if c != 0:
        raise SystemExit("build failed; see steps/01-build.*")
    _, patch, _ = csh(f"cat {drill}/producer.patch", user=None)
    (jr.dir / "producer.patch").write_bytes(patch)
    csh(f"chown -R {USER}:{USER} {drill} && chmod a+rx {drill}/cur {drill}/producer", user=None)
    return sha


def identity(jr, drill, sha):
    ident = {"container": CONTAINER, "commit": sha}
    _, out, _ = csh("uname -a; id; go version", user=None)
    ident["platform"] = out.decode("utf-8", "replace")
    _, out, _ = csh(f"sha256sum {OLD_BIN} | cut -d' ' -f1", user=None)
    ident["old_sha256"] = out.decode().strip()
    ident["old_sha_match"] = ident["old_sha256"] == EXPECT_OLD_SHA
    _, out, _ = dexec([OLD_BIN, "version"])
    ident["old_version"] = out.decode().strip()
    ident["old_version_match"] = ident["old_version"] == EXPECT_OLD_VER
    _, out, _ = csh(f"tar -tf {OLD_TAR} | grep -c 'internal/daemon/delivery_segment' || true", user=None)
    ident["old_source_has_segments"] = out.decode().strip() not in ("", "0")
    _, out, _ = csh(f"tar -tf {OLD_TAR} | grep -c 'internal/daemon/delivery_seal.go' || true", user=None)
    ident["old_source_has_v2_seal_reader"] = out.decode().strip() not in ("", "0")
    for b in ("cur", "producer"):
        _, out, _ = dexec([f"{drill}/{b}", "version"])
        ident[f"{b}_version"] = out.decode().strip()
        _, out, _ = csh(f"sha256sum {drill}/{b} | cut -d' ' -f1")
        ident[f"{b}_sha256"] = out.decode().strip()
    jr.save_json("identity.json", ident)
    jr.meta["identity"] = ident
    if not (ident["old_sha_match"] and ident["old_version_match"]) or ident["old_source_has_segments"]:
        raise SystemExit("old reference binary is not the expected pre-segment 301a8e9 build; see identity.json")
    return ident


def run(jr: Journal, drill: str, commit: str):
    sha = build(jr, drill, commit)
    identity(jr, drill, sha)
    CUR, PRODUCER = f"{drill}/cur", f"{drill}/producer"
    A, HA = f"{drill}/projectA", f"{drill}/home"
    DEST = f"{drill}/restored"
    OLDC = f"{drill}/oldcopy"
    csh(f"mkdir -p {A}/.git {HA} {DEST}/.git")

    # ---- A0. control: the same hook flow with cur, which never rotates at this size. A capture stall
    # seen in BOTH projects is not the rollover's (the base's live-ingest ordering defect, close-out C1.1,
    # stalls captures on this candidate independently of rotation).
    CTRL = f"{drill}/control"
    csh(f"mkdir -p {CTRL}/.git")
    history = "sess-drill-history"
    events = [(f"toolu_drill_{i:02d}", f"QOMPACK-DRILL-HISTORY-{i:02d}-5c1e") for i in range(10)]
    hook_session(jr, drill, CUR, "control", CTRL, HA, history, events)
    control_indexed = indexed_ids(CTRL, [u for u, _ in events])
    rows_ctrl = lease_lines(CTRL)
    control_fsck_exit, control_failures = fsck_failures(jr, CUR, "control", CTRL, HA)

    # ---- A. producer: a real daemon rotates the store -------------------------------------------------
    produced = hook_session(jr, drill, PRODUCER, "producer", A, HA, history, events)
    producer_indexed = indexed_ids(A, [u for u, _ in events])
    rows_a = lease_lines(A)
    jr.save_json("producer-leases.json", rows_a)
    fp_a = tree_files(A)
    ds_a = delivery_state(fp_a)
    jr.save_json("A-delivery-state.json", ds_a)
    _, head, _ = csh(f"cat {A}/.qompack/state/delivery-journal.json")
    _, frozen, _ = csh(f"cat {A}/.qompack/state/delivery-lease-position.json")
    (jr.dir / "A-authority-head.json").write_bytes(head)
    (jr.dir / "A-legacy-lease-seal.json").write_bytes(frozen)
    try:
        active = json.loads(head.decode())["active"]
    except Exception:
        active = -1
    segments = sorted({r["_file"] for r in rows_a})
    history_last = last_arrival(rows_a, history)
    jr.verdict("fixture_rotated",
               "PROVEN" if active >= 3 and history_last >= len(events) and dense(rows_a, history)
               and b'"format":"qompack.delivery.frozen-seal.v1"' in frozen else "FAILED",
               "a real (threshold-patched) daemon leased every delivery with dense arrivals across several "
               "rotations, and froze the archived legacy segment's seals",
               {"active_segment": active, "lease_files": segments, "history_leases": history_last,
                "dense": dense(rows_a, history), "frozen_legacy_seal": frozen.decode("utf-8", "replace")})
    jr.verdict("capture_through_rotation",
               "PROVEN" if len(producer_indexed) >= len(control_indexed) else "FAILED",
               "the rotating producer indexed at least what the non-rotating control indexed from the same "
               "hook flow (both are recorded; a shortfall in both is the base's, not the rollover's)",
               {"producer_indexed": producer_indexed, "control_indexed": control_indexed,
                "producer_wait_all_indexed": produced, "control_leases": len(rows_ctrl),
                "control_dense": dense(rows_ctrl, history)})

    # ---- B. cur, read-only -----------------------------------------------------------------------------
    c_ro, row_ro = fsck(jr, CUR, "cur-A", A, HA, False)
    c_sc, row_sc = fsck(jr, CUR, "cur-A", A, HA, True)
    fp_a_after_fsck = delivery_state(tree_files(A))
    found_a = recall(jr, drill, CUR, "cur-A", A, HA, events[0][1])
    fp_a_after_recall = delivery_state(tree_files(A))
    fsck_delta = delta(fp_a_after_fsck, ds_a)
    jr.verdict("current_certifies_rotated_store",
               "PROVEN" if (row_sc.get("ok") and "seal check passed" in json.dumps(row_sc) and found_a
                            and not any(fsck_delta.values())) else "FAILED",
               "cur fsck (read-only and --seal-check) certifies the rotated store and changes no delivery-state "
               "file; recall finds the history",
               {"fsck_exit": c_ro, "fsck_seal_exit": c_sc, "delivery_row": row_ro, "delivery_row_seal": row_sc,
                "recall_found": found_a, "fsck_changed_delivery_state": fsck_delta,
                "recall_daemon_changed_delivery_state": delta(fp_a_after_recall, fp_a_after_fsck)})

    # ---- C. backup / verify / restore / restart --------------------------------------------------------
    envA = env_for(A, HA)
    c, o, e = dexec([CUR, "backup", "create", "--project", A, "--id", "rotated", "--json"], env=envA)
    jr.step("cur-backup-create", ["backup", "create", "--id", "rotated"], c, o, e, saved="backup-create.json")
    create_ok = c == 0
    c, o, e = dexec([CUR, "backup", "verify", "--project", A, "--id", "rotated", "--json"], env=envA)
    jr.step("cur-backup-verify", ["backup", "verify", "--id", "rotated"], c, o, e, saved="backup-verify.json")
    verify_ok = c == 0
    manifest_names = []
    try:
        manifest_names = [f["name"] for f in json.loads(o.decode())["manifest"]["files"]]
    except Exception:
        pass
    c, o, e = dexec([CUR, "backup", "restore", "--project", A, "--id", "rotated", "--destination", DEST, "--json"], env=envA)
    jr.step("cur-backup-restore", ["backup", "restore", "--id", "rotated", "--destination", "restored"], c, o, e,
            saved="backup-restore.json")
    restore_ok = c == 0
    try:
        restore_doc = json.loads(o.decode("utf-8", "replace"))
    except Exception:
        restore_doc = {}
    restore_failures = failing_checks((restore_doc or {}).get("integrity") or {})
    restore_opened = bool(((restore_doc or {}).get("restore") or {}).get("OpenedOK"))
    # Accepted only when the restore itself opened the copy, the delivery row passed, and every failing
    # integrity row fails the same way on the never-rotated control.
    restore_only_base = (not restore_ok and restore_opened and "delivery" not in restore_failures
                         and bool(restore_failures)
                         and all(control_failures.get(k) == v for k, v in restore_failures.items()))
    ds_dest = delivery_state(tree_files(DEST))
    history_identical = ds_dest == delivery_state(tree_files(A))
    covered = {k: (f"state/{k.split('state/', 1)[-1]}" in manifest_names) for k in ds_a}
    c_d1, row_d1 = fsck(jr, CUR, "cur-restored-before-restart", DEST, HA, True)
    new_sess = "sess-drill-after-restore"
    restart_events_new = [("toolu_drill_restored_new", "QOMPACK-DRILL-RESTORED-NEW-7a2f")]
    restart_events_hist = [("toolu_drill_restored_hist", "QOMPACK-DRILL-RESTORED-HIST-3b9d")]
    ok1 = hook_session(jr, drill, CUR, "cur-restored-new", DEST, HA, new_sess, restart_events_new)
    ok2 = hook_session(jr, drill, CUR, "cur-restored-hist", DEST, HA, history, restart_events_hist)
    rows_dest = lease_lines(DEST)
    jr.save_json("restored-leases.json", rows_dest)
    c_d2, row_d2 = fsck(jr, CUR, "cur-restored-after-restart", DEST, HA, True)
    found_old = recall(jr, drill, CUR, "cur-restored", DEST, HA, events[0][1])
    active_dir = f"state/delivery-segments/{active:020d}/" if active >= 1 else "state/delivery-"
    ds_dest_after = delivery_state(tree_files(DEST))
    archived_unchanged = all(ds_dest_after.get(k) == v for k, v in ds_a.items()
                             if not k.startswith(active_dir)
                             and not k.startswith("state/delivery-generations/manifest")
                             and k not in ("state/delivery-journal.json", "state/delivery-journal-log.jsonl"))
    jr.verdict("backup_restore_segmented",
               "PROVEN" if (create_ok and verify_ok and (restore_ok or restore_only_base) and history_identical
                            and all(covered.values())
                            and row_d1.get("ok") and row_d2.get("ok") and found_old
                            and last_arrival(rows_dest, new_sess) >= 1 and dense(rows_dest, new_sess)
                            and last_arrival(rows_dest, history) > history_last and dense(rows_dest, history))
               else "FAILED",
               "backup create/verify/restore of the rotated store; the restored copy certifies, restarts, and "
               "continues identities densely; the archived history is preserved byte for byte",
               {"create": create_ok, "verify": verify_ok, "restore": restore_ok, "restore_opened": restore_opened,
                "restore_integrity_failures": restore_failures, "control_fsck_exit": control_fsck_exit,
                "control_failures": control_failures,
                "restore_failures_are_the_controls": restore_only_base,
                "restored_delivery_state_identical": history_identical,
                "manifest_covers_every_delivery_file": all(covered.values()),
                "uncovered": [k for k, v in covered.items() if not v],
                "fsck_seal_before_restart": row_d1, "fsck_seal_after_restart": row_d2,
                "new_session_arrival": last_arrival(rows_dest, new_sess),
                "history_arrival_after_restart": last_arrival(rows_dest, history),
                "history_arrival_before": history_last, "recall_history_after_restore": found_old,
                "restart_sessions_indexed": [ok1, ok2], "dense_after_restart": [dense(rows_dest, new_sess),
                                                                              dense(rows_dest, history)],
                "earlier_segments_untouched_by_restart": archived_unchanged})

    # ---- D. the old binary on a disposable copy --------------------------------------------------------
    c, o, e = csh(f"cp -a {A} {OLDC}")
    jr.step("copy-for-old", ["cp", "-a", "projectA", "oldcopy"], c, o, e)
    fp_old_pre = tree_files(OLDC)
    ds_old_pre = delivery_state(fp_old_pre)
    c_of, row_of = fsck(jr, OLD_BIN, "old", OLDC, HA, False)
    c, o, e = dexec([OLD_BIN, "admin", "delivery-seal", "--check"], env=env_for(OLDC, HA))
    jr.step("old-delivery-seal-check", [OLD_BIN, "admin", "delivery-seal", "--check"], c, o, e)
    old_seal_check_exit = c
    old_events = [("toolu_drill_old", "QOMPACK-DRILL-OLD-BINARY-9e44")]
    old_indexed = hook_session(jr, drill, OLD_BIN, "old", OLDC, HA, history, old_events)
    fp_old_post = tree_files(OLDC)
    ds_old_post = delivery_state(fp_old_post)
    ds_delta = delta(ds_old_post, ds_old_pre)
    whole_delta = delta(fp_old_post, fp_old_pre)
    _, daemon_log, _ = csh(f"ls {OLDC}/.qompack/logs 2>/dev/null && tail -n 80 {OLDC}/.qompack/logs/* 2>/dev/null || true")
    (jr.dir / "old-daemon-log-tail.txt").write_bytes(daemon_log)
    jr.verdict("old_reader_refuses_or_degrades_safely",
               "PROVEN" if not (ds_delta["added"] or ds_delta["removed"] or ds_delta["changed"]) else "FAILED",
               "the pre-segment 301a8e9 build, run against a rotated store, changed no delivery-state file: no "
               "identity re-minted, no arrival reassigned, no evidence truncated",
               {"old_fsck_exit": c_of, "old_fsck_delivery_row": row_of,
                "old_delivery_seal_check_exit": old_seal_check_exit,
                "old_hook_session_indexed": old_indexed, "delivery_state_delta": ds_delta,
                "whole_store_delta": whole_delta})
    # cur reopens the old-touched copy and continues exactly where the producer left the history.
    c_c1, row_c1 = fsck(jr, CUR, "cur-oldcopy", OLDC, HA, True)
    cont = [("toolu_drill_after_old", "QOMPACK-DRILL-AFTER-OLD-1d8c")]
    cont_ok = hook_session(jr, drill, CUR, "cur-oldcopy", OLDC, HA, history, cont)
    rows_old = lease_lines(OLDC)
    jr.save_json("oldcopy-leases.json", rows_old)
    new_rows = [r for r in rows_old if r["delivery"] not in {x["delivery"] for x in rows_a}]
    jr.verdict("current_continues_after_old_binary",
               "PROVEN" if (row_c1.get("ok") and dense(rows_old, history)
                            and min((r["arrival"] for r in new_rows if r["session"] == history), default=0) == history_last + 1)
               else "FAILED",
               "after the old build ran, cur certifies the copy and resumes the history's arrivals at the one the "
               "producer left next (the old build consumed none), densely; a delivery the old build captured in "
               "its identity-free degraded mode is not leased again (see leased_after_old)",
               {"fsck_seal": row_c1, "history_arrival_before": history_last,
                "history_arrival_after": last_arrival(rows_old, history), "dense": dense(rows_old, history),
                "leased_after_old": [(r["session"], r["arrival"]) for r in new_rows], "cur_session_indexed": cont_ok})

    for r in (CTRL, A, DEST, OLDC):
        if daemon_pid(r) is not None:
            stop_daemon(drill, r, jr, "final-" + r.rsplit("/", 1)[-1])
    jr.meta["finished_at"] = now_iso()
    jr.meta["drill_root_in_container"] = drill
    jr.flush()
    return jr.verdicts


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--commit", required=True)
    ap.add_argument("--run-id", default=datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
    args = ap.parse_args()
    p = subprocess.run(["docker", "ps", "--filter", f"name=^{CONTAINER}$", "--format", "{{.Names}}"],
                       stdout=subprocess.PIPE, text=True)
    if CONTAINER not in p.stdout:
        raise SystemExit(f"container {CONTAINER} is not running")
    drill = f"/work/cx-rollover-drill-{args.run_id}"
    code, out, _ = csh(f"[ -e {drill} ] && echo PRESENT || echo ABSENT", user=None)
    if out.decode().strip().endswith("PRESENT"):
        raise SystemExit(f"drill workspace {drill} already exists — refusing to reuse it")
    jr = Journal(args.run_id)
    try:
        verdicts = run(jr, drill, args.commit)
        print(json.dumps({"run_id": jr.run_id, "evidence": str(jr.dir),
                          "verdicts": {k: v["status"] for k, v in verdicts.items()}}, indent=2))
        if any(v["status"] != "PROVEN" for v in verdicts.values()):
            sys.exit(1)
    except SystemExit:
        raise
    except Exception as ex:
        jr.meta["aborted_at"] = now_iso()
        jr.meta["error"] = repr(ex)
        jr.flush()
        print(f"DRILL ABORTED (evidence preserved at {jr.dir}): {ex!r}", file=sys.stderr)
        raise


if __name__ == "__main__":
    main()
