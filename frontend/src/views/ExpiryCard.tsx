/* 积分到期提醒卡（首页）：紧凑精炼模式，不占空间，支持一键折叠展开。
   packages 数据与「积分构成」共享（2 分钟缓存，不重复打上游）。 */
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { PackageAccount, CreditPackage } from '../types';
import { SegBar } from '../components/ui';
import { btnXs } from '../components/buttons';
import { fmtTok } from '../fmt';

const EXP_FRESH_MS = 2 * 60 * 1000;

/* 把某账号的包聚合成「到期日 → 该日作废积分」升序列表。 */
export function expBatches(packs: CreditPackage[]): { date: string; remain: number }[] {
  const byDay = new Map<string, number>();
  for (const p of packs || []) {
    const r = Number(p.remain || 0);
    const t = (p.end_time || '').slice(0, 10);
    if (r <= 0 || !t) continue;
    byDay.set(t, (byDay.get(t) || 0) + r);
  }
  return [...byDay.entries()].map(([date, remain]) => ({ date, remain })).sort((a, b) => (a.date < b.date ? -1 : 1));
}

export function expDaysLeft(dateStr: string, today: Date): number {
  return Math.round((new Date(dateStr + 'T00:00:00').getTime() - today.getTime()) / 86400000);
}

/* 全模块共享的 packages 缓存。 */
let cache: { data: PackageAccount[]; at: number } | null = null;
let fetching = false;

export async function fetchPackages(force = false): Promise<PackageAccount[]> {
  if (!force && cache && Date.now() - cache.at < EXP_FRESH_MS) return cache.data;
  if (fetching) return cache?.data || [];
  fetching = true;
  try {
    const d = await api<{ accounts: PackageAccount[] }>('packages');
    cache = { data: d.accounts || [], at: Date.now() };
    return cache.data;
  } finally {
    fetching = false;
  }
}

export function ExpiryCard({ packages, onLoaded }: { packages: PackageAccount[] | null; onLoaded: (v: PackageAccount[]) => void }) {
  const [checking, setChecking] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let dead = false;
    (async () => {
      try {
        const list = await fetchPackages();
        if (!dead) onLoaded(list);
      } catch {
        /* 概览已提示 */
      }
    })();
    return () => {
      dead = true;
    };
  }, [onLoaded]);

  const list = packages || [];
  const today = useMemo(() => {
    const d = new Date();
    d.setHours(0, 0, 0, 0);
    return d;
  }, []);

  /* 紧迫度统计 */
  const { urgentTotal, weekTotal, safeTotal, urgentAccts, weekAccts } = useMemo(() => {
    let uTot = 0;
    let wTot = 0;
    let sTot = 0;
    let uCnt = 0;
    let wCnt = 0;

    for (const a of list) {
      if (a.error) continue;
      const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
      let hasU = false;
      let hasW = false;
      for (const b of bs) {
        const d = expDaysLeft(b.date, today);
        if (d <= 3) {
          uTot += b.remain;
          hasU = true;
        } else if (d <= 7) {
          wTot += b.remain;
          hasW = true;
        } else {
          sTot += b.remain;
        }
      }
      if (hasU) uCnt++;
      if (hasW) wCnt++;
    }
    return { urgentTotal: uTot, weekTotal: wTot, safeTotal: sTot, urgentAccts: uCnt, weekAccts: wCnt };
  }, [list, today]);

  /* 排序：即将到期的账号排在最前 */
  const accountsWithExpiry = useMemo(() => {
    return list
      .map((a) => {
        let days: number | null = null;
        let firstBatch: { date: string; remain: number } | null = null;
        if (!a.error) {
          const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
          if (bs.length) {
            firstBatch = bs[0];
            days = expDaysLeft(firstBatch.date, today);
          }
        }
        return { a, days, firstBatch };
      })
      .filter((x) => x.firstBatch !== null && x.days !== null)
      .sort((x, y) => (x.days! < y.days! ? -1 : 1));
  }, [list, today]);

  const segs = [
    { value: urgentTotal, color: 'var(--bad)', title: `≤3 天紧急 · ${fmtTok(urgentTotal)} 积分` },
    { value: weekTotal, color: 'var(--warn)', title: `4-7 天近期 · ${fmtTok(weekTotal)} 积分` },
    { value: safeTotal, color: 'var(--ok)', title: `>7 天充足 · ${fmtTok(safeTotal)} 积分` },
  ].filter((s) => s.value > 0);

  const hasUrgent = urgentTotal > 0 || weekTotal > 0;

  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-2xs overflow-hidden transition-all">
      {/* 极简紧凑头部：一栏式展示状态与关键数字，不占多余垂直空间 */}
      <div className="flex flex-wrap items-center justify-between gap-2.5 px-4 py-2.5">
        <div className="flex items-center gap-2">
          <span
            className={
              'h-2 w-2 rounded-full shrink-0 ' +
              (urgentTotal > 0 ? 'bg-[var(--bad)] wb-pulse-anim' : weekTotal > 0 ? 'bg-[var(--warn)]' : 'bg-[var(--ok)]')
            }
          />
          <span className="text-[13px] font-semibold text-[var(--ink)]">积分到期提醒</span>

          {/* 紧迫度微型胶囊指示 */}
          <div className="flex items-center gap-1.5 ml-1 text-[11px] tabular">
            {urgentTotal > 0 && (
              <span className="inline-flex items-center gap-1 rounded bg-[var(--bad-soft)] px-1.5 py-0.5 font-semibold text-[var(--bad)] border border-[var(--bad)]/20">
                🚨 ≤3天: {fmtTok(urgentTotal)} ({urgentAccts}号)
              </span>
            )}
            {weekTotal > 0 && (
              <span className="inline-flex items-center gap-1 rounded bg-[var(--warn-soft)] px-1.5 py-0.5 font-medium text-[var(--warn)] border border-[var(--warn)]/20">
                ⚠️ 4-7天: {fmtTok(weekTotal)} ({weekAccts}号)
              </span>
            )}
            {!hasUrgent && (
              <span className="inline-flex items-center gap-1 rounded bg-[var(--ok-soft)] px-1.5 py-0.5 font-medium text-[var(--ok)]">
                近期无紧急到期包
              </span>
            )}
          </div>

          {/* 微型紧凑比例条 */}
          {segs.length > 0 && (
            <div className="hidden md:inline-flex w-[90px] ml-1">
              <SegBar segs={segs} cls="h-[5px]" />
            </div>
          )}
        </div>

        {/* 右侧动作按钮 */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            className={btnXs}
            disabled={checking}
            onClick={async () => {
              setChecking(true);
              try {
                const l = await fetchPackages(true);
                onLoaded(l);
              } finally {
                setChecking(false);
              }
            }}
          >
            {checking ? '查询中…' : '检查'}
          </button>

          {accountsWithExpiry.length > 0 && (
            <button
              type="button"
              className={btnXs + (expanded ? ' border-[var(--accent)] text-[var(--accent)]' : '')}
              onClick={() => setExpanded(!expanded)}
            >
              {expanded ? '收起明细' : `明细 (${accountsWithExpiry.length})`}
            </button>
          )}
        </div>
      </div>

      {/* 展开的紧凑列表（仅在用户点击时展示，限高滚动，绝不大面积占屏） */}
      {expanded && accountsWithExpiry.length > 0 && (
        <div className="border-t border-[var(--line-soft)] bg-[var(--surface-2)]/30 px-3 py-2">
          <div className="max-h-[160px] overflow-y-auto divide-y divide-[var(--line-soft)]/50 pr-1">
            {accountsWithExpiry.map(({ a, days, firstBatch }, i) => {
              const name = a.nickname || a.uid.slice(0, 8);
              const isUrgent = days !== null && days <= 3;
              const isWeek = days !== null && days <= 7;
              const dayText = days === 0 ? '今天' : days === 1 ? '明天' : `${days}天后`;

              return (
                <div key={i} className="flex items-center justify-between py-1.5 text-[12px] hover:bg-[var(--surface-2)]/60 px-2 rounded">
                  <div className="flex items-center gap-2 min-w-0">
                    <span
                      className={
                        'h-1.5 w-1.5 rounded-full shrink-0 ' +
                        (isUrgent ? 'bg-[var(--bad)]' : isWeek ? 'bg-[var(--warn)]' : 'bg-[var(--ok)]')
                      }
                    />
                    <span className="truncate font-medium text-[var(--ink)] max-w-[130px]">{name}</span>
                    <span className="text-[10.5px] font-[family-name:var(--mono)] text-[var(--ink-3)]">{a.uid.slice(0, 8)}</span>
                  </div>

                  <div className="flex items-center gap-3 tabular">
                    <span
                      className={
                        'rounded px-1.5 py-0.2 text-[11px] font-medium ' +
                        (isUrgent
                          ? 'bg-[var(--bad-soft)] text-[var(--bad)]'
                          : isWeek
                            ? 'bg-[var(--warn-soft)] text-[var(--warn)]'
                            : 'text-[var(--ink-3)]')
                      }
                    >
                      {firstBatch?.date} ({dayText})
                    </span>
                    <span className="font-semibold text-[var(--ink)] min-w-[70px] text-right">
                      {fmtTok(firstBatch?.remain)} <span className="font-normal text-[10.5px] text-[var(--ink-3)]">积分</span>
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
