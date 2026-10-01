import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { packCandidate, checkCandidate } from "./candidate";
import { registry } from "./package-policy";

assert(process.argv.length === 3 || (process.argv.length === 4 && process.argv[3] === "--existing"), "Usage: release-dry-run.ts OUTPUT_DIRECTORY [--existing] (build all targets first)");
assert(!process.env.GITHUB_ACTIONS, "Local dry-run only; never a release authorization path");
const root = resolve(import.meta.dir, "..");
const directory = resolve(process.argv[2]!);
const metadata = await Bun.file(join(root, "package.json")).json();
// An all-zero fixture commit explicitly avoids pretending this uncommitted tree is authorized.
const identity = { tag: `v${metadata.version}`, commit: "0".repeat(40) };
const manifest = process.argv[3] === "--existing"
  ? await checkCandidate(root, directory, { ...await Bun.file(join(directory, "release.json")).json(), ...identity })
  : await packCandidate(root, directory, identity.tag, identity.commit);
const tarball = join(directory, manifest.filename);
const scratch = await mkdtemp(join(tmpdir(), "bridge-npm-dryrun-"));
// Build caches contain tools/modules, not user identity. Keep read-only Go
// module directories out of the disposable credential-isolation HOME.
const cache = Bun.spawnSync(["go", "env", "-json", "GOMODCACHE", "GOCACHE"]);
assert.equal(cache.exitCode, 0, "Cannot locate Go build caches");
const { GOMODCACHE, GOCACHE } = JSON.parse(cache.stdout.toString());
const env = { PATH: process.env.PATH!, HOME: scratch, TMPDIR: scratch, GOMODCACHE, GOCACHE, NPM_CONFIG_USERCONFIG: join(scratch, "user.npmrc"), NPM_CONFIG_GLOBALCONFIG: join(scratch, "global.npmrc"), NPM_CONFIG_CACHE: join(scratch, "cache"), NPM_CONFIG_IGNORE_SCRIPTS: "true", NPM_CONFIG_UPDATE_NOTIFIER: "false" };
async function run(args: string[], cwd: string) {
  const child = Bun.spawn(args, { cwd, env, stdout: "pipe", stderr: "pipe" });
  const [out, , code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  assert.equal(code, 0, `Local check failed: ${args[0]} (output withheld)`);
  return out;
}
try {
  console.log((await run([process.execPath, "scripts/verify-package.ts", "--tarball", tarball], root)).trim());
  assert.equal((await run(["node", "--version"], scratch)).trim(), "v24.11.1");
  assert.equal((await run(["npm", "--version"], scratch)).trim(), "11.6.2");
  const result = JSON.parse(await run(["npm", "publish", tarball, "--dry-run", "--ignore-scripts", "--access=public", `--registry=${registry}`, "--json"], scratch));
  assert.equal(result.name, manifest.name);
  assert.equal(result.version, manifest.version);
  assert.equal(result.integrity, manifest.integrity, "npm dry-run saw different bytes");
  await checkCandidate(root, directory, manifest);
  console.log(`npm publish dry-run passed (isolated configuration, no credentials or publication).\nRetained local fixture: ${directory}\nSHA-256: ${manifest.sha256}\nSRI: ${manifest.integrity}\nSource is an unauthorizing all-zero fixture, not a release commit.`);
} finally {
  await rm(scratch, { recursive: true, force: true, maxRetries: 3 });
}
