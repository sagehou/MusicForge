import type { ReactNode } from "react";
import { SlidersHorizontal } from "lucide-react";
import { useI18n } from "../i18n";
import { AppearanceControls } from "./theme-selector";

export function PageHeading({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return <header className="page-heading"><div className="heading-content">{children}</div><details className="heading-preferences" onKeyDown={event => {
    if (event.key === "Escape" && event.currentTarget.open) { event.preventDefault(); event.currentTarget.open = false; event.currentTarget.querySelector("summary")?.focus(); }
  }}><summary aria-label={t("theme.preferences")} title={t("theme.preferences")}><SlidersHorizontal size={16} /></summary><AppearanceControls /></details></header>;
}
