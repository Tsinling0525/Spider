<script lang="ts">
  import { t } from "../lib/i18n";
  import { onMount } from "svelte";
  import { X, Plug, LoaderCircle } from "lucide-svelte";
  import type { Connector, ConnectorInput } from "../lib/dashboard";
  import { parseHeadersText } from "../lib/connectors";
  let { connector, onsave, onclose }: { connector: Connector | null; onsave: (input: ConnectorInput) => Promise<void>; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let name = $state("");
  let url = $state("");
  let description = $state("");
  let enabled = $state(true);
  let headers = $state("");
  let clearHeaders = $state(false);
  let saving = $state(false);
  let error = $state("");
  onMount(() => {
    name = connector?.name || "";
    url = connector?.url || "";
    description = connector?.description || "";
    enabled = connector?.enabled ?? true;
    dialog.showModal();
  });
  async function save(event: SubmitEvent) {
    event.preventDefault(); error = ""; saving = true;
    try {
      const endpoint = new URL(url.trim());
      if (!["http:", "https:"].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error("请填写 HTTP(S) MCP 地址，不要在 URL 中放凭据");
      const input: ConnectorInput = { name: name.trim(), url: url.trim(), description: description.trim(), enabled };
      if (headers.trim()) input.headers = parseHeadersText(headers);
      else if (clearHeaders) input.headers = {};
      await onsave(input); onclose();
    } catch (cause) { error = cause instanceof Error ? cause.message : "保存失败"; }
    finally { saving = false; }
  }
</script>

<dialog bind:this={dialog} oncancel={(event) => { if (saving) event.preventDefault(); else onclose(); }}>
  <form onsubmit={save}>
    <header><div class="heading-icon"><Plug /></div><div><h2>{connector ? $t("编辑连接器") : $t("添加自定义连接器")}</h2><p>{$t("把你自己的工具连接到 Spider。")}</p></div><button type="button" class="icon-button" aria-label={$t("关闭")} disabled={saving} onclick={onclose}><X /></button></header>
    <div class="fields">
      <label>{$t("名称")}<input bind:value={name} placeholder={$t("例如：我的 DoorDash")} maxlength="80" required /></label>
      <label>{$t("MCP 服务地址")}<input type="url" bind:value={url} placeholder="http://127.0.0.1:3000/mcp" required /></label>
      <div class="protocol"><span class="badge">Streamable HTTP</span><span>{$t("支持本地与远程 MCP 服务")}</span></div>
      <label>{$t("描述")} <span class="optional">{$t("可选")}</span><input bind:value={description} placeholder={$t("这个连接器可以帮你做什么？")} maxlength="1024" /></label>
      <label>{$t("请求头")} <span class="optional">{$t("可选")}</span><textarea bind:value={headers} rows="3" placeholder={connector?.has_headers ? $t("已保存凭据，留空保留。填写新值则替换全部请求头。") : "Authorization: Bearer your-token"}></textarea></label>
      <p class="credential-note">{$t("凭据保存在本地服务端，不会出现在连接器列表中。")}</p>
      {#if connector?.has_headers}<label class="check"><input type="checkbox" bind:checked={clearHeaders} />{$t("清除已保存的请求头")}</label>{/if}
      <label class="check"><input type="checkbox" bind:checked={enabled} />{$t("启用这个连接器")}</label>
      {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
    </div>
    <footer><button class="secondary" type="button" disabled={saving} onclick={onclose}>{$t("取消")}</button><button class="primary" type="submit" disabled={saving}>{#if saving}<LoaderCircle class="spin" />{/if}{saving ? $t("正在保存…") : $t("保存连接器")}</button></footer>
  </form>
</dialog>

<style>
  dialog { border: 1px solid var(--line); border-radius: 18px; background: var(--content-raised); color: var(--text); padding: 0; width: min(540px, calc(100vw - 32px)); box-shadow: var(--shadow-float); max-height: 90vh; }
  dialog::backdrop { background: rgb(30 35 50 / 28%); backdrop-filter: blur(3px); }
  header { display: flex; gap: 13px; align-items: center; padding: 24px 24px 18px; border-bottom: 1px solid var(--line); }
  header .icon-button { margin-left: auto; }
  h2 { font-size: 17px; font-weight: 600; }
  header p { color: var(--text-dim); font-size: 12px; margin-top: 3px; }
  .heading-icon { width: 40px; height: 40px; border-radius: 12px; display: flex; align-items: center; justify-content: center; background: var(--blue-wash); color: var(--blue); }
  .fields { padding: 22px 24px; }
  label { display: block; font-size: 12px; font-weight: 550; margin-bottom: 17px; }
  label input:not([type="checkbox"]), label textarea { display: block; margin-top: 7px; width: 100%; font-weight: 400; font-size: 13px; }
  label textarea { font: 12px/1.7 var(--font-mono); }
  .optional { font-size: 11px; color: var(--text-faint); margin-left: 5px; font-weight: 400; }
  .protocol { display: flex; gap: 8px; align-items: center; margin: -9px 0 20px; color: var(--text-faint); font-size: 10px; }
  .credential-note { color: var(--text-faint); font-size: 11px; margin: -7px 0 17px; }
  .check { display: flex; align-items: center; gap: 8px; font-weight: 400; margin-bottom: 9px; }
  .check input { accent-color: var(--accent); }
  .error { font-size: 12px; margin-top: 16px; }
  footer { display: flex; justify-content: flex-end; gap: 9px; border-top: 1px solid var(--line); padding: 16px 24px; }
</style>
