/* 账号池视图：KPI 卡、积分到期提醒、账号表（行内动作）、模型锁池、批量动作。 */
import { useMemo, useState } from 'react';
import { useData } from '../data';
import { api } from '../api';
import { accountAction } from '../actions';
import { toast } from '../toast';
import type { OverviewAccount, PackageAccount } from '../types';
import { Kpi, Tag, RealmTag, SegBar, Empty, Pager } from '../components/ui';
import { btnXs, btnXsGhost, btnXsPrimary } from '../components/buttons';
import { TasksDialog } from './TasksDialog';
import { ExpiryCard } from './ExpiryCard';
import { ago, dur, fmtTok, parseAPITime, fmtLocalDateTime } from '../fmt';

/* 限流/模型不可用行内提示（来自账号的 rate_limited_models）。 */
function RateLimits({ rows }: { rows: OverviewAccount['rate_limited_models'] }) {
  const list = (rows || []).filter((r) => r && r.model);
  if (!list.length) return null;
  const now = Date.now();
  return (
    <div className="mt-1.5 flex flex-col gap-1">
      {list.map((r, i) => {
        const deadline = parseAPITime(r.reset_at) || parseAPITime(r.until);
        const remaining = deadline > now ? Math.round((deadline - now) / 1000) : 0;
        const detail =
          r.kind === 'model_unavailable'
            ? remaining
              ? '预计 ' + dur(remaining) + ' 后重试'
              : '等待重新探测'
            : deadline
              ? '预计 ' + fmtLocalDateTime(deadline) + ' 解封' + (remaining ? '（剩余 ' + dur(remaining) + '）' : '')
              : '预计解封时间未知';
        const title = r.model + '\n' + detail;
        return (
          <div
            key={i}
            title={title}
            className={
              'flex items-center gap-1.5 rounded-md px-1.5 py-0.5 text-[11.5px] ' +
              (r.kind === 'model_unavailable' ? 'bg-[var(--bad-soft)] text-[var(--bad)]' : 'bg-[var(--warn-soft)] text-[var(--warn)]')
            }
          >
            <b className="font-[family-name:var(--mono)]">{r.model}</b>
            <span>{detail}</span>
          </div>
        );
      })}
    </div>
  );
}

function AccountRow({ s, onAction, onTasks, proxyConfigured }: { s: OverviewAccount; onAction: (a: string, s: OverviewAccount) => void; onTasks: (uid: string) => void; proxyConfigured: boolean }) {
  const bl = (parseAPITime(s.breaker_until) - Date.now()) / 1000;
  const dg = (parseAPITime(s.degrade_until) - Date.now()) / 1000;
  const cool = Math.max(s.cool_remaining_sec || 0, bl > 0 ? bl : 0, dg > 0 ? dg : 0);

  let rowCls = '';
  let tag: React.ReactNode;
  if (s.disabled) {
    rowCls = 'opacity-60';
    tag = <Tag tone="bad">已禁用</Tag>;
  } else if (s.paused) {
    rowCls = 'opacity-75';
    tag = <Tag tone="warn">已暂停选号</Tag>;
  } else if (cool > 0) {
    const kind =
      bl > Math.max(s.cool_remaining_sec || 0, dg > 0 ? dg : 0)
        ? '熔断'
        : dg > (s.cool_remaining_sec || 0)
          ? '连败降权'
          : s.cool_kind === 'hard_credit'
            ? '积分冷却'
            : '限流冷却';
    tag = (
      <Tag tone="warn">
        {kind} · {dur(cool)}
      </Tag>
    );
  } else {
    tag = <Tag tone="ok">可用</Tag>;
  }

  const unlimited = s.credits_total === -1;
  const pct = unlimited
    ? 100
    : s.credits_total && s.credits_total > 0
      ? Math.min(100, Math.round(((s.credits || 0) / s.credits_total) * 100))
      : 0;

  const tu = s.token_usage || {};
  const frozen = s.disabled || cool > 0;

  const credTip = unlimited
    ? '企业版不限量（上游 limitNum=-1）'
    : s.credits_total && s.credits_total > 0
      ? `${s.enterprise ? '企业版剩余' : '剩余'} ${s.credits ?? '—'} / ${s.credits_total}（${pct}%）`
      : '积分';

  return (
    <tr className={'transition-colors hover:bg-[var(--surface-2)]/50 ' + rowCls} title={'uid: ' + s.uid}>
      <td className="px-3.5 py-2.5">
        <div className="flex items-center gap-1.5">
          <span className={'h-2 w-2 rounded-full shrink-0 ' + (frozen || s.paused ? 'bg-[var(--warn)]' : s.disabled ? 'bg-[var(--bad)]' : 'bg-[var(--ok)]')} />
          <div className="min-w-0">
            <div className="font-medium text-[var(--ink)]">
              {s.nickname || <span className="text-[var(--ink-3)]">未命名</span>}
              {s.realm === 'global' && <RealmTag>国际版</RealmTag>}
              {s.enterprise && <RealmTag>企业版</RealmTag>}
            </div>
            <div className="font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{s.uid.length > 16 ? s.uid.slice(0, 16) + '…' : s.uid}</div>
          </div>
        </div>
      </td>
      <td className="px-3.5 py-2.5">
        {tag}
        {s.reason && <div className="mt-0.5 text-[11.5px] text-[var(--ink-3)]">{s.reason}</div>}
        <RateLimits rows={s.rate_limited_models} />
      </td>
      <td className="tabular px-3.5 py-2.5" title={credTip}>
        {unlimited ? '不限' : s.credits == null ? '—' : <>{s.credits}{s.credits_total && s.credits_total > 0 ? <span className="text-[var(--ink-3)]">/{s.credits_total}</span> : null}</>}
        {!unlimited && pct > 0 && (
          <div className="mt-1">
            <SegBar segs={[{ value: pct, title: credTip }]} total={100} />
          </div>
        )}
      </td>
      <td className="tabular px-3.5 py-2.5">
        {s.success_count || 0} <span className="text-[var(--ink-3)]">/</span> <span className="text-[var(--bad)]">{s.err_total || 0}</span>
      </td>
      <td className="tabular px-3.5 py-2.5 text-[var(--ink)]">{s.in_flight || 0}</td>
      <td className="tabular px-3.5 py-2.5 text-[12px] text-[var(--ink-2)]">
        <span title={`最近一次：${tu.request_count || 0} 次 / ${fmtTok(tu.total_tokens)} / ${fmtLocalDateTime(Date.now())}`}>
          {tu.request_count || 0} 次 · {tu.total_tokens == null ? '—' : fmtTok(tu.total_tokens)}
        </span>
      </td>
      <td className="tabular px-3.5 py-2.5 text-[var(--ink-3)]">{ago(s.last_success)}</td>
      <td className="px-3.5 py-2.5 text-right">
        <div className="flex flex-wrap justify-end gap-1.5">
          {!s.enterprise && (
            <button className={btnXsGhost} onClick={() => onAction('checkin', s)} title={s.checkin_done ? '今日已签到；点击可重新签到并刷新余额' : undefined}>
              {s.checkin_done ? '已签' : '签到'}
            </button>
          )}
          <button className={btnXsGhost} onClick={() => onAction('balance', s)} title={s.enterprise ? '刷新企业版已分配额度' : undefined}>
            {s.enterprise ? '额度' : '余额'}
          </button>
          {!s.enterprise && (
            <button className={btnXsGhost} onClick={() => onTasks(s.uid)}>
              任务
            </button>
          )}
          {proxyConfigured && (
            <button className={btnXsGhost} onClick={() => onAction('proxy', s)} title="切换该账号是否走全局代理；关闭后该账号直连">
              代理{s.proxy_enabled ? <span className="text-[var(--ok)]"> 开</span> : <span className="text-[var(--ink-3)]"> 关</span>}
            </button>
          )}
          {frozen ? (
            <button className={btnXsPrimary} onClick={() => onAction('revive', s)}>
              解冻
            </button>
          ) : s.paused ? (
            <button className={btnXsPrimary} onClick={() => onAction('resume', s)}>
              恢复选号
            </button>
          ) : (
            <button className={btnXsGhost} onClick={() => onAction('pause', s)} title="退出选号，但照常签到 / 保活 / 刷新余额">
              暂停选号
            </button>
          )}
          {!s.disabled && (
            <button className={btnXsGhost} onClick={() => onAction('disable', s)}>
              禁用
            </button>
          )}
          <button className={btnXsGhost + ' !text-[var(--bad)]'} onClick={() => onAction('remove', s)}>
            移除
          </button>
        </div>
      </td>
    </tr>
  );
}

/* 模型锁池行。 */
function ModelLocksTable({ rows }: { rows: { model: string; realm?: string; state?: string; servable?: number; total?: number; locked?: number; unlock_at?: string; fully_unlock_at?: string; reason?: string }[] | null | undefined }) {
  const list = rows || [];
  if (!list.length) {
    return (
      <tbody>
        <tr>
          <td colSpan={8}>
            <Empty>当前没有模型级限流 —— 所有模型均可选</Empty>
          </td>
        </tr>
      </tbody>
    );
  }
  const STATE: Record<string, ['ok' | 'warn' | 'bad' | 'mute', string]> = {
    locked: ['bad', '整池不可用'],
    starved: ['warn', '没号可用'],
    partial: ['warn', '部分限流'],
  };
  const left = (iso?: string) => {
    const ms = parseAPITime(iso);
    return ms ? dur(Math.max(0, Math.round((ms - Date.now()) / 1000))) : '—';
  };
  return (
    <tbody className="divide-y divide-[var(--line-soft)]/60">
      {list.map((r, i) => {
        const st = STATE[r.state || ''] || ['mute', r.state || '—'];
        return (
          <tr key={i} className="transition-colors hover:bg-[var(--surface-2)]/50">
            <td className="px-3.5 py-2.5 font-[family-name:var(--mono)] text-[12.5px] text-[var(--ink)] font-medium">{r.model}</td>
            <td className="px-3.5 py-2.5">
              <RealmTag>{r.realm === 'global' ? '国际版' : '国内版'}</RealmTag>
            </td>
            <td className="px-3.5 py-2.5">
              <Tag tone={st[0]}>{st[1]}</Tag>
            </td>
            <td className="tabular px-3.5 py-2.5">
              {r.servable || 0} / {r.total || 0}
            </td>
            <td className="tabular px-3.5 py-2.5">{r.locked || 0}</td>
            <td className="tabular px-3.5 py-2.5">{left(r.unlock_at || r.fully_unlock_at)}</td>
            <td className="tabular px-3.5 py-2.5">{left(r.fully_unlock_at)}</td>
            <td className="max-w-[240px] px-3.5 py-2.5 text-[12px] text-[var(--ink-3)]">{r.reason || '—'}</td>
          </tr>
        );
      })}
    </tbody>
  );
}

export function AccountsView() {
  const { overview, refresh } = useData();
  const [tasksUid, setTasksUid] = useState<string | null>(null);
  const [packages, setPackages] = useState<PackageAccount[] | null>(null);
  const [acctPage, setAcctPage] = useState(1);
  const [acctPageSize, setAcctPageSize] = useState(20);

  const d = overview;
  const accounts = d?.accounts || [];
  const acctTotalPages = Math.max(1, Math.ceil(accounts.length / acctPageSize));
  const curAcctPage = Math.min(acctPage, acctTotalPages);
  const pagedAccounts = accounts.slice((curAcctPage - 1) * acctPageSize, curAcctPage * acctPageSize);
  const healthy = d?.healthy || 0;
  const cooling = d?.cooling || 0;
  const disabled = d?.disabled || 0;
  const paused = d?.paused || 0;
  const total = d?.total || 0;

  const creditSum = useMemo(() => {
    const rem = accounts.reduce((a, s) => a + (s.credits || 0), 0);
    const tot = accounts.reduce((a, s) => a + (s.credits_total === -1 ? 0 : s.credits_total || 0), 0);
    return { rem, tot };
  }, [accounts]);

  const onAction = (a: string, s: OverviewAccount) => {
    void accountAction(a, s.uid, s, () => void refresh(true));
  };

  const batch = async (path: string, msg: string) => {
    try {
      await api(path, { method: 'POST' });
      toast(msg, 'ok');
    } catch (e) {
      toast((e as Error).message, 'err');
    }
  };

  return (
    <div className="flex flex-col gap-4">
      {/* KPI 指标卡：精简化展示 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Kpi v={total} k="账号总数" tone="soft" sub={healthy > 0 ? `${healthy} 个正常可用` : '暂无可用账号'} />
        <Kpi
          v={healthy}
          k="可用账号"
          tone={healthy === 0 && total > 0 ? 'bad' : cooling > 0 ? 'warn' : 'ok'}
          sub={[cooling ? `冷却 ${cooling}` : '', disabled ? `禁用 ${disabled}` : '', paused ? `暂停 ${paused}` : ''].filter(Boolean).join(' · ') || '运行状态正常'}
        />
        <Kpi
          v={creditSum.rem}
          k="积分剩余"
          tone="accent"
          sub={creditSum.tot > 0 ? `总额 ${creditSum.tot}` : '全池可用额度'}
        />
        <Kpi v={d?.sticky_sessions ?? 0} k="粘性会话" tone="mute" sub="活跃连接数" />
      </div>

      {/* 积分到期提醒（与积分构成视图共享 packages 缓存） */}
      <ExpiryCard packages={packages} onLoaded={setPackages} />

      {/* 账号表 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-xs overflow-hidden">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3 border-b border-[var(--line-soft)] bg-[var(--surface)]">
          <h3 className="text-[14px] font-semibold text-[var(--ink)]">账号池</h3>
          <span className="flex-1" />
          {d?.in_flight_full ? <span className="text-[12px] text-[var(--warn)]">{d.in_flight_full} 个账号在途占满</span> : null}
          <button className={btnXs} onClick={() => void batch('checkin_all', '全部签到已开始，结果见日志')}>
            全部签到
          </button>
          <button className={btnXs} onClick={() => void batch('travel_all', '旅行巡检已开始（含领养链路），结果见日志')}>
            旅行巡检
          </button>
          <button className={btnXs} onClick={() => void batch('activity_all', '活跃上报已开始，结果见日志')}>
            活跃上报
          </button>
          <button className={btnXs} onClick={() => void batch('keepalive_all', '全部保活已开始，结果见日志')}>
            全部保活
          </button>
        </header>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-b border-[var(--line-soft)] bg-[var(--surface-2)]/50 text-left text-[11.5px] font-semibold text-[var(--ink-2)]">
                <th className="px-3.5 py-2.5 font-medium">账号</th>
                <th className="px-3.5 py-2.5 font-medium">状态</th>
                <th className="px-3.5 py-2.5 font-medium">积分</th>
                <th className="px-3.5 py-2.5 font-medium">成功 / 失败</th>
                <th className="px-3.5 py-2.5 font-medium">在途</th>
                <th className="px-3.5 py-2.5 font-medium">用量</th>
                <th className="px-3.5 py-2.5 font-medium">最近成功</th>
                <th className="px-3.5 py-2.5 text-right font-medium">操作</th>
              </tr>
            </thead>
            {pagedAccounts.length ? (
              <tbody className="divide-y divide-[var(--line-soft)]/60">
                {pagedAccounts.map((s) => (
                  <AccountRow key={s.uid} s={s} onAction={onAction} onTasks={setTasksUid} proxyConfigured={!!d?.proxy_configured} />
                ))}
              </tbody>
            ) : (
              <tbody>
                <tr>
                  <td colSpan={8}>
                    <Empty big="账号池是空的">点击右上角「添加账号」，用浏览器登录一个 WorkBuddy 账号</Empty>
                  </td>
                </tr>
              </tbody>
            )}
          </table>
        </div>
        <Pager
          page={curAcctPage}
          totalPages={acctTotalPages}
          total={accounts.length}
          pageSize={acctPageSize}
          pageSizeOptions={[10, 20, 50]}
          onPage={setAcctPage}
          onPageSizeChange={(sz) => {
            setAcctPageSize(sz);
            setAcctPage(1);
          }}
        />
      </div>

      {/* 模型锁池 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-xs overflow-hidden">
        <header className="flex flex-wrap items-center gap-2.5 px-4 py-3 border-b border-[var(--line-soft)] bg-[var(--surface)]">
          <h3 className="text-[14px] font-semibold text-[var(--ink)]">
            模型锁池 <span className="ml-1 text-[12px] font-normal text-[var(--ink-3)]">哪些模型不能用、还要锁多久</span>
          </h3>
        </header>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-b border-[var(--line-soft)] bg-[var(--surface-2)]/50 text-left text-[11.5px] font-semibold text-[var(--ink-2)]">
                <th className="px-3.5 py-2.5 font-medium">模型</th>
                <th className="px-3.5 py-2.5 font-medium">域</th>
                <th className="px-3.5 py-2.5 font-medium">状态</th>
                <th className="px-3.5 py-2.5 font-medium">可选 / 总数</th>
                <th className="px-3.5 py-2.5 font-medium">锁定账号</th>
                <th className="px-3.5 py-2.5 font-medium">最早解锁</th>
                <th className="px-3.5 py-2.5 font-medium">全池解锁</th>
                <th className="px-3.5 py-2.5 font-medium">原因</th>
              </tr>
            </thead>
            <ModelLocksTable rows={d?.model_locks} />
          </table>
        </div>
      </div>

      {tasksUid && <TasksDialog uid={tasksUid} onClose={() => setTasksUid(null)} />}
    </div>
  );
}
