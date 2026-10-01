import assert from "node:assert/strict";
import { lstat, mkdir, readdir } from "node:fs/promises";
import { join } from "node:path";
import { validateEntries, validateArchiveTypes } from "./package-policy";
import { manifestFor, validateIdentity, validateManifest, type Manifest } from "./release-policy";
import { runReleaseCommand } from "./release-command";

const run = (root: string, args: string[]) => runReleaseCommand(args, root);

export async function packCandidate(root: string, directory: string, tag: string, commit: string) {
  const metadata = await Bun.file(join(root, "package.json")).json();
  validateIdentity(tag, commit, metadata);
  await mkdir(directory); // Refuse to reuse a dirty directory or replace retained bytes.
  const filename = `jaxxstorm-bun-tailscale-bridge-${metadata.version}.tgz`;
  const tarball = join(directory, filename);
  await run(root, [process.execPath, "pm", "pack", "--ignore-scripts", "--filename", tarball]);
  const manifest = manifestFor(tag, commit, metadata, await Bun.file(tarball).bytes());
  await Bun.write(join(directory, "release.json"), JSON.stringify(manifest, null, 2) + "\n");
  await Bun.write(join(directory, "SHA256SUMS"), `${manifest.sha256}  ${filename}\n`);
  return checkCandidate(root, directory, { tag, commit, sha256: manifest.sha256, integrity: manifest.integrity });
}

export async function checkCandidate(root: string, directory: string, expected: { tag: string; commit: string; sha256: string; integrity: string }) {
  const metadata = await Bun.file(join(root, "package.json")).json();
  validateIdentity(expected.tag, expected.commit, metadata);
  assert(/^[a-f0-9]{64}$/.test(expected.sha256) && /^sha512-[A-Za-z0-9+/]{86}==$/.test(expected.integrity), "Expected producer digests required");
  const filename = `jaxxstorm-bun-tailscale-bridge-${metadata.version}.tgz`;
  assert.deepEqual((await readdir(directory)).sort(), [filename, "release.json", "SHA256SUMS"].sort(), "Unexpected candidate bundle contents");
  for (const file of await readdir(directory)) assert((await lstat(join(directory, file))).isFile(), "Candidate must contain regular files");
  const manifest: Manifest = await Bun.file(join(directory, "release.json")).json();
  const tarball = join(directory, filename);
  validateManifest(manifest, expected, metadata, await Bun.file(tarball).bytes());
  assert.equal(await Bun.file(join(directory, "release.json")).text(), JSON.stringify(manifest, null, 2) + "\n", "Noncanonical manifest");
  assert.equal(await Bun.file(join(directory, "SHA256SUMS")).text(), `${manifest.sha256}  ${filename}\n`);
  validateEntries((await run(root, ["tar", "-tzf", tarball])).trim().split("\n"));
  validateArchiveTypes(await run(root, ["tar", "-tvzf", tarball]));
  const packed = JSON.parse(await run(root, ["tar", "-xOf", tarball, "package/package.json"]));
  validateIdentity(expected.tag, expected.commit, packed);
  for (const file of ["LICENSE", "THIRD_PARTY_NOTICES.txt"]) assert.equal(await run(root, ["tar", "-xOf", tarball, `package/${file}`]), await Bun.file(join(root, file)).text(), "License text differs from reviewed source");
  return manifest;
}
