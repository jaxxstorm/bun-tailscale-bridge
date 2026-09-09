## 1. Public Package Preparation

The owner approved `ts_omit_webclient` to exclude the unused web UI, provided
HTTP/CONNECT and SOCKS behavior is preserved and licensing is reverified.

- [x] 1.1 Rename package/consumer imports to `@jaxxstorm/bun-tailscale-bridge`, remove the private gate, add public npm/repository metadata, and regenerate workspace lockfile metadata while preserving runtime/toolchain pins.
- [x] 1.2 Add owner-approved MIT licensing and inventory the dependencies bundled into all four helpers; include required license/notice texts and update the explicit archive allowlist with tests for missing/unexpected content.
- [x] 1.3 Refactor package verification to accept and preserve an existing tarball while retaining local pack-and-check mode; test scoped imports, public metadata, exports/types, all assets, native launch, and unchanged input digest.

## 2. Release Policy and Candidate Artifacts

- [x] 2.1 Implement stable-tag/package identity validation and source checks for canonical repository, immutable peeled tag commit, and main ancestry; unit-test mismatches, leading zeros, prereleases, build suffixes, moved tags, and unsafe ref input using local fixtures.
- [x] 2.2 Add candidate packing plus deterministic manifest/checksum generation (name, version, tag, commit, filename, SHA-256, SHA-512 SRI); verify artifact contents and reject tampering without repacking.
- [x] 2.3 Implement testable npm/GitHub recovery decisions for confirmed absence, matching integrity/assets, conflicts, outages, partial uploads, and latest-version ordering; fail closed without overwriting or moving tags.

## 3. Pull Request Verification

- [x] 3.1 Extend `verify.yml` for PRs, main pushes, and read-only `workflow_call`; preserve Linux/macOS checks and add release-policy tests, bounded PR concurrency, and a stable aggregate status that rejects failed/cancelled/skipped required jobs.
- [x] 3.2 Pin external actions to reviewed commit SHAs, select/pin compatible Node/npm release tooling and actionlint, and add workflow lint/structural tests confirming no PR secrets, OIDC, publishing environment, or `pull_request_target` execution.

## 4. Tag-Driven Release Workflow

- [x] 4.1 Add `release.yml` stable-tag preflight, immutable checkout, package-wide non-cancelling release concurrency, and the reusable verification gate with read-only permissions and no inherited secrets.
- [x] 4.2 Add clean candidate build/pack/upload with 90-day artifact retention, followed by Linux/macOS jobs verifying the exact same downloaded tarball; pass the producing artifact identity and digests to publishing jobs.
- [x] 4.3 Add the protected `npm` environment job with OIDC/provenance, explicit public registry publishing of the verified tarball, disabled lifecycle scripts, source/digest rechecks, and immutable-version/latest checks; do not add a long-lived token fallback.
- [x] 4.4 Add a separate least-privileged GitHub Release job after npm success, requiring the existing tag, uploading the exact tarball/checksum/manifest, and finalizing a draft only after complete asset verification; implement non-clobber matching-state recovery.

## 5. Documentation and Verification

- [x] 5.1 Document scoped installation and stable version/tag procedure, aggregate required checks, npm ownership/trusted-publisher setup, environment and tag protections, first-publication bootstrap, and prerequisites not verifiable locally; do not claim an unpublished version is installable.
- [x] 5.2 Document rerun-failed-jobs recovery using the retained candidate, artifact expiry/full-rebuild conflicts, npm/GitHub partial outcomes, queued-release cancellation behavior, and owner-only deprecation/new-version rollback without automatic tag or version replacement.
- [x] 5.3 Run typecheck, Bun/release tests, Go tests/race checks, pinned workflow lint, all-target build, supplied-tarball clean-consumer validation, and a side-effect-free npm publish dry-run; record unavailable hosted/OIDC/live checks separately.
- [x] 5.4 Review implementation against both capability specs and strict OpenSpec validation; leave external setup and actual publication for explicit owner action, without creating a tag, commit, package publication, or GitHub Release during implementation verification.
