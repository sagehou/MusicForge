"""Run only in Actions: exercise older releases on the candidate's real volumes."""

import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import sqlite3
import struct
import subprocess
import time
import urllib.error
import urllib.request
import zlib


ROOT = Path(".ci/rollback").resolve()
ORIGIN = "http://127.0.0.1:18788"
NAME = "musicforge-rollback"
CANDIDATE = "musicforge:validation"
PASSWORD = "ci-rollback-password"
LEGACY = {
    "v0.2.11": "sha256:dce503fb6f8b75566a479295ccd3027fb9cee2e6ee19efe0cfc9bf52be223c29",
    "v0.2.10": "sha256:5f94b457bcbb208c269f4a9b1b5eef685c707e8bd6c0496fb74f7cabed8514ad",
    "v0.2.9": "sha256:cf3a93e67bfda5754c7bf2395fd092e8aefa4b8b758bb4b3099e617c6f9a0097",
    "v0.2.8": "sha256:665db2d6230cede949c75a7734076dce15f0a04a2aa8eea9c2da93c2f6152fa7",
    "v0.2.7": "sha256:0a9b9addba11189c4a5057b581e73004dc2315bb819ac25e7f15bb05f7964639",
    "v0.2.6": "sha256:c480adec16045489b3859f38af2a61e2e19b3eae0fb6f915c771b369c6c532c4",
    "v0.2.5": "sha256:3dee84549033b769450b599b4b1b12481927f4dcab56fe15055f811e29e30e49",
    "v0.2.4": "sha256:3af1f8f70fcc208fe8ae293da2a78e3e92288edddd8b4c3807a01b5a47903241",
    "v0.2.3": "sha256:94afc5a3b0db22fbd563623b193376677bde460b4561e68afee7f00c372d70a4",
    "v0.2.2": "sha256:fd7f6045a7f31dfe2c1a853cf3004377ebef3cebba4cb3a0b488b925fae28d8d",
    "v0.2.1": "sha256:39a12fbd9db368d4dc688cb929ae74b731ba10ae863b03aec915be3a77d35240",
    "v0.2.0": "sha256:3a176c2fa79dd3e2a47c53803e3d5ac055e9c406061ec69402b63fe4523a2e7c",
}


def docker(*args):
    result = subprocess.run(["docker", *args], capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError("docker " + args[0] + " failed: " + result.stderr.strip())
    return result.stdout


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


def start(image, staging_limit=None):
    extra = [] if staging_limit is None else ["-e", "MUSICFORGE_STAGING_MAX_BYTES=" + str(staging_limit)]
    docker("run", "-d", "--name", NAME, "--user", f"{os.getuid()}:{os.getgid()}", *extra,
           "-p", "127.0.0.1:18788:8787", "-e", "MUSICFORGE_PUBLIC_URL=" + ORIGIN,
           "-e", "MUSICFORGE_ALLOWED_ORIGINS=http://localhost:18788",
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


def save_settings(api, values):
    # Read-only APIs can observe published audio before the worker releases its
    # file lock. Retry only the documented Settings conflict, not arbitrary errors.
    deadline = time.monotonic() + 10
    while True:
        try:
            return api("/api/settings", "PUT", values)
        except urllib.error.HTTPError as error:
            detail = json.load(error) if error.code == 409 else {}
            if error.code != 409 or detail.get("error") != "library file operation in progress; try saving Settings again shortly":
                raise
            if time.monotonic() >= deadline:
                raise RuntimeError("Settings file operation remained busy") from error
            time.sleep(0.1)


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
    result["runtime_config"] = (ROOT / "config/config.json").read_text()
    for directory in ("source", "output"):
        result[directory] = {str(path.relative_to(ROOT / directory)): hashlib.sha256(path.read_bytes()).hexdigest()
                             for path in sorted((ROOT / directory).rglob("*")) if path.is_file()}
    return result


def verification_queued(rel):
    # Multiple scans share preparation; its original task remains the owner.
    with sqlite3.connect(f"file:{ROOT / 'config/musicforge.db'}?mode=ro", uri=True) as db:
        return db.execute("SELECT 1 FROM jobs WHERE kind='scan' AND dedup LIKE 'scan:prepare:%' AND json_extract(args,'$.dirs[0]')=? AND json_extract(args,'$.verify')=1 LIMIT 1", (rel,)).fetchone() is not None


def verify(api, expected_tracks, pending, stopped):
    tracks = api("/api/library")
    assert sorted(tracks, key=lambda track: track["id"]) == expected_tracks, "library/artifact records changed"
    settings = api("/api/settings")
    assert settings["configured"] == {"webhook": True, "nav_password": True, "oidc_secret": True}, "saved credentials lost"
    assert not settings["settings"]["enabled"], "rollback must keep background work paused"
    jobs = api("/api/jobs?state=all")["jobs"]
    assert any(job["id"] == pending and job["args"] == {"manual": True} and job["state"] in ("paused", "pending") for job in jobs), "paused task lost"
    assert any(job["id"] == stopped and job["state"] in ("failed", "stopped") and job["log"] == "Stopped by administrator" and job["attempts"] == 0 for job in jobs), "stopped task or failure count lost"
    # Exercise an older writer, including its handling of redacted secret fields.
    values = settings["settings"]
    interval = values["scan_minutes"]
    for minutes in (interval + 1, interval):
        values["scan_minutes"] = minutes
        save_settings(api, values)
    assert api("/api/settings")["configured"] == settings["configured"], "settings save erased credentials"


def main():
    for directory in ("config", "source/Album", "output"):
        (ROOT / directory).mkdir(parents=True, exist_ok=True)
    (ROOT / "config/config.json").write_text(json.dumps({"public_url": ORIGIN, "allowed_origins": ["http://localhost:18788"], "source_timeout_seconds": 120}) + "\n")
    media = ("run", "--rm", "--user", f"{os.getuid()}:{os.getgid()}", "-v", f"{ROOT / 'source'}:/fixture", "--entrypoint", "ffmpeg", CANDIDATE, "-nostdin", "-v", "error", "-y")
    docker(*media, "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "flac", "-metadata", "artist=Rollback Artist", "-metadata", "album=Rollback Album", "-metadata", "title=Rollback Track", "/fixture/Album/01.flac")
    for rel, codec in (("02.mp3", "libmp3lame"), ("03.m4a", "aac")):
        docker(*media, "-f", "lavfi", "-i", "sine=duration=1", "-c:a", codec, "-metadata", "artist=Rollback Artist", "-metadata", "album=Rollback Album", "-metadata", "title=" + rel, "/fixture/Album/" + rel)
    # The release media tools omit the color filter; a standard-library PNG is sufficient.
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    header = struct.pack(">IIBBBBB", 32, 32, 8, 2, 0, 0, 0)
    pixels = (b"\x00" + b"\x2c\xa0\xb4" * 32) * 32
    cover = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(pixels)) + chunk(b"IEND", b"")
    (ROOT / "source/Album/cover.png").write_bytes(cover)
    stable = time.time() - 120
    for rel in ("01.flac", "02.mp3", "03.m4a"):
        os.utime(ROOT / "source/Album" / rel, (stable, stable))
    try:
        api, _ = start(CANDIDATE)
        values = api("/api/settings")["settings"]
        values["enabled"] = True
        values["webhook_secret"] = "ci-rollback-webhook-secret-24-plus"
        save_settings(api, values)
        tracks = wait_for(lambda: api("/api/library"), lambda rows: len(rows) == 3 and all(row["status"] == "ready" for row in rows), "candidate mixed-source conversion")
        tracks.sort(key=lambda track: track["id"])
        wait_for(lambda: api("/api/jobs")["jobs"], lambda rows: all(row["state"] == "success" for row in rows), "completed conversion jobs")
        values = api("/api/settings")["settings"]
        values.update(enabled=False, nav_url="https://navidrome.example.test", nav_user="admin", nav_password="ci-nav-secret",
                      oidc_issuer="https://issuer.example.test", oidc_client_id="client", oidc_secret="ci-oidc-secret")
        save_settings(api, values)
        stopped = api("/api/navidrome/refresh", "POST", {})["job_id"]
        api("/api/jobs/control", "POST", {"action": "stop", "ids": [stopped]})
        pending = api("/api/navidrome/refresh", "POST", {})["job_id"]
        api("/api/jobs/control", "POST", {"action": "pause", "ids": [pending]})
        verify(api, tracks, pending, stopped)
        stop()
        # Optional read counters must survive older readers and Settings writers too.
        with sqlite3.connect(ROOT / "config/musicforge.db") as db:
            key, raw = db.execute("SELECT m.key,m.value FROM meta m JOIN jobs j ON m.key='task-progress:'||j.id WHERE j.kind='scan' ORDER BY j.id LIMIT 1").fetchone()
            activity = json.loads(raw)
            activity.update(read_bytes=(ROOT / "source/Album/01.flac").stat().st_size,
                            read_total_bytes=(ROOT / "source/Album/01.flac").stat().st_size)
            db.execute("UPDATE meta SET value=? WHERE key=?", (json.dumps(activity), key))
            # Retain the optional rclone witness through old readers/writers while
            # work is disabled. Enabling this local fixture resets it normally.
            identity = db.execute("SELECT value FROM meta WHERE key='source_root'").fetchone()[0]
            witness = {"identity": identity, "signature": hashlib.sha256(b"rollback-remote:Library").hexdigest()}
            db.execute("INSERT INTO meta(key,value) VALUES('source_mount',?)", (json.dumps(witness),))
        expected = snapshot()
        held = next(row for row in expected["jobs"] if row[0] == pending)
        assert held[4] == "pending" and held[8] == 253402300799, "paused task changed schema-2 job semantics"
        assert all(path in expected["output"] for path in ("Album/01.opus", "Album/02.opus", "Album/03.opus", "Album/cover.jpg")), "mixed audio/cover fixture missing"
        evidence = []
        for version, digest in LEGACY.items():
            image = "ghcr.io/sagehou/musicforge@" + digest
            docker("pull", image)
            for actual_image in (image, CANDIDATE):
                api, running = start(actual_image)
                if actual_image == image:
                    assert running == version, "wrong rollback release"
                verify(api, tracks, pending, stopped)
                stop()
                actual = snapshot()
                changed = [key for key in expected if actual[key] != expected[key]]
                assert not changed, "rollback changed persisted state: " + ", ".join(changed)
            evidence.append({"release": version, "manifest": digest, "image_id": docker("image", "inspect", "--format", "{{.Id}}", image).strip(), "round_trip": "passed"})
            print("Passed: candidate -> " + version + " -> candidate on the same config/source/output", flush=True)
        pending_checks = []
        for version, digest in LEGACY.items():
            rel = "Pending/" + version + ".flac"
            (ROOT / "source/Pending").mkdir(exist_ok=True)
            docker(*media, "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "flac", "-metadata", "title=Metadata indexed before content verification", "/fixture/" + rel)
            os.utime(ROOT / "source" / rel, (stable, stable))
            expected_hash = hashlib.sha256((ROOT / "source" / rel).read_bytes()).hexdigest()
            api, _ = start(CANDIDATE, staging_limit=1)
            values = api("/api/settings")["settings"]
            values.update(enabled=True, nav_url="")
            save_settings(api, values)
            api("/api/library/scan", "POST", {"dirs": ["Pending"]})
            wait_for(lambda: verification_queued(rel), bool, "durable compatible verification request")
            rows = wait_for(lambda: api("/api/library"), lambda rows: any(row["path"] == rel and row["hash"] == "" and row["title"] == "Metadata indexed before content verification" for row in rows), "tag-only pending row")
            values["enabled"] = False
            save_settings(api, values)
            stop()
            # Simulate a stopped deployment after metadata indexing, before its
            # compatible full-verification job has finished. No empty-hash build exists.
            with sqlite3.connect(ROOT / "config/musicforge.db") as db:
                assert not db.execute("SELECT 1 FROM jobs WHERE kind IN ('convert','move') AND json_extract(args,'$.hash')='' LIMIT 1").fetchone()
                db.execute("UPDATE jobs SET state='pending',attempts=0,not_before=0,log='' WHERE dedup LIKE 'scan:prepare:%' AND json_extract(args,'$.dirs[0]')=? AND state<>'success'", (rel,))
            api, running = start("ghcr.io/sagehou/musicforge@" + digest)
            assert running == version
            values = api("/api/settings")["settings"]
            values["enabled"] = True
            save_settings(api, values)
            row = next(row for row in wait_for(lambda: api("/api/library"), lambda rows: any(row["path"] == rel and row["status"] == "ready" for row in rows), "older worker completes pending verification") if row["path"] == rel)
            assert row["hash"] == row["built_hash"] == expected_hash, "old worker lost complete content identity"
            values["enabled"] = False
            save_settings(api, values)
            stop()
            api, _ = start(CANDIDATE)
            restored = next(row for row in api("/api/library") if row["path"] == rel)
            assert restored == row, "candidate cannot reopen older worker's completed artifact"
            stop()
            pending_checks.append({"release": version, "pending_verify_then_old_worker_build": "passed", "source_hash": expected_hash})
        Path(".ci/rollback-evidence.json").write_text(json.dumps({"architecture": os.environ["ARCH"], "schema": 2, "checks": evidence, "pending_verification_checks": pending_checks}, indent=2) + "\n")
    finally:
        subprocess.run(["docker", "rm", "-f", NAME], capture_output=True)


if __name__ == "__main__":
    main()
