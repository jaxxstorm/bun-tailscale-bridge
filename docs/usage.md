# Using the bridge

The bridge manages a Tailscale node and two local proxies. Your application owns
the HTTP client, destination URLs, application authentication, and request lifetime.

## opencode-aperture integration

For the intended integration after publication, pin the plugin's dependency
without a caret or tilde:

```json
{
  "optionalDependencies": {
    "@jaxxstorm/bun-tailscale-bridge": "0.1.0"
  }
}
```

This is integration guidance, not a claim that the package is published or that
opencode-aperture has been validated end to end. Until publication, use the
[local tarball](../README.md#install) for integration testing.

Resolve the package specifier from the installed plugin's module context, not
the user's working directory or a global installation. Users should not need an
absolute `modulePath`. If a worker lives outside the plugin tree, resolve the
package in the plugin context and pass that resolved module URL internally to
the worker. Keep the installed package and its adjacent bundled helpers intact.

Run bridge creation and proxied fetches in an external **Bun 1.4.2** worker. The
npm dependency supplies neither that executable nor a guarantee that OpenCode's
embedded runtime can launch it; check the worker's Bun version separately.
Do not use the OpenCode executable as a substitute for `bun`.

Optional dependencies can be omitted or fail to install. Load the bridge only
when tailnet transport is enabled; if the package or external Bun is absent,
report an actionable configuration error. Do not silently fall back to direct
requests when tailnet transport was requested. With the feature disabled, leave
the plugin's existing transport unchanged.

Keep one bridge per worker lifetime and a stable, private absolute `stateDir`
per identity. Reuse it on restart without an auth key after enrollment; do not
delete it on routine shutdown. Pass startup cancellation through `signal` and
request cancellation separately to fetch. An `authKey` is optional;
`onAuthRequired({ url })` is also optional and should be supplied only when the
plugin can privately present an interactive login link. The bridge is transport
only: provider discovery, OAuth, service credentials, and plugin configuration
remain the plugin's responsibility.

## Configuration

```ts
import { join } from "node:path";
import { homedir } from "node:os";
import { createBridge } from "@jaxxstorm/bun-tailscale-bridge";

const bridge = await createBridge({
  hostname: "opencode-aperture",
  stateDir: join(homedir(), ".config", "my-plugin", "tailscale"),
  authKey: process.env.BRIDGE_AUTHKEY,
});
```

| Option | Purpose |
| --- | --- |
| `hostname` | Node name in Tailscale. Required; use a DNS label such as `my-agent`. |
| `stateDir` | Absolute path for a persistent identity. Use this or `ephemeral`, not both. |
| `ephemeral: true` | Enroll an ephemeral node with temporary local state. |
| `authKey` | Optional Tailscale enrollment key. Omit when reusing an enrolled identity. |
| `onAuthRequired` | Optional callback receiving `{ url }` for interactive enrollment. May return a promise. |
| `startupTimeoutMs` | Startup deadline, including enrollment. Defaults to 60,000 ms. |
| `signal` | AbortSignal for startup. Does not cancel requests after startup. |
| `helperPath` | Absolute path to a development helper binary instead of the bundled one. |

State directories and identity files must be private to the current user
(`0700` and `0600`). The helper creates new state with those permissions and
rejects unsafe existing state. A directory can belong to only one running bridge;
use separate directories for separate nodes.

User-controlled symlinks anywhere in the state path are rejected; the exact
root-owned macOS `/var` and `/tmp` system aliases are supported. Existing identity
files are never repaired or replaced by state validation. The persistent
`.bridge.lock` inode is never deleted, including after a crash. An unlocked lock
file can be reused; a held advisory lock is never stolen. Use a trusted local
filesystem with working Unix permissions and advisory locks. Validation checks
owner/mode bits, not extended ACL grants, and does not defend against a malicious
process running as the same user changing paths concurrently.

## Authentication

For unattended startup, supply an auth key explicitly. The library does not pick
up `TS_AUTHKEY` or other Tailscale settings automatically from the environment.
You can read whichever environment variable your application uses and pass it as
`authKey`.

For interactive enrollment, omit the key and provide a callback:

```ts
const bridge = await createBridge({
  hostname: "my-agent",
  stateDir,
  startupTimeoutMs: 120_000,
  onAuthRequired: async ({ url }) => {
    await showLoginLink(url); // Your application's private login UI.
  },
});
```

Startup waits for enrollment. Without saved enrollment, an auth key, or a callback,
it fails with `AUTH_REQUIRED`. Treat login links as sensitive; do not include them
in shared logs or agent transcripts.

Tailscale authentication grants network access, not application access. Supply
API keys, OAuth tokens, or other service credentials in your requests as usual.
Tailnet access policy must allow the bridge's node to reach the destination.

## HTTP and HTTPS

Use the original destination URL and opt into the proxy on each request:

```ts
const response = await fetch(serviceURL, {
  proxy: bridge.httpProxyURL(),
  headers: { Authorization: `Bearer ${applicationToken}` },
  redirect: "error",
  signal: AbortSignal.timeout(30_000),
});
```

Requests that omit `proxy` keep their existing network behavior. The bridge does
not replace global fetch or set `HTTP_PROXY`/`HTTPS_PROXY`.

HTTPS is tunneled with CONNECT. Bun verifies the destination's certificate and
hostname; the helper does not decrypt the connection. For a private CA, pass
`tls: { ca: trustedCertificate }` to fetch instead of disabling verification.

Both request and response streaming work on Bun 1.4.2, including SSE. Consume or
cancel the response body before closing the bridge. Choose deadlines appropriate
for long-lived streams: an `AbortSignal.timeout` applies to the entire request,
not just connection setup.

The helper returns redirects rather than following them. Your fetch options
control what happens next; `redirect: "error"` avoids unexpected destinations.
Keep application credentials scoped to services you trust.

## SOCKS

For an existing SOCKS-capable client, use:

```ts
const { host, port, username, password } = bridge.proxy();
```

Configure proxy-side hostname resolution, often called `socks5h` or remote DNS,
so names reach Tailscale rather than being resolved by the client. For native Bun
fetch, use the HTTP proxy instead. SOCKS and HTTP have separate ports and passwords.

## Shutdown and errors

`createBridge()` resolves when the node and proxies are ready. It does not check
whether each service is reachable. Create one bridge per plugin lifetime rather
than one per request, and call `await bridge.close()` during shutdown.

Closing is idempotent and terminates active proxy connections. Parent exit also
closes the helper's control pipe and stops it. After shutdown or helper failure,
both proxy accessors throw. Discard cached proxy credentials; ports can be reused
by another process. The library does not restart a failed helper automatically.

`BridgeError.code` identifies lifecycle failures: `INVALID_OPTIONS`,
`UNSUPPORTED_RUNTIME`, `UNSUPPORTED_PLATFORM`, `HELPER_UNAVAILABLE`, `AUTH_REQUIRED`, `AUTH_FAILED`,
`CALLBACK_FAILED`, `STATE_UNSAFE`, `STATE_LOCKED`, `STARTUP_TIMEOUT`, `CANCELLED`,
`PROTOCOL_ERROR`, `HELPER_FAILED`, and `CLOSED`. Report these codes rather than
secrets or raw worker diagnostics. HTTP proxy connection failures can return
502/504; failed HTTPS tunnels can surface as fetch errors. Avoid logging raw
client exceptions, which may contain the credential-bearing proxy URL.

Creation rejects runtimes other than Bun 1.4.2 with `UNSUPPORTED_RUNTIME` before
starting a helper. Import alone remains side-effect free, including in Node.
An explicit `helperPath` is trusted executable code, intended only for development.

Closing a persistent bridge keeps its identity. Ephemeral state is removed on
normal shutdown, but a crash may leave temporary files. Removing local state does
not revoke a node or key in Tailscale; manage revocation separately.

## Security and limits

- Never log or persist `httpProxyURL()` or SOCKS credentials. They grant access to
  the local proxy. Loopback authentication does not protect against a compromised
  process running as the same user.
- This is not a tailnet-only firewall. Non-tailnet names and addresses can use
  upstream Tailscale's host DNS and direct-network behavior. Restrict destinations
  in your application if needed.
- HTTP forwarding discards trailers. Go can consume response `Connection: close`
  metadata before the proxy sees it, so extra headers nominated there can survive.
  Proxy credentials and standard hop headers are still removed. These restrictions
  do not apply to opaque application bytes inside CONNECT.
- Tailscale still needs outbound access for coordination, relays, and peers. Its
  own telemetry behavior is separate from the bridge's sanitized diagnostics.
- The host must allow subprocesses, private state files, outbound networking, and
  loopback connections. A hosted agent or browser sandbox may not permit these.

See the [helper reference](../helper/README.md) for protocol-level limits and
[testing guide](validation.md) for platform coverage and live validation.
