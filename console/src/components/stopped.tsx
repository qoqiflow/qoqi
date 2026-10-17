import { useEffect, useState } from 'react';
import { toast } from 'sonner';
import { CircleStop, Loader2, Play } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import { api } from '@/lib/api';
import { keyParts } from '@/lib/qoqi';
import type { ResumePlan, View } from '@/lib/types';

// A stopped instance waits for a person. Once the program is fixed, it is
// resumed: taken up from its last stage in the newest version, what was
// done since kept and not done again. Or it is ended as it is, when a
// person has put the world right by hand.

export function ResumeDialog({ instance, open, onOpenChange }: { instance: View; open: boolean; onOpenChange: (on: boolean) => void }) {
  const [plan, setPlan] = useState<{ state: 'asking' | 'ok' | 'bad'; plan?: ResumePlan; message?: string }>({ state: 'asking' });
  const [busy, setBusy] = useState(false);

  // What it would do, asked each time the dialog opens: the program may
  // have changed since
  useEffect(() => {
    if (!open) return;
    setPlan({ state: 'asking' });
    api
      .plan(instance.id)
      .then((p) => setPlan({ state: 'ok', plan: p }))
      .catch((e: unknown) => setPlan({ state: 'bad', message: e instanceof Error ? e.message : String(e) }));
  }, [open, instance.id]);

  const resume = async () => {
    setBusy(true);
    try {
      await api.resume(instance.id);
      toast.success(`Resuming ${instance.id}`);
      onOpenChange(false);
    } catch (e) {
      toast.error(`${instance.id} was not resumed`, { description: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };

  const p = plan.plan;
  const took = p?.took ?? [];
  const next = p?.next ?? [];
  const forget = p?.forget ?? [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Resume <span className="font-mono">{instance.id}</span>
          </DialogTitle>
          <DialogDescription>
            Taken up from {instance.stage ? <span className="font-mono">stage {instance.stage}</span> : 'its start'}, in the newest version of the program. Found without running a step.
          </DialogDescription>
        </DialogHeader>

        {plan.state === 'asking' && (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Finding what it would do…
          </p>
        )}
        {plan.state === 'bad' && (
          <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/6 p-3 font-mono text-sm break-words">
            {plan.message}
          </p>
        )}
        {plan.state === 'ok' && (
          <ol className="grid gap-3">
            <Stage n={1} title={took.length ? `${took.length} step${took.length > 1 ? 's' : ''} found done, not run again` : 'No step found done since the stage'}>
              {took.length > 0 && (
                <span className="flex flex-wrap gap-x-3 gap-y-1">
                  {took.map((key) => (
                    <span key={key} className="font-mono text-xs" title={key}>
                      {keyParts(key).step}#{keyParts(key).n}
                    </span>
                  ))}
                </span>
              )}
            </Stage>
            {forget.length > 0 && (
              <Stage n={2} title={`${forget.length} undo${forget.length > 1 ? 's' : ''} that failed run again`}>
                <span className="font-mono text-xs">{forget.join(', ')}</span>
              </Stage>
            )}
            <Stage n={forget.length ? 3 : 2} title="Then">
              {next.length > 0 ? (
                <span className="grid gap-1">
                  {next.map((what, i) => (
                    <span key={i} className="font-mono text-xs break-words">
                      {what}
                    </span>
                  ))}
                </span>
              ) : (
                'nothing that leaves'
              )}
            </Stage>
            {p?.stopped && (
              <li role="alert" className="grid gap-1 rounded-lg border border-destructive/30 bg-destructive/6 p-3">
                <span className="text-sm font-medium">It would stop again</span>
                <span className="font-mono text-xs break-words">{p.stopped}</span>
              </li>
            )}
          </ol>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Leave it stopped
          </Button>
          <Button onClick={resume} disabled={busy || plan.state !== 'ok'}>
            {busy ? <Loader2 className="animate-spin" /> : <Play />} Resume {instance.id}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function EndDialog({ instance, open, onOpenChange }: { instance: View; open: boolean; onOpenChange: (on: boolean) => void }) {
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);

  const end = async () => {
    setBusy(true);
    try {
      await api.end(instance.id, reason.trim() || 'ended from the console');
      toast.success(`Ended ${instance.id}`);
      onOpenChange(false);
    } catch (e) {
      toast.error(`${instance.id} was not ended`, { description: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            End <span className="font-mono">{instance.id}</span> as it is
          </DialogTitle>
          <DialogDescription>For when the world was put right by hand.</DialogDescription>
        </DialogHeader>
        <ol className="grid gap-3">
          <Stage n={1} title="Nothing more runs" />
          <Stage n={2} title={instance.undos?.length ? `Its ${instance.undos.length} undo${instance.undos.length > 1 ? 's do' : ' does'} not run` : 'It has nothing to undo'}>
            What it did stays done.
          </Stage>
          <Stage n={3} title="It ends failed">
            Whoever waits for it is told it ended, and why.
          </Stage>
        </ol>
        <label className="grid gap-1.5">
          <span className="text-sm font-medium">Reason</span>
          <Textarea value={reason} onChange={(e) => setReason(e.target.value)} placeholder="refunded by hand, ticket 4411" />
          <span className="text-xs text-muted-foreground">Kept with the instance.</span>
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Leave it stopped
          </Button>
          <Button variant="destructive" onClick={end} disabled={busy}>
            {busy ? <Loader2 className="animate-spin" /> : <CircleStop />} End {instance.id}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Stage({ n, title, children }: { n: number; title: string; children?: React.ReactNode }) {
  return (
    <li className="grid grid-cols-[1.75rem_1fr] gap-2">
      <span className="flex size-6 items-center justify-center rounded-full border text-xs font-semibold text-muted-foreground">{n}</span>
      <span className="grid gap-1">
        <span className="text-sm font-medium">{title}</span>
        {children && <span className="text-sm text-muted-foreground">{children}</span>}
      </span>
    </li>
  );
}
