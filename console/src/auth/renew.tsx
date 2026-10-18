import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { KeyRound } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api } from '@/lib/api';
import { cn } from '@/lib/utils';
import { AuthCard } from './splash';

/** A password given, admin's first: a person chooses their own before anything else. */
export function RenewPage({ name, onSignOut }: { name: string; onSignOut: () => void }) {
  const client = useQueryClient();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [problem, setProblem] = useState('');
  const [busy, setBusy] = useState(false);
  const short = next.length > 0 && next.length < 10;
  const differ = again.length > 0 && again !== next;

  return (
    <AuthCard
      title="Choose your password"
      hint={
        <>
          You signed in as <span className="font-medium text-foreground">{name}</span> with a password you were given. Choose one of your own: the console
          opens then.
        </>
      }
    >
      <form
        className="grid gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setProblem('');
          try {
            await api.password(name, next, current);
            await client.invalidateQueries({ queryKey: ['auth'] });
          } catch (err) {
            setProblem((err as Error).message);
            setBusy(false);
          }
        }}
      >
        <div className="grid gap-1.5">
          <Label htmlFor="current">The password you were given</Label>
          <Input id="current" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} autoFocus />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="next">Your password</Label>
          <Input id="next" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          <p className={cn('text-xs', short ? 'text-destructive' : 'text-muted-foreground')}>10 characters at least.</p>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="again">Again</Label>
          <Input id="again" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} aria-invalid={differ} />
          {differ && <p className="text-xs text-destructive">The two are not the same.</p>}
        </div>
        {problem && (
          <Alert variant="destructive">
            <AlertDescription>{problem}</AlertDescription>
          </Alert>
        )}
        <Button type="submit" disabled={busy || !current || next.length < 10 || next !== again}>
          <KeyRound /> Set it, and open the console
        </Button>
        <button type="button" onClick={onSignOut} className="text-center text-xs text-muted-foreground hover:text-foreground">
          Sign out
        </button>
      </form>
    </AuthCard>
  );
}
