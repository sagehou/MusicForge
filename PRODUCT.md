# MusicForge

This is a source-derived product record, not an interview-approved brand brief.
The agreed behavior and compatibility baseline remains `docs/mvp-design.md`.

## Product

MusicForge is a self-hosted, single-container streaming-library build tool.
Lidarr manages source audio, MusicForge generates playback artifacts with
ffmpeg, and Navidrome serves the output. MusicForge is not a music player.

## Users And Work

One administrator configures source/output mounts and the encoding profile,
inspects the library, monitors asynchronous work, and recovers failed jobs.
The interface supports repeated operational work in English and Simplified
Chinese on desktop and mobile, with light, dark and system appearance.

## Boundaries

- Sources are read-only; managed outputs keep their source-relative hierarchy.
- Existing album covers are real product imagery. Artist mosaics use those
  covers; independent artist portraits are not currently indexed.
- Temporary failures preserve loaded data; confirmed session expiry protects
  the application without discarding mounted local-login form drafts.
- Schema 2 and same-series rollback compatibility remain mandatory.
- Dependency installation, builds and executable validation run only in
  GitHub Actions. No local build or dependency download is permitted.
- Changes do not authorize deployment, merging or image publication.

## Authority

Implementation may establish provisional design decisions. Brand colors,
visual signoff and production incident causes require user evidence or
verification and must not be inferred from this record.
