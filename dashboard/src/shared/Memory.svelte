<script lang="ts">
  import { onMount } from "svelte";
  import { Brain, Plus, Search, RefreshCw, ArrowRight, Pencil, Archive, Check, X, Download, Upload, ShieldCheck, Settings2, GitBranch, Clock, ChevronLeft, ChevronRight, LoaderCircle } from "lucide-svelte";
  import { memoryLayers, kindLabels, display, emptyCounts, type MemoryConfig, type MemoryJob, type MemoryLayer, type MemoryRow, type MemoryList, type AdoptResult, type DoctorReport } from "../lib/memory";
  import type { StatsCounts, StatsGrowthResponse, StatsAtomKindsResponse } from "../lib/memory-types";
  import type { DashboardPort } from "../lib/dashboard";
  import { renderMarkdown } from "../lib/markdown";
  import { modelOptions, type ModelConfiguration } from "../lib/models";

  let { port, modelConfiguration, job, onjob, onerror }: { port: DashboardPort; modelConfiguration: ModelConfiguration; job: MemoryJob; onjob: (job: MemoryJob) => void; onerror: (error: unknown) => void } = $props();
  type Tab = "overview" | "tree" | MemoryLayer | "settings" | "portable";
  const tabs = [{ id: "overview", label: "记忆总览" }, { id: "tree", label: "记忆树" }, ...memoryLayers, { id: "settings", label: "记忆设置" }, { id: "portable", label: "迁移记忆" }];
  let tab = $state<Tab>("overview");
  let counts = $state<StatsCounts>(emptyCounts());
  let growth = $state<StatsGrowthResponse>({ series: [] });
  let kinds = $state<StatsAtomKindsResponse>({ series: [] });
  let config = $state<MemoryConfig>({ memory_enabled: true, extract_on_session_end: true, extract_trigger_mode: "idle", extract_idle_seconds: 300, extract_interval_seconds: 21600, aux_model: "" });
  let loading = $state(false);
  let acting = $state(false);
  let notice = $state("");
  let query = $state("");
  let kind = $state("");
  let importance = $state("");
  let status = $state("pending");
  let entityType = $state("");
  let includeDeprecated = $state(false);
  let emotion = $state("");
  let from = $state("");
  let to = $state("");
  let actor = $state("");
  let sessionID = $state("");
  let entityID = $state("");
  let order = $state("desc");
  let offset = $state(0);
  let listing = $state<MemoryList>({ items: [], total: 0, has_more: false });
  let detail = $state<MemoryRow | null>(null);
  let evidence = $state<MemoryRow | null>(null);
  let entityAtoms = $state<MemoryRow[]>([]);
  let tree = $state<MemoryRow[]>([]);
  let terminals = $state<{ title: string; items: MemoryRow[] }[]>([]);
  let editing = $state<MemoryRow | "new" | null>(null);
  let assertion = $state("");
  let entityName = $state("");
  let atomKind = $state("Fact");
  let atomImportance = $state("medium");
  let reason = $state("");
  let confirmation = $state<{ method: string; row?: MemoryRow; title: string; body: string } | null>(null);
  let migrationFile = $state<File | null>(null);
  let conflict = $state("skip");
  let preview = $state<AdoptResult | null>(null);
  let report = $state<DoctorReport | null>(null);
  let generation = 0;
  let disposed = false;
  let lastJob = "";
  const layer = $derived(memoryLayers.find((item) => item.id === tab));
  const jobRunning = $derived(job.status === "running");
  const models = $derived(modelOptions(modelConfiguration, "chat"));
  const growthMax = $derived(Math.max(1, ...growth.series.map((item) => item.atoms + item.entities + item.episodes)));
  const page = $derived(detail?.page as { summary_markdown?: string; headline?: string; dirty?: boolean; topics?: string[] } | null);
  const fieldLabels: Record<string, string> = { id: "ID", entity_id: "实体 ID", candidate_id: "来源候选", assertion: "记忆内容", verbatim_quote: "原文引用", quote_event_id: "原始证据", raw_event_ids: "来源记录", search_terms: "检索词", canonical_name: "名称", entity_type: "实体类别", atom_count: "原子记忆数", importance: "重要度", confidence: "置信度", status: "状态", kind: "类别", candidate_type: "类别", created_at: "创建时间", updated_at: "更新时间", occurred_at: "发生时间", deprecated_at: "废弃时间", superseded_by: "替代记忆", session_id: "来源会话", thread_id: "来源线程", timestamp: "时间", host: "来源", user: "用户", event_type: "记录类别", payload: "附加信息", summary: "故事", emotion: "情绪", intensity: "强度", people: "人物", topics: "主题", action: "操作", actor: "执行者", note: "备注", target_summary: "影响的记忆", target_entity_id: "目标实体", target_atom_id: "目标记忆", target_candidate_id: "目标候选", before: "操作前", after: "操作后", content: "原始内容", aliases: "别名", title: "标题", subject_name: "主体名称", subject_entity_type: "主体类别", promotion_reason: "提升说明", recommended_action: "建议操作", extractor_version: "提炼版本", decided_at: "审核时间", decided_by: "审核者", page_dirty: "摘要待更新" };
  function modal(node: HTMLDialogElement) { node.showModal(); return { destroy() { node.close(); } }; }
  function title(row: MemoryRow) { return display(row.assertion || row.canonical_name || row.summary || row.content || row.target_summary || row.title || row.action || row.name || row.id); }
  function date(value: unknown) { const parsed = new Date(display(value)); return isNaN(parsed.getTime()) ? display(value) : parsed.toLocaleString("zh-CN"); }
  function value(key: string, raw: unknown) { return key.endsWith("_at") || key === "timestamp" ? date(raw) : kindLabels[display(raw)] || display(raw); }
  function filters(): Record<string, unknown> {
    const result: Record<string, unknown> = { offset, limit: 30 };
    if (query && tab !== "journal") result.query = query;
    if ((tab === "atoms" || tab === "candidates") && kind) result.candidate_type = kind;
    if (tab === "atoms") { result.include_deprecated = includeDeprecated; result.order_by = "created_at"; result.order = order; if (importance) result.importance_min = importance; if (entityID) result.entity_id = entityID; }
    if (tab === "candidates") { if (status) result.status = status; if (sessionID) result.session_id = sessionID; }
    if (tab === "raw_events" && sessionID) result.session_id = sessionID;
    if (tab === "entities") { result.order_by = "atom_count"; result.order = order; if (entityType) result.entity_type = entityType; }
    if (tab === "episodes") { if (emotion) result.emotion = emotion; if (from) result.date_from = from; if (to) result.date_to = to; }
    if (tab === "journal") { if (actor) result.actor = actor; if (from) result.time_from = `${from}T00:00:00`; if (to) result.time_to = `${to}T23:59:59`; }
    return result;
  }
  async function load() {
    const current = ++generation; loading = true;
    try {
      if (tab === "overview") {
        const responses = await Promise.all([port.memory<StatsCounts>("stats_counts"), port.memory<StatsGrowthResponse>("stats_growth", { days: 14 }), port.memory<StatsAtomKindsResponse>("stats_atom_kinds"), ...[
          ["关于我", "terminal_about_me"], ["当前关注", "terminal_current_focus"], ["你告诉我的事", "terminal_things_you_told_me"], ["最近的故事", "terminal_recent_stories"], ["认识的实体", "terminal_entities"],
        ].map(async ([heading, method]) => ({ title: heading, items: (await port.memory<{ items: MemoryRow[] }>(method, { limit: 5 })).items }))]);
        if (disposed || current !== generation) return;
        counts = responses[0] as StatsCounts; growth = responses[1] as StatsGrowthResponse; kinds = responses[2] as StatsAtomKindsResponse; terminals = responses.slice(3) as typeof terminals;
      } else if (tab === "tree") {
        const response = await port.memory<{ items: MemoryRow[] }>("tree"); if (current === generation && !disposed) tree = response.items;
      } else if (layer) {
        const response = await port.memory<MemoryList>(layer.method, filters()); if (current === generation && !disposed) listing = response;
      }
    } catch (error) { if (!disposed && current === generation) onerror(error); }
    finally { if (current === generation) loading = false; }
  }
  function selectTab(next: Tab) { tab = next; detail = null; evidence = null; offset = 0; query = ""; entityID = ""; notice = ""; void load(); }
  function tabKey(event: KeyboardEvent, index: number) {
    let next: number;
    if (event.key === "ArrowRight") next = (index + 1) % tabs.length;
    else if (event.key === "ArrowLeft") next = (index + tabs.length - 1) % tabs.length;
    else if (event.key === "Home") next = 0;
    else if (event.key === "End") next = tabs.length - 1;
    else return;
    event.preventDefault(); selectTab(tabs[next].id as Tab); document.getElementById(`memory-tab-${tabs[next].id}`)?.focus();
  }
  async function inspect(row: MemoryRow, source: Tab = tab) {
    try {
      const singular = source === "entities" ? "entity" : source === "atoms" ? "atom" : source === "episodes" ? "episode" : source === "raw_events" ? "raw_event" : source === "candidates" ? "candidate" : "";
      detail = singular ? await port.memory<MemoryRow>(`get_${singular}`, { [`${singular}_id`]: row.id }) : row;
      evidence = null; entityAtoms = [];
      if (source === "entities") entityAtoms = (await port.memory<MemoryList>("list_atoms", { entity_id: row.id, limit: 100 })).items;
      if (detail.quote_event_id) evidence = await port.memory<MemoryRow>("get_raw_event", { event_id: detail.quote_event_id });
    } catch (error) { onerror(error); }
  }
  function edit(row: MemoryRow | "new") { editing = row; assertion = row === "new" ? "" : display(row.assertion); entityName = ""; reason = ""; atomKind = "Fact"; atomImportance = "medium"; }
  async function saveAtom(event: SubmitEvent) {
    event.preventDefault(); acting = true;
    try {
      if (editing === "new") await port.memory("create_atom", { assertion, entity_name: entityName || "我", entity_type: "User", kind: atomKind, importance: atomImportance, confidence: "high", reason });
      else if (editing) await port.memory("replace_atom", { atom_id: editing.id, assertion, reason });
      editing = null; detail = null; notice = "记忆已保存，来源和修改记录已保留。"; await load();
    } catch (error) { onerror(error); } finally { acting = false; }
  }
  async function perform() {
    if (!confirmation) return;
    acting = true;
    try {
      const action = confirmation;
      if (action.row) {
        const result = await port.memory<Record<string, unknown>>(action.method, { id: action.row.id, reason });
        notice = action.method === "promote_candidate" && result.needs_review ? "候选仍需审核，请查看详情后再次确认。" : "操作已保存并记录到审计日志。";
      } else { onjob(await port.startMemoryJob(action.method)); notice = "已启动后台整理。关闭页面不会取消任务。"; }
      confirmation = null; detail = null; reason = ""; await load();
    } catch (error) { onerror(error); } finally { acting = false; }
  }
  async function startExtract() { acting = true; try { onjob(await port.startMemoryJob("extract")); } catch (error) { onerror(error); } finally { acting = false; } }
  async function saveConfig(event: SubmitEvent) { event.preventDefault(); acting = true; try { config = await port.saveMemoryConfig(config); notice = "记忆设置已保存，下次操作立即生效。"; } catch (error) { onerror(error); } finally { acting = false; } }
  async function exportPackage() {
    acting = true;
    try { const blob = await port.exportMemory(); const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = `spider-memory-${new Date().toISOString().slice(0,10)}.hmpkg`; anchor.click(); setTimeout(() => URL.revokeObjectURL(url), 1000); notice = "记忆包已导出。"; }
    catch (error) { onerror(error); } finally { acting = false; }
  }
  async function adopt(dryRun: boolean) { if (!migrationFile) return; acting = true; try { preview = await port.importMemory(migrationFile, dryRun, conflict); notice = dryRun ? "预检查完成，尚未写入记忆库。" : "记忆导入完成。"; } catch (error) { preview = null; onerror(error); } finally { acting = false; } }
  async function doctor() { acting = true; try { report = await port.doctorMemory(); } catch (error) { onerror(error); } finally { acting = false; } }
  onMount(() => { void port.memoryConfig().then((result) => { if (!disposed) config = result; }).catch(onerror); void load(); return () => { disposed = true; generation++; }; });
  $effect(() => { if (job.id && job.id !== lastJob && job.status !== "running") { lastJob = job.id; void load(); } });
</script>

<svelte:window onkeydown={(event) => { if (event.key === "Escape" && !acting) { editing = null; confirmation = null; detail = null; } }} />
<section class="memory-page">
  <div class="eyebrow">让助手记住重要的事</div>
  <div class="heading"><div><h1><Brain size={27} />记忆管理</h1><p>从对话原文到长期记忆，查看来源、审核候选，随时纠正。</p></div><div class="actions"><button class="secondary" disabled={acting || jobRunning || !config.memory_enabled} onclick={startExtract}><RefreshCw size={15} />立即提炼</button><button class="primary" disabled={acting} onclick={() => edit("new")}><Plus size={15} />添加记忆</button></div></div>
  <div class="tabs" role="tablist" aria-label="记忆视图">{#each tabs as item, index}<button role="tab" id={`memory-tab-${item.id}`} aria-selected={tab === item.id} aria-controls="memory-panel" tabindex={tab === item.id ? 0 : -1} class:active={tab === item.id} onclick={() => selectTab(item.id as Tab)} onkeydown={(event) => tabKey(event, index)}>{item.label}{#if item.id === "candidates" && counts.candidates_pending}<span class="badge">{counts.candidates_pending}</span>{/if}</button>{/each}</div>
  {#if !config.memory_enabled}<div class="notice">记忆已关闭。已有记忆仍可管理；聊天暂停采集、检索和自动提炼。</div>{/if}
  {#if notice}<div class="notice" role="status"><Check size={15} />{notice}</div>{/if}
  {#if loading}<div class="loading" role="status"><LoaderCircle class="spin" size={15} />正在读取记忆…</div>{/if}
  <div id="memory-panel" role="tabpanel" aria-labelledby={`memory-tab-${tab}`}>
  {#if tab === "overview"}
    <div class="count-grid">{#each [{ title: "原始记录", value: counts.raw_events, target: "raw_events" }, { title: "原子记忆", value: counts.atoms, target: "atoms" }, { title: "实体页面", value: counts.entities, target: "entities" }, { title: "情景记忆", value: counts.episodes, target: "episodes" }, { title: "待处理候选", value: counts.candidates_pending, target: "candidates" }] as item}<button class="count-card" aria-label={`${item.title} ${item.value} 查看全部`} onclick={() => selectTab(item.target as Tab)}><span>{item.title}</span><strong>{item.value}</strong><small>查看全部 <ArrowRight size={12} /></small></button>{/each}</div>
    <div class="pipeline">{#each memoryLayers.filter((item) => item.id !== "journal") as item, index}{#if index}<ArrowRight size={14} />{/if}<button onclick={() => selectTab(item.id)}><span>{item.layer}</span><strong>{item.label}</strong></button>{/each}</div>
    <div class="overview-grid"><article class="panel growth"><h2>最近 14 天的记忆增长</h2><p class="muted">原子记忆、实体与情景记忆每日新增数量</p><div class="bars" role="img" aria-label="最近十四天记忆增长柱状图">{#each growth.series as item}<div class="bar-column" title={`${item.date}：原子 ${item.atoms}，实体 ${item.entities}，情景 ${item.episodes}，对话 ${item.turns}`}><span class="bar" style={`height: ${Math.max(2, (item.atoms + item.entities + item.episodes) / growthMax * 100)}%`}></span><small>{item.date.slice(8)}</small></div>{/each}</div><details><summary>查看每日数据</summary><table><thead><tr><th>日期</th><th>原子</th><th>实体</th><th>情景</th><th>对话</th></tr></thead><tbody>{#each growth.series as item}<tr><td>{item.date}</td><td>{item.atoms}</td><td>{item.entities}</td><td>{item.episodes}</td><td>{item.turns}</td></tr>{/each}</tbody></table></details></article>
      <article class="panel"><h2>记忆类别</h2>{#each kinds.series as item}<div class="kind-row"><span>{kindLabels[item.kind] || item.kind}</span><strong>{item.count}</strong></div>{:else}<p class="empty-small">添加记忆或提炼对话后，将在这里看到分类。</p>{/each}<div class="last-run"><Clock size={14} /><span>{counts.last_extract_run?.timestamp ? `最近提炼：${date(counts.last_extract_run.timestamp)}` : "尚未提炼"}</span></div>{#if counts.last_extract_run?.failure_reason}<p class="error">{counts.last_extract_run.failure_reason}</p>{:else if counts.last_extract_run?.quiet}<p class="muted">上次运行没有发现新记忆。</p>{/if}<p class="muted">待更新实体页：{counts.dirty_pages}</p></article>
    </div>
    <div class="terminal-grid">{#each terminals as terminal}<article class="panel"><h2>{terminal.title}</h2>{#each terminal.items as item}<button class="terminal-item" onclick={() => void inspect(item, item.canonical_name ? "entities" : item.summary ? "episodes" : "atoms")}>{title(item)}<ArrowRight size={13} /></button>{:else}<p class="empty-small">这里还没有记忆。先聊一聊，或添加一条记忆。</p>{/each}</article>{/each}</div>
  {:else if tab === "tree"}
    <div class="list-heading"><div><h2><GitBranch size={18} />记忆树</h2><p>按照实体组织原子记忆，展开查看层级。</p></div><button class="secondary" disabled={loading} onclick={load}><RefreshCw size={14} />刷新</button></div>
    {#snippet treeNode(node: MemoryRow)}
      {#if node.atom_id || node.level === "leaf"}
        <div class="tree-leaf"><button onclick={() => node.atom_id && void inspect({ id: display(node.atom_id) }, "atoms")}>{display(node.content || node.title || node.name)}</button>{#if node.metadata}<details><summary>来源信息</summary><pre>{display(node.metadata)}</pre></details>{/if}</div>
      {:else}
        <details open={!node.parent_id}><summary>{display(node.title || node.name || node.content || node.id)}</summary>{#each tree.filter((child) => child.parent_id === node.id) as child}{@render treeNode(child)}{/each}</details>
      {/if}
    {/snippet}
    <div class="panel tree">{#each tree.filter((node) => !node.parent_id) as root}{@render treeNode(root)}{:else}<div class="empty"><GitBranch size={35} /><h2>记忆树尚未长出</h2><p>添加记忆或提炼对话后，会按实体形成层级。</p></div>{/each}</div>
  {:else if layer}
    <div class="list-heading"><div><h2>{layer.layer} · {layer.label}</h2><p>{layer.description}</p></div><button class="secondary" disabled={loading} onclick={load}><RefreshCw size={14} />刷新</button></div>
    <form class="filters" onsubmit={(event) => { event.preventDefault(); offset = 0; void load(); }}>
      {#if tab !== "journal"}<label class="search"><Search size={15} /><input aria-label="搜索记忆" placeholder="搜索内容" bind:value={query} /></label>{/if}
      {#if tab === "atoms" || tab === "candidates"}<select aria-label="记忆类别" bind:value={kind}><option value="">全部类别</option>{#each ["Fact", "Preference", "Decision", "Task", "ConflictCandidate"] as item}<option value={item}>{kindLabels[item]}</option>{/each}</select>{/if}
      {#if tab === "atoms"}<select aria-label="最低重要度" bind:value={importance}><option value="">全部重要度</option>{#each ["low", "medium", "high"] as item}<option value={item}>{kindLabels[item]}</option>{/each}</select><label class="checkbox"><input type="checkbox" bind:checked={includeDeprecated} />含已废弃</label>{/if}
      {#if tab === "candidates"}<select aria-label="候选状态" bind:value={status}><option value="">全部状态</option>{#each ["pending", "needs_review", "conflict", "promoted", "rejected"] as item}<option value={item}>{kindLabels[item]}</option>{/each}</select>{/if}
      {#if tab === "raw_events" || tab === "candidates"}<input aria-label="来源会话 ID" placeholder="来源会话 ID" bind:value={sessionID} />{/if}
      {#if tab === "entities"}<select aria-label="实体类别" bind:value={entityType}><option value="">全部实体</option>{#each ["User", "Person", "Project", "Decision", "Task", "Fact"] as item}<option value={item}>{kindLabels[item] || item}</option>{/each}</select>{/if}
      {#if tab === "entities" || tab === "atoms"}<select aria-label="排序" bind:value={order}><option value="desc">降序</option><option value="asc">升序</option></select>{/if}
      {#if tab === "episodes"}<input aria-label="情绪" placeholder="情绪，如 happy" bind:value={emotion} />{/if}
      {#if tab === "episodes" || tab === "journal"}<input type="date" aria-label="开始日期" bind:value={from} /><input type="date" aria-label="结束日期" bind:value={to} />{/if}
      {#if tab === "journal"}<select aria-label="执行者" bind:value={actor}><option value="">全部执行者</option><option value="user">用户</option><option value="auto">自动</option><option value="rule">规则</option></select>{/if}
      <button class="secondary" type="submit" disabled={loading}>筛选</button>
    </form>
    <div class="record-list">{#each listing.items as row}<article class="record" class:deprecated={!!row.deprecated_at}><button class="record-body" onclick={() => void inspect(row)}><div class="record-meta"><span class="badge">{value("kind", row.kind || row.candidate_type || row.entity_type || row.event_type || row.action)}</span>{#if row.status}<span class="badge">{value("status", row.status)}</span>{/if}{#if row.deprecated_at}<span class="badge">已废弃</span>{/if}<small>{date(row.created_at || row.timestamp || row.occurred_at)}</small></div><p>{title(row)}</p>{#if row.importance}<small>重要度 {value("importance", row.importance)} · 置信度 {value("confidence", row.confidence)}</small>{/if}{#if row.atom_count !== undefined}<small>{display(row.atom_count)} 条原子记忆 · {row.page_dirty ? "摘要待更新" : "摘要已更新"}</small>{/if}</button><div class="record-actions">
      {#if tab === "atoms" && !row.deprecated_at}<button class="icon-button" aria-label={`编辑记忆 ${row.id}`} onclick={() => edit(row)}><Pencil size={15} /></button><button class="icon-button" aria-label={`废弃记忆 ${row.id}`} onclick={() => { reason = ""; confirmation = { method: "deprecate_atom", row, title: "废弃这条记忆？", body: "这条记忆将退出日常检索。原始内容和审计记录仍然保留。" }; }}><Archive size={15} /></button>{/if}
      {#if tab === "candidates" && ["pending", "needs_review", "conflict"].includes(display(row.status))}<button class="secondary" disabled={acting} onclick={() => { reason = ""; confirmation = { method: "promote_candidate", row, title: "确认提升这条候选？", body: "候选会经过重复、冲突和质量检查，成为长期记忆或进入待审核状态。" }; }}><Check size={14} />提升</button><button class="icon-button" aria-label={`拒绝候选 ${row.id}`} onclick={() => { reason = ""; confirmation = { method: "reject_candidate", row, title: "拒绝这条候选？", body: "候选将标记为已拒绝，保留原文和操作记录。" }; }}><X size={15} /></button>{/if}
      <button class="icon-button" aria-label={`查看详情 ${row.id}`} onclick={() => void inspect(row)}><ArrowRight size={15} /></button></div></article>{:else}{#if !loading}<div class="empty"><Brain size={35} /><h2>暂无{layer.label}</h2><p>{query ? "试试其他关键词或筛选条件。" : "新的对话会逐步形成记忆，也可以手动添加。"}</p></div>{/if}{/each}</div>
    <div class="pagination"><span>共 {listing.total} 条 · 第 {Math.floor(offset / 30) + 1} 页</span><button class="secondary" aria-label="上一页记忆" disabled={loading || !offset} onclick={() => { offset = Math.max(0, offset - 30); void load(); }}><ChevronLeft size={14} /></button><button class="secondary" aria-label="下一页记忆" disabled={loading || !listing.has_more} onclick={() => { offset += 30; void load(); }}><ChevronRight size={14} /></button></div>
  {:else if tab === "settings"}
    <form class="panel settings" onsubmit={saveConfig}><h2><Settings2 size={18} />采集与提炼</h2><label class="checkbox"><input type="checkbox" bind:checked={config.memory_enabled} />启用记忆</label><p class="muted">关闭后暂停聊天采集、检索和自动提炼，保留已有内容。</p><label class="checkbox"><input type="checkbox" bind:checked={config.extract_on_session_end} />自动提炼对话</label><label>触发方式<select bind:value={config.extract_trigger_mode}><option value="idle">空闲后提炼</option><option value="interval">定时提炼</option></select></label>{#if config.extract_trigger_mode === "idle"}<label>空闲时间（秒）<input type="number" min="60" max="604800" required bind:value={config.extract_idle_seconds} /></label>{:else}<label>提炼间隔（秒）<input type="number" min="300" max="604800" required bind:value={config.extract_interval_seconds} /></label>{/if}<label>记忆整理模型<select bind:value={config.aux_model}><option value="">自动 · 跟随当前聊天模型</option>{#each models as item}<option value={`${item.provider.id}/${item.model.id}`}>{item.provider.name} · {item.model.label}</option>{/each}</select></label><p class="muted">提炼候选、情景和实体摘要会调用这里选择的模型。</p><button class="primary" type="submit" disabled={acting}>保存记忆设置</button></form>
    <article class="panel maintenance"><h2>整理与维护</h2><p>压缩前完整备份，等待当前对话结束后暂停发送。对话记录和业务记忆不会被裁剪。</p><div class="actions"><button class="secondary" disabled={acting || jobRunning} onclick={() => confirmation = { method: "slim", title: "备份并压缩记忆库？", body: "会额外创建完整备份，暂停新的发送，完成后恢复。大库可能耗时较长，备份不会自动删除。" }}><Archive size={15} />备份并压缩</button><button class="secondary" disabled={acting || jobRunning} onclick={() => confirmation = { method: "consolidate", title: "合并重复记忆？", body: "检查实体下相似的原子记忆，保留更可靠的一条，将重复内容标记为被替代。每次改动会记录到日志。" }}>合并重复记忆</button><button class="secondary" disabled={acting || jobRunning} onclick={() => confirmation = { method: "regenerate_pages", title: "重新生成实体摘要？", body: "根据当前有效记忆更新待整理的实体页面，会调用记忆整理模型。" }}>更新实体摘要</button></div></article>
  {:else if tab === "portable"}
    <div class="portable-grid"><article class="panel"><h2><Download size={18} />导出记忆</h2><p>将原始记录、候选、原子记忆、实体、情景与日志打包为 Octop 的 .hmpkg 格式。</p><button class="primary" disabled={acting || jobRunning} onclick={exportPackage}>下载记忆包</button></article><article class="panel"><h2><Upload size={18} />导入记忆</h2><p>选择 Octop / octop-memory 导出的 .hmpkg 包，先预检查再写入。</p><label>记忆包<input type="file" accept=".hmpkg" onchange={(event) => { migrationFile = event.currentTarget.files?.[0] || null; preview = null; }} /></label><label>冲突处理<select bind:value={conflict} onchange={() => preview = null}><option value="skip">跳过重复内容</option><option value="replace">替换相同 ID 的内容</option><option value="raise">遇到冲突停止</option></select></label><div class="actions"><button class="secondary" disabled={acting || jobRunning || !migrationFile} onclick={() => adopt(true)}>预检查导入</button><button class="primary" disabled={acting || jobRunning || !preview?.dry_run || !!preview.errors} onclick={() => adopt(false)}>确认导入</button></div>{#if preview}<div class="notice"><span>{preview.dry_run ? "预计导入" : "已导入"} {preview.applied} 条，跳过 {preview.skipped} 条，错误 {preview.errors} 条。{preview.already_adopted ? "此包已导入过。" : ""}</span></div><details><summary>各层明细</summary><pre>{display(preview)}</pre></details>{/if}</article></div>
    <article class="panel doctor"><h2><ShieldCheck size={18} />记忆库检查</h2><p>检查数据库结构、索引、计数与引用完整性。</p><button class="secondary" disabled={acting || jobRunning} onclick={doctor}>运行检查</button>{#if report}<p class:passed={report.all_passed} class:error={!report.all_passed}>{report.all_passed ? "全部检查通过" : "存在需要处理的项目"}</p>{#each report.checks as check}<div class="check-row"><span>{check.passed ? "✓" : "!"} {check.name}</span><p>{check.message} {check.hint || ""}</p></div>{/each}{/if}</article>
  {/if}
  </div>
</section>

{#if detail}<div class="overlay"><dialog use:modal class="detail" aria-label="记忆详情" oncancel={() => detail = null}><div class="dialog-heading"><h2>记忆详情</h2><button class="icon-button" aria-label="关闭记忆详情" onclick={() => detail = null}><X /></button></div><h3>{title(detail)}</h3>{#if page}<div class="entity-page"><h3>{page.headline || "实体摘要"}</h3>{#if page.summary_markdown}<div class="markdown">{@html renderMarkdown(page.summary_markdown)}</div>{:else}<p class="muted">摘要尚未生成。可在记忆设置中更新实体摘要。</p>{/if}{#if page.dirty}<span class="badge">待更新</span>{/if}</div>{/if}<dl>{#each Object.entries(detail).filter(([key, raw]) => key !== "page" && raw !== null && raw !== "") as [key, raw]}<div><dt>{fieldLabels[key] || key}</dt><dd>{value(key, raw)}</dd></div>{/each}</dl>{#if evidence}<h3>原始证据</h3><blockquote>{display(evidence.content)}</blockquote><p class="muted">会话 {display(evidence.session_id)} · {date(evidence.timestamp)}</p>{/if}{#if entityAtoms.length}<h3>实体下的原子记忆</h3>{#each entityAtoms as atom}<p class="entity-atom">{display(atom.assertion)}</p>{/each}{/if}</dialog></div>{/if}
{#if editing}<div class="overlay"><dialog use:modal class="dialog" aria-label={editing === "new" ? "添加记忆" : "编辑记忆"} oncancel={(event) => { if (acting) event.preventDefault(); else editing = null; }}><div class="dialog-heading"><h2>{editing === "new" ? "添加记忆" : "编辑记忆"}</h2><button class="icon-button" disabled={acting} aria-label="关闭记忆编辑" onclick={() => editing = null}><X /></button></div><form onsubmit={saveAtom}><label>记忆内容<textarea aria-label="记忆内容" bind:value={assertion} required rows="4" maxlength="12000"></textarea></label>{#if editing === "new"}<label>所属实体<input aria-label="所属实体" bind:value={entityName} placeholder="我" /></label><div class="form-row"><label>类别<select aria-label="新记忆类别" bind:value={atomKind}>{#each ["Fact", "Preference", "Decision", "Task"] as item}<option value={item}>{kindLabels[item]}</option>{/each}</select></label><label>重要度<select aria-label="新记忆重要度" bind:value={atomImportance}>{#each ["low", "medium", "high"] as item}<option value={item}>{kindLabels[item]}</option>{/each}</select></label></div>{:else}<p class="muted">保存会创建替代记忆，旧内容和来源记录会保留。</p>{/if}<label>备注 / 修改原因<textarea aria-label="记忆修改原因" bind:value={reason} rows="2"></textarea></label><div class="actions end"><button class="secondary" type="button" disabled={acting} onclick={() => editing = null}>取消</button><button class="primary" type="submit" disabled={acting}>{acting ? "保存中…" : "保存记忆"}</button></div></form></dialog></div>{/if}
{#if confirmation}<div class="overlay"><dialog use:modal class="dialog" aria-label={confirmation.title} oncancel={(event) => { if (acting) event.preventDefault(); else confirmation = null; }}><div class="dialog-heading"><h2>{confirmation.title}</h2></div><p>{confirmation.body}</p>{#if confirmation.row}<blockquote>{title(confirmation.row)}</blockquote><label>原因（可选）<textarea aria-label="操作原因" bind:value={reason} rows="2"></textarea></label>{/if}<div class="actions end"><button class="secondary" disabled={acting} onclick={() => confirmation = null}>取消</button><button class="primary" disabled={acting} onclick={perform}>确认执行</button></div></dialog></div>{/if}

<style>
  .memory-page { max-width: 1160px; padding: 38px 36px; margin: 0 auto; } .eyebrow { color: var(--accent); font-size: 12px; margin-bottom: 12px; }
  .heading, .list-heading { display: flex; justify-content: space-between; align-items: center; gap: 20px; margin-bottom: 24px; } h1 { font-size: 25px; } h1,h2 { display: flex; align-items: center; gap: 10px; } h2 { font-size: 15px; margin-bottom: 12px; } h3 { font-size: 14px; margin: 15px 0; } p { color: var(--text-dim); } .heading p,.list-heading p { margin-top: 8px; font-size: 13px; }
  .actions { display: flex; flex-wrap: wrap; gap: 10px; } .tabs { display: flex; overflow-x: auto; gap: 5px; border-bottom: 1px solid var(--line); margin-bottom: 24px; padding-bottom: 1px; } .tabs button { padding: 11px 12px; white-space: nowrap; color: var(--text-dim); border-bottom: 2px solid transparent; } .tabs .active { color: var(--accent); border-color: var(--accent); }
  .notice { display: flex; align-items: center; gap: 8px; background: var(--blue-wash); border-radius: 8px; padding: 12px; font-size: 12px; margin-bottom: 16px; } .loading { display: flex; gap: 8px; color: var(--text-dim); margin: 16px 0; }
  .count-grid { display: grid; grid-template-columns: repeat(5,1fr); gap: 12px; } .count-card { flex-direction: column; align-items: flex-start; border: 1px solid var(--line); background: var(--content-raised); border-radius: 12px; padding: 18px; gap: 7px; } .count-card span { color: var(--text-dim); font-size: 12px; } .count-card strong { font-size: 27px; font-weight: 550; } .count-card small { display: flex; gap: 8px; align-items: center; color: var(--text-faint); font-size: 11px; }
  .pipeline { display: flex; align-items: center; justify-content: space-between; padding: 18px; margin: 20px 0; border-radius: 12px; background: var(--content-soft); gap: 12px; } .pipeline button { flex-direction: column; gap: 3px; font-size: 12px; } .pipeline span { color: var(--accent); font-size: 11px; }
  .overview-grid { display: grid; grid-template-columns: 1.8fr 1fr; gap: 18px; margin: 22px 0; } .panel { border: 1px solid var(--line); border-radius: 12px; padding: 22px; background: var(--content-raised); } .panel p { font-size: 12px; } .bars { display: flex; align-items: end; gap: 10px; height: 160px; padding-top: 15px; margin: 15px 0; } .bar-column { display: flex; flex-direction: column; justify-content: flex-end; align-items: center; height: 100%; flex: 1; gap: 6px; } .bar { min-height: 2px; width: 100%; max-width: 28px; border-radius: 4px; background: var(--accent); } .bar-column small { font-size: 10px; color: var(--text-faint); }
  .kind-row { display: flex; justify-content: space-between; font-size: 12px; padding: 7px 0; border-bottom: 1px solid var(--line); } .last-run { display: flex; gap: 7px; align-items: center; font-size: 11px; color: var(--text-dim); margin: 18px 0 8px; } .terminal-grid { display: grid; grid-template-columns: repeat(2,1fr); gap: 18px; } .terminal-item { justify-content: space-between; text-align: left; width: 100%; font-size: 13px; padding: 10px 0; border-bottom: 1px solid var(--line); } .empty-small { padding: 18px 0; line-height: 1.8; }
  .filters { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 20px; align-items: center; } .filters input,.filters select { font-size: 12px; max-width: 180px; } .search { display: flex; align-items: center; border: 1px solid var(--line-strong); border-radius: 8px; padding-left: 10px; } .search input { border: none; } .checkbox { display: flex; align-items: center; gap: 8px; font-size: 12px; } .checkbox input { accent-color: var(--accent); }
  .record { display: flex; align-items: center; padding: 17px; border: 1px solid var(--line); border-radius: 10px; margin-bottom: 10px; background: var(--content-raised); } .record-body { flex: 1; flex-direction: column; align-items: flex-start; text-align: left; padding: 0; min-width: 0; } .record-body p { color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; } .record-meta { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; } .record small { font-size: 11px; color: var(--text-faint); } .record-actions { display: flex; gap: 6px; margin-left: 18px; } .record-actions .secondary { padding: 6px 10px; font-size: 12px; } .deprecated { opacity: .65; } .pagination { display: flex; align-items: center; justify-content: flex-end; gap: 10px; color: var(--text-dim); font-size: 12px; margin-top: 20px; }
  .empty { padding: 50px 20px; display: flex; flex-direction: column; align-items: center; text-align: center; gap: 12px; color: var(--text-faint); } .empty h2 { color: var(--text-dim); margin: 0; } .empty p { font-size: 12px; }
  .tree details { margin: 8px 0 8px 18px; border-left: 1px solid var(--line); padding-left: 15px; } .tree summary { cursor: pointer; font-size: 13px; padding: 7px; overflow-wrap: anywhere; } .tree-leaf { padding: 8px 18px; font-size: 12px; } details summary { font-size: 12px; cursor: pointer; color: var(--text-dim); }
  .settings { max-width: 620px; display: flex; flex-direction: column; gap: 17px; } label:not(.checkbox):not(.search) { display: flex; flex-direction: column; gap: 7px; font-size: 12px; } .settings .primary { align-self: flex-start; } .maintenance { margin-top: 24px; } .maintenance p { margin-bottom: 18px; }
  .portable-grid { display: grid; grid-template-columns: 1fr 1.5fr; gap: 20px; } .portable-grid .panel { display: flex; flex-direction: column; gap: 17px; align-items: flex-start; } .portable-grid label { width: 100%; } .portable-grid input { width: 100%; } .doctor { margin-top: 22px; } .doctor button { margin: 16px 0; } .check-row { border-top: 1px solid var(--line); padding: 12px 0; font-size: 12px; } .passed { color: var(--success); }
  .dialog::backdrop,.detail::backdrop { background: rgb(20 26 40 / 28%); }
  .overlay { position: fixed; inset: 0; z-index: 60; background: rgb(20 26 40 / 28%); display: flex; align-items: center; justify-content: center; padding: 24px; } .dialog,.detail { background: var(--content-plane); border: 1px solid var(--line); border-radius: 16px; box-shadow: var(--shadow-float); padding: 25px; max-height: 85vh; overflow-y: auto; width: 520px; } .detail { width: 730px; } .dialog-heading { display: flex; justify-content: space-between; align-items: center; } .dialog-heading h2 { margin: 0; } .dialog form { display: flex; flex-direction: column; gap: 17px; margin-top: 22px; } .dialog > p { margin: 18px 0; font-size: 13px; } .form-row { display: flex; gap: 15px; } .form-row label { flex: 1; } .end { justify-content: flex-end; margin-top: 20px; }
  dl { font-size: 12px; } dl > div { display: grid; grid-template-columns: 125px 1fr; gap: 15px; border-top: 1px solid var(--line); padding: 11px 0; } dt { color: var(--text-dim); } dd { white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; } blockquote { border-left: 3px solid var(--accent); margin: 20px 0; padding: 10px 15px; background: var(--content-soft); white-space: pre-wrap; font-size: 13px; } .entity-page { background: var(--content-soft); padding: 18px; margin-top: 20px; border-radius: 10px; } .entity-atom { padding: 12px 0; border-top: 1px solid var(--line); font-size: 13px; } pre { font-size: 11px; white-space: pre-wrap; overflow-wrap: anywhere; padding: 10px; background: var(--content-soft); border-radius: 7px; } table { font-size: 11px; width: 100%; border-collapse: collapse; margin-top: 12px; } th,td { text-align: left; padding: 7px; border-bottom: 1px solid var(--line); }
  :global(.memory-page .spin) { animation: spin 1s linear infinite; } @keyframes spin { to { transform: rotate(360deg); } }
  @media(max-width: 1100px) { .memory-page { padding: 25px; } .heading { flex-wrap: wrap; } .count-grid { grid-template-columns: repeat(3,1fr); } }
  @media(max-width: 650px) { .memory-page { padding: 22px 15px; } .count-grid { grid-template-columns: repeat(2,1fr); } .overview-grid,.terminal-grid,.portable-grid { grid-template-columns: 1fr; } .pipeline { overflow-x: auto; } .pipeline button { min-width: 66px; } .record { align-items: flex-start; } .record-actions { flex-direction: column; margin-left: 8px; } .overlay { padding: 12px; } dl > div { grid-template-columns: 90px 1fr; } }
</style>
