/* 积分到期提醒卡：徽章模式，简要直观呈现每个账号的到期时间与失效积分。
   packages 数据与「积分构成」共享（2 分钟缓存，不重复打上游）。 */
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { PackageAccount, CreditPackage } from '../types';
import { btnXs } from '../components/buttons';
import { fmtTok } from '../fmt';
import { copyText } from '../clipboard';
import { toast } from '../toast';

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
  const { urgentTotal, weekTotal, urgentAccts, weekAccts } = useMemo(() => {
    let uTot = 0;
    let wTot = 0;
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
        }
      }
      if (hasU) uCnt++;
      if (hasW) wCnt++;
    }
    return { urgentTotal: uTot, weekTotal: wTot, urgentAccts: uCnt, weekAccts: wCnt };
  }, [list, today]);

  /* 排序：即将到期的账号排在最前 */
  const accountsWithExpiry = useMemo(() => {
    return list
      .map((a) => {
        let days: number | null = null;
        let firstBatch: { date: string; remain: number } | null = null;
        let allBatches: { date: string; remain: number }[] = [];
        if (!a.error) {
          const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
          if (bs.length) {
            firstBatch = bs[0];
            days = expDaysLeft(firstBatch.date, today);
            allBatches = bs;
          }
        }
        return { a, days, firstBatch, allBatches };
      })
      .filter((x): x is { a: PackageAccount; days: number; firstBatch: { date: string; remain: number }; allBatches: { date: string; remain: number }[] } =>
        x.firstBatch !== null && x.days !== null,
      )
      .sort((x, y) => (x.days < y.days ? -1 : 1));
  }, [list, today]);

  const hasUrgent = urgentTotal > 0 || weekTotal > 0;
  const INITIAL_LIMIT = 8;
  const visibleAccounts = expanded ? accountsWithExpiry : accountsWithExpiry.slice(0, INITIAL_LIMIT);
  const hiddenCount = accountsWithExpiry.length - INITIAL_LIMIT;

  const onCopyUid = async (uid: string, name: string) => {
    try {
      await copyText(uid);
      toast(`已复制 ${name} UID`, 'ok');
    } catch {
      toast(`复制失败: ${uid}`, 'err');
    }
  };

  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-xs transition-all">
      {/* 头部状态与操作 */}
      <div className="flex flex-wrap items-center justify-between gap-2.5 px-4 py-2.5 border-b border-[var(--line-soft)]">
        <div className="flex items-center gap-2">
          <span
            className={
              'h-2 w-2 rounded-full shrink-0 ' +
              (urgentTotal > 0 ? 'bg-[var(--bad)] wb-pulse-anim' : weekTotal > 0 ? 'bg-[var(--warn)]' : 'bg-[var(--ok)]')
            }
          />
          <span className="text-[13px] font-semibold text-[var(--ink)]">积分到期提醒</span>

          {/* 紧迫度概览胶囊 */}
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
        </div>

        {/* 右侧动作按钮 */}
        <div className="flex items-center gap-2">
          {accountsWithExpiry.length > INITIAL_LIMIT && (
            <button
              type="button"
              className={btnXs + (expanded ? ' border-[var(--accent)] text-[var(--accent)]' : '')}
              onClick={() => setExpanded(!expanded)}
            >
              {expanded ? '收起' : `展开全部 (${accountsWithExpiry.length})`}
            </button>
          )}

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
        </div>
      </div>

      {/* 徽章列表：简要直观展示每个账号的到期时间与积分 */}
      <div className="p-3">
        {accountsWithExpiry.length === 0 ? (
          <div className="inline-flex items-center gap-2 rounded-lg border border-[var(--ok)]/20 bg-[var(--ok-soft)] px-3 py-1.5 text-[12px] text-[var(--ok)]">
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />
            <span>近期无到期积分，所有账号额度充足。</span>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            {visibleAccounts.map(({ a, days, firstBatch, allBatches }, i) => {
              const name = a.nickname || a.uid.slice(0, 8);
              const isUrgent = days <= 3;
              const isWeek = days <= 7;
              const dayText = days === 0 ? '今天到期' : days === 1 ? '明天到期' : `${days}天后到期`;
              const shortDate = firstBatch.date.slice(5); // MM-DD
              const extraBatches = allBatches.length > 1 ? ` (共${allBatches.length}包待结)` : '';

              const badgeToneCls = isUrgent
                ? 'border-[var(--bad)]/35 bg-[var(--bad-soft)] text-[var(--bad)]'
                : isWeek
                  ? 'border-[var(--warn)]/35 bg-[var(--warn-soft)] text-[var(--warn)]'
                  : 'border-[var(--line)] bg-[var(--surface-2)] text-[var(--ink-2)]';

              const dotCls = isUrgent ? 'bg-[var(--bad)]' : isWeek ? 'bg-[var(--warn)]' : 'bg-[var(--ok)]';

              const tooltip = `账号: ${name} (UID: ${a.uid})\n最近到期: ${firstBatch.date} (${dayText})${extraBatches}\n失效积分: ${firstBatch.remain.toLocaleString()} 积分\n点击可快速复制 UID`;

              return (
                <div
                  key={i}
                  title={tooltip}
                  onClick={() => void onCopyUid(a.uid, name)}
                  className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-[12px] cursor-pointer transition-all hover:brightness-110 active:scale-[0.98] shadow-2xs ${badgeToneCls}`}
                >
                  <span className={`h-1.5 w-1.5 rounded-full shrink-0 ${dotCls}`} />
                  <span className="font-semibold text-[var(--ink)] max-w-[120px] truncate">{name}</span>
                  <span className="opacity-40 text-[11px]">·</span>
                  <span className="tabular font-medium text-[11.5px]">
                    {dayText} <span className="opacity-75 font-normal">({shortDate})</span>
                  </span>
                  <span className="opacity-40 text-[11px]">·</span>
                  <span className="tabular font-bold text-[var(--ink)]">
                    {fmtTok(firstBatch.remain)} <span className="font-normal text-[10.5px] opacity-75">积分</span>
                  </span>
                </div>
              );
            })}

            {!expanded && hiddenCount > 0 && (
              <button
                type="button"
                onClick={() => setExpanded(true)}
                className="inline-flex items-center gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-2.5 py-1 text-[11.5px] text-[var(--ink-3)] hover:text-[var(--ink)] hover:border-[var(--accent)]/40 transition-colors"
              >
                +{hiddenCount} 更多…
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
