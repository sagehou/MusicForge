import { useDeferredValue, useState } from "react";
import { Activity, Search } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { LibraryPage } from "../library-data";
import type { LyricsProvider } from "../types";
import { Button } from "./ui/button";
import { Loading, ResourceWarning, useResource, type Notice } from "./resource";

type ProviderStatus = {
  provider: Exclude<LyricsProvider, "source">; enabled: boolean;
  state: "unknown" | "ok" | "failed" | "cooldown" | "checking";
  checked: number; latency_ms: number; retry_at?: number; candidates: number;
  source_id?: number; title?: string; artist?: string; error?: string;
};

export function LyricsProviderDiagnostics({ notify }: { notify: Notice }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return <section className="panel settings-panel"><div className="settings-title"><Activity /><div><h2>{t("lyrics.diagnostics.title")}</h2><p>{t("lyrics.diagnostics.hint")}</p></div></div>
    <details onToggle={event => setOpen(event.currentTarget.open)}><summary>{t("lyrics.diagnostics.open")}</summary>{open && <ProviderChecks notify={notify} />}</details>
  </section>;
}

function ProviderChecks({ notify }: { notify: Notice }) {
  const { t, locale, date, errorMessage } = useI18n();
  const [query, setQuery] = useState("");
  const search = useDeferredValue(query.trim());
  const [sourceID, setSourceID] = useState(0);
  const [busy, setBusy] = useState<string>();
  const status = useResource<ProviderStatus[]>("/lyrics/providers/status", 5000);
  const tracks = useResource<LibraryPage>(`/library/browse?${new URLSearchParams({ view: "tracks", q: search, offset: "0", locale })}`);
  async function check(provider: ProviderStatus["provider"]) {
    if (!sourceID || busy) return;
    setBusy(provider);
    try {
      const result = await api<ProviderStatus[]>(`/lyrics/providers/${provider}/check`, "POST", { source_id: sourceID });
      status.update(() => result);
    } catch (err) { notify(err as Error, true); } finally { setBusy(undefined); status.retry(); }
  }
  return <div className="comparison-body">
    <p>{t("lyrics.diagnostics.checkHint")}</p>
    <label className="field"><span className="field-label">{t("lyrics.diagnostics.search")}</span><input type="search" value={query} maxLength={250} disabled={!!busy} onChange={event => { setQuery(event.target.value); setSourceID(0); }} /></label>
    <ResourceWarning error={tracks.error} retry={tracks.retry} />
    <label className="field"><span className="field-label">{t("lyrics.diagnostics.track")}</span><select value={sourceID} disabled={!!busy || !tracks.data} onChange={event => setSourceID(Number(event.target.value))}>
      <option value={0}>{t("lyrics.diagnostics.chooseTrack")}</option>
      {tracks.data?.rows.map(source => <option key={source.id} value={source.id} disabled={!source.title.trim() || !source.artist.trim() || !source.duration}>{source.title || source.path} · {source.artist}</option>)}
    </select></label>
    <small>{t("lyrics.diagnostics.trackHint")}</small>
    <ResourceWarning error={status.error} retry={status.retry} />
    {!status.data ? <Loading error={status.error} retry={status.retry} /> : <div className="lyrics-provider-status" aria-label={t("lyrics.diagnostics.title")}>{status.data.map(provider => <fieldset key={provider.provider}>
      <legend>{t(`lyrics.name.${provider.provider}`)}</legend>
      <p>{t(`lyrics.diagnostics.${busy === provider.provider ? "checking" : provider.state}`)} · {t(provider.enabled ? "lyrics.diagnostics.enabled" : "lyrics.diagnostics.disabled")}</p>
      {provider.checked > 0 && <small>{t("lyrics.diagnostics.last", { time: date(provider.checked), milliseconds: provider.latency_ms, count: provider.candidates })}</small>}
      {provider.title && <small dir="auto">{provider.title} · {provider.artist}</small>}
      {provider.retry_at && <p>{t("lyrics.diagnostics.retry", { time: date(provider.retry_at) })}</p>}
      {provider.error && <p role="alert">{errorMessage(provider.error)}</p>}
      <Button type="button" size="sm" variant="outline" disabled={!sourceID || !!busy || provider.state === "checking"} onClick={() => void check(provider.provider)}><Search size={14} />{t("lyrics.diagnostics.check", { name: t(`lyrics.name.${provider.provider}`) })}</Button>
    </fieldset>)}</div>}
  </div>;
}
