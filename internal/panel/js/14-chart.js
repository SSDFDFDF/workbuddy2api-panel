/* panel 分片 14-chart.js —— 图表构件（全站唯一实现）：水平堆叠条 segBar 与时间序列堆叠柱 stackedBarsSVG。 */
/* ── 图表构件 ──────────────────────────────────────────────────────
   此前全站有六处各自手写条形：账号池积分进度条、KPI 构成条、用量表
   prompt/completion 条、积分来源构成条、账号到期条、到期分布图。它们做的是同一件事
   ——按数值把一条切成几段——却各有各的宽度算法/类名/悬浮提示，改一处样式要翻六个地方。
   现在统一到 segBar：调用方只给数值与语义（颜色/提示），几何与标记由这里产出，
   CSS 只有一个 .segbar 基类 + 尺寸修饰类。

   时间序列柱图同理收敛到 stackedBarsSVG：几何、网格、均值线、峰值标注、日界、
   tooltip 结构都只在这里算，调用方负责数据准备与文案。 */

/* segBar 水平堆叠条。segs = [{value, color, title, opacity}]，opts:
     - total  分母（默认各段之和；传 total 可让条不满格，如 prompt+completion 之外还有缓存 token）
     - cls    尺寸修饰类（由 CSS 给高度/外边距，如 us-mix / cred-bar）
     - aria   无障碍名称（没有 title 时同时作为悬浮提示）
   返回 HTML 串；无正数值时返回空串（调用方不必再判空）。 */
function segBar(segs, opts) {
  const o = opts || {};
  const list = (segs || []).filter(s => s && Number(s.value) > 0);
  if (!list.length) return '';
  const total = Number(o.total) > 0 ? Number(o.total) : list.reduce((s, x) => s + Number(x.value), 0);
  const items = list.map(s => {
    const w = Number(s.value) / total * 100;
    return '<i style="width:' + w.toFixed(3) + '%' +
      (s.color ? ';background:' + s.color : '') +
      (s.opacity == null ? '' : ';opacity:' + Number(s.opacity).toFixed(4)) +
      '"' + (s.title ? ' title="' + esc(s.title) + '"' : '') + '></i>';
  }).join('');
  const label = o.aria || '';
  return '<span class="segbar' + (o.cls ? ' ' + o.cls : '') + '"' +
    (label ? ' role="img" aria-label="' + esc(label) + '"' : '') + '>' + items + '</span>';
}

/* stackedBarsSVG 时间序列堆叠柱（prompt / completion 两段）。唯一的时间序列图表实现。
   pts = [{t(ms), raw, pt, ct, tt, req}]，opts:
     - fmt   数值格式化（默认原样）
     - tick  x 轴刻度文案（默认时间戳）
   返回 {svg, peak, avg}：峰值与均值同时供调用方写页脚说明，避免再算一遍。 */
function stackedBarsSVG(pts, opts) {
  const o = opts || {};
  const fmt = o.fmt || (v => String(v));
  const tick = o.tick || (p => String(p.t));

  const W = 1200, H = 200, PL = 58, PR = 14, PT = 18, PB = 30;
  const iw = W - PL - PR, ih = H - PT - PB;

  const t0 = pts[0].t;
  const span = Math.max(1, pts[pts.length - 1].t - t0);
  const max = Math.max(1, ...pts.map(p => p.tt));
  const peak = pts.reduce((a, b) => (b.tt > a.tt ? b : a), pts[0]);
  const avg = pts.reduce((s, p) => s + p.tt, 0) / pts.length;

  // 柱宽取「最小真实间隔」的 70%，并夹在合理区间内——窗口拉到 30 天时柱子会
  // 变细，但不会细到看不见。
  let minGap = Infinity;
  for (let i = 1; i < pts.length; i++) minGap = Math.min(minGap, pts[i].t - pts[i - 1].t);
  if (!isFinite(minGap) || minGap <= 0) minGap = span;
  const slot = iw * (minGap / span);
  const bw = Math.max(2, Math.min(30, slot * 0.7));

  // 首尾各让出半个柱宽：否则第一个点和最后一个点的柱子会各有一半跑到绘图区外
  // （末点柱子贴着卡片右边缘被切掉），刻度仍用同一个 xOf，标签与柱子始终对齐。
  const xOf = t => PL + bw / 2 + (t - t0) / span * Math.max(1, iw - bw);
  const yOf = v => PT + ih - ih * (v / max);

  let out = '<svg viewBox="0 0 ' + W + ' ' + H + '" role="img" ' +
            'preserveAspectRatio="xMidYMid meet">';

  // 柱体渐变：顶部实、底部略透，堆叠时两段仍能一眼分清（纯色块并排会糊成一片）。
  // 注意 stop-color 必须走 style 而不是 presentation 属性——Blink/WebKit 不解析
  // 属性里的 var()，写成 stop-color="var(--accent)" 会整条渐变失效（柱子全透明）。
  out += '<defs>' +
    '<linearGradient id="usGradP" x1="0" y1="0" x2="0" y2="1">' +
    '<stop offset="0" style="stop-color:var(--accent);stop-opacity:1"/>' +
    '<stop offset="1" style="stop-color:var(--accent);stop-opacity:.6"/></linearGradient>' +
    '<linearGradient id="usGradC" x1="0" y1="0" x2="0" y2="1">' +
    '<stop offset="0" style="stop-color:var(--ok);stop-opacity:1"/>' +
    '<stop offset="1" style="stop-color:var(--ok);stop-opacity:.6"/></linearGradient>' +
    '</defs>';

  // y 轴网格 + 刻度
  for (let i = 0; i <= 4; i++) {
    const y = PT + ih - (ih * i / 4);
    out += '<line class="gl" x1="' + PL + '" y1="' + y.toFixed(1) + '" x2="' + (W - PR) +
           '" y2="' + y.toFixed(1) + '"/>';
    out += '<text class="tk" x="' + (PL - 6) + '" y="' + (y + 3.5).toFixed(1) +
           '" text-anchor="end">' + fmt(max * i / 4) + '</text>';
  }

  // 均值参考线：一眼看出"这根是不是异常高"，比只给刻度省心。
  // 标签放左侧：右侧常被峰值柱占用（峰值柱往往就是最后一根），贴左不会被压住。
  if (avg > 0 && avg < max) {
    const y = yOf(avg);
    out += '<line class="avg" x1="' + PL + '" y1="' + y.toFixed(1) + '" x2="' + (W - PR) +
           '" y2="' + y.toFixed(1) + '"/>';
    out += '<text class="tk-avg" x="' + (PL + 5) + '" y="' + (y - 4).toFixed(1) +
           '" text-anchor="start">均值 ' + fmt(avg) + '</text>';
  }

  // 柱子
  const yBase = PT + ih;
  for (const p of pts) {
    const x = xOf(p.t) - bw / 2;
    const hTot = ih * (p.tt / max);
    const hP = p.tt ? hTot * (p.pt / p.tt) : 0;
    const hC = Math.max(p.tt && p.ct ? 1 : 0, hTot - hP);
    // 每根柱子包一个 <g>，把 <title> 放进去。
    //
    // 为什么必须包裹：SVG 里 <title> 描述的是它的**父元素**。此前 <title> 是
    // <rect> 的兄弟节点（rect 自闭合，无法包含子节点），于是全部平铺在 <svg>
    // 根下 —— 整张图只有一个 tooltip（浏览器取第一个），悬停任何柱子都显示同一
    // 份数据（issue #128）。包进 <g> 后 tooltip 跟随该柱，且堆叠的两段
    //（prompt + completion）共用同一个提示。
    out += '<g><title>' + esc(p.raw) + '  ' + fmt(p.pt) + ' prompt / ' +
           fmt(p.ct) + ' completion / ' + p.req + ' 次</title>';
    // 圆角只给堆叠顶端（贴轴的底边保持方角，柱子才像"立"在基线上）。
    // 类名用 usbar 而不是 bar：账号池的积分条曾叫 .bar{height:3px}，而 SVG2 里
    // height 是 rect 的 CSS 几何属性，同名类会把每根柱子压成 3px 高（踩过）。
    if (hP > 0) out += '<rect class="usbar" x="' + x.toFixed(2) + '" y="' + (yBase - hP).toFixed(2) +
      '" width="' + bw.toFixed(2) + '" height="' + hP.toFixed(2) +
      '" fill="url(#usGradP)"' + (hC > 0 ? '' : ' rx="1.5"') + '/>';
    if (hC > 0) out += '<rect class="usbar" x="' + x.toFixed(2) + '" y="' + (yBase - hP - hC).toFixed(2) +
      '" width="' + bw.toFixed(2) + '" height="' + hC.toFixed(2) +
      '" fill="url(#usGradC)" rx="1.5"/>';
    out += '</g>';
  }

  // 峰值标注：柱子够窄时文字压在柱顶，够宽时贴右侧避免和柱体重叠。
  {
    const px = xOf(peak.t);
    const py = yOf(peak.tt);
    const anchor = px > W - PR - 90 ? 'end' : 'middle';
    out += '<text class="tk-peak" x="' + Math.max(PL, Math.min(W - PR, px)).toFixed(1) +
           '" y="' + Math.max(10, py - 5).toFixed(1) + '" text-anchor="' + anchor + '">' +
           '峰值 ' + fmt(peak.tt) + '</text>';
  }

  // x 轴基线画在柱子之后，避免压在柱底
  out += '<line class="ax" x1="' + PL + '" y1="' + yBase + '" x2="' + (W - PR) +
         '" y2="' + yBase + '"/>';

  // x 轴刻度：按真实时间等距取 6 个位置，取该位置**最近的实际柱子**做标签，
  // 所以标签永远落在有数据的点上，不会指到空档里。
  const TICKS = Math.min(6, pts.length);
  const usedLabel = new Set();
  for (let k = 0; k < TICKS; k++) {
    const target = t0 + span * (TICKS === 1 ? 0.5 : k / (TICKS - 1));
    let bi = 0, best = Infinity;
    for (let i = 0; i < pts.length; i++) {
      const d = Math.abs(pts[i].t - target);
      if (d < best) { best = d; bi = i; }
    }
    if (usedLabel.has(bi)) continue;
    usedLabel.add(bi);
    const p = pts[bi];
    // 首尾标签靠边对齐，避免被裁掉
    const cx = xOf(p.t);
    const anchor = cx < PL + 14 ? 'start' : (cx > W - PR - 14 ? 'end' : 'middle');
    out += '<text class="tk" x="' + Math.max(PL, Math.min(W - PR, cx)).toFixed(1) +
           '" y="' + (PT + ih + 15) + '" text-anchor="' + anchor + '">' + esc(tick(p)) + '</text>';
  }

  // 跨天时补一条日期分隔线，让「日界」在长窗口里可见
  let prevDay = null;
  for (const p of pts) {
    const d = new Date(p.t).getDate();
    if (prevDay !== null && d !== prevDay) {
      const x = xOf(p.t).toFixed(1);
      out += '<line class="gl" x1="' + x + '" y1="' + PT + '" x2="' + x + '" y2="' +
             (PT + ih) + '" style="opacity:.45"/>';
    }
    prevDay = d;
  }

  out += '</svg>';
  return { svg: out, peak: peak, avg: avg };
}
