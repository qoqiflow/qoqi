import type { ReactNode } from 'react';
import { RefreshCw } from 'lucide-react';
import { Brand, Mark } from '@/components/brand';
import { Button } from '@/components/ui/button';

/** The page around the forms of signing in: the brand, and a card. */
export function AuthCard({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <div className="relative flex min-h-svh items-center justify-center overflow-hidden bg-background p-6">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -top-40 left-1/2 h-[28rem] w-[56rem] -translate-x-1/2 rounded-full bg-gradient-to-br from-[#3D5AFE]/15 to-[#00C2E0]/10 blur-3xl"
      />
      <div className="relative grid w-full max-w-sm gap-6">
        <Brand className="justify-center text-lg" />
        <div className="grid gap-5 rounded-2xl border bg-card p-6 shadow-sm">
          <div className="grid gap-1.5">
            <h1 className="text-lg font-semibold">{title}</h1>
            {hint && <p className="text-sm text-muted-foreground">{hint}</p>}
          </div>
          {children}
        </div>
      </div>
    </div>
  );
}

/** While the console asks who is there; or when the daemon does not answer. */
export function Splash({ failed, retry }: { failed?: boolean; retry?: () => void }) {
  if (!failed) {
    return (
      <div className="flex min-h-svh items-center justify-center bg-background">
        <Mark className="size-10 animate-pulse" />
      </div>
    );
  }
  return (
    <AuthCard title="The daemon does not answer" hint="It may have stopped, or be starting: qoqi serve runs it.">
      <Button onClick={retry}>
        <RefreshCw /> Try again
      </Button>
    </AuthCard>
  );
}
