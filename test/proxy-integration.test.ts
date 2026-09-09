import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { lookup } from "node:dns/promises";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import * as http from "node:http";
import * as https from "node:https";
import { connect } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createBridge, type Bridge } from "../src/index";

describe("native Bun fetch through real Go proxies (no enrollment)", () => {
  let directory: string;
  let helperPath: string;
  let ca: Buffer;
  let cert: Buffer;
  let key: Buffer;
  const originalFetch = globalThis.fetch;
  const bridges: Bridge[] = [];
  const servers: http.Server[] = [];
  const nativeServers: Bun.Server<undefined>[] = [];

  beforeAll(async () => {
    directory = await mkdtemp(join(tmpdir(), "bridge-proxy-integration-"));
    helperPath = join(directory, "bridge-test-helper");
    const build = Bun.spawn([
      "go", "-C", fileURLToPath(new URL("../helper", import.meta.url)),
      "build", "-tags=ts_omit_webclient", "-o", helperPath, "./internal/testhelper",
    ], { stdout: "pipe", stderr: "pipe" });
    const [exitCode, stdout, stderr] = await Promise.all([
      build.exited, new Response(build.stdout).text(), new Response(build.stderr).text(),
    ]);
    if (exitCode !== 0) throw new Error(`Test helper build failed: ${stdout}${stderr}`);

    // Disposable CA and SAN-bearing leaf, with normal consumer TLS validation.
    await writeFile(join(directory, "leaf.ext"), [
      "basicConstraints=critical,CA:FALSE", "keyUsage=critical,digitalSignature,keyEncipherment",
      "extendedKeyUsage=serverAuth", "subjectAltName=DNS:upstream.test",
    ].join("\n"));
    for (const args of [
      ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2", "-subj", "/CN=Bridge Test CA",
        "-addext", "basicConstraints=critical,CA:TRUE", "-keyout", "ca.key", "-out", "ca.pem"],
      ["req", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=upstream.test", "-keyout", "leaf.key", "-out", "leaf.csr"],
      ["x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial",
        "-days", "2", "-extfile", "leaf.ext", "-out", "leaf.pem"],
    ]) {
      const result = Bun.spawnSync(["openssl", ...args], { cwd: directory, stdout: "pipe", stderr: "pipe" });
      if (result.exitCode !== 0) throw new Error(`TLS fixture generation failed: ${result.stderr.toString()}`);
    }
    [ca, cert, key] = await Promise.all(["ca.pem", "leaf.pem", "leaf.key"].map(name => readFile(join(directory, name)))) as [Buffer, Buffer, Buffer];
  }, 180_000);

  afterEach(async () => {
    await Promise.all(bridges.splice(0).map(bridge => bridge.close()));
    await Promise.all(nativeServers.splice(0).map(server => server.stop(true)));
    await Promise.all(servers.splice(0).map(server => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve());
      server.closeAllConnections();
    })));
  });

  afterAll(async () => {
    if (directory) await rm(directory, { recursive: true, force: true });
  });

  async function start() {
    const bridge = await createBridge({ hostname: "proxy-integration", ephemeral: true, helperPath });
    bridges.push(bridge);
    return bridge;
  }

  async function listen(handler: http.RequestListener, tls = false) {
    const server = tls ? https.createServer({ key, cert }, handler) : http.createServer(handler);
    servers.push(server);
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("Expected a TCP listener");
    return new URL(`${tls ? "https" : "http"}://upstream.test:${address.port}`);
  }

  async function event(reader: ReadableStreamDefaultReader<Uint8Array>) {
    let text = "";
    while (!text.endsWith("\n\n")) {
      const chunk = await reader.read();
      if (chunk.done) throw new Error("SSE ended before the event was complete");
      text += new TextDecoder().decode(chunk.value);
    }
    return text;
  }

  function listenNative(handler: (request: Request) => Response | Promise<Response>, tls = false) {
    const server = Bun.serve({
      hostname: "127.0.0.1", port: 0, idleTimeout: 0,
      ...(tls ? { tls: { key, cert } } : {}), fetch: handler,
    });
    nativeServers.push(server);
    return new URL(`${tls ? "https" : "http"}://upstream.test:${server.port}`);
  }

  for (const tls of [false, true]) {
    const protocol = tls ? "HTTPS CONNECT" : "HTTP";

    test(`${protocol} leaves redirect policy to the fetch caller`, async () => {
      let finalRequests = 0;
      const url = await listen((req, res) => {
        if (req.url === "/final") { finalRequests++; res.end("final"); }
        else { res.writeHead(302, { Location: "/final" }); res.end(); }
      }, tls);
      const bridge = await start();
      const options = { proxy: bridge.httpProxyURL(), tls: { ca }, signal: AbortSignal.timeout(3000) };
      const manual = await fetch(url, { ...options, redirect: "manual" });
      expect(manual.status).toBe(302);
      expect(manual.headers.get("location")).toBe("/final");
      await manual.body?.cancel();
      expect(finalRequests).toBe(0);
      const followed = await fetch(url, { ...options, redirect: "follow" });
      expect(await followed.text()).toBe("final");
      expect(finalRequests).toBe(1);
    });

    test(`${protocol} discovery POST forwards hostname and application auth, not proxy credentials`, async () => {
      const payload = JSON.stringify({ jsonrpc: "2.0", id: 7, method: "tools/list" });
      const observed = Promise.withResolvers<{ method?: string; path?: string; headers: http.IncomingHttpHeaders; body: string }>();
      const url = await listen((req, res) => {
        void (async () => {
          let body = "";
          for await (const chunk of req) body += chunk.toString();
          observed.resolve({ method: req.method, path: req.url, headers: req.headers, body });
          res.writeHead(200, { "content-type": "application/json", "x-discovery": "yes" });
          res.end(JSON.stringify({ jsonrpc: "2.0", id: 7, result: { tools: [] } }));
        })().catch(observed.reject);
      }, tls);
      url.pathname = "/mcp";
      url.search = "?discovery=1";
      const bridge = await start();
      // The simulator alone resolves this name, with the requested port unchanged.
      await expect(lookup(url.hostname)).rejects.toMatchObject({ code: "ENOTFOUND" });
      const response = await fetch(url, {
        proxy: bridge.httpProxyURL(), tls: { ca }, method: "POST", body: payload,
        headers: { "content-type": "application/json", authorization: "Bearer application-only" },
        signal: AbortSignal.timeout(5_000),
      });
      expect(response.status).toBe(200);
      expect(response.headers.get("x-discovery")).toBe("yes");
      expect(await response.json()).toEqual({ jsonrpc: "2.0", id: 7, result: { tools: [] } });
      const request = await observed.promise;
      expect(request).toMatchObject({ method: "POST", path: "/mcp?discovery=1", body: payload });
      expect(request.headers.host).toBe(url.host);
      expect(request.headers.authorization).toBe("Bearer application-only");
      expect(request.headers["proxy-authorization"]).toBeUndefined();
      const proxy = new URL(bridge.httpProxyURL());
      const rawHeaders = JSON.stringify(request.headers);
      expect(rawHeaders.includes(proxy.password)).toBe(false);
      expect(rawHeaders.includes(Buffer.from(`${proxy.username}:${proxy.password}`).toString("base64"))).toBe(false);
    });

    // Regression for oven-sh/bun#33918: streamed HTTPS uploads stalled on 1.3.14.
    test(`${protocol} concurrent multi-megabyte streaming uploads preserve all bytes`, async () => {
      const allStarted = Promise.withResolvers<void>();
      let active = 0;
      let received = 0;
      const url = listenNative(async req => {
        if (++active === 4) allStarted.resolve();
        await allStarted.promise;
        const hash = createHash("sha256");
        let size = 0;
        const reader = req.body!.getReader();
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          hash.update(value);
          size += value.length;
          received += value.length;
        }
        reader.releaseLock();
        return Response.json({ size, hash: hash.digest("hex"), encoding: req.headers.get("transfer-encoding") });
      }, tls);
      const bridge = await start();
      await Promise.all(Array.from({ length: 4 }, async (_, index) => {
        const chunk = Buffer.alloc(32 * 1024, index + 1);
        const hash = createHash("sha256");
        for (let i = 0; i < 96; i++) hash.update(chunk);
        let sent = 0;
        const body = new ReadableStream<Uint8Array>({
          pull(controller) {
            if (sent++ < 96) controller.enqueue(chunk);
            else controller.close();
          },
        });
        const response = await fetch(url, {
          proxy: bridge.httpProxyURL(), tls: { ca }, method: "POST", body,
          signal: AbortSignal.timeout(15_000),
        });
        expect(response.status).toBe(200);
        expect(await response.json()).toEqual({
          size: 3 * 1024 * 1024, hash: hash.digest("hex"), encoding: "chunked",
        });
      })).catch(error => { throw new Error(`Upload failure: ${active}/4 requests started, ${received} bytes received`, { cause: error }); });
      expect(active).toBe(4);
    }, 20_000);

    for (const auth of ["wrong", "missing"] as const) {
      test(`${protocol} rejects ${auth} HTTP proxy credentials before reaching upstream`, async () => {
        let reached = false;
        const url = await listen((_req, res) => { reached = true; res.end("unexpected"); }, tls);
        const proxy = new URL((await start()).httpProxyURL());
        if (auth === "wrong") proxy.password += "wrong";
        else { proxy.username = ""; proxy.password = ""; }
        const outcome = await fetch(url, {
          proxy: proxy.href, tls: { ca }, signal: AbortSignal.timeout(5_000),
        }).then(async response => {
          await response.arrayBuffer();
          return { status: response.status };
        }, error => ({ error: error as Error }));
        if ("status" in outcome) expect(outcome.status).toBe(407);
        else expect(outcome.error.message).toMatch(/proxy|407/i);
        expect(reached).toBe(false);
      });
    }
  }

  for (const tls of [false, true]) {
    test(`${tls ? "HTTPS CONNECT" : "HTTP"} fixed JSON POST receives incremental SSE before upstream completion`, async () => {
      const second = Promise.withResolvers<void>();
      const finish = Promise.withResolvers<void>();
      const observed = Promise.withResolvers<{ method: string; body: string }>();
      const payload = JSON.stringify({ input: "hello", stream: true });
      const url = listenNative(async req => {
        observed.resolve({ method: req.method, body: await req.text() });
        return new Response(new ReadableStream({
          start(controller) {
            controller.enqueue(new TextEncoder().encode("data: first\n\n"));
            void second.promise.then(async () => {
              controller.enqueue(new TextEncoder().encode("data: second\n\n"));
              await finish.promise;
              controller.close();
            });
          },
        }), { headers: { "content-type": "text/event-stream" } });
      }, tls);
      try {
        const response = await fetch(url, {
          proxy: (await start()).httpProxyURL(), tls: { ca },
          method: "POST", body: payload, headers: { "content-type": "application/json" },
          signal: AbortSignal.timeout(10_000),
        });
        expect(response.status).toBe(200);
        expect(response.headers.get("content-type")).toBe("text/event-stream");
        const reader = response.body!.getReader();
        expect(await event(reader)).toBe("data: first\n\n");
        second.resolve();
        expect(await event(reader)).toBe("data: second\n\n");
        finish.resolve();
        expect((await reader.read()).done).toBe(true);
        expect(await observed.promise).toEqual({ method: "POST", body: payload });
        reader.releaseLock();
      } finally { second.resolve(); finish.resolve(); }
    }, 15_000);
  }

  test("HTTPS CONNECT streams the first upload chunk before the producer can finish", async () => {
    const first = Promise.withResolvers<void>();
    const finish = Promise.withResolvers<void>();
    const chunk = Buffer.alloc(32 * 1024, 7);
    let completed = false;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("Streaming upload handshake timed out")), 5_000);
    });
    const url = listenNative(async req => {
      const reader = req.body!.getReader();
      let size = 0;
      try {
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          size += value.length;
          if (size === chunk.length) first.resolve();
        }
        return Response.json({ size, encoding: req.headers.get("transfer-encoding"), length: req.headers.get("content-length") });
      } finally { reader.releaseLock(); }
    }, true);
    let sent = false;
    const body = new ReadableStream<Uint8Array>({
      async pull(stream) {
        if (!sent) { sent = true; stream.enqueue(chunk); return; }
        await finish.promise;
        if (controller.signal.aborted) return;
        stream.enqueue(chunk);
        completed = true;
        stream.close();
      },
    });
    try {
      const response = fetch(url, {
        proxy: (await start()).httpProxyURL(), tls: { ca }, method: "POST", body,
        signal: controller.signal,
      }).then(async response => ({ status: response.status, body: await response.json() }));
      // Observe failures immediately while the producer is held behind the gate.
      void response.catch(first.reject);
      await Promise.race([first.promise, deadline]);
      expect(completed).toBe(false);
      finish.resolve();
      expect(await Promise.race([response, deadline])).toEqual({
        status: 200, body: { size: 2 * chunk.length, encoding: "chunked", length: null },
      });
      expect(completed).toBe(true);
    } finally { clearTimeout(timer!); controller.abort(); finish.resolve(); }
  }, 10_000);

  test("aborting a CONNECT upload closes upstream and cancels the pending producer", async () => {
    const first = Promise.withResolvers<void>();
    const closed = Promise.withResolvers<void>();
    const drained = Promise.withResolvers<void>();
    const finish = Promise.withResolvers<void>();
    const producing = Promise.withResolvers<void>();
    const produced = Promise.withResolvers<void>();
    const cancelled = Promise.withResolvers<void>();
    let stopped = false;
    let sent = 0;
    let received = 0;
    const chunk = Buffer.alloc(32 * 1024, 9);
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error("Upload cancellation timed out")), 5_000);
    });
    const url = listenNative(async req => {
      req.signal.addEventListener("abort", () => closed.resolve(), { once: true });
      const reader = req.body!.getReader();
      try {
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          received += value.length;
          if (received === chunk.length) first.resolve();
        }
      } catch (error) {
        if (!req.signal.aborted) first.reject(error);
      } finally { reader.releaseLock(); drained.resolve(); }
      return new Response("closed");
    }, true);
    const body = new ReadableStream<Uint8Array>({
      async pull(stream) {
        if (sent === 0) { sent++; stream.enqueue(chunk); return; }
        producing.resolve();
        await finish.promise;
        try {
          if (!stopped) { sent++; stream.enqueue(chunk); stream.close(); }
        } finally { produced.resolve(); }
      },
      cancel() { stopped = true; cancelled.resolve(); },
    });
    try {
      const outcome = fetch(url, {
        proxy: (await start()).httpProxyURL(), tls: { ca }, method: "POST", body,
        signal: controller.signal,
      }).then(async response => ({ body: await response.text() }), error => ({ error }));
      await Promise.race([Promise.all([first.promise, producing.promise]), deadline]);
      controller.abort();
      expect(await Promise.race([outcome, deadline])).toMatchObject({ error: { name: "AbortError" } });
      await Promise.race([Promise.all([closed.promise, drained.promise, cancelled.promise]), deadline]);
      finish.resolve();
      await Promise.race([produced.promise, deadline]);
      expect(sent).toBe(1);
      expect(received).toBe(chunk.length);
    } finally { clearTimeout(timer!); controller.abort(); stopped = true; finish.resolve(); }
  }, 10_000);

  test("aborting a CONNECT SSE fetch closes the upstream stream", async () => {
    const closed = Promise.withResolvers<void>();
    const url = listenNative(req => {
      req.signal.addEventListener("abort", () => closed.resolve(), { once: true });
      return new Response(new ReadableStream({
        start(controller) { controller.enqueue(new TextEncoder().encode("data: open\n\n")); },
      }), { headers: { "content-type": "text/event-stream" } });
    }, true);
    const controller = new AbortController();
    try {
      const response = await fetch(url, {
        proxy: (await start()).httpProxyURL(), tls: { ca },
        signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10_000)]),
      });
      const reader = response.body!.getReader();
      expect(await event(reader)).toBe("data: open\n\n");
      controller.abort();
      await expect(reader.read()).rejects.toMatchObject({ name: "AbortError" });
      await closed.promise;
      reader.releaseLock();
    } finally { controller.abort(); }
  }, 15_000);

  for (const invalid of ["untrusted", "hostname mismatch"] as const) {
    test(`HTTPS CONNECT rejects ${invalid} certificates`, async () => {
      let reached = false;
      const url = await listen((_req, res) => { reached = true; res.end(); }, true);
      if (invalid === "hostname mismatch") url.hostname = "localhost";
      await expect(fetch(url, {
        proxy: (await start()).httpProxyURL(),
        ...(invalid === "hostname mismatch" ? { tls: { ca } } : {}),
        signal: AbortSignal.timeout(5_000),
      })).rejects.toThrow(/certificate|issuer|verify|hostname|altname/i);
      expect(reached).toBe(false);
    });
  }

  test("helper close interrupts an active CONNECT stream and refuses new connections", async () => {
    const url = await listen((_req, res) => {
      res.writeHead(200, { "content-type": "text/event-stream" });
      res.write("data: open\n\n");
    }, true);
    const bridge = await start();
    const proxy = bridge.httpProxyURL();
    const signal = AbortSignal.timeout(10_000);
    const response = await fetch(url, { proxy, tls: { ca }, signal });
    const reader = response.body!.getReader();
    expect(await event(reader)).toBe("data: open\n\n");
    const interrupted = reader.read().then(result => ({ result }), error => ({ error }));
    await bridge.close();
    const outcome = await interrupted;
    expect(outcome).toHaveProperty("error");
    expect(signal.aborted).toBe(false);
    reader.releaseLock();
    await expect(fetch(url, { proxy, tls: { ca }, signal: AbortSignal.timeout(5_000) })).rejects.toThrow();
    expect(() => bridge.httpProxyURL()).toThrow("Bridge is closed");
    await bridge.close();
  }, 15_000);

  test("unrelated direct fetch stays unchanged before, during, and after bridge use", async () => {
    const direct = await listen((req, res) => res.end(JSON.stringify({ proxyAuth: req.headers["proxy-authorization"] ?? null })));
    direct.hostname = "127.0.0.1";
    expect(await (await fetch(direct)).json()).toEqual({ proxyAuth: null });
    const bridge = await start();
    const proxied = await listen((_req, res) => res.end("proxied"));
    expect(await (await fetch(proxied, { proxy: bridge.httpProxyURL() })).text()).toBe("proxied");
    expect(globalThis.fetch).toBe(originalFetch);
    expect(await (await fetch(direct)).json()).toEqual({ proxyAuth: null });
    await bridge.close();
    expect(globalThis.fetch).toBe(originalFetch);
    expect(await (await fetch(direct)).json()).toEqual({ proxyAuth: null });
  });

  test("SOCKS still negotiates authenticated TCP with independent credentials", async () => {
    const bridge = await start();
    const proxy = bridge.proxy();
    const httpProxy = new URL(bridge.httpProxyURL());
    expect(proxy.port).not.toBe(Number(httpProxy.port));
    expect(proxy.password === httpProxy.password).toBe(false);
    for (const auth of ["valid", "wrong", "missing"] as const) {
      const socket = connect(proxy.port, proxy.host);
      socket.setTimeout(5_000, () => socket.destroy(new Error("SOCKS handshake timed out")));
      try {
        await once(socket, "connect");
        const chunks = socket[Symbol.asyncIterator]();
        let pending = Buffer.alloc(0);
        async function reply() {
          while (pending.length < 2) {
            const chunk = await chunks.next();
            if (chunk.done) throw new Error("SOCKS closed before replying");
            pending = Buffer.concat([pending, chunk.value]);
          }
          const result = [...pending.subarray(0, 2)];
          pending = pending.subarray(2);
          return result;
        }
        socket.write(Buffer.from([5, 1, auth === "missing" ? 0 : 2]));
        expect(await reply()).toEqual([5, auth === "missing" ? 255 : 2]);
        if (auth !== "missing") {
          const username = Buffer.from(proxy.username);
          const password = Buffer.from(auth === "valid" ? proxy.password : httpProxy.password);
          socket.write(Buffer.concat([Buffer.from([1, username.length]), username, Buffer.from([password.length]), password]));
          expect(await reply()).toEqual([1, auth === "valid" ? 0 : 1]);
        }
      } finally { socket.destroy(); }
    }
  });
});
