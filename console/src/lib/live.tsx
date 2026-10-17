import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { createContext, useContext, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { currentToken } from './api';
import type { Event, View } from './types';

// The console stays current by reading GET /api/events, a stream of what
// happens: an instance that changed is written into what the page holds,
// at once, and the page of an instance asks again for its detail. Read
// with fetch rather than EventSource, so that the token goes in a header.

export type Connection = 'connecting' | 'live' | 'offline' | 'refused';

const ConnectionContext = createContext<Connection>('connecting');

/** The last things that happened, newest first, for the activity feeds. */
class Feed {
  private events: Event[] = [];
  private listeners = new Set<() => void>();

  push(event: Event) {
    this.events = [event, ...this.events].slice(0, 300);
    this.listeners.forEach((listener) => listener());
  }

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  snapshot = () => this.events;
}

export const feed = new Feed();

export function useFeed(): Event[] {
  return useSyncExternalStore(feed.subscribe, feed.snapshot);
}

export function useConnection(): Connection {
  return useContext(ConnectionContext);
}

function apply(client: QueryClient, event: Event, refresh: (id: string) => void) {
  if (event.kind === 'instance' && event.instance) {
    const changed = event.instance;
    client.setQueryData<View[]>(['instances'], (list) => {
      if (!list) return list;
      const at = list.findIndex((v) => v.id === changed.id);
      if (at < 0) return [changed, ...list];
      const next = list.slice();
      next[at] = changed;
      return next;
    });
  }
  if (event.kind === 'deploy') {
    // A new version: the program is what it says now
    client.invalidateQueries({ queryKey: ['program'] });
    client.invalidateQueries({ queryKey: ['deployments'] });
    client.invalidateQueries({ queryKey: ['source'] });
  }
  if (event.id) refresh(event.id);
  if (event.kind !== 'output' || event.text) feed.push(event);
}

export function LiveProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const [connection, setConnection] = useState<Connection>('connecting');
  const pending = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  useEffect(() => {
    let stopped = false;
    let controller: AbortController | null = null;
    let wait = 500;

    // The detail of an instance is asked for again once things settle,
    // not at each of the events a wave of steps makes
    const refresh = (id: string) => {
      const timers = pending.current;
      if (timers.has(id)) return;
      timers.set(
        id,
        setTimeout(() => {
          timers.delete(id);
          client.invalidateQueries({ queryKey: ['instance', id] });
        }, 150),
      );
    };

    const connect = async () => {
      while (!stopped) {
        controller = new AbortController();
        try {
          const response = await fetch('/api/events', {
            headers: { Authorization: 'Bearer ' + currentToken() },
            signal: controller.signal,
          });
          if (response.status === 401) {
            setConnection('refused');
            return;
          }
          if (!response.ok || !response.body) throw new Error(String(response.status));
          setConnection('live');
          wait = 500;
          // What changed while the stream was down
          client.invalidateQueries();
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = '';
          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            let end: number;
            while ((end = buffer.indexOf('\n\n')) >= 0) {
              const block = buffer.slice(0, end);
              buffer = buffer.slice(end + 2);
              for (const line of block.split('\n')) {
                if (line.startsWith('data: ')) {
                  try {
                    apply(client, JSON.parse(line.slice(6)) as Event, refresh);
                  } catch {
                    // An event this console cannot read is skipped
                  }
                }
              }
            }
          }
        } catch {
          if (stopped) return;
        }
        if (stopped) return;
        setConnection('offline');
        await new Promise((resolve) => setTimeout(resolve, wait));
        wait = Math.min(wait * 2, 2000);
      }
    };

    connect();
    return () => {
      stopped = true;
      controller?.abort();
      pending.current.forEach(clearTimeout);
      pending.current.clear();
    };
  }, [client]);

  return <ConnectionContext.Provider value={connection}>{children}</ConnectionContext.Provider>;
}
