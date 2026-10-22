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
    "v0.2.35": "sha256:9cc368ece8ce6d9386469ccf8a14d5aff41b1ae771edf2d2924d9e59307ddfb8",
    "v0.2.33": "sha256:dd5d74399d4b6afcb1d602a299d1ec62acfe1f11990a74eb4ff4733bbb873029",
    "v0.2.32": "sha256:43c37db7ffa658d89500a8a8df4785b7768a215bf502b120c208ae5bcb672b5f",
    "v0.2.31": "sha256:9cbedabe9900f2023177def997dd03068fb0f2ff183b80cc225e8c17e2d6d557",
    "v0.2.26": "sha256:0a08c8bd29344bef6eb9f3fbd8f0104082763a6b1b31d88bc50f0603d64bb1ce",
    "v0.2.25": "sha256:4b8851a44dbd5150def7dbccd5d26658ef5e1526cc840961d367ae763df3e662",
    "v0.2.24": "sha256:7871768e8e5491b6bfe90cc07560c822cd03f56557fe9db560d0c83df2a8a206",
    "v0.2.23": "sha256:7419b87ce48dd917af7b4c8b6ad73fe5808d23b19fa12f97d037debe850a7dac",
    "v0.2.22": "sha256:fe0dd1d3c10487f716e6979d0787ab59454c386caa930a5b2847d5f5a9955cb2",
    "v0.2.21": "sha256:dd973ba0bcf961aa8316ef56d0b65307e0ff1a6f99c0b9dcfe750677867bc365",
    "v0.2.20": "sha256:7be864ae3641fbe0c6d4192c956f49b431cfe5aef908a391b5b5d1f5fe4f74b1",
    "v0.2.19": "sha256:6055f1a63afc31f99c9c54488995611b91b3ba6451e900231e5d7fa7e7be0017",
    "v0.2.18": "sha256:62b9d4871cd0cf8cf3b0670925d745c00c6ba169c564676c18023c2b6855a389",
    "v0.2.16": "sha256:8d29e7f8e589786eb9ac2c6e0e3b40c43b4fbc8aac4c122ab82f70c40dc20ce7",
    "v0.2.17": "sha256:87c36fd544fa805b45cca66a1052bf1f1030a8553cd961353b00ecdc89cd9b0d",
    "v0.2.15": "sha256:630410106ee4ac8177030e9342ca4533d7e633f59190aa627078fb06b2326842",
    "v0.2.14": "sha256:85cfdf9d9d471b89ade90b965e2831075f6f318a9ee49c8e536734ff01dc6815",
    "v0.2.13": "sha256:e351e2de6da05db882e498f089fff061c593145f07d21e2b98ffc814c692d162",
    "v0.2.12": "sha256:1a56218b0eb03a305715900093500d2e4b7200b944445281f7cce23b80e2dc34",
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


def library_record(track):
    # Optional display annotations are absent in older API responses.
    return {key: value for key, value in track.items() if key not in ("artwork_url", "lyrics", "source_bitrate", "source_audio", "output_size", "lyrics_script")}


def seed_refresh():
    # Seed historical work while stopped; the live API rejects disabled refreshes.
    now = int(time.time())
    with sqlite3.connect(ROOT / "config/musicforge.db") as db:
        return db.execute("INSERT INTO jobs(kind,dedup,args,created,updated) VALUES(?,?,?,?,?)",
                          ("refresh", "nav:refresh", '{"manual":true}', now, now)).lastrowid


def verify(api, expected_tracks, pending, stopped):
    tracks = [library_record(track) for track in api("/api/library")]
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
    (ROOT / "source/Album/01.lrc").write_text("[00:00.00]愛這風雲\n", encoding="utf-8")
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
        wait_for(lambda: api("/api/jobs")["jobs"], lambda rows: all(row["state"] == "success" for row in rows), "completed initial conversion jobs")
        # A format replacement must retain a single owned output and leave state
        # that every published schema-2 reader/writer can reopen unchanged.
        retired_paths = set()
        for old_name, new_name, codec in (("02.mp3", "02.flac", "flac"),
                                          ("03.m4a", "03.wv", "wavpack"),
                                          ("02.flac", "02.opus", "libopus")):
            old_rel, new_rel = "Album/" + old_name, "Album/" + new_name
            previous = next(row for row in tracks if row["path"] == old_rel)
            (ROOT / "source" / old_rel).unlink()
            docker(*media, "-f", "lavfi", "-i", "sine=frequency=880:duration=1", "-c:a", codec, "-metadata", "title=Replaced source format", "/fixture/" + new_rel)
            os.utime(ROOT / "source" / new_rel, (stable, stable))
            retired_paths.add(old_rel)
            count = len(tracks) + 1
            api("/api/library/scan", "POST", {})
            tracks = wait_for(lambda: api("/api/library"),
                              lambda rows: len(rows) == count and all(row["status"] == ("expired" if row["path"] in retired_paths else "ready") for row in rows),
                              "candidate source format replacement " + old_name + " -> " + new_name)
            retired = next(row for row in tracks if row["id"] == previous["id"])
            successor = next(row for row in tracks if row["path"] == new_rel)
            assert not retired["output"] and not retired["output_present"], "retired source still claims the replacement"
            assert successor["output"] == previous["output"] and successor["built_hash"] == successor["hash"], "format replacement did not rebuild the same output"
            assert successor["hash"] == hashlib.sha256((ROOT / "source" / new_rel).read_bytes()).hexdigest(), "format replacement lost complete source identity"
            wait_for(lambda: api("/api/jobs")["jobs"], lambda rows: all(row["state"] == "success" for row in rows), "completed source format replacement jobs")
        lyrics_track = next(row for row in tracks if row["path"] == "Album/01.flac")
        api("/api/lyrics/options", "PUT", {"auto_search": False, "min_score": 98})
        api("/api/lyrics/search", "POST", {"ids": [lyrics_track["id"]]})
        wait_for(lambda: api("/api/lyrics")["report"], lambda row: row["state"] == "complete" and row["found"] == 1, "candidate source LRC publication")
        tracks = sorted((library_record(track) for track in api("/api/library")), key=lambda track: track["id"])
        values = api("/api/settings")["settings"]
        api("/api/lidarr/connection", "PUT", {"url": "http://lidarr.example.test:8686", "api_key": "ci-only-lidarr-key"})
        values.update(enabled=False, nav_url="https://navidrome.example.test", nav_user="admin", nav_password="ci-nav-secret",
                      oidc_issuer="https://issuer.example.test", oidc_client_id="client", oidc_secret="ci-oidc-secret")
        save_settings(api, values)
        stop()
        stopped = seed_refresh()
        api, _ = start(CANDIDATE)
        api("/api/jobs/control", "POST", {"action": "stop", "ids": [stopped]})
        stop()
        pending = seed_refresh()
        api, _ = start(CANDIDATE)
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
            # An inactive historical repair annotation must survive older
            # readers/writers without changing current artifact/API records.
            repair = {"state": "imported", "decoded_samples": 44000, "declared_samples": 44100,
                      "pcm_verified": False, "duration": 44000 / 44100,
                      "diagnostics": "Possibly damaged: rollback annotation fixture"}
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)",
                       ("repair-artifact:" + str(tracks[0]["id"]) + ":" + "0" * 64, json.dumps(repair)))
            history = {"id": "a" * 64, "source_id": tracks[0]["id"], "source_hash": tracks[0]["hash"],
                       "state": "failed", "activity": {"phase": "convert", "path": "Album/01.flac"},
                       "profile": values["encoding"], "progress": 0.4, "created": 1800000000,
                       "updated": 1800000001, "output": "", "log": "Repair history rollback fixture", "stopping": False}
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)", ("repair-task:" + history["id"], json.dumps(history)))
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)", ("repair-member:" + history["id"], history["id"]))
            member = {**history, "id": "b" * 64, "source_id": tracks[1]["id"], "source_hash": tracks[1]["hash"], "state": "stopped"}
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)", ("repair-task:" + member["id"], json.dumps(member)))
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)", ("repair-member:" + member["id"], history["id"]))
            # Search caches are optional schema-2 metadata: older writers must
            # preserve recording identities and downloaded pending previews.
            search = {"version": 1, "fingerprint": "0" * 64, "provider": "lrclib",
                      "query": "Rollback Song", "searched": 1800000000, "instrumental": False,
                      "candidates": [{"candidate": {"index": 0, "provider": "lrclib", "title": "Rollback Song",
                                                   "artist": "Artist", "album": "Album", "duration": 2,
                                                   "high_confidence": True, "match_score": 100, "script": "simplified"},
                                      "text": {"original": "[00:01.000]爱这风云\\n"}}]}
            db.execute("INSERT INTO meta(key,value) VALUES(?,?)",
                       ("lyrics-search:" + str(tracks[0]["id"]) + ":" + "0" * 64, json.dumps(search)))
        expected = snapshot()
        held = next(row for row in expected["jobs"] if row[0] == pending)
        assert held[4] == "pending" and held[8] == 253402300799, "paused task changed schema-2 job semantics"
        assert all(path in expected["output"] for path in ("Album/01.opus", "Album/02.opus", "Album/03.opus", "Album/cover.jpg", "Album/01.lrc")), "mixed audio/cover/LRC fixture missing"
        evidence = []
        for version, digest in LEGACY.items():
            image = "ghcr.io/sagehou/musicforge@" + digest
            docker("pull", image)
            for actual_image in (image, CANDIDATE):
                api, running = start(actual_image)
                if actual_image == image:
                    assert running == version, "wrong rollback release"
                if actual_image == image and version in ("v0.2.31", "v0.2.32"):
                    api("/api/lyrics/options", "PUT", {"auto_search": False})
                    api("/api/lyrics/search", "DELETE")
                verify(api, tracks, pending, stopped)
                if actual_image == CANDIDATE:
                    assert api("/api/lyrics")["options"] == {"auto_search": False, "min_score": 98}, "older option/Stop writer lost lyrics settings"
                    restored_track = next(row for row in api("/api/library") if row["path"] == "Album/01.flac")
                    assert restored_track.get("lyrics_script") == "traditional", "older writer lost script classification"
                    restored_lyrics = next(row for row in api("/api/library") if row["path"] == "Album/01.flac").get("lyrics")
                    assert restored_lyrics and restored_lyrics["state"] == "ready" and restored_lyrics["provider"] == "source", "older writer lost optional lyrics annotation"
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
            assert library_record(restored) == library_record(row), "candidate cannot reopen older worker's completed artifact"
            stop()
            pending_checks.append({"release": version, "pending_verify_then_old_worker_build": "passed", "source_hash": expected_hash})
        Path(".ci/rollback-evidence.json").write_text(json.dumps({"architecture": os.environ["ARCH"], "schema": 2, "source_format_replacement": "passed", "checks": evidence, "pending_verification_checks": pending_checks}, indent=2) + "\n")
    finally:
        subprocess.run(["docker", "rm", "-f", NAME], capture_output=True)


if __name__ == "__main__":
    main()
