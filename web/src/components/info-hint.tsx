import type { ReactNode } from "react";
import { Info } from "lucide-react";
import { useI18n } from "../i18n";

export function InfoHint({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return <details className="info-hint" onKeyDown={event => {
    if (event.key === "Escape" && event.currentTarget.open) {
      event.preventDefault(); event.stopPropagation(); event.currentTarget.open = false;
      event.currentTarget.querySelector("summary")?.focus();
    }
  }}><summary aria-label={t("app.moreInfo")}><Info size={15} aria-hidden="true" /></summary><div className="info-hint-content">{children}</div></details>;
}
