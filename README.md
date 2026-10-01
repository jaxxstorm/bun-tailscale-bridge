# bun-tailscale-bridge

Connect to private Tailscale services from Bun without installing or running
Tailscale on the host.

This library starts a userspace Tailscale node and gives your application a local
HTTP or SOCKS5 proxy. Use it with Bun's built-in `fetch` to reach services on your
tailnet. No root access, VPN interface, or system daemon is needed.

## Why use it?

A coding-agent plugin might need to call an internal API, reach a development
service, or reach an Aperture gateway from
[opencode-aperture](https://github.com/jaxxstorm/opencode-aperture).
Requiring users to install and configure a machine-wide VPN makes that
integration harder to set up, especially in containers and restricted environments.

With this bridge, the application manages its own Tailscale connection. It chooses
which requests use the proxy; other network traffic stays as it was. The new node
still needs permission to reach those services under your tailnet's access policy.

## Install

Use **Bun 1.4.2**, the only tested Bun version. Installing this npm package does
not supply an external `bun` executable; install Bun separately, including for
plugin workers. The tarball bundles helpers for macOS and Linux on arm64 and x64;
cross-builds are not proof of runtime coverage on all four platforms. Consumers
do not need Go or an installed Tailscale client.

The package has not been published to npm yet. To try it locally, build a tarball
from this checkout with Go 1.26.2 installed:

```sh
mise install
mise exec -- bun install --frozen-lockfile
mise exec -- bun run build:all
mise exec -- bun run pack
```

Then install it in your application:

```sh
bun add /absolute/path/to/jaxxstorm-bun-tailscale-bridge-0.1.0.tgz
```

After the first public **0.1.0** release, the intended install command is below.
It is not available yet:

```sh
bun add --exact @jaxxstorm/bun-tailscale-bridge@0.1.0
```

## Use it

Provide a Tailscale auth key through `BRIDGE_AUTHKEY`, then create a bridge and
pass its proxy URL to the requests that need tailnet access:

```ts
import { createBridge } from "@jaxxstorm/bun-tailscale-bridge";

const bridge = await createBridge({
  hostname: "my-agent",
  ephemeral: true,
  authKey: process.env.BRIDGE_AUTHKEY,
});

try {
  const response = await fetch("https://api.your-tailnet.ts.net/health", {
    proxy: bridge.httpProxyURL(),
    redirect: "error",
    signal: AbortSignal.timeout(10_000),
  });

  if (!response.ok) {
    await response.body?.cancel();
    throw new Error(`Request failed: HTTP ${response.status}`);
  }

  const health = await response.json();
} finally {
  await bridge.close();
}
```

In a long-running plugin, create the bridge once at startup and close it on
shutdown. Keep it alive while consuming streaming responses. HTTP, HTTPS,
streamed uploads, and SSE work through the HTTP proxy; clients with SOCKS support
can use `bridge.proxy()` instead.

For a persistent node, replace `ephemeral: true` with an absolute `stateDir`.
The bridge reuses that identity on subsequent starts, so an auth key is only
needed for enrollment. Interactive login is also available.

See [configuration and authentication](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/docs/usage.md)
or the [Aperture example](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/examples/aperture.ts) for more.
For the intended pinned optional dependency and external Bun worker setup, see
[opencode-aperture integration](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/docs/usage.md#opencode-aperture-integration).

## How it works

```text
Bun application -> local HTTP/SOCKS proxy -> userspace Tailscale node -> service
```

The TypeScript library launches a bundled Go executable built with Tailscale's
[`tsnet`](https://pkg.go.dev/tailscale.com/tsnet) package. Each bridge has its own
node identity and authenticated loopback proxies. HTTPS uses CONNECT, keeping
TLS between your application and the destination.

Treat proxy URLs and credentials as secrets. The proxy is not an egress firewall:
non-tailnet destinations can still use Tailscale's normal host-network routing.
Your application decides which destinations to request.

## Development

- [Testing and live-tailnet checks](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/docs/validation.md)
- [Go helper and control protocol](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/helper/README.md)
- [Publishing releases](https://github.com/jaxxstorm/bun-tailscale-bridge/blob/main/docs/releases.md)

## License

[MIT](LICENSE). Bundled dependencies retain their own licenses, listed in
[THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt).
