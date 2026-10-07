import type { ModelCapability, ModelConfiguration, ModelProvider, ModelProviderInput, ModelSelection, ModelTestResult } from "./models";
import type { BuiltinConnectorInput, ConnectorProbeResult, ConnectorOAuthStart, ConnectorOAuthStatus } from "./builtin-connectors";
import type { MemoryPort, MemoryConfig, MemoryJob, AdoptResult, DoctorReport } from "./memory";

export interface MCPTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
}

export interface Connector {
  id: string;
  name: string;
  url: string;
  description: string;
  enabled: boolean;
  has_headers: boolean;
  tools: MCPTool[];
  checked_at?: string;
  kind?: string;
  has_credentials?: boolean;
  credentials?: Record<string, string>;
}

export interface ConnectorInput {
  name: string;
  url: string;
  description: string;
  enabled: boolean;
  headers?: Record<string, string>;
}

export interface ToolCall {
  id: string;
  function: { name: string; arguments: string };
}

export interface Message {
  role: "user" | "assistant" | "tool" | "system";
  content: string;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  name?: string;
}

export interface Pending {
  id: string;
  connector_id: string;
  connector_name: string;
  url: string;
  tool: string;
  arguments: Record<string, unknown>;
}

export interface Conversation {
  id: string;
  title: string;
  messages: Message[] | null;
  connector_ids: string[];
  pending?: Pending;
  status: "idle" | "running" | "failed" | "waiting_approval";
  error?: string;
  memory_error?: string;
  version: number;
  created_at: string;
  updated_at: string;
}

export interface Capabilities { chat: boolean; transcription: boolean; speech: boolean }
export type DashboardTransport = (path: string, init?: RequestInit) => Promise<Response>;

export interface DashboardPort extends MemoryPort {
  capabilities(): Promise<Capabilities>;
  connectors(): Promise<Connector[]>;
  saveConnector(id: string | null, input: ConnectorInput): Promise<Connector>;
  deleteConnector(id: string): Promise<void>;
  probeConnector(id: string): Promise<Connector>;
  saveBuiltinConnector(id: string | null, input: BuiltinConnectorInput): Promise<Connector>;
  testBuiltinConnector(id: string | null, input: BuiltinConnectorInput): Promise<ConnectorProbeResult>;
  startConnectorOAuth(id: string | null, input: BuiltinConnectorInput, redirectURI: string): Promise<ConnectorOAuthStart>;
  connectorOAuthStatus(state: string): Promise<ConnectorOAuthStatus>;
  conversations(): Promise<Conversation[]>;
  conversation(id: string): Promise<Conversation>;
  createConversation(): Promise<Conversation>;
  deleteConversation(id: string): Promise<void>;
  send(id: string, version: number, content: string, connectorIDs: string[]): Promise<Conversation>;
  decide(id: string, version: number, approvalID: string, approve: boolean): Promise<Conversation>;
  transcribe(file: Blob): Promise<string>;
  speech(text: string): Promise<Blob>;
  models(): Promise<ModelConfiguration>;
  saveModelProvider(id: string | null, input: ModelProviderInput): Promise<ModelProvider>;
  deleteModelProvider(id: string): Promise<void>;
  selectModel(capability: ModelCapability, selection: ModelSelection): Promise<ModelConfiguration>;
  fetchModelIDs(id: string | null, input?: ModelProviderInput): Promise<string[]>;
  testModel(id: string | null, capability: ModelCapability, modelID: string, input?: ModelProviderInput): Promise<ModelTestResult>;
}

export class SpiderDashboardPort implements DashboardPort {
  private readonly transport: DashboardTransport;
  constructor(transport?: DashboardTransport) {
    // Match Mantle's server-side credential proxy. Production uses same-origin
    // /v1/dashboard endpoints; keys are never VITE_* or localStorage values.
    this.transport = transport ?? ((path, init) => fetch(`${import.meta.env.DEV ? "/spider-api" : ""}/v1/dashboard${path}`, init));
  }

  private async request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await this.fetch(path, init);
    if (!response.ok) {
      const payload = await response.json().catch(() => null);
      throw new Error(payload?.error?.message || (response.status === 500 || response.status === 502 || response.status === 504 ? "无法连接 dashboard 服务，请在 Spider 目录运行 make dev-dashboard，并保持终端运行。" : `Spider 请求失败 (${response.status})`));
    }
    if (response.status === 204) return undefined as T;
    return response.json() as Promise<T>;
  }

  private async fetch(path: string, init?: RequestInit): Promise<Response> {
    try { return await this.transport(path, init); }
    catch (cause) {
      if (cause instanceof TypeError) throw new Error("本地 dashboard 服务已断开，请运行 make dev-dashboard 后重试；当前填写的内容仍然保留。", { cause });
      throw cause;
    }
  }

  private json(method: string, body?: unknown): RequestInit {
    return { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) };
  }

  capabilities() { return this.request<Capabilities>("/capabilities"); }
  async connectors() { return (await this.request<{ connectors: Connector[] }>("/connectors")).connectors; }
  saveConnector(id: string | null, input: ConnectorInput) { return this.request<Connector>(id ? `/connectors/${encodeURIComponent(id)}` : "/connectors", this.json(id ? "PUT" : "POST", input)); }
  deleteConnector(id: string) { return this.request<void>(`/connectors/${encodeURIComponent(id)}`, { method: "DELETE" }); }
  probeConnector(id: string) { return this.request<Connector>(`/connectors/${encodeURIComponent(id)}/probe`, { method: "POST" }); }
  saveBuiltinConnector(id: string | null, input: BuiltinConnectorInput) { return this.request<Connector>(id ? `/connectors/builtin/${encodeURIComponent(id)}` : "/connectors/builtin", this.json(id ? "PUT" : "POST", input)); }
  testBuiltinConnector(id: string | null, input: BuiltinConnectorInput) { return this.request<ConnectorProbeResult>("/connectors/builtin/test", this.json("POST", { connector_id: id ?? "", connector: input })); }
  startConnectorOAuth(id: string | null, input: BuiltinConnectorInput, redirectURI: string) { return this.request<ConnectorOAuthStart>("/connectors/oauth/start", this.json("POST", { connector_id: id ?? "", connector: input, redirect_uri: redirectURI })); }
  connectorOAuthStatus(state: string) { return this.request<ConnectorOAuthStatus>(`/connectors/oauth/status/${encodeURIComponent(state)}`); }
  async conversations() { return (await this.request<{ conversations: Conversation[] }>("/conversations")).conversations; }
  conversation(id: string) { return this.request<Conversation>(`/conversations/${encodeURIComponent(id)}`); }
  createConversation() { return this.request<Conversation>("/conversations", { method: "POST" }); }
  deleteConversation(id: string) { return this.request<void>(`/conversations/${encodeURIComponent(id)}`, { method: "DELETE" }); }
  send(id: string, version: number, content: string, connectorIDs: string[]) {
    return this.request<Conversation>(`/conversations/${encodeURIComponent(id)}/messages`, this.json("POST", { content, connector_ids: connectorIDs, expected_version: version }));
  }
  decide(id: string, version: number, approvalID: string, approve: boolean) {
    return this.request<Conversation>(`/conversations/${encodeURIComponent(id)}/decisions`, this.json("POST", { approval_id: approvalID, expected_version: version, approve }));
  }
  async transcribe(file: Blob) {
    const form = new FormData();
    const extension = file.type.includes("mp4") ? "mp4" : file.type.includes("ogg") ? "ogg" : "webm";
    form.append("file", file, `recording.${extension}`);
    return (await this.request<{ text: string }>("/voice/transcriptions", { method: "POST", body: form })).text;
  }
  async speech(text: string) {
    const response = await this.fetch("/voice/speech", this.json("POST", { text }));
    if (!response.ok) { const payload = await response.json().catch(() => null); throw new Error(payload?.error?.message || "语音生成失败"); }
    return response.blob();
  }
  models() { return this.request<ModelConfiguration>("/models"); }
  saveModelProvider(id: string | null, input: ModelProviderInput) {
    return this.request<ModelProvider>(id ? `/models/providers/${encodeURIComponent(id)}` : "/models/providers", this.json(id ? "PUT" : "POST", input));
  }
  deleteModelProvider(id: string) { return this.request<void>(`/models/providers/${encodeURIComponent(id)}`, { method: "DELETE" }); }
  selectModel(capability: ModelCapability, selection: ModelSelection) {
    return this.request<ModelConfiguration>("/models/active", this.json("PUT", { capability, ...selection }));
  }
  async fetchModelIDs(id: string | null, input?: ModelProviderInput) {
    return (await this.request<{ models: string[] }>("/models/fetch", this.json("POST", { provider_id: id ?? "", provider: input }))).models;
  }
  testModel(id: string | null, capability: ModelCapability, modelID: string, input?: ModelProviderInput) {
    return this.request<ModelTestResult>("/models/test", this.json("POST", { provider_id: id ?? "", provider: input, capability, model_id: modelID }));
  }
  memory<T = unknown>(method: string, params: Record<string, unknown> = {}) { return this.request<T>("/memory/rpc", this.json("POST", { method, params })); }
  memoryConfig() { return this.request<MemoryConfig>("/memory/config"); }
  saveMemoryConfig(config: MemoryConfig) { return this.request<MemoryConfig>("/memory/config", this.json("PUT", config)); }
  memoryStatus() { return this.request<MemoryJob>("/memory/maintenance"); }
  startMemoryJob(kind: string, sessionID = "") { return this.request<MemoryJob>("/memory/jobs", this.json("POST", { kind, session_id: sessionID })); }
  async exportMemory() {
    const response = await this.fetch("/memory/portable/pack", { method: "POST" });
    if (!response.ok) { const payload = await response.json().catch(() => null); throw new Error(payload?.error?.message || "记忆导出失败"); }
    return response.blob();
  }
  importMemory(file: File, dryRun: boolean, conflict: string) {
    const form = new FormData(); form.append("pkg_file", file); form.append("dry_run", String(dryRun)); form.append("on_conflict", conflict);
    return this.request<AdoptResult>("/memory/portable/adopt", { method: "POST", body: form });
  }
  doctorMemory() { return this.request<DoctorReport>("/memory/portable/doctor"); }
}

export function errorMessage(error: unknown): string { return error instanceof Error ? error.message : "操作失败，请重试"; }
