# MusicForge development rules

The agreed MVP baseline is recorded in [docs/mvp-design.md](docs/mvp-design.md).

## Change-scoped acceptance

- Choose acceptance by the complete change from the latest published stable release, including earlier unreleased commits. A trusted successful full CI run on an ancestor may cover inherited work; record that run and inspect its entire remaining diff. Persistence rollback always retains the published-release baseline. Pure UI means styling, layout, icons or display copy with unchanged event handling, API calls, authentication, settings, browser persistence and state semantics. A frontend file path alone does not establish this. Mixed, dependency, deployment, workflow or uncertain changes use the full applicable validation.
- Documentation-only changes need consistency, link and whitespace review. Do not install dependencies, compile, run application acceptance, create a version tag or publish an image solely for documentation changes.
- Pure UI changes need frontend typecheck/build, the affected existing browser checks and representative screenshots. Shared shell changes cover appearance/navigation and affected pages; inspect desktop and a narrow viewport, with light/dark or translated long text where affected. Do not exhaust every page, size, theme and language combination by default. Preserve keyboard access, focus and existing interaction behavior.
- Pure UI acceptance does not require backend race/vet, audio conversion, real Navidrome/rclone integration or full rollback validation when their inputs and the persistence compatibility surface are unchanged. Expand only when the diff, a failure or an unresolved concern identifies an affected behavior. Keep the rollback baseline and eligibility rules below.
- Reuse or adjust existing UI checks for intentional layout changes; retain their useful density and interaction guarantees. Do not add tests merely to mirror reversible styling or relax assertions to hide a regression. After a correction, rerun the affected checks first; broaden only when required by the changed inputs or mandatory CI.
- Validate the candidate and inspect its screenshots before creating a version tag. Do not start identical main/tag acceptance runs together by default. Reuse successful evidence only for identical tested source, dependencies, test configuration and relevant environment, recording the commit and artifacts; changed inputs invalidate the affected evidence. A branch/tag name or version label alone does not require replaying the same frontend acceptance.
- When a release is authorized, build and check the final images on both `linux/amd64` and `linux/arm64` once before publication. Image-specific checks remain necessary even when frontend evidence is reused. UI acceptance alone does not require a new image release.
- CI automatically selects documentation, UI or full acceptance and records its plan in `rollback-scope`. The UI allowlist is CSS, translation catalogs and TSX that passes a conservative AST comparison of executable syntax using the existing locked Rolldown parser; structural, dynamic-expression, active-asset and uncertain changes use full acceptance, including rollback because frontend logic can change persisted Settings values. UI runs build the frontend and selected existing API-mocked Chromium cases without Go, media tools, Navidrome, rclone or image jobs. Version tags always require both final native image checks and any required rollback.
- Reuse only successful same-repository push/dispatch runs of `ci.yml`, excluding PR/fork evidence. Proof must match the source commit and runner image/environment and be at most seven days old; changed inputs require fresh affected checks. A recent full ancestor proof may cover unchanged inherited inputs. Missing/expired/unavailable proof falls back to the published baseline. CI retains the source run/artifact references; the aggregate `ci` job requires every selected check to pass. Do not manually bypass these gates. Report actual checks, reuse and skip reasons briefly.
- Dependency security audit is a separate required job for fresh UI/full validation. Audit locked dependencies without a duplicate install-time audit. Network failure remains a failed gate, but does not prevent unrelated application/image checks; retry the failed job after service recovery instead of replaying successful checks. Evidence is trusted only after the entire source run succeeds.

## Incremental lyric maintenance

- Ordinary filling (all, selected and automatic) skips persisted `ready` results bound to the current audio path/signature before any source/output file access or provider request. Restarting, filling again or changing the matching threshold must not search those tracks again or rewrite their check time.
- New/changed artifacts and unfinished results are the pending work. A still-valid owned LRC also takes precedence before reading a source sidecar or searching online if publication succeeded before its status was saved. Preserve explicit manual choices.
- Automatic retries wait at least one hour after failure and seven days after missing/instrumental/review results. Explicit filling may retry unfinished tracks immediately, reusing valid searches and previews; it still skips ready tracks.
- Searching or replacing ready lyrics requires an explicit action: track re-search, candidate selection or traditional-to-simplified replacement. Output-only script inspection remains available. Source-side LRC updates and externally removed output lyrics require explicit re-search; ordinary filling does not audit completed files. Replacement must retain current ownership, signature, identity, confidence and conflict checks.
- Progress counts the actual pending tracks and reports skipped tracks separately. An empty pending set succeeds without file/network I/O, and automatic no-op ticks preserve the last useful batch report. Whole-library coverage continues to use persisted per-track results.
- Reuse schema-2 metadata and existing background processing. Keep source/tag/audio reads out of planning, preserve existing lyrics on unsuccessful searches, and validate these guarantees in GitHub Actions, including restart and multiarchitecture rollback checks.

## Persistence compatibility and rollback

- Schema 2 is the stable MVP persistence baseline. The `0.2.x` series must keep this schema and preserve the published table/column definitions and meanings.
- Compatibility includes SQLite data, Settings JSON, job kinds/arguments, metadata and recovery journals, ownership markers, and artifact paths/signatures. Do not silently change formats or units while keeping the same schema number.
- Same-series releases must allow the previous compatible application to read and write their data. An unchanged schema number alone does not justify skipping rollback validation.
- Full rollback validation may be skipped only when the entire persistence compatibility surface above is unchanged from the most recent published stable release (excluding the version under test). CI records the baseline and decision, and automatically skips only changes limited to documentation, frontend presentation, and tests. Backend, dependency, deployment, workflow, or unclassified changes require validation; a missing or unrelated baseline also requires it. Other tests follow change-scoped acceptance; released images still require both architecture checks.
- When required, CI exercises published schema-2 images on the candidate's same configuration/source/output volumes, then reopens them with the candidate, on both architectures. New optional metadata and artifact maintenance count as persistence changes even without a schema migration.
- An incompatible persistence change belongs to a separately documented breaking release, with an upgrade backup procedure and a tested restore-and-rollback path. Preserve historical migration definitions and reject unsupported schemas rather than lowering their version number.
- Complete state rollback requires a matching stopped `/config` backup and output snapshot; database compatibility does not undo completed artifact operations. Never overwrite a published version tag or its image to implement a rollback.

## Keep the local environment clean

- Do not compile, build, or install dependencies locally.
- Run dependency installation, backend/frontend compilation, tests that require compilation or dependencies, and Docker image builds only in GitHub Actions.
- Do not run local package-manager operations that download or install dependencies, including Go module downloads or tidy operations.
- Local work may inspect and edit source, documentation, and workflow files, and perform checks that do not compile code or download/install dependencies.
- Do not create local dependency directories, compiler caches, or generated build outputs. Dependency lockfiles are source files; generate or update them through GitHub Actions.

## Build and release through GitHub Actions

- GitHub Actions is the sole environment for dependency installation, build, and executable validation.
- Build the single-container MusicForge image for both linux/amd64 and linux/arm64.
- Publish final container images to GitHub Container Registry (GHCR).
- Pull requests and default-branch commits run CI validation without publishing container images.
- Version tags such as `v0.1.0` trigger publication after successful CI validation.
- Publish a corresponding versioned image tag; stable releases also update `latest`. Prereleases do not update `latest`.
- Do not build or publish images from the local workspace.
