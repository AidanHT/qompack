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


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()

FAKE_HOST = textwrap.dedent('''
    import json, sys, time
    def emit(o):
        sys.stdout.write(json.dumps(o) + "\\n"); sys.stdout.flush()
    emit({"type": "system", "subtype": "init", "cwd": "SCRATCH/project"})
    n = 0
    for line in sys.stdin:
        n += 1
        hid = "h%d" % n
        emit({"type": "system", "subtype": "hook_started", "hook_id": hid,
              "hook_name": "UserPromptSubmit", "hook_event": "UserPromptSubmit"})
        time.sleep(0.15)
        emit({"type": "system", "subtype": "hook_response", "hook_id": hid,
              "hook_name": "UserPromptSubmit", "hook_event": "UserPromptSubmit",
              "outcome": "success", "exit_code": 0})
        emit({"type": "result", "subtype": "success", "echo": json.loads(line)["message"]["content"]})
    emit({"type": "system", "subtype": "hook_started", "hook_id": "end",
          "hook_name": "SessionEnd", "hook_event": "SessionEnd"})
''')


class LiveDriverTest(unittest.TestCase):
    def test_turns_hooks_before_and_scrub(self):
        tmp = tempfile.mkdtemp(prefix="live-driver-test-")
        try:
            project = os.path.join(tmp, "project")
            os.makedirs(os.path.join(project, ".qompack", "run"))
            with open(os.path.join(project, ".qompack", "run", "daemon.lock"), "w") as f:
                json.dump({"pid": os.getpid()}, f)  # a live pid that is not qompack.exe
            host = os.path.join(tmp, "fake_host.py")
            with open(host, "w") as f:
                f.write(FAKE_HOST.replace("SCRATCH", tmp.replace("\\", "/")))
            marker = os.path.join(tmp, "before.txt")
            cfg = {"bin": sys.executable, "args": [host], "cwd": project,
                   "out": os.path.join(tmp, "out"), "scrub": tmp, "timeout_s": 60,
                   "messages": ["first", {"content": "second", "before": [
                       sys.executable, "-c", "open(r'%s','w').write('x'); print('ran')" % marker]}]}
            cfgp = os.path.join(tmp, "cfg.json")
            with open(cfgp, "w") as f:
                json.dump(cfg, f)
            r = subprocess.run([sys.executable, os.path.join(HERE, "live_driver.py"), cfgp],
                               capture_output=True, text=True, timeout=120)
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
            self.assertTrue(all(h["latency_ms"] >= 100 for h in paired), paired)
            end = [h for h in hooks if h["hook_event"] == "SessionEnd"]
            self.assertEqual(end[0]["latency_ms"], None)
            for name in ("driver-config.json", "meta.json"):
                text = read(os.path.join(out, name))
                self.assertNotIn(tmp.replace("\\", "/"), text.replace("\\\\", "/").replace("\\", "/"))
                self.assertIn("<scratch>", text)
            self.assertIn(tmp.replace("\\", "/"), read(os.path.join(out, "stream.jsonl")))
        finally:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
