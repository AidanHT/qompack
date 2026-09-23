"""Real-home guard for scratch smoke runs (mirrors test/e2e's R8-2 fingerprint).

usage: homeguard.py snap <file> | homeguard.py check <file> <project_dir>
check: removes ~/.claude/plugins/data/qompack-inline iff it was absent at snap time and is empty,
stops the trial project's daemon (pid from <project>/.qompack/run/daemon.lock, only when the
process image is qompack.exe), then compares the fingerprint and exits 1 on any difference.
"""
import hashlib
import json
import os
import subprocess
import sys
import time

HOME = os.path.expanduser("~")
DOT = os.path.join(HOME, ".claude")
INLINE = os.path.join(DOT, "plugins", "data", "qompack-inline")


def fingerprint():
    out = []
    s = os.path.join(DOT, "settings.json")
    if os.path.exists(s):
        out.append("settings.json " + hashlib.sha256(open(s, "rb").read()).hexdigest())
    else:
        out.append("settings.json absent")
    base = os.path.join(DOT, "plugins")
    lines = []
    for root, dirs, files in os.walk(base):
        rel = os.path.relpath(root, base).replace("\\", "/")
        lines.append(rel + "/")
        for f in files:
            p = os.path.join(root, f)
            try:
                lines.append(f"{rel}/{f} {os.path.getsize(p)}")
            except OSError:
                lines.append(f"{rel}/{f} vanished")
    out.extend(sorted(lines))
    return out


def image_of(pid):
    r = subprocess.run(["tasklist", "/FI", f"PID eq {pid}", "/FO", "CSV", "/NH"],
                       capture_output=True, text=True)
    line = r.stdout.strip()
    return line.split(",")[0].strip('"') if line.startswith('"') else ""


def main():
    mode, path = sys.argv[1], sys.argv[2]
    if mode == "snap":
        json.dump({"fp": fingerprint(), "inline_existed": os.path.exists(INLINE)}, open(path, "w"))
        print("snap ok")
        return
    snap = json.load(open(path))
    project = sys.argv[3]
    lock = os.path.join(project, ".qompack", "run", "daemon.lock")
    if os.path.exists(lock):
        pid = json.load(open(lock)).get("pid")
        img = image_of(pid) if pid else ""
        if img.lower() == "qompack.exe":
            subprocess.run(["taskkill", "/PID", str(pid), "/F"], capture_output=True)
            print("stopped trial daemon", pid)
        else:
            print("daemon pid", pid, "not running as qompack.exe (image=%r); nothing stopped" % img)
    time.sleep(0.5)
    if not snap["inline_existed"] and os.path.isdir(INLINE):
        try:
            os.rmdir(INLINE)
            print("removed run-created", INLINE)
        except OSError as e:
            print("LEFT non-empty", INLINE, e)
    now = fingerprint()
    if now != snap["fp"]:
        a, b = set(snap["fp"]), set(now)
        print("REAL HOME CHANGED: -", sorted(a - b), "+", sorted(b - a))
        sys.exit(1)
    print("real home fingerprint unchanged")


if __name__ == "__main__":
    main()
