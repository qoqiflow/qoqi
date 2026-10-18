import { useMemo, useState } from 'react';
import { GitCommitVertical, Layers } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { shortFile } from '@/lib/describe';
import { useDeployments, useDiff } from '@/lib/queries';
import { clock, relative, useNow } from '@/lib/time';
import type { DeployView, FileDiff, Hunk } from '@/lib/types';
import { cn } from '@/lib/utils';
import { DeployActions } from './actions';

// The versions deployed, one after another: what each changed of the
// stages, what still runs on it, and what changed of the source from the
// one before, or from any other of the same program.

export function VersionsPage() {
  const deployments = useDeployments();
  const [chosen, setChosen] = useState<number>();
  const list = deployments.data ?? [];
  const to = list.find((d) => d.seq === chosen) ?? list[0];

  if (deployments.isLoading) return <Skeleton className="h-96 rounded-xl" />;

  return (
    <div className="grid gap-6">
      <div className="grid gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Deployments</h1>
        <p className="text-sm text-muted-foreground">Each deploy is kept: what it changed, what still runs on it, and its source.</p>
      </div>
      {list.length === 0 ? (
        <p className="rounded-xl border bg-card p-6 text-sm text-muted-foreground">
          Nothing deployed yet: <span className="font-mono text-foreground">qoqi deploy shop.qi</span>, or the editor
        </p>
      ) : (
        <div className="grid gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]">
          <ol className="grid content-start gap-2" aria-label="Deploys, the last first">
            {list.map((d) => (
              <li key={d.seq}>
                <DeployItem deploy={d} chosen={d.seq === to?.seq} onChoose={() => setChosen(d.seq)} />
              </li>
            ))}
          </ol>
          {to?.removed && (
            <p className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">
              {to.program} was taken away here: the daemon serves it no more. Its versions and its instances are kept.
            </p>
          )}
          {to && !to.removed && (
            <DiffPanel key={to.seq} to={to} earlier={list.filter((d) => d.program === to.program && d.seq < to.seq && !d.removed)} />
          )}
        </div>
      )}
    </div>
  );
}

function short(version: string): string {
  return version.replace(/^sha256:/, '').slice(0, 8);
}

function DeployItem({ deploy, chosen, onChoose }: { deploy: DeployView; chosen: boolean; onChoose: () => void }) {
  const now = useNow(10000);
  const at = Date.parse(deploy.at);
  return (
    <button
      type="button"
      onClick={onChoose}
      aria-current={chosen}
      className={cn(
        'grid w-full gap-1.5 rounded-xl border bg-card px-4 py-3 text-left transition-colors hover:bg-accent/40',
        chosen && 'border-primary/50 bg-primary/5',
      )}
    >
      <span className="flex items-center gap-2">
        <GitCommitVertical className="size-4 text-muted-foreground" />
        <span className="font-mono text-xs text-muted-foreground">#{deploy.seq}</span>
        <span className="font-medium">{deploy.program}</span>
        {deploy.removed ? (
          <span className="text-xs text-muted-foreground">taken away</span>
        ) : (
          <span className="font-mono text-xs text-muted-foreground">{short(deploy.version)}</span>
        )}
        {deploy.serving && <span className="ml-auto rounded-md bg-primary/12 px-1.5 py-0.5 text-xs text-primary">serving</span>}
      </span>
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1 pl-6 text-xs text-muted-foreground">
        <span title={clock(deploy.at)}>{now - at < 60000 ? 'just now' : relative(at, now).replace(/^in /, '') + ' ago'}</span>
        {deploy.alive > 0 && (
          <span className={cn(!deploy.serving && 'text-warning')} title="Instances alive that run this version">
            {deploy.alive} alive
          </span>
        )}
        {(deploy.changes?.length ?? 0) > 0 && (
          <span>
            {deploy.changes!.length} stage {deploy.changes!.length === 1 ? 'change' : 'changes'}
          </span>
        )}
      </span>
    </button>
  );
}

function DiffPanel({ to, earlier }: { to: DeployView; earlier: DeployView[] }) {
  const [from, setFrom] = useState<number | undefined>(earlier[0]?.seq);
  const diff = useDiff(to.seq, from);
  const changed = useMemo(() => (diff.data?.files ?? []).filter((f) => f.status !== 'same'), [diff.data]);

  return (
    <section className="grid min-w-0 content-start gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-semibold">
          {to.program} <span className="font-mono text-muted-foreground">#{to.seq}</span>
        </h2>
        {earlier.length > 0 ? (
          <span className="flex items-center gap-2 text-sm text-muted-foreground">
            from
            <Select value={String(from)} onValueChange={(v) => setFrom(Number(v))}>
              <SelectTrigger className="h-8 w-56 font-mono text-xs" aria-label="Compare with">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {earlier.map((d) => (
                  <SelectItem key={d.seq} value={String(d.seq)} className="font-mono text-xs">
                    #{d.seq} · {short(d.version)} · {clock(d.at)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </span>
        ) : (
          <span className="text-sm text-muted-foreground">its first deploy</span>
        )}
        <span className="ml-auto text-xs text-muted-foreground">
          {to.flows?.join(', ')} · deployed {clock(to.at)}
          {to.moving > 0 && ` · ${to.moving} instances were to move to it`}
        </span>
      </div>
      <DeployActions deploy={to} />

      {(diff.data?.stages?.length ?? 0) > 0 && (
        <div className="grid gap-1.5 rounded-xl border border-warning/30 bg-warning/6 p-4">
          <span className="flex items-center gap-2 text-sm font-medium">
            <Layers className="size-4 text-warning" /> Stages changed: an instance there moves through a migration
          </span>
          {diff.data!.stages!.map((s) => (
            <span key={s} className="pl-6 font-mono text-xs">
              {s}
            </span>
          ))}
        </div>
      )}

      {diff.isLoading && <Skeleton className="h-64 rounded-xl" />}
      {diff.data && changed.length === 0 && <p className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">The source is the same.</p>}
      {changed.map((f) => (
        <FileView key={f.file} file={f} />
      ))}
    </section>
  );
}

function FileView({ file }: { file: FileDiff }) {
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      <div className="flex items-center gap-3 border-b px-4 py-2.5 text-sm">
        <span className="font-mono font-medium" title={file.file}>
          {shortFile(file.file)}
        </span>
        <span className="text-xs text-muted-foreground">{file.status}</span>
        <span className="ml-auto font-mono text-xs">
          <span className="text-success">+{file.added}</span> <span className="text-destructive">−{file.removed}</span>
        </span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse font-mono text-[0.8125rem] leading-6">
          <tbody>
            {(file.hunks ?? []).map((h, i) => (
              <HunkRows key={i} hunk={h} />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function HunkRows({ hunk }: { hunk: Hunk }) {
  let old = hunk.oldStart;
  let neu = hunk.newStart;
  return (
    <>
      <tr className="bg-muted/50 text-xs text-muted-foreground">
        <td colSpan={4} className="px-4 py-1">
          @@ −{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
        </td>
      </tr>
      {hunk.lines.map((l, i) => {
        const left = l.kind === '+' ? '' : old++;
        const right = l.kind === '-' ? '' : neu++;
        return (
          <tr key={i} className={cn(l.kind === '+' && 'bg-success/8', l.kind === '-' && 'bg-destructive/8')}>
            <td className="w-12 select-none pr-2 text-right text-xs text-muted-foreground tabular-nums">{left}</td>
            <td className="w-12 select-none pr-2 text-right text-xs text-muted-foreground tabular-nums">{right}</td>
            <td className={cn('w-5 select-none text-center', l.kind === '+' ? 'text-success' : l.kind === '-' ? 'text-destructive' : 'text-muted-foreground')}>
              {l.kind === ' ' ? '' : l.kind}
            </td>
            <td className="whitespace-pre pr-4">{l.text}</td>
          </tr>
        );
      })}
    </>
  );
}
