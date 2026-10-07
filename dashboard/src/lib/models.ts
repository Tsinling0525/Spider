export type ModelCapability = "chat" | "stt" | "tts";
export type ModelProtocol = "openai" | "anthropic";
export type ModelGroup = "chat" | "voice";
export interface ModelDefinition {
  id: string;
  label: string;
  capability: ModelCapability;
  enabled: boolean;
  voice?: string;
}
export interface ModelProvider {
  id: string;
  name: string;
  base_url: string;
  enabled: boolean;
  has_api_key: boolean;
  note: string;
  models: ModelDefinition[];
  protocol?: ModelProtocol;
  preset_id?: string;
  group?: ModelGroup;
}
export interface ModelProviderInput {
  name: string;
  base_url: string;
  enabled: boolean;
  api_key?: string;
  note: string;
  models: ModelDefinition[];
  protocol?: ModelProtocol;
  preset_id?: string;
  group?: ModelGroup;
}
export interface ModelSelection { provider_id: string; model_id: string }
export interface ModelConfiguration {
  providers: ModelProvider[];
  active: Record<ModelCapability, ModelSelection>;
}
export interface ModelTestResult { ok: boolean; message: string; latency_ms: number }

export const capabilityLabels: Record<ModelCapability, string> = { chat: "聊天", stt: "语音输入", tts: "语音输出" };
export function modelInGroup(model: ModelDefinition, group: ModelGroup): boolean { return group === "chat" ? model.capability === "chat" : model.capability === "stt" || model.capability === "tts"; }
export function providerInGroup(provider: ModelProvider, group: ModelGroup): boolean { return provider.group ? provider.group === group : provider.models.some((model) => modelInGroup(model, group)); }
export const emptyModelConfiguration = (): ModelConfiguration => ({ providers: [], active: { chat: { provider_id: "", model_id: "" }, stt: { provider_id: "", model_id: "" }, tts: { provider_id: "", model_id: "" } } });
export function modelOptions(configuration: ModelConfiguration, capability: ModelCapability) {
  return configuration.providers.filter((provider) => provider.enabled).flatMap((provider) => provider.models.filter((model) => model.enabled && model.capability === capability).map((model) => ({ provider, model })));
}
export function selectionValue(selection: ModelSelection | undefined): string {
  return selection?.provider_id ? JSON.stringify(selection) : "";
}
export function parseSelection(value: string): ModelSelection {
  if (!value) return { provider_id: "", model_id: "" };
  const parsed: unknown = JSON.parse(value);
  if (!parsed || typeof parsed !== "object" || !("provider_id" in parsed) || typeof parsed.provider_id !== "string" || !("model_id" in parsed) || typeof parsed.model_id !== "string") throw new Error("无效的模型选择");
  return { provider_id: parsed.provider_id, model_id: parsed.model_id };
}
