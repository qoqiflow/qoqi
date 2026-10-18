import { useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { LogIn } from 'lucide-react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { api } from '@/lib/api';
import { AuthCard } from './splash';

export function LoginPage() {
  const client = useQueryClient();
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [problem, setProblem] = useState('');
  const [busy, setBusy] = useState(false);

  return (
    <AuthCard
      title="Sign in"
      hint={
        <>
          The first time: <span className="font-medium text-foreground">admin</span>, password <span className="font-medium text-foreground">admin</span>, which
          you change then. Forgot yours? <code className="font-mono text-xs">qoqi users passwd &lt;name&gt;</code> gives another.
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
            await api.login(name.trim(), password);
            await client.invalidateQueries({ queryKey: ['auth'] });
          } catch (err) {
            setProblem((err as Error).message);
            setBusy(false);
          }
        }}
      >
        <div className="grid gap-1.5">
          <Label htmlFor="name">Name</Label>
          <Input id="name" autoComplete="username" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="password">Password</Label>
          <Input id="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        {problem && (
          <Alert variant="destructive">
            <AlertDescription>{problem}</AlertDescription>
          </Alert>
        )}
        <Button type="submit" disabled={busy || !name.trim() || !password}>
          <LogIn /> Sign in
        </Button>
      </form>
    </AuthCard>
  );
}
