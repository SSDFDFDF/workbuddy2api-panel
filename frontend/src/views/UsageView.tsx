/* 用量视图：KPI 卡 + Token 时序图 + 维度明细表（账号/模型/域）+ 积分扣除表。 */
import { useCallback, useEffect, useState } from 'react';
import { api } from '../api';
import { useTimeRange, TimeRangeControl } from '../components/TimeRange';
import { Kpi, SegBar, Empty, Loading } from '../components/ui';
import { StackedBars, toChartPoints } from '../components/StackedBars';
import { btnXs } from '../components/buttons';
import type { UsageRow, UsageSnapshot } from '../types';
import { fmtTok, fmtMs, fmtRate, fmtCredit, trimFixed, cacheRatePct } from '../fmt';

const PAGE = 20;

function sortRows(rows: UsageRow[], sort: string): UsageRow[] {
  const num = (v: unknown) => {
    const n = Number(v || 0);
    return Number.isFinite(n) ? n : 0;
  };
  const val = (a: UsageRow) =>
    sort === 'requests' ? num(a.requests) : sort === 'errors' ? num(a.errors) : sort === 'latency' ? num(a.avg_latency_ms) : num(a.total_tokens);
  return [...rows].sort((a, b) => val(b) - val(a));
}

/* 明细一行：名称 + 域（可选）+ 请求/失败/Token 构成条 + 性能（可选）。 */
function DimRow({ x, withRealm, withPerf }: { x: UsageRow; withRealm: boolean; withPerf: boolean }) {
  const tt = Number(x.total_tokens || 0);
  const pt = Number(x.prompt_tokens || 0);
  const ct = Number(x.completion_tokens || 0);
  const uid = String(x.key || '');
  return (
    <tr>
      <td>
        {withRealm ? (
          <>
            <div className="font-medium">{x.nickname || uid.slice(0, 8) || '—'}</div>
            <div className="text-[11px] text-[var(--ink-3)]">{x.extra || uid.slice(0, 8)}</div>
          </>
        ) : (
          <span className="font-[family-name:var(--mono)] text-[12.5px]">{x.key || '—'}</span>
        )}
      </td>
      {withRealm && <td className="tabular">{x.realm || ''}</td>}
      <td className="tabular">{fmtTok(x.requests)}</td>
      <td className="tabular">{x.errors ? <span className="text-[var(--warn)]">{fmtTok(x.errors)}</span> : '—'}</td>
      <td className="tabular">
        {fmtTok(tt)}
        {tt > 0 && (
          <div className="mt-1 max-w-[180px]">
            <SegBar
              segs={[
                { value: pt, color: 'var(--accent)', title: `prompt ${fmtTok(pt)} (${((pt / tt) * 100).toFixed(1)}%)` },
                { value: ct, color: 'var(--ok)', title: `completion ${fmtTok(ct)} (${((ct / tt) * 100).toFixed(1)}%)` },
              ]}
            />
          </div>
        )}
      </td>
      {withPerf && (
        <>
          <td className="tabular">{fmtMs(x.avg_latency_ms)}</td>
          <td className="tabular">{fmtRate(x.avg_tokens_per_second)}</td>
        </>
      )}
    </tr>
  );
}

/* 缓存命中率格子。 */
function CacheCell({ hit, miss }: { hit?: number; miss?: number }) {
  const pct = cacheRatePct(hit, miss);
  if (pct == null) return <span className="text-[var(--ink-3)]">—</span>;
  const cls = pct >= 90 ? 'text-[var(--ok)]' : pct >= 80 ? 'text-[var(--warn)]' : 'text-[var(--bad)]';
  return (
    <span className={cls} title={`命中 ${fmtTok(hit)} / 未命中 ${fmtTok(miss)} tok`}>
      {trimFixed(pct.toFixed(1))}%
    </span>
  );
}

export function UsageView() {
  const [data, setData] = useState<UsageSnapshot | null>(null);
  const [error, setError] = useState('');
  const [dim, setDim] = useState<'account' | 'model' | 'realm'>('account');
  const [creditDim, setCreditDim] = useState<'account' | 'model'>('account');
  const [sort, setSort] = useState('total');
  const range = useTimeRange('72');

  const load = useCallback(async () => {
    setError('');
    try {
      void api('models').catch(() => {}); // 预热倍率缓存（可选增强）
      const d = await api<UsageSnapshot>('usage?' + range.qs(true));
      setData(d);
    } catch (e) {
      setError((e as Error).message);
      setData(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range.st.preset, range.st.from, range.st.to]);

  useEffect(() => {
    void load();
  }, [load]);

  const d = data;
  const t = d?.totals || {};
  const total = Number(t.total_tokens || 0);
  const pt = Number(t.prompt_tokens || 0);
  const ct = Number(t.completion_tokens || 0);
  const reqs = Number(t.requests || 0);
  const errs = Number(t.errors || 0);
  const okRate = reqs ? ((reqs - errs) / reqs) * 100 : null;

  const dimMeta = { account: { withRealm: true, withPerf: true }, model: { withRealm: false, withPerf: false }, realm: { withRealm: false, withPerf: false } }[dim];
  const dimRows = sortRows(d?.[({ account: 'by_account', model: 'by_model', realm: 'by_realm' } as const)[dim]] || [], sort);

  const creditRows = creditDim === 'model' ? d?.credit_by_model || [] : d?.credit_by_account || [];

  const pts = toChartPoints(d?.series || []);
  const peak = pts.length ? pts.reduce((a, b) => (b.tt > a.tt ? b : a)) : null;
  const avg = pts.length ? pts.reduce((s, p) => s + p.tt, 0) / pts.length : 0;

  const rangeEcho = d?.window_from
    ? String(d.window_from).replace('T', ' ').slice(0, 16) + (d.window_to ? ' → ' + String(d.window_to).replace('T', ' ').slice(0, 16) : ' → 现在')
    : '';
  const note =
    (rangeEcho || range.label() ? (rangeEcho || range.label()) + ' · ' : '') +
    (d?.buckets || 0) + ' 个分桶' +
    (d?.since ? ' · 数据自 ' + d.since.replace('T', ' ') : '');

  return (
    <div className="flex flex-col gap-4">
      {/* 总览 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">用量与积分总览</h3>
          <span className="flex-1" />
          <span className="text-[12px] text-[var(--ink-3)]" title={note}>
            {error ? '读取失败：' + error : note || '—'}
          </span>
          <TimeRangeControl range={range} onChange={() => void load()} />
          <button className={btnXs} onClick={() => void load()}>
            刷新
          </button>
        </header>

        {error ? (
          <Empty>读取用量失败：{error}</Empty>
        ) : !d ? (
          <Loading />
        ) : (
          <div className="px-4 pb-4">
            <div className="flex flex-wrap gap-3">
              <Kpi
                v={fmtTok(reqs)}
                k="请求数"
                tone={errs ? 'warn' : 'accent'}
                sub={okRate == null ? '—' : errs ? `成功率 ${okRate.toFixed(1)}% · 失败 ${errs} 次` : '成功率 100%'}
              />
              <Kpi
                v={fmtTok(total)}
                k="总 token"
                tone="accent"
                sub={`prompt ${fmtTok(pt)} · completion ${fmtTok(ct)}`}
              >
                {total > 0 && (
                  <div className="mt-2">
                    <SegBar
                      segs={[
                        { value: pt, color: 'var(--accent)', title: `prompt ${((pt / total) * 100).toFixed(1)}%` },
                        { value: ct, color: 'var(--ok)', title: `completion ${((ct / total) * 100).toFixed(1)}%` },
                      ]}
                    />
                  </div>
                )}
              </Kpi>
              <Kpi v={fmtMs(t.avg_latency_ms)} k="平均延迟" sub={t.avg_tokens_per_second ? '吐字 ' + fmtRate(t.avg_tokens_per_second) : '无速率样本'} />
              <Kpi v={fmtCredit(t.credits)} k="扣除积分" tone="accent" sub="按上游 usage.credit 累计" />
              <Kpi
                v={t.credits_per_1m_tokens != null && t.credit_tokens ? trimFixed(Number(t.credits_per_1m_tokens).toFixed(4)) : '—'}
                k="平均积分 / 1M Token"
                tone="ok"
                sub={`${t.credit_samples || 0} 个有效样本 · 越低越划算`}
              />
              <Kpi
                v={cacheRatePct(t.cache_hit_tokens, t.cache_miss_tokens) == null ? '—' : trimFixed(cacheRatePct(t.cache_hit_tokens, t.cache_miss_tokens)!.toFixed(1)) + '%'}
                k="缓存命中率"
                tone="mute"
                sub="上游前缀缓存命中比例；低命中意味着费用数倍放大"
              />
            </div>

            {/* 时序图 */}
            <div className="mt-4">
              <div className="mb-1.5 flex items-center gap-2 text-[12px] text-[var(--ink-3)]">
                <span className="font-medium text-[var(--ink-2)]">Token 时序</span>
                  {pts.length ? (
                    <span>
                      {pts.length} 个点 · 峰值 {fmtTok(peak ? peak.tt : 0)} · 均值 {fmtTok(avg)}
                    </span>
                  ) : (
                    <span>—</span>
                  )}
                <span className="flex-1" />
                <span className="flex items-center gap-1">
                  <i className="h-2 w-2 rounded-sm bg-[var(--accent)]" />prompt
                  <i className="ml-2 h-2 w-2 rounded-sm bg-[var(--ok)]" />completion
                </span>
              </div>
              {pts.length ? <StackedBars pts={pts} /> : <Empty>暂无用量数据。发起一次对话后再刷新。</Empty>}
            </div>
          </div>
        )}
      </div>

      {/* 明细 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <div className="flex gap-1">
            {([['account', '按账号'], ['model', '按模型'], ['realm', '按域']] as const).map(([k, label]) => (
              <button
                key={k}
                className={
                  'rounded-md px-2.5 py-1 text-[12.5px] ' +
                  (dim === k ? 'bg-[var(--accent-soft)] font-medium text-[var(--accent)]' : 'text-[var(--ink-3)] hover:text-[var(--ink)]')
                }
                onClick={() => setDim(k)}
              >
                {label}
                {d ? <span className="ml-1 text-[11px] opacity-70">{(d[({ account: 'by_account', model: 'by_model', realm: 'by_realm' } as const)[k]] || []).length}</span> : null}
              </button>
            ))}
          </div>
          <span className="flex-1" />
          <span className="text-[12px] text-[var(--ink-3)]">{dimRows.length} 行 · 请求数含失败尝试</span>
          <label className="flex items-center gap-1 text-[12px] text-[var(--ink-3)]">
            排序
            <select className="wb-select px-2 py-1" value={sort} onChange={(e) => setSort(e.target.value)}>
              <option value="total">合计 Token</option>
              <option value="requests">请求数</option>
              <option value="errors">失败数</option>
              <option value="latency">平均延迟</option>
            </select>
          </label>
        </header>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-y border-[var(--line-soft)] text-left text-[11.5px] text-[var(--ink-3)]">
                <th className="px-3 py-2 font-medium">{dim === 'account' ? '账号' : dim === 'model' ? '模型' : '域'}</th>
                {dimMeta.withRealm && <th className="px-3 py-2 font-medium">域</th>}
                <th className="px-3 py-2 font-medium">请求</th>
                <th className="px-3 py-2 font-medium">失败</th>
                <th className="px-3 py-2 font-medium">Token</th>
                {dimMeta.withPerf && <th className="px-3 py-2 font-medium">均延迟</th>}
                {dimMeta.withPerf && <th className="px-3 py-2 font-medium">均速率</th>}
              </tr>
            </thead>
            <tbody>
              {dimRows.slice(0, PAGE).map((x, i) => (
                <DimRow key={i} x={x} withRealm={dimMeta.withRealm} withPerf={dimMeta.withPerf} />
              ))}
              {!dimRows.length && (
                <tr>
                  <td colSpan={7}>
                    <Empty>{error ? '读取用量失败' : '暂无数据'}</Empty>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 积分扣除 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">积分扣除</h3>
          <div className="flex gap-1">
            {([['account', '按账号'], ['model', '按模型']] as const).map(([k, label]) => (
              <button
                key={k}
                className={
                  'rounded-md px-2.5 py-1 text-[12.5px] ' +
                  (creditDim === k ? 'bg-[var(--accent-soft)] font-medium text-[var(--accent)]' : 'text-[var(--ink-3)] hover:text-[var(--ink)]')
                }
                onClick={() => setCreditDim(k)}
              >
                {label}
                {d ? <span className="ml-1 text-[11px] opacity-70">{(k === 'model' ? d.credit_by_model || [] : d.credit_by_account || []).length}</span> : null}
              </button>
            ))}
          </div>
          <span className="flex-1" />
          <span className="text-[12px] text-[var(--ink-3)]">仅统计与积分同时观测到的 Token</span>
        </header>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-y border-[var(--line-soft)] text-left text-[11.5px] text-[var(--ink-3)]">
                <th className="px-3 py-2 font-medium">{creditDim === 'model' ? '模型' : '账号'}</th>
                {creditDim === 'model' && <th className="px-3 py-2 font-medium">积分倍率</th>}
                <th className="px-3 py-2 font-medium">请求</th>
                <th className="px-3 py-2 font-medium">扣除积分</th>
                <th className="px-3 py-2 font-medium">有效样本 Token</th>
                <th className="px-3 py-2 font-medium">积分 / 1M Token</th>
                <th className="px-3 py-2 font-medium">缓存命中率</th>
              </tr>
            </thead>
            <tbody>
              {creditRows.slice(0, PAGE).map((r, i) => (
                <tr key={i}>
                  <td>
                    {creditDim === 'model' ? (
                      <span className="font-[family-name:var(--mono)] text-[12.5px]">{r.key || '—'}</span>
                    ) : (
                      <>
                        <div className="font-medium">{r.nickname || String(r.key || '').slice(0, 8) || '—'}</div>
                        <div className="text-[11px] text-[var(--ink-3)]">{r.realm || ''} · {String(r.key || '').slice(0, 8)}</div>
                      </>
                    )}
                  </td>
                  {creditDim === 'model' && <td className="tabular">{r.rate ? 'x' + r.rate : '—'}</td>}
                  <td className="tabular">{fmtTok(r.requests)}</td>
                  <td className="tabular">{fmtCredit(r.credits)}</td>
                  <td className="tabular">{fmtTok(r.credit_tokens)}</td>
                  <td className="tabular">
                    {r.credit_samples && r.credit_tokens && r.credits_per_1m_tokens != null ? trimFixed(Number(r.credits_per_1m_tokens).toFixed(4)) : '—'}
                  </td>
                  <td>
                    <CacheCell hit={r.cache_hit_tokens} miss={r.cache_miss_tokens} />
                  </td>
                </tr>
              ))}
              {!creditRows.length && (
                <tr>
                  <td colSpan={7}>
                    <Empty>{error ? '读取用量失败' : '暂无积分扣除记录；升级前仅含 Token 的历史不会伪造积分。'}</Empty>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
