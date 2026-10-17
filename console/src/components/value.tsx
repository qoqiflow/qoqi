import { pieces } from '@/lib/qoqi';
import type { Data } from '@/lib/types';
import { cn } from '@/lib/utils';

// A value written as Qoqi, colored as the editor colors it.

const colors = {
  type: 'text-sky-700 dark:text-sky-300',
  string: 'text-emerald-700 dark:text-emerald-300',
  number: 'text-orange-600 dark:text-orange-300',
  keyword: 'text-violet-600 dark:text-violet-300',
  punct: 'text-muted-foreground',
  name: 'text-foreground',
};

export function Value({ value, className }: { value: Data | undefined; className?: string }) {
  if (value === undefined) return <span className="text-muted-foreground">—</span>;
  return (
    <code className={cn('font-mono text-[0.8125rem] break-words', className)}>
      {pieces(value).map((p, i) => (
        <span key={i} className={colors[p.kind]}>
          {p.text}
        </span>
      ))}
    </code>
  );
}

/** The fields of a struct, one per line, for the side panels. */
export function Fields({ value }: { value: Data | undefined }) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return <Value value={value} />;
  const entries = Object.entries(value).filter(([key]) => !key.startsWith('$'));
  if (entries.length === 0) return <span className="text-muted-foreground text-sm">no fields</span>;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
      {entries.map(([key, item]) => (
        <div key={key} className="contents">
          <dt className="font-mono text-[0.8125rem] text-muted-foreground">{key}</dt>
          <dd className="min-w-0">
            <Value value={item} />
          </dd>
        </div>
      ))}
    </dl>
  );
}
