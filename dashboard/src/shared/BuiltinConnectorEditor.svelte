<script lang="ts">
  import { t } from "../lib/i18n";
  import { onMount } from "svelte";
  import { X, ExternalLink, LoaderCircle, Check, RefreshCw, ShieldCheck } from "lucide-svelte";
  import type { Connector, DashboardPort } from "../lib/dashboard";
  import { mailProviders, type BuiltinConnectorPreset, type BuiltinConnectorInput } from "../lib/builtin-connectors";
  let { preset, connector, port, onsave, onauthorized, onclose }: {
    preset: BuiltinConnectorPreset; connector: Connector | null; port: DashboardPort;
    onsave: (input: BuiltinConnectorInput) => Promise<void>; onauthorized: (id: string) => Promise<void>; onclose: () => void;
  } = $props();
  let dialog: HTMLDialogElement;
  let name = $state("");
  let description = $state("");
  let enabled = $state(true);
  let secret = $state("");
  let provider = $state("qq");
  let email = $state("");
  let imapHost = $state("");
  let smtpHost = $state("");
  let imapPort = $state<number | undefined>(993);
  let smtpPort = $state<number | undefined>(587);
  let manual = $state(false);
  let working = $state(false);
  let authorizing = $state(false);
  let error = $state("");
  let notice = $state("");
  let authURL = $state("");
  let authState = "";
  let popup: Window | null = null;
  let timer: ReturnType<typeof setInterval> | undefined;
  let checking = false;
  let disposed = false;
  const mailProvider = $derived(mailProviders.find((item) => item.id === provider) || mailProviders[0]);
  const secretKey = $derived(preset.authKind === "mail" ? "password" : preset.authKind === "oauth" ? "access_token" : preset.authKind === "token" ? "token" : "api_key");
  const secretLabel = $derived(preset.authKind === "mail" ? "授权码" : preset.authKind === "token" || preset.kind === "baidu-map" || preset.authKind === "oauth" ? "Token" : "API Key");
  onMount(() => {
    name = connector?.name || $t(preset.name); description = connector?.description || $t(preset.description); enabled = connector?.enabled ?? true;
    provider = connector?.credentials?.mail_provider || "qq"; email = connector?.credentials?.email || "";
    imapHost = connector?.credentials?.imap_host || ""; smtpHost = connector?.credentials?.smtp_host || "";
    imapPort = Number(connector?.credentials?.imap_port || "993"); smtpPort = Number(connector?.credentials?.smtp_port || "587");
    dialog.showModal();
    const handleMessage = (event: MessageEvent) => { if (event.origin === window.location.origin && event.data?.type === "spider-connector-oauth" && event.data.state === authState && (!popup || event.source === popup)) void checkOAuth(); };
    window.addEventListener("message", handleMessage);
    return () => { disposed = true; clearInterval(timer); window.removeEventListener("message", handleMessage); };
  });
  function input(includeSecret = true): BuiltinConnectorInput {
    if (!name.trim()) throw new Error("请填写连接器名称");
    const credentials: Record<string, string> = {};
    if (includeSecret) {
      if (secret.trim()) credentials[secretKey] = secret.trim();
      else if (!connector?.has_credentials) throw new Error("请填写凭据或使用一键授权");
      if (preset.authKind === "mail") {
        if (!email.trim()) throw new Error("请填写邮箱地址");
        credentials.mail_provider = provider; credentials.email = email.trim();
        if (provider === "custom") {
          if (!imapHost.trim() || !smtpHost.trim()) throw new Error("请填写 IMAP 和 SMTP 服务器");
          if (!imapPort || !smtpPort || imapPort < 1 || smtpPort < 1 || imapPort > 65535 || smtpPort > 65535) throw new Error("请填写有效的邮箱端口");
          Object.assign(credentials, { imap_host: imapHost.trim(), smtp_host: smtpHost.trim(), imap_port: String(imapPort), smtp_port: String(smtpPort) });
        }
      }
    }
    return { kind: preset.kind, name: name.trim(), description: description.trim(), enabled, credentials };
  }
  async function save(event: SubmitEvent) {
    event.preventDefault(); error = ""; notice = ""; working = true;
    try { await onsave(input()); onclose(); } catch (cause) { error = cause instanceof Error ? cause.message : "保存失败"; }
    finally { working = false; }
  }
  async function test() {
    error = ""; notice = ""; working = true;
    try { notice = (await port.testBuiltinConnector(connector?.id ?? null, input())).message; }
    catch (cause) { error = cause instanceof Error ? cause.message : "连接失败"; }
    finally { working = false; }
  }
  async function checkOAuth() {
    if (!authState || checking || disposed) return;
    checking = true;
    try {
      const status = await port.connectorOAuthStatus(authState);
      if (disposed) return;
      if (status.status === "authorized") {
        clearInterval(timer); await onauthorized(status.connector_id); if (!disposed) onclose();
      } else if (status.status === "error") { clearInterval(timer); authorizing = false; error = status.error || "授权失败，请重试"; }
    } catch (cause) { clearInterval(timer); authorizing = false; error = cause instanceof Error ? cause.message : "授权失败"; }
    finally { checking = false; }
  }
  async function authorize() {
    error = ""; notice = "";
    let draft: BuiltinConnectorInput;
    try { draft = input(false); } catch (cause) { error = (cause as Error).message; return; }
    // Open synchronously on the user's click so popup blockers do not discard
    // the authorization window while the server registers the OAuth client.
    popup = window.open("", "spider-notion-oauth", "popup,width=700,height=760");
    authorizing = true; clearInterval(timer);
    try {
      const callback = new URL(`${import.meta.env.DEV ? "/spider-api" : ""}/v1/dashboard/connectors/oauth/callback`, window.location.origin).href;
      const result = await port.startConnectorOAuth(connector?.id ?? null, draft, callback);
      if (disposed) { popup?.close(); return; }
      authState = result.state; authURL = result.authorize_url;
      if (popup) popup.location.href = authURL;
      timer = setInterval(() => void checkOAuth(), 2000);
    } catch (cause) { popup?.close(); authorizing = false; error = cause instanceof Error ? cause.message : "无法开始授权"; }
  }
</script>

<dialog bind:this={dialog} oncancel={(event) => { if (working) event.preventDefault(); else onclose(); }}>
  <form onsubmit={save}>
    <header><img src={preset.logo} alt="" /><div><h2>{connector ? $t("编辑") : $t("连接")} {$t(preset.name)}</h2><p>{$t(preset.description)}</p></div><button type="button" class="icon-button" aria-label={$t("关闭")} disabled={working} onclick={onclose}><X /></button></header>
    <div class="fields">
      <div class="auth-guide"><ShieldCheck size={17} /><p>{$t(preset.hint)}</p></div>
      {#if preset.authorizeURL}<div class="guide-actions"><a class="secondary" href={preset.authorizeURL} target="_blank" rel="noreferrer">{$t("打开授权页")}<ExternalLink size={13} /></a><a href={preset.guideURL} target="_blank" rel="noreferrer">{$t("使用指南")}<ExternalLink size={12} /></a></div>{/if}
      <fieldset disabled={working || authorizing}>
        <label>{$t("连接器名称")}<input bind:value={name} maxlength="80" required /></label>
        <label>{$t("描述")}<input bind:value={description} maxlength="1024" /></label>
        {#if preset.authKind === "mail"}
          <label>{$t("邮箱服务商")}<select bind:value={provider}>{#each mailProviders as item}<option value={item.id}>{$t(item.label)}</option>{/each}</select></label>
          <label>{$t("邮箱地址")}<input type="email" bind:value={email} placeholder={mailProvider.placeholder} required /></label>
        {/if}
        {#if preset.authKind !== "oauth" || manual}
          <label>{$t(secretLabel)}<input type="password" bind:value={secret} autocomplete="new-password" placeholder={connector?.has_credentials ? $t("已保存凭据，留空表示不修改") : preset.kind === "baidu-map" ? "sk-ap-…" : $t("粘贴你的{0}", { 0: $t(secretLabel) })} required={!connector?.has_credentials} /></label>
          {#if preset.authKind === "mail" && mailProvider.guideURL}<a class="inline-guide" href={mailProvider.guideURL} target="_blank" rel="noreferrer">{$t("如何获取邮箱授权码")}<ExternalLink size={12} /></a>{/if}
        {/if}
        {#if preset.authKind === "mail" && provider === "custom"}
          <div class="host-row"><label>{$t("IMAP 服务器")}<input bind:value={imapHost} placeholder="imap.example.com" required /></label><label>{$t("IMAP 端口")}<input type="number" min="1" max="65535" bind:value={imapPort} required /></label></div>
          <div class="host-row"><label>{$t("SMTP 服务器")}<input bind:value={smtpHost} placeholder="smtp.example.com" required /></label><label>{$t("SMTP 端口")}<input type="number" min="1" max="65535" bind:value={smtpPort} required /></label></div>
          <p class="small-note">{$t("IMAP 使用 TLS，SMTP 使用 STARTTLS。")}</p>
        {/if}
        <label class="check"><input type="checkbox" bind:checked={enabled} />{$t("启用这个连接器")}</label>
      </fieldset>
      {#if preset.authKind === "oauth"}
        <div class="oauth"><button type="button" class="primary" disabled={working || authorizing} onclick={authorize}>{#if authorizing}<LoaderCircle class="spin" size={15} />{/if}{authorizing ? $t("等待 Notion 授权…") : connector ? $t("重新授权 Notion") : $t("一键授权")}<ExternalLink size={13} /></button><a href={preset.guideURL} target="_blank" rel="noreferrer">{$t("使用指南")}</a></div>
        {#if authorizing && authURL}<p class="small-note">{$t("在授权窗口中登录并选择工作空间。")}<a href={authURL} target="spider-notion-oauth" rel="noreferrer">{$t("打开授权窗口")}</a><button type="button" onclick={() => { clearInterval(timer); authorizing = false; }}>{$t("取消等待")}</button></p>{/if}
        <button type="button" class="manual-toggle" disabled={working || authorizing} onclick={() => manual = !manual}>{manual ? $t("收起手动 Token") : $t("手动填写已有 MCP Token")}</button>
      {/if}
      <p class="credential-note">{$t("凭据保存在本地服务端；编辑时留空保留。")}</p>
      {#if notice}<p class="notice" role="status"><Check size={14} />{$t(notice)}</p>{/if}
      {#if error}<p class="error" role="alert">{$t(error)}</p>{/if}
    </div>
    <footer><button class="secondary" type="button" disabled={working} onclick={onclose}>{$t("取消")}</button><button class="secondary" type="button" disabled={working || authorizing || (preset.authKind === "oauth" && !manual && !connector)} onclick={test}>{#if working}<LoaderCircle class="spin" size={14} />{:else}<RefreshCw size={14} />{/if}{$t("测试连接")}</button><button class="primary" type="submit" disabled={working || authorizing || (preset.authKind === "oauth" && !manual && !connector)}>{#if working}<LoaderCircle class="spin" size={14} />{/if}{$t("保存连接器")}</button></footer>
  </form>
</dialog>

<style>
  dialog { border: 1px solid var(--line); border-radius: 18px; background: var(--content-raised); color: var(--text); padding: 0; width: min(550px, calc(100vw - 32px)); box-shadow: var(--shadow-float); max-height: 90vh; }
  dialog::backdrop { background: rgb(30 35 50 / 28%); backdrop-filter: blur(3px); }
  header { display: flex; gap: 13px; align-items: center; padding: 24px; border-bottom: 1px solid var(--line); }
  header img { width: 42px; height: 42px; object-fit: contain; background: #fff; border-radius: 7px; } header .icon-button { margin-left: auto; flex-shrink: 0; }
  h2 { font-size: 17px; font-weight: 600; } header p { color: var(--text-dim); font-size: 12px; margin-top: 4px; }
  .fields { padding: 22px 24px; } fieldset { border: 0; padding: 0; margin: 0; min-width: 0; }
  .auth-guide { display: flex; gap: 9px; color: var(--text-dim); font-size: 12px; line-height: 1.7; background: var(--content-soft); border-radius: 9px; padding: 13px; margin-bottom: 18px; } .auth-guide :global(svg) { flex-shrink: 0; margin-top: 2px; color: var(--blue); }
  .guide-actions { display: flex; gap: 16px; align-items: center; margin-bottom: 22px; } a { font-size: 11px; color: var(--blue); display: inline-flex; gap: 5px; align-items: center; text-decoration: none; }
  label { display: block; font-size: 12px; font-weight: 550; margin-bottom: 17px; } label input:not([type="checkbox"]), label select { display: block; margin-top: 7px; width: 100%; font-weight: 400; font-size: 13px; }
  .host-row { display: grid; grid-template-columns: minmax(0,1fr) 100px; gap: 12px; }
  .inline-guide { margin: -7px 0 20px; } .small-note, .credential-note { font-size: 11px; line-height: 1.7; color: var(--text-faint); margin-bottom: 15px; } .small-note button { color: var(--blue); font-size: 11px; margin-left: 10px; } .credential-note { margin: 16px 0 0; }
  .check { display: flex; align-items: center; gap: 8px; font-weight: 400; margin-bottom: 9px; } .check input { accent-color: var(--accent); }
  .oauth { display: flex; align-items: center; gap: 15px; margin: 18px 0 14px; } .manual-toggle { font-size: 11px; color: var(--text-dim); text-decoration: underline; padding: 0; }
  .error { font-size: 12px; margin-top: 16px; } .notice { display: flex; gap: 7px; font-size: 12px; color: var(--success); margin-top: 16px; }
  footer { display: flex; justify-content: flex-end; gap: 9px; border-top: 1px solid var(--line); padding: 16px 24px; position: sticky; bottom: 0; background: var(--content-raised); } footer button { font-size: 12px; }
</style>
