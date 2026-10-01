import assert from "node:assert/strict";
import { assetDecision, compareVersions } from "./release-policy";

export async function githubResponse(response: Response, binary = false): Promise<any> {
  assert(response.ok, `GitHub API failed (${response.status}); no assumed absence`);
  return binary ? await response.bytes() : await response.json();
}

export function githubLatest(version: string, npmLatest: string | undefined, releases: { tag_name: string; draft: boolean; prerelease: boolean }[]) {
  return npmLatest === version && !releases.some((release) => !release.draft && !release.prerelease && /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(release.tag_name) && compareVersions(release.tag_name.slice(1), version) > 0);
}

export async function ensureRelease(tag: string, releases: any[], create: () => Promise<any>) {
  const matching = releases.filter(release => release.tag_name === tag);
  assert(matching.length <= 1, "Duplicate GitHub releases for tag");
  // Use the POST response: a list immediately after creation may still be stale.
  const release = matching[0] ?? await create();
  assert(release && Number.isSafeInteger(release.id) && release.id > 0 &&
    release.tag_name === tag && release.prerelease === false && typeof release.draft === "boolean",
    "Conflicting or malformed GitHub release identity");
  if (matching.length === 0) assert(release.draft, "New GitHub release must remain a draft until assets are verified");
  return release;
}

// Keep upload/finalization ordering testable without any GitHub mutations in tests.
export async function deliverAssets(expected: Record<string, string>, operations: {
  inspect: () => Promise<{ name: string; sha256: string }[]>;
  upload: (name: string) => Promise<void>;
  finalize: () => Promise<void>;
}) {
  const missing = assetDecision(expected, await operations.inspect());
  for (const name of missing) await operations.upload(name);
  assert.equal(assetDecision(expected, await operations.inspect()).length, 0, "Incomplete GitHub delivery");
  await operations.finalize();
}
