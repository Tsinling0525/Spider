<script lang="ts">
  import { t } from "../lib/i18n";
  import { Plus, Search, Pencil, Trash2, AudioLines, Sparkles, Mic, Volume2, MessageSquare, Check, FlaskConical, Power, LoaderCircle, RefreshCw } from "lucide-svelte";
  import type { DashboardPort } from "../lib/dashboard";
  import type { ModelCapability, ModelConfiguration, ModelProvider, ModelProviderInput, ModelSelection, ModelGroup } from "../lib/models";
  import { capabilityLabels, modelOptions, parseSelection, selectionValue, providerInGroup, modelInGroup } from "../lib/models";
  import ModelProviderEditor from "./ModelProviderEditor.svelte";
  import { presetsForGroup, providerForPreset, type ModelPreset } from "../lib/model-presets";
  import openaiLogo from "../assets/providers/openai.png";
  import claudeLogo from "../assets/providers/anthropic.png";
  import deepseekLogo from "../assets/providers/deepseek.png";
  import mimoLogo from "../assets/providers/mimo.svg";
  const presetLogos: Record<string,string> = { openai: openaiLogo, claude: claudeLogo, deepseek: deepseekLogo, mimo: mimoLogo };
  let { configuration, port, onrefresh, onselect, onerror }: {
    configuration: ModelConfiguration; port: DashboardPort; onrefresh: () => Promise<void>;
    onselect: (capability: ModelCapability, selection: ModelSelection) => Promise<void>; onerror: (cause: unknown) => void;
  } = $props();
  let search = $state("");
  let group = $state<ModelGroup>("chat");
  let editorOpen = $state(false);
  let editing = $state<ModelProvider | null>(null);
  let editingPreset = $state<ModelPreset | null>(null);
  let working = $state("");
  let notice = $state("");
  let deleting = $state<string | null>(null);
  const scopedProviders = $derived(configuration.providers.filter((provider) => providerInGroup(provider, group)));
  const filtered = $derived(scopedProviders.filter((provider) => `${provider.name} ${provider.base_url} ${provider.models.map((model) => model.id).join(" ")}`.toLowerCase().includes(search.toLowerCase())));
  const presets = $derived(presetsForGroup(group));
  const allSlots = [
    { capability: "chat" as const, title: "聊天模型", description: "对话、思考与工具调用", icon: Sparkles },
    { capability: "stt" as const, title: "语音输入", description: "将你的声音转成文字", icon: Mic },
    { capability: "tts" as const, title: "语音输出", description: "用模型的声音播放回复", icon: Volume2 },
  ];
  const slots = $derived(allSlots.filter((slot) => group === "chat" ? slot.capability === "chat" : slot.capability !== "chat"));
  function switchGroup(next: ModelGroup) { group = next; search = ""; notice = ""; deleting = null; }
  function tabKey(event: KeyboardEvent) {
    const next = event.key === "Home" ? "chat" : event.key === "End" ? "voice" : ["ArrowLeft", "ArrowRight"].includes(event.key) ? group === "chat" ? "voice" : "chat" : null;
    if (!next || working) return;
    event.preventDefault(); switchGroup(next); document.getElementById(`models-tab-${next}`)?.focus();
  }
  function openEditor(provider: ModelProvider | null = null, preset: ModelPreset | null = null) {
    editing = provider; editingPreset = preset; editorOpen = true;
  }
  async function select(capability: ModelCapability, value: string) {
    working = `slot-${capability}`;
    try { await onselect(capability, parseSelection(value)); notice = "当前模型已更新，下一次请求立即生效。"; }
    catch (cause) { onerror(cause); }
    finally { working = ""; }
  }
  async function save(input: ModelProviderInput) {
    await port.saveModelProvider(editing?.id ?? null, input); await onrefresh(); notice = "配置已保存。在上方选择当前模型，就可以开始使用。";
  }
  async function test(provider: ModelProvider, capability: ModelCapability, id: string) {
    working = `test-${provider.id}-${capability}-${id}`; notice = "";
    try { const result = await port.testModel(provider.id, capability, id); notice = `${provider.name} / ${id}：${result.message} · ${result.latency_ms} ms`; }
    catch (cause) { onerror(cause); }
    finally { working = ""; }
  }
  async function toggle(provider: ModelProvider) {
    working = provider.id;
    try { await port.saveModelProvider(provider.id, { name: provider.name, base_url: provider.base_url, enabled: !provider.enabled, note: provider.note, models: provider.models, protocol: provider.protocol, preset_id: provider.preset_id, group }); await onrefresh(); }
    catch (cause) { onerror(cause); }
    finally { working = ""; }
  }
  async function remove(id: string) {
    working = id;
    try { await port.deleteModelProvider(id); await onrefresh(); deleting = null; }
    catch (cause) { onerror(cause); }
    finally { working = ""; }
  }
</script>

<section class="models-page">
  <div class="eyebrow">{$t("用你喜欢的模型")}</div>
  <div class="heading"><div><h1>{$t("模型配置")}</h1><p>{group === "chat" ? $t("为对话选择聊天模型，连接你的工具。") : $t("为语音输入与回复分别选择模型和声音。")}</p></div><button class="primary" onclick={() => openEditor()}><Plus />{$t("添加供应商")}</button></div>
  <div class="category-tabs" role="tablist" aria-label={$t("模型配置类别")}>
    <button id="models-tab-chat" role="tab" aria-selected={group === "chat"} aria-controls="models-panel-chat" tabindex={group === "chat" ? 0 : -1} class:active={group === "chat"} disabled={!!working} onclick={() => switchGroup("chat")} onkeydown={tabKey}><MessageSquare size={16} />{$t("对话模型")}</button>
    <button id="models-tab-voice" role="tab" aria-selected={group === "voice"} aria-controls="models-panel-voice" tabindex={group === "voice" ? 0 : -1} class:active={group === "voice"} disabled={!!working} onclick={() => switchGroup("voice")} onkeydown={tabKey}><Mic size={16} />{$t("语音模型")}</button>
  </div>
  <div role="tabpanel" id={`models-panel-${group}`} aria-labelledby={`models-tab-${group}`}>
  <div class="slots" class:chat-slots={group === "chat"}>{#each slots as slot}{@const active = configuration.active[slot.capability]}<article class="slot"><div class={`slot-icon ${slot.capability}`}><slot.icon size={21} /></div><div><h3>{$t(slot.title)}</h3><p>{$t(slot.description)}</p></div><label class="sr-only" for={`active-${slot.capability}`}>{$t("当前{0}", { 0: $t(slot.title) })}</label><select id={`active-${slot.capability}`} value={selectionValue(active)} disabled={!!working} onchange={(event) => select(slot.capability,event.currentTarget.value)}><option value="">{$t("暂不使用")}</option>{#each modelOptions(configuration,slot.capability) as option}<option value={selectionValue({ provider_id: option.provider.id, model_id: option.model.id })}>{option.provider.name} / {option.model.label || option.model.id}</option>{/each}</select><div class="slot-status" class:enabled={!!active?.provider_id}><i></i>{active?.provider_id ? $t("已启用") : $t("未选择")}</div></article>{/each}</div>
  <section class="presets" aria-label={$t("预置供应商")}>
    <div class="preset-heading"><h2>{$t("预置供应商")}</h2><span>{$t("填写你的 API key，模型和地址已为你准备好。")}</span></div>
    <div class="preset-grid">{#each presets as preset}{@const saved = providerForPreset(configuration.providers,preset,group)}<article class="preset-card" class:configured={!!saved}>
      <div class="preset-title"><div class="preset-mark"><img src={presetLogos[preset.id]} alt="" /></div><h3>{$t(preset.name)}</h3><span class="preset-status" class:authorized={saved?.has_api_key && saved?.enabled}><i></i>{saved ? !saved.enabled ? $t("已停用") : saved.has_api_key ? $t("已配置") : $t("待授权") : $t("未配置")}</span></div>
      <p class="preset-description">{$t(preset.description)}</p><div class="preset-url" title={saved?.base_url || preset.baseURL}>{saved?.base_url || preset.baseURL}</div>
      <div class="preset-summary"><span>{$t("{0} 个模型", { 0: (saved?.models || preset.models).length })}</span><span>{preset.protocol === "anthropic" ? "Claude Messages" : $t("OpenAI 兼容")}</span></div>
      <div class="preset-bottom"><span>{(saved?.models || preset.models).filter((model) => modelInGroup(model,group)).slice(0,2).map((model) => model.label).join(" · ")}</span><button disabled={!!working} onclick={() => openEditor(saved || null,preset)} aria-label={$t("配置 {0}", { 0: $t(preset.name) })}>{saved ? $t("编辑配置") : $t("配置")}<Pencil size={12} /></button></div>
    </article>{/each}</div>
  </section>
  <div class="toolbar"><div class="provider-count">{$t("已添加{0}供应商", { 0: group === "chat" ? $t("对话") : $t("语音") })} <span>{scopedProviders.length}</span></div><div class="toolbar-right"><label class="search"><Search size={14} /><input aria-label={$t("搜索模型供应商")} bind:value={search} placeholder={$t("搜索供应商或模型")} /></label><button class="icon-button" aria-label={$t("刷新模型配置")} disabled={!!working} onclick={() => onrefresh().catch(onerror)}><RefreshCw /></button></div></div>
  {#if notice}<div class="notice" role="status"><Check size={15} /><span>{$t(notice)}</span></div>{/if}
  {#if working.startsWith("test-")}<div class="test-progress" role="status"><LoaderCircle class="spin" size={14} />{$t("正在调用模型测试接口…")}</div>{/if}
  <div class="providers">{#each filtered as provider}<article class="provider" class:disabled-provider={!provider.enabled}><div class="provider-header"><div class="provider-logo"><AudioLines size={23} /></div><div class="provider-name"><h3>{provider.name}</h3><div><span class="badge">{provider.protocol === "anthropic" ? "Claude Messages" : $t("OpenAI 兼容")}</span><span class="key-state">{provider.has_api_key ? $t("API key 已保存") : $t("未设置 API key")}</span></div></div><div class="provider-actions"><button class="icon-button" aria-label={$t("编辑 {0}", { 0: provider.name })} disabled={!!working} onclick={() => openEditor(provider)}><Pencil /></button><button class="icon-button" aria-label={`${provider.enabled ? $t("停用") : $t("启用")} ${provider.name}`} disabled={!!working} onclick={() => toggle(provider)}><Power /></button><button class="icon-button" aria-label={$t("删除 {0}", { 0: provider.name })} disabled={!!working} onclick={() => deleting = deleting === provider.id ? null : provider.id}><Trash2 /></button></div></div><div class="provider-url">{provider.base_url}</div>{#if provider.note}<p class="provider-note">{provider.note}</p>{/if}<div class="provider-models">{#each provider.models as model}{@const active = configuration.active[model.capability]}{@const current = active?.provider_id === provider.id && active?.model_id === model.id}<div class="model-line"><span class={`capability ${model.capability}`}>{$t(capabilityLabels[model.capability])}</span><div class="model-name"><strong>{model.label || model.id}</strong><span>{model.id}{model.capability === "tts" ? ` · ${model.voice || "alloy"}` : ""}</span></div>{#if current}<span class="current"><Check size={12} />{$t("当前使用")}</span>{:else if !model.enabled}<span class="badge">{$t("已停用")}</span>{/if}<button class="model-test" disabled={!!working} onclick={() => test(provider,model.capability,model.id)} aria-label={$t("测试 {0} {1} {2}", { 0: provider.name, 1: model.id, 2: $t(capabilityLabels[model.capability]) })}><FlaskConical size={14} />{$t("测试")}</button>{#if !current && provider.enabled && model.enabled}<button class="use-model" disabled={!!working} onclick={() => select(model.capability,selectionValue({ provider_id: provider.id, model_id: model.id }))}>{$t("使用")}</button>{/if}</div>{/each}</div>{#if deleting === provider.id}<div class="delete-confirm"><span>{$t("删除「{0}」及其模型配置？当前使用的模型需先切换。", { 0: provider.name })}</span><button class="secondary" onclick={() => deleting = null}>{$t("取消")}</button><button class="primary" disabled={!!working} onclick={() => remove(provider.id)}>{$t("删除")}</button></div>{/if}</article>{/each}</div>
  {#if !filtered.length}<div class="empty"><div class="empty-icon"><AudioLines size={30} /></div><h3>{scopedProviders.length ? $t("没有匹配的供应商") : group === "chat" ? $t("添加你的对话模型") : $t("添加你的语音模型")}</h3><p>{scopedProviders.length ? $t("试试其他筛选条件。") : $t("从上方选择预置供应商，或添加自定义接口。")}</p>{#if !scopedProviders.length}<button class="primary" onclick={() => openEditor()}><Plus size={15} />{$t("添加模型供应商")}</button>{/if}</div>{/if}
  <div class="footnote"><span>{$t("{0}配置保存在你的本地服务，保存或切换后即时生效。", { 0: group === "chat" ? $t("支持 OpenAI 兼容聊天和 Claude 原生聊天接口。") : $t("语音输入使用 STT 转写，语音输出使用 TTS 合成。") })}</span></div>
  </div>
</section>
{#if editorOpen}<ModelProviderEditor {port} {group} provider={editing} initialPreset={editingPreset} onsave={save} onclose={() => editorOpen = false} />{/if}

<style>
  .models-page { width: min(100%, 1100px); margin: 0 auto; padding: 45px 44px; }
  .eyebrow { font-size: 11px; color: var(--text-faint); margin-bottom: 7px; }
  .heading { display: flex; align-items: center; justify-content: space-between; gap: 20px; margin-bottom: 28px; }
  h1 { font-size: 27px; font-weight: 620; letter-spacing: -.7px; }
  .heading p { font-size: 12px; color: var(--text-dim); margin-top: 9px; }
  .heading button { font-size: 12px; flex-shrink: 0; }
  .category-tabs { display: flex; gap: 28px; border-bottom: 1px solid var(--line); margin-bottom: 21px; }
  .category-tabs button { gap: 7px; font-size: 13px; color: var(--text-dim); padding: 12px 0 14px; border-bottom: 2px solid transparent; margin-bottom: -1px; }
  .category-tabs button.active { color: var(--accent); border-color: var(--accent); font-weight: 550; }
  .slots { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 13px; }
  .slot { padding: 19px 16px 14px; border: 1px solid var(--line); border-radius: 13px; background: var(--content-soft); min-width: 0; }
  .slot-icon { display: flex; align-items: center; justify-content: center; width: 35px; height: 35px; border-radius: 10px; margin-bottom: 13px; color: var(--accent); background: var(--accent-wash); }
  .slot-icon.stt { color: var(--blue); background: var(--blue-wash); }
  .slot-icon.tts { color: #9b71c5; background: color-mix(in srgb,#9b71c5 10%,var(--content-plane)); }
  h3 { font-size: 13px; font-weight: 600; }
  .slot p { font-size: 10px; color: var(--text-faint); margin-top: 5px; }
  .slot select { display: block; width: 100%; font-size: 11px; padding: 9px; margin-top: 16px; overflow: hidden; text-overflow: ellipsis; }
  .slot-status { display: flex; align-items: center; gap: 5px; color: var(--text-faint); font-size: 9px; margin-top: 10px; }
  .slot-status i { width: 4px; height: 4px; background: currentColor; border-radius: 50%; }
  .slot-status.enabled { color: var(--success); }
  .chat-slots { grid-template-columns: 1fr; }
  .chat-slots .slot { display: grid; grid-template-columns: 38px 1fr minmax(170px,40%) auto; align-items: center; gap: 13px; padding: 22px 19px; }
  .chat-slots .slot-icon, .chat-slots select, .chat-slots .slot-status { margin: 0; }
  .presets { margin-top: 30px; }
  .preset-heading { display: flex; align-items: baseline; gap: 13px; margin-bottom: 14px; }
  .preset-heading h2 { font-size: 14px; font-weight: 600; }
  .preset-heading > span { color: var(--text-faint); font-size: 10px; }
  .preset-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 13px; }
  .preset-card { border: 1px solid var(--line); border-radius: 12px; padding: 17px 16px 0; min-width: 0; box-shadow: 0 1px 3px rgb(20 25 40 / 2%); }
  .preset-card.configured { border-color: color-mix(in srgb,var(--accent) 25%,var(--line)); background: color-mix(in srgb,var(--accent) 2%,var(--content-plane)); }
  .preset-title { display: flex; align-items: center; gap: 9px; }
  .preset-mark { display: flex; align-items: center; justify-content: center; width: 30px; height: 30px; border-radius: 8px; background: white; border: 1px solid var(--line); flex-shrink: 0; overflow: hidden; }
  .preset-mark img { width: 24px; height: 24px; object-fit: contain; }
  .preset-title h3 { font-size: 13px; }
  .preset-status { display: flex; align-items: center; gap: 4px; margin-left: auto; color: var(--text-faint); font-size: 9px; white-space: nowrap; }
  .preset-status i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
  .preset-status.authorized { color: var(--success); }
  .preset-description { font-size: 10px; color: var(--text-dim); margin-top: 12px; }
  .preset-url { font: 9px/1.8 var(--font-mono); color: var(--text-faint); margin-top: 6px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .preset-summary { display: flex; justify-content: space-between; font-size: 9px; color: var(--text-dim); margin: 10px 0 13px; }
  .preset-summary > span:last-child { color: var(--text-faint); }
  .preset-bottom { border-top: 1px solid var(--line); padding: 11px 0; display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .preset-bottom > span { font-size: 9px; color: var(--text-faint); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .preset-bottom button { color: var(--accent); font-size: 10px; gap: 5px; flex-shrink: 0; }
  .toolbar { display: flex; align-items: center; justify-content: space-between; gap: 13px; margin-top: 28px; padding-bottom: 12px; border-bottom: 1px solid var(--line); }
  .provider-count { display: flex; align-items: center; gap: 6px; font-size: 11px; font-weight: 550; }
  .provider-count span { padding: 1px 5px; background: var(--content-soft); border-radius: 5px; font-size: 9px; color: var(--text-faint); }
  .toolbar-right { display: flex; gap: 8px; align-items: center; }
  .search { display: flex; align-items: center; gap: 6px; color: var(--text-faint); }
  .search input { width: 150px; border: 0; font-size: 11px; padding: 4px 0; background: none; }
  .toolbar-right button { width: 26px; height: 26px; }
  .toolbar-right button :global(svg) { width: 13px; height: 13px; }
  .providers { display: grid; gap: 15px; margin-top: 18px; }
  .provider { padding: 19px 20px 0; border: 1px solid var(--line-strong); border-radius: 13px; overflow: hidden; }
  .provider-header { display: flex; align-items: center; gap: 12px; }
  .provider-logo { width: 43px; height: 43px; border-radius: 12px; display: flex; align-items: center; justify-content: center; color: var(--blue); background: var(--blue-wash); }
  .provider-name { min-width: 0; }
  .provider-name h3 { overflow-wrap: anywhere; margin-bottom: 6px; }
  .provider-name > div { display: flex; gap: 8px; align-items: center; }
  .provider-name .badge { font-size: 9px; padding: 1px 5px; }
  .key-state { font-size: 9px; color: var(--text-faint); }
  .provider-actions { display: flex; margin-left: auto; gap: 2px; flex-shrink: 0; }
  .provider-actions button { width: 29px; height: 29px; }
  .provider-actions button :global(svg) { width: 14px; height: 14px; }
  .provider-url { font: 10px/1.6 var(--font-mono); color: var(--text-faint); overflow-wrap: anywhere; margin-top: 14px; }
  .provider-note { color: var(--text-dim); font-size: 11px; margin-top: 6px; }
  .provider-models { border-top: 1px solid var(--line); margin-top: 15px; }
  .model-line { display: flex; align-items: center; gap: 10px; padding: 14px 0; border-bottom: 1px solid var(--line); }
  .model-line:last-child { border: 0; }
  .capability { font-size: 9px; padding: 3px 6px; border-radius: 5px; color: var(--accent); background: var(--accent-wash); white-space: nowrap; }
  .capability.stt { color: var(--blue); background: var(--blue-wash); }
  .capability.tts { color: #9b71c5; background: color-mix(in srgb,#9b71c5 9%,var(--content-plane)); }
  .model-name { min-width: 0; margin-right: auto; }
  .model-name strong { display: block; font-size: 11px; font-weight: 550; overflow-wrap: anywhere; }
  .model-name > span { display: block; color: var(--text-faint); font: 9px/1.8 var(--font-mono); overflow-wrap: anywhere; }
  .current { display: flex; gap: 3px; align-items: center; font-size: 9px; color: var(--success); white-space: nowrap; }
  .model-test, .use-model { font-size: 10px; padding: 4px 5px; color: var(--text-dim); gap: 4px; white-space: nowrap; }
  .use-model { color: var(--blue); }
  .disabled-provider { opacity: .65; }
  .notice, .test-progress { display: flex; align-items: center; gap: 7px; padding: 12px; margin-top: 16px; font-size: 11px; border-radius: 8px; }
  .notice { color: var(--success); background: color-mix(in srgb,var(--success) 5%,var(--content-plane)); }
  .test-progress { color: var(--text-dim); background: var(--content-soft); }
  .empty { text-align: center; padding: 45px 20px; }
  .empty-icon { display: inline-flex; width: 62px; height: 62px; border-radius: 18px; color: var(--text-faint); background: var(--content-soft); align-items: center; justify-content: center; margin-bottom: 17px; }
  .empty p { color: var(--text-faint); font-size: 11px; margin-top: 8px; }
  .empty button { font-size: 11px; margin-top: 22px; }
  .footnote { margin-top: 24px; padding-top: 18px; border-top: 1px solid var(--line); color: var(--text-faint); font-size: 10px; line-height: 1.8; }
  .delete-confirm { display: flex; align-items: center; gap: 8px; padding: 13px 0; border-top: 1px solid var(--line); }
  .delete-confirm > span { font-size: 10px; margin-right: auto; color: var(--text-dim); }
  .delete-confirm button { font-size: 10px; padding: 6px 10px; }
  @media (max-width: 1000px) { .models-page { padding: 34px 26px; } .slots { gap: 9px; } .slot { padding: 15px 12px 12px; } }
  @media (min-width: 1500px) { .preset-grid { grid-template-columns: repeat(4,minmax(0,1fr)); } .preset-title { flex-wrap: wrap; } }
  @media (max-width: 700px) { .preset-grid { grid-template-columns: 1fr; } .preset-heading { display: block; } .preset-heading > span { display: block; margin-top: 5px; } }
  @media (max-width: 700px) { .chat-slots .slot { grid-template-columns: 35px 1fr; padding: 15px; } .chat-slots select { grid-column: 1 / -1; margin-top: 12px; } .chat-slots .slot-status { grid-column: 1 / -1; } }
  @media (max-width: 700px) { .models-page { padding: 27px 18px; } .heading { align-items: flex-start; gap: 12px; } .heading p { font-size: 11px; } .heading button { font-size: 10px; padding: 10px; } .slots { grid-template-columns: 1fr; } .slot { display: grid; grid-template-columns: 35px 1fr; gap: 0 12px; padding: 15px; } .slot-icon { margin: 0; } .slot select { grid-column: 1 / -1; margin-top: 12px; } .slot-status { grid-column: 1 / -1; } .toolbar { align-items: flex-start; flex-direction: column; gap: 8px; } .toolbar-right { width: 100%; justify-content: space-between; } .provider { padding: 16px 13px 0; } .provider-actions { gap: 0; } .provider-logo { width: 34px; height: 34px; } .key-state { display: none; } .model-line { flex-wrap: wrap; gap: 8px; } .model-name { flex: 1; } .current { font-size: 8px; } }
</style>
