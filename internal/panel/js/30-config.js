/* panel 分片 30-config.js —— 配置视图：CFG_MAP 映射、指纹规则编解码、表单校验与提交、提示词预设/预览、规则显隐与组合位置示意图。 */
/* ── 配置 ─────────────────────────────────────────────────────────── */
const CFG_MAP = {
  listen: ['listen'], api_key: ['api_key'],
  package_detail_limit: ['panel', 'package_detail_limit'],
  checkin_hours: ['schedule', 'checkin_hours'], checkin_enabled: ['schedule', 'checkin_enabled'], growth_hours: ['schedule', 'growth_hours'], growth_enabled: ['schedule', 'growth_enabled'],
  travel_hours: ['schedule', 'travel_hours'], travel_enabled: ['schedule', 'travel_enabled'],
  activity_hours: ['schedule', 'activity_hours'], activity_enabled: ['schedule', 'activity_enabled'],
  keepalive_hours: ['schedule', 'keepalive_hours'], keepalive_enabled: ['schedule', 'keepalive_enabled'],
  balance_refresh_enabled: ['schedule', 'balance_refresh_enabled'], balance_refresh_minutes: ['schedule', 'balance_refresh_minutes'],
  include_disabled_in_tasks: ['schedule', 'include_disabled_in_tasks'],
  max_in_flight: ['pool', 'max_in_flight'], max_in_flight_global: ['pool', 'max_in_flight_global'],
  breaker_threshold: ['pool', 'breaker_threshold'],
  degrade_threshold: ['pool', 'degrade_threshold'], degrade_cooldown: ['pool', 'degrade_cooldown'],
  degrade_cooldown_max: ['pool', 'degrade_cooldown_max'],
  cost_explore_interval: ['pool', 'cost_explore_interval'],
  credit_floor: ['pool', 'credit_floor'],
  prefer_expiring: ['pool', 'prefer_expiring'], expiring_soon: ['pool', 'expiring_soon'],
  soft_rate: ['cooldown', 'soft_rate'], soft_rate_max: ['cooldown', 'soft_rate_max'],
  breaker_cooldown: ['pool', 'breaker_cooldown'], breaker_cooldown_max: ['pool', 'breaker_cooldown_max'],
  idle_weight_per_hour: ['pool', 'idle_weight_per_hour'], idle_weight_max: ['pool', 'idle_weight_max'],
  ttl: ['session_sticky', 'ttl'],
  timeout_seconds: ['upstream', 'timeout_seconds'], header_timeout_seconds: ['upstream', 'header_timeout_seconds'],
  idle_timeout_seconds: ['upstream', 'idle_timeout_seconds'],
  cn_client_version: ['upstream', 'profiles', 'cn', 'client_version'],
  cn_cli_version: ['upstream', 'profiles', 'cn', 'cli_version'],
  global_client_version: ['upstream', 'profiles', 'global', 'client_version'],
  global_cli_version: ['upstream', 'profiles', 'global', 'cli_version'],
  prompt_mode: ['prompt', 'mode'], prompt_preset: ['prompt', 'preset'], prompt_file: ['prompt', 'file'],
  prompt_text: ['prompt', 'text'],
  prompt_cn_mode: ['prompt', 'profiles', 'cn', 'mode'], prompt_cn_preset: ['prompt', 'profiles', 'cn', 'preset'],
  prompt_cn_text: ['prompt', 'profiles', 'cn', 'text'],
  prompt_global_mode: ['prompt', 'profiles', 'global', 'mode'], prompt_global_preset: ['prompt', 'profiles', 'global', 'preset'],
  prompt_global_text: ['prompt', 'profiles', 'global', 'text'],
  fingerprint_rewrite: ['fingerprint_rewrite'],
  // 多模态与图片策略（media.*）：三档工具图片处理 + 显式的转码/压缩开关。
  media_tool_images: ['media', 'tool_images'],
  media_image_transcode: ['media', 'image_transcode'],
  media_image_max_dimension: ['media', 'image_max_dimension'],
  // 值不是纯路径：面板用「一行一条」文本编辑，由 parseRules/formatRules 编解码。
  fingerprint_rules_text: ['fingerprint_rules'],
  session_sticky_enabled: ['session_sticky', 'enabled'],
  model_default_realm: ['model_default_realm'],
  proxy_url: ['proxy_url'],
  resin_url: ['resin_url'], resin_platform_name: ['resin_platform_name'],
  resin_mode: ['resin_mode'], resin_auth_version: ['resin_auth_version'],
  request_client_info: ['logging', 'request_client_info'],
};
/* 「覆盖型」文本字段：空串本身是有意义的取值（= 回落到内置默认），必须照发。
 *
 * 其余文本字段保持「空 = 不下发」的既有语义——那是防误清空的保护，不是 bug：
 * 表单里某个框没填，通常意味着"没改"，把它当成"请清空"会静默抹掉配置。
 *
 * 但覆盖型字段正好相反：清空 = 明确要求回到默认。漏发它们会让面板显示"已保存"
 * 而值其实没变（issue #102 附带发现 2：user_agent 清空后 config.json 里仍是旧值）。
 *
 * 分域版本四个字段也属于覆盖型：清空 = 用内置默认（后端对空串回落
 * DefaultIdentity，所以删掉值不会让 UA 变成空串）。
 *
 * 刻意不含 api_key：清空它 = 关闭整个鉴权，误触代价是网关变成无鉴权公开服务。
 * 该字段（以及提示文案"留空 = 不鉴权"与现状不符的问题）单独处理。
 */
/* 「手工处理」字段：在 CFG_MAP 里登记（守护测试要求表单字段都有条目），
 * 但**不参与通用回填/收集循环**——它们的值是结构化的，由专门的转换函数处理：
 *   fingerprint_rules_text → parseRules / formatRules（文本 ↔ Rule[]）
 * 通用循环若碰它们，会把数组 join 成字符串、或把文本当字符串下发（类型不符）。 */
const MANUAL_CFG = new Set(['fingerprint_rules_text']);

const CLEARABLE_CFG = new Set(['prompt_file', 'prompt_text', 'prompt_cn_text', 'prompt_global_text',
  'proxy_url', 'resin_url', 'resin_platform_name',
  'cn_client_version', 'cn_cli_version', 'global_client_version', 'global_cli_version']);

/* 「下拉但值是数字」字段：Go 侧是 int（media.image_max_dimension）。
 *
 * 通用收集循环对 select 只取字符串，直接下发 "1080" 会让 Go 的 json 解码失败
 * （不能把字符串解码进 int）。这里显式转 Number，与 <input type="number"> 同口径；
 * 把非法值留给后端校验（错误信息里带允许取值），不在前端再列一份白名单。
 */
const NUMERIC_CFG = new Set(['media_image_max_dimension']);

/* ---------- 自定义指纹规则：文本编解码 ----------
 *
 * 配置文件里是结构化数组（fingerprint_rules: [{match, replace, mode, action}]），
 * 面板用"一行一条"的文本编辑——比嵌套表单好用，也便于整段粘贴。
 *
 * 行格式（前缀可组合，顺序无关）：
 *   匹配串 => 替换文本     literal 替换（大小写敏感）
 *   /匹配串 => 替换文本    fold 替换（忽略 ASCII 大小写）
 *   !匹配串                整段删除（literal）
 *   /!匹配串               整段删除（fold）
 *   # 开头                 注释；空行忽略
 *
 * `=>` 取**首个**出现位置，因此匹配串里不能含 `=>`（替换文本里可以）。
 * 前缀字符 `/` `!` 为保留字，匹配串本身不能以它们开头。
 * 解析结果由后端校验（上限/纯空白/替换含匹配等），非法时保存会被拒并点名行号。
 */
function parseRules(text) {
  const out = [];
  for (const raw of String(text == null ? '' : text).split(/\r?\n/)) {
    let line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    let mode = 'literal', action = 'replace';
    while (line[0] === '/' || line[0] === '!') {
      if (line[0] === '/') mode = 'fold'; else action = 'remove';
      line = line.slice(1).trim();
    }
    let match = line, replace = '';
    const at = line.indexOf('=>');
    if (at >= 0) {
      match = line.slice(0, at).trim();
      replace = line.slice(at + 2).trim();
    }
    if (!match) continue;
    if (action === 'remove') out.push({ match: match, mode: mode, action: 'remove' });
    else out.push({ match: match, replace: replace, mode: mode, action: 'replace' });
  }
  return out;
}
function formatRules(rules) {
  return (Array.isArray(rules) ? rules : []).map(r => {
    const p = (r.mode === 'fold' ? '/' : '') + (r.action === 'remove' ? '!' : '');
    return r.action === 'remove' ? p + r.match : p + r.match + ' => ' + (r.replace == null ? '' : r.replace);
  }).join('\n');
}

function dig(obj, path) { return path.reduce((o, k) => (o == null ? undefined : o[k]), obj); }
function put(obj, path, val) {
  let o = obj;
  for (let i = 0; i < path.length - 1; i++) { if (typeof o[path[i]] !== 'object' || o[path[i]] === null) o[path[i]] = {}; o = o[path[i]]; }
  o[path[path.length - 1]] = val;
}

let cfgVersionInfo = null; // 最近一次 GET config 返回的 version_info（一键填入的数据源）

async function loadConfig() {
  try {
    const d = await api('config');
    cfgLoaded = d.config;
    $('cfgPath').textContent = d.path || '';
    const f = $('cfgForm');
    for (const [name, path] of Object.entries(CFG_MAP)) {
      if (MANUAL_CFG.has(name)) continue; // 见 MANUAL_CFG 注释
      const el = f.elements[name];
      if (!el) continue;
      const v = dig(cfgLoaded, path);
      if (el.type === 'checkbox') el.checked = !!v;
      else if (Array.isArray(v)) el.value = v.join(', ');
      else el.value = v == null ? '' : v;
    }
    // 自定义规则：结构化数组 → 文本行（不在 CFG_MAP 里，单独回填）。
    const rulesEl = f.elements['fingerprint_rules_text'];
    if (rulesEl) rulesEl.value = formatRules(cfgLoaded && cfgLoaded.fingerprint_rules);
    syncFpRulesVisibility();
    renderModeMap();
    markDurationFields(); // 回填后重置校验态（清掉残留红框；现值来自后端必然合法）
    applyVersionInfo(d.version_info, f);
    loadPromptPresets(); // 预设目录来自后端（不硬编码名单）；与回填互不依赖
    refreshPromptPresetHint();
    $('cfgNote').textContent = '';
    // 配置告警（未知/旧配置键）：只告知，不阻断保存。
    const warns = (cfgLoaded && Array.isArray(cfgLoaded._warnings)) ? cfgLoaded._warnings : [];
    if (warns.length && $('cfgNote')) {
      $('cfgNote').textContent = `配置告警：${warns.join('；')}`;
    }
    if (warns.length) console.warn('config warnings:', warns);
  } catch (e) { toast('读取配置失败：' + e.message, 'err'); }
}
// applyVersionInfo 用后端返回的版本信息初始化版本区：
//   - 占位符 = 内置默认 profile 版本（单一事实来源在后端，前端不再硬编码，
//     避免代码升级后面板还显示旧版本号）；
//   - 提示行 = 已拉取的最新版本 + 配置风险提醒；
//   - 四个输入框绑定 input，改动后即时重算提醒。
function applyVersionInfo(v, f) {
  cfgVersionInfo = v || null;
  if (f) {
    for (const n of ['cn_client_version', 'cn_cli_version', 'global_client_version', 'global_cli_version']) {
      const el = f.elements[n];
      if (!el) continue;
      const base = (
        n === 'cn_client_version' ? v && v.builtin_cn_client :
        n === 'cn_cli_version' ? v && v.builtin_cn_cli :
        n === 'global_client_version' ? v && v.builtin_global_client :
        v && v.builtin_global_cli
      );
      if (base) el.placeholder = base;
      el.oninput = refreshVersionNotes;
    }
  }
  refreshVersionNotes();
}

// versionNotes 返回某域的版本配置风险提醒（按严重度只有两类，都是“可能造出
// 不存在的客户端指纹”，不阻止保存）：
//   1. 客户端版本落后于已拉取的最新版（可一键填入）；
//   2. 只改了客户端版本、CLI 版本仍是内置基线值：UA 两段版本不成对。
//      官方 feed 不下发「某构建捆绑的 CLI 号」，网关无法代填，只能提醒人工核对。
function versionNotes(realm) {
  const f = $('cfgForm'), v = cfgVersionInfo;
  if (!f || !v || !f.elements) return [];
  const cn = realm === 'cn', label = cn ? 'CN' : 'Global';
  const clientEl = f.elements[realm + '_client_version'], cliEl = f.elements[realm + '_cli_version'];
  if (!clientEl || !cliEl) return [];
  const bClient = (cn ? v.builtin_cn_client : v.builtin_global_client) || '';
  const bCLI = (cn ? v.builtin_cn_cli : v.builtin_global_cli) || '';
  const latest = (cn ? v.latest_cn : v.latest_global) || '';
  const client = (clientEl.value || '').trim() || bClient;
  const cli = (cliEl.value || '').trim() || bCLI;
  const notes = [];
  if (latest && client && client !== latest) {
    notes.push(`${label} 客户端版本 ${client} 落后于已拉取的最新 ${latest}（可一键填入）`);
  }
  if (bClient && client !== bClient && cli === bCLI) {
    notes.push(`${label} 已改客户端版本但 CLI 版本仍是内置 ${bCLI}，请填该构建捆绑的 CLI 号（否则 UA 两段版本不成对）`);
  }
  return notes;
}

// refreshVersionNotes 重算提示行：已拉取版本信息 + 当前输入的配置风险。
function refreshVersionNotes() {
  if (!$('cfgVersionHint')) return;
  const v = cfgVersionInfo;
  if (!v) { $('cfgVersionHint').textContent = ''; return; }
  const src = v.checked ? '已探测' : '未探测成功，显示内置基线';
  const notes = versionNotes('cn').concat(versionNotes('global'));
  $('cfgVersionHint').textContent =
    `已拉取版本（${src}）：国内 ${v.latest_cn || '-'} | 海外 ${v.latest_global || '-'}` +
    (notes.length ? '；' + notes.join('；') : '');
}

// fillFetchedVersions 一键填入已拉取的客户端版本（CLI 版本不动：官方 feed 不下发
// 内置 CLI 版本，我们无法知道新构建捆绑的 CLI 号，填旧值反而会造成错误指纹）。
function fillFetchedVersions() {
  const f = $('cfgForm'), v = cfgVersionInfo;
  if (!f || !v || !f.elements) { toast('尚未读取到版本信息，请刷新配置后重试', 'err'); return; }
  const filled = [];
  if (v.latest_cn && f.elements.cn_client_version) {
    f.elements.cn_client_version.value = v.latest_cn;
    filled.push('CN ' + v.latest_cn);
  }
  if (v.latest_global && f.elements.global_client_version) {
    f.elements.global_client_version.value = v.latest_global;
    filled.push('Global ' + v.latest_global);
  }
  if (!filled.length) { toast('没有可填入的版本（探测未成功）', 'err'); return; }
  refreshVersionNotes();
  const notes = versionNotes('cn').concat(versionNotes('global'));
  const srcNote = v.checked ? '' : '（未探测成功，这是内置基线版本，建议先确认官方已发布版本）';
  toast('已填入 ' + filled.join('、') + srcNote + (notes.length ? '；' + notes.join('；') : ''),
    (notes.length || !v.checked) ? 'err' : 'ok');
}

function collectConfig() {
  const f = $('cfgForm'), out = {};
  for (const [name, path] of Object.entries(CFG_MAP)) {
    if (MANUAL_CFG.has(name)) continue; // 见 MANUAL_CFG 注释
    const el = f.elements[name];
    if (!el) continue;
    let v;
    if (el.type === 'checkbox') v = el.checked;
    else if (el.type === 'number') { v = el.value.trim() === '' ? undefined : Number(el.value); }
    else {
      const raw = el.value.trim();
      // 覆盖型字段空串照发（见 CLEARABLE_CFG）；其余空 = 不下发。
      if (raw === '') v = CLEARABLE_CFG.has(name) ? '' : undefined;
      else if (NUMERIC_CFG.has(name)) v = Number(raw);
      else if (name.endsWith('_hours')) v = raw.split(/[,，\s]+/).filter(Boolean).map(Number);
      else v = raw;
    }
    if (v !== undefined) put(out, path, v);
  }
  // 自定义规则：文本 → 结构化数组。始终下发（含空数组），
  // 这样"清空输入框"等于明确要求清空规则，而不是"没改"。
  const rulesEl = f.elements['fingerprint_rules_text'];
  if (rulesEl) out.fingerprint_rules = parseRules(rulesEl.value);
  // 提示词分域：整体覆盖语义（见 docs）——该域三个字段全空时不下发该域键。
  // 空串视为"未设置"：分域覆盖里空值就是继承的意思（顶层素材同理：空 = 回落
  // 文件/预设），所以不能让空串把"继承"变成"该域有自己的空配置"。
  for (const realm of ['cn', 'global']) {
    const g = out.prompt && out.prompt.profiles && out.prompt.profiles[realm];
    if (!g) continue;
    const set = ['mode', 'preset', 'text'].some(k => g[k] !== undefined && g[k] !== '');
    if (!set) {
      delete out.prompt.profiles[realm];
      if (Object.keys(out.prompt.profiles).length === 0) delete out.prompt.profiles;
    }
  }
  return out;
}

/* Go 时长字段即时校验：空 = 沿用现值（collectConfig 跳过发送）；非空必须是
   ParseDuration 语法（30m / 2h / 600s / 1h30m，可组合可带小数）。与后端
   config.go normalize() 的 time.ParseDuration 同口径，脏值在前端就地标红，
   不再等到保存被拒。 */
const DURATION_RE = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;
const DURATION_FIELDS = ['soft_rate', 'soft_rate_max', 'breaker_cooldown', 'breaker_cooldown_max',
  'degrade_cooldown', 'degrade_cooldown_max', 'cost_explore_interval', 'expiring_soon', 'ttl'];
const DURATION_TIP = '格式应为 Go 时长：30m / 2h / 600s / 1h30m';
function durationBad(name) {
  const el = $('cfgForm').elements[name];
  if (!el) return false;
  const v = el.value.trim();
  return v !== '' && !DURATION_RE.test(v);
}
function markDurationFields() {
  for (const name of DURATION_FIELDS) {
    const el = $('cfgForm').elements[name];
    if (!el) continue;
    const bad = durationBad(name);
    el.classList.toggle('invalid', bad);
    el.title = bad ? DURATION_TIP : '';
  }
}
$('cfgForm').addEventListener('input', ev => {
  if (DURATION_FIELDS.includes(ev.target.name)) markDurationFields();
  if (ev.target.name === 'prompt_preset') refreshPromptPresetHint();
});
$('btnEye').onclick = () => {
  const el = $('cfgKey');
  const show = el.type === 'password';
  el.type = show ? 'text' : 'password';
  $('btnEye').textContent = show ? '隐藏' : '显示';
};
$('btnCfgReload').onclick = loadConfig;
if ($('btnCfgFillVersions')) $('btnCfgFillVersions').onclick = fillFetchedVersions;
if ($('btnPromptPreview')) $('btnPromptPreview').onclick = previewPrompt;

// 设置页 Tab 切换
const cfgTabs = document.querySelectorAll('.side-tab');
const cfgPanels = document.querySelectorAll('.tab-panel-item');
cfgTabs.forEach(btn => {
  btn.onclick = () => {
    const secId = btn.dataset.sec;
    cfgTabs.forEach(b => b.classList.toggle('on', b === btn));
    cfgPanels.forEach(p => p.classList.toggle('active', p.id === secId));
    if (secId === 'sec-prompt') renderModeMap();
    if (typeof window.scrollTo === 'function') window.scrollTo({ top: 0, behavior: 'smooth' });
  };
});

/* ---------- 系统提示词：预设目录 / 分域预览 ---------- */

// promptDraft 收集当前表单里的 prompt 段（只取提示词字段，其余一律不发）。
// 预览与保存走同一份收集逻辑，因此“预览所见 = 保存后生效”。
function promptDraft() {
  const all = collectConfig();
  return { prompt: (all && all.prompt) || {} };
}

// loadPromptPresets 拉取后端内置预设目录并填充三个下拉（顶层 / CN / Global）。
// 预设名单不在前端硬编码：后端加了新预设，面板刷新即可选。
let promptPresets = [];
async function loadPromptPresets() {
  try {
    const d = await api('prompt/preview', { method: 'POST', body: JSON.stringify({ prompt: {} }) });
    promptPresets = (d && d.presets) || [];
    if (d && d.note && $('promptPreviewHint')) $('promptPreviewHint').textContent = d.note;
  } catch (e) {
    promptPresets = [];
  }
  fillPresetSelect($('promptPreset'), false);
  fillPresetSelect($('promptCnPreset'), true);
  fillPresetSelect($('promptGlobalPreset'), true);
  // 默认预设的说明直接展示在下拉旁，免去用户切换才能看到差异。
  refreshPromptPresetHint();
}

// refreshPromptPresetHint 把当前选中预设的说明与字数写在提示行里（切换时更新）。
function refreshPromptPresetHint() {
  const sel = $('promptPreset');
  if (!sel || !$('promptPresetHint')) return;
  const cur = promptPresets.find(p => p.name === (sel.value || 'default'));
  if (!cur) return;
  $('promptPresetHint').textContent = cur.description + '（CN ' + cur.chars_cn + ' 字' +
    (cur.realms ? ' / Global ' + cur.chars_global + ' 字' : '') + '）';
}
function fillPresetSelect(sel, withInherit) {
  if (!sel) return;
  const prev = sel.value;
  sel.innerHTML = '';
  if (withInherit) {
    const o = document.createElement('option');
    o.value = ''; o.textContent = '继承顶层';
    sel.appendChild(o);
  }
  if (!withInherit) {
    const o = document.createElement('option');
    o.value = ''; o.textContent = 'default — 内置默认';
    sel.appendChild(o);
  }
  for (const p of promptPresets) {
    const o = document.createElement('option');
    o.value = p.name;
    const realms = p.realms ? '（分域）' : '';
    o.textContent = p.name + ' — ' + p.label + ' ' + p.chars_cn + '字' + realms;
    o.title = p.description;
    sel.appendChild(o);
  }
  sel.value = prev;
  if (sel.value !== prev) sel.value = withInherit ? '' : '';
}

// previewPrompt 弹预览：展示 cn/global 两域解析后的正文、来源与字符数。
async function previewPrompt() {
  const btn = $('btnPromptPreview'), out = $('promptPreviewOut');
  if (!out) return;
  btn.disabled = true;
  try {
    const d = await api('prompt/preview', { method: 'POST', body: JSON.stringify(promptDraft()) });
    out.hidden = false;
    out.innerHTML = '';
    const head = document.createElement('div');
    head.className = 'prev-head';
    head.textContent = d.note || '';
    out.appendChild(head);
    for (const [key, label] of [['default', '默认（未知域兜底）'], ['cn', 'CN 账号'], ['global', 'Global 账号']]) {
      const r = d[key];
      if (!r) continue;
      const card = document.createElement('div');
      card.className = 'prev-card';
      const t = document.createElement('div');
      t.className = 't';
      const bits = [label + '：mode=' + r.mode, '来源=' + r.source, '预设=' + (r.preset || 'default'),
        r.inherited ? '继承顶层' : '本域覆盖'];
      if (!r.error) bits.push(r.chars + ' 字 / ' + r.bytes + ' 字节' + (r.truncated ? '（预览已截断）' : ''));
      t.textContent = bits.join(' · ');
      card.appendChild(t);
      const pre = document.createElement('div');
      pre.className = 'prev-out';
      pre.textContent = r.error ? '解析失败：' + r.error : (r.text || '（该模式不注入正文）');
      card.appendChild(pre);
      out.appendChild(card);
    }
    if (d.warnings && d.warnings.length) {
      const warn = document.createElement('div');
      warn.className = 'prev-head';
      warn.textContent = '告警：' + d.warnings.join('；');
      out.appendChild(warn);
    }
  } catch (e) {
    toast('预览失败：' + e.message, 'err');
  } finally {
    btn.disabled = false;
  }
}
$('cfgForm').onsubmit = async ev => {
  ev.preventDefault();
  // 时长字段脏值拦截：标红 + toast 点名，不发保存请求（后端同样会拒，这里前置）。
  markDurationFields();
  const firstBad = DURATION_FIELDS.find(durationBad);
  if (firstBad) {
    const el = $('cfgForm').elements[firstBad];
    el.focus();
    toast('「' + (el.closest('.fld')?.querySelector('.lb')?.textContent || firstBad) + '」' + DURATION_TIP, 'err');
    return;
  }
  const btn = $('btnCfgSave');
  btn.disabled = true; btn.textContent = '保存中…';
  try {
    const r = await api('config', { method: 'POST', body: JSON.stringify(collectConfig()) });
    const n = (r.restart_required || []).length;
    toast(n ? '配置已保存，其中 ' + n + ' 项需重启进程生效' : '配置已保存并立即生效', 'ok');
    // 密钥可能已改：本次会话沿用新值，避免下一次轮询被 401。
    const k = $('cfgKey').value.trim();
    if (k) localStorage.setItem(LS_KEY, k);
    loadConfig();
    loadOverview(true);
  } catch (e) { toast('保存失败：' + e.message, 'err'); }
  finally { btn.disabled = false; btn.textContent = '保存配置'; }
};

// syncFpRulesVisibility 规则编辑区随开关明暗（不禁用：允许"先写规则再开开关"）。
function syncFpRulesVisibility() {
  const on = $('fpRewrite'), wrap = $('fpRulesWrap');
  if (!wrap) return;
  const enabled = !!(on && on.checked);
  wrap.classList.toggle('off', !enabled);
  // 规则计数：让"写了一堆规则但没开开关"一眼可见。
  const n = parseRules($('fpRules') ? $('fpRules').value : '').length;
  if ($('fpRuleCount')) {
    const parts = [n + ' 条自定义规则'];
    if (n > 0 && !enabled) parts.push('（开关未开，当前不生效）');
    $('fpRuleCount').textContent = parts.join('');
  }
}
if ($('fpRewrite')) $('fpRewrite').addEventListener('change', syncFpRulesVisibility);
if ($('fpRules')) $('fpRules').addEventListener('input', syncFpRulesVisibility);

/* ---------- 组合位置实时示意图 ----------
 * 四种 mode 的差别就是"谁在前、谁还在"，画出来比读选项文案快。
 * 每次下拉变化重绘，不需要请求后端。 */
const MODE_MAP = {
  none:    { segs: [['cl', '客户端 system'], ['usr', '对话']], cap: '逐字透传：一个字节都不动' },
  replace: { segs: [['gw', '网关提示词'], ['cl-dropped', '客户端 system'], ['usr', '对话']], cap: '客户端 system 被丢弃（指纹面最小）' },
  after:   { segs: [['gw', '网关提示词'], ['cl', '客户端 system'], ['usr', '对话']], cap: '网关在前，客户端内容紧随其后' },
  append:  { segs: [['cl', '客户端 system'], ['gw', '网关提示词'], ['usr', '对话']], cap: '客户端在前，网关提示词插在其后' },
};
function renderModeMap() {
  const el = $('modeMap');
  if (!el) return;
  const mode = ($('promptMode') || {}).value || 'none';
  const spec = MODE_MAP[mode] || MODE_MAP.none;
  el.innerHTML = '';
  spec.segs.forEach(([cls, label], i) => {
    if (i) el.appendChild(segSpan('arrow', '→'));
    const parts = cls.split('-');
    const sp = segSpan(parts[0], label);
    if (parts.includes('dropped')) sp.classList.add('dropped');
    el.appendChild(sp);
  });
  const cap = document.createElement('span');
  cap.className = 'cap';
  cap.textContent = spec.cap;
  el.appendChild(cap);
}
function segSpan(cls, text) {
  const sp = document.createElement('span');
  sp.className = 'seg ' + cls;
  sp.textContent = text;
  return sp;
}
if ($('promptMode')) $('promptMode').addEventListener('change', renderModeMap);
