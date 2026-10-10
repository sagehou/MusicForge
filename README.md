# MusicForge

**English** · [Simplified Chinese](README.zh-CN.md)

MusicForge builds a streaming edition of your music collection. Lidarr manages the source library, MusicForge generates Opus or MP3 with ffmpeg, and Navidrome serves the output. Incremental builds process only tracks that need work.

```text
Lidarr audio library (read-only)
              ↓
MusicForge · SQLite · ffmpeg
              ↓
Opus or MP3 library (read-write)
              ↓
Navidrome (read-only)
```

One container, one administrator, one source library and one output library. Images are published at `ghcr.io/sagehou/musicforge` for `linux/amd64` and `linux/arm64`.

## Supported source audio

Scan FLAC, MP3, AAC/ALAC in M4A/M4B/MP4, raw AAC, PCM WAV/AIFF, Ogg/Vorbis/Opus, WMA, APE, WavPack and MKA. Extensions are case-insensitive; ffprobe checks actual audio and rejects ordinary video streams while allowing attached artwork. Source formats can coexist in one library. All tracks are re-encoded with the selected Opus/MP3 profile, including lossy inputs; another lossy encode can reduce quality.

Keep the existing source-relative output names. `01.flac` and `01.mp3` map to the same output; the existing registered owner is preserved and the conflicting build fails with the source path in Library and Jobs. Rename the conflicting source to resolve it. Unsupported extensions are skipped. `FLAC_DIR` remains the Compose source-path variable for deployment compatibility; it may point to a mixed audio library.

## Features

- Browse Artist → Album → Track; control entire scan/build queues with live track progress.
- Pause/resume or stop tasks and delete completed history while keeping generated audio.
- Index supported audio files and metadata in SQLite; convert new or changed tracks.
- Move byte-identical renamed files without re-encoding.
- Preserve tags and ReplayGain; store album artwork once as `cover.jpg`.
- Validate replacements before retiring playable output.
- Recover durable background jobs after restart; retry failures automatically.
- Receive Lidarr import/upgrade webhooks and refresh Navidrome after build or deletion batches.
- Use English or Simplified Chinese, local administrator login and optional native OIDC.

Cloud sync, distributed workers, multiple libraries, multiple users and plugins are outside the MVP scope. See the [design baseline](docs/mvp-design.en.md) for the full behavior contract.

## Deploy with Docker Compose

You need Docker Engine with Compose v2, an existing audio library and an empty, dedicated output directory. The Compose example binds to the Docker host's loopback address for use with a reverse proxy.

1. Download the deployment files:

   ```sh
   mkdir musicforge && cd musicforge
   curl -fsSLO https://raw.githubusercontent.com/sagehou/MusicForge/main/docker-compose.yml
   curl -fsSL https://raw.githubusercontent.com/sagehou/MusicForge/main/.env.example -o .env
   ```

2. Edit `.env` to set host directories, `PUID`/`PGID` and the public origin. Pin `MUSICFORGE_VERSION` to a published version such as `v0.2.6`, or use `latest` to follow stable releases. Default paths:

   | Host path | Container path | Access |
   | --- | --- | --- |
   | `/srv/musicforge/config` | `/config` | Read-write; local host disk |
   | `/srv/music/flac` | `/music/source` | Read-only |
   | `/srv/music/streaming` | `/music/output` | Read-write; initially empty |

3. Create the writable directories and grant the configured UID/GID access. For the default `10001:10001`:

   ```sh
   mkdir -p /srv/musicforge/config /srv/music/streaming
   chown -R 10001:10001 /srv/musicforge/config /srv/music/streaming
   chmod 700 /srv/musicforge/config
   docker compose pull
   docker compose up -d
   docker compose logs musicforge
   ```

   Run the ownership command with sufficient host permissions. The selected user also needs read access to source audio. `/config` must use a local filesystem suitable for SQLite WAL, not NFS/SMB. Mount source and output separately.

4. Forward the public HTTPS origin to `127.0.0.1:8787` through your reverse proxy, preserving Host. Set `MUSICFORGE_PUBLIC_URL` to the primary origin, for example `https://musicforge.example.com`, without a path. Add other origins through `MUSICFORGE_ALLOWED_ORIGINS` as described below. MusicForge serves from `/`.

5. Open the Web UI. Enter `setup_code` from the first-start container logs and create the sole administrator. Passwords must contain 12–72 bytes. Setup closes permanently once the account exists.

6. In **Settings**, use container paths `/music/source` and `/music/output`, enable scanning and save. Initial encoding is **Opus VBR at 192 kbps**. MP3 VBR recommends **V2**; both codecs also support CBR, with 192 kbps recommended.

MusicForge enforces mode `0700` on `/config` at startup, including pre-existing host directories, to protect database journals and stored secrets. The configured UID must own this directory. Compose refuses missing host paths and rotates container logs. See the [production trial guide](docs/deployment.md) for proxy networking, acceptance steps and rollback.

Give Navidrome a read-only mount of the same physical output directory. Its container path may differ; its corresponding library root must be this output directory.

## Mounted remote source libraries

An existing rclone/FUSE mount can be the read-only audio source. MusicForge does not manage rclone or synchronize cloud storage. Keep `/config` and the output on local reliable storage. Mount rclone before starting the container and give its UID/GID read access; `--allow-other` may be needed when the mount owner differs.

Scanning indexes tags and file attributes first; it does not perform a separate whole-file hash pass. A worker then reads each selected audio source once into bounded local scratch storage, computing its full SHA-256 during that copy. Encoding and embedded cover extraction reuse the seekable copy. This works without rclone VFS caching; `--vfs-cache-mode full` remains optional. Metadata probing may read headers and seek to the tail, depending on the format. Ordinary unchanged scans skip audio reads. Jobs and logs show the current source, read bytes, encoding progress and waits for staging space.

A source read with no progress for 120 seconds times out. Adjust `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` for your remote if necessary. Hash/enumeration timers reset on progress; ffprobe and cover extraction have an operation deadline, and encoding must keep advancing. A single unreadable track preserves existing audio and hashes, allows healthy tracks to be queued, and prevents deletion detection for that incomplete scan. Each preparation/conversion retries twice, then waits for manual retry; healthy songs do not repeat their downloads because another song failed. Whole-root outages defer work without spending track retry budgets.

After remounting, verify what the container sees. Docker's default private bind does not automatically follow every host remount; recreate the container if needed. A changed-root warning requires verifying the live mount before saving Settings to acknowledge it. See the [deployment guide](docs/deployment.md) for acceptance and diagnostics.

## Interface and language

The UI detects English or Chinese from browser language preferences and falls back to English for unsupported languages. A language selector is available during setup, at login and in the application header. Manual selection is saved in this browser and applies immediately, preserving form input and library filters. If browser storage is unavailable, selection lasts for the current visit.

Navigation, forms, statuses, confirmations, notifications, common API errors, dates and numbers follow the selected language. Music tags, paths and raw job/system diagnostics retain their original content.

| Page | Main actions |
| --- | --- |
| Overview | Library counts, completion, recent jobs and storage status |
| Library | Search/filter, scan, full verification, rebuild and expired-artifact deletion |
| Jobs | Queue progress, page/filter selection, bulk pause/resume/stop/retry and history cleanup |
| Settings | Paths, encoding, scans, concurrency, integrations, OIDC and password |

## Incremental builds and file lifecycle

The source library is the source of truth. MusicForge manages generated audio and covers; use the Web UI to maintain output.

| Trigger | Behavior |
| --- | --- |
| New audio, changed content/tags or missing output | Automatically queue a build |
| Byte-identical rename or move | Relocate the registered artifact without re-encoding |
| Encoding profile change | Mark artifacts **Needs rebuild**; start manually in Library |
| Normal source deletion | Keep output as **Expired · Source deleted** until manual deletion |
| Lidarr upgrade | Delete explicitly replaced artifacts after all replacement tracks validate |
| Output codec switch | Retire each old-format file after its replacement validates |

Ordinary scans compare size and modification time. New or changed files receive metadata probing first and appear as **Waiting for verification and conversion** until a worker verifies their complete-file SHA-256. A size/mtime match alone never becomes a content hash. **Full verification** hashes every source file and detects content changes that leave both size and mtime unchanged. Tag changes rebuild audio because output files carry those tags.

Source scratch files live in `/config/source-staging`, shared by all workers. `MUSICFORGE_STAGING_MAX_BYTES` limits their total size to 4 GiB by default. Keep enough local free space for the largest source and at least 64 MiB for configuration writes. A source larger than the limit fails before downloading; increase the limit and retry. Scratch files are removed after success, failure or cancellation, and abandoned files are cleaned on startup. A failed attempt, restart or changed source may require a new read. Explicit full verification and Lidarr's destructive upgrade cleanup still perform the requested complete-content checks.

Sources must stay unchanged for 30 seconds before conversion. Changes during encoding discard the temporary output and trigger a re-scan without consuming a failed attempt. Replacements must pass codec, duration, stream and full decoding checks before publication. Failure preserves playable output.

Expired artifacts remain in the output and Navidrome until explicit deletion. **Delete all expired** and **Delete selected expired artifacts** schedule deletion and a playback-library refresh. Unregistered files are never overwritten or deleted; path conflicts fail visibly.

Tags and ReplayGain are preserved. Each album gets one external `cover.jpg`, using source external artwork first, then an embedded cover in disc/track order. External artwork changes update the cover independently of audio encoding.

## Reliability and maintenance

Timer scans wait for active and paused library queues to finish, then wait the configured interval. Manual scans and Lidarr imports merge into the active scan task; later scopes are retained on the same task. Select this page or all tasks matching a state to control accumulated queues. Bulk actions report the number changed and preserve completed audio. Numeric job IDs also include internal per-track work; the Jobs total counts logical tasks.

Jobs allow three failed attempts: the initial attempt and two automatic retries with increasing delays. Exhausted jobs wait for manual retry; ordinary scans do not reset unchanged failed targets. Pending and interrupted work survives restart. Interrupted encoding starts again from the beginning of that track.

Failure recovery: when ffmpeg fails to decode a native FLAC file, MusicForge retries with the independent FLAC reference decoder and pipes PCM into the existing encoder. This fallback reuses the complete local source copy without another remote read or a large PCM scratch file. Both processes must succeed and artifact validation must pass before publication; reference-decoder CRC/MD5 failures block publication. If the attempt still fails, the scratch copy is removed and the remaining automatic retries read the source path again, up to three attempts total. The rclone mount can still serve its own cache. Jobs distinguishes source-read, decode, conversion and output-validation failures. Logs include the source path, input SHA-256, byte count and failure stage; the first decoding failure also records media-tool versions. The fallback requires a native `fLaC` file header; FLAC inside other containers continues through ffmpeg.

Offline storage pauses affected jobs. Incomplete scans never expire unseen files. Mount identity and the output `.musicforge` ownership marker protect against missing mounts appearing as empty libraries. After an intentional source mount change, verify paths and save Settings to acknowledge it. Keep the ownership marker intact.

Container root paths cannot change after indexing. Relocate storage through host mounts while retaining container paths. Back up all of `/config`, including its database, with MusicForge stopped before upgrading; keep an independent source-library backup. Upgrade by selecting an image version in `.env`, then running `docker compose pull` and `docker compose up -d`.

## Lidarr integration

Create a native **Webhook** connection in Lidarr:

| Field | Value |
| --- | --- |
| URL | `https://musicforge.example.com/api/webhook/lidarr` |
| Username | `musicforge` |
| Password | The independent webhook secret saved in MusicForge Settings |
| Events | Release import and upgrade notifications |

The connection test creates no conversion work. Native `Download` events use `trackFiles[].path`, `isUpgrade` and `deletedFiles[].path`. Set **Lidarr source path prefix** to an absolute container path if Lidarr sees a different source root. Mapped paths must remain within MusicForge's source root. Custom clients may use Bearer authentication with the same secret.

## Navidrome integration

Save the URL, username, password and library ID in Settings. Automatic refresh combines changed album directories after conversion/deletion batches. Changes remain queued until Navidrome reports scan completion; interrupted refreshes resume after restart. Navidrome 0.59.0+ receives targeted `startScan` requests; older versions receive regular scans. Removed directories use the nearest surviving parent. **Refresh Navidrome manually** uses saved settings.

Authentication uses the Subsonic salted-token API; plaintext passwords are not sent in query parameters. Use HTTPS across untrusted networks.

## Authentication and recovery

The local administrator has access to all operations. You may additionally bind one native OIDC identity. Authenticating proxy headers are not used.

1. Set `MUSICFORGE_PUBLIC_URL` to the primary HTTPS origin.
2. Save the issuer URL, client ID and secret in Settings. Use the provider's exact issuer, including any trailing slash.
3. Register `https://musicforge.example.com/api/auth/oidc/callback` as the redirect URI.
4. While signed in locally at the primary URL, select **Bind your OIDC identity** and complete the provider flow.

Only the bound `issuer + sub` can use OIDC. State, nonce, PKCE and server-side sessions are checked. UI mutations require CSRF tokens; API reads omit secret settings. Keep the local password for recovery.

Local login and setup allow ten credential failures per client in a fifteen-minute window; successful authentication clears that budget. OIDC starts use a separate budget, cleared on successful OIDC login; they cannot exhaust local recovery or authenticated binding. Behind a reverse proxy, configure `MUSICFORGE_TRUSTED_PROXIES` and its `X-Forwarded-For` handling as described in the [deployment guide](docs/deployment.md). Untrusted peers cannot choose their client IP through headers.

Leaving a secret field blank preserves its saved value. To erase the Navidrome password or OIDC client secret, clear the corresponding URL, select its explicit deletion option and save. Clearing the OIDC secret requires local login and revokes OIDC sessions, flows and the identity binding.

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
| `MUSICFORGE_PUBLIC_URL` | Empty; primary origin for OIDC; also allowed for browser access |
| `MUSICFORGE_ALLOWED_ORIGINS` | Empty; comma-separated additional browser origins |
| `MUSICFORGE_TRUSTED_PROXIES` | Empty; comma-separated reverse proxy IP CIDRs for client-IP rate limiting |
| `MUSICFORGE_LOG_LEVEL` | `INFO` |
| `MUSICFORGE_FFMPEG` | `ffmpeg` |
| `MUSICFORGE_FFPROBE` | `ffprobe` |
| `MUSICFORGE_SOURCE_TIMEOUT_SECONDS` | `120`; mounted-source no-progress timeout, 1–3600 seconds |
| `MUSICFORGE_STAGING_MAX_BYTES` | `4294967296`; total local source scratch limit in bytes, shared by workers |

Library, encoding and integration settings live only in SQLite. Logs are JSON. Public `/healthz` checks database liveness; the authenticated Overview reports library availability separately. Migrations run transactionally at startup; unknown newer schemas are rejected.

To use multiple domains, set these values in `.env`:

```dotenv
MUSICFORGE_PUBLIC_URL=https://musicforge.example.com
MUSICFORGE_ALLOWED_ORIGINS=https://musicforge.home.example.com,https://musicforge-alt.example.com
```

The primary URL is always allowed. Point every domain's DNS and reverse proxy to the same instance, preserve Host, and provide HTTPS for each HTTPS origin. Entries include the scheme and optional port; paths and wildcards are rejected. Different hosts may use HTTP and HTTPS, but each hostname must use one scheme because cookies are shared across its ports. Cookies use the configured scheme for the accessed host; forwarded host/protocol headers cannot add allowed origins. Browser writes must match both the allowed origin and request Host, and authenticated writes still require a CSRF token.

Each domain supports local login and has its own host-only login cookie. OIDC started on another domain redirects to the primary URL before creating state; login finishes and stays there. Register only the primary `/api/auth/oidc/callback` with your provider. Identity binding requires local login on that primary URL; Settings provides a link from other domains. These are startup settings, displayed read-only in Settings. Recreate the container with `docker compose up -d` after changing `.env`.

In `config.json`, use an `allowed_origins` array. An empty `MUSICFORGE_ALLOWED_ORIGINS` environment variable clears that array; remove the Compose environment entry if you want to use the file value. This feature does not change schema 2 or persisted authentication formats. Older `0.2.x` images ignore the new startup setting and support the primary URL only.

## Development and releases

Follow [AGENTS.md](AGENTS.md): local work is source/document/workflow editing and static inspection only. Dependency installation, compilation, executable tests and image builds run exclusively in GitHub Actions. Keep dependency directories, compiler caches and build outputs out of the local workspace. Generate or update lockfiles through Actions. Version 0.2 uses database schema 2; rollback to 0.1 requires restoring the matching pre-upgrade `/config` backup.

[CI](.github/workflows/ci.yml) builds frontend/backend, runs race-enabled Go tests with real ffmpeg, exercises the actual app in Chromium and smoke-tests native containers on both architectures. The `canonical-source` artifact supplies CI-generated lockfiles and formatted Go source when needed. CI also verifies playback retention after source deletion and removal after manual deletion against a real Navidrome container with a read-only music mount.

[Stable dependency refresh](.github/workflows/dependencies.yml) runs weekly and can be started manually. It updates stable npm/Go versions and compatible transitive modules, toolchains, pinned Actions and media release checksums, generates lockfiles in Actions and opens a reviewable PR. It explicitly starts CI for the update branch. Review major-version compatibility and merge only after validation; it never merges or publishes automatically. If repository settings prevent bot PR creation, it still pushes the update branch, starts CI and provides a compare link plus a `dependency-source` artifact. This repository currently disables bot PR creation; use the compare link to open a PR manually or have a repository administrator enable bot PRs. Bootstrap with the `artifact_only` option when lockfiles need manual review.

The current toolchain is Go 1.27.1 / Node 26.10.0 with React 19.3, Vite 8.3, TypeScript 7 and Tailwind 4.3. Runtime images use Debian 13 stable and checksum-pinned FFmpeg 9.0.2, Opus 1.6.1 and LAME 4.0. Build/test tools and media executables are upgraded together. Corresponding media source archives and build instructions ship in the container for maintenance and relinking.

Pull requests and `main` pushes validate without publishing. Version tags such as `v0.1.0` publish the matching GHCR image after validation. Stable releases update `latest`; prereleases do not.

Acceptance evidence and deployment limits are recorded in the [acceptance report](docs/acceptance.md).

Translations live in [web/src/locales](web/src/locales); keep keys and interpolation placeholders aligned. Browser tests cover language detection, selection/persistence and bilingual workflows. License information is in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
