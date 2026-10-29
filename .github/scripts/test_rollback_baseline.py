"""Published rollback coverage regression checks; run only in GitHub Actions."""

import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("rollback", Path(__file__).with_name("check-rollback.py"))
rollback = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rollback)


class RollbackBaselineTests(unittest.TestCase):
    def test_new_published_baseline_precedes_all_historical_releases(self):
        digest = "sha256:" + "a" * 64
        with patch.object(rollback, "docker", return_value=digest + "\n") as docker:
            releases = rollback.rollback_releases("v0.2.999")
        docker.assert_called_once_with("buildx", "imagetools", "inspect",
                                       "ghcr.io/sagehou/musicforge:v0.2.999", "--format", "{{.Manifest.Digest}}")
        self.assertEqual(next(iter(releases)), "v0.2.999")
        self.assertEqual(releases.pop("v0.2.999"), digest)
        self.assertEqual(releases, rollback.LEGACY)

    def test_missing_or_invalid_baseline_fails_before_docker(self):
        for baseline in (None, "", 50, "v0.3.0", "v0.2.50-rc.1", "v0.2.50\n", "latest"):
            with self.subTest(baseline=baseline), patch.object(rollback, "docker") as docker:
                with self.assertRaises(ValueError):
                    rollback.rollback_releases(baseline)
                docker.assert_not_called()

    def test_invalid_or_unavailable_manifest_cannot_skip_baseline(self):
        for digest in ("", "sha256:bad", "sha256:" + "A" * 64, "sha256:" + "a" * 65):
            with self.subTest(digest=digest), patch.object(rollback, "docker", return_value=digest):
                with self.assertRaises(ValueError):
                    rollback.rollback_releases("v0.2.999")
        with patch.object(rollback, "docker", side_effect=RuntimeError("registry unavailable")):
            with self.assertRaises(RuntimeError):
                rollback.rollback_releases("v0.2.999")

    def test_pinned_baseline_is_not_duplicated_or_overwritten(self):
        baseline = next(iter(rollback.LEGACY))
        with patch.object(rollback, "docker", return_value=rollback.LEGACY[baseline]):
            self.assertEqual(rollback.rollback_releases(baseline), rollback.LEGACY)
        with patch.object(rollback, "docker", return_value="sha256:" + "0" * 64):
            with self.assertRaises(ValueError):
                rollback.rollback_releases(baseline)


if __name__ == "__main__":
    unittest.main()
