# MusicForge

Self-hosted incremental builds for your streaming music library. Lidarr owns the FLAC masters; MusicForge produces Opus or MP3; Navidrome reads the output.

```text
Lidarr FLAC library (read-only)
            ↓
MusicForge · SQLite · ffmpeg
            ↓
Opus / MP3 library (read-write)
            ↓
Navidrome (read-only)
```

One container, one administrator, one source library, one output library. Native images support `linux/amd64` and `linux/arm64`.

## Deployment

Copy `.env.example` to `.env`, set the host paths and public URL, then use the provided `docker-compose.yml`:

```sh
mkdir -p /srv/musicforge/config /srv/music/streaming
chown -R 10001:10001 /srv/musicforge/config /srv/music/streaming
docker compose pull
docker compose up -d
docker compose logs musicforge
```

Use your own UID/GID through `PUID` and `PGID` when necessary. That user must be able to read FLAC and write `/config` and the output directory. `/config` must be on a local host filesystem suitable for SQLite WAL; do not place it on NFS/SMB. The FLAC mount is read-only. The initial output directory must be empty and dedicated to MusicForge.

The default Compose port binds to `127.0.0.1:8787`. Configure your reverse proxy to forward the public HTTPS origin to this port. Set `MUSICFORGE_PUBLIC_URL` to that exact origin, with no path. The application serves from `/`, not a subpath.

Open the web UI and enter the `setup_code` printed in the first-start logs. Create the only administrator, then configure and enable the library in Settings. Setup is permanently disabled after creation. The initial encoding is **Opus VBR 192kbps**; MP3 VBR recommends **V2**. Both codecs also offer CBR.

Configure Navidrome to read the same physical output directory, preferably as a read-only mount. It can use a different container path.

## Incremental behavior

- New FLAC, changed bytes/tags, or missing output: automatically queue conversion.
- Ordinary scans compare size and modification time; changed files get complete-file SHA-256 and ffprobe metadata. Use **完整校验** for a full hash pass.
- A byte-identical rename/move relocates the registered output without re-encoding.
- Encoding changes mark existing artifacts **待重建**. Start them manually in Library.
- Source files must remain unchanged for 30 seconds. An input that changes during encoding is re-scanned without consuming an attempt.
- A temporary artifact must pass codec, duration, stream and complete decoding checks before replacing the playable file. Changing codec removes the old format only after validation.
- Normal source deletion retains the output as **过期**. It remains playable until you use the explicit bulk deletion action.
- Lidarr upgrades clean up only the explicitly replaced old artifacts after every new track in the event has a validated current output.
- Unregistered files are never overwritten or deleted. Directory conflicts appear in job logs.
- Audio metadata and ReplayGain are preserved. Album artwork is stored once as `cover.jpg`; source external covers take priority, then the first embedded cover in disc/track order.

If both size and mtime stay unchanged despite modified bytes, ordinary scans can miss that change. Full verification detects it. Fast checking deliberately avoids hashing the entire collection on each scan.

Jobs get up to three failed attempts (initial plus two retries), then wait for manual retry. The same failed build target is not reset by ordinary scans. Pending and interrupted work survives a restart; interrupted encoding restarts at the beginning of that track.

Disconnected storage pauses jobs instead of expiring the collection. The output ownership marker and source mount identity protect against a missing mount appearing as an empty library. After an intentional source mount replacement, verify the paths and save Settings to acknowledge the mount. Do not remove the output `.musicforge` ownership marker.

Container paths cannot change after indexing; change host mount locations while keeping the container paths stable. Back up `/config` with the application stopped, including the database. Keep your FLAC backup independently.

## Lidarr

Add a native **Webhook** connection:

- URL: `https://musicforge.example.com/api/webhook/lidarr`
- Username: `musicforge`
- Password: the independent webhook secret saved in MusicForge Settings.
- Enable release import and upgrade notifications.

`Test` succeeds without conversion. Native `Download` events use `trackFiles[].path`, `isUpgrade`, and `deletedFiles[].path`. The optional Lidarr source prefix maps its container paths to MusicForge's source root. Every mapped path is confined to that root. Bearer authentication with the same secret is also supported for custom clients.

## Navidrome

Set the Navidrome URL, username, password and library ID. Automatic refresh coalesces changed album directories after conversion/deletion batches. Navidrome 0.59.0+ receives targeted `startScan` requests; older versions receive a normal scan. Removed directories use the nearest surviving parent. Settings includes a manual refresh action.

The integration uses the Subsonic salted-token API and does not send the plaintext password as a query parameter. Use HTTPS if the service is reached across an untrusted network.

## Authentication and recovery

The single local administrator can additionally bind a native OIDC identity. Authenticating proxy headers are not used.

1. Set the public HTTPS origin in `MUSICFORGE_PUBLIC_URL`.
2. Configure the OIDC issuer, client ID and secret in Settings and save.
3. Register `https://musicforge.example.com/api/auth/oidc/callback` as a redirect URI at the provider.
4. While logged in locally, click **绑定当前 OIDC 身份** and complete the provider flow.

Only the bound `issuer + sub` can log in. Other provider users are rejected. OIDC state, nonce, PKCE and server-side browser sessions are validated. All UI mutations require a CSRF token. Secret settings are omitted from API reads.

To recover the local password, stop the running container, then supply a new password through stdin to the same image and `/config` volume:

```sh
docker compose stop musicforge
read -rs -p 'New password: ' password
printf '%s\n' "$password" | docker compose run --rm -T musicforge -reset-password-stdin
unset password
docker compose up -d
```

Passwords require 12–72 bytes. Recovery revokes all sessions and requires exclusive access to `/config`.

## Runtime configuration

Optional `/config/config.json` follows `config.example.json`. Environment values override runtime configuration:

| Variable | Default |
| --- | --- |
| `MUSICFORGE_CONFIG_DIR` | `/config` |
| `MUSICFORGE_LISTEN` | `:8787` |
| `MUSICFORGE_PUBLIC_URL` | empty; required for OIDC and HTTPS cookies |
| `MUSICFORGE_LOG_LEVEL` | `INFO` |
| `MUSICFORGE_FFMPEG` | `ffmpeg` |
| `MUSICFORGE_FFPROBE` | `ffprobe` |

Library, encoding and integration settings live only in SQLite. Logs are JSON. `/healthz` is a public database liveness check; library availability is reported separately in the authenticated Dashboard. SQLite migrations run transactionally at startup and newer unknown schemas are rejected.

## Development and releases

**Do not install dependencies, compile, execute tests, or build images in the local workspace.** Follow [AGENTS.md](AGENTS.md). Local changes are source/document/workflow edits and static inspection only.

[GitHub Actions](.github/workflows/ci.yml) installs dependencies, builds the frontend and backend, runs race-enabled Go tests with real ffmpeg, browser tests against the real app, and native container smoke tests for both architectures. Initial dependency lockfiles and formatted Go source are generated as the `canonical-source` artifact; retrieve and commit those source files before releasing.

PRs and `main` pushes validate without publishing. Version tags such as `v0.1.0` publish `ghcr.io/sagehou/musicforge:v0.1.0` only after validation. Stable tags update `latest`; prereleases leave `latest` unchanged. Architecture tags are combined into the versioned multiarchitecture manifest.

The agreed behavior and scope are in [docs/mvp-design.md](docs/mvp-design.md).
