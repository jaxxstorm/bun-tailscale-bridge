## Why

Locally extensible agents need private tailnet connectivity without installing a system Tailscale daemon or configuring a host VPN. A Bun library can manage an independent userspace node and offer SOCKS plus an authenticated HTTP/CONNECT proxy, letting plugins explicitly route selected requests with Bun native fetch while leaving unrelated traffic unchanged.

## What Changes

- Bootstrap a Bun/TypeScript library managing a Go helper embedding `tsnet`, with no root, TUN device, or host daemon requirement.
- Preserve explicit enrollment, persistent/ephemeral identity, readiness, cancellation, shutdown, private control pipes, and sanitized failures.
- Expose `createBridge(options)` returning `proxy()`, `httpProxyURL(): string`, and `close()`. Retain supported `tsnet.Server.Loopback` SOCKS access after `Up`, never exposing its separate LocalAPI credential. Add a separate numeric-loopback HTTP proxy with independent per-instance credentials; readiness requires both endpoints.
- Use upstream tsnet routing: SOCKS uses `UserDial`; the Go HTTP proxy uses `tsnet.Server.Dial` directly or a hop through the built-in SOCKS service. Known tailnet destinations use the userspace node; upstream non-tailnet host DNS/direct behavior remains available. This is not an egress firewall or a strict tailnet-only destination API.
- Authenticate every HTTP proxy request before dialing, forward absolute-form plain HTTP with proxy/hop-by-hop header removal, and tunnel CONNECT as opaque bytes. Preserve application authentication and original remote URLs; Bun owns end-to-end HTTPS verification. The helper neither follows redirects nor replays requests; consumers choose fetch redirect/retry/cancellation behavior.
- Pin Bun 1.4.2, Go 1.26.2, and Tailscale 1.96.5. Demonstrate native `fetch(remoteURL, { proxy: bridge.httpProxyURL() })` without a JavaScript HTTP adapter or additional client dependencies. No global interception, proxy-environment mutation, destination allowlist, fixed-origin gateway, or agent SDK/plugin implementation is included.
- Resolve the Bun 1.3.14 streamed HTTPS CONNECT upload regression by upgrading the runtime, not changing Go or adding custom buffering/transport. Require early upload delivery, concurrent multi-megabyte integrity, and mid-upload abort coverage. The upstream fix ([issue #33918](https://github.com/oven-sh/bun/issues/33918), [merged PR #32635](https://github.com/oven-sh/bun/pull/32635)) shipped in Bun 1.4.0 and is included in 1.4.2. Keep prior 1.3.14 evidence explicitly historical.
- Establish four-platform helper packaging, offline tests, and optional live-tailnet evidence.

## Capabilities

### New Capabilities

- `userspace-node-lifecycle`: Managed helper startup, enrollment, state, readiness, shutdown, and private control protocol.
- `tailnet-socks-proxy`: Supported tsnet loopback SOCKS access, credential separation, routing semantics, and minimal consumer API.
- `tailnet-http-proxy`: Authenticated loopback HTTP forwarding and CONNECT tunneling for explicit per-request Bun native fetch.
- `bridge-packaging-and-validation`: Pinned builds, helper distribution, offline/live evidence, and consumer documentation.

### Modified Capabilities

None. This change bootstraps the initial product contract.

## Impact

- Adds the library, helper, build scripts, tests, CI, and agent-independent examples. Runtime consumers need a matching helper, not Go or a Tailscale installation.
- Creates an independent node identity subject to Tailscale access policies. Enrollment secrets, SOCKS credentials, HTTP proxy credentials, LocalAPI credentials, and application credentials are distinct.
- Plugins such as `jaxxstorm/opencode-aperture` select tailnet requests and pass the HTTP proxy URL explicitly to native fetch, or use their own SOCKS-capable clients. Plugin changes, OAuth, discovery policy, and agent compatibility testing remain external.
- The built-in listener multiplexes SOCKS and authenticated LocalAPI; a separate listener serves HTTP/CONNECT. Only SOCKS and HTTP proxy credentials cross the private helper boundary. Loopback authentication does not protect against same-user compromise.
- Initial packaging targets macOS/Linux arm64/x64. Windows, host VPN configuration, agent adapters, and package publication are out of scope. Use standard Go HTTP forwarding and reusable Tailscale `net/connectproxy` tunnel handling, with no custom HTTP parser or JavaScript client dependencies.
