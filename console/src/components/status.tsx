import { cn } from '@/lib/utils';
import type { Status } from '@/lib/types';

// The five states of an instance, each with one color everywhere: amber
// asleep, blue running, red for a person, green done, violet ended in
// failure.

export const statusStyle: Record<Status, { dot: string; text: string; soft: string; label: string }> = {
  waiting: { dot: 'bg-warning', text: 'text-warning', soft: 'bg-warning/12 text-warning border-warning/25', label: 'waiting' },
  running: { dot: 'bg-info', text: 'text-info', soft: 'bg-info/12 text-info border-info/25', label: 'running' },
  stopped: {
    dot: 'bg-destructive',
    text: 'text-destructive',
    soft: 'bg-destructive/10 text-destructive border-destructive/25',
    label: 'stopped',
  },
  done: { dot: 'bg-success', text: 'text-success', soft: 'bg-success/12 text-success border-success/25', label: 'done' },
  failed: {
    dot: 'bg-violet-500',
    text: 'text-violet-600 dark:text-violet-400',
    soft: 'bg-violet-500/10 text-violet-600 dark:text-violet-400 border-violet-500/25',
    label: 'failed',
  },
};

export function StatusDot({ status, pulse, className }: { status: Status; pulse?: boolean; className?: string }) {
  return (
    <span className={cn('relative inline-flex size-2 shrink-0', className)} aria-hidden="true">
      {pulse && <span className={cn('absolute inline-flex size-full animate-ping rounded-full opacity-60', statusStyle[status].dot)} />}
      <span className={cn('relative inline-flex size-2 rounded-full', statusStyle[status].dot)} />
    </span>
  );
}

export function StatusBadge({ status, className }: { status: Status; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium whitespace-nowrap',
        statusStyle[status].soft,
        className,
      )}
    >
      <StatusDot status={status} pulse={status === 'running'} />
      {statusStyle[status].label}
    </span>
  );
}
