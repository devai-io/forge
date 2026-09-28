import { useSyncExternalStore } from "react";

// One shared minute ticker instead of an interval per timestamp on screen.
let now = Date.now();
const listeners = new Set<() => void>();
let timer: number | undefined;

function subscribe(cb: () => void) {
  listeners.add(cb);
  if (timer === undefined) {
    // Idle for a while with nothing on screen: catch up before the first tick.
    // useSyncExternalStore re-reads the snapshot after subscribing.
    now = Date.now();
    timer = window.setInterval(() => {
      now = Date.now();
      listeners.forEach((l) => l());
    }, 30_000);
  }
  return () => {
    listeners.delete(cb);
    if (!listeners.size && timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  };
}

export function useNow(): number {
  return useSyncExternalStore(subscribe, () => now);
}
