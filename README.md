# MusicForge

**English** · [简体中文](README.zh-CN.md)

Keep your original music. Make a lighter copy for everyday listening.

MusicForge turns an existing audio library into Opus or MP3, then keeps that copy up to date as you add or change music. Your source files stay read-only. A common setup is Lidarr → MusicForge → Navidrome, but you can also start with a folder of music and scan it yourself.

```text
Your music collection → MusicForge → Streaming library → Navidrome
     read-only           ffmpeg         Opus / MP3          read-only
```

One container, one administrator, one source library and one output library. Available for `linux/amd64` and `linux/arm64` at `ghcr.io/sagehou/musicforge`.

## What you can do

- **Keep conversion incremental.** New and changed tracks are queued automatically. Byte-identical moves keep their audio without another encode. Tags, ReplayGain and album covers come along.
- **See what is happening.** Browse artists, albums and tracks; follow reads and conversions; pause, resume or retry queues. Failed replacements keep the playable output.
- **Fill in synchronized lyrics.** Use local LRC files or choose online sources and their priority. Preview candidates, import LRC, adjust timing and include available translations or romanization.
- **Tidy the listening copy.** Correct output tags and covers, fill missing loudness tags, review quality issues, map playlists and compare possible audio duplicates. Recent edits can be reviewed and restored.
- **Fit work around your day.** Set build windows and concurrency, prioritize new tracks, and send webhook or ntfy notifications when something needs attention.
- **Use the setup you already have.** Receive Lidarr import/upgrade events, refresh Navidrome, and optionally sign in with OIDC. The interface supports English, Simplified Chinese, phones and light/dark themes.

MusicForge prepares a library for your player; its built-in audition helps you check edits. It does not manage downloads, cloud mounts, multiple users or multiple libraries.

## Get started with Docker Compose

You need Docker Engine with Compose v2, an existing music folder and a **dedicated, initially empty output folder**. Keep `/config` on local storage suitable for SQLite, not NFS or SMB.

1. Get the deployment files:

   ```sh
   mkdir musicforge && cd musicforge
   curl -fsSLO https://raw.githubusercontent.com/sagehou/MusicForge/main/docker-compose.yml
   curl -fsSL https://raw.githubusercontent.com/sagehou/MusicForge/main/.env.example -o .env
   ```

2. Edit `.env`. Choose host directories and `PUID`/`PGID`; pin `MUSICFORGE_VERSION` to a version from [Releases](https://github.com/sagehou/MusicForge/releases), or use `latest` for stable releases.

   | Host setting | Container path | Access |
   | --- | --- | --- |
   | `CONFIG_DIR=/srv/musicforge/config` | `/config` | Read-write; local disk |
   | `FLAC_DIR=/srv/music/flac` | `/music/source` | Read-only; mixed formats are fine |
   | `OUTPUT_DIR=/srv/music/streaming` | `/music/output` | Read-write; dedicated output |

   `FLAC_DIR` is a retained deployment variable name, not a FLAC-only restriction. Keep all three directories separate.

   Set `MUSICFORGE_PUBLIC_URL` before starting: use `http://localhost:8787` for access on the Docker host, or your actual HTTPS origin when using a reverse proxy.

3. Create the writable directories. For the default UID/GID, run with the required host permissions:

   ```sh
   mkdir -p /srv/musicforge/config /srv/music/streaming
   sudo chown 10001:10001 /srv/musicforge/config /srv/music/streaming
   sudo chmod 700 /srv/musicforge/config
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose logs musicforge
   ```

   The same UID needs read access to your source music. Startup enforces private permissions on `/config`, which holds accounts and secrets. Compose refuses missing host paths.

4. The example binds to `127.0.0.1:8787`. For remote access, forward your HTTPS origin to it through a reverse proxy. The app serves from `/`. See [proxy setup](docs/deployment.md#reverse-proxy-and-access-origins).

5. Open the UI, enter `setup_code` from the first-start logs and create your administrator account. In **Settings**, enter the container paths `/music/source` and `/music/output`, enable the library and save. Start a scan. The default is **Opus VBR at 192 kbps**; choose **MP3 VBR V2** if your players need MP3.

Give Navidrome a read-only mount of the same output folder. Try one small album first: check the tags, play a track and scan again to confirm it is skipped. The [deployment guide](docs/deployment.md) covers integrations, remote mounts, recovery and upgrades.

## Everyday use

| When this happens | MusicForge does this |
| --- | --- |
| You add music or change a source file | Builds the new or changed tracks |
| You move a byte-identical source | Moves its registered output without re-encoding |
| You change the encoding profile | Marks tracks **Needs rebuild**; you choose when to start |
| You delete a source normally | Keeps its output as **Expired** until you explicitly delete it |
| Lidarr replaces files during an upgrade | Cleans up explicitly replaced output after the replacements validate |
| A replacement fails | Keeps the existing playable file and shows the failure |

Ordinary scans use size and modification time. Use **Full verification** when a tool may have changed content without changing either. Unregistered output files are never silently overwritten. Two sources such as `01.flac` and `01.mp3` share an output name; rename one to resolve the conflict.

Library searches, filters and sorts run on the server and load 50 entries at a time. Cross-page selection stays with you; unchanged pages reuse their cache. Detailed maintenance data loads when you open the workbench.

Open **Library → Maintenance workbench** for tag, cover, loudness, playlist and quality tools. Saved views remember your usual filters in this browser. **Jobs** collects queue progress, maintenance batches and failures grouped by cause. Best-effort repair is an explicit option for eligible failed native FLAC tracks; its output stays marked **Possibly damaged**, so listen before keeping it.

## Lyrics, including Japanese songs

MusicForge uses real synchronized lyrics. It does not turn plain text into guessed timestamps.

Start with **Settings → Lyrics**: enable the sources you want, move them into order and save. Same-name source `.lrc` or `.LRC` files come first; online matches are ranked by score, with your priority breaking ties.

| Source | Use |
| --- | --- |
| QQ Music, Kugou, NetEase, LRCLIB, Kuwo, Migu | General online candidates; coverage varies by recording and region |
| PetitLyrics | An additional source for Japanese synchronized lyrics |
| [AMLL TTML DB](https://github.com/amll-dev/amll-ttml-db) | Optional community lyrics, including Japanese; preview and select manually because recording duration is unavailable |
| [Unison](https://unison.betterlyrics.org/docs) | Optional community lyrics; supported TTML line timing is converted to LRC |

The two community sources and automatic filling are off by default. Automatic matching starts at 96/100 and still checks recording identity and duration. A high score is a matching aid, not a guarantee that a particular live version or remaster is right.

**Fill lyrics** skips tracks that already have suitable lyrics, even after a restart or a settings change. Use a track's **Search again** to update changed source lyrics or restore a manually removed output LRC. **Choose lyrics** lets you preview another version; the editor can correct timing. Failed searches keep existing files, and lyrics you edited outside MusicForge are protected from replacement.

Lyric batches retain progress across restarts; choose **Continue** in Jobs or lyric settings. Audition supports individual text/timing edits, playhead alignment and line preview before saving.

**Settings → Lyrics → Lyric source checks** shows recent searches and cooldowns. Choose a song to check one source without saving lyrics. Candidate explanations point out differences in title, artist, version, album or duration.

Online services can be unavailable or rate-limited. NetEase works anonymously where allowed and supports an [optional private Cookie file](docs/deployment.md#optional-netease-lyrics-login). See the [lyrics behavior reference](docs/mvp-design.en.md#lrc-lyrics-maintenance) for matching, retries and file ownership.

## Supported source audio

FLAC, MP3, AAC/ALAC in M4A/M4B/MP4, raw AAC, PCM WAV/AIFF, Ogg/Vorbis/Opus, WMA, APE, WavPack and MKA can coexist in one library. Extensions are case-insensitive; ffprobe checks the actual audio and rejects ordinary video streams. All sources are re-encoded to the selected output profile, including lossy inputs, so another lossy encode can reduce quality.

## Mounted remote source libraries

An existing rclone/FUSE mount can be the read-only source. MusicForge reads each selected source into bounded local scratch storage before encoding; it does not manage the mount. Keep configuration and output on reliable local storage, and allow room for the default 4 GiB shared scratch limit. See [mount setup and troubleshooting](docs/deployment.md#remote-mounts-and-troubleshooting).

## Upgrades and help

Before upgrading, stop MusicForge and back up `/config`. To restore the complete library state, take a matching output snapshot too. Then change the image version in `.env`, pull and recreate the container. Switching back to an older image cannot undo completed file changes. The [upgrade guide](docs/deployment.md#upgrade-and-rollback) explains schema-2 compatibility and rollback.

For startup variables, multiple domains, OIDC and a forgotten password, use the [deployment guide](docs/deployment.md). When reporting a problem, include the image version, affected task/path and relevant logs with credentials removed. Please say what you expected and what happened.

Stop the app before making a backup. v0.3.0 includes configuration backup, integrity checking and restore into an empty directory; see [backup and restore](docs/deployment.md#backup-and-restore). Keep a matching stopped output snapshot to restore the complete library state.

## Development

[AGENTS.md](AGENTS.md) describes the development and release rules. Install dependencies, build and run executable tests **only in GitHub Actions**; local work is source editing and static review. CI selects documentation, presentation or full validation from the complete change. Backend checks, real-application browser checks and rclone checks have separate jobs, so a browser retry keeps successful Go results. Version tags publish checked AMD64/ARM64 images; ordinary branches and PRs do not publish.

- [Behavior and compatibility reference](docs/mvp-design.en.md)
- [Design guidance](DESIGN.md)
- [Validation coverage and limits](docs/acceptance.md)
- [Third-party notices](THIRD_PARTY_NOTICES.md)

Translations live in [web/src/locales](web/src/locales). Keep both languages and their placeholders aligned. Dependency versions live in their manifests and lockfiles; [dependency refresh](.github/workflows/dependencies.yml) prepares reviewable updates without merging or releasing them.
