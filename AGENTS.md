# MusicForge development rules

The agreed MVP baseline is recorded in [docs/mvp-design.md](docs/mvp-design.md).

## Incremental lyric maintenance

- Ordinary filling (all, selected and automatic) skips persisted `ready` results bound to the current audio path/signature before any source/output file access or provider request. Restarting, filling again or changing the matching threshold must not search those tracks again or rewrite their check time.
- New/changed artifacts and unfinished results are the pending work. A still-valid owned LRC also takes precedence before reading a source sidecar or searching online if publication succeeded before its status was saved. Preserve explicit manual choices.
- Automatic retries wait at least one hour after failure and seven days after missing/instrumental/review results. Explicit filling may retry unfinished tracks immediately, reusing valid searches and previews; it still skips ready tracks.
- Only explicit re-search or traditional-to-simplified replacement may revisit a ready track. Source-side LRC updates and externally removed output lyrics require explicit re-search; ordinary filling does not audit completed files. Replacement must retain current ownership, signature, identity, confidence and conflict checks.
- Progress counts the actual pending tracks and reports skipped tracks separately. An empty pending set succeeds without file/network I/O, and automatic no-op ticks preserve the last useful batch report. Whole-library coverage continues to use persisted per-track results.
- Reuse schema-2 metadata and existing background processing. Keep source/tag/audio reads out of planning, preserve existing lyrics on unsuccessful searches, and validate these guarantees in GitHub Actions, including restart and multiarchitecture rollback checks.

## Persistence compatibility and rollback

- Schema 2 is the stable MVP persistence baseline. The `0.2.x` series must keep this schema and preserve the published table/column definitions and meanings.
- Compatibility includes SQLite data, Settings JSON, job kinds/arguments, metadata and recovery journals, ownership markers, and artifact paths/signatures. Do not silently change formats or units while keeping the same schema number.
- Same-series releases must allow the previous compatible application to read and write their data. An unchanged schema number alone does not justify skipping rollback validation.
- Full rollback validation may be skipped only when the entire persistence compatibility surface above is unchanged from the most recent published stable release (excluding the version under test). CI records the baseline and decision, and automatically skips only changes limited to documentation, frontend presentation, and tests. Backend, dependency, deployment, workflow, or unclassified changes require validation; a missing or unrelated baseline also requires it. Normal tests and both architecture image checks still run.
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
