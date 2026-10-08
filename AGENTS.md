# MusicForge development rules

The agreed MVP baseline is recorded in [docs/mvp-design.md](docs/mvp-design.md).

## Persistence compatibility and rollback

- Schema 2 is the stable MVP persistence baseline. The `0.2.x` series must keep this schema and preserve the published table/column definitions and meanings.
- Compatibility includes SQLite data, Settings JSON, job kinds/arguments, metadata and recovery journals, ownership markers, and artifact paths/signatures. Do not silently change formats or units while keeping the same schema number.
- Same-series releases must allow the previous compatible application to read and write their data. CI exercises published schema-2 images on the candidate's same configuration/source/output volumes, then reopens them with the candidate, on both architectures.
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
