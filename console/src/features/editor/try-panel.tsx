import { useEffect, useState } from 'react';
import { CircleStop, FastForward, Loader2, Play, Plus, RotateCcw, Send, Trash2, X } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Textarea } from '@/components/ui/textarea';
import { StatusBadge } from '@/components/status';
import { api } from '@/lib/api';
import { keyParts } from '@/lib/qoqi';
import type { Data, Program, Trial } from '@/lib/types';
import { cn } from '@/lib/utils';

// Trying a flow of the draft: the daemon runs an instance of it in memory,
// with a clock that moves only when it is moved here. Its steps run for
// real unless they are answered for: a step that reaches the world is
// best given what it returns. Nothing is deployed, nothing is kept.

interface Answer {
  step: string;
  value: string;
}

function parse(text: string, what: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`${what} is not JSON`);
  }
}

const moves = [
  { label: '+1 min', ms: 60_000 },
  { label: '+1 h', ms: 3_600_000 },
  { label: '+1 day', ms: 86_400_000 },
  { label: '+7 days', ms: 7 * 86_400_000 },
];

export function TryPanel({
  open,
  onOpenChange,
  program,
  flows,
  schema,
  files,
}: {
  open: boolean;
  onOpenChange: (on: boolean) => void;
  program: string;
  flows: string[];
  schema?: Program;
  files: Record<string, string>;
}) {
  const [flow, setFlow] = useState('');
  const [fields, setFields] = useState('{}');
  const [answers, setAnswers] = useState<Answer[]>([]);
  const [trial, setTrial] = useState<Trial | null>(null);
  const [busy, setBusy] = useState(false);
  const [inbox, setInbox] = useState('');
  const [message, setMessage] = useState('""');
  const chosen = flow || flows[0] || '';
  const info = schema?.flows.find((f) => f.name === chosen);

  // The fields of the flow chosen, to start from, while none was written
  useEffect(() => {
    if (!info) return;
    setFields((now) => {
      if (now.trim() !== '{}' && now.trim() !== '') return now;
      const given = (info.fields ?? []).filter((f) => !f.default && !f.optional);
      if (given.length === 0) return '{}';
      return JSON.stringify(Object.fromEntries(given.map((f) => [f.name, sampleOf(f.type.kind, f.name)])), null, 2);
    });
  }, [info]);

  const act = async (what: () => Promise<Trial>) => {
    setBusy(true);
    try {
      setTrial(await what());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const start = () =>
    act(() => {
      const replace: Record<string, unknown> = {};
      for (const a of answers) if (a.step.trim()) replace[a.step.trim()] = parse(a.value, `What ${a.step} returns`);
      return api.tryDraft(program, files, chosen, parse(fields, 'The fields'), replace);
    });

  const close = async () => {
    if (trial) await api.closeTrial(trial.id).catch(() => undefined);
    setTrial(null);
  };

  const inst = trial?.instance;
  const waiting = inst?.tasks?.find((t) => t.waiting)?.waiting;

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b p-4">
          <SheetTitle>Try {program}</SheetTitle>
          <SheetDescription>
            An instance of the draft, run in memory, its clock moved by hand. Steps run for real unless answered for. Nothing is deployed, nothing kept.
          </SheetDescription>
        </SheetHeader>

        <div className="grid min-h-0 flex-1 content-start gap-5 overflow-y-auto p-4">
          {!trial ? (
            <>
              <label className="grid gap-1.5">
                <span className="text-sm font-medium">Flow</span>
                <select value={chosen} onChange={(e) => setFlow(e.target.value)} className="h-9 rounded-md border bg-transparent px-2 text-sm">
                  {flows.map((f) => (
                    <option key={f}>{f}</option>
                  ))}
                </select>
              </label>
              <label className="grid gap-1.5">
                <span className="text-sm font-medium">Its fields, as JSON</span>
                <Textarea value={fields} onChange={(e) => setFields(e.target.value)} className="min-h-24 font-mono text-sm" />
                <span className="text-xs text-muted-foreground">As a start takes them: {'{"customer": "ada", "patience": "2d"}'}</span>
              </label>
              <div className="grid gap-2">
                <span className="text-sm font-medium">Steps answered for</span>
                {answers.map((a, i) => (
                  <div key={i} className="flex gap-2">
                    <Input
                      value={a.step}
                      onChange={(e) => setAnswers((all) => all.map((x, j) => (j === i ? { ...x, step: e.target.value } : x)))}
                      placeholder="charge"
                      className="w-36 font-mono"
                    />
                    <Input
                      value={a.value}
                      onChange={(e) => setAnswers((all) => all.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
                      placeholder='{"Ok": "ch-1"}'
                      className="flex-1 font-mono"
                    />
                    <Button size="icon" variant="ghost" onClick={() => setAnswers((all) => all.filter((_, j) => j !== i))} aria-label="Remove">
                      <X />
                    </Button>
                  </div>
                ))}
                <Button size="sm" variant="outline" className="w-fit" onClick={() => setAnswers((all) => [...all, { step: '', value: 'null' }])}>
                  <Plus /> Answer for a step
                </Button>
              </div>
              <Button onClick={start} disabled={busy || !chosen}>
                {busy ? <Loader2 className="animate-spin" /> : <Play />} Start {chosen}
              </Button>
            </>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-3">
                {inst && <StatusBadge status={inst.status} />}
                <span className="text-sm text-muted-foreground">
                  clock <span className="font-mono text-foreground">+{span(trial.elapsed)}</span>
                </span>
                <span className="ml-auto flex gap-1">
                  <Button size="sm" variant="ghost" onClick={() => close().then(start)} disabled={busy}>
                    <RotateCcw /> Again
                  </Button>
                  <Button size="sm" variant="ghost" onClick={close}>
                    <Trash2 /> Close
                  </Button>
                </span>
              </div>

              {inst?.status === 'done' && (
                <div className="rounded-lg border border-success/30 bg-success/6 p-3">
                  <span className="text-xs font-medium text-muted-foreground uppercase">run returned</span>
                  <pre className="mt-1 font-mono text-sm whitespace-pre-wrap">{JSON.stringify(inst.result, null, 2)}</pre>
                </div>
              )}
              {inst?.stopped && (
                <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/6 p-3 font-mono text-sm break-words">
                  {inst.stopped}
                </p>
              )}
              {waiting && <p className="text-sm text-muted-foreground">Waits for {waiting}.</p>}

              {inst && ['waiting', 'running'].includes(inst.status) && (
                <div className="grid gap-3 rounded-lg border p-3">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="mr-1 text-sm font-medium">Move the clock</span>
                    {moves.map((m) => (
                      <Button key={m.ms} size="sm" variant="outline" disabled={busy} onClick={() => act(() => api.trialAct(trial.id, 'advance', { ms: m.ms }))}>
                        <FastForward /> {m.label}
                      </Button>
                    ))}
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <Input
                      value={inbox}
                      onChange={(e) => setInbox(e.target.value)}
                      placeholder={info?.inboxes?.[0]?.name ?? 'inbox'}
                      list="trial-inboxes"
                      className="w-32 font-mono"
                    />
                    <datalist id="trial-inboxes">
                      {(info?.inboxes ?? []).map((i) => (
                        <option key={i.name} value={i.name}>
                          {i.type.text}
                        </option>
                      ))}
                    </datalist>
                    <Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder='"a message"' className="min-w-40 flex-1 font-mono" />
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy || !inbox.trim()}
                      onClick={() => act(() => api.trialAct(trial.id, 'send', { inbox: inbox.trim(), message: parse(message, 'The message') }))}
                    >
                      <Send /> Send
                    </Button>
                  </div>
                  <div>
                    <Button size="sm" variant="ghost" className="text-destructive" disabled={busy} onClick={() => act(() => api.trialAct(trial.id, 'cancel', {}))}>
                      <CircleStop /> Cancel it
                    </Button>
                  </div>
                </div>
              )}

              <Section title="What happened">
                <ol className="grid gap-0.5 font-mono text-xs">
                  {trial.said.map((line, i) => (
                    <li key={i}>{line}</li>
                  ))}
                </ol>
              </Section>

              {(inst?.kept.length ?? 0) > 0 && (
                <Section title="Steps, and what they returned">
                  <ul className="grid gap-1">
                    {inst!.kept.map((k) => (
                      <li key={k.key} className="flex gap-2 font-mono text-xs">
                        <span className="shrink-0 text-sky-700 dark:text-sky-300">
                          {keyParts(k.key).step}#{keyParts(k.key).n}
                        </span>
                        <span className="truncate text-muted-foreground" title={JSON.stringify(k.result)}>
                          → {JSON.stringify(k.result)}
                        </span>
                        {trial.replaced[keyParts(k.key).step] !== undefined && <span className="text-warning">answered for</span>}
                      </li>
                    ))}
                  </ul>
                </Section>
              )}

              {(inst?.output.length ?? 0) > 0 && (
                <Section title="What it printed">
                  <pre className="max-h-48 overflow-auto rounded bg-muted p-2 font-mono text-xs">{inst!.output.map((l) => l.text).join('\n')}</pre>
                </Section>
              )}
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function sampleOf(kind: string, name: string): Data {
  switch (kind) {
    case 'string':
      return name;
    case 'int':
    case 'float':
      return 1;
    case 'bool':
      return false;
    case 'duration':
      return '1m';
    case 'array':
      return [];
    default:
      return null;
  }
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className={cn('grid gap-1.5')}>
      <h3 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">{title}</h3>
      {children}
    </section>
  );
}

function span(ms: number): string {
  if (ms === 0) return '0s';
  const d = Math.floor(ms / 86_400_000);
  const h = Math.floor((ms % 86_400_000) / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  const s = Math.floor((ms % 60_000) / 1000);
  return [d && `${d}d`, h && `${h}h`, m && `${m}m`, s && `${s}s`].filter(Boolean).join(' ') || `${ms}ms`;
}
