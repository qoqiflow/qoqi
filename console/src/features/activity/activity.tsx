import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { BarChart3, Footprints } from 'lucide-react';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useAuth } from '@/auth/auth';
import { useProgram, useStats } from '@/lib/queries';
import type { DayStats, Stats } from '@/lib/types';
import { cn } from '@/lib/utils';
import { ContributionGrid, HourGrid, Legend, longDay, OutcomeBars, OutcomeLegend, StatTile, took } from './charts';

// What happened, over a year: a square a day, darker the more instances
// started, ended or failed that day, or the more steps left; how they
// ended, day by day; when in the week the flows are busy; each flow, and
// the steps that leave the most.

type Measure = 'started' | 'done' | 'failed' | 'steps';

const measures: Record<Measure, { label: string; one: string; many: string }> = {
  started: { label: 'Instances started', one: 'instance started', many: 'instances started' },
  done: { label: 'Instances done', one: 'instance done', many: 'instances done' },
  failed: { label: 'Instances failed', one: 'instance failed', many: 'instances failed' },
  steps: { label: 'Steps run', one: 'step run', many: 'steps run' },
};

const spans = [
  { days: 30, label: 'Last 30 days' },
  { days: 90, label: 'Last 90 days' },
  { days: 365, label: 'Last year' },
];

export function ActivityPage() {
  const [params, setParams] = useSearchParams();
  const flow = params.get('flow') ?? '';
  const program = useProgram();
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  };
  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Activity</h1>
          <p className="text-sm text-muted-foreground">What the flows did, day by day: kept by the daemon as it happens.</p>
        </div>
        <Select value={flow || '*'} onValueChange={(v) => set('flow', v === '*' ? '' : v)}>
          <SelectTrigger className="w-48" aria-label="Flow">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="*">Every flow</SelectItem>
            {program.data?.flows.map((f) => (
              <SelectItem key={f.name} value={f.name}>
                {f.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <ActivityOf flow={flow} />
    </div>
  );
}

/** The activity of a flow, or of all: on its own page, and on a flow's. */
export function ActivityOf({ flow = '' }: { flow?: string }) {
  const [measure, setMeasure] = useState<Measure>('started');
  const [span, setSpan] = useState(30);
  const [picked, setPicked] = useState<string | null>(null);
  const year = useStats(365, flow);
  const { prefs } = useAuth();

  if (year.isLoading) return <Skeleton className="h-96 rounded-xl" />;
  if (year.isError || !year.data) {
    return (
      <p className="rounded-xl border bg-card p-6 text-sm text-muted-foreground">
        This daemon keeps nothing of what happened: it was started without the console's tables (qoqi serve keeps them in its store).
      </p>
    );
  }
  const stats = year.data;
  const m = measures[measure];
  const value = (d: DayStats) => d[measure];
  const total = stats.days.reduce((n, d) => n + value(d), 0);
  const recent = stats.days.slice(-span);

  return (
    <div className="grid gap-6">
      <section className="grid gap-4 rounded-xl border bg-card p-5" aria-label="A year, day by day">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-sm font-semibold">
            <span className="tabular-nums">{total.toLocaleString()}</span> {total === 1 ? m.one : m.many} in the last year
          </h2>
          <div className="flex flex-wrap gap-1 rounded-lg border p-1" role="tablist" aria-label="What is counted">
            {(Object.keys(measures) as Measure[]).map((k) => (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={measure === k}
                onClick={() => setMeasure(k)}
                className={cn('rounded-md px-2.5 py-1 text-xs transition-colors', measure === k ? 'bg-accent font-medium' : 'text-muted-foreground hover:text-foreground')}
              >
                {measures[k].label}
              </button>
            ))}
          </div>
        </div>
        <ContributionGrid
          days={stats.days}
          value={value}
          what={(n) => `${n.toLocaleString()} ${n === 1 ? m.one : m.many}`}
          weekStart={prefs.weekStart ?? 0}
          picked={picked}
          onPick={setPicked}
        />
        <div className="flex flex-wrap items-center justify-between gap-3">
          <span className="text-xs text-muted-foreground">
            {picked ? <PickedDay stats={stats} day={picked} /> : 'A square is a day: pick one to see it.'}
          </span>
          <Legend />
        </div>
      </section>

      <Summary days={recent} span={span} setSpan={setSpan} />

      <div className="grid gap-6 xl:grid-cols-2">
        <section className="grid content-start gap-4 rounded-xl border bg-card p-5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">How they ended</h2>
            <OutcomeLegend />
          </div>
          <OutcomeBars days={recent} />
        </section>
        <section className="grid content-start gap-4 rounded-xl border bg-card p-5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">When they start</h2>
            <Legend />
          </div>
          <HourGrid hours={stats.hours} weekStart={prefs.weekStart ?? 0} />
          <p className="text-xs text-muted-foreground">Over the last year, by the hour where you are.</p>
        </section>
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        {!flow && <FlowTable stats={stats} />}
        <TopSteps stats={stats} />
      </div>
    </div>
  );
}

function PickedDay({ stats, day }: { stats: Stats; day: string }) {
  const d = stats.days.find((x) => x.day === day);
  if (!d) return null;
  return (
    <span className="text-foreground">
      <span className="font-semibold">{longDay(day)}</span>: {d.started} started · {d.done} done · {d.failed} failed · {d.stopped} stopped ·{' '}
      {d.steps.toLocaleString()} steps
    </span>
  );
}

function Summary({ days, span, setSpan }: { days: DayStats[]; span: number; setSpan: (n: number) => void }) {
  const sum = (k: keyof Omit<DayStats, 'day'>) => days.reduce((n, d) => n + d[k], 0);
  const ended = sum('done') + sum('failed');
  const failRate = ended > 0 ? sum('failed') / ended : 0;
  return (
    <section className="grid gap-3" aria-label="The span">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold text-muted-foreground">{spans.find((s) => s.days === span)?.label}</h2>
        <div className="flex gap-1 rounded-lg border bg-card p-1" role="tablist" aria-label="Span">
          {spans.map((s) => (
            <button
              key={s.days}
              type="button"
              role="tab"
              aria-selected={span === s.days}
              onClick={() => setSpan(s.days)}
              className={cn('rounded-md px-2.5 py-1 text-xs transition-colors', span === s.days ? 'bg-accent font-medium' : 'text-muted-foreground hover:text-foreground')}
            >
              {s.label}
            </button>
          ))}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <StatTile label="Started" value={sum('started').toLocaleString()} hint={`${(sum('started') / Math.max(1, days.length)).toFixed(1)} a day`} />
        <StatTile label="Done" value={sum('done').toLocaleString()} />
        <StatTile label="Failed" value={sum('failed').toLocaleString()} hint={ended > 0 ? `${(failRate * 100).toFixed(1)}% of those that ended` : 'none ended'} tone={failRate > 0.05 ? 'bad' : undefined} />
        <StatTile label="Stopped" value={sum('stopped').toLocaleString()} hint="a person decided" />
        <StatTile label="Steps run" value={sum('steps').toLocaleString()} hint={`${Math.round(sum('steps') / Math.max(1, days.length)).toLocaleString()} a day`} />
      </div>
    </section>
  );
}

function FlowTable({ stats }: { stats: Stats }) {
  return (
    <section className="overflow-hidden rounded-xl border bg-card">
      <h2 className="flex items-center gap-2 border-b px-4 py-3 text-sm font-semibold">
        <BarChart3 className="size-4 text-primary" /> By flow, over the year
      </h2>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="pl-4">Flow</TableHead>
            <TableHead className="text-right">Started</TableHead>
            <TableHead className="text-right">Done</TableHead>
            <TableHead className="text-right">Failed</TableHead>
            <TableHead className="text-right">Median</TableHead>
            <TableHead className="pr-4 text-right">95th</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {stats.flows.map((f) => (
            <TableRow key={f.flow}>
              <TableCell className="pl-4">
                <Link to={`/flows/${encodeURIComponent(f.flow)}/activity`} className="font-medium hover:underline">
                  {f.flow}
                </Link>
                <span className="ml-2 text-xs text-muted-foreground">{f.program}</span>
              </TableCell>
              <TableCell className="text-right tabular-nums">{f.started.toLocaleString()}</TableCell>
              <TableCell className="text-right tabular-nums">{f.done.toLocaleString()}</TableCell>
              <TableCell className={cn('text-right tabular-nums', f.failed > 0 && 'text-destructive')}>{f.failed.toLocaleString()}</TableCell>
              <TableCell className="text-right tabular-nums">{took(f.p50)}</TableCell>
              <TableCell className="pr-4 text-right tabular-nums">{took(f.p95)}</TableCell>
            </TableRow>
          ))}
          {stats.flows.length === 0 && (
            <TableRow>
              <TableCell colSpan={6} className="px-4 py-8 text-center text-sm text-muted-foreground">
                Nothing happened yet.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </section>
  );
}

function TopSteps({ stats }: { stats: Stats }) {
  const top = stats.steps.slice(0, 10);
  const max = Math.max(1, ...top.map((s) => s.runs));
  return (
    <section className="rounded-xl border bg-card">
      <h2 className="flex items-center gap-2 border-b px-4 py-3 text-sm font-semibold">
        <Footprints className="size-4 text-primary" /> The steps that run the most
      </h2>
      {top.length === 0 ? (
        <p className="px-4 py-6 text-sm text-muted-foreground">No step ran yet.</p>
      ) : (
        <ol className="grid gap-3 p-4">
          {top.map((s) => (
            <li key={s.flow + s.step} className="grid gap-1">
              <span className="flex items-baseline justify-between gap-3 text-sm">
                <span className="truncate font-mono">
                  {s.step} <span className="font-sans text-xs text-muted-foreground">{s.flow}</span>
                </span>
                <span className="tabular-nums text-muted-foreground">{s.runs.toLocaleString()}</span>
              </span>
              <span className="h-1.5 overflow-hidden rounded-full bg-muted">
                <span className="block h-full rounded-full bg-primary" style={{ width: `${(s.runs / max) * 100}%` }} />
              </span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
