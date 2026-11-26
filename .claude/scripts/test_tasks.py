#!/usr/bin/env python3
"""Tests of `tasks.py cleanup` on throwaway git repositories (GitHub calls are faked).

Run: python3 -B .claude/scripts/test_tasks.py
"""
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tasks  # noqa: E402


def run(cwd, *args):
    return subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@example.invalid", *args],
                          cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()


class CleanupTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.origin = base / "origin.git"
        self.clone = base / "clone"
        run(base, "init", "--quiet", "--bare", "--initial-branch=main", str(self.origin))
        run(base, "clone", "--quiet", str(self.origin), str(self.clone))
        (self.clone / "f").write_text("0\n")
        run(self.clone, "add", "f")
        run(self.clone, "commit", "--quiet", "-m", "init")
        run(self.clone, "push", "--quiet", "origin", "main")
        self.merged = []
        self.saved = (tasks.ROOT, tasks.gh_json)
        tasks.ROOT = self.clone
        tasks.gh_json = self.fake_gh_json

    def tearDown(self):
        tasks.ROOT, tasks.gh_json = self.saved
        self.tmp.cleanup()

    def fake_gh_json(self, *args):
        if args[:2] == ("repo", "view"):
            return {"defaultBranchRef": {"name": "main"}}
        if args[:2] == ("pr", "list"):
            return self.merged
        raise AssertionError(f"unexpected gh call: {args}")

    def branch(self, name, push=True):
        """Create a branch with one commit, push it and record it as a merged PR at that commit."""
        run(self.clone, "switch", "--quiet", "-c", name, "main")
        (self.clone / name).write_text(name)
        run(self.clone, "add", name)
        run(self.clone, "commit", "--quiet", "-m", name)
        if push:
            run(self.clone, "push", "--quiet", "origin", name)
        self.merged.append({"headRefName": name, "headRefOid": run(self.clone, "rev-parse", name), "number": len(self.merged) + 1})
        run(self.clone, "switch", "--quiet", "main")

    def local(self):
        return set(run(self.clone, "for-each-ref", "--format=%(refname:short)", "refs/heads").split())

    def remote(self):
        return set(run(self.origin, "for-each-ref", "--format=%(refname:short)", "refs/heads").split())

    def cleanup(self):
        tasks.cmd_cleanup(SimpleNamespace(quiet=True))

    def test_merged_branch_deleted_locally_and_remotely(self):
        self.branch("done")
        self.cleanup()
        self.assertEqual(self.local(), {"main"})
        self.assertEqual(self.remote(), {"main"})

    def test_local_commits_beyond_the_pr_keep_the_branch(self):
        self.branch("ahead")
        run(self.clone, "switch", "--quiet", "ahead")
        (self.clone / "f").write_text("more\n")
        run(self.clone, "commit", "--quiet", "-am", "more")
        run(self.clone, "switch", "--quiet", "main")
        self.cleanup()
        self.assertIn("ahead", self.local())
        self.assertIn("ahead", self.remote())

    def test_remote_moved_past_the_pr_keeps_the_remote_branch(self):
        self.branch("moved")
        other = Path(self.tmp.name) / "other"
        run(Path(self.tmp.name), "clone", "--quiet", "--branch", "moved", str(self.origin), str(other))
        (other / "f").write_text("pushed later\n")
        run(other, "commit", "--quiet", "-am", "later")
        run(other, "push", "--quiet", "origin", "moved")
        self.cleanup()
        self.assertNotIn("moved", self.local())
        self.assertIn("moved", self.remote())

    def test_current_branch_with_changes_is_kept(self):
        self.branch("dirty")
        run(self.clone, "switch", "--quiet", "dirty")
        (self.clone / "f").write_text("uncommitted\n")
        self.cleanup()
        self.assertIn("dirty", self.local())
        self.assertEqual(run(self.clone, "branch", "--show-current"), "dirty")

    def test_clean_current_branch_switches_to_main_then_deletes(self):
        self.branch("current")
        run(self.clone, "switch", "--quiet", "current")
        self.cleanup()
        self.assertEqual(run(self.clone, "branch", "--show-current"), "main")
        self.assertNotIn("current", self.local())

    def test_unmerged_branch_is_untouched(self):
        run(self.clone, "switch", "--quiet", "-c", "wip", "main")
        run(self.clone, "switch", "--quiet", "main")
        self.cleanup()
        self.assertIn("wip", self.local())


if __name__ == "__main__":
    unittest.main()
