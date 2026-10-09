/* 全局数据上下文：overview 轮询 + 刷新 + 401 密钥门。
   5s 轮询页面隐藏时暂停；视图级数据（用量/模型/…）由各视图自取。 */
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import type { Overview } from './types';
import { api, setUnauthorizedHandler } from './api';
import { toast } from './toast';

interface DataCtx {
  overview: Overview | null;
  refresh: (quiet?: boolean) => Promise<void>;
  hardRefresh: () => Promise<void>;
}

const Ctx = createContext<DataCtx>({ overview: null, refresh: async () => {}, hardRefresh: async () => {} });

export function useData() {
  return useContext(Ctx);
}

export function DataProvider({ children, onNeedKey }: { children: React.ReactNode; onNeedKey: () => void }) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const inFlight = useRef(false);

  useEffect(() => {
    setUnauthorizedHandler(onNeedKey);
    return () => setUnauthorizedHandler(null);
  }, [onNeedKey]);

  const refresh = useCallback(async (quiet = false) => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const d = await api<Overview>('overview');
      setOverview(d);
    } catch (e) {
      if (!quiet) toast((e as Error).message, 'err');
    } finally {
      inFlight.current = false;
    }
  }, []);

  /* hardRefresh：全池余额刷新（打上游）+ 重拉 overview。 */
  const hardRefresh = useCallback(async () => {
    try {
      await api('balance_all', { method: 'POST' });
      await refresh(true);
      toast('余额已从上游刷新', 'ok');
    } catch (e) {
      toast('刷新失败：' + (e as Error).message, 'err');
      await refresh(true);
    }
  }, [refresh]);

  /* 5s 轮询：页面隐藏时暂停（切后台/最小化没人看）。 */
  useEffect(() => {
    void refresh();
    const id = setInterval(() => {
      if (!document.hidden) void refresh(true);
    }, 5000);
    return () => clearInterval(id);
  }, [refresh]);

  return <Ctx.Provider value={{ overview, refresh, hardRefresh }}>{children}</Ctx.Provider>;
}
