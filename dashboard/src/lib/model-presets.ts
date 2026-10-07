import type { ModelDefinition, ModelProtocol, ModelProvider, ModelProviderInput, ModelGroup } from "./models";
import { modelInGroup, providerInGroup } from "./models";

export interface ModelPreset {
  id: string; name: string; baseURL: string; protocol: ModelProtocol;
  description: string; mark: string; color: string; models: ModelDefinition[];
}
const chat = (id: string, label: string): ModelDefinition => ({ id, label, capability: "chat", enabled: true });

// Verified against the official provider docs on 2026-10-07. These are editable
// starting points; the account's /models response is available in the editor.
export const cloudModelPresets: ModelPreset[] = [
  { id: "openai", name: "OpenAI", baseURL: "https://api.openai.com/v1", protocol: "openai", mark: "O", color: "#23876d", description: "聊天、语音转写与语音合成", models: [
    chat("gpt-4.1", "GPT-4.1"), chat("gpt-4.1-mini", "GPT-4.1 mini"), chat("gpt-5-mini", "GPT-5 mini"),
    { id: "gpt-4o-transcribe", label: "GPT-4o Transcribe", capability: "stt", enabled: true },
    { id: "gpt-4o-mini-transcribe", label: "GPT-4o mini Transcribe", capability: "stt", enabled: true },
    { id: "gpt-4o-mini-tts", label: "GPT-4o mini TTS", capability: "tts", enabled: true, voice: "alloy" },
  ] },
  { id: "claude", name: "Claude", baseURL: "https://api.anthropic.com/v1", protocol: "anthropic", mark: "✳", color: "#c57652", description: "Anthropic 原生聊天与工具调用", models: [
    chat("claude-sonnet-5-5", "Claude Sonnet 5.5"), chat("claude-opus-5-5", "Claude Opus 5.5"), chat("claude-haiku-4-5-20251001", "Claude Haiku 4.5"),
  ] },
  { id: "deepseek", name: "DeepSeek", baseURL: "https://api.deepseek.com", protocol: "openai", mark: "D", color: "#5366ed", description: "Flash 与 Pro，支持推理和工具调用", models: [
    chat("deepseek-flash", "DeepSeek Flash"), chat("deepseek-v4-pro", "DeepSeek V4 Pro"),
  ] },
  { id: "mimo", name: "Xiaomi MiMo", baseURL: "https://api.xiaomimimo.com/v1", protocol: "openai", mark: "Mi", color: "#f08035", description: "MiMo V2.5 系列聊天与工具调用", models: [
    chat("mimo-v2.5-pro", "MiMo V2.5 Pro"), chat("mimo-v2.5", "MiMo V2.5"),
  ] },
];
export const modelPresets: ModelPreset[] = [...cloudModelPresets, { id: "ollama", name: "Ollama（本地）", baseURL: "http://127.0.0.1:11434/v1", protocol: "openai", mark: "O", color: "#687488", description: "读取本地模型列表或手动填写 ID", models: [chat("", "")] }];
export function presetsForGroup(group: ModelGroup, includeLocal = false): ModelPreset[] {
  return (includeLocal ? modelPresets : cloudModelPresets).map((preset) => ({ ...preset, description: group === "voice" ? "语音转写与语音合成" : preset.id === "openai" ? "聊天与工具调用" : preset.description, models: preset.models.filter((model) => modelInGroup(model, group)) })).filter((preset) => preset.models.length > 0);
}
export function presetInput(preset: ModelPreset, group: ModelGroup = "chat"): ModelProviderInput {
  return { name: preset.name, base_url: preset.baseURL, protocol: preset.protocol, preset_id: preset.id, group, enabled: true, note: "", models: preset.models.filter((model) => modelInGroup(model, group)).map((model) => ({ ...model })) };
}
export function providerForPreset(providers: ModelProvider[], preset: ModelPreset, group: ModelGroup = "chat"): ModelProvider | undefined {
  const scoped = providers.filter((provider) => providerInGroup(provider, group));
  return scoped.find((provider) => provider.preset_id === preset.id) ?? scoped.find((provider) => provider.name.toLowerCase() === preset.name.toLowerCase() && provider.base_url.replace(/\/$/, "") === preset.baseURL);
}
