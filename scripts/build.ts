import { chmod, mkdir, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const all = process.argv.slice(2);
if (all.some((arg) => arg !== "--all")) throw new Error("Usage: bun scripts/build.ts [--all]");
if (Bun.version !== "1.4.2") throw new Error("Build requires Bun 1.4.2");
const version = Bun.spawnSync(["go", "version"], { cwd: root });
if (version.exitCode !== 0 || !version.stdout.toString().startsWith("go version go1.26.2 ")) {
  throw new Error("Build requires Go 1.26.2");
}
const targets = ["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"];
const selected = all.includes("--all") ? targets : [`${process.platform}-${process.arch}`];
if (selected.some((target) => !targets.includes(target))) throw new Error("Unsupported build host");

await rm(join(root, "dist"), { recursive: true, force: true });
await mkdir(join(root, "bin"), { recursive: true });
const result = await Bun.build({
  entrypoints: [join(root, "src/index.ts")],
  outdir: join(root, "dist"),
  target: "bun",
  format: "esm",
});
if (!result.success) throw new AggregateError(result.logs, "Bun build failed");
const types = Bun.spawn([process.execPath, join(root, "node_modules/typescript/bin/tsc"), "-p", "tsconfig.build.json"], {
  cwd: root, stdout: "inherit", stderr: "inherit",
});
if (await types.exited !== 0) throw new Error("Declaration build failed (install pinned dependencies first)");
for (const target of selected) {
  const [platform, arch] = target.split("-");
  const output = join(root, "bin", `bridge-${target}`);
  const child = Bun.spawn(["go", "-C", "helper", "build", "-tags=ts_omit_webclient", "-trimpath", "-ldflags", "-s -w", "-o", output, "./cmd/bridge"], {
    cwd: root,
    env: { ...process.env, GOFLAGS: "", GOOS: platform!, GOARCH: arch === "x64" ? "amd64" : "arm64", CGO_ENABLED: "0", GOTOOLCHAIN: "local" },
    stdout: "inherit", stderr: "inherit",
  });
  if (await child.exited !== 0) throw new Error(`Helper build failed: ${target}`);
  await chmod(output, 0o755);
  console.log(`Built ${target} (compilation only, not runtime validation)`);
}
