import assert from "node:assert/strict";
import test from "node:test";
import { SpiderDashboardPort } from "../src/lib/dashboard.ts";
import { parseHeadersText } from "../src/lib/connectors.ts";
import { parseSelection, selectionValue } from "../src/lib/models.ts";

test("chat and approval requests preserve server versions and encode identifiers", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  const port = new SpiderDashboardPort(async (path, init) => { calls.push({ path, init }); return Response.json({ id: "run" }); });
  await port.send("a/b", 8, "查询", ["connector"]);
  await port.decide("a/b", 10, "approval", false);
  assert.equal(calls[0].path, "/conversations/a%2Fb/messages");
  assert.deepEqual(JSON.parse(calls[0].init!.body as string), { content: "查询", connector_ids: ["connector"], expected_version: 8 });
  assert.deepEqual(JSON.parse(calls[1].init!.body as string), { approval_id: "approval", expected_version: 10, approve: false });
});

test("editing connector credentials supports preserve, replace and explicit removal", async () => {
  const bodies: Record<string, unknown>[] = [];
  const port = new SpiderDashboardPort(async (_path, init) => { bodies.push(JSON.parse(init!.body as string)); return Response.json({}); });
  const input = { name: "Local MCP", url: "http://localhost:3000/mcp", description: "", enabled: true };
  await port.saveConnector("id", input);
  await port.saveConnector("id", { ...input, headers: {} });
  assert.equal("headers" in bodies[0], false);
  assert.deepEqual(bodies[1].headers, {});
});

test("transcription uploads audio instead of JSON and handles provider errors", async () => {
  const port = new SpiderDashboardPort(async (path, init) => {
    assert.equal(path, "/voice/transcriptions");
    assert.ok(init?.body instanceof FormData);
    const file = init.body.get("file") as File;
    assert.equal(file.name, "recording.mp4");
    assert.equal(await file.text(), "audio");
    return Response.json({ text: "你好 Spider" });
  });
  assert.equal(await port.transcribe(new Blob(["audio"], { type: "audio/mp4" })), "你好 Spider");
  const broken = new SpiderDashboardPort(async () => Response.json({ error: { message: "服务不可用" } }, { status: 502 }));
  await assert.rejects(() => broken.speech("你好"), /服务不可用/);
});

test("MCP header editor preserves embedded colons and rejects malformed names", () => {
  assert.deepEqual(parseHeadersText("Authorization: Bearer example:token\nX-Workspace: personal\n"), { Authorization: "Bearer example:token", "X-Workspace": "personal" });
  assert.throws(() => parseHeadersText("invalid line"), /格式/);
  assert.throws(() => parseHeadersText("Bad Header: value"), /名称/);
});

test("offline saving explains how to restore the service without exposing headers", async () => {
  const offline = new SpiderDashboardPort(async () => { throw new TypeError("Failed to fetch"); });
  await assert.rejects(() => offline.saveConnector(null, { name: "Local", url: "http://localhost:3000/mcp", description: "", enabled: true, headers: { Authorization: "Bearer hidden" } }), /make dev-dashboard/);
  const unavailable = new SpiderDashboardPort(async () => new Response("proxy failed", { status: 500 }));
  await assert.rejects(() => unavailable.connectors(), /保持终端运行/);
});

test("model editing preserves saved keys and selection uses separate capability slots", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  const port = new SpiderDashboardPort(async (path, init) => { calls.push({ path, init }); return Response.json({ models: ["remote-model"] }); });
  const input = { name: "My provider", base_url: "http://localhost:11434/v1", enabled: true, note: "", models: [{ id: "custom/model:v1", label: "Local", capability: "chat" as const, enabled: true }] };
  await port.saveModelProvider("a/b", input);
  await port.saveModelProvider("a/b", { ...input, api_key: "" });
  await port.selectModel("chat", { provider_id: "a/b", model_id: "custom/model:v1" });
  assert.deepEqual(await port.fetchModelIDs("a/b", input), ["remote-model"]);
  await port.testModel("a/b", "chat", "custom/model:v1", input);
  assert.equal(calls[0].path, "/models/providers/a%2Fb");
  assert.equal("api_key" in JSON.parse(calls[0].init!.body as string), false);
  assert.equal(JSON.parse(calls[1].init!.body as string).api_key, "");
  assert.deepEqual(JSON.parse(calls[2].init!.body as string), { capability: "chat", provider_id: "a/b", model_id: "custom/model:v1" });
  assert.deepEqual(JSON.parse(calls[4].init!.body as string), { provider_id: "a/b", provider: input, capability: "chat", model_id: "custom/model:v1" });
  const selected = { provider_id: "a/b", model_id: "custom/model:v1" };
  assert.deepEqual(parseSelection(selectionValue(selected)), selected);
});

test("memory transport sends typed RPC, asynchronous jobs and explicit import preview", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  const port = new SpiderDashboardPort(async (path, init) => { calls.push({ path, init }); return Response.json({ items: [], status: "running" }); });
  await port.memory("list_atoms", { query: "Python", offset: 30, include_deprecated: true });
  await port.startMemoryJob("extract", "chat/id");
  const file = new File(["fixture"], "memory.hmpkg");
  await port.importMemory(file, true, "skip");
  assert.equal(calls[0].path, "/memory/rpc");
  assert.deepEqual(JSON.parse(calls[0].init!.body as string), { method: "list_atoms", params: { query: "Python", offset: 30, include_deprecated: true } });
  assert.deepEqual(JSON.parse(calls[1].init!.body as string), { kind: "extract", session_id: "chat/id" });
  const form = calls[2].init!.body as FormData;
  assert.equal(form.get("dry_run"), "true"); assert.equal(form.get("on_conflict"), "skip"); assert.equal((form.get("pkg_file") as File).name, "memory.hmpkg");
});
