"""Drive an old qompack build's real hooks against a fresh temp project to produce its store.

usage: python gen_store.py <qompack.exe> <workdir>

Creates <workdir>/proj (git-initialized project) and <workdir>/home (fake HOME/USERPROFILE), runs a
short session through the build's own hook subcommands (the daemon they spawn is that build's), and
waits for the daemon's idle exit. Prints each hook's exit code. Never touches the real ~/.qompack.
"""

import json
import os
import subprocess
import sys
import time

exe, work = sys.argv[1], os.path.abspath(sys.argv[2])
proj = os.path.join(work, "proj")
home = os.path.join(work, "home")
os.makedirs(proj, exist_ok=True)
os.makedirs(home, exist_ok=True)
with open(os.path.join(proj, "notes.md"), "w", newline="\n") as f:
    f.write("# notes\nThe release marker is AMBER-FALCON-2207.\n")
subprocess.run(["git", "init", "-q", proj], check=True)
subprocess.run(["git", "-C", proj, "add", "notes.md"], check=True)

env = dict(os.environ)
for k in list(env):
    if k.startswith("QOMPACK_") or k.startswith("CLAUDE_"):
        del env[k]
env.update({
    "QOMPACK_PROJECT_ROOT": proj,
    "HOME": home,
    "USERPROFILE": home,
    "QOMPACK_HOME": os.path.join(home, ".qompack"),
    "QOMPACK_RUNTIME__DAEMON__IDLEEXITSECONDS": "5",
})
sess = "sess-crossversion-fixture"
transcript = os.path.join(proj, "transcript.jsonl")


def hook(args, payload):
    p = subprocess.run([exe] + args, input=json.dumps(payload).encode(), env=env, cwd=proj,
                       capture_output=True, timeout=60)
    print(" ".join(args), "->", p.returncode, p.stdout[:200], p.stderr[:200])


base = {"session_id": sess, "cwd": "/fixture/proj", "transcript_path": "/fixture/proj/transcript.jsonl"}
hook(["session-start"], dict(base, hook_event_name="SessionStart", source="startup"))
time.sleep(1.5)
hook(["observe", "prompt"], dict(base, hook_event_name="UserPromptSubmit",
                                 prompt="Read notes.md and tell me the release marker."))
hook(["observe", "tool"], dict(base, hook_event_name="PostToolUse", tool_name="Read",
                               tool_use_id="toolu_fixture_1",
                               tool_input={"file_path": "notes.md"},
                               tool_response={"content": "# notes\nThe release marker is AMBER-FALCON-2207.\n"}))
hook(["observe", "stop"], dict(base, hook_event_name="Stop"))
hook(["observe", "prompt"], dict(base, hook_event_name="UserPromptSubmit",
                                 prompt="Now list the files in the project."))
hook(["observe", "tool"], dict(base, hook_event_name="PostToolUse", tool_name="Bash",
                               tool_use_id="toolu_fixture_2",
                               tool_input={"command": "ls"},
                               tool_response={"stdout": "notes.md\n", "stderr": ""}))
hook(["observe", "stop"], dict(base, hook_event_name="Stop"))
hook(["checkpoint"], dict(base, hook_event_name="PreCompact", trigger="manual"))
hook(["session-start"], dict(base, hook_event_name="SessionStart", source="compact"))
hook(["observe", "prompt"], dict(base, hook_event_name="UserPromptSubmit",
                                 prompt="What was the release marker again?"))
hook(["observe", "stop"], dict(base, hook_event_name="Stop"))
hook(["flush"], dict(base, hook_event_name="SessionEnd", reason="exit"))

lock = os.path.join(proj, ".qompack", "run", "daemon.lock")
deadline = time.time() + 120
while time.time() < deadline and os.path.exists(lock):
    time.sleep(1)
print("daemon lock present after wait:", os.path.exists(lock))
