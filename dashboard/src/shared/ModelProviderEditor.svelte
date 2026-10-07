<script lang="ts">
  import { onMount } from "svelte";
  import { X, Plus, Trash2, Download, LoaderCircle, AudioLines, Check, FlaskConical } from "lucide-svelte";
  import type { DashboardPort } from "../lib/dashboard";
  import { errorMessage } from "../lib/dashboard";
  import type { ModelDefinition, ModelProvider, ModelProviderInput, ModelProtocol, ModelGroup } from "../lib/models";
  import { capabilityLabels, modelInGroup } from "../lib/models";
  import { presetsForGroup, presetInput, type ModelPreset } from "../lib/model-presets";
  let { provider, group, initialPreset = null, port, onsave, onclose }: { provider: ModelProvider | null; group: ModelGroup; initialPreset?: ModelPreset | null; port: DashboardPort; onsave: (input: ModelProviderInput) => Promise<void>; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let name = $state("");
  let baseURL = $state("");
  let apiKey = $state("");
  let clearKey = $state(false);
  let note = $state("");
  let enabled = $state(true);
  let preset = $state("custom");
  let protocol = $state<ModelProtocol>("openai");
  let rows = $state<ModelDefinition[]>([]);
  let remoteModels = $state<string[]>([]);
  let working = $state("");
  let error = $state("");
  let notice = $state("");
  const availablePresets = $derived(presetsForGroup(group, group === "chat"));
  function emptyRow(): ModelDefinition { return { id: "", label: "", capability: group === "chat" ? "chat" : "stt", enabled: true }; }
  onMount(() => {
    name = provider?.name ?? ""; baseURL = provider?.base_url ?? ""; note = provider?.note ?? ""; enabled = provider?.enabled ?? true;
    rows = provider?.models.filter((model) => modelInGroup(model, group)).map((model) => ({ ...model })) ?? [emptyRow()];
    preset = provider?.preset_id || initialPreset?.id || "custom";
    protocol = provider?.protocol ?? "openai";
    if (!provider && initialPreset) applyPreset(initialPreset.id);
    dialog.showModal();
  });
  function applyPreset(value: string) {
    preset = value;
    const template = availablePresets.find((item) => item.id === value);
    if (template) { const defaults = presetInput(template, group); name = defaults.name; baseURL = defaults.base_url; protocol = template.protocol; rows = defaults.models; }
    else protocol = "openai";
    apiKey = ""; clearKey = false; remoteModels = []; error = ""; notice = "";
  }
  function input(discovery = false): ModelProviderInput {
    const endpoint = new URL(baseURL.trim());
    if (!["http:", "https:"].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error("API 地址必须是 HTTP(S)，不要在 URL 中填写 key");
    if (!discovery && !name.trim()) throw new Error("请填写供应商名称");
    const payload: ModelProviderInput = { name: name.trim() || "未保存供应商", base_url: baseURL.trim(), note: note.trim(), enabled, protocol, group, preset_id: preset === "custom" ? "" : preset, models: rows.filter((row) => row.id.trim()).map((row) => ({ ...row, id: row.id.trim(), label: row.label.trim() || row.id.trim(), voice: row.capability === "tts" ? row.voice?.trim() || "alloy" : undefined })) };
    if (payload.models.some((model) => !modelInGroup(model, group))) throw new Error("请在对应的对话或语音配置页添加模型");
    if (apiKey.trim()) payload.api_key = apiKey.trim();
    else if (clearKey) payload.api_key = "";
    return payload;
  }
  async function fetchModels() {
    working = "fetch"; error = ""; notice = "";
    try { remoteModels = await port.fetchModelIDs(provider?.id ?? null, input(true)); notice = remoteModels.length ? `已读取 ${remoteModels.length} 个模型，可在模型 ID 中选择。` : "供应商返回了空列表，你仍可手动填写模型 ID。"; }
    catch (cause) { error = errorMessage(cause); }
    finally { working = ""; }
  }
  async function test(index: number) {
    working = `test-${index}`; error = ""; notice = "";
    try { const row = rows[index]; const result = await port.testModel(provider?.id ?? null, row.capability, row.id.trim(), input()); notice = `${row.label || row.id}：${result.message} · ${result.latency_ms} ms`; }
    catch (cause) { error = errorMessage(cause); }
    finally { working = ""; }
  }
  async function save(event: SubmitEvent) {
    event.preventDefault(); error = ""; working = "save";
    try { const payload = input(); if (!payload.models.length) throw new Error("请至少添加一个模型"); await onsave(payload); onclose(); }
    catch (cause) { error = errorMessage(cause); }
    finally { working = ""; }
  }
</script>

<dialog bind:this={dialog} oncancel={(event) => { if (working) event.preventDefault(); else onclose(); }}>
  <form onsubmit={save}>
    <header><div class="heading-icon"><AudioLines size={23} /></div><div><h2>{provider ? "编辑" : "添加"}{group === "chat" ? "对话" : "语音"}供应商</h2><p>{group === "chat" ? "配置聊天模型和工具调用，供对话中选择。" : "配置语音转写和语音合成模型，分别选择输入与输出。"}</p></div><button type="button" class="icon-button" aria-label="关闭模型配置" disabled={!!working} onclick={onclose}><X /></button></header>
    <fieldset disabled={!!working}>
      <div class="two-columns"><label>供应商名称<input bind:value={name} placeholder="例如：我的模型服务" maxlength="80" required /></label><label>接口预设<select value={preset} disabled={!!provider} onchange={(event) => applyPreset(event.currentTarget.value)}><option value="custom">自定义接口</option>{#each availablePresets as item}<option value={item.id}>{item.name}</option>{/each}</select></label></div>
      {#if group === "chat"}<label>接口协议<select bind:value={protocol}><option value="openai">OpenAI Chat Completions</option><option value="anthropic">Claude Messages</option></select></label>{:else}<p class="voice-protocol">OpenAI 兼容语音接口</p>{/if}
      <label>API Base URL<input type="url" bind:value={baseURL} placeholder="https://your-provider.example/v1" required /></label><p class="field-hint">填写 API 根地址，包含供应商所需的 /v1。{group === "voice" ? "转写和语音合成会使用各自的音频接口。" : "保存后即可在对话中选择模型。"}</p>
      <label>API Key <span class="optional">本地服务可留空</span><input type="password" bind:value={apiKey} autocomplete="off" placeholder={provider?.has_api_key ? "已保存 key，留空保留；填写新值则替换" : "填写你的 API key"} /></label>
      {#if provider?.has_api_key}<label class="check"><input type="checkbox" bind:checked={clearKey} />清除已保存的 API key</label>{/if}
      <div class="model-heading"><h3>模型列表</h3><button type="button" class="secondary" disabled={!baseURL.trim()} onclick={fetchModels}>{#if working === "fetch"}<LoaderCircle class="spin" size={14} />{:else}<Download size={14} />{/if}读取模型列表</button></div>
      <datalist id="provider-model-ids">{#each remoteModels as id}<option value={id}></option>{/each}</datalist>
      <div class="model-rows">{#each rows as row,index}<div class="model-row"><div class="row-top"><span>模型 {index + 1}</span><label class="check"><input type="checkbox" bind:checked={row.enabled} />启用</label><button class="icon-button" type="button" aria-label={`移除模型 ${index + 1}`} onclick={() => rows = rows.filter((_,i) => i !== index)}><Trash2 size={14} /></button></div><div class="row-fields"><label>模型 ID<input aria-label={`模型 ID ${index + 1}`} list="provider-model-ids" bind:value={row.id} placeholder="供应商的模型名称" maxlength="256" required /></label><label>能力<select aria-label={`能力 ${index + 1}`} bind:value={row.capability} onchange={() => { if (row.capability !== "tts") row.voice = undefined; }}>{#if group === "chat"}<option value="chat">聊天</option>{:else}<option value="stt">语音输入（STT）</option><option value="tts">语音输出（TTS）</option>{/if}</select></label></div><div class="row-fields"><label>显示名称 <span class="optional">可选</span><input aria-label={`显示名称 ${index + 1}`} bind:value={row.label} placeholder={group === "chat" ? "例如：日常助手" : "例如：语音输入"} maxlength="256" /></label>{#if row.capability === "tts"}<label>音色<input aria-label={`音色 ${index + 1}`} bind:value={row.voice} placeholder="alloy 或供应商音色 ID" maxlength="256" /></label>{:else}<div class="row-test"><button type="button" class="secondary" disabled={!row.id.trim() || !name.trim() || !baseURL.trim()} onclick={() => test(index)}><FlaskConical size={14} />测试{capabilityLabels[row.capability]}模型</button></div>{/if}</div>{#if row.capability === "tts"}<button class="tts-test secondary" type="button" disabled={!row.id.trim() || !name.trim() || !baseURL.trim()} onclick={() => test(index)}><FlaskConical size={14} />测试语音输出模型</button>{/if}</div>{/each}</div>
      <button type="button" class="add-model" onclick={() => rows = [...rows, emptyRow()]}><Plus size={14} />添加模型</button>
      <label class="note">备注 <span class="optional">可选</span><input bind:value={note} placeholder="记录这个供应商的用途" maxlength="1024" /></label><label class="check"><input type="checkbox" bind:checked={enabled} />启用这个供应商</label>
    </fieldset>
    <div class="feedback">{#if error}<p class="error" role="alert">{error}</p>{/if}{#if notice}<p class="notice" role="status"><Check size={14} />{notice}</p>{/if}</div>
    <footer><span>测试会向供应商发送少量测试内容。凭据仅保存在服务端。</span><button class="secondary" type="button" disabled={!!working} onclick={onclose}>取消</button><button class="primary" type="submit" disabled={!!working || !rows.length}>{#if working}<LoaderCircle class="spin" size={15} />{/if}保存配置</button></footer>
  </form>
</dialog>

<style>
  dialog { padding: 0; width: min(690px, calc(100vw - 28px)); max-height: 92dvh; border: 1px solid var(--line); border-radius: 18px; background: var(--content-raised); color: var(--text); box-shadow: var(--shadow-float); }
  dialog::backdrop { background: rgb(30 35 50 / 28%); backdrop-filter: blur(3px); }
  header { display: flex; gap: 13px; padding: 23px 25px 19px; border-bottom: 1px solid var(--line); align-items: center; }
  header .icon-button { margin-left: auto; }
  h2 { font-size: 17px; font-weight: 600; }
  header p { font-size: 11px; color: var(--text-dim); margin-top: 4px; }
  .heading-icon { display: flex; align-items: center; justify-content: center; width: 42px; height: 42px; border-radius: 12px; background: var(--blue-wash); color: var(--blue); flex-shrink: 0; }
  fieldset { border: 0; margin: 0; padding: 23px 25px 5px; min-width: 0; }
  label { display: block; font-size: 12px; font-weight: 550; margin-bottom: 17px; min-width: 0; }
  label input:not([type="checkbox"]), label select { display: block; width: 100%; margin-top: 7px; font-weight: 400; font-size: 12px; }
  .two-columns, .row-fields { display: grid; grid-template-columns: 1fr 1fr; gap: 13px; }
  .optional { color: var(--text-faint); font-size: 10px; font-weight: 400; margin-left: 5px; }
  .field-hint { font-size: 10px; color: var(--text-faint); margin: -10px 0 17px; }
  .voice-protocol { font-size: 11px; color: var(--text-dim); margin-bottom: 17px; }
  .check { display: flex; align-items: center; gap: 6px; font-weight: 400; margin-bottom: 10px; font-size: 11px; }
  .check input { accent-color: var(--accent); }
  .model-heading { display: flex; align-items: center; justify-content: space-between; margin: 22px 0 13px; }
  h3 { font-size: 13px; font-weight: 550; }
  .model-heading button { font-size: 10px; padding: 6px 9px; }
  .model-row { border: 1px solid var(--line); border-radius: 10px; padding: 12px 13px 0; margin-bottom: 10px; background: var(--content-soft); }
  .row-top { display: flex; align-items: center; gap: 9px; font-size: 10px; color: var(--text-dim); margin-bottom: 8px; }
  .row-top > span { margin-right: auto; }
  .row-top label { margin: 0; }
  .row-top button { width: 26px; height: 26px; }
  .row-fields label { font-size: 10px; margin-bottom: 12px; }
  .row-fields input, .row-fields select { font-size: 11px; padding: 8px 9px; }
  .row-test { display: flex; align-items: flex-end; padding-bottom: 12px; }
  .row-test button, .tts-test { font-size: 10px; padding: 8px 10px; }
  .tts-test { margin: 0 0 12px; }
  .add-model { color: var(--blue); font-size: 11px; padding: 6px 0; }
  .note { margin-top: 19px; }
  .feedback { padding: 0 25px; font-size: 12px; }
  .feedback p { margin-bottom: 15px; }
  .notice { display: flex; gap: 7px; align-items: center; color: var(--success); }
  footer { display: flex; align-items: center; gap: 9px; padding: 16px 25px; border-top: 1px solid var(--line); position: sticky; bottom: 0; background: var(--content-raised); }
  footer > span { font-size: 9px; color: var(--text-faint); margin-right: auto; max-width: 245px; }
  footer button { font-size: 12px; flex-shrink: 0; }
  @media (max-width: 600px) { header { padding: 19px; } fieldset { padding: 19px 19px 5px; } header p { font-size: 10px; } .two-columns { grid-template-columns: 1fr; gap: 0; } .row-fields { grid-template-columns: 1fr; gap: 0; } footer { padding: 14px 19px; } footer > span { max-width: 130px; } }
</style>
