import { InfoHint } from "./info-hint";
import { useState } from "react";
import { Loader2, Search } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import { Button } from "./ui/button";

export function artworkFile(image: string) {
  return new File([Uint8Array.from(atob(image), value => value.charCodeAt(0))], "cover.jpg", { type: "image/jpeg" });
}
type Candidate = { id: string; title: string; artist: string; date: string; country: string; source: string; url: string };

export function ArtworkSearch({ source, disabled, choose, loading }: { source: Source; disabled: boolean; choose: (file: File) => void; loading: (busy: boolean) => void }) {
  const { t, errorMessage } = useI18n();
  const [query, setQuery] = useState(`${source.artist} ${source.album}`.trim());
  const [candidates, setCandidates] = useState<Candidate[]>();
  const [preview, setPreview] = useState<{ width: number; height: number; source: string; url: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error>();
  async function search() {
    setBusy(true); loading(true); setError(undefined);
    try { setCandidates((await api<{ candidates: Candidate[] }>(`/library/${source.id}/artwork/search`, "POST", { query })).candidates); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); loading(false); }
  }
  async function pick(id: string) {
    setBusy(true); loading(true); setError(undefined);
    try {
      const result = await api<{ image: string; width: number; height: number; source: string; url: string }>(`/library/${source.id}/artwork/preview?release=${id}`);
      choose(artworkFile(result.image)); setPreview(result);
    } catch (err) { setError(err as Error); }
    finally { setBusy(false); loading(false); }
  }
  return <details className="cover-search"><summary><Search size={15} />{t("coverSearch.title")}</summary><div className="comparison-body"><InfoHint>{t("coverSearch.hint")}</InfoHint><label className="field"><span className="field-label">{t("coverSearch.query")}</span><input value={query} maxLength={500} disabled={disabled || busy} onChange={event => setQuery(event.target.value)} /></label><Button size="sm" variant="outline" disabled={disabled || busy || !query.trim()} onClick={() => void search()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Search size={14} />}{t("coverSearch.search")}</Button>{error && <p role="alert">{errorMessage(error.message)}</p>}{candidates?.length === 0 && <p>{t("coverSearch.empty")}</p>}
    {candidates && <ul className="cover-candidates">{candidates.map(candidate => <li key={candidate.id}><div><strong dir="auto">{candidate.title}</strong><small dir="auto">{candidate.artist} · {candidate.date} · {candidate.country}</small><a href={candidate.url} target="_blank" rel="noreferrer">{candidate.source}</a></div><Button size="sm" variant="outline" disabled={disabled || busy} onClick={() => void pick(candidate.id)}>{t("coverSearch.preview")}</Button></li>)}</ul>}
    {preview && <p role="status">{t("coverSearch.loaded", { width: preview.width, height: preview.height })} · <a href={preview.url} target="_blank" rel="noreferrer">{preview.source}</a></p>}
  </div></details>;
}
