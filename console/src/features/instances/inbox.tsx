import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { Inbox, Loader2, Send } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Editor, initial } from '@/features/instances/fields';
import { api } from '@/lib/api';
import { awaited } from '@/lib/describe';
import type { Data, Field, Program, View } from '@/lib/types';

// A message for an inbox of an instance, written in a form made from the
// type the inbox takes: what a webhook or a person would send it. A key
// makes a message sent twice one message.

export function Inboxes({ instance, inboxes, program }: { instance: View; inboxes: Field[]; program: Program }) {
  const waitingOn = new Set(awaited(instance).inboxes);
  return (
    <section className="rounded-xl border bg-card">
      <h2 className="flex items-baseline gap-2 border-b px-4 py-3 text-sm font-semibold">
        <Inbox className="size-4 self-center text-primary" />
        {inboxes.length === 1 ? 'Inbox' : 'Inboxes'}
        <span className="ml-auto text-xs font-normal text-muted-foreground">messages from outside</span>
      </h2>
      <div className="grid gap-5 p-4">
        {inboxes.map((box) => (
          <Message key={box.name} instance={instance} box={box} program={program} waited={waitingOn.has(box.name)} />
        ))}
      </div>
    </section>
  );
}

function Message({ instance, box, program, waited }: { instance: View; box: Field; program: Program; waited: boolean }) {
  const type = box.type.elem;
  const fresh = useMemo<Data>(() => (type ? initial(type, program) : null), [type, program]);
  const [message, setMessage] = useState<Data>(fresh);
  const [key, setKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  if (!type) return null;
  const path = `${box.name}-message`;

  const send = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const { sent } = await api.send(instance.id, box.name, message, key.trim());
      if (sent === 'already') toast.info(`${instance.id} had it already`, { description: `A message under the key ${key.trim()} was sent before.` });
      else toast.success(`Sent to ${box.name} of ${instance.id}`);
      setKey('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="grid gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="font-mono text-sm font-medium">{box.name}</span>
        <span className="font-mono text-[0.75rem] text-muted-foreground">{box.type.text}</span>
        {waited && <span className="ml-auto text-xs text-warning">waited on now</span>}
      </div>
      {box.doc && <span className="text-xs text-muted-foreground">{box.doc}</span>}
      <Editor type={type} value={message} onChange={setMessage} program={program} path={path} error={error} invalid={!!error} />
      <label className="grid gap-1">
        <span className="text-xs text-muted-foreground">Key, to send it once</span>
        <Input className="font-mono" value={key} onChange={(e) => setKey(e.target.value)} placeholder="evt_9f2" />
      </label>
      {error && (
        <p role="alert" className="font-mono text-xs break-words text-destructive">
          {error}
        </p>
      )}
      <Button type="submit" variant="outline" disabled={busy}>
        {busy ? <Loader2 className="animate-spin" /> : <Send />} Send
      </Button>
    </form>
  );
}
