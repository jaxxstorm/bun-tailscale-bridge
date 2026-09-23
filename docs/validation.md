# Testing

Run commands from the repository root. Use Bun **1.4.2**, Go **1.26.2**, and the
checked-in lockfiles (Tailscale **1.96.5**). `mise.toml` selects Bun only; put
Go 1.26.2 on `PATH` separately. Go tests that spawn Bun need the same environment.

## Local Checks

```sh
mise exec -- bun --version
mise exec -- go version
mise exec -- bun install --frozen-lockfile
mise exec -- bun run typecheck
mise exec -- bun test test
mise exec -- bun run test:go
CGO_ENABLED=1 mise exec -- go -C helper test -tags=ts_omit_webclient -race -count=1 ./...
mise exec -- go -C helper vet -tags=ts_omit_webclient ./...
mise exec -- bun run build:all
mise exec -- bun run verify:licenses
mise exec -- bun run verify:helpers
mise exec -- bun run verify:package
BRIDGE_LIVE=0 mise exec -- bun run test:live
```

Production builds use `CGO_ENABLED=0`; race tests need a C compiler. Builds,
Go tests, and license scans use `ts_omit_webclient` to exclude the unused web UI.

The suites use local fixtures for native Bun HTTP/CONNECT, TLS verification,
streaming and cancellation, SOCKS, credentials, state locking, and process
cleanup. `verify:package` checks the package allowlist, notices, executable modes,
isolated import/types, and native helper startup. It expects a sanitized protocol
error from deliberately invalid input and never enrolls a node.

To rerun the HTTPS upload regression specifically:

```sh
mise exec -- bun test test --test-name-pattern 'HTTPS CONNECT concurrent multi-megabyte streaming uploads'
```

Do not skip this test on Bun 1.4.2. Bun 1.3.14 stalled on these uploads; the
[upstream fix](https://github.com/oven-sh/bun/pull/32635) shipped in Bun 1.4.0.

CI is configured for Ubuntu 24.04 and macOS 14. Four compiled helpers do not
establish four-platform runtime support, and configured jobs do not prove a
successful hosted run. Local fixtures do not prove live enrollment, MagicDNS,
tailnet policy, or external-agent compatibility.

## Live Prerequisites

Live testing skips unless both `BRIDGE_LIVE=1` and `BRIDGE_LIVE_AUTHKEY` are set.
Keep enrollment keys out of argv, checked-in files, and CI. `TS_AUTHKEY` alone
does not opt in.

1. Use a disposable VM/container as a non-root user, without a host Tailscale
   daemon/client, mounted socket/state, host networking, `/dev/net/tun`, or
   network-administration capability. Privately check identity, processes,
   mounts, and TUN availability. The script cannot detect every host daemon;
   `BRIDGE_LIVE_DAEMONLESS=1` is your attestation.
2. Build first. The script loads `dist/index.js` and the matching production
   helper. Allow outbound control-plane, DERP, bootstrap DNS, and peer traffic.
   Runtime needs neither Go nor a system Tailscale installation.
3. Provide a permitted HTTPS MagicDNS fixture: `/health` returns 200; `/events`
   returns `text/event-stream`, immediately flushes a small LF-delimited event,
   and stays open at least five seconds. Also provide a literal tailnet IP origin
   with a valid IP certificate or an explicitly chosen HTTP port.
4. Provide a policy-denied, known tailnet IP fixture that is healthy from an
   authorized node. It must use HTTP and an address in `100.64.0.0/10` or
   `fd7a:115c:a1e0::/48`. This avoids confusing public routing or TLS errors with
   policy denial. Have policy/service logs available to corroborate the result.

## Run And Clean Up

Inject a disposable, suitably scoped `BRIDGE_LIVE_AUTHKEY` privately, then set
these nonsecret values for your fixtures:

```sh
export BRIDGE_LIVE=1
export BRIDGE_LIVE_DAEMONLESS=1
export BRIDGE_LIVE_DNS_ORIGIN=https://fixture.example-tailnet.ts.net
export BRIDGE_LIVE_IP_ORIGIN=http://100.64.0.10:8080
export BRIDGE_LIVE_DENIED_ORIGIN=http://100.64.0.11:8080
# Optional: export BRIDGE_LIVE_CA_FILE=/absolute/path/fixture-ca.pem
mise exec -- bun run test:live
```

Use credential-free origins without paths. A private CA file must contain PEM
certificates, never a private key. The script uses per-request `tls.ca` with
verification enabled; do not disable TLS verification or alter global TLS settings.

Every resource request explicitly uses `bridge.httpProxyURL()` with the original
destination URL. Checks cover DNS/IP health, early SSE delivery and cancellation,
denied connection failure, both listener ports closing, stale accessors failing,
and persistent restart without an auth key. Denial accepts proxy 502/504 or
connection failure/timeout, not application 401/403. Unreachability alone does
not prove policy enforcement. Non-tailnet host DNS/direct routing is intentional;
this is not an egress firewall test or a live SOCKS data-path test.

Record OS/architecture and tool versions without secrets, proxy URLs, raw client
errors, or response bodies. Confirm the same node identity in the admin console,
cancellation at the fixture, denial in policy/service logs, and no remaining
helper/listeners. Separately test ephemeral close and parent EOF if validating
those lifecycle paths; the script's PASS does not establish them.

The script removes only its disposable state directory in `finally`. Crashes
may leave state: remove only directories known to belong to this test. Revoke
the disposable node/key and unset live variables afterward. Local deletion is
not revocation; persistent close preserves identity. Do not delete provider or
agent configuration, or use the separate LocalAPI credential as a proxy password.

See [helper limits](../helper/README.md), [release checks](releases.md), and the
[historical local record](release-validation.md).
