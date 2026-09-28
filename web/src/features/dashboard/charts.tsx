// Dashboard figures: stat tiles, the weekly-goal ring and the 28-day
// completions chart. Built to the dataviz rules: one series, no legend (the
// title names it), 4px rounded data-ends on a square baseline, bars capped at
// 24px with a 2px surface gap, recessive hairline grid, a per-bar hover/focus
// tooltip, a table view for screen readers, and text in ink tokens only.

import clsx from "clsx";
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { DayCount } from "@/api/types";
import { barPath, niceMax } from "@/lib/chart";
import { formatCalendarDate, weekdayOf } from "@/lib/format";

export function StatTile({
  label,
  value,
  icon,
  sub,
  delta,
  tone,
}: {
  label: string;
  value: ReactNode;
  icon?: ReactNode;
  sub?: ReactNode;
  delta?: { value: number; label: string; upIsGood?: boolean } | null;
  tone?: "critical" | "warning";
}) {
  const good = delta ? (delta.upIsGood ?? true) === delta.value >= 0 : false;
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-line bg-surface p-3.5">
      <div className="flex items-center gap-1.5 text-[12px] text-fg-2">
        {icon ? (
          <span
            className={clsx(
              "[&>svg]:size-3.5",
              tone === "critical" ? "text-critical" : tone === "warning" ? "text-warning" : "text-fg-3",
            )}
          >
            {icon}
          </span>
        ) : null}
        {label}
      </div>
      <div className="text-[26px] leading-tight font-semibold tracking-tight">{value}</div>
      <div className="flex flex-wrap items-center gap-x-2 text-[11.5px] text-fg-3">
        {delta && delta.value !== 0 ? (
          <span className={clsx("font-medium", good ? "text-good-ink" : "text-critical-ink")}>
            {delta.value > 0 ? "▲" : "▼"} {Math.abs(delta.value)} {delta.label}
          </span>
        ) : delta ? (
          <span>= {delta.label}</span>
        ) : null}
        {sub}
      </div>
    </div>
  );
}

/** Weekly goal as a ring meter: accent fill on a lighter step of the same ramp. */
export function GoalRing({ done, goal, size = 64 }: { done: number; goal: number; size?: number }) {
  const stroke = 7;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const frac = goal > 0 ? Math.min(1, done / goal) : 0;
  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="meter"
      aria-label="Weekly goal"
      aria-valuemin={0}
      aria-valuemax={goal}
      aria-valuenow={done}
      className="shrink-0"
    >
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--track)" strokeWidth={stroke} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke="var(--series-1)"
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={`${c * frac} ${c}`}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ transition: "stroke-dasharray 500ms ease" }}
      />
      <text
        x="50%"
        y="50%"
        dominantBaseline="central"
        textAnchor="middle"
        className="fill-fg text-[13px] font-semibold"
      >
        {Math.round(frac * 100)}%
      </text>
    </svg>
  );
}

function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([entry]) => setWidth(entry.contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

export function CompletionsChart({ days, today }: { days: DayCount[]; today: string }) {
  const [ref, width] = useWidth<HTMLDivElement>();
  const [active, setActive] = useState<number | null>(null);
  const height = 132;
  const pad = { top: 16, right: 4, bottom: 20, left: 22 };
  const innerW = Math.max(0, width - pad.left - pad.right);
  const innerH = height - pad.top - pad.bottom;
  const max = Math.max(0, ...days.map((d) => d.done));
  const { top, step } = niceMax(max);
  const slot = days.length ? innerW / days.length : 0;
  const barW = Math.max(2, Math.min(24, slot - 2));
  const y = (v: number) => pad.top + innerH - (v / top) * innerH;
  const maxIndex = max > 0 ? days.findIndex((d) => d.done === max) : -1;
  const ticks = Array.from({ length: Math.floor(top / step) + 1 }, (_, i) => i * step);
  const shown = active !== null ? days[active] : null;
  const total = days.reduce((a, d) => a + d.done, 0);

  return (
    <figure className="m-0">
      <figcaption className="sr-only">Tasks completed per day, last {days.length} days</figcaption>
      <div
        ref={ref}
        className="relative outline-none"
        tabIndex={0}
        aria-label={`Completions chart: ${total} tasks done in ${days.length} days. Use arrow keys to read each day.`}
        onKeyDown={(e) => {
          if (e.key === "ArrowRight") setActive((a) => Math.min(days.length - 1, (a ?? days.length - 1) + (a === null ? 0 : 1)));
          else if (e.key === "ArrowLeft") setActive((a) => Math.max(0, (a ?? days.length - 1) - 1));
          else if (e.key === "Escape") setActive(null);
        }}
        onBlur={() => setActive(null)}
        onPointerLeave={() => setActive(null)}
      >
        {width > 0 ? (
          <svg width={width} height={height} aria-hidden className="block">
            {ticks.map((t) => (
              <g key={t}>
                <line
                  x1={pad.left}
                  x2={width - pad.right}
                  y1={y(t)}
                  y2={y(t)}
                  stroke={t === 0 ? "var(--axis)" : "var(--grid)"}
                  strokeWidth={1}
                  shapeRendering="crispEdges"
                />
                <text x={pad.left - 6} y={y(t)} dominantBaseline="central" textAnchor="end" className="tabular fill-fg-3 text-[10px]">
                  {t}
                </text>
              </g>
            ))}
            {days.map((d, i) => {
              const x = pad.left + i * slot + (slot - barW) / 2;
              const h = (d.done / top) * innerH;
              const isToday = d.date === today;
              return (
                <g key={d.date}>
                  {d.done > 0 ? (
                    <path
                      d={barPath(x, y(d.done), barW, h)}
                      fill="var(--series-1)"
                      opacity={active === null || active === i ? 1 : 0.55}
                    />
                  ) : null}
                  {i === maxIndex && active === null ? (
                    <text x={x + barW / 2} y={y(d.done) - 4} textAnchor="middle" className="tabular fill-fg-2 text-[10px] font-medium">
                      {d.done}
                    </text>
                  ) : null}
                  {(days.length - 1 - i) % 7 === 0 ? (
                    // The last label hugs the right edge instead of centring
                    // under its bar, where "Today" would be clipped.
                    <text
                      x={i === days.length - 1 ? x + barW : x + barW / 2}
                      y={height - 5}
                      textAnchor={i === days.length - 1 ? "end" : "middle"}
                      className="fill-fg-3 text-[10px]"
                    >
                      {isToday ? "Today" : formatCalendarDate(d.date, today)}
                    </text>
                  ) : null}
                  {/* Hit target: the whole slot, taller than the bar. */}
                  <rect
                    x={pad.left + i * slot}
                    y={pad.top}
                    width={slot}
                    height={innerH}
                    fill="transparent"
                    onPointerEnter={() => setActive(i)}
                  />
                </g>
              );
            })}
          </svg>
        ) : (
          <div style={{ height }} />
        )}
        {shown && active !== null && width > 0 ? (
          <div
            role="status"
            className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 rounded-md border border-line-strong bg-surface px-2 py-1 text-[11.5px] whitespace-nowrap shadow-pop"
            style={{
              left: Math.min(Math.max(pad.left + active * slot + slot / 2, 60), width - 60),
            }}
          >
            <span className="font-semibold text-fg">{shown.done} done</span>
            <span className="text-fg-3"> · {shown.created} created</span>
            <div className="text-fg-3">
              {weekdayOf(shown.date)} {formatCalendarDate(shown.date, today)}
            </div>
          </div>
        ) : null}
      </div>
      <table className="sr-only">
        <caption>Tasks per day</caption>
        <thead>
          <tr>
            <th>Date</th>
            <th>Done</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {days.map((d) => (
            <tr key={d.date}>
              <td>{d.date}</td>
              <td>{d.done}</td>
              <td>{d.created}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}
