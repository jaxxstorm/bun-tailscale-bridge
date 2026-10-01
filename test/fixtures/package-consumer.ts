// Copied to a disposable consumer. Runtime imports only the installed tarball.
import assert from "node:assert/strict";
import { chmod, mkdir, readdir, rename } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const name = "@jaxxstorm/bun-tailscale-bridge";
const present = process.argv[2] === "present";
const home = process.env.HOME!;
const temporary = process.env.TMPDIR!;
const originalFetch = globalThis.fetch;
const spawn = Bun.spawn;
const spawnSync = Bun.spawnSync;
const children: Bun.Subprocess[] = [];
let importing = true;
// Catch helper/browser startup even if it would leave no filesystem trace.
Bun.spawn = ((...args: Parameters<typeof Bun.spawn>) => {
  assert(!importing, "Import must not spawn helpers or browsers");
  const child = spawn(...args);
  children.push(child);
  return child;
}) as typeof Bun.spawn;
Bun.spawnSync = (() => { throw new Error("Unexpected synchronous helper/browser startup"); }) as typeof Bun.spawnSync;
const before = [await readdir(home, { recursive: true }), await readdir(temporary, { recursive: true })];
const server = Bun.serve({ hostname: "127.0.0.1", port: 0, async fetch(req) {
  assert.equal(req.headers.get("proxy-authorization"), null);
  return Response.json({ body: await req.text(), auth: req.headers.get("authorization"), host: new URL(req.url).host });
} });
try {
  // Erased source-only typing; installed declarations are checked separately.
  let api: typeof import("../../src/index") | undefined;
  if (present) {
    assert(import.meta.resolve(name).startsWith(new URL("./node_modules/", import.meta.url).href));
    api = await import(name);
  } else {
    await assert.rejects(import(name), /Cannot find|resolve|not found/);
  }
  await Bun.sleep(50);
  assert.deepEqual([await readdir(home, { recursive: true }), await readdir(temporary, { recursive: true })], before, "Import created state");
  assert.equal(globalThis.fetch, originalFetch);
  importing = false;
  const direct = async () => {
    const response = await fetch(`http://127.0.0.1:${server.port}`, { signal: AbortSignal.timeout(3000) });
    assert.equal((await response.json()).body, "");
  };
  await direct();
  if (api) {
    const { createBridge, BridgeError } = api;
    assert.equal(new BridgeError("PROTOCOL_ERROR").code, "PROTOCOL_ERROR");
    const unsafe = join(home, "unsafe-state");
    await mkdir(unsafe, { mode: 0o700 });
    await Bun.write(join(unsafe, "unsafe-file"), "synthetic fixture only");
    await chmod(join(unsafe, "unsafe-file"), 0o644);
    await assert.rejects(createBridge({ hostname: "package-check", stateDir: unsafe, startupTimeoutMs: 3000 }), { code: "STATE_UNSAFE" });
    assert.equal(children.length, 1, "Default installed helper must actually run");
    assert(children[0]!.exitCode !== null, "Production helper must exit after unsafe state rejection");

    // Keep the production protocol smoke independent of createBridge.
    const helper = fileURLToPath(new URL(`../bin/bridge-${process.platform}-${process.arch}`, import.meta.resolve(name)));
    const native = spawn([helper], { stdin: "pipe", stdout: "pipe", stderr: "pipe", env: {} });
    const timer = setTimeout(() => native.kill("SIGKILL"), 5000);
    try {
      native.stdin.write(JSON.stringify({ type: "start", version: -1, options: { authKey: "synthetic-secret-must-not-appear" } }) + "\n");
      native.stdin.end();
      const [out, err] = await Promise.all([new Response(native.stdout).text(), new Response(native.stderr).text(), native.exited]);
      assert.deepEqual(JSON.parse(out.trim()), { type: "error", version: 1, code: "PROTOCOL_ERROR" });
      assert.equal(err, "");
    } finally { clearTimeout(timer); native.kill(); }

    await rename(helper, helper + ".missing");
    try {
      await assert.rejects(createBridge({ hostname: "missing", ephemeral: true }), { code: "HELPER_UNAVAILABLE" });
    } finally { await rename(helper + ".missing", helper); }

    const helperPath = fileURLToPath(new URL("./synthetic-helper", import.meta.url));
    const stateDir = join(home, "authorized-state");
    let callbacks = 0;
    const onAuthRequired = ({ url }: { url: string }) => {
      assert.equal(url, "https://login.tailscale.com/a/synthetic-package-fixture");
      callbacks++;
    };
    const options = { hostname: "package-check", stateDir, helperPath, startupTimeoutMs: 3000 };
    for (let attempt = 0; attempt < 2; attempt++) {
      const bridge = await createBridge({ ...options, onAuthRequired });
      const proxy = bridge.httpProxyURL();
      try {
        assert.equal(callbacks, 1, "Authorized reuse must not prompt again");
        const socks = bridge.proxy();
        assert.equal(socks.host, "127.0.0.1");
        assert.notEqual(socks.password, new URL(proxy).password);
        const response = await fetch(`http://upstream.test:${server.port}/mcp`, {
          proxy, method: "POST", body: "installed-tarball", headers: { authorization: "Bearer synthetic-application" }, signal: AbortSignal.timeout(3000),
        });
        assert.deepEqual(await response.json(), { body: "installed-tarball", auth: "Bearer synthetic-application", host: `upstream.test:${server.port}` });
        await direct();
        assert.equal(globalThis.fetch, originalFetch);
      } finally {
        const closing = bridge.close();
        assert.equal(bridge.close(), closing);
        await closing;
      }
      assert.throws(() => bridge.proxy(), { code: "CLOSED" });
      assert.throws(() => bridge.httpProxyURL(), { code: "CLOSED" });
      await assert.rejects(fetch(`http://upstream.test:${server.port}`, { proxy, signal: AbortSignal.timeout(1000) }));
      assert(children.every(child => child.exitCode !== null), "Close must reap helpers");
    }
    for (const scenario of ["cancel", "timeout", "failure", "callback"] as const) {
      const controller = new AbortController();
      await assert.rejects(createBridge({
        hostname: scenario === "failure" ? "failure" : "hang", ephemeral: true, helperPath,
        startupTimeoutMs: scenario === "timeout" ? 300 : 3000, signal: controller.signal,
        onAuthRequired() {
          if (scenario === "cancel") controller.abort("synthetic-private-reason");
          if (scenario === "callback") throw new Error("synthetic-private-callback");
        },
      }), { code: { cancel: "CANCELLED", timeout: "STARTUP_TIMEOUT", failure: "AUTH_FAILED", callback: "CALLBACK_FAILED" }[scenario] });
      assert(children.every(child => child.exitCode !== null), `${scenario} must reap helpers`);
      assert.deepEqual(await readdir(temporary, { recursive: true }), before[1], `${scenario} leaked ephemeral state`);
    }
    await direct();
  }
  console.log(`Installed optional dependency ${present ? "present: native smoke, default resolution, lifecycle, proxy" : "absent: direct HTTP fallback"} passed; no enrollment`);
} finally {
  Bun.spawn = spawn;
  Bun.spawnSync = spawnSync;
  for (const child of children) if (child.exitCode === null) child.kill("SIGKILL");
  await Promise.all(children.map(child => child.exited));
  await server.stop(true);
}
