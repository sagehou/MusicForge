"""CI policy checks; run only in GitHub Actions."""

from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("ci_scope", Path(__file__).with_name("check-ci-scope.py"))
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)
ENV = {"GITHUB_REPOSITORY": "sagehou/MusicForge", "ImageOS": "ubuntu24", "ImageVersion": "20261020.1",
       "RUNNER_OS": "Linux", "RUNNER_ARCH": "X64"}


class CIScopeTests(unittest.TestCase):
    def test_parallel_integrations_reuse_this_runs_binary_and_remain_required(self):
        workflow = Path(__file__).parents[1].joinpath("workflows/ci.yml").read_text()
        jobs = dict(re.findall(r"^  (\w+):\n((?:    .*\n|\n)+)", workflow, re.MULTILINE))
        self.assertNotIn("npm test", jobs["backend"])
        self.assertNotIn("check-rclone.py", jobs["backend"])
        self.assertIn("go build -mod=readonly", jobs["runtime"])
        self.assertNotIn("go test", jobs["runtime"])
        self.assertNotIn("go build", jobs["backend"])
        for name, variable in (("backend", "BACKEND"), ("rclone", "RCLONE"), ("app_browser", "APP_BROWSER")):
            self.assertIn("needs: [plan, runtime]", jobs[name])
            self.assertIn("name: validation-runtime", jobs[name])
            self.assertNotIn("run-id:", jobs[name])
            if name != "backend":
                self.assertNotIn("go test", jobs[name])
            self.assertRegex(jobs["validate"], r"needs: \[[^\]]*\b" + name + r"\b")
            self.assertIn(f"test \"${variable}\" = success", jobs["validate"])
            self.assertIn(f"test \"${variable}\" = skipped", jobs["validate"])
        self.assertIn("test \"$RUNTIME\" = success", jobs["validate"])
        self.assertIn("test \"$RUNTIME\" = skipped", jobs["validate"])

    def test_release_with_skipped_ancestors_requires_all_release_gates(self):
        workflow = Path(__file__).parents[1].joinpath("workflows/ci.yml").read_text()
        job = re.search(r"^  publish:\n(?P<body>(?:    .*\n|\n)+)", workflow, re.MULTILINE).group("body")
        condition = re.search(r"^    if: (.+)$", job, re.MULTILINE).group(1)
        needs = re.search(r"^    needs: \[(.+)\]$", job, re.MULTILINE).group(1)
        self.assertEqual({name.strip() for name in needs.split(",")}, {"audit", "image", "ci"})
        explicit_status = bool(re.search(r"\b(?:always|cancelled|failure|success)\s*\(", condition))
        expression = condition.removeprefix("${{").removesuffix("}}").strip()
        expression = expression.replace("github.ref", "ref").replace("&&", " and ").replace("||", " or ")
        expression = re.sub(r"!(?!=)", "not ", expression)
        expression = re.sub(r"needs\.(\w+)\.result", r'results["\1"]', expression)
        success = {"audit": "success", "image": "success", "ci": "success"}
        cases = [("refs/tags/v0.2.58", success, False, False, True),
                 ("refs/tags/v0.2.58", success, False, True, True),
                 ("refs/heads/main", success, False, True, False),
                 ("refs/tags/v0.2.58", success, True, True, False)]
        for gate in success:
            for result in ("failure", "cancelled", "skipped"):
                cases.append(("refs/tags/v0.2.58", {**success, gate: result}, False, False, False))
        for ref, results, cancelled, ancestors_succeeded, expected in cases:
            with self.subTest(ref=ref, results=results, cancelled=cancelled, ancestors_succeeded=ancestors_succeeded):
                context = {"ref": ref, "results": results, "cancelled": lambda: cancelled,
                           "startsWith": str.startswith, "success": lambda: ancestors_succeeded,
                           "always": lambda: True, "failure": lambda: "failure" in results.values()}
                # Actions implicitly adds success() unless a status function is present.
                eligible = (explicit_status or ancestors_succeeded) and eval(expression, {"__builtins__": {}}, context)
                self.assertEqual(eligible, expected)

    def test_scope_is_an_allowlist_and_tsx_requires_an_additional_ast_gate(self):
        for paths, expected in [([], "docs"), (["AGENTS.md", "docs/a.md"], "docs"),
                                (["web/src/style.css", "README.md"], "ui"),
                                (["web/src/components/library.tsx", "web/src/locales/en.json"], "ui"),
                                (["web/src/api.ts"], "full"), (["web/src/types.ts"], "full"),
                                (["web/index.html"], "full"), (["web/tests/workflow.spec.ts"], "full"),
                                (["web/src/style.css", "internal/forge/store.go"], "full")]:
            self.assertEqual(ci.classify(paths), expected, paths)
        for path in ("go.mod", "web/package-lock.json", ".node-version", "Dockerfile", "docker-compose.yml",
                     ".github/workflows/ci.yml", ".github/scripts/check-ci-scope.py", "unknown"):
            self.assertEqual(ci.classify(["README.md", path]), "full", path)
        # The old rollback path allowlist alone cannot establish presentation-only semantics.
        planned = ci.plan({"required": False, "baseline": "v0.2.35", "changed_files": ["web/src/api.ts"]}, [])
        self.assertTrue(planned["required"])

    def test_trust_excludes_forks_prs_failed_runs_expired_proofs_and_environment_changes(self):
        with patch.dict(os.environ, ENV):
            run = {"id": 10, "conclusion": "success", "event": "push", "path": ".github/workflows/ci.yml",
                   "head_repository": {"full_name": ENV["GITHUB_REPOSITORY"]}, "head_sha": "abc"}
            proof = {"policy": ci.POLICY, "sha": "abc", "run_id": "10", "scope": "full", "environment": ci.environment(),
                     "created_at": datetime.now(timezone.utc).isoformat()}
            self.assertTrue(ci.trusted(run, proof))
            for key, value in (("event", "pull_request"), ("conclusion", "failure"), ("path", ".github/workflows/other.yml"),
                               ("head_repository", {"full_name": "fork/MusicForge"})):
                self.assertFalse(ci.trusted({**run, key: value}, proof), key)
            for key, value in (("sha", "other"), ("run_id", "11"), ("policy", 0), ("scope", "unknown"),
                               ("created_at", "2020-01-01T00:00:00+00:00"),
                               ("covered_at", "2020-01-01T00:00:00+00:00"), ("environment", {}),
                               ("environment", {**ci.environment(), "ImageVersion": "another-image"})):
                self.assertFalse(ci.trusted(run, {**proof, key: value}), key)

    def test_git_plans_cover_unreleased_work_without_hiding_mixed_changes(self):
        previous = Path.cwd()
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, ENV):
            os.chdir(directory)
            try:
                def git(*args):
                    subprocess.run(["git", "-c", "user.name=CI", "-c", "user.email=ci@example.test", *args],
                                   check=True, capture_output=True)

                def commit(message):
                    git("add", ".")
                    git("commit", "-m", message)
                    return ci.git("rev-parse", "HEAD")

                git("init")
                Path("README.md").write_text("[Design](docs/design.md)\n")
                Path("docs").mkdir()
                Path("docs/design.md").write_text("Design\n")
                commit("baseline")
                git("tag", "v0.2.35")
                Path("backend.go").write_text("package main\n")
                sha = commit("unreleased backend")
                run = {"id": 10, "html_url": "https://example.test/10", "head_sha": sha,
                       "conclusion": "success", "event": "push", "path": ".github/workflows/ci.yml",
                       "head_repository": {"full_name": ENV["GITHUB_REPOSITORY"]}}
                proof = {"policy": ci.POLICY, "sha": sha, "run_id": "10", "scope": "full", "environment": ci.environment(),
                         "baseline": "v0.2.35", "created_at": datetime.now(timezone.utc).isoformat()}
                Path("docs/design.md").write_text("Updated design\n")
                commit("documentation")
                with patch.object(ci, "evidence", return_value=proof):
                    planned = ci.plan(ci.rollback.compare("v0.2.35"), [run])
                    self.assertEqual(planned["scope"], "docs")
                    self.assertEqual(planned["acceptance_base"], sha)
                    self.assertTrue(planned["required"])  # Rollback still compares the published release.
                    ci.check_documents(planned)
                    self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [])["scope"], "full")
                    with patch.object(ci, "evidence", side_effect=OSError("expired")):
                        self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [run])["scope"], "full")
                    Path("web/src").mkdir(parents=True)
                    Path("web/src/style.css").write_text("body { margin: 0; }\n")
                    ui_sha = commit("presentation")
                    self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [run])["scope"], "ui")
                    # A merge with the tested UI tree must retain its UI evidence.
                    ui_run = {**run, "head_sha": ui_sha}
                    ui_proof = {**proof, "sha": ui_sha, "scope": "ui"}
                    git("commit", "--allow-empty", "-m", "merge identical source")
                    with patch.object(ci, "evidence", return_value=ui_proof):
                        planned = ci.plan(ci.rollback.compare("v0.2.35"), [ui_run])
                        self.assertEqual(planned["reuse_run"], "10")
                        self.assertEqual(planned["scope"], "ui")
                        self.assertEqual(planned["acceptance_base"], ui_sha)
                        self.assertEqual(planned["covered_at"], ui_proof["created_at"])
                        self.assertTrue(planned["required"])
                    Path("web/package-lock.json").write_text("{}\n")
                    commit("mixed dependencies")
                    with patch.object(ci, "evidence") as lookup:
                        self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [run])["scope"], "full")
                        lookup.assert_not_called()
                    self.assertEqual(ci.plan(ci.rollback.compare(None), [run])["scope"], "full")
                # Same-commit success can be reused, but the baseline must match.
                proof["sha"] = ci.git("rev-parse", "HEAD")
                run["head_sha"] = proof["sha"]
                with patch.object(ci, "evidence", return_value=proof):
                    self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [run])["reuse_run"], "10")
                    proof["baseline"] = "v0.2.33"
                    self.assertEqual(ci.plan(ci.rollback.compare("v0.2.35"), [run])["reuse_run"], "")
                Path("README.md").write_text("[Missing](docs/missing.md)\n")
                commit("broken documentation link")
                with self.assertRaisesRegex(ValueError, "broken local link"):
                    ci.check_documents({"acceptance_base": sha})
                git("mv", "backend.go", "docs/moved.md")
                commit("rename cannot hide backend removal")
                self.assertEqual(ci.classify(ci.changes(sha)), "full")
            finally:
                os.chdir(previous)

    def test_failed_baseline_lookup_records_full_plan(self):
        previous = Path.cwd()
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {**ENV, "GITHUB_OUTPUT": "output", "GITHUB_STEP_SUMMARY": "summary"}):
            os.chdir(directory)
            try:
                with patch.object(ci, "api", side_effect=OSError("unavailable")), patch.object(ci, "check_documents"):
                    ci.main()
                planned = json.loads(Path(".ci/ci-scope.json").read_text())
                self.assertEqual(planned["scope"], "full")
                self.assertTrue(planned["required"])
                self.assertIn("rollback_required=true", Path("output").read_text())
                self.assertEqual(planned["environment"], ci.environment())
                with patch.dict(os.environ, {"ImageVersion": "unrelated-summary-runner", "VALIDATED_SCOPE": "full", "GITHUB_RUN_ID": "10"}), \
                        patch.object(ci, "git", return_value="abc"):
                    ci.record()
                proof = json.loads(Path(".ci/validation-evidence.json").read_text())
                self.assertEqual(proof["environment"], ci.environment())
            finally:
                os.chdir(previous)


if __name__ == "__main__":
    unittest.main()
