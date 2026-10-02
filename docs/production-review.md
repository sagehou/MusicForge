# Production hardening review

This ledger tracks six successive review/fix/validation rounds against the bilingual MVP baseline. Production readiness requires evidence for file ownership and replacement safety, incremental correctness, restart recovery, integrations, authentication, current stable dependencies, maintainability, and usable bilingual UI. No claim of absolute defect freedom is made.

All executable checks, dependency resolution and builds run in GitHub Actions. Local operations are source edits and static inspections only. Successful CI does not replace missing scenario coverage.

| Round | Scope | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Upgrade cleanup, scoped moves/copies, damaged source isolation | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37011006737), `internal/forge/safety_test.go` |
| 2 | Queue, retries, interruption, atomic publication and deletion recovery | Validation in progress | `internal/forge/deletion_test.go`, existing recovery/retry tests |
| 3 | Native Lidarr/Navidrome integration behavior and concurrency | Pending | — |
| 4 | Administrator/OIDC security, settings and deployment boundaries | Pending | — |
| 5 | Latest stable dependencies/toolchains/images/actions, upgrade automation and operational validation | Pending | — |
| 6 | Full acceptance audit and focused UI/accessibility/responsive improvements | Pending | — |

## Round 1

Initial audit findings: upgrade cleanup trusts stale index state during the source quiet period; a scoped scan only detects moves whose old paths are inside its scope; a corrupt source can stop processing healthy tracks. [Regression-only CI](https://github.com/sagehou/MusicForge/actions/runs/37010589260) confirmed stale-index cleanup and redundant move indexing, and revealed that ffprobe can identify invalid bytes as FLAC with no valid duration. Source validation must check duration as well as codec.

Implemented: destructive cleanup checks current source bytes, stat stability, source errors and output ownership; scoped rename detection checks actual old-path absence and preserves copies; invalid sources are indexed with per-file errors and invalid hashes while healthy files continue. Incomplete scans preserve unseen sources. Ordinary scans cache unchanged invalid-source failures until changes or full verification. Regression cases cover stable replacement gating, moves versus copies, empty/invalid input, healthy-track progress and recovery after repair. Fix validation passed backend race/integration tests, all browser tests and native amd64/arm64 image smoke tests.

## Round 2

Review found unlink/registry/source-update crash windows in expired deletion, shared artwork cleanup incorrectly treating the output root as empty, and recovery discarding promotion journals on unreadable/cancelled files. Added durable deletion intent before unlink, directory synchronization, atomic registry/source/refresh/journal completion, replay that preserves unregistered replacement files, correct root artwork reference counting, and retention of interrupted/unreadable promotion journals. Claims continue to serialize builds of the same source across profiles.

New regression cases simulate restart after unlink and unregister, verify idempotent cleanup and refresh retention, refuse foreign replacement deletion, preserve root-level shared artwork and check per-source claim serialization. Existing tests cover encoder failures retaining playback, promotion replay and three-attempt retry exhaustion. Await round-two CI.

Durability design reference: [SQLite atomic commit](https://www.sqlite.org/atomiccommit.html) describes flush ordering and filesystem assumptions; [WAL documentation](https://www.sqlite.org/wal.html) requires host-local shared state. Audio file promotion also needs directory synchronization and a recoverable journal, since filesystem and SQLite commits are separate operations.

## Upstream comparison and dependencies

Record upstream release metadata, source references and resulting decisions here as each relevant round progresses. Check stable releases rather than assuming pinned historical versions are current. Fetching metadata is read-only; installing/resolving dependencies and generating lockfiles is CI-only. Preserve a reproducible dependency graph and add ongoing upgrade checks before final delivery.

2026-10-02 metadata snapshot: Go 1.27.1; Node 26.10.0 stable / 24.21.0 LTS; React 19.3.0; Vite 8.3.2; TypeScript 7.0.2; Tailwind 4.3.3; Playwright 1.63.0; go-oidc 3.21.0, x/crypto 0.57.0, x/oauth2 0.37.0 and modernc SQLite 1.60.1. Upgrade and compatibility validation are pending round 5. Sources: npm's published `latest` metadata, Go module proxy `@latest`, `go.dev/dl/?mode=json`, and `nodejs.org/dist/index.json`.
