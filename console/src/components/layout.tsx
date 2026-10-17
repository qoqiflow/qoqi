import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { KeyRound, Monitor, Moon, Play, Search, Sun } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { StatusDot } from '@/components/status';
import { useLaunch } from '@/components/launch';
import { setToken } from '@/lib/api';
import { useConnection } from '@/lib/live';
import { useInstances, useProgram } from '@/lib/queries';
import { useTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

function Brand() {
  return (
    <NavLink to="/" className="flex items-center gap-2.5 font-semibold tracking-tight">
      <svg viewBox="0 0 512 512" className="size-7" aria-hidden="true">
        <defs>
          <linearGradient id="brand" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0" stopColor="#3D5AFE" />
            <stop offset="1" stopColor="#00C2E0" />
          </linearGradient>
        </defs>
        <rect width="512" height="512" rx="120" fill="url(#brand)" />
        <g fill="white" transform="translate(146 91) scale(2.2)">
          <path fillRule="evenodd" d="M50 0a50 50 0 1 0 0 100a50 50 0 1 0 0-100zM50 22a28 28 0 1 1 0 56a28 28 0 1 1 0-56z" />
          <path d="M78 0H100V139A11 11 0 0 1 78 139Z" />
        </g>
      </svg>
      <span>
        qoqi <span className="text-muted-foreground font-medium">flow</span>
      </span>
    </NavLink>
  );
}

function Live() {
  const connection = useConnection();
  const label = { connecting: 'connecting', live: 'live', offline: 'reconnecting', refused: 'token refused' }[connection];
  return (
    <span className="hidden items-center gap-2 text-xs text-muted-foreground md:inline-flex" title="The console follows the daemon's events as they happen">
      <StatusDot status={connection === 'live' ? 'done' : connection === 'refused' ? 'stopped' : 'waiting'} pulse={connection === 'live'} />
      {label}
    </span>
  );
}

function ThemeMenu() {
  const { choice, resolved, choose } = useTheme();
  const Icon = choice === 'system' ? Monitor : resolved === 'dark' ? Moon : Sun;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label="Theme">
          <Icon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => choose('light')}>
          <Sun /> Light
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => choose('dark')}>
          <Moon /> Dark
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => choose('system')}>
          <Monitor /> As the system
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Find an instance by its name, from anywhere: / puts the cursor there. */
function QuickFind() {
  const instances = useInstances().data ?? [];
  const navigate = useNavigate();
  const [text, setText] = useState('');
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState(0);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if ((e.key === '/' || (e.key === 'k' && (e.metaKey || e.ctrlKey))) && !target.closest('input, textarea, [contenteditable]')) {
        e.preventDefault();
        input.current?.focus();
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, []);

  const found = useMemo(() => {
    const q = text.trim().toLowerCase();
    if (!q) return [];
    return instances.filter((v) => v.id.toLowerCase().includes(q) || v.flow.toLowerCase().includes(q)).slice(0, 8);
  }, [instances, text]);

  const go = (id: string) => {
    setText('');
    setOpen(false);
    input.current?.blur();
    navigate(`/instances/${encodeURIComponent(id)}`);
  };

  return (
    <div className="relative w-full max-w-xs">
      <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        ref={input}
        value={text}
        placeholder="Find an instance"
        className="h-9 pl-9 pr-10"
        onChange={(e) => {
          setText(e.target.value);
          setOpen(true);
          setAt(0);
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') setAt((i) => Math.min(i + 1, found.length - 1));
          else if (e.key === 'ArrowUp') setAt((i) => Math.max(i - 1, 0));
          else if (e.key === 'Enter' && found[at]) go(found[at].id);
          else if (e.key === 'Escape') input.current?.blur();
        }}
        aria-label="Find an instance"
      />
      <kbd className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 rounded border px-1.5 font-mono text-[0.6875rem] text-muted-foreground">/</kbd>
      {open && found.length > 0 && (
        <ul className="absolute z-50 mt-1 w-full overflow-hidden rounded-lg border bg-popover p-1 shadow-lg" role="listbox">
          {found.map((v, i) => (
            <li key={v.id} role="option" aria-selected={i === at}>
              <button
                type="button"
                onMouseDown={(e) => e.preventDefault()}
                onClick={() => go(v.id)}
                className={cn('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm', i === at && 'bg-accent')}
              >
                <StatusDot status={v.status} />
                <span className="truncate font-mono">{v.id}</span>
                <span className="ml-auto text-xs text-muted-foreground">{v.flow}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

const nav = [
  { to: '/', label: 'Overview', end: true },
  { to: '/instances', label: 'Instances' },
  { to: '/program', label: 'Program' },
  { to: '/versions', label: 'Versions' },
];

export function Layout() {
  const launch = useLaunch();
  const connection = useConnection();
  const program = useProgram();

  if (connection === 'refused') return <TokenGate />;

  return (
    <div className="min-h-svh bg-background text-foreground">
      <header className="sticky top-0 z-40 border-b bg-background/85 backdrop-blur supports-[backdrop-filter]:bg-background/70">
        <div className="mx-auto flex max-w-[1400px] flex-wrap items-center gap-x-6 gap-y-2 px-4 py-2.5 lg:px-8">
          <Brand />
          <nav className="flex items-center gap-1" aria-label="Sections">
            {nav.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                className={({ isActive }) =>
                  cn(
                    'rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
                    isActive ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground',
                  )
                }
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex flex-1 items-center justify-end gap-3">
            <QuickFind />
            <Live />
            <ThemeMenu />
            <Button onClick={() => launch()} disabled={!program.data}>
              <Play /> Start a flow
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-[1400px] px-4 py-6 lg:px-8">
        <Outlet />
      </main>
    </div>
  );
}

function TokenGate() {
  const [value, setValue] = useState('');
  return (
    <div className="flex min-h-svh items-center justify-center bg-background p-6">
      <form
        className="grid w-full max-w-md gap-4 rounded-2xl border bg-card p-6 shadow-sm"
        onSubmit={(e) => {
          e.preventDefault();
          setToken(value);
          location.reload();
        }}
      >
        <Brand />
        <div className="grid gap-1">
          <h1 className="text-lg font-semibold">This daemon asks for its token</h1>
          <p className="text-sm text-muted-foreground">
            Open the console with <code className="font-mono">qoqi ui</code>, which gives it, or paste the token it wrote next to its store (
            <code className="font-mono">flows.db.daemon</code>).
          </p>
        </div>
        <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="token" className="font-mono" autoFocus />
        <Button type="submit" disabled={!value.trim()}>
          <KeyRound /> Open the console
        </Button>
      </form>
    </div>
  );
}
