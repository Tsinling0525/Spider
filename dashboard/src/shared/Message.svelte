<script lang="ts">
  import { t } from "../lib/i18n";
  import { Copy, Volume2, Check, Wrench, ChevronDown } from "lucide-svelte";
  import type { Message } from "../lib/dashboard";
  import { renderMarkdown } from "../lib/markdown";
  import Brand from "./Brand.svelte";
  let { message, speechEnabled, onspeak, speaking = false, onerror }: {
    message: Message; speechEnabled: boolean; onspeak: (text: string) => void; speaking?: boolean; onerror: (error: unknown) => void;
  } = $props();
  let copied = $state(false);
  let expanded = $state(false);
  async function copy() {
    try { await navigator.clipboard.writeText(message.content); copied = true; window.setTimeout(() => copied = false, 1600); }
    catch (error) { onerror(error); }
  }
</script>

{#if message.role === "tool"}
  <div class="tool-result">
    <button onclick={() => expanded = !expanded} aria-expanded={expanded}><Wrench size={14} />{$t("工具返回结果")}<ChevronDown size={14} /></button>
    {#if expanded}<pre>{message.content}</pre>{/if}
  </div>
{:else if message.role !== "system" && (message.content || message.tool_calls?.length)}
  <article class:user={message.role === "user"}>
    {#if message.role === "assistant"}<div class="avatar"><Brand size={24} /></div>{/if}
    <div class="body">
      {#if message.role === "assistant"}<div class="author">Spider <span>{$t("助手")}</span></div>{/if}
      {#if message.content}
        {#if message.role === "user"}<div class="user-text">{message.content}</div>
        {:else}<div class="markdown">{@html renderMarkdown(message.content)}</div>{/if}
      {/if}
      {#if message.tool_calls?.length}<div class="call"><Wrench size={14} />{$t("请求调用连接器工具")}</div>{/if}
      {#if message.role === "assistant" && message.content}
        <div class="actions">
          <button class="icon-button" aria-label={copied ? $t("已复制") : $t("复制回复")} onclick={copy}>{#if copied}<Check />{:else}<Copy />{/if}</button>
          <button class="icon-button" class:playing={speaking} aria-label={speaking ? $t("停止播放") : $t("播放语音回复")} disabled={!speechEnabled} onclick={() => onspeak(message.content)}><Volume2 /></button>
        </div>
      {/if}
    </div>
  </article>
{/if}

<style>
  article { display: flex; gap: 13px; margin-bottom: 26px; }
  .avatar { display: flex; align-items: center; justify-content: center; width: 33px; height: 33px; border: 1px solid var(--line); border-radius: 11px; flex-shrink: 0; }
  .body { min-width: 0; max-width: 100%; }
  .author { margin: 3px 0 12px; font-weight: 600; font-size: 13px; }
  .author span { margin-left: 6px; color: var(--text-faint); font-weight: 400; font-size: 11px; }
  .user { justify-content: flex-end; }
  .user .body { max-width: 86%; }
  .user-text { white-space: pre-wrap; overflow-wrap: anywhere; padding: 12px 18px; border-radius: 15px 15px 4px 15px; background: var(--content-soft); }
  .markdown { overflow-wrap: anywhere; line-height: 1.85; font-size: 14px; user-select: text; }
  .markdown :global(p) { margin: 0 0 12px; }
  .markdown :global(h1), .markdown :global(h2), .markdown :global(h3) { margin: 20px 0 10px; font-size: 17px; }
  .markdown :global(pre) { padding: 14px; overflow: auto; border-radius: 9px; background: var(--content-soft); font: 12px/1.7 var(--font-mono); }
  .markdown :global(code) { font-family: var(--font-mono); font-size: 12px; padding: 2px 4px; border-radius: 4px; background: var(--content-soft); }
  .markdown :global(pre code) { padding: 0; background: none; }
  .markdown :global(blockquote) { margin: 12px 0; padding-left: 16px; border-left: 3px solid var(--line-strong); color: var(--text-dim); }
  .markdown :global(table) { display: block; max-width: 100%; overflow: auto; border-collapse: collapse; margin: 12px 0; }
  .markdown :global(th), .markdown :global(td) { padding: 8px 12px; border: 1px solid var(--line); text-align: left; }
  .actions { display: flex; margin: 6px 0 0 -6px; }
  .actions button { width: 29px; height: 29px; color: var(--text-faint); }
  .actions button :global(svg) { width: 14px; height: 14px; }
  .playing { color: var(--accent) !important; }
  .call, .tool-result button { display: flex; gap: 7px; align-items: center; font-size: 12px; color: var(--text-dim); }
  .call { margin-top: 8px; }
  .tool-result { margin: -12px 0 24px 46px; }
  .tool-result button { padding: 6px 10px; border: 1px solid var(--line); border-radius: 8px; }
  .tool-result pre { max-height: 250px; overflow: auto; white-space: pre-wrap; background: var(--content-soft); padding: 12px; font: 12px/1.6 var(--font-mono); border-radius: 8px; }
</style>
