"""Self-test for homeguard.py on a fake home. Run: python homeguard_test.py (never touches ~)."""
import builtins
import contextlib
import io
import os
import shutil
import tempfile
import unittest

import homeguard


def write(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(data)


class HomeGuardTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="homeguard-test-")
        self.home = os.path.join(self.tmp, "home")
        c = os.path.join(self.home, ".claude")
        write(os.path.join(c, "settings.json"), '{"a": 1}')
        write(os.path.join(c, ".credentials.json"), "SECRET-DO-NOT-READ")
        write(os.path.join(c, "plugins", "installed_plugins.json"), '{"p": "x"}')
        write(os.path.join(c, "plugins", "known_marketplaces.json"), '{"m": "y"}')
        write(os.path.join(c, "plugins", "cache", "mkt", "other", "1.0", "f.txt"), "z")
        os.makedirs(os.path.join(c, "plugins", "marketplaces", "mkt"))
        os.makedirs(os.path.join(c, "plugins", "data", "qompack-old"))
        os.makedirs(os.path.join(self.home, ".qompack", "bin", "aaa"))
        self.snap = os.path.join(self.tmp, "snap.json")
        self.c = c
        self.assertEqual(self.run_("snap")[0], 0)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def run_(self, mode):
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            rc = homeguard.main([mode, self.snap, "--home", self.home])
        return rc, buf.getvalue()

    def test_unchanged_passes(self):
        rc, out = self.run_("check")
        self.assertEqual(rc, 0, out)
        self.assertIn("real home fingerprint unchanged", out)

    def test_same_size_byte_change_fails(self):
        write(os.path.join(self.c, "plugins", "installed_plugins.json"), '{"p": "X"}')
        rc, out = self.run_("check")
        self.assertEqual(rc, 1, out)
        self.assertIn("DIFF  plugins/installed_plugins.json", out)

    def test_each_file_is_guarded(self):
        for rel in ("settings.json", "plugins/known_marketplaces.json"):
            p = os.path.join(self.c, *rel.split("/"))
            with open(p, encoding="utf-8") as f:
                orig = f.read()
            write(p, orig + " ")
            self.assertEqual(self.run_("check")[0], 1, rel)
            write(p, orig)
            self.assertEqual(self.run_("check")[0], 0, rel)

    def test_cache_version_left_behind_fails_until_removed(self):
        v = os.path.join(self.c, "plugins", "cache", "qompack-live", "qompack", "0.3.0")
        os.makedirs(v)
        rc, out = self.run_("check")
        self.assertEqual(rc, 1)
        self.assertIn("qompack-live/", out)
        shutil.rmtree(os.path.join(self.c, "plugins", "cache", "qompack-live"))
        self.assertEqual(self.run_("check")[0], 0)

    def test_clean_removes_only_run_created_empty_qompack_data(self):
        os.makedirs(os.path.join(self.c, "plugins", "data", "qompack-inline"))
        write(os.path.join(self.c, "plugins", "data", "qompack-full", "x"), "1")
        self.assertEqual(self.run_("check")[0], 1)
        rc, out = self.run_("clean")
        self.assertIn("removed run-created empty plugins/data/qompack-inline/", out)
        self.assertIn("LEFT non-empty run-created plugins/data/qompack-full/", out)
        self.assertTrue(os.path.isdir(os.path.join(self.c, "plugins", "data", "qompack-old")))
        shutil.rmtree(os.path.join(self.c, "plugins", "data", "qompack-full"))
        self.assertEqual(self.run_("check")[0], 0)

    def test_qompack_additions_are_info_losses_fail(self):
        os.makedirs(os.path.join(self.home, ".qompack", "bin", "bbb"))
        os.makedirs(os.path.join(self.home, ".qompack", "logs"))
        rc, out = self.run_("check")
        self.assertEqual(rc, 0, out)
        self.assertIn("INFO  .qompack/ gained", out)
        shutil.rmtree(os.path.join(self.home, ".qompack", "bin", "aaa"))
        rc, out = self.run_("check")
        self.assertEqual(rc, 1, out)
        self.assertIn("lost pre-existing ['bin/aaa/']", out)

    def test_qompack_absent_at_snap_then_created_is_info(self):
        shutil.rmtree(os.path.join(self.home, ".qompack"))
        os.remove(self.snap)
        self.assertEqual(self.run_("snap")[0], 0)
        os.makedirs(os.path.join(self.home, ".qompack", "logs"))
        os.makedirs(os.path.join(self.home, ".qompack", "bin", "abc"))
        rc, out = self.run_("check")
        self.assertEqual(rc, 0, out)
        self.assertIn("INFO  .qompack/ gained", out)
        self.assertNotIn("lost pre-existing", out)

    def test_credentials_never_opened(self):
        opened = []
        real_open = builtins.open

        def spy(file, *a, **k):
            opened.append(os.path.basename(str(file)))
            return real_open(file, *a, **k)

        builtins.open = spy
        try:
            os.remove(self.snap)
            self.run_("snap")
            self.run_("check")
            self.run_("clean")
        finally:
            builtins.open = real_open
        self.assertNotIn(".credentials.json", opened)
        self.assertLessEqual(set(opened), {"settings.json", "installed_plugins.json",
                                           "known_marketplaces.json", "snap.json"})

    def test_snap_refuses_overwrite(self):
        rc, out = self.run_("snap")
        self.assertEqual(rc, 2)
        self.assertIn("REFUSED", out)

    def test_absent_files_are_fingerprinted(self):
        os.remove(os.path.join(self.c, "settings.json"))
        rc, out = self.run_("check")
        self.assertEqual(rc, 1)
        self.assertIn("-> absent", out)


if __name__ == "__main__":
    unittest.main(verbosity=2)
