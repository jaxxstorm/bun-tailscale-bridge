import assert from "node:assert/strict";

export const packageName = "@jaxxstorm/bun-tailscale-bridge";
export const repository = "jaxxstorm/bun-tailscale-bridge";
export const registry = "https://registry.npmjs.org/";
export const targets = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"];
export const packageFiles = [
  "package.json", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.txt",
  "dist/index.js", "dist/index.d.ts", "dist/protocol.d.ts",
  ...targets.map((target) => `bin/bridge-${target}`),
];

export function validateMetadata(metadata: any) {
  assert.equal(metadata.name, packageName);
  assert.equal(metadata.private, undefined);
  assert.equal(metadata.license, "MIT");
  assert.deepEqual(metadata.repository, { type: "git", url: `git+https://github.com/${repository}.git` });
  assert.equal(metadata.homepage, `https://github.com/${repository}#readme`);
  assert.deepEqual(metadata.bugs, { url: `https://github.com/${repository}/issues` });
  assert.deepEqual(metadata.publishConfig, { access: "public", registry });
  assert.deepEqual(metadata.exports, { ".": { types: "./dist/index.d.ts", import: "./dist/index.js" } });
  assert.equal(metadata.type, "module");
  assert.equal(metadata.packageManager, "bun@1.4.2");
  assert.deepEqual(metadata.engines, { bun: "1.4.2" });
  for (const hook of ["preinstall", "install", "postinstall", "prepare", "prepublish", "prepublishOnly", "prepack", "postpack"]) {
    assert.equal(metadata.scripts?.[hook], undefined, "Installation and publication must not run lifecycle hooks");
  }
  for (const field of ["dependencies", "optionalDependencies", "peerDependencies", "bundledDependencies"]) {
    assert(!Object.keys(metadata[field] ?? {}).length, "Runtime dependencies require explicit packaging review");
  }
}

export function validateEntries(entries: string[]) {
  assert.equal(new Set(entries).size, entries.length, "Duplicate archive entries");
  const files = entries.filter((entry) => !entry.endsWith("/"));
  assert.deepEqual(files.toSorted(), packageFiles.map((file) => `package/${file}`).toSorted(), "Archive must match the explicit allowlist");
  for (const entry of entries.filter((entry) => entry.endsWith("/"))) {
    assert(["package/", "package/dist/", "package/bin/"].includes(entry), "Unexpected archive directory");
  }
  return files;
}

export function validateArchiveTypes(listing: string) {
  for (const line of listing.trim().split("\n")) assert(line.startsWith("-") || line.startsWith("d"), "Archive links and special files are forbidden");
}
