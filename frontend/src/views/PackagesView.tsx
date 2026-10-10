/* 积分构成视图：账号余额表（来源构成条 + 分页 + 排序）+ 逐包明细弹窗。 */
import { useEffect, useMemo, useState } from 'react';
import { fetchPackages, expBatches, expDaysLeft } from './ExpiryCard';
import type { PackageAccount, CreditPackage } from '../types';
import { Dialog } from '../components/Dialog';
import { RealmTag, Empty, Loading, Pager } from '../components/ui';
import { btnXs, btnPrimary } from '../components/buttons';
import { fmtTok } from '../fmt';

const PK_COLORS = ['#4f8cff', '#25b08b', '#e8a33d', '#c96bd6', '#e2607a', '#5aa9e6', '#8fbf3f', '#b58b5a', '#7d8fa8', '#d4785c'];

/* 包按 code+name 归并：来源 → 面额/余额/个数。 */
interface SourceGroup {
  key: string;
  name: string;
  n: number;
  remain: number;
  size: number;
}

function bySource(packs: CreditPackage[]): SourceGroup[] {
  const m = new Map<string, SourceGroup>();
  for (const p of packs || []) {
    const k = (p.package_code || '') + '|' + (p.name || '(未命名)');
    const e = m.get(k) || { key: k, name: p.name || '(未命名)', n: 0, remain: 0, size: 0 };
    e.n += 1;
    e.remain += Number(p.remain || 0);
    e.size += Number(p.size || 0);
    m.set(k, e);
  }
  return [...m.values()].sort((a, b) => b.size - a.size);
}

function PackagesModal({ a, onClose }: { a: PackageAccount; onClose: () => void }) {
  const pks = [...(a.packages || [])].sort((x, y) => (x.end_time || '9999').localeCompare(y.end_time || '9999'));
  return (
    <Dialog
      title="积分包明细"
      hint={(a.nickname || a.uid.slice(0, 8)) + ' · ' + (a.realm || '')}
      width={780}
      onClose={onClose}
      footer={
        <>
          <span className="flex-1" />
          <button className={btnPrimary} onClick={onClose}>
            关闭
          </button>
        </>
      }
    >
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="border-b border-[var(--line-soft)] text-left text-[11.5px] text-[var(--ink-3)]">
            <th className="px-2 py-2 font-medium">包名 / 来源</th>
            <th className="px-2 py-2 font-medium">面额</th>
            <th className="px-2 py-2 font-medium">剩余</th>
            <th className="px-2 py-2 font-medium">已用</th>
            <th className="px-2 py-2 font-medium">发放时刻</th>
            <th className="px-2 py-2 font-medium">到期时刻</th>
          </tr>
        </thead>
        <tbody>
          {pks.map((p, i) => (
            <tr key={i}>
              <td>
                <div>{p.name || '(未命名)'}</div>
                <div className="font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{p.package_code || ''}</div>
              </td>
              <td className="tabular">{fmtTok(p.size)}</td>
              <td className="tabular font-medium">{fmtTok(p.remain)}</td>
              <td className="tabular">{fmtTok(p.used)}</td>
              <td className="tabular text-[12px]">{(p.created_at || '').slice(0, 16).replace('T', ' ') || '—'}</td>
              <td className="tabular">{(p.end_time || '').slice(0, 10) || '永久/无'}</td>
            </tr>
          ))}
          {!pks.length && (
            <tr>
              <td colSpan={6}>
                <Empty>无积分包记录</Empty>
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </Dialog>
  );
}

export function PackagesView() {
  const [list, setList] = useState<PackageAccount[] | null>(null);
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [sortMode, setSortMode] = useState<'end_asc' | 'size_desc'>('end_asc');
  const [detailUid, setDetailUid] = useState<string | null>(null);

  const load = async () => {
    setError('');
    try {
      setList(await fetchPackages(true));
    } catch (e) {
      setError((e as Error).message);
      setList([]);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const today = useMemo(() => {
    const d = new Date();
    d.setHours(0, 0, 0, 0);
    return d;
  }, []);

  /* 排序：到期升序（默认）或面额降序。 */
  const sorted = useMemo(() => {
    if (!list) return [];
    const out = [...list];
    if (sortMode === 'end_asc') {
      out.sort((a, b) => {
        const ka = a.error ? null : expBatches(a.packages || []).find((x) => expDaysLeft(x.date, today) >= 0)?.date || null;
        const kb = b.error ? null : expBatches(b.packages || []).find((x) => expDaysLeft(x.date, today) >= 0)?.date || null;
        if (ka === kb) return 0;
        if (ka === null) return 1;
        if (kb === null) return -1;
        return ka < kb ? -1 : 1;
      });
    } else {
      out.sort((a, b) => (b.remain || 0) - (a.remain || 0));
    }
    return out;
  }, [list, sortMode, today]);

  const totalPages = Math.max(1, Math.ceil((sorted.length || 0) / pageSize));
  const cur = Math.min(page, totalPages);
  const paged = sorted.slice((cur - 1) * pageSize, cur * pageSize);
  const detail = detailUid ? sorted.find((a) => a.uid === detailUid) : null;

  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">积分构成与明细</h3>
          <span className="flex-1" />
          <span className="text-[12px] text-[var(--ink-3)]">{error ? '读取失败：' + error : `${sorted.length} 个账号 · 实时查询上游`}</span>
          <select className="wb-select px-2 py-1 text-[12px]" value={sortMode} onChange={(e) => setSortMode(e.target.value as 'end_asc' | 'size_desc')} title="排序规则">
            <option value="end_asc">按到期时间</option>
            <option value="size_desc">按面额大小</option>
          </select>
          <button className={btnXs} onClick={() => void load()}>
            刷新
          </button>
        </header>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-y border-[var(--line-soft)] bg-[var(--surface-2)]/50 text-left text-[11.5px] font-semibold text-[var(--ink-2)]">
                <th className="px-3.5 py-2.5 font-medium">账号</th>
                <th className="px-3.5 py-2.5 font-medium">UID</th>
                <th className="px-3.5 py-2.5 font-medium">域</th>
                <th className="px-3.5 py-2.5 font-medium">剩余积分</th>
                <th className="px-3.5 py-2.5 font-medium">总额度</th>
                <th className="px-3.5 py-2.5 font-medium">可用包数</th>
                <th className="px-3.5 py-2.5 font-medium">最近到期</th>
                <th className="px-3.5 py-2.5 font-medium">主要构成来源</th>
                <th className="px-3.5 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--line-soft)]/60">
              {list == null ? (
                <tr>
                  <td colSpan={9}>
                    <Loading>查询中（逐账号向上游实时查询）</Loading>
                  </td>
                </tr>
              ) : !paged.length ? (
                <tr>
                  <td colSpan={9}>
                    <Empty>{error ? '读取失败：' + error : '没有账号数据'}</Empty>
                  </td>
                </tr>
              ) : (
                paged.map((a) => {
                  if (a.error) {
                    return (
                      <tr key={a.uid} className="transition-colors hover:bg-[var(--surface-2)]/60">
                        <td className="px-3.5 py-2.5 font-medium">{a.nickname || a.uid.slice(0, 8)}</td>
                        <td className="px-3.5 py-2.5 font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{a.uid.slice(0, 8)}</td>
                        <td className="px-3.5 py-2.5">
                          <RealmTag>{a.realm || ''}</RealmTag>
                        </td>
                        <td colSpan={6} className="px-3.5 py-2.5 text-[var(--bad)]">
                          查询失败：{a.error}
                        </td>
                      </tr>
                    );
                  }
                  const srcs = bySource(a.packages || []);
                  const totalSize = srcs.reduce((s, x) => s + x.size, 0) || 1;
                  const availablePks = (a.packages || []).filter((p) => Number(p.remain) > 0).length;
                  const bs = expBatches(a.packages || []).filter((b) => expDaysLeft(b.date, today) >= 0);
                  const first = bs[0];
                  const days = first ? expDaysLeft(first.date, today) : null;
                  const dayWord = days === 0 ? '今天' : days === 1 ? '明天' : days != null ? days + '天后' : '';
                  const expColor = days == null ? '' : days <= 3 ? 'var(--bad)' : days <= 7 ? 'var(--warn)' : 'inherit';
                  return (
                    <tr key={a.uid} className="transition-colors hover:bg-[var(--surface-2)]/60">
                      <td className="px-3.5 py-2.5 font-medium text-[var(--ink)]">{a.nickname || a.uid.slice(0, 8)}</td>
                      <td className="px-3.5 py-2.5 font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{a.uid.slice(0, 8)}</td>
                      <td className="px-3.5 py-2.5">
                        <RealmTag>{a.realm || ''}</RealmTag>
                      </td>
                      <td className="tabular px-3.5 py-2.5 font-semibold text-[var(--ink)]">{fmtTok(a.remain)}</td>
                      <td className="tabular px-3.5 py-2.5 text-[var(--ink-2)]">{fmtTok(a.size)}</td>
                      <td className="tabular px-3.5 py-2.5 text-[var(--ink-2)]">
                        {availablePks} / {(a.packages || []).length}
                      </td>
                      <td className="px-3.5 py-2.5">
                        {first ? (
                          <span className="tabular font-medium" style={{ color: expColor }}>
                            <b>{first.date}</b> · {fmtTok(first.remain)} <span className="text-[11.5px] text-[var(--ink-3)]">({dayWord})</span>
                          </span>
                        ) : (
                          <span className="text-[var(--ink-3)]">—</span>
                        )}
                      </td>
                      <td className="px-3.5 py-2.5">
                        <div className="flex flex-wrap gap-1">
                          {srcs.slice(0, 3).map((s, i) => {
                            const pct = Math.round((s.size / totalSize) * 100);
                            return (
                              <span key={i} title={`${s.name} 共 ${fmtTok(s.size)}`} className="flex items-center gap-1 rounded-md bg-[var(--surface-2)] px-1.5 py-0.5 text-[11.5px]">
                                <i className="h-2 w-2 rounded-sm" style={{ background: PK_COLORS[i % PK_COLORS.length] }} />
                                <span className="max-w-[110px] truncate">{s.name.replace(/^CodeBuddy/, '')}</span> <b>{pct}%</b>
                              </span>
                            );
                          })}
                          {!srcs.length && <span className="text-[var(--ink-3)]">—</span>}
                        </div>
                      </td>
                      <td className="px-3.5 py-2.5 text-right">
                        <button className={btnXs} onClick={() => setDetailUid(a.uid)}>
                          包明细
                        </button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
        <Pager
          page={cur}
          totalPages={totalPages}
          total={sorted.length}
          pageSize={pageSize}
          pageSizeOptions={[10, 20, 50]}
          onPage={setPage}
          onPageSizeChange={(sz) => {
            setPageSize(sz);
            setPage(1);
          }}
        />
      </div>

      {detail && <PackagesModal a={detail} onClose={() => setDetailUid(null)} />}
    </div>
  );
}
