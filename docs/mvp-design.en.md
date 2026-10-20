# MusicForge MVP design baseline

**English** · [Simplified Chinese](mvp-design.md)

This document records the agreed product behavior and serves as the implementation and acceptance baseline. See the [README](../README.md) for deployment and CI, and [AGENTS.md](../AGENTS.md) for development constraints. Keep both language versions in sync.

## Goal and scope

MusicForge is a self-hosted streaming-library build tool. Lidarr manages audio sources, MusicForge calls ffmpeg to generate playback artifacts, and Navidrome serves those artifacts.

- The source library is the source of truth for music and tags; the application generates output audio and covers.
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
- Mount the audio source separately, read-only.
- Mount the output separately, read-write. Background jobs write, replace, move and delete artifacts.
- Give Navidrome a read-only mount of the same host output directory; container paths may differ.
- Keep source/output separate and validate all task paths against their configured roots.

Output is dedicated to MusicForge. Initialize with an empty directory, then manage only generated, registered artifacts. Never automatically overwrite or delete unregistered files; report conflicts explicitly.

Mirror relative directories and filenames, replacing only the extension:

```text
source/Artist/Album/01 - Title.flac
output/Artist/Album/01 - Title.opus
```

## Supported source formats

Allow mixed FLAC, MP3, AAC/ALAC, PCM WAV/AIFF, Ogg/Vorbis/Opus, WMA, APE, WavPack and MKA sources. Scan `.flac`, `.mp3`, `.m4a`, `.m4b`, `.mp4`, `.aac`, `.wav`, `.wave`, `.aif`, `.aiff`, `.aifc`, `.ogg`, `.oga`, `.opus`, `.wma`, `.ape`, `.wv` and `.mka`, case-insensitively. Skip unsupported extensions.

ffprobe validates audio streams and finite positive duration. Attached artwork is allowed; ordinary video streams are rejected. Encode the first audio stream and retain original tags, recognizing common track/disc aliases such as `tracknumber`/`discnumber` for display. Re-encode every source using the current output profile, including lossy or same-codec inputs; another lossy encode can reduce quality.

Preserve existing output paths, hashes and signatures. Coexisting same-named sources with different extensions can conflict, such as `01.flac` and `01.mp3`. Do not automatically select or overwrite another present source's artifact: expose the error in Jobs and Library and resolve it by renaming the source and rescanning. When only the source extension changes and the old source is confirmed absent, the new source inherits the registered artifact at the same path and rebuilds it as a content change. This applies to any supported source formats in either direction, regardless of whether either format is lossy or lossless. Replace it atomically after validation; failure, pause or stop preserves the old audio, and existing failed tasks can be retried directly. Lidarr Download/Upgrade accepts the same source extensions; explicitly replaced artifacts retain the existing validated-album cleanup rules.

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

Scan supported audio files and store artist, album, title, track/disc number, metadata, size and modification time in SQLite.

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
- Manually remove missing-source index records without artifacts, individually or in bulk. Preserve files, task history and optional annotations; skip records referenced by pending/running/paused work, registered ownership or recovery journals. The authenticated, CSRF-protected operation accesses SQLite only, returns removed/skipped counts and rejects cleanup during a file operation. A restored source is indexed again on scanning. Artifact deletion remains a separate worker operation with its existing persisted arguments.

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
- Otherwise extract from the first audio file with artwork in track order.
- Generate one `cover.jpg` per output album.
- Update external artwork independently of audio encoding.

## LRC lyrics maintenance

- Maintain a same-name `.lrc` beside registered playable audio, such as `01.opus` and `01.lrc`, independently of encoding. Prefer the source's same-name `.lrc`, also accepting `.LRC`. Accept only synchronized UTF-8 lyrics up to 1 MiB; normalize BOM and line endings. Sources stay read-only and use the existing cancellable, time-bounded source I/O subprocess.
- When source lyrics are missing, search [LRCLIB](https://lrclib.net/docs) → QQ Music → Kugou → NetEase, without user API keys. Borrow the multi-provider matching and format-handling approach from [LDDC](https://github.com/chenmozhijin/LDDC), implementing adapters independently in Go. Regional providers use unofficial interfaces and can be affected by service changes or region restrictions. LRCLIB attempts signature lookup before search; missing synchronized results, recording mismatches and request failures fall through to the next provider. Convert cloud QRC and enhanced lyrics to standard line-synced LRC; never manufacture timestamps for plain lyrics or instrumental tracks.
- NetEase uses EAPI anonymous sessions, cached only in memory for up to ten days and renewed on expiry. Optional `MUSICFORGE_NETEASE_COOKIE_FILE` points to a read-only, regular secret file containing a single Cookie header value with `MUSIC_U` (at most 16 KiB). Re-read each operation; changed credentials clear auth cooldowns, preserving HTTP rate limits. Credential/verification rejection pauses only NetEase for thirty minutes (one minute for guests), with sanitized diagnostics. Guest session expiry invalidates it for renewal on a later retry. Cookies never enter Settings, database metadata, journals, logs or API responses; no automated account-verification flow.
- Automatic adoption requires matching title, artist, supplied album and duration within two seconds. Matching preserves Unicode letters and handles full-width Latin characters, case, punctuation, collaboration separators/order and explicit aliases returned by providers. Preserve Live/Remix and other recording-version words. Ambiguity, insufficient metadata and crowded matches need manual selection. Download at most three matching candidates per provider automatically; conflicting lyrics fall through, with unresolved candidates marked for review.
- Library provides individual re-search, selected/all-artifact filling, automatic filling, progress and stop. Automatic filling defaults off and handles new/rebuilt or unchecked artifacts. Retry failures after at least an hour and missing/instrumental/review results after at least seven days. Re-run filling or individual search to notice source-LRC changes or manually removed output lyrics after a successful check. Batch filling keeps existing valid lyrics; individual re-search may refresh online lyrics. Failures or no match preserve existing files.
- Automatic maintenance runs one background batch independently of audio Jobs, returning immediately and showing progress in LRC lyrics. Track checks and per-provider diagnostics persist. Stop cancels reads/requests and disables automatic filling, retaining published LRC. Progress does not survive restart; an enabled automatic option selects unfinished targets again. Requests to each provider are serialized at least 800ms apart with independent `Retry-After` cooldown and jitter; long cooldowns fall through immediately. Network waits release file locks and do not affect audio job state or retry budgets.
- A track's **Choose lyrics** supports original-title/alias queries, showing up to eight candidates per provider in priority order. Providers load independently so partial results survive a provider failure. Preview before explicitly saving; original lyrics are the default, with optional merging of existing synchronized translations/romanization and no automatic translation. Candidate sets/previews stay in memory for ten minutes, with at most sixteen sets and 16 MiB of lyric content, and are discarded on restart. Search/preview never publish files. Saving checks the captured source/output roots, audio path/signature and previous lyrics signature, rejecting intervening moves, rebuilds or lyric changes. Background publication shares this protection against concurrent selections.

- Replace/delete only LRC files whose SHA-256 matches their ownership annotation. Preserve and report unregistered, symlinked, edited or conflicting files. Lyrics follow byte-identical audio moves, share one path across codec switches, survive ordinary source deletion and are cleaned with explicit artifact deletion. The candidate reconciles audio moves/deletions made by older applications during its first file recovery. Publish through registered temporary files and atomic operations; creating a new LRC uses a hard link to avoid overwriting concurrently created user files, so output storage must support hard links and directory sync. Successful changes reuse dirty-directory Navidrome refreshes.
- Keep schema 2, Settings JSON, existing job kinds/arguments, audio/cover ownership and recovery journals unchanged. Optional `meta` keys: `lyrics:options` stores automatic filling; `lyrics-status:<source_id>` stores the last check bound to output path and audio SHA-256 with optional per-provider `attempts`, with `checked` in Unix seconds; `lyrics-file:<SHA-256 of relative LRC path>` stores relative path, source ID, built source hash, LRC SHA-256, optional pending-publication signature and provider. Temporary files retain `managed.kind='temp'`. Older applications preserve and ignore these annotations and LRC files without maintaining lyrics; supporting versions revalidate them. All new endpoints require administrator authentication and mutations require CSRF.

## Queue and recovery

- Treat a scan and its conversion, move and upgrade-cleanup queue as one controllable task. Execute the track queue after indexing finishes. Group a manual rebuild into one task as well, reusing existing active targets.
- Timer scans wait while any scan/build/move/delete/upgrade queue is active or paused; the next interval starts after that work finishes. Manual scans and Lidarr imports share the active scan task, merging scopes and verification without creating another parent task. Requests arriving after indexing are retained as a follow-up on the same task.
- Show Pending, Running, Paused, Success, Failed and Stopped tasks. Keep per-track states and retry budgets internally and aggregate the queue into one task-list entry.
- Select the current page or every task matching the current state filter across pages. Bulk actions skip ineligible tasks and report the number changed; deleting history retains audio.
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

FLAC compatibility recovery: only an ffmpeg decoding failure on a complete scratch file with a native `fLaC` header enables the independent reference `flac` decoder. Nonzero STREAMINFO MD5 and total samples in a supported 8/16/24/32-bit format enable error-tolerant raw PCM decoding, with independent streaming verification of the exact byte count and PCM MD5. The reference decoder uses an explicit sample endpoint to exclude trailing frames; the independent PCM MD5 remains mandatory even when the decoder skips its own end-of-stream check. A mismatch rejects the temporary artifact. Other streams retain strict RF64 decoding. PCM is piped into ffmpeg without a large scratch file or ReplayGain application. Existing metadata, external artwork and artifact validation still apply; the source is never rewritten. Both processes must exit successfully; pause, stop and idle timeout cancel the whole pipeline. This fallback belongs to the same processing attempt and creates no new job. A failed attempt removes the scratch file; the next attempt reads the source again. Schema 2, job arguments, encoding signatures and ownership formats remain compatible; diagnostics remain ordinary log text and API error categories are derived from it.

### Manual best-effort repair and import

Indexed native FLAC tracks with decoding failures and no existing playable output offer a per-track, explicitly confirmed best-effort repair. Failed-track filters support page selection and selecting all matching tracks across pages; task details can select repairable tracks on the current page. A batch receives one explicit confirmation, skipping unsupported formats, stale fingerprints and existing playable artifacts. Confirmation warns about missing audio, silence and sudden loud noise. Successful audio uses the normal filename, current encoding and output hierarchy, entering the existing dirty-directory Navidrome scan flow. MusicForge always marks it possibly damaged, even if PCM verification happens to pass; filenames and audio tags have no added damage marker. Sources and existing artifacts are not replaced, and the repair annotation retains the original strict failure diagnostics.

Reference FLAC tolerant decoding records actual/declared samples and PCM MD5. Missing samples or a checksum mismatch are allowed only for this manually confirmed mode; empty PCM, incomplete samples, excess data, encoding failures and invalid output are rejected. Output duration is verified against actual decoded samples. Source hashes, paths, attributes and encoding settings are checked before and after processing. Cancellation before publication and explicit retries and explicitly confirmed batches are supported without automatic opt-in; cancellation after publication does not delete an artifact.

Each batch runs sequentially as one repair queue, waiting for the existing conversion capacity when busy. At most one repair runs at a time within that concurrency limit; periodic scans defer while the repair queue is active. Unfinished operations do not automatically resume after restart. Temporary outputs retain the existing `managed.kind='temp'` registration; publication reuses unchanged promotion journals, artifact SHA-256 signatures and ownership rules. Optional `meta` keys `repair-artifact:<source_id>:<artifact_sha256>` store JSON diagnostics (state, actual/declared samples, PCM verification, actual duration in seconds and diagnostics including the original error) in the transaction that records the promotion journal. Only annotations matching the currently owned audio signature show a warning. Scans, restarts, normal moves and older promotion recovery retain that relationship. Previously queued automatic retries accept a completed manual import without restoring a source failure when no new source error exists. Explicit strict rebuilding does not skip marked artifacts merely because build hashes match; success clears the current warning while failure and its retries preserve the playable output and warning. Historical annotations do not mark a new normal artifact. Older applications do not display the new warning but retain optional annotations while reading/writing standard artifacts.

Jobs combines repair queues with scans and conversions in one list, ordered by creation time with shared filters and pagination. Stop cancels the entire repair queue including waiting tracks; finished records support bulk deletion, and details show per-track outcomes. Ordinary strict retry does not consent to best-effort repair. Optional `meta` keys `repair-task:<random 64-character hex ID>` store JSON history: source ID/hash, track details, encoding profile, progress (0–1), creation/update timestamps in Unix seconds, output path, diagnostics and stop-request status. States are `running`, `success`, `failed` and `stopped`; read counters reuse Activity byte units. Startup marks leftover running records interrupted, without resuming or including them in automatic/bulk retries. Optional `meta` keys `repair-member:<repair history ID>` store the root repair history ID for grouping; existing history without this annotation remains independently visible. Waiting members retain the existing `running` state with the `repair_wait` Activity phase, allowing older startup recovery to mark them interrupted. History is not executable work and adds no persisted job kind; older versions ignore and retain it and its grouping annotations. Deleting history does not remove standard artifacts or `repair-artifact` annotations. Attempts made before this feature cannot be reconstructed.

Generation/cancellation require administrator authentication and CSRF protection. Optional `can_repair`, `repair`, `possibly_damaged` API fields and the `possibly_damaged` status support library display and filtering. No schema-2 tables/columns, Settings, existing job arguments, source metadata, published recovery-journal formats or artifact-signature meanings change.

## Scan triggers

- Configurable periodic scans discover deletions, moves and missed changes.
- Manual scans support first import, troubleshooting and full verification.
- Lidarr webhooks scan affected directories and create conversion jobs promptly.

## Lidarr integration

Endpoint: `POST /api/webhook/lidarr`.

- Authenticate with a separate integration secret, scoped to source-library scan/build triggers.
- Accept native import/upgrade payloads: `eventType=Download`, with `isUpgrade` identifying upgrades.
- Use `trackFiles[].path` and upgrade `deletedFiles[].path` to locate directories; no extra album-path field is required.
- Validate every notified track through the existing `upgrade` job and New/Old arguments. Ordinary imports use empty Old, preserve old artifacts and do not repeat full source reads. If the mount has not exposed a file, rescan its directory within the same task, retaining the wait across restarts and displaying the expected source path. An empty scan does not complete an import; ordinary deletion scans retain their empty-directory semantics.
- Accept connection-test events without creating real conversion work.
- Support one source-prefix mapping for different container mount paths; nonempty prefixes must be absolute container paths, checked when saving.
- Confine mapped paths to MusicForge's configured source root.
- Use Lidarr's native Username/Password fields for HTTP Basic webhook credentials.

### File-count reconciliation

Library provides a manual Lidarr comparison using authenticated GET requests for imported files per artist and unmapped files. Compare deduplicated, exactly mapped relative paths against the current source index. Keep cover files, track listings and output counts separate. Count other source roots separately and explain unsupported, unindexed, expired, unimported, unmapped and failed files without treating an absent index entry as proof of a missing disk file.

One background comparison runs at a time; API calls return immediately and polling reads in-memory progress. Each remote request times out after 20 seconds; a comparison is limited to 10 minutes. Do not redirect API keys or accept partial lists as complete. Comparison does not read audio, reserve conversion capacity or create persistent jobs. Selected difference directories use the existing coalesced scan queue. It never writes to Lidarr or automatically deletes sources/artifacts. Results are snapshots cleared on restart.

Edit the Lidarr URL and API key in Settings → Integrations → Lidarr, saving them atomically with other settings. The Library comparison shows connection status and links to that section. The optional `meta` key `lidarr:connection` holds JSON `url`/`api_key`. A blank key preserves the key for the same URL; changing the URL requires a new key; clearing the URL clears both. Responses expose only the URL and whether a key is configured. Require administrator authentication and CSRF for writes. Older versions retain this optional key. Settings, schema 2, job arguments, recovery journals and artifact formats remain unchanged; rollback CI checks connection retention through older writers.

## Navidrome integration

- The corresponding Navidrome library root is MusicForge's output root with identical album-relative paths.
- Provide manual refresh.
- Manual refresh uses saved settings and returns the queued result immediately, without waiting for a remote scan in the HTTP request. Reject enqueueing when the library is disabled or Navidrome is unconfigured. Deferred scans retain a readable reason and expected retry time instead of presenting an internal error.
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
- Sessions retain a seven-day lifetime. Authentication checks renew valid sessions within their last day for another seven days without rotating the cookie token or CSRF, and never revive expired or revoked sessions. Foreground pages check every thirty minutes, on visibility return and browser-cache restoration. Temporary verification failures preserve the page; confirmed expiry locks it for reauthentication. Local reauthentication preserves mounted form drafts. Only reads may be retried once automatically; mutations require explicit resubmission.
- OIDC started elsewhere first redirects to the primary URL; callbacks and completed logins stay there. Identity binding requires a local administrator session on the primary URL. Settings displays the primary and allowed origins read-only without changing persisted authentication formats.

- Local login/setup allow ten credential failures per client in fifteen minutes and reset on success. OIDC starts have a separate budget, reset on successful OIDC login; local recovery and authenticated binding remain independent.
- By default, rate limiting uses the connection peer. Explicit trusted proxy CIDRs enable `X-Forwarded-For`, walking right to left to the first untrusted address; this header never supplies identity.
- Blank secret fields retain saved values. Explicit actions delete the Navidrome password or OIDC client secret after disabling the relevant integration. OIDC deletion requires local login and revokes OIDC sessions, flows and binding.

## Web UI

Use React, TypeScript, TailwindCSS and shadcn/ui.

- Overview: source/output counts, build completion and recent jobs.
- Desktop navigation supports a browser-persisted icon-only sidebar. Overview uses actual current-profile counts in its circular completion meter, encoding settings and recent task summaries; decorative waveform art carries no statistical meaning. Jobs use compact state-colored cards and progress bars with accessible percentages, retaining live source paths and existing controls. Small-screen navigation and reduced-motion support remain available.
- Library: browse Artist → Album → Track using source-relative directories, breadcrumbs, disc/track ordering, search and status filters. Select artists, albums or tracks for bulk actions with clear expired/rebuild status. Output keeps the source library’s relative hierarchy.
- Library provides artist/all-album views, grid/list layouts, name/attention sorting, pagination and shareable hierarchy URLs. Albums show existing output `cover.jpg`; artist artwork is an album-cover mosaic, not an independent portrait. Missing or unreadable images use a fallback without blocking browsing. Album details expose durations, multi-disc groups, source/output paths and indeterminate partial-selection checkboxes.
- Jobs: whole scan/build queue states, live track progress, human-readable details, queue controls and bulk history cleanup.
- Settings: paths, encoding/mode/parameters, concurrency, scan interval, OIDC and integrations.
- Login and first-run setup.
- English and Simplified Chinese; initially use browser preference, falling back to English. Provide a selector and persist manual selection in the browser.
- Localize copy, common API errors, dates and numbers. Switching preserves unsaved input and filters; tags, paths and raw diagnostics retain original content.
- Light, dark and system appearance; system is the default and manual choices persist in the browser. Switching does not remount page state. Desktop cover grids adapt to container width and small/medium/large sizing; cover size and grid/list preferences persist in the browser. At 800px and below, retain two columns, hide cover sizing and place filtering/sorting in a keyboard-accessible menu that closes after selection; Escape closes it and restores focus. Artwork uses 8px corners; loading, selection and press states give brief feedback. Supporting browsers carry album artwork into its detail header with a shared-element transition; polling never replays entrances and all motion honors reduced motion.
- Polling pauses in the background, resumes in the foreground and backs off during temporary failures while retaining loaded data. Foreground checks and reauthentication do not overwrite loaded Settings drafts. Errors retain request paths and IDs for correlation with server logs; failed-action notices remain until dismissed, while success notices expire after six seconds.

## Configuration and quality

- Set startup options such as listening address/log level through a file or environment variables.
- Store Web-editable paths, encoding and integration settings in SQLite.
- Each setting has one authoritative source; file configuration and database must not overwrite each other.
- Provide structured logging, health endpoint, migrations, README and single-service Compose example.
- APIs return `X-Request-ID`, shared by failure and slow-request logs. Deferred work, SQLite busy errors and request timeouts use retryable 503 responses with `Retry-After`, never masquerading as invalid credentials. Optional response fields `artwork_url`, `waiting_reason` and `retry_at` (Unix seconds) are derived without persistence changes. The authenticated artwork endpoint serves only registered nonempty-signature JPEG covers inside the output root, never reading the remote source.
- Support `linux/amd64` and `linux/arm64` images.

## Persistence compatibility and rollback

- Schema 2 is the stable MVP baseline. The `0.2.x` series keeps this structure and the published table/column meanings.
- Compatibility includes Settings JSON, job kinds/arguments, metadata/recovery journals, ownership markers and artifact paths/signatures. Keeping the schema number while changing formats or units is incompatible.
- Same-series releases must support older compatible readers and writers. On both architectures, CI opens the candidate's same volumes with published older images, checks library, credentials, jobs and artifacts, then returns to the candidate.
- Incompatible changes require a clearly identified breaking release with an upgrade backup procedure and a verified restore-and-rollback path. Preserve historical migrations; do not force a lower database version.
- Complete state rollback requires matching stopped `/config` and output snapshots. Switching images cannot undo completed replacements, moves or deletions. Published version tags and images are immutable.

Task presentation is additive metadata: `task-member:<job ID>` contains the decimal root job ID, `task-control:<root ID>` contains `paused` or `stopped`, and `task-progress:<job ID>` contains a JSON phase/path/tag/counter snapshot (`processed`/`total` are file counts, `percent` is 0–100). Older releases ignore and retain these optional keys. Primitive job kinds/arguments and table/column definitions remain unchanged. A pause keeps a job `pending` with `not_before=253402300799` (Unix seconds); stopping uses the existing terminal `failed` state and an explicit log message without incrementing actual failed attempts. Older UIs show these as deferred/failed individual jobs; the new UI restores grouping when reopened. Progress annotations are diagnostic snapshots, never file-recovery journals.

`source_root` retains its acknowledged root's decimal `device:inode` format and meaning. Named `fuse.rclone` sources add optional `source_mount` JSON: `identity` must match `source_root`, and `signature` is the hex SHA-256 of a JSON array `[filesystem type,mount source,effective source directory]`; the mount source is never stored verbatim. The effective directory combines the mountinfo root with the source's path relative to the mountpoint. Transient mount IDs, devices, inodes, UID and mount options are excluded. Establish a witness only on an acknowledged root, then transactionally update both keys on a same-signature reconnect. Missing, different or unconfirmed sources still defer work. Legacy roots learn a witness when their ID still matches; already-remounted roots require one verified Settings save. Old versions ignore and retain the optional key; their explicit `source_root` saves invalidate stale witnesses. Other filesystems and generic rclone device labels retain strict root checks. Verification does not read audio contents.

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
