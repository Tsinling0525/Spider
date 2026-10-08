<script lang="ts">
  import { t, locale, setLocale, initializeLocale, type Locale } from "./lib/i18n";
  import { onMount, tick } from "svelte";
  import { Brain, MessageSquare, Plug, SlidersHorizontal, AudioLines, ChevronDown, Search, Plus, PanelLeftClose, PanelLeftOpen, ArrowUpRight, ShieldCheck, Sparkles, Sun, Moon, RefreshCw, Trash2, X, CircleHelp, Check, LoaderCircle } from "lucide-svelte";
  import { SpiderDashboardPort, errorMessage, type Capabilities, type Connector, type Conversation, type DashboardPort } from "./lib/dashboard";
  import { startRecording, type Recording } from "./lib/voice";
  import { emptyModelConfiguration, type ModelConfiguration, type ModelCapability, type ModelSelection } from "./lib/models";
  import Brand from "./shared/Brand.svelte";
  import Composer from "./shared/Composer.svelte";
  import Message from "./shared/Message.svelte";
  import Connectors from "./shared/Connectors.svelte";
  import Models from "./shared/Models.svelte";
  import Memory from "./shared/Memory.svelte";
  import MemoryMaintenance from "./shared/MemoryMaintenance.svelte";
  import type { MemoryJob } from "./lib/memory";

  let { port = new SpiderDashboardPort() }: { port?: DashboardPort } = $props();
  type Route = "chat" | "connectors" | "models" | "memory" | "settings";
  let route = $state<Route>("chat");
  let connectors = $state<Connector[]>([]);
  let conversations = $state<Conversation[]>([]);
  let active = $state<Conversation | null>(null);
  let selected = $state<string[]>([]);
  let capabilities = $state<Capabilities>({ chat: false, transcription: false, speech: false });
  let modelConfiguration = $state<ModelConfiguration>(emptyModelConfiguration());
  let online = $state(false);
  let loading = $state(true);
  let busy = $state(false);
  let error = $state("");
  let draft = $state("");
  let search = $state("");
  let historyOpen = $state(true);
  let narrowHistory = $state(false);
  let mobileNav = $state(false);
  let dark = $state(false);
  let autoSpeak = $state(false);
  let recording = $state(false);
  let transcribing = $state(false);
  let playingText = $state("");
  let deleting = $state(false);
  let transcript = $state<HTMLDivElement>();
  let recorder: Recording | null = null;
  let audio: HTMLAudioElement | null = null;
  let audioURL: string | null = null;
  let speechGeneration = 0;
  let disposed = false;
  let pollTimer: ReturnType<typeof setTimeout> | undefined;
  let memoryTimer: ReturnType<typeof setTimeout> | undefined;
  let memoryJob = $state<MemoryJob>({ status: "idle" });
  let memoryStale = $state(false);
  let syncStale = $state(false);
  let seenVersions = $state<Record<string, number>>({});
  const remoteUpdates = $derived(conversations.filter((conversation) => conversation.source?.kind === "xiaozhi" && (conversation.id !== active?.id || route !== "chat") && (conversation.pending || conversation.version > (seenVersions[conversation.id] ?? 0))));
  const filteredConversations = $derived(conversations.filter((conversation) => conversation.title.toLowerCase().includes(search.toLowerCase())));
  const memoryBlocked = $derived(memoryJob.status === "running" && memoryJob.kind === "slim");
  const locked = $derived(active?.status === "running" || !!active?.pending || memoryBlocked);

  async function pollMemory() {
    try { const next = await port.memoryStatus(); if (!disposed) { memoryJob = next; memoryStale = false; } }
    catch { if (!disposed) memoryStale = true; }
    finally { if (!disposed) memoryTimer = setTimeout(pollMemory, 2000); }
  }

  function report(cause: unknown) { error = errorMessage(cause); }
  function navigate(next: Route) {
    if (recording) { recorder?.cancel(); recorder = null; recording = false; }
    route = next; mobileNav = false; narrowHistory = false; error = "";
  }
  function toggleHistory() {
    if (window.matchMedia("(max-width: 950px)").matches) narrowHistory = !narrowHistory;
    else historyOpen = !historyOpen;
  }
  async function refreshConnectors() {
    connectors = await port.connectors();
    selected = selected.filter((id) => connectors.some((item) => item.id === id && item.enabled && item.checked_at));
  }
  async function refreshConversations() { conversations = await port.conversations(); }
  async function refreshModels() {
    [modelConfiguration, capabilities] = await Promise.all([port.models(), port.capabilities()]);
    if (!capabilities.speech) { autoSpeak = false; stopAudio(); }
  }
  async function selectModel(capability: ModelCapability, selection: ModelSelection) {
    modelConfiguration = await port.selectModel(capability, selection);
    await refreshModels();
  }
  function selectConversation(conversation: Conversation) {
    active = conversation;
    seenVersions[conversation.id] = conversation.version;
    selected = conversation.connector_ids.filter((id) => connectors.some((item) => item.id === id && item.enabled && item.checked_at));
    try { localStorage.setItem("spider.last-conversation", conversation.id); } catch { /* Optional browser preference. */ }
  }
  function schedulePoll() {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(async () => {
      try {
        const latest = await port.conversations();
        if (disposed) return;
        conversations = latest;
        syncStale = false;
        const current = active;
        const summary = current && latest.find((item) => item.id === current.id);
        if (current && summary && !busy && (summary.version > current.version || current.status === "running")) {
          const conversation = await port.conversation(current.id);
          // A fetch begun before a local send, selection or approval must not
          // overwrite its newer snapshot, selected connectors or draft.
          if (!disposed && !busy && active?.id === current.id && active.version === current.version && conversation.version >= current.version) {
            active = conversation;
            seenVersions[conversation.id] = conversation.version;
            selected = conversation.connector_ids.filter((id) => connectors.some((item) => item.id === id && item.enabled && item.checked_at));
          }
        } else if (!current && route === "chat" && !draft.trim() && !busy && !recording && !transcribing) {
          const remote = latest.find((item) => item.source?.kind === "xiaozhi" && item.version > (seenVersions[item.id] ?? 0));
          if (remote) {
            const conversation = await port.conversation(remote.id);
            if (!disposed && !active && route === "chat" && !draft.trim() && !busy && !recording && !transcribing) selectConversation(conversation);
          }
        }
      } catch { if (!disposed) syncStale = true; }
      finally { if (!disposed) schedulePoll(); }
    }, 2000);
  }
  async function boot() {
    loading = true; error = "";
    try {
      const result = await Promise.all([port.capabilities(), port.connectors(), port.conversations(), port.models()]);
      if (disposed) return;
      [capabilities, connectors, conversations, modelConfiguration] = result; online = true;
      seenVersions = Object.fromEntries(conversations.map((conversation) => [conversation.id, conversation.version]));
      let last: string | null = null;
      try { last = localStorage.getItem("spider.last-conversation"); } catch { /* Optional preference. */ }
      const saved = conversations.find((conversation) => conversation.id === last);
      if (saved) selectConversation(await port.conversation(saved.id));
      schedulePoll();
    } catch (cause) { online = false; report(cause); }
    finally { loading = false; }
  }
  async function newConversation() {
    if (busy || recording || transcribing) return;
    error = ""; draft = ""; active = null; selected = []; route = "chat"; deleting = false; mobileNav = false; narrowHistory = false;
  }
  async function openConversation(id: string) {
    if (busy || recording || transcribing) return;
    busy = true; error = ""; deleting = false; draft = "";
    try { selectConversation(await port.conversation(id)); route = "chat"; mobileNav = false; narrowHistory = false; }
    catch (cause) { report(cause); }
    finally { busy = false; }
  }
  async function send() {
    const content = draft.trim();
    if (!content || busy || locked) return;
    const connectorIDs = [...selected];
    busy = true; error = "";
    let conversation = active;
    try {
      if (!conversation) { conversation = await port.createConversation(); selectConversation(conversation); selected = connectorIDs; }
      const id = conversation.id;
      active = { ...conversation, messages: [...(conversation.messages || []), { role: "user", content }] };
      draft = "";
      const result = await port.send(id, conversation.version, content, connectorIDs);
      if (!disposed && active?.id === id) { active = result; seenVersions[id] = result.version; }
      await refreshConversations();
      if (autoSpeak && capabilities.speech) {
        const last = result.messages?.at(-1);
        if (last?.role === "assistant" && last.content && !result.pending) void speak(last.content);
      }
    } catch (cause) {
      report(cause);
      if (conversation) {
        try {
          const latest = await port.conversation(conversation.id); active = latest;
          if (latest.version === conversation.version) draft = content;
          await refreshConversations();
        } catch { active = conversation; draft = content; }
      } else draft = content;
    } finally { busy = false; }
  }
  async function decide(approve: boolean) {
    if (!active?.pending || busy) return;
    const conversation = active;
    busy = true; error = "";
    try {
      active = await port.decide(conversation.id, conversation.version, conversation.pending!.id, approve);
      seenVersions[active.id] = active.version;
      await refreshConversations();
      const last = active.messages?.at(-1);
      if (autoSpeak && capabilities.speech && last?.role === "assistant" && last.content && !active.pending) void speak(last.content);
    } catch (cause) {
      report(cause);
      try { active = await port.conversation(conversation.id); } catch { /* Keep the reviewable snapshot. */ }
    } finally { busy = false; }
  }
  async function deleteConversation() {
    if (!active || busy) return;
    busy = true;
    try { await port.deleteConversation(active.id); active = null; deleting = false; selected = []; await refreshConversations(); }
    catch (cause) { report(cause); }
    finally { busy = false; }
  }
  async function record() {
    error = "";
    if (recording && recorder) {
      transcribing = true; recording = false;
      try {
        const file = await recorder.stop(); recorder = null;
        const text = await port.transcribe(file);
        if (!disposed) draft = [draft, text].filter(Boolean).join(" ");
      } catch (cause) { if (!disposed) report(cause); }
      finally { transcribing = false; }
    } else {
      transcribing = true;
      try {
        const session = await startRecording(() => { if (!disposed && recording && recorder) void record(); });
        if (disposed) { session.cancel(); return; }
        recorder = session; recording = true;
      } catch (cause) { report(cause); }
      finally { transcribing = false; }
    }
  }
  function stopAudio() {
    speechGeneration++;
    audio?.pause(); audio = null;
    if (audioURL) URL.revokeObjectURL(audioURL);
    audioURL = null; playingText = "";
  }
  async function speak(text: string) {
    if (playingText === text) { stopAudio(); return; }
    stopAudio(); const generation = speechGeneration;
    playingText = text;
    try {
      const blob = await port.speech(text);
      if (disposed || generation !== speechGeneration) return;
      audioURL = URL.createObjectURL(blob); audio = new Audio(audioURL);
      audio.onended = stopAudio;
      audio.onerror = () => { report(new Error("语音播放失败")); stopAudio(); };
      await audio.play();
    } catch (cause) { if (generation === speechGeneration) { report(cause); stopAudio(); } }
  }
  function toggleTheme() {
    dark = !dark; document.documentElement.dataset.theme = dark ? "dark" : "light";
    try { localStorage.setItem("spider.theme", dark ? "dark" : "light"); } catch { /* Optional preference. */ }
  }
  onMount(() => {
    const stopLocaleSync = initializeLocale();
    try { dark = localStorage.getItem("spider.theme") === "dark"; } catch { /* Optional preference. */ }
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    void boot();
    void pollMemory();
    return () => { stopLocaleSync(); disposed = true; recorder?.cancel(); stopAudio(); clearTimeout(pollTimer); clearTimeout(memoryTimer); };
  });
  $effect(() => {
    document.documentElement.lang = $locale;
    document.title = $t("Spider · 我的 AI 工作台");
  });
  $effect(() => {
    active?.messages?.length; busy;
    void tick().then(() => { transcript?.scrollTo?.({ top: transcript.scrollHeight, behavior: "smooth" }); });
  });
  const prompts = [
    { title: "把日常交给助手", text: "帮我整理今天需要处理的事情，先问我你需要了解哪些信息。", icon: Sparkles },
    { title: "用上自己的工具", text: "介绍当前已选连接器能做什么，并给我几个使用建议。", icon: Plug },
    { title: "从一个想法开始", text: "我有一个新想法，帮我一步步把它变成可执行的计划。", icon: MessageSquare },
  ];
</script>

<div class="shell" class:show-mobile-nav={mobileNav}>
  <aside class="sidebar">
    <div class="brand"><Brand size={33} /><strong>Spider</strong><span>{$t("个人版")}</span></div>
    <nav aria-label={$t("主导航")}>
      <button class:current={route === "chat"} onclick={() => navigate("chat")}><MessageSquare />{$t("对话")}</button>
      <div class="nav-group"><span>{$t("设置")}</span><ChevronDown size={12} /></div>
      <button class:current={route === "connectors"} onclick={() => navigate("connectors")}><Plug />{$t("连接器")}{#if connectors.length}<small>{connectors.length}</small>{/if}</button>
      <button class:current={route === "models"} onclick={() => navigate("models")}><AudioLines />{$t("模型与语音")}</button>
      <button class:current={route === "memory"} onclick={() => navigate("memory")}><Brain />{$t("记忆管理")}</button>
      <button class:current={route === "settings"} onclick={() => navigate("settings")}><SlidersHorizontal />{$t("应用设置")}</button>
    </nav>
    <div class="sidebar-bottom"><div class="local-status"><i class:online></i>{loading ? $t("正在连接…") : online ? $t("本地服务已连接") : $t("本地服务未连接")}<button class="icon-button" aria-label={$t("重新连接")} disabled={loading || busy} onclick={boot}><RefreshCw /></button></div><button class="profile" onclick={() => navigate("settings")}><span class="profile-icon">T</span><span><strong>Tsinling</strong><small>{$t("个人空间")}</small></span><ChevronDown size={13} /></button></div>
  </aside>
  {#if mobileNav}<button class="nav-backdrop" aria-label={$t("关闭导航")} onclick={() => mobileNav = false}></button>{/if}
  {#if route === "chat" && (historyOpen || narrowHistory)}
    <aside class="history" class:narrow-open={narrowHistory}>
      <div class="history-tools"><label><Search size={15} /><input aria-label={$t("搜索会话")} placeholder={$t("搜索会话")} bind:value={search} /></label><button class="icon-button" aria-label={$t("新建对话")} disabled={busy || recording || transcribing} onclick={newConversation}><Plus /></button></div>
      <div class="agent-card"><div class="agent-avatar"><Brand size={27} /></div><div><strong>{$t("Spider · 我的助手")}</strong><p>{$t("你的模型，你的工具。")}<br />{$t("从一句话开始。")}</p></div></div>
      <div class="history-label">{$t("最近对话")} <span>{conversations.length}</span></div>
      <div class="history-list">
        {#each filteredConversations as conversation}<button class:chosen={conversation.id === active?.id} disabled={busy || recording || transcribing} onclick={() => openConversation(conversation.id)}><MessageSquare size={13} /><span>{conversation.title}</span>{#if conversation.source?.kind === "xiaozhi"}<small class="source-badge">{conversation.source.name}</small>{/if}{#if conversation.pending}<i title={$t("等待确认")}></i>{/if}</button>{/each}
        {#if !filteredConversations.length}<p>{search ? $t("没有匹配的会话") : $t("你的对话会出现在这里")}</p>{/if}
      </div>
      <div class="history-footer"><ShieldCheck size={13} />{$t("会话保存在你的本地服务")}</div>
    </aside>
  {/if}
  <main>
    <header class="topbar"><div><button class="icon-button mobile-toggle" aria-label={$t("打开导航")} onclick={() => mobileNav = !mobileNav}><PanelLeftOpen /></button>{#if route === "chat"}<button class="icon-button history-toggle" aria-label={historyOpen ? $t("收起会话列表") : $t("展开会话列表")} onclick={toggleHistory}>{#if historyOpen}<PanelLeftClose />{:else}<PanelLeftOpen />{/if}</button><span>{active?.title || $t("新对话")}</span>{:else}<span>{route === "connectors" ? $t("连接器") : route === "models" ? $t("模型与语音") : route === "memory" ? $t("记忆管理") : $t("应用设置")}</span>{/if}</div><div class="topbar-right"><span class="badge local-badge"><span class="tiny-dot"></span>{$t("本地工作空间")}</span><button class="icon-button" aria-label={dark ? $t("切换浅色模式") : $t("切换深色模式")} onclick={toggleTheme}>{#if dark}<Sun />{:else}<Moon />{/if}</button>{#if route === "chat" && active}<button class="icon-button" aria-label={$t("删除当前对话")} disabled={busy || locked} onclick={() => deleting = !deleting}><Trash2 /></button>{/if}</div></header>
    <MemoryMaintenance job={memoryJob} stale={memoryStale} />
    {#if remoteUpdates.length}
      <div class="channel-notice" role="status"><AudioLines size={16} /><span>{$t("{0} · {1} 个会话", { 0: remoteUpdates.some((conversation) => conversation.pending) ? $t("小智的请求需要你确认") : $t("小智有新消息"), 1: remoteUpdates.length })}</span><button class="secondary" disabled={busy || recording || transcribing} onclick={() => void openConversation((remoteUpdates.find((conversation) => conversation.pending) ?? remoteUpdates[0]).id)}>{$t("查看小智会话")}</button></div>
    {/if}
    {#if syncStale}<div class="sync-notice" role="status">{$t("会话同步暂时中断，正在重连…")}</div>{/if}
    {#if error}<div class="error-banner" role="alert"><CircleHelp size={15} /><span>{$t(error)}</span>{#if !online}<button onclick={boot} disabled={loading}>{$t("重新连接")}</button>{/if}<button class="icon-button" aria-label={$t("关闭错误提示")} onclick={() => error = ""}><X /></button></div>{/if}
    {#if deleting}<div class="delete-banner"><span>{$t("删除当前对话和全部聊天记录？")}</span><button class="secondary" onclick={() => deleting = false}>{$t("取消")}</button><button class="primary" disabled={busy} onclick={deleteConversation}>{$t("删除对话")}</button></div>{/if}
    {#if route === "chat"}
      <div class="chat-layout">
        {#if active?.source?.kind === "xiaozhi"}<div class="conversation-source"><AudioLines size={14} />{$t("来自 {0} · 设备和网页共用此对话", { 0: active.source.name })}</div>{/if}
        <div class="transcript" bind:this={transcript}>
          {#if !active?.messages?.length}
            <div class="welcome"><div class="welcome-mark"><Brand size={56} /></div><div class="welcome-kicker">{$t("你的私人 AI 工作空间")}</div><h1>{$t("今天，想做点什么？")}</h1><p>{$t("聊一个想法，连一件工具，")}<br class="mobile-break" />{$t("或直接说给我听。")}</p><div class="suggestions">{#each prompts as prompt}<button onclick={() => draft = $t(prompt.text)}><prompt.icon size={18} /><strong>{$t(prompt.title)}</strong><span>{$t(prompt.text)}</span><ArrowUpRight size={14} /></button>{/each}</div><button class="welcome-connect" onclick={() => navigate("connectors")}><Plug size={14} />{$t("从连接你的第一个工具开始")}<ArrowUpRight size={13} /></button></div>
          {:else}
            <div class="messages">{#each active.messages as message}<Message {message} speechEnabled={capabilities.speech} onspeak={speak} speaking={!!playingText && playingText === message.content} onerror={report} />{/each}
              {#if active.pending}<div class="approval"><div class="approval-heading"><ShieldCheck size={20} /><div><h3>{$t("等待你的确认")}</h3><p>{$t("Spider 想使用「{0}」的工具。", { 0: active.pending.connector_name })}</p></div></div><div class="approval-tool"><strong>{active.pending.tool}</strong><span>{active.pending.url}</span></div><pre>{JSON.stringify(active.pending.arguments, null, 2)}</pre>{#if active.pending.voice}<div class="voice-approval" role="note"><p>{active.pending.voice.summary}</p><p>{#if active.pending.voice.phrase}{$t("也可在小智上说「{0}」或「取消」。", { 0: active.pending.voice.phrase })}{:else}{$t("可在小智上说「取消」；执行需要网页确认。")}{/if}</p></div>{/if}<div class="approval-actions"><span>{$t("确认后，工具将在连接器服务中执行。")}</span><button class="secondary" disabled={busy || memoryBlocked} onclick={() => decide(false)}>{$t("拒绝")}</button><button class="primary" disabled={busy || memoryBlocked} onclick={() => decide(true)}><Check size={15} />{$t("确认执行")}</button></div></div>{/if}
              {#if active.memory_error}<div class="conversation-error" role="status">{active.memory_error}</div>{/if}
              {#if active.error}<div class="conversation-error" role="status">{active.error}</div>{/if}
              {#if busy || active.status === "running"}<div class="thinking" role="status"><LoaderCircle class="spin" size={15} />{$t("Spider 正在处理…")}</div>{/if}
            </div>
          {/if}
        </div>
        {#if online && !capabilities.chat}<div class="model-note"><AudioLines size={13} /><span>{$t("配置聊天模型后即可开始对话。")}</span><button onclick={() => navigate("models")}>{$t("查看配置")}</button></div>{/if}
        <Composer bind:draft bind:selected {connectors} {busy} {locked} {recording} {transcribing} {modelConfiguration} voiceEnabled={capabilities.transcription} speechEnabled={capabilities.speech} bind:autoSpeak onsend={send} onrecord={record} onmanage={() => navigate("connectors")} onmodelmanage={() => navigate("models")} onmodelselect={(selection) => void selectModel("chat", selection).catch(report)} />
      </div>
    {:else if route === "connectors"}
      <div class="page-scroll"><Connectors {connectors} {port} onrefresh={refreshConnectors} onerror={report} onchat={(id) => { route = "chat"; if (!selected.includes(id)) selected = [...selected, id]; }} /></div>
    {:else if route === "models"}
      <div class="page-scroll"><Models configuration={modelConfiguration} {port} onrefresh={refreshModels} onselect={selectModel} onerror={report} /></div>
    {:else if route === "memory"}
      <div class="page-scroll"><Memory {port} {modelConfiguration} job={memoryJob} onjob={(job) => memoryJob = job} onerror={report} /></div>
    {:else}
      <section class="settings-page">
        <div class="eyebrow">{$t("让空间更像你")}</div>
        <h1>{$t("应用设置")}</h1>
        <p class="page-description">{$t("简单的偏好，舒服的工作方式。")}</p>
        <div class="preference">
          <div><h3>{$t("外观")}</h3><p>{$t("为当前浏览器选择浅色或深色模式。")}</p></div>
          <button class="secondary" onclick={toggleTheme}>{#if dark}<Moon size={15} />{$t("深色")}{:else}<Sun size={15} />{$t("浅色")}{/if}</button>
        </div>
        <div class="preference language-preference">
          <div>
            <h3><label for="interface-language">{$t("语言")}</label></h3>
            <p>{$t("选择界面显示语言，设置会保存在当前浏览器。")}</p>
          </div>
          <select id="interface-language" value={$locale} onchange={(event) => setLocale(event.currentTarget.value as Locale)}>
            <option value="zh-CN" lang="zh-CN">简体中文</option>
            <option value="en" lang="en">English</option>
          </select>
        </div>
        <div class="preference">
          <div><h3>{$t("自动朗读")}</h3><p>{$t("使用配置的 TTS 模型播放新回复。")}</p></div>
          <button class="switch" class:on={autoSpeak} role="switch" aria-label={$t("自动朗读")} aria-checked={autoSpeak} disabled={!capabilities.speech} onclick={() => autoSpeak = !autoSpeak}><i></i></button>
        </div>
        <div class="preference">
          <div><h3>{$t("连接状态")}</h3><p>{online ? $t("当前已连接本地 dashboard 服务。") : $t("尚未连接 dashboard 服务。")}</p></div>
          <button class="secondary" onclick={boot} disabled={loading || busy}><RefreshCw size={14} />{$t("刷新")}</button>
        </div>
        <div class="about">
          <Brand size={30} /><h3>Spider Dashboard</h3>
          <p>{$t("界面与交互参考 Octop，结构与组件沿用 Mantle 的设计方式。")}</p>
          <span>{$t("个人工作空间 · v0.1.0")}</span>
        </div>
      </section>
    {/if}
  </main>
</div>

<style>
  .language-preference select { min-width: 130px; flex-shrink: 0; font-size: 12px; }
  .shell { display: flex; height: 100dvh; overflow: hidden; }
  .sidebar { display: flex; flex-direction: column; flex-shrink: 0; width: 205px; border-right: 1px solid var(--line); background: var(--chrome-sidebar); }
  .brand { display: flex; align-items: center; gap: 6px; padding: 23px 18px 25px; }
  .brand strong { font-size: 21px; font-weight: 700; letter-spacing: -.8px; }
  .brand > span { font-size: 9px; color: var(--text-faint); background: var(--content-soft); border: 1px solid var(--line); border-radius: 5px; padding: 2px 5px; margin-left: 3px; white-space: nowrap; flex-shrink: 0; }
  nav { padding: 0 12px; }
  nav > button { width: 100%; justify-content: flex-start; text-align: left; gap: 12px; height: 42px; border-radius: 8px; padding: 0 12px; color: var(--text-dim); font-size: 13px; margin-bottom: 4px; }
  nav > button :global(svg) { color: var(--text-faint); width: 16px; height: 16px; }
  nav > button.current { color: var(--accent); background: var(--accent-wash); font-weight: 550; }
  nav > button.current :global(svg) { color: var(--accent); }
  nav small { margin-left: auto; color: var(--text-faint); font-size: 10px; }
  .nav-group { display: flex; justify-content: space-between; align-items: center; margin: 28px 10px 11px; color: var(--text-faint); font-size: 10px; }
  .sidebar-bottom { margin-top: auto; padding: 14px 17px; }
  .local-status { display: flex; align-items: center; gap: 6px; font-size: 10px; color: var(--text-faint); padding: 0 3px 11px; border-bottom: 1px solid var(--line); }
  .local-status > i { width: 5px; height: 5px; border-radius: 50%; background: var(--text-faint); }
  .local-status > i.online { background: var(--success); }
  .local-status button { margin-left: auto; width: 24px; height: 24px; }
  .local-status button :global(svg) { width: 12px; height: 12px; }
  .profile { width: 100%; display: flex; gap: 10px; justify-content: flex-start; text-align: left; padding: 15px 3px 4px; }
  .profile-icon { display: flex; align-items: center; justify-content: center; width: 32px; height: 32px; color: var(--blue); background: var(--blue-wash); border-radius: 10px; font-size: 12px; font-weight: 600; }
  .profile strong { display: block; font-size: 12px; font-weight: 600; }
  .profile small { color: var(--text-faint); font-size: 9px; }
  .profile > :global(svg) { margin-left: auto; color: var(--text-faint); }
  .history { width: 260px; border-right: 1px solid var(--line); padding: 20px 13px 14px; display: flex; flex-direction: column; flex-shrink: 0; }
  .history-tools { display: flex; gap: 5px; align-items: center; }
  .history-tools label { display: flex; align-items: center; gap: 7px; border: 1px solid var(--line); border-radius: 8px; padding: 9px 10px; color: var(--text-faint); flex: 1; min-width: 0; }
  .history-tools input { border: 0; padding: 0; width: 100%; font-size: 11px; background: transparent; }
  .history-tools > button { width: 27px; height: 29px; }
  .agent-card { display: flex; gap: 10px; align-items: flex-start; padding: 15px 12px; border: 1px solid #e9ebfa; border-radius: 12px; background: color-mix(in srgb, var(--blue) 3%, var(--content-plane)); margin-top: 13px; }
  .agent-avatar { display: flex; align-items: center; justify-content: center; width: 34px; height: 34px; border-radius: 11px; background: var(--content-plane); border: 1px solid var(--line); flex-shrink: 0; }
  .agent-card strong { font-size: 12px; font-weight: 600; }
  .agent-card p { color: var(--text-faint); font-size: 11px; line-height: 1.7; margin-top: 5px; }
  .history-label { color: var(--text-faint); font-size: 10px; display: flex; justify-content: space-between; margin: 25px 9px 10px; }
  .history-list { flex: 1; overflow-y: auto; }
  .history-list button { display: flex; width: 100%; align-items: center; justify-content: flex-start; padding: 10px 9px; border-radius: 8px; color: var(--text-dim); font-size: 11px; gap: 8px; margin-bottom: 3px; text-align: left; }
  .history-list button :global(svg) { width: 13px; height: 13px; color: var(--text-faint); }
  .history-list button.chosen { background: var(--content-soft); color: var(--text); }
  .history-list button > span { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .history-list button i { margin-left: auto; width: 5px; height: 5px; border-radius: 50%; background: var(--accent); flex-shrink: 0; }
  .source-badge { flex-shrink: 0; font-size: 9px; padding: 2px 5px; border-radius: 4px; color: var(--blue); background: var(--blue-wash); max-width: 70px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .channel-notice { display: flex; align-items: center; gap: 8px; padding: 10px 20px; color: var(--blue); background: var(--blue-wash); font-size: 12px; flex-shrink: 0; }
  .channel-notice span { flex: 1; }
  .channel-notice button { font-size: 11px; padding: 5px 9px; }
  .sync-notice { padding: 7px 20px; font-size: 11px; color: var(--text-dim); background: var(--content-soft); }
  .voice-approval { padding: 10px 14px; background: var(--blue-wash); border-radius: 8px; font-size: 12px; }
  .voice-approval p { margin: 4px 0; line-height: 1.6; }
  .conversation-source { display: flex; align-items: center; justify-content: center; gap: 6px; padding: 10px 16px; color: var(--text-dim); font-size: 11px; border-bottom: 1px solid var(--line); }
  .history-list p { text-align: center; color: var(--text-faint); font-size: 10px; margin-top: 25px; }
  .history-footer { display: flex; gap: 5px; align-items: center; justify-content: center; color: var(--text-faint); font-size: 9px; padding-top: 20px; }
  main { display: flex; flex-direction: column; flex: 1; min-width: 0; }
  .topbar { height: 58px; border-bottom: 1px solid var(--line); display: flex; align-items: center; justify-content: space-between; padding: 0 22px 0 13px; gap: 15px; flex-shrink: 0; }
  .topbar > div { display: flex; align-items: center; gap: 9px; min-width: 0; }
  .topbar > div > span:not(.badge) { font-size: 12px; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .topbar-right { flex-shrink: 0; }
  .topbar-right .badge { font-size: 9px; background: transparent; }
  .tiny-dot { width: 4px; height: 4px; background: var(--success); border-radius: 50%; }
  .mobile-toggle { display: none; }
  .chat-layout { display: flex; flex-direction: column; flex: 1; min-height: 0; }
  .transcript { flex: 1; min-height: 0; overflow-y: auto; }
  .welcome { width: min(100%, var(--frame)); margin: 0 auto; text-align: center; padding: clamp(40px, 11vh, 125px) 28px 40px; }
  .welcome-mark { display: inline-flex; align-items: center; justify-content: center; width: 76px; height: 76px; background: color-mix(in srgb, var(--accent) 4%, var(--content-plane)); border-radius: 23px; border: 1px solid color-mix(in srgb, var(--accent) 11%, var(--line)); margin-bottom: 20px; }
  .welcome-kicker { font-size: 10px; color: var(--text-faint); letter-spacing: 1.5px; margin-bottom: 10px; }
  .welcome h1 { font-size: clamp(25px, 2.3vw, 34px); font-weight: 580; letter-spacing: -1px; }
  .welcome > p { font-size: 13px; color: var(--text-dim); margin-top: 13px; }
  .mobile-break { display: none; }
  .suggestions { display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; margin-top: 38px; text-align: left; }
  .suggestions > button { display: flex; position: relative; flex-direction: column; align-items: flex-start; justify-content: flex-start; padding: 18px 17px; border: 1px solid var(--line); border-radius: 12px; gap: 11px; text-align: left; }
  .suggestions > button > :global(svg:first-child) { color: var(--accent); }
  .suggestions > button:nth-child(2) > :global(svg:first-child) { color: var(--blue); }
  .suggestions > button:nth-child(3) > :global(svg:first-child) { color: #a384c0; }
  .suggestions strong { font-size: 11px; font-weight: 550; }
  .suggestions span { font-size: 10px; color: var(--text-faint); line-height: 1.8; }
  .suggestions > button > :global(svg:last-child) { position: absolute; top: 17px; right: 13px; width: 12px; height: 12px; color: var(--text-faint); }
  .welcome-connect { font-size: 10px; color: var(--text-faint); margin-top: 25px; gap: 6px; }
  .welcome-connect :global(svg) { width: 13px; height: 13px; }
  .messages { width: min(100%, var(--frame)); margin: 0 auto; padding: 35px 28px 25px; }
  .thinking { display: flex; align-items: center; gap: 9px; color: var(--text-faint); font-size: 12px; margin: 12px 0 20px 46px; }
  .approval { border: 1px solid color-mix(in srgb, var(--accent) 25%, var(--line)); background: color-mix(in srgb, var(--accent) 2%, var(--content-plane)); border-radius: 13px; padding: 20px; margin: 12px 0 24px 46px; }
  .approval-heading { display: flex; gap: 10px; align-items: center; }
  .approval-heading > :global(svg) { color: var(--accent); }
  .approval h3 { font-size: 13px; }
  .approval p { font-size: 11px; color: var(--text-dim); margin-top: 4px; }
  .approval-tool { margin-top: 17px; }
  .approval-tool strong { font: 12px var(--font-mono); }
  .approval-tool span { display: block; font: 10px var(--font-mono); color: var(--text-faint); margin-top: 5px; overflow-wrap: anywhere; }
  .approval pre { font: 11px/1.7 var(--font-mono); padding: 13px; background: var(--content-plane); border: 1px solid var(--line); border-radius: 7px; max-height: 220px; overflow: auto; white-space: pre-wrap; }
  .approval-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; }
  .approval-actions span { margin-right: auto; font-size: 9px; color: var(--text-faint); }
  .approval-actions button { padding: 7px 11px; font-size: 11px; }
  .error-banner, .delete-banner { display: flex; align-items: center; gap: 9px; padding: 11px 22px; font-size: 12px; background: var(--accent-wash); color: var(--danger); flex-shrink: 0; }
  .error-banner span { flex: 1; overflow-wrap: anywhere; }
  .error-banner button:not(.icon-button) { padding: 4px 8px; text-decoration: underline; }
  .error-banner .icon-button { width: 23px; height: 23px; }
  .error-banner .icon-button :global(svg) { width: 14px; height: 14px; }
  .delete-banner { color: var(--text); }
  .delete-banner span { margin-right: auto; }
  .delete-banner button { font-size: 11px; padding: 6px 11px; }
  .conversation-error { padding: 12px 16px; margin: 12px 0 18px; background: var(--accent-wash); border-radius: 9px; color: var(--danger); font-size: 12px; }
  .model-note { display: flex; align-items: center; justify-content: center; gap: 5px; font-size: 10px; color: var(--text-faint); padding: 6px 16px 12px; }
  .model-note button { color: var(--blue); font-size: 10px; }
  .page-scroll { overflow-y: auto; flex: 1; }
  .settings-page { width: min(100%, 850px); margin: 0 auto; padding: 47px 48px; overflow-y: auto; }
  .eyebrow { font-size: 11px; color: var(--text-faint); margin-bottom: 7px; }
  .settings-page h1 { font-size: 27px; font-weight: 620; letter-spacing: -.7px; }
  .page-description { font-size: 13px; color: var(--text-dim); margin: 8px 0 29px; }
  .preference h3 { font-size: 13px; font-weight: 550; }
  .preference p { color: var(--text-faint); font-size: 11px; margin-top: 5px; }
  .preference { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 24px 0; border-bottom: 1px solid var(--line); }
  .preference button { font-size: 11px; }
  .switch { width: 35px; height: 21px; border-radius: 12px; background: var(--line-strong); padding: 2px; justify-content: flex-start; }
  .switch.on { background: var(--accent); }
  .switch i { width: 17px; height: 17px; border-radius: 50%; background: white; box-shadow: 0 1px 3px rgb(0 0 0 / 15%); }
  .switch.on i { transform: translateX(14px); }
  .about { text-align: center; padding: 48px 0 0; color: var(--text-faint); }
  .about h3 { font-size: 13px; font-weight: 550; color: var(--text-dim); margin-top: 10px; }
  .about p { font-size: 11px; margin-top: 9px; }
  .about > span { display: block; font-size: 9px; margin-top: 10px; }
  @media (max-width: 1200px) { .history { width: 225px; } .sidebar { width: 185px; } .suggestions { gap: 8px; } .suggestions > button { padding: 16px 12px; } }
  @media (max-width: 950px) { .history { display: none; } .history.narrow-open { display: flex; position: absolute; z-index: 5; left: 185px; top: 58px; bottom: 0; background: var(--content-plane); box-shadow: var(--shadow-float); } .welcome { padding-top: 9vh; } }
  @media (max-width: 700px) {
    .sidebar { display: none; position: fixed; top: 0; bottom: 0; left: 0; z-index: 10; width: 225px; }
    .show-mobile-nav .sidebar { display: flex; }
    .nav-backdrop { position: fixed; inset: 0; background: rgb(20 25 40 / 20%); z-index: 9; }
    .mobile-toggle { display: inline-flex; }
    .history.narrow-open { left: 0; top: 54px; }
    .topbar { height: 54px; padding: 0 13px; }
    .local-badge { display: none; }
    .welcome { padding: 50px 22px 25px; }
    .welcome h1 { font-size: 26px; }
    .welcome-mark { width: 65px; height: 65px; border-radius: 20px; }
    .suggestions { grid-template-columns: 1fr; margin-top: 27px; gap: 9px; }
    .suggestions > button { padding: 13px 16px; display: grid; grid-template-columns: 19px 1fr; gap: 4px 10px; }
    .suggestions > button > :global(svg:first-child) { grid-row: span 2; align-self: center; }
    .suggestions span { grid-column: 2; }
    .mobile-break { display: block; }
    .messages { padding: 24px 16px; }
    .approval { margin-left: 0; padding: 16px; }
    .approval-actions { flex-wrap: wrap; }
    .approval-actions span { width: 100%; }
    .settings-page { padding: 28px 22px; }
    .error-banner { padding: 10px 14px; font-size: 11px; }
  }
</style>
