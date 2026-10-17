import { Link } from 'react-router-dom';
import { ArrowRight, CircleAlert, Play, Terminal, Workflow, Zap } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { useLaunch } from '@/components/launch';
import { StatusDot, statusStyle } from '@/components/status';
import { doing } from '@/lib/describe';
import { useFeed } from '@/lib/live';
import { emptyCounts, useCounts, useInstances, useProgram, type Counts } from '@/lib/queries';
import { clock, useNow } from '@/lib/time';
import { statuses, type Event, type FlowInfo } from '@/lib/types';
import { cn } from '@/lib/utils';

export function OverviewPage() {
  const program = useProgram();
  const instances = useInstances();
  const { all, byFlow } = useCounts(instances.data);
  const total = instances.data?.length ?? 0;

  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Flows</h1>
          <p className="text-sm text-muted-foreground">
            {program.data ? (
              <>
                <span className="font-mono">{program.data.entry.split('/').pop()}</span> · {program.data.flows.length} flow
                {program.data.flows.length === 1 ? '' : 's'} · {total} instance{total === 1 ? '' : 's'} in the store
              </>
            ) : (
              'Reading the program…'
            )}
          </p>
        </div>
      </div>

      <section className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5" aria-label="Instances by status">
        {statuses.map((s) => (
          <Link
            key={s}
            to={`/instances?status=${s}`}
            className={cn(
              'group grid gap-1 rounded-xl border bg-card p-4 transition hover:shadow-sm',
              s === 'stopped' && all.stopped > 0 && 'border-destructive/40 bg-destructive/5',
            )}
          >
            <span className="flex items-center gap-2 text-sm text-muted-foreground">
              <StatusDot status={s} pulse={s === 'running' && all.running > 0} />
              {s}
            </span>
            <span className={cn('text-3xl font-semibold tabular-nums tracking-tight', s === 'stopped' && all.stopped > 0 && 'text-destructive')}>
              {instances.isLoading ? <Skeleton className="h-9 w-12" /> : all[s]}
            </span>
            <span className="text-xs text-muted-foreground">{hint(s, all)}</span>
          </Link>
        ))}
      </section>

      {!instances.isLoading && total === 0 && program.data && <FirstStart flows={program.data.flows} />}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <section className="grid content-start gap-4 sm:grid-cols-2" aria-label="The flows of the program">
          {program.isLoading && [0, 1].map((i) => <Skeleton key={i} className="h-44 rounded-xl" />)}
          {program.data?.flows.map((f) => <FlowCard key={f.name} flow={f} counts={byFlow.get(f.name) ?? emptyCounts()} />)}
        </section>
        <aside className="grid content-start gap-6">
          <Stopped />
          <Activity />
        </aside>
      </div>
    </div>
  );
}

function hint(status: string, all: Counts): string {
  switch (status) {
    case 'waiting':
      return 'asleep in the store';
    case 'running':
      return 'taken up now';
    case 'stopped':
      return all.stopped > 0 ? 'a person decides' : 'nothing for a person';
    case 'done':
      return 'run returned';
    default:
      return 'cancelled, undos run';
  }
}

function FirstStart({ flows }: { flows: FlowInfo[] }) {
  const launch = useLaunch();
  return (
    <section className="grid gap-4 rounded-2xl border border-dashed bg-card/60 p-6 sm:grid-cols-[1fr_auto] sm:items-center">
      <div className="grid gap-1.5">
        <h2 className="text-lg font-semibold">No instance yet</h2>
        <p className="max-w-prose text-sm text-muted-foreground">
          Start one: its fields are a form made from their types. It runs here, asleep in the store while it waits, and goes on where it was if the daemon stops.
        </p>
        <p className="font-mono text-xs text-muted-foreground">
          <Terminal className="mr-1 inline size-3.5" />
          or: qoqi start {flows[0]?.name ?? 'Flow'} {flows[0]?.name.toLowerCase() ?? 'flow'}:1 '{'{…}'}'
        </p>
      </div>
      <Button size="lg" onClick={() => launch(flows.length === 1 ? { flow: flows[0].name } : undefined)}>
        <Play /> Start a flow
      </Button>
    </section>
  );
}

function FlowCard({ flow, counts }: { flow: FlowInfo; counts: Counts }) {
  const launch = useLaunch();
  const total = statuses.reduce((n, s) => n + counts[s], 0);
  const active = counts.waiting + counts.running + counts.stopped;
  return (
    <article className="flex flex-col gap-4 rounded-xl border bg-card p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="grid gap-1">
          <h2 className="flex items-center gap-2 font-semibold">
            <Workflow className="size-4 text-primary" />
            {flow.name}
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

function Stopped() {
  const instances = useInstances().data ?? [];
  const now = useNow(5000);
  const stopped = instances.filter((v) => v.status === 'stopped');
  return (
    <section className="rounded-xl border bg-card">
      <h2 className="flex items-center gap-2 border-b px-4 py-3 text-sm font-semibold">
        <CircleAlert className={cn('size-4', stopped.length ? 'text-destructive' : 'text-muted-foreground')} />
        For a person
        <span className="ml-auto text-xs font-normal text-muted-foreground tabular-nums">{stopped.length}</span>
      </h2>
      {stopped.length === 0 ? (
        <p className="px-4 py-5 text-sm text-muted-foreground">Nothing stopped. An instance that meets a mistake, or asks a question, waits here.</p>
      ) : (
        <ul className="divide-y">
          {stopped.slice(0, 8).map((v) => (
            <li key={v.id}>
              <Link to={`/instances/${encodeURIComponent(v.id)}`} className="grid gap-0.5 px-4 py-3 hover:bg-accent/50">
                <span className="flex items-center gap-2 font-mono text-sm">
                  <StatusDot status="stopped" /> {v.id}
                </span>
                <span className="line-clamp-2 pl-4 text-xs text-muted-foreground">{doing(v, now).text}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function eventLine(e: Event): { icon: 'step' | 'out' | 'status'; text: string } {
  if (e.kind === 'step') return { icon: 'step', text: `${e.step} left${e.once ? ', never twice' : ''}` };
  if (e.kind === 'output') return { icon: 'out', text: e.text ?? '' };
  const v = e.instance!;
  return { icon: 'status', text: v.status === 'waiting' && v.wakeAt ? 'asleep' : v.status };
}

export function Activity({ only, limit = 14 }: { only?: string; limit?: number }) {
  const events = useFeed().filter((e) => !only || e.id === only);
  return (
    <section className="rounded-xl border bg-card">
      <h2 className="flex items-center gap-2 border-b px-4 py-3 text-sm font-semibold">
        <Zap className="size-4 text-primary" /> As it happens
      </h2>
      {events.length === 0 ? (
        <p className="px-4 py-5 text-sm text-muted-foreground">Steps that leave, what the program prints, instances that wake: they show here as they happen.</p>
      ) : (
        <ol className="max-h-[28rem] divide-y overflow-y-auto">
          {events.slice(0, limit).map((e, i) => {
            const line = eventLine(e);
            return (
              <li key={`${e.at}-${i}`} className="grid grid-cols-[auto_1fr] gap-x-3 px-4 py-2.5 text-sm">
                <span className="pt-1">
                  {line.icon === 'status' && e.instance ? (
                    <StatusDot status={e.instance.status} />
                  ) : (
                    <span className={cn('block size-2 rounded-sm', line.icon === 'step' ? 'bg-primary' : 'bg-muted-foreground/40')} />
                  )}
                </span>
                <span className="grid min-w-0 gap-0.5">
                  <span className={cn('truncate', line.icon === 'out' && 'font-mono text-[0.8125rem]')}>{line.text}</span>
                  <span className="flex gap-2 text-xs text-muted-foreground">
                    {e.id && !only && (
                      <Link to={`/instances/${encodeURIComponent(e.id)}`} className="truncate font-mono hover:text-foreground">
                        {e.id}
                      </Link>
                    )}
                    <span className="ml-auto shrink-0 tabular-nums">{clock(e.at)}</span>
                  </span>
                </span>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
