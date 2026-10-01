import assert from "node:assert/strict";
import { appendFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { packageName, repository, registry } from "./package-policy";
import { digests, npmDecision, registryState, validateIdentity, validateSource } from "./release-policy";
import { packCandidate, checkCandidate } from "./candidate";
import { deliverAssets, ensureRelease, githubLatest, githubResponse } from "./github-delivery";
import { runReleaseCommand } from "./release-command";

const root = resolve(import.meta.dir, "..");
const run = (args: string[]) => runReleaseCommand(args, root);
function required(name: string) { const value = process.env[name]; assert(value, `Missing ${name}`); return value; }
const metadata = await Bun.file(join(root, "package.json")).json();
const mode = process.argv[2];
assert(process.argv.length === 3 && ["preflight", "pack", "check", "npm", "github"].includes(mode!), "Usage: release.ts preflight|pack|check|npm|github");
let commit = required("RELEASE_COMMIT");
const tag = required("RELEASE_TAG");
if (mode === "preflight") {
  assert.equal(commit, required("GITHUB_SHA"), "Preflight must start from the event object");
  assert(commit.length === 40 && /^[a-f0-9]{40}$/.test(commit), "Immutable event object required");
  // Both lightweight and annotated tags are bound to their immutable event object.
  commit = (await run(["git", "rev-parse", `${commit}^{commit}`])).trim();
}
validateIdentity(tag, commit, metadata);
async function source() {
  assert.equal(required("GITHUB_EVENT_NAME"), "push", "Only push events authorize publication");
  assert.equal(required("GITHUB_REF"), `refs/tags/${tag}`);
  await validateSource({ repository: required("GITHUB_REPOSITORY"), ref: required("GITHUB_REF"), commit, event: required("GITHUB_SHA") }, metadata, (args) => run(["git", ...args]));
}
async function output(values: Record<string, string>) {
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, Object.entries(values).map(([key, value]) => `${key}=${value}\n`).join(""));
}
const directory = resolve(process.env.CANDIDATE_DIR ?? join(root, "candidate"));
if (mode === "preflight") {
  await source();
  await output({ tag, commit });
} else {
  const filename = `jaxxstorm-bun-tailscale-bridge-${metadata.version}.tgz`;
  const tarball = join(directory, filename);
  if (mode === "pack") {
    await source();
    const manifest = await packCandidate(root, directory, tag, commit);
    await output({ sha256: manifest.sha256, integrity: manifest.integrity });
    console.log(`Packed candidate ${manifest.version}: ${manifest.sha256}`);
    process.exit(0);
  }
  const expected = { tag, commit, sha256: required("RELEASE_SHA256"), integrity: required("RELEASE_INTEGRITY") };
  const manifest = await checkCandidate(root, directory, expected);
  console.log(`Verified candidate ${manifest.version}: ${manifest.sha256}`);

  async function npmState() {
    const response = await fetch(`${registry}${encodeURIComponent(packageName)}`, { redirect: "error", signal: AbortSignal.timeout(30_000) });
    return registryState(response.status, await response.json());
  }
  if (mode === "npm") {
    assert.equal((await run(["node", "--version"])).trim(), "v24.11.1");
    assert.equal((await run(["npm", "--version"])).trim(), "11.6.2");
    await source();
    if (npmDecision(manifest, await npmState()) === "publish") {
      await source();
      await checkCandidate(root, directory, expected);
      await run(["npm", "publish", tarball, "--access=public", "--provenance", "--ignore-scripts", `--registry=${registry}`, "--tag=latest"]);
      assert.equal(npmDecision(manifest, await npmState()), "complete", "npm post-publish integrity check failed");
    }
    console.log("npm exact-version integrity confirmed; no existing version or dist-tag replaced");
  }
  if (mode === "github") {
    await source();
    const state = await npmState();
    assert.equal(npmDecision(manifest, state), "complete", "npm delivery must be complete first");
    const token = required("GH_TOKEN");
    async function api(path: string, method = "GET", body?: unknown, binary = false) {
      const response = await fetch(`https://api.github.com/repos/${repository}/${path}`, { method, headers: { Authorization: `Bearer ${token}`, Accept: binary ? "application/octet-stream" : "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28", ...(body ? { "Content-Type": "application/json" } : {}) }, ...(body ? { body: JSON.stringify(body) } : {}), signal: AbortSignal.timeout(60_000) });
      return githubResponse(response, binary);
    }
    async function releases() {
      const result: any[] = [];
      for (let page = 1; ; page++) {
        const batch = await api(`releases?per_page=100&page=${page}`);
        assert(Array.isArray(batch), "Malformed GitHub release response");
        for (const item of batch) assert(Number.isSafeInteger(item.id) && item.id > 0 && typeof item.tag_name === "string" && typeof item.draft === "boolean" && typeof item.prerelease === "boolean", "Malformed GitHub release identity");
        result.push(...batch);
        if (batch.length < 100) return result;
      }
    }
    const release = await ensureRelease(tag, await releases(), async () => {
      await source();
      return api("releases", "POST", {
        tag_name: tag, target_commitish: commit, name: tag,
        draft: true, prerelease: false, generate_release_notes: true,
      });
    });
    const hashes: Record<string, string> = {};
    for (const file of [filename, "SHA256SUMS", "release.json"]) hashes[file] = digests(new Uint8Array(await Bun.file(join(directory, file)).arrayBuffer())).sha256;
    async function inspectAssets() {
      const assets: any[] = await api(`releases/${release.id}/assets?per_page=100`);
      assert(Array.isArray(assets) && assets.length <= 3, "Unexpected release assets");
      const existing = [];
      for (const asset of assets) {
        assert(Number.isSafeInteger(asset.id) && asset.id > 0 && Object.hasOwn(hashes, asset.name) && asset.state === "uploaded", "Unknown or incomplete GitHub asset; owner review required");
        const data = await api(`releases/assets/${asset.id}`, "GET", undefined, true);
        existing.push({ name: asset.name, sha256: digests(data).sha256 });
      }
      return existing;
    }
    await deliverAssets(hashes, {
      inspect: inspectAssets,
      upload: async (file) => {
        await source();
        await checkCandidate(root, directory, expected);
        await run(["gh", "release", "upload", tag, join(directory, file), "--repo", repository]);
      },
      finalize: async () => {
        await source();
        await checkCandidate(root, directory, expected);
        if (!release.draft) return; // Matching published state is a no-op, including latest.
        const current = await npmState();
        assert.equal(npmDecision(manifest, current), "complete");
        const latest = githubLatest(manifest.version, current.latest, await releases());
        await api(`releases/${release.id}`, "PATCH", { draft: false, make_latest: latest ? "true" : "false" });
      },
    });
    console.log("GitHub Release assets verified without clobbering existing content");
  }
}
