import { useEffect, useState } from 'react';

// Times as a person reads them: "in 3 s", "2 min ago", "18 Oct 14:00".

/** Now, again every period: what makes a countdown move. */
export function useNow(period = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), period);
    return () => clearInterval(timer);
  }, [period]);
  return now;
}

export function span(ms: number): string {
  const s = Math.abs(ms) / 1000;
  if (s < 1) return `${Math.round(Math.abs(ms))} ms`;
  if (s < 60) return `${Math.floor(s)} s`;
  const m = s / 60;
  if (m < 60) return `${Math.floor(m)} min${Math.floor(s % 60) && m < 10 ? ` ${Math.floor(s % 60)} s` : ''}`;
  const h = m / 60;
  if (h < 24) return `${Math.floor(h)} h${Math.floor(m % 60) ? ` ${Math.floor(m % 60)} min` : ''}`;
  const d = h / 24;
  return `${Math.floor(d)} d${Math.floor(h % 24) ? ` ${Math.floor(h % 24)} h` : ''}`;
}

/** A moment from now: "in 3 s", "2 min ago", "now". */
export function relative(at: number, now: number): string {
  const ms = at - now;
  if (Math.abs(ms) < 500) return 'now';
  return ms > 0 ? `in ${span(ms)}` : `${span(ms)} ago`;
}

export function clock(at: number | string): string {
  const date = new Date(at);
  const today = date.toDateString() === new Date().toDateString();
  return today
    ? date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : date.toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}
