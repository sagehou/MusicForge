import { useEffect, useId, useRef, type ReactNode } from "react";
import { X } from "lucide-react";
import { useI18n } from "../i18n";
import { Button } from "./ui/button";

export function InlineDetails({ id, title, subtitle, children, close, returnFocusID, closeLabel, busy = false, actions, className = "" }: {
  id: string; title: string; subtitle?: string; children: ReactNode; close: () => void; returnFocusID: string; closeLabel?: string;
  busy?: boolean; actions?: ReactNode; className?: string;
}) {
  const { t } = useI18n();
  const headingID = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const latest = useRef({ close, busy });
  latest.current = { close, busy };
  function collapse() {
    if (latest.current.busy) return;
    latest.current.close();
    requestAnimationFrame(() => (document.getElementById(returnFocusID) || document.getElementById("library-title"))?.focus({ preventScroll: true }));
  }
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      heading.current?.focus({ preventScroll: true });
      heading.current?.scrollIntoView({ block: "nearest" });
    });
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented && !heading.current?.closest("[inert]")) {
        event.preventDefault(); collapse();
      }
    };
    document.addEventListener("keydown", escape);
    return () => { cancelAnimationFrame(frame); document.removeEventListener("keydown", escape); };
  }, [returnFocusID]);
  return <section id={id} role="region" className={`inline-details ${className}`} aria-labelledby={headingID} aria-busy={busy}>
    <header className="detail-heading"><div className="detail-titlebar"><div><h2 id={headingID} ref={heading} tabIndex={-1}>{title}</h2>{subtitle && <p dir="auto">{subtitle}</p>}</div><div className="actions">{actions}<Button type="button" variant="ghost" size="icon" aria-label={closeLabel || t("app.close")} title={closeLabel || t("app.close")} disabled={busy} onClick={collapse}><X size={18} /></Button></div></div></header>
    <div className="detail-content">{children}</div>
  </section>;
}
