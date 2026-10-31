"""Run in Actions: skip full rollback only for known non-persistent changes."""

import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request


def baseline_release(releases, current_tag):
    tags = [release["tag_name"] for release in releases
            if not release["draft"] and not release["prerelease"]
            and re.fullmatch(r"v0\.2\.\d+", release["tag_name"])
            and release["tag_name"] != current_tag]
    return max(tags, key=lambda tag: int(tag.rsplit(".", 1)[1]), default=None)


def presentation_or_test(path):
    # allowlist known non-persistent paths; everything else runs rollback.
    return (path.startswith(("web/src/", "web/public/", "web/tests/"))
            or path == "web/index.html"
            or path.endswith(".md") and ("/" not in path or path.startswith("docs/"))
            or path.startswith(("cmd/", "internal/")) and path.endswith("_test.go"))


def compare(baseline):
    result = {"required": True, "baseline": baseline, "reason": "no published stable baseline"}
    if not baseline:
        return result
    base_ref = "refs/tags/" + baseline
    if subprocess.run(["git", "merge-base", "--is-ancestor", base_ref, "HEAD"],
                      capture_output=True, check=False).returncode != 0:
        result["reason"] = "published baseline missing or not an ancestor"
        return result
    # Disable rename detection so moving backend code into an allowed path cannot skip.
    raw = subprocess.check_output(["git", "diff", "--no-renames", "--name-only", "-z",
                                   base_ref, "HEAD", "--"], text=True, stderr=subprocess.PIPE)
    changed = raw.rstrip("\0").split("\0") if raw else []
    candidates = [path for path in changed if not presentation_or_test(path)]
    result.update(required=bool(candidates), changed_files=changed, persistence_candidates=candidates,
                  reason="potential persistence changes" if candidates else "only presentation, documentation or tests changed")
    return result


def main():
    try:
        request = urllib.request.Request(
            "https://api.github.com/repos/" + os.environ["GITHUB_REPOSITORY"] + "/releases?per_page=100",
            headers={"Accept": "application/vnd.github+json", "User-Agent": "MusicForge-rollback-scope",
                     "Authorization": "Bearer " + os.environ["GITHUB_TOKEN"]})
        with urllib.request.urlopen(request, timeout=15) as response:
            releases = json.load(response)
        current_tag = os.environ.get("GITHUB_REF_NAME", "") if os.environ.get("GITHUB_REF_TYPE") == "tag" else ""
        result = compare(baseline_release(releases, current_tag))
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
        result = {"required": True, "baseline": None, "reason": "baseline lookup failed; full validation required"}
    Path(".ci").mkdir(exist_ok=True)
    Path(".ci/rollback-scope.json").write_text(json.dumps(result, indent=2) + "\n")
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write("required=" + str(result["required"]).lower() + "\n")
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
        summary.write("Full rollback validation: **" + ("required" if result["required"] else "skipped")
                      + "**. Baseline: `" + (result["baseline"] or "unavailable") + "`. " + result["reason"] + ".\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
