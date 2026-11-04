# Validation coverage

**English** · [简体中文](acceptance.zh-CN.md)

This page describes what CI checks and what still needs a trial on your own deployment. The behavior reference is [mvp-design.en.md](mvp-design.en.md); exact acceptance and release gates live in [AGENTS.md](../AGENTS.md). Fix-by-fix history is available in Git history and [Releases](https://github.com/sagehou/MusicForge/releases).

## Published reference

The latest published reference reviewed for this cleanup is **v0.2.59**, source commit `987f2ffed87d3c4aca96a2d76a170f4e95da59b9`:

- [Main CI](https://github.com/sagehou/MusicForge/actions/runs/41775030154): full application validation, backend race/integration/vet, 104 browser cases (Chromium plus focused phone WebKit), real-application workflows, real Navidrome/rclone and native image checks.
- [Release CI](https://github.com/sagehou/MusicForge/actions/runs/41776429704): reused successful application evidence; fresh dependency audit, final AMD64/ARM64 image/Compose checks and schema-2 rollback through v0.2.58.
- [Release and image details](https://github.com/sagehou/MusicForge/releases/tag/v0.2.59): `ghcr.io/sagehou/musicforge:v0.2.59`, multiarchitecture digest `sha256:fc5f541e2c20b219d85205b63c10da5e06ffdd14aa7e8aeb935d829cea21982d`.

These runs establish the published reference, not automatic approval of later changes. Each candidate's Actions run records its own scope and evidence. Artifact downloads have limited retention; release links and commit IDs identify the source after artifacts expire.

## Coverage

| Area | Checks |
| --- | --- |
| Audio lifecycle | Real mixed-format inputs to Opus/MP3, VBR/CBR, tags, ReplayGain, artwork, decoding validation and failed-replacement retention |
| Incremental work | Unchanged scans, full hashes, moves versus copies, filename conflicts, source deletion retention and explicit cleanup |
| Jobs and recovery | Coalesced scans, retry budgets, pause/resume/stop, restart recovery, concurrent controls and publication/deletion journals |
| Mounted sources | Real rclone WebDAV/FUSE reads without VFS caching, stalls, byte progress, pause/resume, timeout/recovery and mount identity |
| Lyrics | Provider response fixtures, identity/duration matching, real LRC/TTML timing, priorities, caching, explicit selection and ownership conflicts; ready results skip I/O across repeated filling and restart |
| Maintenance | Output-only editing, signature/conflict checks, loudness, history/undo, playlist mapping, duplicate clues and storage reports |
| Authentication | Setup, local login/recovery, CSRF, trusted proxies, multiple origins and signed test-provider OIDC/PKCE/replay/revocation |
| Integrations | Native Lidarr HTTP payloads and path mapping; real Navidrome discovery, refresh, playback retention and explicit removal |
| Interface | Bilingual workflows, retained drafts, pagination, queue controls, focus, contrast, overflow and screenshots; real-app Chromium plus focused phone audition in WebKit |
| Compatibility | Both native architectures, Compose ownership/health and candidate → published schema-2 image → candidate round trips when required |
| Dependencies and publication | Locked dependencies, a separate current security audit, required aggregate checks and version-tag-only GHCR publication |

## Keep validation proportional

Documentation gets link/consistency/whitespace checks. Presentation changes get frontend typecheck/build, affected existing browser checks and representative screenshots. Functional, backend, dependency, deployment, workflow, test-configuration or uncertain changes use full applicable validation. The comparison includes all unreleased work, not just the last commit.

Trusted same-repository push/dispatch evidence can cover identical tested inputs or unchanged inherited work within the policy's seven-day/environment limits. PR/fork evidence cannot. `rollback-scope` records the baseline, selected checks and reused run; `validation-evidence` records successful application evidence; browser artifacts retain screenshots. A new version label does not by itself require replaying identical application checks. Every release still needs a current audit and both final native image checks, plus any required rollback. Never manually bypass failed gates.

## Limits to verify on your host

CI uses Lidarr payload fixtures and a signed OIDC test provider, not a full Lidarr or production Authentik deployment. Online lyric services and artwork searches are mocked for deterministic coverage; upstream availability, regional restrictions, credentials and a particular recording's match need real use. Navidrome and rclone are exercised as actual services.

Chromium covers the application; WebKit coverage is limited to phone audition. Other browser/platform combinations are not comprehensively certified. A 5,000-track API fixture and remote-stall tests are regression checks, not a large-library capacity guarantee. Changed library responses still contain the complete index.

Storage must meet the [deployment requirements](deployment.md). Ordinary scans trust size/mtime; use full verification for content edits that preserve both. Same-schema compatibility does not restore files already moved, replaced or deleted: complete rollback needs matching stopped config/output snapshots. Follow the [small deployment trial](deployment.md#try-your-deployment) before a full import.
