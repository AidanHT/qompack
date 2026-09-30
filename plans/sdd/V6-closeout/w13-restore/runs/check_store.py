"""Run a candidate build over a COPY of a store an older build wrote.

usage: python check_store.py <candidate qompack.exe> <generated workdir> <outdir>

Copies <workdir>/proj to <outdir>/proj (the original stays as written), then runs the candidate's
fsck --json, backup create/verify/restore --json, a daemon start through session-start and its idle
exit, and fsck again. Records each command's exit code and the rows that failed.
"""

import json
import os
import shutil
import subprocess
import sys
import time

exe, work, out = sys.argv[1], os.path.abspath(sys.argv[2]), os.path.abspath(sys.argv[3])
LONG = "\\\\?\\"
if os.path.exists(out):
    shutil.rmtree(LONG + out)
os.makedirs(out)
proj = os.path.join(out, "proj")
shutil.copytree(LONG + os.path.join(work, "proj"), LONG + proj, symlinks=False)
home = os.path.join(out, "home")
os.makedirs(home)

env = dict(os.environ)
for k in list(env):
    if k.startswith("QOMPACK_") or k.startswith("CLAUDE_"):
        del env[k]
env.update({
    "QOMPACK_PROJECT_ROOT": proj, "HOME": home, "USERPROFILE": home,
    "QOMPACK_HOME": os.path.join(home, ".qompack"),
    "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS": "5",
})


def run(name, args, stdin=b""):
    p = subprocess.run([exe] + args, input=stdin, env=env, cwd=proj, capture_output=True, timeout=180)
    with open(os.path.join(out, name + ".stdout.txt"), "wb") as f:
        f.write(p.stdout)
    with open(os.path.join(out, name + ".stderr.txt"), "wb") as f:
        f.write(p.stderr)
    failing = []
    try:
        doc = json.loads(p.stdout.decode("utf-8", "replace").strip().splitlines()[-1])
        checks = doc.get("checks") or (doc.get("integrity") or {}).get("checks") or []
        failing = [(c["id"], c.get("detail")) for c in checks if not c.get("ok")]
        if doc.get("error"):
            failing.append(("error", doc["error"]))
    except Exception:  # not JSON
        pass
    print(f"{name}: exit {p.returncode}; failing rows: {failing}")
    return p.returncode


run("01-fsck", ["fsck", "--json"])
run("02-backup-create", ["backup", "create", "--id", "xv", "--json"])
run("03-backup-verify", ["backup", "verify", "--id", "xv", "--json"])
run("04-backup-restore", ["backup", "restore", "--id", "xv", "--destination",
                          os.path.join(out, "restored"), "--json"])
sess = {"session_id": "sess-candidate-after-upgrade", "cwd": proj,
        "transcript_path": os.path.join(proj, "t.jsonl"), "hook_event_name": "SessionStart", "source": "startup"}
run("05-session-start", ["session-start"], json.dumps(sess).encode())
time.sleep(3)
end = dict(sess, hook_event_name="SessionEnd", reason="exit")
run("06-flush", ["flush"], json.dumps(end).encode())
lock = os.path.join(proj, ".qompack", "run", "daemon.lock")
deadline = time.time() + 120
while time.time() < deadline and os.path.exists(lock):
    time.sleep(1)
print("daemon lock present after wait:", os.path.exists(lock))
loud = os.path.join(proj, ".qompack", "logs", "LOUD.log")
print("LOUD.log:", open(loud, encoding="utf-8").read() if os.path.exists(loud) else "(absent)")
run("07-fsck-after-daemon", ["fsck", "--json"])
