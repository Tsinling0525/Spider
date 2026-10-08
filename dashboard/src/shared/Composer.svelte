<script lang="ts">
  import { t } from "../lib/i18n";
  import { Plus, X, Mic, Square, ArrowUp, AudioLines, Plug, Check, LoaderCircle } from "lucide-svelte";
  import type { Connector } from "../lib/dashboard";
  import { emptyModelConfiguration, modelOptions, selectionValue, parseSelection, type ModelConfiguration, type ModelSelection } from "../lib/models";
  let { draft = $bindable(""), selected = $bindable<string[]>([]), connectors, busy, locked, recording, transcribing, voiceEnabled, speechEnabled, autoSpeak = $bindable(false), modelConfiguration = emptyModelConfiguration(), onsend, onrecord, onmanage, onmodelselect, onmodelmanage }: {
    draft?: string; selected?: string[]; connectors: Connector[]; busy: boolean; locked: boolean;
    recording: boolean; transcribing: boolean; voiceEnabled: boolean; speechEnabled: boolean; autoSpeak?: boolean;
    onsend: () => void; onrecord: () => void; onmanage: () => void;
    modelConfiguration?: ModelConfiguration; onmodelselect?: (selection: ModelSelection) => void; onmodelmanage?: () => void;
  } = $props();
  let pickerOpen = $state(false);
  const available = $derived(connectors.filter((connector) => connector.enabled && connector.checked_at));
  const chatModels = $derived(modelOptions(modelConfiguration, "chat"));
  function toggle(id: string) { selected = selected.includes(id) ? selected.filter((item) => item !== id) : [...selected, id]; }
</script>

<div class="dock">
  <div class="composer" class:recording>
    {#if selected.length}
      <div class="chips">{#each selected as id}{@const connector = connectors.find((item) => item.id === id)}
        <span class="chip"><Plug size={13} />{connector?.name || $t("已移除连接器")}<button aria-label={$t("取消选择 {0}", { 0: connector?.name || $t("连接器") })} disabled={busy || locked} onclick={() => toggle(id)}><X size={12} /></button></span>
      {/each}</div>
    {/if}
    <textarea aria-label={$t("消息")} bind:value={draft} placeholder={recording ? $t("正在聆听…点击停止，转换成文字") : $t("和 Spider 说点什么吧…")} rows="3" disabled={busy || locked || recording || transcribing}
      onkeydown={(event) => { if (event.key === "Enter" && !event.shiftKey && !event.isComposing && event.keyCode !== 229) { event.preventDefault(); if (draft.trim() && !busy && !locked) onsend(); } }}></textarea>
    <div class="toolbar">
      <div class="left">
        <div class="picker-wrap">
          <button class="round add" aria-label={$t("选择连接器")} aria-expanded={pickerOpen} disabled={busy || locked} onclick={() => pickerOpen = !pickerOpen}><Plus /></button>
          {#if pickerOpen}
            <div class="picker">
              <div class="picker-title">{$t("本次对话的连接器")} <button class="icon-button" aria-label={$t("关闭连接器选择")} onclick={() => pickerOpen = false}><X /></button></div>
              {#each available as connector}<button class="picker-item" onclick={() => toggle(connector.id)}><Plug size={16} /><span>{connector.name}<small>{$t("{0} 个工具", { 0: connector.tools.length })}</small></span>{#if selected.includes(connector.id)}<Check size={16} />{/if}</button>{/each}
              {#if !available.length}<p>{$t("添加并测试连接器后，即可在这里选择。")}</p>{/if}
              <button class="manage" onclick={() => { pickerOpen = false; onmanage(); }}><Plus size={15} />{$t("管理连接器")}</button>
            </div>
          {/if}
        </div>
        {#if chatModels.length}<select class="model" aria-label={$t("聊天模型")} value={selectionValue(modelConfiguration.active.chat)} disabled={busy || locked || recording || transcribing} onchange={(event) => onmodelselect?.(parseSelection(event.currentTarget.value))}><option value="">{$t("选择聊天模型")}</option>{#each chatModels as option}<option value={selectionValue({ provider_id: option.provider.id, model_id: option.model.id })}>{option.model.label || option.model.id} · {option.provider.name}</option>{/each}</select>{:else}<button class="model" onclick={onmodelmanage}>{$t("配置模型")}</button>{/if}
      </div>
      <div class="right">
        <button class="round" class:active={autoSpeak} aria-label={$t("自动朗读回复")} aria-pressed={autoSpeak} title={$t("自动朗读回复")} disabled={!speechEnabled} onclick={() => autoSpeak = !autoSpeak}><AudioLines /></button>
        <button class="round" class:record-button={recording} aria-label={recording ? $t("停止录音") : $t("开始录音")} title={voiceEnabled ? $t("语音输入") : $t("需配置语音转写模型")} disabled={!voiceEnabled || busy || locked || transcribing} onclick={onrecord}>{#if recording}<Square size={15} />{:else if transcribing}<LoaderCircle class="spin" />{:else}<Mic />{/if}</button>
        <button class="send" aria-label={$t("发送消息")} disabled={!draft.trim() || busy || locked || recording || transcribing} onclick={onsend}>{#if busy}<LoaderCircle class="spin" />{:else}<ArrowUp />{/if}</button>
      </div>
    </div>
  </div>
  <div class="hint">{#if recording}<span class="live-dot"></span>{$t("正在录音 · 最长 2 分钟")}{:else if transcribing}{$t("正在将语音转换成文字…")}{:else}{$t("Enter 发送，Shift + Enter 换行")} <span>·</span> {$t("AI 回复请留意核实")}{/if}</div>
</div>

<style>
  .dock { width: min(100%, var(--frame)); margin: 0 auto; padding: 0 28px 14px; }
  .composer { border: 1px solid color-mix(in srgb, var(--accent) 18%, var(--line)); border-radius: 20px; padding: 14px 16px 12px; background: var(--content-plane); box-shadow: var(--shadow-raised); transition: border-color var(--motion-state), box-shadow var(--motion-state); }
  .composer:focus-within { border-color: color-mix(in srgb, var(--accent) 45%, var(--line)); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 5%, transparent), var(--shadow-raised); }
  textarea { display: block; width: 100%; min-height: 84px; max-height: 170px; border: 0; padding: 4px 1px; resize: none; outline: none !important; font-size: 14px; line-height: 1.7; }
  .toolbar, .left, .right { display: flex; align-items: center; gap: 9px; }
  .toolbar { justify-content: space-between; border-top: 1px solid var(--line); padding-top: 10px; }
  .round { border: 1px solid var(--line); width: 33px; height: 33px; border-radius: 50%; color: var(--text-faint); background: var(--content-soft); }
  .round :global(svg) { width: 16px; height: 16px; }
  .add { color: var(--blue); background: var(--blue-wash); border-color: color-mix(in srgb, var(--blue) 25%, var(--line)); }
  .send { background: var(--accent); border-radius: 12px; color: white; width: 36px; height: 36px; }
  .send:not(:disabled):hover { background: color-mix(in srgb, var(--accent) 85%, black); }
  .send:disabled { opacity: 1; background: color-mix(in srgb, var(--accent) 32%, var(--content-plane)); }
  .model { color: var(--text-dim); font-size: 11px; padding: 5px; border: 0; max-width: 260px; background: transparent; }
  .hint { text-align: center; color: var(--text-faint); font-size: 10px; padding-top: 10px; }
  .hint span:not(.live-dot) { margin: 0 6px; }
  .chips { display: flex; gap: 6px; flex-wrap: wrap; margin: 0 0 7px; }
  .chip { display: inline-flex; align-items: center; gap: 5px; padding: 3px 7px; border: 1px solid #d9e2ff; color: var(--blue); background: var(--blue-wash); border-radius: 6px; font-size: 11px; }
  .chip button { padding: 0; color: inherit; }
  .picker-wrap { position: relative; }
  .picker { position: absolute; bottom: 45px; left: -6px; width: 270px; max-height: 330px; overflow-y: auto; padding: 8px; border: 1px solid var(--line); background: var(--content-raised); border-radius: 13px; box-shadow: var(--shadow-float); z-index: 3; }
  .picker-title { display: flex; align-items: center; justify-content: space-between; font-size: 12px; font-weight: 600; padding: 0 3px 6px 7px; }
  .picker-item { width: 100%; border-radius: 8px; justify-content: flex-start; padding: 10px; gap: 10px; text-align: left; }
  .picker-item span { flex: 1; }
  .picker-item small { display: block; font-size: 10px; color: var(--text-faint); }
  .picker p { color: var(--text-dim); font-size: 12px; padding: 12px; }
  .manage { width: 100%; border-top: 1px solid var(--line); margin-top: 4px; padding: 10px; font-size: 12px; color: var(--blue); }
  .active { color: var(--accent); background: var(--accent-wash); }
  .recording { border-color: var(--accent); }
  .record-button { background: var(--accent-wash); color: var(--accent); border-color: var(--accent); }
  .live-dot { display: inline-block; width: 5px; height: 5px; border-radius: 50%; background: var(--accent); margin-right: 5px; }
  :global(.spin) { animation: spin 1s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (max-width: 700px) { .dock { padding: 0 14px 12px; } .model { max-width: 135px; font-size: 10px; } .toolbar, .left, .right { gap: 5px; } }
</style>
