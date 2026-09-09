import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { connect, isIP } from "node:net";
import assert from "node:assert/strict";

if (process.env.BRIDGE_LIVE !== "1") {
  console.log("SKIP live tailnet: set BRIDGE_LIVE=1 and follow docs/validation.md; no enrollment attempted");
} else if (!process.env.BRIDGE_LIVE_AUTHKEY) {
  console.log("SKIP live tailnet: BRIDGE_LIVE_AUTHKEY is required; no enrollment attempted");
} else {
  let directory: string | undefined;
  try {
    assert(process.getuid?.() !== 0, "Live verification must run as non-root");
    assert.equal(process.env.BRIDGE_LIVE_DAEMONLESS, "1", "Confirm isolated no-daemon/no-TUN environment");
    const origins = {
      dns: process.env.BRIDGE_LIVE_DNS_ORIGIN!,
      ip: process.env.BRIDGE_LIVE_IP_ORIGIN!,
      denied: process.env.BRIDGE_LIVE_DENIED_ORIGIN!,
    };
    for (const origin of Object.values(origins)) {
      assert(origin, "Set all three BRIDGE_LIVE_*_ORIGIN variables");
      const url = new URL(origin);
      assert(["http:", "https:"].includes(url.protocol) && !url.username && !url.password && url.href === `${url.origin}/`, "Use credential-free HTTP/HTTPS origins");
    }
    assert.equal(isIP(new URL(origins.dns).hostname), 0, "DNS target must be a MagicDNS name");
    assert(isIP(new URL(origins.ip).hostname.replace(/^\[|\]$/g, "")), "IP target must be a literal tailnet IP");
    // An ordinary public destination is not a denial fixture: tsnet can dial it directly.
    const deniedURL = new URL(origins.denied);
    const deniedHost = deniedURL.hostname.replace(/^\[|\]$/g, "");
    const octets = deniedHost.split(".").map(Number);
    assert(isIP(deniedHost) === 4 && octets[0] === 100 && octets[1]! >= 64 && octets[1]! <= 127 ||
      isIP(deniedHost) === 6 && deniedHost.startsWith("fd7a:115c:a1e0:"), "Use a known Tailscale IP for the denied fixture");
    assert.equal(deniedURL.protocol, "http:", "Use an HTTP denial fixture so TLS errors cannot masquerade as policy denial");
    const tls = process.env.BRIDGE_LIVE_CA_FILE ? {
      ca: await Bun.file(process.env.BRIDGE_LIVE_CA_FILE).text(), rejectUnauthorized: true,
    } : undefined;
    const { createBridge }: typeof import("../src/index") = await import(new URL("../dist/index.js", import.meta.url).href);
    directory = await mkdtemp(join(tmpdir(), "bridge-live-"));
    const options = { hostname: "bun-bridge-live", stateDir: join(directory, "state"), startupTimeoutMs: 60_000 };
    const bridge = await createBridge({ ...options, authKey: process.env.BRIDGE_LIVE_AUTHKEY });
    const listeners: { host: string; port: number }[] = [];
    try {
      const socks = bridge.proxy();
      const http = new URL(bridge.httpProxyURL());
      listeners.push({ host: socks.host, port: socks.port }, { host: http.hostname, port: Number(http.port) });
      for (const target of [origins.dns, origins.ip]) {
        const response = await fetch(new URL("/health", target), {
          proxy: bridge.httpProxyURL(), redirect: "error", tls, signal: AbortSignal.timeout(10_000),
        });
        try { assert.equal(response.status, 200, "Permitted health request failed"); }
        finally { await response.body?.cancel(); }
      }
      const started = performance.now();
      const stream = await fetch(new URL("/events", origins.dns), {
        proxy: bridge.httpProxyURL(), redirect: "error", tls, signal: AbortSignal.timeout(15_000),
      });
      assert(stream.body, "Missing SSE body");
      const reader = stream.body.getReader();
      try {
        assert.equal(stream.status, 200);
        assert(stream.headers.get("content-type")?.startsWith("text/event-stream"));
        let text = "";
        const decoder = new TextDecoder();
        while (!text.replace(/\r\n/g, "\n").includes("\n\n")) {
          const chunk = await reader.read();
          assert(!chunk.done, "Stream ended before first SSE event");
          text += decoder.decode(chunk.value, { stream: true });
          assert(text.length < 65_536, "SSE fixture first event too large");
        }
        assert(performance.now() - started < 3_000, "First event was buffered; fixture must stay open for at least 5 seconds");
      } finally { await reader.cancel(); reader.releaseLock(); }
      let denied = false;
      try {
        const response = await fetch(new URL("/health", origins.denied), {
          proxy: bridge.httpProxyURL(), redirect: "manual", signal: AbortSignal.timeout(35_000),
        });
        denied = [502, 504].includes(response.status);
        await response.body?.cancel();
      } catch { denied = true; }
      assert(denied, "Denied tailnet target did not fail at the proxy/connection layer");
    } finally {
      await bridge.close();
      await bridge.close();
    }
    assert.throws(() => bridge.httpProxyURL());
    assert.throws(() => bridge.proxy());
    // Check listener closure without sending stale credentials to a potentially reused port.
    for (const listener of listeners) {
      await new Promise<void>((resolve, reject) => {
        const socket = connect(listener);
        socket.setTimeout(1_000);
        socket.once("error", (error: NodeJS.ErrnoException) => error.code === "ECONNREFUSED" ? resolve() : reject(new Error("Could not verify listener closure")));
        socket.once("connect", () => { socket.destroy(); reject(new Error("Proxy listener remained open")); });
        socket.once("timeout", () => { socket.destroy(); reject(new Error("Listener closure check timed out")); });
      });
    }
    const reused = await createBridge(options);
    try {
      const response = await fetch(new URL("/health", origins.dns), {
        proxy: reused.httpProxyURL(), redirect: "error", tls, signal: AbortSignal.timeout(10_000),
      });
      try { assert.equal(response.status, 200, "Persisted identity reuse failed"); }
      finally { await response.body?.cancel(); }
    } finally { await reused.close(); }
    console.log("PASS live: native Bun HTTP proxy MagicDNS/IP requests, SSE/cancellation, denied tailnet connection, closed HTTP/SOCKS listeners, persisted reuse");
    console.log("Operator must confirm policy/service logs, same node identity, process/temp cleanup, and revoke disposable node/key; this is not an egress firewall test");
  } catch {
    // Native client errors can contain proxy URLs; never print them or upstream bodies.
    throw new Error("Live verification failed; check docs/validation.md fixture, policy, and environment privately");
  } finally {
    if (directory) await rm(directory, { recursive: true, force: true });
  }
}
