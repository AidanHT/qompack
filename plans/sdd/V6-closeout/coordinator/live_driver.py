"""Live-lane session driver: one `claude -p` streaming-input session, one user message per turn.

usage: python live_driver.py <config.json>

Derived from plans/sdd/V6-closeout/eval/runs/smoke_driver.py (same turn discipline: each message
is sent only after the previous turn's `result` line). Additions for the Phase 4 lane:

- every stdout line's arrival time is kept, and hooks.json pairs each hook_started with its
  hook_response by hook_id: the host's stream carries no duration field, so arrival deltas are
  the hook latency as the host saw it (an upper bound: it includes stream buffering). A started
  hook with no response (cancelled, timed out) is listed with latency_ms null.
- a message may be {"content": "...", "before": [argv...]}: argv runs (no shell, 120 s cap) before
  the message is sent, its output and exit code go to meta.json. Degraded-path steps use it.
- after every turn, when <project>/.qompack/run/daemon.lock names a live qompack.exe, the daemon's
  working set, peak working set and CPU seconds are sampled (PowerShell Get-Process), and the
  .qompack/ byte total is recorded (C5.6).
- driver-config.json and meta.json have cfg["scrub"] (the scratch prefix, / or \ form) replaced
  by <scratch>; stream.jsonl and stderr.txt are kept exactly as the host wrote them.

Config keys: bin (default "claude"), args, cwd, out, env, messages, timeout_s (default 600),
project (default cwd), scrub (optional).
"""
import json
import os
import subprocess
import sys
import threading
import time


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
    started, out = {}, []
    for t, obj in timed:
        if obj.get("type") != "system":
            continue
        hid = obj.get("hook_id")
        if obj.get("subtype") == "hook_started":
            started[hid] = (t, obj)
        elif obj.get("subtype") == "hook_response":
            t0, s = started.pop(hid, (None, {}))
            out.append({"hook_id": hid, "hook_name": obj.get("hook_name"),
                        "hook_event": obj.get("hook_event"),
                        "latency_ms": None if t0 is None else t - t0,
                        "outcome": obj.get("outcome"), "exit_code": obj.get("exit_code")})
    for hid, (t0, s) in started.items():
        out.append({"hook_id": hid, "hook_name": s.get("hook_name"),
                    "hook_event": s.get("hook_event"), "latency_ms": None,
                    "outcome": "no hook_response in stream", "exit_code": None})
    return out


def main():
    with open(sys.argv[1], encoding="utf-8") as f:
        cfg = json.load(f)
    out_dir = cfg["out"]
    os.makedirs(out_dir, exist_ok=True)
    project = cfg.get("project", cfg["cwd"])
    forms = set()
    if cfg.get("scrub"):
        p = cfg["scrub"].replace("\\", "/")
        forms = {p, p.replace("/", "\\"), json.dumps(p.replace("/", "\\"))[1:-1]}

    def scrub(text):
        for form in sorted(forms, key=len, reverse=True):
            text = text.replace(form, "<scratch>")
        return text

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
                            stderr=stderr, env=env, shell=False)
    meta["pid"] = proc.pid
    print("launched pid", proc.pid, flush=True)
    stream = open(os.path.join(out_dir, "stream.jsonl"), "wb")
    deadline = t0 + cfg.get("timeout_s", 600)
    timed, results = [], [0]

    def reader():
        for raw in proc.stdout:
            t = now_ms()
            stream.write(raw)
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
    stream.close()
    stderr.close()
    meta["exit"] = proc.returncode
    meta["wall_s"] = round(time.time() - t0, 3)
    meta["store_bytes_after"] = store_bytes(project)
    with open(os.path.join(out_dir, "hooks.json"), "w", encoding="utf-8") as f:
        json.dump(pair_hooks(timed), f, indent=1)
    with open(os.path.join(out_dir, "meta.json"), "w", encoding="utf-8") as f:
        f.write(scrub(json.dumps(meta, indent=1)))
    print("exit", proc.returncode, flush=True)


if __name__ == "__main__":
    main()
