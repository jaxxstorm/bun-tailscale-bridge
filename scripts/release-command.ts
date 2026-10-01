import { basename } from "node:path";
import { stripVTControlCharacters } from "node:util";

/** Keep successful stdout machine-readable; report bounded, redacted failures. */
export async function runReleaseCommand(args: string[], cwd: string, env: NodeJS.ProcessEnv = process.env) {
  function redact(text: string) {
    text = stripVTControlCharacters(text).replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "");
    const secrets = Object.entries(env)
      .filter(([name, value]) => value && /token|password|secret|credential|auth|(?:^|_)key$/i.test(name))
      .flatMap(([, value]) => [value!, encodeURIComponent(value!)])
      .sort((a, b) => b.length - a.length);
    for (const secret of secrets) text = text.replaceAll(secret, "[REDACTED]");
    return text
      .replace(/\b(?:npm_[A-Za-z0-9]+|gh[pousr]_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+)\b/g, "[REDACTED]")
      .replace(/\b(?:Bearer|Basic)\s+[A-Za-z0-9+/_=.-]+/gi, "[REDACTED AUTH]")
      .replace(/((?:["']?)(?:_authToken|_auth|authorization|token|password|secret|authKey|otp)(?:["']?)\s*[:=]\s*)(?:"[^"\n]*"|'[^'\n]*'|[^\s,;]+)/gi, "$1[REDACTED]")
      // URLs can carry credentials, OIDC request tokens, and enrollment links.
      .replace(/https?:\/\/[^\s"'<>]+/gi, "[REDACTED URL]");
  }
  function diagnostic(text: string) {
    const clean = redact(text).trim();
    const bounded = clean.length > 16_384 ? clean.slice(0, 16_384) + "\n[output truncated]" : clean;
    return bounded ? bounded.split("\n").map(line => `  ${line}`).join("\n") : "  (empty)";
  }
  // Do not echo arbitrary arguments: they can contain inline credentials.
  const command = redact([basename(args[0]!), ...args.slice(1, 2)].join(" "));
  let child;
  try {
    child = Bun.spawn(args, { cwd, env, stdout: "pipe", stderr: "pipe" });
  } catch (error) {
    throw new Error(`Release command could not start: ${command}\n${diagnostic(error instanceof Error ? error.message : "Spawn failed")}`);
  }
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
  ]);
  if (code !== 0) {
    throw new Error(`Release command failed: ${command} (exit ${code}${child.signalCode ? `, signal ${child.signalCode}` : ""})\nstderr:\n${diagnostic(stderr)}\nstdout:\n${diagnostic(stdout)}`);
  }
  return stdout;
}
