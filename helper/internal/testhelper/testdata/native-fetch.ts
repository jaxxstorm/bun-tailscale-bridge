// Test-only native Bun client. No Tailscale enrollment or agent dependency.
const [binary, plainPort, securePort] = process.argv.slice(2);
const child = Bun.spawn([binary!], { stdin: "pipe", stdout: "pipe", stderr: "pipe" });
const assert = (value: unknown) => { if (!value) throw new Error("native proxy assertion failed"); };
try {
  child.stdin.write(JSON.stringify({ type: "start", version: 1, options: {
    hostname: "native-test", ephemeral: true, startupTimeoutMs: 3000, interactive: false,
  } }) + "\n");
  const reader = child.stdout.getReader();
  const decoder = new TextDecoder();
  let line = "";
  while (!line.includes("\n")) {
    const chunk = await reader.read();
    assert(!chunk.done);
    line += decoder.decode(chunk.value, { stream: true });
  }
  const ready = JSON.parse(line.trim());
  const p = ready.httpProxy;
  assert(ready.type === "ready" && p.host === "127.0.0.1" && p.username === "tsnet");
  assert(p.port !== ready.proxy.port && p.password !== ready.proxy.password);
  const proxy = `http://${p.username}:${p.password}@${p.host}:${p.port}`;
  const url = `http://upstream.test:${plainPort}/a%2Fb?q=x%2Fy`;
  const headers = { Authorization: "Bearer synthetic-application-secret" };
  const response = await fetch(url, { proxy, method: "POST", headers, body: "body", redirect: "manual" });
  const body = await response.json();
  assert(body.method === "POST" && body.body === "body" && body.path === "/a%2Fb?q=x%2Fy");
  assert(body.host === `upstream.test:${plainPort}` && body.authorization === headers.Authorization && body.proxyAuthorization === "");
  const denied = await fetch(url, { proxy: `http://${p.host}:${p.port}`, redirect: "manual" });
  assert(denied.status === 407);
  await denied.arrayBuffer();
  const wrongCredential = await fetch(url, { proxy: `http://tsnet:${ready.proxy.password}@${p.host}:${p.port}` });
  assert(wrongCredential.status === 407);
  await wrongCredential.arrayBuffer();
  const tls = { ca: process.env.BRIDGE_TEST_CA! };
  const httpsURL = `https://upstream.test:${securePort}/secure`;
  let failed = false;
  try { await fetch(httpsURL, { proxy }); } catch { failed = true; }
  assert(failed);
  const secure = await fetch(httpsURL, { proxy, tls, headers });
  const secureBody = await secure.json();
  assert(secureBody.serverName === "upstream.test" && secureBody.proxyAuthorization === "");
  assert(secureBody.authorization === headers.Authorization);
  failed = false;
  try { await fetch(`https://localhost:${securePort}/secure`, { proxy, tls }); } catch { failed = true; }
  assert(failed);
  const abort = new AbortController();
  const stream = await fetch(`http://upstream.test:${plainPort}/stream`, { proxy, signal: abort.signal });
  const streamReader = stream.body!.getReader();
  const first = await streamReader.read();
  assert(new TextDecoder().decode(first.value).includes("data: first"));
  abort.abort();
  await streamReader.cancel().catch(() => {});
  const direct = await fetch(`http://127.0.0.1:${plainPort}/direct`);
  assert((await direct.json()).path === "/direct");
  child.stdin.write('{"type":"close","version":1}\n');
  assert(await child.exited === 0);
  assert(await new Response(child.stderr).text() === "");
  assert((await reader.read()).done);
  failed = false;
  try { await fetch(url, { proxy, signal: AbortSignal.timeout(1000) }); } catch { failed = true; }
  assert(failed);
} catch {
  console.error("native Bun HTTP/CONNECT verification failed");
  process.exitCode = 1;
} finally {
  child.stdin.end();
  if (child.exitCode === null) child.kill();
  await child.exited;
}
