## ADDED Requirements

### Requirement: Authenticated HTTP proxy endpoint
The helper SHALL provide a separate ephemeral listener bound only to numeric IPv4 loopback `127.0.0.1` for HTTP forwarding and CONNECT. `httpProxyURL(): string` SHALL synchronously return `http://tsnet:<password>@127.0.0.1:<port>` only while the bridge is ready. The HTTP password SHALL be independently generated per instance using 128 cryptographically random bits encoded as 32 hexadecimal characters, separate from SOCKS and LocalAPI credentials. Every request, including CONNECT and requests on reused connections, MUST authenticate using HTTP Basic `Proxy-Authorization` before destination dialing. Missing, malformed, or incorrect credentials SHALL receive 407 with a Basic `Proxy-Authenticate` challenge and no dial.

#### Scenario: Valid ready endpoint
- **WHEN** startup has completed with both proxy endpoints listening
- **THEN** `httpProxyURL()` returns the credential-bearing HTTP URL for the separate loopback listener without exposing SOCKS or LocalAPI passwords in that URL

#### Scenario: Unauthorized request
- **WHEN** a plain HTTP or CONNECT request lacks valid HTTP proxy credentials, including on a previously authenticated connection
- **THEN** the proxy responds with 407 and a Basic challenge without dialing any destination

#### Scenario: Independent credentials
- **WHEN** a caller attempts HTTP proxy authentication using the SOCKS or LocalAPI password
- **THEN** authentication fails and no destination is dialed

### Requirement: Standard plain HTTP forwarding
The proxy SHALL accept absolute-form plain HTTP targets and forward to the requested remote authority using standard Go HTTP facilities, not a fixed-origin gateway or custom HTTP parser. It SHALL preserve method, path/query, body, remote Host, application Authorization/cookies, upstream status, and ordinary end-to-end headers. It MUST unconditionally remove proxy credentials and the explicit standard hop-header denylist, and remove additional `Connection`-nominated fields when that metadata is available from Go's HTTP parser. Documentation SHALL disclose that Go consumes a response `Connection` header containing `close`, so additional nominated fields cannot always be identified, including on informational responses. Request/response trailers SHALL be discarded; trailer-dependent protocols are unsupported. The helper SHALL stream with bounded buffering/backpressure and incremental flushing, propagate client disconnect/cancellation, and neither follow redirects nor replay requests. Redirect responses SHALL reach the consumer unchanged except for normal proxy header removal.

#### Scenario: Application request preservation
- **WHEN** an authenticated absolute-form HTTP POST carries a path/query, body, and application Authorization
- **THEN** the chosen remote service receives those application values and its own Host, but not local proxy credentials or hop-by-hop headers
- **AND** upstream status and streamed response data reach the client without waiting for the entire body

#### Scenario: Consumer controls redirects and retries
- **WHEN** the upstream returns a redirect or the forwarding connection fails
- **THEN** the helper returns the redirect or a sanitized failure without following or replaying the request
- **AND** subsequent redirect or retry behavior belongs to the consumer's fetch configuration

### Requirement: Opaque CONNECT and end-to-end TLS
The proxy SHALL authenticate CONNECT, validate the requested host:port authority, and tunnel bytes bidirectionally after a successful tsnet-backed dial. The implementation SHALL reuse Tailscale `net/connectproxy` tunnel handling with explicit lifecycle tracking of active tunnels. The helper MUST NOT terminate TLS, inspect encrypted application headers, or disable remote verification. Bun SHALL retain the original destination for HTTPS SNI and certificate/hostname verification. Closing the bridge or disconnecting SHALL release tunnel resources, including hijacked connections not handled by ordinary HTTP server shutdown.

#### Scenario: HTTPS through CONNECT
- **WHEN** Bun native fetch requests an HTTPS remote URL with the explicit HTTP proxy option
- **THEN** the proxy opens a tunnel to that authority and Bun performs end-to-end TLS verification against the original destination
- **AND** an invalid certificate fails in the consumer without TLS interception or insecure downgrade

#### Scenario: Active tunnel shutdown
- **WHEN** the bridge closes with an active CONNECT tunnel
- **THEN** both tunnel sides are closed and no hijacked connection survives helper shutdown

### Requirement: Native HTTPS upload streaming
Bun 1.4.2 native fetch through the authenticated CONNECT proxy SHALL stream HTTPS request bodies without waiting for complete body production. The implementation MUST NOT introduce custom full-body buffering or a replacement HTTP transport. Concurrent multi-megabyte uploads SHALL preserve each request body's integrity. Mid-upload cancellation SHALL stop the request-body producer and upstream work and release the associated tunnel resources, without replay or direct fallback.

#### Scenario: Early chunk delivery
- **WHEN** a native HTTPS upload produces an initial 32 KiB chunk and gates the remaining body production on upstream receipt
- **THEN** upstream receives that chunk before the producer gate opens and before the complete body exists

#### Scenario: Concurrent upload integrity
- **WHEN** four native HTTPS CONNECT requests concurrently stream multi-megabyte bodies
- **THEN** each upstream request receives its complete expected body with matching length and content

#### Scenario: Abort during upload
- **WHEN** the consumer aborts a native HTTPS CONNECT request after upstream receives initial data but before body completion
- **THEN** the producer and upstream upload work stop and the associated tunnel resources are released without replay or direct fallback

### Requirement: Upstream routing without egress isolation
Both forwarding and CONNECT SHALL dial using `tsnet.Server.Dial` directly or a hop through the built-in SOCKS proxy, retaining upstream routing semantics. Known tailnet destinations SHALL use the userspace node subject to Tailscale policy without root, TUN, or host daemon. Non-tailnet host DNS/direct behavior SHALL remain available upstream. The implementation MUST NOT add an independent host-network bypass, ambient HTTP proxy chaining, destination allowlist, or egress-firewall claim.

#### Scenario: Requested hostname reaches the dial path
- **WHEN** an authenticated proxy request names a destination
- **THEN** its hostname and port reach the tsnet-backed dial path rather than requiring consumer-side destination resolution
- **AND** documentation states that upstream non-tailnet DNS/direct routing remains possible

### Requirement: Explicit per-request native Bun consumption
Examples and cross-language tests SHALL use Bun 1.4.2 native `fetch(remoteURL, { proxy: bridge.httpProxyURL() })` without an additional JavaScript HTTP/SOCKS adapter or client dependency. The original remote URL SHALL remain unchanged. Plugins SHALL choose which requests use the proxy; the bridge MUST NOT replace global fetch, mutate proxy environment variables, intercept unrelated traffic, or configure destinations globally. Consumers SHALL own application auth, TLS trust, response consumption, cancellation, and native fetch redirect/retry decisions. No agent SDK or plugin implementation SHALL be required.

#### Scenario: Selected tailnet request
- **WHEN** a plugin explicitly supplies the HTTP proxy URL to fetch for a tailnet service
- **THEN** that request uses the local authenticated proxy with its original remote URL and application credentials

#### Scenario: Unrelated direct request
- **WHEN** another fetch call omits the proxy option in an otherwise direct-network environment
- **THEN** it continues directly without traversing the bridge or being affected by bridge startup or shutdown
