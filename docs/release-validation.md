# Release Checks

## Local Candidate: 2026-09-30

Release-readiness verification of the uncommitted worktree, on **macOS arm64**,
with **Bun 1.4.2**, **Go 1.26.2**, and **Node 24.11.1 / npm 11.6.2**. Nothing
was published, tagged, committed, or pushed. No account settings were changed;
no real credentials or Tailscale identity files were used. Node is tested only
for side-effect-free import and the expected `UNSUPPORTED_RUNTIME` failure, not
as a bridge runtime. Other Bun versions remain unverified and are rejected.

### Retained Candidate

Directory:

```text
/var/folders/_9/zz5h76fj6sg42r_w3n0408mw0000gn/T/opencode/bridge-public-candidate-0.1.0
```

| Property | Value |
| --- | --- |
| Package/version | `@jaxxstorm/bun-tailscale-bridge@0.1.0` |
| Tarball | `jaxxstorm-bun-tailscale-bridge-0.1.0.tgz` |
| SHA-256 | `e5a4757de037c36ccebe9e84002d0f7b1836734af5a1561be98ad236cddda679` |
| SHA-512 SRI | `sha512-LVEAumpYkwvUt3IuXfGJppZYXFnpH0ebwA3sRdPaKciS1CdS3rMkjNxxkX+7/91ktxuiLmlov+a0oobFGgd2zg==` |
| Compressed size | 33,689,705 bytes (33.69 MB) |
| Unpacked file size | 90,238,729 bytes (90.24 MB) |
| Files | 11 |

The bundle also retains `release.json` and `SHA256SUMS`. Its all-zero source
commit marks it as a **local validation fixture**, not an authorized release
commit. Only one candidate tarball was packed; subsequent checks used
`--existing`, never a rebuild/repack. Small disposable fixture archives produced
by source tests are not release candidates.

The 11-file allowlist is `package.json`, `README.md`, `LICENSE`,
`THIRD_PARTY_NOTICES.txt`, `dist/index.js`, `dist/index.d.ts`,
`dist/protocol.d.ts`, and `bin/bridge-{darwin,linux}-{arm64,x64}`. All four
helpers retained mode `0755`. No source, development fixtures, node_modules,
logs, caches, credentials, or identity state appear in the archive. The combined
helper distribution is retained for simplicity; no install compilation/download
hooks or external Bun executable are included.

### Commands And Outcomes

| Command | Outcome |
| --- | --- |
| `bun run typecheck` | PASS |
| `mise exec node@24.11.1 -- bun test test` | PASS: 61 tests across 7 files, 541 assertions |
| `go -C helper test -tags=ts_omit_webclient ./...` | PASS: all test packages |
| `go -C helper test -tags=ts_omit_webclient -race ./...` | PASS: all test packages |
| `go -C helper vet -tags=ts_omit_webclient ./...` | PASS |
| `bun run build:all` | PASS: four helper targets and JS/declarations |
| `bun run verify:licenses` | PASS: 53 entries per Darwin target, 56 per Linux target; no drift |
| `bun run verify:helpers` | PASS: target/toolchain/production entrypoint/module inventory checks; four pinned web assets absent per target |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -shellcheck= -pyflakes= .github/workflows/verify.yml .github/workflows/release.yml` | PASS |
| `mise exec node@24.11.1 -- bun scripts/release-dry-run.ts <candidate-directory> --existing` | PASS: exact candidate, clean consumers, isolated npm publish **dry run**, matching SRI, cleanup |
| `env BRIDGE_LIVE=0 bun run test:live` | SKIP, explicitly no enrollment attempted |
| `git diff --check` | PASS |

The initial dry run passed all consumer and byte checks but failed removing
temporary Go module-cache directories. Cache isolation/cleanup was corrected,
the leftover scratch directory was removed, and the retained candidate passed
the complete dry run again without repacking. The import fixture's source-only
typing was also corrected so source typecheck does not require prebuilt `dist`.

### Evidence Boundaries

- **Source tests:** protocol/schema errors, sanitized lifecycle failures,
  cancellation/timeouts, idempotent close, HTTP/CONNECT/SOCKS fixtures,
  streaming/TLS/cancellation, state ownership/mode/link/lock handling, and exact
  macOS `/var` and `/tmp` aliases. These are offline tests.
- **Packaged consumers:** separate Bun and npm parents install the exact tarball
  as an optional dependency with scripts disabled, both present and omitted.
  Package-name imports, declaration/API checking, helper build metadata and
  permissions, missing-helper errors, and direct local HTTP without the optional
  package all pass. Runtime consumers and helpers are denied source-checkout
  reads by the macOS sandbox. TypeScript tools/types and synthetic helper build
  preparation use checkout tooling, not a standalone developer-toolchain install.
- **Native production helper:** macOS arm64 invalid-protocol and unsafe-state
  rejection through installed default resolution pass before tsnet construction.
  No real node is brought up.
- **Synthetic packaged lifecycle/proxy:** first login callback, authorized marker
  reuse without another URL, HTTP POST forwarding, cancellation, timeout,
  callback/helper failure, repeated close, listener closure, child reaping, and
  ephemeral-state cleanup pass. Marker state is not a real Tailscale identity.
- **Other platforms:** macOS x64 and Linux arm64/x64 compile and pass static
  build/license/permission checks only. This machine did not execute them.
- **Live/integration:** real enrollment, real enrolled-state reuse, tailnet
  connectivity, browser UI, and actual opencode-aperture execution were not tested.

### Remaining Gates

Run the hosted native checks and exact-artifact checks from reviewed source;
record additional architecture evidence before making broader runtime claims.
The existing CI matrix executes Linux x64 and macOS arm64, not all four targets.
An explicitly approved live test and actual plugin integration remain separate
from these synthetic checks. State privacy assumes trusted local Unix
permissions/advisory locks; extended ACL grants and concurrent hostile same-user
path replacement are not validated.

Confirm npm scope/package ownership and version availability, protected
branch/tags, approval-protected `npm` environment, and npm trusted-publisher
settings. Resolve owner-authenticated first-publication bootstrap/provenance
before authorization. None of those hosting/account conditions was inspected or
changed. Follow [Releases](releases.md) for exact-byte delivery and partial-failure
recovery. The plugin integration changes are documented in [Usage](usage.md).

## Historical Checks

This summarizes the local results recorded in commit `4b9a79f` on **2026-09-09**,
not a new test run. The environment was macOS arm64, Bun 1.4.2, Go 1.26.2,
and Node 24.11.1/npm 11.6.2. No publication was attempted.

The record reports passing typecheck, Bun tests, tagged Go tests/race checks and
vet, workflow lint, and four-target builds using `ts_omit_webclient`. License
drift and helper checks passed, including module/build settings and exclusion
of the pinned web assets. Only macOS arm64 was executed natively.

One local candidate passed the package allowlist, isolated import/types,
executable modes, and native protocol-error check without enrollment. npm's
credential-isolated publish dry-run reported the same SRI; a later check of
the retained bundle passed without repacking.

| Candidate | Recorded value |
| --- | --- |
| Filename | `jaxxstorm-bun-tailscale-bridge-0.1.0.tgz` |
| SHA-256 | `a8c6f8d626cc99bb0160f66b848b41d3dc1dd640295bf302e9697067bbac2ab6` |
| SHA-512 SRI | `sha512-Be/gZsOhrbvti2jVJr/l7ieHIqEeWfft2hH2TyJyT5YE+aWCo43ypdDTx+dw+Z8Oxj/u2EKLk4derBYh3tyCgw==` |

The manifest used an all-zero fixture commit, not a release source commit.
These digests identify that historical local bundle, not a published package
or a candidate for a later checkout.

Live testing reported SKIP. Hosted CI, Linux native execution, live enrollment,
repository/npm ownership and protections, OIDC, provenance, and npm/GitHub
delivery were not verified. No hosting configuration was changed.

Use [Testing](validation.md) and [Releases](releases.md) for current procedures.
