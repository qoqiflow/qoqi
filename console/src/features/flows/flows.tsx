import { Link } from 'react-router-dom';
import { useState } from 'react';
import { Code2, FilePlus2, GitBranch, PackageX } from 'lucide-react';
import { UndeployDialog } from '@/features/deployments/actions';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { emptyCounts, useCounts, useDeployments, useInstances, useProgram } from '@/lib/queries';
import { relative, useNow } from '@/lib/time';
import { FlowCard } from './card';
import { DeclCard, StepCard } from './reference';

// Every flow the daemon runs, program by program; then the steps they
// share, and the types they hold.

export function FlowsPage() {
  const program = useProgram();
  const deployments = useDeployments();
  const instances = useInstances();
  const { byFlow } = useCounts(instances.data);
  const now = useNow(30_000);

  const serving = (deployments.data ?? []).filter((d) => d.serving && !d.removed);
  const flowsOf = new Map(program.data?.flows.map((f) => [f.name, f]) ?? []);
  const own = Object.values(program.data?.types ?? {}).filter((d) => !d.name.startsWith('std.'));

  return (
    <div className="grid gap-8">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Flows</h1>
          <p className="text-sm text-muted-foreground">
            {program.data ? `${program.data.flows.length} flows in ${Math.max(1, serving.length)} programs · ${program.data.steps.length} steps` : 'What the daemon runs'}
          </p>
        </div>
        <Button asChild>
          <Link to="/editor?new=1">
            <FilePlus2 /> New program
          </Link>
        </Button>
      </div>

      {program.isLoading && <Skeleton className="h-44 rounded-xl" />}
      {program.isError && (
        <p className="rounded-xl border border-dashed bg-card p-6 text-sm text-muted-foreground">
          No program yet. <Link to="/editor?new=1" className="font-medium text-primary hover:underline">Write one</Link>, or{' '}
          <span className="font-mono text-foreground">qoqi deploy shop.qi</span>.
        </p>
      )}

      {serving.map((d) => {
        const flows = (d.flows ?? []).map((name) => flowsOf.get(name)).filter((f) => f !== undefined);
        return (
          <section key={d.program} className="grid gap-3" aria-label={d.program}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <h2 className="font-mono text-sm font-semibold">{d.program}</h2>
              <span className="text-xs text-muted-foreground">
                version {d.version.replace(/^sha256:/, '').slice(0, 8)} · deployed {relative(Date.parse(d.at), now)}
              </span>
              <span className="ml-auto flex gap-1">
                <Button size="sm" variant="ghost" asChild>
                  <Link to={`/editor/${encodeURIComponent(d.program)}`}>
                    <Code2 /> Edit
                  </Link>
                </Button>
                <Button size="sm" variant="ghost" asChild>
                  <Link to="/deployments">
                    <GitBranch /> Versions
                  </Link>
                </Button>
                <TakeAway program={d.program} alive={d.alive} />
              </span>
            </div>
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {flows.map((f) => (
                <FlowCard key={f.name} flow={f} counts={byFlow.get(f.name) ?? emptyCounts()} />
              ))}
            </div>
          </section>
        );
      })}

      {/* A daemon given its program at start, without deploys */}
      {serving.length === 0 && program.data && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {program.data.flows.map((f) => (
            <FlowCard key={f.name} flow={f} counts={byFlow.get(f.name) ?? emptyCounts()} />
          ))}
        </div>
      )}

      {program.data && program.data.steps.length > 0 && (
        <section className="grid gap-4">
          <div className="grid gap-1">
            <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">Steps</h2>
            <p className="text-sm text-muted-foreground">The only way a flow reaches the world: what each returns is kept under its key, and it is not done again.</p>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            {program.data.steps.map((s) => (
              <StepCard key={s.name} step={s} />
            ))}
          </div>
        </section>
      )}

      {own.length > 0 && (
        <section className="grid gap-4">
          <h2 className="text-sm font-semibold tracking-wide text-muted-foreground uppercase">Types</h2>
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

function TakeAway({ program, alive }: { program: string; alive: number }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="ghost" className="text-destructive" onClick={() => setOpen(true)}>
        <PackageX /> Take away
      </Button>
      <UndeployDialog program={program} alive={alive} open={open} onOpenChange={setOpen} />
    </>
  );
}
