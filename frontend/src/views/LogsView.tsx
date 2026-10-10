/* 日志视图：请求记录表（筛选/时间范围）+ 频道运行日志（自动滚动）。 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { useTimeRange, TimeRangeControl } from '../components/TimeRange';
import { Tag, Empty } from '../components/ui';
import { btnXs } from '../components/buttons';
import type { LogEntry, RequestEvent, RequestMetrics } from '../types';
import { fmtMs, fmtTok, fmtBytes, fmtTimeHM, cacheRateText } from '../fmt';

const reqCls = 'w-full rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-2.5 py-1.5 text-[12.5px] outline-none focus:border-[var(--accent)]';

function outcomeTag(e: RequestEvent) {
  const o = String(e.outcome || '');
  const label = { success: '成功', http_error: 'HTTP 错误', stream_error: '流错误', interrupted: '中断' }[o] || o || '—';
  const tone = o === 'success' ? 'ok' : o === 'interrupted' ? 'warn' : o ? 'bad' : 'mute';
  return (
    <Tag tone={tone as 'ok' | 'warn' | 'bad' | 'mute'}>
      {String(e.status ?? '—')} {label}
    </Tag>
  );
}

function tokenCell(e: RequestEvent): string {
  const total = Number(e.total_tokens || 0) || Number(e.prompt_tokens || 0) + Number(e.completion_tokens || 0);
  return total ? fmtTok(total) : '—';
}

function creditCell(e: RequestEvent) {
  if (!e.credit_known) return <span className="text-[var(--ink-3)]">—</span>;
  const v = Number(e.credit);
  return Number.isFinite(v) ? v.toFixed(2) : <span className="text-[var(--ink-3)]">—</span>;
}

/* 丢弃字段：跨协议入口接受但无法表达的客户端字段（如 Codex 的 include / reasoning 历史）。
   推理历史会折叠为 input[]，通常 1-2 项，超出折叠为 +N，完整列表在 title 里。 */
function droppedCell(e: RequestEvent) {
  const list = e.dropped || [];
  if (!list.length) return <span className="text-[var(--ink-3)]">—</span>;
  const shown = list.slice(0, 2);
  const rest = list.length - shown.length;
  return (
    <span className="flex max-w-[190px] items-center gap-1" title={list.join(' · ')}>
      {shown.map((k) => (
        <Tag key={k} tone="warn">
          <span className="tabular block max-w-[120px] truncate">{k}</span>
        </Tag>
      ))}
      {rest > 0 && <span className="text-[11px] text-[var(--ink-3)]">+{rest}</span>}
    </span>
  );
}

function rowTip(e: RequestEvent): string {
  const label = { success: '成功', http_error: 'HTTP 错误', stream_error: '流错误', interrupted: '中断' }[String(e.outcome || '')] || e.outcome || '—';
  const bits = [
    fmtTimeHM(e.time),
    `${String(e.status ?? '—')} ${label}`,
    e.model || '—',
    e.account || '—',
    e.client_ip || '—',
    e.user_agent || '—',
    fmtMs(e.duration_ms),
    fmtTok(Number(e.total_tokens || 0) || Number(e.prompt_tokens || 0) + Number(e.completion_tokens || 0)) + ' tok',
    e.credit_known ? Number(e.credit).toFixed(2) + ' credit' : 'credit —',
    cacheRateText(e.cache_hit_tokens, e.cache_miss_tokens) === '—' ? '' : '命中 ' + cacheRateText(e.cache_hit_tokens, e.cache_miss_tokens),
    e.dropped?.length ? '丢弃 ' + e.dropped.join(' · ') : '',
    e.request_id || '—',
    e.prompt_mode ? `prompt=${e.prompt_mode}${e.prompt_preset ? ' ' + e.prompt_preset : ''}${e.prompt_chars ? ' ' + e.prompt_chars + ' 字' : ''}${e.prompt_sha256 ? ' sha ' + e.prompt_sha256 : ''}` : '',
  ];
  return bits.filter(Boolean).join(' | ');
}

export function LogsView() {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [metrics, setMetrics] = useState<RequestMetrics | null>(null);
  const [reqRows, setReqRows] = useState<RequestEvent[]>([]);
  const [ch, setCh] = useState('all');
  const [pin, setPin] = useState(true);
  const [q, setQ] = useState('');
  const [outcome, setOutcome] = useState('');
  const [limit, setLimit] = useState(100);
  const range = useTimeRange('0');
  const boxRef = useRef<HTMLPreElement>(null);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const rq = new URLSearchParams();
      const qs = range.qs(false);
      if (qs) rq.set('from', (qs.match(/from=(\d+)/)?.[1]) || '');
      if (qs.includes('to=')) rq.set('to', qs.match(/to=(\d+)/)?.[1] || '');
      rq.set('limit', String(limit));
      const [d, m, rows] = await Promise.all([
        api<{ entries: LogEntry[] }>('logs'),
        api<RequestMetrics>('request_metrics').catch(() => ({}) as RequestMetrics),
        api<{ entries: RequestEvent[] }>('request_logs?' + rq.toString()).catch(() => ({ entries: [] })),
      ]);
      setEntries(d.entries || []);
      setMetrics(m || {});
      const archiveOn = !!(m && m.archive && m.archive.enabled);
      setReqRows(archiveOn ? rows.entries || [] : m.recent || []);
    } catch {
      /* 概览已提示 */
    } finally {
      inFlight.current = false;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [limit, range.st.preset, range.st.from, range.st.to]);

  useEffect(() => {
    void load();
    const id = setInterval(() => {
      if (!document.hidden) void load();
    }, 5000);
    return () => clearInterval(id);
  }, [load]);

  /* 自动滚动：贴底时跟随。 */
  const box = boxRef.current;
  useEffect(() => {
    if (box && pin) box.scrollTop = box.scrollHeight;
  });

  const filtered = (entries || []).filter((e) => ch === 'all' || e.ch === ch);
  const counts = { task: 0, chat: 0, sys: 0 } as Record<string, number>;
  for (const e of entries || []) counts[e.ch || ''] = (counts[e.ch || ''] || 0) + 1;

  /* 请求记录前端筛选。 */
  const reqFiltered = (reqRows || []).filter((e) => {
    if (outcome && String(e.outcome || '') !== outcome) return false;
    if (q) {
      const text = [e.client_ip, e.user_agent, e.model, e.account, e.request_id, e.prompt_preset, e.prompt_mode, e.prompt_sha256, ...(e.dropped || [])]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      for (const kw of q.toLowerCase().split(/\s+/).filter(Boolean)) {
        if (!text.includes(kw)) return false;
      }
    }
    return true;
  });

  const a = metrics?.archive;
  const hasSource = (reqRows || []).some((e) => e.client_ip || e.user_agent);

  return (
    <div className="flex flex-col gap-4">
      {/* 请求记录 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">请求记录</h3>
          <span className="flex-1" />
          {metrics && (
            <span className="tabular text-[12px] text-[var(--ink-3)]">
              已完成 {fmtTok(metrics.completed)} · 成功 {metrics.success_rate == null ? '—' : Number(metrics.success_rate).toFixed(1) + '%'} · HTTP{' '}
              {metrics.http_success_rate == null ? '—' : Number(metrics.http_success_rate).toFixed(1) + '%'} · 平均 {fmtMs(metrics.avg_duration_ms)} · 进行中 {metrics.in_flight || 0}
            </span>
          )}
          {a && (
            <span className="text-[12px] text-[var(--ink-3)]">
              {a.enabled ? `JSONL 归档 ${fmtBytes(a.bytes)}${a.dropped_writes ? ' · 丢弃 ' + a.dropped_writes + ' 条' : ''}${a.last_error ? ' · 错误：' + a.last_error : ''}` : '仅内存指标，JSONL 归档已关闭'}
            </span>
          )}
        </header>
        <div className="flex flex-wrap items-center gap-2 border-y border-[var(--line-soft)] px-4 py-2.5">
          <input type="search" className={reqCls + ' w-[220px]'} placeholder="搜索 IP / UA / 模型 / 账号 / 请求 ID / 丢弃字段" value={q} onChange={(e) => setQ(e.target.value.trim())} aria-label="搜索请求记录" />
          <select className="wb-select px-2 py-1.5 text-[12.5px]" value={outcome} onChange={(e) => setOutcome(e.target.value)} aria-label="按结果筛选">
            <option value="">全部结果</option>
            <option value="success">成功</option>
            <option value="http_error">HTTP 错误</option>
            <option value="stream_error">流错误</option>
            <option value="interrupted">中断</option>
          </select>
          <TimeRangeControl range={range} onChange={() => void load()} />
          <select className="wb-select px-2 py-1.5 text-[12.5px]" value={limit} onChange={(e) => setLimit(Number(e.target.value))} aria-label="读取条数">
            <option value={100}>最近 100 条</option>
            <option value={300}>最近 300 条</option>
            <option value={1000}>最近 1000 条</option>
          </select>
          <span className="flex-1" />
          <span className={'text-[12px] ' + (q || outcome ? 'text-[var(--accent)]' : hasSource ? 'text-[var(--ink-3)]' : 'text-[var(--ink-3)] italic')}>
            {!reqRows?.length ? '' : `${q || outcome ? '命中 ' + reqFiltered.length + ' / ' + reqRows.length + ' 条' : reqRows.length + ' 条'}${hasSource ? '' : ' · 来源未记录'}`}
          </span>
          <button className={btnXs} onClick={() => void load()}>
            重新读取
          </button>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[12.5px]">
            <thead>
              <tr className="border-y border-[var(--line-soft)] text-left text-[11.5px] text-[var(--ink-3)]">
                <th className="px-3 py-2 font-medium">时间</th>
                <th className="px-3 py-2 font-medium">结果</th>
                <th className="px-3 py-2 font-medium">模型</th>
                <th className="px-3 py-2 font-medium">账号</th>
                <th className="px-3 py-2 font-medium">来源 IP</th>
                <th className="px-3 py-2 font-medium">User-Agent</th>
                <th className="px-3 py-2 font-medium">耗时</th>
                <th className="px-3 py-2 font-medium">Token</th>
                <th className="px-3 py-2 font-medium">积分</th>
                <th className="px-3 py-2 font-medium">丢弃字段</th>
                <th className="px-3 py-2 font-medium">请求 ID</th>
              </tr>
            </thead>
            <tbody>
              {reqFiltered.map((e, i) => (
                <tr key={i} title={rowTip(e)}>
                  <td className="tabular px-3 py-1.5">{fmtTimeHM(e.time)}</td>
                  <td className="px-3 py-1.5">{outcomeTag(e)}</td>
                  <td className="max-w-[160px] truncate px-3 py-1.5">{e.model || '—'}</td>
                  <td className="max-w-[110px] truncate px-3 py-1.5">{e.account || '—'}</td>
                  <td className="px-3 py-1.5">
                    {e.client_ip ? (
                      <span className="tabular block max-w-[110px] truncate font-[family-name:var(--mono)] text-[11.5px]">{e.client_ip}</span>
                    ) : (
                      <span className="text-[var(--ink-3)]">—</span>
                    )}
                  </td>
                  <td className="px-3 py-1.5">
                    {e.user_agent ? (
                      <span className="block max-w-[160px] truncate text-[11.5px] text-[var(--ink-2)]">{e.user_agent}</span>
                    ) : (
                      <span className="text-[var(--ink-3)]">—</span>
                    )}
                  </td>
                  <td className="tabular px-3 py-1.5">{fmtMs(e.duration_ms)}</td>
                  <td className="tabular px-3 py-1.5">{tokenCell(e)}</td>
                  <td className="tabular px-3 py-1.5">{creditCell(e)}</td>
                  <td className="px-3 py-1.5">{droppedCell(e)}</td>
                  <td className="px-3 py-1.5">
                    {e.request_id ? (
                      <span className="tabular block max-w-[120px] truncate font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{e.request_id}</span>
                    ) : (
                      <span className="text-[var(--ink-3)]">—</span>
                    )}
                  </td>
                </tr>
              ))}
              {!reqFiltered.length && (
                <tr>
                  <td colSpan={11}>
                    <Empty>{reqRows?.length ? '没有符合当前筛选条件的请求记录' : '暂无请求记录'}</Empty>
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 运行日志 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">运行日志</h3>
          <span className="flex-1" />
          <div className="flex gap-1">
            {([['all', '全部'], ['task', '任务'], ['chat', '对话'], ['sys', '系统']] as const).map(([k, label]) => (
              <button
                key={k}
                className={'rounded-full px-2.5 py-0.5 text-[12px] ' + (ch === k ? 'bg-[var(--accent-soft)] font-medium text-[var(--accent)]' : 'text-[var(--ink-3)] hover:text-[var(--ink)]')}
                onClick={() => setCh(k)}
              >
                {label}
              </button>
            ))}
          </div>
          <span className="tabular text-[12px] text-[var(--ink-3)]">
            {ch === 'all' ? `任务 ${counts.task || 0} · 对话 ${counts.chat || 0} · 系统 ${counts.sys || 0}` : `${filtered.length} 行`}
          </span>
          <button className={btnXs} onClick={() => setPin((p) => !p)}>
            自动滚动：{pin ? '开' : '关'}
          </button>
        </header>
        <pre ref={boxRef} className="m-0 max-h-[420px] overflow-auto px-4 pb-4 font-[family-name:var(--mono)] text-[12px] leading-relaxed whitespace-pre-wrap">
          {filtered.length ? (
            filtered.map((e, i) => {
              const text = e.text || '';
              const lvl = /error|失败|错误/.test(text) ? 'text-[var(--bad)]' : /warn|冷却|熔断/.test(text) ? 'text-[var(--warn)]' : '';
              const t = e.ts ? new Date(e.ts).toLocaleTimeString('zh-CN', { hour12: false }) : '';
              const chTag = ch === 'all' && e.ch ? (
                <i
                  className={
                    'mr-2 inline-block rounded px-1.5 py-px text-[10px] not-italic ' +
                    (e.ch === 'task' ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : e.ch === 'chat' ? 'bg-[var(--ok-soft)] text-[var(--ok)]' : 'bg-[var(--surface-2)] text-[var(--ink-3)]')
                  }
                >
                  {{ task: '任务', chat: '对话', sys: '系统' }[e.ch] || e.ch}
                </i>
              ) : null;
              return (
                <span key={i} className={'block ' + lvl}>
                  {chTag}
                  {t} {text}
                </span>
              );
            })
          ) : (
            <span className="text-[var(--ink-3)]">暂无日志</span>
          )}
        </pre>
      </div>
    </div>
  );
}
