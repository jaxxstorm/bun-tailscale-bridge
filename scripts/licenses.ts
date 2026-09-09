import assert from "node:assert/strict";
import { mkdtemp, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { createHash } from "node:crypto";
import { targets } from "./package-policy";

// Generated notices are reviewed and committed, never discovered during publication.
const root = resolve(import.meta.dir, "..");
assert(process.argv.length === 2 || (process.argv.length === 3 && process.argv[2] === "--check"), "Usage: licenses.ts [--check]");
const scratch = await mkdtemp(join(tmpdir(), "bridge-licenses-"));
async function run(cmd: string[], env = process.env) {
  const child = Bun.spawn(cmd, { cwd: join(root, "helper"), env, stdout: "pipe", stderr: "pipe" });
  const [out, err, code] = await Promise.all([new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited]);
  assert.equal(code, 0, "License tool failed; review dependency licenses locally");
  assert(!/Error identifying license|Unknown,Unknown/.test(out + err), "Unidentified license");
  return out;
}
try {
  assert((await run(["go", "version"])).startsWith("go version go1.26.2 "), "License inventory requires Go 1.26.2");
  const tool = join(scratch, "go-licenses");
  await run(["go", "install", "github.com/google/go-licenses/v2@v2.0.1"], { ...process.env, GOBIN: scratch });
  const texts = new Map<string, string>();
  const inventory: Record<string, unknown[]> = {};
  for (const target of targets) {
    const [GOOS, arch] = target.split("-");
    const report = await run([tool, "report", "--ignore", "github.com/jaxxstorm/bun-tailscale-bridge/helper", "--template", join(root, "scripts/license-report.tmpl"), "./cmd/bridge"], { ...process.env, GOFLAGS: "-tags=ts_omit_webclient", GOOS, GOARCH: arch === "x64" ? "amd64" : "arm64", CGO_ENABLED: "0" });
    assert(!report.includes("web-client-prebuilt"), "Web client reentered the dependency graph");
    inventory[target] = [];
    for (const line of report.trim().split("\n")) {
      const [name, version, path, license] = line.split("\t").map((value) => JSON.parse(value) as string);
      assert(name && version && path && license);
      assert(["MIT", "BSD-2-Clause", "BSD-3-Clause", "Apache-2.0", "ISC"].includes(license), `License review needed: ${name}`);
      const licenseText = await Bun.file(path).text();
      inventory[target]!.push({ name, version, license, sha256: createHash("sha256").update(licenseText).digest("hex") });
      texts.set(`${name} ${version} / ${path.split("/").at(-1)}`, licenseText);
      for (const file of await readdir(dirname(path))) {
        if (/^(NOTICE|COPYING|COPYRIGHT|AUTHORS)(\..*)?$/i.test(file)) {
          const candidate = Bun.file(join(dirname(path), file));
          if (await candidate.exists()) texts.set(`${name} ${version} / ${file}`, await candidate.text());
        }
      }
    }
    console.log(`License classification passed: ${target} (${inventory[target]!.length} entries)`);
  }
  const goroot = (await run(["go", "env", "GOROOT"])).trim();
  texts.set("Go 1.26.2 runtime and standard library / LICENSE", await Bun.file(join(goroot, "LICENSE")).text());
  const output = "Third-party notices for the four bundled Go helpers (ts_omit_webclient).\nThis project's MIT license does not relicense these components.\nGenerated with go-licenses v2.0.1; see docs/license-inventory.json.\nThe unused web UI and its JavaScript/font assets are not bundled.\n\n" + [...texts].sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0).map(([name, text]) => `===== ${name} =====\n${text.trim()}\n`).join("\n");
  for (const [file, text] of [["THIRD_PARTY_NOTICES.txt", output], ["docs/license-inventory.json", JSON.stringify(inventory, null, 2) + "\n"]] as const) {
    if (process.argv[2] === "--check") assert.equal(await Bun.file(join(root, file)).text(), text, `License inventory drift: ${file}; regenerate and review`);
    else await Bun.write(join(root, file), text);
  }
} finally {
  await rm(scratch, { recursive: true, force: true });
}
