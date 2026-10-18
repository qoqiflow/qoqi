import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { api } from './api';
import type { DeployView, Status, View } from './types';

export function useProgram() {
  return useQuery({ queryKey: ['program'], queryFn: api.program, staleTime: Infinity });
}

/** Every instance: the stream keeps the list current, so it is asked for once. */
/** The versions deployed, the last first: asked again at each deploy. */
export function useDeployments() {
  return useQuery({ queryKey: ['deployments'], queryFn: api.deployments });
}

export function useDiff(seq: number | undefined, from?: number) {
  return useQuery({ queryKey: ['diff', seq, from], queryFn: () => api.diff(seq!, from), enabled: seq !== undefined, staleTime: Infinity });
}

export function useInstances() {
  return useQuery({ queryKey: ['instances'], queryFn: () => api.instances(), staleTime: Infinity });
}

export function useInstance(id: string) {
  return useQuery({ queryKey: ['instance', id], queryFn: () => api.instance(id), enabled: id !== '' });
}

export function useSource(file: string | undefined) {
  return useQuery({
    queryKey: ['source', file],
    queryFn: () => api.source(file ?? ''),
    enabled: !!file,
    staleTime: Infinity,
  });
}

export type Counts = Record<Status, number>;

export function emptyCounts(): Counts {
  return { waiting: 0, running: 0, stopped: 0, done: 0, failed: 0 };
}

/** How many instances are in each status, for all flows and for each. */
export function useCounts(instances: View[] | undefined) {
  return useMemo(() => {
    const all = emptyCounts();
    const byFlow = new Map<string, Counts>();
    for (const inst of instances ?? []) {
      all[inst.status]++;
      if (!byFlow.has(inst.flow)) byFlow.set(inst.flow, emptyCounts());
      byFlow.get(inst.flow)![inst.status]++;
    }
    return { all, byFlow };
  }, [instances]);
}

/** The programs of the editor: deployed, or written here. */
export function useWorkspace() {
  return useQuery({ queryKey: ['workspace'], queryFn: api.workspace });
}

/** What happened over the last days, of a flow or of all. */
export function useStats(days: number, flow = '') {
  return useQuery({ queryKey: ['stats', days, flow], queryFn: () => api.stats(days, flow), refetchInterval: 60_000 });
}

export function useUsers() {
  return useQuery({ queryKey: ['users'], queryFn: api.users });
}

/** The program each flow is of, as the deploys say. */
export function useFlowPrograms() {
  const deployments = useDeployments();
  return useMemo(() => {
    const of = new Map<string, DeployView>();
    for (const d of deployments.data ?? []) {
      if (!d.serving) continue;
      for (const flow of d.flows ?? []) of.set(flow, d);
    }
    return of;
  }, [deployments.data]);
}
