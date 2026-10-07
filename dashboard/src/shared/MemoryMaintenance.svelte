<script lang="ts">
  import { LoaderCircle, CheckCircle2, CircleAlert, X } from "lucide-svelte";
  import { phaseLabel, jobSummary, type MemoryJob } from "../lib/memory";
  let { job, stale = false }: { job: MemoryJob; stale?: boolean } = $props();
  let dismissed = $state("");
  const visible = $derived(!!job.id && dismissed !== job.id && ["running", "done", "failed"].includes(job.status));
</script>

{#if visible}
  <div class="memory-maintenance" class:failed={job.status === "failed"} role="status">
    {#if job.status === "running"}<LoaderCircle class="spin" size={17} />{:else if job.status === "failed"}<CircleAlert size={17} />{:else}<CheckCircle2 size={17} />{/if}
    <div><strong>{job.status === "running" ? phaseLabel(job) : job.status === "failed" ? "记忆整理失败" : "记忆整理已完成"}</strong>
      <p>{#if job.status === "running"}{Math.floor(job.elapsed_seconds || 0)} 秒。{job.kind === "slim" ? "整理期间暂停发送，完成后自动恢复。" : "对话可以继续。"}{:else if job.status === "failed"}{job.error} 对话已恢复可用。{:else}记忆已保存，可以继续使用。{/if}</p>
      {#if stale}<p>连接暂时中断，当前状态可能已过时；正在重试。</p>{/if}
      {#if job.status === "running" && (job.elapsed_seconds || 0) >= 60}<p>大库整理或磁盘压缩可能需要更长时间；当前没有可用的剩余时间估计。</p>{/if}
      {#if job.result?.backup_path}<p>备份：{String(job.result.backup_path).split(/[\/]/).at(-1)}</p>{/if}
      {#if jobSummary(job)}<p>{jobSummary(job)}</p>{/if}
    </div>
    {#if job.status !== "running"}<button class="icon-button" aria-label="关闭记忆整理结果" onclick={() => dismissed = job.id || ""}><X size={15} /></button>{/if}
  </div>
{/if}

<style>
  .memory-maintenance { display: flex; align-items: flex-start; gap: 10px; padding: 13px 24px; background: var(--blue-wash); color: var(--text); border-bottom: 1px solid var(--line); }
  .memory-maintenance > :global(svg) { margin-top: 3px; color: var(--blue); }
  .memory-maintenance > div { flex: 1; } strong { font-size: 13px; } p { font-size: 12px; color: var(--text-dim); }
  .failed { background: var(--accent-wash); } .failed > :global(svg) { color: var(--danger); }
  .memory-maintenance :global(.spin) { animation: spin 1s linear infinite; } @keyframes spin { to { transform: rotate(360deg); } }
</style>
