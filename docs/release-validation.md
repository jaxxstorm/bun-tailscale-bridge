# Historical Release Checks

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
