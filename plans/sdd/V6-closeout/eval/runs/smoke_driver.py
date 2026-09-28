"""Scratch smoke driver: one claude -p streaming-input session, one user message per turn.

Waits for each turn's `result` line before sending the next message, so messages never queue
into a running turn. Writes the raw stdout stream, stderr and a small meta record.
"""
import json
import os
import subprocess
import sys
import threading
import time

def main():
    cfg = json.load(open(sys.argv[1], encoding="utf-8"))
    out_dir = cfg["out"]
    os.makedirs(out_dir, exist_ok=True)
    args = [cfg.get("bin", "claude")] + cfg["args"]
    env = dict(os.environ)
    env.update(cfg.get("env", {}))
    t0 = time.time()
    proc = subprocess.Popen(
        args, cwd=cfg["cwd"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=open(os.path.join(out_dir, "stderr.txt"), "wb"), env=env, shell=False,
    )
    meta = {"pid": proc.pid, "args": args, "cwd": cfg["cwd"], "turns": []}
    print("launched pid", proc.pid, flush=True)
    stream = open(os.path.join(out_dir, "stream.jsonl"), "wb")
    deadline = t0 + cfg.get("timeout_s", 600)

    lines = []
    def reader():
        for raw in proc.stdout:
            stream.write(raw)
            stream.flush()
            lines.append(raw)
    th = threading.Thread(target=reader, daemon=True)
    th.start()

    def wait_results(n):
        while time.time() < deadline:
            cnt = 0
            for raw in list(lines):
                try:
                    if json.loads(raw).get("type") == "result":
                        cnt += 1
                except Exception:
                    pass
            if cnt >= n:
                return True
            if proc.poll() is not None:
                return False
            time.sleep(0.2)
        return False

    for i, msg in enumerate(cfg["messages"]):
        line = {"type": "user", "message": {"role": "user", "content": msg}}
        proc.stdin.write((json.dumps(line) + "\n").encode("utf-8"))
        proc.stdin.flush()
        ts = time.time()
        ok = wait_results(i + 1)
        meta["turns"].append({"msg": msg, "ok": ok, "wall_s": round(time.time() - ts, 3)})
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
    meta["exit"] = proc.returncode
    meta["wall_s"] = round(time.time() - t0, 3)
    json.dump(meta, open(os.path.join(out_dir, "meta.json"), "w"), indent=2)
    print("exit", proc.returncode, flush=True)

if __name__ == "__main__":
    main()
