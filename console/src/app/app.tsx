import { lazy, Suspense } from 'react';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { Skeleton } from '@/components/ui/skeleton';
import { AuthGate } from '@/auth/auth';
import { ActivityPage } from '@/features/activity/activity';
import { VersionsPage } from '@/features/deployments/versions';
import { FlowPage } from '@/features/flows/flow';
import { FlowsPage } from '@/features/flows/flows';
import { InstancePage } from '@/features/instances/instance';
import { InstancesPage } from '@/features/instances/instances';
import { LaunchProvider } from '@/features/instances/launch';
import { LogsPage } from '@/features/logs/logs';
import { OverviewPage } from '@/features/overview/overview';
import { SettingsPage } from '@/features/settings/settings';
import { LiveProvider } from '@/lib/live';
import { Shell } from './shell';

// The editor brings CodeMirror: it is loaded when it is opened
const EditorPage = lazy(() => import('@/features/editor/editor').then((m) => ({ default: m.EditorPage })));

/** The pages of the console, once someone is signed in. */
export function App() {
  return (
    <BrowserRouter>
      <AuthGate>
        <LiveProvider>
          <LaunchProvider>
            <Routes>
              <Route element={<Shell />}>
                <Route index element={<OverviewPage />} />
                <Route path="flows" element={<FlowsPage />} />
                <Route path="flows/:name" element={<FlowPage />} />
                <Route path="flows/:name/:tab" element={<FlowPage />} />
                <Route path="instances" element={<InstancesPage />} />
                <Route path="instances/:id" element={<InstancePage />} />
                <Route path="activity" element={<ActivityPage />} />
                <Route path="logs" element={<LogsPage />} />
                <Route
                  path="editor/:program?"
                  element={
                    <Suspense fallback={<Skeleton className="m-6 h-[70vh] rounded-xl" />}>
                      <EditorPage />
                    </Suspense>
                  }
                />
                <Route path="deployments" element={<VersionsPage />} />
                <Route path="settings/:tab?" element={<SettingsPage />} />
                {/* Where the pages were before */}
                <Route path="program" element={<Navigate to="/flows" replace />} />
                <Route path="versions" element={<Navigate to="/deployments" replace />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Routes>
          </LaunchProvider>
        </LiveProvider>
      </AuthGate>
    </BrowserRouter>
  );
}
