import { useMemo, useState, type ReactNode } from 'react';
import type { DayStats } from '@/lib/types';
import { cn } from '@/lib/utils';

// The charts of the pages of activity. Magnitude is one hue, the
// primary, from light to dark, over an empty step of the surface; every
// mark says its number when hovered; nothing is told by color alone.

/** The steps of the one hue, the first for nothing at all. */
const levels = [
  'bg-muted',
  'bg-[color-mix(in_oklch,var(--primary)_22%,var(--card))]',
  'bg-[color-mix(in_oklch,var(--primary)_45%,var(--card))]',
  'bg-[color-mix(in_oklch,var(--primary)_70%,var(--card))]',
  'bg-primary',
];

/** The step of a count, among counts up to max: by quarters of the busiest. */
function level(n: number, max: number): number {
  if (n <= 0 || max <= 0) return 0;
  return Math.min(4, Math.max(1, Math.ceil((n / max) * 4)));
}

export function Legend() {
  return (
    <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
      Less
      {levels.map((c) => (
        <span key={c} className={cn('size-2.5 rounded-[3px]', c)} />
      ))}
      More
    </span>
  );
}

/** A tooltip that follows the mark hovered. */
function useTip() {
  const [tip, setTip] = useState<{ x: number; y: number; text: ReactNode } | null>(null);
  const show = (e: React.MouseEvent | React.FocusEvent, text: ReactNode) => {
    const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setTip({ x: box.left + box.width / 2, y: box.top, text });
  };
  const node = tip && (
    <div
      role="tooltip"
      className="pointer-events-none fixed z-50 -translate-x-1/2 -translate-y-full rounded-md border bg-popover px-2 py-1 text-xs whitespace-nowrap text-popover-foreground shadow-md"
      style={{ left: tip.x, top: tip.y - 6 }}
    >
      {tip.text}
    </div>
  );
  return { show, hide: () => setTip(null), node };
}

const monthNames = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const dayNames = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

function dateOf(day: string): Date {
  const [y, m, d] = day.split('-').map(Number);
  return new Date(y, m - 1, d);
}

export function longDay(day: string): string {
  return dateOf(day).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' });
}

/**
 * The days as GitHub shows contributions: a column a week, a row a day of
 * the week, each cell darker the more there was.
 */
export function ContributionGrid({
  days,
  value,
  what,
  weekStart = 0,
  onPick,
  picked,
}: {
  days: DayStats[];
  value: (d: DayStats) => number;
  what: (n: number) => string;
  weekStart?: 0 | 1;
  onPick?: (day: string | null) => void;
  picked?: string | null;
}) {
  const tip = useTip();
  const { weeks, months, max } = useMemo(() => {
    const weeks: (DayStats | null)[][] = [];
    let week: (DayStats | null)[] = [];
    if (days.length > 0) {
      const lead = (dateOf(days[0].day).getDay() - weekStart + 7) % 7;
      for (let i = 0; i < lead; i++) week.push(null);
    }
    let max = 0;
    for (const d of days) {
      max = Math.max(max, value(d));
      week.push(d);
      if (week.length === 7) {
        weeks.push(week);
        week = [];
      }
    }
    if (week.length) weeks.push(week);
    // A month is named over the first week it starts in
    const months: { at: number; name: string }[] = [];
    let last = -1;
    weeks.forEach((w, i) => {
      const first = w.find((d) => d !== null);
      if (!first) return;
      const m = dateOf(first.day).getMonth();
      if (m !== last) {
        if (months.length === 0 || i - months[months.length - 1].at >= 3) months.push({ at: i, name: monthNames[m] });
        last = m;
      }
    });
    return { weeks, months, max };
  }, [days, value, weekStart]);

  const cell = 11;
  const gap = 3;
  return (
    <div className="overflow-x-auto pb-1">
      <div className="inline-grid gap-1" style={{ gridTemplateColumns: 'auto 1fr' }}>
        <span />
        <div className="relative h-4 text-xs text-muted-foreground" style={{ width: weeks.length * (cell + gap) }}>
          {months.map((m) => (
            <span key={m.at} className="absolute" style={{ left: m.at * (cell + gap) }}>
              {m.name}
            </span>
          ))}
        </div>
        <div className="grid pr-1 text-[0.6875rem] leading-none text-muted-foreground" style={{ gridTemplateRows: `repeat(7, ${cell}px)`, rowGap: gap }}>
          {[0, 1, 2, 3, 4, 5, 6].map((r) => (
            <span key={r} className="flex items-center">
              {r % 2 === 1 ? dayNames[(r + weekStart) % 7] : ''}
            </span>
          ))}
        </div>
        <div className="flex" style={{ gap }} role="grid" aria-label="Activity by day">
          {weeks.map((w, i) => (
            <div key={i} className="grid" style={{ gridTemplateRows: `repeat(7, ${cell}px)`, rowGap: gap }} role="row">
              {w.map((d, j) =>
                d === null ? (
                  <span key={j} />
                ) : (
                  <button
                    key={d.day}
                    type="button"
                    role="gridcell"
                    aria-label={`${longDay(d.day)}: ${what(value(d))}`}
                    className={cn(
                      'rounded-[3px] outline-offset-1 transition-shadow hover:ring-2 hover:ring-foreground/30 focus-visible:outline-2 focus-visible:outline-ring',
                      levels[level(value(d), max)],
                      picked === d.day && 'ring-2 ring-foreground',
                    )}
                    style={{ width: cell, height: cell }}
                    onMouseEnter={(e) => tip.show(e, <><span className="font-semibold">{what(value(d))}</span> · {longDay(d.day)}</>)}
                    onFocus={(e) => tip.show(e, <><span className="font-semibold">{what(value(d))}</span> · {longDay(d.day)}</>)}
                    onMouseLeave={tip.hide}
                    onBlur={tip.hide}
                    onClick={() => onPick?.(picked === d.day ? null : d.day)}
                  />
                ),
              )}
            </div>
          ))}
        </div>
      </div>
      {tip.node}
    </div>
  );
}

/** The starts by day of the week and hour: when the flows are busy. */
export function HourGrid({ hours, weekStart = 0 }: { hours: number[][]; weekStart?: 0 | 1 }) {
  const tip = useTip();
  const max = Math.max(0, ...hours.flat());
  const rows = [0, 1, 2, 3, 4, 5, 6].map((r) => (r + weekStart) % 7);
  return (
    <div className="overflow-x-auto">
      <div className="inline-grid min-w-full gap-[3px] text-[0.6875rem] text-muted-foreground" style={{ gridTemplateColumns: 'auto repeat(24, minmax(14px, 1fr))' }}>
        <span />
        {Array.from({ length: 24 }, (_, h) => (
          <span key={h} className="text-center">
            {h % 6 === 0 ? `${h}h` : ''}
          </span>
        ))}
        {rows.map((day) => (
          <div key={day} className="contents">
            <span className="pr-1.5 leading-[14px]">{dayNames[day]}</span>
            {(hours[day] ?? []).map((n, h) => (
              <span
                key={h}
                tabIndex={0}
                aria-label={`${dayNames[day]} ${h}:00: ${n} started`}
                className={cn('h-3.5 rounded-[3px]', levels[level(n, max)])}
                onMouseEnter={(e) => tip.show(e, `${n} started · ${dayNames[day]} ${h}:00–${h + 1}:00`)}
                onFocus={(e) => tip.show(e, `${n} started · ${dayNames[day]} ${h}:00–${h + 1}:00`)}
                onMouseLeave={tip.hide}
                onBlur={tip.hide}
              />
            ))}
          </div>
        ))}
      </div>
      {tip.node}
    </div>
  );
}

const outcomeStyle = {
  done: { bar: 'bg-success', label: 'done' },
  failed: { bar: 'bg-destructive', label: 'failed' },
  stopped: { bar: 'bg-warning', label: 'stopped' },
} as const;

export type Outcome = keyof typeof outcomeStyle;

export function OutcomeLegend() {
  return (
    <span className="inline-flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
      {(Object.keys(outcomeStyle) as Outcome[]).map((k) => (
        <span key={k} className="inline-flex items-center gap-1.5">
          <span className={cn('size-2.5 rounded-[3px]', outcomeStyle[k].bar)} />
          {outcomeStyle[k].label}
        </span>
      ))}
    </span>
  );
}

/** How the instances ended, day by day: a stacked bar a day, done at the base. */
export function OutcomeBars({ days }: { days: DayStats[] }) {
  const tip = useTip();
  const max = Math.max(1, ...days.map((d) => d.done + d.failed + d.stopped));
  const keys: Outcome[] = ['done', 'failed', 'stopped'];
  return (
    <div>
      <div className="flex h-36 items-end gap-[2px] border-b" role="img" aria-label="Outcomes by day">
        {days.map((d) => {
          const total = d.done + d.failed + d.stopped;
          return (
            <div
              key={d.day}
              className="flex h-full min-w-0 flex-1 cursor-default flex-col justify-end gap-[2px] rounded-t-[4px] hover:bg-accent/60"
              onMouseEnter={(e) =>
                tip.show(
                  e,
                  <span className="grid gap-0.5">
                    <span className="font-semibold">{longDay(d.day)}</span>
                    {keys.map((k) => (
                      <span key={k} className="flex items-center gap-1.5">
                        <span className={cn('size-2 rounded-[2px]', outcomeStyle[k].bar)} />
                        {d[k]} {k}
                      </span>
                    ))}
                  </span>,
                )
              }
              onMouseLeave={tip.hide}
            >
              {total > 0 && (
                <span className="flex w-full flex-col-reverse gap-[2px] overflow-hidden rounded-t-[4px]" style={{ height: `${(total / max) * 100}%` }}>
                  {keys.map((k) => d[k] > 0 && <span key={k} className={cn('w-full', outcomeStyle[k].bar)} style={{ flexGrow: d[k], flexBasis: 0, minHeight: 2 }} />)}
                </span>
              )}
            </div>
          );
        })}
      </div>
      <div className="mt-1.5 flex justify-between text-xs text-muted-foreground">
        <span>{days[0] && dateOf(days[0].day).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}</span>
        <span>{days.length > 0 && dateOf(days[days.length - 1].day).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}</span>
      </div>
      {tip.node}
    </div>
  );
}

/** A number that stands alone, with what it is. */
export function StatTile({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: 'bad' }) {
  return (
    <div className={cn('grid gap-1 rounded-xl border bg-card p-4', tone === 'bad' && 'border-destructive/40 bg-destructive/5')}>
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={cn('text-3xl font-semibold tracking-tight tabular-nums', tone === 'bad' && 'text-destructive')}>{value}</span>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </div>
  );
}

/** A length of time, short: 340ms, 4.2s, 12m, 3h, 2d. */
export function took(ms: number): string {
  if (ms <= 0) return '—';
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)}s`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)}m`;
  if (ms < 86_400_000) return `${(ms / 3_600_000).toFixed(1).replace(/\.0$/, '')}h`;
  return `${(ms / 86_400_000).toFixed(1).replace(/\.0$/, '')}d`;
}
