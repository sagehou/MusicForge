# Production hardening review

This ledger tracks six successive review/fix/validation rounds against the bilingual MVP baseline. Production readiness requires evidence for file ownership and replacement safety, incremental correctness, restart recovery, integrations, authentication, current stable dependencies, maintainability, and usable bilingual UI. No claim of absolute defect freedom is made.

All executable checks, dependency resolution and builds run in GitHub Actions. Local operations are source edits and static inspections only. Successful CI does not replace missing scenario coverage.

| Round | Scope | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Upgrade cleanup, scoped moves/copies, damaged source isolation | Regression verification in progress | `internal/forge/safety_test.go` |
| 2 | Queue, retries, interruption, atomic publication and deletion recovery | Pending | — |
| 3 | Native Lidarr/Navidrome integration behavior and concurrency | Pending | — |
| 4 | Administrator/OIDC security, settings and deployment boundaries | Pending | — |
| 5 | Latest stable dependencies/toolchains/images/actions, upgrade automation and operational validation | Pending | — |
| 6 | Full acceptance audit and focused UI/accessibility/responsive improvements | Pending | — |

## Round 1

Initial audit findings: upgrade cleanup trusts stale index state during the source quiet period; a scoped scan only detects moves whose old paths are inside its scope; a corrupt source can stop processing healthy tracks. [Regression-only CI](https://github.com/sagehou/MusicForge/actions/runs/37010589260) confirmed stale-index cleanup and redundant move indexing, and revealed that ffprobe can identify invalid bytes as FLAC with no valid duration. Source validation must check duration as well as codec.

Implemented: destructive cleanup checks current source bytes, stat stability, source errors and output ownership; scoped rename detection checks actual old-path absence and preserves copies; invalid sources are indexed with per-file errors and invalid hashes while healthy files continue. Incomplete scans preserve unseen sources. Ordinary scans cache unchanged invalid-source failures until changes or full verification. Regression cases cover stable replacement gating, moves versus copies, empty/invalid input, healthy-track progress and recovery after repair. Fix validation pending.

## Upstream comparison and dependencies

Record upstream release metadata, source references and resulting decisions here as each relevant round progresses. Check stable releases rather than assuming pinned historical versions are current. Fetching metadata is read-only; installing/resolving dependencies and generating lockfiles is CI-only. Preserve a reproducible dependency graph and add ongoing upgrade checks before final delivery.
