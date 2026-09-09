## ADDED Requirements

### Requirement: Managed userspace node
The library SHALL manage an independent Go tsnet helper through Bun without requiring root privileges, a host Tailscale daemon, a TUN device, or host VPN configuration. Known tailnet connections SHALL use the embedded userspace node; non-tailnet host DNS/direct behavior remains as defined by `tailnet-socks-proxy` and `tailnet-http-proxy`. Node readiness MUST NOT be represented as proof of destination reachability.

#### Scenario: Start without host Tailscale
- **WHEN** a non-root Bun caller starts a bridge with valid enrollment and network access in an environment without a host Tailscale daemon or TUN device
- **THEN** startup resolves only after `tsnet.Server.Up` succeeds and both the Loopback SOCKS endpoint and separate HTTP/CONNECT endpoint are listening

#### Scenario: Second listener fails
- **WHEN** either proxy endpoint cannot start after the other has begun listening
- **THEN** startup rejects and closes both endpoints, active connections, and tsnet without publishing partial readiness

### Requirement: Explicit enrollment and bounded startup
The library SHALL reuse enrolled state or accept an explicit auth key. For unenrolled state without a key it SHALL deliver a structured login URL only through an opted-in authentication callback, or fail with `AUTH_REQUIRED` when no callback exists. Startup SHALL support cancellation and a configurable deadline defaulting to 60 seconds, cleaning up partially started resources on failure. The helper MUST NOT implicitly consume ambient Tailscale authentication/configuration variables.

#### Scenario: Interactive enrollment
- **WHEN** a new identity starts without an auth key and with an authentication callback
- **THEN** the callback receives the login URL and startup waits for enrollment within the configured deadline
- **AND** the login URL is not emitted in diagnostics

#### Scenario: No enrollment mechanism
- **WHEN** a new identity starts without an auth key or authentication callback
- **THEN** startup fails with `AUTH_REQUIRED` and leaves no running helper or listening proxy

#### Scenario: Startup timeout or cancellation
- **WHEN** startup exceeds its deadline or the caller aborts startup
- **THEN** the promise rejects with a sanitized timeout or cancellation error and the helper and any listeners are stopped

#### Scenario: Ambient credentials are ignored
- **WHEN** the parent environment contains Tailscale credentials but the caller supplies no explicit auth key
- **THEN** the helper does not enroll using those ambient credentials

### Requirement: Private persistent and ephemeral identity
Persistent mode SHALL require an absolute state directory, reuse its enrolled identity, and protect the directory and identity files with owner-only permissions. Unsafe ownership, permissions, symlinked state locations, and concurrent use of one state directory MUST be rejected. Ephemeral mode SHALL be mutually exclusive with a persistent directory, use private temporary state, and remove bridge-owned temporary state on normal shutdown. Persistent shutdown MUST NOT delete or revoke identity.

#### Scenario: Persistent identity reuse
- **WHEN** a bridge is closed and restarted using the same valid state directory without an auth key
- **THEN** it reuses the enrolled identity without requiring fresh enrollment

#### Scenario: Concurrent or unsafe state
- **WHEN** another helper holds the directory lock or the supplied state location is unsafe
- **THEN** startup fails explicitly without modifying another instance's identity

#### Scenario: Ephemeral cleanup
- **WHEN** an ephemeral bridge closes normally
- **THEN** its temporary state is removed without deleting any caller-owned persistent directory
- **AND** documentation does not promise immediate remote node deletion or crash-proof temporary cleanup

### Requirement: Versioned private helper protocol
The library and helper SHALL exchange bounded, versioned JSON control messages over inherited stdin/stdout. Frames MUST be limited to 64 KiB. Auth keys, SOCKS/HTTP proxy credentials, and credential-bearing URLs MUST NOT appear in argv, ordinary diagnostics, or persisted control transcripts. Ready frames SHALL carry both validated proxy endpoints with separate credentials; the LocalAPI credential MUST NOT cross this channel. Malformed, oversized, unknown, or incompatible frames and invalid/missing proxy endpoint data MUST fail the instance rather than be accepted as readiness.

#### Scenario: Protocol incompatibility
- **WHEN** the helper reports an unsupported protocol version or invalid frame during startup
- **THEN** startup rejects with a sanitized protocol error and terminates the helper

#### Scenario: Secret handling
- **WHEN** startup uses a synthetic enrollment secret and obtains separate SOCKS and HTTP proxy passwords
- **THEN** none of those values or credential-bearing proxy URLs appear in captured argv, logs, or error messages

### Requirement: Deterministic shutdown and failure
The bridge SHALL provide idempotent `close()`, terminate active SOCKS connections, HTTP forwarding work, and explicitly tracked hijacked CONNECT tunnels during shutdown, close both listeners/transports and tsnet, and bound graceful shutdown to five seconds before terminating an unresponsive helper with forced escalation if necessary. The helper SHALL shut down on control-channel EOF and release state resources. Unexpected helper failure SHALL invalidate the instance without automatic restart or replacement direct connections; this does not restrict normal upstream non-tailnet routing.

#### Scenario: Repeated close
- **WHEN** the caller closes a bridge with active SOCKS, HTTP streaming, and CONNECT connections and then closes it again
- **THEN** all connections are terminated, the helper and both listeners stop, and repeated close completes safely

#### Scenario: Parent disappears
- **WHEN** the parent exits and the helper observes control-channel EOF
- **THEN** the helper stops accepting connections and shuts down without remaining as a detached daemon

#### Scenario: Helper crashes
- **WHEN** the helper exits unexpectedly after readiness
- **THEN** existing connections fail and later `proxy()` or `httpProxyURL()` calls report a failed instance without restarting or returning a replacement direct endpoint
