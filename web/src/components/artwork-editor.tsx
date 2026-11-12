import { useEffect, useRef, useState } from "react";
import { Check, Loader2, RotateCcw, Upload } from "lucide-react";
import { api } from "../api";
import { useI18n } from "../i18n";
import type { Source } from "../types";
import { Button } from "./ui/button";
import { EditHistory } from "./edit-history";
import { ArtworkSearch, artworkFile } from "./artwork-search";
import { InlineDetails } from "./inline-details";
import { InfoHint } from "./info-hint";

export function ArtworkEditor({ source, saved, close, returnFocusID, onBusyChange }: { source: Source; saved: () => void; close: () => void; returnFocusID: string; onBusyChange: (busy: boolean) => void }) {
  const { t, errorMessage } = useI18n();
  const [data, setData] = useState<{ token: string; override: boolean }>();
  const [file, setFile] = useState<File>();
  const [crop, setCrop] = useState(false);
  const [image, setImage] = useState("");
  const [busy, setBusy] = useState(false);
  const [processing, setProcessing] = useState(false);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState<Error>();
  const [attempt, setAttempt] = useState(0);
  const generation = useRef(0);
  useEffect(() => { onBusyChange(busy || searching || processing); return () => onBusyChange(false); }, [busy, searching, processing, onBusyChange]);
  useEffect(() => {
    const controller = new AbortController();
    setData(undefined); setError(undefined);
    void api<{ token: string; override: boolean }>(`/library/${source.id}/artwork/edit`, "GET", undefined, controller.signal).then(value => { if (!controller.signal.aborted) setData(value); }).catch((err: Error) => { if (!controller.signal.aborted) setError(err); });
    return () => controller.abort();
  }, [source.id, attempt]);
  useEffect(() => {
    const version = ++generation.current;
    setImage("");
    if (!file) return;
    setProcessing(true); setError(undefined);
    void (async () => {
      if (!["image/jpeg", "image/png"].includes(file.type) || file.size > 8 * 1024 * 1024) throw new Error(t("coverEdit.limit"));
      const bitmap = await createImageBitmap(file);
      try {
        if (bitmap.width > 4096 || bitmap.height > 4096) throw new Error(t("coverEdit.limit"));
        const width = crop ? Math.min(bitmap.width, bitmap.height) : bitmap.width;
        const height = crop ? width : bitmap.height;
        const scale = Math.min(1, 1200 / Math.max(width, height));
        const canvas = document.createElement("canvas");
        canvas.width = Math.max(1, Math.round(width * scale)); canvas.height = Math.max(1, Math.round(height * scale));
        const context = canvas.getContext("2d");
        if (!context) throw new Error(t("coverEdit.failed"));
        context.fillStyle = "#fff"; context.fillRect(0, 0, canvas.width, canvas.height);
        context.drawImage(bitmap, (bitmap.width - width) / 2, (bitmap.height - height) / 2, width, height, 0, 0, canvas.width, canvas.height);
        if (version === generation.current) setImage(canvas.toDataURL("image/jpeg", 0.9));
      } finally { bitmap.close(); }
    })().catch((err: Error) => { if (version === generation.current) setError(err); }).finally(() => { if (version === generation.current) setProcessing(false); });
    return () => { generation.current++; };
  }, [file, crop, t]);
  async function save(reset = false) {
    setBusy(true); setError(undefined);
    try { await api(`/library/${source.id}/artwork/edit`, reset ? "DELETE" : "PUT", { token: data?.token, ...(reset ? {} : { image: image.split(",")[1] }) }); saved(); close(); requestAnimationFrame(() => document.getElementById(returnFocusID)?.focus()); }
    catch (err) { setError(err as Error); }
    finally { setBusy(false); }
  }
  return <InlineDetails className="artwork-editor" id={`artwork-editor-${source.id}`} title={t("coverEdit.title")} subtitle={source.album || source.path} close={close} returnFocusID={returnFocusID} busy={busy || searching || processing}>
    <InfoHint>{t("coverEdit.hint", { path: source.path.split("/").slice(0, -1).join("/") || "." })} {t("coverEdit.limit")}</InfoHint>
    {error && <div role="alert" className="error-box"><p>{errorMessage(error.message)}</p>{!data && <Button size="sm" variant="outline" onClick={() => setAttempt(value => value + 1)}>{t("audition.reload")}</Button>}</div>}
    {!data && !error && <Loader2 size={18} className="animate-spin" />}
    {data && <><ArtworkSearch source={source} disabled={busy || processing} loading={setSearching} choose={file => { setFile(file); setCrop(false); }} /><div className="artwork-editor-preview">{(image || source.artwork_url) && <img src={image || source.artwork_url} alt={t("coverEdit.preview")} />}</div><div className="actions"><label className="maintenance-upload"><Upload size={14} />{t("coverEdit.upload")}<input type="file" accept="image/jpeg,image/png" disabled={busy || processing || searching} onChange={event => { setFile(event.target.files?.[0]); event.target.value = ""; }} /></label><label className="check-field"><input type="checkbox" checked={crop} disabled={busy || processing || searching} onChange={event => setCrop(event.target.checked)} />{t("coverEdit.crop")}</label></div><div className="actions"><Button size="sm" disabled={busy || processing || searching || !image} onClick={() => void save()}>{busy || processing ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}{t("coverEdit.save")}</Button><Button size="sm" variant="outline" disabled={busy || searching || !data.override} onClick={() => { if (confirm(t("coverEdit.resetConfirm"))) void save(true); }}><RotateCcw size={14} />{t("coverEdit.reset")}</Button></div><EditHistory id={source.id} kind="artwork" disabled={busy || processing || searching} restore={content => { if (content.image) { setFile(artworkFile(content.image)); setCrop(false); } }} /></>}
  </InlineDetails>;
}
