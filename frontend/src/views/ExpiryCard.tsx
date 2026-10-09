/* 积分到期提醒卡（首页）：按最近到期批次排序 + 紧迫度归集条。
   packages 数据与「积分构成」共享（2 分钟缓存，不重复打上游）。 */
import { useEffect, useState } from 'react';
import { api } from '../api';
import type { PackageAccount, CreditPackage } from '../types';
import { SegBar, Empty } from '../components/ui';
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

/* 全模块共享的 packages 缓存（模块级单例）。 */
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

export function packagesCacheAge(): number {
  return cache ? Date.now() - cache.at : 0;
}

export function ExpiryCard({ packages, onLoaded }: { packages: PackageAccount[] | null; onLoaded: (v: PackageAccount[]) => void }) {
  const [note, setNote] = useState('');
  const [checking, setChecking] = useState(false);

  useEffect(() => {
    let dead = false;
    (async () => {
      setNote('查询中…');
      try {
        const list = await fetchPackages();
        if (!dead) {
          onLoaded(list);
          const ageMin = cache ? Math.floor((Date.now() - cache.at) / 60000) : 0;
          setNote(ageMin > 0 ? `${list.length} 个账号 · ${ageMin} 分钟前的数据` : `${list.length} 个账号 · 实时查询上游`);
        }
      } catch (e) {
        if (!dead) setNote('查询失败：' + (e as Error).message);
      }
    })();
    return () => {
      dead = true;
    };
    // 仅挂载时拉一次（packages prop 由父级缓存，不重复触发）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const list = packages || [];
  const today = new Date();
  today.setHours(0, 0, 0, 0);

  /* 排序：最快到期的排最前；查询失败/无到期的排最后。 */
  const keyed = list
    .map((a) => {
      let key: string | null = null;
      if (!a.error) {
        const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
        if (bs.length) key = bs[0].date;
      }
      return { a, key };
    })
    .sort((x, y) => {
      if (x.key === y.key) return 0;
      if (x.key === null) return 1;
      if (y.key === null) return -1;
      return x.key! < y.key! ? -1 : 1;
    });

  /* 紧迫度归集条。 */
  const buckets = [
    { label: '≤3 天', value: 0, color: 'var(--bad)', title: '≤3 天' },
    { label: '4-7 天', value: 0, color: 'var(--warn)', title: '4-7 天' },
    { label: '7 天以上', value: 0, color: 'var(--ok)', title: '7 天以上' },
  ];
  for (const a of list) {
    if (a.error) continue;
    for (const b of expBatches(a.packages || [])) {
      const days = expDaysLeft(b.date, today);
      if (days < 0) continue;
      const i = days <= 3 ? 0 : days <= 7 ? 1 : 2;
      buckets[i].value += b.remain;
    }
  }
  const segs = buckets.filter((b) => b.value > 0).map((b) => ({ value: b.value, color: b.color, title: `${b.title} · ${fmtTok(b.value)} 积分` }));

  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
      <header className="flex items-center gap-2.5 px-4 py-3">
        <h3 className="text-[14px] font-semibold">
          积分到期提醒
          <span className="ml-1 text-[12px] font-normal text-[var(--ink-3)]">
            按最近到期批次估算 · 上游按失效时刻先后自动优先扣减（FEFO）
          </span>
        </h3>
        <span className="flex-1" />
        <span className="text-[12px] text-[var(--ink-3)]">{note}</span>
        <button
          className={btnXs}
          disabled={checking}
          onClick={async () => {
            setChecking(true);
            try {
              const l = await fetchPackages(true);
              onLoaded(l);
              setNote(`${l.length} 个账号 · 实时查询上游`);
            } catch (e) {
              setNote('查询失败：' + (e as Error).message);
            } finally {
              setChecking(false);
            }
          }}
        >
          检查
        </button>
      </header>
      <div className="px-4 pb-4">
        {segs.length > 0 && (
          <div className="mb-3">
            <SegBar segs={segs} aria="全部账号的积分到期紧迫度" />
          </div>
        )}
        <div className="flex flex-col gap-1.5">
          {keyed.length === 0 && <Empty>没有账号</Empty>}
          {keyed.map(({ a }, i) => {
            const name = a.nickname || a.uid.slice(0, 8);
            if (a.error) {
              return (
                <div key={i} className="flex items-center gap-2 text-[13px]">
                  <span className="h-[7px] w-[7px] shrink-0 rounded-full bg-[var(--bad)]" />
                  <span className="w-[120px] shrink-0 truncate">{name}</span>
                  <span className="text-[var(--bad)]">查询失败：{a.error}</span>
                </div>
              );
            }
            const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
            if (!bs.length) {
              return (
                <div key={i} className="flex items-center gap-2 text-[13px]">
                  <span className="h-[7px] w-[7px] shrink-0 rounded-full bg-[var(--line)]" />
                  <span className="w-[120px] shrink-0 truncate">{name}</span>
                  <span className="text-[var(--ink-3)]">近期无到期包</span>
                </div>
              );
            }
            const first = bs[0];
            const days = expDaysLeft(first.date, today);
            const dayWord = days === 0 ? '今天到期' : days === 1 ? '明天到期' : days + ' 天后';
            const color = days <= 3 ? 'var(--bad)' : days <= 7 ? 'var(--warn)' : 'var(--ok)';
            return (
              <div key={i} className="flex items-center gap-2 text-[13px]">
                <span className="h-[7px] w-[7px] shrink-0 rounded-full" style={{ background: color }} />
                <span className="w-[120px] shrink-0 truncate">{name}</span>
                <span>
                  最近 <b className="tabular">{first.date}</b>（{dayWord}）· <b className="tabular">{fmtTok(first.remain)}</b> 积分
                </span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
