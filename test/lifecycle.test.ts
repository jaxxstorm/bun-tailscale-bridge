import { afterAll, beforeAll, expect, test } from "bun:test";
import { chmod, mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createBridge, type BridgeOptions } from "../src/index";
import { parseEvent } from "../src/protocol";
import fixtures from "../fixtures/protocol.json";

let directory: string;
let helperPath: string;
beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), "bridge-lifecycle-"));
  helperPath = join(directory, "helper.ts");
  await Bun.write(helperPath, `#!${process.execPath}\n` + await Bun.file(new URL("./fixtures/fake-helper.ts", import.meta.url)).text());
  await chmod(helperPath, 0o700);
});
afterAll(async () => { await rm(directory, { recursive: true, force: true }); });

function options(hostname: string): BridgeOptions { return { hostname, ephemeral: true, helperPath }; }
async function exited(name: string) {
  const pid = Number(await readFile(join(directory, `${name}.pid`), "utf8"));
  expect(() => process.kill(pid, 0)).toThrow();
}

test("shared helper events validate and do not expose extra credentials", () => {
  for (const event of fixtures.events) expect(parseEvent(JSON.stringify(event)) as unknown).toEqual(event);
  const ready = fixtures.events[0]!;
  for (const event of [
    { ...ready, localAPICred: "secret" },
    { ...ready, proxy: { ...ready.proxy, localAPICred: "secret" } },
    { ...ready, httpProxy: ready.proxy },
    { type: "auth_required", version: 1, url: "http://login.example/secret" },
    { type: "error", version: 1, code: "secret" },
    { type: "error", version: 1, code: "toString" },
  ]) expect(() => parseEvent(JSON.stringify(event))).toThrow("helper protocol");
  expect(() => parseEvent("x".repeat(65_537))).toThrow("helper protocol");
});

test("invalid options fail before a helper can be launched", async () => {
  for (const change of [
    { hostname: "" }, { hostname: "bad host" }, { ephemeral: false },
    { stateDir: "/tmp/state" }, { startupTimeoutMs: 0 }, { startupTimeoutMs: NaN },
    { startupTimeoutMs: Infinity }, { startupTimeoutMs: 1.5 }, { startupTimeoutMs: 2 ** 31 },
    { helperPath: "relative" }, { authKey: "" }, { authKey: "secret\n" },
    { onAuthRequired: "not a function" }, { signal: {} },
  ]) {
    await expect(createBridge({ ...options("invalid"), ...change } as BridgeOptions)).rejects.toMatchObject({ code: "INVALID_OPTIONS" });
  }
  await expect(createBridge({ hostname: "invalid", stateDir: "relative", helperPath })).rejects.toMatchObject({ code: "INVALID_OPTIONS" });
  await expect(createBridge({ ...options("invalid"), authKey: "s".repeat(65536) })).rejects.toMatchObject({ code: "INVALID_OPTIONS" });
  expect(await Bun.file(join(directory, "invalid.pid")).exists()).toBe(false);
});

test("missing or non-executable helper fails locally", async () => {
  await expect(createBridge({ ...options("missing"), helperPath: join(directory, "absent") })).rejects.toMatchObject({ code: "HELPER_UNAVAILABLE" });
  const file = join(directory, "not-executable");
  await Bun.write(file, "not executable");
  await chmod(file, 0o600);
  await expect(createBridge({ ...options("missing"), helperPath: file })).rejects.toMatchObject({ code: "HELPER_UNAVAILABLE" });
});

test("unsupported platform fails before enrollment", async () => {
  const descriptor = Object.getOwnPropertyDescriptor(process, "platform")!;
  try {
    Object.defineProperty(process, "platform", { ...descriptor, value: "win32" });
    await expect(createBridge(options("unsupported"))).rejects.toMatchObject({ code: "UNSUPPORTED_PLATFORM" });
  } finally { Object.defineProperty(process, "platform", descriptor); }
});

for (const [name, code] of [
  ["malformed", "PROTOCOL_ERROR"], ["oversized", "PROTOCOL_ERROR"], ["partial", "PROTOCOL_ERROR"],
  ["mismatch", "PROTOCOL_ERROR"], ["invalid-proxy", "PROTOCOL_ERROR"],
  ["exit", "HELPER_FAILED"], ["error", "AUTH_REQUIRED"],
] as const) test(`${name} startup cleans up with a sanitized error`, async () => {
  await expect(createBridge(options(name))).rejects.toMatchObject({ code });
  await exited(name);
});

test("proxy descriptors are private copies and repeated close terminates helper", async () => {
  const originalFetch = globalThis.fetch;
  const bridge = await createBridge(options("fragmented"));
  const proxy = bridge.proxy();
  proxy.password = "mutated";
  expect(bridge.proxy().password).toBe("0".repeat(32));
  const http = new URL(bridge.httpProxyURL());
  expect(http.hostname).toBe("127.0.0.1");
  expect(http.username).toBe("tsnet");
  expect(http.password).toBe("1".repeat(32));
  expect(http.port).toBe("12346");
  expect(globalThis.fetch).toBe(originalFetch);
  const firstClose = bridge.close();
  expect(bridge.close()).toBe(firstClose);
  expect(() => bridge.proxy()).toThrow("closed");
  expect(() => bridge.httpProxyURL()).toThrow("closed");
  await firstClose;
  await exited("fragmented");
});

test("startup abort is sanitized and does not affect a ready bridge", async () => {
  const before = AbortSignal.abort("private abort reason");
  await expect(createBridge({ ...options("abort-before"), signal: before })).rejects.toMatchObject({ code: "CANCELLED" });
  const controller = new AbortController();
  const bridge = await createBridge({ ...options("abort-ready"), signal: controller.signal });
  controller.abort("private abort reason");
  expect(bridge.proxy().host).toBe("127.0.0.1");
  await bridge.close();
  const during = new AbortController();
  const promise = createBridge({ ...options("hang"), signal: during.signal });
  const timer = setTimeout(() => during.abort("private abort reason"), 100);
  try { await expect(promise).rejects.toMatchObject({ code: "CANCELLED" }); }
  finally { clearTimeout(timer); }
  await exited("hang");
});

test("startup timeout terminates a silent helper", async () => {
  await expect(createBridge({ ...options("hang"), startupTimeoutMs: 100 })).rejects.toMatchObject({ code: "STARTUP_TIMEOUT" });
  await exited("hang");
});

test("private enrollment callback runs without blocking cancellation", async () => {
  let url = "";
  const bridge = await createBridge({ ...options("auth"), onAuthRequired: event => { url = event.url; } });
  expect(url).toBe("https://login.tailscale.com/a/synthetic");
  await bridge.close();
  await expect(createBridge({ ...options("auth-throw"), onAuthRequired() { throw new Error("private callback details"); } })).rejects.toMatchObject({ code: "CALLBACK_FAILED" });
  await exited("auth-throw");
  await expect(createBridge({ ...options("auth-hang"), startupTimeoutMs: 100, onAuthRequired: () => new Promise(() => {}) })).rejects.toMatchObject({ code: "STARTUP_TIMEOUT" });
  await exited("auth-hang");
});

test("ambient credentials are not inherited", async () => {
  const names = ["TS_AUTHKEY", "TSNET_FORCE_LOGIN", "TAILSCALE_SECRET", "PRIVATE_SECRET"];
  const saved = names.map(name => process.env[name]);
  try {
    for (const name of names) process.env[name] = "synthetic-secret";
    const bridge = await createBridge(options("environment"));
    await bridge.close();
  } finally {
    names.forEach((name, index) => {
      if (saved[index] === undefined) delete process.env[name]; else process.env[name] = saved[index];
    });
  }
});

test("pending callbacks do not hide helper exit or protocol failures", async () => {
  for (const [name, code] of [["auth-exit", "HELPER_FAILED"], ["auth-protocol", "PROTOCOL_ERROR"]]) {
    const start = performance.now();
    await expect(createBridge({ ...options(name!), startupTimeoutMs: 5000, onAuthRequired: () => new Promise(() => {}) })).rejects.toMatchObject({ code });
    expect(performance.now() - start).toBeLessThan(2000);
    await exited(name!);
  }
  let notifications = 0;
  const bridge = await createBridge({ ...options("auth-many"), onAuthRequired: () => { notifications++; } });
  expect(notifications).toBe(101);
  await bridge.close();
});

test("retained descendant stdout does not hold startup cleanup open", async () => {
  const start = performance.now();
  await expect(createBridge({ ...options("retained-stdout"), startupTimeoutMs: 100 })).rejects.toMatchObject({ code: "HELPER_FAILED" });
  expect(performance.now() - start).toBeLessThan(1000);
  await exited("retained-stdout");
});

test("helper death invalidates both proxy accessors without restart", async () => {
  const bridge = await createBridge(options("crash"));
  await Bun.sleep(150);
  expect(() => bridge.proxy()).toThrow("helper failed");
  expect(() => bridge.httpProxyURL()).toThrow("helper failed");
  await bridge.close();
  await exited("crash");
});

test("unresponsive helper receives bounded termination escalation", async () => {
  await expect(createBridge({ ...options("stubborn"), startupTimeoutMs: 100 })).rejects.toMatchObject({ code: "STARTUP_TIMEOUT" });
  await exited("stubborn");
}, 10_000);
