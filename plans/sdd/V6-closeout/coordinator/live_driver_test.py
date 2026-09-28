"""Self-test for live_driver.py against a fake host (no model, no claude). Run: python live_driver_test.py"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
DRIVER = os.path.join(HERE, "live_driver.py")
# Built from parts so no committed file holds a planted string; the second needs JSON escaping.
SECRETS = ["QPK" + "FAKE_" + "9f3a" * 6, 'pl"ant' + "\\" + "ed-" + "zz81" * 3]


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def forms(s):
    e1 = json.dumps(s)[1:-1]
    return (s, e1, json.dumps(e1)[1:-1])


FAKE_HOST = textwrap.dedent('''
    import json, sys, time
    def emit(o):
        sys.stdout.write(json.dumps(o) + "\\n"); sys.stdout.flush()
    emit({"type": "system", "subtype": "hook_started", "hook_id": "ss",
          "hook_name": "SessionStart:startup", "hook_event": "SessionStart"})
    emit({"type": "system", "subtype": "hook_response", "hook_id": "ss",
          "hook_name": "SessionStart:startup", "hook_event": "SessionStart",
          "outcome": "success", "exit_code": 0})
    emit({"type": "system", "subtype": "init", "cwd": "SCRATCH/project"})
    n = 0
    for line in sys.stdin:
        n += 1
        hid = "h%d" % n
        content = json.loads(line)["message"]["content"]
        sys.stderr.write("host saw: " + content + "\\n"); sys.stderr.flush()
        emit({"type": "system", "subtype": "hook_started", "hook_id": hid,
              "hook_name": "UserPromptSubmit", "hook_event": "UserPromptSubmit"})
        time.sleep(0.15)
        emit({"type": "system", "subtype": "hook_response", "hook_id": hid,
              "hook_name": "UserPromptSubmit", "hook_event": "UserPromptSubmit",
              "output": json.dumps({"additionalContext": content}),
              "outcome": "success", "exit_code": 0})
        emit({"type": "result", "subtype": "success", "echo": content})
    emit({"type": "system", "subtype": "hook_started", "hook_id": "end",
          "hook_name": "SessionEnd", "hook_event": "SessionEnd"})
''')


def write_host(tmp):
    host = os.path.join(tmp, "fake_host.py")
    with open(host, "w") as f:
        f.write(FAKE_HOST.replace("SCRATCH", tmp.replace("\\", "/")))
    return host


def run_driver(tmp, cfg):
    cfgp = os.path.join(tmp, "cfg.json")
    with open(cfgp, "w") as f:
        json.dump(cfg, f)
    return subprocess.run([sys.executable, DRIVER, cfgp], capture_output=True, text=True,
                          timeout=120)


class LiveDriverTest(unittest.TestCase):
    def test_turns_hooks_before_and_scrub(self):
        tmp = tempfile.mkdtemp(prefix="live-driver-test-")
        try:
            project = os.path.join(tmp, "project")
            os.makedirs(os.path.join(project, ".qompack", "run"))
            with open(os.path.join(project, ".qompack", "run", "daemon.lock"), "w") as f:
                json.dump({"pid": os.getpid()}, f)  # a live pid that is not qompack.exe
            marker = os.path.join(tmp, "before.txt")
            cfg = {"bin": sys.executable, "args": [write_host(tmp)], "cwd": project,
                   "out": os.path.join(tmp, "out"), "scrub": tmp, "timeout_s": 60,
                   "messages": ["first", {"content": "second", "before": [
                       sys.executable, "-c", "open(r'%s','w').write('x'); print('ran')" % marker]}]}
            r = run_driver(tmp, cfg)
            self.assertEqual(r.returncode, 0, r.stderr)
            out = cfg["out"]
            meta = json.loads(read(os.path.join(out, "meta.json")))
            self.assertEqual([t["ok"] for t in meta["turns"]], [True, True])
            self.assertEqual(meta["turns"][1]["before"]["exit"], 0)
            self.assertIn("ran", meta["turns"][1]["before"]["stdout"])
            self.assertTrue(os.path.exists(marker))
            self.assertEqual(meta["turns"][0]["daemon"]["daemon"], "not running as qompack.exe")
            self.assertGreater(meta["turns"][0]["store_bytes"], 0)
            hooks = json.loads(read(os.path.join(out, "hooks.json")))
            paired = [h for h in hooks if h["hook_event"] == "UserPromptSubmit"]
            self.assertEqual(len(paired), 2)
            self.assertTrue(all(h["latency_ms"] >= 100 and h["measured"] for h in paired), paired)
            start = [h for h in hooks if h["hook_event"] == "SessionStart"]
            self.assertTrue(start[0]["before_init"])
            self.assertFalse(start[0]["measured"], start)
            end = [h for h in hooks if h["hook_event"] == "SessionEnd"]
            self.assertEqual(end[0]["latency_ms"], None)
            self.assertFalse(end[0]["measured"])
            for name in ("driver-config.json", "meta.json"):
                text = read(os.path.join(out, name))
                self.assertNotIn(tmp.replace("\\", "/"), text.replace("\\\\", "/").replace("\\", "/"))
                self.assertIn("<scratch>", text)
            self.assertIn(tmp.replace("\\", "/"), read(os.path.join(out, "stream.jsonl")))
            self.assertIn("host saw: second", read(os.path.join(out, "stderr.txt")))
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

    def test_planted_secrets_never_reach_outputs(self):
        tmp = tempfile.mkdtemp(prefix="live-driver-secrets-")
        try:
            project = os.path.join(tmp, "project")
            os.makedirs(project)
            sec = os.path.join(tmp, "secrets.json")
            with open(sec, "w", encoding="utf-8") as f:
                json.dump(SECRETS, f)
            cfg = {"bin": sys.executable, "args": [write_host(tmp)], "cwd": project,
                   "secrets_file": sec, "out": os.path.join(tmp, "out"), "timeout_s": 60,
                   "messages": ["read " + SECRETS[0], {"content": "and " + SECRETS[1], "before": [
                       sys.executable, "-c", "print(%r)" % SECRETS[0]]}]}
            r = run_driver(tmp, cfg)
            self.assertEqual(r.returncode, 0, r.stderr)
            out = cfg["out"]
            seen = set()
            for name in os.listdir(out):
                text = read(os.path.join(out, name))
                for s in SECRETS:
                    for form in forms(s):
                        self.assertNotIn(form, text, name)
                seen |= {m for m in ("@@SEC_PLANTED_1@@", "@@SEC_PLANTED_2@@") if m in text}
            self.assertEqual(seen, {"@@SEC_PLANTED_1@@", "@@SEC_PLANTED_2@@"})
            self.assertIn("@@SEC_PLANTED_2@@", read(os.path.join(out, "stderr.txt")))
            self.assertIn("@@SEC_PLANTED_2@@", read(os.path.join(out, "stream.jsonl")))
            self.assertIn("@@SEC_PLANTED_1@@", read(os.path.join(out, "meta.json")))
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

    def test_scan_staged_and_secrets_file_outside_work_tree(self):
        tmp = tempfile.mkdtemp(prefix="live-driver-scan-")
        try:
            repo = os.path.join(tmp, "repo")
            os.makedirs(repo)
            subprocess.run(["git", "init", "-q", repo], check=True)
            sec = os.path.join(tmp, "secrets.json")
            with open(sec, "w", encoding="utf-8") as f:
                json.dump(SECRETS, f)
            with open(os.path.join(repo, "evidence.txt"), "w", encoding="utf-8") as f:
                f.write("clean\n")
            with open(os.path.join(repo, "leak.json"), "w", encoding="utf-8") as f:
                f.write(json.dumps({"t": SECRETS[1]}))
            subprocess.run(["git", "-C", repo, "add", "."], check=True)
            scan = [sys.executable, DRIVER, "scan-staged", repo, sec]
            r = subprocess.run(scan, capture_output=True, text=True)
            self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
            self.assertIn("leak.json", r.stdout)
            self.assertNotIn("evidence.txt", r.stdout)
            self.assertNotIn(SECRETS[1], r.stdout)
            subprocess.run(["git", "-C", repo, "rm", "-q", "--cached", "leak.json"], check=True)
            r = subprocess.run(scan, capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
            inside = os.path.join(repo, "secrets.json")
            shutil.copy(sec, inside)
            r = run_driver(tmp, {"bin": sys.executable, "args": ["-c", "pass"], "cwd": tmp,
                                 "out": os.path.join(tmp, "out"), "secrets_file": inside,
                                 "messages": []})
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("inside a git work tree", r.stderr)
            self.assertFalse(os.path.exists(os.path.join(tmp, "out")))
        finally:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
