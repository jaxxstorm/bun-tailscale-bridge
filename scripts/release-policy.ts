import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { packageName, repository, validateMetadata } from "./package-policy";

export function stableVersion(version: string) {
  assert(version.trim() === version && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version), "Canonical stable version required");
  return version.split(".").map(BigInt);
}
export function compareVersions(a: string, b: string) {
  const left = stableVersion(a), right = stableVersion(b);
  for (let i = 0; i < 3; i++) if (left[i] !== right[i]) return left[i]! > right[i]! ? 1 : -1;
  return 0;
}
export function validateIdentity(tag: string, commit: string, metadata: any) {
  assert(tag.startsWith("v"), "Stable vX.Y.Z tag required");
  stableVersion(tag.slice(1));
  assert(commit.length === 40 && /^[a-f0-9]{40}$/.test(commit), "Immutable commit required");
  validateMetadata(metadata);
  assert.equal(metadata.version, tag.slice(1), "Tag/package version mismatch");
}
export async function validateSource(input: { repository: string; ref: string; commit: string; event: string }, metadata: any, git: (args: string[]) => Promise<string>) {
  assert.equal(input.repository, repository, "Canonical repository required");
  assert(input.ref.startsWith("refs/tags/"), "Tag event required");
  const tag = input.ref.slice("refs/tags/".length);
  validateIdentity(tag, input.commit, metadata);
  assert(input.event.length === 40 && /^[a-f0-9]{40}$/.test(input.event), "Immutable event object required");
  assert.equal((await git(["rev-parse", `${input.event}^{commit}`])).trim(), input.commit, "Commit differs from immutable peeled event object");
  assert.equal((await git(["rev-parse", "HEAD"])).trim(), input.commit, "Checkout differs from event commit");
  // Fetch exact validated refs; do not trust a stale local tag or remote-tracking branch.
  await git(["fetch", "--no-tags", "origin", `refs/tags/${tag}`]);
  assert.equal((await git(["rev-parse", "FETCH_HEAD^{commit}"])).trim(), input.commit, "Tag moved from event commit");
  await git(["fetch", "--no-tags", "origin", "refs/heads/main"]);
  await git(["merge-base", "--is-ancestor", input.commit, "FETCH_HEAD"]);
  return tag;
}
export function digests(bytes: Uint8Array) {
  return { sha256: createHash("sha256").update(bytes).digest("hex"), integrity: `sha512-${createHash("sha512").update(bytes).digest("base64")}` };
}
export function manifestFor(tag: string, commit: string, metadata: any, bytes: Uint8Array) {
  validateIdentity(tag, commit, metadata);
  return { name: packageName, version: metadata.version as string, tag, commit, filename: `jaxxstorm-bun-tailscale-bridge-${metadata.version}.tgz`, ...digests(bytes) };
}
export type Manifest = ReturnType<typeof manifestFor>;
export function validateManifest(manifest: Manifest, expected: { tag: string; commit: string; sha256?: string; integrity?: string }, metadata: any, bytes: Uint8Array) {
  assert.deepEqual(manifest, manifestFor(expected.tag, expected.commit, metadata, bytes), "Candidate manifest or tarball mismatch");
  if (expected.sha256 !== undefined) assert.equal(manifest.sha256, expected.sha256, "Producing artifact SHA-256 mismatch");
  if (expected.integrity !== undefined) assert.equal(manifest.integrity, expected.integrity, "Producing artifact SRI mismatch");
}
export function registryState(status: number, body: any): { versions: Record<string, any>; latest?: string } {
  if (status === 404 && body?.error === "Not found") return { versions: {} };
  assert.equal(status, 200, "Registry lookup failed (not confirmed absent)");
  assert.equal(body?.name, packageName, "Malformed registry package");
  assert(body.versions && typeof body.versions === "object" && !Array.isArray(body.versions), "Malformed registry versions");
  assert(body["dist-tags"] && typeof body["dist-tags"] === "object" && !Array.isArray(body["dist-tags"]), "Malformed registry tags");
  const latest = body["dist-tags"].latest;
  if (latest !== undefined) {
    assert.equal(typeof latest, "string");
    stableVersion(latest);
    assert(body.versions[latest], "Latest version missing from registry metadata");
  } else assert.equal(Object.keys(body.versions).length, 0, "Existing package has no stable latest; owner review required");
  return { versions: body.versions, latest };
}
export function npmDecision(manifest: Manifest, state: ReturnType<typeof registryState>) {
  const existing = state.versions[manifest.version];
  if (existing !== undefined) {
    assert.equal(existing?.dist?.integrity, manifest.integrity, "Immutable npm version conflict");
    return "complete";
  }
  if (state.latest !== undefined) assert(compareVersions(manifest.version, state.latest) > 0, "New publication must advance stable latest");
  return "publish";
}
export function assetDecision(expected: Record<string, string>, existing: { name: string; sha256: string }[]) {
  const seen = new Set<string>();
  for (const asset of existing) {
    assert(!seen.has(asset.name), "Duplicate GitHub asset");
    seen.add(asset.name);
    assert.equal(asset.sha256, expected[asset.name], "Unexpected or conflicting GitHub asset");
  }
  return Object.keys(expected).filter((name) => !seen.has(name));
}
