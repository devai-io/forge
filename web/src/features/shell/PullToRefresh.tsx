// Pull to refresh on touch screens, on every page: pull down from the top
// of the page and let go past the mark, and every query the page shows is
// fetched again (no reload — scroll, open drawers and typed text stay). The
// home-screen app has no browser refresh at all, so this is how a phone
// gets fresh data. The browser's own pull-to-refresh is turned off on
// touch screens (index.css) so the two never both fire.
//
// A pull is ignored when it starts in a field, a terminal, an open dialog,
// or inside a scrolled list that is not at its own top (that gesture scrolls
// the list), and when it is more sideways than down.

import { useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { PULL_MAX, PULL_THRESHOLD, RESISTANCE, MIN_SPIN_MS, canPullFrom } from "@/lib/pull";

export function PullToRefresh() {
  const qc = useQueryClient();
  const [pull, setPull] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const gesture = useRef<{ x: number; y: number; decided: boolean } | null>(null);
  const distance = useRef(0);
  const busy = useRef(false);

  const refresh = useCallback(async () => {
    busy.current = true;
    setRefreshing(true);
    try {
      await Promise.all([qc.refetchQueries({ type: "active" }), new Promise((r) => setTimeout(r, MIN_SPIN_MS))]);
    } finally {
      busy.current = false;
      setRefreshing(false);
    }
  }, [qc]);

  useEffect(() => {
    const start = (e: TouchEvent) => {
      const t = e.touches[0];
      gesture.current =
        t && e.touches.length === 1 && !busy.current && window.scrollY <= 0 && canPullFrom(e.target)
          ? { x: t.clientX, y: t.clientY, decided: false }
          : null;
    };
    const move = (e: TouchEvent) => {
      const g = gesture.current;
      const t = e.touches[0];
      if (!g || !t) return;
      const dx = t.clientX - g.x;
      const dy = t.clientY - g.y;
      if (!g.decided) {
        if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return;
        // Down, mostly vertical, and still at the top: a pull. Anything else: let it scroll.
        if (dy <= 0 || Math.abs(dx) > Math.abs(dy) || window.scrollY > 0) {
          gesture.current = null;
          return;
        }
        g.decided = true;
      }
      const d = Math.min(PULL_MAX, Math.max(0, dy * RESISTANCE));
      if (e.cancelable) e.preventDefault(); // no rubber-band under the indicator
      distance.current = d;
      setPull(d);
    };
    const end = () => {
      const g = gesture.current;
      gesture.current = null;
      if (!g?.decided) return;
      const d = distance.current;
      distance.current = 0;
      setPull(0);
      if (d >= PULL_THRESHOLD) void refresh();
    };
    window.addEventListener("touchstart", start, { passive: true });
    window.addEventListener("touchmove", move, { passive: false });
    window.addEventListener("touchend", end);
    window.addEventListener("touchcancel", end);
    return () => {
      window.removeEventListener("touchstart", start);
      window.removeEventListener("touchmove", move);
      window.removeEventListener("touchend", end);
      window.removeEventListener("touchcancel", end);
    };
  }, [refresh]);

  if (!pull && !refreshing) return null;
  const ready = refreshing || pull >= PULL_THRESHOLD;
  const offset = refreshing ? PULL_THRESHOLD * 0.6 : pull * 0.6;
  return (
    <div
      className="pointer-events-none fixed inset-x-0 top-0 z-50 flex justify-center"
      style={{ transform: `translateY(calc(env(safe-area-inset-top, 0px) + ${offset - 36}px))` }}
    >
      <div
        role="status"
        aria-label={refreshing ? "Refreshing" : ready ? "Release to refresh" : "Pull to refresh"}
        className={clsx(
          "grid size-9 place-items-center rounded-full border border-line bg-surface shadow-pop transition-colors",
          ready ? "text-accent" : "text-fg-3",
        )}
        style={{ opacity: refreshing ? 1 : Math.min(1, pull / PULL_THRESHOLD + 0.2) }}
      >
        <RefreshCw
          className={clsx("size-4", refreshing && "animate-spin")}
          style={refreshing ? undefined : { transform: `rotate(${pull * 3}deg)` }}
          aria-hidden
        />
      </div>
    </div>
  );
}
