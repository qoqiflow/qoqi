import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { KeyRound, Monitor, Moon, Palette, Sun, Terminal, Trash2, UserPlus, UserRound, Users } from 'lucide-react';
import { toast } from 'sonner';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useAuth } from '@/auth/auth';
import { api } from '@/lib/api';
import { useUsers } from '@/lib/queries';
import { useTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

// The console's own settings: the person's account, the people who sign
// in, how the pages look, and how to reach the daemon from a terminal.

const sections = [
  { id: 'account', label: 'Account', icon: UserRound },
  { id: 'users', label: 'Users', icon: Users },
  { id: 'appearance', label: 'Appearance', icon: Palette },
  { id: 'cli', label: 'Terminal & API', icon: Terminal },
] as const;

export function SettingsPage() {
  const { tab = 'account' } = useParams();
  const navigate = useNavigate();
  const { state } = useAuth();
  const shown = sections.filter((s) => state.accounts || (s.id !== 'users' && s.id !== 'account'));
  const current = shown.find((s) => s.id === tab)?.id ?? shown[0].id;

  return (
    <div className="grid gap-6">
      <div className="grid gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground">Of the console: who signs in, and how it looks.</p>
      </div>
      <div className="grid gap-6 md:grid-cols-[12rem_minmax(0,1fr)]">
        <nav className="flex gap-1 overflow-x-auto md:grid md:content-start" aria-label="Settings">
          {shown.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => navigate(`/settings/${s.id}`, { replace: true })}
              className={cn(
                'flex shrink-0 items-center gap-2 rounded-lg px-3 py-2 text-left text-sm font-medium',
                current === s.id ? 'bg-accent text-foreground' : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
              )}
            >
              <s.icon className="size-4" /> {s.label}
            </button>
          ))}
        </nav>
        <div className="grid content-start gap-6">
          {current === 'account' && <Account />}
          {current === 'users' && <UsersPanel />}
          {current === 'appearance' && <Appearance />}
          {current === 'cli' && <Cli />}
        </div>
      </div>
    </div>
  );
}

function Panel({ title, hint, children, action }: { title: string; hint?: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="grid gap-4 rounded-xl border bg-card p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <h2 className="font-semibold">{title}</h2>
          {hint && <p className="text-sm text-muted-foreground">{hint}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function Account() {
  const { me, signOut } = useAuth();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const change = useMutation({
    mutationFn: () => api.password(me.user!, next, current),
    onSuccess: () => {
      toast.success('The password is changed: your other sessions are ended');
      setCurrent('');
      setNext('');
      setAgain('');
    },
    onError: (err) => toast.error((err as Error).message),
  });

  return (
    <>
      <Panel title={me.user!} hint="Signed in to this console.">
        <div>
          <Button variant="outline" onClick={() => signOut()}>
            Sign out
          </Button>
        </div>
      </Panel>
      <Panel title="Password" hint="A new one ends your sessions everywhere else.">
        <form
          className="grid max-w-sm gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            change.mutate();
          }}
        >
          <div className="grid gap-1.5">
            <Label htmlFor="current">Current password</Label>
            <Input id="current" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="next">New password</Label>
            <Input id="next" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
            <p className="text-xs text-muted-foreground">10 characters at least.</p>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="again">Again</Label>
            <Input id="again" type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} />
          </div>
          <div>
            <Button type="submit" disabled={change.isPending || !current || next.length < 10 || next !== again}>
              <KeyRound /> Change the password
            </Button>
          </div>
        </form>
      </Panel>
    </>
  );
}

function UsersPanel() {
  const users = useUsers();
  const { me } = useAuth();
  const client = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [resetting, setResetting] = useState<string | null>(null);
  const [removing, setRemoving] = useState<string | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => api.removeUser(name),
    onSuccess: (_, name) => {
      toast.success(`${name} is taken away`);
      client.invalidateQueries({ queryKey: ['users'] });
    },
    onError: (err) => toast.error((err as Error).message),
  });

  return (
    <Panel
      title="Users"
      hint="The people who sign in to this console. Each can do everything: start, cancel, edit and deploy."
      action={
        <Button onClick={() => setAdding(true)}>
          <UserPlus /> Add a user
        </Button>
      }
    >
      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Name</TableHead>
              <TableHead>Since</TableHead>
              <TableHead className="pr-4 text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(users.data ?? []).map((u) => (
              <TableRow key={u.name}>
                <TableCell className="pl-4 font-medium">
                  {u.name}
                  {u.name === me.user && <span className="ml-2 text-xs text-muted-foreground">you</span>}
                  {u.renew && <span className="ml-2 rounded bg-warning/15 px-1.5 text-xs text-warning">to choose a password</span>}
                </TableCell>
                <TableCell className="text-muted-foreground">{new Date(u.created).toLocaleDateString()}</TableCell>
                <TableCell className="pr-4 text-right">
                  <div className="flex justify-end gap-1">
                    {u.name !== me.user && (
                      <>
                        <Button size="sm" variant="ghost" onClick={() => setResetting(u.name)}>
                          <KeyRound /> New password
                        </Button>
                        <Button size="sm" variant="ghost" className="text-destructive" onClick={() => setRemoving(u.name)} aria-label={`Take ${u.name} away`}>
                          <Trash2 />
                        </Button>
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        From a terminal: <code className="font-mono">qoqi users add|passwd|rm &lt;name&gt;</code>
      </p>

      <PasswordDialog
        open={adding}
        onClose={() => setAdding(false)}
        title="Add a user"
        withName
        onSubmit={async (name, password) => {
          await api.addUser(name, password);
          toast.success(`${name} signs in from now on`);
          client.invalidateQueries({ queryKey: ['users'] });
        }}
      />
      <PasswordDialog
        open={resetting !== null}
        onClose={() => setResetting(null)}
        title={`A new password for ${resetting}`}
        onSubmit={async (_, password) => {
          await api.password(resetting!, password);
          toast.success(`${resetting} has a new password, to change at their next sign in`);
        }}
      />
      <AlertDialog open={removing !== null} onOpenChange={(open) => !open && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Take {removing} away?</AlertDialogTitle>
            <AlertDialogDescription>They sign in no more, and their sessions end now. What they deployed stays.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep them</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                remove.mutate(removing!);
                setRemoving(null);
              }}
            >
              Take them away
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}

function PasswordDialog({
  open,
  onClose,
  title,
  withName,
  onSubmit,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  withName?: boolean;
  onSubmit: (name: string, password: string) => Promise<void>;
}) {
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [problem, setProblem] = useState('');
  const [busy, setBusy] = useState(false);
  const close = () => {
    setName('');
    setPassword('');
    setProblem('');
    onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setProblem('');
            try {
              await onSubmit(name.trim(), password);
              close();
            } catch (err) {
              setProblem((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          {withName && (
            <div className="grid gap-1.5">
              <Label htmlFor="user-name">Name</Label>
              <Input id="user-name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" autoFocus />
            </div>
          )}
          <div className="grid gap-1.5">
            <Label htmlFor="user-password">Password</Label>
            <Input id="user-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus={!withName} />
            <p className="text-xs text-muted-foreground">10 characters at least. Give it to them: they choose their own when they sign in.</p>
          </div>
          {problem && <p className="text-sm text-destructive">{problem}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || (withName && !name.trim()) || password.length < 10}>
              {withName ? 'Add' : 'Set the password'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Choice<T extends string | number>({ value, options, onChange }: { value: T; options: { value: T; label: ReactNode }[]; onChange: (v: T) => void }) {
  return (
    <div className="flex w-fit flex-wrap gap-1 rounded-lg border p-1">
      {options.map((o) => (
        <button
          key={String(o.value)}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm',
            value === o.value ? 'bg-accent font-medium' : 'text-muted-foreground hover:text-foreground',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Row({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="grid gap-2 border-t pt-4 first:border-t-0 first:pt-0 sm:grid-cols-[14rem_1fr] sm:items-center">
      <div className="grid gap-0.5">
        <span className="text-sm font-medium">{label}</span>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

function Appearance() {
  const { choice, choose } = useTheme();
  const { prefs, setPrefs, me } = useAuth();
  return (
    <Panel title="Appearance" hint={me.user ? 'Kept with your account, on every browser.' : 'Kept in this browser.'}>
      <div className="grid gap-4">
        <Row label="Theme">
          <Choice
            value={choice}
            onChange={choose}
            options={[
              { value: 'light', label: <><Sun className="size-4" /> Light</> },
              { value: 'dark', label: <><Moon className="size-4" /> Dark</> },
              { value: 'system', label: <><Monitor className="size-4" /> As the system</> },
            ]}
          />
        </Row>
        <Row label="Density" hint="How close the rows of the tables are.">
          <Choice
            value={prefs.density ?? 'comfortable'}
            onChange={(density) => setPrefs({ density })}
            options={[
              { value: 'comfortable', label: 'Comfortable' },
              { value: 'compact', label: 'Compact' },
            ]}
          />
        </Row>
        <Row label="Weeks start on" hint="In the grids of activity.">
          <Choice
            value={prefs.weekStart ?? 0}
            onChange={(weekStart) => setPrefs({ weekStart })}
            options={[
              { value: 0, label: 'Sunday' },
              { value: 1, label: 'Monday' },
            ]}
          />
        </Row>
        <Row label="Editor text size">
          <Choice
            value={prefs.editorFontSize ?? 13}
            onChange={(editorFontSize) => setPrefs({ editorFontSize })}
            options={[12, 13, 14, 16].map((n) => ({ value: n, label: `${n}px` }))}
          />
        </Row>
        <Row label="Wrap long lines" hint="In the editor.">
          <Switch checked={prefs.editorWrap ?? false} onCheckedChange={(editorWrap) => setPrefs({ editorWrap })} />
        </Row>
      </div>
    </Panel>
  );
}

function Cli() {
  const lines = [
    ['qoqi serve', 'the daemon; its files in QOQI_PATH, ~/.qoqi by default'],
    ['qoqi deploy shop.qi', 'a version of a program, from a terminal; --watch for each change'],
    ['qoqi ls --status stopped', 'the instances that wait for a person'],
    ['qoqi users add ada', 'a person who signs in here'],
  ];
  return (
    <>
      <Panel
        title="From a terminal"
        hint="The commands talk to the daemon of QOQI_PATH on its machine, or to any other with QOQI_URL and QOQI_TOKEN (--url, --token)."
      >
        <dl className="grid gap-2 font-mono text-sm">
          {lines.map(([cmd, what]) => (
            <div key={cmd} className="flex flex-wrap items-baseline gap-x-3">
              <dt className="rounded bg-muted px-1.5 py-0.5">{cmd}</dt>
              <dd className="font-sans text-xs text-muted-foreground">{what}</dd>
            </div>
          ))}
        </dl>
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">
          {`export QOQI_URL=${location.origin}\nexport QOQI_TOKEN=$(ssh daemon-host cat ~/.qoqi/token)\nqoqi ls`}
        </pre>
      </Panel>
      <Panel title="The API" hint="Applications and scripts give the daemon's token, in QOQI_PATH/token: it opens everything, keep it as a secret.">
        <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">
          {`curl -H "Authorization: Bearer $(cat ~/.qoqi/token)" ${location.origin}/api/instances`}
        </pre>
      </Panel>
    </>
  );
}
