"""Actions-only acceptance planning and successful-run evidence; fail closed."""

from datetime import datetime, timezone
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import zipfile


spec = importlib.util.spec_from_file_location("rollback_scope", Path(__file__).with_name("check-rollback-scope.py"))
rollback = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rollback)
POLICY = 1


def git(*args):
    return subprocess.check_output(["git", *args], text=True, stderr=subprocess.PIPE).strip()


def documentation(path):
    return path.endswith(".md") and ("/" not in path or path.startswith("docs/"))


def classify(paths):
    if all(documentation(path) for path in paths):
        return "docs"
    # TSX is provisional: the TypeScript AST gate must also pass before using UI.
    if all(documentation(path) or path.startswith("web/src/") and path.endswith((".css", ".tsx"))
           or path in ("web/src/locales/en.json", "web/src/locales/zh-CN.json", "web/public/favicon.svg")
           for path in paths):
        return "ui"
    return "full"


def changes(base):
    raw = subprocess.check_output(["git", "diff", "--no-renames", "--name-only", "-z", base, "HEAD", "--"], text=True)
    return raw.rstrip("\0").split("\0") if raw else []


def environment():
    return {key: os.environ.get(key, "") for key in ("ImageOS", "ImageVersion", "RUNNER_OS", "RUNNER_ARCH")}


def api(path):
    request = urllib.request.Request("https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"] + path,
                                     headers={"Accept": "application/vnd.github+json", "User-Agent": "MusicForge-CI-scope",
                                              "Authorization": "Bearer " + os.environ["GITHUB_TOKEN"]})
    with urllib.request.urlopen(request, timeout=15) as response:
        return json.load(response)


def evidence(run):
    artifacts = api(f"/actions/runs/{run['id']}/artifacts?per_page=100")["artifacts"]
    artifact = next((item for item in artifacts if item["name"] == "validation-evidence" and not item["expired"]), None)
    if not artifact:
        return None
    # API redirect authentication must not reach the signed artifact storage URL.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, request, fp, code, message, headers, newurl):
            return None

    request = urllib.request.Request("https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"]
                                     + f"/actions/artifacts/{artifact['id']}/zip",
                                     headers={"Authorization": "Bearer " + os.environ["GITHUB_TOKEN"],
                                              "User-Agent": "MusicForge-CI-scope"})
    try:
        urllib.request.build_opener(NoRedirect()).open(request, timeout=15)
        return None
    except urllib.error.HTTPError as redirect:
        if redirect.code != 302:
            raise
        location = redirect.headers["Location"]
    parsed = urllib.parse.urlsplit(location)
    if parsed.scheme != "https" or not parsed.hostname or not parsed.hostname.endswith((".blob.core.windows.net", ".actions.githubusercontent.com")):
        return None
    with urllib.request.urlopen(location, timeout=15) as response:
        with zipfile.ZipFile(io.BytesIO(response.read())) as archive:
            return json.loads(archive.read("validation-evidence.json"))


def trusted(run, proof):
    if (not proof or run.get("conclusion") != "success" or run.get("event") not in ("push", "workflow_dispatch")
            or run.get("head_repository", {}).get("full_name") != os.environ["GITHUB_REPOSITORY"]
            or run.get("path") != ".github/workflows/ci.yml" or proof.get("policy") != POLICY
            or proof.get("sha") != run.get("head_sha") or proof.get("run_id") != str(run["id"])
            or proof.get("environment") != environment() or not all(environment().values())
            or proof.get("scope") not in ("docs", "ui", "full")):
        return False
    age = (datetime.now(timezone.utc) - datetime.fromisoformat(proof.get("covered_at", proof["created_at"]))).total_seconds()
    return 0 <= age <= 7 * 24 * 60 * 60


def plan(result, runs):
    """The published baseline governs rollback; full CI evidence may cover inherited work."""
    scope = classify(result.get("changed_files", [])) if "changed_files" in result else "full"
    if scope == "full" and any(path.startswith(("web/src/", "web/public/")) or path == "web/index.html"
                               for path in result.get("changed_files", [])):
        result.update(required=True, reason="frontend executable or uncertain changes may affect persisted settings")
    result.update(scope=scope, acceptance_base="refs/tags/" + result["baseline"] if "changed_files" in result else "",
                  reuse_run="", covered_by="", covered_at="")
    if "changed_files" not in result:
        return result
    for run in runs:
        try:
            proof = evidence(run)
            if not trusted(run, proof):
                continue
            sha = proof["sha"]
            if sha == git("rev-parse", "HEAD") and proof.get("baseline") == result["baseline"]:
                result.update(scope=proof["scope"], reuse_run=str(run["id"]), covered_by=run["html_url"],
                              covered_at=proof.get("covered_at", proof["created_at"]))
                return result
            if proof["scope"] != "full":
                continue
            if subprocess.run(["git", "merge-base", "--is-ancestor", sha, "HEAD"], capture_output=True).returncode:
                continue
            paths = changes(sha)
            if classify(paths) != "full":
                result.update(scope=classify(paths), acceptance_base=sha, covered_by=run["html_url"],
                              covered_at=proof.get("covered_at", proof["created_at"]))
                return result
        except (OSError, ValueError, KeyError, TypeError, zipfile.BadZipFile, subprocess.CalledProcessError):
            continue  # No usable proof: retain all checks selected from the published release.
    return result


def check_documents(result):
    base = result.get("acceptance_base")
    subprocess.run(["git", "diff", "--check", base or "HEAD^", "HEAD", "--"], check=True)
    paths = changes(base) if base else git("ls-files").splitlines()
    for name in filter(documentation, paths):
        path = Path(name)
        if not path.is_file():
            continue
        # Check repository links, ignoring examples in fenced code and external URLs.
        content = re.sub(r"```.*?```", "", path.read_text(), flags=re.S)
        for target in re.findall(r"\[[^\]]*\]\(([^)\s]+)(?:\s+[^)]*)?\)", content):
            target = urllib.parse.unquote(target.strip("<>").split("#", 1)[0])
            if not target or urllib.parse.urlsplit(target).scheme or target.startswith("//"):
                continue
            linked = Path(target.lstrip("/")) if target.startswith("/") else path.parent / target
            if not linked.exists():
                raise ValueError(f"{name}: broken local link: {target}")


def record():
    result = json.loads(Path(".ci/ci-scope.json").read_text())
    proof = {"policy": POLICY, "sha": git("rev-parse", "HEAD"), "run_id": os.environ["GITHUB_RUN_ID"],
             "created_at": datetime.now(timezone.utc).isoformat(), "environment": environment(),
             "scope": os.environ["VALIDATED_SCOPE"], "baseline": result["baseline"],
             "acceptance_base": result["acceptance_base"], "covered_by": result["covered_by"],
             "covered_at": result["covered_at"] or datetime.now(timezone.utc).isoformat()}
    Path(".ci/validation-evidence.json").write_text(json.dumps(proof, indent=2) + "\n")


def main():
    try:
        releases = api("/releases?per_page=100")
        tag = os.environ.get("GITHUB_REF_NAME", "") if os.environ.get("GITHUB_REF_TYPE") == "tag" else ""
        result = rollback.compare(rollback.baseline_release(releases, tag))
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
        result = {"required": True, "baseline": None, "reason": "baseline lookup failed; full validation required"}
    try:
        runs = api("/actions/workflows/ci.yml/runs?status=success&per_page=20")["workflow_runs"]
    except (OSError, ValueError, KeyError, TypeError):
        runs = []
    result = plan(result, runs)
    Path(".ci").mkdir(exist_ok=True)
    Path(".ci/rollback-scope.json").write_text(json.dumps(result, indent=2) + "\n")
    Path(".ci/ci-scope.json").write_text(json.dumps(result, indent=2) + "\n")
    check_documents(result)
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        for key in ("scope", "acceptance_base", "reuse_run"):
            output.write(key + "=" + result[key] + "\n")
        output.write("rollback_required=" + str(result["required"]).lower() + "\n")
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
        summary.write(f"Acceptance plan: **{result['scope']}**. Published baseline: `{result['baseline'] or 'unavailable'}`. "
                      f"Full rollback: **{'required' if result['required'] else 'skipped'}** ({result['reason']}).\n\n")
        if result["covered_by"]:
            summary.write(f"Successful evidence: {result['covered_by']}. "
                          + ("Same commit: reuse application checks.\n" if result["reuse_run"] else
                             f"Inherited work covered; inspect all changes after `{result['acceptance_base']}`.\n"))
        summary.write("Version tags always build and check both final native images.\n")
    print(json.dumps(result))


if __name__ == "__main__":
    record() if sys.argv[1:] == ["--record"] else main()
