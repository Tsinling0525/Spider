<script lang="ts">
  import { Plug, Plus, Search, ArrowUpRight, Ellipsis, RefreshCw, Pencil, Trash2, Wrench, Check, Power, LoaderCircle, ChevronDown, Blocks, Link } from "lucide-svelte";
  import type { Connector, ConnectorInput, DashboardPort } from "../lib/dashboard";
  import { accentForServerName } from "../lib/connectors";
  import ConnectorEditor from "./ConnectorEditor.svelte";
  import BuiltinConnectorEditor from "./BuiltinConnectorEditor.svelte";
  import { builtinConnectorPresets, builtinPreset, type BuiltinConnectorPreset, type BuiltinConnectorInput } from "../lib/builtin-connectors";
  let { connectors, port, onrefresh, onchat, onerror }: {
    connectors: Connector[]; port: DashboardPort; onrefresh: () => Promise<void>; onchat: (id: string) => void; onerror: (error: unknown) => void;
  } = $props();
  let search = $state("");
  let editor = $state(false);
  let editing = $state<Connector | null>(null);
  let builtinEditing = $state<BuiltinConnectorPreset | null>(null);
  let tab = $state<"enabled" | "builtin" | "custom">("enabled");
  let probing = $state<string | null>(null);
  let menu = $state<string | null>(null);
  let expanded = $state<string | null>(null);
  let notice = $state("");
  let confirmingDelete = $state<string | null>(null);
  const filtered = $derived(connectors.filter((item) => (tab === "custom" ? !item.kind : item.enabled) && `${item.name} ${item.description}`.toLowerCase().includes(search.toLowerCase())));
  const presets = $derived(builtinConnectorPresets.filter((item) => `${item.name} ${item.description}`.toLowerCase().includes(search.toLowerCase())));
  const configuredBuiltins = $derived(connectors.filter((item) => item.kind).length);
  const tabs = [{ id: "enabled", label: "已启用连接器", icon: Link }, { id: "builtin", label: "内置连接器", icon: Blocks }, { id: "custom", label: "自定义连接器", icon: Wrench }] as const;
  function changeTab(next: typeof tab) { tab = next; search = ""; notice = ""; menu = null; expanded = null; }
  function tabKey(event: KeyboardEvent, index: number) {
    let next = index;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (index + tabs.length - 1) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    else return;
    event.preventDefault(); changeTab(tabs[next].id); document.getElementById(`connectors-tab-${tabs[next].id}`)?.focus();
  }
  function edit(connector: Connector) { editing = connector; builtinEditing = builtinPreset(connector.kind) ?? null; editor = !builtinEditing; menu = null; }
  async function save(input: ConnectorInput) { await port.saveConnector(editing?.id || null, input); await onrefresh(); notice = "已保存。测试连接后，就可以在对话中选择它。"; }
  async function saveBuiltin(input: BuiltinConnectorInput) { await port.saveBuiltinConnector(editing?.id || null, input); await onrefresh(); notice = "已保存。测试连接后，就可以在对话中选择它。"; }
  async function authorized(id: string) { await onrefresh(); changeTab("enabled"); await probe(id); }
  async function probe(id: string) {
    probing = id; notice = "";
    try { const tested = await port.probeConnector(id); await onrefresh(); notice = `${tested.name} 已连接，发现 ${tested.tools.length} 个工具。`; }
    catch (error) { onerror(error); }
    finally { probing = null; }
  }
  async function remove(id: string) { try { await port.deleteConnector(id); await onrefresh(); confirmingDelete = null; } catch (error) { onerror(error); } }
  async function toggle(connector: Connector) {
    try {
      if (connector.kind) await port.saveBuiltinConnector(connector.id, { kind: builtinPreset(connector.kind)!.kind, name: connector.name, description: connector.description, enabled: !connector.enabled });
      else await port.saveConnector(connector.id, { name: connector.name, url: connector.url, description: connector.description, enabled: !connector.enabled });
      await onrefresh();
    }
    catch (error) { onerror(error); }
    menu = null;
  }
</script>

<section class="connectors-page">
  <div class="eyebrow">你的工具，你的工作方式</div>
  <div class="page-heading"><div><h1>连接器</h1><p>账户级连接文档、邮箱与出行服务；对话时按需勾选。</p></div><button class="primary" onclick={() => { editing = null; builtinEditing = null; editor = true; }}><Plus />添加连接器</button></div>
  <div class="connector-tabs" role="tablist" aria-label="连接器类别">{#each tabs as item, index}<button role="tab" id={`connectors-tab-${item.id}`} aria-selected={tab === item.id} aria-controls={`connectors-panel-${item.id}`} tabindex={tab === item.id ? 0 : -1} class:active={tab === item.id} onclick={() => changeTab(item.id)} onkeydown={(event) => tabKey(event, index)}><item.icon size={15} />{item.label}</button>{/each}</div>
  <div class="list-heading"><span>{tab === "builtin" ? `当前支持 ${builtinConnectorPresets.length} 个连接器，已配置 ${configuredBuiltins} 个` : tab === "custom" ? `自定义连接器 ${filtered.length} 个` : `已启用 ${filtered.length} 个连接器`} </span><div class="list-actions"><label class="search"><Search size={15} /><input aria-label="搜索连接器" bind:value={search} placeholder="搜索连接器" /></label><button class="secondary refresh" onclick={() => void onrefresh().catch(onerror)}><RefreshCw size={14} />刷新</button></div></div>
  {#if notice}<div class="notice" role="status"><Check size={15} />{notice}</div>{/if}
  <div role="tabpanel" id={`connectors-panel-${tab}`} aria-labelledby={`connectors-tab-${tab}`}>
  {#if tab === "builtin"}
    <div class="builtin-cards">
      {#each presets as preset}
        <button class="builtin-card" class:monochrome={preset.kind === "notion"} style={`--connector-accent: ${preset.color}`} aria-label={`连接 ${preset.name}`} onclick={() => { editing = null; editor = false; builtinEditing = preset; }}>
          <div class="builtin-top"><img src={preset.logo} alt="" /><div><h3>{preset.name}</h3><span class="category">{preset.category}</span></div></div>
          <p>{preset.description}</p><div class="builtin-bottom"><span>点击连接</span>{#if connectors.some((item) => item.kind === preset.kind)}<small><Check size={12} />已配置</small>{/if}</div>
        </button>
      {/each}
    </div>
    {#if !presets.length}<div class="empty-search">没有找到匹配的内置连接器。</div>{/if}
  {:else}
  <div class="cards">
    {#each filtered as connector}
      <article>
        <div class="card-top"><div class="connector-icon" style={`--connector-accent: ${builtinPreset(connector.kind)?.color || accentForServerName(connector.name)}`}>{#if builtinPreset(connector.kind)}<img src={builtinPreset(connector.kind)!.logo} alt="" />{:else}<Plug size={25} />{/if}</div>
          <div class="card-name"><h3>{connector.name}</h3><span class="badge">{builtinPreset(connector.kind)?.name || "自定义 MCP"}</span></div>
          <div class="menu-wrap"><button class="icon-button" aria-label={`${connector.name} 操作`} aria-expanded={menu === connector.id} onclick={() => menu = menu === connector.id ? null : connector.id}><Ellipsis /></button>
            {#if menu === connector.id}<div class="menu"><button onclick={() => edit(connector)}><Pencil size={14} />编辑配置</button><button onclick={() => toggle(connector)}><Power size={14} />{connector.enabled ? "停用" : "启用"}</button><button class="error" onclick={() => { confirmingDelete = connector.id; menu = null; }}><Trash2 size={14} />删除连接器</button></div>{/if}
          </div>
        </div>
        <p class="description">{connector.description || "连接你自己的 MCP 工具服务。"}</p>
        <div class="endpoint">{connector.url}</div>
        <div class="card-bottom"><span class="status" class:connected={connector.enabled && !!connector.checked_at}><i></i>{!connector.enabled ? "已停用" : connector.checked_at ? "已测试" : "待测试"}</span><button class="test" disabled={probing !== null} onclick={() => probe(connector.id)}>{#if probing === connector.id}<LoaderCircle class="spin" size={13} />{:else}<RefreshCw size={13} />{/if}{probing === connector.id ? "测试中…" : "测试连接"}</button></div>
        {#if connector.checked_at}
          <div class="tool-row"><button onclick={() => expanded = expanded === connector.id ? null : connector.id} aria-expanded={expanded === connector.id}><Wrench size={13} />{connector.tools.length} 个工具<ChevronDown size={13} /></button><button class="use" disabled={!connector.enabled} onclick={() => onchat(connector.id)}>在对话中使用<ArrowUpRight size={13} /></button></div>
          {#if expanded === connector.id}<div class="tool-list">{#each connector.tools as tool}<div><strong>{tool.name}</strong><p>{tool.description}</p></div>{/each}</div>{/if}
        {/if}
        {#if confirmingDelete === connector.id}<div class="delete-confirm"><p>删除「{connector.name}」的配置？</p><div><button class="secondary" onclick={() => confirmingDelete = null}>取消</button><button class="primary" onclick={() => remove(connector.id)}>删除</button></div></div>{/if}
      </article>
    {/each}
    {#if !search}<button class="add-card" onclick={() => { if (tab === "enabled") changeTab("builtin"); else { editing = null; builtinEditing = null; editor = true; } }}><span class="add-icon"><Plus size={24} /></span><strong>{tab === "enabled" ? "连接更多服务" : "连接你自己的工具"}</strong><span>{tab === "enabled" ? "浏览内置连接器，添加你的常用服务" : "添加一个 MCP 服务，开始新的可能"}</span></button>{/if}
  </div>
  {#if search && !filtered.length}<div class="empty-search">没有找到匹配的连接器。</div>{/if}
  {#if !search && tab === "enabled" && connectors.some((item) => !item.enabled)}<div class="disabled-connectors"><h3>已停用连接器</h3>{#each connectors.filter((item) => !item.enabled) as item}<div><span>{item.name}</span><button class="secondary" onclick={() => edit(item)}>编辑配置</button><button class="secondary" onclick={() => toggle(item)}>启用</button></div>{/each}</div>{/if}
  {/if}
  </div>
  <div class="footnote"><Plug size={13} />支持 Streamable HTTP；服务端自动处理 JSON 和 SSE 响应。</div>
</section>
{#if editor}<ConnectorEditor connector={editing} onsave={save} onclose={() => editor = false} />{/if}
{#if builtinEditing}<BuiltinConnectorEditor preset={builtinEditing} connector={editing} {port} onsave={saveBuiltin} onauthorized={authorized} onclose={() => builtinEditing = null} />{/if}

<style>
  .connectors-page { width: min(100%, 1440px); margin: 0 auto; padding: 47px 48px; }
  .eyebrow { font-size: 11px; color: var(--text-faint); margin-bottom: 7px; }
  .page-heading { display: flex; justify-content: space-between; align-items: center; gap: 20px; margin-bottom: 30px; }
  h1 { font-size: 27px; letter-spacing: -.7px; font-weight: 620; }
  .page-heading p { font-size: 13px; color: var(--text-dim); margin-top: 8px; }
  .page-heading button { font-size: 12px; flex-shrink: 0; }
  h3 { font-size: 14px; font-weight: 600; }
  .connector-tabs { display: flex; border-bottom: 1px solid var(--line); gap: 15px; }
  .connector-tabs button { gap: 7px; font-size: 12px; padding: 15px 10px; border-bottom: 2px solid transparent; color: var(--text-faint); }
  .connector-tabs .active { color: var(--accent); border-color: var(--accent); }
  .list-heading { display: flex; align-items: center; justify-content: space-between; margin: 22px 0 20px; gap: 15px; color: var(--text-dim); font-size: 12px; }
  .list-actions { display: flex; align-items: center; gap: 16px; } .refresh { font-size: 11px; }
  .builtin-cards { display: grid; grid-template-columns: repeat(3, minmax(0,1fr)); gap: 16px; }
  .builtin-card { display: flex; flex-direction: column; align-items: stretch; text-align: left; padding: 23px; min-height: 185px; border: 1px solid var(--line-strong); border-radius: 12px; background: var(--content-plane); transition: border-color .15s, box-shadow .15s; }
  .builtin-card:hover { border-color: var(--connector-accent); box-shadow: 0 3px 12px rgb(15 23 42 / 4%); }
  .builtin-top { display: flex; gap: 12px; align-items: center; } .builtin-top img { width: 40px; height: 40px; object-fit: contain; flex-shrink: 0; background: #fff; border-radius: 7px; }
  :global(:root[data-theme="dark"]) .builtin-card.monochrome { --connector-accent: var(--text) !important; }
  .category { display: inline-block; color: var(--connector-accent); background: color-mix(in srgb, var(--connector-accent) 9%, var(--content-plane)); border-radius: 8px; padding: 2px 7px; font-size: 9px; margin-top: 5px; }
  .builtin-card p { font-size: 12px; line-height: 1.7; color: var(--text-faint); margin: 16px 0 20px; flex: 1; }
  .builtin-bottom { display: flex; align-items: center; justify-content: space-between; color: var(--connector-accent); font-size: 11px; font-weight: 550; } .builtin-bottom small { color: var(--success); display: flex; gap: 3px; font-size: 10px; }
  .disabled-connectors { border-top: 1px solid var(--line); margin-top: 28px; padding-top: 20px; } .disabled-connectors > div { display: flex; gap: 8px; align-items: center; margin-top: 12px; font-size: 12px; } .disabled-connectors span { flex: 1; } .disabled-connectors button { font-size: 11px; }
  .search { display: flex; gap: 7px; align-items: center; color: var(--text-faint); }
  .search input { border: 0; background: none; padding: 3px; font-size: 12px; width: 150px; }
  .cards { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
  article { padding: 21px; border: 1px solid var(--line-strong); border-radius: 13px; position: relative; min-width: 0; }
  .card-top { display: flex; align-items: center; gap: 12px; }
  .connector-icon { display: flex; align-items: center; justify-content: center; width: 46px; height: 46px; border-radius: 12px; color: var(--connector-accent); background: color-mix(in srgb, var(--connector-accent) 9%, var(--content-plane)); }
  .connector-icon img { width: 36px; height: 36px; object-fit: contain; background: #fff; border-radius: 7px; }
  .card-name { min-width: 0; }
  .card-name h3 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-bottom: 5px; }
  .card-name .badge { font-size: 9px; padding: 1px 5px; }
  .description { color: var(--text-dim); font-size: 12px; margin: 19px 0 10px; min-height: 19px; }
  .endpoint { font: 10px/1.5 var(--font-mono); color: var(--text-faint); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; padding: 7px 9px; border-radius: 6px; background: var(--content-soft); }
  .card-bottom { display: flex; justify-content: space-between; align-items: center; margin-top: 19px; }
  .status { display: flex; gap: 5px; align-items: center; color: var(--text-faint); font-size: 11px; }
  .status i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
  .connected { color: var(--success); }
  .test { color: var(--text-dim); font-size: 11px; gap: 5px; padding: 4px 0; }
  .menu-wrap { margin-left: auto; position: relative; }
  .menu { position: absolute; top: 37px; right: 0; z-index: 2; border: 1px solid var(--line); border-radius: 9px; padding: 5px; width: 150px; background: var(--content-raised); box-shadow: var(--shadow-raised); }
  .menu button { width: 100%; justify-content: flex-start; border-radius: 5px; padding: 8px; font-size: 12px; }
  .tool-row { margin-top: 17px; padding-top: 12px; border-top: 1px solid var(--line); display: flex; justify-content: space-between; }
  .tool-row button { font-size: 10px; color: var(--text-dim); gap: 5px; }
  .tool-row .use { color: var(--blue); }
  .tool-list { margin-top: 12px; max-height: 200px; overflow-y: auto; }
  .tool-list > div { padding: 8px 0; border-bottom: 1px solid var(--line); }
  .tool-list strong { font: 11px var(--font-mono); }
  .tool-list p { font-size: 11px; color: var(--text-dim); margin-top: 4px; }
  .add-card { min-height: 233px; padding: 30px 20px; display: flex; flex-direction: column; gap: 8px; border: 1px dashed var(--line-strong); border-radius: 13px; color: var(--text-dim); }
  .add-icon { display: flex; width: 42px; height: 42px; border: 1px solid var(--line); border-radius: 12px; align-items: center; justify-content: center; color: var(--text-faint); margin-bottom: 7px; }
  .add-card strong { font-size: 12px; font-weight: 550; }
  .add-card > span:last-child { font-size: 11px; color: var(--text-faint); }
  .footnote { display: flex; align-items: center; gap: 6px; margin-top: 23px; color: var(--text-faint); font-size: 10px; }
  .notice { display: flex; align-items: center; gap: 8px; padding: 12px; color: var(--success); font-size: 12px; margin-bottom: 16px; background: color-mix(in srgb, var(--success) 5%, var(--content-plane)); border-radius: 8px; }
  .empty-search { text-align: center; padding: 60px 0; color: var(--text-faint); }
  .delete-confirm { margin-top: 16px; border-top: 1px solid var(--line); padding-top: 15px; }
  .delete-confirm p { font-size: 12px; margin-bottom: 10px; }
  .delete-confirm div { display: flex; justify-content: flex-end; gap: 8px; }
  .delete-confirm button { font-size: 11px; padding: 5px 10px; }
  @media (max-width: 1050px) { .connectors-page { padding: 35px 28px; } .cards { grid-template-columns: 1fr; } .builtin-cards { grid-template-columns: repeat(2,minmax(0,1fr)); } }
  @media (max-width: 700px) { .connectors-page { padding: 28px 18px; } .page-heading { align-items: flex-start; } .page-heading p { max-width: 190px; } .page-heading button { padding: 9px 11px; } .search input { width: 105px; } .builtin-cards { grid-template-columns: 1fr; } .connector-tabs { gap: 2px; } .connector-tabs button { padding: 14px 7px; font-size: 11px; } .list-heading { flex-wrap: wrap; } .list-actions { width: 100%; justify-content: space-between; } }
</style>
