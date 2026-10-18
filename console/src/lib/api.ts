import type {
  AuthState,
  Checked,
  Data,
  DeployView,
  Detail,
  Diff,
  DraftDeploy,
  DryRun,
  FlowCounts,
  Line,
  Opened,
  Program,
  Report,
  ResumePlan,
  Source,
  Stats,
  User,
  View,
  WorkspaceProgram,
} from './types';

// Everything the console shows comes from the daemon's API. A person
// signs in, and their browser holds the session in a cookie the page
// never reads. The daemon's token is for the commands and the programs:
// the console has no use for it.

export function headers(): Record<string, string> {
  // Says the request is the console's: a page of another site cannot send it
  return { 'Content-Type': 'application/json', 'X-Qoqi-Console': '1' };
}

/** A refusal of the daemon: its status, its code, and what it says. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly data?: unknown,
  ) {
    super(message);
  }
}

/** Told when a request is refused for want of a session: the console asks to sign in again. */
let onSignedOut: () => void = () => {};
export function whenSignedOut(listener: () => void) {
  onSignedOut = listener;
}

export async function call<T>(method: string, path: string, body?: unknown, accept: number[] = []): Promise<{ status: number; data: T }> {
  const response = await fetch(path, {
    method,
    headers: headers(),
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (response.status >= 400 && !accept.includes(response.status)) {
    const refusal = (data ?? {}) as { error?: string; message?: string };
    // Signed out, or a password to change first: the console asks who is there again
    if ((response.status === 401 || (data as { error?: string })?.error === 'renew') && !path.startsWith('/api/auth')) onSignedOut();
    throw new ApiError(response.status, refusal.error ?? 'http', refusal.message ?? `the daemon answered ${response.status}`, data);
  }
  return { status: response.status, data: data as T };
}

const at = (id: string) => '/api/instances/' + encodeURIComponent(id);
const workspaceOf = (program: string) => '/api/workspace/' + encodeURIComponent(program);

export const api = {
  // Who is there
  auth: () => call<AuthState>('GET', '/api/auth').then((r) => r.data),
  login: (name: string, password: string) => call('POST', '/api/auth/login', { name, password }),
  logout: () => call('POST', '/api/auth/logout'),
  users: () => call<User[]>('GET', '/api/users').then((r) => r.data ?? []),
  addUser: (name: string, password: string) => call<User>('POST', '/api/users', { name, password }),
  removeUser: (name: string) => call('DELETE', '/api/users/' + encodeURIComponent(name)),
  password: (name: string, password: string, current?: string) =>
    call('POST', '/api/users/' + encodeURIComponent(name) + '/password', { password, current }),
  prefs: (prefs: Record<string, unknown>) => call('PUT', '/api/me/prefs', prefs),

  // What runs
  program: () => call<Program>('GET', '/api/program').then((r) => r.data),
  flows: () => call<FlowCounts[] | null>('GET', '/api/flows').then((r) => r.data ?? []),
  instances: (flow = '', status = '') => {
    const query = new URLSearchParams();
    if (flow) query.set('flow', flow);
    if (status) query.set('status', status);
    return call<View[]>('GET', '/api/instances?' + query.toString()).then((r) => r.data ?? []);
  },
  instance: (id: string) => call<Detail>('GET', at(id)).then((r) => r.data),
  source: (file: string) => call<Source>('GET', '/api/source?file=' + encodeURIComponent(file)).then((r) => r.data),
  output: (instance = '') => call<Line[]>('GET', '/api/output?instance=' + encodeURIComponent(instance)).then((r) => r.data ?? []),
  start: (flow: string, id: string, fields: Data) => call<View>('POST', '/api/instances', { flow, id, fields }),
  check: (flow: string, id: string, fields: Data) => call<DryRun>('POST', '/api/instances?dry=1', { flow, id, fields }).then((r) => r.data),
  cancel: (id: string, reason: string) => call<View>('POST', at(id) + '/cancel', { reason }).then((r) => r.data),
  plan: (id: string) => call<ResumePlan>('POST', at(id) + '/resume', { dry: true }).then((r) => r.data),
  resume: (id: string) => call<View>('POST', at(id) + '/resume', {}).then((r) => r.data),
  answer: (id: string, key: string, value: unknown, nothing: boolean) =>
    call<{ answered: boolean }>('POST', at(id) + '/answer', nothing ? { key, nothing: true } : { key, value }).then((r) => r.data),
  end: (id: string, reason: string) => call<View>('POST', at(id) + '/end', { reason }).then((r) => r.data),
  send: (id: string, inbox: string, message: Data, key: string) =>
    call<{ sent: 'delivered' | 'already' }>('POST', at(id) + '/inboxes/' + encodeURIComponent(inbox), key ? { message, key } : { message }).then(
      (r) => r.data,
    ),

  // What was deployed
  deployments: () => call<DeployView[]>('GET', '/api/deployments').then((r) => r.data ?? []),
  diff: (seq: number, from?: number) => call<Diff>('GET', `/api/deployments/${seq}/diff` + (from ? `?from=${from}` : '')).then((r) => r.data),
  undeploy: (program: string) => call('POST', '/api/undeploy', { program }),
  redeploy: (seq: number, dry: boolean) => call<Report>('POST', `/api/deployments/${seq}/redeploy` + (dry ? '?dry=1' : '')).then((r) => r.data),

  // The editor
  workspace: () => call<WorkspaceProgram[]>('GET', '/api/workspace').then((r) => r.data ?? []),
  open: (program: string) => call<Opened>('GET', workspaceOf(program)).then((r) => r.data),
  saveFile: (program: string, file: string, text: string) => call('PUT', workspaceOf(program) + '/files/' + file, { text }),
  removeFile: (program: string, file: string) => call('DELETE', workspaceOf(program) + '/files/' + file),
  discard: (program: string) => call('DELETE', workspaceOf(program)),
  checkDraft: (program: string, files: Record<string, string>) =>
    call<Checked>('POST', workspaceOf(program) + '/check', { files }).then((r) => r.data),
  deployDraft: (program: string, files: Record<string, string>, dry: boolean) =>
    call<DraftDeploy>('POST', workspaceOf(program) + '/deploy' + (dry ? '?dry=1' : ''), { files }, [409, 422]),
  format: (text: string) => call<{ text: string }>('POST', '/api/format', { text }).then((r) => r.data.text),

  // What happened
  stats: (days: number, flow = '') => {
    const query = new URLSearchParams({ days: String(days), tz: String(-new Date().getTimezoneOffset()) });
    if (flow) query.set('flow', flow);
    return call<Stats>('GET', '/api/stats?' + query.toString()).then((r) => r.data);
  },
};
