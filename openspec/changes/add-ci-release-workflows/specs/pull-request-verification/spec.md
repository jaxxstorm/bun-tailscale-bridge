## ADDED Requirements

### Requirement: Secret-free pull request verification
GitHub Actions SHALL run verification for pull requests, including fork PRs, and pushes to main using `pull_request` rather than `pull_request_target`. Verification MUST have read-only repository permissions, no publishing environment or secrets, no OIDC permission, and no live-tailnet enrollment. It SHALL use bounded jobs and PR/ref-scoped cancellation of obsolete verification runs.

#### Scenario: Fork pull request
- **WHEN** a fork submits a pull request
- **THEN** its code runs only in unprivileged verification jobs without npm/GitHub publishing authority or Tailscale credentials

#### Scenario: New PR revision
- **WHEN** a newer commit updates a pull request with verification in progress
- **THEN** obsolete verification can be cancelled without cancelling another PR or an active release publication

### Requirement: Complete pinned verification matrix
Verification SHALL retain Ubuntu 24.04 and macOS 14 checks using pinned Bun 1.4.2 and Go 1.26.2. Required checks SHALL include frozen dependency installation, TypeScript checking, Bun unit/integration tests including streamed HTTPS requests, Go tests and supported race checks, four-platform builds, clean-package checks, release-policy tests, and workflow lint. External actions SHALL be SHA-pinned. A stable aggregate status MUST fail when any required job fails, is cancelled, or unexpectedly skips.

#### Scenario: Unit test failure
- **WHEN** a required Bun or Go test fails on either native runner
- **THEN** the verification status is unsuccessful and cannot satisfy the release gate

#### Scenario: Successful matrix
- **WHEN** all required jobs succeed
- **THEN** the aggregate check succeeds and reports the actual native platforms separately from cross-compiled assets
- **AND** no live-tailnet or hosted-CI success is inferred from local validation alone

### Requirement: Reusable verification for release commits
The verification workflow SHALL expose `workflow_call` so release candidates run the same required verification on the exact tagged commit with read-only permissions and no inherited secrets. Prior PR or branch checks for a different commit MUST NOT authorize a release. Documentation SHALL identify the aggregate status for owner-configured branch protection.

#### Scenario: Tagged commit release gate
- **WHEN** a valid release tag triggers the release workflow
- **THEN** the reusable verification checks its exact commit before publication is eligible

#### Scenario: Historical successful PR
- **WHEN** an earlier PR passed but the tagged commit's current verification fails
- **THEN** publication remains blocked despite the earlier successful check
