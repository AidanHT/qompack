"""Map every Windows hot-path harness row (table.txt) to the host's power source.

Read-only. Inputs: table.txt (from parse.py), acdc-events.txt (Kernel-Power event 105 history).
For each row's log, the run's end is bounded above by the commit that added the log
(git log --diff-filter=A in the qompack-v6 worktree). The power state is reported over the
45 minutes before that bound, which is only meaningful when the commit followed the run closely;
rows with a precise run time (phase3 chain JSON) are reported exactly.
"""
import datetime as dt
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = r"C:/Users/Quant/Documents/Programming/Projects/qompack-v6"
BASE = "plans/sdd/V6-closeout"

events = []
for line in open(os.path.join(HERE, "acdc-events.txt"), encoding="utf-8-sig"):
    m = re.match(r"(\S+) AC=(true|false) (\d+)%", line.strip())
    if m:
        events.append((dt.datetime.fromisoformat(m.group(1)), m.group(2) == "true", int(m.group(3))))
events.sort()


def state_at(t):
    cur = None
    for when, ac, pct in events:
        if when <= t:
            cur = (ac, when, pct)
        else:
            break
    return cur


def states_between(a, b):
    out = []
    s = state_at(a)
    if s:
        out.append("AC" if s[0] else "BAT")
    for when, ac, pct in events:
        if a < when <= b:
            out.append(("AC" if ac else "BAT") + "@" + when.strftime("%H:%M"))
    return out


def commit_time(rel):
    p = BASE + "/" + rel.replace("\\", "/")
    try:
        out = subprocess.run(["git", "-C", REPO, "log", "--diff-filter=A", "--format=%ad", "--date=iso-strict-local", "--", p],
                             capture_output=True, text=True, check=True).stdout.strip().splitlines()
    except subprocess.CalledProcessError:
        return None
    if not out:
        return None
    return dt.datetime.fromisoformat(out[-1]).replace(tzinfo=None)


rows = [l for l in open(os.path.join(HERE, "table.txt"), encoding="utf-8") if "windows/amd64" in l]
for l in rows:
    parts = [x.strip() for x in l.split("|")]
    rel, test, arm = parts[0], parts[2], parts[3]
    floor = parts[4]
    ba = parts[5]
    bb = parts[6]
    obs = parts[8]
    led = parts[9]
    ct = commit_time(rel)
    if ct is None:
        span = "uncommitted"
    else:
        if ct < events[0][0]:
            span = f"commit {ct:%m-%d %H:%M} (before event history)"
        else:
            span = f"commit {ct:%m-%d %H:%M} " + ",".join(states_between(ct - dt.timedelta(minutes=45), ct))
    print(f"{rel[:70]:70} {arm[:12]:12} {floor:16} {ba[:34]:34} {bb[:34]:34} {obs:20} {led:18} {span}")
