import assert from "node:assert/strict";
import { join, resolve } from "node:path";
import { targets } from "./package-policy";

const root = resolve(import.meta.dir, "..");
const inventory = await Bun.file(join(root, "docs/license-inventory.json")).json();
function go(args: string[]) {
  const result = Bun.spawnSync(["go", ...args], { cwd: root });
  assert.equal(result.exitCode, 0, "Helper inspection failed");
  return result.stdout.toString();
}
// Compare actual embedded bytes, not just a module name absent from go version -m.
const prebuilt = JSON.parse(go(["-C", "helper", "mod", "download", "-json", "github.com/tailscale/web-client-prebuilt"]));
const assets: Uint8Array[] = [];
for await (const file of new Bun.Glob("build/**/*").scan({ cwd: prebuilt.Dir, onlyFiles: true })) {
  assets.push(await Bun.file(join(prebuilt.Dir, file)).bytes());
}
assert(assets.length >= 4, "Expected pinned web assets for exclusion check");
for (const target of targets) {
  const path = join(root, "bin", `bridge-${target}`);
  const info = go(["version", "-m", path]);
  assert(info.includes("-tags=ts_omit_webclient"), "Production helper lacks approved build tag");
  assert(info.includes("CGO_ENABLED=0"), "Production helper must be CGO-free");
  const [os, arch] = target.split("-");
  assert(info.includes(`GOOS=${os}`) && info.includes(`GOARCH=${arch === "x64" ? "amd64" : arch}`), "Wrong helper target");
  for (const line of info.split("\n")) {
    const fields = line.trim().split("\t");
    if (fields[0] === "dep") assert(inventory[target].some((entry: any) => (entry.name === fields[1] || entry.name.startsWith(`${fields[1]}/`)) && entry.version === fields[2]), `Uninventoried helper module: ${fields[1]}`);
  }
  assert(!info.includes("web-client-prebuilt"), "Unexpected web client dependency");
  const binary = Buffer.from(await Bun.file(path).bytes());
  for (const asset of assets) assert(!binary.includes(asset), "Web-client asset is still embedded");
  console.log(`${target}: build settings and module inventory match; all ${assets.length} pinned web assets absent`);
}
