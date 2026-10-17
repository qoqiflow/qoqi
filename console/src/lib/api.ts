import type { Data, DeployView, Detail, Diff, DryRun, FlowCounts, Line, Program, ResumePlan, Source, View } from './types';

// Everything the console shows comes from the daemon's API, with the
// token `qoqi ui` gives in the address after #token=. It is kept for
// this tab, and never sent but in the Authorization header.

const tokenKey = 'qoqi-token';

export function readToken(): string {
  const hash = new URLSearchParams(location.hash.slice(1));
  const given = hash.get('token');
  try {
    if (given) {
      sessionStorage.setItem(tokenKey, given);
      history.replaceState(null, '', location.pathname + location.search);
    }
    return given || sessionStorage.getItem(tokenKey) || '';
  } catch {
    return given || '';
  }
}

let token = readToken();

// A token given again in the address, by `qoqi ui` in a tab that is
// open already, is the one used from then on
window.addEventListener('hashchange', () => {
  if (new URLSearchParams(location.hash.slice(1)).has('token')) {
    token = readToken();
    location.reload();
  }
});

export function currentToken(): string {
  return token;
}

export function setToken(next: string) {
  token = next.trim();
  try {
    sessionStorage.setItem(tokenKey, token);
  } catch {
    // Kept in memory only
  }
}

/** A refusal of the daemon: its status, its code, and what it says. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export async function call<T>(method: string, path: string, body?: unknown): Promise<{ status: number; data: T }> {
  const response = await fetch(path, {
    method,
    headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await response.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (response.status >= 400) {
    const refusal = (data ?? {}) as { error?: string; message?: string };
    throw new ApiError(response.status, refusal.error ?? 'http', refusal.message ?? `the daemon answered ${response.status}`);
  }
  return { status: response.status, data: data as T };
}

export const api = {
  program: () => call<Program>('GET', '/api/program').then((r) => r.data),
  flows: () => call<FlowCounts[] | null>('GET', '/api/flows').then((r) => r.data ?? []),
  instances: (flow = '', status = '') => {
    const query = new URLSearchParams();
    if (flow) query.set('flow', flow);
    if (status) query.set('status', status);
    return call<View[]>('GET', '/api/instances?' + query.toString()).then((r) => r.data ?? []);
  },
  instance: (id: string) => call<Detail>('GET', '/api/instances/' + encodeURIComponent(id)).then((r) => r.data),
  source: (file: string) => call<Source>('GET', '/api/source?file=' + encodeURIComponent(file)).then((r) => r.data),
  output: (instance = '') => call<Line[]>('GET', '/api/output?instance=' + encodeURIComponent(instance)).then((r) => r.data ?? []),
  start: (flow: string, id: string, fields: Data) => call<View>('POST', '/api/instances', { flow, id, fields }),
  check: (flow: string, id: string, fields: Data) =>
    call<DryRun>('POST', '/api/instances?dry=1', { flow, id, fields }).then((r) => r.data),
  cancel: (id: string, reason: string) =>
    call<View>('POST', '/api/instances/' + encodeURIComponent(id) + '/cancel', { reason }).then((r) => r.data),
  deployments: () => call<DeployView[]>('GET', '/api/deployments').then((r) => r.data ?? []),
  diff: (seq: number, from?: number) =>
    call<Diff>('GET', `/api/deployments/${seq}/diff` + (from ? `?from=${from}` : '')).then((r) => r.data),
  plan: (id: string) => call<ResumePlan>('POST', '/api/instances/' + encodeURIComponent(id) + '/resume', { dry: true }).then((r) => r.data),
  resume: (id: string) => call<View>('POST', '/api/instances/' + encodeURIComponent(id) + '/resume', {}).then((r) => r.data),
  end: (id: string, reason: string) =>
    call<View>('POST', '/api/instances/' + encodeURIComponent(id) + '/end', { reason }).then((r) => r.data),
  send: (id: string, inbox: string, message: Data, key: string) =>
    call<{ sent: 'delivered' | 'already' }>(
      'POST',
      '/api/instances/' + encodeURIComponent(id) + '/inboxes/' + encodeURIComponent(inbox),
      key ? { message, key } : { message },
    ).then((r) => r.data),
};
