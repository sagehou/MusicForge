# Production trial guide

**English** · [Simplified Chinese](deployment.zh-CN.md)

This guide targets v0.2.3. Installation, builds and automated validation run in GitHub Actions; the production host pulls published images. See the [acceptance report](acceptance.md) for verified behavior.

## Before deployment

- Pin `MUSICFORGE_VERSION=v0.2.3` in `.env` so a trial does not change with `latest`.
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
4. Delete a FLAC only from the test source library. After scanning it should be expired, while output and Navidrome playback remain. Manually delete its expired artifact in MusicForge and refresh: only then should the playback entry disappear.
5. Change an encoding parameter: the artifact should need rebuilding until manually started. Its validated replacement retires the old file. Restart the container and confirm successful tracks are skipped while pending work continues.
6. For OIDC, retain the local recovery password, bind an identity and test login in another browser session. Rebinding revokes previous OIDC sessions while retaining local sessions. Test your Lidarr connection, one import and one upgrade.

Automation exercises real ffmpeg, Navidrome, Chromium and both native image architectures. Lidarr tests use native payloads and OIDC tests use a signed test provider. Your deployed Lidarr, Authentik/other OIDC provider, reverse proxy and storage combination still needs the acceptance above. The library API currently returns the complete index and the UI polls every five seconds; large-library capacity has not been load-tested, so observe memory and response time during initial import. Symlinks within the source tree pause scanning; use real directories or mount paths.

## Upgrade and rollback

Stop MusicForge and back up all of `/config`. For complete library-state rollback, also snapshot the corresponding output directory and record the previous image version while MusicForge remains stopped. Keep an independent FLAC backup.

Schema 2 is the stable MVP baseline. The `0.2.x` series preserves the database structure and persisted Settings, jobs and recovery formats. v0.2.0/v0.2.1/v0.2.2 → v0.2.3 does not migrate the schema. A same-series software rollback can reuse the current `/config`: pause background work, stop the application, pin the earlier image, then verify accounts, library and jobs before resuming. Do not change database version numbers to perform a rollback.

The CI step `Same-schema rollback with published releases` uses digest-pinned v0.2.2/v0.2.1/v0.2.0 images on AMD64 and ARM64. They read the candidate's real audio, cover, credentials and pending task, save settings, then return to the candidate; database and artifact state are compared. Successful runs produce `rollback-amd64` / `rollback-arm64` evidence artifacts. Equal schema numbers are only one condition; older readers and writers must also remain compatible.

Software rollback retains the current library state. Restoring the full pre-upgrade state requires matching `/config` and output snapshots. Switching images cannot undo completed replacements, moves or deletions.

Version 0.1 cannot read schema 2 and requires its matching pre-upgrade backup. Future incompatible persistence changes require a clearly marked breaking release with upgrade backups and a CI-verified restore-and-rollback path. Do not manually lower `user_version`; this does not convert data structures or recover deleted data.

If a problem occurs, disable background work in Settings and retain logs/mount state. Do not delete `.musicforge`, the database or old playable artifacts as a repair attempt. A changed-source-mount notice requires checking the real mount before acknowledging it by saving Settings. See the README for password recovery.
