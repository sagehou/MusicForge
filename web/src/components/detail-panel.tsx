import { createContext, useContext, useEffect, useId, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import { useI18n } from "../i18n";
import { AppearanceControls } from "./theme-selector";
import { Button } from "./ui/button";

export const DetailPanelSuspended = createContext(false);

export function DetailPanel({ title, subtitle, children, close, closeLabel, busy = false, actions, className = "" }: {
  title: string; subtitle?: string; children: ReactNode; close: () => void; closeLabel?: string;
  busy?: boolean; actions?: ReactNode; className?: string;
}) {
  const { t } = useI18n();
  const suspended = useContext(DetailPanelSuspended);
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const ignoredCloses = useRef(0);
  useEffect(() => {
    const panel = dialog.current;
    if (!panel) return;
    if (suspended) {
      if (panel.open) { ignoredCloses.current++; panel.close(); }
      return;
    }
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    panel.showModal();
    return () => { document.body.style.overflow = overflow; };
  }, [suspended]);
  return createPortal(<dialog ref={dialog} className={`detail-panel ${className}`} aria-labelledby={id} onClose={() => {
    if (ignoredCloses.current) { ignoredCloses.current--; return; }
    close();
  }} onCancel={event => { if (busy) event.preventDefault(); }}>
    <header className="detail-heading"><AppearanceControls /><div className="detail-titlebar"><div><h2 id={id}>{title}</h2>{subtitle && <p dir="auto">{subtitle}</p>}</div><div className="actions">{actions}<Button type="button" variant="ghost" size="icon" aria-label={closeLabel || t("app.close")} disabled={busy} onClick={() => dialog.current?.close()}><X size={18} /></Button></div></div></header>
    <div className="detail-content">{children}</div>
  </dialog>, document.body);
}
