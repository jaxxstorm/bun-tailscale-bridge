# Releases

The intended public package is `@jaxxstorm/bun-tailscale-bridge`, MIT, copyright
2026 Lee Briggs. It is **unpublished**. The installation command after a real
first release is `bun add @jaxxstorm/bun-tailscale-bridge`; until then install a
verified local `.tgz`. Nothing in this implementation authorizes a publication
or claims that hosted configuration has been checked.

## Owner Setup

Complete these external steps before authorizing a real release:

1. Establish the canonical public repository `jaxxstorm/bun-tailscale-bridge`,
   review and commit the source/workflows, and push protected `main`.
2. Require the stable **Required Verification** check on main. Require reviews,
   restrict bypasses, and protect workflow/build/release-policy changes. YAML
   alone does not enforce branch protection. Confirm both native matrix jobs
   actually pass on GitHub, including fork PR verification without secrets.
3. Protect `v*` tags with a ruleset: restrict creation to release owners, prohibit
   updates and deletions, and restrict bypass. Ancestry is necessary but not a
   substitute for main/tag protection.
4. Create a GitHub environment named **npm** with required reviewers, prevention
   of self-review where supported, and deployment rules permitting only release
   tags. Do not admit arbitrary branches. Confirm these protections are available
   under the repository's hosting plan.
5. Verify control of the npm `@jaxxstorm` scope and package, public visibility,
   account security/2FA, and authorization to distribute the bundled software.
6. Configure the npm package's GitHub Actions trusted publisher with owner
   **jaxxstorm**, repository **bun-tailscale-bridge**, workflow filename
   **release.yml**, environment **npm**. These strings must match exactly. The
   public repository/package and GitHub-hosted runner must satisfy npm provenance
   prerequisites. No long-lived npm token fallback belongs in these workflows.

Trusted publishing can require an already-existing npm package. If the package
does not exist, the owner must arrange the initial publication separately: run
the reviewed tag workflow through candidate production and both exact-artifact
checks, retain/download that same verified bundle, and use an explicitly
approved one-time credential outside the normal workflow to publish those exact
bytes with public access and scripts disabled. Do not use a dummy package or a
separately rebuilt tarball for the intended version. Then configure the trusted
publisher and rerun failed jobs: matching registry integrity makes npm delivery
a no-op and permits GitHub completion. If the owner's provenance policy cannot
permit a credential-based bootstrap, resolve package registration with npm
before any publication. There is no automated bootstrap or secret fallback.

## Stable Version Procedure

Only canonical `vX.Y.Z` tags are accepted. Leading zeros, prereleases and build
suffixes are rejected. Version changes belong in reviewed main commits; there
is no automated bump or tag creation. Update `package.json`, run frozen install
and all checks, and review any resulting lockfile metadata. A new version must
be greater than the current stable npm `latest`.

After owner setup and successful review, the owner can create an annotated or
lightweight `v<package-version>` tag at that exact main commit and push it. These
are owner actions, not implementation verification commands. Preflight peels the
immutable event object, checks package identity, compares checkout/tag/event
commits, and fetches main to verify ancestry. Every publication stage rechecks
source resolution; moving a tag cannot authorize different bytes.

The workflow runs this chain:

1. Read-only preflight, then the reusable `verify.yml` on the immutable commit.
2. A clean read-only Linux build of all four helpers and one candidate tarball.
   The bundle contains `.tgz`, `SHA256SUMS`, and deterministic `release.json`
   recording name, version, tag, commit, filename, SHA-256 and SHA-512 SRI.
3. A run-scoped artifact retained for **90 days**. Linux and macOS download its
   producing artifact ID, recheck identity/digests, and install/type-check/launch
   the same supplied tarball without repacking or enrolling a node.
4. The protected `npm` job, with `contents: read` and `id-token: write` only.
   Node **24.11.1** includes npm **11.6.2**; both versions are checked. No project
   dependency install or package lifecycle runs here. npm receives the exact
   verified tarball with `--access=public --provenance --ignore-scripts`, explicit
   `https://registry.npmjs.org/`, and `latest` only for a new newer version.
5. A separate `contents: write` GitHub job after npm success or matching existing
   integrity. It requires the existing tag, creates a draft with generated notes,
   uploads only missing assets without clobber, downloads/verifies every asset,
   and finalizes only after the complete matching set is present. Older recovery
   does not change npm dist-tags or mark an older GitHub release latest.

External actions are SHA-pinned, selected through read-only GitHub tag lookups:
checkout v4.2.2, setup-bun v2.0.2, setup-go v5.5.0, setup-node v4.4.0,
upload-artifact v4.6.2, download-artifact v4.3.0. Review updates deliberately.
Verification uses Bun 1.4.2, Go 1.26.2, and actionlint v1.7.7; optional external
shellcheck/pyflakes integrations are explicitly disabled. Node/npm are release
tooling, not library runtime dependencies.

## Recovery

Prefer GitHub's **Re-run failed jobs**, preserving the successful candidate job
and its artifact ID/digests from that run. Do not default to rerunning all jobs:
a full rebuild can produce different bytes, and published npm versions are
immutable even when the source version is unchanged.

- Exact npm version absent in a successful metadata response, or a confirmed
  package-not-found response: eligible to publish only if newer than stable latest.
- Existing version with the same SHA-512 SRI: skip publication and leave dist-tags
  unchanged. Missing/mismatched integrity, malformed responses, auth/rate-limit
  failures, outages or network errors stop delivery; none imply absence.
- npm succeeded but GitHub failed: rerun failed jobs with the retained candidate.
  A matching partial draft receives only missing assets and remains a draft until
  every expected asset is verified. Matching published state is a no-op.
- A mismatched, duplicate, unexpected or incomplete/starter GitHub asset: stop
  for owner investigation. The workflow does not delete or replace it. A failed
  upload may leave such a starter asset and need manual owner reconciliation.
- Expired artifact, lost original bundle, or conflicting full rebuild: stop.
  Recover the original independently verified bundle if available or issue a new
  version; never silently adopt an unknown registry tarball, move a tag, clobber
  an asset, delete/unpublish a version, or republish different bytes.

Releases serialize under one package-wide concurrency group with
`cancel-in-progress: false`. Running publications are not cancelled by newer
tags, but GitHub can supersede pending runs rather than maintaining a FIFO
queue. Owners must check every intended tag and explicitly rerun any displaced
run; an older unpublished version may then fail the newer-than-latest rule and
need a new version instead. Do not manually cancel an active publication unless
prepared to investigate its partial result.

Before publication, rollback means withholding environment approval or disabling
the workflow. After publication, the owner may separately deprecate a defective
npm version and release a fix under a new version. No automatic unpublish,
replacement, GitHub asset deletion, or tag movement is a rollback strategy.

## Licensing And Web UI

The owner approved `ts_omit_webclient` for production and native test-helper
builds, test/race runs, and license scans. This removes only Tailscale's unused
embedded web UI, including its JavaScript/font assets; HTTP/CONNECT, SOCKS,
tsnet dialing, and lifecycle source remain unchanged. The previous licensing
blocker was resolved by verifying exclusion, not by approving unidentified assets.

`scripts/licenses.ts` uses go-licenses v2.0.1 on `helper/cmd/bridge` with the exact
target/tag/CGO settings. The reviewed inventory has 53 entries per Darwin target
and 56 per Linux target, including nested package-specific and mixed licenses.
Classifications are MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 and ISC. The
generated notice file includes full license texts, adjacent notices/copyrights/
authors (including AWS notices and nested singleflight/compression licenses),
and the Go 1.26.2 runtime/standard-library license. These are attribution and
redistribution notices, not a relicensing under this project's MIT license.

CI's `verify:licenses` regenerates in memory and rejects notice/inventory drift.
For a dependency upgrade, run `bun scripts/licenses.ts`, review the actual
licenses and notices plus non-Go/embedded payloads, and commit reviewed results.
`verify:helpers` verifies build settings and module versions against the inventory
and checks that every pinned web asset's exact bytes are absent from all four
binaries. The unused prebuilt module remains in go.mod's upstream graph, but is
not in the built helpers or their license inventory.

## Local Validation

Run with the pinned Bun and Go toolchains; Node 24.11.1 is needed only for npm
dry-run. The following commands do not publish or create Git references:

```sh
mise exec -- bun install --frozen-lockfile
mise exec -- bun run typecheck
mise exec -- bun test test
mise exec -- go -C helper test -tags=ts_omit_webclient ./...
mise exec -- go -C helper test -tags=ts_omit_webclient -race -count=1 ./...
mise exec -- bun run verify:licenses
mise exec -- bun run build:all
mise exec -- bun run verify:helpers
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= -pyflakes= .github/workflows/verify.yml .github/workflows/release.yml
mise exec node@24.11.1 -- bun scripts/release-dry-run.ts /absolute/new/candidate-directory
openspec validate add-ci-release-workflows --strict
```

The local dry-run packs once from existing build outputs, retains the candidate,
verifies a clean native consumer, and checks npm's reported SRI against those
same bytes. It uses a disposable HOME/config/cache with no inherited npm
credentials. Its all-zero **fixture commit is not source authorization**; this
supports local validation in an uncommitted repository without inventing a
commit or tag. Do not submit this local fixture as a real release artifact.

Native execution here is macOS arm64 only; four builds are compilation evidence,
not four native platforms. Live-tailnet tests remain explicitly skipped. The
workflow must still be run on GitHub to establish Linux/macOS hosted execution,
environment approval, npm ownership/OIDC/provenance, and successful delivery.
See `docs/release-validation.md` for recorded local evidence and retained digests.
