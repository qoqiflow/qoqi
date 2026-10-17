import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { ChevronsUpDown } from 'lucide-react';
import { useSource } from '@/lib/queries';
import { shortFile } from '@/lib/describe';
import { cn } from '@/lib/utils';

// A file of the program, with what an instance did and does written in its
// margin and at the end of its lines: the line each task waits on, what
// each step returned. The program is the chart.

export interface Note {
  line: number;
  kind: 'here' | 'step' | 'run' | 'mark';
  gutter?: string;
  text?: ReactNode;
}

const keywords = new Set([
  'fn', 'let', 'if', 'else', 'match', 'case', 'return', 'for', 'in', 'while', 'wait', 'step', 'impl', 'struct', 'enum',
  'import', 'pub', 'self', 'true', 'false', 'null', 'stage', 'migrate', 'default', 'trait', 'const', 'break',
  'continue', 'defer', 'is', 'priv', 'static', 'extern',
]);

const token = /(\/\/.*$)|("(?:[^"\\]|\\.)*")|(\b\d[\d_]*(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)(\s*\()?|(.)/g;

function highlight(line: string): ReactNode[] {
  const out: ReactNode[] = [];
  let m: RegExpExecArray | null;
  let i = 0;
  token.lastIndex = 0;
  while ((m = token.exec(line)) !== null) {
    const [whole, comment, string, number, word, call] = m;
    if (comment) out.push(<span key={i++} className="text-muted-foreground italic">{comment}</span>);
    else if (string) out.push(<span key={i++} className="text-emerald-700 dark:text-emerald-300">{string}</span>);
    else if (number) out.push(<span key={i++} className="text-orange-600 dark:text-orange-300">{number}</span>);
    else if (word) {
      const cls = keywords.has(word)
        ? 'text-violet-600 dark:text-violet-300 font-medium'
        : call
          ? 'text-sky-700 dark:text-sky-300'
          : /^[A-Z]/.test(word)
            ? 'text-teal-700 dark:text-teal-300'
            : undefined;
      out.push(<span key={i++} className={cls}>{word}</span>);
      if (call) out.push(call);
    } else out.push(whole);
    if (whole === '') token.lastIndex++;
  }
  return out;
}

export function CodeView({
  file,
  notes,
  from,
  to,
  className,
}: {
  file: string;
  notes: Note[];
  /** The lines shown first: those of the flow's run, say. */
  from?: number;
  to?: number;
  className?: string;
}) {
  const source = useSource(file);
  const [whole, setWhole] = useState(false);
  const here = useRef<HTMLDivElement>(null);

  const byLine = useMemo(() => {
    const map = new Map<number, Note[]>();
    for (const note of notes) {
      if (!map.has(note.line)) map.set(note.line, []);
      map.get(note.line)!.push(note);
    }
    return map;
  }, [notes]);

  const lines = source.data?.lines ?? [];
  const focus = notes.find((n) => n.kind === 'here')?.line;
  // Around the line it waits on, from the start of the function when it
  // is near; without such a line, the function from its start
  const anchor = from ?? focus ?? 1;
  const start = whole ? 1 : Math.max(1, Math.min(anchor, focus ?? Infinity) - 2);
  const end = whole ? lines.length : Math.min(lines.length, Math.max(to ?? anchor + 30, (focus ?? 0) + 8));

  useEffect(() => {
    here.current?.scrollIntoView({ block: 'nearest' });
  }, [focus, source.data]);

  if (source.isError) {
    return <div className="text-muted-foreground p-4 text-sm">The source of {shortFile(file)} cannot be shown: {String(source.error.message)}</div>;
  }

  return (
    <div className={cn('overflow-hidden rounded-lg border bg-bg-base-100 dark:bg-bg-base-100', className)}>
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="font-mono text-xs text-muted-foreground" title={file}>
          {shortFile(file)}
          {!whole && lines.length > 0 && <span className="opacity-70"> · lines {start}–{end}</span>}
        </span>
        <button
          type="button"
          onClick={() => setWhole((w) => !w)}
          className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs"
        >
          <ChevronsUpDown className="size-3.5" />
          {whole ? 'Around it' : 'Whole file'}
        </button>
      </div>
      <div className={cn('overflow-auto py-2 font-mono text-[0.8125rem] leading-6', whole && 'max-h-[70vh]')}>
        {source.isLoading && <div className="text-muted-foreground px-4">…</div>}
        {lines.slice(start - 1, end).map((text, i) => {
          const n = start + i;
          const marks = byLine.get(n) ?? [];
          const isHere = marks.some((m) => m.kind === 'here');
          const isStep = marks.some((m) => m.kind === 'step');
          const gutter = marks.find((m) => m.gutter)?.gutter;
          return (
            <div
              key={n}
              ref={isHere ? here : undefined}
              className={cn(
                'group grid grid-cols-[3.25rem_1.5rem_minmax(0,max-content)_1fr] items-baseline gap-0 pr-4',
                isHere && 'bg-warning/12 dark:bg-warning/10',
              )}
            >
              <span className={cn('select-none pr-3 text-right text-muted-foreground/70', isHere && 'text-warning font-semibold')}>{n}</span>
              <span
                className={cn(
                  'select-none text-center',
                  isHere ? 'text-warning' : isStep ? 'text-success' : 'text-muted-foreground',
                )}
              >
                {gutter}
              </span>
              <span className="whitespace-pre">{highlight(text)}</span>
              <span className="flex min-w-0 flex-wrap items-baseline gap-x-3 pl-6">
                {marks
                  .filter((m) => m.text)
                  .map((m, j) => (
                    <span
                      key={j}
                      className={cn(
                        'truncate font-sans text-xs',
                        m.kind === 'here' ? 'text-warning font-semibold' : 'text-muted-foreground',
                      )}
                    >
                      {m.text}
                    </span>
                  ))}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
