# bun-tailscale-bridge

The HTTP-plus-SOCKS implementation is verified on Bun 1.4.2 and macOS arm64, including streamed HTTPS uploads, the full test suite, and clean-package checks. Helpers compile for macOS/Linux arm64/x64. Other native platforms, live enrollment, and external-agent compatibility remain unverified. This MIT package is **unpublished**; public release preparation does not mean it is available on npm. See [validation evidence and limits](docs/validation.md) and [release procedures](docs/releases.md).

A Bun ESM library managing a Go helper with an independent userspace Tailscale node. It exposes an authenticated HTTP proxy for native Bun `fetch` and an authenticated SOCKS5 proxy for SOCKS-capable clients. No root, system Tailscale daemon, host VPN, or TUN device is required.

## Build And Verify

Pins: **Bun 1.4.2**, **Go 1.26.2**, **Tailscale 1.96.5**, **TypeScript 5.9.3**. Development types use **@types/bun 1.4.1**, the latest published version at the upgrade; 1.4.2 types were unavailable. These are verification baselines, not promises about older versions. The Bun examples require no additional HTTP transport package. Go is needed to build, not to consume built helpers.

Use the project-pinned Bun 1.4.2 from `.bun-version` via your version manager or put that installation's `bin` directory first on `PATH`. Check `bun --version` returns `1.4.2` before the commands below, including Go tests that launch Bun. The project pin does not upgrade a global Bun installation; no global upgrade is required.

```sh
bun install --frozen-lockfile
bun run typecheck
bun test test
go -C helper test -tags=ts_omit_webclient ./...
go -C helper test -tags=ts_omit_webclient -race ./...
bun run build          # dist/index.js, declarations, native host helper
bun run build:all      # all four helpers, CGO_ENABLED=0
bun run verify:licenses # check reviewed four-target inventory/notices
bun run verify:helpers  # build settings, inventory, and absent web assets
bun run verify:package # pack, allowlist inspection, clean install/types/native launch
```

Artifact names are `bin/bridge-{darwin,linux}-{arm64,x64}`. Go builds map `x64` to `amd64`. Runtime selects exactly `../bin/bridge-${platform}-${arch}` relative to `src` or `dist`; it does not download, compile, or try another architecture. An absolute `helperPath` override is available for development. Missing/non-executable helpers, unsupported platforms, and mismatched protocols fail explicitly. Windows is outside the initial matrix.

Production and test helpers use the owner-approved `ts_omit_webclient` Go build tag.
Only Tailscale's unused embedded web UI is omitted; HTTP/CONNECT, SOCKS and bridge
lifecycle behavior are unchanged. CI checks that all pinned web JavaScript/font
assets are absent from every binary. The MIT project license and bundled Go
dependency licenses/notices are included in the package; MIT does not relicense
those dependencies.

`bun run pack` creates a local tarball after building. `verify:package` requires all four assets and uses `bun pm pack` with package scripts disabled. It installs into a temporary consumer, checks public exports/declarations and executable bits, and sends an incompatible protocol version to the native helper. A pass proves native launch without enrollment, not tailnet connectivity. No publication occurs. See [validation](docs/validation.md).

Use `bun scripts/verify-package.ts --tarball /absolute/path/package.tgz` to validate
an existing tarball without repacking or changing its digest. For once-packed
local candidate and isolated npm dry-run checks, follow [release procedures](docs/releases.md).

## Native Bun Requests

Install a built local tarball with `bun add /absolute/path/jaxxstorm-bun-tailscale-bridge-0.1.0.tgz`. After the owner completes the first public release, the intended registry installation is `bun add @jaxxstorm/bun-tailscale-bridge`.

```ts
import { createBridge } from "@jaxxstorm/bun-tailscale-bridge";

const bridge = await createBridge({
  hostname: "agent-bridge",
  stateDir: "/absolute/private/bridge-state", // Or ephemeral: true, never both.
  authKey: process.env.BRIDGE_AUTHKEY,        // Omit for enrolled state reuse.
  startupTimeoutMs: 60_000,
  // signal: controller.signal,
  // onAuthRequired: async ({ url }) => { /* present privately */ },
  // helperPath: "/absolute/development/helper",
});
try {
  const response = await fetch("https://aperture.example-tailnet.ts.net/api/providers", {
    proxy: bridge.httpProxyURL(),
    redirect: "error",
    headers: { Authorization: "Bearer synthetic-application-token" },
    signal: AbortSignal.timeout(10_000),
  });
  if (!response.ok) {
    await response.body?.cancel();
    throw new Error("Application request failed");
  }
  await response.json(); // Consume privately; SSE bodies support for-await.
} finally {
  await bridge.close();
}
```

Pass `proxy: bridge.httpProxyURL()` **on every relevant request**, keeping the original remote URL. Do not replace its hostname with loopback. No global fetch, `HTTP_PROXY`/`HTTPS_PROXY`, agent settings, or credential files are changed. The library does not intercept requests that omit the proxy option.

`httpProxyURL()` returns a sensitive string shaped like `http://tsnet:<32-hex-password>@127.0.0.1:<port>`. Bun uses its credentials for proxy authentication, independently of application Authorization/cookies. Never log, persist, or place that URL in argv or global environment variables. Native client errors can include sensitive URLs; report only allowlisted local error categories, not raw exceptions or upstream bodies.

The HTTP listener accepts authenticated absolute-form HTTP forwarding and CONNECT tunnels. HTTP requests are forwarded through tsnet; HTTPS uses CONNECT, then Bun performs TLS with the **original destination hostname**, certificate verification, and SNI. The helper does not terminate HTTPS. Do not disable certificate verification. A private fixture CA can be supplied explicitly through Bun's per-request `tls: { ca: trustedCertificate }`; an HTTPS IP URL needs a certificate valid for that IP.

The proxy has **no destination allowlist**. Redirects and application credential scoping belong to the consumer; examples use `redirect: "error"` to avoid implicit destination changes. HTTP statuses and response bodies remain application data. A proxy dial failure can produce a sanitized 502/504 response; HTTPS CONNECT failures can surface as native fetch errors. The helper does not replay failed application requests. Request cancellation and response consumption belong to the client; always consume or cancel bodies before closing. Example abort signals impose deliberate total deadlines, not unlimited stream lifetimes.

**Streamed HTTPS uploads:** Bun 1.4.2 fixes the native fetch CONNECT upload regression observed on Bun 1.3.14 ([upstream issue #33918](https://github.com/oven-sh/bun/issues/33918), [merged fix #32635](https://github.com/oven-sh/bun/pull/32635), released in [Bun 1.4.0](https://bun.sh/blog/bun-v1.4.0) and included in 1.4.2). Native regression tests cover concurrent multi-megabyte body integrity, first-chunk delivery before the producer finishes, and mid-upload abort stopping the producer and upstream work. No custom buffering or alternative transport is used. Fixed request bodies and streamed responses (including SSE) remain supported; see the separate historical and upgrade evidence in [validation](docs/validation.md).

Plain HTTP forwarding uses standard Go header handling, always removes proxy credentials and standard hop headers, and discards trailers. Additional fields nominated by a response `Connection` header cannot always be removed when Go consumes that header because it contains `close` (including informational responses). Trailer-dependent HTTP protocols are unsupported. CONNECT does not parse or modify tunneled application traffic.

## SOCKS Clients

`bridge.proxy()` remains available for clients with explicit SOCKS support:

```ts
const socks = bridge.proxy();
// { host: "127.0.0.1", port: number, username: "tsnet", password: string }
// password is 32 hexadecimal characters. Treat the whole descriptor as sensitive.
```

Configure those clients to send destination names to the proxy, usually called SOCKS5 remote DNS or `socks5h`. That permits MagicDNS resolution through tsnet. Use `httpProxyURL()` with native Bun `fetch` rather than passing it a SOCKS URL or a Node HTTP agent; the bridge's native-fetch contract covers the HTTP proxy, not SOCKS. There is no bundled third-party client adapter.

The SOCKS listener comes from the public `tsnet.Server.Loopback` API. It also serves HTTP LocalAPI using a **separate credential that is never exposed** by the bridge API. That listener is not the HTTP forward proxy. The authenticated forward/CONNECT proxy has its own numeric loopback listener, returned only by `httpProxyURL()`. Neither proxy credential is an application credential or LocalAPI credential.

## Routing And Trust

- Both proxy paths use standard tsnet dialing semantics. Tailnet destinations use userspace routing and access policy for this independent node, not the host's existing node identity.
- **Non-tailnet names/destinations retain upstream tsnet host DNS/direct behavior. These proxies are not an egress firewall or a guarantee of tailnet-only traffic.** Delegating a name to the proxy does not mean every name resolves inside the tailnet. Consumers enforce any required destination restrictions.
- Ordinary outbound networking remains necessary for Tailscale control-plane, DERP, bootstrap DNS, and peer connectivity. No host routes are installed, and requests outside the proxy are not intercepted.
- Numeric IPv4 loopback and authentication do not protect against a compromised same-user process. Proxy credentials grant routing access. Plain HTTP is visible to the proxy; HTTPS stays encrypted between the client and destination through CONNECT.
- The initial contract covers HTTP forwarding, CONNECT and SOCKS5 TCP. Do not infer UDP, browser, host-wide VPN, or universal plugin compatibility. Browser environments generally cannot configure these per-request proxy options.
- `BridgeError` lifecycle failures use sanitized codes such as `AUTH_REQUIRED`, `STATE_UNSAFE`, `STATE_LOCKED`, `STARTUP_TIMEOUT`, `PROTOCOL_ERROR`, and `HELPER_FAILED`. Native HTTP/SOCKS/TLS client exceptions and upstream bodies are not sanitized by the bridge.
- Raw tsnet logs are suppressed at the bridge boundary. Tailscale has its own control-plane/telemetry behavior; this is not a promise of no upstream telemetry. Review Tailscale's deployment/privacy documentation.

See Tailscale's [userspace networking documentation](https://tailscale.com/kb/1112/userspace-networking) and [`tsnet.Server.Loopback`](https://pkg.go.dev/tailscale.com/tsnet#Server.Loopback). This package embeds tsnet rather than invoking host `tailscaled`.

## Identity And Lifecycle

Use an absolute `stateDir` for persistent identity or `ephemeral: true` for disposable identity, never both. Persistent state is owner-only (`0700` directory, `0600` identity files), exclusively locked, and rejects unsafe ownership, permissions, and symlinked locations. Do not run two bridges against one directory. Separate directories mean separate nodes even with identical hostnames.

Reuse existing enrollment first. For new state, provide `authKey` explicitly or opt into `onAuthRequired: async ({ url }) => { /* present privately */ }`. Without either, startup fails with `AUTH_REQUIRED`. Login URLs are sensitive and delivered only to that callback. Ambient `TS_*` settings are not implicit enrollment configuration. The default startup deadline is 60 seconds; a startup `signal` cancels creation.

Creation resolves after node/proxy readiness, not proof of destination reachability. `close()` is idempotent and stops both listeners and active work. The parent bounds graceful helper shutdown before termination escalation; control-pipe EOF stops the helper on parent disappearance. Keep the bridge alive through response consumption. Both accessors fail after close/failure. Discard cached proxy URLs/descriptors and do not send stale credentials to a potentially reused port. Never silently retry without a proxy.

Persistent close does not delete identity or log out. Ephemeral state is removed on normal shutdown, but crashes can leave files and remote node deletion is not immediate or guaranteed by `close()`. Revoke nodes/keys separately in Tailscale. Never commit, package, upload, or place state, enrollment keys, proxy URLs/passwords, LocalAPI credentials, application credentials, or control transcripts under the package tree.

## Consumer Boundary

[The Aperture example](examples/aperture.ts) uses native Bun fetch with an explicit HTTP proxy for `/api/providers` and streamed `/codex/responses`. Application credentials/model data are synthetic placeholders. No SDK, OAuth flow, discovery/model policy, or plugin implementation is included.

Consumers own routing every relevant request, application authentication, redirect policy, and compatibility testing. Existing `jaxxstorm/opencode-aperture` is not claimed to work unchanged. Claude Code, Codex, Cowork, and other local/desktop agents are potential consumers only where the extension host can execute Bun/helpers, write private state, reach outbound networking/loopback, and configure HTTP or SOCKS proxy use. Hosted/sandboxed products are not supported by implication. No live-tailnet or external-agent compatibility is claimed by the documentation alone.
