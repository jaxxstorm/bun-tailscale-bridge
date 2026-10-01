import { describe, expect, test } from "bun:test";
import actualMetadata from "../package.json";
import { packageFiles, packageName, repository, validateEntries, validateMetadata } from "../scripts/package-policy";
import { assetDecision, compareVersions, digests, manifestFor, npmDecision, registryState, stableVersion, validateIdentity, validateManifest, validateSource } from "../scripts/release-policy";

const commit = "a".repeat(40);
const metadata = { ...actualMetadata, version: "0.1.0" };
const bytes = new TextEncoder().encode("candidate bytes");
const candidate = manifestFor("v0.1.0", commit, metadata, bytes);
test("release-authorizing entry points require explicit source identity", () => {
  for (const mode of ["preflight", "pack", "npm", "github"]) {
    const result = Bun.spawnSync([process.execPath, "scripts/release.ts", mode], { cwd: new URL("../", import.meta.url).pathname, env: {} });
    expect(result.exitCode).not.toBe(0);
    expect(result.stderr.toString()).toContain("Missing RELEASE_COMMIT");
  }
});
describe("public package policy", () => {
  test("scoped public metadata and strict archive", () => {
    validateMetadata(actualMetadata);
    const entries = packageFiles.map((file) => `package/${file}`);
    validateEntries(entries);
    for (const file of entries) expect(() => validateEntries(entries.filter((entry) => entry !== file))).toThrow();
    for (const file of ["package/.npmrc", "package/state/key", "package/dist/extra.d.ts", "package/../escape", "package/extra/"]) expect(() => validateEntries([...entries, file])).toThrow();
    expect(() => validateEntries([...entries, entries[0]!])).toThrow();
    for (const override of [{ name: "bun-tailscale-bridge" }, { private: true }, { license: "UNLICENSED" }, { publishConfig: {} }, { repository: {} }, { exports: {} }, { dependencies: { bad: "1" } }]) expect(() => validateMetadata({ ...metadata, ...override })).toThrow();
    for (const hook of ["preinstall", "install", "postinstall", "prepare", "prepublish", "prepublishOnly", "prepack", "postpack"]) {
      expect(() => validateMetadata({ ...metadata, scripts: { ...metadata.scripts, [hook]: "unsafe-command" } })).toThrow();
    }
  });
});
describe("release authorization", () => {
  test("reviewed version bumps do not depend on the initial package version", () => {
    for (const version of [actualMetadata.version, "0.1.1", "1.0.0", "2.3.4"]) {
      const updated = { ...actualMetadata, version };
      validateMetadata(updated);
      expect(manifestFor(`v${version}`, commit, updated, bytes).version).toBe(version);
    }
  });
  test("canonical stable versions only", () => {
    for (const value of ["0.0.0", "1.2.3", "10.20.300"]) stableVersion(value);
    for (const value of ["01.2.3", "1.02.3", "1.2.03", "1.2", "1.2.3-rc.1", "1.2.3+build", "1.2.3\n", "-1.2.3", "1.2.3;touch /tmp/no", "$(id)"]) expect(() => stableVersion(value)).toThrow();
    for (const tag of ["0.1.0", "v0.2.0", "v0.1.0-rc.1", "v0.1.0+build", "v00.1.0", "v0.1.0\n"]) expect(() => validateIdentity(tag, commit, metadata)).toThrow();
    expect(() => validateIdentity("v0.1.0", "main", metadata)).toThrow();
    expect(compareVersions("2.0.0", "10.0.0")).toBe(-1);
    expect(compareVersions("0.10.0", "0.9.99")).toBe(1);
    expect(compareVersions("0.1.0", "0.1.0")).toBe(0);
  });
  test("source fixture checks exact refs, moved tags, ancestry and event identity without creating refs", async () => {
    const input = { repository, ref: "refs/tags/v0.1.0", commit, event: "c".repeat(40) };
    const commands: string[][] = [];
    const git = async (args: string[]) => { commands.push(args); return args[0] === "rev-parse" ? commit + "\n" : ""; };
    await validateSource(input, metadata, git);
    expect(commands).toEqual([
      ["rev-parse", `${input.event}^{commit}`],
      ["rev-parse", "HEAD"], ["fetch", "--no-tags", "origin", "refs/tags/v0.1.0"],
      ["rev-parse", "FETCH_HEAD^{commit}"], ["fetch", "--no-tags", "origin", "refs/heads/main"],
      ["merge-base", "--is-ancestor", commit, "FETCH_HEAD"],
    ]);
    for (const override of [{ repository: "fork/bridge" }, { ref: "refs/heads/main" }, { ref: "refs/tags/v0.1.0;id" }, { commit: "main" }, { event: "--unsafe" }]) {
      let invoked = false;
      await expect(validateSource({ ...input, ...override }, metadata, async () => { invoked = true; return commit; })).rejects.toThrow();
      expect(invoked).toBe(false);
    }
    for (const failAt of [`${input.event}^{commit}`, "HEAD", "FETCH_HEAD^{commit}", "merge-base", "fetch"]) {
      await expect(validateSource(input, metadata, async (args) => {
        if (args.includes(failAt)) { if (args[0] === "rev-parse") return "b".repeat(40); throw new Error("fixture source failure"); }
        return git(args);
      })).rejects.toThrow();
    }
    await validateSource({ ...input, event: commit }, metadata, git); // lightweight tag
  });
});
describe("candidate identity and recovery", () => {
  test("deterministic manifest and tampering", () => {
    expect(candidate).toEqual(manifestFor("v0.1.0", commit, metadata, bytes));
    const expected = { tag: "v0.1.0", commit, ...digests(bytes) };
    validateManifest(candidate, expected, metadata, bytes);
    for (const key of Object.keys(candidate)) expect(() => validateManifest({ ...candidate, [key]: "tampered" }, expected, metadata, bytes)).toThrow();
    expect(() => validateManifest(candidate, expected, metadata, new Uint8Array([0]))).toThrow();
    expect(() => validateManifest(candidate, { ...expected, sha256: "0".repeat(64) }, metadata, bytes)).toThrow();
    expect(() => validateManifest(candidate, { ...expected, integrity: "sha512-wrong" }, metadata, bytes)).toThrow();
  });
  test("registry failures never become absence", () => {
    expect(npmDecision(candidate, registryState(404, { error: "Not found" }))).toBe("publish");
    for (const [status, body] of [[401, {}], [403, {}], [429, {}], [500, {}], [404, {}], [404, { error: "unauthorized" }], [200, {}], [200, { name: packageName, versions: [], "dist-tags": {} }]]) expect(() => registryState(status as number, body)).toThrow();
    const existing = { dist: { integrity: candidate.integrity } };
    const state = registryState(200, { name: packageName, versions: { "0.1.0": existing, "1.0.0": {} }, "dist-tags": { latest: "1.0.0" } });
    expect(npmDecision(candidate, state)).toBe("complete");
    expect(() => npmDecision(candidate, { versions: { "0.1.0": {} } })).toThrow();
    expect(() => npmDecision(candidate, { versions: { "0.1.0": { dist: { integrity: "different" } } } })).toThrow();
    expect(() => npmDecision(candidate, { versions: {}, latest: "1.0.0" })).toThrow();
    expect(() => npmDecision(candidate, { versions: {}, latest: "0.1.0" })).toThrow();
    expect(npmDecision(candidate, { versions: {}, latest: "0.0.9" })).toBe("publish");
    for (const latest of ["1.0.0-rc.1", "01.0.0", null]) expect(() => registryState(200, { name: packageName, versions: {}, "dist-tags": { latest } })).toThrow();
    expect(() => registryState(200, { name: packageName, versions: { "0.1.0": existing }, "dist-tags": {} })).toThrow();
    expect(() => registryState(200, { name: packageName, versions: {}, "dist-tags": [] })).toThrow();
  });
  test("GitHub partial delivery only adds missing matching assets", () => {
    const assets = { "package.tgz": "abc", SHA256SUMS: "def", "release.json": "ghi" };
    expect(assetDecision(assets, [])).toEqual(Object.keys(assets));
    expect(assetDecision(assets, [{ name: "package.tgz", sha256: "abc" }])).toEqual(["SHA256SUMS", "release.json"]);
    expect(assetDecision(assets, Object.entries(assets).map(([name, sha256]) => ({ name, sha256 })))).toEqual([]);
    for (const existing of [[{ name: "package.tgz", sha256: "bad" }], [{ name: "other", sha256: "abc" }], [{ name: "package.tgz", sha256: "abc" }, { name: "package.tgz", sha256: "abc" }]]) expect(() => assetDecision(assets, existing)).toThrow();
  });
});
