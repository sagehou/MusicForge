# Production trial guide

**English** · [Simplified Chinese](deployment.zh-CN.md)

This guide targets v0.2.6. Installation, builds and automated validation run in GitHub Actions; the production host pulls published images. See the [acceptance report](acceptance.md) for verified behavior.

## Before deployment

- Pin `MUSICFORGE_VERSION=v0.2.6` in `.env` so a trial does not change with `latest`.
- Keep `/config` on local host storage, owned by `PUID`, with mode `0700`. It contains accounts, OIDC/Navidrome secrets, the index and jobs. Do not share it publicly. Startup enforces private directory permissions.
- `FLAC_DIR` must exist and is mounted read-only; `OUTPUT_DIR` must exist, be writable and initially empty. Use a dedicated output directory. None of the three roots may contain another. Compose refuses missing host paths.
- Verify source read access and output write access for the configured UID/GID. Give Navidrome read-only access to the same output. Reserve disk space for both output and temporary files.
- Set `MUSICFORGE_PUBLIC_URL` to the primary HTTPS origin, without a subpath. List additional origins in `MUSICFORGE_ALLOWED_ORIGINS`. Proxy all site paths for every domain and preserve Host.

See the [README](../README.md) for directory creation and startup. Check `docker compose config --quiet`, then run `docker compose pull` and `docker compose up -d`. Container logs rotate across at most three 10 MB files. The first-start setup code creates the administrator; do not publish those logs.

For multiple domains, follow the [runtime configuration example](../README.md#runtime-configuration). Test local login, saving Settings and logout on each origin; cookies remain separate. OIDC started on an additional origin redirects to the primary URL and stays there after login. Bind the identity while logged in locally at the primary URL. HTTPS-cookie behavior uses the configured origin, even when TLS terminates at the proxy. Changes to the allowlist require container recreation.

## Reverse proxy networking

A host-process Nginx/Caddy can use `http://127.0.0.1:8787` upstream. The default Compose file binds only the host loopback address.

A containerized proxy should share a Docker network with MusicForge and use `http://musicforge:8787` upstream. Inside the proxy container, `127.0.0.1` refers to that proxy. For an existing external network named `proxy`, create `compose.proxy.yml`:

```yaml
services:
  musicforge:
    networks:
      - proxy
networks:
  proxy:
    external: true
```

Join the proxy to that network too, then run `docker compose -f docker-compose.yml -f compose.proxy.yml up -d`. Adjust the network name to your deployment. Retain MusicForge local/OIDC authentication; the proxy terminates HTTPS and forwards requests. Navidrome URLs must also be reachable from the container; `localhost` means MusicForge itself.

Configure `MUSICFORGE_TRUSTED_PROXIES` with the proxy IP CIDR(s) as seen by MusicForge, for example `172.20.0.2/32`; separate multiple IPv4/IPv6 CIDRs with commas. A host-process proxy may appear as the Docker bridge gateway rather than `127.0.0.1`. Pin the proxy address or restrict the trusted range to its isolated network. The proxy must overwrite `X-Forwarded-For` with the actual client IP or append the actual connecting IP to the existing chain (Nginx: `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`). Never forward a client-supplied value unchanged or trust `0.0.0.0/0` / `::/0`. MusicForge walks the chain from the trusted connection peer to the first untrusted hop. Without this setting, headers are ignored and clients behind the same proxy share a failure budget. This setting identifies an IP for rate limiting; it does not enable Forward Auth or header-based identity.

Local login/setup count credential failures only, reset on success and allow ten failures per fifteen-minute fixed window. Their budgets and the OIDC start budget are separate. OIDC callback success resets its start budget; existing callbacks and authenticated binding are not blocked by public start exhaustion. Verify that repeated successful logins work and one test client's failures do not block another client through your proxy.

## Initial acceptance

For a small album trial, use a separate temporary `/config`, dedicated output and test source library. Root paths cannot change through the UI after indexing. Deploy the full library with its own configuration/output, without reusing a temporary instance's ownership marker.

1. Confirm `docker compose ps` reports healthy. Complete setup over public HTTPS, log out and log in. Health checks cover the database; separately confirm source/output storage is online in Overview.
2. Keep concurrency at 1 and default Opus VBR 192 kbps. Convert an album, check tags and the single `cover.jpg`, then scan again: no new conversion jobs should appear.
3. Play a converted track in Navidrome. Save integration settings and manually refresh; confirm the job succeeds and tracks appear.
4. Delete a source audio file only from the test source library. After scanning it should be expired, while output and Navidrome playback remain. Manually delete its expired artifact in MusicForge and refresh: only then should the playback entry disappear.
5. Change an encoding parameter: the artifact should need rebuilding until manually started. Its validated replacement retires the old file. Restart the container and confirm successful tracks are skipped while pending work continues.
6. For OIDC, retain the local recovery password, bind an identity and test login in another browser session. Rebinding revokes previous OIDC sessions while retaining local sessions. Test your Lidarr connection, one import and one upgrade.

Automation exercises real ffmpeg, Navidrome, Chromium and both native image architectures. Lidarr tests use native payloads and OIDC tests use a signed test provider. Your deployed Lidarr, Authentik/other OIDC provider, reverse proxy and storage combination still needs the acceptance above. The library API currently returns the complete index and the UI polls every five seconds; large-library capacity has not been load-tested, so observe memory and response time during initial import. Symlinks within the source tree pause scanning; use real directories or mount paths.

## Task controls and progress

A scan and its track queue occupy one Jobs entry. New scans/rebuilds carry grouping annotations; pre-upgrade history remains individually controllable rather than guessing historical relationships. Open Details for scope, scan counts, profiles, individual results and diagnostics. The Library starts at artists, then albums, then tracks; search/status filters narrow these groups, and selecting a group selects its matching tracks. Output paths keep the source-relative hierarchy.

Pause stops active encoders and holds unfinished work across restarts. Resume starts unfinished tracks from the beginning. Stop leaves completed audio in place and requires explicit retry; retry-all skips deliberately stopped tasks. Delete history requires the queue to be finished or stopped and its workers to have exited. It removes task records only. Deleting failed/stopped history removes that target’s scan-retry suppression, so a later scan may create it again.

Jobs refresh every two seconds and show the current path, artist/album/title, track encoding percentage and queue counts. Container logs emit `job progress` records with `task`, `job`, `phase`, `path`, `title`, `processed`, `total` and `percent`. Encoding reports at most once per second. Discovery has an unknown total until enumeration finishes; artwork/validation are separate phases. Retries may return a track’s percentage to zero.

Schema 2 remains unchanged. Task grouping/control/progress use optional metadata keys, retaining the original executable jobs and arguments. Older images retain held `pending` jobs through their future `not_before` timestamp and show stopped work as `failed` with a cancellation message; their UI has individual jobs and lacks Resume. Resume through v0.2.6 when reopening. Snapshot comparison includes task annotations and the held timestamp.

## Upgrade and rollback

Stop MusicForge and back up all of `/config`. For complete library-state rollback, also snapshot the corresponding output directory and record the previous image version while MusicForge remains stopped. Keep an independent source-library backup.

Schema 2 is the stable MVP baseline. The `0.2.x` series preserves the database structure and persisted Settings, jobs and recovery formats. v0.2.0/v0.2.1/v0.2.2/v0.2.3/v0.2.4/v0.2.5 → v0.2.6 does not migrate the schema. A same-series software rollback can reuse the current `/config`: pause background work, stop the application, pin the earlier image, then verify accounts, library and jobs before resuming. Do not change database version numbers to perform a rollback.

The CI step `Same-schema rollback with published releases` uses digest-pinned v0.2.5/v0.2.4/v0.2.3/v0.2.2/v0.2.1/v0.2.0 images on AMD64 and ARM64. They read the candidate's FLAC/MP3/M4A records, real audio, cover, credentials and pending task, save settings, then return to the candidate; database and artifact state are compared. Successful runs produce `rollback-amd64` / `rollback-arm64` evidence artifacts. Equal schema numbers are only one condition; older readers and writers must also remain compatible.

Software rollback retains the current library state. Restoring the full pre-upgrade state requires matching `/config` and output snapshots. Switching images cannot undo completed replacements, moves or deletions.

Version 0.1 cannot read schema 2 and requires its matching pre-upgrade backup. Future incompatible persistence changes require a clearly marked breaking release with upgrade backups and a CI-verified restore-and-rollback path. Do not manually lower `user_version`; this does not convert data structures or recover deleted data.

If a problem occurs, disable background work in Settings and retain logs/mount state. Do not delete `.musicforge`, the database or old playable artifacts as a repair attempt. A changed-source-mount notice requires checking the real mount before acknowledging it by saving Settings. See the README for password recovery.

## Mixed audio libraries

Use the same read-only source mount for supported audio formats; the existing `FLAC_DIR` Compose variable may point to a mixed library. See the [supported source formats](../README.md#supported-source-audio). After upgrading, run a scan to discover files that older versions skipped. The UI reports audio-source counts and displays each track's extension.

The schema-2 storage formats remain unchanged. Versions through v0.2.5 retain mixed-source records, artifacts and job arguments when reopening and saving settings with background work disabled. Their scanner still discovers only FLAC; if resumed, it can mark non-FLAC sources expired while retaining output. Some older media-tool builds also lack new input decoders. Keep background work disabled during a feature rollback, then use the newer release to resume mixed-source work. A downgrade does not add new format support to older versions.

## Rclone mounts and stall diagnostics

Follow the [README mount guidance](../README.md#mounted-remote-source-libraries) and establish the host mount before container startup. Tag scanning finishes first; workers copy selected sources into `/config/source-staging`, computing full SHA-256 while copying, and encode the local copies. Reserve local disk space for the default 4 GiB aggregate limit. VFS full caching is optional. Ordinary scans still use size/mtime for incremental checks. After a remount, verify the container's view and root identity before acknowledging it in Settings.

Jobs show read/total MiB. Structured `job progress` adds optional `read_bytes`/`read_total_bytes` byte counters; existing `processed`/`total` remain file counts and `percent` remains 0–100. These fields are diagnostics, not recovery records. The default no-progress timeout is 120 seconds. Set `MUSICFORGE_SOURCE_TIMEOUT_SECONDS=300` in `.env` and recreate the container for a slower remote. Pause/stop cancel source-reading child processes; resume restarts the current track.

Individual read failures appear in Library while other tracks continue through indexing and stay in the same task. The scan retries twice after a complete pass, then waits for manual retry. Whole-mount outages defer work. Incomplete scans never expire unseen sources. Overview reads background storage-check results and explicitly shows when checking is still underway.

Record the task ID, source path, time and HTTP status when reporting an error. `docker compose logs --since 10m musicforge` includes `source read failed`, `API request failed` and `slow API request` with paths/stages/diagnostics. Request bodies, passwords and OIDC callback query parameters are not logged. HTML or empty proxy responses produce an HTTP-status message while existing task data remains visible.

Actions adds actual rclone WebDAV/FUSE acceptance with VFS caching disabled: mix FLAC/MP3/M4A, stall the 36th download in a 40-track library, check byte progress, API response time, pause/resume and whole-file hashes, then test a new-track timeout and recovery. A separate successful build transfers its 2,781,108-byte source plus only 65,536 bytes for metadata, and leaves no scratch files. Evidence is uploaded as `rclone-acceptance`. A permanently blocked kernel storage call may require host mount recovery; the application bounds unreaped source helper processes.
