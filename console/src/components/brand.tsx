import { cn } from '@/lib/utils';

/** The q of Qoqi, on its gradient. */
export function Mark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 512 512" className={cn('size-7 shrink-0', className)} aria-hidden="true">
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
  );
}

export function Brand({ className }: { className?: string }) {
  return (
    <span className={cn('flex items-center gap-2.5 font-semibold tracking-tight', className)}>
      <Mark />
      <span>flow</span>
    </span>
  );
}
