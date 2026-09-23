# Go Proxy Helper

The helper runs an independent userspace Tailscale node and exposes authenticated
loopback SOCKS and HTTP/CONNECT proxies. It needs no system Tailscale install,
root, or TUN. The module pins **Go 1.26.2** and **Tailscale 1.96.5**.

SOCKS uses `tsnet.Server.Up` and `Loopback`; HTTP uses `Server.Dial`, Go's
`ReverseProxy`/`Transport`, and Tailscale's `connectproxy.Handler`.
For the Bun API, see [usage](../docs/usage.md).

## Build And Test

From the repository root, with Go 1.26.2 on `PATH` and Bun 1.4.2 selected by mise:

```sh
mise exec -- bun install --frozen-lockfile
mise exec -- bun run build
mise exec -- bun run test:go
CGO_ENABLED=1 mise exec -- go -C helper test -tags=ts_omit_webclient -race -count=1 ./...
mise exec -- go -C helper vet -tags=ts_omit_webclient ./...
```

`build` produces the native `bin/bridge-<platform>-<arch>` plus the Bun bundle;
`build:all` produces all four targets. Production builds use `CGO_ENABLED=0`
and `ts_omit_webclient` to exclude the unused web UI. Race tests need a C compiler.
Only ship executables built from `helper/cmd/bridge`. See [Testing](../docs/validation.md)
for package and live checks.

## Private Protocol

Use inherited private stdin/stdout pipes, not command-line credentials. Each
frame is one newline-terminated JSON object, at most 65,536 UTF-8 bytes excluding
the newline. Unknown fields/types, duplicate keys, missing fields, invalid
values/versions, and unfinished frames fail. Defaults belong to the parent.

Start with exactly one state mode:

```json
{"type":"start","version":1,"options":{"hostname":"my-bridge","ephemeral":true,"startupTimeoutMs":60000,"interactive":false}}
```

| Option | Constraint |
| --- | --- |
| `hostname` | Required, 1-63 character DNS label. |
| `ephemeral` | Required boolean. If true, omit `stateDir`. |
| `stateDir` | Required absolute path when `ephemeral:false`. |
| `startupTimeoutMs` | Required integer, 1 through 2,147,483,647. |
| `interactive` | Required boolean; enrollment prompts require true. |
| `authKey` | Optional explicit key through the pipe; omit rather than send empty. No surrounding whitespace, CR/LF, or NUL. |

`origins` and `requestTimeoutMs` are not accepted. Once the node and both
listeners are ready, the helper returns:

```json
{"type":"ready","version":1,"proxy":{"host":"127.0.0.1","port":12345,"username":"tsnet","password":"00000000000000000000000000000000"},"httpProxy":{"host":"127.0.0.1","port":12346,"username":"tsnet","password":"11111111111111111111111111111111"}}
```

These passwords are synthetic examples. Real SOCKS and HTTP passwords are
independent 128-bit random secrets encoded as 32 lowercase hex characters.
Never log descriptors or proxy URLs. The separate LocalAPI password is discarded,
not returned; the built-in SOCKS listener also multiplexes LocalAPI, which needs
that separate password and `Sec-Tailscale: localapi`.

Interactive enrollment uses structured LocalClient notifications, not log parsing:

```json
{"type":"auth_required","version":1,"url":"https://login.tailscale.com/a/synthetic"}
```

Failures expose only allowlisted codes, never raw upstream errors:

```json
{"type":"error","version":1,"code":"AUTH_REQUIRED"}
```

Close with `{"type":"close","version":1}`, stdin EOF, SIGINT, SIGTERM, or SIGHUP.
A second start or invalid later frame fails the instance. Normal disposal exits
zero; failures exit nonzero. There are no progress/closed frames or automatic restarts.

## State And Lifecycle

Persistent enrollment is reused; supplying a key does not force re-enrollment.
A new identity without a key or interactive opt-in fails with `AUTH_REQUIRED`
when backend state requires login. Readiness does not prove destination access.

Persistent directories must belong to the effective user with mode `0700`;
regular state files require the same owner and mode `0600`. Symlinks inside
state or at the final directory component, hardlinks, special files, and unsafe
parent permissions fail. Trusted parent aliases such as macOS `/var` are resolved;
ancestors must belong to root or this user and not be writable by others, except
sticky directories such as `/tmp`.

`.bridge.lock` holds a nonblocking exclusive `flock`; persistent close never
removes or replaces it. Use a local filesystem with working Unix advisory locks.
Persistent close preserves identity and does not log out. Ephemeral mode uses a
private temporary directory removed on normal close/cooperative startup failure;
caller-owned directories are never recursively deleted. Tailscale controls
remote ephemeral-node deletion.

Startup is bounded even if upstream initialization stalls. Cleanup waits at most
four seconds before process exit; an initializing node's directory is not unlocked
or deleted. Forced exits/crashes can leave temporary state. HTTP close cancels
dials, streams, and CONNECT tunnels before tsnet closes; process exit is the final
boundary for all SOCKS tunnels, including host-routed connections.

The parent must filter `TS_*`, `TSNET_*`, and `TAILSCALE_*` before spawning and
avoid inheriting secrets. The helper clears these settings and cached knobs too,
but dependency initialization happens before `main`. After entering `main`, raw
stdout/stderr and standard logs go to the null device; only the private protocol
writer remains. tsnet callbacks are silent, but upstream telemetry and private
state logs are not guaranteed absent.

## Routing And HTTP Limits

Both proxies retain upstream `UserDial` behavior: known tailnet destinations use
userspace Tailscale routing/policy; other destinations can use host DNS and direct
networking. **This is not a tailnet-only egress firewall.** Clients own destination
selection, TLS verification, application credentials, redirects, retries, timeouts,
and cancellation. SOCKS clients should forward hostnames (`socks5h`). Native Bun
1.4.2 fetch uses the separate HTTP proxy:

```ts
await fetch(remoteURL, { proxy: bridge.httpProxyURL() });
```

The URL stays unchanged; no global proxy configuration is needed. The HTTP proxy
URL is `http://tsnet:<http-password>@127.0.0.1:<http-port>`. Every request, including
reused connections and CONNECT, needs HTTP proxy Basic authentication. Invalid
credentials get 407 before dialing. SOCKS/LocalAPI passwords do not work here.

| Behavior | Limit |
| --- | --- |
| Plain HTTP | Absolute-form `http://host[:port]/path?query` only. |
| CONNECT | Valid `host:port`, no body; opaque tunnel without TLS termination. |
| Rejected requests | Origin-form/LocalAPI, URL credentials/fragments, browser Origin/Fetch Metadata headers, HTTP upgrades. |
| Headers | Application Authorization/cookies and remote Host preserved; proxy credentials, standard hop-by-hop and forwarding headers removed, including informational responses. |
| Trailers | All discarded; trailer-dependent protocols unsupported. |
| Streaming | Incremental; no total duration limit for healthy streams. |
| Redirects/replay | Redirects returned, not followed; upstream reuse disabled to prevent Go transport replay. |
| Timeouts | HTTP dial/response headers: 30 seconds; CONNECT dial: 15 seconds. Incoming headers: 15 seconds; idle connection: 30 seconds. |

Go 1.26 consumes `Connection` metadata containing `close` before proxy hooks see
it. Arbitrary response headers nominated alongside `close` can therefore survive.
Known proxy credentials and standard hop headers are still removed; arbitrary
nominated headers are removed when that metadata survives parsing. This is not
complete arbitrary response-hop filtering. CONNECT traffic remains opaque.

## Test-Only Executable

From the repository root:

```sh
GOTOOLCHAIN=local mise exec -- go -C helper build -tags=ts_omit_webclient -o bin/bridge-test-helper ./internal/testhelper
```

Never ship this executable. Set a development `helperPath` to the absolute path
of `helper/bin/bridge-test-helper`. It uses the production protocol/lifecycle,
real upstream SOCKS server, and production HTTP listener with a simulated node;
it never starts tsnet or enrolls.

The simulated dialer accepts numeric loopback, `localhost`, and `upstream.test`.
The names map to `127.0.0.1` without changing the port. Bind a local fixture on
`127.0.0.1:0`, then request `http://upstream.test:<port>` through HTTP or
`upstream.test:<port>` through SOCKS to test remote-name forwarding. Other names
and non-loopback IPs fail. Production has no flag/environment switch for this dialer.
Shared protocol fixtures live in [`fixtures/protocol.json`](../fixtures/protocol.json).
