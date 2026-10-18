import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  AlertTriangle,
  Check,
  CircleX,
  FileCode2,
  FileJson,
  FilePlus2,
  FlaskConical,
  Info,
  Loader2,
  PlayCircle,
  Rocket,
  Sparkles,
  Trash2,
  Undo2,
  Workflow,
} from 'lucide-react';
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
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useAuth } from '@/auth/auth';
import { api, ApiError } from '@/lib/api';
import { useWorkspace } from '@/lib/queries';
import { relative, useNow } from '@/lib/time';
import type { Checked, DraftDeploy, Problem, TestRun, WorkspaceProgram } from '@/lib/types';
import { cn } from '@/lib/utils';
import { CodeEditor, type CodeEditorHandle } from './code-editor';
import { flowNameOf, templates } from './templates';
import { firstTest } from './first-test';
import { TestsPanel } from './tests-panel';
import { TryPanel } from './try-panel';

// The editor: the programs on the left, those deployed and those being
// written; the files of the one open, its code, and what the compiler
// says of it as one types. What is written is kept as a draft as it is
// written; deploying it checks it against the version served, says what
// it would change, and serves it.

export function EditorPage() {
  const { program } = useParams();
  const [params, setParams] = useSearchParams();
  const creating = params.get('new') === '1';
  const workspace = useWorkspace();

  return (
    <div className="grid h-[calc(100svh-3.6rem)] min-h-0 grid-cols-1 md:grid-cols-[15rem_minmax(0,1fr)]">
      <aside className="hidden min-h-0 flex-col border-r bg-card/40 md:flex">
        <div className="flex items-center justify-between border-b px-3 py-2.5">
          <span className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Programs</span>
          <Button size="sm" variant="ghost" onClick={() => setParams({ new: '1' })}>
            <FilePlus2 /> New
          </Button>
        </div>
        <ProgramList programs={workspace.data ?? []} loading={workspace.isLoading} current={program} />
      </aside>
      <section className="flex min-h-0 min-w-0 flex-col">
        {program ? (
          <Workbench key={program} program={program} />
        ) : (
          <Welcome programs={workspace.data ?? []} onNew={() => setParams({ new: '1' })} />
        )}
      </section>
      <NewProgram open={creating} onClose={() => setParams({}, { replace: true })} taken={(workspace.data ?? []).map((p) => p.program)} />
    </div>
  );
}

function ProgramList({ programs, loading, current }: { programs: WorkspaceProgram[]; loading: boolean; current?: string }) {
  const now = useNow(30_000);
  if (loading) return <Skeleton className="m-3 h-24" />;
  if (programs.length === 0) return <p className="p-4 text-sm text-muted-foreground">No program yet.</p>;
  return (
    <ul className="min-h-0 flex-1 overflow-y-auto p-2">
      {programs.map((p) => (
        <li key={p.program}>
          <Link
            to={`/editor/${encodeURIComponent(p.program)}`}
            className={cn('grid gap-0.5 rounded-lg px-2.5 py-2 text-sm', current === p.program ? 'bg-accent' : 'hover:bg-accent/60')}
          >
            <span className="flex items-center gap-2">
              <Workflow className="size-3.5 text-primary" />
              <span className="truncate font-mono font-medium">{p.program}</span>
              {p.draft && (
                <span className="ml-auto rounded bg-warning/15 px-1.5 text-[0.6875rem] font-medium text-warning" title="Changed here, not deployed">
                  draft
                </span>
              )}
            </span>
            <span className="truncate pl-5.5 text-xs text-muted-foreground">
              {p.deployed ? 'deployed' : 'never deployed'}
              {p.updated ? ` · ${relative(p.updated, Math.max(now, p.updated))}` : ''}
              {p.draft && p.author ? ` · ${p.author}` : ''}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  );
}

function Welcome({ programs, onNew }: { programs: WorkspaceProgram[]; onNew: () => void }) {
  return (
    <div className="flex flex-1 items-center justify-center p-6">
      <div className="grid max-w-md gap-4 text-center">
        <FileCode2 className="mx-auto size-10 text-primary" />
        <h1 className="text-xl font-semibold">Write a program, deploy it from here</h1>
        <p className="text-sm text-muted-foreground">
          Open one of those the daemon runs to change it, or start a new one. What you write is kept as a draft until it is deployed; the compiler checks it as
          you type.
        </p>
        <div className="flex flex-wrap justify-center gap-2">
          <Button onClick={onNew}>
            <FilePlus2 /> New program
          </Button>
        </div>
        {programs.length > 0 && (
          <ul className="flex flex-wrap justify-center gap-2 md:hidden">
            {programs.map((p) => (
              <li key={p.program}>
                <Button size="sm" variant="outline" asChild>
                  <Link to={`/editor/${encodeURIComponent(p.program)}`}>{p.program}</Link>
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

const programName = /^[a-z][a-z0-9_]{0,63}$/;

function NewProgram({ open, onClose, taken }: { open: boolean; onClose: () => void; taken: string[] }) {
  const [name, setName] = useState('');
  const [template, setTemplate] = useState(templates[0].id);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const client = useQueryClient();
  const problem = !name ? '' : !programName.test(name) ? 'a lower case letter, then letters, digits and _' : taken.includes(name) ? 'there is a program of that name' : '';

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>A new program</DialogTitle>
          <DialogDescription>Its flows start in {name || 'name'}.qi. It is a draft until you deploy it.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            try {
              const t = templates.find((x) => x.id === template)!;
              await api.saveFile(name, `${name}.qi`, t.text(flowNameOf(name)));
              await client.invalidateQueries({ queryKey: ['workspace'] });
              onClose();
              setName('');
              navigate(`/editor/${name}`);
            } catch (err) {
              toast.error((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <div className="grid gap-1.5">
            <Label htmlFor="program-name">Name</Label>
            <Input id="program-name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="orders" className="font-mono" autoFocus />
            <p className={cn('text-xs', problem ? 'text-destructive' : 'text-muted-foreground')}>
              {problem || (name ? `Its first flow: ${flowNameOf(name)}` : 'Lower case letters, digits and _')}
            </p>
          </div>
          <fieldset className="grid gap-2">
            <legend className="mb-1 text-sm font-medium">Start from</legend>
            {templates.map((t) => (
              <label
                key={t.id}
                className={cn('flex cursor-pointer gap-3 rounded-lg border p-3 text-sm', template === t.id ? 'border-primary bg-primary/5' : 'hover:bg-accent/50')}
              >
                <input type="radio" name="template" value={t.id} checked={template === t.id} onChange={() => setTemplate(t.id)} className="mt-1 accent-[var(--primary)]" />
                <span className="grid gap-0.5">
                  <span className="font-medium">{t.title}</span>
                  <span className="text-xs text-muted-foreground">{t.hint}</span>
                </span>
              </label>
            ))}
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !name || !!problem}>
              <FilePlus2 /> Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

type Saving = 'saved' | 'saving' | 'unsaved' | 'failed';

/** A program open: its files, the one being written, what the compiler says. */
function Workbench({ program }: { program: string }) {
  const client = useQueryClient();
  const { prefs } = useAuth();
  const opened = useQuery({ queryKey: ['draft', program], queryFn: () => api.open(program), staleTime: Infinity, retry: false });
  const [buffers, setBuffers] = useState<Record<string, string>>({});
  const [saved, setSaved] = useState<Record<string, string>>({});
  const [file, setFile] = useState('');
  const [checked, setChecked] = useState<Checked | null>(null);
  const [checking, setChecking] = useState(false);
  const [saving, setSaving] = useState<Saving>('saved');
  const [deploying, setDeploying] = useState(false);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [discarding, setDiscarding] = useState(false);
  const [drafted, setDrafted] = useState(false); // saved here since it was opened
  const [bottom, setBottom] = useState<'problems' | 'tests'>('problems');
  const [testRun, setTestRun] = useState<TestRun | null>(null);
  const [testing, setTesting] = useState(false);
  const [trying, setTrying] = useState(false);
  const editor = useRef<CodeEditorHandle>(null);
  const entry = `${program}.qi`;

  // What the daemon has, taken once: from then on the editor has the text
  const loaded = useRef(false);
  useEffect(() => {
    if (!opened.data || loaded.current) return;
    loaded.current = true;
    const files = Object.fromEntries(opened.data.files.map((f) => [f.file, f.text]));
    setBuffers(files);
    setSaved(files);
    setFile(files[entry] !== undefined ? entry : (opened.data.files[0]?.file ?? entry));
  }, [opened.data, entry]);

  const dirty = useMemo(() => Object.keys(buffers).filter((f) => buffers[f] !== saved[f]), [buffers, saved]);

  // Kept as it is written, a moment after the last key
  useEffect(() => {
    if (dirty.length === 0) return;
    setSaving('unsaved');
    const timer = setTimeout(() => save(), 900);
    return () => clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [buffers]);

  const save = useCallback(async () => {
    const changed = Object.keys(buffers).filter((f) => buffers[f] !== saved[f]);
    if (changed.length === 0) return;
    setSaving('saving');
    try {
      for (const f of changed) await api.saveFile(program, f, buffers[f]);
      setSaved((s) => ({ ...s, ...Object.fromEntries(changed.map((f) => [f, buffers[f]])) }));
      setSaving('saved');
      setDrafted(true);
      client.invalidateQueries({ queryKey: ['workspace'] });
    } catch (err) {
      setSaving('failed');
      toast.error('Not saved: ' + (err as Error).message);
    }
  }, [buffers, saved, program, client]);

  // Checked as it is written: what the editor has, saved or not
  useEffect(() => {
    if (!loaded.current || Object.keys(buffers).length === 0) return;
    let gone = false;
    const timer = setTimeout(async () => {
      setChecking(true);
      try {
        const result = await api.checkDraft(program, buffers);
        if (!gone) setChecked(result);
      } catch {
        // Said again at the next change
      } finally {
        if (!gone) setChecking(false);
      }
    }, 600);
    return () => {
      gone = true;
      clearTimeout(timer);
    };
  }, [buffers, program]);

  const format = useCallback(async () => {
    if (!file.endsWith('.qi')) return;
    try {
      const tidy = await api.format(buffers[file] ?? '');
      setBuffers((b) => ({ ...b, [file]: tidy }));
    } catch (err) {
      toast.error((err as Error).message);
    }
  }, [buffers, file]);

  const runTests = useCallback(async () => {
    setBottom('tests');
    setTesting(true);
    try {
      setTestRun(await api.testDraft(program, buffers));
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setTesting(false);
    }
  }, [program, buffers]);

  const goto = (f: string, line: number, column: number) => {
    setFile(f);
    // The file shown first, then the place in it
    setTimeout(() => editor.current?.goto(line, column), 0);
  };

  const problems = checked?.problems ?? [];
  const errors = problems.filter((p) => p.severity === 'error').length;
  const warnings = problems.length - errors;
  const files = Object.keys(buffers).sort((a, b) => (a === entry ? -1 : b === entry ? 1 : a.localeCompare(b)));
  const isDraft = !!opened.data?.draft || drafted || dirty.length > 0;

  if (opened.isLoading) return <Skeleton className="m-6 h-[60vh] rounded-xl" />;
  if (opened.isError) {
    return (
      <div className="p-6 text-sm text-muted-foreground">
        {(opened.error as ApiError).status === 404 ? `There is no program ${program}.` : (opened.error as Error).message}{' '}
        <Link to="/editor" className="font-medium text-primary hover:underline">
          The programs
        </Link>
      </div>
    );
  }

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 border-b px-3 py-2">
        <span className="font-mono text-sm font-semibold">{program}</span>
        <span className="text-xs text-muted-foreground">
          {opened.data?.deployed ? `serving ${opened.data.deployed.replace(/^sha256:/, '').slice(0, 8)}` : 'never deployed'}
          {isDraft && ' · draft'}
        </span>
        <SaveState saving={saving} />
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button size="sm" variant="ghost" onClick={format} disabled={!file.endsWith('.qi')}>
                <Sparkles /> Format
              </Button>
            </TooltipTrigger>
            <TooltipContent>⇧⌥F</TooltipContent>
          </Tooltip>
          <Button size="sm" variant="ghost" onClick={runTests} disabled={testing}>
            {testing ? <Loader2 className="animate-spin" /> : <FlaskConical />} Test
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setTrying(true)} disabled={!checked?.ok}>
            <PlayCircle /> Try
          </Button>
          {isDraft && opened.data?.deployed && (
            <Button size="sm" variant="ghost" onClick={() => setDiscarding(true)}>
              <Undo2 /> Discard draft
            </Button>
          )}
          <Button size="sm" onClick={() => setDeploying(true)} disabled={!checked?.ok || checking}>
            <Rocket /> Deploy
          </Button>
        </div>
      </div>

      <div className="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[12rem_minmax(0,1fr)]">
        <div className="hidden min-h-0 flex-col border-r lg:flex">
          <div className="flex items-center justify-between px-3 py-2">
            <span className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Files</span>
            <Button size="icon" variant="ghost" className="size-7" onClick={() => setAdding(true)} aria-label="A new file">
              <FilePlus2 className="size-4" />
            </Button>
          </div>
          <ul className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
            {files.map((f) => {
              const count = problems.filter((p) => p.file === f && p.severity === 'error').length;
              return (
                <li key={f} className="group relative">
                  <button
                    type="button"
                    onClick={() => setFile(f)}
                    className={cn('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-[0.8125rem]', file === f ? 'bg-accent' : 'hover:bg-accent/60')}
                  >
                    {f.endsWith('.json') ? <FileJson className="size-3.5 shrink-0 text-muted-foreground" /> : <FileCode2 className="size-3.5 shrink-0 text-primary" />}
                    <span className="truncate">{f}</span>
                    {buffers[f] !== saved[f] && <span className="size-1.5 shrink-0 rounded-full bg-foreground/60" title="Not saved yet" />}
                    {count > 0 && <span className="ml-auto text-xs font-semibold text-destructive tabular-nums">{count}</span>}
                  </button>
                  {f !== entry && (
                    <button
                      type="button"
                      onClick={() => setRemoving(f)}
                      className="absolute top-1/2 right-1 hidden -translate-y-1/2 rounded p-1 text-muted-foreground group-hover:block hover:text-destructive"
                      aria-label={`Take ${f} away`}
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        </div>

        <div className="flex min-h-0 min-w-0 flex-col">
          {/* The files, as tabs, where the list does not fit */}
          <div className="flex gap-1 overflow-x-auto border-b px-2 py-1.5 lg:hidden">
            {files.map((f) => (
              <button
                key={f}
                type="button"
                onClick={() => setFile(f)}
                className={cn('shrink-0 rounded-md px-2 py-1 font-mono text-xs', file === f ? 'bg-accent' : 'text-muted-foreground')}
              >
                {f}
              </button>
            ))}
            <button type="button" onClick={() => setAdding(true)} className="shrink-0 rounded-md px-2 py-1 text-xs text-primary">
              + file
            </button>
          </div>
          <div className="min-h-0 flex-1">
            {file && buffers[file] !== undefined && (
              <CodeEditor
                ref={editor}
                path={file}
                value={buffers[file]}
                onChange={(text) => setBuffers((b) => (b[file] === text ? b : { ...b, [file]: text }))}
                problems={problems.filter((p) => p.file === file)}
                fontSize={prefs.editorFontSize ?? 13}
                wrap={prefs.editorWrap ?? false}
                onSave={save}
                onFormat={format}
              />
            )}
          </div>
          <Problems
            problems={problems}
            checking={checking}
            checked={checked}
            errors={errors}
            warnings={warnings}
            onPick={(p) => goto(p.file, p.line, p.column)}
            tab={bottom}
            onTab={setBottom}
            testsState={
              testRun &&
              !testing && (
                <span className={cn('font-medium normal-case', testRun.ok ? 'text-success' : 'text-destructive')}>
                  {testRun.passed} passed{testRun.failed > 0 && `, ${testRun.failed} failed`}
                </span>
              )
            }
            tests={
              <TestsPanel
                run={testRun}
                running={testing}
                hasTests={files.some((f) => f.endsWith('_test.qi'))}
                onRun={runTests}
                onNew={() => {
                  const f = `${program}_test.qi`;
                  setBuffers((b) => ({ ...b, [f]: b[f] ?? firstTest(program, checked?.flows[0] ?? flowNameOf(program), checked?.schema?.flows[0]) }));
                  setFile(f);
                }}
                onPick={(f, line) => goto(f, line, 1)}
              />
            }
          />
        </div>
      </div>

      <TryPanel open={trying} onOpenChange={setTrying} program={program} flows={checked?.flows ?? []} schema={checked?.schema} files={buffers} />
      <DeployDialog
        open={deploying}
        onClose={() => setDeploying(false)}
        program={program}
        files={buffers}
        onDeployed={async () => {
          setSaved(buffers);
          setDrafted(false);
          loaded.current = false;
          await Promise.all([
            client.invalidateQueries({ queryKey: ['draft', program] }),
            client.invalidateQueries({ queryKey: ['workspace'] }),
            client.invalidateQueries({ queryKey: ['program'] }),
            client.invalidateQueries({ queryKey: ['deployments'] }),
          ]);
        }}
      />
      <NewFile
        open={adding}
        onClose={() => setAdding(false)}
        taken={files}
        onCreate={(f) => {
          setBuffers((b) => ({ ...b, [f]: f.endsWith('.json') ? '{\n  "queues": {}\n}\n' : `// ${f}: a module of ${program}, imported by its path\n` }));
          setFile(f);
        }}
      />
      <AlertDialog open={removing !== null} onOpenChange={(o) => !o && setRemoving(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Take {removing} away?</AlertDialogTitle>
            <AlertDialogDescription>From the draft. The version deployed keeps it until another is deployed.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep it</AlertDialogCancel>
            <AlertDialogAction
              onClick={async () => {
                const f = removing!;
                setRemoving(null);
                try {
                  if (saved[f] !== undefined) await api.removeFile(program, f);
                  const drop = <T,>(m: Record<string, T>) => Object.fromEntries(Object.entries(m).filter(([k]) => k !== f));
                  setBuffers(drop);
                  setSaved(drop);
                  if (file === f) setFile(entry);
                  client.invalidateQueries({ queryKey: ['workspace'] });
                } catch (err) {
                  toast.error((err as Error).message);
                }
              }}
            >
              Take it away
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog open={discarding} onOpenChange={setDiscarding}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Discard the draft of {program}?</AlertDialogTitle>
            <AlertDialogDescription>What was written here and not deployed goes; the program is what was deployed of it.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep the draft</AlertDialogCancel>
            <AlertDialogAction
              onClick={async () => {
                await api.discard(program);
                setDrafted(false);
                loaded.current = false;
                await client.invalidateQueries({ queryKey: ['draft', program] });
                client.invalidateQueries({ queryKey: ['workspace'] });
              }}
            >
              Discard
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function SaveState({ saving }: { saving: Saving }) {
  const text = { saved: 'saved', saving: 'saving…', unsaved: 'editing', failed: 'not saved' }[saving];
  return <span className={cn('text-xs', saving === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>· {text}</span>;
}

const fileName = /^([a-z0-9_]+\/)*[a-z0-9_]+\.qi$/;

function NewFile({ open, onClose, taken, onCreate }: { open: boolean; onClose: () => void; taken: string[]; onCreate: (file: string) => void }) {
  const [name, setName] = useState('');
  const file = name && !name.includes('.') ? `${name}.qi` : name;
  const problem = !name ? '' : taken.includes(file) ? 'there is a file of that name' : file !== 'qoqi.json' && !fileName.test(file) ? 'lower case letters, digits, _ and / for folders' : '';
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>A new file</DialogTitle>
          <DialogDescription>A module, imported by its path: billing/rates.qi is import billing.rates. Or qoqi.json, for the queues of its steps.</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            onCreate(file);
            setName('');
            onClose();
          }}
        >
          <Input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="billing/rates.qi" className="font-mono" autoFocus />
          {problem && <p className="text-xs text-destructive">{problem}</p>}
          <DialogFooter>
            <Button type="submit" disabled={!name || !!problem}>
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ProblemIcon({ severity }: { severity: string }) {
  if (severity === 'error') return <CircleX className="size-3.5 shrink-0 text-destructive" />;
  if (severity === 'warning') return <AlertTriangle className="size-3.5 shrink-0 text-warning" />;
  return <Info className="size-3.5 shrink-0 text-muted-foreground" />;
}

function Problems({
  problems,
  checking,
  checked,
  errors,
  warnings,
  onPick,
  tab,
  onTab,
  tests,
  testsState,
}: {
  problems: Problem[];
  checking: boolean;
  checked: Checked | null;
  errors: number;
  warnings: number;
  onPick: (p: Problem) => void;
  tab: 'problems' | 'tests';
  onTab: (tab: 'problems' | 'tests') => void;
  tests: React.ReactNode;
  testsState: React.ReactNode;
}) {
  const open = true;
  return (
    <div className="border-t bg-card/40">
      <div className="flex w-full items-center gap-3 px-3 py-1.5 text-xs">
        {(['problems', 'tests'] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => onTab(t)}
            className={cn('font-semibold tracking-wide uppercase', tab === t ? 'text-foreground' : 'text-muted-foreground hover:text-foreground')}
          >
            {t}
          </button>
        ))}
        {tab === 'tests' && testsState}
        {tab === 'problems' && checking ? (
          <span className="inline-flex items-center gap-1 text-muted-foreground">
            <Loader2 className="size-3 animate-spin" /> checking
          </span>
        ) : tab === 'problems' && checked?.ok ? (
          <span className="inline-flex items-center gap-1 text-success">
            <Check className="size-3.5" /> compiles · {checked.flows.join(', ')}
          </span>
        ) : null}
        <span className="ml-auto inline-flex items-center gap-3 tabular-nums">
          <span className="inline-flex items-center gap-1">
            <CircleX className="size-3.5 text-destructive" /> {errors}
          </span>
          <span className="inline-flex items-center gap-1">
            <AlertTriangle className="size-3.5 text-warning" /> {warnings}
          </span>
        </span>
      </div>
      {tab === 'tests' && tests}
      {tab === 'problems' && open && problems.length > 0 && (
        <ul className="max-h-40 overflow-y-auto border-t py-1">
          {problems.map((p, i) => (
            <li key={i}>
              <button type="button" onClick={() => onPick(p)} className="flex w-full items-start gap-2 px-3 py-1 text-left text-[0.8125rem] hover:bg-accent/60">
                <span className="pt-0.5">
                  <ProblemIcon severity={p.severity} />
                </span>
                <span className="min-w-0 flex-1">
                  {p.message}
                  {p.suggestion && <span className="text-muted-foreground"> — {p.suggestion}</span>}
                </span>
                <span className="shrink-0 font-mono text-xs text-muted-foreground">
                  {p.file}:{p.line}:{p.column}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** Deploying: what it would do first, then doing it. */
function DeployDialog({
  open,
  onClose,
  program,
  files,
  onDeployed,
}: {
  open: boolean;
  onClose: () => void;
  program: string;
  files: Record<string, string>;
  onDeployed: () => Promise<void>;
}) {
  const [plan, setPlan] = useState<DraftDeploy | null>(null);
  const [status, setStatus] = useState(0);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    if (!open) return;
    setPlan(null);
    api
      .deployDraft(program, files, true)
      .then((r) => {
        setPlan(r.data);
        setStatus(r.status);
      })
      .catch((err) => {
        setPlan({ ok: false, flows: [], problems: [], why: [(err as Error).message] });
        setStatus(500);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const report = plan?.report;
  const can = status === 200 && report && !report.same;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Deploy {program}</DialogTitle>
          <DialogDescription>Checked against the version the daemon serves: nothing is served until you say so.</DialogDescription>
        </DialogHeader>
        {!plan ? (
          <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> What it would do…
          </div>
        ) : (
          <div className="grid gap-3 text-sm">
            {report && (
              <p>
                {report.new
                  ? `A new program: the daemon starts to serve ${plan.flows.join(', ')}.`
                  : report.same
                    ? 'This is the version served already: there is nothing to deploy.'
                    : `In place of version ${report.before?.replace(/^sha256:/, '').slice(0, 8)}: ${report.moving} instance${report.moving === 1 ? '' : 's'} alive move${report.moving === 1 ? 's' : ''} to it at their next stage.`}
              </p>
            )}
            {(report?.changes?.length ?? 0) > 0 && (
              <div className="grid gap-1">
                <span className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">What it changes of the stages</span>
                <ul className="grid gap-1 rounded-lg bg-muted p-3 font-mono text-xs">
                  {report!.changes!.map((c) => (
                    <li key={c}>{c}</li>
                  ))}
                </ul>
              </div>
            )}
            {(report?.said?.length ?? 0) > 0 && (
              <ul className="grid gap-1 text-xs text-muted-foreground">
                {report!.said!.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            )}
            {(plan.why?.length ?? 0) > 0 && (
              <div className="grid gap-1 rounded-lg border border-destructive/40 bg-destructive/5 p-3">
                <span className="font-medium text-destructive">The daemon refuses it</span>
                <ul className="grid gap-1 text-xs">
                  {plan.why!.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </div>
            )}
            {!plan.ok && plan.problems.filter((p) => p.severity === 'error').length > 0 && (
              <p className="text-destructive">It does not compile: see the problems.</p>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={!can || busy}
            onClick={async () => {
              setBusy(true);
              try {
                const r = await api.deployDraft(program, files, false);
                if (r.status !== 200) {
                  setPlan(r.data);
                  setStatus(r.status);
                  return;
                }
                await onDeployed();
                onClose();
                toast.success(`${program} is deployed`, {
                  action: r.data.flows[0] ? { label: 'See it', onClick: () => navigate(`/flows/${r.data.flows[0]}`) } : undefined,
                });
              } catch (err) {
                toast.error((err as Error).message);
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? <Loader2 className="animate-spin" /> : <Rocket />} Deploy
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
