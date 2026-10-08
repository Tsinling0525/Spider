import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import App from "../../src/App.svelte";
import Composer from "../../src/shared/Composer.svelte";
import { renderMarkdown } from "../../src/lib/markdown";
import type { Connector, Conversation, DashboardPort } from "../../src/lib/dashboard";
import { emptyModelConfiguration, selectionValue, type ModelConfiguration, type ModelProvider } from "../../src/lib/models";
import { cloudModelPresets } from "../../src/lib/model-presets";
import { builtinConnectorPresets } from "../../src/lib/builtin-connectors";
import { locale, setLocale, translate } from "../../src/lib/i18n";
import { english } from "../../src/lib/translations";
import MemoryMaintenance from "../../src/shared/MemoryMaintenance.svelte";

const connector: Connector = { id: "local", name: "我的 MCP", url: "http://localhost:3000/mcp", description: "自己的工具", enabled: true, has_headers: true, tools: [{ name: "lookup", description: "查找", inputSchema: { type: "object" } }], checked_at: "2026-10-07T00:00:00Z" };
const fresh: Conversation = { id: "chat", title: "新对话", messages: [], connector_ids: [], status: "idle", version: 1, created_at: "2026-10-07T00:00:00Z", updated_at: "2026-10-07T00:00:00Z" };

function fakePort(overrides: Partial<DashboardPort> = {}): DashboardPort {
  return {
    capabilities: vi.fn().mockResolvedValue({ chat: true, transcription: true, speech: true }),
    connectors: vi.fn().mockResolvedValue([connector]),
    saveConnector: vi.fn().mockResolvedValue(connector),
    deleteConnector: vi.fn().mockResolvedValue(undefined),
    probeConnector: vi.fn().mockResolvedValue(connector),
    saveBuiltinConnector: vi.fn().mockResolvedValue(connector), testBuiltinConnector: vi.fn(), startConnectorOAuth: vi.fn(), connectorOAuthStatus: vi.fn(),
    conversations: vi.fn().mockResolvedValue([]),
    conversation: vi.fn().mockResolvedValue(fresh),
    createConversation: vi.fn().mockResolvedValue(fresh),
    deleteConversation: vi.fn().mockResolvedValue(undefined),
    send: vi.fn().mockResolvedValue({ ...fresh, version: 3, messages: [{ role: "user", content: "你好" }, { role: "assistant", content: "你好，我是 Spider。" }] }),
    decide: vi.fn().mockResolvedValue(fresh),
    transcribe: vi.fn().mockResolvedValue("转写内容"),
    speech: vi.fn().mockResolvedValue(new Blob()),
    models: vi.fn().mockResolvedValue(emptyModelConfiguration()),
    saveModelProvider: vi.fn(), deleteModelProvider: vi.fn(),
    selectModel: vi.fn().mockResolvedValue(emptyModelConfiguration()),
    fetchModelIDs: vi.fn().mockResolvedValue([]), testModel: vi.fn(),
    memory: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }), memoryConfig: vi.fn().mockResolvedValue({ memory_enabled: true, extract_on_session_end: true, extract_trigger_mode: "idle", extract_idle_seconds: 300, extract_interval_seconds: 21600, aux_model: "" }), saveMemoryConfig: vi.fn(), memoryStatus: vi.fn().mockResolvedValue({ status: "idle" }), startMemoryJob: vi.fn(), exportMemory: vi.fn(), importMemory: vi.fn(), doctorMemory: vi.fn(),
    ...overrides,
  };
}

beforeAll(() => {
  // Node 26 exposes its own localStorage accessor. Use an isolated browser
  // preference store so tests never depend on the host's --localstorage-file.
  const values = new Map<string, string>();
  const storage: Storage = {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, value); },
  };
  Object.defineProperty(globalThis, "localStorage", { value: storage, configurable: true });
  Object.defineProperty(window, "localStorage", { value: storage, configurable: true });
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
});
afterEach(() => { cleanup(); localStorage.clear(); locale.set("zh-CN"); vi.restoreAllMocks(); });

describe("interface language", () => {
  it("switches the settings and navigation immediately, persists the choice and restores it on reload", async () => {
    const port = fakePort();
    const app = render(App, { port });
    await screen.findByText("本地服务已连接");
    await fireEvent.click(screen.getByRole("button", { name: "应用设置" }));
    expect((screen.getByRole("combobox", { name: "语言" }) as HTMLSelectElement).value).toBe("zh-CN");
    await fireEvent.change(screen.getByRole("combobox", { name: "语言" }), { target: { value: "en" } });
    expect(await screen.findByRole("heading", { name: "App settings" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Models & voice" })).toBeTruthy();
    expect(screen.getByRole("switch", { name: "Auto read aloud" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Switch to dark mode" })).toBeTruthy();
    expect(localStorage.getItem("spider.language")).toBe("en");
    expect(document.documentElement.lang).toBe("en");
    expect(document.title).toBe("Spider · My AI workspace");
    app.unmount();
    locale.set("zh-CN");
    render(App, { port });
    expect(await screen.findByRole("button", { name: "App settings" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "App settings" }));
    expect((screen.getByRole("combobox", { name: "Language" }) as HTMLSelectElement).value).toBe("en");
    await fireEvent.change(screen.getByRole("combobox", { name: "Language" }), { target: { value: "zh-CN" } });
    expect(await screen.findByRole("heading", { name: "应用设置" })).toBeTruthy();
    expect(document.documentElement.lang).toBe("zh-CN");
    expect(localStorage.getItem("spider.language")).toBe("zh-CN");
  });

  it("keeps conversation content, connector names and an unsent draft unchanged when switching", async () => {
    const conversation = { ...fresh, title: "连接器", messages: [{ role: "assistant" as const, content: "应用设置" }] };
    localStorage.setItem("spider.last-conversation", fresh.id);
    const port = fakePort({ conversations: vi.fn().mockResolvedValue([conversation]), conversation: vi.fn().mockResolvedValue(conversation) });
    render(App, { port });
    await screen.findByRole("button", { name: "复制回复" });
    await fireEvent.input(screen.getByRole("textbox", { name: "消息" }), { target: { value: "我的中文草稿 {0}" } });
    await fireEvent.click(screen.getByRole("button", { name: "应用设置" }));
    await fireEvent.change(screen.getByRole("combobox", { name: "语言" }), { target: { value: "en" } });
    await fireEvent.click(screen.getByRole("button", { name: "Chat" }));
    expect((screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement).value).toBe("我的中文草稿 {0}");
    expect(screen.getByText("应用设置")).toBeTruthy();
    expect(screen.getByRole("button", { name: "连接器" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy reply" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Select connectors" }));
    expect(screen.getByText("我的 MCP")).toBeTruthy();
    expect(port.send).not.toHaveBeenCalled();
    expect(port.saveConnector).not.toHaveBeenCalled();
  });

  it("localizes connector presets, model dialogs and memory filters", async () => {
    localStorage.setItem("spider.language", "en");
    const port = fakePort();
    render(App, { port });
    await screen.findByText("Local service connected");
    await fireEvent.click(screen.getByRole("button", { name: /^Connectors\s*1$/ }));
    await fireEvent.click(screen.getByRole("tab", { name: "Built-in connectors" }));
    expect(screen.getByRole("heading", { name: "Tencent Meeting" })).toBeTruthy();
    expect(screen.getByText("5 supported connectors, 0 configured")).toBeTruthy();
    await fireEvent.input(screen.getByRole("textbox", { name: "Search connectors" }), { target: { value: "Baidu" } });
    expect(screen.getByRole("heading", { name: "Baidu Maps" })).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "Tencent Meeting" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Connect Baidu Maps" }));
    expect(screen.getByRole("textbox", { name: "Connector name" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save connector" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await fireEvent.click(screen.getByRole("button", { name: "Models & voice" }));
    expect(screen.getByRole("heading", { name: "Chat model" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Add provider" }));
    expect(screen.getByRole("heading", { name: "Add Chat provider" })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Provider name" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Close model configuration" }));
    // Open a list immediately so the fake overview response is not used as chart data.
    port.memory = vi.fn().mockImplementation(async (method) => method === "stats_counts" ? { raw_events: 0, atoms: 0, entities: 0, episodes: 0, candidates_pending: 0, dirty_pages: 0 } : method.startsWith("stats_") ? { series: [] } : { items: [], total: 0, has_more: false });
    await fireEvent.click(screen.getByRole("button", { name: "Memory" }));
    await fireEvent.click(screen.getByRole("tab", { name: "Atomic memories" }));
    expect(screen.getByRole("textbox", { name: "Search memories" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Minimum importance" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Filter" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Add memory" }));
    expect(screen.getByRole("dialog", { name: "Add memory" })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "Memory content" })).toBeTruthy();
    expect(port.saveModelProvider).not.toHaveBeenCalled();
    expect(port.memory).not.toHaveBeenCalledWith("create_atom", expect.anything());
  });

  it("falls back to Chinese for an invalid saved language and still switches when storage is unavailable", async () => {
    localStorage.setItem("spider.language", "unknown");
    render(App, { port: fakePort() });
    await screen.findByRole("button", { name: "应用设置" });
    await fireEvent.click(screen.getByRole("button", { name: "应用设置" }));
    vi.spyOn(localStorage, "setItem").mockImplementation(() => { throw new Error("Storage unavailable"); });
    await fireEvent.change(screen.getByRole("combobox", { name: "语言" }), { target: { value: "en" } });
    expect(await screen.findByRole("heading", { name: "App settings" })).toBeTruthy();
  });

  it("synchronizes a language preference changed in another tab", async () => {
    render(App, { port: fakePort() });
    await screen.findByRole("button", { name: "应用设置" });
    localStorage.setItem("spider.language", "en");
    window.dispatchEvent(Object.assign(new Event("storage"), {
      storageArea: localStorage, key: "spider.language", newValue: "en",
    }));
    expect(await screen.findByRole("button", { name: "App settings" })).toBeTruthy();
    localStorage.removeItem("spider.language");
    window.dispatchEvent(Object.assign(new Event("storage"), {
      storageArea: localStorage, key: "spider.language", newValue: null,
    }));
    expect(await screen.findByRole("button", { name: "应用设置" })).toBeTruthy();
  });

  it("updates maintenance summaries and preserves interpolated content across language changes", async () => {
    render(MemoryMaintenance, { job: { id: "job", kind: "extract", status: "done", result: { results: [{ candidates: 3, promotion: { promoted: 2 }, episodes: { extracted: 1 } }] } } });
    expect(screen.getByText("处理 1 个会话，提取 3 条候选，确认 2 条原子记忆，记录 1 个情景。")).toBeTruthy();
    setLocale("en");
    expect(await screen.findByText("Memory maintenance complete")).toBeTruthy();
    expect(screen.getByText("Processed 1 conversations, extracted 3 candidates, confirmed 2 atomic memories and recorded 1 episodes.")).toBeTruthy();
    expect(translate("en", "连接 {0}", { 0: "应用设置 {1}" })).toBe("Connect 应用设置 {1}");
    expect(translate("en", "constructor")).toBe("constructor");
    // Every translated sentence keeps the same substitution slots in both languages.
    for (const [source, translated] of Object.entries(english)) {
      expect(translated.match(/\{\d+\}/g)?.sort() ?? []).toEqual(source.match(/\{\d+\}/g)?.sort() ?? []);
    }
  });
});

describe("dashboard user journeys", () => {
  it("shows the five copied built-in connector cards and scopes the custom tab", async () => {
    const port = fakePort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("tab", { name: "内置连接器" }));
    for (const preset of builtinConnectorPresets) {
      expect(screen.getByRole("button", { name: `连接 ${preset.name}` })).toBeTruthy();
      expect(screen.getByText(preset.description)).toBeTruthy();
    }
    expect(screen.queryByText("我的 MCP")).toBeNull();
    await fireEvent.keyDown(screen.getByRole("tab", { name: "内置连接器" }), { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "自定义连接器" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.getByText("我的 MCP")).toBeTruthy();
  });

  it.each(builtinConnectorPresets.filter((preset) => preset.authKind === "token" || preset.authKind === "api_key"))("tests and saves $name with its copied credential flow", async (preset) => {
    const port = fakePort({ testBuiltinConnector: vi.fn().mockResolvedValue({ ok: true, message: "连接成功，发现 3 个工具", tools: [] }) });
    render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("tab", { name: "内置连接器" }));
    await fireEvent.click(screen.getByRole("button", { name: `连接 ${preset.name}` }));
    expect((screen.getByRole("textbox", { name: "连接器名称" }) as HTMLInputElement).value).toBe(preset.name);
    const key = preset.authKind === "token" ? "token" : "api_key";
    await fireEvent.input(screen.getByLabelText(preset.kind === "didi" ? "API Key" : "Token"), { target: { value: "builtin-fixture-secret" } });
    await fireEvent.click(screen.getByRole("button", { name: "测试连接", exact: true }));
    expect(await screen.findByRole("status")).toHaveProperty("textContent", expect.stringContaining("连接成功"));
    expect(port.saveBuiltinConnector).not.toHaveBeenCalled();
    await fireEvent.submit(screen.getByRole("button", { name: "保存连接器" }).closest("form")!);
    await waitFor(() => expect(port.saveBuiltinConnector).toHaveBeenCalledWith(null, expect.objectContaining({ kind: preset.kind, name: preset.name, credentials: { [key]: "builtin-fixture-secret" } })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.queryByText("builtin-fixture-secret")).toBeNull();
  });

  it("saves a personal mailbox with provider, address, authorization code and custom TLS hosts", async () => {
    const port = fakePort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("tab", { name: "内置连接器" }));
    await fireEvent.click(screen.getByRole("button", { name: "连接 个人邮箱" }));
    await fireEvent.change(screen.getByRole("combobox", { name: "邮箱服务商" }), { target: { value: "custom" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "邮箱地址" }), { target: { value: "me@example.com" } });
    await fireEvent.input(screen.getByLabelText("授权码"), { target: { value: "mail-fixture-secret" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "IMAP 服务器" }), { target: { value: "imap.example.com" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "SMTP 服务器" }), { target: { value: "smtp.example.com" } });
    await fireEvent.input(screen.getByRole("spinbutton", { name: "IMAP 端口" }), { target: { value: "1993" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存连接器" }).closest("form")!);
    await waitFor(() => expect(port.saveBuiltinConnector).toHaveBeenCalledWith(null, expect.objectContaining({ kind: "qq-mail", credentials: { mail_provider: "custom", email: "me@example.com", password: "mail-fixture-secret", imap_host: "imap.example.com", smtp_host: "smtp.example.com", imap_port: "1993", smtp_port: "587" } })));
  });

  it("keeps a saved mailbox authorization code on edit and toggles the built-in endpoint", async () => {
    const mailbox: Connector = { ...connector, id: "mail", name: "我的邮箱", kind: "qq-mail", has_headers: false, has_credentials: true, credentials: { email: "me@gmail.com", mail_provider: "gmail" } };
    const port = fakePort({ connectors: vi.fn().mockResolvedValue([mailbox]) }); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("button", { name: "我的邮箱 操作" }));
    await fireEvent.click(screen.getByRole("button", { name: "编辑配置" }));
    expect((screen.getByLabelText("授权码") as HTMLInputElement).value).toBe("");
    await fireEvent.submit(screen.getByRole("button", { name: "保存连接器" }).closest("form")!);
    await waitFor(() => expect(port.saveBuiltinConnector).toHaveBeenCalledWith("mail", expect.objectContaining({ credentials: { email: "me@gmail.com", mail_provider: "gmail" } })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await fireEvent.click(screen.getByRole("button", { name: "我的邮箱 操作" }));
    await fireEvent.click(screen.getByRole("button", { name: "停用" }));
    await waitFor(() => expect(port.saveBuiltinConnector).toHaveBeenLastCalledWith("mail", expect.objectContaining({ kind: "qq-mail", enabled: false })));
    expect(port.saveConnector).not.toHaveBeenCalled();
  });

  it("authorizes Notion through a popup and refreshes/probes the saved connector", async () => {
    let complete = false;
    const popup = { location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(popup as unknown as Window);
    const port = fakePort({ startConnectorOAuth: vi.fn().mockResolvedValue({ state: "oauth-fixture-state", authorize_url: "https://mcp.notion.com/authorize?state=oauth-fixture-state" }), connectorOAuthStatus: vi.fn().mockImplementation(async () => ({ status: complete ? "authorized" : "pending", connector_id: "notion-fixture", error: "" })) });
    render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("tab", { name: "内置连接器" }));
    await fireEvent.click(screen.getByRole("button", { name: "连接 Notion" }));
    await fireEvent.click(screen.getByRole("button", { name: "一键授权" }));
    await waitFor(() => expect(popup.location.href).toContain("https://mcp.notion.com/authorize"));
    expect(port.startConnectorOAuth).toHaveBeenCalledWith(null, expect.objectContaining({ kind: "notion", credentials: {} }), expect.stringContaining("/spider-api/v1/dashboard/connectors/oauth/callback"));
    complete = true;
    await fireEvent(window, new MessageEvent("message", { origin: window.location.origin, source: popup as unknown as Window, data: { type: "spider-connector-oauth", state: "oauth-fixture-state" } }));
    await waitFor(() => expect(port.probeConnector).toHaveBeenCalledWith("notion-fixture"));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(port.saveBuiltinConnector).not.toHaveBeenCalled();
  });

  it("sends a real port request with selected connectors and renders the response", async () => {
    const port = fakePort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "选择连接器" }));
    await fireEvent.click(screen.getByRole("button", { name: /我的 MCP.*1 个工具/ }));
    await fireEvent.input(screen.getByRole("textbox", { name: "消息" }), { target: { value: "你好" } });
    await fireEvent.click(screen.getByRole("button", { name: "发送消息" }));
    await waitFor(() => expect(port.send).toHaveBeenCalledWith("chat", 1, "你好", ["local"]));
    expect(await screen.findByText("你好，我是 Spider。")).toBeTruthy();
  });

  it("never executes a tool just by displaying a pending approval", async () => {
    const pending: Conversation = { ...fresh, title: "查询记录", connector_ids: ["local"], status: "waiting_approval", version: 7, messages: [{ role: "user", content: "查询记录" }], pending: { id: "approval", connector_id: "local", connector_name: "我的 MCP", url: connector.url, tool: "lookup", arguments: { query: "example" } } };
    localStorage.setItem("spider.last-conversation", "chat");
    const port = fakePort({ conversations: vi.fn().mockResolvedValue([pending]), conversation: vi.fn().mockResolvedValue(pending) });
    render(App, { port });
    expect(await screen.findByText("等待你的确认")).toBeTruthy();
    expect(port.decide).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "拒绝" }));
    await waitFor(() => expect(port.decide).toHaveBeenCalledExactlyOnceWith("chat", 7, "approval", false));
  });

  it("creates a connector while keeping credentials out of the list", async () => {
    const port = fakePort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: /^连接器/ }));
    await fireEvent.click(screen.getByRole("button", { name: "添加连接器" }));
    await fireEvent.input(screen.getByRole("textbox", { name: "名称" }), { target: { value: "私人 MCP" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "MCP 服务地址" }), { target: { value: "http://localhost:4000/mcp" } });
    await fireEvent.input(screen.getByRole("textbox", { name: /请求头/ }), { target: { value: "Authorization: Bearer example" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存连接器" }).closest("form")!);
    await waitFor(() => expect(port.saveConnector).toHaveBeenCalledWith(null, { name: "私人 MCP", url: "http://localhost:4000/mcp", description: "", enabled: true, headers: { Authorization: "Bearer example" } }));
    expect(screen.queryByText("Bearer example")).toBeNull();
  });

  it("shows model configuration requirements and prevents unusable voice controls", async () => {
    const port = fakePort({ capabilities: vi.fn().mockResolvedValue({ chat: false, transcription: false, speech: false }) });
    render(App, { port });
    expect(await screen.findByText("配置聊天模型后即可开始对话。")).toBeTruthy();
    expect((screen.getByRole("button", { name: "开始录音" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "自动朗读回复" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("saves an unsaved provider draft with separate STT and TTS models", async () => {
    const port = fakePort({ testModel: vi.fn().mockResolvedValue({ ok: true, message: "模型接口调用成功", latency_ms: 12 }), fetchModelIDs: vi.fn().mockResolvedValue(["my-stt"]) });
    render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "模型与语音" }));
    await fireEvent.click(screen.getByRole("tab", { name: "语音模型" }));
    await fireEvent.click(screen.getByRole("button", { name: "添加供应商" }));
    await fireEvent.input(screen.getByRole("textbox", { name: "供应商名称" }), { target: { value: "My models" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "API Base URL" }), { target: { value: "https://example.com/v1" } });
    await fireEvent.input(screen.getByLabelText(/API Key/), { target: { value: "fixture-key" } });
    await fireEvent.click(screen.getByRole("button", { name: "读取模型列表" }));
    expect(await screen.findByText(/已读取 1 个模型/)).toBeTruthy();
    await fireEvent.input(screen.getByLabelText("模型 ID 1"), { target: { value: "my-stt" } });
    await fireEvent.click(screen.getByRole("button", { name: "测试语音输入模型" }));
    await waitFor(() => expect(port.testModel).toHaveBeenCalledWith(null, "stt", "my-stt", expect.objectContaining({ api_key: "fixture-key", group: "voice" })));
    expect(await screen.findByText(/模型接口调用成功/)).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "添加模型" }));
    await fireEvent.input(screen.getByLabelText("模型 ID 2"), { target: { value: "my-tts" } });
    await fireEvent.change(screen.getByRole("combobox", { name: "能力 2" }), { target: { value: "tts" } });
    await fireEvent.input(screen.getByRole("textbox", { name: "音色 2" }), { target: { value: "custom-voice" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(port.saveModelProvider).toHaveBeenCalledWith(null, expect.objectContaining({ name: "My models", api_key: "fixture-key", group: "voice", models: [
      { id: "my-stt", label: "my-stt", capability: "stt", enabled: true, voice: undefined },
      { id: "my-tts", label: "my-tts", capability: "tts", enabled: true, voice: "custom-voice" },
    ] })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("edits a saved provider without reading its key and switches the chat model", async () => {
    const provider: ModelProvider = { id: "provider", name: "Local", base_url: "http://localhost:11434/v1", has_api_key: true, enabled: true, note: "", models: [{ id: "chat-a", label: "A", capability: "chat", enabled: true }, { id: "chat-b", label: "B", capability: "chat", enabled: true }] };
    const configuration: ModelConfiguration = { ...emptyModelConfiguration(), providers: [provider] };
    configuration.active.chat = { provider_id: "provider", model_id: "chat-a" };
    const next: ModelConfiguration = { ...configuration, active: { ...configuration.active, chat: { provider_id: "provider", model_id: "chat-b" } } };
    const models = vi.fn().mockResolvedValue(configuration);
    const port = fakePort({ models, selectModel: vi.fn().mockImplementation(async () => { models.mockResolvedValue(next); return next; }) });
    render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.change(screen.getByRole("combobox", { name: "聊天模型" }), { target: { value: selectionValue(next.active.chat) } });
    await waitFor(() => expect(port.selectModel).toHaveBeenCalledWith("chat", next.active.chat));
    await fireEvent.click(screen.getByRole("button", { name: "模型与语音" }));
    expect((screen.getByRole("combobox", { name: "当前聊天模型" }) as HTMLSelectElement).value).toBe(selectionValue(next.active.chat));
    await fireEvent.click(screen.getByRole("button", { name: "编辑 Local" }));
    expect((screen.getByLabelText(/API Key/) as HTMLInputElement).value).toBe("");
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(port.saveModelProvider).toHaveBeenCalledWith("provider", expect.not.objectContaining({ api_key: expect.anything() })));
  });

  it.each(cloudModelPresets)("configures the $name preset with its protocol and editable models", async (preset) => {
    const port = fakePort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "模型与语音" }));
    for (const item of cloudModelPresets) expect(screen.getByRole("button", { name: `配置 ${item.name}` })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: `配置 ${preset.name}` }));
    expect((screen.getByRole("textbox", { name: "供应商名称" }) as HTMLInputElement).value).toBe(preset.name);
    expect((screen.getByRole("textbox", { name: "API Base URL" }) as HTMLInputElement).value).toBe(preset.baseURL);
    expect((screen.getByRole("combobox", { name: "接口协议" }) as HTMLSelectElement).value).toBe(preset.protocol);
    expect((screen.getByLabelText("模型 ID 1") as HTMLInputElement).value).toBe(preset.models[0].id);
    await fireEvent.input(screen.getByLabelText(/API Key/), { target: { value: "preset-fixture-key" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(port.saveModelProvider).toHaveBeenCalledWith(null, expect.objectContaining({ name: preset.name, protocol: preset.protocol, preset_id: preset.id, api_key: "preset-fixture-key", models: expect.arrayContaining([expect.objectContaining({ id: preset.models[0].id })]) })));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("reuses an existing preset instead of creating a duplicate", async () => {
    const preset = cloudModelPresets.find((item) => item.id === "claude")!;
    const provider: ModelProvider = { id: "saved-claude", name: "My Claude", base_url: preset.baseURL, enabled: true, has_api_key: true, note: "", protocol: "anthropic", preset_id: "claude", models: [{ id: "custom-claude", label: "Custom", capability: "chat", enabled: true }] };
    const configuration = { ...emptyModelConfiguration(), providers: [provider] };
    const port = fakePort({ models: vi.fn().mockResolvedValue(configuration) }); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "模型与语音" }));
    await fireEvent.click(screen.getByRole("button", { name: "配置 Claude" }));
    expect((screen.getByRole("textbox", { name: "供应商名称" }) as HTMLInputElement).value).toBe("My Claude");
    expect((screen.getByLabelText("模型 ID 1") as HTMLInputElement).value).toBe("custom-claude");
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(port.saveModelProvider).toHaveBeenCalledWith("saved-claude", expect.objectContaining({ name: "My Claude", preset_id: "claude", protocol: "anthropic" })));
    const sent = vi.mocked(port.saveModelProvider).mock.calls[0][1];
    expect("api_key" in sent).toBe(false);
  });

  it("keeps chat and voice tabs, credentials, forms and current selections independent", async () => {
    const chat: ModelProvider = { id: "chat-provider", name: "OpenAI", group: "chat", preset_id: "openai", protocol: "openai", base_url: "https://chat.example/v1", has_api_key: true, enabled: true, note: "", models: [{ id: "chat", label: "Chat", capability: "chat", enabled: true }] };
    const voice: ModelProvider = { ...chat, id: "voice-provider", group: "voice", base_url: "https://voice.example/v1", models: [{ id: "stt", label: "STT", capability: "stt", enabled: true }, { id: "tts", label: "TTS", capability: "tts", enabled: true, voice: "custom" }] };
    const configuration: ModelConfiguration = { ...emptyModelConfiguration(), providers: [chat, voice] };
    configuration.active = { chat: { provider_id: chat.id, model_id: "chat" }, stt: { provider_id: voice.id, model_id: "stt" }, tts: { provider_id: voice.id, model_id: "tts" } };
    const port = fakePort({ models: vi.fn().mockResolvedValue(configuration) }); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "模型与语音" }));
    expect(screen.getByRole("tab", { name: "对话模型" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByRole("combobox", { name: "当前语音输入" })).toBeNull();
    expect((screen.getByRole("combobox", { name: "当前聊天模型" }) as HTMLSelectElement).value).toBe(selectionValue(configuration.active.chat));
    await fireEvent.click(screen.getByRole("button", { name: "配置 OpenAI" }));
    expect((screen.getByLabelText("模型 ID 1") as HTMLInputElement).value).toBe("chat");
    expect(screen.queryByLabelText("模型 ID 2")).toBeNull();
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(port.saveModelProvider).toHaveBeenCalledWith(chat.id, expect.objectContaining({ group: "chat", base_url: chat.base_url }));
    await fireEvent.keyDown(screen.getByRole("tab", { name: "对话模型" }), { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "语音模型" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByRole("combobox", { name: "当前聊天模型" })).toBeNull();
    expect(screen.queryByRole("button", { name: "配置 Claude" })).toBeNull();
    expect((screen.getByRole("combobox", { name: "当前语音输出" }) as HTMLSelectElement).value).toBe(selectionValue(configuration.active.tts));
    await fireEvent.click(screen.getByRole("button", { name: "配置 OpenAI" }));
    expect((screen.getByRole("textbox", { name: "API Base URL" }) as HTMLInputElement).value).toBe(voice.base_url);
    expect((screen.getByLabelText("模型 ID 1") as HTMLInputElement).value).toBe("stt");
    expect(screen.queryByRole("option", { name: "聊天" })).toBeNull();
    await fireEvent.input(screen.getByRole("textbox", { name: "音色 2" }), { target: { value: "new-voice" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存配置" }).closest("form")!);
    await waitFor(() => expect(port.saveModelProvider).toHaveBeenCalledWith(voice.id, expect.objectContaining({ group: "voice", models: [expect.objectContaining({ id: "stt" }), expect.objectContaining({ id: "tts", voice: "new-voice" })] })));
    expect(vi.mocked(port.saveModelProvider).mock.calls.every(([,input]) => !("api_key" in input))).toBe(true);
  });
});

it("does not send during Chinese IME composition or when Enter inserts a newline", async () => {
  const onsend = vi.fn();
  render(Composer, { connectors: [], busy: false, locked: false, recording: false, transcribing: false, voiceEnabled: true, speechEnabled: true, onsend, onrecord: vi.fn(), onmanage: vi.fn() });
  const input = screen.getByRole("textbox", { name: "消息" });
  await fireEvent.input(input, { target: { value: "你好" } });
  await fireEvent.keyDown(input, { key: "Enter", isComposing: true });
  await fireEvent.keyDown(input, { key: "Enter", keyCode: 229 });
  await fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
  expect(onsend).not.toHaveBeenCalled();
  await fireEvent.keyDown(input, { key: "Enter" });
  expect(onsend).toHaveBeenCalledOnce();
});

it("renders assistant Markdown without script handlers or remote image requests", () => {
  const result = renderMarkdown('## 标题\n<script>alert(1)</script><img src="https://example.com/track" onerror="alert(1)">\n[link](javascript:alert(1))');
  expect(result).toContain("<h2>标题</h2>");
  expect(result).not.toMatch(/<script|<img|onerror|href="javascript:/);
});

describe("memory management journeys", () => {
  const atom = { id: "atom", assertion: "我喜欢 Python", kind: "Preference", importance: "high", confidence: "high", created_at: "2026-10-07T00:00:00Z", entity_id: "me" };
  function memoryPort(overrides: Partial<DashboardPort> = {}) {
    return fakePort({ memory: vi.fn().mockImplementation(async (method) => {
      if (method === "stats_counts") return { raw_events: 2, atoms: 1, entities: 1, episodes: 0, candidates_pending: 1, dirty_pages: 0 };
      if (method === "stats_growth" || method === "stats_atom_kinds") return { series: [] };
      if (method === "get_atom") return atom;
      if (method === "list_atoms") return { items: [atom], total: 1, has_more: false };
      if (method === "list_candidates") return { items: [{ ...atom, id: "candidate", status: "needs_review", candidate_type: "Preference" }], total: 1, has_more: false };
      return { items: [], total: 0, has_more: false };
    }), ...overrides });
  }

  it("opens all memory layers, adds a memory and corrects it through a replacement", async () => {
    const port = memoryPort(); render(App, { port });
    await waitFor(() => expect(screen.getByText("本地服务已连接")).toBeTruthy());
    await fireEvent.click(screen.getByRole("button", { name: "记忆管理" }));
    await waitFor(() => expect(screen.getByRole("button", { name: /原子记忆 1/ })).toBeTruthy());
    for (const label of ["记忆树", "原始记录", "候选记忆", "原子记忆", "情景记忆", "实体页面", "审计日志", "记忆设置", "迁移记忆"]) expect(screen.getByRole("tab", { name: new RegExp(label) })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "添加记忆" }));
    await fireEvent.input(screen.getByRole("textbox", { name: "记忆内容" }), { target: { value: "我喜欢咖啡" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存记忆" }).closest("form")!);
    await waitFor(() => expect(port.memory).toHaveBeenCalledWith("create_atom", expect.objectContaining({ assertion: "我喜欢咖啡", entity_name: "我", kind: "Fact" })));
    await fireEvent.click(screen.getByRole("tab", { name: "原子记忆" }));
    await fireEvent.click(await screen.findByRole("button", { name: "编辑记忆 atom" }));
    await fireEvent.input(screen.getByRole("textbox", { name: "记忆内容" }), { target: { value: "我现在喜欢 Go" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存记忆" }).closest("form")!);
    await waitFor(() => expect(port.memory).toHaveBeenCalledWith("replace_atom", { atom_id: "atom", assertion: "我现在喜欢 Go", reason: "" }));
  });

  it("requires confirmation for candidate promotion and retains failed editor text", async () => {
    const port = memoryPort(); render(App, { port });
    await fireEvent.click(screen.getByRole("button", { name: "记忆管理" }));
    await fireEvent.click(screen.getByRole("tab", { name: /候选记忆/ }));
    await fireEvent.click(await screen.findByRole("button", { name: "提升" }));
    expect(vi.mocked(port.memory).mock.calls.some(([method]) => method === "promote_candidate")).toBe(false);
    await fireEvent.click(screen.getByRole("button", { name: "确认执行" }));
    await waitFor(() => expect(port.memory).toHaveBeenCalledWith("promote_candidate", { id: "candidate", reason: "" }));
    await fireEvent.click(screen.getByRole("button", { name: "添加记忆" }));
    vi.mocked(port.memory).mockRejectedValueOnce(new Error("无法保存"));
    await fireEvent.input(screen.getByRole("textbox", { name: "记忆内容" }), { target: { value: "保留这段草稿" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存记忆" }).closest("form")!);
    expect(await screen.findByText("无法保存")).toBeTruthy();
    expect((screen.getByRole("textbox", { name: "记忆内容" }) as HTMLTextAreaElement).value).toBe("保留这段草稿");
  });

  it("saves extraction cadence and keeps package import behind a preview", async () => {
    const port = memoryPort({ saveMemoryConfig: vi.fn().mockImplementation(async (config) => config), importMemory: vi.fn().mockResolvedValue({ applied: 3, skipped: 1, errors: 0, dry_run: true, already_adopted: false }) }); render(App, { port });
    await fireEvent.click(screen.getByRole("button", { name: "记忆管理" }));
    await fireEvent.click(screen.getByRole("tab", { name: "记忆设置" }));
    await fireEvent.change(screen.getByRole("combobox", { name: "触发方式" }), { target: { value: "interval" } });
    await fireEvent.input(screen.getByRole("spinbutton", { name: "提炼间隔（秒）" }), { target: { value: "900" } });
    await fireEvent.submit(screen.getByRole("button", { name: "保存记忆设置" }).closest("form")!);
    await waitFor(() => expect(port.saveMemoryConfig).toHaveBeenCalledWith(expect.objectContaining({ extract_trigger_mode: "interval", extract_interval_seconds: 900 })));
    await fireEvent.click(screen.getByRole("tab", { name: "迁移记忆" }));
    expect((screen.getByRole("button", { name: "确认导入" }) as HTMLButtonElement).disabled).toBe(true);
    const file = new File(["fixture"], "memory.hmpkg");
    await fireEvent.change(screen.getByLabelText("记忆包"), { target: { files: [file] } });
    await fireEvent.click(screen.getByRole("button", { name: "预检查导入" }));
    await waitFor(() => expect(port.importMemory).toHaveBeenCalledWith(file, true, "skip"));
    await waitFor(() => expect((screen.getByRole("button", { name: "确认导入" }) as HTMLButtonElement).disabled).toBe(false));
    await fireEvent.click(screen.getByRole("button", { name: "确认导入" }));
    await waitFor(() => expect(port.importMemory).toHaveBeenCalledWith(file, false, "skip"));
  });

  it("pauses sending during compaction even when maintenance polling loses connection", async () => {
    const maintenance = vi.fn().mockResolvedValue({ id: "slim", kind: "slim", phase: "vacuum", status: "running", elapsed_seconds: 65 });
    const port = memoryPort({ memoryStatus: maintenance }); render(App, { port });
    expect(await screen.findByText("压缩数据库")).toBeTruthy();
    await fireEvent.input(screen.getByRole("textbox", { name: "消息" }), { target: { value: "等待维护结束" } });
    expect((screen.getByRole("button", { name: "发送消息" }) as HTMLButtonElement).disabled).toBe(true);
    maintenance.mockRejectedValue(new Error("offline"));
    await screen.findByText("连接暂时中断，当前状态可能已过时；正在重试。", {}, { timeout: 3500 });
    expect((screen.getByRole("button", { name: "发送消息" }) as HTMLButtonElement).disabled).toBe(true);
  });
});
