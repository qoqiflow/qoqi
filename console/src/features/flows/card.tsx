import { Link } from 'react-router-dom';
import { ArrowRight, Play, Workflow } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { StatusDot, statusStyle } from '@/components/status';
import { useLaunch } from '@/features/instances/launch';
import type { Counts } from '@/lib/queries';
import { statuses, type FlowInfo } from '@/lib/types';

/** A flow, and how many of its instances are in each status. */
export function FlowCard({ flow, counts }: { flow: FlowInfo; counts: Counts }) {
  const launch = useLaunch();
  const total = statuses.reduce((n, s) => n + counts[s], 0);
  const active = counts.waiting + counts.running + counts.stopped;
  return (
    <article className="flex flex-col gap-4 rounded-xl border bg-card p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="grid gap-1">
          <h2 className="flex items-center gap-2 font-semibold">
            <Workflow className="size-4 text-primary" />
            <Link to={`/flows/${encodeURIComponent(flow.name)}`} className="hover:underline">
              {flow.name}
            </Link>
          </h2>
          <span className="font-mono text-xs text-muted-foreground">Flow&lt;{flow.returns.text}&gt;</span>
        </div>
        <Button size="sm" variant="outline" onClick={() => launch({ flow: flow.name })}>
          <Play /> Start
        </Button>
      </div>
      {flow.doc && <p className="line-clamp-3 text-sm text-muted-foreground">{flow.doc}</p>}
      <div className="mt-auto grid gap-2">
        <div className="flex h-2 overflow-hidden rounded-full bg-muted" role="img" aria-label={statuses.map((s) => `${counts[s]} ${s}`).join(', ')}>
          {total > 0 && statuses.map((s) => counts[s] > 0 && <span key={s} className={statusStyle[s].dot} style={{ width: `${(counts[s] / total) * 100}%` }} />)}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
          <span className="flex flex-wrap gap-3 text-muted-foreground">
            {statuses
              .filter((s) => counts[s] > 0)
              .map((s) => (
                <Link key={s} to={`/instances?flow=${flow.name}&status=${s}`} className="inline-flex items-center gap-1.5 hover:text-foreground">
                  <StatusDot status={s} />
                  <span className="tabular-nums">{counts[s]}</span> {s}
                </Link>
              ))}
            {total === 0 && 'no instance yet'}
          </span>
          {total > 0 && (
            <Link to={`/instances?flow=${flow.name}`} className="inline-flex items-center gap-1 font-medium text-primary hover:underline">
              {active > 0 ? `${active} alive` : 'All'} <ArrowRight className="size-3.5" />
            </Link>
          )}
        </div>
      </div>
    </article>
  );
}

