import { access, constants, stat } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";
import {
  BridgeError, MAX_FRAME_BYTES, PROTOCOL_VERSION, parseEvent,
  type BridgeErrorCode, type BridgeOptions, type SocksProxy, type StartOptions,
} from "./protocol";

export { BridgeError } from "./protocol";
export type { BridgeErrorCode, BridgeOptions, SocksProxy } from "./protocol";

export interface Bridge {
  /** Sensitive instance credentials. The caller owns its SOCKS client. */
  proxy(): SocksProxy;
  /** Credential-bearing URL for Bun fetch's per-request proxy option. Do not log. */
  httpProxyURL(): string;
  close(): Promise<void>;
}

function validate(options: BridgeOptions): StartOptions {
  if (!options || typeof options !== "object" ||
    typeof options.hostname !== "string" || !/^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$/.test(options.hostname) ||
    (options.ephemeral !== undefined && typeof options.ephemeral !== "boolean") ||
    (options.ephemeral === true ? options.stateDir !== undefined :
      typeof options.stateDir !== "string" || !isAbsolute(options.stateDir) || options.stateDir.includes("\0")) ||
    (options.helperPath !== undefined && (typeof options.helperPath !== "string" || !isAbsolute(options.helperPath) || options.helperPath.includes("\0"))) ||
    (options.authKey !== undefined && (typeof options.authKey !== "string" || !options.authKey || options.authKey.trim() !== options.authKey || /[\r\n\0]/.test(options.authKey))) ||
    (options.onAuthRequired !== undefined && typeof options.onAuthRequired !== "function") ||
    (options.signal !== undefined && !(options.signal instanceof AbortSignal))) {
    throw new BridgeError("INVALID_OPTIONS");
  }
  const timeout = options.startupTimeoutMs ?? 60_000;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 2_147_483_647) throw new BridgeError("INVALID_OPTIONS");
  return {
    hostname: options.hostname,
    ephemeral: options.ephemeral === true,
    ...(options.stateDir === undefined ? {} : { stateDir: options.stateDir }),
    ...(options.authKey === undefined ? {} : { authKey: options.authKey }),
    startupTimeoutMs: timeout,
    interactive: options.onAuthRequired !== undefined,
  };
}

/** Starts an independent userspace Tailscale node with local SOCKS and HTTP proxies. */
export async function createBridge(options: BridgeOptions): Promise<Bridge> {
  const nodeOptions = validate(options);
  const frame = JSON.stringify({ type: "start", version: PROTOCOL_VERSION, options: nodeOptions });
  if (Buffer.byteLength(frame) > MAX_FRAME_BYTES) throw new BridgeError("INVALID_OPTIONS");
  if (!["darwin", "linux"].includes(process.platform) || !["arm64", "x64"].includes(process.arch)) {
    throw new BridgeError("UNSUPPORTED_PLATFORM");
  }
  if (options.signal?.aborted) throw new BridgeError("CANCELLED");
  const path = options.helperPath ?? fileURLToPath(new URL(`../bin/bridge-${process.platform}-${process.arch}`, import.meta.url));
  try {
    await access(path, constants.X_OK);
    if (!(await stat(path)).isFile()) throw 0;
  } catch { throw new BridgeError("HELPER_UNAVAILABLE"); }
  if (options.signal?.aborted) throw new BridgeError("CANCELLED");

  // Pass only operational environment, never the parent's ambient credentials.
  const env: Record<string, string> = {};
  for (const name of ["HOME", "PATH", "TMPDIR", "TMP", "TEMP", "LANG", "SSL_CERT_FILE", "SSL_CERT_DIR"]) {
    if (process.env[name] !== undefined) env[name] = process.env[name]!;
  }
  let child: Bun.Subprocess<"pipe", "pipe", "ignore">;
  try {
    child = Bun.spawn([path], { env, stdin: "pipe", stdout: "pipe", stderr: "ignore" });
  } catch { throw new BridgeError("HELPER_UNAVAILABLE"); }

  let phase: "starting" | "ready" | "failed" | "closed" = "starting";
  let failure: BridgeErrorCode = "HELPER_FAILED";
  let descriptor: SocksProxy | undefined;
  let httpDescriptor: SocksProxy | undefined;
  let readyReceived = false;
  let pendingCallbacks = 0;
  let closing: Promise<void> | undefined;
  const reader = child.stdout.getReader();
  const startup = Promise.withResolvers<void>();
  // A synchronous pipe failure can reject before the caller reaches the await.
  void startup.promise.catch(() => {});
  const timer = setTimeout(() => fail("STARTUP_TIMEOUT"), nodeOptions.startupTimeoutMs);
  const abort = () => fail("CANCELLED");
  options.signal?.addEventListener("abort", abort, { once: true });

  function endStartup() {
    clearTimeout(timer);
    options.signal?.removeEventListener("abort", abort);
  }
  async function waitForExit(ms: number) {
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([
        child.exited.then(() => true),
        new Promise<false>((resolve) => { timer = setTimeout(() => resolve(false), ms); }),
      ]);
    } finally { clearTimeout(timer); }
  }
  function close(): Promise<void> {
    if (closing) return closing;
    if (phase !== "failed") phase = "closed";
    descriptor = undefined;
    httpDescriptor = undefined;
    endStartup();
    closing = (async () => {
      const cancelled = reader.cancel().catch(() => {});
      try { void Promise.resolve(child.stdin.end()).catch(() => {}); } catch { /* Child may already have exited. */ }
      if (!await waitForExit(5_000)) {
        child.kill("SIGTERM");
        if (!await waitForExit(1_000)) {
          child.kill("SIGKILL");
          await child.exited;
        }
      }
      await cancelled;
    })();
    return closing;
  }
  function fail(code: BridgeErrorCode) {
    if (phase === "closed" || phase === "failed") return;
    failure = code;
    phase = "failed";
    startup.reject(new BridgeError(code));
    void close();
  }
  function publishReady() {
    if (phase === "starting" && readyReceived && pendingCallbacks === 0) {
      phase = "ready";
      endStartup();
      startup.resolve();
    }
  }

  // Read byte-bounded frames, even when a helper never supplies a newline.
  const pump = (async () => {
    let pending = Buffer.alloc(0);
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) {
          fail(pending.length ? "PROTOCOL_ERROR" : "HELPER_FAILED");
          return;
        }
        let offset = 0;
        while (offset < value.length) {
          const newline = value.indexOf(10, offset);
          const end = newline === -1 ? value.length : newline;
          if (pending.length + end - offset > MAX_FRAME_BYTES) throw new BridgeError("PROTOCOL_ERROR");
          pending = Buffer.concat([pending, value.subarray(offset, end)]);
          offset = end + 1;
          if (newline === -1) break;
          if (closing) return;
          const line = new TextDecoder("utf-8", { fatal: true }).decode(pending);
          const event = parseEvent(line);
          pending = Buffer.alloc(0);
          if (event.type === "error") {
            fail(event.code);
            return;
          }
          if (phase !== "starting" || readyReceived) throw new BridgeError("PROTOCOL_ERROR");
          if (event.type === "auth_required") {
            if (!options.onAuthRequired || pendingCallbacks >= 32) throw new BridgeError("PROTOCOL_ERROR");
            pendingCallbacks++;
            // Keep reading errors/EOF while user interaction is pending.
            void Promise.resolve().then(() => options.onAuthRequired!({ url: event.url })).then(() => {
              pendingCallbacks--;
              publishReady();
            }, () => fail("CALLBACK_FAILED"));
          } else {
            descriptor = { ...event.proxy };
            httpDescriptor = { ...event.httpProxy };
            readyReceived = true;
            publishReady();
          }
        }
      }
    } catch {
      fail("PROTOCOL_ERROR");
    } finally {
      reader.releaseLock();
    }
  })();
  void child.exited.then(() => {
    // Give buffered error frames one turn to supply a more specific error.
    if (phase === "ready") fail("HELPER_FAILED");
    else if (phase === "starting") setTimeout(() => fail("HELPER_FAILED"), 0);
  });
  try {
    if (options.signal?.aborted) fail("CANCELLED");
    if (phase === "starting") {
      child.stdin.write(frame + "\n");
      void Promise.resolve(child.stdin.flush()).catch(() => fail("HELPER_FAILED"));
    }
    await startup.promise;
    if (!descriptor || child.exitCode !== null) throw new BridgeError(failure);
  } catch (error) {
    fail(error instanceof BridgeError ? error.code : "HELPER_FAILED");
    await close();
    await pump;
    throw new BridgeError(failure);
  }
  return {
    proxy() {
      if (phase === "ready" && child.exitCode !== null) fail("HELPER_FAILED");
      if (phase !== "ready" || !descriptor) throw new BridgeError(phase === "closed" ? "CLOSED" : failure);
      return { ...descriptor };
    },
    httpProxyURL() {
      if (phase === "ready" && child.exitCode !== null) fail("HELPER_FAILED");
      if (phase !== "ready" || !httpDescriptor) throw new BridgeError(phase === "closed" ? "CLOSED" : failure);
      const url = new URL(`http://${httpDescriptor.host}:${httpDescriptor.port}`);
      url.username = httpDescriptor.username;
      url.password = httpDescriptor.password;
      return url.href;
    },
    close,
  };
}
