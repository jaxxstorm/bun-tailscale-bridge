import assert from "node:assert/strict";
import { assetDecision, compareVersions } from "./release-policy";

export async function githubResponse(response: Response, binary = false): Promise<any> {
  assert(response.ok, `GitHub API failed (${response.status}); no assumed absence`);
  return binary ? await response.bytes() : await response.json();
}

export function githubLatest(version: string, npmLatest: string | undefined, releases: { tag_name: string; draft: boolean; prerelease: boolean }[]) {
  return npmLatest === version && !releases.some((release) => !release.draft && !release.prerelease && /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(release.tag_name) && compareVersions(release.tag_name.slice(1), version) > 0);
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
