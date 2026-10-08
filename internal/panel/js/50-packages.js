/* panel 分片 50-packages.js —— 积分包：数据层（配色/逐包分组/到期聚合）、构成与逐包明细渲染、排序模式、首页积分到期提醒卡。 */
/* ── 积分构成 ─────────────────────────────────────────────────────── */
/* 一个账号的余额是若干积分包之和。包按来源命名（「国内运营裂变包」「拉新权益包」
   「个人体验版」…），面额从 6 到 1500 不等，且**按次发放**。所以两个任务完成度
   完全一致的账号，余额可能差上千——差别只在包里。这里把逐包明细摊开，并给每个
   包名一个稳定配色，跨账号对比时同色即同类。 */

const PK_COLORS = ['#4f8cff', '#25b08b', '#e8a33d', '#c96bd6', '#e2607a',
                   '#5aa9e6', '#8fbf3f', '#b58b5a', '#7d8fa8', '#d4785c'];
function pkColor(i) { return PK_COLORS[i % PK_COLORS.length]; }

/* pkBySource 把包按名称归并，得到「来源 → 面额/余额/个数」。这是对比的关键视图：
   两个号的差异一定体现在某几个来源的面额上。 */
function pkBySource(packs) {
  const m = new Map();
  for (const p of packs) {
    // 分组键用 code + name，而不是只 name：上游给「首登赠送」和普通活动包用了
    // **同一个 PackageName 和同一个 PackageCode**，只按 name 会把两类混成一类，
    // 那正是当初「两个号为何差 1500」看不出来的原因。这里至少把 code 带进键里，
    // 并在卡片上显示最早的发放时间。
    const k = (p.package_code || '') + '|' + (p.name || '(未命名)');
    const e = m.get(k) || {
      key: k, name: p.name || '(未命名)', code: p.package_code || '',
      n: 0, remain: 0, size: 0, used: 0, minEnd: '', minCreated: '',
    };
    e.n += 1;
    e.remain += Number(p.remain || 0);
    e.size += Number(p.size || 0);
    e.used += Number(p.used || 0);
    const t = (p.end_time || '').slice(0, 10);
    if (t && (!e.minEnd || t < e.minEnd)) e.minEnd = t;
    const c = (p.created_at || '').slice(0, 10);
    if (c && (!e.minCreated || c < e.minCreated)) e.minCreated = c;
    m.set(k, e);
  }
  return [...m.values()].sort((a, b) => b.size - a.size);
}

const PK_DEFAULT_DETAIL_LIMIT = 5;

function pkDetailLimitValue(raw) {
  const n = Number(raw);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : PK_DEFAULT_DETAIL_LIMIT;
}

function pkDetailLimit(cfg) {
  return pkDetailLimitValue(cfg && cfg.panel && cfg.panel.package_detail_limit);
}

const PK_DAY_MS = 24 * 3600 * 1000;

function pkExpiryMs(p) {
  const raw = Number(p && p.expires_at);
  if (Number.isFinite(raw) && raw > 0) return raw;
  const text = String((p && p.end_time) || '').trim();
  if (!text) return null;
  let iso = text.includes('T') ? text : text.replace(' ', 'T');
  if (!/(?:Z|[+-]\d\d:\d\d)$/.test(iso)) iso += '+08:00';
  const parsed = Date.parse(iso);
  return Number.isFinite(parsed) ? parsed : null;
}

// pkDetailCompare 只服务单账号逐包明细：正余额包先按到期时间挑选默认展示项，
// 其余正余额包与已用完包分别折叠；同一到期时间按面额降序。主键跟随视图排序
// 模式（pkSortMode，声明在本区块末尾的绑定块）：end_asc 到期升序、size_desc
// 面额降序（同面额按到期升序）。typeof 守卫：前端 harness 的区域切片求值里
// 没有该全局，回落 end_asc（= 上游原有语义，切片测试的期望序不受影响）。
function pkDetailCompare(a, b) {
  const sizeOf = p => {
    const n = Number(p && p.size);
    return Number.isFinite(n) ? n : 0;
  };
  const mode = (typeof pkSortMode === 'string' && pkSortMode) || 'end_asc';
  if (mode === 'size_desc') {
    const d = sizeOf(b) - sizeOf(a);
    if (d !== 0) return d;
  }
  const ea = pkExpiryMs(a), eb = pkExpiryMs(b);
  if (ea == null && eb != null) return 1;
  if (ea != null && eb == null) return -1;
  if (ea != null && eb != null && ea !== eb) return ea - eb;
  return sizeOf(b) - sizeOf(a);
}

function pkDetailGroups(packs, limit) {
  const active = [], used = [];
  let usedSize = 0, restSize = 0, restRemain = 0;
  for (const p of packs || []) {
    const remain = Number(p && p.remain);
    if (remain > 0) {
      active.push(p);
      continue;
    }
    used.push(p);
    const size = Number(p && p.size);
    if (Number.isFinite(size)) usedSize += size;
  }
  active.sort(pkDetailCompare);
  used.sort(pkDetailCompare);
  const visible = active.slice(0, pkDetailLimitValue(limit));
  const rest = active.slice(visible.length);
  for (const p of rest) {
    const size = Number(p && p.size);
    if (Number.isFinite(size)) restSize += size;
    const remain = Number(p && p.remain);
    if (Number.isFinite(remain)) restRemain += remain;
  }
  return { visible, rest, used, restSize, restRemain, usedSize };
}

function pkAccountSegments(a, now) {
  let balance = Math.max(0, Number(a.remain || 0));
  const out = [];
  for (const p of a.packages || []) {
    const remain = Number(p.remain || 0);
    if (!Number.isFinite(remain) || remain <= 0 || balance <= 0) continue;
    const amount = Math.min(balance, remain);
    const expiresAt = pkExpiryMs(p);
    out.push({
      amount,
      expiresAt,
      days: expiresAt == null ? null : Math.max(0, Math.ceil((expiresAt - now) / PK_DAY_MS)),
      source: p.name || '积分',
      uid: String(a.uid || ''),
      accountName: a.nickname || String(a.uid || '').slice(0, 8) || '未命名账号',
    });
    balance -= amount;
  }
  return out.sort((x, y) => {
    if (x.expiresAt == null && y.expiresAt != null) return 1;
    if (x.expiresAt != null && y.expiresAt == null) return -1;
    return (x.expiresAt || 0) - (y.expiresAt || 0);
  });
}

// summarizeCreditDays 对齐 WorkDaddy：按精确剩余天数逐行聚合，无有效到期时间的余额
// 不进入图表，也不猜测到期日。账号内先按总余额约束逐包金额，避免上游重复记录膨胀。
function summarizeCreditDays(list, now) {
  const buckets = new Map();
  let unavailable = 0;
  for (const a of list || []) {
    if (a.error || !Number.isFinite(Number(a.remain))) {
      unavailable++;
      continue;
    }
    for (const segment of pkAccountSegments(a, now)) {
      if (segment.days == null) continue;
      let row = buckets.get(segment.days);
      if (!row) {
        row = { days: segment.days, credits: 0, segments: [] };
        buckets.set(segment.days, row);
      }
      row.credits += segment.amount;
      row.segments.push(segment);
    }
  }
  const rows = [...buckets.values()].sort((a, b) => a.days - b.days);
  for (const row of rows) {
    row.segments.sort((a, b) =>
      (a.expiresAt || Infinity) - (b.expiresAt || Infinity) ||
      a.accountName.localeCompare(b.accountName) ||
      a.source.localeCompare(b.source));
  }
  return { rows, accountCount: (list || []).length, unavailable };
}


let pkPage = 1, pkPageSize = 20;

function renderPackages(d, detailLimit) {
  const list = (d.accounts || []);
  const today = new Date(); today.setHours(0, 0, 0, 0);

  if ($('pkSummary')) $('pkSummary').innerHTML = '';
  if ($('pkDetail')) $('pkDetail').innerHTML = '';

  const tb = $('pkTableBody');
  if (!tb) return;

  if (!list.length) {
    tb.innerHTML = '<tr><td colspan="9"><div class="empty">没有账号数据</div></td></tr>';
    renderPagination($('pkPager'), 1, 1, 0, () => {});
    $('pkNote').textContent = '0 个账号';
    return;
  }

  const names = [];
  for (const a of list) for (const s of pkBySource(a.packages || [])) {
    if (!names.includes(s.key)) names.push(s.key);
  }
  const colorOf = n => pkColor(names.indexOf(n));

  $('pkNote').textContent = list.length + ' 个账号 · 实时查询上游';

  const totalPages = Math.max(1, Math.ceil(list.length / pkPageSize));
  if (pkPage > totalPages) pkPage = totalPages;
  const paged = list.slice((pkPage - 1) * pkPageSize, pkPage * pkPageSize);

  tb.innerHTML = paged.map(a => {
    if (a.error) {
      return '<tr><td class="mark" aria-hidden="true"><i style="background:var(--bad)"></i></td>' +
        '<td><b>' + esc(a.nickname || a.uid.slice(0, 8)) + '</b></td>' +
        '<td><span class="num note">' + esc(a.uid.slice(0, 8)) + '</span></td>' +
        '<td><span class="realm-tag">' + esc(a.realm || '') + '</span></td>' +
        '<td colspan="6" style="color:var(--bad)">查询失败：' + esc(a.error) + '</td></tr>';
    }
    const bs = expBatches(a.packages || []).filter(b => expDaysLeft(b.date, today) >= 0);
    let expHtml = '<span class="note">—</span>';
    if (bs.length) {
      const first = bs[0];
      const days = expDaysLeft(first.date, today);
      const dayWord = days === 0 ? '今天' : days === 1 ? '明天' : days + '天后';
      const c = days <= 3 ? 'var(--bad)' : days <= 7 ? 'var(--warn)' : 'inherit';
      expHtml = '<span style="color:' + c + '"><b>' + esc(first.date) + '</b> · ' + fmtTok(first.remain) +
        ' <span class="note">(' + dayWord + ')</span></span>';
    }
    const srcs = pkBySource(a.packages || []);
    const totalSize = srcs.reduce((s, x) => s + (x.size || 0), 0) || 1;
    const srcTags = srcs.slice(0, 3).map(s => {
      const pct = Math.round((s.size || 0) / totalSize * 100);
      const shortName = esc(s.name.replace(/^CodeBuddy/, '').slice(0, 8));
      return '<span class="pk-src-tag" title="' + esc(s.name) + ' 共 ' + fmtTok(s.size) + '">' +
        '<i class="pk-src-dot" style="background:' + colorOf(s.key) + '"></i>' +
        shortName + ' <b>' + pct + '%</b></span>';
    }).join('');
    const availablePks = (a.packages || []).filter(p => Number(p.remain) > 0).length;

    return '<tr><td class="mark" aria-hidden="true"><i style="background:var(--ok)"></i></td>' +
      '<td><b>' + esc(a.nickname || a.uid.slice(0, 8)) + '</b></td>' +
      '<td><span class="num note">' + esc(a.uid.slice(0, 8)) + '</span></td>' +
      '<td><span class="realm-tag">' + esc(a.realm || '') + '</span></td>' +
      '<td class="num"><b>' + fmtTok(a.remain) + '</b></td>' +
      '<td class="num">' + fmtTok(a.size) + '</td>' +
      '<td class="num">' + availablePks + ' / ' + (a.packages || []).length + '</td>' +
      '<td>' + expHtml + '</td>' +
      '<td>' + (srcTags || '<span class="note">—</span>') + '</td>' +
      '<td class="acts"><button class="xs" data-pk-view="' + esc(a.uid) + '">包明细</button></td></tr>';
  }).join('');

  renderPagination($('pkPager'), pkPage, totalPages, list.length, p => {
    pkPage = p;
    renderPackages(lastPackages, detailLimit);
  });
}

function openAccountPackages(uid) {
  const list = (lastPackages && lastPackages.accounts) || [];
  const a = list.find(x => x.uid === uid);
  if (!a) return;
  if ($('pkModalWho')) $('pkModalWho').textContent = (a.nickname || a.uid.slice(0, 8)) + ' · ' + (a.realm || '');
  const pks = (a.packages || []).slice().sort((x, y) => {
    const tx = x.end_time || '9999', ty = y.end_time || '9999';
    return tx.localeCompare(ty);
  });
  const body = $('pkModalBody');
  if (body) {
    body.innerHTML = pks.map(p =>
      '<tr><td class="mark" aria-hidden="true"><i style="background:var(--accent)"></i></td>' +
      '<td><div class="nm">' + esc(p.name || '(未命名)') + '</div><div class="id">' + esc(p.package_code || '') + '</div></td>' +
      '<td class="num">' + fmtTok(p.size) + '</td>' +
      '<td class="num"><b>' + fmtTok(p.remain) + '</b></td>' +
      '<td class="num">' + fmtTok(p.used) + '</td>' +
      '<td class="num">' + esc((p.created_at || '').slice(0, 16).replace('T', ' ') || '—') + '</td>' +
      '<td class="num">' + esc((p.end_time || '').slice(0, 10) || '永久/无') + '</td></tr>'
    ).join('') || '<tr><td colspan="7"><div class="empty">无积分包记录</div></td></tr>';
  }
  if ($('pkVeil')) $('pkVeil').classList.add('on');
}

if ($('pkTableBody')) $('pkTableBody').onclick = ev => {
  const btn = ev.target.closest('button[data-pk-view]');
  if (!btn) return;
  openAccountPackages(btn.dataset.pkView);
};
if ($('btnPkModalClose')) $('btnPkModalClose').onclick = () => {
  if ($('pkVeil')) $('pkVeil').classList.remove('on');
};

if ($('pkDetail')) $('pkDetail').addEventListener('click', ev => {
  const btn = ev.target.closest('button[data-pk-group]');
  if (!btn) return;
  const body = btn.closest('tbody');
  if (!body) return;
  const group = btn.dataset.pkGroup;
  const expanded = btn.getAttribute('aria-expanded') === 'true';
  body.querySelectorAll('tr[data-pk-row="' + group + '"]').forEach(row => { row.hidden = expanded; });
  const count = btn.dataset.count || '0';
  const size = btn.dataset.size || '0';
  const remain = btn.dataset.remain || '0';
  btn.setAttribute('aria-expanded', String(!expanded));
  if (group === 'rest') {
    btn.textContent = expanded
      ? '其余未用完 ' + count + ' 个包（面额合计 ' + fmtTok(size) + ' · 剩余 ' +
        fmtTok(remain) + '），展开'
      : '收起其余未用完 ' + count + ' 个包';
  } else {
    btn.textContent = expanded
      ? '已用完 ' + count + ' 个包（面额合计 ' + fmtTok(size) + '），展开'
      : '收起已用完 ' + count + ' 个包';
  }
});

/* ── 逐包明细排序模式 ─────────────────────────────────────────────── */
/* 逐包明细的排序规则（选择持久化在 localStorage，跨会话记住）：
   end_asc   到期升序（默认）——快过期的包排最前，提醒优先消耗；无到期时间的
             包（上游没下发 end_time）没有可比的日期，统一垫底，不掺进日期序里；
   size_desc 面额降序——原来的展示口径，看「钱从哪来」。
   行序统一由 pkDetailCompare 实现（明细折叠分组共用同一比较器，切换模式时
   折叠组内行序同步跟随）。本块整体放在 renderPackages / renderExpiry 之后：
   前端 harness 按区域切片求值（[PK_DEFAULT_DETAIL_LIMIT, renderPackages) 与
   [expBatches, loadExpiry)），顶层 localStorage/$ 语句落进切片区会让无 DOM 桩的
   求值环境 ReferenceError——上游测试的切片边界不动。 */
const LS_PK_SORT = 'pkSortMode';
let pkSortMode = localStorage.getItem(LS_PK_SORT) || 'end_asc';
const PK_SORT_LABELS = { end_asc: '按到期升序 · 近的在前', size_desc: '按面额降序' };
// 排序切换时重排明细需要 detailLimit（上游折叠配置），loadPackages 拉到后缓存。
let lastDetailLimit = PK_DEFAULT_DETAIL_LIMIT;

// 排序规则控件：恢复上次选择并绑定切换。
if ($('pkSort')) {
  $('pkSort').value = pkSortMode;
  if ($('pkSort').value !== pkSortMode) {        // localStorage 里存了废弃值：回落默认
    pkSortMode = 'end_asc';
    localStorage.setItem(LS_PK_SORT, pkSortMode);   // 不用 removeItem：harness 桩无此方法
  }
  $('pkSort').onchange = () => {
    pkSortMode = $('pkSort').value;
    localStorage.setItem(LS_PK_SORT, pkSortMode);
    if (lastPackages) renderPackages(lastPackages, lastDetailLimit);   // 数据在内存，直接重排
  };
}

async function loadPackages() {
  $('pkSummary').innerHTML = '<div class="empty">查询中…（逐账号向上游实时查询）</div>';
  $('pkDetail').innerHTML = '';
  try {
    const [d, c] = await Promise.all([
      api('packages'),
      api('config').catch(() => null),
    ]);
    lastPackages = d;                            // 缓存供首页到期卡片与排序切换复用
    lastPackagesAt = Date.now();
    lastDetailLimit = pkDetailLimit(c && c.config);
    renderPackages(d, lastDetailLimit);
  } catch (e) {
    $('pkSummary').innerHTML = '<div class="empty">读取失败：' + esc(e.message) + '</div>';
  }
}

/* ── 积分到期提醒（首页卡片）────────────────────────────────────────── */
/* 积分不是永久的：签到/任务发的裂变包约一个月失效。只看「剩余积分 ÷ 日消耗」
   会系统性偏乐观——用不完的部分到期直接蒸发。这里把「最近要过期的是哪批、
   有多少、到期前每天要至少消耗多少」顶到首页，数据源与「积分构成」共用
   （lastPackages 缓存，EXP_FRESH_MS 内复用，不重复打上游）。 */

// expBatches 把某账号的包聚合成「到期日 → 该日作废积分」升序列表。
// 只统计 remain>0 且有到期时间的包——没余额/长期包到期没有任何影响。
function expBatches(packs) {
  const byDay = new Map();
  for (const p of packs || []) {
    const r = Number(p.remain || 0);
    const t = (p.end_time || '').slice(0, 10);
    if (r <= 0 || !t) continue;
    byDay.set(t, (byDay.get(t) || 0) + r);
  }
  return [...byDay.entries()]
    .map(([date, remain]) => ({ date, remain }))
    .sort((a, b) => (a.date < b.date ? -1 : 1));
}

function expDaysLeft(dateStr, today) {
  return Math.round((new Date(dateStr + 'T00:00:00') - today) / 86400000);
}

function renderExpiry(d) {
  const list = (d.accounts || []);
  const today = new Date(); today.setHours(0, 0, 0, 0);
  // 按「最近到期」升序排（issue #125）。
  //
  // 后端 /panel/api/packages 是按**余额降序**返回的，恰好把 CN 账号都排在前面、
  // global 排在末尾，看起来像"按域分组"，其实只是余额顺序。这里再排一次，让
  // "最快过期的排最前"这个唯一重要的顺序成立，且不分域。
  //
  // 排序键用最早到期批次的日期（expBatches 已按日期升序，故 [0] 即最早）。
  // 查询失败的账号、以及 7 天内无到期的账号没有可比较的到期时间，统一排在最后，
  // 保持它们原本的相对顺序（稳定排序）。
  const keyed = list.map(a => {
    let key = null;
    if (!a.error) {
      const bs = expBatches(a.packages).filter(b => expDaysLeft(b.date, today) >= 0);
      if (bs.length) key = bs[0].date;
    }
    return { a, key };
  });
  keyed.sort((x, y) => {
    if (x.key === y.key) return 0;
    if (x.key === null) return 1;
    if (y.key === null) return -1;
    return x.key < y.key ? -1 : 1;
  });
  const rows = keyed.map(({ a }) => {
    const name = esc(a.nickname || a.uid.slice(0, 8));
    if (a.error) {
      return '<div class="exp-row"><span class="exp-dot" style="background:var(--bad)"></span>' +
        '<span class="exp-nm">' + name + '</span>' +
        '<span class="exp-main err">查询失败：' + esc(a.error) + '</span></div>';
    }
    const bs = expBatches(a.packages).filter(b => expDaysLeft(b.date, today) >= 0);
    if (!bs.length) {
      return '<div class="exp-row"><span class="exp-dot" style="background:var(--line)"></span>' +
        '<span class="exp-nm">' + name + '</span>' +
        '<span class="exp-main" style="color:var(--ink-3)">近期无到期包</span></div>';
    }
    const first = bs[0];
    const days = expDaysLeft(first.date, today);
    const cls = days <= 3 ? 'var(--bad)' : days <= 7 ? 'var(--warn)' : 'var(--ok)';
    const dayWord = days === 0 ? '今天到期' : days === 1 ? '明天到期' : days + ' 天后';
    return '<div class="exp-row"><span class="exp-dot" style="background:' + cls + '"></span>' +
      '<span class="exp-nm">' + name + '</span>' +
      '<span class="exp-main">最近 <b>' + esc(first.date) + '</b>（' + dayWord + '）· <b>' + fmtTok(first.remain) + '</b> 积分</span></div>';
  }).join('');
  // 归集条：全账号按紧迫度分桶（≤3 天 / 4-7 天 / 更远 / 无到期），一眼看出"要抓紧的有多少"。
  // 这是到期信息唯一的图表视图（原「积分到期分布」图与账号卡上的到期条均已并到这里）。
  const buckets = [
    { label: '≤3 天', max: 3, color: 'var(--bad)' },
    { label: '4-7 天', max: 7, color: 'var(--warn)' },
    { label: '7 天以上', max: Infinity, color: 'var(--ok)' },
  ].map(b => ({ value: 0, color: b.color, title: b.label }));
  const nowMs = today.getTime();
  summarizeCreditDays(list, nowMs).rows.forEach(r => {
    const i = r.days <= 3 ? 0 : (r.days <= 7 ? 1 : 2);
    buckets[i].value += r.credits;
  });
  // 没有到期时间的余额单独一段（上游没下发 end_time）：它不会作废，不应混进紧迫度里。
  let noExpiry = 0;
  for (const a of list) {
    if (a.error || !Number.isFinite(Number(a.remain))) continue;
    const known = pkAccountSegments(a, nowMs).reduce((t, s) => t + s.amount, 0);
    noExpiry += Math.max(0, Number(a.remain) - known);
  }
  if (noExpiry > 0) buckets.push({ value: noExpiry, color: 'var(--line)', title: '无到期时间' });
  const segs = buckets.filter(b => b.value > 0)
    .map(b => ({ value: b.value, color: b.color, title: b.title + ' · ' + fmtTok(b.value) + ' 积分' }));
  $('expList').innerHTML = segBar(segs, { cls: 'exp-sum', aria: '全部账号的积分到期紧迫度' }) +
    '<div class="exp-rows">' + (rows || '<div class="empty">没有账号</div>') + '</div>';
  // 数据新鲜度透明化：走缓存时标注年龄，免得把旧数据误当实时。
  const ageMin = lastPackages ? Math.floor((Date.now() - lastPackagesAt) / 60000) : 0;
  $('expNote').textContent = (lastPackagesAt && ageMin > 0)
    ? list.length + ' 个账号 · ' + ageMin + ' 分钟前的数据，可点「检查」刷新'
    : list.length + ' 个账号 · 实时查询上游';
  $('expBox').hidden = false;
}

async function loadExpiry(force) {
  if (!$('expBox')) return;
  if (expFetching) return;
  const fresh = lastPackages && (Date.now() - lastPackagesAt) < EXP_FRESH_MS;
  if (fresh && !force) { renderExpiry(lastPackages); return; }
  expFetching = true;
  $('expBox').hidden = false;
  if (!$('expList').children.length) $('expList').innerHTML = '<div class="empty">查询中…（逐账号向上游实时查询）</div>';
  $('expNote').textContent = '查询中…';
  try {
    const d = await api('packages');
    lastPackages = d;                            // 与「积分构成」视图共用同一份缓存
    lastPackagesAt = Date.now();
    renderExpiry(d);
  } catch (e) {
    $('expNote').textContent = '查询失败：' + esc(e.message);
  }
  expFetching = false;
}

if ($('btnExp')) $('btnExp').onclick = () => loadExpiry(true);

if ($('btnPk')) $('btnPk').onclick = loadPackages;
