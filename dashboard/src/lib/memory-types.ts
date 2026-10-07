// Adapted from TencentCloud/Octop (MIT); see THIRD_PARTY_NOTICES.md.
export type AtomKind =
  | "Fact"
  | "Decision"
  | "Task"
  | "Preference"
  | "ConflictCandidate";

export type Importance = "low" | "medium" | "high";
export type Confidence = "low" | "medium" | "high";

export interface AtomItem {
  id: string;
  entity_id: string;
  candidate_id: string;
  assertion: string;
  verbatim_quote: string;
  search_terms?: string[];
  occurred_at?: string | null;
  importance: Importance;
  confidence: Confidence;
  /**
   * Backend AtomCard has no status field. Deprecation is represented by deprecated_at:
   * null = active; timestamp = deprecated by a newer atom or manual action.
   * Use isAtomDeprecated() instead of comparing against the removed status field.
   */
  deprecated_at?: string | null;
  superseded_by?: string | null;
  created_at: string;
  /** Filled by the dashboard handler from the linked Candidate. */
  kind: AtomKind | null;
}

/** Whether an atom is deprecated. Active atoms have deprecated_at = null. */
export function isAtomDeprecated(
  atom: Pick<AtomItem, "deprecated_at">,
): boolean {
  return atom.deprecated_at != null;
}

export interface ListAtomsResponse {
  items: AtomItem[];
  total: number;
  has_more: boolean;
}

export interface RawEventItem {
  id: string;
  host: string;
  session_id: string | null;
  thread_id: string | null;
  user: string | null;
  timestamp: string;
  event_type: string;
  content: string;
  payload?: Record<string, unknown>;
}

export interface ListRawEventsResponse {
  items: RawEventItem[];
  total: number;
  has_more: boolean;
}

export interface ListRawEventsBody {
  session_id?: string;
  thread_id?: string;
  event_type?: string;
  query?: string;
  offset?: number;
  limit?: number;
}

export type ExtractTriggerMode = "idle" | "interval";

export interface ExtractConfig {
  /** Missing on older Octop API processes; absence keeps the historical enabled default. */
  memory_enabled?: boolean;
  extract_on_session_end: boolean;
  extract_trigger_mode: ExtractTriggerMode;
  extract_idle_seconds: number;
  extract_interval_seconds: number;
  /**
   * "provider/model" ref used for extraction / promotion. null/absent = AUTO
   * (follow the chat model); send "" to reset back to AUTO.
   */
  aux_model?: string | null;
}

export interface EntityItem {
  id: string;
  entity_type: string;
  canonical_name: string;
  aliases: string[];
  atom_count: number;
  created_at: string;
  page_dirty?: boolean;
}

/** L3 entity page — the long-form, LLM-regenerated summary for a topic. */
export interface EntityPage {
  id: string;
  entity_id: string;
  summary_markdown: string;
  headline: string;
  topics: string[];
  dirty: boolean;
  summary_version: number;
  created_at: string;
  updated_at: string;
}

export type EntityDetail = EntityItem & { page: EntityPage | null };

export interface ListEntitiesResponse {
  items: EntityItem[];
  total: number;
  has_more: boolean;
}

export interface EpisodeItem {
  id: string;
  occurred_at: string;
  summary: string;
  verbatim_quote: string;
  emotion: string;
  intensity: number;
  people: string[];
  topics: string[];
  created_at: string;
}

export interface ListEpisodesResponse {
  items: EpisodeItem[];
  total: number;
  has_more: boolean;
}

/** Structured run stats attached to an ``extract_run`` journal entry. */
export interface ExtractRunStats {
  events_considered?: number;
  events_extracted?: number;
  candidates?: number;
  promoted?: number;
  merged?: number;
  conflicts?: number;
  needs_review?: number;
  dropped?: number;
  llm_calls?: number;
  failure_reason?: string | null;
}

export interface JournalItem {
  id: string;
  timestamp: string;
  action: string;
  actor: string;
  target_entity_id?: string | null;
  target_atom_id?: string | null;
  target_candidate_id?: string | null;
  note?: string | null;
  before?: Record<string, unknown> | null;
  /** Short target memory/topic text enriched by the backend for specific action display. */
  target_summary?: string | null;
  /** Present on ``extract_run`` rows: structured stats for this extraction pass. */
  after?: ExtractRunStats | Record<string, unknown> | null;
}

export interface ListJournalResponse {
  items: JournalItem[];
  total: number;
  has_more: boolean;
}

export type CandidateStatus =
  | "pending"
  | "needs_review"
  | "conflict"
  | "promoted"
  | "rejected";

export interface CandidateItem {
  id: string;
  raw_event_ids: string[];
  candidate_type: AtomKind;
  status: CandidateStatus;
  title: string;
  assertion: string;
  verbatim_quote: string;
  quote_event_id: string;
  subject_name: string;
  subject_entity_type: string;
  target_entity_id: string | null;
  confidence: Confidence;
  importance: Importance;
  recommended_action: string;
  promotion_reason: string;
  extractor_version: string;
  created_at: string;
  decided_at?: string | null;
  decided_by?: string | null;
  session_id?: string | null;
}

export interface ListCandidatesResponse {
  items: CandidateItem[];
  total: number;
  has_more: boolean;
}

export interface PromoteCandidateResponse {
  promoted: number;
  merged: number;
  conflicts: number;
  needs_review: number;
  dropped: number;
  llm_calls: number;
}

export interface RejectCandidateResponse {
  candidate_id: string;
  status: "rejected";
}

export interface CreateAtomResponse {
  atom: AtomItem;
  entity: EntityItem;
  created_entity: boolean;
  status: "created";
}

export interface ReplaceAtomResponse {
  old_atom_id: string;
  atom: AtomItem;
  status: "replaced" | "unchanged";
}

export interface LastExtractRun {
  timestamp?: string;
  session_id?: string | null;
  quiet?: boolean;
  note?: string;
  events_extracted?: number;
  candidates?: number;
  failure_reason?: string | null;
}

export interface StatsCounts {
  raw_events: number;
  atoms: number;
  entities: number;
  dirty_pages: number;
  episodes: number;
  candidates_pending: number;
  atoms_delta_7d: number;
  entities_delta_7d: number;
  episodes_delta_7d: number;
  last_extract_run?: LastExtractRun | null;
}

export interface StatsAtomKindsResponse {
  series: Array<{ kind: string; count: number }>;
}

export interface StatsGrowthBucket {
  date: string; // YYYY-MM-DD
  atoms: number;
  entities: number;
  episodes: number;
  /** Daily count of user_message raw events (one user utterance per turn). */
  turns: number;
}

export interface StatsGrowthResponse {
  series: StatsGrowthBucket[];
}

export interface RecentJournalResponse {
  items: JournalItem[];
}

export interface TerminalAtomResponse {
  items: AtomItem[];
}

export interface TerminalEpisodeResponse {
  items: EpisodeItem[];
}

export interface TerminalEntityResponse {
  items: EntityItem[];
}

// ---------------------------------------------------------------------------
// Request bodies
// ---------------------------------------------------------------------------

export interface ListAtomsBody {
  entity_id?: string;
  candidate_type?: AtomKind;
  importance_min?: Importance;
  include_deprecated?: boolean;
  query?: string;
  order_by?: "created_at" | "occurred_at" | "importance";
  order?: "asc" | "desc";
  offset?: number;
  limit?: number;
}

export interface ListEntitiesBody {
  entity_type?: string;
  query?: string;
  order_by?: "created_at" | "atom_count";
  order?: "asc" | "desc";
  offset?: number;
  limit?: number;
}

export interface ListEpisodesBody {
  emotion?: string;
  intensity_min?: number;
  date_from?: string;
  date_to?: string;
  topic?: string;
  query?: string;
  offset?: number;
  limit?: number;
}

export interface ListJournalBody {
  action?: string;
  target_type?: "atom" | "entity" | "candidate";
  actor?: string;
  time_from?: string;
  time_to?: string;
  target_entity_id?: string;
  target_atom_id?: string;
  target_candidate_id?: string;
  offset?: number;
  limit?: number;
}

export interface ListCandidatesBody {
  status?: CandidateStatus;
  candidate_type?: AtomKind;
  session_id?: string;
  target_entity_id?: string;
  time_from?: string;
  time_to?: string;
  query?: string;
  offset?: number;
  limit?: number;
}
