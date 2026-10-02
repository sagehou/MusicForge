# MusicForge

**English** · [Simplified Chinese](README.zh-CN.md)

MusicForge builds a streaming edition of your lossless music collection. Lidarr manages the FLAC masters, MusicForge generates Opus or MP3 with ffmpeg, and Navidrome serves the output. Incremental builds process only tracks that need work.

```text
Lidarr FLAC library (read-only)
              ↓
MusicForge · SQLite · ffmpeg
              ↓
Opus or MP3 library (read-write)
              ↓
Navidrome (read-only)
```

One container, one administrator, one source library and one output library. Images are published at `ghcr.io/sagehou/musicforge` for `linux/amd64` and `linux/arm64`.

## Features

- Index FLAC files and metadata in SQLite; convert new or changed tracks.
- Move byte-identical renamed files without re-encoding.
- Preserve tags and ReplayGain; store album artwork once as `cover.jpg`.
- Validate replacements before retiring playable output.
- Recover durable background jobs after restart; retry failures automatically.
- Receive Lidarr import/upgrade webhooks and refresh Navidrome after build or deletion batches.
- Use English or Simplified Chinese, local administrator login and optional native OIDC.

Cloud sync, distributed workers, multiple libraries, multiple users and plugins are outside the MVP scope. See the [design baseline](docs/mvp-design.en.md) for the full behavior contract.

## Deploy with Docker Compose

You need Docker Engine with Compose v2, an existing FLAC library and an empty, dedicated output directory. The Compose example binds to the Docker host's loopback address for use with a reverse proxy.

1. Download the deployment files:

   ```sh
   mkdir musicforge && cd musicforge
   curl -fsSLO https://raw.githubusercontent.com/sagehou/MusicForge/main/docker-compose.yml
   curl -fsSL https://raw.githubusercontent.com/sagehou/MusicForge/main/.env.example -o .env
   ```

2. Edit `.env` to set host directories, `PUID`/`PGID` and the public origin. Pin `MUSICFORGE_VERSION` to a published version such as `v0.1.1`, or use `latest` to follow stable releases. Default paths:

   | Host path | Container path | Access |
   | --- | --- | --- |
   | `/srv/musicforge/config` | `/config` | Read-write; local host disk |
   | `/srv/music/flac` | `/music/source` | Read-only |
   | `/srv/music/streaming` | `/music/output` | Read-write; initially empty |

3. Create the writable directories and grant the configured UID/GID access. For the default `10001:10001`:

   ```sh
   mkdir -p /srv/musicforge/config /srv/music/streaming
   chown -R 10001:10001 /srv/musicforge/config /srv/music/streaming
   docker compose pull
   docker compose up -d
   docker compose logs musicforge
   ```

   Run the ownership command with sufficient host permissions. The selected user also needs read access to FLAC. `/config` must use a local filesystem suitable for SQLite WAL, not NFS/SMB. Mount source and output separately.

4. Forward the public HTTPS origin to `127.0.0.1:8787` through your reverse proxy. Set `MUSICFORGE_PUBLIC_URL` to that exact origin, for example `https://musicforge.example.com`, without a path. MusicForge serves from `/`.

5. Open the Web UI. Enter `setup_code` from the first-start container logs and create the sole administrator. Passwords must contain 12–72 bytes. Setup closes permanently once the account exists.

6. In **Settings**, use container paths `/music/source` and `/music/output`, enable scanning and save. Initial encoding is **Opus VBR at 192 kbps**. MP3 VBR recommends **V2**; both codecs also support CBR, with 192 kbps recommended.

Give Navidrome a read-only mount of the same physical output directory. Its container path may differ; its corresponding library root must be this output directory.

## Interface and language

The UI detects English or Chinese from browser language preferences and falls back to English for unsupported languages. A language selector is available during setup, at login and in the application header. Manual selection is saved in this browser and applies immediately, preserving form input and library filters. If browser storage is unavailable, selection lasts for the current visit.

Navigation, forms, statuses, confirmations, notifications, common API errors, dates and numbers follow the selected language. Music tags, paths and raw job/system diagnostics retain their original content.

| Page | Main actions |
| --- | --- |
| Overview | Library counts, completion, recent jobs and storage status |
| Library | Search/filter, scan, full verification, rebuild and expired-artifact deletion |
| Jobs | Queue status, logs and individual/bulk failed-job retries |
| Settings | Paths, encoding, scans, concurrency, integrations, OIDC and password |

## Incremental builds and file lifecycle

FLAC is the source of truth. MusicForge manages generated audio and covers; use the Web UI to maintain output.

| Trigger | Behavior |
| --- | --- |
| New FLAC, changed content/tags or missing output | Automatically queue a build |
| Byte-identical rename or move | Relocate the registered artifact without re-encoding |
| Encoding profile change | Mark artifacts **Needs rebuild**; start manually in Library |
| Normal source deletion | Keep output as **Expired · Source deleted** until manual deletion |
| Lidarr upgrade | Delete explicitly replaced artifacts after all replacement tracks validate |
| Output codec switch | Retire each old-format file after its replacement validates |

Ordinary scans compare size and modification time. New or changed files receive complete-file SHA-256 verification and metadata probing. **Full verification** hashes every source file and detects content changes that leave both size and mtime unchanged. Tag changes rebuild audio because output files carry those tags.

Sources must stay unchanged for 30 seconds before conversion. Changes during encoding discard the temporary output and trigger a re-scan without consuming a failed attempt. Replacements must pass codec, duration, stream and full decoding checks before publication. Failure preserves playable output.

Expired artifacts remain in the output and Navidrome until explicit deletion. **Delete all expired** and **Delete selected expired artifacts** schedule deletion and a playback-library refresh. Unregistered files are never overwritten or deleted; path conflicts fail visibly.

Tags and ReplayGain are preserved. Each album gets one external `cover.jpg`, using source external artwork first, then an embedded cover in disc/track order. External artwork changes update the cover independently of audio encoding.

## Reliability and maintenance

Jobs allow three failed attempts: the initial attempt and two automatic retries with increasing delays. Exhausted jobs wait for manual retry; ordinary scans do not reset unchanged failed targets. Pending and interrupted work survives restart. Interrupted encoding starts again from the beginning of that track.

Offline storage pauses affected jobs. Incomplete scans never expire unseen files. Mount identity and the output `.musicforge` ownership marker protect against missing mounts appearing as empty libraries. After an intentional source mount change, verify paths and save Settings to acknowledge it. Keep the ownership marker intact.

Container root paths cannot change after indexing. Relocate storage through host mounts while retaining container paths. Back up all of `/config`, including its database, with MusicForge stopped; keep an independent FLAC backup. Upgrade by selecting an image version in `.env`, then running `docker compose pull` and `docker compose up -d`.

## Lidarr integration

Create a native **Webhook** connection in Lidarr:

| Field | Value |
| --- | --- |
| URL | `https://musicforge.example.com/api/webhook/lidarr` |
| Username | `musicforge` |
| Password | The independent webhook secret saved in MusicForge Settings |
| Events | Release import and upgrade notifications |

The connection test creates no conversion work. Native `Download` events use `trackFiles[].path`, `isUpgrade` and `deletedFiles[].path`. Set **Lidarr source path prefix** if Lidarr sees a different source root. Mapped paths must remain within MusicForge's FLAC root. Custom clients may use Bearer authentication with the same secret.

## Navidrome integration

Save the URL, username, password and library ID in Settings. Automatic refresh combines changed album directories after conversion/deletion batches. Changes remain queued until Navidrome reports scan completion; interrupted refreshes resume after restart. Navidrome 0.59.0+ receives targeted `startScan` requests; older versions receive regular scans. Removed directories use the nearest surviving parent. **Refresh Navidrome manually** uses saved settings.

Authentication uses the Subsonic salted-token API; plaintext passwords are not sent in query parameters. Use HTTPS across untrusted networks.

## Authentication and recovery

The local administrator has access to all operations. You may additionally bind one native OIDC identity. Authenticating proxy headers are not used.

1. Set `MUSICFORGE_PUBLIC_URL` to the public HTTPS origin.
2. Save the issuer URL, client ID and secret in Settings. Use the provider's exact issuer, including any trailing slash.
3. Register `https://musicforge.example.com/api/auth/oidc/callback` as the redirect URI.
4. While signed in locally, select **Bind your OIDC identity** and complete the provider flow.

Only the bound `issuer + sub` can use OIDC. State, nonce, PKCE and server-side sessions are checked. UI mutations require CSRF tokens; API reads omit secret settings. Keep the local password for recovery.

To reset a forgotten password, stop MusicForge, then supply the new password through stdin using the same image and `/config` volume. Run in Bash:

```bash
docker compose stop musicforge
read -rs -p 'New password: ' new_password
printf '%s\n' "$new_password" | docker compose run --rm -T musicforge -reset-password-stdin
unset new_password
docker compose up -d
```

Passwords require 12–72 bytes. Recovery needs exclusive access to `/config` and revokes all sessions.

## Runtime configuration

Optional `/config/config.json` follows [config.example.json](config.example.json). Environment variables override startup configuration:

| Variable | Default / purpose |
| --- | --- |
| `MUSICFORGE_CONFIG_DIR` | `/config` |
| `MUSICFORGE_LISTEN` | `:8787` |
| `MUSICFORGE_PUBLIC_URL` | Empty; set for OIDC and HTTPS cookies |
| `MUSICFORGE_LOG_LEVEL` | `INFO` |
| `MUSICFORGE_FFMPEG` | `ffmpeg` |
| `MUSICFORGE_FFPROBE` | `ffprobe` |

Library, encoding and integration settings live only in SQLite. Logs are JSON. Public `/healthz` checks database liveness; the authenticated Overview reports library availability separately. Migrations run transactionally at startup; unknown newer schemas are rejected.

## Development and releases

Follow [AGENTS.md](AGENTS.md): local work is source/document/workflow editing and static inspection only. Dependency installation, compilation, executable tests and image builds run exclusively in GitHub Actions. Keep dependency directories, compiler caches and build outputs out of the local workspace. Generate or update lockfiles through Actions.

[CI](.github/workflows/ci.yml) builds frontend/backend, runs race-enabled Go tests with real ffmpeg, exercises the actual app in Chromium and smoke-tests native containers on both architectures. The `canonical-source` artifact supplies CI-generated lockfiles and formatted Go source when needed. CI also verifies playback retention after source deletion and removal after manual deletion against a real Navidrome container with a read-only music mount.

[Stable dependency refresh](.github/workflows/dependencies.yml) runs weekly and can be started manually. It updates stable npm/Go versions and compatible transitive modules, toolchains, pinned Actions and media release checksums, generates lockfiles in Actions and opens a reviewable PR. It explicitly starts CI for the update branch. Review major-version compatibility and merge only after validation; it never merges or publishes automatically. If repository settings prevent bot PR creation, the `dependency-source` artifact still contains the generated files. Bootstrap with the `artifact_only` option when lockfiles need manual review.

The current toolchain is Go 1.27.1 / Node 26.10.0 with React 19.3, Vite 8.3, TypeScript 7 and Tailwind 4.3. Runtime images use Debian 13 stable and checksum-pinned FFmpeg 9.0.2, Opus 1.6.1 and LAME 4.0. Build/test tools and media executables are upgraded together. Corresponding media source archives and build instructions ship in the container for maintenance and relinking.

Pull requests and `main` pushes validate without publishing. Version tags such as `v0.1.0` publish the matching GHCR image after validation. Stable releases update `latest`; prereleases do not.

Translations live in [web/src/locales](web/src/locales); keep keys and interpolation placeholders aligned. Browser tests cover language detection, selection/persistence and bilingual workflows. License information is in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
