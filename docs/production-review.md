# Production hardening review

This ledger tracks six successive review/fix/validation rounds against the bilingual MVP baseline. Production readiness requires evidence for file ownership and replacement safety, incremental correctness, restart recovery, integrations, authentication, current stable dependencies, maintainability, and usable bilingual UI. No claim of absolute defect freedom is made.

All executable checks, dependency resolution and builds run in GitHub Actions. Local operations are source edits and static inspections only. Successful CI does not replace missing scenario coverage.

| Round | Scope | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Upgrade cleanup, scoped moves/copies, damaged source isolation | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37011006737), `internal/forge/safety_test.go` |
| 2 | Queue, retries, interruption, atomic publication and deletion recovery | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37011732986), deletion/recovery/retry tests |
| 3 | Native Lidarr/Navidrome integration behavior and concurrency | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37012740241), integration queue regressions |
| 4 | Administrator/OIDC security, settings and deployment boundaries | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37013229507), security regressions |
| 5 | Latest stable dependencies/toolchains/images/actions, upgrade automation and operational validation | Passed | [CI](https://github.com/sagehou/MusicForge/actions/runs/37016003413), real Navidrome + native encoders |
| 6 | Full acceptance audit and focused UI/accessibility/responsive improvements | Validation in progress | Bilingual browser workflow, mobile screenshots, job-filter regression |

## Round 1

Initial audit findings: upgrade cleanup trusts stale index state during the source quiet period; a scoped scan only detects moves whose old paths are inside its scope; a corrupt source can stop processing healthy tracks. [Regression-only CI](https://github.com/sagehou/MusicForge/actions/runs/37010589260) confirmed stale-index cleanup and redundant move indexing, and revealed that ffprobe can identify invalid bytes as FLAC with no valid duration. Source validation must check duration as well as codec.

Implemented: destructive cleanup checks current source bytes, stat stability, source errors and output ownership; scoped rename detection checks actual old-path absence and preserves copies; invalid sources are indexed with per-file errors and invalid hashes while healthy files continue. Incomplete scans preserve unseen sources. Ordinary scans cache unchanged invalid-source failures until changes or full verification. Regression cases cover stable replacement gating, moves versus copies, empty/invalid input, healthy-track progress and recovery after repair. Fix validation passed backend race/integration tests, all browser tests and native amd64/arm64 image smoke tests.

## Round 2

Review found unlink/registry/source-update crash windows in expired deletion, shared artwork cleanup incorrectly treating the output root as empty, and recovery discarding promotion journals on unreadable/cancelled files. Added durable deletion intent before unlink, directory synchronization, atomic registry/source/refresh/journal completion, replay that preserves unregistered replacement files, correct root artwork reference counting, and retention of interrupted/unreadable promotion journals. Claims continue to serialize builds of the same source across profiles.

New regression cases simulate restart after unlink and unregister, verify idempotent cleanup and refresh retention, refuse foreign replacement deletion, preserve root-level shared artwork and check per-source claim serialization. Existing tests cover encoder failures retaining playback, promotion replay and three-attempt retry exhaustion. Round-two CI passed backend race tests, browser checks and both native image smoke tests.

Durability design reference: [SQLite atomic commit](https://www.sqlite.org/atomiccommit.html) describes flush ordering and filesystem assumptions; [WAL documentation](https://www.sqlite.org/wal.html) requires host-local shared state. Audio file promotion also needs directory synchronization and a recoverable journal, since filesystem and SQLite commits are separate operations.

## Upstream comparison and dependencies

Record upstream release metadata, source references and resulting decisions here as each relevant round progresses. Check stable releases rather than assuming pinned historical versions are current. Fetching metadata is read-only; installing/resolving dependencies and generating lockfiles is CI-only. Preserve a reproducible dependency graph and add ongoing upgrade checks before final delivery.

2026-10-02 metadata snapshot: Go 1.27.1; Node 26.10.0 stable / 24.21.0 LTS; React 19.3.0; Vite 8.3.2; TypeScript 7.0.2; Tailwind 4.3.3; Playwright 1.63.0; go-oidc 3.21.0, x/crypto 0.57.0, x/oauth2 0.37.0 and modernc SQLite 1.60.1. Upgrade and compatibility validation are pending round 5. Sources: npm's published `latest` metadata, Go module proxy `@latest`, `go.dev/dl/?mode=json`, and `nodejs.org/dist/index.json`.

## Round 3

Coalescing previously dropped new scan requests while the matching job was running and discarded scopes of pending manual scans. Pending requests now merge scopes and verification flags; running jobs keep a durable trailing request, atomically consumed when the worker completes. Full scans dominate scoped requests. Restart retains the trailing request. Dirty-directory stamps are monotonic nanoseconds, preventing a concurrent mutation from being cleared by a refresh snapshot within the same second. Schema migration upgrades existing second-based stamps. Lidarr accepts unknown native fields but rejects trailing JSON. Tests cover native Download/Upgrade requests with Basic/Bearer authentication, mapping, safe cleanup gating, coalescing/restart and concurrent refresh retention. Integration HTTP tests use mock remote services; a real Navidrome service acceptance check remains pending.

## Round 4

OIDC unbinding/configuration changes previously preserved already-issued identity sessions. Changes now require local login and transactionally revoke OIDC sessions and flows together with persisted settings. Session issuance checks current binding/configuration under the same mutation lock, including client-secret changes. Password reset and session revocation now commit atomically. Public origins reject embedded credentials; integration URLs reject queries/fragments. Storage validation resolves configuration symlinks before overlap checks. HTTP request body/write timeouts bound stalled clients. Regression tests cover local-only changes, revoked identity access, resolved overlap and unsafe URL inputs.

## Round 5

Upgrade target includes direct and compatible transitive application dependencies, Go/Node, Actions pinned to current stable commit SHAs, Debian 13 stable, FFmpeg 9.0.2 and libopus 1.6.1 (official Opus website is newer than GitHub `releases/latest` 1.5.2). LAME's official website identifies 4.0 as current stable; the earlier 3.100 assumption was corrected before acceptance. Removed unused PostCSS/autoprefixer configuration while migrating Tailwind 4 to its native Vite plugin. The same checksum-pinned media toolchain is built in Actions for validation and in native release images. Corresponding source archives and build instructions ship in the image. Lockfiles and initial media source hashes must be generated by the dependency-refresh Action before normal CI can validate this upgrade. Weekly updates create reviewable PRs and explicitly dispatch CI because pushes made with GITHUB_TOKEN do not trigger push workflows. No automated merging/publication occurs.

Dependency bootstrap [Action](https://github.com/sagehou/MusicForge/actions/runs/37013930817) successfully generated updated npm/Go locks and pinned media hashes. Initial upgrade CI before that bootstrap failed on missing media checksums, as expected; do not use that commit as a release. Added durable Navidrome refresh completion acknowledgment after confirming current upstream starts scans asynchronously; mock regression covers restart without restarting an in-flight scan. A live official Navidrome service now verifies discovery, ordinary-deletion retention and manual-deletion removal with a read-only music mount. Native image smoke additionally exercises both encoders. Artwork contents are synchronized before publication, as well as its directory. Complete compatibility validation remains pending.

LAME 4.0 validation exposed its default external mpg123 decoder dependency; this build needs only encoding, so disable LAME decoding instead of adding an unused decoder. FFmpeg handles playback validation. Live maintenance run confirmed the repository disables bot PR creation (HTTP 403); maintenance now dispatches validation before opening a PR and falls back to a compare link, update branch and source artifact when creation is unavailable. This is a repository setting, not a claim that automatic PR creation is enabled.

## Round 6

Focused UI findings: status filters hid older failures because they ran after pagination; mobile hid the only logout control; failed polling silently showed old data; selected ready tracks exposed a deletion button that could do no work. Filtering now runs in SQL with matching totals, with a regression using a failure older than 105 successes. Polling is serialized, discards responses for departed routes and visibly reports stale data with retry. Mobile has readable bottom navigation and accessible logout. Navigation has current-page semantics and a skip link; build progress has an accessible numeric value. Expired deletion is enabled only for applicable selected tracks; real file deletion/restoration exercises bilingual confirmations safely. Settings distinguish unsaved/saved changes, refresh secret-configuration flags, and avoid nested labelable controls. Dashboard attention includes failed sources and labels the codec as a target format during rebuilds. Browser validation now includes mobile screenshots, no horizontal page overflow, polling failure/recovery and unsaved-input preservation across languages. Final compatibility and image checks remain pending.

Final concurrency audit found that OIDC session issuance could wait behind a full scan's file mutex. OIDC binding/session changes now use the short authentication/configuration mutex shared by Settings updates. Busy Settings writes return a localized conflict promptly and preserve form input rather than queueing a surprise mutation behind a long scan. Regressions hold the file mutex while logging in through the signed OIDC provider and while submitting Settings. The base-image maintenance run also proved the bot-PR-disabled fallback succeeds and explicitly starts update-branch CI.

Round-six browser validation passed seven checks and exposed a mobile-only presentation defect: the existing responsive rule hid the newly added unsaved-state caption. The assertion remains in place; the caption is now visible beside Save on mobile. Base-image digest refresh [Action](https://github.com/sagehou/MusicForge/actions/runs/37017541033) succeeded, pushed generated source and dispatched [branch CI](https://github.com/sagehou/MusicForge/actions/runs/37017633826) despite disabled bot PR creation. Its source was reviewed and incorporated; final CI includes the presentation and authentication-concurrency corrections.

Visual inspection of passing mobile artifacts identified a cramped English encoding-panel heading beside its restore button. Header text now retains a readable flex basis and the action wraps; the browser check asserts usable heading width. The skip link uses clipped content until keyboard focus, preventing off-screen fixed content from appearing in full-page captures. Added a mobile viewport screenshot alongside the full-page artifact.

Upgrade acceptance now explicitly recreates schema-1 state and verifies artifact ownership/playback records, exhausted retry budgets and dirty-directory timestamp migration survive startup. A separate regression rejects a newer schema. This closes the remaining migration scenario gap before publishing a schema-2 release.

Final persistence audit found the worker logged completion-update failures and then abandoned a still-running database row. It now retries persisting the same result until storage recovers or shutdown, preserving the source claim and counting the execution failure once. Fault injection rejects completion writes through a SQLite trigger, then removes it and verifies the running job recovers without another execution/attempt.

Queue-history audit also covers bulk retry activating an older row while newer failed history exists for the same target. Enqueue now prioritizes the active row over historical records, returning its ID instead of reporting a uniqueness conflict. A regression verifies both manual and automatic duplicate requests reuse that target.
