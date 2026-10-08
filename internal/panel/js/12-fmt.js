/* panel 分片 12-fmt.js —— 数值与文案格式化：限流/模型不可用文案、token 体积、时长、速率、积分、缓存命中率，以及用量 KPI 卡构件；被账号池/日志/用量/积分包共用。 */
function rateLimitMeta(row, now) {
  const model = String(row && row.model || '未知模型');
  const kind = String(row && row.kind || 'rate_limit');
  const resetAt = parseAPITime(row && row.reset_at);
  const until = parseAPITime(row && row.until);
  const deadline = resetAt || until;
  const remaining = deadline > now ? Math.round((deadline - now) / 1000) : 0;
  if (kind === 'model_unavailable') {
    return {
      model,
      kind,
      detail: remaining ? '预计 ' + dur(remaining) + ' 后重试' : '等待重新探测',
      title: model + '\n模型当前不可用' + (deadline ? '\n最早重试：' + fmtLocalDateTime(deadline) : ''),
    };
  }
  let detail = resetAt
    ? '预计 ' + fmtLocalDateTime(resetAt) + ' 解封' + (remaining ? '（剩余 ' + dur(remaining) + '）' : '')
    : (until ? '预计 ' + fmtLocalDateTime(until) + ' 恢复（剩余 ' + dur(remaining) + '）' : '预计解封时间未知');
  const title = [model, resetAt ? '上游重置：' + fmtLocalDateTime(resetAt) : '上游重置：时间未知'];
  if (until && resetAt && until < resetAt) {
    detail += ' · 网关最快 ' + dur(Math.max(0, Math.round((until - now) / 1000))) + ' 后重试';
    title.push('网关最早重试：' + fmtLocalDateTime(until));
  }
  return { model, kind, detail, title: title.join('\n') };
}
function rateLimitRowsHtml(rows, now) {
  const list = Array.isArray(rows) ? rows.filter(row => row && row.model) : [];
  if (!list.length) return '';
  return '<div class="rate-limits">' + list.map(row => {
    const m = rateLimitMeta(row, now);
    return '<div class="rate-limit ' + (m.kind === 'model_unavailable' ? 'model-unavailable' : '') +
      '" title="' + esc(m.title) + '"><b>' + esc(m.model) + '</b><span>' + esc(m.detail) + '</span></div>';
  }).join('') + '</div>';
}

/* 数值格式化已统一到 43-usage-fmt.js 的 fmtTok / fmtMs / fmtRate：
   同一指标在账号卡与用量表曾经各算一套（1.5m vs 1.50M、1.3s vs 1.25s），
   用户看到的口径不一致。本分片只留限流文案。 */

/* ── 用量 ─────────────────────────────────────────────────────────── */
/* 图表用原生 SVG 手绘：面板是 go:embed 单文件、无构建步骤，引入图表库
   就得带上打包器，得不偿失。这里只需要堆叠柱状图，二十行足够。 */

function fmtTok(n) {
  const v = Number(n || 0);
  if (v >= 1e9) return trimFixed((v / 1e9).toFixed(2)) + 'B';
  if (v >= 1e6) return trimFixed((v / 1e6).toFixed(2)) + 'M';
  if (v >= 1e3) return trimFixed((v / 1e3).toFixed(2)) + 'k';
  return String(v);
}
function fmtMs(ms) {
  ms = Number(ms || 0);
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms >= 1000) return trimFixed((ms / 1000).toFixed(2)) + 's';
  return Math.round(ms) + 'ms';
}
function fmtRate(r) {
  const n = Number(r);
  return (r && Number.isFinite(n)) ? n.toFixed(1) + ' tok/s' : '—';
}
function trimFixed(s) {
  if (!String(s).includes('.')) return String(s);
  return String(s).replace(/0+$/, '').replace(/\.$/, '');
}
function fmtCredit(n) {
  const v = Number(n || 0);
  if (!Number.isFinite(v)) return '—';
  return trimFixed(v.toFixed(2));
}
function fmtCreditRatio(v, samples, tokens) {
  if (!samples || !tokens) return '—';
  const n = Number(v || 0);
  if (!Number.isFinite(n)) return '—';
  return trimFixed(n.toFixed(4)) + ' / 1M';
}
function fmtModelRate(rate) {
  const s = String(rate || '').trim();
  return s ? 'x' + s : '—';
}

/* cacheRatePct 缓存命中率百分比（0-100），无样本返回 null。
   请求记录文本、用量明细表与 KPI 卡共用这一份口径（issue #92），
   避免同一个数字在三处各算一遍、四舍五入到不同位数。 */
function cacheRatePct(hit, miss) {
  const h = Number(hit || 0), m = Number(miss || 0), total = h + m;
  return total ? h / total * 100 : null;
}

/* usKpi 用量页的指标卡。比账号池的 .stat 多两样：语义色轨（cls）与副标题（sub，
   放"占比 / 均速率"这类解释性数字）；bar 是卡片内的构成条 HTML，只有需要时才传。 */
function usKpi(v, k, cls, sub, bar) {
  return '<div class="kpi ' + (cls || '') + '">' +
    '<div class="k">' + esc(k) + '</div>' +
    '<div class="v">' + esc(v) + '</div>' +
    (bar || '') +
    (sub ? '<div class="s">' + esc(sub) + '</div>' : '') +
    '</div>';
}

/* usMixBar prompt/completion 占比条（走共用 segBar）。绝对量与占比都放 title：
   明细表因此只需一个「Token」列，总量 + 条已经说明结构，拆成 prompt / completion /
   合计 三列只是把同一个加法写三遍。 */
function usMixBar(prompt, completion, total) {
  const t = Number(total || 0);
  if (!t) return '';
  const pp = Number(prompt || 0) / t * 100;
  const pc = Number(completion || 0) / t * 100;
  return segBar([
    { value: prompt, color: 'var(--accent)',
      title: 'prompt ' + fmtTok(prompt) + ' (' + pp.toFixed(1) + '%)' },
    { value: completion, color: 'var(--ok)',
      title: 'completion ' + fmtTok(completion) + ' (' + pc.toFixed(1) + '%)' },
  ], { total: t, cls: 'us-mix' });
}

/* usPct 占比文案（0 值不显示 "0.0%"，直接 —，避免一行全是零）。 */
function usPct(part, total) {
  const t = Number(total || 0);
  if (!t) return '—';
  return (Number(part || 0) / t * 100).toFixed(1) + '%';
}

/* usRow 生成一行。mid 是插在「名称」之后、请求数之前的额外单元格（如「域」列）。
   withPerf 控制延迟/速率两列；列开关显式传入，避免调用方改动后与表头错列。
   Token 只出一列（合计）——prompt/completion 的绝对量与占比都在构成条的 title 里。 */
function usRow(name, sub, a, mid, withPerf) {
  const label = sub
    ? '<span class="row-cols"><span class="nm">' + esc(name) + '</span><span class="note">' + esc(sub) + '</span></span>'
    : esc(name);
  return '<tr>' +
    '<td class="mark" aria-hidden="true"></td>' +
    '<td>' + label + '</td>' +
    (mid || '') +
    '<td class="num">' + fmtTok(a.requests) + '</td>' +
    '<td class="num">' + (a.errors ? '<span style="color:var(--warn)">' + fmtTok(a.errors) + '</span>' : '—') + '</td>' +
    '<td class="num">' + fmtTok(a.total_tokens) +
      usMixBar(a.prompt_tokens, a.completion_tokens, a.total_tokens) + '</td>' +
    (withPerf
      ? '<td class="num">' + fmtMs(a.avg_latency_ms) + '</td>' +
        '<td class="num">' + fmtRate(a.avg_tokens_per_second) + '</td>'
      : '') +
    '</tr>';
}

/* renderPagination 通用轻量翻页条。 */
function renderPagination(el, page, totalPages, totalCount, onPage) {
  if (!el) return;
  if (totalPages <= 1) { el.innerHTML = ''; el.hidden = true; return; }
  el.hidden = false;
  el.innerHTML =
    '<div class="pg-info">共 ' + totalCount + ' 项 · 第 ' + page + ' / ' + totalPages + ' 页</div>' +
    '<div class="pg-acts">' +
    '<button type="button" class="xs" data-pg="prev"' + (page <= 1 ? ' disabled' : '') + '>上一页</button>' +
    '<button type="button" class="xs" data-pg="next"' + (page >= totalPages ? ' disabled' : '') + '>下一页</button>' +
    '</div>';
  el.onclick = ev => {
    const btn = ev.target.closest('button[data-pg]');
    if (!btn || btn.disabled) return;
    if (btn.dataset.pg === 'prev' && page > 1) onPage(page - 1);
    else if (btn.dataset.pg === 'next' && page < totalPages) onPage(page + 1);
  };
}
