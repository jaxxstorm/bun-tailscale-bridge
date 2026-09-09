// Test-only control-channel peer. No enrollment or network listener.
let started = false;
let ignoreEOF = false;
let name = "";
const ready = {
  type: "ready", version: 1,
  proxy: { host: "127.0.0.1", port: 12345, username: "tsnet", password: "0".repeat(32) },
  httpProxy: { host: "127.0.0.1", port: 12346, username: "tsnet", password: "1".repeat(32) },
};
const emit = (value: unknown) => process.stdout.write(JSON.stringify(value) + "\n");
const hold = setInterval(() => {}, 1000);
let pending = "";
const input = Bun.stdin.stream().getReader();
while (true) {
  const { value: chunk, done } = await input.read();
  if (done) break;
  pending += new TextDecoder().decode(chunk);
  const newline = pending.indexOf("\n");
  if (newline < 0 || started) continue;
  started = true;
  const { options } = JSON.parse(pending.slice(0, newline));
  name = options.hostname;
  await Bun.write(new URL(`./${name}.pid`, import.meta.url), String(process.pid));
  if (name === "retained-stdout") {
    const child = Bun.spawn([process.execPath, "-e", "setTimeout(() => {}, 2000)"], { stdout: "inherit", stderr: "ignore" });
    child.unref();
    process.exit(0);
  }
  if (name === "hang" || name === "stubborn") {
    ignoreEOF = name === "stubborn";
    if (ignoreEOF) process.on("SIGTERM", () => {});
    continue;
  }
  if (name === "exit") process.exit(1);
  if (name === "error") { emit({ type: "error", version: 1, code: "AUTH_REQUIRED" }); process.exit(1); }
  if (name === "malformed") { process.stdout.write("not-json-secret\n"); continue; }
  if (name === "oversized") { process.stdout.write("a".repeat(65_537)); continue; }
  if (name === "partial") { process.stdout.write('{"secret":'); process.exit(1); }
  if (name === "mismatch") { emit({ ...ready, version: 999 }); continue; }
  if (name === "invalid-proxy") { emit({ ...ready, httpProxy: { ...ready.httpProxy, host: "example.com" } }); continue; }
  if (name.startsWith("auth")) {
    emit({ type: "auth_required", version: 1, url: "https://login.tailscale.com/a/synthetic" });
    if (name === "auth-exit") { setTimeout(() => process.exit(1), 50); continue; }
    if (name === "auth-protocol") { process.stdout.write("invalid\n"); continue; }
    if (name === "auth-many") {
      for (let i = 0; i < 100; i++) {
        emit({ type: "auth_required", version: 1, url: `https://login.tailscale.com/a/synthetic-${i}` });
        await Bun.sleep(1);
      }
    }
  }
  if (name === "environment" && Object.keys(process.env).some(key => /^(TS_|TSNET_|TAILSCALE_|PRIVATE_SECRET)/.test(key))) {
    emit({ type: "error", version: 1, code: "HELPER_FAILED" }); continue;
  }
  process.stderr.write("synthetic-secret-on-stderr\n");
  if (name === "fragmented") {
    const bytes = Buffer.from(JSON.stringify(ready) + "\n");
    for (const byte of bytes) process.stdout.write(Buffer.from([byte]));
  } else emit(ready);
  if (name === "crash") setTimeout(() => process.exit(1), 50);
  if (name === "duplicate") emit(ready);
}
if (!ignoreEOF) { clearInterval(hold); process.exit(0); }
