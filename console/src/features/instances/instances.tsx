import { useMemo } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { Play, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useLaunch } from '@/features/instances/launch';
import { StatusBadge, StatusDot } from '@/components/status';
import { doing } from '@/lib/describe';
import { emptyCounts, useInstances, useProgram } from '@/lib/queries';
import { useNow } from '@/lib/time';
import { statuses, type Status } from '@/lib/types';
import { cn } from '@/lib/utils';

// Every instance, those that need a look first: stopped, running, asleep,
// then those that ended. Filtered by flow, by status and by name, in the
// address, so that a view can be sent to someone.

const order: Record<Status, number> = { stopped: 0, running: 1, waiting: 2, done: 3, failed: 4 };

export function InstancesPage() {
  const [params] = useSearchParams();
  const flow = params.get('flow') ?? '';
  const launch = useLaunch();
  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Instances</h1>
          <p className="text-sm text-muted-foreground">Each is one process of a flow: what it is doing now is read from what the store keeps of it.</p>
        </div>
        <Button variant="outline" onClick={() => launch(flow ? { flow } : undefined)}>
          <Play /> Start {flow || 'a flow'}
        </Button>
      </div>
      <InstanceList />
    </div>
  );
}

/** The instances, of every flow or of one, filtered as the address says. */
export function InstanceList({ only }: { only?: string }) {
  const [params, setParams] = useSearchParams();
  const flow = only ?? params.get('flow') ?? '';
  const status = (params.get('status') ?? '') as Status | '';
  const q = params.get('q') ?? '';
  const instances = useInstances();
  const program = useProgram();
  const navigate = useNavigate();
  const now = useNow();

  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  };

  const ofFlow = useMemo(() => (instances.data ?? []).filter((v) => !flow || v.flow === flow), [instances.data, flow]);
  const counts = useMemo(() => {
    const c = emptyCounts();
    ofFlow.forEach((v) => c[v.status]++);
    return c;
  }, [ofFlow]);
  const shown = useMemo(() => {
    const text = q.trim().toLowerCase();
    return ofFlow
      .filter((v) => !status || v.status === status)
      .filter((v) => !text || v.id.toLowerCase().includes(text) || JSON.stringify(v.fields ?? '').toLowerCase().includes(text))
      .sort((a, b) => order[a.status] - order[b.status] || b.id.localeCompare(a.id, undefined, { numeric: true }));
  }, [ofFlow, status, q]);

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex flex-wrap gap-1 rounded-lg border bg-card p-1" role="tablist" aria-label="Status">
          <StatusTab active={!status} onClick={() => set('status', '')} label="all" count={ofFlow.length} />
          {statuses.map((s) => (
            <StatusTab key={s} active={status === s} onClick={() => set('status', s)} label={s} count={counts[s]} status={s} />
          ))}
        </div>
        {!only && <Select value={flow || '*'} onValueChange={(v) => set('flow', v === '*' ? '' : v)}>
          <SelectTrigger className="w-44">
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
        </Select>}
        <div className="relative min-w-56 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-9" placeholder="A name, or a value of its fields" value={q} onChange={(e) => set('q', e.target.value)} aria-label="Filter" />
        </div>
      </div>

      <div className="overflow-hidden rounded-xl border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Name</TableHead>
              <TableHead>Flow</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Stage</TableHead>
              <TableHead className="w-full">Now</TableHead>
              <TableHead className="text-right">Steps</TableHead>
              <TableHead className="pr-4">Parent</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {instances.isLoading &&
              [0, 1, 2].map((i) => (
                <TableRow key={i}>
                  <TableCell colSpan={7} className="px-4">
                    <Skeleton className="h-5" />
                  </TableCell>
                </TableRow>
              ))}
            {shown.map((v) => {
              const d = doing(v, now);
              const steps = Object.values(v.steps ?? {}).reduce((n, c) => n + c, 0);
              return (
                <TableRow
                  key={v.id}
                  className="cursor-pointer"
                  onClick={(e) => {
                    if (!(e.target as HTMLElement).closest('a')) navigate(`/instances/${encodeURIComponent(v.id)}`);
                  }}
                >
                  <TableCell className="pl-4">
                    <Link to={`/instances/${encodeURIComponent(v.id)}`} className="font-mono font-medium hover:underline">
                      {v.id}
                    </Link>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{v.flow}</TableCell>
                  <TableCell>
                    <StatusBadge status={v.status} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    {v.stage && (
                      <span className="font-mono text-xs">
                        {v.stage}
                        {v.replaying && <span className="ml-1.5 font-sans text-warning">replaying</span>}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="max-w-0">
                    <span className={cn('block truncate text-sm', d.tone === 'stop' && 'text-destructive', d.tone === 'wait' && 'text-muted-foreground')} title={d.text}>
                      {d.text}
                    </span>
                  </TableCell>
                  <TableCell className="text-right tabular-nums text-muted-foreground">{steps || ''}</TableCell>
                  <TableCell className="pr-4">
                    {v.parent && (
                      <Link to={`/instances/${encodeURIComponent(v.parent)}`} className="font-mono text-xs text-muted-foreground hover:text-foreground">
                        {v.parent}
                      </Link>
                    )}
                  </TableCell>
                </TableRow>
              );
            })}
            {!instances.isLoading && shown.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="px-4 py-10 text-center text-sm text-muted-foreground">
                  {ofFlow.length === 0 ? 'No instance yet.' : 'No instance is like that.'}
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function StatusTab({ active, onClick, label, count, status }: { active: boolean; onClick: () => void; label: string; count: number; status?: Status }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors',
        active ? 'bg-accent font-medium text-foreground' : 'text-muted-foreground hover:text-foreground',
      )}
    >
      {status && <StatusDot status={status} />}
      {label}
      <span className="tabular-nums text-xs text-muted-foreground">{count}</span>
    </button>
  );
}
