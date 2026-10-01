import { expect, test } from "bun:test";
import { runReleaseCommand } from "../scripts/release-command";

const cwd = new URL("../", import.meta.url).pathname;
const execute = (code: string, env: NodeJS.ProcessEnv = {}) => runReleaseCommand([process.execPath, "-e", code], cwd, env);

test("successful release command preserves exact stdout for parsing", async () => {
  expect(await execute('process.stdout.write("  machine-readable\\n"); process.stderr.write("notice");')).toBe("  machine-readable\n");
});

test("failed release command reports exit status and both output streams", async () => {
  const error = await execute('console.error("npm error code E403\\nnpm error Publishing requires two-factor authentication"); console.log("publication not completed"); process.exit(7);').catch(error => error);
  expect(error).toBeInstanceOf(Error);
  expect(error.message).toContain("bun -e (exit 7)");
  expect(error.message).toContain("stderr:\n  npm error code E403");
  expect(error.message).toContain("Publishing requires two-factor authentication");
  expect(error.message).toContain("stdout:\n  publication not completed");
});

test("release diagnostics redact credentials and URLs before truncation", async () => {
  const env = { NPM_TOKEN: "synthetic+private/value", ACTIONS_ID_TOKEN_REQUEST_TOKEN: "synthetic-oidc" };
  const output = [
    "npm error code E401",
    env.NPM_TOKEN, encodeURIComponent(env.NPM_TOKEN), env.ACTIONS_ID_TOKEN_REQUEST_TOKEN,
    "npm_UnknownToken123", "ghp_UnknownToken456", "github_pat_Unknown_789",
    "Authorization: Bearer generated.jwt.secret", "Basic c3ludGhldGljOnNlY3JldA==",
    '_authToken="quoted private value"', '"password": "private password"', "token=generated-token",
    "https://user:pass@registry.example/path?token=private", "https://login.tailscale.com/a/private",
    "\x1b[31mcolored error\x1b[0m", "x".repeat(20_000), "must-be-truncated",
  ].join("\n");
  const error = await execute(`process.stderr.write(${JSON.stringify(output)}); process.exit(1);`, env).catch(error => error);
  expect(error.message).toContain("npm error code E401");
  expect(error.message).toContain("colored error");
  expect(error.message).toContain("[REDACTED]");
  expect(error.message).toContain("[output truncated]");
  for (const secret of [env.NPM_TOKEN, encodeURIComponent(env.NPM_TOKEN), env.ACTIONS_ID_TOKEN_REQUEST_TOKEN,
    "npm_UnknownToken123", "ghp_UnknownToken456", "github_pat_Unknown_789", "generated.jwt.secret",
    "c3ludGhldGljOnNlY3JldA==", "quoted private value", "private password", "generated-token",
    "https://", "must-be-truncated", "\x1b"]) expect(error.message).not.toContain(secret);
  expect(error.message.length).toBeLessThan(17_000);
});

test("empty failure output and spawn failures are actionable", async () => {
  await expect(execute("process.exit(2)")).rejects.toThrow("stderr:\n  (empty)\nstdout:\n  (empty)");
  await expect(runReleaseCommand(["/nonexistent-release-test-executable"], cwd, {})).rejects.toThrow("Release command could not start: nonexistent-release-test-executable");
});
