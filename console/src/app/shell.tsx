import { useEffect, useMemo, useRef, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  BarChart3,
  Code2,
  GitBranch,
  LayoutDashboard,
  ListTree,
  LogOut,
  Menu,
  Monitor,
  Moon,
  Play,
  ScrollText,
  Search,
  Settings,
  Sun,
  UserRound,
  Workflow,
  type LucideIcon,
} from 'lucide-react';
import { Brand } from '@/components/brand';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet';
import { StatusDot } from '@/components/status';
import { useAuth } from '@/auth/auth';
import { useLaunch } from '@/features/instances/launch';
import { useConnection } from '@/lib/live';
import { useInstances, useProgram } from '@/lib/queries';
import { useTheme } from '@/lib/theme';
import { cn } from '@/lib/utils';

// The frame of every page: what there is to see on the left, in three
// parts (what runs, what is built, the console's own settings), and on
// top, finding an instance and starting a flow.

interface Item {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

const sections: { title?: string; items: Item[] }[] = [
  {
    items: [
      { to: '/', label: 'Overview', icon: LayoutDashboard, end: true },
      { to: '/flows', label: 'Flows', icon: Workflow },
      { to: '/instances', label: 'Instances', icon: ListTree },
      { to: '/activity', label: 'Activity', icon: BarChart3 },
      { to: '/logs', label: 'Logs', icon: ScrollText },
    ],
  },
  {
    title: 'Build',
    items: [
      { to: '/editor', label: 'Editor', icon: Code2 },
      { to: '/deployments', label: 'Deployments', icon: GitBranch },
    ],
  },
  {
    title: 'Console',
    items: [{ to: '/settings', label: 'Settings', icon: Settings }],
  },
];

function Nav({ onGo }: { onGo?: () => void }) {
  const stopped = (useInstances().data ?? []).filter((v) => v.status === 'stopped').length;
  return (
    <nav className="grid gap-5" aria-label="Sections">
      {sections.map((section, i) => (
        <div key={i} className="grid gap-0.5">
          {section.title && <span className="px-3 pb-1 text-[0.6875rem] font-semibold tracking-wide text-muted-foreground uppercase">{section.title}</span>}
          {section.items.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              onClick={onGo}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm font-medium transition-colors',
                  isActive ? 'bg-accent text-foreground' : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground',
                )
              }
            >
              <item.icon className="size-4" />
              {item.label}
              {item.to === '/instances' && stopped > 0 && (
                <span className="ml-auto rounded-full bg-destructive/12 px-1.5 text-xs font-semibold text-destructive tabular-nums" title={`${stopped} stopped`}>
                  {stopped}
                </span>
              )}
            </NavLink>
          ))}
        </div>
      ))}
    </nav>
  );
}

function Live() {
  const connection = useConnection();
  const label = { connecting: 'connecting', live: 'live', offline: 'reconnecting', refused: 'refused' }[connection];
  return (
    <span className="inline-flex items-center gap-2 text-xs text-muted-foreground" title="The console follows the daemon's events as they happen">
      <StatusDot status={connection === 'live' ? 'done' : connection === 'refused' ? 'stopped' : 'waiting'} pulse={connection === 'live'} />
      {label}
    </span>
  );
}

function UserMenu() {
  const { who, me, signOut } = useAuth();
  const { choice, choose } = useTheme();
  const navigate = useNavigate();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className="flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-accent/60">
          <span className="flex size-7 items-center justify-center rounded-full bg-primary/12 text-xs font-semibold text-primary uppercase">
            {me.user ? me.user.slice(0, 2) : <UserRound className="size-4" />}
          </span>
          <span className="grid min-w-0 flex-1">
            <span className="truncate font-medium">{who}</span>
            <span className="truncate text-xs text-muted-foreground">{me.user ? 'signed in' : "with the daemon's token"}</span>
          </span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="w-56">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        {(
          [
            ['light', Sun, 'Light'],
            ['dark', Moon, 'Dark'],
            ['system', Monitor, 'As the system'],
          ] as const
        ).map(([value, Icon, label]) => (
          <DropdownMenuItem key={value} onClick={() => choose(value)} className={cn(choice === value && 'font-medium text-foreground')}>
            <Icon /> {label}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => navigate('/settings')}>
          <Settings /> Settings
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => signOut()}>
          <LogOut /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Sidebar({ onGo }: { onGo?: () => void }) {
  return (
    <div className="flex h-full flex-col gap-6 p-3">
      <NavLink to="/" onClick={onGo} className="px-2 pt-1">
        <Brand />
      </NavLink>
      <Nav onGo={onGo} />
      <div className="mt-auto grid gap-3 border-t pt-3">
        <div className="px-3">
          <Live />
        </div>
        <UserMenu />
      </div>
    </div>
  );
}

/** Find an instance by its name, from anywhere: / or ⌘K puts the cursor there. */
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
      if ((e.key === '/' || (e.key === 'k' && (e.metaKey || e.ctrlKey))) && !target.closest('input, textarea, [contenteditable], .cm-editor')) {
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
    <div className="relative w-full max-w-sm">
      <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        ref={input}
        value={text}
        placeholder="Find an instance"
        className="h-9 pr-10 pl-9"
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

export function Shell() {
  const launch = useLaunch();
  const program = useProgram();
  const [drawer, setDrawer] = useState(false);
  const { prefs } = useAuth();
  const location = useLocation();
  // The editor takes the whole width, and the whole height
  const wide = location.pathname.startsWith('/editor');

  return (
    <div className={cn('min-h-svh bg-background text-foreground lg:grid lg:grid-cols-[15rem_minmax(0,1fr)]', prefs.density === 'compact' && 'density-compact')}>
      <aside className="sticky top-0 hidden h-svh border-r bg-card lg:block">
        <Sidebar />
      </aside>
      <Sheet open={drawer} onOpenChange={setDrawer}>
        <SheetContent side="left" className="w-64 p-0">
          <SheetTitle className="sr-only">Sections</SheetTitle>
          <Sidebar onGo={() => setDrawer(false)} />
        </SheetContent>
      </Sheet>
      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-40 border-b bg-background/85 backdrop-blur supports-[backdrop-filter]:bg-background/70">
          <div className="flex items-center gap-3 px-4 py-2.5 lg:px-8">
            <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Sections" onClick={() => setDrawer(true)}>
              <Menu />
            </Button>
            <QuickFind />
            <div className="ml-auto flex items-center gap-2">
              <Button onClick={() => launch()} disabled={!program.data || program.data.flows.length === 0}>
                <Play /> <span className="hidden sm:inline">Start a flow</span>
              </Button>
            </div>
          </div>
        </header>
        <main className={cn('w-full flex-1', wide ? 'px-0 py-0' : 'mx-auto max-w-[1400px] px-4 py-6 lg:px-8')}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}
