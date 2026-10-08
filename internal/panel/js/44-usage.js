/* panel 分片 44-usage.js —— 用量视图：维度明细表（账号/模型/域）、积分构成表、时序图与加载、模型速率预热。 */
/* ── 用量明细：三个维度共用一张表 + 页内切换 ──────────────────────────
   账号 / 模型 / 域三张表此前各自占一个 box，页面纵向拉得很长且表头结构几乎一样。
   现在合成一个 box：表头由维度定义生成，行渲染复用 usRow，切换零请求。 */
const US_DIMS = {
  account: { key: 'by_account', title: '账号', withRealm: true, withPerf: true, span: 8 },
  model: { key: 'by_model', title: '模型', withRealm: false, withPerf: false, span: 5 },
  realm: { key: 'by_realm', title: 'realm', withRealm: false, withPerf: false, span: 5 },
};

// usSortRows 按当前排序字段降序（默认合计 Token，最大者最相关）。
function usSortRows(rows, sort) {
  const out = rows.slice();
  const num = v => { const n = Number(v || 0); return Number.isFinite(n) ? n : 0; };
  const val = a => sort === 'requests' ? num(a.requests)
    : sort === 'errors' ? num(a.errors)
    : sort === 'latency' ? num(a.avg_latency_ms)
    : num(a.total_tokens);
  out.sort((a, b) => val(b) - val(a));
  return out;
}

function usDimHead(dim) {
  const m = US_DIMS[dim] || US_DIMS.account;
  return '<tr><th class="mark" aria-hidden="true"></th><th>' + esc(m.title) + '</th>' +
    (m.withRealm ? '<th>域</th>' : '') +
    '<th class="num">请求</th><th class="num">失败</th>' +
    '<th class="num">Token</th>' +
    (m.withPerf ? '<th class="num">均延迟</th><th class="num">均速率</th>' : '') +
    '</tr>';
}

/* usTabsHtml 维度切换按钮（带条数徽标）。整段 innerHTML 重写而不是逐个改 class：
   容器的 click 监听是委托式的，换掉子节点不会丢事件，代码也更短。 */
function usTabsHtml(dims, active, counts) {
  return dims.map(([k, label]) => {
    const n = counts ? counts[k] : null;
    return '<button data-dim="' + k + '"' + (k === active ? ' class="on"' : '') + '>' +
      esc(label) + (n == null ? '' : '<span class="cnt">' + n + '</span>') + '</button>';
  }).join('');
}

const US_DIM_TABS = [['account', '按账号'], ['model', '按模型'], ['realm', '按域']];

function renderUsageDim() {
  const d = usageData || {};
  const m = US_DIMS[usDim] || US_DIMS.account;
  const rows = usSortRows(d[m.key] || [], usSort);
  $('usDimTabs').innerHTML = usTabsHtml(US_DIM_TABS, usDim, {
    account: (d.by_account || []).length,
    model: (d.by_model || []).length,
    realm: (d.by_realm || []).length,
  });
  $('usDimHead').innerHTML = usDimHead(usDim);
  $('usDimBody').innerHTML = rows.map(x =>
    usRow(usDim === 'account' ? String(x.key || '').slice(0, 8) : x.key,
      usDim === 'account' ? (x.extra || '') : '',
      x,
      usDim === 'account' ? '<td class="num">' + esc(x.realm || '') + '</td>' : '',
      m.withPerf)
  ).join('') || '<tr><td colspan="' + m.span + '" class="empty">暂无数据</td></tr>';
  $('usDimNote').textContent = rows.length + ' 行 · 请求数含失败尝试';
}

/* 积分扣除：按账号 / 按模型两个维度共用一张表（同上，两个 box 合成一个）。 */
function usCreditHead(dim) {
  return dim === 'model'
    ? '<tr><th class="mark" aria-hidden="true"></th><th>模型</th><th>积分倍率</th>' +
      '<th class="num">请求</th><th class="num">扣除积分</th>' +
      '<th class="num">有效样本 Token</th><th class="num">积分 / 1M Token</th><th class="num">缓存命中率</th></tr>'
    : '<tr><th class="mark" aria-hidden="true"></th><th>账号</th>' +
      '<th class="num">请求</th><th class="num">扣除积分</th>' +
      '<th class="num">有效样本 Token</th><th class="num">积分 / 1M Token</th><th class="num">缓存命中率</th></tr>';
}

/* 缓存命中率（issue #92）：颜色即健康度——≥90% 绿 / 80–90% 黄 / <80% 红，
   样本不足灰。值走 cacheRatePct（与请求记录文本同一口径），颜色走 .hit-* 类
   而非内联 style：内联样式一来让严格 CSP 白白带着 style-src 'unsafe-inline'，
   二来同一色阶散在 JS 字符串里没法统一调。 */
function cacheRateCell(hit, miss) {
  const pct = cacheRatePct(hit, miss);
  if (pct == null) return '<span class="muted">—</span>';
  const cls = pct >= 90 ? 'hit-hi' : (pct >= 80 ? 'hit-mid' : 'hit-lo');
  const txt = trimFixed(pct.toFixed(1)) + '%';
  return '<span class="' + cls + '" title="命中 ' + fmtTok(hit) + ' / 未命中 ' + fmtTok(miss) + ' tok">' + txt + '</span>';
}

function renderCreditDim() {
  const d = usageData || {};
  $('usCreditHead').innerHTML = usCreditHead(usCreditDim);
  const empty = '暂无积分扣除记录；升级前仅含 Token 的历史不会伪造积分。';
  if (usCreditDim === 'model') {
    const models = d.credit_by_model || [];
    $('usCreditBody').innerHTML = models.map(row =>
      '<tr>' +
        '<td class="mark" aria-hidden="true"></td>' +
        '<td>' + esc(row.key || '—') + '</td>' +
        '<td>' + esc(fmtModelRate(row.rate)) + '</td>' +
        '<td class="num">' + fmtTok(row.requests) + '</td>' +
        '<td class="num">' + fmtCredit(row.credits) + '</td>' +
        '<td class="num">' + fmtTok(row.credit_tokens) + '</td>' +
        '<td class="num">' + fmtCreditRatio(row.credits_per_1m_tokens, row.credit_samples, row.credit_tokens) + '</td>' +
        '<td class="num">' + cacheRateCell(row.cache_hit_tokens, row.cache_miss_tokens) + '</td>' +
      '</tr>'
    ).join('') || '<tr><td colspan="8" class="empty">' + empty + '</td></tr>';
  } else {
    const accounts = d.credit_by_account || [];
    $('usCreditBody').innerHTML = accounts.map(row => {
      const uid = String(row.key || '');
      const account = row.nickname || uid.slice(0, 8) || '—';
      return '<tr>' +
        '<td class="mark" aria-hidden="true"></td>' +
        '<td>' + esc(account) + '<div class="note">' + esc(row.realm || '') + ' · ' + esc(uid.slice(0, 8)) + '</div></td>' +
        '<td class="num">' + fmtTok(row.requests) + '</td>' +
        '<td class="num">' + fmtCredit(row.credits) + '</td>' +
        '<td class="num">' + fmtTok(row.credit_tokens) + '</td>' +
        '<td class="num">' + fmtCreditRatio(row.credits_per_1m_tokens, row.credit_samples, row.credit_tokens) + '</td>' +
        '<td class="num">' + cacheRateCell(row.cache_hit_tokens, row.cache_miss_tokens) + '</td>' +
        '</tr>';
    }).join('') || '<tr><td colspan="7" class="empty">' + empty + '</td></tr>';
  }
  $('usCreditTabs').innerHTML = usTabsHtml([['account', '按账号'], ['model', '按模型']], usCreditDim, {
    account: (d.credit_by_account || []).length,
    model: (d.credit_by_model || []).length,
  });
}

function renderUsage(d) {
  usageData = d || {};
  const t = usageData.totals || {};
  const total = Number(t.total_tokens || 0);
  const pt = Number(t.prompt_tokens || 0);
  const ct = Number(t.completion_tokens || 0);
  const reqs = Number(t.requests || 0);
  const errs = Number(t.errors || 0);
  const okRate = reqs ? (reqs - errs) / reqs * 100 : null;
  const pctW = (part) => total ? Math.max(0, Math.min(100, Number(part || 0) / total * 100)) : 0;
  // 卡片只留「结论」级指标：请求数 / 总 token / 平均延迟，再加积分区的四张。
  // prompt、completion、成功率、样本数都是构成项，放副标题或构成条 title——
  // 此前把构成项也各占一张卡，同一组数字在页面上写了两遍（共 11 张卡）。
  $('usStats').innerHTML =
    usKpi(fmtTok(reqs), '请求数', errs ? 'c-warn' : 'c-accent',
      okRate == null ? '—' : (errs ? '成功率 ' + okRate.toFixed(1) + '% · 失败 ' + errs + ' 次' : '成功率 100%')) +
    usKpi(fmtTok(total), '总 token', 'c-accent',
      'prompt ' + fmtTok(pt) + ' · completion ' + fmtTok(ct),
      segBar([
        { value: pctW(pt), color: 'var(--accent)', title: 'prompt ' + usPct(pt, total) },
        { value: pctW(ct), color: 'var(--ok)', title: 'completion ' + usPct(ct, total) },
      ], { cls: 'kbar' })) +
    usKpi(fmtMs(t.avg_latency_ms), '平均延迟', 'c-soft',
      t.avg_tokens_per_second ? '吐字 ' + fmtRate(t.avg_tokens_per_second) : '无速率样本');

  // 卡片、明细表与时序图全部按所选窗口统计（切窗口数字随之变化）；
  // 「全部历史」含 90 天前折叠出的日桶。这里标注当前口径与数据起点。
  const winLabel = trangeLabel('usRange');
  // 服务端回显的实际区间优先（自定义区间下它就是权威口径）；滚动窗口没有回显，
  // 用控件自己的标签。
  const rangeEcho = usageData.window_from
    ? String(usageData.window_from).replace('T', ' ').slice(0, 16) +
      (usageData.window_to ? ' → ' + String(usageData.window_to).replace('T', ' ').slice(0, 16) : ' → 现在')
    : '';
  const note = (rangeEcho || winLabel ? (rangeEcho || winLabel) + ' · ' : '') +
    (usageData.buckets || 0) + ' 个分桶' +
    (usageData.since ? ' · 数据自 ' + usageData.since.replace('T', ' ') : '') +
    (usageData.file_bytes ? ' · 文件 ' + (usageData.file_bytes / 1024).toFixed(1) + ' KB' : '');
  $('usNote').textContent = note;
  $('usNote').title = note; // 窄屏单行截断时靠悬停看全

  // 积分区四张卡。「有效积分样本」不是独立结论（样本数决定折算可信度），
  // 并入「平均积分 / 1M Token」的副标题。
  $('usCreditStats').innerHTML =
    usKpi(fmtCredit(t.credits), '扣除积分', 'c-accent', '按上游 usage.credit 累计') +
    usKpi(fmtTok(t.credit_tokens), '匹配 Token', 'c-mute', '与积分同时观测到的 Token') +
    usKpi(fmtCreditRatio(t.credits_per_1m_tokens, t.credit_samples, t.credit_tokens),
      '平均积分 / 1M Token', 'c-ok',
      (t.credit_samples || 0) + ' 个有效样本 · 越低越划算') +
    usKpi(cacheRateText(t.cache_hit_tokens, t.cache_miss_tokens), '缓存命中率', 'c-mute',
      '上游前缀缓存命中 / (命中+未命中)；低命中意味着费用数倍放大');
  $('usCreditNote').textContent =
    (usageData.credit_by_account || []).length + ' 个账号 · ' +
    (usageData.credit_by_model || []).length + ' 个模型倍率分组 · 仅统计与积分同时观测到的 Token';

  renderUsageDim();
  renderCreditDim();
  renderUsageChart(usageData.series || []);
}

// 维度切换 / 排序控件。
if ($('usDimTabs')) $('usDimTabs').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-dim]');
  if (!b) return;
  usDim = b.dataset.dim;
  renderUsageDim();
});
if ($('usCreditTabs')) $('usCreditTabs').addEventListener('click', ev => {
  const b = ev.target.closest('button[data-dim]');
  if (!b) return;
  usCreditDim = b.dataset.dim;
  renderCreditDim();
});
if ($('usSort')) $('usSort').onchange = () => {
  usSort = $('usSort').value;
  renderUsageDim();
};

/* renderUsageChart 画堆叠柱状图。
 *
 * x 轴是**真实时间轴**，不是按序号等距。这一点很重要：数据里存在 1 小时的
 * 间隔，也存在 6~8 小时的断档（没请求的时段不产生桶），等距排布会把 8 小时
 * 画得和 1 小时一样宽，让「什么时候用的」完全失真。
 *
 * 另外不再用 preserveAspectRatio="none"：那会把 viewBox 横向拉伸到容器宽度，
 * 柱子和文字都变形。改为固定比例、按容器宽度自适应高度。
 *
 * viewBox 取 1200×200（原 760×180）：SVG 以 width:100% 渲染，高宽比决定实际
 * 高度——旧比例在 1500px 宽的主区里会撑到 ~355px，只有一两根柱子时整块几乎是
 * 空白。宽 viewBox 把同宽度下的高度压到 ~250px，与下方表格的视觉重量相当。
 *
 * 时间轴用本地时间解析（后端返回的就是本地时区），day 点按当天 00:00 参与定位，
 * 与 hour 点在同一个连续轴上——日桶本来就是他那天所有小时的聚合。
 */

/* parsePointTime 把后端的 t 解析成毫秒时间戳。 */
function parsePointTime(p) {
  // hour: "2026-09-16T13"  day: "2026-09-16"
  const s = p.t.length === 13 ? p.t + ':00:00' : p.t + 'T00:00:00';
  const d = new Date(s);
  return isNaN(d.getTime()) ? null : d.getTime();
}

/* fmtTokTimeLabel 时间桶的短标签，与 x 轴刻度同一口径（日桶 MM-DD，小时桶 HH:00）。 */
function fmtTokTimeLabel(p) {
  const d = new Date(p.t);
  return p.scope === 'day'
    ? (d.getMonth() + 1) + '-' + String(d.getDate()).padStart(2, '0')
    : String(d.getHours()).padStart(2, '0') + ':00';
}

/* renderUsageChart 用量时序图：数据准备 + 页脚说明，几何与标记全在 14-chart.js
   的 stackedBarsSVG（全站唯一的时间序列图表实现）。 */
function renderUsageChart(series) {
  const host = $('usChart');
  // 丢掉时间解析不出来的点，而不是让 NaN 传染整张图。
  const pts = [];
  for (const p of series) {
    const t = parsePointTime(p);
    if (t === null) continue;
    const pt = Number(p.prompt_tokens || 0);
    const ct = Number(p.completion_tokens || 0);
    pts.push({ t, scope: p.scope, raw: p.t, pt, ct, tt: Number(p.total_tokens || 0) || (pt + ct),
               req: p.requests || 0 });
  }
  if (!pts.length) {
    host.innerHTML = '<div class="us-empty">暂无用量数据。发起一次对话后再刷新。</div>';
    $('usChartNote').textContent = '—';
    return;
  }
  const chart = stackedBarsSVG(pts, { fmt: fmtTok, tick: fmtTokTimeLabel });
  host.innerHTML = chart.svg;
  $('usChartNote').textContent =
    pts.length + ' 个点 · 峰值 ' + fmtTok(chart.peak.tt) + ' @ ' + fmtTokTimeLabel(chart.peak) +
    ' · 均值 ' + fmtTok(chart.avg);
}

let usageRateWarmAt = 0;
async function warmUsageModelRates() {
  if (Date.now() - usageRateWarmAt < 10 * 60 * 1000) return;
  try {
    await api('models');
  } catch (e) {
    // 倍率回填是可选增强；失败不阻塞用量统计，10 分钟后再试。
  }
  usageRateWarmAt = Date.now();
}

async function loadUsage() {
  const q = trangeQuery('usRange', true);
  try {
    await warmUsageModelRates();
    const d = await api('usage?' + q.toString());
    renderUsage(d);
  } catch (e) {
    // 失败时三块都要清干净：只改图表会留下上一次窗口的数字，看起来像"刷新成功"。
    usageData = null;
    $('usChart').innerHTML = '<div class="us-empty">读取用量失败：' + esc(e.message) + '</div>';
    $('usChartNote').textContent = '—';
    $('usStats').innerHTML = '';
    $('usCreditStats').innerHTML = '';
    $('usNote').textContent = '—';
    $('usCreditNote').textContent = '—';
    $('usDimNote').textContent = '—';
    $('usDimHead').innerHTML = '';
    $('usCreditHead').innerHTML = '';
    $('usDimTabs').innerHTML = usTabsHtml(US_DIM_TABS, usDim, null);
    $('usCreditTabs').innerHTML = usTabsHtml([['account', '按账号'], ['model', '按模型']], usCreditDim, null);
    $('usDimBody').innerHTML = '<tr><td colspan="10" class="empty">读取用量失败</td></tr>';
    $('usCreditBody').innerHTML = '<tr><td colspan="7" class="empty">读取用量失败</td></tr>';
  }
}

if ($('btnUsage')) $('btnUsage').onclick = loadUsage;
// 时间范围控件绑定：任何改动（预设切换 / 自定义起止）都重新拉一次用量。
if ($('usRange')) trangeBind('usRange', loadUsage);
