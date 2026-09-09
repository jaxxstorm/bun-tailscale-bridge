## ADDED Requirements

### Requirement: Approved public package identity
Release preparation SHALL use npm name `@jaxxstorm/bun-tailscale-bridge`, owner-approved MIT licensing, public npm metadata, repository URLs, ESM/declarations, and the existing four helper assets. It SHALL remove the private publication block and update hard-coded imports and verifier expectations consistently. The artifact MUST include the project license and applicable reviewed bundled-dependency notices, and MUST exclude secrets, node state, local configuration, and unintended files. It MUST NOT introduce JavaScript runtime dependencies solely for release tooling.

#### Scenario: Publishable package preparation
- **WHEN** a candidate is packed
- **THEN** its metadata and reviewed contents identify the scoped public MIT package and all four executable helpers
- **AND** clean-consumer imports use the scoped name

#### Scenario: Invalid public metadata or contents
- **WHEN** the package is private, has the wrong name/license, lacks required notices, or contains an unexpected file
- **THEN** release preflight or artifact verification fails before publishing

#### Scenario: Approved omission of unused web UI
- **WHEN** production helpers and native test helpers are compiled
- **THEN** the owner-approved `ts_omit_webclient` build tag excludes the unused Tailscale web UI
- **AND** four-target license inventory and actual binary checks verify its JavaScript/font assets are absent while HTTP/CONNECT and SOCKS regression tests remain required

### Requirement: Stable tag and source authorization
The release workflow SHALL respond to tag pushes and accept only canonical stable `vX.Y.Z` tags whose version exactly matches package metadata. It MUST reject leading-zero versions, prerelease/build suffixes, non-tag invocations, a noncanonical repository, or a tag commit not reachable from protected main. The peeled tag commit MUST match the immutable event commit and be rechecked before publication. Tag text MUST be parsed as data, not injected into shell commands.

#### Scenario: Matching stable tag
- **WHEN** the canonical repository receives `v0.1.0` for a reviewed main commit declaring version `0.1.0`
- **THEN** preflight admits the candidate to verification without itself publishing anything

#### Scenario: Invalid tag or source
- **WHEN** a tag disagrees with the package version, is a prerelease, points outside main, changes resolution, or originates from another repository
- **THEN** no public publishing job is authorized

### Requirement: Identical verified artifact delivery
The workflow SHALL build the candidate in a clean unprivileged job, pack a single versioned tarball, and record its tag, commit, package identity, SHA-256, and SHA-512 SRI in a manifest/checksum bundle. Read-only Linux and macOS jobs SHALL verify that same supplied tarball, including native helper launch without enrollment, without rebuilding or modifying it. Both publishing destinations MUST receive the same verified bytes from the same run-scoped artifact. Artifact retention SHALL be 90 days for recovery.

#### Scenario: Candidate handoff
- **WHEN** the candidate moves from build to verification and publication
- **THEN** every stage checks its expected manifest/digests and package identity
- **AND** no publishing stage rebuilds, repacks, or consumes a PR/foreign-run artifact

#### Scenario: Altered candidate
- **WHEN** a tarball, manifest identity, or checksum does not match the approved tagged candidate
- **THEN** verification or publication fails before registry or GitHub mutations

#### Scenario: Existing-tarball verification
- **WHEN** the package verifier receives an explicit tarball path
- **THEN** it validates that file in a clean consumer and leaves its bytes unchanged rather than creating a replacement

### Requirement: Privilege-separated npm trusted publishing
Publishing SHALL require successful reusable verification and exact-artifact checks. The npm job SHALL use a protected `npm` environment, GitHub OIDC trusted publishing, public access, provenance, an explicit npm registry, pinned compatible Node/npm tooling, and disabled package lifecycle scripts. Only that job SHALL receive the required OIDC permission. It MUST NOT use a long-lived npm-token fallback, install project dependencies in the privileged job, or pass credentials into package scripts.

#### Scenario: Authorized npm publish
- **WHEN** checks succeed, environment approval is satisfied, and trusted publishing is configured
- **THEN** npm receives the verified tarball with public access and provenance

#### Scenario: Missing prerequisites
- **WHEN** checks fail or environment/trusted-publisher configuration is unavailable
- **THEN** publishing fails or remains gated without falling back to stored registry credentials

### Requirement: GitHub release delivery after npm
A separate job with narrowly scoped `contents: write` SHALL create or reconcile a GitHub Release only after npm publication succeeds or an identical existing npm version is confirmed. It SHALL use the existing validated tag and attach the verified tarball, `SHA256SUMS`, and `release.json`. New releases SHALL remain drafts until all expected assets are verified. The workflow MUST NOT implicitly create/move tags or overwrite existing mismatched assets.

#### Scenario: Successful dual delivery
- **WHEN** npm publication and GitHub asset verification succeed
- **THEN** a published GitHub Release exposes the same tarball bytes distributed by npm, with checksums and source manifest

#### Scenario: Asset upload failure
- **WHEN** npm succeeds but one GitHub asset upload fails
- **THEN** the workflow reports partial failure and a new release remains a draft until recovery completes its assets

### Requirement: Serialized immutable recovery
Releases SHALL serialize per package without cancelling an active publication. A new npm publication MUST be newer than the current stable latest version. An existing exact npm version SHALL be accepted only when registry SHA-512 integrity matches the candidate; otherwise the workflow MUST fail without republishing or changing npm tags. Registry/network/authentication errors MUST NOT be treated as version absence. Matching existing GitHub assets SHALL be retained, missing assets uploaded, and mismatches rejected. Recovery of older existing versions MUST NOT move latest backward.

#### Scenario: npm completed on an earlier attempt
- **WHEN** the verified candidate version already exists with matching integrity
- **THEN** npm publication is skipped and the workflow can complete matching GitHub delivery without changing npm dist-tags

#### Scenario: Immutable content conflict
- **WHEN** npm integrity or an existing GitHub asset differs from the candidate
- **THEN** recovery stops without overwriting, deleting, unpublishing, or moving a tag

#### Scenario: Registry outage
- **WHEN** registry lookup fails for a reason other than confirmed not-found
- **THEN** the workflow fails closed rather than attempting an assumed first publish

#### Scenario: Retained artifact recovery
- **WHEN** an owner reruns failed jobs after partial publication
- **THEN** the workflow uses the successful build's retained artifact and revalidates it
- **AND** expired artifacts or a mismatched full rebuild require owner intervention rather than automatic replacement

### Requirement: Release prerequisites and side-effect-free validation
Documentation SHALL describe npm scope ownership, exact trusted-publisher repository/workflow/environment configuration, branch/tag protections, environment approval, first-publish bootstrap if needed, stable tag creation, partial-failure recovery, and non-destructive rollback. Local tests and dry-run commands SHALL validate positive/negative release policy and artifact cases without publishing, pushing tags, changing hosting configuration, or enrolling a tailnet node. Actual npm/GitHub setup and successful hosted runs MUST be reported separately from local checks.

#### Scenario: First release not configured
- **WHEN** the owner has not yet created/configured the npm package or trusted publisher
- **THEN** the checklist identifies the required separate owner action and does not claim the workflow is already operational

#### Scenario: Implementation verification
- **WHEN** release unit tests, workflow lint, package validation, and npm publish dry-run pass locally
- **THEN** the result is recorded as local evidence, not as a real package publication or GitHub Actions success
