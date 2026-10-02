# MusicForge acceptance evidence

**English** · [Simplified Chinese](acceptance.zh-CN.md)

The accepted behavior is defined in the [MVP baseline](mvp-design.en.md). The [six-round review ledger](production-review.md) records findings, corrections and validation links. All six review rounds passed executable validation. The final full-suite [CI](https://github.com/sagehou/MusicForge/actions/runs/37025923050) includes backend race/integration/vet checks, eight Chromium browser checks and native amd64/arm64 image smoke tests. The matrix records verified behavior and its limits; container publication is a separate version-tag gate.

| Contract | Evidence in GitHub Actions |
| --- | --- |
| Opus/MP3, VBR/CBR, tags, ReplayGain and one external cover | Real ffmpeg lifecycle tests; both native images encode and probe Opus/MP3 |
| Incremental builds, full verification and tag changes | Incremental/lifecycle tests including preserved size/mtime, failed replacement and unchanged output |
| Renames versus copies, scoped moves and damaged sources | Safety regressions preserve identities, avoid redundant encoding and isolate invalid inputs; a damaged middle-frame CRC rejects replacement and preserves playable output |
| Ordinary deletion retention and explicit upgrade cleanup | Safe upgrade regressions; native Lidarr Download/Upgrade HTTP payloads with Basic/Bearer auth |
| Retry budget, interrupted work and atomic publication/deletion | Retry/recovery tests; promotion/deletion journals replay idempotently and preserve foreign replacements |
| Repeated imports and refresh concurrency | Durable trailing scans, merged scopes, restart retention and monotonic dirty stamps |
| Navidrome playback-library lifecycle | Official Navidrome 0.64.2 container reads output only; discovers converted tracks, retains ordinary deletions and removes manually deleted artifacts |
| Local administrator/OIDC boundaries | CSRF, setup, password reset, spoofed proxy headers, signed test-provider OIDC/PKCE/replay and revoked binding sessions |
| Storage/ownership/schema boundaries | Overlap/symlink/offline guards, instance lock, managed-file conflicts; schema 1→2 preserves artifacts, ownership and exhausted retry budgets, and newer schemas are rejected |
| Bilingual UI, pagination and mobile behavior | Real application in Chromium; Chinese/English detection and persistence, unsaved form retention, expiration confirmations, stale-data recovery, mobile logout and screenshots |
| Stable dependencies and maintenance | Registry/official release metadata, Actions-generated lockfiles, npm audit, weekly updates with branch CI and a PR/compare-link fallback |
| Publication | Native amd64/arm64 checks gate version-tag GHCR publication; default-branch and PR validation do not publish |

## Verified scope and deployment limits

Integration HTTP tests exercise native Lidarr payloads against MusicForge; CI does not run a complete Lidarr installation. OIDC validation uses a signed test provider, not a deployed Authentik installation. A real current Navidrome container is included. Browser acceptance runs Chromium; other browsers are not separately certified.

Ordinary scans intentionally trust size and mtime, as agreed. Use full verification if an external editor preserves both. Output remains dedicated to MusicForge and `/config` must use local storage suitable for SQLite WAL. Keep source and output mounts separate and grant Navidrome read-only output access.

The repository currently prohibits bot-created PRs. The maintenance workflow still pushes update branches, dispatches CI and provides a compare link/source artifact. A repository administrator can enable bot PRs; manual review and merge remain required either way.

Back up stopped `/config` before upgrading. Schema 2 cannot be opened by version 0.1; rollback requires its matching pre-upgrade backup. Version tags repeat validation before publication; actual publication, image digest and platform evidence are recorded in the [v0.2.0 release](https://github.com/sagehou/MusicForge/releases/tag/v0.2.0).
