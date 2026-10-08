"""Run only in Actions: exercise older releases on the candidate's real volumes."""

import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request


ROOT = Path(".ci/rollback").resolve()
ORIGIN = "http://127.0.0.1:18788"
NAME = "musicforge-rollback"
CANDIDATE = "musicforge:validation"
PASSWORD = "ci-rollback-password"
LEGACY = {
    "v0.2.1": "sha256:39a12fbd9db368d4dc688cb929ae74b731ba10ae863b03aec915be3a77d35240",
    "v0.2.0": "sha256:3a176c2fa79dd3e2a47c53803e3d5ac055e9c406061ec69402b63fe4523a2e7c",
}


def docker(*args):
    return subprocess.run(["docker", *args], check=True, capture_output=True, text=True).stdout


def wait_for(read, ready, description):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        try:
            result = read()
            if ready(result):
                return result
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            pass
        time.sleep(0.5)
    raise RuntimeError("Timed out: " + description)


def start(image):
    docker("run", "-d", "--name", NAME, "--user", f"{os.getuid()}:{os.getgid()}",
           "-p", "127.0.0.1:18788:8787", "-e", "MUSICFORGE_PUBLIC_URL=" + ORIGIN,
           "-v", f"{ROOT / 'config'}:/config", "-v", f"{ROOT / 'source'}:/music/source:ro",
           "-v", f"{ROOT / 'output'}:/music/output", image)
    cookies = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
    csrf = ""

    def api(path, method="GET", body=None):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(ORIGIN + path, data=data, method=method,
                                         headers={"Content-Type": "application/json", "X-CSRF-Token": csrf})
        with client.open(request, timeout=10) as response:
            return json.load(response)

    wait_for(lambda: api("/healthz"), lambda value: value["status"] == "ok", image + " startup")
    me = api("/api/auth/me")
    credentials = {"username": "admin", "password": PASSWORD}
    if me["initialized"]:
        api("/api/auth/login", "POST", credentials)
    else:
        logs = docker("logs", NAME)
        code = next(json.loads(line)["setup_code"] for line in logs.splitlines() if '"setup_code"' in line)
        api("/api/auth/setup", "POST", {**credentials, "code": code})
    me = api("/api/auth/me")
    assert me["authenticated"] and me["username"] == "admin", "administrator cannot log in"
    csrf = me["csrf"]
    return api, me["version"]


def stop():
    docker("stop", "-t", "20", NAME)
    docker("rm", NAME)


def snapshot():
    # Stop first so WAL and files represent the same completed application state.
    with sqlite3.connect(f"file:{ROOT / 'config/musicforge.db'}?mode=ro", uri=True) as db:
        assert db.execute("PRAGMA user_version").fetchone()[0] == 2, "0.2.x schema contract changed"
        assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok", "database integrity failure"
        tables = ("meta", "settings", "admin", "sources", "managed", "jobs", "migrations", "dirty_dirs", "oidc_flows")
        result = {table: db.execute(f"SELECT * FROM {table} ORDER BY 1").fetchall() for table in tables}
        result["schema"] = db.execute("SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name").fetchall()
    for directory in ("source", "output"):
        result[directory] = {str(path.relative_to(ROOT / directory)): hashlib.sha256(path.read_bytes()).hexdigest()
                             for path in sorted((ROOT / directory).rglob("*")) if path.is_file()}
    return result


def verify(api, expected_track, pending):
    tracks = api("/api/library")
    assert len(tracks) == 1 and tracks[0] == expected_track, "library/artifact record changed"
    settings = api("/api/settings")
    assert settings["configured"] == {"webhook": True, "nav_password": True, "oidc_secret": True}, "saved credentials lost"
    assert not settings["settings"]["enabled"], "rollback must keep background work paused"
    jobs = api("/api/jobs?state=pending")["jobs"]
    assert any(job["id"] == pending and job["args"] == {"manual": True} for job in jobs), "pending task lost"
    # Exercise an older writer, including its handling of redacted secret fields.
    values = settings["settings"]
    interval = values["scan_minutes"]
    for minutes in (interval + 1, interval):
        values["scan_minutes"] = minutes
        api("/api/settings", "PUT", values)
    assert api("/api/settings")["configured"] == settings["configured"], "settings save erased credentials"


def main():
    for directory in ("config", "source/Album", "output"):
        (ROOT / directory).mkdir(parents=True, exist_ok=True)
    media = ("run", "--rm", "--user", f"{os.getuid()}:{os.getgid()}", "-v", f"{ROOT / 'source'}:/fixture", "--entrypoint", "ffmpeg", CANDIDATE, "-nostdin", "-v", "error", "-y")
    docker(*media, "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "flac", "-metadata", "artist=Rollback Artist", "-metadata", "album=Rollback Album", "-metadata", "title=Rollback Track", "/fixture/Album/01.flac")
    docker(*media, "-f", "lavfi", "-i", "color=c=teal:s=32x32:d=0.1", "-frames:v", "1", "/fixture/Album/cover.jpg")
    stable = time.time() - 120
    os.utime(ROOT / "source/Album/01.flac", (stable, stable))
    try:
        api, _ = start(CANDIDATE)
        values = api("/api/settings")["settings"]
        values["enabled"] = True
        values["webhook_secret"] = "ci-rollback-webhook-secret-24-plus"
        api("/api/settings", "PUT", values)
        track = wait_for(lambda: api("/api/library"), lambda rows: len(rows) == 1 and rows[0]["status"] == "ready", "candidate conversion")[0]
        wait_for(lambda: api("/api/jobs")["jobs"], lambda rows: all(row["state"] == "success" for row in rows), "completed conversion jobs")
        values = api("/api/settings")["settings"]
        values.update(enabled=False, nav_url="https://navidrome.example.test", nav_user="admin", nav_password="ci-nav-secret",
                      oidc_issuer="https://issuer.example.test", oidc_client_id="client", oidc_secret="ci-oidc-secret")
        api("/api/settings", "PUT", values)
        pending = api("/api/navidrome/refresh", "POST", {})["job_id"]
        verify(api, track, pending)
        stop()
        expected = snapshot()
        assert "Album/01.opus" in expected["output"] and "Album/cover.jpg" in expected["output"], "audio/cover fixture missing"
        evidence = []
        for version, digest in LEGACY.items():
            image = "ghcr.io/sagehou/musicforge@" + digest
            docker("pull", image)
            for actual_image in (image, CANDIDATE):
                api, running = start(actual_image)
                if actual_image == image:
                    assert running == version, "wrong rollback release"
                verify(api, track, pending)
                stop()
                actual = snapshot()
                changed = [key for key in expected if actual[key] != expected[key]]
                assert not changed, "rollback changed persisted state: " + ", ".join(changed)
            evidence.append({"release": version, "manifest": digest, "image_id": docker("image", "inspect", "--format", "{{.Id}}", image).strip(), "round_trip": "passed"})
            print("Passed: candidate -> " + version + " -> candidate on the same config/source/output", flush=True)
        Path(".ci/rollback-evidence.json").write_text(json.dumps({"architecture": os.environ["ARCH"], "schema": 2, "checks": evidence}, indent=2) + "\n")
    finally:
        subprocess.run(["docker", "rm", "-f", NAME], capture_output=True)


if __name__ == "__main__":
    main()
