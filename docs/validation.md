# Validation And Evidence

## Default Checks

Use Bun 1.4.2, Go 1.26.2, Tailscale 1.96.5, TypeScript 5.9.3, and pinned lockfiles. Development types use `@types/bun` 1.4.1, the latest published version at the upgrade because 1.4.2 types were unavailable; this does not change the runtime pin. There are no third-party HTTP transport dependencies. Production helpers use `CGO_ENABLED=0`; native Go race checks need a C compiler and `CGO_ENABLED=1`. Build scripts use the installed TypeScript compiler, not unpinned downloads.

Select the project Bun installation matching `.bun-version` with your version manager, or prepend the Bun 1.4.2 installation's `bin` directory to `PATH`. Confirm `bun --version` prints `1.4.2`; use that same environment for Bun commands and Go tests that spawn Bun. The global Bun installation is not upgraded by the project pin and need not be upgraded. From the repository root, reproduce the formerly failing regression without changing or skipping the test:

```sh
bun --version # Must print 1.4.2
bun test test --test-name-pattern 'HTTPS CONNECT concurrent multi-megabyte streaming uploads'
```

`bun test test` is the Bun test entry point. Local tests must exercise native Bun fetch with the explicit HTTP proxy, HTTP forwarding, CONNECT/HTTPS trust and hostname verification, credentials, DNS destinations, streaming/cancellation, denial, lifecycle, and lack of client-side proxy bypass. SOCKS protocol tests remain separate coverage. These are local fixture checks, not live enrollment. A Node subprocess result is not evidence of native Bun HTTP support. No previous adapter test result establishes support for this new HTTP proxy implementation.

CI is configured for Ubuntu 24.04 and macOS 14. Jobs report their actual platform/architecture, run Bun typecheck/tests and Go tests/race checks, compile four assets, and inspect a clean tarball/native helper. A configured job is not a successful run. Linux arm64 and macOS x64 remain compilation-only unless checked on native runners.

`verify:package` creates/removes a temporary tarball and isolated consumer. Its allowlist accepts only package metadata, README, bundled ESM/declarations, and four expected helpers. It rejects unexpected paths, duplicates, missing assets, nonregular installed files, lost executable modes, runtime dependencies, and non-private metadata. The consumer imports `createBridge`/`BridgeError` and type-checks `proxy()`, `httpProxyURL(): string`, and `close()`. It uses no source imports or Go compiler; type checking uses the installed compiler/types as development tooling. Native launch sends an invalid protocol version with a synthetic secret and expects only sanitized `PROTOCOL_ERROR` and empty stderr. It never enrolls or exposes LocalAPI credentials.

## Opt-In Live Check

**Live validation skips unless `BRIDGE_LIVE=1` and `BRIDGE_LIVE_AUTHKEY` are supplied.** Do not add these to CI or checked-in environment files. No live result is claimed until an operator runs the procedure and records observations.

1. Use a disposable VM/container as an ordinary non-root user without a host Tailscale daemon/client, mounted Tailscale socket/state, host networking, `/dev/net/tun`, or network-administration capability. Check `id`, processes, mounts, and TUN availability privately. The script requires `BRIDGE_LIVE_DAEMONLESS=1` attestation; it cannot detect every host daemon from inside a container.
2. Build beforehand and use production `dist/index.js` and the matching helper. The live script uses native Bun fetch, not a Node subprocess or external HTTP adapter. Allow outbound control-plane, DERP, bootstrap DNS, and peer connectivity. No Go compiler/system Tailscale installation is needed at runtime.
3. Prepare a permitted tailnet fixture serving `GET /health` as HTTP 200 and `GET /events` as `text/event-stream`. Flush a small first LF-delimited event immediately; keep the stream open at least five seconds before the final event. Use a trusted HTTPS MagicDNS URL. Supply a literal tailnet IP origin for a second check, with a valid IP certificate or an explicitly selected HTTP port. For a private fixture CA, set `BRIDGE_LIVE_CA_FILE` to its PEM certificate; it is passed through per-request `tls.ca` with verification enabled. Never supply a private key, disable verification, or change global TLS configuration.
4. Prepare a policy-denied **known tailnet IP** fixture, healthy from an authorized node. The script requires an HTTP origin in `100.64.0.0/10` or `fd7a:115c:a1e0::/48`, so a public destination or TLS certificate error cannot masquerade as tailnet denial. It accepts proxy 502/504 or connection failure/timeout, not application 401/403. Corroborate using policy and service logs; unreachability alone does not establish policy enforcement. This is not an egress firewall test: non-tailnet host DNS/direct behavior is intentional.
5. Inject a disposable, suitably scoped enrollment key privately through your secret manager/environment, not argv or a committed file. Set the nonsecret variables below and run from the built checkout. A normal `TS_AUTHKEY` does not opt in.

```sh
export BRIDGE_LIVE=1
export BRIDGE_LIVE_DAEMONLESS=1
export BRIDGE_LIVE_DNS_ORIGIN=https://fixture.example-tailnet.ts.net
export BRIDGE_LIVE_IP_ORIGIN=http://100.64.0.10:8080
export BRIDGE_LIVE_DENIED_ORIGIN=http://100.64.0.11:8080
# Optional: export BRIDGE_LIVE_CA_FILE=/absolute/path/fixture-ca.pem
# Inject BRIDGE_LIVE_AUTHKEY privately before running.
bun run test:live
```

Every resource fetch explicitly sets `proxy: bridge.httpProxyURL()` and retains the original destination URL; HTTPS is verified by Bun through CONNECT. The script checks MagicDNS/IP health, timely first SSE delivery/cancellation, denied-tailnet failure, and restart with the same persistent state **without an auth key**. It closes both listeners, verifies stale accessors fail, and checks their TCP ports without sending stale credentials to a potentially reused listener. It removes only its own disposable state directory in `finally`. It never logs proxy URLs, raw client errors, or response bodies.

The live script exercises HTTP proxy traffic and SOCKS listener closure; it is not a live SOCKS data-path test. Test SOCKS-capable consumers separately if they will use that interface.

## Manual Observations

Record exact OS/architecture and Bun/Go/Tailscale versions without secrets or application content. Confirm the second start retains the same admin-console node identity without enrollment, the fixture observes stream cancellation, and no helper/listener remains after close. Corroborate denial using policy/service logs. Public destination reachability is not a proxy escape; tsnet's non-tailnet direct behavior is expected.

Also exercise `ephemeral: true` with disposable enrollment and confirm normal close removes bridge-owned state. Kill a parent in a separate run and confirm EOF stops the helper. Crashes can leave temporary state; remove only directories positively identified as belonging to the disposable test. These observations are not automatically established by the live PASS line.

Revoke the disposable node/key in Tailscale and unset live variables. Local state deletion is not control-plane revocation; persistent close intentionally preserves identity. Do not delete provider credentials/agent configuration as cleanup. Do not expose or use the separate LocalAPI credential as a proxy credential.

## Evidence Ledger

### Historical Bun 1.3.14 Baseline

Verified locally on 2026-09-07, macOS arm64, Bun 1.3.14, Go 1.26.2, Tailscale 1.96.5, TypeScript 5.9.3. These results predate the runtime upgrade and are not Bun 1.4.2 full-check evidence:

| Check | Result |
| --- | --- |
| `bun install --frozen-lockfile` | Passed |
| `bun run typecheck` | Passed |
| `bun test test` | 37 passed, 1 explicitly skipped, 0 failed |
| `go -C helper test ./...` | Passed: 55 top-level tests, 158 including subtests |
| `go -C helper test -race ./...` | Passed across all helper packages |
| `go -C helper vet ./...` | Passed |
| `bun run build:all` | Four helpers compiled; ESM and declarations generated |
| `bun run verify:package` | Nine-file allowlist, clean import/types/install, executable bits, native macOS arm64 protocol launch passed |
| Live checks without opt-in or without credentials | Both reported SKIP; no enrollment attempted |
| Strict OpenSpec validation | Passed |

The Bun suite runs native fetch against the real Go HTTP/CONNECT implementation with a simulated node/dialer. It covers authentication, remote-name forwarding, HTTP and HTTPS fixed JSON POSTs, TLS chain/hostname validation, incremental SSE, cancellation, HTTP streaming uploads, redirect choices, direct-fetch isolation, and active tunnel shutdown. Go tests also cover SOCKS, actual CONNECT/SNI traffic, state locking, private protocol handling, credentials, parent EOF, and cleanup. Lifecycle regressions cover retained descendant stdout and helper failure during pending authentication callbacks.

**Historical Bun 1.3.14 regression:** streamed HTTPS request bodies through CONNECT stalled, including with `duplex: "half"` or explicit `Content-Length`. Direct Bun HTTPS streaming and curl 8.7.1 chunked uploads through the same helper each delivered all 3 MiB with TLS verification enabled. The historical suite above skipped that compatibility test; this is not an instruction to skip it on the supported runtime. Fixed JSON/byte HTTPS request bodies and incremental SSE response bodies passed on that baseline.

### Bun 1.4.2 Upgrade Evidence

The exact previously failing concurrent HTTPS CONNECT streaming-upload test delivered zero body bytes across four requests on Bun 1.3.14; the identical test passed on Bun 1.4.2. Upstream [oven-sh/bun#33918](https://github.com/oven-sh/bun/issues/33918) tracks the regression; merged [PR #32635](https://github.com/oven-sh/bun/pull/32635) fixed it in [Bun 1.4.0](https://bun.sh/blog/bun-v1.4.0), included in 1.4.2. The fix is the approved runtime upgrade, not a Go helper change, custom full-body buffer, or replacement HTTP transport. Go and Tailscale pins remain unchanged.

The regression is now enabled. Local macOS arm64 targeted native CONNECT tests passed 10 repetitions on Bun 1.4.2: concurrent multi-megabyte upload integrity, plus two new tests proving that the first 32 KiB reaches upstream before the producer gate opens and that aborting mid-upload stops the producer and upstream work. These are local fixtures with TLS verification, not live-tailnet evidence.

Full verification completed on 2026-09-07 with Bun 1.4.2 (`744846f84`), macOS arm64, Go 1.26.2, Tailscale 1.96.5, TypeScript 5.9.3, and `@types/bun` 1.4.1:

| Check | Bun 1.4.2 result |
| --- | --- |
| `bun install --frozen-lockfile` | Passed |
| `bun run typecheck` | Passed |
| `bun test test` | 40 passed, 0 skipped, 0 failed; 178 assertions |
| `go -C helper test -race -count=1 ./...` | Passed across every helper package, uncached |
| Native Go-to-Bun HTTP/HTTPS test | Explicit verbose rerun passed, not skipped; 1.4.2 selected on PATH |
| `go -C helper vet ./...` | Passed |
| `bun run build:all` | ESM/declarations and all four helpers built |
| `bun run verify:package` | Nine-file allowlist, isolated install/types, executable bits, and native macOS arm64 launch passed |
| Live checks | Both missing-opt-in and missing-credential cases reported SKIP; no enrollment |
| Strict OpenSpec validation | Passed |

The downloaded 1.4.2 binary was selected per command through PATH; the globally installed Bun was not upgraded. There are no skipped upload tests or extra transport dependencies. Platform and live-connectivity limits below remain unchanged.

Plain HTTP uses Go's standard parser: response `Connection` metadata containing `close` is consumed, so arbitrary extra nominated fields can survive. Raw-wire tests cover final and informational responses while proving actual proxy credentials are still removed. All HTTP trailers are discarded; trailer-dependent protocols are unsupported. CONNECT traffic remains opaque.

Only macOS arm64 has local native-runtime evidence. Other helper assets are compilation-only; configured GitHub Actions jobs have not been run here. Live tailnet enrollment, MagicDNS, policy enforcement, and external-agent compatibility remain unverified. No real credentials, provider auth files, or tailnet resources were accessed during these checks.
