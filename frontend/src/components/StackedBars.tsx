/* StackedBars 时序堆叠柱图（全站唯一时间序列图表）：真实时间轴、
   均值线、峰值标注、日界分隔、逐柱 tooltip。prompt/completion 两段。 */
import { useMemo } from 'react';
import { fmtTok } from '../fmt';

export interface ChartPoint {
  t: number; // 毫秒
  raw: string; // 原始标签（tooltip 首行）
  pt: number;
  ct: number;
  tt: number;
  req: number;
  scope?: string;
}

function tickLabel(p: { t: number; scope?: string }): string {
  const d = new Date(p.t);
  const p2 = (n: number) => String(n).padStart(2, '0');
  return p.scope === 'day' ? `${d.getMonth() + 1}-${p2(d.getDate())}` : p2(d.getHours()) + ':00';
}

export function StackedBars({ pts, tick }: { pts: ChartPoint[]; tick?: (p: { t: number; scope?: string }) => string }) {
  const geo = useMemo(() => {
    if (!pts.length) return null;
    const W = 1200;
    const H = 120;
    const PL = 48;
    const PR = 12;
    const PT = 10;
    const PB = 22;
    const iw = W - PL - PR;
    const ih = H - PT - PB;
    const t0 = pts[0].t;
    const span = Math.max(1, pts[pts.length - 1].t - t0);
    const max = Math.max(1, ...pts.map((p) => p.tt));
    const peak = pts.reduce((a, b) => (b.tt > a.tt ? b : a), pts[0]);
    const avg = pts.reduce((s, p) => s + p.tt, 0) / pts.length;

    let minGap = Infinity;
    for (let i = 1; i < pts.length; i++) minGap = Math.min(minGap, pts[i].t - pts[i - 1].t);
    if (!isFinite(minGap) || minGap <= 0) minGap = span;
    const bw = Math.max(2, Math.min(30, iw * (minGap / span) * 0.7));

    const xOf = (t: number) => PL + bw / 2 + ((t - t0) / span) * Math.max(1, iw - bw);
    const yOf = (v: number) => PT + ih - ih * (v / max);
    return { W, H, PL, PR, PT, PB, iw: 0, ih, t0, span, max, peak, avg, bw, xOf, yOf };
  }, [pts]);

  if (!geo) return null;
  const { W, H, PL, PR, PT, ih, max, peak, avg, bw, xOf, yOf } = geo;
  const yBase = PT + ih;
  const tl = tick || tickLabel;

  // x 轴刻度：等距取 6 个位置，取最近实际柱子做标签。
  const TICKS = Math.min(6, pts.length);
  const used = new Set<number>();
  const ticks: { x: number; anchor: 'start' | 'middle' | 'end'; text: string }[] = [];
  for (let k = 0; k < TICKS; k++) {
    const target = geo.t0 + geo.span * (TICKS === 1 ? 0.5 : k / (TICKS - 1));
    let bi = 0;
    let best = Infinity;
    for (let i = 0; i < pts.length; i++) {
      const d = Math.abs(pts[i].t - target);
      if (d < best) {
        best = d;
        bi = i;
      }
    }
    if (used.has(bi)) continue;
    used.add(bi);
    const cx = xOf(pts[bi].t);
    const anchor = cx < PL + 14 ? 'start' : cx > W - PR - 14 ? 'end' : 'middle';
    ticks.push({ x: Math.max(PL, Math.min(W - PR, cx)), anchor, text: tl({ t: pts[bi].t, scope: pts[bi].scope }) });
  }

  // 跨日分隔线
  const dayLines: string[] = [];
  let prevDay: number | null = null;
  for (const p of pts) {
    const d = new Date(p.t).getDate();
    if (prevDay !== null && d !== prevDay) dayLines.push(xOf(p.t).toFixed(1));
    prevDay = d;
  }

  const px = xOf(peak.t);
  const py = yOf(peak.tt);
  const peakAnchor = px > W - PR - 90 ? 'end' : 'middle';

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" preserveAspectRatio="xMidYMid meet">
      <defs>
        <linearGradient id="usGradP" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" style={{ stopColor: 'var(--accent)', stopOpacity: 1 }} />
          <stop offset="1" style={{ stopColor: 'var(--accent)', stopOpacity: 0.6 }} />
        </linearGradient>
        <linearGradient id="usGradC" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" style={{ stopColor: 'var(--ok)', stopOpacity: 1 }} />
          <stop offset="1" style={{ stopColor: 'var(--ok)', stopOpacity: 0.6 }} />
        </linearGradient>
      </defs>

      {/* y 轴网格 + 刻度 */}
      {Array.from({ length: 4 }, (_, i) => {
        const y = PT + ih - (ih * i) / 3;
        return (
          <g key={i}>
            <line className="stroke-[var(--line-soft)]" x1={PL} y1={y} x2={W - PR} y2={y} />
            <text className="fill-[var(--ink-3)] text-[10px] tabular" x={PL - 5} y={y + 3} textAnchor="end">
              {fmtTok((max * i) / 3)}
            </text>
          </g>
        );
      })}

      {/* 均值线 */}
      {avg > 0 && avg < max && (
        <g>
          <line className="stroke-[var(--warn)]" strokeDasharray="4 3" opacity="0.7" x1={PL} y1={yOf(avg)} x2={W - PR} y2={yOf(avg)} />
          <text className="fill-[var(--warn)] text-[10px] tabular" x={PL + 5} y={yOf(avg) - 3} textAnchor="start">
            均值 {fmtTok(avg)}
          </text>
        </g>
      )}

      {/* 柱子（每柱一个 <g> 包住 <title>，tooltip 跟随该柱） */}
      {pts.map((p, i) => {
        const x = xOf(p.t) - bw / 2;
        const hTot = ih * (p.tt / max);
        const hP = p.tt ? hTot * (p.pt / p.tt) : 0;
        const hC = Math.max(p.tt && p.ct ? 1 : 0, hTot - hP);
        return (
          <g key={i}>
            <title>
              {p.raw}  {fmtTok(p.pt)} prompt / {fmtTok(p.ct)} completion / {p.req} 次
            </title>
            {hP > 0 && (
              <rect className="usbar" x={x.toFixed(2)} y={(yBase - hP).toFixed(2)} width={bw.toFixed(2)} height={hP.toFixed(2)} fill="url(#usGradP)" {...(hC > 0 ? {} : { rx: 1.5 })} />
            )}
            {hC > 0 && (
              <rect className="usbar" x={x.toFixed(2)} y={(yBase - hP - hC).toFixed(2)} width={bw.toFixed(2)} height={hC.toFixed(2)} fill="url(#usGradC)" rx={1.5} />
            )}
          </g>
        );
      })}

      {/* 峰值标注 */}
      <text className="fill-[var(--ink-2)] text-[11px] tabular font-medium" x={Math.max(PL, Math.min(W - PR, px)).toFixed(1)} y={Math.max(10, py - 5).toFixed(1)} textAnchor={peakAnchor}>
        峰值 {fmtTok(peak.tt)}
      </text>

      {/* 基线 */}
      <line className="stroke-[var(--line)]" x1={PL} y1={yBase} x2={W - PR} y2={yBase} />

      {/* x 轴刻度 */}
      {ticks.map((t, i) => (
        <text key={i} className="fill-[var(--ink-3)] text-[11px] tabular" x={t.x.toFixed(1)} y={PT + ih + 15} textAnchor={t.anchor}>
          {t.text}
        </text>
      ))}

      {/* 日界分隔 */}
      {dayLines.map((x, i) => (
        <line key={i} className="stroke-[var(--line-soft)]" x1={x} y1={PT} x2={x} y2={PT + ih} opacity="0.45" />
      ))}
    </svg>
  );
}

/** 后端 series 点 → ChartPoint（丢掉解析失败的点，不让 NaN 传染）。 */
export function toChartPoints(
  series: { t: string; scope?: string; prompt_tokens?: number; completion_tokens?: number; total_tokens?: number; requests?: number }[],
): ChartPoint[] {
  const out: ChartPoint[] = [];
  for (const p of series || []) {
    const s = String(p.t || '');
    const iso = s.length === 13 ? s + ':00:00' : s.includes('T') || s.includes('-') ? s + (s.length === 10 ? 'T00:00:00' : '') : s;
    const t = Date.parse(iso);
    if (!Number.isFinite(t)) continue;
    const pt = Number(p.prompt_tokens || 0);
    const ct = Number(p.completion_tokens || 0);
    out.push({ t, raw: s, pt, ct, tt: Number(p.total_tokens || 0) || pt + ct, req: p.requests || 0, scope: p.scope });
  }
  return out;
}
