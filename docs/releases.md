# Releases

Release `@jaxxstorm/bun-tailscale-bridge` through
[`release.yml`](../.github/workflows/release.yml). The package was unpublished
in the [local validation record](release-validation.md); check npm before the
first release. Local checks do not establish hosted setup or publication.

## One-Time Setup

1. Push reviewed source and workflows to `jaxxstorm/bun-tailscale-bridge`.
   Protect `main` with reviews and the **Required Verification** check; restrict
   bypasses and changes to release workflows. Confirm both native CI jobs pass,
   including fork PR checks without secrets.
2. Protect `v*` tags: restrict creation to maintainers and prohibit updates and
   deletions. Main ancestry checks do not replace branch/tag protection.
3. Create the GitHub environment **npm**, with required reviewers, no self-review
   where supported, and deployment rules allowing release tags only. Confirm
   these protections are available on the repository's hosting plan.
4. Confirm control of the npm `@jaxxstorm` scope/package, public visibility,
   account security/2FA, and rights to distribute the bundled software.
5. Configure npm's GitHub Actions trusted publisher with these exact values:

| Setting | Value |
| --- | --- |
| Owner | `jaxxstorm` |
| Repository | `bun-tailscale-bridge` |
| Workflow filename | `release.yml` |
| Environment | `npm` |

The public repository/package and GitHub-hosted runner must meet npm provenance
requirements. Do not add a long-lived npm token fallback to the workflow.

### First Publication

Trusted publishing may require an existing package. If registration requires a
credential-based first publish, run the tag workflow through candidate creation
and both artifact checks. Download that same bundle and publish its tarball with
a one-time credential outside the normal workflow, public access, and scripts
disabled. Do not substitute a dummy package or rebuild the intended version.
Then configure trusted publishing and rerun failed jobs: matching npm integrity
skips publication and allows GitHub delivery to finish. If credential-based
bootstrap cannot meet your provenance requirements, resolve registration with
npm first. There is no automated bootstrap.

## Stable Release

1. Update `package.json` on reviewed main, run [Testing](validation.md), and
   review lockfile metadata. The version must exceed stable npm `latest`.
2. Create and push `v<package-version>` at that main commit. Lightweight and
   annotated tags work; only canonical `vX.Y.Z` is accepted, without leading
   zeros, prereleases, or build suffixes. Version bumps and tags are manual.
3. Check the workflow results before approving the **npm** environment.

The workflow checks package identity, the immutable event/tag/checkout commit,
and main ancestry, then runs reusable verification. It builds all four helpers
on Linux and packs once. The retained bundle contains the `.tgz`, `SHA256SUMS`,
and `release.json` with identity, source commit, SHA-256, and SHA-512 SRI.

Linux and macOS download the producing artifact ID and check/install/type-check/
launch that tarball without repacking or enrollment. Retention is **90 days**.
The protected npm job uses Node **24.11.1** / npm **11.6.2**, OIDC, and:

```text
npm publish <candidate.tgz> --access=public --provenance --ignore-scripts --registry=https://registry.npmjs.org/ --tag=latest
```

It does not install project dependencies or run package lifecycle scripts.
A separate GitHub job creates a draft on the existing tag, uploads only missing
assets, downloads and checks all three, then finalizes. Publication stages
recheck source and digests. Older recovery does not change npm dist-tags or mark
an older GitHub release latest.

## Local Dry Run

After the [standard checks](validation.md), lint workflows and test a candidate
with Node 24.11.1 available through mise. Use a new output directory whose parent
exists:

```sh
mise exec -- go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= -pyflakes= .github/workflows/verify.yml .github/workflows/release.yml
mise exec node@24.11.1 -- bun scripts/release-dry-run.ts /absolute/new/candidate-directory
```

The dry-run script packs once from existing build outputs, retains the bundle,
checks a clean native consumer, and compares npm's dry-run SRI with the tarball.
It uses disposable HOME/config/cache without inherited npm credentials and does
not publish or create Git references. Its all-zero fixture commit is not a
release commit; do not publish this local fixture.

## Recovery

Use **Re-run failed jobs** to keep the successful candidate and its artifact ID.
Rerunning all jobs can rebuild different bytes for an immutable npm version.

| Situation | Action |
| --- | --- |
| Version absent in valid registry metadata, or confirmed package-not-found | Publish only if newer than stable `latest`. |
| Version exists with matching SHA-512 SRI | Skip npm publication; leave dist-tags unchanged. |
| Integrity mismatch/missing, malformed response, auth/rate-limit/network failure | Stop and investigate. Failure is not proof of absence. |
| npm succeeded, GitHub failed | Rerun failed jobs with the retained bundle. Matching partial drafts receive missing assets only. |
| Conflicting, duplicate, unexpected, or incomplete GitHub asset | Reconcile manually; the workflow will not delete or replace it. |
| Artifact expired or original bundle lost | Recover the original bundle and check it, or use a new version. Never adopt unknown registry bytes. |

Matching published state is a no-op. Never move tags, clobber assets, unpublish,
or replace a version to retry. Before publication, withhold environment approval
or disable the workflow; afterward, deprecate a defective version separately
and release a fix under a new version.

Releases share one concurrency group with `cancel-in-progress: false`. GitHub
can supersede pending runs, so check every intended tag and rerun displaced
runs. An older unpublished version may then need a new version to exceed
`latest`. Avoid cancelling active publication without checking for partial delivery.

## Licensing

The project is [MIT licensed](../LICENSE). Bundled dependencies keep their own
licenses in [THIRD_PARTY_NOTICES.txt](../THIRD_PARTY_NOTICES.txt).
`ts_omit_webclient` excludes Tailscale's unused web UI and its JavaScript/fonts;
it does not remove HTTP/CONNECT, SOCKS, or tsnet routing.

For dependency upgrades, run `mise exec -- bun scripts/licenses.ts` and review
the generated inventory/notices, including embedded non-Go assets. The script
uses go-licenses v2.0.1 with each production target's build settings.
`verify:licenses` rejects drift; `verify:helpers` checks build settings/module
versions and absence of the pinned web assets in all four binaries.
