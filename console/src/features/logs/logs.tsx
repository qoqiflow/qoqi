import { useQuery } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { Pause, Play, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { api } from '@/lib/api';
import { useFeed } from '@/lib/live';
import { clock } from '@/lib/time';
import type { Line } from '@/lib/types';

// What the flows print, every instance together, as it is printed: the
// last lines the daemon keeps, then those that come. Filtered by an
// instance or a text, in the address.

export function LogsPage() {
  const [params, setParams] = useSearchParams();
  const q = params.get('q') ?? '';
  const kept = useQuery({ queryKey: ['output'], queryFn: () => api.output(), staleTime: Infinity });
  const feed = useFeed();
  const [follow, setFollow] = useState(true);
  const bottom = useRef<HTMLDivElement>(null);

  // The lines kept, then those printed since they were asked for
  const lines = useMemo(() => {
    const from = kept.data ?? [];
    const last = from.length ? from[from.length - 1].at : '';
    const since: Line[] = feed
      .filter((e) => e.kind === 'output' && e.text !== undefined && e.at > last)
      .map((e) => ({ at: e.at, id: e.id, text: e.text! }))
      .reverse();
    return [...from, ...since];
  }, [kept.data, feed]);

  const shown = useMemo(() => {
    const text = q.trim().toLowerCase();
    if (!text) return lines;
    return lines.filter((l) => l.text.toLowerCase().includes(text) || (l.id ?? '').toLowerCase().includes(text));
  }, [lines, q]);

  useEffect(() => {
    if (follow) bottom.current?.scrollIntoView({ block: 'end' });
  }, [shown.length, follow]);

  return (
    <div className="grid gap-5">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Logs</h1>
          <p className="text-sm text-muted-foreground">What the flows print, as they print it. The daemon keeps all of it in its home, logs/flows.log.</p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative w-64">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-9"
              placeholder="An instance, or a text"
              value={q}
              onChange={(e) => {
                const next = new URLSearchParams(params);
                if (e.target.value) next.set('q', e.target.value);
                else next.delete('q');
                setParams(next, { replace: true });
              }}
              aria-label="Filter"
            />
          </div>
          <Button variant="outline" onClick={() => setFollow((f) => !f)} aria-pressed={follow}>
            {follow ? <Pause /> : <Play />} {follow ? 'Following' : 'Follow'}
          </Button>
        </div>
      </div>
      <div className="max-h-[calc(100svh-14rem)] overflow-y-auto rounded-xl border bg-card p-2 font-mono text-[0.8125rem]">
        {shown.length === 0 ? (
          <p className="p-4 font-sans text-sm text-muted-foreground">{kept.isLoading ? 'Reading…' : 'Nothing printed yet.'}</p>
        ) : (
          <ol>
            {shown.map((l, i) => (
              <li key={i} className="grid grid-cols-[6.5rem_12rem_1fr] gap-3 rounded px-2 py-0.5 hover:bg-accent/50">
                <span className="text-muted-foreground tabular-nums">{clock(l.at)}</span>
                {l.id ? (
                  <Link to={`/instances/${encodeURIComponent(l.id)}`} className="truncate text-primary hover:underline" title={l.id}>
                    {l.id}
                  </Link>
                ) : (
                  <span />
                )}
                <span className="break-words whitespace-pre-wrap">{l.text}</span>
              </li>
            ))}
          </ol>
        )}
        <div ref={bottom} />
      </div>
    </div>
  );
}
