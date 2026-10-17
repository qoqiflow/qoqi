import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

// Light, dark, or as the system says: the choice is kept in this browser,
// and the class `dark` on <html> is what the AppSwite tokens read.

export type ThemeChoice = 'light' | 'dark' | 'system';

interface ThemeState {
  choice: ThemeChoice;
  resolved: 'light' | 'dark';
  choose: (choice: ThemeChoice) => void;
}

const ThemeContext = createContext<ThemeState | null>(null);

const key = 'qoqi-theme';

function readChoice(): ThemeChoice {
  try {
    const kept = localStorage.getItem(key);
    if (kept === 'light' || kept === 'dark' || kept === 'system') return kept;
  } catch {
    // A browser that keeps nothing: the system decides
  }
  return 'system';
}

function systemDark(): boolean {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [choice, setChoice] = useState<ThemeChoice>(readChoice);
  const [dark, setDark] = useState(systemDark);

  useEffect(() => {
    const media = window.matchMedia?.('(prefers-color-scheme: dark)');
    if (!media) return;
    const changed = () => setDark(media.matches);
    media.addEventListener('change', changed);
    return () => media.removeEventListener('change', changed);
  }, []);

  const resolved = choice === 'system' ? (dark ? 'dark' : 'light') : choice;

  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolved === 'dark');
  }, [resolved]);

  const state = useMemo<ThemeState>(
    () => ({
      choice,
      resolved,
      choose: (next) => {
        setChoice(next);
        try {
          localStorage.setItem(key, next);
        } catch {
          // Kept for this visit only
        }
      },
    }),
    [choice, resolved],
  );

  return <ThemeContext.Provider value={state}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeState {
  const state = useContext(ThemeContext);
  if (!state) throw new Error('useTheme outside ThemeProvider');
  return state;
}
