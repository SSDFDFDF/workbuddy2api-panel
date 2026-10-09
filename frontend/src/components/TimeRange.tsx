/* TimeRange 时间范围控件（用量 / 请求记录共用）：
   预设（今天/近N小时/全部历史/自定义）→ 查询参数。
   「今天」与「自定义」由前端算好 from/to 再发（浏览器本地时区）；
   滚动预设发 hours（服务端整点对齐）。 */
import { useState } from 'react';

export const TRANGE_PRESETS: [string, string][] = [
  ['today', '今天'],
  ['24', '近 24 小时'],
  ['72', '近 3 天'],
  ['168', '近 7 天'],
  ['720', '近 30 天'],
  ['0', '全部历史'],
  ['custom', '自定义…'],
];

export interface RangeQuery {
  from?: number; // unix 秒
  to?: number; // unix 秒
  hours?: number; // 滚动窗口小时（0 = 全部）
}

interface St {
  preset: string;
  from: string; // datetime-local 值
  to: string;
}

function dtLocalValue(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

function midnight(): Date {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  return d;
}

export function useTimeRange(initial = '72') {
  const [st, setSt] = useState<St>(() => ({
    preset: initial,
    from: dtLocalValue(midnight()),
    to: dtLocalValue(new Date()),
  }));

  const setPreset = (preset: string) => setSt((s) => ({ ...s, preset }));

  const setCustom = (from: string, to: string) => setSt((s) => ({ ...s, from, to, preset: 'custom' }));

  /* 查询参数。rolling=true → 滚动预设发 hours；今天/自定义发 from/to。 */
  const query = (): RangeQuery => {
    if (st.preset === 'custom') {
      const q: RangeQuery = {};
      if (st.from) q.from = Math.floor(new Date(st.from).getTime() / 1000);
      if (st.to) q.to = Math.floor(new Date(st.to).getTime() / 1000);
      return q;
    }
    if (st.preset === 'today') return { from: Math.floor(midnight().getTime() / 1000) };
    if (st.preset === '0') return { hours: 0 };
    return { hours: Number(st.preset) || 72 };
  };

  /* 参数串（rolling=false 时滚动预设转 from）。 */
  const qs = (rolling: boolean): string => {
    if (rolling) {
      const q = query();
      if (q.hours != null) return 'hours=' + q.hours;
      const parts: string[] = [];
      if (q.from) parts.push('from=' + q.from);
      if (q.to) parts.push('to=' + q.to);
      return parts.join('&');
    }
    if (st.preset === '0') return '';
    if (st.preset === 'custom' || st.preset === 'today') {
      const q = query();
      const parts: string[] = [];
      if (q.from) parts.push('from=' + q.from);
      if (q.to) parts.push('to=' + q.to);
      return parts.join('&');
    }
    const h = Number(st.preset) || 72;
    return 'from=' + Math.floor((Date.now() - h * 3600 * 1000) / 1000);
  };

  const label = (): string => {
    const found = TRANGE_PRESETS.find((p) => p[0] === st.preset);
    if (st.preset !== 'custom') return found ? found[1] : '';
    const f = (v: string) => v.replace('T', ' ').slice(5, 16) || '…';
    return f(st.from) + ' → ' + f(st.to);
  };

  return { st, setPreset, setCustom, query, qs, label };
}

/* 受控组件形态：preset 下拉 + 自定义起止输入。 */
export function TimeRangeControl({
  range,
  onChange,
}: {
  range: ReturnType<typeof useTimeRange>;
  onChange: () => void;
}) {
  const { st, setPreset, setCustom } = range;
  const custom = st.preset === 'custom';
  return (
    <span className="flex items-center gap-1.5 text-[12px]">
      <select
        className="wb-select px-2 py-1"
        value={st.preset}
        onChange={(e) => {
          setPreset(e.target.value);
          onChange();
        }}
        aria-label="时间范围"
      >
        {TRANGE_PRESETS.map(([v, label]) => (
          <option key={v} value={v}>
            {label}
          </option>
        ))}
      </select>
      {custom && (
        <span className="flex items-center gap-1">
          <input
            type="datetime-local"
            className="wb-select px-2 py-1"
            value={st.from}
            onChange={(e) => {
              setCustom(e.target.value, st.to);
              onChange();
            }}
            aria-label="起始时间"
          />
          <span className="text-[var(--ink-3)]">→</span>
          <input
            type="datetime-local"
            className="wb-select px-2 py-1"
            value={st.to}
            onChange={(e) => {
              setCustom(st.from, e.target.value);
              onChange();
            }}
            aria-label="结束时间"
          />
        </span>
      )}
    </span>
  );
}
