/* panel 分片 20-accounts.js —— 账号池视图：KPI 统计、账号表格与批量动作、模型锁池。 */
/* ── 账号池 ───────────────────────────────────────────────────────── */
function renderAccounts(list) {
  const tb = $('accBody');
  if (!list.length) {
    tb.innerHTML = '<tr><td colspan="9"><div class="empty"><div class="big">账号池是空的</div>点击右上角「添加账号」，用浏览器登录一个 WorkBuddy 账号</div></td></tr>';
    return;
  }
  // 有总额度（credits_total）→ 进度条按自身 剩余/总额 百分比；旧数据无总额 → 退回池内最高=100%
  const maxCred = Math.max(1, ...list.map(s => s.credits || 0));
  tb.innerHTML = list.map(s => {
    const bl = (new Date(s.breaker_until || 0) - Date.now()) / 1000;
    const dg = (new Date(s.degrade_until || 0) - Date.now()) / 1000;
    const cool = Math.max(s.cool_remaining_sec || 0, bl > 0 ? bl : 0, dg > 0 ? dg : 0);
    let cls = '', tag;
    if (s.disabled) { cls = 'off'; tag = '<span class="tag bad">已禁用</span>'; }
    else if (s.paused) { cls = 'off'; tag = '<span class="tag warn">已暂停选号</span>'; }
    else if (cool > 0) {
      cls = 'cool';
      const kind = bl > Math.max(s.cool_remaining_sec || 0, dg > 0 ? dg : 0) ? '熔断'
        : (dg > (s.cool_remaining_sec || 0) ? '连败降权' : (s.cool_kind === 'hard_credit' ? '积分冷却' : '限流冷却'));
      tag = '<span class="tag warn">' + kind + ' · ' + dur(cool) + '</span>';
    } else tag = '<span class="tag ok">可用</span>' + (s.in_flight ? '' : '');
    const note = s.reason ? '<div class="hint" style="font-size:11.5px;color:var(--ink-3);margin-top:3px">' + esc(s.reason) + '</div>' : '';
    const rateLimits = rateLimitRowsHtml(s.rate_limited_models, Date.now());
    const short = s.uid.length > 16 ? s.uid.slice(0, 16) + '…' : s.uid;
    // 企业版不限量：上游 limitNum == -1，网关以 credits_total=-1 透出（见 upstream
    // enterpriseUnlimitedTotal）。此时剩余额度不参与展示，直接标「不限」。
    const unlimited = s.credits_total === -1;
    const cred = unlimited ? '不限'
      : (s.credits == null ? '—' : (s.credits_total > 0 ? s.credits + '<span class="of">/' + s.credits_total + '</span>' : String(s.credits)));
    const pct = unlimited ? 100
      : (s.credits_total > 0
        ? Math.min(100, Math.round((s.credits || 0) / s.credits_total * 100))
        : Math.round((s.credits || 0) / maxCred * 100));
    // 成本台账 tooltip（model_costs）：每模型实测单价（≤0 = 实测免费），运维据此
    // 看「为什么总选它」——免费号垄断 / 单价排序一眼可见。
    let credTip;
    if (unlimited) credTip = '企业版不限量（上游 limitNum=-1）';
    else if (s.credits_total > 0) {
      credTip = (s.enterprise ? '企业版剩余额度 ' : '剩余 ')
        + s.credits + ' / ' + (s.enterprise ? '分配 ' : '总额 ') + s.credits_total + '（' + pct + '%）';
    } else credTip = '积分（相对池内最高）';
    const costs = (s.model_costs || []).filter(c => c.model);
    if (costs.length) {
      credTip += '\n实测单价（credits/1K）：\n' + costs.map(c =>
        '  ' + c.model + '：' + (c.cost_per_1k <= 0 ? '免费' : c.cost_per_1k)).join('\n');
    }
    const frozen = s.disabled || cool > 0;
    // 账号级代理开关（仅全局配置了代理时显示）：开 = 走代理，关 = 直连。
    const proxyBtn = (overviewData && overviewData.proxy_configured)
      ? '<button class="xs ghost" data-a="proxy" data-u="' + esc(s.uid) + '" title="切换该账号是否走全局代理（' + esc(overviewData.proxy_mode || '') + '）；关闭后该账号直连">' +
        '代理' + (s.proxy_enabled ? '<span style="color:var(--ok)"> 开</span>' : '<span style="color:var(--ink-3)"> 关</span>') + '</button>'
      : '';
    const tu = s.token_usage || {};
    const req = tu.request_count || 0;
    const totalTok = tu.total_tokens == null ? '—' : fmtTok(tu.total_tokens);
    const totalTokUnit = totalTok === '—' ? '' : '<em>tok</em>';
    const latency = fmtMs(tu.last_latency_ms);
    const rate = fmtRate(tu.last_tokens_per_second);
    const usageTitle = '最近一次：' + req + ' 次 / ' + totalTok + ' / 延迟 ' + latency + ' / ' + rate;
    // 转义只做一次：同一串同时当悬浮提示（窄屏截断时看全）与无障碍名称。
    const usageAttr = esc(usageTitle);
    return '<tr class="' + cls + '" title="uid: ' + esc(s.uid) + '">' +
      '<td class="mark" aria-hidden="true"><i></i></td>' +
      '<td class="who"><div class="nm">' + (s.nickname ? esc(s.nickname) : '<span style="color:var(--ink-3)">未命名</span>') + (s.realm === 'global' ? ' <span class="realm-tag">国际版</span>' : '') + (s.enterprise ? ' <span class="realm-tag">企业版</span>' : '') + '</div><div class="id">' + esc(short) + '</div></td>' +
      '<td>' + tag + note + rateLimits + '</td>' +
      '<td class="cred" title="' + esc(credTip) + '"><div class="n">' + cred + '</div>' +
        segBar([{ value: pct, title: credTip }], { total: 100, cls: 'cred-bar' }) + '</td>' +
      '<td class="num">' + (s.success_count || 0) + ' <span style="color:var(--ink-3)">/</span> <span style="color:var(--bad)">' + (s.err_total || 0) + '</span></td>' +
      '<td class="num">' + (s.in_flight || 0) + '</td>' +
      '<td class="num usage-cell" title="' + usageAttr + '"><span class="usage-line" aria-label="' + usageAttr + '">' +
        '<span class="usage-item usage-count"><b>' + req + '</b><em>次</em></span>' +
        '<span class="usage-item usage-total"><b>' + totalTok + '</b>' + totalTokUnit + '</span>' +
        '<span class="usage-item usage-latency"><b>' + latency + '</b></span>' +
      '</span></td>' +
      '<td class="num" style="color:var(--ink-3)">' + ago(s.last_success) + '</td>' +
      '<td class="acts">' +
        // 企业版无个人成长体系（签到 400「企业账号不支持该操作」/ 成长任务 403）：
        // 不渲染「签到」「任务」按钮，只留「额度」——点它走 /balance，企业额度由
        // upstream 的 get-enterprise-user-usage 口径填充。
        (s.enterprise ? '' :
          '<button class="xs ghost" data-a="checkin" data-u="' + esc(s.uid) + '"' + (s.checkin_done ? ' title="今日已签到；点击可重新签到并刷新余额"' : '') + '>' + (s.checkin_done ? '已签' : '签到') + '</button>') +
        '<button class="xs ghost" data-a="balance" data-u="' + esc(s.uid) + '"' + (s.enterprise ? ' title="刷新企业版已分配额度（上游 get-enterprise-user-usage）"' : '') + '>' + (s.enterprise ? '额度' : '余额') + '</button>' +
        (s.enterprise ? '' :
          '<button class="xs ghost" data-a="tasks" data-u="' + esc(s.uid) + '">任务</button>') +
        proxyBtn +
        (frozen ? '<button class="xs primary" data-a="revive" data-u="' + esc(s.uid) + '">解冻</button>'
                : (s.paused ? '<button class="xs primary" data-a="resume" data-u="' + esc(s.uid) + '">恢复选号</button>'
                            : '<button class="xs ghost" data-a="pause" data-u="' + esc(s.uid) + '" title="' + (s.enterprise ? '退出选号，但照常保活 / 刷新额度' : '退出选号，但照常签到 / 活跃上报 / 保活 / 刷新余额') + '">暂停选号</button>')) +
        (s.disabled ? '' : '<button class="xs ghost" data-a="disable" data-u="' + esc(s.uid) + '">禁用</button>') +
        '<button class="xs ghost danger" data-a="remove" data-u="' + esc(s.uid) + '">移除</button>' +
      '</td></tr>';
  }).join('');
}

// renderModelLocks 模型锁池：哪些模型不能用、锁了几个号、还要锁多久。
// 后端 model_locks 已按「整池不可用 → 没号可用 → 部分限流」排好序，这里只做展示。
function renderModelLocks(rows) {
  const tb = $('mlBody');
  if (!tb) return;
  const note = $('mlNote');
  if (!rows || !rows.length) {
    tb.innerHTML = '<tr><td colspan="8"><div class="empty">当前没有模型级限流 —— 所有模型均可选</div></td></tr>';
    if (note) note.textContent = '';
    return;
  }
  const STATE = { locked: ['bad', '整池不可用'], starved: ['warn', '没号可用'], partial: ['warn', '部分限流'] };
  const left = iso => {
    const ms = parseAPITime(iso);
    return ms ? dur(Math.max(0, Math.round((ms - Date.now()) / 1000))) : '—';
  };
  tb.innerHTML = rows.map(r => {
    const st = STATE[r.state] || ['mute', r.state || '—'];
    const realm = r.realm === 'global' ? '国际版' : '国内版';
    return '<tr>' +
      '<td>' + esc(r.model) + '</td>' +
      '<td><span class="realm-tag">' + realm + '</span></td>' +
      '<td><span class="tag ' + st[0] + '">' + st[1] + '</span></td>' +
      '<td class="num">' + (r.servable || 0) + ' / ' + (r.total || 0) + '</td>' +
      '<td class="num">' + (r.locked || 0) + '</td>' +
      '<td class="num">' + left(r.unlock_at || r.fully_unlock_at) + '</td>' +
      '<td class="num">' + left(r.fully_unlock_at) + '</td>' +
      '<td>' + (r.reason ? '<div class="note">' + esc(r.reason) + '</div>' : '—') + '</td>' +
      '</tr>';
  }).join('');
  if (note) {
    const bad = rows.filter(r => r.state === 'locked' || r.state === 'starved').length;
    note.textContent = bad ? bad + ' 个模型整池不可用' : rows.length + ' 个模型部分限流';
  }
}

async function loadOverview(quiet) {
  try {
    const d = await api('overview');
    overviewData = d;
    $('sTotal').textContent = d.total;
    const healthy = d.healthy || 0;
    const cooling = d.cooling || 0;
    const disabled = d.disabled || 0;
    const paused = d.paused == null ? 0 : d.paused;
    const total = d.total || 0;

    $('sHealthy').textContent = healthy;
    const parts = [];
    if (cooling) parts.push('冷却 ' + cooling);
    if (disabled) parts.push('禁用 ' + disabled);
    if (paused) parts.push('暂停 ' + paused);
    $('sAvailNote').textContent = parts.length ? parts.join(' · ') : (total ? '全部正常可用' : '池内暂无账号');

    const availCard = $('statAvail');
    if (availCard) {
      availCard.className = 'kpi ' + (healthy === 0 && total > 0 ? 'c-bad'
        : cooling > 0 ? 'c-warn'
        : 'c-ok');
    }
    const barEl = $('sAvailBar');
    if (barEl) {
      barEl.innerHTML = total > 0 ? segBar([
        { value: healthy, color: 'var(--ok)', title: '可用 ' + healthy },
        { value: cooling, color: 'var(--warn)', title: '冷却 ' + cooling },
        { value: paused, color: 'var(--ink-3)', title: '暂停 ' + paused },
        { value: disabled, color: 'var(--bad)', title: '禁用 ' + disabled },
      ], { total: total, aria: '账号池可用比例' }) : '';
    }
    const remSum = (d.accounts || []).reduce((a, s) => a + (s.credits || 0), 0);
  const totSum = (d.accounts || []).reduce((a, s) => a + (s.credits_total || 0), 0);
  $('sCredits').textContent = totSum > 0 ? remSum + ' / ' + totSum : remSum;
    $('sSticky').textContent = d.sticky_sessions;
    $('navSub').textContent = 'v' + d.version;
    $('navVer').textContent = 'v' + d.version;
    $('navRedis').textContent = d.redis_mode === 'upstash' ? 'Redis 镜像' : '本地内存';
    $('navState').textContent = d.healthy > 0 ? '服务正常' : (d.total ? '无可用账号' : '待添加账号');
    const p = $('navPulse');
    p.className = 'pulse' + (d.healthy > 0 ? '' : (d.total ? ' warn' : ' bad'));
    $('accNote').textContent = d.in_flight_full ? d.in_flight_full + ' 个账号在途占满' : '';
    const up = Math.floor(d.uptime_sec);
    $('subMeta').textContent = '运行 ' + (up >= 86400 ? Math.floor(up / 86400) + ' 天 ' : '') + Math.floor(up % 86400 / 3600) + ' 时 ' + Math.floor(up % 3600 / 60) + ' 分';
    renderAccounts(d.accounts || []);
    renderModelLocks(d.model_locks);
  } catch (e) { if (!quiet) toast(e.message, 'err'); }
}

$('accBody').addEventListener('click', async ev => {
  const b = ev.target.closest('button[data-a]');
  if (!b) return;
  const u = b.dataset.u, a = b.dataset.a;
  if (a === 'remove' && !confirm('移除账号将删除池状态与 auths/ 下的凭证文件，且不可恢复。确认移除？')) return;
  if (a === 'disable' && !confirm('禁用后该账号不再参与选号（保号任务默认也跳过），需手动解冻才能恢复。若只是想临时让位、仍要保号，请改用「暂停选号」。确认禁用？')) return;
  b.disabled = true;
  try {
    if (a === 'checkin') {
      const r = await api('accounts/' + encodeURIComponent(u) + '/checkin', { method: 'POST' });
      toast('签到完成' + (r.credits != null ? '，积分 ' + r.credits + (r.credits_total > 0 ? '/' + r.credits_total : '') : '') + (r.checkin_message ? '（' + r.checkin_message + '）' : ''), 'ok');
    } else if (a === 'balance') {
      const r = await api('accounts/' + encodeURIComponent(u) + '/balance', { method: 'POST' });
      toast('余额已更新：' + r.credits + (r.credits_total > 0 ? ' / ' + r.credits_total : ''), 'ok');
    } else if (a === 'revive') {
      await api('accounts/' + encodeURIComponent(u) + '/revive', { method: 'POST' });
      toast('已解冻', 'ok');
    } else if (a === 'disable') {
      await api('accounts/' + encodeURIComponent(u) + '/disable', { method: 'POST' });
      toast('已禁用', 'ok');
    } else if (a === 'pause') {
      await api('accounts/' + encodeURIComponent(u) + '/pause', { method: 'POST' });
      toast('已暂停选号（签到 / 保活照常）', 'ok');
    } else if (a === 'resume') {
      await api('accounts/' + encodeURIComponent(u) + '/resume', { method: 'POST' });
      toast('已恢复选号', 'ok');
    } else if (a === 'proxy') {
      const s = (overviewData && overviewData.accounts || []).find(x => x.uid === u);
      const enabled = !(s && s.proxy_enabled);
      const r = await api('accounts/' + encodeURIComponent(u) + '/proxy', { method: 'POST', body: JSON.stringify({ enabled: enabled }) });
      toast('该账号代理已' + (r.proxy_enabled ? '开启' : '关闭（直连）'), 'ok');
    } else if (a === 'tasks') {
      openTasks(u);
    } else if (a === 'remove') {
      const r = await api('accounts/' + encodeURIComponent(u) + '/remove', { method: 'POST' });
      toast(r.file_error ? '已移除（凭证文件删除失败：' + r.file_error + '）' : '已移除', 'ok');
    }
  } catch (e) { toast(e.message, 'err'); }
  finally { b.disabled = false; loadOverview(true); }
});

$('btnCheckinAll').onclick = async () => {
  try { await api('checkin_all', { method: 'POST' }); toast('全部签到已开始，结果见日志', 'ok'); }
  catch (e) { toast(e.message, 'err'); }
};
$('btnKeepaliveAll').onclick = async () => {
  try { await api('keepalive_all', { method: 'POST' }); toast('全部保活已开始，结果见日志', 'ok'); }
  catch (e) { toast(e.message, 'err'); }
};
$('btnTravelAll').onclick = async () => {
  try { await api('travel_all', { method: 'POST' }); toast('旅行巡检已开始（含领养链路），结果见日志', 'ok'); }
  catch (e) { toast(e.message, 'err'); }
};
$('btnActivityAll').onclick = async () => {
  try { await api('activity_all', { method: 'POST' }); toast('活跃上报已开始，结果见日志', 'ok'); }
  catch (e) { toast(e.message, 'err'); }
};
