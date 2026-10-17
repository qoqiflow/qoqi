import { useState } from 'react';
import { toast } from 'sonner';
import { Ban, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import { StatusBadge } from '@/components/status';
import { Value } from '@/components/value';
import { api } from '@/lib/api';
import { useInstances } from '@/lib/queries';
import type { View } from '@/lib/types';

// Cancelling an instance, with what it will do laid out first, in the
// order it does it: its children first, then what it gave flow.undo, the
// last first.

export function CancelDialog({ instance, open, onOpenChange }: { instance: View; open: boolean; onOpenChange: (on: boolean) => void }) {
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const all = useInstances().data ?? [];
  const children = (instance.children ?? [])
    .map((id) => all.find((v) => v.id === id))
    .filter((v): v is View => !!v && v.status !== 'done' && v.status !== 'failed');
  const undos = [...(instance.undos ?? [])].reverse();

  const cancel = async () => {
    setBusy(true);
    try {
      await api.cancel(instance.id, reason.trim() || 'cancelled from the console');
      toast.success(`Cancelling ${instance.id}`, { description: undos.length ? `${undos.length} undo${undos.length > 1 ? 's' : ''} to run` : undefined });
      onOpenChange(false);
    } catch (e) {
      toast.error(`${instance.id} was not cancelled`, { description: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };

  const steps: { title: string; detail?: React.ReactNode }[] = [
    { title: 'No task goes on, and no new step leaves.', detail: 'A step under way ends, and its answer is kept.' },
  ];
  if (children.length > 0) {
    steps.push({
      title: `Its ${children.length === 1 ? 'child is' : `${children.length} children are`} cancelled first`,
      detail: (
        <span className="flex flex-wrap gap-2">
          {children.map((c) => (
            <span key={c.id} className="inline-flex items-center gap-1.5 font-mono text-xs">
              {c.id} <StatusBadge status={c.status} />
            </span>
          ))}
        </span>
      ),
    });
  }
  steps.push(
    undos.length > 0
      ? {
          title: `Then its ${undos.length === 1 ? 'undo runs' : `${undos.length} undos run, the last first`}`,
          detail: (
            <span className="grid gap-1">
              {undos.map((u, i) => (
                <span key={i} className="font-mono text-xs">
                  <span className="text-sky-700 dark:text-sky-300">{u.step}</span>(<Value value={u.argument} className="text-xs" />)
                </span>
              ))}
            </span>
          ),
        }
      : { title: 'It has nothing to undo.' },
    { title: 'It ends failed', detail: 'Whoever waits for it is told it was cancelled, and why.' },
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Cancel <span className="font-mono">{instance.id}</span>
          </DialogTitle>
          <DialogDescription>
            {instance.flow} · {instance.status}
          </DialogDescription>
        </DialogHeader>
        <ol className="grid gap-3">
          {steps.map((s, i) => (
            <li key={i} className="grid grid-cols-[1.75rem_1fr] gap-2">
              <span className="flex size-6 items-center justify-center rounded-full border text-xs font-semibold text-muted-foreground">{i + 1}</span>
              <span className="grid gap-1">
                <span className="text-sm font-medium">{s.title}</span>
                {s.detail && <span className="text-sm text-muted-foreground">{s.detail}</span>}
              </span>
            </li>
          ))}
        </ol>
        <label className="grid gap-1.5">
          <span className="text-sm font-medium">Reason</span>
          <Textarea value={reason} onChange={(e) => setReason(e.target.value)} placeholder="the customer changed their mind" />
          <span className="text-xs text-muted-foreground">Kept with the instance, and given to whoever waits for it.</span>
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Keep it running
          </Button>
          <Button variant="destructive" onClick={cancel} disabled={busy}>
            {busy ? <Loader2 className="animate-spin" /> : <Ban />} Cancel {instance.id}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
