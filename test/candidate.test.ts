import { expect, test } from "bun:test";
import { mkdtemp, mkdir, rm, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import metadata from "../package.json";
import { checkCandidate, packCandidate } from "../scripts/candidate";
import { packageFiles, validateArchiveTypes } from "../scripts/package-policy";
import { digests } from "../scripts/release-policy";

test("candidate packs once, preserves supplied bytes, and rejects bundle tampering", async () => {
  const scratch = await mkdtemp(join(tmpdir(), "bridge-candidate-test-"));
  const root = join(scratch, "source"), bundle = join(scratch, "bundle");
  const tag = `v${metadata.version}`;
  try {
    await mkdir(root);
    for (const file of packageFiles) await Bun.write(join(root, file), file === "package.json" ? JSON.stringify(metadata) : `fixture ${file}\n`);
    const manifest = await packCandidate(root, bundle, tag, "a".repeat(40));
    const tarball = join(bundle, manifest.filename);
    const original = await Bun.file(tarball).bytes();
    await checkCandidate(root, bundle, manifest);
    expect(digests(await Bun.file(tarball).bytes())).toEqual(digests(original));
    await expect(packCandidate(root, bundle, tag, "a".repeat(40))).rejects.toThrow();
    await Bun.write(tarball, new Uint8Array([0]));
    await expect(checkCandidate(root, bundle, manifest)).rejects.toThrow();
    await Bun.write(tarball, original);
    await Bun.write(join(bundle, "unexpected"), "unreviewed");
    await expect(checkCandidate(root, bundle, manifest)).rejects.toThrow();
    await rm(join(bundle, "unexpected"));
    for (const file of ["release.json", "SHA256SUMS"]) {
      const contents = await Bun.file(join(bundle, file)).text();
      await Bun.write(join(bundle, file), contents + "\n");
      await expect(checkCandidate(root, bundle, manifest)).rejects.toThrow();
      await Bun.write(join(bundle, file), contents);
    }
    await rm(tarball);
    const outside = join(scratch, "outside.tgz");
    await Bun.write(outside, original);
    await symlink(outside, tarball);
    await expect(checkCandidate(root, bundle, manifest)).rejects.toThrow();
  } finally { await rm(scratch, { recursive: true, force: true }); }
});

test("archive links and special files fail before extraction", () => {
  validateArchiveTypes("-rwxr-xr-x 0 user group 1 Jan 1 package/bin/helper\ndrwxr-xr-x 0 user group 0 Jan 1 package/");
  for (const type of ["l", "h", "b", "c", "p", "s"]) expect(() => validateArchiveTypes(`${type}rwxr-xr-x fixture`)).toThrow();
});
