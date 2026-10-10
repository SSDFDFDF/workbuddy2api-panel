/* 模型与档位视图：上游清单 + 前端筛选排序分页 + 实测上限标注 + 促销价。 */
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type { ModelEntry, ProbeEntry } from '../types';
import { Tag, Empty, Loading, Pager } from '../components/ui';
import { btnXs, btnXsGhost } from '../components/buttons';
import { fmtK } from '../fmt';

function rateValue(m: ModelEntry): number {
  const raw = m.promo_credits != null && m.promo_credits !== '' ? m.promo_credits : m.credits;
  const n = parseFloat(String(raw == null ? '' : raw).replace(/[^\d.]/g, ''));
  return Number.isFinite(n) ? n : Infinity;
}

function searchText(m: ModelEntry): string {
  return [m.id, m.name, m.vendor, m.description, (m.tags || []).join(' ')].filter(Boolean).join(' ').toLowerCase();
}

function match(m: ModelEntry, f: { q: string; realm: string; cap: string; effort: string; promo: string }): boolean {
  if (f.q) {
    const text = searchText(m);
    for (const kw of f.q.toLowerCase().split(/\s+/).filter(Boolean)) {
      if (!text.includes(kw)) return false;
    }
  }
  if (f.realm && !m.id.startsWith(f.realm + ':')) return false;
  if (f.cap === 'tool' && !m.supports_tool_call) return false;
  if (f.cap === 'vision' && !m.supports_images) return false;
  if (f.cap === 'reasoning' && !m.supports_reasoning) return false;
  if (f.cap === 'default' && !m.is_default) return false;
  if (f.effort === 'off') {
    if (!m.can_disable_thinking) return false;
  } else if (f.effort && !(m.supported_efforts || []).includes(f.effort)) return false;
  const factor = m.promo_factor == null ? null : Number(m.promo_factor);
  if (f.promo === 'promo' && factor == null && !m.promo_label) return false;
  if (f.promo === 'free' && factor !== 0) return false;
  if (f.promo === 'discount' && !(factor != null && factor > 0)) return false;
  return true;
}

/* 倍率列：生效价 + 标签 + 划线牌价。 */
function RateCell({ m }: { m: ModelEntry }) {
  if (m.promo_factor != null && m.promo_credits) {
    return (
      <span title={m.promo_note} className="cursor-help">
        <b>{String(m.promo_credits)}</b>
        {m.promo_label && (
          <>
            {' '}
            <Tag tone="ok">{m.promo_label}</Tag>
          </>
        )}
        {m.credits ? (
          <>
            {' '}
            <s className="text-[11.5px] text-[var(--ink-3)]">{String(m.credits)}</s>
          </>
        ) : null}
      </span>
    );
  }
  if (m.promo_label) {
    return (
      <span title={m.promo_note} className="cursor-help">
        {m.credits ? String(m.credits) : '—'} <Tag tone="warn">{m.promo_label}</Tag>
      </span>
    );
  }
  return <>{m.credits ? String(m.credits) : '—'}</>;
}

/* 实测上限列。 */
function OutputCell({ m, pr }: { m: ModelEntry; pr?: ProbeEntry }) {
  if (!pr) return <td className="tabular">{m.max_output_tokens ? fmtK(m.max_output_tokens) : '—'}</td>;
  const days = pr.tested_at ? Math.floor((Date.now() - new Date(String(pr.tested_at).replace(' ', 'T')).getTime()) / 86400000) : null;
  const stale = days != null && days > 30 ? ' · ' + days + ' 天前' : '';
  const tip = `声称 ${pr.claimed ? fmtK(pr.claimed) : '?'} · 实测 ${pr.measured ? fmtK(pr.measured) : '?'}${pr.note ? ' · ' + pr.note : ''}${pr.tested_at ? ' · 探测于 ' + pr.tested_at : ''}`;
  if (pr.verdict === 'clamped' && pr.measured) {
    if (pr.claimed && pr.measured < pr.claimed) {
      const x = pr.claimed / pr.measured;
      return (
        <td className="tabular" title={tip}>
          <span className="font-semibold text-[var(--warn)]">{fmtK(pr.measured)} ⚠</span>
          <div className="text-[11px] text-[var(--ink-3)]">钳制 {(x >= 10 ? Math.round(x) : Math.round(x * 10) / 10) + '×' + stale}</div>
        </td>
      );
    }
    return (
      <td className="tabular" title={tip}>
        <span className="text-[var(--ok)]">
          {fmtK(pr.measured)}
          {pr.claimed && pr.measured > pr.claimed ? ' ↑' : ' ✓'}
        </span>
      </td>
    );
  }
  if (pr.verdict === 'at_least' && pr.measured) {
    return (
      <td className="tabular" title={tip}>
        <span className="text-[var(--ink-3)]">≥{fmtK(pr.measured)}</span>
      </td>
    );
  }
  return (
    <td className="tabular" title={tip}>
      <span className="text-[var(--ink-3)]">?</span>
      <div className="text-[11px] text-[var(--ink-3)]">未测出{stale}</div>
    </td>
  );
}

export function ModelsView() {
  const [all, setAll] = useState<ModelEntry[] | null>(null);
  const [probes, setProbes] = useState<Record<string, ProbeEntry>>({});
  const [error, setError] = useState('');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [f, setF] = useState({ q: '', realm: '', cap: '', effort: '', promo: '', sort: 'default' });
  const [loaded, setLoaded] = useState(false);

  const load = async () => {
    setError('');
    try {
      const [d, pr] = await Promise.all([api<{ models: ModelEntry[] }>('models'), api<{ probes?: Record<string, ProbeEntry> }>('model_probes').catch(() => ({ probes: {} }))]);
      setAll(d.models || []);
      setProbes(pr.probes || {});
      setLoaded(true);
    } catch (e) {
      setError((e as Error).message);
      setAll([]);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const probeOf = (id: string): ProbeEntry | undefined => {
    if (probes[id]) return probes[id];
    const k = Object.keys(probes).find((k) => k.endsWith(':' + id));
    return k ? probes[k] : undefined;
  };

  const list = useMemo(() => {
    const filtered = (all || []).filter((m) => match(m, f));
    const out = [...filtered];
    const num = (v: unknown) => {
      const n = Number(v || 0);
      return Number.isFinite(n) ? n : 0;
    };
    if (f.sort === 'rate') out.sort((a, b) => rateValue(a) - rateValue(b));
    else if (f.sort === 'context') out.sort((a, b) => num(b.context_length) - num(a.context_length));
    else if (f.sort === 'output') out.sort((a, b) => num(b.max_output_tokens) - num(a.max_output_tokens));
    else if (f.sort === 'name') out.sort((a, b) => a.id.localeCompare(b.id));
    return out;
  }, [all, f]);

  const totalPages = Math.max(1, Math.ceil(list.length / pageSize));
  const cur = Math.min(page, totalPages);
  const paged = list.slice((cur - 1) * pageSize, cur * pageSize);
  const probeHit = (all || []).filter((m) => probeOf(m.id)).length;

  const sel = 'wb-select px-2 py-1 text-[12.5px]';
  const setFilter = (k: keyof typeof f, v: string) => {
    setF((s) => ({ ...s, [k]: v }));
    setPage(1);
  };

  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
      <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
        <h3 className="text-[14px] font-semibold">
          模型能力 <span className="ml-1 text-[12px] font-normal text-[var(--ink-3)]">「重新获取」同时刷新网关模型校验目录（不在目录里的模型会被网关直接拒绝）；官方返回的最大输出仅为参考</span>
        </h3>
        <span className="flex-1" />
        <span className="text-[12px] text-[var(--ink-3)]">{loaded && all ? `${all.length} 个模型${probeHit ? ' · ' + probeHit + ' 个有实测上限' : ''}` : '实时查询上游'}</span>
        <button className={btnXs} onClick={() => void load()}>
          重新获取
        </button>
      </header>

      {/* 筛选栏 */}
      <div className="flex flex-wrap items-center gap-2 border-y border-[var(--line-soft)] px-4 py-2.5">
        <input
          type="search"
          placeholder="搜索模型 ID / 名称 / 描述 / 厂商"
          className="w-[220px] rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-2.5 py-1.5 text-[12.5px] outline-none focus:border-[var(--accent)]"
          value={f.q}
          onChange={(e) => setFilter('q', e.target.value.trim())}
          aria-label="搜索模型"
        />
        <select className={sel} value={f.realm} onChange={(e) => setFilter('realm', e.target.value)} aria-label="按域筛选">
          <option value="">全部域</option>
          <option value="cn">国内版 CN</option>
          <option value="global">国际版 Global</option>
        </select>
        <select className={sel} value={f.cap} onChange={(e) => setFilter('cap', e.target.value)} aria-label="按能力筛选">
          <option value="">全部能力</option>
          <option value="tool">支持工具调用</option>
          <option value="vision">支持视觉</option>
          <option value="reasoning">支持思考</option>
          <option value="default">默认模型</option>
        </select>
        <select className={sel} value={f.effort} onChange={(e) => setFilter('effort', e.target.value)} aria-label="按思考档位筛选">
          <option value="">全部档位</option>
          <option value="off">可关闭思考</option>
          <option value="low">low</option>
          <option value="medium">medium</option>
          <option value="high">high</option>
          <option value="xhigh">xhigh</option>
          <option value="max">max</option>
        </select>
        <select className={sel} value={f.promo} onChange={(e) => setFilter('promo', e.target.value)} aria-label="按价格筛选">
          <option value="">全部价格</option>
          <option value="promo">有折扣 / 限时免费</option>
          <option value="free">限时免费</option>
          <option value="discount">打折（非免费）</option>
        </select>
        <select className={sel} value={f.sort} onChange={(e) => setFilter('sort', e.target.value)} aria-label="排序方式">
          <option value="default">上游默认顺序</option>
          <option value="rate">积分倍率 低 → 高</option>
          <option value="context">上下文长度 大 → 小</option>
          <option value="output">最大输出 大 → 小</option>
          <option value="name">模型 ID A → Z</option>
        </select>
        <span className="flex-1" />
        <span className={'text-[12px] ' + (list.length !== (all || []).length ? 'text-[var(--accent)]' : 'text-[var(--ink-3)]')}>
          {all && all.length ? (list.length !== all.length ? `命中 ${list.length} / ${all.length} 个模型` : all.length + ' 个模型') : ''}
        </span>
        <button className={btnXsGhost} onClick={() => { setF({ q: '', realm: '', cap: '', effort: '', promo: '', sort: 'default' }); setPage(1); }} title="清空全部筛选条件">
          重置
        </button>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[13px]">
          <thead>
            <tr className="border-y border-[var(--line-soft)] bg-[var(--surface-2)]/50 text-left text-[11.5px] font-semibold text-[var(--ink-2)]">
              <th className="px-3.5 py-2.5 font-medium">模型 ID</th>
              <th className="px-3.5 py-2.5 font-medium">模型名称</th>
              <th className="px-3.5 py-2.5 font-medium">能力</th>
              <th className="px-3.5 py-2.5 font-medium">积分倍率</th>
              <th className="px-3.5 py-2.5 font-medium">默认档</th>
              <th className="px-3.5 py-2.5 font-medium">支持的思考档位</th>
              <th className="px-3.5 py-2.5 font-medium">上下文长度</th>
              <th className="px-3.5 py-2.5 font-medium">最大输出</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--line-soft)]/60">
            {all == null ? (
              <tr>
                <td colSpan={8}>
                  <Loading>{error ? error : '正在向上游查询…'}</Loading>
                </td>
              </tr>
            ) : !paged.length ? (
              <tr>
                <td colSpan={8}>
                  <Empty>{error || '没有符合当前筛选条件的模型'}</Empty>
                </td>
              </tr>
            ) : (
              paged.map((m) => {
                const eff = (m.supported_efforts || []).slice();
                if (m.can_disable_thinking && eff.length && !eff.includes('off')) eff.push('off（可关）');
                const pr = probeOf(m.id);
                return (
                  <tr key={m.id} className="transition-colors hover:bg-[var(--surface-2)]/60">
                    <td className="px-3.5 py-2.5 font-[family-name:var(--mono)] text-[12.5px] font-medium text-[var(--ink)]" title={m.description}>
                      {m.id}
                    </td>
                    <td className="px-3.5 py-2.5 text-[var(--ink-2)]">{m.name || '—'}</td>
                    <td className="px-3.5 py-2.5">
                      <div className="flex flex-wrap gap-1">
                        {m.is_default && <Tag tone="ok">默认</Tag>}
                        {m.supports_tool_call && <Tag tone="warn">工具</Tag>}
                        {m.supports_images && <Tag tone="warn">视觉</Tag>}
                        {m.supports_reasoning && !m.can_disable_thinking && <Tag tone="warn">思考常开</Tag>}
                        {!m.is_default && !m.supports_tool_call && !m.supports_images && <span className="text-[var(--ink-3)]">—</span>}
                      </div>
                    </td>
                    <td className="tabular px-3.5 py-2.5">
                      <RateCell m={m} />
                    </td>
                    <td className="px-3.5 py-2.5">{m.default_effort ? <Tag tone="ok">{m.default_effort}</Tag> : <span className="text-[var(--ink-3)]">—</span>}</td>
                    <td className="max-w-[240px] whitespace-normal px-3.5 py-2.5">
                      {eff.length ? (
                        <div className="flex flex-wrap gap-1">
                          {eff.map((e) => (
                            <Tag key={e} tone="warn">
                              {e}
                            </Tag>
                          ))}
                        </div>
                      ) : (
                        <span className="text-[12.5px] text-[var(--ink-3)]">{m.supports_reasoning ? '固定档 · 默认 ' + (m.default_effort || '?') : '不支持思考'}</span>
                      )}
                    </td>
                    <td className="tabular px-3.5 py-2.5">{m.context_length ? Math.round(m.context_length / 1000) + 'K' : '—'}</td>
                    <OutputCell m={m} pr={pr} />
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
        total={list.length}
        pageSize={pageSize}
        pageSizeOptions={[10, 20, 50, 100]}
        onPage={setPage}
        onPageSizeChange={(sz) => {
          setPageSize(sz);
          setPage(1);
        }}
      />
    </div>
  );
}
