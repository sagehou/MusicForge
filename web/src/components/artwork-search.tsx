import { InfoHint } from "./info-hint";
import { useState, type ReactNode } from "react";
import { Loader2, Search } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import { Button } from "./ui/button";

export function artworkFile(image: string) {
  return new File([Uint8Array.from(atob(image), value => value.charCodeAt(0))], "cover.jpg", { type: "image/jpeg" });
}
type Candidate = { id: string; title: string; artist: string; date: string; country: string; source: string; url: string };
export type ArtworkPreview = { width: number; height: number; source: string; url: string };

export function ArtworkSearch({ source, disabled, choose, loading, children }: { source: Source; disabled: boolean; choose: (file: File, preview: ArtworkPreview) => void; loading: (busy: boolean) => void; children: ReactNode }) {
  const { t, errorMessage } = useI18n();
  const [query, setQuery] = useState(`${source.artist} ${source.album}`.trim());
  const [provider, setProvider] = useState("itunes");
  const [candidates, setCandidates] = useState<Candidate[]>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  async function search(selectedProvider = provider) {
    setBusy(true); loading(true); setError(undefined); setCandidates(undefined); setProvider(selectedProvider);
    try { setCandidates((await api<{ candidates: Candidate[] }>(`/library/${source.id}/artwork/search`, "POST", { query, provider: selectedProvider })).candidates); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); loading(false); }
  }
  async function pick(id: string) {
    setBusy(true); loading(true); setError(undefined);
    try {
      const { image, ...preview } = await api<ArtworkPreview & { image: string }>(`/library/${source.id}/artwork/preview?release=${encodeURIComponent(id)}`);
      choose(artworkFile(image), preview);
    } catch (err) { setError(err as Error); }
    finally { setBusy(false); loading(false); }
  }
  return <section className="cover-search" aria-label={t("coverSearch.title")}><form className="cover-search-form" onSubmit={event => { event.preventDefault(); if (!disabled && !busy && query.trim()) void search(); }}><label className="field"><span className="field-label">{t("coverSearch.query")}</span><input value={query} maxLength={500} disabled={disabled || busy} onChange={event => setQuery(event.target.value)} /></label><label className="field cover-provider"><span className="field-label">{t("coverSearch.provider")}</span><select value={provider} disabled={disabled || busy} onChange={event => { setProvider(event.target.value); setCandidates(undefined); setError(undefined); }}><option value="itunes">Apple Music / iTunes</option><option value="netease">{t("lyrics.name.netease")}</option></select></label><Button type="submit" size="sm" variant="outline" disabled={disabled || busy || !query.trim()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Search size={14} />}{t("coverSearch.search")}</Button><InfoHint>{t("coverSearch.hint")}</InfoHint></form>{error && <p role="alert">{errorMessage(error.message)}</p>}{(error || candidates?.length === 0) && <div className="cover-search-empty">{!error && <p>{t("coverSearch.empty")}</p>}<Button size="sm" variant="outline" disabled={disabled || busy || !query.trim()} onClick={() => void search(provider === "itunes" ? "netease" : "itunes")}>{t("coverSearch.tryOther")}</Button></div>}
    <div className="cover-search-results">
      {children}
      {!!candidates?.length && <ul className="cover-candidates" role="list">{candidates.map(candidate => <li key={candidate.id}><div><strong dir="auto">{candidate.title}</strong><small dir="auto">{candidate.artist} · {candidate.date} · {candidate.country}</small><a href={candidate.url} target="_blank" rel="noreferrer">{candidate.source}</a></div><Button type="button" size="sm" variant="outline" disabled={disabled || busy} onClick={() => void pick(candidate.id)}>{t("coverSearch.preview")}</Button></li>)}</ul>}
    </div>
  </section>;
}
