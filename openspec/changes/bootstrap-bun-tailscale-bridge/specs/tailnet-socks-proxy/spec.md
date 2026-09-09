## ADDED Requirements

### Requirement: Supported authenticated loopback SOCKS endpoint
The helper SHALL call supported `tsnet.Server.Loopback` after `tsnet.Server.Up` succeeds and expose its built-in SOCKS endpoint, not a custom proxy. The descriptor SHALL contain `host: '127.0.0.1'`, an ephemeral TCP port, `username: 'tsnet'`, and the tsnet-generated random 128-bit password encoded as 32 hexadecimal characters. The built-in listener multiplexes SOCKS and separately authenticated LocalAPI; the LocalAPI credential MUST NOT be exposed through public APIs, IPC, examples, or diagnostics.

#### Scenario: Ready proxy
- **WHEN** tsnet is up, Loopback has returned its listening endpoint, and the HTTP proxy endpoint is also listening
- **THEN** startup may publish the SOCKS descriptor with exactly the SOCKS credential supplied by tsnet
- **AND** the separate LocalAPI credential remains internal to the helper

#### Scenario: Invalid SOCKS authentication
- **WHEN** a local client omits or supplies incorrect SOCKS credentials
- **THEN** the upstream SOCKS server rejects authentication before dialing a destination

#### Scenario: Credential separation
- **WHEN** a consumer receives the SOCKS descriptor
- **THEN** it cannot authenticate to the multiplexed LocalAPI using the SOCKS password and receives no LocalAPI password from the bridge

### Requirement: Upstream userspace routing semantics
Production SOCKS connections SHALL use the built-in tsnet `UserDial` behavior. Known tailnet destinations SHALL use the embedded userspace node subject to Tailscale policies without a host daemon, TUN, or root. Non-tailnet destinations SHALL retain upstream host DNS/direct dialing behavior. The bridge MUST NOT claim all destinations or DNS are tailnet-only, implement a destination allowlist, or advertise egress-firewall isolation.

#### Scenario: Known tailnet destination
- **WHEN** a SOCKS client connects to a known permitted tailnet destination
- **THEN** upstream routes the connection through the userspace node subject to tailnet policy

#### Scenario: Non-tailnet destination
- **WHEN** a SOCKS client requests a non-tailnet destination
- **THEN** upstream host DNS/direct behavior remains available rather than being blocked by a custom strict dialer

#### Scenario: Remote name resolution
- **WHEN** a consumer passes the destination hostname to the SOCKS connector for proxy-side DNS
- **THEN** the client sends the hostname to the SOCKS server instead of resolving it locally
- **AND** documentation explains that upstream may still use host DNS for non-tailnet names

### Requirement: Minimal connectivity API
The library SHALL expose `createBridge({ hostname, stateDir?, ephemeral?, authKey?, startupTimeoutMs?, signal?, onAuthRequired?, helperPath? })` returning `proxy()`, `httpProxyURL()` as defined by `tailnet-http-proxy`, and asynchronous idempotent `close()`. Exactly one of absolute `stateDir` or `ephemeral: true` SHALL be required. Startup timeout SHALL default to 60,000 ms. `proxy()` SHALL synchronously return a fresh SOCKS descriptor only while both proxy endpoints are ready. The library SHALL have no destination configuration, bridge-owned fetch wrapper, agent SDK dependency, or global network/configuration mutation.

#### Scenario: Consumer obtains connectivity
- **WHEN** a caller creates a bridge with valid options and startup succeeds
- **THEN** `proxy()` supplies credentials for its own SOCKS-capable client and the caller selects destinations independently

#### Scenario: Invalid options
- **WHEN** a caller supplies both state modes, neither state mode, an empty hostname, a nonpositive/nonfinite timeout, or a relative state/helper path
- **THEN** startup fails with a sanitized configuration error before enrollment

#### Scenario: Closed or failed instance
- **WHEN** the helper has failed or the bridge has closed
- **THEN** `proxy()` fails locally, existing connections are terminated, and no replacement direct endpoint or automatic helper restart is provided

### Requirement: Consumer-owned application transport
Consumers SHALL own destinations, application protocols, TLS, authentication, redirects, retries, request deadlines, and cancellation. The SOCKS endpoint SHALL remain a byte-level interface without application-level handling or header transformation; the separate HTTP endpoint SHALL follow `tailnet-http-proxy`. SOCKS consumers MAY use their own compatible clients without a mandated package or URI format. Bun HTTP examples SHALL use native fetch through the HTTP proxy rather than a JavaScript SOCKS-to-HTTP adapter.

#### Scenario: Existing SOCKS client
- **WHEN** a consumer supplies `proxy()` credentials to its own SOCKS-capable client
- **THEN** it chooses destinations and application behavior independently of the HTTP proxy and retains responsibility for client disposal
