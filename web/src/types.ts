export type Encoding = { codec: "opus" | "mp3"; mode: "vbr" | "cbr"; bitrate: number; quality: number };
export type Settings = {
  enabled: boolean; source: string; output: string; encoding: Encoding; concurrency: number; scan_minutes: number;
  lidarr_prefix: string; nav_url: string; nav_user: string; nav_password?: string; nav_library: number;
  oidc_issuer: string; oidc_client_id: string; oidc_secret?: string; bound_issuer?: string; bound_subject?: string;
  bound_username?: string; bound_email?: string;
};
export type RepairStatus = { state: "running" | "imported" | "failed"; decoded_samples?: number; declared_samples?: number; pcm_verified: boolean; duration?: number; diagnostics?: string };
export type LyricsProvider = "source" | "lrclib" | "qq" | "kugou" | "netease";
export type LyricsStatus = { state: "ready" | "not_found" | "instrumental" | "failed" | "needs_review"; output: string; signature: string; path?: string; provider?: LyricsProvider; error?: string; checked: number; attempts?: { provider: LyricsProvider; state: "ready" | "not_found" | "instrumental" | "failed" | "needs_review"; error?: string }[] };
export type Source = { id: number; path: string; hash?: string; artist: string; album: string; title: string; track: number; disc: number; duration?: number; artwork_url?: string; present: boolean; output: string; output_present: boolean; status: string; error: string; size: number; source_bitrate?: number; source_audio?: { codec?: string; sample_rate?: number; bit_depth?: number; channels?: number }; output_size?: number; metadata: Record<string, string>; can_repair?: boolean; repair?: RepairStatus; possibly_damaged?: boolean; lyrics?: LyricsStatus; lyrics_script?: "simplified" | "traditional" | "unknown" };
export type Activity = { phase: string; path: string; artist: string; album: string; title: string; processed: number; total: number; percent: number; read_bytes?: number; read_total_bytes?: number };
export type Job = { id: number; kind: string; state: string; attempts: number; progress: number; log?: string; waiting_reason?: string; retry_at?: number; created: number; updated: number; args: { id?: number; dirs?: string[]; verify?: boolean }; counts: { total: number; done: number; failed: number; pending: number; running: number; cancelled: number }; scan?: Activity; current: Activity[]; scope: string[]; profile?: Encoding; can_delete: boolean; repair_id?: string; stopping?: boolean };
export type TaskItem = Pick<Job, "id" | "kind" | "state" | "attempts" | "progress" | "log" | "updated" | "profile"> & { activity: Activity; output: string; failure?: "read" | "decode" | "encode" | "validate"; repair_target?: { id: number; hash: string; can_repair: boolean } };
export type Dashboard = { source_count: number; output_count: number; ready_count: number; expired_count: number; rebuild_count: number; failed_count: number; online: boolean; storage_checking?: boolean; storage_message: string; codec: string; enabled: boolean; version: string };
export type Me = { initialized: boolean; authenticated: boolean; username?: string; method?: string; csrf?: string; oidc: boolean; version: string };

export type RepairTask = { id: string; source_id: number; source_hash: string; state: "running" | "success" | "failed" | "stopped"; activity: Activity; profile: Encoding; progress: number; created: number; updated: number; output: string; log: string; stopping: boolean };
