export type Encoding = { codec: "opus" | "mp3"; mode: "vbr" | "cbr"; bitrate: number; quality: number };
export type Settings = {
  enabled: boolean; source: string; output: string; encoding: Encoding; concurrency: number; scan_minutes: number;
  lidarr_prefix: string; nav_url: string; nav_user: string; nav_password?: string; nav_library: number;
  oidc_issuer: string; oidc_client_id: string; oidc_secret?: string; bound_issuer?: string; bound_subject?: string;
  bound_username?: string; bound_email?: string;
};
export type Source = { id: number; path: string; artist: string; album: string; title: string; track: number; disc: number; present: boolean; output: string; output_present: boolean; status: string; error: string; size: number; metadata: Record<string, string> };
export type Job = { id: number; kind: string; state: string; attempts: number; progress: number; log?: string; created: number; updated: number; args: { id?: number; dirs?: string[] } };
export type Dashboard = { source_count: number; output_count: number; ready_count: number; expired_count: number; rebuild_count: number; failed_count: number; online: boolean; storage_message: string; codec: string; enabled: boolean; version: string };
export type Me = { initialized: boolean; authenticated: boolean; username?: string; method?: string; csrf?: string; oidc: boolean; version: string };
