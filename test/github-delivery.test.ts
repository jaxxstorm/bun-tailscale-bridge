import { expect, test } from "bun:test";
import { deliverAssets, githubLatest, githubResponse } from "../scripts/github-delivery";

test("GitHub outages, auth failures, missing responses and malformed JSON fail closed", async () => {
  for (const status of [401, 403, 404, 429, 500, 503]) await expect(githubResponse(new Response('{"message":"Not Found"}', { status }))).rejects.toThrow();
  await expect(githubResponse(new Response("not JSON"))).rejects.toThrow();
  expect(await githubResponse(Response.json([]))).toEqual([]);
  expect(await githubResponse(new Response(new Uint8Array([1, 2])), true)).toEqual(new Uint8Array([1, 2]));
});

test("partial upload never finalizes; retry adds missing assets, conflict never clobbers", async () => {
  const hashes = { "package.tgz": "aaa", SHA256SUMS: "bbb", "release.json": "ccc" };
  const existing: { name: string; sha256: string }[] = [];
  const uploaded: string[] = [];
  let finalized = 0, fail = true;
  const operations = {
    inspect: async () => [...existing],
    upload: async (name: string) => {
      if (fail && name === "SHA256SUMS") throw new Error("fixture upload outage");
      uploaded.push(name);
      existing.push({ name, sha256: hashes[name as keyof typeof hashes] });
    },
    finalize: async () => { finalized++; },
  };
  await expect(deliverAssets(hashes, operations)).rejects.toThrow();
  expect(finalized).toBe(0);
  expect(uploaded).toEqual(["package.tgz"]);
  fail = false;
  await deliverAssets(hashes, operations);
  expect(finalized).toBe(1);
  expect(uploaded).toEqual(Object.keys(hashes));
  await deliverAssets(hashes, operations);
  expect(uploaded).toEqual(Object.keys(hashes));
  existing[0]!.sha256 = "conflict";
  await expect(deliverAssets(hashes, operations)).rejects.toThrow();
  expect(finalized).toBe(2);
  expect(uploaded).toEqual(Object.keys(hashes));
});

test("incomplete post-upload state or inspection outage cannot finalize", async () => {
  let finalized = false;
  for (const inspect of [async () => [], async () => { throw new Error("fixture API outage"); }]) {
    await expect(deliverAssets({ a: "hash" }, { inspect, upload: async () => {}, finalize: async () => { finalized = true; } })).rejects.toThrow();
    expect(finalized).toBe(false);
  }
});

test("older recovery never marks a GitHub release latest", () => {
  expect(githubLatest("0.1.0", "0.2.0", [])).toBe(false);
  expect(githubLatest("0.1.0", undefined, [])).toBe(false);
  expect(githubLatest("0.1.0", "0.1.0", [])).toBe(true);
  const newer = { tag_name: "v0.2.0", draft: false, prerelease: false };
  expect(githubLatest("0.1.0", "0.1.0", [newer])).toBe(false);
  expect(githubLatest("0.1.0", "0.1.0", [{ ...newer, draft: true }])).toBe(true);
  expect(githubLatest("0.1.0", "0.1.0", [{ ...newer, prerelease: true }])).toBe(true);
});
