// Small geometry helpers for the hand-built SVG charts.

/** Round an axis maximum up to a clean number with 2-4 ticks. */
export function niceMax(max: number): { top: number; step: number } {
  if (max <= 0) return { top: 4, step: 2 };
  const candidates = [1, 2, 5, 10, 20, 25, 50, 100];
  for (const step of candidates) {
    const top = Math.ceil(max / step) * step;
    if (top / step <= 4) return { top, step };
  }
  const step = Math.pow(10, Math.ceil(Math.log10(max / 4)));
  return { top: Math.ceil(max / step) * step, step };
}

/** Column path with a 4px rounded top and a square base. */
export function barPath(x: number, y: number, w: number, h: number): string {
  const r = Math.min(4, w / 2, h);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}
