import { mkdtemp, mkdir, rm, lstat, copyFile, chmod } from "node:fs/promises";
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
async function run(cmd: string[], cwd: string, env: Record<string, string | undefined> = {
  PATH: process.env.PATH, HOME: scratch, TMPDIR: scratch,
  NPM_CONFIG_USERCONFIG: join(scratch, "user.npmrc"), NPM_CONFIG_GLOBALCONFIG: join(scratch, "global.npmrc"),
  NPM_CONFIG_CACHE: join(scratch, "npm-cache"),
  NPM_CONFIG_UPDATE_NOTIFIER: "false",
}, timeout = 120_000) {
  const child = Bun.spawn(cmd, { cwd, env, stdout: "pipe", stderr: "pipe" });
  const timer = setTimeout(() => child.kill("SIGKILL"), timeout);
  try {
    const [stdout, stderr, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
    assert.equal(code, 0, `${cmd[0]} failed: ${stdout}${stderr}`);
    return stdout;
  } finally { clearTimeout(timer); }
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
  // Build tools may use the checkout and Go cache. The resulting standalone
  // TEST ONLY binary is copied beside the consumer, never into the package.
  const synthetic = join(scratch, "synthetic-helper");
  await run(["go", "build", "-trimpath", "-tags=ts_omit_webclient", "-o", synthetic, "./internal/testhelper/packagefixture"], join(root, "helper"), {
    PATH: process.env.PATH, HOME: process.env.HOME!, TMPDIR: scratch,
    GOMODCACHE: process.env.GOMODCACHE, GOCACHE: process.env.GOCACHE,
  }, 180_000);
  for (const manager of ["bun", "npm"]) for (const mode of ["present", "absent"]) {
    const consumer = join(scratch, `${manager}-${mode}`);
    await mkdir(consumer);
    await Bun.write(join(consumer, "package.json"), JSON.stringify({ private: true, type: "module", optionalDependencies: { [packageName]: `file:${tarball}` } }));
    await run([manager === "bun" ? process.execPath : "npm", "install", "--ignore-scripts", ...(manager === "npm" ? ["--no-audit", "--no-fund"] : []), ...(mode === "absent" ? ["--omit", "optional"] : [])], consumer);
    const installed = join(consumer, "node_modules", packageName);
    if (mode === "present") {
      for (const file of files) {
        const path = join(installed, file.slice("package/".length));
        const stat = await lstat(path);
        assert(stat.isFile() && !stat.isSymbolicLink(), "Only regular package files are allowed");
        if (file.includes("/bin/")) {
          assert.equal(stat.mode & 0o777, 0o755, "Helper executable permissions changed");
          const info = await run(["go", "version", "-m", path], consumer);
          const [os, arch] = file.split("bridge-")[1]!.split("-");
          assert(info.split("\n")[0]!.endsWith("go1.26.2"), "Unexpected installed helper toolchain");
          assert(info.includes("\tpath\tgithub.com/jaxxstorm/bun-tailscale-bridge/helper/cmd/bridge\n"), "Unexpected installed helper entrypoint");
          for (const setting of ["-tags=ts_omit_webclient", "CGO_ENABLED=0", `GOOS=${os}`, `GOARCH=${arch === "x64" ? "amd64" : arch}`]) assert(info.includes(setting), "Wrong installed helper build settings");
        }
      }
      await Bun.write(join(consumer, "consumer.ts"), `
import { createBridge, BridgeError, type BridgeOptions } from "@jaxxstorm/bun-tailscale-bridge";
const options: BridgeOptions = {
  hostname: "package-check", stateDir: "/unused-typecheck-only",
  startupTimeoutMs: 3000, signal: new AbortController().signal,
  authKey: undefined, onAuthRequired: ({ url }) => { const value: string = url; void value; },
};
type Bridge = Awaited<ReturnType<typeof createBridge>>;
function checkTypes(bridge: Bridge) {
  const proxy: { host: "127.0.0.1"; port: number; username: "tsnet"; password: string } = bridge.proxy();
  const httpProxyURL: string = bridge.httpProxyURL();
  const closed: Promise<void> = bridge.close();
  return { proxy, httpProxyURL, closed };
}
if (typeof createBridge !== "function" || new BridgeError("PROTOCOL_ERROR").code !== "PROTOCOL_ERROR") throw new Error("Public exports missing");
void options; void checkTypes;
`);
      // Use the already-installed compiler/types; the consumer itself installs only the tarball.
      await run([process.execPath, join(root, "node_modules/typescript/bin/tsc"), "--noEmit", "--strict", "--skipLibCheck", "--target", "ESNext", "--module", "Preserve", "--moduleResolution", "Bundler", "--types", "bun", "--typeRoots", join(root, "node_modules/@types"), "consumer.ts"], consumer);
      await run(["node", "--input-type=module", "-e", `
        import assert from "node:assert/strict";
        const { createBridge } = await import("${packageName}");
        await assert.rejects(createBridge({ hostname: "node-check", ephemeral: true }), { code: "UNSUPPORTED_RUNTIME" });
      `], consumer);
    }
    await copyFile(join(root, "test/fixtures/package-consumer.ts"), join(consumer, "runtime.ts"));
    await copyFile(synthetic, join(consumer, "synthetic-helper"));
    await chmod(join(consumer, "synthetic-helper"), 0o700);
    const home = join(consumer, "home");
    const temporary = join(consumer, "tmp");
    await mkdir(home, { mode: 0o700 });
    await mkdir(temporary, { mode: 0o700 });
    // No checkout paths, ambient credentials, preload flags, or host HOME in the
    // runtime environment. Only compilation above uses checkout tools/types.
    const command = [process.execPath, "--no-install", "runtime.ts", mode];
    if (process.platform === "darwin") {
      command.unshift("/usr/bin/sandbox-exec", "-p", `(version 1) (allow default) (deny file-read* (subpath ${JSON.stringify(resolve(root))}))`);
    }
    console.log(`${manager}: ` + (await run(command, consumer, { PATH: "/usr/bin:/bin", HOME: home, TMPDIR: temporary }, 60_000)).trim());
  }
} finally {
  await rm(scratch, { recursive: true, force: true, maxRetries: 3 });
  if (supplied) assert.equal(await digest(supplied), originalDigest, "Supplied tarball changed");
}
