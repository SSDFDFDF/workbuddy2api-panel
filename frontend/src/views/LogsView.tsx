/* 日志视图：
   - 顶部双 Tab 切换：【请求记录】（详细请求指标、来源、Token 读/取/缓存深度透视）与【运行日志】（按频道过滤的控制台日志流）
   - Token 维度全覆盖：提示词读入 (Prompt Tokens)、模型输出提取 (Completion Tokens)、上下文缓存命中 (Cache Hit)、缓存未命中 (Cache Miss)、缓存命中率等。
*/
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../api';
import { useTimeRange, TimeRangeControl } from '../components/TimeRange';
import { Tag, Empty, Pager } from '../components/ui';
import { btnXs } from '../components/buttons';
import { copyText } from '../clipboard';
import { toast } from '../toast';
import type { LogEntry, RequestEvent, RequestMetrics } from '../types';
import { fmtMs, fmtTok, fmtBytes, fmtTimeHM, fmtNum, cacheRatePct, cacheRateText } from '../fmt';

const inputCls =
  'rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-2.5 py-1.5 text-[12.5px] outline-none transition-all focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]/30';

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

function creditCell(e: RequestEvent) {
  if (!e.credit_known) return <span className="text-[var(--ink-3)]">—</span>;
  const v = Number(e.credit);
  return Number.isFinite(v) ? <span className="tabular font-medium">{v.toFixed(2)}</span> : <span className="text-[var(--ink-3)]">—</span>;
}

/* 丢弃字段展示 */
function droppedCell(e: RequestEvent) {
  const list = e.dropped || [];
  if (!list.length) return <span className="text-[var(--ink-3)]">—</span>;
  const shown = list.slice(0, 2);
  const rest = list.length - shown.length;
  return (
    <span className="flex max-w-[170px] items-center gap-1" title={list.join(' · ')}>
      {shown.map((k) => (
        <Tag key={k} tone="warn">
          <span className="tabular block max-w-[100px] truncate">{k}</span>
        </Tag>
      ))}
      {rest > 0 && <span className="text-[11px] font-medium text-[var(--ink-3)]">+{rest}</span>}
    </span>
  );
}

/* 单条请求展开详情卡片（含 Token 读、取、缓存全维度深度解析） */
function RequestDetailRow({ e, onCopy }: { e: RequestEvent; onCopy: (text: string, label: string) => void }) {
  const total = Number(e.total_tokens || 0) || Number(e.prompt_tokens || 0) + Number(e.completion_tokens || 0);
  const prompt = Number(e.prompt_tokens || 0);
  const completion = Number(e.completion_tokens || 0);
  const hit = Number(e.cache_hit_tokens || 0);
  const miss = Number(e.cache_miss_tokens || 0);
  const ratePct = cacheRatePct(hit, miss);
  const hasCacheData = hit > 0 || miss > 0;
  const hitRatioInPrompt = prompt > 0 && hit > 0 ? Math.min(100, Math.round((hit / prompt) * 100)) : null;

  return (
    <div className="border-t border-[var(--line-soft)] bg-[var(--surface-2)]/60 px-5 py-4 transition-all">
      {/* 顶部标题与快速复制 */}
      <div className="mb-3.5 flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line-soft)] pb-2.5">
        <div className="flex items-center gap-2">
          <span className="font-semibold text-[13px] text-[var(--ink)]">请求深度剖析</span>
          <span className="font-[family-name:var(--mono)] text-[11.5px] text-[var(--ink-3)]">{e.request_id || '无请求 ID'}</span>
        </div>
        <div className="flex items-center gap-2">
          {e.request_id && (
            <button
              type="button"
              className={btnXs}
              onClick={() => onCopy(e.request_id!, '请求 ID')}
            >
              复制 ID
            </button>
          )}
          <button
            type="button"
            className={btnXs}
            onClick={() => onCopy(JSON.stringify(e, null, 2), '完整 JSON')}
          >
            复制 JSON
          </button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {/* 左侧：Token 读、取、缓存核心指标透视 */}
        <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3.5 shadow-xs">
          <div className="mb-2.5 flex items-center justify-between">
            <div className="flex items-center gap-1.5 font-medium text-[12.5px] text-[var(--ink)]">
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" strokeWidth="1.5" className="text-[var(--accent)]">
                <circle cx="8" cy="8" r="6" />
                <path d="M8 5v6M5 8h6" />
              </svg>
              <span>Token 读 · 取 · 缓存指标</span>
            </div>
            {hasCacheData && (
              <span
                className={
                  'rounded-full px-2 py-0.5 text-[11px] font-semibold tabular ' +
                  (hit > 0 ? 'bg-[var(--ok-soft)] text-[var(--ok)]' : 'bg-[var(--warn-soft)] text-[var(--warn)]')
                }
              >
                {hit > 0 ? `⚡ 缓存命中 ${cacheRateText(hit, miss)}` : '未命中缓存 (0%)'}
              </span>
            )}
          </div>

          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {/* 读 (Prompt Tokens) */}
            <div className="rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] p-2.5">
              <div className="flex items-center gap-1 text-[11px] text-[var(--ink-3)]">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--accent)]" />
                <span>读 (Prompt)</span>
              </div>
              <div className="tabular mt-1 text-[16px] font-bold text-[var(--ink)]">{fmtNum(prompt)}</div>
              <div className="mt-0.5 text-[10.5px] text-[var(--ink-3)]">{fmtTok(prompt)} tok</div>
            </div>

            {/* 取 (Completion Tokens) */}
            <div className="rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] p-2.5">
              <div className="flex items-center gap-1 text-[11px] text-[var(--ink-3)]">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-[#a855f7]" />
                <span>取 (Completion)</span>
              </div>
              <div className="tabular mt-1 text-[16px] font-bold text-[var(--ink)]">{fmtNum(completion)}</div>
              <div className="mt-0.5 text-[10.5px] text-[var(--ink-3)]">{fmtTok(completion)} tok</div>
            </div>

            {/* 缓存命中 (Cache Hit) */}
            <div className="rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] p-2.5">
              <div className="flex items-center gap-1 text-[11px] text-[var(--ink-3)]">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />
                <span>缓存命中 (Hit)</span>
              </div>
              <div className="tabular mt-1 text-[16px] font-bold text-[var(--ok)]">{fmtNum(hit)}</div>
              <div className="mt-0.5 text-[10.5px] text-[var(--ink-3)]">
                {hitRatioInPrompt != null ? `占读入 ${hitRatioInPrompt}%` : fmtTok(hit) + ' tok'}
              </div>
            </div>

            {/* 缓存未命中 (Cache Miss) */}
            <div className="rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] p-2.5">
              <div className="flex items-center gap-1 text-[11px] text-[var(--ink-3)]">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-[var(--warn)]" />
                <span>未命中 (Miss)</span>
              </div>
              <div className="tabular mt-1 text-[16px] font-bold text-[var(--ink-2)]">{fmtNum(miss)}</div>
              <div className="mt-0.5 text-[10.5px] text-[var(--ink-3)]">{fmtTok(miss)} tok</div>
            </div>
          </div>

          {/* 缓存命中进度条与总体摘要 */}
          <div className="mt-3 rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)]/50 px-3 py-2 text-[11.5px]">
            <div className="flex items-center justify-between text-[var(--ink-2)]">
              <span>总 Token 消耗：<b className="tabular font-semibold text-[var(--ink)]">{fmtNum(total)}</b> ({fmtTok(total)})</span>
              <span>
                本次积分：{e.credit_known ? <b className="tabular text-[var(--ink)]">{Number(e.credit).toFixed(2)} credit</b> : '—'}
              </span>
            </div>

            {hasCacheData && ratePct != null && (
              <div className="mt-2">
                <div className="mb-1 flex justify-between text-[11px] text-[var(--ink-3)]">
                  <span>缓存命中率分布</span>
                  <span className="tabular font-medium text-[var(--ok)]">{ratePct.toFixed(1)}%</span>
                </div>
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--line)]">
                  <div
                    className="h-full rounded-full bg-[var(--ok)] transition-all"
                    style={{ width: `${Math.min(100, Math.max(0, ratePct))}%` }}
                  />
                </div>
              </div>
            )}
            {!hasCacheData && (
              <div className="mt-1 text-[11px] text-[var(--ink-3)] italic">
                注：当前模型或上游未返回缓存（Cache Hit/Miss）细分维度。
              </div>
            )}
          </div>
        </div>

        {/* 右侧：请求环境与元数据 */}
        <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3.5 shadow-xs">
          <div className="mb-2.5 flex items-center gap-1.5 font-medium text-[12.5px] text-[var(--ink)]">
            <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" strokeWidth="1.5" className="text-[var(--accent)]">
              <rect x="2" y="3" width="12" height="10" rx="2" />
              <path d="M5 7h6M5 10h4" />
            </svg>
            <span>请求属性与调用来源</span>
          </div>

          <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-[12px]">
            <div>
              <span className="text-[var(--ink-3)]">请求时间：</span>
              <span className="tabular ml-1 text-[var(--ink)]">{e.time ? new Date(e.time).toLocaleString('zh-CN') : '—'}</span>
            </div>
            <div>
              <span className="text-[var(--ink-3)]">总耗时：</span>
              <span className="tabular ml-1 font-medium text-[var(--ink)]">{fmtMs(e.duration_ms)}</span>
              {e.ttfb_ms ? <span className="tabular ml-1 text-[11px] text-[var(--ink-3)]">(首字 {fmtMs(e.ttfb_ms)})</span> : null}
            </div>
            <div>
              <span className="text-[var(--ink-3)]">调用模型：</span>
              <span className="tabular ml-1 font-medium text-[var(--ink)]">{e.model || '—'}</span>
            </div>
            <div>
              <span className="text-[var(--ink-3)]">使用账号：</span>
              <span className="ml-1 text-[var(--ink)]">{e.account || '—'}</span>
            </div>
            <div className="col-span-2">
              <span className="text-[var(--ink-3)]">来源客户端：</span>
              <span className="tabular ml-1 font-[family-name:var(--mono)] text-[11.5px] text-[var(--ink)]">
                {e.client_ip || '来源 IP 未记录'}
              </span>
              {e.user_agent && (
                <div className="mt-0.5 break-all text-[11px] text-[var(--ink-3)]">{e.user_agent}</div>
              )}
            </div>

            {/* 提示词指纹与丢弃字段 */}
            {(e.prompt_mode || e.prompt_preset || e.prompt_sha256) && (
              <div className="col-span-2 border-t border-[var(--line-soft)] pt-1.5 text-[11.5px]">
                <span className="text-[var(--ink-3)]">提示词指纹：</span>
                <span className="ml-1 text-[var(--ink-2)]">
                  {e.prompt_mode ? `mode=${e.prompt_mode}` : ''}
                  {e.prompt_preset ? ` · preset=${e.prompt_preset}` : ''}
                  {e.prompt_chars ? ` · ${e.prompt_chars} 字符` : ''}
                  {e.prompt_sha256 ? ` · sha ${e.prompt_sha256}` : ''}
                </span>
              </div>
            )}
            {e.dropped && e.dropped.length > 0 && (
              <div className="col-span-2 border-t border-[var(--line-soft)] pt-1.5 text-[11.5px]">
                <span className="text-[var(--warn)] font-medium">丢弃字段：</span>
                <span className="ml-1 text-[var(--ink-2)]">{e.dropped.join(' · ')}</span>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export function LogsView() {
  // 顶层主 Tab: 'requests' (请求记录) | 'runtime' (运行日志)
  const [activeTab, setActiveTab] = useState<'requests' | 'runtime'>('requests');

  // 数据状态
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [metrics, setMetrics] = useState<RequestMetrics | null>(null);
  const [reqRows, setReqRows] = useState<RequestEvent[]>([]);

  // 运行日志相关控制
  const [ch, setCh] = useState('all');
  const [pin, setPin] = useState(true);
  const [logFilter, setLogFilter] = useState('');

  // 请求记录相关控制
  const [q, setQ] = useState('');
  const [outcome, setOutcome] = useState('');
  const [limit, setLimit] = useState(100);
  const [reqPage, setReqPage] = useState(1);
  const [reqPageSize, setReqPageSize] = useState(20);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const range = useTimeRange('0');
  const boxRef = useRef<HTMLPreElement>(null);
  const inFlight = useRef(false);

  const load = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const rq = new URLSearchParams();
      const qs = range.qs(false);
      if (qs) rq.set('from', qs.match(/from=(\d+)/)?.[1] || '');
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
  }, [limit, range]);

  useEffect(() => {
    void load();
    const id = setInterval(() => {
      if (!document.hidden) void load();
    }, 5000);
    return () => clearInterval(id);
  }, [load]);

  // 运行日志自动滚动
  const box = boxRef.current;
  useEffect(() => {
    if (box && pin && activeTab === 'runtime') {
      box.scrollTop = box.scrollHeight;
    }
  });

  // 复制剪贴板辅助
  const handleCopy = (text: string, label: string) => {
    copyText(text)
      .then(() => toast(`已复制 ${label}`, 'ok'))
      .catch(() => toast('复制失败', 'err'));
  };

  // 运行日志过滤
  const filteredLogs = useMemo(() => {
    let list = (entries || []).filter((e) => ch === 'all' || e.ch === ch);
    if (logFilter.trim()) {
      const kw = logFilter.trim().toLowerCase();
      list = list.filter((e) => (e.text || '').toLowerCase().includes(kw));
    }
    return list;
  }, [entries, ch, logFilter]);

  const logCounts = useMemo(() => {
    const c = { task: 0, chat: 0, sys: 0 } as Record<string, number>;
    for (const e of entries || []) c[e.ch || ''] = (c[e.ch || ''] || 0) + 1;
    return c;
  }, [entries]);

  // 请求记录筛选
  const reqFiltered = useMemo(() => {
    return (reqRows || []).filter((e) => {
      if (outcome && String(e.outcome || '') !== outcome) return false;
      if (q) {
        const text = [
          e.client_ip,
          e.user_agent,
          e.model,
          e.account,
          e.request_id,
          e.prompt_preset,
          e.prompt_mode,
          e.prompt_sha256,
          ...(e.dropped || []),
        ]
          .filter(Boolean)
          .join(' ')
          .toLowerCase();
        for (const kw of q.toLowerCase().split(/\s+/).filter(Boolean)) {
          if (!text.includes(kw)) return false;
        }
      }
      return true;
    });
  }, [reqRows, outcome, q]);

  // 请求记录分页
  const reqTotalPages = Math.max(1, Math.ceil(reqFiltered.length / reqPageSize));
  const curReqPage = Math.min(reqPage, reqTotalPages);
  const pagedReqs = reqFiltered.slice((curReqPage - 1) * reqPageSize, curReqPage * reqPageSize);

  const a = metrics?.archive;
  const hasSource = (reqRows || []).some((e) => e.client_ip || e.user_agent);

  return (
    <div className="flex flex-col gap-4">
      {/* 顶栏 Tab 分段切换器 */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-2 shadow-xs">
        <div className="flex items-center gap-1.5 rounded-lg bg-[var(--surface-2)] p-1">
          {/* Tab 1: 请求记录 */}
          <button
            type="button"
            className={
              'flex items-center gap-2 rounded-md px-3.5 py-1.5 text-[13px] font-medium transition-all ' +
              (activeTab === 'requests'
                ? 'bg-[var(--surface)] text-[var(--accent)] font-semibold shadow-xs'
                : 'text-[var(--ink-2)] hover:text-[var(--ink)]')
            }
            onClick={() => setActiveTab('requests')}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.5">
              <path d="M2.5 4h11M2.5 8h7M2.5 12h11" />
            </svg>
            <span>请求记录</span>
            <span
              className={
                'rounded-full px-2 py-0.2 text-[11px] tabular ' +
                (activeTab === 'requests'
                  ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                  : 'bg-[var(--line-soft)] text-[var(--ink-3)]')
              }
            >
              {reqRows.length ? fmtTok(reqRows.length) : '0'}
            </span>
          </button>

          {/* Tab 2: 运行日志 */}
          <button
            type="button"
            className={
              'flex items-center gap-2 rounded-md px-3.5 py-1.5 text-[13px] font-medium transition-all ' +
              (activeTab === 'runtime'
                ? 'bg-[var(--surface)] text-[var(--accent)] font-semibold shadow-xs'
                : 'text-[var(--ink-2)] hover:text-[var(--ink)]')
            }
            onClick={() => setActiveTab('runtime')}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.5">
              <rect x="2" y="2.5" width="12" height="11" rx="2" />
              <path d="M5 6.5l2.5 1.5L5 9.5M8.5 9.5h2.5" />
            </svg>
            <span>运行日志</span>
            <span
              className={
                'rounded-full px-2 py-0.2 text-[11px] tabular ' +
                (activeTab === 'runtime'
                  ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                  : 'bg-[var(--line-soft)] text-[var(--ink-3)]')
              }
            >
              {entries.length ? `${entries.length} 行` : '0'}
            </span>
          </button>
        </div>

        {/* 顶部辅助状态 */}
        <div className="flex items-center gap-2.5 text-[12px] text-[var(--ink-3)]">
          {activeTab === 'requests' && metrics && (
            <span className="tabular hidden sm:inline">
              已完成 {fmtTok(metrics.completed)} · 平均耗时 {fmtMs(metrics.avg_duration_ms)} · 并发 {metrics.in_flight || 0}
            </span>
          )}
          {activeTab === 'runtime' && (
            <span className="tabular hidden sm:inline">
              任务 {logCounts.task || 0} · 对话 {logCounts.chat || 0} · 系统 {logCounts.sys || 0}
            </span>
          )}
          <button className={btnXs} onClick={() => void load()}>
            刷新数据
          </button>
        </div>
      </div>

      {/* ── TAB 1：请求记录 ────────────────────────────────────────── */}
      {activeTab === 'requests' && (
        <div className="flex flex-col gap-3.5">
          {/* 核心指标统计微卡条 */}
          {metrics && (
            <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-5">
              <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3 shadow-xs">
                <div className="text-[11.5px] text-[var(--ink-3)]">已完成请求</div>
                <div className="tabular mt-1 text-[18px] font-bold text-[var(--ink)]">{fmtNum(metrics.completed)}</div>
              </div>
              <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3 shadow-xs">
                <div className="text-[11.5px] text-[var(--ink-3)]">整体成功率</div>
                <div className="tabular mt-1 text-[18px] font-bold text-[var(--ok)]">
                  {metrics.success_rate == null ? '—' : Number(metrics.success_rate).toFixed(1) + '%'}
                </div>
              </div>
              <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3 shadow-xs">
                <div className="text-[11.5px] text-[var(--ink-3)]">HTTP 成功率</div>
                <div className="tabular mt-1 text-[18px] font-bold text-[var(--ink)]">
                  {metrics.http_success_rate == null ? '—' : Number(metrics.http_success_rate).toFixed(1) + '%'}
                </div>
              </div>
              <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3 shadow-xs">
                <div className="text-[11.5px] text-[var(--ink-3)]">平均响应耗时</div>
                <div className="tabular mt-1 text-[18px] font-bold text-[var(--ink)]">{fmtMs(metrics.avg_duration_ms)}</div>
              </div>
              <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3 shadow-xs">
                <div className="text-[11.5px] text-[var(--ink-3)]">进行中并发 / 归档</div>
                <div className="tabular mt-1 text-[16px] font-bold text-[var(--ink)]">
                  {metrics.in_flight || 0}{' '}
                  <span className="text-[11.5px] font-normal text-[var(--ink-3)]">
                    {a?.enabled ? `(归档 ${fmtBytes(a.bytes)})` : '(无归档)'}
                  </span>
                </div>
              </div>
            </div>
          )}

          {/* 表格容器卡片 */}
          <div className="overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-xs">
            {/* 筛选与搜索工具栏 */}
            <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line-soft)] bg-[var(--surface)] px-4 py-3">
              <div className="relative">
                <input
                  type="search"
                  className={inputCls + ' w-[240px] pl-8'}
                  placeholder="搜索 IP / UA / 模型 / 账号 / ID"
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value.trim());
                    setReqPage(1);
                  }}
                  aria-label="搜索请求记录"
                />
                <svg
                  viewBox="0 0 16 16"
                  width="13"
                  height="13"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--ink-3)]"
                >
                  <circle cx="7" cy="7" r="4.5" />
                  <path d="M10.5 10.5L14 14" />
                </svg>
              </div>

              <select
                className="wb-select px-2.5 py-1.5 text-[12.5px]"
                value={outcome}
                onChange={(e) => {
                  setOutcome(e.target.value);
                  setReqPage(1);
                }}
                aria-label="按结果筛选"
              >
                <option value="">全部结果</option>
                <option value="success">成功</option>
                <option value="http_error">HTTP 错误</option>
                <option value="stream_error">流错误</option>
                <option value="interrupted">中断</option>
              </select>

              <TimeRangeControl range={range} onChange={() => void load()} />

              <select
                className="wb-select px-2.5 py-1.5 text-[12.5px]"
                value={limit}
                onChange={(e) => setLimit(Number(e.target.value))}
                aria-label="读取条数"
              >
                <option value={100}>最近 100 条</option>
                <option value={300}>最近 300 条</option>
                <option value={1000}>最近 1000 条</option>
              </select>

              <span className="flex-1" />

              <span className={'text-[12px] tabular ' + (q || outcome ? 'text-[var(--accent)] font-medium' : 'text-[var(--ink-3)]')}>
                {!reqRows?.length
                  ? ''
                  : `${q || outcome ? '命中 ' + reqFiltered.length + ' / ' + reqRows.length + ' 条' : '共 ' + reqRows.length + ' 条'}${
                      hasSource ? '' : ' · 来源未记录'
                    }`}
              </span>

              <button className={btnXs} onClick={() => void load()}>
                重新读取
              </button>
            </div>

            {/* 请求记录数据表 */}
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[12.5px]">
                <thead>
                  <tr className="border-b border-[var(--line-soft)] bg-[var(--surface-2)]/40 text-left text-[11.5px] font-medium text-[var(--ink-3)]">
                    <th className="px-3.5 py-2.5">时间</th>
                    <th className="px-3 py-2.5">结果</th>
                    <th className="px-3 py-2.5">模型</th>
                    <th className="px-3 py-2.5">账号</th>
                    <th className="px-3 py-2.5">调用来源 (IP / UA)</th>
                    <th className="px-3 py-2.5">耗时</th>
                    <th className="px-3 py-2.5" title="包含读入 (Prompt) 与 取出 (Completion) Token">
                      Token (总计 · 读 · 取)
                    </th>
                    <th className="px-3 py-2.5" title="上游上下文缓存命中与命中率">
                      缓存 (命中 · 命中率)
                    </th>
                    <th className="px-3 py-2.5">积分</th>
                    <th className="px-3 py-2.5">丢弃字段</th>
                    <th className="px-3 py-2.5 text-right">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[var(--line-soft)]">
                  {pagedReqs.map((e, i) => {
                    const rowKey = e.request_id || `${e.time}-${i}`;
                    const isExpanded = expandedId === rowKey;
                    const totalTok = Number(e.total_tokens || 0) || Number(e.prompt_tokens || 0) + Number(e.completion_tokens || 0);
                    const promptTok = Number(e.prompt_tokens || 0);
                    const compTok = Number(e.completion_tokens || 0);
                    const hitTok = Number(e.cache_hit_tokens || 0);
                    const missTok = Number(e.cache_miss_tokens || 0);
                    const hasHit = hitTok > 0;
                    const hasMiss = missTok > 0;

                    return (
                      <tr
                        key={rowKey}
                        className={
                          'group cursor-pointer transition-colors hover:bg-[var(--surface-2)]/60 ' +
                          (isExpanded ? 'bg-[var(--surface-2)]/40' : '')
                        }
                        onClick={() => setExpandedId(isExpanded ? null : rowKey)}
                      >
                        {/* 时间 */}
                        <td className="tabular px-3.5 py-2 text-[var(--ink-2)]">
                          <span title={e.time ? new Date(e.time).toLocaleString('zh-CN') : ''}>{fmtTimeHM(e.time)}</span>
                        </td>

                        {/* 结果 */}
                        <td className="px-3 py-2">{outcomeTag(e)}</td>

                        {/* 模型 */}
                        <td className="max-w-[150px] truncate px-3 py-2 font-medium text-[var(--ink)]" title={e.model}>
                          {e.model || '—'}
                        </td>

                        {/* 账号 */}
                        <td className="max-w-[110px] truncate px-3 py-2 text-[var(--ink-2)]" title={e.account}>
                          {e.account || '—'}
                        </td>

                        {/* 调用来源 (IP + UA) */}
                        <td className="max-w-[170px] px-3 py-2">
                          {e.client_ip ? (
                            <span className="tabular block truncate font-[family-name:var(--mono)] text-[11.5px] text-[var(--ink)]">
                              {e.client_ip}
                            </span>
                          ) : (
                            <span className="text-[var(--ink-3)]">—</span>
                          )}
                          {e.user_agent && (
                            <span className="block truncate text-[10.5px] text-[var(--ink-3)]" title={e.user_agent}>
                              {e.user_agent}
                            </span>
                          )}
                        </td>

                        {/* 耗时 */}
                        <td className="tabular px-3 py-2 text-[var(--ink)]">
                          {fmtMs(e.duration_ms)}
                          {e.ttfb_ms ? (
                            <span className="block text-[10.5px] text-[var(--ink-3)]" title={`首字耗时: ${e.ttfb_ms}ms`}>
                              首字 {fmtMs(e.ttfb_ms)}
                            </span>
                          ) : null}
                        </td>

                        {/* Token (总计 · 读 Prompt · 取 Completion) */}
                        <td className="tabular px-3 py-2">
                          {totalTok ? (
                            <div>
                              <span className="font-semibold text-[var(--ink)]">{fmtTok(totalTok)}</span>
                              {(promptTok > 0 || compTok > 0) && (
                                <div className="text-[10.5px] text-[var(--ink-3)]">
                                  <span>读 {fmtTok(promptTok)}</span>
                                  <span className="mx-1 opacity-40">·</span>
                                  <span>取 {fmtTok(compTok)}</span>
                                </div>
                              )}
                            </div>
                          ) : (
                            <span className="text-[var(--ink-3)]">—</span>
                          )}
                        </td>

                        {/* 缓存 (命中 · 命中率) */}
                        <td className="tabular px-3 py-2">
                          {hasHit ? (
                            <div>
                              <span className="inline-flex items-center gap-1 rounded bg-[var(--ok-soft)] px-1.5 py-0.5 text-[11px] font-semibold text-[var(--ok)]">
                                <span>⚡ {fmtTok(hitTok)}</span>
                                <span className="opacity-75">({cacheRateText(hitTok, missTok)})</span>
                              </span>
                              {hasMiss && (
                                <div className="text-[10.5px] text-[var(--ink-3)]" title={`缓存未命中: ${missTok}`}>
                                  未命中 {fmtTok(missTok)}
                                </div>
                              )}
                            </div>
                          ) : hasMiss ? (
                            <span className="inline-block rounded bg-[var(--warn-soft)] px-1.5 py-0.5 text-[11px] font-medium text-[var(--warn)]">
                              未命中 (0%)
                            </span>
                          ) : (
                            <span className="text-[var(--ink-3)]">—</span>
                          )}
                        </td>

                        {/* 积分 */}
                        <td className="tabular px-3 py-2">{creditCell(e)}</td>

                        {/* 丢弃字段 */}
                        <td className="px-3 py-2">{droppedCell(e)}</td>

                        {/* 展开/折叠操作 */}
                        <td className="px-3 py-2 text-right">
                          <button
                            type="button"
                            className={
                              'rounded p-1 text-[var(--ink-3)] transition-colors hover:bg-[var(--surface-2)] hover:text-[var(--ink)] ' +
                              (isExpanded ? 'text-[var(--accent)] rotate-180' : '')
                            }
                            onClick={(ev) => {
                              ev.stopPropagation();
                              setExpandedId(isExpanded ? null : rowKey);
                            }}
                            title={isExpanded ? '收起详情' : '展开详情'}
                            aria-label="展开或收起详情"
                          >
                            <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" strokeWidth="1.5">
                              <path d="M4 6l4 4 4-4" />
                            </svg>
                          </button>
                        </td>
                      </tr>
                    );
                  })}

                  {/* 展开详情卡片行 */}
                  {pagedReqs.map((e, i) => {
                    const rowKey = e.request_id || `${e.time}-${i}`;
                    if (expandedId !== rowKey) return null;
                    return (
                      <tr key={`exp-${rowKey}`}>
                        <td colSpan={11} className="p-0">
                          <RequestDetailRow e={e} onCopy={handleCopy} />
                        </td>
                      </tr>
                    );
                  })}

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

            {/* 分页控制台 */}
            <Pager
              page={curReqPage}
              totalPages={reqTotalPages}
              total={reqFiltered.length}
              pageSize={reqPageSize}
              pageSizeOptions={[10, 20, 50, 100]}
              onPage={setReqPage}
              onPageSizeChange={(sz) => {
                setReqPageSize(sz);
                setReqPage(1);
              }}
            />
          </div>
        </div>
      )}

      {/* ── TAB 2：运行日志 ────────────────────────────────────────── */}
      {activeTab === 'runtime' && (
        <div className="flex flex-col gap-3">
          {/* 终端卡片容器 */}
          <div className="overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-xs">
            {/* 终端顶部操作栏 */}
            <div className="flex flex-wrap items-center gap-2.5 border-b border-[var(--line-soft)] bg-[var(--surface)] px-4 py-3">
              {/* macOS 风格三色灯装饰 */}
              <div className="hidden sm:flex items-center gap-1.5 mr-1">
                <span className="h-2.5 w-2.5 rounded-full bg-[#f0655f]/80" />
                <span className="h-2.5 w-2.5 rounded-full bg-[#f5b544]/80" />
                <span className="h-2.5 w-2.5 rounded-full bg-[#3ddc97]/80" />
              </div>

              {/* 频道切换 Pill 按钮 */}
              <div className="flex items-center gap-1 rounded-lg bg-[var(--surface-2)] p-0.5">
                {(
                  [
                    ['all', '全部', entries.length],
                    ['task', '任务', logCounts.task || 0],
                    ['chat', '对话', logCounts.chat || 0],
                    ['sys', '系统', logCounts.sys || 0],
                  ] as const
                ).map(([k, label, count]) => (
                  <button
                    key={k}
                    type="button"
                    className={
                      'flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[12px] font-medium transition-all ' +
                      (ch === k
                        ? 'bg-[var(--surface)] text-[var(--accent)] font-semibold shadow-xs'
                        : 'text-[var(--ink-2)] hover:text-[var(--ink)]')
                    }
                    onClick={() => setCh(k)}
                  >
                    <span>{label}</span>
                    <span className="text-[10.5px] opacity-75 tabular">({count})</span>
                  </button>
                ))}
              </div>

              {/* 日志内容关键词搜索 */}
              <div className="relative">
                <input
                  type="search"
                  className={inputCls + ' w-[180px] pl-7 py-1 text-[12px]'}
                  placeholder="过滤日志内容…"
                  value={logFilter}
                  onChange={(e) => setLogFilter(e.target.value)}
                  aria-label="过滤日志"
                />
                <svg
                  viewBox="0 0 16 16"
                  width="12"
                  height="12"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--ink-3)]"
                >
                  <circle cx="7" cy="7" r="4.5" />
                  <path d="M10.5 10.5L14 14" />
                </svg>
              </div>

              <span className="flex-1" />

              <span className="tabular text-[12px] text-[var(--ink-3)]">
                {logFilter ? `命中 ${filteredLogs.length} / ${entries.length} 行` : `${filteredLogs.length} 行`}
              </span>

              <button
                type="button"
                className={btnXs}
                onClick={() => {
                  const txt = filteredLogs.map((e) => `[${e.ch || 'sys'}] ${e.ts || ''} ${e.text || ''}`).join('\n');
                  handleCopy(txt, '日志文本');
                }}
              >
                复制日志
              </button>

              <button
                type="button"
                className={btnXs + (pin ? ' border-[var(--accent)] text-[var(--accent)]' : '')}
                onClick={() => setPin((p) => !p)}
              >
                自动滚动：{pin ? '已开启' : '已暂停'}
              </button>
            </div>

            {/* 控制台代码视窗 */}
            <pre
              ref={boxRef}
              className="m-0 max-h-[580px] min-h-[360px] overflow-auto bg-[#0a0c10] p-4 font-[family-name:var(--mono)] text-[12px] leading-relaxed text-[#c9d1d9] whitespace-pre-wrap select-text"
            >
              {filteredLogs.length ? (
                filteredLogs.map((e, i) => {
                  const text = e.text || '';
                  const isErr = /error|失败|错误/.test(text);
                  const isWarn = /warn|冷却|熔断/.test(text);
                  const lvlCls = isErr ? 'text-[#ff7b72] font-semibold' : isWarn ? 'text-[#f2cc60]' : 'text-[#c9d1d9]';
                  const t = e.ts ? new Date(e.ts).toLocaleTimeString('zh-CN', { hour12: false }) : '';
                  const chTag =
                    ch === 'all' && e.ch ? (
                      <span
                        className={
                          'mr-2 inline-block rounded px-1.5 py-px text-[10px] font-medium not-italic ' +
                          (e.ch === 'task'
                            ? 'bg-[#58a6ff]/20 text-[#58a6ff]'
                            : e.ch === 'chat'
                              ? 'bg-[#3fb950]/20 text-[#3fb950]'
                              : 'bg-[#30363d] text-[#8b949e]')
                        }
                      >
                        {{ task: '任务', chat: '对话', sys: '系统' }[e.ch] || e.ch}
                      </span>
                    ) : null;

                  return (
                    <div key={i} className={'py-0.5 transition-colors hover:bg-white/5 ' + lvlCls}>
                      <span className="mr-2 text-[#6e7681] select-none tabular">{t}</span>
                      {chTag}
                      <span>{text}</span>
                    </div>
                  );
                })
              ) : (
                <div className="flex h-36 items-center justify-center text-[#6e7681]">暂无日志记录</div>
              )}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}
