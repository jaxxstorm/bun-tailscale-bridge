## Why

The bridge already has local verification and a PR workflow, but consumers cannot obtain an authorized public release. We need reliable PR checks and a tag-driven path that publishes a verified package to npm and the identical artifact to GitHub Releases.

## What Changes

- Preserve and extend the existing secret-free GitHub Actions verification for pull requests and main-branch changes; reuse it as a release gate rather than duplicate its commands.
- Publish the owner-selected npm package `@jaxxstorm/bun-tailscale-bridge` under the owner-approved MIT license, including metadata, license text, applicable bundled-dependency notices, and updated consumption examples.
- Replace the initial private-package publication block with explicit release authorization: validated stable version tags, matching package versions, successful checks, and configured trusted publishing.
- Build all four helper assets, pack once, and verify that exact tarball on Linux and macOS before public distribution.
- With subsequent owner approval, omit Tailscale's unused embedded web UI using `ts_omit_webclient`, verify actual asset exclusion and the regenerated license inventory, and preserve HTTP/CONNECT and SOCKS behavior.
- Publish the verified tarball to npm using GitHub OIDC, then attach the same tarball and checksums to a GitHub Release.
- Add release preflight/dry-run tests, credential separation, immutable-version checks, partial-failure recovery, and first-publication setup documentation.

## Capabilities

### New Capabilities

- `pull-request-verification`: Required secret-free Bun/Go and package checks with reusable release gating.
- `tagged-package-releases`: Authorized stable-tag validation, publishable package preparation, identical-artifact npm/GitHub delivery, and recovery.

### Modified Capabilities

None in canonical specs: `openspec/specs/` is currently empty. This change builds on the completed but unarchived `bootstrap-bun-tailscale-bridge` implementation. Its publication-disabled-until-authorized condition is satisfied by the user's explicit npm/GitHub publishing request, scoped package selection, and MIT approval. The baseline packaging, networking, and lifecycle guarantees remain intact; do not silently archive or rewrite that change.

## Impact

- Changes `.github/workflows/verify.yml`, adds a release workflow and release support/tests, and extends existing package verification to consume an already-built tarball.
- Updates package name, public metadata, license/notices, lockfile metadata, package allowlists, README, examples, and hard-coded clean-consumer imports. No package has been published, so no compatibility alias is needed.
- Uses the existing Bun 1.4.2, Go 1.26.2, and four-platform helper build. Node/npm are release tooling only, not runtime dependencies.
- Requires repository/environment protections, npm scope ownership and trusted-publisher configuration, plus any first-publish bootstrap performed by the owner. No registry credentials or live tailnet keys belong in PR jobs.
- Introduces public release side effects only when the configured release workflow processes an authorized tag. Artifact creation and later local implementation checks do not authorize pushing tags, publishing packages, or creating GitHub Releases from this session.
- Stable `vX.Y.Z` releases are in scope. Prereleases, automatic version bumps, changelog generation, standalone helper downloads, and additional registries are deferred.
