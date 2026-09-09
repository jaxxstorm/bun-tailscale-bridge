import { mkdtemp, mkdir, rm, lstat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createHash } from "node:crypto";
import { validateEntries, validateArchiveTypes, validateMetadata, packageName } from "./package-policy";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const root = fileURLToPath(new URL("../", import.meta.url));
const scratch = await mkdtemp(join(tmpdir(), "bridge-package-"));
const args = process.argv.slice(2);
assert(args.length === 0 || (args.length === 2 && args[0] === "--tarball"), "Usage: verify-package.ts [--tarball PATH]");
const supplied = args[1] ? resolve(args[1]) : undefined;
const digest = async (path: string) => createHash("sha256").update(new Uint8Array(await Bun.file(path).arrayBuffer())).digest("hex");
const originalDigest = supplied ? await digest(supplied) : undefined;
async function run(cmd: string[], cwd: string) {
  const child = Bun.spawn(cmd, { cwd, env: { PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch }, stdout: "pipe", stderr: "pipe" });
  const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  assert.equal(code, 0, `${cmd[0]} failed: ${stderr}`);
  return stdout;
}

try {
  assert.equal(Bun.version, "1.4.2", "Use Bun 1.4.2");
  const tarball = supplied ?? join(scratch, "bridge.tgz");
  if (!supplied) await run([process.execPath, "pm", "pack", "--ignore-scripts", "--filename", tarball], root);
  const entries = (await run(["tar", "-tzf", tarball], scratch)).trim().split("\n");
  const files = validateEntries(entries);
  validateArchiveTypes(await run(["tar", "-tvzf", tarball], scratch));
  console.log(`Archive allowlist passed (${files.length} files)`);
  const metadata = JSON.parse(await run(["tar", "-xOf", tarball, "package/package.json"], scratch));
  validateMetadata(metadata);
  for (const name of ["LICENSE", "THIRD_PARTY_NOTICES.txt"]) {
    assert.equal(await run(["tar", "-xOf", tarball, `package/${name}`], scratch), await Bun.file(join(root, name)).text(), "License text must match reviewed source");
  }
  const consumer = join(scratch, "consumer");
  await mkdir(consumer);
  await Bun.write(join(consumer, "package.json"), JSON.stringify({ private: true, type: "module", dependencies: { [packageName]: `file:${tarball}` } }));
  await run([process.execPath, "install", "--ignore-scripts"], consumer);
  const installed = join(consumer, "node_modules", packageName);
  for (const file of files) {
    const stat = await lstat(join(installed, file.slice("package/".length)));
    assert(stat.isFile() && !stat.isSymbolicLink(), "Only regular package files are allowed");
    if (file.includes("/bin/")) assert(stat.mode & 0o111, "Helper executable bit was lost");
  }
  await Bun.write(join(consumer, "consumer.ts"), `
import { createBridge, BridgeError, type BridgeOptions } from "@jaxxstorm/bun-tailscale-bridge";
import { fileURLToPath } from "node:url";
const options: BridgeOptions = { hostname: "package-check", ephemeral: true };
type Bridge = Awaited<ReturnType<typeof createBridge>>;
function checkTypes(bridge: Bridge) {
  const proxy: { host: "127.0.0.1"; port: number; username: "tsnet"; password: string } = bridge.proxy();
  const httpProxyURL: string = bridge.httpProxyURL();
  const closed: Promise<void> = bridge.close();
  return { proxy, httpProxyURL, closed };
}
if (typeof createBridge !== "function" || new BridgeError("PROTOCOL_ERROR").code !== "PROTOCOL_ERROR") throw new Error("Public exports missing");
// Deliberately do not call createBridge: the native check below cannot enroll.
void options; void checkTypes;
const helper = new URL("../bin/bridge-" + process.platform + "-" + process.arch, import.meta.resolve("@jaxxstorm/bun-tailscale-bridge"));
const child = Bun.spawn([fileURLToPath(helper)], { stdin: "pipe", stdout: "pipe", stderr: "pipe", env: {} });
const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
try {
  child.stdin.write(JSON.stringify({ type: "start", version: -1, options: { authKey: "synthetic-secret-must-not-appear" } }) + "\\n");
  child.stdin.end();
  const [out, err] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  const event = JSON.parse(out.trim());
  if (JSON.stringify(Object.keys(event).sort()) !== JSON.stringify(["code", "type", "version"]) || event.type !== "error" || event.version !== 1 || event.code !== "PROTOCOL_ERROR" || err !== "") throw new Error("Native helper did not return the sanitized protocol error");
} finally { clearTimeout(timer); child.kill(); }
console.log("Native packaged helper protocol check passed: " + process.platform + "-" + process.arch + "; no enrollment attempted");
`);
  // Use the already-installed compiler/types; the consumer itself installs only the tarball.
  await run([process.execPath, join(root, "node_modules/typescript/bin/tsc"), "--noEmit", "--strict", "--skipLibCheck", "--target", "ESNext", "--module", "Preserve", "--moduleResolution", "Bundler", "--types", "bun", "--typeRoots", join(root, "node_modules/@types"), "consumer.ts"], consumer);
  console.log((await run([process.execPath, "consumer.ts"], consumer)).trim());
} finally {
  await rm(scratch, { recursive: true, force: true });
  if (supplied) assert.equal(await digest(supplied), originalDigest, "Supplied tarball changed");
}
