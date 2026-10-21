import type { ReactNode } from "react";
import { AppearanceControls } from "./theme-selector";

export function PageHeading({ children }: { children: ReactNode }) {
  return <header className="page-heading"><div className="heading-content">{children}</div><AppearanceControls /></header>;
}
