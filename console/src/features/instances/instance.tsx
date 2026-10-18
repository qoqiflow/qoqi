import { useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Ban, ChevronRight, CircleAlert, CircleStop, Clock, Copy, CornerDownRight, GitFork, MessageSquareReply, Play, RotateCcw, Undo2 } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { CancelDialog } from '@/features/instances/cancel';
import { CodeView, type Note } from '@/components/code';
import { Inboxes } from '@/features/instances/inbox';
import { useLaunch } from '@/features/instances/launch';
import { StatusBadge, StatusDot } from '@/components/status';
import { AnswerDialog, EndDialog, ResumeDialog } from '@/features/instances/stopped';
import { Fields, Value } from '@/components/value';
import { awaited, doing, isLibrary, shortFile, waitsFor } from '@/lib/describe';
import { givenFields, keyParts, qoqi, outcomeOf, toPlain } from '@/lib/qoqi';
import { useInstance, useInstances, useProgram, useSource } from '@/lib/queries';
import { clock, relative, useNow } from '@/lib/time';
import type { Detail, FlowInfo, Kept, Place, PlaceFrame, Program } from '@/lib/types';
import { cn } from '@/lib/utils';

export function InstancePage() {
  const id = decodeURIComponent(useParams().id ?? '');
  const detail = useInstance(id);
  const program = useProgram();

  if (detail.isLoading) {
    return (
      <div className="grid gap-4">
        <Skeleton className="h-10 w-80" />
        <Skeleton className="h-96 rounded-xl" />
      </div>
    );
  }
  if (!detail.data) {
    return (
      <div className="grid justify-items-center gap-3 py-20 text-center">
        <h1 className="font-mono text-xl">{id}</h1>
        <p className="text-muted-foreground">There is no instance of that name in the store{detail.error ? `: ${detail.error.message}` : ''}.</p>
        <Button asChild variant="outline">
          <Link to="/instances">All instances</Link>
        </Button>
      </div>
    );
  }
  const flow = program.data?.flows.find((f) => f.name === detail.data.flow);
  return <InstanceView detail={detail.data} flow={flow} program={program.data} />;
}

/** The frames of a task that are the program's own, innermost first. */
function ownFrames(place: Place): PlaceFrame[] {
  return (place.frames ?? []).filter((f) => !isLibrary(f.file));
}

function InstanceView({ detail, flow, program }: { detail: Detail; flow?: FlowInfo; program?: Program }) {
  const now = useNow();
  const launch = useLaunch();
  const [cancelling, setCancelling] = useState(false);
  const [resuming, setResuming] = useState(false);
  const [ending, setEnding] = useState(false);
  const [answering, setAnswering] = useState(false);
  const given = givenFields(detail.fields);
  const ended = detail.status === 'done' || detail.status === 'failed';
  const d = doing(detail, now);

  const curl = `curl -H "Authorization: Bearer $(cat ~/.qoqi/token)" ${location.origin}/api/instances/${encodeURIComponent(detail.id)}`;

  return (
    <div className="grid gap-6">
      <nav className="flex items-center gap-1.5 text-sm text-muted-foreground" aria-label="Where">
        <Link to="/instances" className="hover:text-foreground">
          Instances
        </Link>
        <ChevronRight className="size-3.5" />
        <Link to={`/instances?flow=${detail.flow}`} className="hover:text-foreground">
          {detail.flow}
        </Link>
        <ChevronRight className="size-3.5" />
        <span className="font-mono text-foreground">{detail.id}</span>
      </nav>

      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="grid min-w-0 gap-2">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="font-mono text-2xl font-semibold tracking-tight break-all">{detail.id}</h1>
            <StatusBadge status={detail.status} />
          </div>
          <p className={cn('text-[0.9375rem]', d.tone === 'stop' ? 'text-destructive' : 'text-foreground')}>
            {d.text}
            {d.until && <span className="text-muted-foreground"> · at {clock(d.until)}</span>}
          </p>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>
              {detail.flow} <span className="font-mono">Flow&lt;{flow?.returns.text ?? '…'}&gt;</span>
            </span>
            <span aria-hidden="true">·</span>
            <span>
              birth <span className="font-mono">{detail.birth}</span>
            </span>
            {detail.stage && (
              <>
                <span aria-hidden="true">·</span>
                <span>
                  stage <span className="font-mono text-foreground">{detail.stage}</span>, pass {detail.pass}
                </span>
              </>
            )}
            {detail.replaying && (
              <>
                <span aria-hidden="true">·</span>
                <span className="text-warning">resumed, replaying to its next stage</span>
              </>
            )}
            {detail.version && (
              <>
                <span aria-hidden="true">·</span>
                <span title={detail.version}>
                  version <span className="font-mono">{detail.version.replace(/^sha256:/, '').slice(0, 8)}</span>
                </span>
              </>
            )}
            {detail.phase === 'finish' && (
              <>
                <span aria-hidden="true">·</span>
                <span>ending: undos</span>
              </>
            )}
            {detail.parent && (
              <>
                <span aria-hidden="true">·</span>
                <span>
                  child of{' '}
                  <Link className="font-mono text-primary hover:underline" to={`/instances/${encodeURIComponent(detail.parent)}`}>
                    {detail.parent}
                  </Link>
                </span>
              </>
            )}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            onClick={() => launch({ flow: detail.flow, fields: toPlain(given) })}
            title="A new instance, with the same fields"
          >
            <RotateCcw /> Start again
          </Button>
          <Button
            variant="outline"
            size="icon"
            aria-label="Copy as curl"
            title="Copy as curl"
            onClick={() => navigator.clipboard?.writeText(curl).then(() => toast.success('Copied as curl'))}
          >
            <Copy />
          </Button>
          {!ended && (
            <Button variant="destructive" onClick={() => setCancelling(true)}>
              <Ban /> Cancel…
            </Button>
          )}
        </div>
      </header>

      {detail.status === 'stopped' && (
        <div role="alert" className="flex gap-3 rounded-xl border border-destructive/30 bg-destructive/6 p-4">
          <CircleAlert className="mt-0.5 size-5 shrink-0 text-destructive" />
          <div className="grid gap-1">
            <span className="font-medium">Stopped, for a person</span>
            <span className="font-mono text-sm break-words">{detail.stopped}</span>
            <span className="text-sm text-muted-foreground">
              Nothing more happens alone. What waits for it waits on. Once the program is fixed, resume it; if the world was put right by hand, end it.
            </span>
            <div className="mt-2 flex flex-wrap gap-2">
              <Button onClick={() => setResuming(true)}>
                <Play /> Resume…
              </Button>
              <Button variant="outline" onClick={() => setEnding(true)}>
                <CircleStop /> End as it is…
              </Button>
              <Button variant="ghost" onClick={() => setAnswering(true)}>
                <MessageSquareReply /> Answer a step…
              </Button>
            </div>
          </div>
        </div>
      )}

      {ended && <Ended detail={detail} />}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_21rem]">
        <div className="grid min-w-0 content-start gap-6">
          <Where detail={detail} flow={flow} program={program} now={now} />
          <Tabs defaultValue="steps">
            <TabsList>
              <TabsTrigger value="steps">Steps · {detail.kept.length}</TabsTrigger>
              <TabsTrigger value="output">Output · {detail.output.length}</TabsTrigger>
              <TabsTrigger value="kept">What is kept</TabsTrigger>
            </TabsList>
            <TabsContent value="steps">
              <Steps detail={detail} />
            </TabsContent>
            <TabsContent value="output">
              <Output detail={detail} />
            </TabsContent>
            <TabsContent value="kept">
              <pre className="max-h-[32rem] overflow-auto rounded-xl border bg-card p-4 font-mono text-xs leading-5">
                {JSON.stringify({ ...detail, output: undefined }, null, 2)}
              </pre>
            </TabsContent>
          </Tabs>
        </div>
        <aside className="grid content-start gap-4">
          {!ended && <WaitsFor detail={detail} now={now} />}
          {!ended && program && (flow?.inboxes?.length ?? 0) > 0 && <Inboxes instance={detail} inboxes={flow!.inboxes!} program={program} />}
          <Panel title="Fields" note="what it was started with">
            <Fields value={given} />
          </Panel>
          <Undos detail={detail} />
          <Family detail={detail} />
        </aside>
      </div>
      <CancelDialog instance={detail} open={cancelling} onOpenChange={setCancelling} />
      {detail.status === 'stopped' && (
        <>
          <ResumeDialog instance={detail} open={resuming} onOpenChange={setResuming} />
          <EndDialog instance={detail} open={ending} onOpenChange={setEnding} />
          <AnswerDialog instance={detail} open={answering} onOpenChange={setAnswering} />
        </>
      )}
    </div>
  );
}

function Panel({ title, note, children, icon }: { title: string; note?: string; children: React.ReactNode; icon?: React.ReactNode }) {
  return (
    <section className="rounded-xl border bg-card">
      <h2 className="flex items-baseline gap-2 border-b px-4 py-3 text-sm font-semibold">
        {icon}
        {title}
        {note && <span className="ml-auto text-xs font-normal text-muted-foreground">{note}</span>}
      </h2>
      <div className="p-4">{children}</div>
    </section>
  );
}

function Ended({ detail }: { detail: Detail }) {
  const outcome = outcomeOf(detail.result);
  if (detail.status === 'failed' && detail.ended) {
    return (
      <section className="grid gap-1 rounded-xl border border-violet-500/30 bg-violet-500/6 p-4">
        <span className="text-sm font-medium">Ended by a person, as it was</span>
        <span className="text-sm">{detail.ended}</span>
        <span className="text-xs text-muted-foreground">Nothing was undone: what it did stays done.</span>
      </section>
    );
  }
  if (detail.status === 'failed') {
    return (
      <section className="grid gap-1 rounded-xl border border-violet-500/30 bg-violet-500/6 p-4">
        <span className="text-sm font-medium">Cancelled</span>
        <span className="text-sm">{detail.cancel || 'cancelled'}</span>
        <span className="text-xs text-muted-foreground">What it gave flow.undo has run, the last first.</span>
      </section>
    );
  }
  return (
    <section
      className={cn(
        'grid gap-2 rounded-xl border p-4',
        outcome === 'err' ? 'border-destructive/30 bg-destructive/5' : 'border-success/30 bg-success/6',
      )}
    >
      <span className="text-sm font-medium">{outcome === 'err' ? 'Its run returned an Err: what it did was undone' : 'Its run returned'}</span>
      <Value value={detail.result} className="text-[0.9375rem]" />
    </section>
  );
}

/** Steps by name, with what each of their calls returned. */
function stepResults(kept: Kept[]) {
  const byStep = new Map<string, Kept[]>();
  for (const k of kept) {
    const { step, pass } = keyParts(k.key);
    if (pass === '0' || /^\d+$/.test(pass)) {
      if (!byStep.has(step)) byStep.set(step, []);
      byStep.get(step)!.push(k);
    }
  }
  return byStep;
}

function Where({ detail, flow, program, now }: { detail: Detail; flow?: FlowInfo; program?: Program; now: number }) {
  const tasks = detail.tasks;
  const [chosen, setChosen] = useState(0);
  const [library, setLibrary] = useState(false);
  const place = tasks[Math.min(chosen, tasks.length - 1)];
  const own = place ? ownFrames(place) : [];
  const top = own[0];
  const file = top?.file ?? flow?.run?.file ?? flow?.at?.file;
  const steps = useMemo(() => new Set((program?.steps ?? []).map((s) => s.name)), [program]);
  const results = useMemo(() => stepResults(detail.kept), [detail.kept]);

  const notes = useMemo<Note[]>(() => {
    const out: Note[] = [];
    if (!file) return out;
    // Where each task is
    tasks.forEach((t, i) => {
      const frames = ownFrames(t);
      frames.forEach((f, depth) => {
        if (f.file !== file || !f.line) return;
        if (depth === 0) {
          out.push(
            i === chosen
              ? { line: f.line, kind: 'here', gutter: '▶', text: tasks.length > 1 ? `task ${t.task} ${waitsFor(t, detail, now)}` : waitsFor(t, detail, now) }
              : { line: f.line, kind: 'mark', gutter: '▷', text: `task ${t.task} is here` },
          );
        } else if (i === chosen) {
          out.push({ line: f.line, kind: 'mark', gutter: '↳', text: `called from here` });
        }
      });
    });
    return out;
  }, [tasks, chosen, file, detail, now]);

  return (
    <section className="grid gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">
          {detail.status === 'done' || detail.status === 'failed' ? 'Its run' : 'Where it is'}
          <span className="ml-2 font-normal text-muted-foreground">the program is the chart</span>
        </h2>
        {tasks.length > 1 && (
          <div className="flex flex-wrap gap-1 rounded-lg border bg-card p-1" role="tablist" aria-label="Tasks">
            {tasks.map((t, i) => (
              <button
                key={t.task}
                type="button"
                role="tab"
                aria-selected={i === chosen}
                onClick={() => setChosen(i)}
                className={cn('rounded-md px-2.5 py-1 font-mono text-xs', i === chosen ? 'bg-accent text-foreground' : 'text-muted-foreground')}
              >
                task {t.task}
              </button>
            ))}
          </div>
        )}
      </div>
      {file ? (
        <SourceWithSteps file={file} notes={notes} steps={steps} results={results} detail={detail} from={flow?.run?.file === file ? flow?.run?.line : undefined} />
      ) : (
        <p className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">The source of this flow is not served.</p>
      )}
      {place && (place.frames?.length ?? 0) > 0 && (
        <div className="rounded-xl border bg-card">
          <div className="flex items-center justify-between border-b px-4 py-2.5">
            <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Calls, innermost first</span>
            <button type="button" className="text-xs text-muted-foreground hover:text-foreground" onClick={() => setLibrary((l) => !l)}>
              {library ? 'Hide the library' : 'Show the library'}
            </button>
          </div>
          <ol className="divide-y">
            {(place.frames ?? [])
              .filter((f) => library || !isLibrary(f.file))
              .map((f, i) => (
                <li key={i} className={cn('flex items-center gap-3 px-4 py-2 font-mono text-[0.8125rem]', isLibrary(f.file) && 'text-muted-foreground')}>
                  {i === 0 ? <Clock className="size-3.5 text-warning" /> : <CornerDownRight className="size-3.5 text-muted-foreground" />}
                  <span className="truncate">{f.function}</span>
                  <span className="ml-auto shrink-0 text-xs text-muted-foreground">
                    {shortFile(f.file)}:{f.line}
                  </span>
                </li>
              ))}
          </ol>
        </div>
      )}
    </section>
  );
}

/** The source, with what each step called in it returned written at the end of its line. */
function SourceWithSteps({
  file,
  notes,
  steps,
  results,
  detail,
  from,
}: {
  file: string;
  notes: Note[];
  steps: Set<string>;
  results: Map<string, Kept[]>;
  detail: Detail;
  from?: number;
}) {
  return <StepNotes file={file} steps={steps} results={results} detail={detail}>{(stepNotes) => <CodeView file={file} notes={[...notes, ...stepNotes]} from={from} />}</StepNotes>;
}


function StepNotes({
  file,
  steps,
  results,
  detail,
  children,
}: {
  file: string;
  steps: Set<string>;
  results: Map<string, Kept[]>;
  detail: Detail;
  children: (notes: Note[]) => React.ReactNode;
}) {
  const source = useSource(file);
  const notes = useMemo<Note[]>(() => {
    const out: Note[] = [];
    const lines = source.data?.lines ?? [];
    const leftNoAnswer = new Map<string, number>();
    for (const [key, tries] of Object.entries(detail.tries ?? {})) {
      if (!detail.kept.some((k) => k.key === key)) leftNoAnswer.set(keyParts(key).step, tries);
    }
    lines.forEach((text, i) => {
      const code = text.replace(/\/\/.*$/, '');
      for (const step of steps) {
        const call = new RegExp(`(^|[^\\w.])${step}\\s*\\(`);
        if (!call.test(code) || new RegExp(`\\bfn\\s+${step}\\b`).test(code)) continue;
        const kept = results.get(step);
        if (kept?.length) {
          const last = kept[kept.length - 1];
          out.push({
            line: i + 1,
            kind: 'step',
            gutter: '✓',
            text: (
              <span>
                <span className="text-success">{step}</span>
                {kept.length > 1 && <span> ×{kept.length}</span>} → <span className="font-mono">{qoqi(last.result)}</span>
              </span>
            ),
          });
        } else if (leftNoAnswer.has(step)) {
          out.push({ line: i + 1, kind: 'step', gutter: '…', text: `${step} left, attempt ${leftNoAnswer.get(step)}, no answer yet` });
        }
      }
    });
    return out;
  }, [source.data, steps, results, detail]);
  return <>{children(notes)}</>;
}

function Steps({ detail }: { detail: Detail }) {
  if (detail.kept.length === 0 && Object.keys(detail.tries ?? {}).length === 0) {
    return <p className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">No step has left yet.</p>;
  }
  const keys = new Set([...detail.kept.map((k) => k.key), ...Object.keys(detail.tries ?? {})]);
  const rows = [...keys].sort().map((key) => ({ key, parts: keyParts(key), kept: detail.kept.find((k) => k.key === key), tries: detail.tries?.[key] ?? 0, retry: detail.retries?.[key] }));
  return (
    <ol className="divide-y overflow-hidden rounded-xl border bg-card">
      {rows.map((r) => (
        <li key={r.key} className="grid gap-1.5 px-4 py-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className={cn('size-2 rounded-full', r.kept ? 'bg-success' : 'bg-warning')} />
            <span className="font-mono text-sm font-medium">
              {r.parts.step}
              <span className="text-muted-foreground">#{r.parts.n}</span>
            </span>
            {r.parts.task === 'finish' ? (
              <span className="rounded-md bg-violet-500/10 px-1.5 py-0.5 text-xs text-violet-600 dark:text-violet-400">undo</span>
            ) : (
              r.parts.task !== '0' && <span className="font-mono text-xs text-muted-foreground">task {r.parts.task}</span>
            )}
            {r.tries > 1 && <span className="text-xs text-warning">{r.tries} attempts</span>}
            {r.retry && <span className="text-xs text-muted-foreground">tried again at {clock(r.retry)}</span>}
            <span className="ml-auto max-w-full min-w-0 truncate">{r.kept ? <Value value={r.kept.result} /> : <span className="text-sm text-muted-foreground">left, no answer yet</span>}</span>
          </div>
          <span className="truncate pl-5 font-mono text-[0.6875rem] text-muted-foreground" title={r.key}>
            {r.key}
          </span>
          {r.kept?.undos?.map((u, i) => (
            <span key={i} className="flex items-center gap-1.5 pl-5 text-xs text-muted-foreground">
              <Undo2 className="size-3" /> undone by <span className="font-mono text-foreground">{u.step}</span>(<Value value={u.argument} className="text-xs" />)
            </span>
          ))}
        </li>
      ))}
    </ol>
  );
}

function Output({ detail }: { detail: Detail }) {
  if (detail.output.length === 0) {
    return (
      <p className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">
        Nothing printed while this daemon ran it. What a step prints shows here, as it prints it.
      </p>
    );
  }
  return (
    <ol className="max-h-[28rem] overflow-auto rounded-xl border bg-card py-2 font-mono text-[0.8125rem] leading-6">
      {detail.output.map((l, i) => (
        <li key={i} className="grid grid-cols-[6.5rem_1fr] gap-3 px-4 hover:bg-accent/40">
          <span className="text-xs leading-6 text-muted-foreground tabular-nums">{clock(l.at)}</span>
          <span className="break-words whitespace-pre-wrap">{l.text}</span>
        </li>
      ))}
    </ol>
  );
}

function WaitsFor({ detail, now }: { detail: Detail; now: number }) {
  if (detail.tasks.length === 0) return null;
  return (
    <Panel title="Waits for" icon={<Clock className="size-4 self-center text-warning" />}>
      <ul className="grid gap-4">
        {detail.tasks.map((t) => {
          const left = t.until ? t.until - now : 0;
          return (
            <li key={t.task} className="grid gap-1.5">
              {detail.tasks.length > 1 && <span className="font-mono text-xs text-muted-foreground">task {t.task}</span>}
              <span className="text-sm">{waitsFor(t, detail, now)}</span>
              {t.until && (
                <div className="grid gap-1">
                  <span className="font-mono text-2xl font-semibold tabular-nums tracking-tight">{left > 0 ? relative(t.until, now).replace('in ', '') : 'now'}</span>
                  <span className="text-xs text-muted-foreground">
                    {clock(t.until)} · the store wakes it, whatever runs meanwhile
                  </span>
                </div>
              )}
              {t.waiting === 'suspend' && awaited(detail).instances.map((a) => (
                <Link key={a} to={`/instances/${encodeURIComponent(a)}`} className="font-mono text-sm text-primary hover:underline">
                  {a} →
                </Link>
              ))}
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

function Undos({ detail }: { detail: Detail }) {
  const undos = detail.undos ?? [];
  return (
    <Panel title="If it fails" note={undos.length ? 'undone, the last first' : undefined} icon={<Undo2 className="size-4 self-center text-violet-500" />}>
      {undos.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing to undo.</p>
      ) : (
        <ol className="grid gap-2">
          {[...undos].reverse().map((u, i) => (
            <li key={i} className="flex items-baseline gap-2 font-mono text-[0.8125rem]">
              <span className="w-4 text-right text-xs text-muted-foreground">{i + 1}</span>
              <span>
                <span className="text-sky-700 dark:text-sky-300">{u.step}</span>(<Value value={u.argument} />)
              </span>
            </li>
          ))}
        </ol>
      )}
    </Panel>
  );
}

function Family({ detail }: { detail: Detail }) {
  const all = useInstances().data ?? [];
  const children = detail.children ?? [];
  if (!detail.parent && children.length === 0) return null;
  return (
    <Panel title="Family" icon={<GitFork className="size-4 self-center text-primary" />}>
      <div className="grid gap-3 text-sm">
        {detail.parent && (
          <div className="grid gap-1">
            <span className="text-xs text-muted-foreground">started by</span>
            <Link to={`/instances/${encodeURIComponent(detail.parent)}`} className="font-mono text-primary hover:underline">
              {detail.parent}
            </Link>
          </div>
        )}
        {children.length > 0 && (
          <div className="grid gap-1.5">
            <span className="text-xs text-muted-foreground">started, cancelled first if it fails</span>
            {children.map((c) => {
              const child = all.find((v) => v.id === c);
              return (
                <Link key={c} to={`/instances/${encodeURIComponent(c)}`} className="flex items-center gap-2 font-mono hover:underline">
                  {child && <StatusDot status={child.status} />}
                  {c}
                  {child && <span className="ml-auto font-sans text-xs text-muted-foreground">{child.status}</span>}
                </Link>
              );
            })}
          </div>
        )}
      </div>
    </Panel>
  );
}
