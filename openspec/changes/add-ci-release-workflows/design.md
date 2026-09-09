## Context

The working tree already contains `.github/workflows/verify.yml`: read-only push/PR jobs on Ubuntu 24.04 and macOS 14 run Bun/Go tests, race checks, four-target builds, and clean-package verification. No workflow has yet been verified on GitHub. The package is private, unscoped, and lacks a license. The current verifier creates/deletes its own tarball, hard-codes the package name, and rejects public package metadata or license files.

The user approved public npm plus GitHub Releases, the name `@jaxxstorm/bun-tailscale-bridge`, and MIT. This changes release authorization, not the bridge APIs. The completed bootstrap change remains active and canonical specs are empty; reference its packaging baseline without silently archiving it or creating competing networking requirements.

## Goals / Non-Goals

**Goals:**

- Keep PR checks secret-free and reuse their logic for release verification.
- Publish only approved stable tags whose package version, commit, metadata, tests, and artifact contents agree.
- Verify once-packed bytes and deliver those identical bytes to npm and GitHub.
- Make privileged jobs narrow and recovery explicit.

**Non-Goals:**

- Automatically bump versions, push tags, publish during local implementation, or generate a changelog system.
- Support prereleases, build-metadata versions, multiple registries, standalone helper release assets, or automatic unpublish/rollback.
- Run live-tailnet enrollment in PR/release CI or claim cross-compilation proves native compatibility.

## Decisions

### 1. Extend existing CI and reuse a single verification definition

Keep `verify.yml` as the entry point for `pull_request`, pushes to `main`, and `workflow_call`. Preserve Ubuntu 24.04 and macOS 14 jobs, exact Bun 1.4.2 and Go 1.26.2 pins, frozen installs, typecheck, Bun tests (including streamed CONNECT uploads), Go tests/race checks, four-platform packaging checks, and live-test skip. Avoid path filters that could leave a required check permanently pending. Add a stable aggregate check that fails if a required matrix job fails, cancels, or unexpectedly skips.

Use `pull_request`, never `pull_request_target`, for untrusted PR code. Verification has only `contents: read`, no publish environment, no registry credentials, and no OIDC token. Use PR/ref-scoped concurrency to cancel obsolete verification runs, bounded timeouts, and SHA-pinned external actions with version comments. Release calls the same verification workflow with read-only permissions; no inherited secrets.

This is preferable to separate copies of test commands in a publish workflow that can drift or relying on a previous PR check for a different commit. Repository owners must configure branch protection to require the aggregate status; YAML alone cannot enforce it.

### 2. Publishable metadata and licensing are deliberate

Rename the package and hard-coded consumer imports to `@jaxxstorm/bun-tailscale-bridge`, remove `private: true`, add `license: MIT`, repository/homepage/bugs metadata for `jaxxstorm/bun-tailscale-bridge`, and public npm publish configuration. Add the MIT license with the repository owner's attribution, and inventory license/notice obligations for dependencies actually bundled into the four Go binaries. Include required texts/notices in the tarball; MIT applies to this project, not a relicensing of its dependencies.

Update lockfile workspace metadata, README installation instructions, examples, and the package verifier together. Verify the exact scoped name and intended public metadata instead of requiring privacy. Preserve the no-JavaScript-runtime-dependency contract. The archive allowlist explicitly admits reviewed license/notice files, never broad arbitrary directories or local config/state. No backward-compatible unscoped alias is needed because no release exists.

The owner subsequently approved compiling with `ts_omit_webclient` to exclude
Tailscale's unused embedded web UI and its JavaScript/font assets. Apply this tag
to all production targets, native test-helper builds, Go test/race invocations,
and license scans. This is a compiled-helper change only; bridge HTTP/CONNECT,
SOCKS, tsnet dialing, and lifecycle behavior remain unchanged. Regenerate and
review notices, check inventory drift in CI, and verify the exact pinned web
asset bytes are absent from every built helper before releasing.

### 3. Stable tags authorize a release candidate, not unconditional publishing

Add `release.yml` triggered by tag pushes matching `v*`, followed by strict validation of canonical `vX.Y.Z` (no leading zeros, prerelease suffix, or build metadata). Require tag version to equal the committed package version, package name/license/public metadata to match policy, and the peeled tag commit to match the event commit and be reachable from protected `origin/main`. Recheck tag resolution immediately before publication. Only the canonical repository can publish.

Check out the immutable commit, not a moving branch. Treat ref text as data: pass it through environment/arguments to a parser, never interpolate an untrusted ref into shell code. A merge already present on main plus owner-controlled tag rules and environment approval supplies the trust boundary; ancestry alone does not substitute for branch/tag protection.

Serialize releases with a package-wide concurrency group and `cancel-in-progress: false`. GitHub can supersede pending runs, so document rerunning any tag that was queued out; running publishes must not be cancelled by newer tags. Before a new npm publication, require its stable version to be greater than the current stable `latest`, preventing an old tag from moving `latest` backward. Recovery of an already-published older version never changes npm tags or marks an older GitHub release latest.

### 4. Build once and verify the exact candidate artifact

After tag preflight and reusable checks pass, a clean unprivileged Linux job builds all four helpers plus ESM/declarations and packs one versioned `.tgz`. Compute SHA-256 for downloads and npm-compatible SHA-512 SRI for byte identity. Produce a small `release.json` manifest containing package name/version, source commit, tag, tarball filename, and digests, plus `SHA256SUMS`. No timestamps or secrets are needed in the manifest.

Extend `verify-package.ts` with an explicit existing-tarball input. It must inspect, install, type-check, and launch the native helper from that file without rebuilding/repacking or modifying it. Preserve its current convenience mode for local verification. Use the manifest/expected scoped name rather than interpolating arbitrary package metadata into executable source.

Upload the candidate bundle as a run-scoped artifact retained for 90 days. Separate Linux/macOS read-only jobs download that same bundle, verify digest/manifest/tag consistency, and exercise native clean-consumer checks. Both must pass. This gives two native host checks and four built binaries without claiming four native platforms.

Publication jobs download the verified bundle by the producing job's artifact identity within the current run. They do not rebuild, install project dependencies, run package lifecycle hooks, or consume artifacts from PR/other runs. Recheck manifest/digests before either destination. One verified tarball is the release unit; separately rebuilding on each publishing job is rejected because the outputs can diverge.

### 5. npm uses OIDC and GitHub receives the same bytes

Use npm CLI trusted publishing rather than Bun's publisher. During implementation pin an exact supported Node LTS and npm CLI version with GitHub OIDC support (npm 11.5.1 or newer); these are release tooling, not bridge runtime dependencies. A dedicated npm job uses the protected `npm` environment, `id-token: write`, and only required read permissions. Configure the npm trusted publisher for the exact repository, `release.yml`, and environment. Publish the supplied tarball with public access, provenance, explicit npm registry, and lifecycle scripts disabled. No long-lived `NPM_TOKEN` fallback in the normal workflow.

Once npm succeeds or its existing identical version is verified, a separate GitHub delivery job with `contents: write` creates a release for the already-existing tag and uploads the same `.tgz`, `SHA256SUMS`, and `release.json`. Prefer a draft while assets upload; publish it only after digest verification and complete assets. Do not create tags implicitly. Generated GitHub release notes are sufficient; no changelog tooling is required.

PR/build jobs never receive these permissions or environments. Release credentials must not be passed to package scripts or embedded in generated files/logs. Use explicit `needs` dependencies; a failed check or failed npm publish prevents GitHub release finalization.

### 6. Treat partial publication as recovery, not replacement

npm and GitHub cannot commit atomically. Before npm publish, query the exact name/version. Only a confirmed not-found result means absent; network, authentication, malformed metadata, or registry errors fail closed. If present, require `dist.integrity` to equal the candidate SHA-512 SRI. Equal means npm is already complete and must not be republished; unequal means stop without changing tags or GitHub assets.

For an existing GitHub release, validate its tag commit and every existing named asset against the candidate digests. Upload missing assets, complete a matching draft, or succeed without changes when already complete. Never use clobber or replace a mismatched asset.

The documented preferred recovery is GitHub's rerun-failed-jobs action, retaining the successful candidate build and its artifact from that run. A rerun of all jobs can rebuild different bytes; it is safe only if integrity matches. If the original artifact has expired or checksums disagree, stop for owner investigation, retrieve the original verified artifact if available, or cut a new version. Do not silently adopt an unknown registry tarball, delete a published version, or move a tag. This deliberately favors a small, fail-closed workflow over complex cross-run artifact discovery.

### 7. Test release logic without release side effects

Keep tag/version parsing, metadata/manifest validation, digest comparison, and recovery decisions in a small script/module usable by workflows and unit tests. External npm/GitHub calls use checked exit statuses and explicit failure categories. Tests use local fixtures/mocks for not-found, matching/mismatched integrity, registry failures, and partial GitHub uploads. A local dry-run validates/builds/packs/checks without publishing or mutating GitHub.

Run workflow lint (actionlint pinned during implementation), existing tests, release-policy tests, and an npm publish dry-run with scripts disabled against the verified tarball. A dry-run is not proof of registry ownership, OIDC configuration, a GitHub Actions run, or a successful release.

## Risks / Trade-offs

- Public package setup is external -> Document scope ownership, trusted publisher, environment reviewers, tag protections, and a first-release checklist; fail clearly until configured.
- npm trusted publishing can require an existing package -> Owner performs any one-time initial publication/registration separately with the verified tarball using an approved credential, then configures OIDC. Do not embed a fallback secret or automatically bootstrap ownership.
- Non-atomic destinations -> Preserve exact candidate bytes and reconcile only matching existing state; npm cannot be rolled back safely by replacing a version.
- Binary/license payload grows -> Review bundled dependency obligations and strict tarball contents; fail packaging if limits or required notices cannot be satisfied.
- A public repo and npm provenance prerequisites may not yet hold -> Verify them before first real release; implementation completion does not claim hosting configuration is ready.
- Protected-main ancestry depends on actual remote configuration -> Fetch required refs and fail when unavailable; the present local checkout has no configured remote, so live release validation cannot run here.

## Migration Plan

Implement metadata/verifier changes first, extend existing CI, then add candidate and publishing jobs with local negative-path tests. Update docs from private-only installation to the intended scoped package, clearly marking it unavailable until an actual first release.

Before release the owner pushes/reviews the workflows, configures branch/tag protections and the `npm` environment, verifies npm scope ownership, arranges initial package/trusted publisher setup, and confirms GitHub-hosted checks pass. Version changes occur in normal reviewed commits. Only then push `v<version>` for that reviewed main commit.

Before any public publish, rollback is disabling the release workflow or rejecting the environment deployment. After npm publication, do not replace/unpublish automatically: deprecate a defective version through a separate owner action and release a fix under a new version. Preserve artifacts needed to reconcile GitHub delivery.

## Open Questions

- npm ownership/trusted publisher and first-publish bootstrap state are not known; owner setup is required before executing a real release.
- Tooling resolved during implementation: Node 24.11.1 with bundled npm 11.6.2, actionlint 1.7.7, and SHA-pinned actions documented in `docs/releases.md`.
- MIT attribution is owner-approved as Lee Briggs, 2026. The regenerated four-target Go inventory is reviewed, and actual web-asset exclusion is verified. The owner must still confirm actual GitHub protection settings; no external configuration is changed by artifact creation.
