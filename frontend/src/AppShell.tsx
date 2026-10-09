/* 应用外壳：左侧导航 + 顶栏 + hash 路由 + 全局弹窗与密钥门。 */
import { useEffect, useState } from 'react';
import { useData } from './data';
import { useTheme } from './theme';
import { fmtUptime } from './fmt';
import { KeyGate } from './components/KeyGate';
import { AddAccountDialog } from './components/AddAccount';
import { btnPrimary, btnGhost } from './components/buttons';

export const VIEWS = [
  { id: 'accounts', title: '账号池', icon: 'M2.8 13.5c.6-2.6 2.7-4 5.2-4s4.6 1.4 5.2 4M5.4 5.5a2.6 2.6 0 1 0 5.2 0 2.6 2.6 0 1 0-5.2 0' },
  { id: 'usage', title: '用量', icon: 'M2 13.5h12M4.5 13.5V8.2M8 13.5V3.5M11.5 13.5v-3' },
  { id: 'packages', title: '积分构成', icon: 'M2.2 5.4 8 2.5l5.8 2.9v5.2L8 13.5l-5.8-2.9zM2.2 5.4 8 8.3l5.8-2.9M8 8.3v5.2' },
  { id: 'taskscenter', title: '任务中心', icon: 'M3 2.5h10v9H8l-3 3v-3H3zM6 6.5h4M6 9h2.5' },
  { id: 'models', title: '模型与档位', icon: 'M8 1.8 14 5v6L8 14.2 2 11V5zM2 5l6 3.2L14 5M8 8.2v6' },
  { id: 'config', title: '配置', icon: 'M8 5.8a2.2 2.2 0 1 0 0 4.4 2.2 2.2 0 1 0 0-4.4M8 1.6v1.9M8 12.5v1.9M1.6 8h1.9M12.5 8h1.9M3.5 3.5l1.3 1.3M11.2 11.2l1.3 1.3M12.5 3.5l-1.3 1.3M4.8 11.2l-1.3 1.3' },
  { id: 'logs', title: '运行日志', icon: 'M2.5 3.5h11M2.5 8h11M2.5 12.5h7' },
] as const;

export type ViewId = (typeof VIEWS)[number]['id'];

function viewFromHash(): ViewId {
  const h = (location.hash || '#accounts').slice(1);
  return (VIEWS.find((v) => v.id === h)?.id || 'accounts') as ViewId;
}

function Ico({ d }: { d: string }) {
  return (
    <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.4" className="shrink-0 opacity-85">
      <path d={d} />
    </svg>
  );
}

export function AppShell({ children }: { children: (view: ViewId) => React.ReactNode }) {
  const { overview, refresh, hardRefresh } = useData();
  const { theme, toggle } = useTheme();
  const [view, setView] = useState<ViewId>(viewFromHash);
  const [needKey, setNeedKey] = useState(false);
  const [adding, setAdding] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  useEffect(() => {
    const onHash = () => setView(viewFromHash());
    addEventListener('hashchange', onHash);
    return () => removeEventListener('hashchange', onHash);
  }, []);

  const go = (v: ViewId) => {
    history.replaceState(null, '', '#' + v);
    setView(v);
  };

  const title = VIEWS.find((v) => v.id === view)?.title || '';
  const d = overview;
  const healthy = d?.healthy || 0;
  const total = d?.total || 0;

  return (
    <div className="grid min-h-screen grid-cols-[208px_1fr]">
      {/* 导航 */}
      <nav className="sticky top-0 flex h-screen flex-col border-r border-[var(--line)] bg-[var(--surface)]">
        <div className="border-b border-[var(--line-soft)] px-[18px] pt-5 pb-4">
          <div className="text-[15px] font-semibold tracking-tight">WorkBuddy Manager</div>
          <div className="mt-0.5 font-[family-name:var(--mono)] text-[11.5px] text-[var(--ink-3)]">v{d?.version || '-'}</div>
        </div>
        <ul className="flex-1 list-none px-2 py-2.5">
          {VIEWS.map((v) => (
            <li key={v.id}>
              <a
                href={'#' + v.id}
                onClick={(e) => {
                  e.preventDefault();
                  go(v.id);
                }}
                className={
                  'mb-0.5 flex items-center gap-2.5 rounded-lg px-[11px] py-2 text-[13.5px] no-underline ' +
                  (view === v.id
                    ? 'bg-[var(--accent-soft)] font-medium text-[var(--accent)]'
                    : 'text-[var(--ink-2)] hover:bg-[var(--surface-2)] hover:text-[var(--ink)]')
                }
              >
                <Ico d={v.icon} />
                {v.title}
              </a>
            </li>
          ))}
        </ul>
        <div className="border-t border-[var(--line-soft)] px-4 py-3 text-[11.5px] text-[var(--ink-3)]">
          <div className="flex items-center gap-1.5">
            <span
              className={
                'h-[7px] w-[7px] shrink-0 rounded-full ' +
                (healthy > 0 ? 'bg-[var(--ok)]' : total > 0 ? 'bg-[var(--warn)]' : 'bg-[var(--bad)]')
              }
            />
            <span>{healthy > 0 ? '服务正常' : total ? '无可用账号' : '待添加账号'}</span>
          </div>
          <div className="mt-1">
            {d?.redis_mode === 'upstash' ? 'Redis 镜像' : '本地内存'}
          </div>
        </div>
      </nav>

      {/* 主区 */}
      <div className="flex min-w-0 flex-col">
        <div className="sticky top-0 z-20 flex items-center gap-2.5 border-b border-[var(--line)] bg-[var(--bg)]/95 px-6 py-3 backdrop-blur">
          <h2 className="text-[16px] font-semibold">{title}</h2>
          <span className="flex-1" />
          <span className="tabular text-[12px] text-[var(--ink-3)]" title="服务连续运行时长">
            {d ? '运行 ' + fmtUptime(d.uptime_sec || 0) : '运行 -'}
          </span>
          <button
            className="rounded-lg border border-[var(--line)] bg-transparent p-1.5 text-[var(--ink-2)] hover:text-[var(--ink)]"
            onClick={toggle}
            title={theme === 'light' ? '切换到深色' : '切换到浅色'}
            aria-label="切换主题"
          >
            {theme === 'light' ? (
              <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.4">
                <path d="M13.2 9.6A5.6 5.6 0 0 1 6.4 2.8a5.6 5.6 0 1 0 6.8 6.8z" />
              </svg>
            ) : (
              <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.4">
                <circle cx="8" cy="8" r="3" />
                <path d="M8 1v2M8 13v2M1 8h2M13 8h2M3.2 3.2l1.4 1.4M11.4 11.4l1.4 1.4M12.8 3.2l-1.4 1.4M4.6 11.4l-1.4 1.4" />
              </svg>
            )}
          </button>
          <button
            className={btnGhost}
            disabled={refreshing}
            onClick={async () => {
              setRefreshing(true);
              await hardRefresh();
              setRefreshing(false);
            }}
          >
            {refreshing ? '刷新中…' : '刷新'}
          </button>
          <button className={btnPrimary} onClick={() => setAdding(true)}>
            添加账号
          </button>
        </div>

        <main className="flex-1 p-5">{children(view)}</main>
      </div>

      {needKey && (
        <KeyGate
          onReady={() => {
            setNeedKey(false);
            void refresh();
          }}
        />
      )}
      {adding && (
        <AddAccountDialog
          onClose={() => setAdding(false)}
          onAdded={() => {
            void refresh(true);
          }}
        />
      )}
    </div>
  );
}
