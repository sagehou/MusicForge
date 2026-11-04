# Deployment and operations

**English** · [简体中文](deployment.zh-CN.md)

Use the [README](../README.md#get-started-with-docker-compose) for first startup. This guide covers the parts that depend on your host, storage and other services. Choose a published stable version from [Releases](https://github.com/sagehou/MusicForge/releases); the host only needs to pull images, not build them.

## Storage and permissions

- Keep `/config` on local storage suitable for SQLite WAL. Its owner must match `PUID`; startup enforces mode `0700`. It holds accounts, credentials, the library index, jobs and recovery records.
- Mount the source read-only and a dedicated output read-write. The output must be empty on first setup. None of these roots may contain another, and Compose refuses missing host paths.
- The configured UID/GID needs source read access and output/config write access. Navidrome should read the same physical output through a read-only mount. Its container path may differ.
- Leave room for output and temporary files. Source staging shares a default 4 GiB limit under `/config/source-staging`; one source must fit within that limit, with at least 64 MiB left for configuration writes. Staging is removed after success, failure or cancellation and cleaned on startup.
- Output storage must support hard links for safe LRC creation. Source-tree symlinks pause scanning; use real directories or mount paths.

Once indexing begins, container root paths cannot be changed in Settings. Move storage by changing host mounts while keeping the container paths. The storage workbench can check mounts, verify selected output checksums and preview a migration checklist; it does not copy data or apply new paths.

## Reverse proxy and access origins

The default Compose port is `127.0.0.1:8787`. A host-process Nginx/Caddy can use `http://127.0.0.1:8787` upstream. Set `MUSICFORGE_PUBLIC_URL` to the public origin, including its scheme and optional port, with no subpath. Preserve the request Host and proxy all site paths. MusicForge handles authentication; proxy identity headers do not grant access.

A containerized proxy should share a Docker network and use `http://musicforge:8787`; `127.0.0.1` inside that container refers to the proxy itself. For an existing external network named `proxy`, add `compose.proxy.yml`:

```yaml
services:
  musicforge:
    networks:
      - proxy
networks:
  proxy:
    external: true
```

Join the proxy to that network too, then use both files: `docker compose -f docker-compose.yml -f compose.proxy.yml up -d`.

For more than one domain, set these values in `.env`:

```dotenv
MUSICFORGE_PUBLIC_URL=https://musicforge.example.com
MUSICFORGE_ALLOWED_ORIGINS=https://musicforge.home.example.com,https://musicforge-alt.example.com
```

The primary origin is automatically allowed. Configure DNS, proxying and HTTPS for each domain. Wildcards and paths are rejected; use only one scheme per hostname. Each domain has its own host-only login cookie. Browser writes must match both the allowed origin and Host and still require CSRF protection. Forwarded host/protocol headers cannot add origins.

OIDC started on another domain redirects to the primary URL and stays there after login. Register only the primary callback and bind the identity while logged in locally on that primary origin. Recreate the container after editing startup settings. Test login, Settings save and logout on each configured origin.

Set `MUSICFORGE_TRUSTED_PROXIES` to the proxy IP CIDRs **as seen by MusicForge**, separated by commas. A host proxy may appear as the Docker bridge gateway. Pin its address or use an isolated network; do not trust `0.0.0.0/0` or `::/0`. The proxy must overwrite `X-Forwarded-For` with the real client address or append the actual connecting address (Nginx: `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`). Never forward a client-supplied value unchanged.

MusicForge walks that chain from the trusted connection peer to the first untrusted address. Without trusted proxies, clients behind the proxy share a rate-limit budget. Local login/setup allow ten credential failures in fifteen minutes and reset on success; OIDC starts have a separate budget. Confirm that one client's failed attempts do not block another through your proxy.

## Connect Lidarr and Navidrome

In **Settings → Integrations → Lidarr**, save a container-reachable URL and API key for file-list comparison. A blank key preserves it for the same URL; clearing the URL clears the connection. **Library → File counts · compare with Lidarr** compares indexed paths without downloading audio or changing Lidarr. Results are snapshots; “unindexed” does not prove a file is missing from disk. Re-scan selected difference directories after checking the path mapping.

For automatic imports, create a native **Webhook** connection in Lidarr:

| Field | Value |
| --- | --- |
| URL | `https://musicforge.example.com/api/webhook/lidarr` |
| Username | `musicforge` |
| Password | The independent webhook secret saved in MusicForge |
| Events | Release import and upgrade |

Set **Lidarr source path prefix** if Lidarr sees a different absolute source root. Mapped paths must stay inside the MusicForge source root. The connection test creates no work. Import tasks wait for the notified tracks to appear and finish conversion, including across restarts; a persistent wait shows the expected source path. Ordinary imports keep expired output. Only upgrade events authorize cleanup of their explicitly replaced files, after replacements validate.

For Navidrome, save its URL, username, password and library ID. Its library root must correspond to this output directory. Refresh uses saved settings and coalesces changed directories; pending refreshes survive restart. Navidrome 0.59.0+ receives targeted scans, older versions regular scans. **Refresh Navidrome manually** is useful for checking the connection. URLs must be reachable from inside MusicForge; `localhost` means this container. Use HTTPS across untrusted networks. Authentication uses Subsonic salted tokens, not plaintext passwords in query parameters.

## OIDC and password recovery

The local administrator can optionally bind one OIDC identity:

1. Set the primary HTTPS origin in `MUSICFORGE_PUBLIC_URL`.
2. Save the provider's exact issuer URL, client ID and secret in Settings.
3. Register `https://musicforge.example.com/api/auth/oidc/callback` with the provider.
4. While signed in locally on that primary origin, choose **Bind your OIDC identity** and complete the provider flow.

Only the bound `issuer + sub` can sign in. Keep the local password for recovery. Rebinding revokes previous OIDC sessions while retaining local access. Leaving a secret blank preserves it; use the explicit deletion option after clearing the relevant URL to erase a Navidrome password or OIDC secret. OIDC deletion requires local login and clears the binding and OIDC sessions.

To reset a forgotten password, stop the app and pass the new password through stdin using the same image and `/config` volume. Run in Bash:

```bash
docker compose stop musicforge
read -rs -p 'New password: ' new_password
printf '%s\n' "$new_password" | docker compose run --rm -T musicforge -reset-password-stdin
unset new_password
docker compose up -d
```

Use any Compose overrides from your deployment in these commands too. Passwords require 12–72 bytes. Recovery needs exclusive access to `/config` and revokes all sessions.

## Optional NetEase lyrics login

Enable NetEase and choose its priority in Settings → Lyrics. NetEase first uses an anonymous session, reused in memory for up to ten days and renewed after expiry; restart creates a new session. No account is required by default. Platform or regional restrictions can still block guest access.

If NetEase requires an account, sign in and complete any phone/account verification on the official NetEase website or app. Obtain your own logged-in website request's `Cookie` header and save its value as one line in a private `netease.cookie` file, for example `MUSIC_U=<your login value>; __csrf=<your csrf value>`. The file must contain a nonempty `MUSIC_U`, stay within 16 KiB, and contain no `Cookie:` prefix, browser-export JSON or Netscape cookie-file format. MusicForge does not perform password/SMS login or bypass platform verification. The MusicForge administrator account is separate from this provider login.

For the default UID/GID, prepare the file on the host (adjust both IDs to your `PUID`/`PGID`):

```bash
sudo install -d -m 0700 -o 10001 -g 10001 /srv/musicforge/secrets
sudo install -m 0600 -o 10001 -g 10001 /dev/null /srv/musicforge/secrets/netease.cookie
sudoedit /srv/musicforge/secrets/netease.cookie
```

Add `NETEASE_SECRETS_DIR=/srv/musicforge/secrets` to `.env`, then use the optional [Compose override](../docker-compose.netease.yml):

```bash
docker compose -f docker-compose.yml -f docker-compose.netease.yml config --quiet
docker compose -f docker-compose.yml -f docker-compose.netease.yml up -d
```

Use both files for subsequent Compose commands. The override mounts that directory read-only and sets `MUSICFORGE_NETEASE_COOKIE_FILE=/run/secrets/musicforge/netease.cookie`. Other deployment systems can set this environment variable to any absolute, container-readable secret-file path. The Cookie itself never belongs in `.env`, Settings, source control or shared logs. Keep its directory outside the library/output roots and protect backups as credentials. No default secret mount is needed for anonymous access.

The application rereads the file for each NetEase operation and never writes it. Replace its contents privately, preserving owner/permissions; the directory mount also makes atomic replacement visible. A changed Cookie clears the credential cooldown on the next lookup without restarting; HTTP `Retry-After` limits still apply. An unreadable or invalid configured file pauses NetEase rather than silently using a different identity. Login expiry/rejection or account verification is reported for this provider, with a thirty-minute cooldown (one minute for rejected guest sessions); guest sessions renew on a later retry. These errors preserve existing lyrics and the other providers' results. Complete verification and refresh the file when prompted; re-run the track's lyrics search afterward. Guest credentials are memory-only, and login Cookies are never saved to the database or returned by APIs.

## Runtime configuration

Optional `/config/config.json` follows [config.example.json](../config.example.json). Environment variables override it; library, encoding and integration settings are saved through the UI in SQLite.

| Variable | Default / purpose |
| --- | --- |
| `MUSICFORGE_CONFIG_DIR` | `/config` |
| `MUSICFORGE_LISTEN` | `:8787` |
| `MUSICFORGE_PUBLIC_URL` | Primary browser/OIDC origin |
| `MUSICFORGE_ALLOWED_ORIGINS` | Additional origins, comma-separated |
| `MUSICFORGE_TRUSTED_PROXIES` | Proxy IP CIDRs for client-IP rate limiting |
| `MUSICFORGE_LOG_LEVEL` | `INFO`; structured JSON logs |
| `MUSICFORGE_FFMPEG` / `MUSICFORGE_FFPROBE` | `ffmpeg` / `ffprobe` |
| `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` | `120`; source no-progress timeout, 1–3600 seconds |
| `MUSICFORGE_STAGING_MAX_BYTES` | `4294967296`; shared source scratch limit in bytes |
| `MUSICFORGE_NETEASE_COOKIE_FILE` | Optional absolute path to a read-only secret file |

In `config.json`, additional origins use an `allowed_origins` array. An empty `MUSICFORGE_ALLOWED_ORIGINS` clears that array; remove the Compose environment entry to use the file value. Startup values are shown read-only in Settings. Recreate the container after changing them.

## Remote mounts and troubleshooting

Establish rclone/FUSE mounts before starting MusicForge and grant its UID read access; `--allow-other` may be needed. Keep config and output local. MusicForge scans tags/attributes, then copies each selected source once into bounded local staging while calculating SHA-256. Encoding and embedded artwork reuse that seekable copy. Metadata probing may still read headers or seek to the tail. Unchanged scans skip audio reads; VFS full caching is optional.

A named `fuse.rclone` source records a witness of the remote and effective directory. Reconnecting the same source can acknowledge changed device/inode IDs automatically, including across restart. Other filesystems, generic `--devname` labels, changed sources and unwitnessed mounts use strict identity checks. If a changed-mount notice remains, inspect the real directory **inside the container** before saving Settings to acknowledge it. Docker's private binds may require container recreation after a host remount. Never acknowledge an unmounted empty directory.

Jobs show source-read bytes, encoding progress and capacity/schedule waits. A source read stalled for 120 seconds times out; for a slower remote, adjust `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` and recreate the container. Individual failures retain existing output and allow healthy tracks to continue. Incomplete scans never expire unseen sources; whole-mount outages defer work without spending track retries. Each ordinary track gets an initial attempt and two automatic retries before waiting for manual retry. A new attempt may reread the source.

Pause stops active encoding and holds unfinished work across restarts; resume restarts the current track. Stop preserves completed audio and requires an explicit retry. Deleting finished/stopped history removes records, not audio, and may allow a later scan to retry a previously suppressed target. Best-effort repair and interrupted manual maintenance do not restart automatically.

For a problem report, record the image version, task ID, path and time, then inspect `docker compose logs --since 10m musicforge`. Source reads, API failures and slow requests include stage/path diagnostics. Do not share credentials or setup codes. Logs rotate across three 10 MB files with the example Compose file.

`/healthz` checks database liveness; Overview separately reports storage availability. A protected API returns 401 for an invalid/expired session and 503 with `Retry-After: 1` for temporary session-storage failures. Temporary errors retain loaded pages and drafts; only confirmed expiry opens reauthentication. Library browsing filters and sorts on the server, returning 50 tracks or groups per page. Conditional requests return 304 when unchanged, and a known previous page can receive only changed entries. The server caches the annotated index in memory; database writes rebuild that index. Opening the maintenance workbench still loads its full scope. Browser caches clear on logout. These improvements do not establish a maximum library capacity.

A permanently blocked kernel storage call may need host mount recovery. Do not remove `.musicforge`, the database or old playable files as a repair attempt.

## Try your deployment

Use one small album in a separate test source/config/output set before pointing at the full library:

1. Complete setup, log out/in and check both container health and storage availability.
2. Convert with concurrency 1 and the default profile. Check tags and `cover.jpg`, play in Navidrome, then scan again: unchanged tracks should be skipped.
3. Delete a source **only in this test library**. The output should remain playable as expired. Delete its expired output explicitly and refresh Navidrome; only then should it disappear.
4. Change a profile and start a rebuild. Restart during queued work and check that completed tracks are kept and pending ordinary jobs continue.
5. Try your Lidarr import/upgrade, proxy client-IP handling and optional OIDC login. Automated fixtures cannot verify your credentials, mount behavior or service configuration.

Current automated coverage and its limits are recorded in [acceptance](acceptance.md).

## Backup and restore

These commands are available from v0.3.0. Stop the app and prepare a backup directory outside config, writable by your container UID/GID, such as `./backups`:

```sh
docker compose stop musicforge
mkdir -p backups
# Set the owner of backups to the PUID/PGID from .env.
docker compose run --rm --no-deps -v "$PWD/backups:/backups" musicforge -backup /backups/config.zip
docker compose run --rm --no-deps -v "$PWD/backups:/backups:ro" musicforge -check-backup /backups/config.zip
```

Commands print the application version, schema, backup time, instance ID and library paths. The archive includes the database, stored credentials and edit history from `/config`, excluding staging files. Existing ZIPs are never overwritten. Checking reads the entire archive and validates SQLite without changing the current config. Treat backups as credentials. Source/output music, `.env` and separately mounted secrets need their own backups. Capture the matching output snapshot while the app is still stopped, then start it again.

For recovery, prepare a **new empty config directory** with the same UID/GID. Replace the host path below with that directory:

```sh
CONFIG_DIR=/srv/musicforge/restored-config docker compose run --rm --no-deps \
  -v "$PWD/backups:/backups:ro" musicforge -restore-backup /backups/config.zip
```

Restore validates the archive and database first and refuses nonempty destinations. Your original config stays available, so you can rehearse in the new directory. Check the reported version and library paths; restore the matching stopped output snapshot for complete state recovery, then set `CONFIG_DIR` in `.env` to the restored directory and start the app. Restoring config alone does not undo file replacements or deletions. The backup command also reads compatible v0.2.x schema-2 configs while the old application is stopped.

## Upgrade and rollback

Stop MusicForge, back up all of `/config` and record the previous image version. For complete library-state rollback, snapshot the matching output while still stopped. Keep an independent source backup. Change `MUSICFORGE_VERSION`, then run `docker compose pull` and `docker compose up -d` with your usual overrides.

The `0.2.x` and `0.3.x` series use schema 2 and preserve published persistence formats and meanings. A same-series **software rollback** can reuse the current configuration: disable background work, stop the app, pin the earlier image, then check accounts, library and jobs. Older versions may not understand newer optional maintenance features; keep background work disabled until returning to a supporting release. Never lower SQLite `user_version` manually. Version 0.1 requires its matching pre-upgrade backup.

A software rollback keeps the current library state. Only restoring matching stopped config/output snapshots restores the pre-upgrade state; changing images cannot undo completed replacements, moves or deletions. Published version tags and images are never overwritten for rollback.

When the persistence surface changes, CI exercises the latest published compatible schema-2 image on the candidate's same volumes, then reopens with the candidate, on both architectures. The optional `historical_rollback` dispatch also covers all pinned historical releases. `rollback-scope` records the baseline and decision; architecture artifacts record the round trip. Documentation/presentation-only changes can skip it when the complete persistence surface is unchanged. Version tags still require a current dependency audit and both final native image checks, while eligible successful application evidence can be reused. See [AGENTS.md](../AGENTS.md) for the exact gates.
