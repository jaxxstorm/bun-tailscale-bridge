## 1. Repository and Protocol Foundation

- [x] 1.1 Create the private Bun/TypeScript package and Go module; pin Bun 1.4.2, Go 1.26.2, Tailscale 1.96.5, and an exact TypeScript version, with lockfiles, ESM/declarations, build/typecheck/test commands, and secret/state exclusions.
- [x] 1.2 Define `createBridge`, exclusive state options, `proxy()`/`httpProxyURL()`/`close()`, lifecycle/error types, and versioned 64 KiB-bounded private JSON frames; validate options and both ready endpoints with shared valid/invalid fixtures and separate SOCKS/HTTP credentials.

## 2. Userspace Node Lifecycle

- [x] 2.1 Implement owner-only persistent state and OS-backed exclusive locking; test unsafe permissions/ownership/symlinks, concurrent use, and identity preservation.
- [x] 2.2 Implement mutually exclusive ephemeral state with tsnet ephemeral enrollment and bridge-owned temporary cleanup; test normal and partial-start cleanup without deleting caller state.
- [x] 2.3 Implement tsnet enrollment reuse, explicit auth keys, structured interactive-auth callback events, `AUTH_REQUIRED`, and bounded/abortable `Up`; test using fake tsnet lifecycle without log parsing or ambient credentials.
- [x] 2.4 Implement private control protocol parsing, version negotiation, stdout isolation, and allowlisted diagnostics; test malformed/oversized/unknown/incompatible frames and secret redaction.
- [x] 2.5 Implement helper shutdown on command, EOF, and signals, closing active connections/listener/tsnet and releasing state; test parent disappearance and partial-start failure.

## 3. Tailnet SOCKS and HTTP Proxies

- [x] 3.1 Call supported `tsnet.Server.Loopback` after `Up`, publish only its loopback SOCKS descriptor (`tsnet`, tsnet-generated 32-hex password), and keep its separate LocalAPI credential internal; test readiness order and credential separation against the pinned implementation.
- [x] 3.2 Test the real upstream SOCKS server with an injected dialer for authentication rejection before dialing, hostname/IP forwarding, bidirectional byte transfer, and connection teardown; keep production `UserDial` unchanged and record its tailnet versus non-tailnet routing semantics.
- [x] 3.3 Implement a separate authenticated numeric-loopback HTTP/CONNECT proxy with independent random 32-hex HTTP credentials, `tsnet.Server.Dial` or a built-in SOCKS hop, standard HTTP forwarding and reusable `net/connectproxy` tunneling; require auth per request, strip proxy/hop-by-hop headers under the documented Go parser limits, preserve application auth, do not follow/replay requests, and track active CONNECT tunnels for shutdown.
- [x] 3.4 Test HTTP 407-before-dial auth including reused connections, credential separation, absolute-form forwarding, header handling, opaque CONNECT, redirects/no replay, streaming/abort, and teardown with injected dialing; extend the checked lifecycle/protocol/SOCKS baseline tests for readiness only after both listeners, second-listener failure, HTTP secret redaction, and CONNECT cleanup without treating those extensions as already verified.

## 4. Bun Consumption API

- [x] 4.1 Implement exact platform helper resolution and shell-free spawning with private pipes and filtered environment; test unsupported platforms, missing/non-executable helpers, and protocol mismatch before enrollment.
- [x] 4.2 Implement startup deadlines/cancellation, auth callback delivery, exit monitoring, partial-start cleanup, and idempotent `close()` with five-second grace and termination escalation.
- [x] 4.3 Implement synchronous ready-only `proxy()` and `httpProxyURL()`, returning the SOCKS descriptor and validated credential-bearing HTTP URL respectively; test both-endpoint readiness, lifecycle races, stale instances, endpoint validation, and no restart or global fetch/environment/configuration mutation.
- [x] 4.4 Add offline cross-language Bun native fetch tests against the real Go HTTP/CONNECT proxy with fake lifecycle and injected dialing: HTTP GET/POST, original URL/Host and application auth, HTTPS chain/hostname/SNI through opaque CONNECT, SSE/abort, consumer redirects, shutdown, and unrelated direct requests unaffected; use no additional JavaScript client dependencies.

## 5. Packaging and Consumer Documentation

- [x] 5.1 Build/package executable helpers for macOS/Linux arm64/x64 with ESM/declarations; verify exact asset selection and record build versus native-runtime coverage.
- [x] 5.2 Add clean-consumer tarball verification for contents, exports/types, executable assets, and native bundled-helper launch without enrollment, source-tree paths, runtime compilation, or downloads.
- [x] 5.3 Document enrollment/state/lifecycle, separate SOCKS/HTTP/LocalAPI credentials, upstream non-tailnet host DNS/direct behavior, lack of egress isolation, per-request routing, consumer TLS/redirect ownership, telemetry, errors, and sandbox prerequisites.
- [x] 5.4 Add an agent-independent Aperture example for `/api/providers` and streamed `/codex/responses` using native `fetch(remoteURL, { proxy: bridge.httpProxyURL() })`, application auth, normal HTTPS verification, cancellation, consumer redirect choices, and close; show unrelated requests remain direct and require no adapter, extra client dependencies, global mutation, SDK, or plugin code.

## 6. Verification and Evidence

- [x] 6.1 Add secret-free CI for Bun typecheck/tests, Go tests and supported race checks, four-platform builds, and clean-package verification.
- [x] 6.2 Add explicitly opt-in live verification for non-root daemonless/TUN-free enrollment, MagicDNS/permitted traffic, denied/unreachable known-tailnet targets, persistent identity reuse, and cleanup; skip without opt-in/credentials and do not assert non-tailnet blocking.
- [x] 6.3 Run available default/package checks and explicitly configured live checks; record exact pins, native platforms, simulated versus live evidence, and skipped checks without claiming agent compatibility.
- [x] 6.4 Have the main verifier review implementation evidence against all four capabilities and run strict OpenSpec validation before declaring implementation complete.
- [x] 6.5 Fix streamed HTTPS request-body compatibility without full-body buffering, verify the previously skipped regression, and update the supported Bun runtime and evidence.
