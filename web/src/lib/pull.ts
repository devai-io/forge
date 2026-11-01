// Pull to refresh (features/shell/PullToRefresh.tsx): its distances and
// where a pull may start.

/** Pull distance (after resistance) that refreshes on release. */
export const PULL_THRESHOLD = 70;
export const PULL_MAX = 110;
export const RESISTANCE = 0.5; // the indicator moves half as far as the finger
export const MIN_SPIN_MS = 500; // a refresh shows long enough to be seen

/** Whether a pull may start at this element. */
export function canPullFrom(target: EventTarget | null): boolean {
  if (document.querySelector("[aria-modal=true]")) return false;
  for (let el = target instanceof Element ? target : null; el && el !== document.body; el = el.parentElement) {
    if (el.matches("input, textarea, select, [contenteditable=''], [contenteditable=true], .xterm, [data-no-pull]")) return false;
    const oy = getComputedStyle(el).overflowY;
    if ((oy === "auto" || oy === "scroll") && el.scrollTop > 0) return false;
  }
  return true;
}
