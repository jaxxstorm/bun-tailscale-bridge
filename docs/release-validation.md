# Release Validation Evidence

Local validation of `add-ci-release-workflows`, with Bun 1.4.2, Go 1.26.2 and
Node 24.11.1/npm 11.6.2, on macOS arm64. No real publication was attempted.

The approved `ts_omit_webclient` build compiled all four production helpers.
`verify:helpers` confirmed matching build settings/module versions and absence
of all four exact pinned web assets in each binary. The regenerated license
inventory passed its subsequent read-only drift check: 53 entries on each Darwin
target, 56 on each Linux target. The tagged HTTP/SOCKS Bun tests and uncached
Go race suite passed; no networking library source was changed.

One real local candidate was packed from these outputs and retained at:

`/var/folders/_9/zz5h76fj6sg42r_w3n0408mw0000gn/T/opencode/bridge-release-candidate`

- Filename: `jaxxstorm-bun-tailscale-bridge-0.1.0.tgz`
- SHA-256: `a8c6f8d626cc99bb0160f66b848b41d3dc1dd640295bf302e9697067bbac2ab6`
- SHA-512 SRI: `sha512-Be/gZsOhrbvti2jVJr/l7ieHIqEeWfft2hH2TyJyT5YE+aWCo43ypdDTx+dw+Z8Oxj/u2EKLk4derBYh3tyCgw==`
- Strict allowlist: 11 files, including MIT/third-party notices and all helpers.
- Supplied-tarball clean consumer: scoped import, types, executable bits, native
  protocol failure without enrollment; input digest unchanged.
- npm publish dry-run: isolated configuration/credentials, scripts disabled,
  public registry/access, same name/version/SRI confirmed. No publish occurred.

The manifest uses an all-zero local fixture commit because this checkout has
no commits or remote. It is not an authorized release candidate. Hosted source
verification instead binds to the immutable peeled event object and protected
main ancestry, with negative/moved-tag cases tested using local command fixtures
without creating commits or tags.

Final local checks passed:

- Frozen Bun dependency installation and TypeScript checking.
- 57 Bun tests, 497 assertions across six files, including streamed HTTPS,
  stable/annotated-tag policies, tarball tampering, registry failure/idempotence,
  GitHub upload failures/conflicts/latest ordering, and workflow permissions.
- Independent workflow review found and fixed version-coupled test fixtures;
  a version-bump regression now verifies subsequent releases without changing
  fixed-version policy fixtures. The full suite and typecheck passed again.
- Uncached tagged Go tests, uncached tagged native race checks, and Go vet.
- All-target build, reviewed license drift check, and actual binary web-asset
  exclusion/module-inventory checks.
- Supplied-tarball clean consumer and npm 11.6.2 publish dry-run with matching SRI.
- The final `release.ts check` CLI and a second native check of the same retained
  candidate succeeded, without repacking or modifying it.
- actionlint v1.7.7 for both workflows, plus strict OpenSpec validation.
- `BRIDGE_LIVE=0` explicitly reported SKIP, with no enrollment attempted.

Hosted CI, Linux native execution, live-tailnet enrollment, ownership/protection
settings, OIDC exchange, provenance issuance and real npm/GitHub delivery remain
external checks, not inferred successes. No hosting configuration was mutated.

## Implementation Files

Full change inventory across both implementation sessions (the worktree was
untracked before implementation, so Git cannot supply a baseline diff):

```text
.github/workflows/verify.yml
.github/workflows/release.yml
LICENSE
THIRD_PARTY_NOTICES.txt
README.md
package.json
bun.lock
examples/aperture.ts
helper/README.md
helper/internal/testhelper/main_test.go
scripts/build.ts
scripts/package-policy.ts
scripts/verify-package.ts
scripts/licenses.ts
scripts/license-report.tmpl
scripts/verify-helpers.ts
scripts/release-policy.ts
scripts/candidate.ts
scripts/release.ts
scripts/github-delivery.ts
scripts/release-dry-run.ts
test/proxy-integration.test.ts
test/release.test.ts
test/workflows.test.ts
test/candidate.test.ts
test/github-delivery.test.ts
docs/license-inventory.json
docs/releases.md
docs/release-validation.md
openspec/changes/add-ci-release-workflows/proposal.md
openspec/changes/add-ci-release-workflows/design.md
openspec/changes/add-ci-release-workflows/specs/tagged-package-releases/spec.md
openspec/changes/add-ci-release-workflows/tasks.md
```

No networking library source, `.opencode/**`, or unrelated baseline OpenSpec
artifacts were changed. All 16 tasks are checked; no commit, tag, push,
publication, GitHub Release, or hosted configuration mutation was performed.
