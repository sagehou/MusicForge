"""Actions-only acceptance against a real rclone FUSE mount and a stalled WebDAV read."""

import hashlib
import http.client
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import threading
import time
import urllib.parse
import urllib.request


ROOT = Path(".ci/rclone").resolve()
ORIGIN = "http://127.0.0.1:19094"
RELEASE = threading.Event()
BLOCKED = threading.Event()
fault_path = "/36.flac"
TRANSFERRED = {}
TRANSFER_LOCK = threading.Lock()


class Proxy(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def forward(self):
        upstream = http.client.HTTPConnection("127.0.0.1", 19090, timeout=15)
        size = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(size) if size else None
        try:
            upstream.request(self.command, self.path, body=body, headers=dict(self.headers))
            response = upstream.getresponse()
            self.send_response(response.status)
            for key, value in response.getheaders():
                if key.lower() not in ("connection", "transfer-encoding"):
                    self.send_header(key, value)
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            if self.command == "HEAD":
                return
            stalled = self.command == "GET" and urllib.parse.unquote(self.path).endswith(fault_path)
            sent = 0
            while chunk := response.read(64 * 1024):
                key = urllib.parse.unquote(self.path)
                with TRANSFER_LOCK:
                    previous = TRANSFERRED.get(key, 0)
                if stalled and previous >= 256 * 1024 and not RELEASE.is_set():
                    BLOCKED.set()
                    RELEASE.wait(90)
                self.wfile.write(chunk)
                self.wfile.flush()
                sent += len(chunk)
                with TRANSFER_LOCK:
                    TRANSFERRED[key] = TRANSFERRED.get(key, 0) + len(chunk)
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            pass
        finally:
            upstream.close()

    do_GET = do_HEAD = do_PROPFIND = do_OPTIONS = forward


def wait_for(read, accept, timeout=60):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            last = read()
            if accept(last):
                return last
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(0.1)
    raise AssertionError(f"condition timed out: {last}")


jar = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
csrf = ""


def api(path, body=None, method=None):
    request = urllib.request.Request(
        ORIGIN + path, data=None if body is None else json.dumps(body).encode(),
        method=method or ("GET" if body is None else "POST"),
        headers={"Content-Type": "application/json", "Origin": ORIGIN, "X-CSRF-Token": csrf},
    )
    with client.open(request, timeout=5) as response:
        return json.load(response)


def task(job_id):
    return next(job for job in api("/api/jobs")["jobs"] if job["id"] == job_id)


def control(job_id, action):
    return api("/api/jobs/control", {"ids": [job_id], "action": action})


def run():
    global csrf, fault_path
    for name in ("config", "source/Artist/Album", "mount", "output", "cache"):
        (ROOT / name).mkdir(parents=True, exist_ok=True)
    album = ROOT / "source/Artist/Album"
    for duration, path, signal in ((2, album / "01.flac", "sine"), (180, album / "36.flac", "sine")):
        subprocess.run(["ffmpeg", "-v", "error", "-f", "lavfi", "-i", f"{signal}=duration={duration}", "-c:a", "flac", "-metadata", "artist=Remote Artist", "-metadata", "album=Remote Album", "-metadata", "title=Remote track", str(path)], check=True)
    for index in range(2, 41):
        if index not in (2, 3, 36):
            shutil.copyfile(album / "01.flac", album / f"{index:02d}.flac")
    for rel, codec in (("02.mp3", "libmp3lame"), ("03.m4a", "aac")):
        subprocess.run(["ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=2", "-c:a", codec, "-metadata", "artist=Remote Artist", "-metadata", "album=Remote Album", "-metadata", "title=Remote track", str(album / rel)], check=True)
    for path in album.iterdir():
        os.utime(path, (time.time() - 120, time.time() - 120))

    processes = []
    proxy = http.server.ThreadingHTTPServer(("127.0.0.1", 19091), Proxy)
    threading.Thread(target=proxy.serve_forever, daemon=True).start()
    logs = []
    try:
        processes.append(subprocess.Popen(["rclone", "serve", "webdav", str(ROOT / "source"), "--addr", "127.0.0.1:19090", "--dir-cache-time", "1s", "--log-file", str(ROOT / "webdav.log")]))
        remote = ":webdav,url='http://127.0.0.1:19091',vendor='other':"

        def mount(source):
            process = subprocess.Popen(["rclone", "mount", source, str(ROOT / "mount"), "--read-only", "--vfs-cache-mode", "off", "--buffer-size", "0", "--vfs-read-chunk-size", "64k", "--vfs-read-chunk-size-limit", "64k", "--cache-dir", str(ROOT / "cache"), "--dir-cache-time", "1s", "--attr-timeout", "1s", "--poll-interval", "0", "--timeout", "30s", "--low-level-retries", "1", "--rc", "--rc-addr", "127.0.0.1:19093", "--rc-no-auth", "--log-file", str(ROOT / "mount.log")])
            processes.append(process)
            wait_for(lambda: subprocess.run(["mountpoint", "-q", str(ROOT / "mount")], timeout=5).returncode, lambda code: code == 0, timeout=15)
            return process

        def unmount(process):
            subprocess.run(["fusermount3", "-u", str(ROOT / "mount")], check=True, capture_output=True, timeout=5)
            process.wait(timeout=10)

        def source_meta():
            with sqlite3.connect(f"file:{ROOT / 'config/musicforge.db'}?mode=ro", uri=True) as db:
                return dict(db.execute("SELECT key,value FROM meta WHERE key IN ('source_root','source_mount')"))

        mounted = mount(remote)
        app_log = open(ROOT / "app.log", "w")
        logs.append(app_log)
        environment = dict(os.environ, MUSICFORGE_CONFIG_DIR=str(ROOT / "config"), MUSICFORGE_LISTEN="127.0.0.1:19094", MUSICFORGE_PUBLIC_URL=ORIGIN, MUSICFORGE_ALLOWED_ORIGINS="", MUSICFORGE_SOURCE_TIMEOUT_SECONDS="2")
        application = subprocess.Popen([str(Path(".ci/musicforge").resolve())], env=environment, stdout=app_log, stderr=subprocess.STDOUT)
        processes.append(application)
        wait_for(lambda: api("/healthz"), lambda data: data["status"] == "ok", timeout=15)
        records = [json.loads(line) for line in (ROOT / "app.log").read_text().splitlines() if line.startswith("{")]
        code = next(record["setup_code"] for record in records if "setup_code" in record)
        api("/api/auth/setup", {"code": code, "username": "admin", "password": "ci-rclone-password"})
        csrf = api("/api/auth/me")["csrf"]
        settings = api("/api/settings")["settings"]
        settings.update(enabled=True, source=str(ROOT / "mount"), output=str(ROOT / "output"), scan_minutes=60)
        api("/api/settings", settings, "PUT")
        root = wait_for(lambda: api("/api/jobs")["jobs"], lambda jobs: any(job["kind"] == "scan" for job in jobs))[0]["id"]
        reading = wait_for(lambda: task(root), lambda job: BLOCKED.is_set() and any(activity["phase"] == "read" and activity["path"].endswith("36.flac") and activity.get("read_bytes", 0) > 0 for activity in job["current"]), timeout=45)
        assert BLOCKED.is_set(), "fixture did not stall an actual remote GET"
        response_times = []
        for path in ("/api/jobs", "/api/library", "/api/dashboard", "/healthz"):
            started = time.monotonic()
            api(path)
            response_times.append(round(time.monotonic() - started, 3))
        assert max(response_times) < 1, response_times
        control(root, "pause")
        wait_for(lambda: api(f"/api/jobs/{root}/items")["items"], lambda items: any(item["kind"] == "scan" and item["log"] == "Paused by administrator" for item in items), timeout=5)
        assert task(root)["attempts"] == 0, "pause consumed a failure attempt"
        for _ in range(5):
            assert api("/api/library/scan", {"dirs": ["Artist/Album"]})["job_id"] == root, "active queue created another scan task"
        assert api("/api/jobs")["total"] == 1, "repeated requests split the stalled queue"
        RELEASE.set()
        control(root, "resume")
        wait_for(lambda: task(root), lambda job: job["state"] == "success" and job["counts"]["done"] == 40, timeout=90)
        indexed = api("/api/library")
        assert len(indexed) == 40 and all(source["status"] == "ready" for source in indexed)
        assert {Path(source["path"]).suffix for source in indexed} == {".flac", ".mp3", ".m4a"}
        for source in indexed:
            assert source["hash"] == hashlib.sha256((ROOT / "source" / source["path"]).read_bytes()).hexdigest()

        fault_path = "/41.flac"
        BLOCKED.clear()
        RELEASE.clear()
        shutil.copyfile(album / "36.flac", album / "41.flac")
        os.utime(album / "41.flac", (time.time() - 120, time.time() - 120))
        # Both the WebDAV server and mount have VFS directory caches.
        time.sleep(1.1)
        subprocess.run(["rclone", "rc", "vfs/forget", "--url", "http://127.0.0.1:19093"], check=True, capture_output=True)
        second = api("/api/library/scan", {})["job_id"]
        sources = wait_for(lambda: api("/api/library"), lambda sources: any(source["path"].endswith("41.flac") and source["error"].startswith("Source read unavailable: ") for source in sources), timeout=30)
        assert BLOCKED.is_set(), "timeout fixture did not reach remote storage"
        assert all(source["status"] == "ready" for source in sources if not source["path"].endswith("41.flac"))
        wait_for(lambda: task(second), lambda job: job["attempts"] == 1 and job["counts"]["pending"] > 0, timeout=5)
        control(second, "pause")
        RELEASE.set()
        control(second, "resume")
        wait_for(lambda: task(second), lambda job: job["state"] == "success", timeout=30)
        assert all(source["status"] == "ready" for source in api("/api/library"))
        single = ROOT / "source/Artist/Single"
        single.mkdir()
        shutil.copyfile(album / "36.flac", single / "42.flac")
        os.utime(single / "42.flac", (time.time() - 120, time.time() - 120))
        time.sleep(1.1)
        subprocess.run(["rclone", "rc", "vfs/forget", "--url", "http://127.0.0.1:19093"], check=True, capture_output=True)
        third = api("/api/library/scan", {"dirs": ["Artist/Single"]})["job_id"]
        wait_for(lambda: task(third), lambda job: job["state"] == "success", timeout=30)
        with TRANSFER_LOCK:
            single_bytes = sum(count for path, count in TRANSFERRED.items() if path.endswith("/42.flac"))
        single_size = (single / "42.flac").stat().st_size
        assert single_bytes >= single_size and single_bytes <= single_size + 512 * 1024, (single_size, single_bytes)
        assert not list((ROOT / "config/source-staging").iterdir()), "source scratch files leaked"

        # A real unmount must never turn a known remote library into an empty one.
        before = source_meta()
        witness = json.loads(before["source_mount"])
        assert witness["identity"] == before["source_root"] and len(witness["signature"]) == 64
        before_outputs = {path.relative_to(ROOT / "output").as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in (ROOT / "output").rglob("*") if path.is_file()}
        unmount(mounted)
        wait_for(lambda: api("/api/dashboard"), lambda data: not data["online"] and data["storage_message"].startswith("Source mount changed;"), timeout=15)
        fourth = api("/api/library/scan", {})["job_id"]
        wait_for(lambda: task(fourth), lambda job: job["state"] == "pending" and job.get("log", "").startswith("Source mount changed;"), timeout=10)
        assert all(source["present"] for source in api("/api/library")), "missing mount expired known sources"
        assert source_meta() == before, "missing mount overwrote source acknowledgment"

        # A different remote subdirectory is not a reconnect, even on this path.
        mounted = mount(remote + "Artist")
        time.sleep(5.1)
        wait_for(lambda: api("/api/dashboard"), lambda data: not data["online"] and data["storage_message"].startswith("Source mount changed;"), timeout=15)
        assert source_meta() == before, "wrong remote was automatically acknowledged"
        unmount(mounted)

        # The same named remote survives new device IDs without a Settings save.
        mounted = mount(remote)
        wait_for(lambda: api("/api/dashboard"), lambda data: data["online"], timeout=15)
        after = source_meta()
        assert after["source_root"] != before["source_root"], "fixture did not change root identity"
        assert json.loads(after["source_mount"])["signature"] == witness["signature"], "same remote changed stable signature"
        wait_for(lambda: task(fourth), lambda job: job["state"] == "success" and job["attempts"] == 0, timeout=45)
        after_outputs = {path.relative_to(ROOT / "output").as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in (ROOT / "output").rglob("*") if path.is_file()}
        assert after_outputs == before_outputs, "reconnect modified playable output"
        assert all(source["status"] == "ready" for source in api("/api/library"))
        application.terminate()
        application.wait(timeout=20)
        unmount(mounted)
        mounted = mount(remote)
        application = subprocess.Popen([str(Path(".ci/musicforge").resolve())], env=environment, stdout=app_log, stderr=subprocess.STDOUT)
        processes.append(application)
        wait_for(lambda: api("/api/dashboard"), lambda data: data["online"], timeout=15)
        restarted = source_meta()
        assert restarted["source_root"] != after["source_root"], "restart fixture did not change root identity"
        assert json.loads(restarted["source_mount"])["signature"] == witness["signature"], "restart lost stable witness"
        wait_for(lambda: api("/api/jobs")["jobs"], lambda jobs: all(job["state"] == "success" for job in jobs), timeout=45)
        assert all(source["status"] == "ready" for source in api("/api/library"))
        evidence = {"source_reconnect": {"missing_mount_blocked": True, "wrong_remote_blocked": True, "same_remote_resumed_without_settings_save": True, "restart_reconnect_without_settings_save": True, "device_identity_changed": True, "outputs_unchanged": True, "attempts": 0}, "coalesced_scan_requests": 5, "vfs_cache_mode": "off", "single_pass_source_bytes": single_size, "single_pass_remote_bytes": single_bytes, "metadata_overhead_budget": 512 * 1024, "rclone_version": subprocess.check_output(["rclone", "version"], text=True).splitlines()[0], "tracks": 42, "source_formats": ["flac", "mp3", "m4a"], "read_bytes_before_pause": max(activity.get("read_bytes", 0) for activity in reading["current"]), "api_response_seconds_while_stalled": response_times, "pause_attempts": 0, "timeout_attempts_before_recovery": 1, "full_content_hash_preserved": True}
        (ROOT / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
        print(json.dumps(evidence))
    finally:
        RELEASE.set()
        for process in reversed(processes):
            process.terminate()
            try:
                process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        subprocess.run(["fusermount3", "-u", str(ROOT / "mount")], capture_output=True, timeout=5)
        proxy.shutdown()
        for log in logs:
            log.close()
        path = ROOT / "app.log"
        if path.exists():
            path.write_text("\n".join(line for line in path.read_text().splitlines() if '"setup_code"' not in line) + "\n")


if __name__ == "__main__":
    run()
