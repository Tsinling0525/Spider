import type { StatsCounts } from "./memory-types";

export interface MemoryConfig {
  memory_enabled: boolean;
  extract_on_session_end: boolean;
  extract_trigger_mode: "idle" | "interval";
  extract_idle_seconds: number;
  extract_interval_seconds: number;
  aux_model: string;
}
export interface MemoryJob {
  id?: string;
  kind?: string;
  status: "idle" | "running" | "done" | "failed" | "unavailable";
  phase?: string;
  started_at?: string;
  finished_at?: string;
  elapsed_seconds?: number;
  session_id?: string;
  result?: Record<string, unknown>;
  error?: string;
}
export interface MemoryPort {
  memory<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T>;
  memoryConfig(): Promise<MemoryConfig>;
  saveMemoryConfig(config: MemoryConfig): Promise<MemoryConfig>;
  memoryStatus(): Promise<MemoryJob>;
  startMemoryJob(kind: string, sessionID?: string): Promise<MemoryJob>;
  exportMemory(): Promise<Blob>;
  importMemory(file: File, dryRun: boolean, conflict: string): Promise<AdoptResult>;
  doctorMemory(): Promise<DoctorReport>;
}
export interface AdoptResult {
  applied: number;
  skipped: number;
  errors: number;
  dry_run: boolean;
  already_adopted: boolean;
  applied_by_table?: Record<string, number>;
  skipped_by_table?: Record<string, number>;
}
export interface DoctorReport { all_passed: boolean; checks: { name: string; passed: boolean; message?: string; hint?: string }[] }
export type MemoryRow = { id: string; [key: string]: unknown };
export interface MemoryList { items: MemoryRow[]; total: number; has_more: boolean }
export const emptyCounts = (): StatsCounts => ({ raw_events: 0, atoms: 0, entities: 0, dirty_pages: 0, episodes: 0, candidates_pending: 0, atoms_delta_7d: 0, entities_delta_7d: 0, episodes_delta_7d: 0 });
export const memoryLayers = [
  { id: "raw_events", method: "list_raw_events", label: "原始记录", layer: "L0", description: "对话原文与来源，记忆提炼的起点。" },
  { id: "candidates", method: "list_candidates", label: "候选记忆", layer: "L1", description: "模型提取的待处理内容；冲突与不确定内容等待审核。" },
  { id: "atoms", method: "list_atoms", label: "原子记忆", layer: "L2", description: "已经确认的事实、偏好、决策和任务。" },
  { id: "episodes", method: "list_episodes", label: "情景记忆", layer: "L2.5", description: "经历、情绪与生活故事。" },
  { id: "entities", method: "list_entities", label: "实体页面", layer: "L3", description: "人物、项目和主题的持续摘要。" },
  { id: "journal", method: "list_journal", label: "审计日志", layer: "日志", description: "提炼、审核、合并和废弃的可追溯记录。" },
] as const;
export type MemoryLayer = typeof memoryLayers[number]["id"];
export const kindLabels: Record<string, string> = { Fact: "事实", Preference: "偏好", Decision: "决策", Task: "任务", ConflictCandidate: "冲突", User: "用户", Person: "人物", Project: "项目", pending: "待处理", needs_review: "待审核", conflict: "冲突", promoted: "已提升", rejected: "已拒绝", high: "高", medium: "中", low: "低" };
export function display(value: unknown): string { return value == null ? "" : Array.isArray(value) ? value.map(display).join("、") : typeof value === "object" ? JSON.stringify(value, null, 2) : String(value); }
export function phaseLabel(job: MemoryJob): string { return ({ waiting_idle: "等待当前对话结束", backup: "备份记忆数据库", vacuum: "压缩数据库", organizing: "提炼与整理记忆", done: "已完成", failed: "失败" } as Record<string, string>)[job.phase || ""] || "准备中"; }
export function jobSummary(job: MemoryJob): string {
  const result = job.result;
  if (!result) return "";
  if (job.kind === "slim") return `数据库：${Number(result.bytes_before || 0).toLocaleString()} → ${Number(result.bytes_after || 0).toLocaleString()} 字节。`;
  if (job.kind === "extract" && Array.isArray(result.results)) {
    const rows = result.results as Record<string, unknown>[];
    const candidates = rows.reduce((sum, item) => sum + Number(item.candidates || 0), 0);
    const promoted = rows.reduce((sum, item) => sum + Number((item.promotion as Record<string, unknown> | null)?.promoted || 0), 0);
    const episodes = rows.reduce((sum, item) => sum + Number((item.episodes as Record<string, unknown> | null)?.extracted || 0), 0);
    return `处理 ${rows.length} 个会话，提取 ${candidates} 条候选，确认 ${promoted} 条原子记忆，记录 ${episodes} 个情景。`;
  }
  return "";
}
