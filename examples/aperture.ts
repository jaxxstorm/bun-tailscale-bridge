import { createBridge } from "../src/index";

// Installed consumers import createBridge from "@jaxxstorm/bun-tailscale-bridge".
// Native Bun fetch uses the explicit HTTP proxy, not the SOCKS listener.
const remote = process.env.APERTURE_ORIGIN;
const authKey = process.env.BRIDGE_EXAMPLE_AUTHKEY;
if (!remote || !authKey) throw new Error("Set APERTURE_ORIGIN and BRIDGE_EXAMPLE_AUTHKEY to run this example");
const base = new URL(remote);
if (!["http:", "https:"].includes(base.protocol) || base.username || base.password || base.href !== `${base.origin}/`) {
  throw new Error("APERTURE_ORIGIN must be an HTTP/HTTPS origin without credentials or a path");
}
// Explicitly running with an auth key enrolls a disposable node.
const bridge = await createBridge({ hostname: "aperture-example", ephemeral: true, authKey });
try {
  // Never log the credential-bearing proxy URL; it authenticates the proxy, not Aperture.
  const headers = { Authorization: "Bearer synthetic-aperture-credential" };
  const providers = await fetch(new URL("/api/providers", base), {
    proxy: bridge.httpProxyURL(), redirect: "error",
    headers, signal: AbortSignal.timeout(10_000),
  });
  if (!providers.ok) {
    await providers.body?.cancel();
    throw new Error("Aperture discovery request failed");
  }
  await providers.json(); // Consumer owns discovery/model-selection policy.

  const response = await fetch(new URL("/codex/responses", base), {
    proxy: bridge.httpProxyURL(), redirect: "error",
    method: "POST", headers: { ...headers, "Content-Type": "application/json" },
    signal: AbortSignal.timeout(60_000),
    body: JSON.stringify({ model: "synthetic-model", input: "Hello", stream: true }),
  });
  if (!response.ok || !response.body) {
    await response.body?.cancel();
    throw new Error("Aperture response request failed");
  }
  const reader = response.body.getReader();
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      // Feed chunks into the consumer's SSE parser without logging content.
      void value;
    }
  } finally { await reader.cancel(); reader.releaseLock(); }
} catch {
  // Native errors may include credential-bearing proxy URLs.
  throw new Error("Aperture request failed; inspect service configuration privately");
} finally {
  await bridge.close(); // Keep the bridge alive through response consumption.
}
