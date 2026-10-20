import { Play, ShieldCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { CodeView } from '@/components/code';
import { useLaunch } from '@/features/instances/launch';
import { shortFile } from '@/lib/describe';
import { shortName } from '@/lib/utils';
import type { Decl, Field, FlowInfo, StepInfo } from '@/lib/types';

// The program the daemon runs, as a reference: a flow and what it is
// started with, the steps, the only way its flows reach the world, and the
// types they hold.

export function FieldList({ fields }: { fields: Field[] | null | undefined }) {
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

export function FlowDetail({ flow }: { flow: FlowInfo }) {
  const launch = useLaunch();
  return (
    <div className="grid gap-4 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <div className="grid content-start gap-4 rounded-xl border bg-card p-5">
        <div className="grid gap-1">
          <span className="font-semibold break-all">{flow.name}</span>
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
          <Play /> Start {shortName(flow.name)}
        </Button>
      </div>
      {flow.run && <CodeView file={flow.run.file} from={flow.run.line} to={flow.run.line + 40} notes={[{ line: flow.run.line, kind: 'mark', gutter: '◆', text: 'run' }]} />}
    </div>
  );
}

export function StepCard({ step }: { step: StepInfo }) {
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

export function DeclCard({ decl }: { decl: Decl }) {
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
