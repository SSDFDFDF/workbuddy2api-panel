/* 通用小组件：Tag、SegBar、Empty、State、Dots、Kpi、Pager。
   全站唯一实现，各视图按语义取用。 */
import type { ReactNode } from 'react';

/* Tag 语义标签（ok/warn/bad/mute/accent）。 */
export function Tag({ tone = 'mute', children, title }: { tone?: 'ok' | 'warn' | 'bad' | 'mute' | 'accent'; children: ReactNode; title?: string }) {
  const map = {
    ok: 'bg-[var(--ok-soft)] text-[var(--ok)] border-[var(--ok)]/20',
    warn: 'bg-[var(--warn-soft)] text-[var(--warn)] border-[var(--warn)]/20',
    bad: 'bg-[var(--bad-soft)] text-[var(--bad)] border-[var(--bad)]/20',
    mute: 'bg-[var(--surface-2)] text-[var(--ink-3)] border-[var(--line)]',
    accent: 'bg-[var(--accent-soft)] text-[var(--accent)] border-[var(--accent)]/20',
  } as const;
  return (
    <span title={title} className={'inline-flex items-center rounded-md border px-1.5 py-0.5 text-[11px] leading-tight font-medium ' + map[tone]}>
      {children}
    </span>
  );
}

/* RealmTag 域标（国内/国际/企业）。 */
export function RealmTag({ children }: { children: ReactNode }) {
  return (
    <span className="ml-1.5 inline-block rounded border border-[color-mix(in_srgb,var(--accent)_22%,transparent)] bg-[var(--accent-soft)] px-1 py-px text-[10px] leading-[1.4] text-[var(--accent)] align-[1px]">
      {children}
    </span>
  );
}

export interface SegSpec {
  value: number;
  color?: string;
  title?: string;
}

/* SegBar 水平堆叠条（全站唯一的条形基元）。
   total 指定分母（默认各段之和）；无正数值段时返回 null。 */
export function SegBar({ segs, total, cls, aria }: { segs: SegSpec[]; total?: number; cls?: string; aria?: string }) {
  const list = segs.filter((s) => Number(s.value) > 0);
  if (!list.length) return null;
  const sum = total && total > 0 ? total : list.reduce((s, x) => s + Number(x.value), 0);
  return (
    <span
      className={'inline-flex h-[5px] w-full max-w-[220px] gap-px overflow-hidden rounded-sm bg-[var(--surface-2)] ' + (cls || '')}
      role="img"
      aria-label={aria || (list[0].title || '')}
    >
      {list.map((s, i) => (
        <i
          key={i}
          title={s.title}
          style={{ width: ((Number(s.value) / sum) * 100).toFixed(3) + '%', background: s.color || 'var(--accent)' }}
          className="block h-full shrink-0"
        />
      ))}
    </span>
  );
}

/* Empty 空态占位。 */
export function Empty({ big, children }: { big?: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-1 px-4 py-10 text-center text-[13px] text-[var(--ink-3)]">
      {big && <div className="text-[15px] font-medium text-[var(--ink-2)]">{big}</div>}
      {children}
    </div>
  );
}

/* State 行内状态行（加载/错误/成功提示，面板弹窗内常用）。 */
export function State({ kind, children }: { kind?: 'err' | 'ok'; children: ReactNode }) {
  return (
    <div
      className={
        'rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] px-3.5 py-2.5 text-[13px] ' +
        (kind === 'err' ? 'text-[var(--bad)]' : kind === 'ok' ? 'text-[var(--ok)]' : 'text-[var(--ink-2)]')
      }
    >
      {children}
    </div>
  );
}

/* Dots 加载点动画（"加载中···"）。 */
export function Dots({ children }: { children?: ReactNode }) {
  return (
    <span className="relative">
      {children || '加载中'}
      <span className="ml-0.5 inline-block w-4 text-left after:inline after:content-['···']" style={{ animation: 'wb-dots 1.2s steps(1) infinite' }} />
    </span>
  );
}

/* Kpi 指标卡。 */
export function Kpi({
  v,
  k,
  sub,
  tone = 'soft',
  children,
}: {
  v: ReactNode;
  k: ReactNode;
  sub?: ReactNode;
  tone?: 'soft' | 'ok' | 'warn' | 'bad' | 'accent' | 'mute';
  children?: ReactNode;
}) {
  const map = {
    soft: 'border-l-[var(--ink-3)]',
    ok: 'border-l-[var(--ok)]',
    warn: 'border-l-[var(--warn)]',
    bad: 'border-l-[var(--bad)]',
    accent: 'border-l-[var(--accent)]',
    mute: 'border-l-[var(--line)]',
  } as const;
  return (
    <div className={'flex-1 rounded-xl border border-[var(--line)] border-l-[3px] bg-[var(--surface)] p-4 shadow-xs transition-all ' + map[tone]}>
      <div className="text-[12px] font-medium text-[var(--ink-3)]">{k}</div>
      <div className="tabular mt-1 text-[22px] font-semibold leading-tight text-[var(--ink)]">{v}</div>
      {children}
      {sub && <div className="mt-1.5 text-[11.5px] leading-snug text-[var(--ink-3)]">{sub}</div>}
    </div>
  );
}

/* Pager 轻量翻页条。 */
export function Pager({ page, totalPages, total, onPage }: { page: number; totalPages: number; total: number; onPage: (p: number) => void }) {
  if (totalPages <= 1) return null;
  return (
    <div className="flex items-center justify-between px-4 py-2.5 text-[12px] text-[var(--ink-3)]">
      <div className="tabular">
        共 {total} 项 · 第 {page} / {totalPages} 页
      </div>
      <div className="flex gap-2">
        <button className="btn-xs" disabled={page <= 1} onClick={() => onPage(page - 1)}>
          上一页
        </button>
        <button className="btn-xs" disabled={page >= totalPages} onClick={() => onPage(page + 1)}>
          下一页
        </button>
      </div>
    </div>
  );
}

/* Loading 状态行（表格/区块内）。 */
export function Loading({ children }: { children?: ReactNode }) {
  return (
    <div className="flex items-center justify-center gap-2 px-4 py-8 text-[13px] text-[var(--ink-3)]">
      <Dots>{children || '加载中'}</Dots>
    </div>
  );
}
