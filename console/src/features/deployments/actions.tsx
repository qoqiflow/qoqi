import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Code2, History, Loader2, PackageX } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { api } from '@/lib/api';
import type { DeployView, Report } from '@/lib/types';

// What a person does with a version: open its program in the editor,
// serve it again in place of the one served, or take the program away.

const short = (v: string) => v.replace(/^sha256:/, '').slice(0, 8);

function useRefresh() {
  const client = useQueryClient();
  return () =>
    Promise.all(['deployments', 'program', 'workspace', 'flows'].map((key) => client.invalidateQueries({ queryKey: [key] })));
}

export function DeployActions({ deploy }: { deploy: DeployView }) {
  const [again, setAgain] = useState(false);
  const [away, setAway] = useState(false);
  if (deploy.removed) return null;
  return (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" asChild>
        <Link to={`/editor/${encodeURIComponent(deploy.program)}`}>
          <Code2 /> Open in the editor
        </Link>
      </Button>
      {!deploy.serving && (
        <Button size="sm" variant="outline" onClick={() => setAgain(true)}>
          <History /> Serve this version again…
        </Button>
      )}
      {deploy.serving && (
        <Button size="sm" variant="outline" className="text-destructive" onClick={() => setAway(true)}>
          <PackageX /> Take {deploy.program} away…
        </Button>
      )}
      <RedeployDialog deploy={deploy} open={again} onOpenChange={setAgain} />
      <UndeployDialog program={deploy.program} alive={deploy.alive} open={away} onOpenChange={setAway} />
    </div>
  );
}

function RedeployDialog({ deploy, open, onOpenChange }: { deploy: DeployView; open: boolean; onOpenChange: (on: boolean) => void }) {
  const [plan, setPlan] = useState<{ report?: Report; problem?: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const refresh = useRefresh();
  useEffect(() => {
    if (!open) return;
    setPlan(null);
    api
      .redeploy(deploy.seq, true)
      .then((report) => setPlan({ report }))
      .catch((e: unknown) => setPlan({ problem: e instanceof Error ? e.message : String(e) }));
  }, [open, deploy.seq]);

  const report = plan?.report;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Serve {deploy.program} {short(deploy.version)} again
          </DialogTitle>
          <DialogDescription>Deployed as a new version, checked as any is: a version whose stages the instances alive cannot move to is refused.</DialogDescription>
        </DialogHeader>
        {!plan ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> What it would do…
          </p>
        ) : plan.problem ? (
          <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/6 p-3 text-sm break-words">
            {plan.problem}
          </p>
        ) : report?.same ? (
          <p className="text-sm">It is the version served already.</p>
        ) : (
          <div className="grid gap-2 text-sm">
            <p>
              In place of {short(report?.before ?? '')}: {report?.moving ?? 0} instance{report?.moving === 1 ? '' : 's'} alive move to it at their next stage.
            </p>
            {(report?.changes?.length ?? 0) > 0 && (
              <ul className="grid gap-1 rounded-lg bg-muted p-3 font-mono text-xs">
                {report!.changes!.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={busy || !report || report.same}
            onClick={async () => {
              setBusy(true);
              try {
                await api.redeploy(deploy.seq, false);
                await refresh();
                toast.success(`${deploy.program} serves ${short(deploy.version)} again`);
                onOpenChange(false);
              } catch (e) {
                toast.error('Not deployed', { description: e instanceof Error ? e.message : String(e) });
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? <Loader2 className="animate-spin" /> : <History />} Serve it again
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function UndeployDialog({ program, alive, open, onOpenChange }: { program: string; alive: number; open: boolean; onOpenChange: (on: boolean) => void }) {
  const [busy, setBusy] = useState(false);
  const refresh = useRefresh();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Take {program} away?</DialogTitle>
          <DialogDescription>
            The daemon serves it no more, nor starts its flows. Its versions and what its instances were stay in the store; deploying it again serves it again.
          </DialogDescription>
        </DialogHeader>
        {alive > 0 && (
          <p role="alert" className="rounded-lg border border-warning/30 bg-warning/6 p-3 text-sm">
            {alive} instance{alive === 1 ? ' is' : 's are'} alive: it is refused until they end, by cancel or end.
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Keep it
          </Button>
          <Button
            variant="destructive"
            disabled={busy}
            onClick={async () => {
              setBusy(true);
              try {
                await api.undeploy(program);
                await refresh();
                toast.success(`${program} is taken away`);
                onOpenChange(false);
              } catch (e) {
                toast.error(`${program} stays`, { description: e instanceof Error ? e.message : String(e) });
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? <Loader2 className="animate-spin" /> : <PackageX />} Take it away
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
