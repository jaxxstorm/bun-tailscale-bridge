import { expect, test } from "bun:test";

const verify: any = Bun.YAML.parse(await Bun.file(new URL("../.github/workflows/verify.yml", import.meta.url)).text());
const release: any = Bun.YAML.parse(await Bun.file(new URL("../.github/workflows/release.yml", import.meta.url)).text());
const pins = new Set([
  "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
  "oven-sh/setup-bun@735343b667d3e6f658f44d0eca948eb6282f2b76",
  "actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5",
  "actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020",
  "actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02",
  "actions/download-artifact@d3f86a106a0bac45b974a628896c90dbdf5c8093",
]);
test("all external actions are reviewed immutable pins and checkouts do not persist credentials", () => {
  for (const workflow of [verify, release]) for (const job of Object.values(workflow.jobs) as any[]) {
    if (!job.uses) expect(job["timeout-minutes"]).toBeGreaterThan(0);
    for (const step of job.steps ?? []) {
      if (step.uses) expect(pins.has(step.uses)).toBe(true);
      if (step.uses?.startsWith("actions/checkout@")) {
        expect(step.with["persist-credentials"]).toBe(false);
        expect(step.with.ref).toBeDefined();
      }
    }
  }
});
test("PR verification has no secrets, OIDC, publishing environment or target trigger", () => {
  expect(Object.keys(verify.on).sort()).toEqual(["pull_request", "push", "workflow_call"]);
  expect(verify.on.push.branches).toEqual(["main"]);
  expect(verify.permissions).toEqual({ contents: "read" });
  const text = JSON.stringify(verify);
  for (const forbidden of ["secrets.", "id-token", "pull_request_target", "npm publish", "GH_TOKEN", "NPM_TOKEN"]) expect(text).not.toContain(forbidden);
  for (const job of Object.values(verify.jobs) as any[]) {
    expect(job.environment).toBeUndefined();
    expect(job.permissions).toBeUndefined();
    expect(job.secrets).toBeUndefined();
  }
  expect(verify.jobs.verify.strategy.matrix.os).toEqual(["ubuntu-24.04", "macos-14"]);
  expect(verify.concurrency.group).toContain("github.event.pull_request.number || github.ref");
  expect(verify.jobs.required.name).toBe("Required Verification");
  expect(verify.jobs.required.needs).toEqual(["verify"]);
  expect(verify.jobs.required.if).toBe("${{ always() }}");
  expect(verify.jobs.required.steps[0].run).toBe('test "$RESULT" = success');
  for (const command of ["bun install --frozen-lockfile", "bun run typecheck", "bun test test", "go -C helper test -tags=ts_omit_webclient ./...", "go -C helper test -tags=ts_omit_webclient -race ./...", "bun run build:all", "bun run verify:package", "bun run verify:licenses", "bun run verify:helpers", "actionlint@v1.7.7"]) expect(text).toContain(command);
});
test("release gates, exact artifact identity and least privilege", () => {
  expect(release.permissions).toEqual({ contents: "read" });
  expect(release.on).toEqual({ push: { tags: ["v*"] } });
  expect(release.concurrency).toEqual({ group: "release-jaxxstorm-bun-tailscale-bridge", "cancel-in-progress": false });
  expect(release.jobs.verify.uses).toBe("./.github/workflows/verify.yml");
  expect(release.jobs.verify.permissions).toEqual({ contents: "read" });
  expect(release.jobs.verify.secrets).toBeUndefined();
  expect(release.jobs.verify.needs).toEqual(["preflight"]);
  expect(release.jobs.candidate.needs).toEqual(["preflight", "verify"]);
  expect(release.jobs["artifact-check"].strategy.matrix.os).toEqual(["ubuntu-24.04", "macos-14"]);
  expect(release.jobs.npm.needs).toEqual(["preflight", "candidate", "artifact-check"]);
  expect(release.jobs.github.needs).toEqual(["preflight", "candidate", "npm"]);
  for (const [name, job] of Object.entries(release.jobs) as [string, any][]) {
    if (["candidate", "artifact-check", "npm", "github"].includes(name)) expect(job.env.RELEASE_COMMIT).toBe("${{ needs.preflight.outputs.commit }}");
    expect(job.secrets).toBeUndefined();
    if (name === "npm") {
      expect(job.environment).toBe("npm");
      expect(job.permissions).toEqual({ contents: "read", "id-token": "write" });
    } else {
      expect(job.environment).toBeUndefined();
      expect(job.permissions ?? release.permissions).toEqual({ contents: name === "github" ? "write" : "read" });
    }
    for (const step of job.steps ?? []) {
      if (step.uses?.startsWith("actions/upload-artifact@")) {
        expect(step.with["retention-days"]).toBe(90);
        expect(step.with.overwrite).toBeUndefined();
      }
      if (step.uses?.startsWith("actions/download-artifact@")) {
        expect(step.with["artifact-ids"]).toBe("${{ needs.candidate.outputs.artifact }}");
        expect(step.with["run-id"]).toBeUndefined();
        expect(step.with["github-token"]).toBeUndefined();
      }
      if (["npm", "github"].includes(name)) for (const forbidden of ["bun install", "build:all", "pm pack", "--clobber"]) expect(step.run ?? "").not.toContain(forbidden);
      if (step.run) expect(step.run).not.toContain("${{ github.ref");
    }
  }
  const text = JSON.stringify(release);
  expect(text).not.toContain("secrets.");
  expect(text).not.toContain("NPM_TOKEN");
  expect(text).toContain("11.6.2");
  expect(text).not.toContain("npm install");
  expect(text).toContain("24.11.1");
});
