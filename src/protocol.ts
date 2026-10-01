export const PROTOCOL_VERSION = 1;
export const MAX_FRAME_BYTES = 65_536;

export const errorMessages = {
  INVALID_OPTIONS: "Invalid bridge options; check hostname, state mode, and startup timeout",
  UNSUPPORTED_RUNTIME: "Run the bridge in an external Bun 1.4.2 process",
  UNSUPPORTED_PLATFORM: "Supported platforms are macOS and Linux on arm64 and x64",
  HELPER_UNAVAILABLE: "Matching executable helper is unavailable; reinstall the package and check executable permissions",
  PROTOCOL_ERROR: "Incompatible or invalid helper protocol",
  AUTH_REQUIRED: "Tailscale enrollment required; supply authKey or onAuthRequired",
  AUTH_FAILED: "Tailscale enrollment failed",
  STATE_UNSAFE: "State directory must be private, owned by this user, and not symlinked",
  STATE_LOCKED: "State directory is already in use",
  STARTUP_TIMEOUT: "Bridge startup timed out",
  CANCELLED: "Bridge operation cancelled",
  HELPER_FAILED: "Bridge helper failed",
  CLOSED: "Bridge is closed",
  CALLBACK_FAILED: "Authentication callback failed",
} as const;

export type BridgeErrorCode = keyof typeof errorMessages;

export class BridgeError extends Error {
  constructor(public readonly code: BridgeErrorCode) {
    super(errorMessages[code]);
    this.name = "BridgeError";
  }
}

export type BridgeOptions = {
  hostname: string;
  authKey?: string;
  startupTimeoutMs?: number;
  helperPath?: string;
  signal?: AbortSignal;
  onAuthRequired?: (event: { url: string }) => void | Promise<void>;
} & ({ stateDir: string; ephemeral?: false } | { stateDir?: never; ephemeral: true });

export interface StartOptions {
  hostname: string;
  stateDir?: string;
  ephemeral: boolean;
  authKey?: string;
  startupTimeoutMs: number;
  interactive: boolean;
}

export type HelperEvent =
  | { type: "ready"; version: 1; proxy: SocksProxy; httpProxy: SocksProxy }
  | { type: "auth_required"; version: 1; url: string }
  | { type: "error"; version: 1; code: BridgeErrorCode };

export interface SocksProxy {
  host: "127.0.0.1";
  port: number;
  username: "tsnet";
  password: string;
}

export function parseEvent(line: string): HelperEvent {
  try {
    if (Buffer.byteLength(line) > MAX_FRAME_BYTES) throw 0;
    const event = JSON.parse(line);
    // JSON.parse validates syntax but discards duplicate keys. Scan complete
    // strings and containers so escaped keys compare by value, per object.
    const containers: (Set<string> | null)[] = [];
    for (const token of line.matchAll(/"(?:[^"\\]|\\.)*"(\s*:)?|[{}\[\]]/g)) {
      const text = token[0];
      if (text === "{" || text === "[") {
        if (containers.length >= 16) throw 0;
        containers.push(text === "{" ? new Set<string>() : null);
      } else if (text === "}" || text === "]") {
        containers.pop();
      } else if (token[1]) {
        const key = JSON.parse(text.slice(0, -token[1].length));
        const keys = containers[containers.length - 1]!;
        if (keys.has(key)) throw 0;
        keys.add(key);
      }
    }
    if (!event || event.version !== PROTOCOL_VERSION) throw 0;
    const keys = Object.keys(event).sort().join(",");
    if (event.type === "error" && keys === "code,type,version" && typeof event.code === "string" && Object.hasOwn(errorMessages, event.code)) return event;
    if (event.type === "auth_required" && keys === "type,url,version" && typeof event.url === "string" && !/[\r\n\0]/.test(event.url)) {
      const url = new URL(event.url);
      if (url.protocol === "https:" && !url.username && !url.password) return event;
    }
    if (event.type === "ready" && keys === "httpProxy,proxy,type,version" &&
      [event.proxy, event.httpProxy].every((proxy) => proxy &&
        Object.keys(proxy).sort().join(",") === "host,password,port,username" &&
        proxy.host === "127.0.0.1" && Number.isInteger(proxy.port) && proxy.port > 0 && proxy.port <= 65535 &&
        proxy.username === "tsnet" && typeof proxy.password === "string" && /^[a-f0-9]{32}$/.test(proxy.password)) &&
      event.proxy.port !== event.httpProxy.port && event.proxy.password !== event.httpProxy.password) return event;
  } catch { /* Only expose locally defined errors, never protocol contents. */ }
  throw new BridgeError("PROTOCOL_ERROR");
}
