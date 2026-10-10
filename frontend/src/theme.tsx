/* 明暗主题：两态切换（浅/深），首次访问跟随系统；选择持久化 localStorage。 */
import { createContext, useContext, useEffect, useState } from 'react';

const LS_THEME = 'workbuddy_manager.theme';

export type Theme = 'light' | 'dark';

function initialTheme(): Theme {
  const saved = localStorage.getItem(LS_THEME);
  if (saved === 'light' || saved === 'dark') return saved;
  return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

interface ThemeCtx {
  theme: Theme;
  toggle: () => void;
}

const Ctx = createContext<ThemeCtx>({ theme: 'dark', toggle: () => {} });

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<Theme>(initialTheme);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    const mq = matchMedia('(prefers-color-scheme: light)');
    const onChange = () => {
      // 没有显式选择（localStorage 空）时跟随系统。
      if (!localStorage.getItem(LS_THEME)) setTheme(mq.matches ? 'light' : 'dark');
    };
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  const toggle = () => {
    setTheme((t) => {
      const next = t === 'light' ? 'dark' : 'light';
      localStorage.setItem(LS_THEME, next);
      document.documentElement.dataset.theme = next;
      return next;
    });
  };

  return <Ctx.Provider value={{ theme, toggle }}>{children}</Ctx.Provider>;
}

export function useTheme() {
  return useContext(Ctx);
}
