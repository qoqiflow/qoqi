import { useState } from 'react';
import { ChevronRight, CircleCheck, CircleX, FlaskConical, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import type { TestRun } from '@/lib/types';
import { cn } from '@/lib/utils';

// The tests of the program, run by the daemon as qoqi test runs them:
// each file name_test.qi, its flows tried by flow.test in memory. What
// failed is shown where it failed.

export function TestsPanel({
  run,
  running,
  hasTests,
  onRun,
  onNew,
  onPick,
}: {
  run: TestRun | null;
  running: boolean;
  hasTests: boolean;
  onRun: () => void;
  onNew: () => void;
  onPick: (file: string, line: number) => void;
}) {
  const [open, setOpen] = useState<Record<string, boolean>>({});
  if (!hasTests) {
    return (
      <div className="flex flex-wrap items-center gap-3 px-3 py-3 text-sm text-muted-foreground">
        No test yet: a test file is name_test.qi, and its tests are its functions testSomething(). flow.test tries a flow in it, its clock moved by
        hand.
        <Button size="sm" variant="outline" onClick={onNew}>
          <FlaskConical /> Write the first test
        </Button>
      </div>
    );
  }
  return (
    <div className="max-h-56 overflow-y-auto">
      <div className="flex items-center gap-3 border-b px-3 py-1.5 text-xs">
        <Button size="sm" variant="ghost" className="h-7" onClick={onRun} disabled={running}>
          {running ? <Loader2 className="animate-spin" /> : <FlaskConical />} Run the tests
        </Button>
        {run && !running && (
          <span className={cn('font-medium', run.ok ? 'text-success' : 'text-destructive')}>
            {run.passed} passed{run.failed > 0 && `, ${run.failed} failed`}
            {!run.ok && run.failed === 0 && ': a file did not run'}
          </span>
        )}
      </div>
      {run?.files.map((f) => (
        <div key={f.file} className="border-b last:border-b-0">
          <div className="flex items-center gap-2 px-3 py-1.5 font-mono text-xs">
            <span className="font-semibold">{f.file}</span>
            <span className="text-muted-foreground">{f.ms} ms</span>
            {f.output && (
              <button type="button" onClick={() => setOpen((o) => ({ ...o, [f.file]: !o[f.file] }))} className="ml-auto inline-flex items-center gap-1 text-muted-foreground hover:text-foreground">
                <ChevronRight className={cn('size-3 transition-transform', open[f.file] && 'rotate-90')} /> what it printed
              </button>
            )}
          </div>
          {open[f.file] && f.output && <pre className="mx-3 mb-2 max-h-32 overflow-auto rounded bg-muted p-2 text-xs">{f.output}</pre>}
          {(f.problems ?? []).map((p, i) => (
            <button key={i} type="button" onClick={() => onPick(p.file, p.line)} className="flex w-full items-start gap-2 px-3 py-1 text-left text-[0.8125rem] hover:bg-accent/60">
              <CircleX className="mt-0.5 size-3.5 shrink-0 text-destructive" />
              <span className="flex-1">{p.message}</span>
              <span className="font-mono text-xs text-muted-foreground">
                {p.file}:{p.line}
              </span>
            </button>
          ))}
          {f.failure && <p className="px-3 py-1 font-mono text-xs text-destructive">{f.failure}</p>}
          {f.tests.map((t) => (
            <button
              key={t.name}
              type="button"
              disabled={!t.file}
              onClick={() => t.file && onPick(t.file, t.line ?? 1)}
              className="flex w-full items-start gap-2 px-3 py-1 text-left text-[0.8125rem] enabled:hover:bg-accent/60"
            >
              {t.ok ? <CircleCheck className="mt-0.5 size-3.5 shrink-0 text-success" /> : <CircleX className="mt-0.5 size-3.5 shrink-0 text-destructive" />}
              <span className="grid flex-1 gap-0.5">
                <span>{t.name}</span>
                {t.message && <span className="font-mono text-xs whitespace-pre-wrap text-destructive">{t.message}</span>}
              </span>
              {t.file && (
                <span className="font-mono text-xs text-muted-foreground">
                  {t.file}:{t.line}
                </span>
              )}
            </button>
          ))}
        </div>
      ))}
    </div>
  );
}
