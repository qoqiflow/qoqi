import { Link, useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, Code2, Play, Workflow } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { CodeView } from '@/components/code';
import { StatusDot } from '@/components/status';
import { ActivityOf } from '@/features/activity/activity';
import { InstanceList } from '@/features/instances/instances';
import { useLaunch } from '@/features/instances/launch';
import { emptyCounts, useCounts, useFlowPrograms, useInstances, useProgram } from '@/lib/queries';
import { statuses } from '@/lib/types';
import { FieldList, FlowDetail } from './reference';

// One flow: what it is, its instances, what it did over the year, and
// its code; the tab in the address.

const tabs = ['overview', 'instances', 'activity', 'code'] as const;

export function FlowPage() {
  const { name = '', tab = 'overview' } = useParams();
  const navigate = useNavigate();
  const program = useProgram();
  const launch = useLaunch();
  const programs = useFlowPrograms();
  const { byFlow } = useCounts(useInstances().data);

  if (program.isLoading) return <Skeleton className="h-96 rounded-xl" />;
  const flow = program.data?.flows.find((f) => f.name === name);
  if (!flow) {
    return (
      <p className="rounded-xl border bg-card p-6 text-sm text-muted-foreground">
        The daemon runs no flow {name}. <Link to="/flows" className="font-medium text-primary hover:underline">Every flow</Link>
      </p>
    );
  }
  const deployed = programs.get(flow.name);
  const counts = byFlow.get(flow.name) ?? emptyCounts();
  const current = (tabs as readonly string[]).includes(tab) ? tab : 'overview';

  return (
    <div className="grid gap-6">
      <div className="grid gap-3">
        <Link to="/flows" className="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ChevronLeft className="size-4" /> Flows
        </Link>
        <div className="flex flex-wrap items-end justify-between gap-4">
          <div className="grid gap-1">
            <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
              <Workflow className="size-5 text-primary" /> {flow.name}
            </h1>
            <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
              <span className="font-mono">Flow&lt;{flow.returns.text}&gt;</span>
              {deployed && (
                <span>
                  of <span className="font-mono text-foreground">{deployed.program}</span>, version {deployed.version.replace(/^sha256:/, '').slice(0, 8)}
                </span>
              )}
              {statuses
                .filter((s) => counts[s] > 0)
                .map((s) => (
                  <span key={s} className="inline-flex items-center gap-1.5">
                    <StatusDot status={s} /> <span className="tabular-nums">{counts[s]}</span> {s}
                  </span>
                ))}
            </p>
          </div>
          <div className="flex gap-2">
            {deployed && (
              <Button variant="outline" asChild>
                <Link to={`/editor/${encodeURIComponent(deployed.program)}`}>
                  <Code2 /> Edit
                </Link>
              </Button>
            )}
            <Button onClick={() => launch({ flow: flow.name })}>
              <Play /> Start {flow.name}
            </Button>
          </div>
        </div>
      </div>

      <Tabs value={current} onValueChange={(t) => navigate(`/flows/${encodeURIComponent(flow.name)}${t === 'overview' ? '' : '/' + t}`, { replace: true })}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="instances">Instances</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
          <TabsTrigger value="code">Code</TabsTrigger>
        </TabsList>
        <TabsContent value="overview" className="mt-5">
          <FlowDetail flow={flow} />
          {(flow.inboxes?.length ?? 0) > 0 && (
            <section className="mt-6 grid gap-2 rounded-xl border bg-card p-5">
              <h2 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Its inboxes</h2>
              <FieldList fields={flow.inboxes} />
            </section>
          )}
        </TabsContent>
        <TabsContent value="instances" className="mt-5">
          <InstanceList only={flow.name} />
        </TabsContent>
        <TabsContent value="activity" className="mt-5">
          <ActivityOf flow={flow.name} />
        </TabsContent>
        <TabsContent value="code" className="mt-5">
          {flow.at ? (
            <CodeView file={flow.at.file} notes={[{ line: flow.at.line, kind: 'mark', gutter: '◆', text: flow.name }]} />
          ) : (
            <p className="text-sm text-muted-foreground">The daemon does not say where {flow.name} is written.</p>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}
