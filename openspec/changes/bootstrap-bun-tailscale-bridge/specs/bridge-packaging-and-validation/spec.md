## ADDED Requirements

### Requirement: Installable self-contained runtime artifacts
The build SHALL produce a Bun-compatible ESM library, TypeScript declarations, and version-matched executable helpers for macOS/Linux arm64/x64. Runtime selection SHALL use the exact platform/architecture asset or explicit absolute `helperPath` development override. Unsupported platforms and missing, non-executable, or incompatible helpers MUST fail before enrollment. Runtime operation MUST NOT require Go, installed Tailscale, compilation, or downloads. Publication SHALL remain disabled until separately authorized.

#### Scenario: Clean packaged consumption
- **WHEN** a clean Bun project installs the locally packed artifact on a supported platform
- **THEN** it can import the API/types and launch the native bundled helper without source-tree paths, runtime compilation, or enrollment for the launch check
- **AND** package inspection confirms all four executable helper assets and excludes secrets, node state, and local agent configuration

#### Scenario: Missing platform asset
- **WHEN** the matching helper is absent or the platform is unsupported
- **THEN** startup fails locally without downloading a replacement or enrolling a node

### Requirement: Pinned reproducible offline verification
The repository SHALL pin Bun 1.4.2, Go 1.26.2, Tailscale 1.96.5, and an exact TypeScript version with lockfiles and build/typecheck/test/package commands. Development types SHALL pin `@types/bun` 1.4.1 because 1.4.2 types were unavailable at the upgrade; this SHALL NOT change the Bun runtime requirement. Tailscale 1.102.3 requires Go 1.26.6 and SHALL NOT replace the chosen pin under Go 1.26.2. Secret-free CI SHALL test fake tsnet lifecycle/control helpers, the real pinned upstream SOCKS server, and the Go HTTP/CONNECT proxy with injected dialing, without live enrollment. Consumer tests/examples SHALL use Bun native fetch with its explicit per-request HTTP proxy option, without additional JavaScript client dependencies or an HTTP/SOCKS adapter. Documentation SHALL explain selection of the project-pinned runtime without requiring a global Bun upgrade and distinguish historical Bun 1.3.14 evidence from new-runtime results.

#### Scenario: Offline protocol and connectivity tests
- **WHEN** default verification runs after dependencies are installed
- **THEN** lifecycle/state/protocol tests and real upstream SOCKS authentication, hostname forwarding, byte transfer, and teardown tests run without tailnet credentials
- **AND** Go HTTP/CONNECT tests cover 407-before-dial authentication, credential separation, forwarding/header behavior, opaque tunnels, redirect return/no replay, streaming, and shutdown
- **AND** cross-language Bun native fetch tests through the Go proxy cover HTTP GET/POST, original URL/Host and auth, HTTPS chain/hostname verification and SNI, incremental SSE/abort teardown, consumer redirect choices, and unrelated direct fetch requests remaining unaffected
- **AND** native HTTPS CONNECT upload regressions run without a compatibility skip on Bun 1.4.2, proving early chunk delivery before body completion, concurrent multi-megabyte body integrity, and mid-upload abort stopping producer/upstream work
- **AND** lifecycle tests cover both-endpoint readiness, partial-start cleanup, HTTP secret redaction, and tracked CONNECT shutdown
- **AND** simulated evidence is distinguished from real upstream-server coverage and no injected-dialer test is presented as proof of live routing or non-tailnet egress isolation

#### Scenario: Multiplexed credential boundary
- **WHEN** verification checks the pinned Loopback integration
- **THEN** it verifies readiness ordering and separation of SOCKS, HTTP proxy, and LocalAPI credentials without exposing the LocalAPI credential through the bridge boundary

### Requirement: Honest live connectivity evidence
The repository SHALL provide opt-in live verification using operator-supplied disposable credentials and targets. It SHALL cover non-root daemonless/TUN-free enrollment, permitted known-tailnet traffic through SOCKS and native fetch through HTTP/CONNECT, MagicDNS, denied/unreachable known-tailnet targets, persisted identity reuse, and shutdown. It MUST NOT assert that non-tailnet destinations are blocked. Reports SHALL distinguish offline simulation, cross-compilation, native runtime checks, and live-tailnet evidence.

#### Scenario: No live credentials
- **WHEN** explicit opt-in or required credentials are absent
- **THEN** live enrollment is not attempted and verification reports the check as skipped, not passed

#### Scenario: Live daemonless connectivity
- **WHEN** an operator runs the live procedure with permitted and denied/unreachable known-tailnet targets in a non-root environment without host Tailscale or TUN
- **THEN** permitted traffic uses tsnet, the denied/unreachable known-tailnet target fails, identity can be reused, and close stops local resources
- **AND** the report makes no egress-firewall claim about non-tailnet host networking

### Requirement: Consumer-oriented documentation
Documentation SHALL cover enrollment, private state, lifecycle, separate SOCKS/HTTP/LocalAPI credentials, upstream routing semantics, sanitized diagnostics, telemetry, platform evidence, and sandbox prerequisites. It SHALL explain `proxy()` for compatible SOCKS clients and `httpProxyURL()` for explicit per-request Bun native fetch, retaining original remote URLs and end-to-end HTTPS through CONNECT. Proxy-side destination dialing MUST NOT be described as eliminating upstream non-tailnet host DNS/direct routing. An agent-independent Aperture example SHALL use native fetch for discovery and streamed requests without additional client dependencies, an adapter, SDKs, plugin code, global mutation, or a fixed-origin gateway. Consumers SHALL own request selection, destinations, TLS trust, application auth, redirect/retry decisions, cancellation, plugin changes, and compatibility testing.

#### Scenario: Aperture consumer adopts connectivity
- **WHEN** a plugin author follows the example
- **THEN** they can create the bridge, pass `httpProxyURL()` explicitly to native fetch for `/api/providers` and streamed `/codex/responses` on the original remote service URL, leave unrelated requests direct, consume responses, and close the bridge
- **AND** the example neither modifies global fetch/agent configuration nor claims existing plugins work unchanged

#### Scenario: Secret-safe diagnostics
- **WHEN** synthetic auth keys, SOCKS/HTTP/LocalAPI passwords, login URLs, or sensitive upstream error text occur during execution
- **THEN** bridge-generated diagnostics and errors contain only allowlisted stages and sanitized codes, never those values or credential-bearing proxy URLs
