"""Live-lane session driver: one `claude -p` streaming-input session, one user message per turn.

usage: python live_driver.py <config.json>
       python live_driver.py scan-staged <repo> <secrets.json>   exit 1 if a staged file holds one

Derived from plans/sdd/V6-closeout/eval/runs/smoke_driver.py (same turn discipline: each message
is sent only after the previous turn's `result` line). Additions for the Phase 4 lane:

- every stdout line's arrival time is kept, and hooks.json pairs each hook_started with its
  hook_response by hook_id: the host's stream carries no duration field, so the stream arrival
  delta is the only host-side latency there is. It is APPROXIMATE and can under-read: the host
  may batch output (the SessionStart pair arrives before the init event, after the hook ran).
  A pair whose response arrived before init, or within ARRIVAL_FLOOR_MS of its start, is marked
  "measured": false and must be reported as not measured, never as ~0 ms. A started hook with no
  response (cancelled, timed out) is listed with latency_ms null.
- a message may be {"content": "...", "before": [argv...]}: argv runs (no shell, 120 s cap) before
  the message is sent, its output and exit code go to meta.json. Degraded-path steps use it.
- after every turn, when <project>/.qompack/run/daemon.lock names a live qompack.exe, the daemon's
  working set, peak working set and CPU seconds are sampled (PowerShell Get-Process), and the
  .qompack/ byte total is recorded (C5.6).
- every output has cfg["scrub"] (the scratch prefix) replaced by <scratch> in each spelling it
  can take: the / form, the \\ form, and that form JSON-escaped once and twice (a Windows path in
  the host's stream, and in a hook output the stream carries as a JSON string; D50). Scrubbing
  changes byte lengths, so measure response sizes from probe outputs, not from stream.jsonl.
  Apart from that, stream.jsonl and stderr.txt keep the host's bytes, EXCEPT planted secrets:
  cfg["secrets_file"] names a JSON list of literal strings kept outside every git work tree
  (the driver refuses one inside a work tree). Every output the driver writes has the n-th
  secret (1-based; literal, JSON-escaped and doubly JSON-escaped forms) replaced by
  @@SEC_PLANTED_n@@, so a credential-shaped string never reaches an evidence folder.
- scan-staged is the commit gate for evidence the driver did not write: it runs git grep over the
  index for every form of every secret and prints only the names of the files that hold one.

Config keys: bin (default "claude"), args, cwd, out, env, messages, timeout_s (default 600),
project (default cwd), scrub (optional), secrets_file (optional).
"""
import json
import os
import subprocess
import sys
import threading
import time

ARRIVAL_FLOOR_MS = 5


def now_ms():
    return int(time.time() * 1000)


def store_bytes(project):
    total = 0
    for root, _, files in os.walk(os.path.join(project, ".qompack")):
        for f in files:
            try:
                total += os.path.getsize(os.path.join(root, f))
            except OSError:
                pass
    return total


def sample_daemon(project):
    lock = os.path.join(project, ".qompack", "run", "daemon.lock")
    try:
        with open(lock, encoding="utf-8") as f:
            pid = int(json.load(f).get("pid") or 0)
    except (OSError, ValueError):
        return {"daemon": "no lock"}
    if not pid:
        return {"daemon": "lock without pid"}
    r = subprocess.run(["tasklist", "/FI", f"PID eq {pid}", "/FO", "CSV", "/NH"],
                       capture_output=True, text=True)
    image = r.stdout.strip().split(",")[0].strip('"') if r.stdout.strip().startswith('"') else ""
    if image.lower() != "qompack.exe":
        return {"daemon": "not running as qompack.exe", "pid": pid, "image": image}
    ps = ("Get-Process -Id %d | Select-Object WorkingSet64,PeakWorkingSet64,CPU,StartTime "
          "| ConvertTo-Json -Compress" % pid)
    r = subprocess.run(["powershell", "-NoProfile", "-Command", ps], capture_output=True, text=True)
    try:
        return {"daemon": "sampled", "pid": pid, **json.loads(r.stdout)}
    except ValueError:
        return {"daemon": "sample failed", "pid": pid, "stderr": r.stderr[-300:]}


def pair_hooks(timed):
    started, out, seen_init = {}, [], False
    for t, obj in timed:
        if obj.get("type") != "system":
            continue
        if obj.get("subtype") == "init":
            seen_init = True
        hid = obj.get("hook_id")
        if obj.get("subtype") == "hook_started":
            started[hid] = (t, obj)
        elif obj.get("subtype") == "hook_response":
            t0, s = started.pop(hid, (None, {}))
            lat = None if t0 is None else t - t0
            out.append({"hook_id": hid, "hook_name": obj.get("hook_name"),
                        "hook_event": obj.get("hook_event"), "latency_ms": lat,
                        "before_init": not seen_init,
                        "measured": lat is not None and seen_init and lat >= ARRIVAL_FLOOR_MS,
                        "outcome": obj.get("outcome"), "exit_code": obj.get("exit_code")})
    for hid, (t0, s) in started.items():
        out.append({"hook_id": hid, "hook_name": s.get("hook_name"),
                    "hook_event": s.get("hook_event"), "latency_ms": None,
                    "before_init": not seen_init, "measured": False,
                    "outcome": "no hook_response in stream", "exit_code": None})
    return out


def inside_work_tree(path):
    d = os.path.dirname(os.path.abspath(path))
    r = subprocess.run(["git", "-C", d, "rev-parse", "--is-inside-work-tree"],
                       capture_output=True, text=True)
    return r.stdout.strip() == "true"


def secret_forms(path):
    """[(form, replacement)] for every planted secret, longest form first."""
    with open(path, encoding="utf-8") as f:
        secrets = json.load(f)
    pairs = []
    for n, sec in enumerate(secrets, 1):
        if not isinstance(sec, str) or len(sec) < 8:
            raise SystemExit("secrets_file: every secret must be a string of 8+ characters")
        e1 = json.dumps(sec)[1:-1]
        for form in {sec, e1, json.dumps(e1)[1:-1]}:
            pairs.append((form, "@@SEC_PLANTED_%d@@" % n))
    return sorted(pairs, key=lambda p: len(p[0]), reverse=True)


def scan_staged(repo, secrets_path):
    args = ["git", "-C", repo, "grep", "--cached", "-l", "-F"]
    for form in sorted({f for f, _ in secret_forms(secrets_path)}):
        args += ["-e", form]
    r = subprocess.run(args, capture_output=True, text=True)
    if r.returncode == 0:
        print("PLANTED SECRET STAGED in:", *r.stdout.split(), sep="\n  ")
        print("unstage those files (git restore --staged <path>), scrub them, then re-add")
        return 1
    if r.returncode == 1:
        print("no planted secret in the index")
        return 0
    print("git grep failed:", r.stderr.strip())
    return 2


def scrub_forms(prefix):
    """Every spelling of the scratch prefix an output can carry, longest first: the / form, the \\
    form, and the \\ form JSON-escaped once (a Windows path in stream.jsonl) and twice (a path in
    a hook output the stream carries as a JSON string)."""
    if not prefix:
        return []
    p = prefix.replace("\\", "/")
    b = p.replace("/", "\\")
    e1 = json.dumps(b)[1:-1]
    e2 = json.dumps(e1)[1:-1]
    return sorted({p, b, e1, e2}, key=len, reverse=True)


def main():
    if sys.argv[1] == "scan-staged":
        sys.exit(scan_staged(sys.argv[2], sys.argv[3]))
    with open(sys.argv[1], encoding="utf-8") as f:
        cfg = json.load(f)
    out_dir = cfg["out"]
    project = cfg.get("project", cfg["cwd"])
    forms = scrub_forms(cfg.get("scrub"))
    planted = []
    if cfg.get("secrets_file"):
        if inside_work_tree(cfg["secrets_file"]):
            raise SystemExit("secrets_file is inside a git work tree; keep it in scratch")
        planted = secret_forms(cfg["secrets_file"])
    os.makedirs(out_dir, exist_ok=True)

    def unplant(text):
        for form, repl in planted:
            text = text.replace(form, repl)
        return text

    def scrub(text):
        for form in forms:
            text = text.replace(form, "<scratch>")
        return unplant(text)

    def scrub_bytes(raw):
        if not planted and not forms:
            return raw
        return scrub(raw.decode("utf-8", "surrogateescape")).encode("utf-8", "surrogateescape")

    with open(os.path.join(out_dir, "driver-config.json"), "w", encoding="utf-8") as f:
        f.write(scrub(json.dumps(cfg, indent=1)))

    args = [cfg.get("bin", "claude")] + cfg["args"]
    env = dict(os.environ)
    env.update(cfg.get("env", {}))
    t0 = time.time()
    meta = {"args": cfg["args"], "turns": [], "store_bytes_before": store_bytes(project),
            "started_ms": now_ms()}
    stderr = open(os.path.join(out_dir, "stderr.txt"), "wb")
    proc = subprocess.Popen(args, cwd=cfg["cwd"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, env=env, shell=False)
    meta["pid"] = proc.pid
    print("launched pid", proc.pid, flush=True)
    stream = open(os.path.join(out_dir, "stream.jsonl"), "wb")
    deadline = t0 + cfg.get("timeout_s", 600)
    timed, results = [], [0]

    def err_reader():
        for raw in proc.stderr:
            stderr.write(scrub_bytes(raw))
            stderr.flush()

    def reader():
        for raw in proc.stdout:
            t = now_ms()
            stream.write(scrub_bytes(raw))
            stream.flush()
            try:
                obj = json.loads(raw)
            except ValueError:
                continue
            timed.append((t, obj))
            if obj.get("type") == "result":
                results[0] += 1

    th = threading.Thread(target=reader, daemon=True)
    th.start()
    eth = threading.Thread(target=err_reader, daemon=True)
    eth.start()

    def wait_results(n):
        while time.time() < deadline:
            if results[0] >= n:
                return True
            if proc.poll() is not None:
                return False
            time.sleep(0.2)
        return False

    for i, m in enumerate(cfg["messages"]):
        turn = {}
        if isinstance(m, dict):
            content = m["content"]
            if m.get("before"):
                try:
                    r = subprocess.run(m["before"], capture_output=True, text=True, timeout=120)
                    turn["before"] = {"argv": m["before"], "exit": r.returncode,
                                      "stdout": r.stdout[-2000:], "stderr": r.stderr[-2000:]}
                except (OSError, subprocess.TimeoutExpired) as e:
                    turn["before"] = {"argv": m["before"], "error": str(e)}
        else:
            content = m
        line = {"type": "user", "message": {"role": "user", "content": content}}
        ts = time.time()
        proc.stdin.write((json.dumps(line) + "\n").encode("utf-8"))
        proc.stdin.flush()
        ok = wait_results(i + 1)
        turn.update({"msg": content, "ok": ok, "wall_s": round(time.time() - ts, 3),
                     "daemon": sample_daemon(project), "store_bytes": store_bytes(project)})
        meta["turns"].append(turn)
        print("turn", i, "ok", ok, round(time.time() - ts, 1), flush=True)
        if not ok:
            break
    proc.stdin.close()
    try:
        proc.wait(timeout=max(5, deadline - time.time()))
    except subprocess.TimeoutExpired:
        print("timeout; killing own pid", proc.pid, flush=True)
        proc.kill()
        proc.wait()
    th.join(timeout=10)
    eth.join(timeout=10)
    stream.close()
    stderr.close()
    meta["exit"] = proc.returncode
    meta["wall_s"] = round(time.time() - t0, 3)
    meta["store_bytes_after"] = store_bytes(project)
    with open(os.path.join(out_dir, "hooks.json"), "w", encoding="utf-8") as f:
        f.write(scrub(json.dumps(pair_hooks(timed), indent=1)))
    with open(os.path.join(out_dir, "meta.json"), "w", encoding="utf-8") as f:
        f.write(scrub(json.dumps(meta, indent=1)))
    print("exit", proc.returncode, flush=True)


if __name__ == "__main__":
    main()
