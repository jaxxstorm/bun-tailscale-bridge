import { expect, test } from "bun:test";
import { BridgeError, MAX_FRAME_BYTES, parseEvent } from "../src/protocol";
import fixtures from "../fixtures/protocol.json";

const ready = {
  type: "ready", version: 1,
  proxy: { host: "127.0.0.1", port: 1234, username: "tsnet", password: "a".repeat(32) },
  httpProxy: { host: "127.0.0.1", port: 1235, username: "tsnet", password: "b".repeat(32) },
} as const;
const frame = JSON.stringify(ready);

test("event validation preserves accepted schemas and escaped strings", () => {
  for (const event of [...fixtures.events, ready, { type: "error", version: 1, code: "UNSUPPORTED_RUNTIME" }]) {
    expect(parseEvent(JSON.stringify(event)) as unknown).toEqual(event);
  }
  expect(parseEvent(frame.replace('"version"', '"vers\\u0069on"'))).toEqual(ready);
  const event = { type: "auth_required", version: 1, url: 'https://login.test/{["key":"value",]}\\path' } as const;
  expect(parseEvent(JSON.stringify(event))).toEqual(event);
  expect(parseEvent(frame + " ".repeat(MAX_FRAME_BYTES - Buffer.byteLength(frame)))).toEqual(ready);
});

test("duplicate event keys are rejected before overwritten values can pass validation", () => {
  for (const line of [
    frame.replace('"version":1', '"version":1,"version":1'),
    frame.replace('"version":1', '"version":2,"version":1'),
    frame.replace('"version":1', '"vers\\u0069on":1,"version":1'),
    frame.replace('"proxy":', '"proxy":null,"proxy":'),
    frame.replace('"port":1234', '"port":0,"port":1234'),
    frame.replace('"port":1235', '"p\\u006frt":1235,"port":1235'),
    frame.replace('"host":', '"host" \t : "localhost", "host":'),
    '{"type":"error","version":1,"code":"synthetic-secret","code":"HELPER_FAILED"}',
    '{"type":"auth_required","version":1,"url":"http://login.test","url":"https://login.test"}',
  ]) {
    expect(() => parseEvent(line)).toThrow(new BridgeError("PROTOCOL_ERROR"));
  }
});

test("invalid schemas, excessive nesting, and frame sizes remain rejected", () => {
  for (const line of [
    "null", "[]", "{}", frame + "{}", frame.slice(0, -1),
    JSON.stringify({ ...ready, extra: true }),
    JSON.stringify({ ...ready, proxy: { ...ready.proxy, extra: true } }),
    JSON.stringify({ ...ready, httpProxy: { ...ready.httpProxy, port: ready.proxy.port } }),
    JSON.stringify({ ...ready, httpProxy: { ...ready.httpProxy, password: ready.proxy.password } }),
    '{"type":"error","version":1,"code":"toString"}',
    '['.repeat(1000) + '{}' + ']'.repeat(1000),
    frame + " ".repeat(MAX_FRAME_BYTES + 1 - Buffer.byteLength(frame)),
  ]) {
    expect(() => parseEvent(line)).toThrow(new BridgeError("PROTOCOL_ERROR"));
  }
});
