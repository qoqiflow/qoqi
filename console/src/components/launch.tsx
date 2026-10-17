import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { toast } from 'sonner';
import { AlertCircle, ArrowLeft, Check, Copy, Loader2, Play, Workflow } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { FieldRow, initial } from '@/components/fields';
import { StatusBadge } from '@/components/status';
import { api, ApiError } from '@/lib/api';
import { useProgram } from '@/lib/queries';
import type { Data, DryRun, FlowInfo, Program } from '@/lib/types';
import { cn } from '@/lib/utils';

// Starting an instance, from anywhere in the console: choose a flow, fill
// in a form made from the types of its fields, and see the same start as
// JSON, as curl and as a command, while the daemon checks it as it is
// written.

interface LaunchRequest {
  flow?: string;
  fields?: Data;
}

const LaunchContext = createContext<(request?: LaunchRequest) => void>(() => {});

export function useLaunch() {
  return useContext(LaunchContext);
}

export function LaunchProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<LaunchRequest | null>(null);
  const open = useCallback((r?: LaunchRequest) => setRequest(r ?? {}), []);

  useEffect(() => {
    // n starts a flow, from anywhere but a field
    const key = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (e.key === 'n' && !e.metaKey && !e.ctrlKey && !e.altKey && !target.closest('input, textarea, select, [contenteditable]')) {
        e.preventDefault();
        setRequest({});
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, []);

  return (
    <LaunchContext.Provider value={open}>
      {children}
      <Dialog open={request !== null} onOpenChange={(on) => !on && setRequest(null)}>
        <DialogContent className="max-h-[92vh] gap-0 overflow-hidden p-0 sm:max-w-5xl">
          {request && <Launch request={request} close={() => setRequest(null)} />}
        </DialogContent>
      </Dialog>
    </LaunchContext.Provider>
  );
}

const lastKey = (flow: string) => `qoqi-last:${flow}`;

function remembered(flow: string): Data | undefined {
  try {
    const kept = localStorage.getItem(lastKey(flow));
    return kept ? (JSON.parse(kept) as Data) : undefined;
  } catch {
    return undefined;
  }
}

function Launch({ request, close }: { request: LaunchRequest; close: () => void }) {
  const program = useProgram();
  const flows = program.data?.flows ?? [];
  const [flow, setFlow] = useState<string | undefined>(request.flow ?? (flows.length === 1 ? flows[0].name : undefined));

  useEffect(() => {
    if (!flow && flows.length === 1) setFlow(flows[0].name);
  }, [flow, flows]);

  if (program.isLoading) {
    return <div className="p-10 text-center text-muted-foreground">Reading the program…</div>;
  }
  if (!program.data) {
    return <div className="p-10 text-center text-muted-foreground">The daemon does not describe its program.</div>;
  }
  const info = flows.find((f) => f.name === flow);
  if (!info) return <ChooseFlow program={program.data} choose={setFlow} />;
  return (
    <LaunchForm
      key={info.name}
      program={program.data}
      flow={info}
      given={request.flow === info.name ? request.fields : undefined}
      back={flows.length > 1 ? () => setFlow(undefined) : undefined}
      close={close}
    />
  );
}

function signature(flow: FlowInfo) {
  return `Flow<${flow.returns.text}>`;
}

function ChooseFlow({ program, choose }: { program: Program; choose: (flow: string) => void }) {
  return (
    <div className="grid gap-5 p-6">
      <DialogHeader>
        <DialogTitle>Start a flow</DialogTitle>
        <DialogDescription>The flows of {program.entry.split('/').pop()}. Each instance runs until it ends, across restarts.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-3 sm:grid-cols-2">
        {program.flows.map((f) => (
          <button
            key={f.name}
            type="button"
            onClick={() => choose(f.name)}
            className="group flex flex-col gap-2 rounded-xl border bg-card p-4 text-left transition hover:border-primary/50 hover:shadow-sm focus-visible:ring-[3px] focus-visible:ring-ring/50 outline-none"
          >
            <span className="flex items-center gap-2">
              <Workflow className="size-4 text-primary" />
              <span className="font-semibold">{f.name}</span>
              <span className="font-mono text-xs text-muted-foreground">{signature(f)}</span>
            </span>
            {f.doc && <span className="line-clamp-2 text-sm text-muted-foreground">{f.doc}</span>}
            <span className="font-mono text-xs text-muted-foreground">
              {(f.fields ?? []).map((field) => `${field.name}: ${field.type.text}`).join(', ') || 'no fields'}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}

/** A name for the instance from its fields: order:7 for an Order whose cart.id is "7". */
function suggestId(flow: string, fields: Data): string {
  const prefix = flow.toLowerCase();
  const find = (value: Data, depth: number): string | undefined => {
    if (!value || typeof value !== 'object' || Array.isArray(value) || depth > 2) return undefined;
    const object = value as Record<string, Data>;
    if (typeof object.id === 'string' && object.id) return object.id;
    if (typeof object.id === 'number') return String(object.id);
    for (const item of Object.values(object)) {
      const found = find(item, depth + 1);
      if (found) return found;
    }
    return undefined;
  };
  const id = find(fields, 0);
  return id ? `${prefix}:${id}` : '';
}

function shellQuote(text: string) {
  return `'${text.replace(/'/g, `'\\''`)}'`;
}

function LaunchForm({
  program,
  flow,
  given,
  back,
  close,
}: {
  program: Program;
  flow: FlowInfo;
  given?: Data;
  back?: () => void;
  close: () => void;
}) {
  const navigate = useNavigate();
  const fresh = useMemo<Data>(() => Object.fromEntries((flow.fields ?? []).map((f) => [f.name, initial(f.type, program)])), [program, flow]);
  const [fields, setFields] = useState<Data>(() => given ?? remembered(flow.name) ?? fresh);
  const [id, setId] = useState('');
  const [idEdited, setIdEdited] = useState(false);
  const [check, setCheck] = useState<{ state: 'checking' | 'ok' | 'bad'; message?: string; dry?: DryRun }>({ state: 'checking' });
  const [starting, setStarting] = useState(false);

  const suggested = suggestId(flow.name, fields);
  const name = idEdited ? id : suggested;

  // The daemon checks the fields as they are written
  useEffect(() => {
    setCheck((c) => ({ ...c, state: 'checking' }));
    const timer = setTimeout(() => {
      api
        .check(flow.name, name, fields)
        .then((dry) => setCheck({ state: 'ok', dry }))
        .catch((e: unknown) => setCheck({ state: 'bad', message: e instanceof Error ? e.message : String(e) }));
    }, 250);
    return () => clearTimeout(timer);
  }, [flow.name, name, fields]);

  const body = JSON.stringify({ flow: flow.name, ...(name ? { id: name } : {}), fields });
  const start = async () => {
    setStarting(true);
    try {
      const { status, data } = await api.start(flow.name, name, fields);
      try {
        localStorage.setItem(lastKey(flow.name), JSON.stringify(fields));
      } catch {
        // Not remembered: no harm
      }
      if (status === 200) toast.info(`${data.id} exists already`, { description: 'A start with a name that is taken gives the instance that has it.' });
      else toast.success(`Started ${data.id}`);
      close();
      navigate(`/instances/${encodeURIComponent(data.id)}`);
    } catch (e) {
      setCheck({ state: 'bad', message: e instanceof ApiError ? e.message : String(e) });
    } finally {
      setStarting(false);
    }
  };

  const command = `qoqi start ${flow.name} ${name || '<name>'} ${shellQuote(JSON.stringify(fields))}`;
  const curl = `curl -X POST ${location.origin}/api/instances \\\n  -H "Authorization: Bearer $(jq -r .token flows.db.daemon)" \\\n  -d ${shellQuote(body)}`;

  return (
    <form
      className="grid max-h-[92vh] grid-rows-[auto_1fr_auto]"
      onSubmit={(e) => {
        e.preventDefault();
        if (check.state !== 'bad') start();
      }}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
          e.preventDefault();
          start();
        }
      }}
    >
      <DialogHeader className="flex-row items-start gap-3 border-b p-5">
        {back && (
          <Button type="button" variant="ghost" size="icon" onClick={back} aria-label="Choose another flow">
            <ArrowLeft />
          </Button>
        )}
        <div className="grid gap-1">
          <DialogTitle className="flex items-baseline gap-2">
            Start {flow.name}
            <span className="font-mono text-sm font-normal text-muted-foreground">{signature(flow)}</span>
          </DialogTitle>
          <DialogDescription>{flow.doc || 'Fill in its fields: the form is made from their types.'}</DialogDescription>
        </div>
      </DialogHeader>

      <div className="grid min-h-0 overflow-y-auto md:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
        <div className="grid content-start gap-5 p-5">
          <div className="grid gap-1.5 sm:grid-cols-[11rem_1fr] sm:items-start sm:gap-4">
            <label htmlFor="instance-name" className="flex flex-col pt-1.5">
              <span className="text-sm font-medium">Name</span>
              <span className="text-xs text-muted-foreground">Starting a name twice gives one instance</span>
            </label>
            <Input
              id="instance-name"
              className="font-mono"
              value={name}
              placeholder={`${flow.name.toLowerCase()}:… drawn if empty`}
              onChange={(e) => {
                setIdEdited(true);
                setId(e.target.value);
              }}
            />
          </div>
          {(flow.fields ?? []).length === 0 && <p className="text-sm text-muted-foreground">This flow has no fields.</p>}
          {(flow.fields ?? []).map((f) => (
            <FieldRow
              key={f.name}
              field={f}
              value={(fields as Record<string, Data>)?.[f.name] ?? initial(f.type, program)}
              onChange={(v) => setFields({ ...((fields as Record<string, Data>) ?? {}), [f.name]: v })}
              program={program}
              path={f.name}
              error={check.state === 'bad' ? check.message : undefined}
            />
          ))}
          <div>
            <Button type="button" variant="ghost" size="sm" className="text-muted-foreground" onClick={() => setFields(fresh)}>
              Clear the form
            </Button>
          </div>
        </div>

        <aside className="grid content-start gap-4 border-t bg-muted/30 p-5 md:border-t-0 md:border-l">
          <CheckState check={check} />
          <Tabs defaultValue="json">
            <TabsList>
              <TabsTrigger value="json">JSON</TabsTrigger>
              <TabsTrigger value="cli">Command</TabsTrigger>
              <TabsTrigger value="curl">curl</TabsTrigger>
            </TabsList>
            <TabsContent value="json">
              <Snippet text={JSON.stringify(check.dry?.fields ?? fields, null, 2)} note={check.dry ? 'As the program keeps it' : 'As written'} />
            </TabsContent>
            <TabsContent value="cli">
              <Snippet text={command} note="From the folder of the store" />
            </TabsContent>
            <TabsContent value="curl">
              <Snippet text={curl} note="From any program: the API is HTTP and JSON" />
            </TabsContent>
          </Tabs>
        </aside>
      </div>

      <footer className="flex items-center justify-between gap-3 border-t p-4">
        <span className="hidden text-xs text-muted-foreground sm:block">
          <kbd className="rounded border px-1.5 py-0.5 font-mono">⌘</kbd> <kbd className="rounded border px-1.5 py-0.5 font-mono">↵</kbd> to start
        </span>
        <div className="flex gap-2">
          <Button type="button" variant="outline" onClick={close}>
            Close
          </Button>
          <Button type="submit" disabled={starting || check.state === 'bad'}>
            {starting ? <Loader2 className="animate-spin" /> : <Play />}
            {check.dry?.taken ? `Open ${name}` : 'Start'}
          </Button>
        </div>
      </footer>
    </form>
  );
}

function CheckState({ check }: { check: { state: 'checking' | 'ok' | 'bad'; message?: string; dry?: DryRun } }) {
  if (check.state === 'bad') {
    return (
      <div role="alert" className="flex gap-2.5 rounded-lg border border-destructive/30 bg-destructive/8 p-3 text-sm">
        <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
        <span>
          <span className="font-medium text-destructive">The daemon would refuse it.</span> <span className="font-mono text-[0.8125rem]">{check.message}</span>
        </span>
      </div>
    );
  }
  if (check.dry?.taken) {
    return (
      <div className="grid gap-2 rounded-lg border border-info/30 bg-info/8 p-3 text-sm">
        <span className="flex items-center gap-2">
          <span className="font-mono font-medium">{check.dry.taken.id}</span> exists <StatusBadge status={check.dry.taken.status} />
        </span>
        <span className="text-muted-foreground">Starting it again gives that instance, whatever the fields: a request sent twice makes one.</span>
      </div>
    );
  }
  return (
    <div className={cn('flex items-center gap-2 rounded-lg border p-3 text-sm', check.state === 'ok' ? 'border-success/30 bg-success/8' : '')}>
      {check.state === 'checking' ? <Loader2 className="size-4 animate-spin text-muted-foreground" /> : <Check className="size-4 text-success" />}
      <span>{check.state === 'checking' ? 'Checking against the types…' : 'Every field has its type: ready to start.'}</span>
    </div>
  );
}

function Snippet({ text, note }: { text: string; note: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="grid gap-1.5">
      <div className="relative">
        <pre className="max-h-80 overflow-auto rounded-lg border bg-background p-3 pr-10 font-mono text-[0.75rem] leading-5 whitespace-pre-wrap break-all">{text}</pre>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="absolute top-1.5 right-1.5 size-7"
          aria-label="Copy"
          onClick={() => {
            navigator.clipboard?.writeText(text).then(() => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1200);
            });
          }}
        >
          {copied ? <Check className="text-success" /> : <Copy />}
        </Button>
      </div>
      <span className="text-xs text-muted-foreground">{note}</span>
    </div>
  );
}
