"""Policy regression checks; execute only in GitHub Actions."""

import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("rollback_scope", Path(__file__).with_name("check-rollback-scope.py"))
scope = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scope)


class RollbackScopeTests(unittest.TestCase):
    def test_release_baseline_excludes_current_drafts_and_prereleases(self):
        releases = [{"tag_name": tag, "draft": draft, "prerelease": prerelease}
                    for tag, draft, prerelease in (("v0.2.9", False, False), ("v0.2.22", False, False),
                                                   ("v0.2.23", False, False), ("v0.2.24", True, False),
                                                   ("v0.2.25-rc.1", False, True), ("v0.3.0", False, False))]
        self.assertEqual(scope.baseline_release(releases, "v0.2.23"), "v0.2.22")
        self.assertIsNone(scope.baseline_release([], ""))

    def test_only_known_paths_can_skip(self):
        for path in ("README.md", "AGENTS.md", "docs/mvp-design.md", "web/src/App.tsx",
                     "web/public/favicon.svg", "web/tests/lyrics.spec.ts", "internal/forge/lyrics_test.go"):
            with self.subTest(path=path):
                self.assertTrue(scope.presentation_or_test(path))
        for path in ("internal/forge/store.go", "internal/forge/lyrics_files.go", "web/embed.go",
                     "go.mod", "go.sum", "web/package-lock.json", "Dockerfile", "docker-compose.yml",
                     ".github/workflows/ci.yml", ".github/scripts/check-rollback.py", "unknown.file"):
            with self.subTest(path=path):
                self.assertFalse(scope.presentation_or_test(path))

    def test_actual_git_diff_and_backend_rename(self):
        previous = Path.cwd()
        with tempfile.TemporaryDirectory() as directory:
            os.chdir(directory)
            try:
                def git(*args):
                    subprocess.run(["git", "-c", "user.name=CI", "-c", "user.email=ci@example.test", *args],
                                   check=True, capture_output=True)

                git("init")
                Path("internal/forge").mkdir(parents=True)
                Path("internal/forge/store.go").write_text("package forge\n")
                git("add", ".")
                git("commit", "-m", "baseline")
                git("tag", "v0.2.22")
                Path("README.md").write_text("Documentation update\n")
                git("add", ".")
                git("commit", "-m", "documentation")
                self.assertFalse(scope.compare("v0.2.22")["required"])
                Path("docs").mkdir()
                git("mv", "internal/forge/store.go", "docs/old.md")
                git("commit", "-m", "move backend into documentation")
                result = scope.compare("v0.2.22")
                self.assertTrue(result["required"])
                self.assertIn("internal/forge/store.go", result["persistence_candidates"])
                self.assertTrue(scope.compare("v0.2.999")["required"])
                self.assertTrue(scope.compare(None)["required"])
                git("tag", "v0.2.23")
                git("checkout", "v0.2.22")
                self.assertTrue(scope.compare("v0.2.23")["required"])
            finally:
                os.chdir(previous)

    def test_lookup_failure_requires_validation_and_records_output(self):
        previous = Path.cwd()
        with tempfile.TemporaryDirectory() as directory:
            os.chdir(directory)
            try:
                with patch.dict(os.environ, {"GITHUB_REPOSITORY": "sagehou/MusicForge", "GITHUB_TOKEN": "ci-fake",
                                             "GITHUB_OUTPUT": "output", "GITHUB_STEP_SUMMARY": "summary"}):
                    with patch.object(scope.urllib.request, "urlopen", side_effect=OSError("unavailable")):
                        scope.main()
                self.assertEqual(Path("output").read_text(), "required=true\n")
                self.assertIn("baseline lookup failed", Path(".ci/rollback-scope.json").read_text())
            finally:
                os.chdir(previous)


if __name__ == "__main__":
    unittest.main()
