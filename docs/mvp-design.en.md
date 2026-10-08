# MusicForge MVP design baseline

**English** · [Simplified Chinese](mvp-design.md)

This document records the agreed product behavior and serves as the implementation and acceptance baseline. See the [README](../README.md) for deployment and CI, and [AGENTS.md](../AGENTS.md) for development constraints. Keep both language versions in sync.

## Goal and scope

MusicForge is a self-hosted streaming-library build tool. Lidarr manages FLAC sources, MusicForge calls ffmpeg to generate playback artifacts, and Navidrome serves those artifacts.

- FLAC is the source of truth for music and tags; the application generates output audio and covers.
- Prioritize a single machine, Docker deployment, reliability and long-term maintainability.
- Maintain one source/output pair and one current encoding profile.
- Exclude cloud sync, rclone mount management, distributed workers, user permissions, multitenancy and plugins from the MVP. Existing rclone/FUSE mounts are supported as read-only sources.
- Use SQLite; do not introduce Kubernetes, Redis or PostgreSQL.

## Single-container architecture

- One `musicforge` container runs one long-lived Go application process. Disposable child processes read mounted sources, and ffmpeg handles encoding.
- The application serves Web/API requests and executes scanning, conversion, file management and integrations in the background.
- APIs create durable jobs; request handlers do not execute ffmpeg directly.
- SQLite stores the index, artifact state, queue, account and Web settings.
- Ship the built React UI with the application; include ffmpeg and ffprobe in the runtime image.
- Keep clear functional boundaries, using the standard library and necessary mature dependencies.

## Mounts and ownership

- Mount `/config` read-write on local Docker host storage, including the database and WAL state.
- Mount the FLAC source separately, read-only.
- Mount the output separately, read-write. Background jobs write, replace, move and delete artifacts.
- Give Navidrome a read-only mount of the same host output directory; container paths may differ.
- Keep source/output separate and validate all task paths against their configured roots.

Output is dedicated to MusicForge. Initialize with an empty directory, then manage only generated, registered artifacts. Never automatically overwrite or delete unregistered files; report conflicts explicitly.

Mirror relative directories and filenames, replacing only the extension:

```text
source/Artist/Album/01 - Title.flac
output/Artist/Album/01 - Title.opus
```

## Encoding profile

Support Opus and MP3, each with VBR or CBR. Choose one format for the single output library.

| Codec | Encoder | Recommended mode | Recommended value |
| --- | --- | --- | --- |
| Opus | `libopus` | VBR | 192 kbps average target |
| MP3 | `libmp3lame` | VBR | V2; content-dependent bitrate, typically around 190 kbps |

- Default to Opus VBR at 192 kbps.
- Recommend 192 kbps for CBR with either codec.
- Show quality levels for MP3 VBR, average target bitrate for Opus VBR and fixed bitrate for CBR.
- Show recommendations and provide a restore-recommendations action.
- Profile changes mark mismatched artifacts **Needs rebuild** and wait for manual initiation.
- Distinguish artifacts needing rebuild from expired artifacts whose sources were deleted.

## Scanning and incremental builds

Scan FLAC and store artist, album, title, track/disc number, metadata, size and modification time in SQLite.

Ordinary scanning uses a fast check:

1. On discovery, read tags and hash the complete file.
2. If size or mtime changes, read again and compare hashes.
3. If both are unchanged, skip content reads.
4. Provide manual full verification to check source hashes again.

Accept the fast-check limitation: ordinary scans may miss modified content if both size and mtime remain unchanged. Full verification detects it.

Build rules:

- Changed content, including tag-only edits, re-encodes the track.
- Missing output generates a new artifact from source.
- Profile-only changes wait for manual rebuild.
- Byte-identical source renames/moves relocate registered artifacts and update records without re-encoding.
- Duplicate scans/webhooks do not enqueue the same target repeatedly or write the same output concurrently.

New/modified sources must remain unchanged for at least 30 seconds before conversion. Check size and mtime again at completion. A change during encoding discards temporary output and requeues the source, preserving existing output without consuming a failure attempt.

Generate a temporary file and validate before replacement. Failed conversion preserves playable artifacts.

## Deletion and replacement

### Ordinary source deletion

- Detect deletion only after a complete successful scan of accessible source storage.
- Retain the artifact and visibly mark it **Expired** in the UI.
- Keep it in the output directory and Navidrome until manual deletion.
- Provide bulk deletion of expired artifacts, limited to registered application-owned files.
- Refresh Navidrome after deletion so it removes the entries.

### Lidarr upgrades

- Treat upgrades as explicit version replacements.
- Use the event's new-file and replaced-file lists even if filenames differ.
- Automatically delete listed old artifacts only after all new files in the event convert and validate.
- Preserve old output after failure until a retry succeeds.

### Output codec changes

- Manually start the rebuild; generate replacements one track at a time.
- Delete each old-format artifact after its new-format replacement validates.
- Preserve the old format after failure for continued playback.

## Tags and artwork

- Preserve audio metadata and existing ReplayGain tags.
- Do not embed duplicate artwork in every output track.
- Prefer external source album covers such as `cover.jpg` or `folder.jpg`.
- Otherwise extract from the first FLAC with artwork in track order.
- Generate one `cover.jpg` per output album.
- Update external artwork independently of audio encoding.

## Queue and recovery

- Treat a scan and its conversion, move and upgrade-cleanup queue as one controllable task. Execute the track queue after indexing finishes. Group a manual rebuild into one task as well, reusing existing active targets.
- Show Pending, Running, Paused, Success, Failed and Stopped tasks. Keep per-track states and retry budgets internally and aggregate the queue into one task-list entry.
- Pause, resume, stop, retry and delete selected task histories. Pause/stop terminate active encoding subprocesses and clean registered temporary files; published results remain. Resume restarts unfinished tracks without consuming a failure attempt.
- Pauses survive container restarts. Stopped unfinished targets require explicit retry; retry-all does not restart deliberately stopped tasks. Periodic scanning is an independent trigger and may discover new targets.
- Delete history only when no work remains queued and workers have exited; retain library records and audio. Deleting failed/stopped history also removes its target's automatic-retry suppression record.
- Show discovered/inspected file counts, the current path, artist/album/title, track encoding percentage, validation phase and completed/pending/failed queue counts. Refresh the task view every two seconds and emit the same progress in structured Docker logs.
- Details show scan scope, timestamps, profiles, track results and expandable diagnostics, with 50 results per page.
- Default to one concurrent ffmpeg conversion; allow adjustment in Settings.
- Retry twice after the initial failure, allowing three failed attempts total with increasing delays.
- Exhausted jobs wait for individual or bulk manual retry.
- Ordinary scans do not restart an unchanged failed target indefinitely.
- Bound source-mount calls in cancellable child processes, show hash-read bytes, and keep API requests independent of remote storage checks. Individual source-read failures preserve known signatures/output, continue indexing healthy tracks and prevent expiration for that incomplete scan; retry the scan twice before manual intervention.

When source or output storage is unavailable:

- Show the library offline, pause affected jobs and resume automatically when accessible.
- Incomplete scans never expire unseen sources.
- Whole-storage outages do not consume track failure attempts.

After restart:

- Skip successful tracks and continue pending work.
- Recheck interrupted tasks and restart encoding from the beginning of the track.
- Clean registered unfinished temporary files while preserving existing output.
- Interruptions do not count as failures. Recovery is per track, without resuming within a file.

## Scan triggers

- Configurable periodic scans discover deletions, moves and missed changes.
- Manual scans support first import, troubleshooting and full verification.
- Lidarr webhooks scan affected directories and create conversion jobs promptly.

## Lidarr integration

Endpoint: `POST /api/webhook/lidarr`.

- Authenticate with a separate integration secret, scoped to source-library scan/build triggers.
- Accept native import/upgrade payloads: `eventType=Download`, with `isUpgrade` identifying upgrades.
- Use `trackFiles[].path` and upgrade `deletedFiles[].path` to locate directories; no extra album-path field is required.
- Accept connection-test events without creating real conversion work.
- Support one source-prefix mapping for different container mount paths; nonempty prefixes must be absolute container paths, checked when saving.
- Confine mapped paths to MusicForge's configured source root.
- Use Lidarr's native Username/Password fields for HTTP Basic webhook credentials.

## Navidrome integration

- The corresponding Navidrome library root is MusicForge's output root with identical album-relative paths.
- Provide manual refresh.
- Automatically refresh after conversion or bulk deletion, coalescing directories within a batch.
- Navidrome 0.59.0+ supports `startScan` with `target` as `libraryID:relativeDirectory`.
- Prefer targeted album scans when supported.
- When a whole album directory is deleted, scan the nearest surviving parent to detect removal; the root target is `libraryID:.`, for example `1:.`.
- Fall back to a regular scan after the batch for older versions.

## Authentication

- One administrator with full access; local username/password and native OIDC login.
- Create the account through Web setup, using a one-time code in first-start container logs.
- Account creation requires the code; success invalidates it and closes setup.
- Configure and bind OIDC while logged in as the local administrator.
- Only the bound `issuer + sub` may log in; username/email are display and audit information.
- Ordinary UI/API access requires administrator login; no anonymous LAN mode.
- Do not use Forward Auth or OIDC Proxy identity headers. Authentik can be a native OIDC provider.
- The reverse proxy provides HTTPS and request forwarding.
- Support multiple explicit access origins through startup `MUSICFORGE_ALLOWED_ORIGINS`; `MUSICFORGE_PUBLIC_URL` is the automatically allowed primary origin. Reject wildcards and subpaths.
- Each domain has its own host-only login cookie and supports local login. Browser writes must match the allowed origin and current Host, with CSRF checks preserved.
- OIDC started elsewhere first redirects to the primary URL; callbacks and completed logins stay there. Identity binding requires a local administrator session on the primary URL. Settings displays the primary and allowed origins read-only without changing persisted authentication formats.

- Local login/setup allow ten credential failures per client in fifteen minutes and reset on success. OIDC starts have a separate budget, reset on successful OIDC login; local recovery and authenticated binding remain independent.
- By default, rate limiting uses the connection peer. Explicit trusted proxy CIDRs enable `X-Forwarded-For`, walking right to left to the first untrusted address; this header never supplies identity.
- Blank secret fields retain saved values. Explicit actions delete the Navidrome password or OIDC client secret after disabling the relevant integration. OIDC deletion requires local login and revokes OIDC sessions, flows and binding.

## Web UI

Use React, TypeScript, TailwindCSS and shadcn/ui.

- Overview: FLAC/output counts, build completion and recent jobs.
- Library: browse Artist → Album → Track using source-relative directories, breadcrumbs, disc/track ordering, search and status filters. Select artists, albums or tracks for bulk actions with clear expired/rebuild status. Output keeps the source library’s relative hierarchy.
- Jobs: whole scan/build queue states, live track progress, human-readable details, queue controls and bulk history cleanup.
- Settings: paths, encoding/mode/parameters, concurrency, scan interval, OIDC and integrations.
- Login and first-run setup.
- English and Simplified Chinese; initially use browser preference, falling back to English. Provide a selector and persist manual selection in the browser.
- Localize copy, common API errors, dates and numbers. Switching preserves unsaved input and filters; tags, paths and raw diagnostics retain original content.

## Configuration and quality

- Set startup options such as listening address/log level through a file or environment variables.
- Store Web-editable paths, encoding and integration settings in SQLite.
- Each setting has one authoritative source; file configuration and database must not overwrite each other.
- Provide structured logging, health endpoint, migrations, README and single-service Compose example.
- Support `linux/amd64` and `linux/arm64` images.

## Persistence compatibility and rollback

- Schema 2 is the stable MVP baseline. The `0.2.x` series keeps this structure and the published table/column meanings.
- Compatibility includes Settings JSON, job kinds/arguments, metadata/recovery journals, ownership markers and artifact paths/signatures. Keeping the schema number while changing formats or units is incompatible.
- Same-series releases must support older compatible readers and writers. On both architectures, CI opens the candidate's same volumes with published older images, checks library, credentials, jobs and artifacts, then returns to the candidate.
- Incompatible changes require a clearly identified breaking release with an upgrade backup procedure and a verified restore-and-rollback path. Preserve historical migrations; do not force a lower database version.
- Complete state rollback requires matching stopped `/config` and output snapshots. Switching images cannot undo completed replacements, moves or deletions. Published version tags and images are immutable.

Task presentation is additive metadata: `task-member:<job ID>` contains the decimal root job ID, `task-control:<root ID>` contains `paused` or `stopped`, and `task-progress:<job ID>` contains a JSON phase/path/tag/counter snapshot (`processed`/`total` are file counts, `percent` is 0–100). Older releases ignore and retain these optional keys. Primitive job kinds/arguments and table/column definitions remain unchanged. A pause keeps a job `pending` with `not_before=253402300799` (Unix seconds); stopping uses the existing terminal `failed` state and an explicit log message without incrementing actual failed attempts. Older UIs show these as deferred/failed individual jobs; the new UI restores grouping when reopened. Progress annotations are diagnostic snapshots, never file-recovery journals.

## Development and publication

- Local work is editing, reading and static inspection without compilation or dependency downloads.
- Do not compile, install/download dependencies or build Docker images locally.
- Run installation, compilation, executable tests/validation and image builds in GitHub Actions only.
- Generate/update lockfiles through Actions.
- Pull requests and default-branch commits validate without publishing images.
- Version tags such as `v0.1.0` publish validated multiarchitecture images to GHCR.
- Publish the versioned image tag; stable releases also update `latest`, prereleases do not.

## Verified upstream references

- [Lidarr webhook payload construction](https://github.com/Lidarr/Lidarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/WebhookBase.cs)
- [Lidarr webhook sending and authentication](https://github.com/Lidarr/Lidarr/blob/develop/src/NzbDrone.Core/Notifications/Webhook/WebhookProxy.cs)
- [Navidrome 0.59.0 release](https://github.com/navidrome/navidrome/releases/tag/v0.59.0)
- [Navidrome targeted scans #4674](https://github.com/navidrome/navidrome/pull/4674)

Optional progress fields `read_bytes` and `read_total_bytes` use bytes; existing file-count and percentage units are unchanged. Startup `source_timeout_seconds` is ignored by older releases. Neither addition changes Settings JSON, job arguments, recovery journals or schema 2.
