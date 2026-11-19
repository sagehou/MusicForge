import { useEffect, useRef } from "react";

export function useRevealPreview<T extends HTMLElement>(result: unknown, focus = false) {
  const ref = useRef<T>(null);
  useEffect(() => {
    if (!result) return;
    const frame = requestAnimationFrame(() => {
      const panel = ref.current;
      if (!panel?.getClientRects().length || panel.closest("[inert], details:not([open])")) return;
      if (focus && !panel.contains(document.activeElement)) panel.focus({ preventScroll: true });
      panel.scrollIntoView({ block: "nearest", inline: "nearest" });
    });
    return () => cancelAnimationFrame(frame);
  }, [result, focus]);
  return ref;
}
