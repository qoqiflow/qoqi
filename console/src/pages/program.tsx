import { useState } from 'react';
import { Play, ShieldCheck, Workflow } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { CodeView } from '@/components/code';
import { useLaunch } from '@/components/launch';
import { shortFile } from '@/lib/describe';
import { useProgram } from '@/lib/queries';
import type { Decl, Field, FlowInfo, StepInfo } from '@/lib/types';
import { cn } from '@/lib/utils';

// The program the daemon runs, as a reference: its flows and what they
// are started with, its steps, the only way its flows reach the world, and
// the types they hold.

export function ProgramPage() {
  const program = useProgram();
  const [shown, setShown] = useState<string | null>(null);

  if (program.isLoading) return <Skeleton className="h-96 rounded-xl" />;
  if (!program.data) return <p className="text-muted-foreground">The daemon does not describe its program.</p>;
  const p = program.data;
  const flow = p.flows.find((f) => f.name === shown) ?? p.flows[0];
  const own = Object.values(p.types).filter((d) => !d.name.startsWith('std.'));

  return (
    <div className="grid gap-8">
      <div className="grid gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Program</h1>
        <p className="text-sm text-muted-foreground">
          <span className="font-mono">{p.entry}</span> · {p.flows.length} flows · {p.steps.length} steps
        </p>
      </div>

      <section className="grid gap-4">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">Flows</h2>
        <div className="flex flex-wrap gap-2">
          {p.flows.map((f) => (
            <button
              key={f.name}
              type="button"
              onClick={() => setShown(f.name)}
              className={cn(
                'flex items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-colors',
                f.name === flow?.name ? 'border-primary bg-primary/8 font-medium' : 'bg-card hover:bg-accent',
              )}
            >
              <Workflow className="size-4 text-primary" /> {f.name}
            </button>
          ))}
        </div>
        {flow && <FlowDetail flow={flow} />}
      </section>

      <section className="grid gap-4">
        <div className="grid gap-1">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">Steps</h2>
          <p className="text-sm text-muted-foreground">
            The only way a flow reaches the world: what each returns is kept under its key, and it is not done again.
          </p>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          {p.steps.map((s) => (
            <StepCard key={s.name} step={s} />
          ))}
        </div>
      </section>

      {own.length > 0 && (
        <section className="grid gap-4">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">Types</h2>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {own.map((d) => (
              <DeclCard key={d.name} decl={d} />
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function FieldList({ fields }: { fields: Field[] | null | undefined }) {
  if (!fields || fields.length === 0) return <span className="text-sm text-muted-foreground">none</span>;
  return (
    <dl className="grid gap-1.5">
      {fields.map((f) => (
        <div key={f.name} className="flex flex-wrap items-baseline gap-x-2 font-mono text-[0.8125rem]">
          <dt>{f.name}</dt>
          <dd className="text-sky-700 dark:text-sky-300">{f.type.text}</dd>
          {f.doc && <dd className="font-sans text-xs text-muted-foreground">{f.doc}</dd>}
        </div>
      ))}
    </dl>
  );
}

function FlowDetail({ flow }: { flow: FlowInfo }) {
  const launch = useLaunch();
  return (
    <div className="grid gap-4 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <div className="grid content-start gap-4 rounded-xl border bg-card p-5">
        <div className="grid gap-1">
          <span className="font-semibold">{flow.name}</span>
          <span className="font-mono text-xs text-muted-foreground">impl Flow&lt;{flow.returns.text}&gt;</span>
        </div>
        {flow.doc && <p className="text-sm text-muted-foreground">{flow.doc}</p>}
        <div className="grid gap-2">
          <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Its fields</span>
          <FieldList fields={flow.fields} />
        </div>
        {flow.at && (
          <span className="font-mono text-xs text-muted-foreground">
            {shortFile(flow.at.file)}:{flow.at.line}
          </span>
        )}
        <Button onClick={() => launch({ flow: flow.name })}>
          <Play /> Start {flow.name}
        </Button>
      </div>
      {flow.run && <CodeView file={flow.run.file} from={flow.run.line} to={flow.run.line + 40} notes={[{ line: flow.run.line, kind: 'mark', gutter: '◆', text: 'run' }]} />}
    </div>
  );
}

function StepCard({ step }: { step: StepInfo }) {
  return (
    <article className="grid gap-2 rounded-xl border bg-card p-4">
      <div className="flex flex-wrap items-baseline gap-x-2 font-mono text-[0.8125rem]">
        <span className="text-violet-600 dark:text-violet-300">step fn</span>
        <span className="font-semibold text-sky-700 dark:text-sky-300">{step.name}</span>
        <span className="text-muted-foreground">({(step.params ?? []).map((p) => `${p.name}: ${p.type.text}`).join(', ')})</span>
        {step.returns.kind !== 'void' && <span className="text-muted-foreground">-&gt; {step.returns.text}</span>}
      </div>
      {step.doc && <p className="text-sm text-muted-foreground">{step.doc}</p>}
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        {step.once && (
          <span className="inline-flex items-center gap-1 rounded-md bg-warning/12 px-1.5 py-0.5 text-warning">
            <ShieldCheck className="size-3" /> never run twice
          </span>
        )}
        {step.at && (
          <span className="ml-auto font-mono">
            {shortFile(step.at.file)}:{step.at.line}
          </span>
        )}
      </div>
    </article>
  );
}

function DeclCard({ decl }: { decl: Decl }) {
  return (
    <article className="grid content-start gap-2 rounded-xl border bg-card p-4">
      <div className="font-mono text-[0.8125rem]">
        <span className="text-violet-600 dark:text-violet-300">{decl.kind}</span> <span className="font-semibold text-teal-700 dark:text-teal-300">{decl.text}</span>
      </div>
      {decl.doc && <p className="text-sm text-muted-foreground">{decl.doc}</p>}
      {decl.kind === 'struct' ? (
        <FieldList fields={decl.fields} />
      ) : (
        <ul className="grid gap-1 font-mono text-[0.8125rem]">
          {(decl.variants ?? []).map((v) => (
            <li key={v.name}>
              {v.name}
              {(v.values?.length ?? 0) > 0 && (
                <span className="text-muted-foreground">({(v.values ?? []).map((f) => (v.named ? `${f.name}: ` : '') + f.type.text).join(', ')})</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </article>
  );
}
