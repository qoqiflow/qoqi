import '@fontsource-variable/manrope';
import '@fontsource-variable/jetbrains-mono';
import './index.css';

import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter, Route, Routes } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { TooltipProvider } from '@/components/ui/tooltip';
import { LaunchProvider } from '@/components/launch';
import { Layout } from '@/components/layout';
import { LiveProvider } from '@/lib/live';
import { ThemeProvider } from '@/lib/theme';
import { InstancePage } from '@/pages/instance';
import { InstancesPage } from '@/pages/instances';
import { OverviewPage } from '@/pages/overview';
import { ProgramPage } from '@/pages/program';
import { VersionsPage } from '@/pages/versions';

const client = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={client}>
        <LiveProvider>
          <TooltipProvider delayDuration={300}>
            <BrowserRouter>
              <LaunchProvider>
                <Routes>
                  <Route element={<Layout />}>
                    <Route index element={<OverviewPage />} />
                    <Route path="instances" element={<InstancesPage />} />
                    <Route path="instances/:id" element={<InstancePage />} />
                    <Route path="program" element={<ProgramPage />} />
                    <Route path="versions" element={<VersionsPage />} />
                    <Route path="*" element={<OverviewPage />} />
                  </Route>
                </Routes>
              </LaunchProvider>
            </BrowserRouter>
          </TooltipProvider>
        </LiveProvider>
        <Toaster position="bottom-right" richColors />
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
